package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-broker/internal/api/middleware"
	"github.com/mehdiazizian/liqo-resource-broker/internal/transport/dto"
	k8sretry "k8s.io/client-go/util/retry"
)

// PostAdvertisement handles POST /api/v1/advertisements
// CRITICAL: Preserves Reserved field from existing advertisement
func (h *Handler) PostAdvertisement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("advertisement-handler")

	// Decode incoming advertisement
	var incomingAdv dto.AdvertisementDTO
	if err := json.NewDecoder(r.Body).Decode(&incomingAdv); err != nil {
		logger.Error(err, "Failed to decode request body")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate cluster ID matches certificate
	certClusterID, _ := middleware.GetClusterID(ctx)
	if incomingAdv.ClusterID != certClusterID {
		logger.Error(nil, "Cluster ID mismatch",
			"advertised", incomingAdv.ClusterID,
			"certificate", certClusterID)
		http.Error(w, "Cluster ID does not match certificate", http.StatusForbidden)
		return
	}

	// CRITICAL: Fetch existing advertisement to preserve Reserved field
	existing := &brokerv1alpha1.ClusterAdvertisement{}
	advName := incomingAdv.ClusterID + "-adv"
	err := h.k8sClient.Get(ctx,
		types.NamespacedName{Name: advName, Namespace: h.namespace},
		existing)

	// Convert DTO to k8s ClusterAdvertisement
	clusterAdv, err2 := dto.ToClusterAdvertisement(&incomingAdv, h.namespace)
	if err2 != nil {
		logger.Error(err2, "Failed to convert advertisement")
		http.Error(w, "Failed to process advertisement", http.StatusInternalServerError)
		return
	}

	if err == nil {
		// Advertisement exists - CRITICAL: Preserve Reserved field
		importRetry := true
		_ = importRetry // Just to note that we might need to add retry import if not present

		// We will use retry on conflict here
		err = k8sretry.RetryOnConflict(k8sretry.DefaultRetry, func() error {
			// Re-fetch the latest advertisement
			latest := &brokerv1alpha1.ClusterAdvertisement{}
			if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: advName, Namespace: h.namespace}, latest); err != nil {
				return err
			}

			// Keep a copy of the old reserved field
			var reserved *brokerv1alpha1.ResourceQuantities
			if latest.Spec.Resources.Reserved != nil {
				reserved = latest.Spec.Resources.Reserved
			}

			// Keep a copy of the old policy field
			oldPolicy := latest.Spec.Policy

			// Keep a copy of the old carbon intensity data (broker-managed)
			oldCarbonIntensity := latest.Spec.CarbonIntensity
			oldCarbonLastUpdate := latest.Spec.CarbonLastUpdate

			// Convert DTO to k8s ClusterAdvertisement
			clusterAdvRetry, err2 := dto.ToClusterAdvertisement(&incomingAdv, h.namespace)
			if err2 != nil {
				return err2
			}

			// Apply new spec but preserve the old reserved and policy fields
			latest.Spec = clusterAdvRetry.Spec
			latest.Spec.Resources.Reserved = reserved
			latest.Spec.Policy = oldPolicy

			// Preserve broker-managed CarbonIntensity if the agent didn't send its own
			if len(latest.Spec.CarbonIntensity) == 0 && len(oldCarbonIntensity) > 0 {
				latest.Spec.CarbonIntensity = oldCarbonIntensity
				latest.Spec.CarbonLastUpdate = oldCarbonLastUpdate
			}

			// CRITICAL: Subtract Reserved from the Agent's reported Available to prevent
			// over-allocation in the window before the Agent enforces the instruction!
			if reserved != nil {
				latest.Spec.Resources.Available.CPU.Sub(reserved.CPU)
				latest.Spec.Resources.Available.Memory.Sub(reserved.Memory)
				if reserved.GPU != nil && latest.Spec.Resources.Available.GPU != nil {
					latest.Spec.Resources.Available.GPU.Sub(*reserved.GPU)
				}
			}

			return h.k8sClient.Update(ctx, latest)
		})

		if err != nil {
			logger.Error(err, "Failed to update advertisement after retries")
			http.Error(w, fmt.Sprintf("Failed to update advertisement: %v", err),
				http.StatusInternalServerError)
			return
		}

		locStr := "N/A"
		if incomingAdv.Location != nil {
			locStr = fmt.Sprintf("%+v", *incomingAdv.Location)
		}
		logger.Info("Updated advertisement",
			"clusterID", incomingAdv.ClusterID,
			"availableCPU", incomingAdv.Resources.Available.CPU,
			"availableMemory", incomingAdv.Resources.Available.Memory,
			"location", locStr)

	} else if apierrors.IsNotFound(err) {
		// Advertisement doesn't exist - create new
		if err := h.k8sClient.Create(ctx, clusterAdv); err != nil {
			logger.Error(err, "Failed to create advertisement")
			http.Error(w, fmt.Sprintf("Failed to create advertisement: %v", err),
				http.StatusInternalServerError)
			return
		}

		locStr := "N/A"
		if incomingAdv.Location != nil {
			locStr = fmt.Sprintf("%+v", *incomingAdv.Location)
		}
		logger.Info("Created new advertisement",
			"clusterID", incomingAdv.ClusterID,
			"availableCPU", incomingAdv.Resources.Available.CPU,
			"availableMemory", incomingAdv.Resources.Available.Memory,
			"location", locStr)

	} else {
		// Unexpected error
		logger.Error(err, "Failed to check existing advertisement")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Build response with updated advertisement
	responseDTO := dto.FromClusterAdvertisement(clusterAdv)

	// Piggyback provider instructions: include any Reserved-phase reservations
	// where this cluster is the provider. This eliminates the need for polling.
	var providerInstructions []*dto.ReservationDTO
	reservationList := &brokerv1alpha1.ReservationList{}
	if err := h.k8sClient.List(ctx, reservationList); err != nil {
		logger.Error(err, "Failed to list reservations for provider instructions")
	} else {
		for i := range reservationList.Items {
			rsv := &reservationList.Items[i]
			if rsv.Status.Phase == brokerv1alpha1.ReservationPhaseReserved &&
				rsv.Spec.TargetClusterID == incomingAdv.ClusterID {
				providerInstructions = append(providerInstructions, dto.FromReservation(rsv))
			}
		}
		if len(providerInstructions) > 0 {
			logger.Info("Including provider instructions in advertisement response",
				"clusterID", incomingAdv.ClusterID,
				"count", len(providerInstructions))
		}
	}

	response := &dto.AdvertisementResponseDTO{
		Advertisement:        responseDTO,
		ProviderInstructions: providerInstructions,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}

// GetAdvertisement handles GET /api/v1/advertisements/{clusterID}
func (h *Handler) GetAdvertisement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("advertisement-handler")

	clusterID := r.PathValue("clusterID")
	if clusterID == "" {
		http.Error(w, "Missing clusterID parameter", http.StatusBadRequest)
		return
	}

	// Fetch advertisement
	existing := &brokerv1alpha1.ClusterAdvertisement{}
	err := h.k8sClient.Get(ctx,
		types.NamespacedName{Name: clusterID + "-adv", Namespace: h.namespace},
		existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			http.Error(w, "Advertisement not found", http.StatusNotFound)
		} else {
			logger.Error(err, "Failed to fetch advertisement")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	// Convert to DTO (includes Reserved field if present)
	responseDTO := dto.FromClusterAdvertisement(existing)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(responseDTO); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}

// GetInstructions handles GET /api/v1/instructions
// Returns pending provider instructions for the calling cluster.
// Agents poll this endpoint every few seconds for near-instant instruction delivery,
// instead of waiting for the next advertisement cycle (30s).
func (h *Handler) GetInstructions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("instructions-handler")

	// Get cluster ID from mTLS certificate
	clusterID, ok := middleware.GetClusterID(ctx)
	if !ok || clusterID == "" {
		respondWithError(w, http.StatusForbidden, "Could not determine cluster ID from certificate")
		return
	}

	// Find all Reserved-phase reservations where this cluster is the provider
	reservationList := &brokerv1alpha1.ReservationList{}
	if err := h.k8sClient.List(ctx, reservationList); err != nil {
		logger.Error(err, "Failed to list reservations")
		respondWithError(w, http.StatusInternalServerError, "Failed to list reservations")
		return
	}

	var instructions []*dto.ReservationDTO
	for i := range reservationList.Items {
		rsv := &reservationList.Items[i]
		if rsv.Status.Phase == brokerv1alpha1.ReservationPhaseReserved &&
			rsv.Spec.TargetClusterID == clusterID {
			instructions = append(instructions, dto.FromReservation(rsv))
		}
	}

	logger.V(1).Info("Returning provider instructions",
		"clusterID", clusterID,
		"count", len(instructions))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(instructions); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}

// respondWithError sends a JSON error response
func respondWithError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

// readBody safely reads and limits request body
func readBody(r *http.Request) ([]byte, error) {
	const maxBodySize = 1 << 20 // 1MB
	return io.ReadAll(io.LimitReader(r.Body, maxBodySize))
}
