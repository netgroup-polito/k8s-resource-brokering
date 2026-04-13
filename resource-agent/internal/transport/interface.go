package transport

import (
	"context"

	"github.com/mehdiazizian/liqo-resource-agent/internal/transport/dto"
)

// BrokerCommunicator abstracts broker communication protocol (agent-side interface)
// Implementations: HTTP REST API, Kubernetes CRD-based, MQTT, etc.
type BrokerCommunicator interface {
	// PublishAdvertisement publishes cluster resource advertisement to broker.
	// Returns any piggybacked provider instructions from the broker response,
	// eliminating the need for separate polling.
	PublishAdvertisement(ctx context.Context, adv *dto.AdvertisementDTO) ([]*dto.ReservationDTO, error)

	// RequestReservation sends a synchronous reservation request to the broker.
	// The broker decides and reserves resources inline, returning the instruction
	// in the response. No polling needed.
	RequestReservation(ctx context.Context, req *dto.ReservationRequestDTO) (*dto.ReservationDTO, error)

	// FetchInstructions polls the broker for pending provider instructions.
	// This provides near-instant instruction delivery (every few seconds)
	// instead of waiting for the next advertisement cycle.
	FetchInstructions(ctx context.Context) ([]*dto.ReservationDTO, error)

	// GetReservation fetches a specific reservation by ID
	GetReservation(ctx context.Context, reservationID string) (*dto.ReservationDTO, error)

	// UploadPeeringKubeconfig securely sends the generated peering-user kubeconfig
	// to the broker, so that the requester cluster can download and use it.
	UploadPeeringKubeconfig(ctx context.Context, reservationID string, kubeconfig string) error

	// Ping checks connectivity to broker
	Ping(ctx context.Context) error

	// Close cleans up resources
	Close() error
}
