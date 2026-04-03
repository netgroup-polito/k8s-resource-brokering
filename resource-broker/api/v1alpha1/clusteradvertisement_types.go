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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterAdvertisementSpec defines the desired state of ClusterAdvertisement
type ClusterAdvertisementSpec struct {
	// ClusterID is the unique identifier of the source cluster
	ClusterID string `json:"clusterID"`

	// ClusterName is a human-readable name for the cluster
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// Resources available in the cluster
	Resources ResourceMetrics `json:"resources"`

	// Cost information (optional)
	// +optional
	Cost *CostInfo `json:"cost,omitempty"`

	// Timestamp when this advertisement was received
	Timestamp metav1.Time `json:"timestamp"`

	// EndpointURL is the API endpoint of the source cluster
	// +optional
	EndpointURL string `json:"endpointURL,omitempty"`
}

// ResourceMetrics represents available resources with detailed breakdown
type ResourceMetrics struct {
	// Capacity - Total physical resources the cluster has
	Capacity ResourceQuantities `json:"capacity"`

	// Allocatable - Capacity minus system reservations
	Allocatable ResourceQuantities `json:"allocatable"`

	// Allocated - Sum of resources requested by all pods
	Allocated ResourceQuantities `json:"allocated"`

	// Reserved - Resources locked by reservations (NEW!)
	// +optional
	Reserved *ResourceQuantities `json:"reserved,omitempty"`

	// Available - Allocatable minus Allocated (what's still schedulable)
	Available ResourceQuantities `json:"available"`
}

// ResourceQuantities represents resource amounts
type ResourceQuantities struct {
	// CPU in cores
	CPU resource.Quantity `json:"cpu"`

	// Memory in bytes
	Memory resource.Quantity `json:"memory"`

	// GPU (optional)
	// +optional
	GPU *resource.Quantity `json:"gpu,omitempty"`

	// Storage (optional)
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
}

// CostInfo represents cost information
type CostInfo struct {
	// CPUCost per core per hour
	CPUCost string `json:"cpuCost,omitempty"`

	// MemoryCost per GB per hour
	MemoryCost string `json:"memoryCost,omitempty"`

	// Currency for pricing
	Currency string `json:"currency,omitempty"`

	// Renewable indicates if the cluster uses renewable energy
	// +optional
	Renewable bool `json:"renewable,omitempty"`

	// EnergyCost is the cost of energy (0-1 normalization recommended)
	// +optional
	EnergyCost float64 `json:"energyCost,omitempty"`
}

// ClusterAdvertisementStatus defines the observed state of ClusterAdvertisement
type ClusterAdvertisementStatus struct {
	// Phase represents the current state
	// +optional
	Phase string `json:"phase,omitempty"`

	// LastUpdateTime is when this advertisement was last updated
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`

	// Active indicates if this cluster is currently available
	// +optional
	Active bool `json:"active,omitempty"`

	// Message provides additional information
	// +optional
	Message string `json:"message,omitempty"`

	// Score is calculated based on availability and cost (higher is better)
	// +optional
	Score string `json:"score,omitempty"`

	// Conditions represent the latest observations of the cluster advertisement state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// ClusterAdvertisementConditionReady indicates the cluster is ready to accept reservations
	ClusterAdvertisementConditionReady = "Ready"
	// ClusterAdvertisementConditionStale indicates the advertisement is stale
	ClusterAdvertisementConditionStale = "Stale"
	// ClusterAdvertisementConditionOvercommitted indicates reserved > available
	ClusterAdvertisementConditionOvercommitted = "Overcommitted"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="ClusterID",type=string,JSONPath=`.spec.clusterID`
// +kubebuilder:printcolumn:name="Available-CPU",type=string,JSONPath=`.spec.resources.available.cpu`
// +kubebuilder:printcolumn:name="Available-Memory",type=string,JSONPath=`.spec.resources.available.memory`
// +kubebuilder:printcolumn:name="Score",type=number,JSONPath=`.status.score`
// +kubebuilder:printcolumn:name="Active",type=boolean,JSONPath=`.status.active`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterAdvertisement is the Schema for the clusteradvertisements API
type ClusterAdvertisement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterAdvertisementSpec   `json:"spec,omitempty"`
	Status ClusterAdvertisementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterAdvertisementList contains a list of ClusterAdvertisement
type ClusterAdvertisementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAdvertisement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterAdvertisement{}, &ClusterAdvertisementList{})
}
