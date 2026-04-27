package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport"
	"github.com/mehdiazizian/liqo-resource-agent/internal/transport/dto"
)

// InstructionPoller polls the broker for provider instructions at a configurable interval.
// This provides near-instant instruction delivery instead of waiting for the next
// advertisement cycle (30s). The poller creates ProviderInstruction CRDs locally.
type InstructionPoller struct {
	Client               client.Client
	BrokerCommunicator   transport.BrokerCommunicator
	PollInterval         time.Duration
	InstructionNamespace string
}

// Start runs the instruction polling loop until the context is cancelled.
func (p *InstructionPoller) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("instruction-poller")
	logger.Info("Starting instruction poller", "interval", p.PollInterval)

	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Instruction poller stopped")
			return nil
		case <-ticker.C:
			instructions, err := p.BrokerCommunicator.FetchInstructions(ctx)
			if err != nil {
				logger.V(1).Info("Failed to fetch instructions", "error", err)
				continue
			}
			if len(instructions) > 0 {
				p.processInstructions(ctx, instructions)
			}
		}
	}
}

// processInstructions creates ProviderInstruction CRDs from fetched instructions.
func (p *InstructionPoller) processInstructions(ctx context.Context, instructions []*dto.ReservationDTO) {
	logger := log.FromContext(ctx).WithName("instruction-poller")

	for _, rsv := range instructions {
		if rsv.Status.Phase != "Reserved" {
			continue
		}

		instructionName := fmt.Sprintf("%s-provider", rsv.ID)

		// Check if instruction already exists
		existing := &rearv1alpha1.ProviderInstruction{}
		if err := p.Client.Get(ctx, types.NamespacedName{
			Name: instructionName, Namespace: p.InstructionNamespace,
		}, existing); err == nil {
			continue // Already exists
		}

		var expiresAt *metav1.Time
		if rsv.Status.ExpiresAt != nil {
			expiresAt = &metav1.Time{Time: *rsv.Status.ExpiresAt}
		}

		instruction := &rearv1alpha1.ProviderInstruction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instructionName,
				Namespace: p.InstructionNamespace,
			},
			Spec: rearv1alpha1.ProviderInstructionSpec{
				ReservationName:    rsv.ID,
				RequesterClusterID: rsv.RequesterID,
				RequestedCPU:       rsv.RequestedResources.CPU,
				RequestedMemory:    rsv.RequestedResources.Memory,
				RequestedGPU:       rsv.RequestedResources.GPU,
				Message: fmt.Sprintf("Hold %s CPU / %s Memory / %s GPU for requester %s",
					rsv.RequestedResources.CPU,
					rsv.RequestedResources.Memory,
					rsv.RequestedResources.GPU,
					rsv.RequesterID),
				ExpiresAt: expiresAt,
			},
		}

		if err := p.Client.Create(ctx, instruction); err != nil {
			logger.Error(err, "Failed to create provider instruction",
				"reservation", rsv.ID,
				"requester", rsv.RequesterID)
		} else {
			logger.Info("Created provider instruction from poll",
				"reservation", rsv.ID,
				"requester", rsv.RequesterID,
				"cpu", rsv.RequestedResources.CPU,
				"memory", rsv.RequestedResources.Memory)
		}
	}
}
