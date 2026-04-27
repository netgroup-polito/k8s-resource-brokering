package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport/dto"
)

// ResourceRequestReconciler reconciles a ResourceRequest object.
// When a user creates a ResourceRequest, this controller sends a synchronous
// reservation request to the broker and creates a ReservationInstruction
// from the response. No polling needed.
type ResourceRequestReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	BrokerCommunicator   transport.BrokerCommunicator
	InstructionNamespace string
}

// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=resourcerequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=resourcerequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=reservationinstructions,verbs=get;list;watch;create;update;patch

func (r *ResourceRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("resourcerequest-controller")

	// Fetch the ResourceRequest
	resourceReq := &rearv1alpha1.ResourceRequest{}
	if err := r.Get(ctx, req.NamespacedName, resourceReq); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Skip if already processed (Reserved or Failed)
	if resourceReq.Status.Phase == "Reserved" {
		// Periodically re-evaluate (every hour)
		if r.BrokerCommunicator != nil {
			logger.Info("Re-evaluating best provider", "current", resourceReq.Status.TargetClusterID)
			evalReq := &dto.ReservationRequestDTO{
				RequestedResources: dto.ResourceQuantitiesDTO{
					CPU:    resourceReq.Spec.RequestedCPU,
					Memory: resourceReq.Spec.RequestedMemory,
					GPU:    resourceReq.Spec.RequestedGPU,
				},
				Priority: resourceReq.Spec.Priority,
				Duration: resourceReq.Spec.Duration,
			}

			evaluation, err := r.BrokerCommunicator.EvaluateProviders(ctx, evalReq)
			if err != nil {
				logger.Error(err, "Failed to evaluate providers")
			} else if evaluation.TargetClusterID != "" && evaluation.TargetClusterID != resourceReq.Status.TargetClusterID {
				logger.Info("Found better provider!", "old", resourceReq.Status.TargetClusterID, "new", evaluation.TargetClusterID)

				// 1. Delete old instruction to trigger teardown
				ns := r.InstructionNamespace
				if ns == "" {
					ns = resourceReq.Namespace
				}
				oldInstruction := &rearv1alpha1.ReservationInstruction{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceReq.Status.ReservationName,
						Namespace: ns,
					},
				}

				if err := r.Delete(ctx, oldInstruction); err != nil && !apierrors.IsNotFound(err) {
					logger.Error(err, "Failed to delete old instruction")
				}

				// 2. Set phase to Pending so we request a new reservation
				return r.updateStatus(ctx, resourceReq, "Pending", "", "", "Switching to a better provider: "+evaluation.TargetClusterID)
			}
		}
		return ctrl.Result{RequeueAfter: 1 * time.Hour}, nil
	}

	if resourceReq.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}

	// Skip if no broker communicator is configured
	if r.BrokerCommunicator == nil {
		logger.Info("No broker communicator configured, skipping ResourceRequest")
		return r.updateStatus(ctx, resourceReq, "Failed", "", "",
			"No broker communicator configured")
	}

	logger.Info("Processing ResourceRequest",
		"name", resourceReq.Name,
		"cpu", resourceReq.Spec.RequestedCPU,
		"memory", resourceReq.Spec.RequestedMemory)

	// Mark as Pending
	if resourceReq.Status.Phase == "" {
		if _, err := r.updateStatus(ctx, resourceReq, "Pending", "", "",
			"Sending reservation request to broker"); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Send synchronous reservation request to broker
	reservationReq := &dto.ReservationRequestDTO{
		RequestedResources: dto.ResourceQuantitiesDTO{
			CPU:    resourceReq.Spec.RequestedCPU,
			Memory: resourceReq.Spec.RequestedMemory,
			GPU:    resourceReq.Spec.RequestedGPU,
		},
		Priority: resourceReq.Spec.Priority,
		Duration: resourceReq.Spec.Duration,
	}

	reservation, err := r.BrokerCommunicator.RequestReservation(ctx, reservationReq)
	if err != nil {
		logger.Error(err, "Reservation request failed",
			"cpu", resourceReq.Spec.RequestedCPU,
			"memory", resourceReq.Spec.RequestedMemory)
		return r.updateStatus(ctx, resourceReq, "Failed", "", "",
			fmt.Sprintf("Reservation request failed: %v", err))
	}

	// Create ReservationInstruction from the response
	if err := r.createReservationInstruction(ctx, resourceReq, reservation); err != nil {
		logger.Error(err, "Failed to create ReservationInstruction")
		return r.updateStatus(ctx, resourceReq, "Failed", reservation.TargetClusterID, reservation.ID,
			fmt.Sprintf("Reservation succeeded but failed to create local instruction: %v", err))
	}

	logger.Info("ResourceRequest processed successfully",
		"reservation", reservation.ID,
		"targetCluster", reservation.TargetClusterID,
		"cpu", reservation.RequestedResources.CPU,
		"memory", reservation.RequestedResources.Memory)

	return r.updateStatus(ctx, resourceReq, "Reserved", reservation.TargetClusterID, reservation.ID,
		fmt.Sprintf("Resources reserved in cluster %s", reservation.TargetClusterID))
}

func (r *ResourceRequestReconciler) createReservationInstruction(
	ctx context.Context,
	resourceReq *rearv1alpha1.ResourceRequest,
	reservation *dto.ReservationDTO,
) error {
	instructionName := reservation.ID
	ns := r.InstructionNamespace
	if ns == "" {
		ns = resourceReq.Namespace
	}

	// Check if instruction already exists
	existing := &rearv1alpha1.ReservationInstruction{}
	err := r.Get(ctx, types.NamespacedName{Name: instructionName, Namespace: ns}, existing)
	if err == nil {
		return nil // Already exists
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	var expiresAt *metav1.Time
	if reservation.Status.ExpiresAt != nil {
		expiresAt = &metav1.Time{Time: *reservation.Status.ExpiresAt}
	}

	instruction := &rearv1alpha1.ReservationInstruction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instructionName,
			Namespace: ns,
		},
		Spec: rearv1alpha1.ReservationInstructionSpec{
			ReservationName: reservation.ID,
			TargetClusterID: reservation.TargetClusterID,
			RequestedCPU:    reservation.RequestedResources.CPU,
			RequestedMemory: reservation.RequestedResources.Memory,
			RequestedGPU:    reservation.RequestedResources.GPU,
			Message: fmt.Sprintf("Use %s for %s CPU / %s Memory / %s GPU",
				reservation.TargetClusterID,
				reservation.RequestedResources.CPU,
				reservation.RequestedResources.Memory,
				reservation.RequestedResources.GPU),
			ExpiresAt: expiresAt,
		},
	}

	return r.Create(ctx, instruction)
}

func (r *ResourceRequestReconciler) updateStatus(
	ctx context.Context,
	resourceReq *rearv1alpha1.ResourceRequest,
	phase, targetClusterID, reservationName, message string,
) (ctrl.Result, error) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &rearv1alpha1.ResourceRequest{}
		if err := r.Get(ctx, types.NamespacedName{Name: resourceReq.Name, Namespace: resourceReq.Namespace}, latest); err != nil {
			return err
		}

		latest.Status.Phase = phase
		latest.Status.TargetClusterID = targetClusterID
		latest.Status.ReservationName = reservationName
		latest.Status.Message = message
		latest.Status.LastUpdateTime = metav1.Now()

		return r.Status().Update(ctx, latest)
	})

	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ResourceRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rearv1alpha1.ResourceRequest{}).
		Named("resourcerequest").
		Complete(r)
}
