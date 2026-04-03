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
	"fmt"
	"math/rand"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-agent/internal/metrics"
	"github.com/mehdiazizian/liqo-resource-agent/internal/publisher" // ← Add this line
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport/dto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AdvertisementReconciler reconciles an Advertisement object
type AdvertisementReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	MetricsCollector     *metrics.Collector
	BrokerClient         *publisher.BrokerClient      // Legacy Kubernetes transport
	BrokerCommunicator   transport.BrokerCommunicator // New transport abstraction
	TargetKey            types.NamespacedName
	RequeueInterval      time.Duration // Configurable requeue interval
	InstructionNamespace string        // Namespace for ProviderInstruction CRDs
	Renewable            bool          // Green energy flag
	EnergyCost           float64       // Normalized cost (0-1)
}

// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=advertisements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=advertisements/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rear.fluidos.eu,resources=advertisements/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *AdvertisementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling advertisement",
		"name", req.Name,
		"namespace", req.Namespace)

	// Fetch the Advertisement instance
	advertisement := &rearv1alpha1.Advertisement{}
	err := r.Get(ctx, req.NamespacedName, advertisement)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.Info("advertisement not found, may have been deleted",
				"name", req.Name,
				"namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get advertisement",
			"name", req.Name,
			"namespace", req.Namespace)
		return ctrl.Result{}, err
	}

	// Collect current cluster metrics
	resourceData, err := r.MetricsCollector.CollectClusterResources(ctx)
	if err != nil {
		logger.Error(err, "failed to collect cluster resources")
		return r.updateStatus(ctx, advertisement, "Error", false, fmt.Sprintf("Failed to collect metrics: %v", err))
	}

	// Get cluster ID
	clusterID, err := r.MetricsCollector.GetClusterID(ctx)
	if err != nil {
		logger.Error(err, "failed to get cluster ID")
		return r.updateStatus(ctx, advertisement, "Error", false, fmt.Sprintf("Failed to get cluster ID: %v", err))
	}

	// Update the Advertisement spec with collected data
	advertisement.Spec.ClusterID = clusterID
	advertisement.Spec.Resources = *resourceData
	advertisement.Spec.Timestamp = metav1.Now()

	// Update cost information
	advertisement.Spec.Cost = &rearv1alpha1.CostInfo{
		Renewable:  r.Renewable,
		EnergyCost: r.EnergyCost,
	}

	// Update the Advertisement resource
	if err := r.Update(ctx, advertisement); err != nil {
		logger.Error(err, "failed to update advertisement spec",
			"name", advertisement.Name,
			"namespace", advertisement.Namespace)
		return ctrl.Result{}, err
	}

	// Log with better readability - single message with newlines
	logger.Info(fmt.Sprintf("📊 Advertisement updated\n"+
		"  └─ Cluster: %s\n"+
		"  └─ CPU: allocatable=%s, allocated=%s, available=%s\n"+
		"  └─ Memory: allocatable=%s, allocated=%s, available=%s",
		clusterID,
		resourceData.Allocatable.CPU.String(),
		resourceData.Allocated.CPU.String(),
		resourceData.Available.CPU.String(),
		resourceData.Allocatable.Memory.String(),
		resourceData.Allocated.Memory.String(),
		resourceData.Available.Memory.String()))

	// Publish to broker and update status accordingly
	publishErr := r.publishToBroker(ctx, advertisement, clusterID)
	if publishErr != nil {
		return r.updateStatus(ctx, advertisement, "Active", false,
			fmt.Sprintf("Metrics collected but broker unreachable: %v", publishErr))
	}

	return r.updateStatus(ctx, advertisement, "Active", true, "Advertisement updated and published successfully")
}

// publishToBroker publishes the advertisement to the broker via the configured transport.
// Also processes any provider instructions piggybacked in the broker response.
// Returns nil if no transport is configured (local-only mode).
func (r *AdvertisementReconciler) publishToBroker(ctx context.Context, advertisement *rearv1alpha1.Advertisement, clusterID string) error {
	logger := log.FromContext(ctx)

	if r.BrokerCommunicator != nil {
		advDTO := dto.ToAdvertisementDTO(advertisement)
		providerInstructions, err := r.BrokerCommunicator.PublishAdvertisement(ctx, advDTO)
		if err != nil {
			logger.Error(err, fmt.Sprintf("Failed to publish to broker (will retry)\n  Cluster: %s", clusterID))
			return err
		}
		logger.Info(fmt.Sprintf("Published to broker successfully (via transport abstraction)\n  Cluster: %s", clusterID))

		// Process piggybacked provider instructions
		r.processProviderInstructions(ctx, providerInstructions, clusterID)
		return nil
	}

	if r.BrokerClient != nil && r.BrokerClient.Enabled {
		if err := r.BrokerClient.PublishAdvertisement(ctx, advertisement); err != nil {
			logger.Error(err, fmt.Sprintf("Failed to publish to broker (will retry)\n  Cluster: %s", clusterID))
			return err
		}
		logger.Info(fmt.Sprintf("Published to broker successfully (via legacy client)\n  Cluster: %s", clusterID))
	}

	return nil
}

// processProviderInstructions creates ProviderInstruction CRDs from broker response.
// These are piggybacked on the advertisement POST response, eliminating the need
// for a separate polling loop.
func (r *AdvertisementReconciler) processProviderInstructions(ctx context.Context, instructions []*dto.ReservationDTO, clusterID string) {
	logger := log.FromContext(ctx)

	for _, rsv := range instructions {
		if rsv.Status.Phase != "Reserved" {
			continue
		}

		instructionName := fmt.Sprintf("%s-provider", rsv.ID)
		ns := r.InstructionNamespace
		if ns == "" {
			ns = r.TargetKey.Namespace
		}

		// Check if instruction already exists
		existing := &rearv1alpha1.ProviderInstruction{}
		err := r.Get(ctx, types.NamespacedName{Name: instructionName, Namespace: ns}, existing)
		if err == nil {
			continue // Already exists
		}

		var expiresAt *metav1.Time
		if rsv.Status.ExpiresAt != nil {
			expiresAt = &metav1.Time{Time: *rsv.Status.ExpiresAt}
		}

		instruction := &rearv1alpha1.ProviderInstruction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instructionName,
				Namespace: ns,
			},
			Spec: rearv1alpha1.ProviderInstructionSpec{
				ReservationName:    rsv.ID,
				RequesterClusterID: rsv.RequesterID,
				RequestedCPU:       rsv.RequestedResources.CPU,
				RequestedMemory:    rsv.RequestedResources.Memory,
				Message: fmt.Sprintf("Hold %s CPU / %s Memory for requester %s",
					rsv.RequestedResources.CPU,
					rsv.RequestedResources.Memory,
					rsv.RequesterID),
				ExpiresAt: expiresAt,
			},
		}

		if err := r.Create(ctx, instruction); err != nil {
			logger.Error(err, "Failed to create provider instruction",
				"reservation", rsv.ID,
				"requester", rsv.RequesterID)
		} else {
			logger.Info("Created provider instruction from advertisement response",
				"reservation", rsv.ID,
				"requester", rsv.RequesterID,
				"cpu", rsv.RequestedResources.CPU,
				"memory", rsv.RequestedResources.Memory)
		}
	}
}

// updateStatus updates the Advertisement status
func (r *AdvertisementReconciler) updateStatus(
	ctx context.Context,
	advertisement *rearv1alpha1.Advertisement,
	phase string,
	published bool,
	message string,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	advertisement.Status.Phase = phase
	advertisement.Status.Published = published
	advertisement.Status.Message = message
	advertisement.Status.LastUpdateTime = metav1.Now()

	if err := r.Status().Update(ctx, advertisement); err != nil {
		logger.Error(err, "failed to update advertisement status",
			"name", advertisement.Name,
			"namespace", advertisement.Namespace,
			"phase", phase)
		return ctrl.Result{}, err
	}

	// Schedule next advertisement with jitter to avoid thundering herd
	requeueInterval := r.RequeueInterval
	if requeueInterval == 0 {
		requeueInterval = 1 * time.Minute // Default: 1 minute
	}

	// Add up to 10% jitter to spread agent advertisements over time
	jitter := time.Duration(rand.Int63n(int64(requeueInterval) / 10))
	waitDuration := requeueInterval + jitter

	logger.Info("scheduled next advertisement update",
		"waitDuration", waitDuration.Round(time.Second))

	return ctrl.Result{RequeueAfter: waitDuration}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *AdvertisementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize metrics collector if not set
	if r.MetricsCollector == nil {
		r.MetricsCollector = &metrics.Collector{}
	}
	r.MetricsCollector.Client = r.Client

	return ctrl.NewControllerManagedBy(mgr).
		For(&rearv1alpha1.Advertisement{}).
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.findAdvertisementsForNode),
		).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.findAdvertisementsForPod),
		).
		Named("advertisement").
		Complete(r)
}

// findAdvertisementsForNode triggers reconciliation when nodes change
func (r *AdvertisementReconciler) findAdvertisementsForNode(ctx context.Context, node client.Object) []reconcile.Request {
	if r.TargetKey.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: r.TargetKey}}
}

// findAdvertisementsForPod triggers reconciliation when pods change
func (r *AdvertisementReconciler) findAdvertisementsForPod(ctx context.Context, pod client.Object) []reconcile.Request {
	if r.TargetKey.Name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: r.TargetKey}}
}
