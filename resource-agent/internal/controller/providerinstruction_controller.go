package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport"
)

// ProviderInstructionReconciler acknowledges provider instructions.
type ProviderInstructionReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	BrokerCommunicator transport.BrokerCommunicator
	KubeconfigsDir     string
	ClusterID          string
}

// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=providerinstructions,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=providerinstructions/status,verbs=get;update;patch

func (r *ProviderInstructionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	instruction := &rearv1alpha1.ProviderInstruction{}
	if err := r.Get(ctx, req.NamespacedName, instruction); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("provider instruction deleted", "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check if expired - if so, we can potentially delete it or just mark as not enforced
	if instruction.Spec.ExpiresAt != nil && instruction.Spec.ExpiresAt.Time.Before(time.Now()) {
		logger.Info("provider instruction expired",
			"instruction", instruction.Name,
			"reservation", instruction.Spec.ReservationName,
			"expiresAt", instruction.Spec.ExpiresAt.Time)

		// Mark as not enforced so it won't be counted in reserved resources
		instruction.Status.Enforced = false
		instruction.Status.LastUpdateTime = metav1.Now()

		if err := r.Status().Update(ctx, instruction); err != nil {
			logger.Error(err, "failed to mark expired instruction")
			return ctrl.Result{}, err
		}

		// No need to requeue - it's expired
		return ctrl.Result{}, nil
	}

	// If already enforced, just requeue to check expiration later
	if instruction.Status.Enforced {
		// Requeue before expiration to mark it as expired promptly
		if instruction.Spec.ExpiresAt != nil {
			timeUntilExpiry := time.Until(instruction.Spec.ExpiresAt.Time)
			if timeUntilExpiry > 0 {
				return ctrl.Result{RequeueAfter: timeUntilExpiry}, nil
			}
		}
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Mark as enforced
	expiryInfo := ""
	if instruction.Spec.ExpiresAt != nil {
		expiryInfo = fmt.Sprintf("\n  └─ Expiration: %s", instruction.Spec.ExpiresAt.Format("15:04:05"))
	}
	logger.Info(fmt.Sprintf("🔒 Provider Instruction Received\n"+
		"  └─ Reservation: %s\n"+
		"  └─ Requester Cluster: %s\n"+
		"  └─ Resources: cpu=%s, memory=%s%s",
		instruction.Spec.ReservationName,
		instruction.Spec.RequesterClusterID,
		instruction.Spec.RequestedCPU,
		instruction.Spec.RequestedMemory,
		expiryInfo))

	// Generate peering-user kubeconfig and upload to broker
	if r.BrokerCommunicator != nil {
		// Clean up any existing CSR (certificate signing request) and RBAC artifacts for this peering user before attempting to generate a new one
		localKubeconfig := filepath.Join(r.KubeconfigsDir, r.ClusterID+".kubeconfig")
		var stdout bytes.Buffer

		if _, err := os.Stat(localKubeconfig); err == nil {
			logger.Info("Peering kubeconfig already exists locally, skipping regeneration to avoid invalidating existing user", "requesterClusterID", instruction.Spec.RequesterClusterID)
			
			// Just read the existing one
			content, err := os.ReadFile(localKubeconfig)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to read existing local kubeconfig: %v", err)
			}
			stdout.WriteString(string(content))
		} else {
			// This ensures idempotency if the pod crashed after creating roles but before uploading the kubeconfig
			cleanupResources := [][]string{
				{"csr", fmt.Sprintf("liqo-peer-user-%s", instruction.Spec.RequesterClusterID)},
				{"rolebinding", fmt.Sprintf("liqo-peer-user-%s-liqo-ns-reader", instruction.Spec.RequesterClusterID), "-n", "liqo"},
				{"clusterrolebinding", fmt.Sprintf("liqo-peer-user-%s-tenant-controlplane", instruction.Spec.RequesterClusterID)},
				{"clusterrole", fmt.Sprintf("liqo-peer-user-%s-tenant-controlplane", instruction.Spec.RequesterClusterID)},
			}

			for _, res := range cleanupResources {
				// Cleanup with a timeout context to avoid hanging if kubectl has issues
				ctxObj, cancelObj := context.WithTimeout(ctx, 15*time.Second)
				args := append([]string{"--kubeconfig", localKubeconfig, "delete"}, res...)
				args = append(args, "--ignore-not-found=true") //idempotent cleanup
				cleanupCmd := exec.CommandContext(ctxObj, "kubectl", args...)
				if err := cleanupCmd.Run(); err != nil {
					logger.Info("Cleanup returned an error (can be ignored)", "resource", res[0], "error", err)
				}
				cancelObj()
			}

			//peeringCTX is a new context (inherited from the main ctx) with a timeout of 2 minutes, used to set the timeout for the peering user kubeconfig generation command. 
			//cancelPeering is a function (context.CancelFunc) that will free resources associated with the peeringCtx when called.
			peeringCtx, cancelPeering := context.WithTimeout(ctx, 2*time.Minute)

			//GO: defer indica che la funzione cancelPeering() verrà eseguita alla fine del blocco corrente (la funzione Reconcile),
			// indipendentemente da come si esce da esso (sia in caso di successo che di errore). 
			// In questo caso, garantisce che le risorse associate al contesto peeringCtx vengano rilasciate correttamente dopo l'esecuzione del comando per generare il kubeconfig.
			defer cancelPeering()
			
			//Execution of the command to generate the peering-user kubeconfig. 
			peeringUserCmd := exec.CommandContext(peeringCtx, "liqoctl", "generate", "peering-user",
				"--kubeconfig", localKubeconfig,
				"--consumer-cluster-id", instruction.Spec.RequesterClusterID)

			var stderr bytes.Buffer
			peeringUserCmd.Stdout = &stdout
			peeringUserCmd.Stderr = &stderr

			logger.Info("Generating peering-user kubeconfig", "requesterClusterID", instruction.Spec.RequesterClusterID)
			if err := peeringUserCmd.Run(); err != nil {
				logger.Error(err, "Failed to generate peering-user kubeconfig", "stderr", stderr.String())
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
		}
		kubeconfigContent := strings.TrimSpace(stdout.String())

		// Upload to the broker
		logger.Info("Uploading peering kubeconfig to broker")
		err := r.BrokerCommunicator.UploadPeeringKubeconfig(ctx, instruction.Spec.ReservationName, kubeconfigContent)
		if err != nil {
			logger.Error(err, "Failed to upload peering kubeconfig to broker")
			// We requeue and try again. Next time it will regenerate, which is idempotent and fine.
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	} else {
		logger.Info("BrokerCommunicator is nil, skipping peering kubeconfig generation")
	}

	instruction.Status.Enforced = true
	instruction.Status.LastUpdateTime = metav1.Now()

	if err := r.Status().Update(ctx, instruction); err != nil {
		logger.Error(err, "failed to enforce provider instruction")
		return ctrl.Result{}, err
	}

	// Requeue to check for expiration
	if instruction.Spec.ExpiresAt != nil {
		timeUntilExpiry := time.Until(instruction.Spec.ExpiresAt.Time)
		if timeUntilExpiry > 0 {
			logger.Info("will requeue to check expiration",
				"timeUntilExpiry", timeUntilExpiry)
			return ctrl.Result{RequeueAfter: timeUntilExpiry}, nil
		}
	}

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *ProviderInstructionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rearv1alpha1.ProviderInstruction{}).
		Named("providerinstruction").
		Complete(r)
}
