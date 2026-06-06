package dto

import "time"

// Role represents the cluster's role in a reservation
type Role string

const (
	// RoleRequester means this cluster is requesting resources
	RoleRequester Role = "requester"

	// RoleProvider means this cluster is providing resources
	RoleProvider Role = "provider"
)

// ReservationDTO is a protocol-agnostic representation of a resource reservation
type ReservationDTO struct {
	ID                 string                `json:"id"`
	RequesterID        string                `json:"requesterID"`
	TargetClusterID    string                `json:"targetClusterID"`
	RequestedResources ResourceQuantitiesDTO `json:"requestedResources"`
	Status             ReservationStatusDTO  `json:"status"`
	CreatedAt          time.Time             `json:"createdAt"`
}

// ReservationStatusDTO represents the status of a reservation
type ReservationStatusDTO struct {
	Phase             string     `json:"phase"` // Pending, Reserved, Active, Released, Failed
	Message           string     `json:"message"`
	ReservedAt        *time.Time `json:"reservedAt,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	PeeringKubeconfig string     `json:"peeringKubeconfig,omitempty"`
}

// ReservationRequestDTO is sent by the agent to request a resource reservation.
// The requesterID is extracted from the mTLS certificate on the broker side.
type ReservationRequestDTO struct {
	RequestedResources ResourceQuantitiesDTO `json:"requestedResources"`
	Priority           int32                 `json:"priority,omitempty"`
	Duration           string                `json:"duration,omitempty"`        // e.g., "1h", "30m"
	TargetClusterID    string                `json:"targetClusterID,omitempty"` // Optional specific cluster
	Location           *LocationDTO          `json:"location,omitempty"`

	// These two fields are used for the re-evaluations. They represent the current provider the requester is peered with and their latency
	CurrentProviderID string   `json:"currentProviderID,omitempty"`
	MeasuredLatencyMs *float64 `json:"measuredLatencyMs,omitempty"`
}

// CandidateClusterDTO pairs a cluster ID with information about the ranking decision
type CandidateClusterDTO struct {
	ClusterID   string `json:"clusterID"`
	Information string `json:"information,omitempty"`
}

// EvaluationResponseDTO is returned when evaluating a request without reserving
type EvaluationResponseDTO struct {
	CandidateClusters []CandidateClusterDTO `json:"candidateClusters"`
}
