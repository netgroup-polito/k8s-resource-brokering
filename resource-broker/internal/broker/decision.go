package broker

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DecisionEngine selects the best cluster for resource allocation
type DecisionEngine struct {
	Client client.Client
}

// ScoredCluster associates a cluster with its computed score and whether the latency is actual
type ScoredCluster struct {
	cluster  *brokerv1alpha1.ClusterAdvertisement
	score    float64
	isActual bool // true if score is based on actual measured latency
}

// RankedCluster pairs a cluster with an information string about the ranking decision
type RankedCluster struct {
	Cluster     *brokerv1alpha1.ClusterAdvertisement
	Information string
}

// RankClusters finds the most suitable clusters based on requested resources and returns up to maxResults sorted from best to worst
func (d *DecisionEngine) RankClusters(
	ctx context.Context,
	requesterID string,
	requestedCPU, requestedMemory resource.Quantity,
	requestedGPU *resource.Quantity,
	priority int32,
	maxResults int,
	policy string,
) ([]RankedCluster, error) {

	// List all cluster advertisements
	advList := &brokerv1alpha1.ClusterAdvertisementList{}
	if err := d.Client.List(ctx, advList); err != nil {
		return nil, fmt.Errorf("failed to list cluster advertisements: %w", err)
	}

	if len(advList.Items) == 0 {
		return nil, fmt.Errorf("no clusters available")
	}

	var scoredClusters []ScoredCluster

	var requesterAdv *brokerv1alpha1.ClusterAdvertisement
	if policy == "latency" {
		for i := range advList.Items {
			if advList.Items[i].Spec.ClusterID == requesterID {
				requesterAdv = &advList.Items[i]
				break
			}
		}
		if requesterAdv == nil || requesterAdv.Spec.Location == nil {
			return nil, fmt.Errorf("requester location not found for latency policy")
		}
	}

	for i := range advList.Items {
		cluster := &advList.Items[i]

		// Skip if it's the requester's own cluster
		if cluster.Spec.ClusterID == requesterID {
			continue
		}

		// Skip inactive clusters
		if !cluster.Status.Active {
			continue
		}

		// Check if cluster has enough resources
		if !d.hasEnoughResources(cluster, requestedCPU, requestedMemory, requestedGPU) {
			continue
		}

		if policy == "latency" && cluster.Spec.Location != nil {
			// Calculate latency-based score
			score, isActual := d.calculateScoreLatency(ctx, requesterAdv, cluster)
			scoredClusters = append(scoredClusters, ScoredCluster{cluster: cluster, score: score, isActual: isActual})
		} else {
			// Calculate standard score
			score := d.calculateScore(cluster, requestedCPU, requestedMemory, requestedGPU, priority)
			scoredClusters = append(scoredClusters, ScoredCluster{cluster: cluster, score: score})
		}
	}

	if len(scoredClusters) == 0 {
		return nil, fmt.Errorf("no suitable cluster found for requested resources")
	}

	// Rank clusters based on policy
	if policy == "latency" {
		return rankLatency(scoredClusters, maxResults), nil
	}
	return rankStandard(scoredClusters, maxResults), nil
}

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

// rankStandard sorts scored clusters by standard score (descending) and returns up to maxResults
func rankStandard(scoredClusters []ScoredCluster, maxResults int) []RankedCluster {
	// Sort descending by score (higher = better)
	sort.Slice(scoredClusters, func(i, j int) bool {
		return scoredClusters[i].score > scoredClusters[j].score
	})

	var bestClusters []RankedCluster
	for i, sc := range scoredClusters {
		if i >= maxResults {
			break
		}
		info := buildStandardInfo(sc.cluster)
		bestClusters = append(bestClusters, RankedCluster{Cluster: sc.cluster, Information: info})
	}
	return bestClusters
}

// buildStandardInfo generates a descriptive information string based on
// the cluster's resource availability tier and energy characteristics.
func buildStandardInfo(cluster *brokerv1alpha1.ClusterAdvertisement) string {
	// Calculate average availability ratio
	allocCPU := cluster.Spec.Resources.Allocatable.CPU.AsApproximateFloat64()
	allocMem := cluster.Spec.Resources.Allocatable.Memory.AsApproximateFloat64()
	availCPU := cluster.Spec.Resources.Available.CPU.AsApproximateFloat64()
	availMem := cluster.Spec.Resources.Available.Memory.AsApproximateFloat64()

	avgRatio := 0.0
	if allocCPU > 0 && allocMem > 0 {
		avgRatio = ((availCPU / allocCPU) + (availMem / allocMem)) / 2.0
	}

	// Tier based on availability
	var baseInfo string
	switch {
	case avgRatio >= 0.6:
		baseInfo = "High resource availability"
	case avgRatio >= 0.3:
		baseInfo = "Moderate resource availability"
	default:
		baseInfo = "Low resource availability"
	}

	// Append energy characteristics
	if cluster.Spec.Cost != nil {
		if cluster.Spec.Cost.Renewable {
			baseInfo += ", eco-friendly"
		}
		if cluster.Spec.Cost.EnergyCost < 0.3 {
			baseInfo += ", low energy cost"
		}
	}

	costStr := "unknown"
	if cluster.Spec.Cost != nil {
		costStr = fmt.Sprintf("%.2f", cluster.Spec.Cost.EnergyCost)
	}

	return fmt.Sprintf("cost: %s. info: %s", costStr, baseInfo)
}

// hasEnoughResources checks if cluster has sufficient available resources
func (d *DecisionEngine) hasEnoughResources(
	cluster *brokerv1alpha1.ClusterAdvertisement,
	requestedCPU, requestedMemory resource.Quantity,
	requestedGPU *resource.Quantity,
) bool {
	availableCPU := cluster.Spec.Resources.Available.CPU
	availableMemory := cluster.Spec.Resources.Available.Memory

	hasCPUAndMem := availableCPU.Cmp(requestedCPU) >= 0 && availableMemory.Cmp(requestedMemory) >= 0

	if requestedGPU != nil && requestedGPU.Sign() > 0 {
		if cluster.Spec.Resources.Available.GPU == nil || cluster.Spec.Resources.Available.GPU.Cmp(*requestedGPU) < 0 {
			return false
		}
	}

	return hasCPUAndMem
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

// calculateScore computes a score for the cluster based on availability
// Higher score = better choice
func (d *DecisionEngine) calculateScore(
	cluster *brokerv1alpha1.ClusterAdvertisement,
	requestedCPU, requestedMemory resource.Quantity,
	requestedGPU *resource.Quantity,
	priority int32,
) float64 {
	// Calculate CPU utilization after reservation (0-1)
	allocatableCPU := cluster.Spec.Resources.Allocatable.CPU.AsApproximateFloat64()
	availableCPU := cluster.Spec.Resources.Available.CPU.AsApproximateFloat64()
	requestedCPUFloat := requestedCPU.AsApproximateFloat64()

	cpuUtilization := 1.0 - ((availableCPU - requestedCPUFloat) / allocatableCPU)

	// Calculate Memory utilization after reservation (0-1)
	allocatableMemory := cluster.Spec.Resources.Allocatable.Memory.AsApproximateFloat64()
	availableMemory := cluster.Spec.Resources.Available.Memory.AsApproximateFloat64()
	requestedMemoryFloat := requestedMemory.AsApproximateFloat64()

	memoryUtilization := 1.0 - ((availableMemory - requestedMemoryFloat) / allocatableMemory)

	// Calculate GPU utilization if requested
	gpuUtilization := 0.0
	gpuWeight := 0.0
	if requestedGPU != nil && requestedGPU.Sign() > 0 && cluster.Spec.Resources.Allocatable.GPU != nil {
		allocatableGPU := cluster.Spec.Resources.Allocatable.GPU.AsApproximateFloat64()
		availableGPU := cluster.Spec.Resources.Available.GPU.AsApproximateFloat64()
		requestedGPUFloat := requestedGPU.AsApproximateFloat64()
		if allocatableGPU > 0 {
			gpuUtilization = 1.0 - ((availableGPU - requestedGPUFloat) / allocatableGPU)
			gpuWeight = 0.33 // distribute weight between cpu, mem, gpu
		}
	}

	// 1. Resource Availability (0-1) - Lower utilization means higher availability
	var resourceAvailability float64
	if gpuWeight > 0 {
		resourceAvailability = ((1.0 - cpuUtilization) * 0.33) + ((1.0 - memoryUtilization) * 0.34) + ((1.0 - gpuUtilization) * 0.33)
	} else {
		resourceAvailability = ((1.0 - cpuUtilization) * 0.5) + ((1.0 - memoryUtilization) * 0.5)
	}

	// 2. Cost and Renewable factors
	energyCost := 0.0
	renewableBonus := 0.0
	if cluster.Spec.Cost != nil {
		energyCost = cluster.Spec.Cost.EnergyCost
		if cluster.Spec.Cost.Renewable {
			renewableBonus = 0.1
		}
	}

	// Final weighted score = 70% resource availability + 20% energy cost + 10% renewable bonus
	score := (resourceAvailability * 0.7) + ((1.0 - energyCost) * 0.2) + renewableBonus

	priorityBonus := float64(priority) * 0.01

	return score + priorityBonus
}

// UpdateClusterScore updates the score field in the cluster advertisement status
func (d *DecisionEngine) UpdateClusterScore(
	ctx context.Context,
	cluster *brokerv1alpha1.ClusterAdvertisement,
) error {
	// Calculate base score (without specific reservation request)
	score := d.calculateBaseScore(cluster)

	cluster.Status.Score = strconv.FormatFloat(score, 'f', 2, 64)

	return nil
}

// calculateBaseScore computes the base score for a cluster
func (d *DecisionEngine) calculateBaseScore(cluster *brokerv1alpha1.ClusterAdvertisement) float64 {
	allocatableCPU := cluster.Spec.Resources.Allocatable.CPU.AsApproximateFloat64()
	availableCPU := cluster.Spec.Resources.Available.CPU.AsApproximateFloat64()

	allocatableMemory := cluster.Spec.Resources.Allocatable.Memory.AsApproximateFloat64()
	availableMemory := cluster.Spec.Resources.Available.Memory.AsApproximateFloat64()

	if allocatableCPU == 0 || allocatableMemory == 0 {
		return 0
	}

	// Score based on available percentage (0-1)
	cpuAvailableRatio := availableCPU / allocatableCPU
	memoryAvailableRatio := availableMemory / allocatableMemory
	resourceAvailability := (cpuAvailableRatio * 0.5) + (memoryAvailableRatio * 0.5)

	// Cost and Renewable factors
	energyCost := 0.0
	renewableBonus := 0.0
	if cluster.Spec.Cost != nil {
		energyCost = cluster.Spec.Cost.EnergyCost
		if cluster.Spec.Cost.Renewable {
			renewableBonus = 0.1
		}
	}

	// Final weighted score (0-1 range)
	return (resourceAvailability * 0.7) + ((1.0 - energyCost) * 0.2) + renewableBonus
}
