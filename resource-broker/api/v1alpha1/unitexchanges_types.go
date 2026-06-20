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

// UnitExchangesSpec defines the desired state of UnitExchanges
type UnitExchangesSpec struct {
	// PrimaryUnit is the target currency (e.g. "EUR/MWh")
	PrimaryUnit string `json:"primaryUnit"`

	// Rates maps currency codes to their exchange rate vs the primary unit's base currency
	// e.g. {"USD": 1.08, "GBP": 0.85, "CAD": 1.50, ...}
	// Meaning: 1 EUR = 1.08 USD, 1 EUR = 0.85 GBP, etc.
	Rates map[string]float64 `json:"rates"`

	// LastUpdateUnit is when the exchange rates were last refreshed
	LastUpdateUnit metav1.Time `json:"lastUpdateUnit"`
}

// UnitExchangesStatus defines the observed state of UnitExchanges
type UnitExchangesStatus struct {
	// LastUpdateTime is when this CRD was last reconciled
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="PrimaryUnit",type=string,JSONPath=`.spec.primaryUnit`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// UnitExchanges is the Schema for the unitexchanges API.
// It caches currency exchange rates for normalizing energy costs.
type UnitExchanges struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UnitExchangesSpec   `json:"spec,omitempty"`
	Status UnitExchangesStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UnitExchangesList contains a list of UnitExchanges
type UnitExchangesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UnitExchanges `json:"items"`
}

func init() {
	SchemeBuilder.Register(&UnitExchanges{}, &UnitExchangesList{})
}
