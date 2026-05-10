package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/mehdiazizian/liqo-resource-broker/internal/api/middleware"
	"github.com/mehdiazizian/liqo-resource-broker/internal/transport/dto"
)

// PostEvaluation handles POST /api/v1/evaluations
// Evaluates requested resources and returns the best provider without making a reservation.
func (h *Handler) PostEvaluation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := log.FromContext(ctx).WithName("evaluation-handler")

	// Decode request
	var reqDTO dto.ReservationRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&reqDTO); err != nil {
		logger.Error(err, "Failed to decode request body")
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Get requester ID
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

	// Run decision engine synchronously to get top 10 candidates
	bestClusters, err := h.decisionEngine.RankClusters(
		ctx, requesterID, requestedCPU, requestedMemory, requestedGPU, reqDTO.Priority, 10,
	)
	if err != nil {
		logger.Info("No suitable cluster found for evaluation",
			"requesterID", requesterID,
			"requestedCPU", requestedCPU.String(),
			"requestedMemory", requestedMemory.String())
		respondWithError(w, http.StatusConflict, fmt.Sprintf("No suitable cluster found: %v", err))
		return
	}

	var candidates []string
	for _, cluster := range bestClusters {
		candidates = append(candidates, cluster.Spec.ClusterID)
	}

	response := dto.EvaluationResponseDTO{
		CandidateClusters: candidates,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}
