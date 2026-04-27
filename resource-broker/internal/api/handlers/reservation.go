package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-broker/internal/api/middleware"
	resourceutil "github.com/mehdiazizian/liqo-resource-broker/internal/resource"
	"github.com/mehdiazizian/liqo-resource-broker/internal/transport/dto"
)

// PostReservation handles POST /api/v1/reservations
// This is a synchronous endpoint: the agent sends a reservation request,
// the broker decides and reserves resources, and returns the instruction
// in the response. No polling needed.
func (h *Handler) PostReservation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("reservation-handler")

	// Decode reservation request
	var reqDTO dto.ReservationRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&reqDTO); err != nil {
		logger.Error(err, "Failed to decode request body")
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Get requester ID from mTLS certificate (prevents spoofing)
	requesterID, ok := middleware.GetClusterID(ctx)
	if !ok || requesterID == "" {
		respondWithError(w, http.StatusForbidden, "Could not determine cluster ID from certificate")
		return
	}

	// Validate requested resources
	if reqDTO.RequestedResources.CPU == "" || reqDTO.RequestedResources.Memory == "" {
		respondWithError(w, http.StatusBadRequest, "requestedResources.cpu and requestedResources.memory are required")
		return
	}

	requestedCPU, err := resource.ParseQuantity(reqDTO.RequestedResources.CPU)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid CPU quantity: %v", err))
		return
	}
	requestedMemory, err := resource.ParseQuantity(reqDTO.RequestedResources.Memory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid memory quantity: %v", err))
		return
	}

	if requestedCPU.Sign() <= 0 || requestedMemory.Sign() <= 0 {
		respondWithError(w, http.StatusBadRequest, "Requested CPU and memory must be greater than zero")
		return
	}

	var requestedGPU *resource.Quantity
	if reqDTO.RequestedResources.GPU != "" {
		gpuParsed, err := resource.ParseQuantity(reqDTO.RequestedResources.GPU)
		if err == nil && gpuParsed.Sign() > 0 {
			requestedGPU = &gpuParsed
		}
	}

	// Run decision engine synchronously
	bestCluster, err := h.decisionEngine.SelectBestCluster(
		ctx, requesterID, requestedCPU, requestedMemory, requestedGPU, reqDTO.Priority,
	)
	if err != nil {
		logger.Error(err, "No suitable cluster found",
			"requesterID", requesterID,
			"requestedCPU", requestedCPU.String(),
			"requestedMemory", requestedMemory.String())
		respondWithError(w, http.StatusConflict,
			fmt.Sprintf("No suitable cluster found: %v", err))
		return
	}

	// Generate reservation name
	reservationName := fmt.Sprintf("rsv-%s-%d", requesterID, time.Now().UnixMilli())

	// Create Reservation CRD for record-keeping and lifecycle management
	reservation := &brokerv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reservationName,
			Namespace: h.namespace,
		},
		Spec: brokerv1alpha1.ReservationSpec{
			RequesterID:     requesterID,
			TargetClusterID: bestCluster.Spec.ClusterID,
			RequestedResources: brokerv1alpha1.RequestedResourceQuantities{
				CPU:    requestedCPU,
				Memory: requestedMemory,
			},
			Priority: reqDTO.Priority,
		},
	}

	// Parse duration if provided
	if reqDTO.Duration != "" {
		d, err := time.ParseDuration(reqDTO.Duration)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Invalid duration: %v", err))
			return
		}
		reservation.Spec.Duration = &metav1.Duration{Duration: d}
	}

	// Create the reservation CRD
	if err := h.k8sClient.Create(ctx, reservation); err != nil {
		logger.Error(err, "Failed to create reservation CRD")
		respondWithError(w, http.StatusInternalServerError, "Failed to create reservation")
		return
	}

	// Add finalizer
	controllerutil.AddFinalizer(reservation, brokerv1alpha1.ReservationFinalizer)
	if err := h.k8sClient.Update(ctx, reservation); err != nil {
		logger.Error(err, "Failed to add finalizer to reservation")
	}

	// Lock resources in the target cluster using retry for conflict resolution
	lockErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		clusterAdv := &brokerv1alpha1.ClusterAdvertisement{}
		if err := h.k8sClient.Get(ctx,
			types.NamespacedName{Name: bestCluster.Name, Namespace: bestCluster.Namespace},
			clusterAdv); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("target cluster not found: %s", bestCluster.Spec.ClusterID)
			}
			return err
		}

		if !resourceutil.CanReserve(clusterAdv, requestedCPU, requestedMemory) {
			return fmt.Errorf("insufficient resources in cluster %s", bestCluster.Spec.ClusterID)
		}

		if err := resourceutil.AddReservation(clusterAdv, requestedCPU, requestedMemory); err != nil {
			return err
		}

		return h.k8sClient.Update(ctx, clusterAdv)
	})

	if lockErr != nil {
		logger.Error(lockErr, "Failed to lock resources")
		// Mark reservation as failed
		_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &brokerv1alpha1.Reservation{}
			if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: reservation.Name, Namespace: reservation.Namespace}, latest); err != nil {
				return err
			}
			latest.Status.Phase = brokerv1alpha1.ReservationPhaseFailed
			latest.Status.Message = fmt.Sprintf("Failed to lock resources: %v", lockErr)
			latest.Status.LastUpdateTime = metav1.Now()
			return h.k8sClient.Status().Update(ctx, latest)
		})

		respondWithError(w, http.StatusConflict, fmt.Sprintf("Failed to reserve resources: %v", lockErr))
		return
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &brokerv1alpha1.Reservation{}
		if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: reservation.Name, Namespace: reservation.Namespace}, latest); err != nil {
			return err
		}

		latest.Status.Phase = brokerv1alpha1.ReservationPhaseReserved
		latest.Status.Message = fmt.Sprintf("Resources locked in cluster %s", bestCluster.Spec.ClusterID)
		now := metav1.Now()
		latest.Status.ReservedAt = &now
		latest.Status.LastUpdateTime = now

		if latest.Spec.Duration != nil {
			expiresAt := metav1.NewTime(now.Add(latest.Spec.Duration.Duration))
			latest.Status.ExpiresAt = &expiresAt
		}

		err := h.k8sClient.Status().Update(ctx, latest)
		if err == nil {
			// Update local object for the JSON response
			reservation.Status = latest.Status
		}
		return err
	}); err != nil {
		logger.Error(err, "Failed to update reservation status")
	}

	logger.Info("Reservation created synchronously",
		"reservation", reservationName,
		"requester", requesterID,
		"targetCluster", bestCluster.Spec.ClusterID,
		"cpu", requestedCPU.String(),
		"memory", requestedMemory.String())

	// Return the instruction in the response
	response := dto.FromReservation(reservation)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}

// PostPeeringKubeconfig handles POST /api/v1/reservations/{id}/kubeconfig
// The provider agent uploads the peering kubeconfig for the requester cluster.
func (h *Handler) PostPeeringKubeconfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("reservation-handler")

	reservationID := r.PathValue("id")
	if reservationID == "" {
		respondWithError(w, http.StatusBadRequest, "Missing reservation id parameter")
		return
	}

	// Get provider ID from mTLS certificate
	providerID, ok := middleware.GetClusterID(ctx)
	if !ok || providerID == "" {
		respondWithError(w, http.StatusForbidden, "Could not determine cluster ID from certificate")
		return
	}

	var payload struct {
		Kubeconfig string `json:"kubeconfig"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		logger.Error(err, "Failed to decode request body")
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if payload.Kubeconfig == "" {
		respondWithError(w, http.StatusBadRequest, "Kubeconfig cannot be empty")
		return
	}

	// Fetch the reservation
	reservation := &brokerv1alpha1.Reservation{}
	if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: reservationID, Namespace: h.namespace}, reservation); err != nil {
		if apierrors.IsNotFound(err) {
			respondWithError(w, http.StatusNotFound, "Reservation not found")
		} else {
			logger.Error(err, "Failed to fetch reservation")
			respondWithError(w, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	// Verify that the calling cluster is indeed the target provider
	if reservation.Spec.TargetClusterID != providerID {
		logger.Error(nil, "Unauthorized kubeconfig upload",
			"expectedProvider", reservation.Spec.TargetClusterID,
			"actualProvider", providerID)
		respondWithError(w, http.StatusForbidden, "Only the assigned provider can upload the peering kubeconfig")
		return
	}

	// Update reservation status with the credential
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &brokerv1alpha1.Reservation{}
		if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: reservation.Name, Namespace: reservation.Namespace}, latest); err != nil {
			return err
		}
		latest.Status.PeeringKubeconfig = payload.Kubeconfig
		latest.Status.LastUpdateTime = metav1.Now()
		return h.k8sClient.Status().Update(ctx, latest)
	}); err != nil {
		logger.Error(err, "Failed to update reservation status with peering kubeconfig")
		respondWithError(w, http.StatusInternalServerError, "Failed to save peering kubeconfig")
		return
	}

	logger.Info("Successfully saved peering kubeconfig", "reservationID", reservationID, "providerID", providerID)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

// GetReservation handles GET /api/v1/reservations/{id}
func (h *Handler) GetReservation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("reservation-handler")

	reservationID := r.PathValue("id")
	if reservationID == "" {
		respondWithError(w, http.StatusBadRequest, "Missing reservation id parameter")
		return
	}

	// Fetch reservation
	reservation := &brokerv1alpha1.Reservation{}
	if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: reservationID, Namespace: h.namespace}, reservation); err != nil {
		if apierrors.IsNotFound(err) {
			respondWithError(w, http.StatusNotFound, "Reservation not found")
		} else {
			logger.Error(err, "Failed to fetch reservation")
			respondWithError(w, http.StatusInternalServerError, "Internal server error")
		}
		return
	}

	// Convert to DTO
	responseDTO := dto.FromReservation(reservation)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(responseDTO); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}
