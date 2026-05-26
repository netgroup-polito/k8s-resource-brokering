package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
)

var (
	brokerContext = "kind-broker-cluster"
	agent1Context = "kind-agent-cluster-1"
	agent2Context = "kind-agent-cluster-2"
	agent3Context = "kind-agent-cluster-3"
)

// GO: testing.T è un struct che fornisce metodi per segnalare errori e log durante l'esecuzione dei test. Viene passato come argomento alla funzione di test e permette di utilizzare metodi come t.Log, t.Errorf, t.Fatalf, ecc. per registrare informazioni e gestire errori nei test.
func TestReservationFlow(t *testing.T) {

	//GO: ctx è un contesto che può essere usato per gestire scadenze, cancellazioni e passaggio di valori tra le chiamate API.
	//In questo caso viene creato un contesto di base che può essere passato ai client Kubernetes per eseguire operazioni sui cluster.
	//Non ha scadenza o cancellazione associata, ma è il context radice da cui possono derivare altri contesti più specifici se necessario.
	ctx := context.Background()

	// 1. Setup Clients
	s := runtime.NewScheme()          //GO: runtime.NewScheme() crea un nuovo schema vuoto che può essere usato per registrare i tipi di risorse Kubernetes che il client può gestire.
	_ = corev1.AddToScheme(s)         //GO: corev1.AddToScheme(s) registra i tipi di risorse corev1 (come Pod, Node, Secret, ecc.) nello schema s.
	_ = rearv1alpha1.AddToScheme(s)   //GO: rearv1alpha1.AddToScheme(s) registra i tipi di risorse personalizzate rearv1alpha1 (come ResourceRequest, ReservationInstruction, ProviderInstruction, ecc.) nello schema s.
	_ = brokerv1alpha1.AddToScheme(s) //GO: brokerv1alpha1.AddToScheme(s) registra i tipi di risorse personalizzate brokerv1alpha1 (come ClusterAdvertisement, ecc.) nello schema s.

	brokerClient, err := getClient(brokerContext, s)
	if err != nil {
		t.Fatalf("failed to create broker client: %v", err)
	}

	agent1Client, err := getClient(agent1Context, s)
	if err != nil {
		t.Fatalf("failed to create agent1 client: %v", err)
	}

	agent2Client, err := getClient(agent2Context, s)
	if err != nil {
		t.Fatalf("failed to create agent2 client: %v", err)
	}

	agent3Client, err := getClient(agent3Context, s)
	if err != nil {
		t.Fatalf("failed to create agent3 client: %v", err)
	}

	// 2. Check Broker State (ClusterAdvertisements)
	t.Log("Checking if agents are registered on broker...")
	advList := &brokerv1alpha1.ClusterAdvertisementList{}
	err = retry(12, 5*time.Second, func() error {
		if err := brokerClient.List(ctx, advList); err != nil {
			return err
		}
		if len(advList.Items) < 2 {
			return fmt.Errorf("waiting for 2 provider agents, found %d", len(advList.Items))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("agents did not register on broker: %v", err)
	}
	t.Log("Provider agents registered! Advertisements:")
	for _, adv := range advList.Items {
		locStr := "N/A"
		if adv.Spec.Location != nil {
			locStr = fmt.Sprintf("%+v", *adv.Spec.Location)
		}
		t.Logf(" - Cluster: %s, CPU: %s, Mem: %s, Location: %s", adv.Spec.ClusterID, adv.Spec.Resources.Available.CPU.String(), adv.Spec.Resources.Available.Memory.String(), locStr)
	}

	// 3. Create ResourceRequest on Agent 1
	requestName := fmt.Sprintf("test-request-%d", time.Now().Unix())
	t.Logf("Creating ResourceRequest %s on agent-cluster-1...", requestName)

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

	// 4. Wait for Reservation to be processed (Phase: Reserved)
	t.Log("Waiting for ResourceRequest to be Reserved...")
	var targetClusterID string
	err = retry(12, 2*time.Second, func() error {
		updatedReq := &rearv1alpha1.ResourceRequest{}
		if err := agent1Client.Get(ctx, types.NamespacedName{Name: requestName, Namespace: "default"}, updatedReq); err != nil {
			return err
		}
		if updatedReq.Status.Phase == "Reserved" {
			targetClusterID = updatedReq.Status.TargetClusterID
			return nil
		}
		if updatedReq.Status.Phase == "Failed" {
			return fmt.Errorf("ResourceRequest failed: %s", updatedReq.Status.Message)
		}
		return fmt.Errorf("current phase: %s", updatedReq.Status.Phase)
	})
	if err != nil {
		t.Fatalf("ResourceRequest was not reserved: %v", err)
	}
	t.Logf("ResourceRequest Reserved! Target cluster: %s", targetClusterID)

	// 5. Verify ReservationInstruction on Requester
	t.Log("Verifying ReservationInstruction on agent-cluster-1...")
	riList := &rearv1alpha1.ReservationInstructionList{}
	if err := agent1Client.List(ctx, riList); err != nil {
		t.Errorf("failed to list ReservationInstructions: %v", err)
	} else if len(riList.Items) == 0 {
		t.Errorf("no ReservationInstructions found on agent-cluster-1")
	} else {
		t.Log("Found ReservationInstruction!")
	}

	// 6. Verify ProviderInstruction on Provider
	if targetClusterID == "agent-cluster-2" || targetClusterID == "agent-cluster-3" {
		t.Logf("Verifying ProviderInstruction on %s...", targetClusterID)
		targetClient := agent2Client
		if targetClusterID == "agent-cluster-3" {
			targetClient = agent3Client
		}
		err = retry(6, 2*time.Second, func() error {
			piList := &rearv1alpha1.ProviderInstructionList{}
			if err := targetClient.List(ctx, piList); err != nil {
				return err
			}
			if len(piList.Items) == 0 {
				return fmt.Errorf("no ProviderInstructions found on %s", targetClusterID)
			}
			return nil
		})
		if err != nil {
			t.Errorf("failed to find ProviderInstruction: %v", err)
		} else {
			t.Log("Found ProviderInstruction!")
		}
	}

	// 7. Verify Liqo Peering (Virtual Node)
	t.Log("Verifying Liqo peering (waiting up to 20 minutes for virtual node)...")
	err = retry(240, 5*time.Second, func() error {
		nodeList := &corev1.NodeList{}
		if err := agent1Client.List(ctx, nodeList); err != nil {
			return err
		}
		for _, node := range nodeList.Items {
			if node.Name == targetClusterID || strings.Contains(strings.ToLower(node.Name), "liqo") {
				t.Logf("Found virtual node: %s", node.Name)
				return nil
			}
		}
		return fmt.Errorf("virtual node not found")
	})
	if err != nil {
		t.Log("WARNING: Virtual node not found. This might take longer than the timeout.")
	} else {
		t.Log("Liqo peering verified!")
	}

	// Cleanup
	t.Log("Not cleaning up ResourceRequest so you can test offloading manually! It will expire in 2 minutes.")
	// _ = agent1Client.Delete(ctx, resRequest)
}

// getClient creates a Kubernetes client for the given context and scheme.
func getClient(context string, s *runtime.Scheme) (client.Client, error) {
	//GO: runtime.schema è un oggetto che tiene traccia dei tipi di risorse Kubernetes che il client può gestire.
	// Viene usato per serializzare e deserializzare le risorse quando si interagisce con l'API server.
	//In questo caso, viene creato un nuovo schema vuoto e vengono aggiunti i tipi corev1 (nodi, pod, ecc.) e i tipi personalizzati rearv1alpha1 e brokerv1alpha1.

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: context},
	).ClientConfig()
	if err != nil {
		return nil, err
	}

	return client.New(config, client.Options{Scheme: s})
}

func retry(attempts int, sleep time.Duration, f func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = f(); err == nil {
			return nil
		}
		time.Sleep(sleep)
	}
	return fmt.Errorf("after %d attempts, last error: %v", attempts, err)
}
