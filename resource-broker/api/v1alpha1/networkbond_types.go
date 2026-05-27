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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkBondSpec defines the desired state of NetworkBond
type NetworkBondSpec struct {
	// RequesterClusterID is the ID of the cluster requesting resources
	RequesterClusterID string `json:"requesterClusterID"`

	// ProviderClusterID is the ID of the cluster providing resources
	ProviderClusterID string `json:"providerClusterID"`

	// EstimatedLatency is the estimated RTT latency between the clusters in milliseconds
	EstimatedLatency float64 `json:"estimatedLatency,omitempty"`

	// ActualLatency is the actual measured RTT latency between the clusters in milliseconds (reserved for future use)
	ActualLatency float64 `json:"actualLatency,omitempty"`
}

// NetworkBondStatus defines the observed state of NetworkBond
type NetworkBondStatus struct {
	// LastUpdateTime is when this bond was last updated
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Requester",type=string,JSONPath=`.spec.requesterClusterID`
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.providerClusterID`
// +kubebuilder:printcolumn:name="Est-Latency(ms)",type=number,JSONPath=`.spec.estimatedLatency`

// NetworkBond is the Schema for the networkbonds API
type NetworkBond struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkBondSpec   `json:"spec,omitempty"`
	Status NetworkBondStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkBondList contains a list of NetworkBond
type NetworkBondList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkBond `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NetworkBond{}, &NetworkBondList{})
}
