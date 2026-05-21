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
	ClusterID   string          `json:"clusterID"`
	//omitempty = 
	ClusterName string          `json:"clusterName,omitempty"` //omit empty allows to not include in JSON if empty
	Resources   ResourceMetrics `json:"resources"`
	Cost *CostInfo `json:"cost,omitempty"`
	Location *LocationInfo `json:"location,omitempty"`

	// Timestamp when this advertisement was received
	Timestamp metav1.Time `json:"timestamp"`

	// EndpointURL is the API endpoint of the source cluster
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

	// Reserved - Resources locked by reservations 
	Reserved *ResourceQuantities `json:"reserved,omitempty"`

	// Available - Allocatable minus Allocated 
	Available ResourceQuantities `json:"available"`
}

// ResourceQuantities represents resource amounts
type ResourceQuantities struct {
	// CPU in cores [resource is a package of k8s.io/apimachinery/pkg/api/resource, used to reppresent CPU, memory,.. as quantity]
	CPU resource.Quantity `json:"cpu"`

	// Memory in bytes
	Memory resource.Quantity `json:"memory"`

	// GPU
	GPU *resource.Quantity `json:"gpu,omitempty"`

	// Storage (optional)
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

// LocationInfo represents geographic location information
type LocationInfo struct {
	ContinentCode string  `json:"continentCode,omitempty"`
	CountryCode   string  `json:"countryCode,omitempty"`
	Region        string  `json:"region,omitempty"`
	RegionName    string  `json:"regionName,omitempty"`
	City          string  `json:"city,omitempty"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
	ISP           string  `json:"isp,omitempty"`
}

// ClusterAdvertisementStatus defines the observed state of ClusterAdvertisement
type ClusterAdvertisementStatus struct {
	// Phase represents the current state
	Phase string `json:"phase,omitempty"`

	// LastUpdateTime is when this advertisement was last updated
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`

	// Active indicates if this cluster is currently available
	Active bool `json:"active,omitempty"`

	// Message provides additional information
	Message string `json:"message,omitempty"`

	// Score is calculated based on availability and cost (higher is better)
	Score string `json:"score,omitempty"`

	// Conditions represent the latest observations of the cluster advertisement state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	ClusterAdvertisementConditionReady = "Ready" //the cluster is ready to accept reservations
	ClusterAdvertisementConditionStale = "Stale" //the advertisement is stale
	ClusterAdvertisementConditionOvercommitted = "Overcommitted" //indicates reserved > available
)





/*HOW TO USE KUBEBUILDER MARKERS
This is a way to autogenerate Kubernetes API types using kubebuilder. 
The comments with +kubebuilder:... are markers that kubebuilder uses to generate code and CRD manifests. 
For example, +kubebuilder:object:root=true indicates that this struct is a root object in the API, 
+kubebuilder:subresource:status indicates that it has a status subresource 
and the printcolumn markers define how the resource will be displayed when "kubectl get clusteradvertisement".

In fact, in our case we generate the crd for the ClusterAdvertisement resource (struct defined below)
that will be normalized in "clusteradvertisements" and will be used by the Resource Broker to store the advertisements received from the Resource Agents.

If we use kubectl get clusteradvertisements, the output will include columns for ClusterID, Available-CPU, ..
*/


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


// kubectl get clusteradvertisements will show the various clusteradvertisements with the columns defined above

// +kubebuilder:object:root=true

// ClusterAdvertisementList contains a list of ClusterAdvertisement
type ClusterAdvertisementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAdvertisement `json:"items"`
}

// init() is called when the package is initialized. Here we register our types with the scheme so that they can be used by the controller-runtime and the API machinery.
func init() {
	SchemeBuilder.Register(&ClusterAdvertisement{}, &ClusterAdvertisementList{})
}
