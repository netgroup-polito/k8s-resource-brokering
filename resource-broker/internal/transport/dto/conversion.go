package dto

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	brokerv1alpha1 "github.com/mehdiazizian/liqo-resource-broker/api/v1alpha1"
)

// ToClusterAdvertisement converts DTO to broker's ClusterAdvertisement CRD
func ToClusterAdvertisement(dto *AdvertisementDTO, namespace string) (*brokerv1alpha1.ClusterAdvertisement, error) {
	capacity, err := fromResourceQuantitiesDTO(dto.Resources.Capacity)
	if err != nil {
		return nil, err
	}

	allocatable, err := fromResourceQuantitiesDTO(dto.Resources.Allocatable)
	if err != nil {
		return nil, err
	}

	allocated, err := fromResourceQuantitiesDTO(dto.Resources.Allocated)
	if err != nil {
		return nil, err
	}

	available, err := fromResourceQuantitiesDTO(dto.Resources.Available)
	if err != nil {
		return nil, err
	}

	clusterAdv := &brokerv1alpha1.ClusterAdvertisement{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dto.ClusterID + "-adv",
			Namespace: namespace,
		},
		Spec: brokerv1alpha1.ClusterAdvertisementSpec{
			ClusterID:   dto.ClusterID,
			ClusterName: dto.ClusterName,
			Timestamp:   metav1.Time{Time: dto.Timestamp},
			Resources: brokerv1alpha1.ResourceMetrics{
				Capacity:    capacity,
				Allocatable: allocatable,
				Allocated:   allocated,
				Available:   available,
				// Reserved: will be preserved from existing if present
			},
		},
	}

	//Correctly saves in the ClusterAdvertisement CRD the cost information
	if dto.Cost != nil {
		clusterAdv.Spec.Cost = &brokerv1alpha1.CostInfo{
			Renewable:  dto.Cost.Renewable,
			EnergyCost: dto.Cost.EnergyCost,
		}
	}

	if dto.Location != nil {
		clusterAdv.Spec.Location = &brokerv1alpha1.LocationInfo{
			ContinentCode: dto.Location.ContinentCode,
			CountryCode:   dto.Location.CountryCode,
			Region:        dto.Location.Region,
			RegionName:    dto.Location.RegionName,
			City:          dto.Location.City,
			Lat:           dto.Location.Lat,
			Lon:           dto.Location.Lon,
			ISP:           dto.Location.ISP,
		}
	}

	// CRITICAL: Preserve Reserved field from DTO if present (broker-managed)
	if dto.Resources.Reserved != nil {
		reserved, err := fromResourceQuantitiesDTO(*dto.Resources.Reserved)
		if err != nil {
			return nil, err
		}
		clusterAdv.Spec.Resources.Reserved = &reserved
	}

	return clusterAdv, nil
}

// FromClusterAdvertisement converts broker's ClusterAdvertisement to DTO
func FromClusterAdvertisement(clusterAdv *brokerv1alpha1.ClusterAdvertisement) *AdvertisementDTO {
	dto := &AdvertisementDTO{
		ClusterID:   clusterAdv.Spec.ClusterID,
		ClusterName: clusterAdv.Spec.ClusterName,
		Timestamp:   clusterAdv.Spec.Timestamp.Time,
		Resources: ResourceMetricsDTO{
			Capacity:    toResourceQuantitiesDTO(clusterAdv.Spec.Resources.Capacity),
			Allocatable: toResourceQuantitiesDTO(clusterAdv.Spec.Resources.Allocatable),
			Allocated:   toResourceQuantitiesDTO(clusterAdv.Spec.Resources.Allocated),
			Available:   toResourceQuantitiesDTO(clusterAdv.Spec.Resources.Available),
		},
	}

	if clusterAdv.Spec.Cost != nil {
		dto.Cost = &CostInfoDTO{
			Renewable:  clusterAdv.Spec.Cost.Renewable,
			EnergyCost: clusterAdv.Spec.Cost.EnergyCost,
		}
	}

	if clusterAdv.Spec.Location != nil {
		dto.Location = &LocationDTO{
			ContinentCode: clusterAdv.Spec.Location.ContinentCode,
			CountryCode:   clusterAdv.Spec.Location.CountryCode,
			Region:        clusterAdv.Spec.Location.Region,
			RegionName:    clusterAdv.Spec.Location.RegionName,
			City:          clusterAdv.Spec.Location.City,
			Lat:           clusterAdv.Spec.Location.Lat,
			Lon:           clusterAdv.Spec.Location.Lon,
			ISP:           clusterAdv.Spec.Location.ISP,
		}
	}

	// CRITICAL: Include Reserved field if present (broker-managed)
	if clusterAdv.Spec.Resources.Reserved != nil {
		reserved := toResourceQuantitiesDTO(*clusterAdv.Spec.Resources.Reserved)
		dto.Resources.Reserved = &reserved
	}

	return dto
}

// FromReservation converts broker's Reservation to DTO
func FromReservation(rsv *brokerv1alpha1.Reservation) *ReservationDTO {
	dto := &ReservationDTO{
		ID:              rsv.Name,
		RequesterID:     rsv.Spec.RequesterID,
		TargetClusterID: rsv.Spec.TargetClusterID,
		RequestedResources: ResourceQuantitiesDTO{
			CPU:    rsv.Spec.RequestedResources.CPU.String(),
			Memory: rsv.Spec.RequestedResources.Memory.String(),
		},
		Status: ReservationStatusDTO{
			Phase:             string(rsv.Status.Phase),
			Message:           rsv.Status.Message,
			PeeringKubeconfig: rsv.Status.PeeringKubeconfig,
		},
		CreatedAt: rsv.CreationTimestamp.Time,
	}

	// Include GPU if present
	if rsv.Spec.RequestedResources.GPU != nil {
		dto.RequestedResources.GPU = rsv.Spec.RequestedResources.GPU.String()
	}

	// Include status times
	if rsv.Status.ReservedAt != nil {
		dto.Status.ReservedAt = &rsv.Status.ReservedAt.Time
	}

	if rsv.Status.ExpiresAt != nil {
		dto.Status.ExpiresAt = &rsv.Status.ExpiresAt.Time
	}

	return dto
}

// toResourceQuantitiesDTO converts k8s ResourceQuantities to DTO format (string-based)
func toResourceQuantitiesDTO(rq brokerv1alpha1.ResourceQuantities) ResourceQuantitiesDTO {
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
func fromResourceQuantitiesDTO(dto ResourceQuantitiesDTO) (brokerv1alpha1.ResourceQuantities, error) {
	rq := brokerv1alpha1.ResourceQuantities{}

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
