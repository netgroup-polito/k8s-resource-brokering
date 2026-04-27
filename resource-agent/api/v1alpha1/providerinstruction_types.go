package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProviderInstructionSpec carries information for the providing cluster.
type ProviderInstructionSpec struct {
	// ReservationName identifies the broker reservation.
	ReservationName string `json:"reservationName"`

	// RequesterClusterID is who will consume the resources.
	RequesterClusterID string `json:"requesterClusterID"`

	// RequestedCPU amount.
	RequestedCPU string `json:"requestedCPU"`

	// RequestedMemory amount.
	RequestedMemory string `json:"requestedMemory"`

	// RequestedGPU amount.
	RequestedGPU string `json:"requestedGPU,omitempty"`

	// Message is a human description.
	Message string `json:"message,omitempty"`

	// ExpiresAt mirrors broker expiry.
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// ProviderInstructionStatus tracks enforcement.
type ProviderInstructionStatus struct {
	// Enforced marks whether the provider controller acknowledged the instruction.
	Enforced bool `json:"enforced,omitempty"`

	// LastUpdateTime records status updates.
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Reservation",type=string,JSONPath=`.spec.reservationName`
// +kubebuilder:printcolumn:name="Requester",type=string,JSONPath=`.spec.requesterClusterID`
// +kubebuilder:printcolumn:name="CPU",type=string,JSONPath=`.spec.requestedCPU`
// +kubebuilder:printcolumn:name="Memory",type=string,JSONPath=`.spec.requestedMemory`

// ProviderInstruction notifies provider clusters.
type ProviderInstruction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderInstructionSpec   `json:"spec,omitempty"`
	Status ProviderInstructionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProviderInstructionList lists ProviderInstruction.
type ProviderInstructionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderInstruction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProviderInstruction{}, &ProviderInstructionList{})
}
