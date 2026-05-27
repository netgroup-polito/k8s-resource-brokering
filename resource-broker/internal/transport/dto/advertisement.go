package dto

import "time"

// AdvertisementDTO is a protocol-agnostic representation of cluster advertisement
// It decouples business logic from transport protocol (HTTP, Kubernetes CRDs, etc.)
type AdvertisementDTO struct {
	ClusterID   string             `json:"clusterID"`
	ClusterName string             `json:"clusterName"`
	Resources   ResourceMetricsDTO `json:"resources"`
	Cost        *CostInfoDTO       `json:"cost,omitempty"`
	Location    *LocationDTO       `json:"location,omitempty"`
	Policy      string             `json:"policy,omitempty"`
	Timestamp   time.Time          `json:"timestamp"`
}

// LocationDTO represents geographic location information
type LocationDTO struct {
	ContinentCode string  `json:"continentCode"`
	CountryCode   string  `json:"countryCode"`
	Region        string  `json:"region"`
	RegionName    string  `json:"regionName"`
	City          string  `json:"city"`
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
	ISP           string  `json:"isp"`
	Org           string  `json:"org"`
	AS            string  `json:"as"`
}

// CostInfoDTO represents cost information in a protocol-agnostic way
type CostInfoDTO struct {
	Renewable  bool    `json:"renewable"`
	EnergyCost float64 `json:"energyCost"`
}

// ResourceMetricsDTO represents resource metrics in a protocol-agnostic way
type ResourceMetricsDTO struct {
	Capacity    ResourceQuantitiesDTO  `json:"capacity"`
	Allocatable ResourceQuantitiesDTO  `json:"allocatable"`
	Allocated   ResourceQuantitiesDTO  `json:"allocated"`
	Reserved    *ResourceQuantitiesDTO `json:"reserved,omitempty"` // CRITICAL: Broker-managed field
	Available   ResourceQuantitiesDTO  `json:"available"`
}

// ResourceQuantitiesDTO represents resource quantities using strings
// This avoids coupling to k8s.io/apimachinery/pkg/api/resource.Quantity
type ResourceQuantitiesDTO struct {
	CPU     string `json:"cpu"`               // e.g., "4000m" or "4"
	Memory  string `json:"memory"`            // e.g., "8Gi" or "8589934592"
	GPU     string `json:"gpu,omitempty"`     // e.g., "2"
	Storage string `json:"storage,omitempty"` // e.g., "100Gi"
}

// AdvertisementResponseDTO is the response for POST /api/v1/advertisements.
// It wraps the updated advertisement and piggybacks any pending provider instructions,
// eliminating the need for agents to poll for provider-role reservations.
type AdvertisementResponseDTO struct {
	Advertisement        *AdvertisementDTO `json:"advertisement"`
	ProviderInstructions []*ReservationDTO `json:"providerInstructions,omitempty"`
}
