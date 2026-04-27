package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReservationInstructionSpec carries information delivered from the broker to the local cluster.
type ReservationInstructionSpec struct {
	// ReservationName mirrors the Reservation on the broker cluster.
	ReservationName string `json:"reservationName"`

	// TargetClusterID is where the workload should consume resources.
	TargetClusterID string `json:"targetClusterID"`

	// RequestedCPU is the CPU quantity to consume (string to avoid Quantity parsing on the agent side).
	RequestedCPU string `json:"requestedCPU"`

	// RequestedMemory is the memory quantity to consume.
	RequestedMemory string `json:"requestedMemory"`

	// RequestedGPU is the GPU quantity to consume.
	RequestedGPU string `json:"requestedGPU,omitempty"`

	// Message provides human-readable hints for operators.
	Message string `json:"message,omitempty"`

	// ExpiresAt mirrors the broker-side reservation expiry.
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// PeeringKubeconfig is the securely populated kubeconfig for peering
	PeeringKubeconfig string `json:"peeringKubeconfig,omitempty"`
}

// ReservationInstructionStatus tracks whether the instruction reached local automation.
type ReservationInstructionStatus struct {
	// ObservedReservationResourceVersion is used to avoid double processing.
	ObservedReservationResourceVersion string `json:"observedReservationResourceVersion,omitempty"`

	// Delivered marks that local automation has acknowledged the instruction.
	Delivered bool `json:"delivered,omitempty"`

	// LastUpdateTime records the last update timestamp.
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Reservation",type=string,JSONPath=`.spec.reservationName`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetClusterID`
// +kubebuilder:printcolumn:name="CPU",type=string,JSONPath=`.spec.requestedCPU`
// +kubebuilder:printcolumn:name="Memory",type=string,JSONPath=`.spec.requestedMemory`

// ReservationInstruction is created by the broker watcher to notify the local cluster.
type ReservationInstruction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReservationInstructionSpec   `json:"spec,omitempty"`
	Status ReservationInstructionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReservationInstructionList contains a list of ReservationInstruction.
type ReservationInstructionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReservationInstruction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReservationInstruction{}, &ReservationInstructionList{})
}
