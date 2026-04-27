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

// Package v1alpha1 contains API Schema definitions for the broker v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=broker.fluidos.eu
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (

	/*Note: in kubernetes, all the resources are identified univocally by a Group, Version and Kind.
	In our case, they are:
	- Group: broker.fluidos.eu
	- Version: v1alpha1
	- Kind: ClusterAdvertisement

	The API machinery uses this information to identify and manage resources.
	- Group and version -> define the API group and version
	- Kind -> defines the type of resource
	- Plural name -> defines the plural name of the resource (used for kubectl commands)
		In our case: "clusteradvertisements"
	*/

	// GroupVersion indicates univocally the API group and version of our API group.
	// It is used to register our types with the scheme so that they can be used by the controller-runtime and the API machinery.
	GroupVersion = schema.GroupVersion{Group: "broker.fluidos.eu", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
