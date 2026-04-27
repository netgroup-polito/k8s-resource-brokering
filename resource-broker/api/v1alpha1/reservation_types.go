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

// ReservationFinalizer is the finalizer for reservations
const ReservationFinalizer = "reservation.broker.fluidos.eu/finalizer"

// ReservationSpec defines the desired state of Reservation
type ReservationSpec struct {
	// If not specified, the broker will automatically select the best cluster
	TargetClusterID string `json:"targetClusterID,omitempty"`

	RequestedResources RequestedResourceQuantities `json:"requestedResources"`

	// Duration is how long the reservation should last (optional)
	Duration *metav1.Duration `json:"duration,omitempty"`

	// Priority of this reservation (higher number = higher priority)
	Priority int32 `json:"priority,omitempty"`

	// RequesterID identifies who is requesting the reservation
	RequesterID string `json:"requesterID,omitempty"`
}

// RequestedResourceQuantities represents requested resource amounts
type RequestedResourceQuantities struct {
	CPU resource.Quantity `json:"cpu"`
	Memory resource.Quantity `json:"memory"`
	GPU *resource.Quantity `json:"gpu,omitempty"`

	// Storage requested (optional)
	Storage *resource.Quantity `json:"storage,omitempty"`
}

// ReservationStatus defines the observed state of Reservation
type ReservationStatus struct {
	// Possible values: Pending, Reserved, Active, Failed, Released
	Phase ReservationPhase `json:"phase,omitempty"`

	Message string `json:"message,omitempty"`
	ReservedAt *metav1.Time `json:"reservedAt,omitempty"`
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// PeeringKubeconfig contains the restricted credentials for the requester
	PeeringKubeconfig string `json:"peeringKubeconfig,omitempty"`

	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`

	// Conditions represent the latest observations of the reservation state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	ReservationConditionRequesterActive = "RequesterActive" //indicates the requester signaled readiness
	ReservationConditionRequesterReleased = "RequesterReleased" //indicates the requester finished consuming resource
)

// ReservationPhase represents the phase of a reservation
type ReservationPhase string

const (
	ReservationPhasePending ReservationPhase = "Pending" //Reservation request is pending
	ReservationPhaseReserved ReservationPhase = "Reserved" //Resources are reserved but not yet active
	ReservationPhaseActive ReservationPhase = "Active" //Reservation is active and in use
	ReservationPhaseFailed ReservationPhase = "Failed" //Reservation failed
	ReservationPhaseReleased ReservationPhase = "Released" //Reservation has been released
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Target-Cluster",type=string,JSONPath=`.spec.targetClusterID`
// +kubebuilder:printcolumn:name="CPU",type=string,JSONPath=`.spec.requestedResources.cpu`
// +kubebuilder:printcolumn:name="Memory",type=string,JSONPath=`.spec.requestedResources.memory`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Reservation is the Schema for the reservations API
type Reservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReservationSpec   `json:"spec,omitempty"`
	Status ReservationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReservationList contains a list of Reservation
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Reservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
