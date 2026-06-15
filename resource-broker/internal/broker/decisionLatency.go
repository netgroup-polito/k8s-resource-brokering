package broker

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// rankLatency sorts scored clusters by latency score (ascending) and returns up to maxResults.
// If possible, the first element is always a "never connected" cluster (isActual == false),
// so the requester sees a new cluster suggestion first.
func rankLatency(scoredClusters []ScoredCluster, maxResults int) []RankedCluster {
	// Sort ascending by score (lower latency = better)
	sort.Slice(scoredClusters, func(i, j int) bool {
		return scoredClusters[i].score < scoredClusters[j].score
	})

	// Promote the best "never connected" cluster to position 0, if one exists.
	// Shift the others down so the original first stays at position 1.
	for i := range scoredClusters {
		if !scoredClusters[i].isActual {
			// Save the element to promote
			promoted := scoredClusters[i]
			// Shift elements 0..i-1 one position to the right
			copy(scoredClusters[1:i+1], scoredClusters[0:i])
			// Place the promoted element at position 0
			scoredClusters[0] = promoted
			break
		}
	}

	var bestClusters []RankedCluster
	for i, sc := range scoredClusters {
		if i >= maxResults {
			break
		}
		baseInfo := "Never connected to this cluster"
		if sc.isActual {
			baseInfo = "Already connected with this cluster"
		}
		
		costStr := "unknown"
		if sc.cluster.Spec.Cost != nil {
			costStr = fmt.Sprintf("%.2f", sc.cluster.Spec.Cost.EnergyCost)
		}
		
		info := fmt.Sprintf("cost: %s. info: %s", costStr, baseInfo)
		bestClusters = append(bestClusters, RankedCluster{Cluster: sc.cluster, Information: info})
	}
	return bestClusters
}

// estimatedRTTms estimates the Round Trip Time (in milliseconds) between two cluster locations.
// It uses the Haversine formula for geographic distance, applies a 1.3x fiber path multiplier,
// computes RTT as (distance_km * 1.3 * 2) / 200 km/ms, and adds penalties for different AS/ISP.
func estimatedRTTms(loc1, loc2 *brokerv1alpha1.LocationInfo) float64 {
	const earthRadiusKm = 6371.0
	const fiberPathMultiplier = 1.3 // real fiber paths are not straight lines
	const fiberSpeedKmPerMs = 200.0 // speed of light in fiber: ~200 km/ms
	const asPenaltyMs = 20.0        // penalty for crossing different Autonomous Systems
	const ispPenaltyMs = 20.0       // penalty for crossing different ISPs

	toRad := func(deg float64) float64 {
		return deg * math.Pi / 180.0
	}

	// 1. Haversine distance
	phi1 := toRad(loc1.Lat)
	phi2 := toRad(loc2.Lat)
	dPhi := toRad(loc2.Lat - loc1.Lat)
	dLambda := toRad(loc2.Lon - loc1.Lon)

	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*
			math.Sin(dLambda/2)*math.Sin(dLambda/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	distanceKm := earthRadiusKm * c

	// 2. Apply fiber path multiplier (real cables don't follow great circles)
	fiberDistanceKm := distanceKm * fiberPathMultiplier

	// 3. RTT = (fiberDistance * 2) / fiberSpeed  →  in milliseconds
	rttMs := (fiberDistanceKm * 2) / fiberSpeedKmPerMs

	// 4. Add penalties for different AS and ISP
	if loc1.AS != "" && loc2.AS != "" && loc1.AS != loc2.AS {
		rttMs += asPenaltyMs
	}
	if loc1.ISP != "" && loc2.ISP != "" && loc1.ISP != loc2.ISP {
		rttMs += ispPenaltyMs
	}

	return rttMs
}

// calculateScoreLatency computes a latency-based score for the cluster.
// It first checks if a NetworkBond already exists between the two clusters.
// Returns the negated latency as score and a bool indicating if the value is from actual measurement.
func (d *DecisionEngine) calculateScoreLatency(
	ctx context.Context,
	requesterAdv *brokerv1alpha1.ClusterAdvertisement,
	cluster *brokerv1alpha1.ClusterAdvertisement,
) (float64, bool) {
	logger := log.FromContext(ctx).WithName("decision-engine")

	bondName := fmt.Sprintf("%s-%s", requesterAdv.Spec.ClusterID, cluster.Spec.ClusterID)

	// 1. Check if a NetworkBond already exists
	existingBond := &brokerv1alpha1.NetworkBond{}
	err := d.Client.Get(ctx, types.NamespacedName{
		Name:      bondName,
		Namespace: cluster.Namespace,
	}, existingBond)

	if err == nil {
		// Check if the bond is older than 30 days
		if !existingBond.Spec.Timestamp.IsZero() && time.Since(existingBond.Spec.Timestamp.Time) > 30*24*time.Hour {
			logger.Info("NetworkBond is older than 30 days, recalculating estimated latency and discarding actual latency",
				"requester", requesterAdv.Spec.ClusterID,
				"provider", cluster.Spec.ClusterID)
			
			existingBond.Spec.ActualLatency = 0
			existingBond.Spec.EstimatedLatency = estimatedRTTms(requesterAdv.Spec.Location, cluster.Spec.Location)
			existingBond.Spec.Timestamp = metav1.Now()
			
			if updateErr := d.Client.Update(ctx, existingBond); updateErr != nil {
				logger.Error(updateErr, "Failed to update expired NetworkBond", "bond", bondName)
			}
			
			return existingBond.Spec.EstimatedLatency, false
		}

		// 1a. NetworkBond exists
		if existingBond.Spec.ActualLatency > 0 {
			// 2a. ActualLatency is available — use it
			logger.Info("Using actual measured latency from NetworkBond",
				"requester", requesterAdv.Spec.ClusterID,
				"provider", cluster.Spec.ClusterID,
				"actual_latency_ms", existingBond.Spec.ActualLatency)
			return existingBond.Spec.ActualLatency, true
		}

		// ActualLatency not available — use EstimatedLatency from the bond
		logger.Info("Using estimated latency from existing NetworkBond",
			"requester", requesterAdv.Spec.ClusterID,
			"provider", cluster.Spec.ClusterID,
			"estimated_latency_ms", existingBond.Spec.EstimatedLatency)
		return existingBond.Spec.EstimatedLatency, false
	}

	// 1b. NetworkBond does not exist — compute region match and check for existing bonds in the same region pair
	match := regionMatch(requesterAdv.Spec.Location, cluster.Spec.Location)

	// Try to find an existing bond with the same region match that has actual latency
	var latency float64
	if match != "" {
		bondList := &brokerv1alpha1.NetworkBondList{}
		if listErr := d.Client.List(ctx, bondList); listErr == nil {
			for i := range bondList.Items {
				if bondList.Items[i].Spec.Match == match && bondList.Items[i].Spec.ActualLatency > 0 {
					latency = bondList.Items[i].Spec.ActualLatency
					logger.Info("Using actual latency from matching region bond",
						"requester", requesterAdv.Spec.ClusterID,
						"provider", cluster.Spec.ClusterID,
						"matchingBond", bondList.Items[i].Name,
						"match", match,
						"latency_ms", latency)
					break
				}
			}
		}
	}

	// Fallback to Haversine if no matching bond with actual latency was found
	if latency == 0 {
		latency = estimatedRTTms(requesterAdv.Spec.Location, cluster.Spec.Location)
		logger.Info("Calculated geographical distance for latency policy",
			"requester", requesterAdv.Spec.ClusterID,
			"provider", cluster.Spec.ClusterID,
			"estimated_latency_ms", latency)
	}

	// 2b. Create NetworkBond CRD in background for latency tracking
	go func(reqID, provID string, lat float64, m string) {
		bond := &brokerv1alpha1.NetworkBond{}
		bond.Name = bondName
		bond.Namespace = cluster.Namespace
		bond.Spec.RequesterClusterID = reqID
		bond.Spec.ProviderClusterID = provID
		bond.Spec.EstimatedLatency = lat
		bond.Spec.Match = m

		if createErr := d.Client.Create(context.Background(), bond); createErr == nil {
			logger.Info("Created NetworkBond CRD for latency tracking", "bond", bond.Name, "match", m)
		}
	}(requesterAdv.Spec.ClusterID, cluster.Spec.ClusterID, latency, match)

	return latency, false
}

// regionMatch returns the sorted region pair string (e.g. "CA-LOM") from two LocationInfo.
// Returns empty string if either location is nil or has no Region.
func regionMatch(loc1, loc2 *brokerv1alpha1.LocationInfo) string {
	if loc1 == nil || loc2 == nil || loc1.Region == "" || loc2.Region == "" {
		return ""
	}
	a, b := loc1.Region, loc2.Region
	if a > b {
		a, b = b, a
	}
	return a + "-" + b
}
