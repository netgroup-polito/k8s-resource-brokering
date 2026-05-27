package broker

import (
	"context"
	"testing"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Helper function to create a test cluster advertisement
func makeClusterAdvertisement(name, clusterID string, allocatableCPU, allocatableMemory, availableCPU, availableMemory string, active bool) *brokerv1alpha1.ClusterAdvertisement {
	return &brokerv1alpha1.ClusterAdvertisement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: brokerv1alpha1.ClusterAdvertisementSpec{
			ClusterID: clusterID,
			Resources: brokerv1alpha1.ResourceMetrics{
				Allocatable: brokerv1alpha1.ResourceQuantities{
					CPU:    resource.MustParse(allocatableCPU),
					Memory: resource.MustParse(allocatableMemory),
				},
				Available: brokerv1alpha1.ResourceQuantities{
					CPU:    resource.MustParse(availableCPU),
					Memory: resource.MustParse(availableMemory),
				},
			},
		},
		Status: brokerv1alpha1.ClusterAdvertisementStatus{
			Active: active,
		},
	}
}

// Create a fake client with the given objects
func createFakeClient(objects ...runtime.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = brokerv1alpha1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objects...).Build()
}

// Test: When two clusters exist, pick the one with more available resources
func TestRankClusters_PicksClusterWithMoreResources(t *testing.T) {
	// Setup: cluster-1 has 1000m CPU, cluster-2 has 4000m CPU
	cluster1 := makeClusterAdvertisement("cluster-1-adv", "cluster-1", "2000m", "4Gi", "1000m", "2Gi", true)
	cluster2 := makeClusterAdvertisement("cluster-2-adv", "cluster-2", "8000m", "16Gi", "4000m", "8Gi", true)

	fakeClient := createFakeClient(cluster1, cluster2)
	engine := &DecisionEngine{Client: fakeClient}

	// Request 500m CPU, 1Gi memory from requester "cluster-0"
	results, err := engine.RankClusters(
		context.Background(),
		"cluster-0", // requester (not cluster-1 or cluster-2)
		resource.MustParse("500m"),
		resource.MustParse("1Gi"),
		nil, // requestedGPU
		0,   // priority
		1,   // maxResults
		"",  // policy
	)

	// Verify: should pick cluster-2 (more headroom = higher score)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 || results[0].Spec.ClusterID != "cluster-2" {
		t.Errorf("expected cluster-2, got %v", results)
	}
}

// Test: Never pick the requester's own cluster
func TestRankClusters_SkipsRequesterOwnCluster(t *testing.T) {
	// Setup: cluster-1 is the requester, cluster-2 is available
	cluster1 := makeClusterAdvertisement("cluster-1-adv", "cluster-1", "8000m", "16Gi", "6000m", "12Gi", true)
	cluster2 := makeClusterAdvertisement("cluster-2-adv", "cluster-2", "4000m", "8Gi", "2000m", "4Gi", true)

	fakeClient := createFakeClient(cluster1, cluster2)
	engine := &DecisionEngine{Client: fakeClient}

	// cluster-1 requests resources (should not pick itself even though it has more)
	results, err := engine.RankClusters(
		context.Background(),
		"cluster-1", // requester IS cluster-1
		resource.MustParse("500m"),
		resource.MustParse("1Gi"),
		nil, // requestedGPU
		0,
		1,
		"",
	)

	// Verify: must pick cluster-2, not cluster-1
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 || results[0].Spec.ClusterID != "cluster-2" {
		t.Errorf("expected cluster-2, got %v (should never pick own cluster)", results)
	}
}

// Test: Return error when no clusters are available at all
func TestRankClusters_NoClusterAvailable(t *testing.T) {
	// Setup: no clusters in the system
	fakeClient := createFakeClient() // empty
	engine := &DecisionEngine{Client: fakeClient}

	_, err := engine.RankClusters(
		context.Background(),
		"cluster-0",
		resource.MustParse("500m"),
		resource.MustParse("1Gi"),
		nil,
		0,
		1,
		"",
	)

	// Verify: should return error
	if err == nil {
		t.Error("expected error when no clusters available, got nil")
	}
}

// Test: Skip inactive clusters
func TestRankClusters_SkipsInactiveClusters(t *testing.T) {
	// Setup: cluster-1 is inactive (has more resources), cluster-2 is active
	cluster1 := makeClusterAdvertisement("cluster-1-adv", "cluster-1", "8000m", "16Gi", "6000m", "12Gi", false) // inactive
	cluster2 := makeClusterAdvertisement("cluster-2-adv", "cluster-2", "4000m", "8Gi", "2000m", "4Gi", true)    // active

	fakeClient := createFakeClient(cluster1, cluster2)
	engine := &DecisionEngine{Client: fakeClient}

	results, err := engine.RankClusters(
		context.Background(),
		"cluster-0",
		resource.MustParse("500m"),
		resource.MustParse("1Gi"),
		nil,
		0,
		1,
		"",
	)

	// Verify: should pick cluster-2 (active), not cluster-1 (inactive)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 || results[0].Spec.ClusterID != "cluster-2" {
		t.Errorf("expected cluster-2 (active), got %v", results)
	}
}

// Test: Return error when request exceeds all clusters' capacity
func TestRankClusters_RequestExceedsAllClusters(t *testing.T) {
	// Setup: clusters have 1000m and 2000m available CPU
	cluster1 := makeClusterAdvertisement("cluster-1-adv", "cluster-1", "2000m", "4Gi", "1000m", "2Gi", true)
	cluster2 := makeClusterAdvertisement("cluster-2-adv", "cluster-2", "4000m", "8Gi", "2000m", "4Gi", true)

	fakeClient := createFakeClient(cluster1, cluster2)
	engine := &DecisionEngine{Client: fakeClient}

	// Request 10000m CPU - more than any cluster has
	_, err := engine.RankClusters(
		context.Background(),
		"cluster-0",
		resource.MustParse("10000m"), // 10 cores - too much
		resource.MustParse("1Gi"),
		nil,
		0,
		1,
		"",
	)

	// Verify: should return error
	if err == nil {
		t.Error("expected error when request exceeds all clusters, got nil")
	}
}

// Test: With equal available, cluster with higher available/allocatable ratio wins
// The scoring algorithm calculates post-reservation utilization as:
//
//	utilization = 1.0 - ((available - requested) / allocatable)
//
// Lower utilization = higher score
func TestRankClusters_PrefersHigherAvailableRatio(t *testing.T) {
	// Setup: both have same available (2000m), but different allocatable
	// cluster-1: 2000m available / 4000m allocatable = 50% free → lower post-request utilization
	// cluster-2: 2000m available / 8000m allocatable = 25% free → higher post-request utilization
	// The algorithm prefers cluster-1 because (available-request)/allocatable is higher
	cluster1 := makeClusterAdvertisement("cluster-1-adv", "cluster-1", "4000m", "8Gi", "2000m", "4Gi", true)
	cluster2 := makeClusterAdvertisement("cluster-2-adv", "cluster-2", "8000m", "16Gi", "2000m", "4Gi", true)

	fakeClient := createFakeClient(cluster1, cluster2)
	engine := &DecisionEngine{Client: fakeClient}

	results, err := engine.RankClusters(
		context.Background(),
		"cluster-0",
		resource.MustParse("500m"),
		resource.MustParse("1Gi"),
		nil,
		0,
		1,
		"",
	)

	// Verify: should pick cluster-1 (higher available/allocatable ratio = better score)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 || results[0].Spec.ClusterID != "cluster-1" {
		t.Errorf("expected cluster-1 (higher available ratio), got %v", results)
	}
}

// Test: hasEnoughResources correctly checks both CPU and memory
func TestHasEnoughResources_ChecksBothCPUAndMemory(t *testing.T) {
	engine := &DecisionEngine{}

	tests := []struct {
		name            string
		availableCPU    string
		availableMemory string
		requestedCPU    string
		requestedMemory string
		expected        bool
	}{
		{
			name:            "enough of both",
			availableCPU:    "2000m",
			availableMemory: "4Gi",
			requestedCPU:    "1000m",
			requestedMemory: "2Gi",
			expected:        true,
		},
		{
			name:            "not enough CPU",
			availableCPU:    "500m",
			availableMemory: "4Gi",
			requestedCPU:    "1000m",
			requestedMemory: "2Gi",
			expected:        false,
		},
		{
			name:            "not enough memory",
			availableCPU:    "2000m",
			availableMemory: "1Gi",
			requestedCPU:    "1000m",
			requestedMemory: "2Gi",
			expected:        false,
		},
		{
			name:            "exactly matching",
			availableCPU:    "1000m",
			availableMemory: "2Gi",
			requestedCPU:    "1000m",
			requestedMemory: "2Gi",
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := makeClusterAdvertisement("test", "test-cluster", "4000m", "8Gi", tt.availableCPU, tt.availableMemory, true)
			result := engine.hasEnoughResources(cluster, resource.MustParse(tt.requestedCPU), resource.MustParse(tt.requestedMemory), nil)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// Test: calculateBaseScore returns 0 when allocatable is 0
func TestCalculateBaseScore_ZeroAllocatable(t *testing.T) {
	engine := &DecisionEngine{}

	cluster := makeClusterAdvertisement("test", "test-cluster", "0", "0", "0", "0", true)
	score := engine.calculateBaseScore(cluster)

	if score != 0 {
		t.Errorf("expected score 0 for zero allocatable, got %f", score)
	}
}

// Test: calculateBaseScore gives higher score to more available resources
func TestCalculateBaseScore_HigherAvailableGivesHigherScore(t *testing.T) {
	engine := &DecisionEngine{}

	// cluster with 50% available
	cluster1 := makeClusterAdvertisement("test1", "cluster-1", "4000m", "8Gi", "2000m", "4Gi", true)
	// cluster with 75% available
	cluster2 := makeClusterAdvertisement("test2", "cluster-2", "4000m", "8Gi", "3000m", "6Gi", true)

	score1 := engine.calculateBaseScore(cluster1)
	score2 := engine.calculateBaseScore(cluster2)

	if score2 <= score1 {
		t.Errorf("expected cluster2 (more available) to have higher score, got score1=%f, score2=%f", score1, score2)
	}
}
