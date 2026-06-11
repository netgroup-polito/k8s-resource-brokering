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

// RegionEcoForecastSpec defines the desired state of RegionEcoForecast
type RegionEcoForecastSpec struct {
	// Region is the region code (e.g. "LOM", "QC", "CA")
	Region string `json:"region"`

	// CarbonIntensity is the 24-hour carbon intensity forecast in gCO2eq/kWh
	CarbonIntensity []int `json:"carbonIntensity"`

	// LastUpdate is when this forecast was last fetched from the carbon intensity service
	LastUpdate metav1.Time `json:"lastUpdate"`
}

// RegionEcoForecastStatus defines the observed state of RegionEcoForecast
type RegionEcoForecastStatus struct {
	// LastUpdateTime is when this CRD was last reconciled
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RegionEcoForecast is the Schema for the regionecoforecasts API.
// It caches per-region carbon intensity forecast data fetched from the carbon intensity service.
type RegionEcoForecast struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RegionEcoForecastSpec   `json:"spec,omitempty"`
	Status RegionEcoForecastStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RegionEcoForecastList contains a list of RegionEcoForecast
type RegionEcoForecastList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RegionEcoForecast `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RegionEcoForecast{}, &RegionEcoForecastList{})
}
