/*
Copyright 2026 The Cairn Authors.

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

// ClusterRightsizePolicySpec defines the desired state of ClusterRightsizePolicy

// NamespaceSelector defines which namespaces the cluster policy applies to.
type NamespaceSelector struct {
	// MatchNames is an explicit list of namespace names.
	// +optional
	MatchNames []string `json:"matchNames,omitempty"`

	// ExcludeNames is a list of namespace names to exclude.
	// +optional
	ExcludeNames []string `json:"excludeNames,omitempty"`

	// LabelSelector selects namespaces by labels.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}
type ClusterRightsizePolicySpec struct {
	// Enabled must be explicitly set to true for this cluster policy to
	// take effect. Defaults to false so cluster policies are safe to deploy
	// without immediately impacting workloads.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// NamespaceSelector defines which namespaces this policy applies to.
	// If unset and enabled=true, applies to all namespaces.
	// +optional
	NamespaceSelector *NamespaceSelector `json:"namespaceSelector,omitempty"`

	// Priority determines which cluster policy wins when multiple
	// ClusterRightsizePolicies match the same workload. Higher wins.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`

	// CommonPolicySpec contains all policy settings (targetRef, mode,
	// updateStrategy, containers, java, window, etc.) shared with
	// RightsizePolicy.
	CommonPolicySpec `json:",inline"`
}

// ClusterRightsizePolicyStatus defines the observed state of ClusterRightsizePolicy.
type ClusterRightsizePolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the ClusterRightsizePolicy resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TargetedNamespaces is the number of namespaces matched by this policy.
	TargetedNamespaces int32 `json:"targetedNamespaces,omitempty"`

	// TargetedWorkloads is the total number of workloads matched across
	// all namespaces.
	TargetedWorkloads int32 `json:"targetedWorkloads,omitempty"`

	// RecommendationsReady is the number of workloads with ready recommendations.
	RecommendationsReady int32 `json:"recommendationsReady,omitempty"`

	// LastReconcileTime is the timestamp of the last successful reconciliation.
	// +optional
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Namespaces",type=integer,JSONPath=`.status.targetedNamespaces`
// +kubebuilder:printcolumn:name="Workloads",type=integer,JSONPath=`.status.targetedWorkloads`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.recommendationsReady`
// +kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspended`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ClusterRightsizePolicy is the Schema for the clusterrightsizepolicies API
type ClusterRightsizePolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ClusterRightsizePolicy
	// +required
	Spec ClusterRightsizePolicySpec `json:"spec"`

	// status defines the observed state of ClusterRightsizePolicy
	// +optional
	Status ClusterRightsizePolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterRightsizePolicyList contains a list of ClusterRightsizePolicy
type ClusterRightsizePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterRightsizePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterRightsizePolicy{}, &ClusterRightsizePolicyList{})
}
