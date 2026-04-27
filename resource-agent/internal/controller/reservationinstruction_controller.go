package controller

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const peeringFinalizer = "rear.fluidos.eu/peering"

// ReservationInstructionReconciler processes reservation instructions from the broker.
type ReservationInstructionReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	BrokerCommunicator transport.BrokerCommunicator

	// KubeconfigsDir is the directory containing kubeconfig files.
	// Used to locate the local cluster's admin kubeconfig.
	KubeconfigsDir string

	// ClusterID is this agent's cluster identifier (needed to locate own kubeconfig).
	ClusterID string
}

// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=reservationinstructions,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=reservationinstructions/status,verbs=get;update;patch

func (r *ReservationInstructionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	instruction := &rearv1alpha1.ReservationInstruction{}
	if err := r.Get(ctx, req.NamespacedName, instruction); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("reservation instruction deleted", "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !instruction.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(instruction, peeringFinalizer) {
			logger.Info("Reservation instruction is being deleted, executing teardown",
				"targetCluster", instruction.Spec.TargetClusterID)
			
			// Execute liqoctl unpeer
			if instruction.Status.Delivered {
				if err := r.executeLiqoUnpeer(ctx, instruction.Spec.TargetClusterID, instruction.Spec.PeeringKubeconfig); err != nil {
					logger.Error(err, "Failed to execute liqoctl unpeer during teardown", "targetCluster", instruction.Spec.TargetClusterID)
					// Requeue to retry teardown
					return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
				}
			}

			controllerutil.RemoveFinalizer(instruction, peeringFinalizer)
			if err := r.Update(ctx, instruction); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is present
	if !controllerutil.ContainsFinalizer(instruction, peeringFinalizer) {
		controllerutil.AddFinalizer(instruction, peeringFinalizer)
		if err := r.Update(ctx, instruction); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Check if expired
	if instruction.Spec.ExpiresAt != nil && instruction.Spec.ExpiresAt.Time.Before(time.Now()) {
		logger.Info("reservation instruction expired",
			"instruction", instruction.Name,
			"reservation", instruction.Spec.ReservationName,
			"targetCluster", instruction.Spec.TargetClusterID,
			"expiresAt", instruction.Spec.ExpiresAt.Time)

		// Delete the instruction to trigger the finalizer and teardown
		logger.Info("Deleting expired reservation instruction to trigger teardown")
		if err := r.Delete(ctx, instruction); err != nil {
			logger.Error(err, "failed to delete expired instruction")
			return ctrl.Result{}, err
		}

		// No need to requeue - it's expired and being deleted
		return ctrl.Result{}, nil
	}

	// If already delivered, just requeue to check expiration later
	if instruction.Status.Delivered {
		// Requeue before expiration to mark it as expired promptly
		if instruction.Spec.ExpiresAt != nil {
			timeUntilExpiry := time.Until(instruction.Spec.ExpiresAt.Time)
			if timeUntilExpiry > 0 {
				return ctrl.Result{RequeueAfter: timeUntilExpiry}, nil
			}
		}
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Process the instruction
	logger.Info(fmt.Sprintf("Reservation Instruction Received\n"+
		"  Reservation: %s\n"+
		"  Target Cluster: %s\n"+
		"  Resources: cpu=%s, memory=%s\n"+
		"  Message: %s",
		instruction.Spec.ReservationName,
		instruction.Spec.TargetClusterID,
		instruction.Spec.RequestedCPU,
		instruction.Spec.RequestedMemory,
		instruction.Spec.Message))

	// Check if we have the peering credential.
	if instruction.Spec.PeeringKubeconfig == "" {

		//check if BrokerCommunicator is configured - if not,after 30 seconds the reconciler will requeue and check again.
		if r.BrokerCommunicator == nil {
			logger.Info("Cannot fetch PeeringKubeconfig because BrokerCommunicator is not configured")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		//the broker is configured, but the peering credentials?
		logger.Info("Peering credential not yet available, polling broker...", "reservation", instruction.Spec.ReservationName)
		rsv, err := r.BrokerCommunicator.GetReservation(ctx, instruction.Spec.ReservationName)
		
		//if the fetching fails, we log the error and requeue after 10 seconds to retry fetching the reservation (and its peering credential)
		if err != nil {
			logger.Error(err, "Failed to fetch reservation from broker")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		//check if the provider has uploaded the peering credential in the reservation status. If not, we log and requeue after 10 seconds to check again.
		if rsv.Status.PeeringKubeconfig == "" {
			logger.Info("Provider has not yet uploaded the peering credential, waiting...")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		// Credential found! Save to instruction spec.
		instruction.Spec.PeeringKubeconfig = rsv.Status.PeeringKubeconfig

		//after the update, if there was an error, we log it and return it to requeue and retry the update
		if err := r.Update(ctx, instruction); err != nil {
			logger.Error(err, "Failed to update instruction with peering kubeconfig")
			return ctrl.Result{}, err
		}

		// Requeue to let the updated resource trigger the reconciler again
		return ctrl.Result{}, nil
	}

	// Trigger Liqo peering using the credential
	logger.Info("Initiating Liqo peering with target cluster using downloaded credential",
		"targetCluster", instruction.Spec.TargetClusterID)

	if err := r.executeLiqoPeering(ctx, instruction.Spec.TargetClusterID, instruction.Spec.PeeringKubeconfig); err != nil {
		logger.Error(err, "Liqo peering failed, will retry",
			"targetCluster", instruction.Spec.TargetClusterID)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	logger.Info("Liqo peering completed successfully",
		"localCluster", r.ClusterID,
		"remoteCluster", instruction.Spec.TargetClusterID)

	// Mark as delivered
	instruction.Status.Delivered = true
	instruction.Status.LastUpdateTime = metav1.Now()

	if err := r.Status().Update(ctx, instruction); err != nil {
		logger.Error(err, "failed to mark reservation instruction as delivered")
		return ctrl.Result{}, err
	}

	// Requeue to check for expiration
	if instruction.Spec.ExpiresAt != nil {
		timeUntilExpiry := time.Until(instruction.Spec.ExpiresAt.Time)
		if timeUntilExpiry > 0 {
			logger.Info("reservation instruction delivered, will requeue to check expiration",
				"timeUntilExpiry", timeUntilExpiry)
			return ctrl.Result{RequeueAfter: timeUntilExpiry}, nil
		}
	}

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}


//GO: questo modo di dichiarare funzioni significa che la funzione executeLiqoPeering è un metodo del tipo ReservationInstructionReconciler, e può essere chiamata su qualsiasi istanza di questo tipo.
// executeLiqoPeering runs liqoctl peer to establish Liqo peering with the target cluster.
func (r *ReservationInstructionReconciler) executeLiqoPeering(ctx context.Context, targetClusterID string, remoteKubeconfigContent string) error {
	localKubeconfig := filepath.Join(r.KubeconfigsDir, r.ClusterID+".kubeconfig")

	// Write remote kubeconfig to a temporary file
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("peering-%s-*.kubeconfig", targetClusterID))
	if err != nil {
		return fmt.Errorf("failed to create temporary peering kubeconfig file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(remoteKubeconfigContent); err != nil {
		return fmt.Errorf("failed to write into temp peering file: %w", err)
	}
	tmpFile.Close()
	remoteKubeconfig := tmpFile.Name()

	// Verify local kubeconfig exists
	if _, err := os.Stat(localKubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("local kubeconfig not found: %s", localKubeconfig)
	}

	// Check that liqoctl is available
	if _, err := exec.LookPath("liqoctl"); err != nil {
		return fmt.Errorf("liqoctl not found in PATH: %w", err)
	}

	// Run liqoctl peer with a 5-minute timeout
	peerCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	//liqoctl peer --kubeconfig <local> --remote-kubeconfig <remote> --gw-server-service-type NodePort
	cmd := exec.CommandContext(peerCtx, "liqoctl", "peer",
		"--kubeconfig", localKubeconfig,
		"--remote-kubeconfig", remoteKubeconfig,
		"--gw-server-service-type", "NodePort",
	)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("liqoctl peer failed: %w", err)
	}

	return nil
}

// executeLiqoUnpeer runs liqoctl unpeer to tear down the connection.
func (r *ReservationInstructionReconciler) executeLiqoUnpeer(ctx context.Context, targetClusterID string, remoteKubeconfigContent string) error {
	localKubeconfig := filepath.Join(r.KubeconfigsDir, r.ClusterID+".kubeconfig")

	// Verify local kubeconfig exists
	if _, err := os.Stat(localKubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("local kubeconfig not found: %s", localKubeconfig)
	}

	// Write remote kubeconfig to a temporary file
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("unpeering-%s-*.kubeconfig", targetClusterID))
	if err != nil {
		return fmt.Errorf("failed to create temporary unpeering kubeconfig file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(remoteKubeconfigContent); err != nil {
		return fmt.Errorf("failed to write into temp unpeering file: %w", err)
	}
	tmpFile.Close()
	remoteKubeconfig := tmpFile.Name()

	// Check that liqoctl is available
	if _, err := exec.LookPath("liqoctl"); err != nil {
		return fmt.Errorf("liqoctl not found in PATH: %w", err)
	}

	// Run liqoctl unpeer with a 2-minute timeout
	unpeerCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	logger := log.FromContext(ctx).WithName("liqo-unpeer")
	logger.Info("Executing liqoctl unpeer", "targetCluster", targetClusterID, "kubeconfig", localKubeconfig)

	cmd := exec.CommandContext(unpeerCtx, "liqoctl", "unpeer",
		"--kubeconfig", localKubeconfig,
		"--remote-kubeconfig", remoteKubeconfig,
	)

	// Capture output to see what's wrong
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("liqoctl unpeer failed: %w (output: %s)", err, string(output))
	}

	logger.Info("liqoctl unpeer succeeded", "output", string(output))
	return nil
}

func (r *ReservationInstructionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rearv1alpha1.ReservationInstruction{}).
		Named("reservationinstruction").
		Complete(r)
}
