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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	k8sretry "k8s.io/client-go/util/retry"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-broker/internal/broker"
	"github.com/mehdiazizian/liqo-resource-broker/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterAdvertisementReconciler reconciles a ClusterAdvertisement object
type ClusterAdvertisementReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	DecisionEngine     *broker.DecisionEngine
	StalenessThreshold time.Duration // Configurable staleness threshold
}

// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=clusteradvertisements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=clusteradvertisements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=broker.fluidos.eu,resources=clusteradvertisements/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop
func (r *ClusterAdvertisementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ClusterAdvertisement", "name", req.Name, "namespace", req.Namespace)

	// Fetch the ClusterAdvertisement instance
	clusterAdv := &brokerv1alpha1.ClusterAdvertisement{}
	err := r.Get(ctx, req.NamespacedName, clusterAdv)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.Info("ClusterAdvertisement not found, may have been deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ClusterAdvertisement")
		return ctrl.Result{}, err
	}

	// First retry loop: update the Spec if needed
	err = k8sretry.RetryOnConflict(k8sretry.DefaultRetry, func() error {
		latest := &brokerv1alpha1.ClusterAdvertisement{}
		if err := r.Get(ctx, req.NamespacedName, latest); err != nil {
			return err
		}

		// Recalculate Available using single source of truth
		resource.UpdateAvailableResources(&latest.Spec.Resources)

		// Update the spec with recalculated available
		return r.Update(ctx, latest)
	})
	if err != nil {
		logger.Error(err, "Failed to update available resources")
		// Continue anyway to update status
	}

	// Second retry loop: update the Status
	err = k8sretry.RetryOnConflict(k8sretry.DefaultRetry, func() error {
		latest := &brokerv1alpha1.ClusterAdvertisement{}
		if err := r.Get(ctx, req.NamespacedName, latest); err != nil {
			return err
		}

		// Check if advertisement is stale
		age := time.Since(latest.Spec.Timestamp.Time)
		stalenessThreshold := r.StalenessThreshold
		if stalenessThreshold == 0 {
			stalenessThreshold = 2 * time.Minute // Default: 2 minutes
		}
		isStale := age > stalenessThreshold

		// Update status
		latest.Status.Active = !isStale
		if isStale {
			latest.Status.Phase = "Stale"
			latest.Status.Message = "Advertisement has not been updated recently"
		} else {
			latest.Status.Phase = "Active"
			latest.Status.Message = "Cluster is active and available"
		}

		// Calculate and update score
		if err := r.DecisionEngine.UpdateClusterScore(ctx, latest); err != nil {
			logger.Error(err, "Failed to update cluster score")
		}

		// Update conditions
		r.updateConditions(latest, isStale)
		latest.Status.LastUpdateTime = metav1.Now()

		return r.Status().Update(ctx, latest)
	})

	if err != nil {
		logger.Error(err, "Failed to update ClusterAdvertisement status")
		return ctrl.Result{}, err
	}

	logger.Info("Updated ClusterAdvertisement",
		"clusterID", clusterAdv.Spec.ClusterID,
		"availableCPU", clusterAdv.Spec.Resources.Available.CPU.String(),
		"availableMemory", clusterAdv.Spec.Resources.Available.Memory.String(),
		//We used a function to get the cost information. This is because the cost information is optional in the CRD
		//Otherwise, without the function, we would get a nil pointer dereference error (panic)
		"energyCost", func() float64 {
			if clusterAdv.Spec.Cost != nil {
				return clusterAdv.Spec.Cost.EnergyCost
			}
			return 0.0
		}(),
		"renewable", func() bool {
			if clusterAdv.Spec.Cost != nil {
				return clusterAdv.Spec.Cost.Renewable
			}
			return false
		}(),
		"score", clusterAdv.Status.Score,
		"active", clusterAdv.Status.Active)

	// Requeue after 30 seconds to check for staleness
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// updateConditions updates the status conditions for the cluster advertisement
func (r *ClusterAdvertisementReconciler) updateConditions(clusterAdv *brokerv1alpha1.ClusterAdvertisement, isStale bool) {
	now := metav1.Now()

	// Ready condition
	readyStatus := metav1.ConditionTrue
	readyReason := "ClusterActive"
	readyMessage := "Cluster is active and ready to accept reservations"
	if isStale {
		readyStatus = metav1.ConditionFalse
		readyReason = "ClusterStale"
		readyMessage = "Cluster advertisement is stale and not accepting new reservations"
	}
	meta.SetStatusCondition(&clusterAdv.Status.Conditions, metav1.Condition{
		Type:               brokerv1alpha1.ClusterAdvertisementConditionReady,
		Status:             readyStatus,
		Reason:             readyReason,
		Message:            readyMessage,
		LastTransitionTime: now,
	})

	// Stale condition
	staleStatus := metav1.ConditionFalse
	if isStale {
		staleStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&clusterAdv.Status.Conditions, metav1.Condition{
		Type:               brokerv1alpha1.ClusterAdvertisementConditionStale,
		Status:             staleStatus,
		Reason:             "AdvertisementAge",
		Message:            "Advertisement freshness check",
		LastTransitionTime: now,
	})

	// Overcommitted condition - check if reserved > available
	isOvercommitted := false
	if clusterAdv.Spec.Resources.Reserved != nil {
		if clusterAdv.Spec.Resources.Reserved.CPU.Cmp(clusterAdv.Spec.Resources.Available.CPU) > 0 ||
			clusterAdv.Spec.Resources.Reserved.Memory.Cmp(clusterAdv.Spec.Resources.Available.Memory) > 0 {
			isOvercommitted = true
		}
	}

	overcommittedStatus := metav1.ConditionFalse
	overcommittedMessage := "Resources are within acceptable limits"
	if isOvercommitted {
		overcommittedStatus = metav1.ConditionTrue
		overcommittedMessage = "Reserved resources exceed available capacity"
	}
	meta.SetStatusCondition(&clusterAdv.Status.Conditions, metav1.Condition{
		Type:               brokerv1alpha1.ClusterAdvertisementConditionOvercommitted,
		Status:             overcommittedStatus,
		Reason:             "ResourceCheck",
		Message:            overcommittedMessage,
		LastTransitionTime: now,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterAdvertisementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize decision engine if not set
	if r.DecisionEngine == nil {
		r.DecisionEngine = &broker.DecisionEngine{
			Client: r.Client,
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&brokerv1alpha1.ClusterAdvertisement{}).
		Named("clusteradvertisement").
		Complete(r)
}
