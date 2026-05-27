package dto

import (
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rearv1alpha1 "github.com/mehdiazizian/liqo-resource-agent/api/v1alpha1"
)

// ToAdvertisementDTO converts agent's local Advertisement to protocol-agnostic DTO
// Note: Agent's Advertisement does NOT have Reserved field - broker manages it
func ToAdvertisementDTO(adv *rearv1alpha1.Advertisement) *AdvertisementDTO {
	dto := &AdvertisementDTO{
		ClusterID:   adv.Spec.ClusterID,
		ClusterName: adv.Name,
		Timestamp:   time.Now(), // Always send fresh timestamp to broker
		Resources: ResourceMetricsDTO{
			Capacity:    toResourceQuantitiesDTO(adv.Spec.Resources.Capacity),
			Allocatable: toResourceQuantitiesDTO(adv.Spec.Resources.Allocatable),
			Allocated:   toResourceQuantitiesDTO(adv.Spec.Resources.Allocated),
			Available:   toResourceQuantitiesDTO(adv.Spec.Resources.Available),
			// Reserved: nil - will be fetched from broker during publish
		},
		Policy: adv.Spec.Policy,
	}

	if adv.Spec.Cost != nil {
		dto.Cost = &CostInfoDTO{
			Renewable:  adv.Spec.Cost.Renewable,
			EnergyCost: adv.Spec.Cost.EnergyCost,
		}
	}

	return dto
}

// toResourceQuantitiesDTO converts k8s ResourceQuantities to DTO format (string-based)
func toResourceQuantitiesDTO(rq rearv1alpha1.ResourceQuantities) ResourceQuantitiesDTO {
	dto := ResourceQuantitiesDTO{
		CPU:    rq.CPU.String(),
		Memory: rq.Memory.String(),
	}

	if rq.GPU != nil {
		dto.GPU = rq.GPU.String()
	}

	if rq.Storage != nil {
		dto.Storage = rq.Storage.String()
	}

	return dto
}

// fromResourceQuantitiesDTO converts DTO (string-based) to k8s ResourceQuantities
func fromResourceQuantitiesDTO(dto ResourceQuantitiesDTO) (rearv1alpha1.ResourceQuantities, error) {
	rq := rearv1alpha1.ResourceQuantities{}

	// Parse CPU
	cpuQty, err := resource.ParseQuantity(dto.CPU)
	if err != nil {
		return rq, err
	}
	rq.CPU = cpuQty

	// Parse Memory
	memQty, err := resource.ParseQuantity(dto.Memory)
	if err != nil {
		return rq, err
	}
	rq.Memory = memQty

	// Parse optional GPU
	if dto.GPU != "" {
		gpuQty, err := resource.ParseQuantity(dto.GPU)
		if err != nil {
			return rq, err
		}
		rq.GPU = &gpuQty
	}

	// Parse optional Storage
	if dto.Storage != "" {
		storageQty, err := resource.ParseQuantity(dto.Storage)
		if err != nil {
			return rq, err
		}
		rq.Storage = &storageQty
	}

	return rq, nil
}

// FromReservationDTO converts DTO to agent's ReservationInstruction spec data
// This is used when agent receives reservation from broker via HTTP
func FromReservationDTO(dto *ReservationDTO) (requesterID, targetClusterID, cpu, memory string, expiresAt *metav1.Time) {
	requesterID = dto.RequesterID
	targetClusterID = dto.TargetClusterID
	cpu = dto.RequestedResources.CPU
	memory = dto.RequestedResources.Memory

	if dto.Status.ExpiresAt != nil {
		expiresAt = &metav1.Time{Time: *dto.Status.ExpiresAt}
	}

	return
}
