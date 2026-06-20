package broker

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DecisionEngine selects the best cluster for resource allocation
type DecisionEngine struct {
	Client          client.Client
	MockEcoURL      string // URL for the mock-eco carbon intensity service
	MockCurrencyURL string // URL for the mock-currency exchange rates service
}

// ScoredCluster associates a cluster with its computed score and whether the latency is actual
type ScoredCluster struct {
	cluster  *brokerv1alpha1.ClusterAdvertisement
	score    float64
	isActual bool // true if score is based on actual measured latency (latency policy)
	increase bool // true if carbon intensity is expected to increase (eco policy)
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

	// Update cost and energy price information for all clusters
	d.updateCosts(ctx, advList)

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

		switch {
		case policy == "latency" && cluster.Spec.Location != nil:
			// Calculate latency-based score
			score, isActual := d.calculateScoreLatency(ctx, requesterAdv, cluster)
			scoredClusters = append(scoredClusters, ScoredCluster{cluster: cluster, score: score, isActual: isActual})
		case policy == "eco":
			// Calculate eco (carbon intensity) score
			score, increase := d.calculateScoreEco(ctx, cluster)
			scoredClusters = append(scoredClusters, ScoredCluster{cluster: cluster, score: score, increase: increase})
		default:
			// Calculate standard score
			score := d.calculateScore(cluster, requestedCPU, requestedMemory, requestedGPU, priority)
			scoredClusters = append(scoredClusters, ScoredCluster{cluster: cluster, score: score})
		}
	}

	if len(scoredClusters) == 0 {
		return nil, fmt.Errorf("no suitable cluster found for requested resources")
	}

	// Rank clusters based on policy
	switch policy {
	case "latency":
		return rankLatency(scoredClusters, maxResults), nil
	case "eco":
		return rankEco(scoredClusters, maxResults), nil
	default:
		return rankStandard(scoredClusters, maxResults), nil
	}
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
