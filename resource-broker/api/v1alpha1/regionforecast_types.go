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

// RegionForecastSpec defines the desired state of RegionForecast
type RegionForecastSpec struct {
	// Region is the region code (e.g. "LOM", "QC", "CA")
	Region string `json:"region"`

	// --- Carbon fields ---

	// CarbonIntensity is the 24-hour carbon intensity forecast in gCO2eq/kWh
	CarbonIntensity []int `json:"carbonIntensity,omitempty"`

	// LastUpdateCarbon is when this forecast was last fetched from the carbon intensity service
	LastUpdateCarbon metav1.Time `json:"lastUpdateCarbon,omitempty"`

	// --- Cost fields ---

	// Cost is the 24-hour day-ahead energy price forecast
	Cost []float64 `json:"cost,omitempty"`

	// CostUnit is the currency unit for cost values (e.g. "EUR/MWh", "USD/MWh")
	CostUnit string `json:"costUnit,omitempty"`

	// LastUpdateCost is when the cost forecast was last fetched
	LastUpdateCost metav1.Time `json:"lastUpdateCost,omitempty"`
}

// RegionForecastStatus defines the observed state of RegionForecast
type RegionForecastStatus struct {
	// LastUpdateTime is when this CRD was last reconciled
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RegionForecast is the Schema for the regionforecasts API.
// It caches per-region carbon intensity and energy price forecast data.
type RegionForecast struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RegionForecastSpec   `json:"spec,omitempty"`
	Status RegionForecastStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RegionForecastList contains a list of RegionForecast
type RegionForecastList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RegionForecast `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RegionForecast{}, &RegionForecastList{})
}
