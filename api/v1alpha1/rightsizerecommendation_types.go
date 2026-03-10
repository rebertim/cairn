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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RightsizeRecommendationSpec defines the desired state of RightsizeRecommendation

// PolicyReference identifies which policy generated this recommendation.
type PolicyReference struct {
	// Kind is either RightsizePolicy or ClusterRightsizePolicy.
	Kind string `json:"kind"`

	// Name of the policy.
	Name string `json:"name"`

	// Namespace of the policy. Empty for ClusterRightsizePolicy.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ResourceValues holds CPU and memory quantities.
type ResourceValues struct {
	// CPU resource quantity.
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`

	// Memory resource quantity.
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
}

// JVMFlags holds the JVM flag recommendations for a Java container.
type JVMFlags struct {
	// Xmx is the maximum heap size (-Xmx).
	// +optional
	Xmx string `json:"xmx,omitempty"`

	// Xms is the initial heap size (-Xms).
	// +optional
	Xms string `json:"xms,omitempty"`

	// MaxMetaspaceSize is -XX:MaxMetaspaceSize.
	// +optional
	MaxMetaspaceSize string `json:"maxMetaspaceSize,omitempty"`

	// ReservedCodeCacheSize is -XX:ReservedCodeCacheSize.
	// +optional
	ReservedCodeCacheSize string `json:"reservedCodeCacheSize,omitempty"`

	// MaxDirectMemorySize is -XX:MaxDirectMemorySize.
	// +optional
	MaxDirectMemorySize string `json:"maxDirectMemorySize,omitempty"`
}

// JVMRecommendation contains JVM-specific observation and recommendations.
type JVMRecommendation struct {
	// Detected indicates whether this container was identified as a
	// Java application.
	Detected bool `json:"detected"`

	// AgentInjected indicates whether the Cairn JVM agent was injected.
	AgentInjected bool `json:"agentInjected"`

	// CurrentFlags are the JVM flags currently observed on the container.
	// +optional
	CurrentFlags *JVMFlags `json:"currentFlags,omitempty"`

	// RecommendedFlags are the recommended JVM flags.
	// +optional
	RecommendedFlags *JVMFlags `json:"recommendedFlags,omitempty"`

	// HeapUsedP99 is the P99 heap usage observed during the window.
	// +optional
	HeapUsedP99 *resource.Quantity `json:"heapUsedP99,omitempty"`

	// NonHeapUsedP99 is the P99 non-heap (metaspace + codecache) usage.
	// +optional
	NonHeapUsedP99 *resource.Quantity `json:"nonHeapUsedP99,omitempty"`

	// GCOverheadPercent is the percentage of time spent in GC.
	// +optional
	GCOverheadPercent *resource.Quantity `json:"gcOverheadPercent,omitempty"`

	// PeakThreadCount is the observed peak thread count.
	// +optional
	PeakThreadCount *int32 `json:"peakThreadCount,omitempty"`
}

// ContainerRecommendation holds the current and recommended resources for
// a single container.
type ContainerRecommendation struct {
	// ContainerName is the name of the container.
	ContainerName string `json:"containerName"`

	// Current holds the resources currently set on the container.
	Current corev1.ResourceRequirements `json:"current"`

	// Recommended holds the calculated resource recommendations.
	// +optional
	Recommended *corev1.ResourceRequirements `json:"recommended,omitempty"`

	// LowerBound is the minimum recommended resources. Going below this
	// risks OOMKills or CPU throttling.
	// +optional
	LowerBound *ResourceValues `json:"lowerBound,omitempty"`

	// UpperBound is the maximum recommended resources. Going above this
	// is wasteful.
	// +optional
	UpperBound *ResourceValues `json:"upperBound,omitempty"`

	// JVM contains JVM-specific recommendation data.
	// Only populated for detected Java containers.
	// +optional
	JVM *JVMRecommendation `json:"jvm,omitempty"`

	// Burst hold information about recent Burst on the container meaning spike in cpu or memory usage
	// +optional
	Burst *BurstState `json:"burst,omitempty"`
}

// SavingsEstimate holds the projected resource savings if recommendations
// are applied.
type SavingsEstimate struct {
	// CPUMillis is the total CPU savings in millicores.
	CPUMillis int64 `json:"cpuMillis,omitempty"`

	// MemoryMiB is the total memory savings in MiB.
	MemoryMiB int64 `json:"memoryMiB,omitempty"`
}

type BurstPhase string

const (
	BurstPhaseNormal   BurstPhase = "Normal"
	BurstPhaseBursting BurstPhase = "Bursting"
)

type BurstState struct {
	Phase           BurstPhase         `json:"phase"`
	BurstPeakCPU    *resource.Quantity `json:"burstPeakCPU,omitempty"`
	BurstPeakMemory *resource.Quantity `json:"burstPeakMemory,omitempty"`
	BurstStartTime  *metav1.Time       `json:"burstStartTime,omitempty"`
}

type RightsizeRecommendationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	TargetRef TargetRef       `json:"targetRef"`
	PolicyRef PolicyReference `json:"policyRef"`
}

// RightsizeRecommendationStatus defines the observed state of RightsizeRecommendation.
type RightsizeRecommendationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the RightsizeRecommendation resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Containers holds per-container recommendations.
	// +optional
	Containers []ContainerRecommendation `json:"containers,omitempty"`

	// Savings is the projected resource savings if all recommendations
	// in this object are applied.
	// +optional
	Savings *SavingsEstimate `json:"savings,omitempty"`

	// LastRecommendationTime is when the recommendation was last computed.
	// +optional
	LastRecommendationTime *metav1.Time `json:"lastRecommendationTime,omitempty"`

	// LastAppliedTime is when the recommendation was last applied to the
	// workload. Nil if never applied.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.kind`
// +kubebuilder:printcolumn:name="Workload",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=`.spec.policyRef.name`
// +kubebuilder:printcolumn:name="CPU Savings",type=integer,JSONPath=`.status.savings.cpuMillis`
// +kubebuilder:printcolumn:name="Mem Savings (MiB)",type=integer,JSONPath=`.status.savings.memoryMiB`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// RightsizeRecommendation is the Schema for the rightsizerecommendations API

type RightsizeRecommendation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of RightsizeRecommendation
	// +required
	Spec RightsizeRecommendationSpec `json:"spec"`

	// status defines the observed state of RightsizeRecommendation
	// +optional
	Status RightsizeRecommendationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RightsizeRecommendationList contains a list of RightsizeRecommendation
type RightsizeRecommendationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RightsizeRecommendation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RightsizeRecommendation{}, &RightsizeRecommendationList{})
}
