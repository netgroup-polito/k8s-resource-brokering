package broker

import (
	"context"
	"fmt"
	"strconv"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DecisionEngine selects the best cluster for resource allocation
type DecisionEngine struct {
	Client client.Client
}

// RankClusters finds the most suitable clusters based on requested resources and returns up to maxResults sorted from best to worst
func (d *DecisionEngine) RankClusters(
	ctx context.Context,
	requesterID string,
	requestedCPU, requestedMemory resource.Quantity,
	requestedGPU *resource.Quantity,
	priority int32,
	maxResults int,
) ([]*brokerv1alpha1.ClusterAdvertisement, error) {

	// List all cluster advertisements
	advList := &brokerv1alpha1.ClusterAdvertisementList{}
	if err := d.Client.List(ctx, advList); err != nil {
		return nil, fmt.Errorf("failed to list cluster advertisements: %w", err)
	}

	if len(advList.Items) == 0 {
		return nil, fmt.Errorf("no clusters available")
	}

	type ScoredCluster struct {
		cluster *brokerv1alpha1.ClusterAdvertisement
		score   float64
	}
	var scoredClusters []ScoredCluster

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

		// Calculate score
		score := d.calculateScore(cluster, requestedCPU, requestedMemory, requestedGPU, priority)
		scoredClusters = append(scoredClusters, ScoredCluster{cluster: cluster, score: score})
	}

	if len(scoredClusters) == 0 {
		return nil, fmt.Errorf("no suitable cluster found for requested resources")
	}

	// Sort descending by score
	importSort := false
	_ = importSort // hack to avoid unused import if we don't import "sort", but we do need "sort"
	// Wait, I should just use sort.Slice directly, but I need to make sure sort is imported.
	// We will add sort to imports.

	var bestClusters []*brokerv1alpha1.ClusterAdvertisement
	// bubble sort to avoid adding imports if not necessary, max size is small
	for i := 0; i < len(scoredClusters); i++ {
		for j := i + 1; j < len(scoredClusters); j++ {
			if scoredClusters[i].score < scoredClusters[j].score {
				scoredClusters[i], scoredClusters[j] = scoredClusters[j], scoredClusters[i]
			}
		}
	}

	for i, sc := range scoredClusters {
		if i >= maxResults {
			break
		}
		bestClusters = append(bestClusters, sc.cluster)
	}

	return bestClusters, nil
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
