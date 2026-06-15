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

// AdvertisementSpec defines the desired state of Advertisement
type AdvertisementSpec struct {
	// ClusterID is the unique identifier of the cluster
	ClusterID string `json:"clusterID"`

	// Resources available in this cluster
	Resources ResourceMetrics `json:"resources"`

	// Policy for cluster ranking
	Policy string `json:"policy,omitempty"`

	// Cost information
	Cost *CostInfo `json:"cost,omitempty"`

	// Timestamp when this advertisement was created
	Timestamp metav1.Time `json:"timestamp"`
}

// ResourceMetrics represents available resources with detailed breakdown
type ResourceMetrics struct {
	// Capacity - Total physical resources the cluster has
	Capacity ResourceQuantities `json:"capacity"`

	// Allocatable - Capacity minus system reservations (what pods can use)
	Allocatable ResourceQuantities `json:"allocatable"`

	// Allocated - Sum of resources requested by all pods
	Allocated ResourceQuantities `json:"allocated"`

	// Used - Resources actually being consumed right now
	Used *ResourceQuantities `json:"used,omitempty"`

	// Available - Allocatable minus Allocated (what's still schedulable)
	Available ResourceQuantities `json:"available"`
}

// ResourceQuantities represents resource amounts
type ResourceQuantities struct {
	// CPU in cores
	CPU resource.Quantity `json:"cpu"`

	// Memory in bytes
	Memory resource.Quantity `json:"memory"`

	// GPU
	GPU *resource.Quantity `json:"gpu,omitempty"`

	// Storage
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
	Renewable bool `json:"renewable,omitempty"`

	// EnergyCost is the cost of energy (0-1 normalization recommended)
	EnergyCost float64 `json:"energyCost,omitempty"`
}

// AdvertisementStatus defines the observed state of Advertisement
type AdvertisementStatus struct {
	// Phase represents the current state of the advertisement
	Phase string `json:"phase,omitempty"`

	// LastUpdateTime is when this advertisement was last updated
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`

	// Published indicates if this advertisement has been published to the broker
	Published bool `json:"published,omitempty"`

	// Message provides additional information about the status
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="ClusterID",type=string,JSONPath=`.spec.clusterID`
// +kubebuilder:printcolumn:name="Allocatable-CPU",type=string,JSONPath=`.spec.resources.allocatable.cpu`
// +kubebuilder:printcolumn:name="Available-CPU",type=string,JSONPath=`.spec.resources.available.cpu`
// +kubebuilder:printcolumn:name="Allocatable-Mem",type=string,JSONPath=`.spec.resources.allocatable.memory`
// +kubebuilder:printcolumn:name="Available-Mem",type=string,JSONPath=`.spec.resources.available.memory`
// +kubebuilder:printcolumn:name="Published",type=boolean,JSONPath=`.status.published`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Advertisement is the Schema for the advertisements API
type Advertisement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AdvertisementSpec   `json:"spec,omitempty"`
	Status AdvertisementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AdvertisementList contains a list of Advertisement
type AdvertisementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Advertisement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Advertisement{}, &AdvertisementList{})
}
