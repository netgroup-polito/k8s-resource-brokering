package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
)

// TestReEvaluationFlow tests the full re-evaluation lifecycle:
//  1. Agent-1 (requester) creates a ResourceRequest → first reservation with provider X
//  2. After the peering is established, agent-1 re-evaluates (every 30s in test config)
//  3. Due to "new cluster first" logic, the broker promotes the never-connected provider Y
//  4. Agent-1 switches to provider Y
//
// This test validates that the latency scraping, re-evaluation, and provider switching work end-to-end.
func TestReEvaluationFlow(t *testing.T) {
	ctx := context.Background()

	// Setup Clients
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = rearv1alpha1.AddToScheme(s)
	_ = brokerv1alpha1.AddToScheme(s)

	brokerClient, err := getClient(brokerContext, s)
	if err != nil {
		t.Fatalf("failed to create broker client: %v", err)
	}

	agent1Client, err := getClient(agent1Context, s)
	if err != nil {
		t.Fatalf("failed to create agent1 client: %v", err)
	}

	// 1. Wait for agents to register on broker
	t.Log("Step 1: Waiting for agents to register on broker...")
	advList := &brokerv1alpha1.ClusterAdvertisementList{}
	err = retry(12, 5*time.Second, func() error {
		if err := brokerClient.List(ctx, advList); err != nil {
			return err
		}
		if len(advList.Items) < 2 {
			return fmt.Errorf("waiting for at least 2 agents, found %d", len(advList.Items))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("agents did not register on broker: %v", err)
	}
	t.Logf("Agents registered! Found %d advertisements", len(advList.Items))

	// 2. Create ResourceRequest on Agent 1
	requestName := fmt.Sprintf("test-reeval-%d", time.Now().Unix())
	t.Logf("Step 2: Creating ResourceRequest %s on agent-cluster-1...", requestName)

	resRequest := &rearv1alpha1.ResourceRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rear.fluidos.eu/v1alpha1",
			Kind:       "ResourceRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      requestName,
			Namespace: "default",
		},
		Spec: rearv1alpha1.ResourceRequestSpec{
			RequestedCPU:    "500m",
			RequestedMemory: "256Mi",
			Priority:        10,
			Duration:        "10m",
		},
	}

	if err := agent1Client.Create(ctx, resRequest); err != nil {
		t.Fatalf("failed to create ResourceRequest: %v", err)
	}

	// 3. Wait for first reservation (Phase: Reserved)
	t.Log("Step 3: Waiting for first reservation...")
	var firstTarget string
	err = retry(30, 2*time.Second, func() error {
		updatedReq := &rearv1alpha1.ResourceRequest{}
		if err := agent1Client.Get(ctx, types.NamespacedName{Name: requestName, Namespace: "default"}, updatedReq); err != nil {
			return err
		}
		if updatedReq.Status.Phase == "Reserved" {
			firstTarget = updatedReq.Status.TargetClusterID
			return nil
		}
		if updatedReq.Status.Phase == "Failed" {
			return fmt.Errorf("ResourceRequest failed: %s", updatedReq.Status.Message)
		}
		return fmt.Errorf("current phase: %s", updatedReq.Status.Phase)
	})
	if err != nil {
		t.Fatalf("first reservation failed: %v", err)
	}
	t.Logf("✅ First reservation established! Target: %s", firstTarget)

	// 4. Check NetworkBond on broker (should have been created during evaluation)
	t.Log("Step 4: Checking NetworkBond on broker...")
	bondList := &brokerv1alpha1.NetworkBondList{}
	err = retry(6, 5*time.Second, func() error {
		if err := brokerClient.List(ctx, bondList); err != nil {
			return err
		}
		if len(bondList.Items) == 0 {
			return fmt.Errorf("no NetworkBonds found yet")
		}
		return nil
	})
	if err != nil {
		t.Logf("WARNING: No NetworkBonds found on broker: %v", err)
	} else {
		t.Logf("Found %d NetworkBond(s):", len(bondList.Items))
		for _, bond := range bondList.Items {
			t.Logf("  - %s: requester=%s, provider=%s, estimated=%.2fms, actual=%.2fms, timestamp=%s",
				bond.Name,
				bond.Spec.RequesterClusterID,
				bond.Spec.ProviderClusterID,
				bond.Spec.EstimatedLatency,
				bond.Spec.ActualLatency,
				bond.Spec.Timestamp.Time.Format(time.RFC3339))
		}
	}

	// 5. Wait for re-evaluation to trigger a provider switch
	// With re-eval-interval=30s, after the first reservation is established,
	// the controller will re-evaluate and find a "never connected" cluster to switch to.
	t.Log("Step 5: Waiting for re-evaluation to trigger provider switch (up to 3 minutes)...")
	var secondTarget string
	err = retry(36, 5*time.Second, func() error {
		updatedReq := &rearv1alpha1.ResourceRequest{}
		if err := agent1Client.Get(ctx, types.NamespacedName{Name: requestName, Namespace: "default"}, updatedReq); err != nil {
			return err
		}

		// Check if the target has changed
		if updatedReq.Status.Phase == "Reserved" && updatedReq.Status.TargetClusterID != firstTarget {
			secondTarget = updatedReq.Status.TargetClusterID
			return nil
		}

		// Also accept if we see Pending phase (switch in progress)
		if updatedReq.Status.Phase == "Pending" {
			return fmt.Errorf("switch in progress (phase=Pending), waiting for new Reserved...")
		}

		return fmt.Errorf("still reserved with %s (phase=%s)", updatedReq.Status.TargetClusterID, updatedReq.Status.Phase)
	})
	if err != nil {
		t.Fatalf("re-evaluation did not trigger provider switch: %v", err)
	}
	t.Logf("✅ Re-evaluation triggered provider switch! %s → %s", firstTarget, secondTarget)

	// 6. Verify the NetworkBond was updated with actual latency (if Liqo metrics are available)
	t.Log("Step 6: Checking NetworkBond for actual latency data...")
	err = retry(6, 5*time.Second, func() error {
		updatedBondList := &brokerv1alpha1.NetworkBondList{}
		if err := brokerClient.List(ctx, updatedBondList); err != nil {
			return err
		}
		for _, bond := range updatedBondList.Items {
			if bond.Spec.RequesterClusterID == "agent-cluster-1" && bond.Spec.ProviderClusterID == firstTarget {
				if bond.Spec.ActualLatency > 0 {
					t.Logf("✅ NetworkBond %s has actual latency: %.2fms (timestamp: %s)",
						bond.Name, bond.Spec.ActualLatency, bond.Spec.Timestamp.Time.Format(time.RFC3339))
					return nil
				}
				return fmt.Errorf("NetworkBond %s found but actualLatency is 0", bond.Name)
			}
		}
		return fmt.Errorf("NetworkBond for agent-cluster-1 → %s not found", firstTarget)
	})
	if err != nil {
		t.Logf("WARNING: Actual latency not yet populated in NetworkBond (Liqo metrics may not be enabled): %v", err)
		t.Log("This is expected if Liqo was installed without --enable-metrics")
	}

	// 7. Final state summary
	t.Log("Step 7: Final state summary")
	t.Logf("  First provider:  %s", firstTarget)
	t.Logf("  Second provider: %s", secondTarget)

	// Verify the two targets are different
	if firstTarget == secondTarget {
		t.Errorf("Expected provider switch but both targets are the same: %s", firstTarget)
	} else {
		t.Log("✅ Re-evaluation test passed! Provider switch confirmed.")
	}
}
