/*
Copyright 2026 Big Bang.

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PackageSpec defines the desired state of Package.
//
// Fields mirror the bb-common values schema (see hack/schema-to-go.sh and
// zz_generated.types.go). Only the subsystems supported in v1 are wired here;
// `global` and `selfTest` from bb-common are intentionally omitted.
type PackageSpec struct {
	// istio configures Istio resources (PeerAuthentication, Sidecar,
	// AuthorizationPolicies, ServiceEntries).
	// +optional
	Istio *Istio `json:"istio,omitempty"`

	// networkPolicies configures default policies and shorthand / raw
	// NetworkPolicy resources.
	// +optional
	NetworkPolicies *NetworkPolicies `json:"networkPolicies,omitempty"`

	// routes configures Istio inbound (VirtualService/ServiceEntry +
	// NetworkPolicy/AuthorizationPolicy) and outbound (ServiceEntry) routing.
	// +optional
	Routes *Routes `json:"routes,omitempty"`
}

// PackageStatus defines the observed state of Package.
type PackageStatus struct {
	// observedGeneration is the .metadata.generation the reconciler last
	// successfully applied.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions tracks reconciliation state. The Ready condition is set
	// True when desired resources are applied and pruned, False otherwise.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// appliedResources is an informational list of objects the reconciler
	// emitted on the last successful pass. Prune is label-driven, so this
	// list is for human / dashboard consumption — it is not authoritative.
	// +optional
	AppliedResources []AppliedResource `json:"appliedResources,omitempty"`
}

// AppliedResource is one entry in PackageStatus.AppliedResources.
type AppliedResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Package is the Schema for the packages API
type Package struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Package
	// +required
	Spec PackageSpec `json:"spec"`

	// status defines the observed state of Package
	// +optional
	Status PackageStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PackageList contains a list of Package
type PackageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Package `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Package{}, &PackageList{})
}
