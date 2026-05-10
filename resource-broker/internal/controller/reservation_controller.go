/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-broker/internal/broker"
	"github.com/mehdiazizian/liqo-resource-broker/internal/resource"
)

// ReservationReconciler reconciles a Reservation object
type ReservationReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	DecisionEngine *broker.DecisionEngine
}

var (
	errTargetClusterNotFound = errors.New("target cluster not found")
	errInsufficientResources = errors.New("insufficient resources")
)

// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=reservations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=reservations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=reservations/finalizers,verbs=update
// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=clusteradvertisements,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *ReservationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Reservation", "name", req.Name, "namespace", req.Namespace)

	// Fetch the Reservation instance
	reservation := &brokerv1alpha1.Reservation{}
	err := r.Get(ctx, req.NamespacedName, reservation)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.Info("Reservation not found, may have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Reservation")
		return ctrl.Result{}, err
	}

	// Handle deletion with finalizer
	if reservation.ObjectMeta.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(reservation, brokerv1alpha1.ReservationFinalizer) {
			// Release resources before deletion
			if err := r.releaseResources(ctx, reservation, logger); err != nil {
				logger.Error(err, "Failed to release resources")
				return ctrl.Result{}, err
			}

			// Remove finalizer
			controllerutil.RemoveFinalizer(reservation, brokerv1alpha1.ReservationFinalizer)
			if err := r.Update(ctx, reservation); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(reservation, brokerv1alpha1.ReservationFinalizer) {
		controllerutil.AddFinalizer(reservation, brokerv1alpha1.ReservationFinalizer)
		if err := r.Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := validateReservationSpec(reservation); err != nil {
		logger.Error(err, "invalid reservation spec",
			"reservation", reservation.Name,
			"requesterID", reservation.Spec.RequesterID)
		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseFailed
		reservation.Status.Message = fmt.Sprintf("Invalid reservation specification: %v. "+
			"Please check that requesterID is set and requested resources are positive values.", err)
		reservation.Status.LastUpdateTime = metav1.Now()
		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle different phases
	switch reservation.Status.Phase {
	case "": // New reservation
		return r.handlePendingReservation(ctx, reservation, logger)

	case brokerv1alpha1.ReservationPhasePending:
		return r.handlePendingReservation(ctx, reservation, logger)

	case brokerv1alpha1.ReservationPhaseReserved:
		return r.handleReservedReservation(ctx, reservation, logger)

	case brokerv1alpha1.ReservationPhaseActive:
		return r.handleActiveReservation(ctx, reservation, logger)

	case brokerv1alpha1.ReservationPhaseFailed, brokerv1alpha1.ReservationPhaseReleased:
		// Terminal states - no action needed
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// handlePendingReservation processes a new reservation request
func (r *ReservationReconciler) handlePendingReservation(
	ctx context.Context,
	reservation *brokerv1alpha1.Reservation,
	logger logr.Logger,
) (ctrl.Result, error) {

	// If TargetClusterID is already specified, use it
	if reservation.Spec.TargetClusterID != "" {
		return r.reserveInTargetCluster(ctx, reservation, logger)
	}

	// Otherwise, select best cluster based on decision engine
	bestClusters, err := r.DecisionEngine.RankClusters(
		ctx,
		reservation.Spec.RequesterID,
		reservation.Spec.RequestedResources.CPU,
		reservation.Spec.RequestedResources.Memory,
		reservation.Spec.RequestedResources.GPU,
		reservation.Spec.Priority,
		1,
	)

	if err != nil || len(bestClusters) == 0 {
		logger.Error(err, "failed to select cluster",
			"requesterID", reservation.Spec.RequesterID,
			"requestedCPU", reservation.Spec.RequestedResources.CPU.String(),
			"requestedMemory", reservation.Spec.RequestedResources.Memory.String())
		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseFailed
		reservation.Status.Message = fmt.Sprintf("No suitable cluster found. Requested: %s CPU, %s Memory. "+
			"Ensure clusters are registered, active, and have sufficient available resources.",
			reservation.Spec.RequestedResources.CPU.String(),
			reservation.Spec.RequestedResources.Memory.String())
		reservation.Status.LastUpdateTime = metav1.Now()

		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Update reservation with selected cluster
	bestCluster := bestClusters[0]
	reservation.Spec.TargetClusterID = bestCluster.Spec.ClusterID
	if err := r.Update(ctx, reservation); err != nil {
		logger.Error(err, "Failed to update reservation with target cluster")
		return ctrl.Result{}, err
	}

	return r.reserveInTargetCluster(ctx, reservation, logger)
}

// reserveInTargetCluster attempts to reserve resources in the target cluster
func (r *ReservationReconciler) reserveInTargetCluster(
	ctx context.Context,
	reservation *brokerv1alpha1.Reservation,
	logger logr.Logger,
) (ctrl.Result, error) {

	var lockedCluster *brokerv1alpha1.ClusterAdvertisement

	lockErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		clusterAdv, err := r.findClusterByID(ctx, reservation.Spec.TargetClusterID)
		if err != nil {
			return err
		}

		if !resource.CanReserve(
			clusterAdv,
			reservation.Spec.RequestedResources.CPU,
			reservation.Spec.RequestedResources.Memory,
		) {
			return errInsufficientResources
		}

		if err := resource.AddReservation(
			clusterAdv,
			reservation.Spec.RequestedResources.CPU,
			reservation.Spec.RequestedResources.Memory,
		); err != nil {
			return err
		}

		lockedCluster = clusterAdv
		return r.Update(ctx, clusterAdv)
	})

	switch {
	case errors.Is(lockErr, errTargetClusterNotFound):
		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseFailed
		reservation.Status.Message = fmt.Sprintf("Target cluster '%s' not found. "+
			"The cluster may have been removed or is not registered with the broker.",
			reservation.Spec.TargetClusterID)
		reservation.Status.LastUpdateTime = metav1.Now()
		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	case errors.Is(lockErr, errInsufficientResources):
		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseFailed
		reservation.Status.Message = fmt.Sprintf("Insufficient resources in cluster '%s'. "+
			"Requested: %s CPU, %s Memory. "+
			"The cluster may have insufficient available capacity or resources may have been allocated to other reservations.",
			reservation.Spec.TargetClusterID,
			reservation.Spec.RequestedResources.CPU.String(),
			reservation.Spec.RequestedResources.Memory.String())
		reservation.Status.LastUpdateTime = metav1.Now()
		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	case lockErr != nil:
		logger.Error(lockErr, "failed to lock resources in cluster",
			"targetClusterID", reservation.Spec.TargetClusterID,
			"requestedCPU", reservation.Spec.RequestedResources.CPU.String(),
			"requestedMemory", reservation.Spec.RequestedResources.Memory.String())
		return ctrl.Result{}, lockErr
	}

	// Mark as reserved
	now := metav1.Now()
	reservation.Status.Phase = brokerv1alpha1.ReservationPhaseReserved
	reservation.Status.Message = fmt.Sprintf("Resources locked in cluster %s", reservation.Spec.TargetClusterID)
	reservation.Status.ReservedAt = &now

	// Set expiration if duration is specified
	if reservation.Spec.Duration != nil {
		expiresAt := metav1.NewTime(now.Add(reservation.Spec.Duration.Duration))
		reservation.Status.ExpiresAt = &expiresAt
	}

	reservation.Status.LastUpdateTime = now

	if err := r.Status().Update(ctx, reservation); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info(fmt.Sprintf("✅ Resources Locked Successfully\n"+
		"  └─ Reservation: %s\n"+
		"  └─ Target Cluster: %s\n"+
		"  └─ Locked: cpu=%s, memory=%s\n"+
		"  └─ Remaining Available: cpu=%s, memory=%s",
		reservation.Name,
		reservation.Spec.TargetClusterID,
		reservation.Spec.RequestedResources.CPU.String(),
		reservation.Spec.RequestedResources.Memory.String(),
		lockedCluster.Spec.Resources.Available.CPU.String(),
		lockedCluster.Spec.Resources.Available.Memory.String()))

	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
}

// handleReservedReservation manages a reserved reservation
func (r *ReservationReconciler) handleReservedReservation(
	ctx context.Context,
	reservation *brokerv1alpha1.Reservation,
	logger logr.Logger,
) (ctrl.Result, error) {

	if reservationHasCondition(reservation, brokerv1alpha1.ReservationConditionRequesterActive) {
		logger.Info("Requester confirmed activation, promoting reservation to Active")
		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseActive
		reservation.Status.Message = "Requester confirmed activation"
		reservation.Status.LastUpdateTime = metav1.Now()
		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Check if expired
	if reservation.Status.ExpiresAt != nil && time.Now().After(reservation.Status.ExpiresAt.Time) {
		logger.Info("Reservation expired, releasing resources")

		// Release resources
		if err := r.releaseResources(ctx, reservation, logger); err != nil {
			logger.Error(err, "Failed to release resources on expiration")
			return ctrl.Result{}, err
		}

		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseReleased
		reservation.Status.Message = "Reservation expired and resources released"
		reservation.Status.LastUpdateTime = metav1.Now()

		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Still valid, check again in 1 minute
	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
}

// handleActiveReservation manages an active reservation
func (r *ReservationReconciler) handleActiveReservation(
	ctx context.Context,
	reservation *brokerv1alpha1.Reservation,
	logger logr.Logger,
) (ctrl.Result, error) {

	if reservationHasCondition(reservation, brokerv1alpha1.ReservationConditionRequesterReleased) {
		logger.Info("Requester signaled release, freeing resources")
		if err := r.releaseResources(ctx, reservation, logger); err != nil {
			return ctrl.Result{}, err
		}
		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseReleased
		reservation.Status.Message = "Requester released reservation"
		reservation.Status.LastUpdateTime = metav1.Now()
		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Check if expired
	if reservation.Status.ExpiresAt != nil && time.Now().After(reservation.Status.ExpiresAt.Time) {
		logger.Info("Active reservation expired, releasing resources")

		// Release resources
		if err := r.releaseResources(ctx, reservation, logger); err != nil {
			logger.Error(err, "Failed to release resources on expiration")
			return ctrl.Result{}, err
		}

		reservation.Status.Phase = brokerv1alpha1.ReservationPhaseReleased
		reservation.Status.Message = "Reservation expired and released"
		reservation.Status.LastUpdateTime = metav1.Now()

		if err := r.Status().Update(ctx, reservation); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
}

// releaseResources releases reserved resources when reservation is deleted
func (r *ReservationReconciler) releaseResources(
	ctx context.Context,
	reservation *brokerv1alpha1.Reservation,
	logger logr.Logger,
) error {
	// Only release if reservation was actually reserved
	if reservation.Status.Phase != brokerv1alpha1.ReservationPhaseReserved &&
		reservation.Status.Phase != brokerv1alpha1.ReservationPhaseActive {
		return nil
	}

	// Find the cluster advertisement
	clusterList := &brokerv1alpha1.ClusterAdvertisementList{}
	if err := r.List(ctx, clusterList); err != nil {
		return err
	}

	var targetCluster *brokerv1alpha1.ClusterAdvertisement
	for i := range clusterList.Items {
		if clusterList.Items[i].Spec.ClusterID == reservation.Spec.TargetClusterID {
			targetCluster = &clusterList.Items[i]
			break
		}
	}

	if targetCluster == nil {
		logger.Info("Target cluster not found, skipping resource release")
		return nil
	}

	// Release the resources
	err := resource.RemoveReservation(
		targetCluster,
		reservation.Spec.RequestedResources.CPU,
		reservation.Spec.RequestedResources.Memory,
	)
	if err != nil {
		return fmt.Errorf("failed to remove reservation: %w", err)
	}

	// Update the cluster advertisement
	if err := r.Update(ctx, targetCluster); err != nil {
		return fmt.Errorf("failed to update cluster after releasing resources: %w", err)
	}

	logger.Info("Successfully released resources",
		"cluster", reservation.Spec.TargetClusterID,
		"cpu", reservation.Spec.RequestedResources.CPU.String(),
		"memory", reservation.Spec.RequestedResources.Memory.String())

	return nil
}

func (r *ReservationReconciler) findClusterByID(
	ctx context.Context,
	clusterID string,
) (*brokerv1alpha1.ClusterAdvertisement, error) {
	clusterList := &brokerv1alpha1.ClusterAdvertisementList{}
	if err := r.List(ctx, clusterList); err != nil {
		return nil, err
	}

	for i := range clusterList.Items {
		item := clusterList.Items[i]
		if item.Spec.ClusterID != clusterID {
			continue
		}

		cluster := &brokerv1alpha1.ClusterAdvertisement{}
		if err := r.Get(ctx, types.NamespacedName{Name: item.Name, Namespace: item.Namespace}, cluster); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("%w: %s", errTargetClusterNotFound, clusterID)
			}
			return nil, err
		}

		return cluster, nil
	}

	return nil, fmt.Errorf("%w: %s", errTargetClusterNotFound, clusterID)
}

func reservationHasCondition(reservation *brokerv1alpha1.Reservation, conditionType string) bool {
	for _, cond := range reservation.Status.Conditions {
		if cond.Type == conditionType && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func validateReservationSpec(reservation *brokerv1alpha1.Reservation) error {
	if reservation.Spec.RequesterID == "" {
		return errors.New("spec.requesterID must be set")
	}
	if reservation.Spec.RequestedResources.CPU.Sign() <= 0 {
		return errors.New("requested CPU must be greater than zero")
	}
	if reservation.Spec.RequestedResources.Memory.Sign() <= 0 {
		return errors.New("requested memory must be greater than zero")
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReservationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize decision engine if not set
	if r.DecisionEngine == nil {
		r.DecisionEngine = &broker.DecisionEngine{
			Client: r.Client,
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&brokerv1alpha1.Reservation{}).
		Named("reservation").
		Complete(r)
}
