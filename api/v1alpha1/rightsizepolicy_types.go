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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RightsizePolicySpec defines the desired state of RightsizePolicy

type PolicyMode string

const (
	PolicyModeRecommended PolicyMode = "recommend"
	PolicyModeDryRun      PolicyMode = "dry-run"
	PolicyModeAuto        PolicyMode = "auto"
)

type UpdateStrategy string

const (
	UpdateStrategyInPlace UpdateStrategy = "in-place"
	UpdateStrategyRestart UpdateStrategy = "restart"
)

type JVMFlagMethod string

const (
	JVMFlagMethodEnv        JVMFlagMethod = "env"
	JVMFlagMethodAnnotation JVMFlagMethod = "annotation"
)

// TargetRef identifies the workload(s) to rightsize.
type TargetRef struct {
	// Kind of the target workload.
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet;Rollout
	Kind string `json:"kind"`
	// Name of the target workload. Use "*" to match all workloads of the
	// specified kind in the namespace.
	// +kubebuilder:default="*"
	Name string `json:"name"`
	// LabelSelector further filters which workloads to target.
	// Only used when Name is "*".
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

type ContainerResourcePolicy struct {
	// Percentile of usage to base the recommendation on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=95
	Percentile int32 `json:"percentile,omitempty"`

	// HeadroomPercent is the additional headroom to add on top of the
	// calculated percentile, as a percentage.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=200
	// +kubebuilder:default=15
	HeadroomPercent int32 `json:"headroomPercent,omitempty"`

	// MinRequest is the floor for the recommended request.
	// +optional
	MinRequest *resource.Quantity `json:"minRequest,omitempty"`

	// MaxRequest is the ceiling for the recommended request.
	// +optional
	MaxRequest *resource.Quantity `json:"maxRequest,omitempty"`
}

// ContainerPolicies defines the resource policies for CPU and memory.
type ContainerPolicies struct {
	// CPU resource policy.
	// +optional
	CPU *ContainerResourcePolicy `json:"cpu,omitempty"`

	// Memory resource policy.
	// +optional
	Memory *ContainerResourcePolicy `json:"memory,omitempty"`
}

// JavaPolicy configures JVM-aware rightsizing.
type JavaPolicy struct {
	// Enabled toggles JVM-aware rightsizing for detected Java containers.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// InjectAgent controls whether the JVM metrics agent is automatically
	// injected into Java containers via mutating webhook.
	// +kubebuilder:default=true
	InjectAgent bool `json:"injectAgent,omitempty"`

	// HeapHeadroomPercent is the headroom to add on top of observed heap usage.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=200
	// +kubebuilder:default=15
	HeapHeadroomPercent int32 `json:"heapHeadroomPercent,omitempty"`

	// PinHeapMinMax sets -Xms equal to -Xmx for predictable memory behavior.
	// +kubebuilder:default=true
	PinHeapMinMax bool `json:"pinHeapMinMax,omitempty"`

	// GCOverheadWeight controls how much GC pressure inflates the CPU
	// recommendation. 0 disables the factor, 1.0 is the default weight.
	// +kubebuilder:default="1.0"
	// +optional
	GCOverheadWeight *resource.Quantity `json:"gcOverheadWeight,omitempty"`

	// ManageJVMFlags enables recommending and applying JVM flags
	// (-Xmx, -XX:MaxMetaspaceSize, etc.).
	// +kubebuilder:default=false
	ManageJVMFlags bool `json:"manageJvmFlags,omitempty"`

	// FlagMethod controls how JVM flags are delivered to containers.
	// +kubebuilder:default=env
	FlagMethod JVMFlagMethod `json:"flagMethod,omitempty"`
}

// CommonPolicySpec contains all policy settings shared between RightsizePolicy
// and ClusterRightsizePolicy. It is embedded inline in both specs so the CRD
// schema and JSON representation remain flat and unchanged.
type CommonPolicySpec struct {
	// TargetRef identifies which workloads this policy applies to.
	TargetRef TargetRef `json:"targetRef"`

	// +kubebuilder:default=recommend
	Mode PolicyMode `json:"mode,omitempty"`

	// UpdateStrategy defines how changes are applied when mode is "auto".
	// +kubebuilder:default=restart
	UpdateStrategy UpdateStrategy `json:"updateStrategy,omitempty"`

	// Containers defines the resource policies for CPU and memory.
	// +optional
	Containers *ContainerPolicies `json:"containers,omitempty"`

	// Java configures JVM-aware rightsizing.
	// +optional
	Java *JavaPolicy `json:"java,omitempty"`

	// Window is the lookback duration for metrics aggregation.
	// +kubebuilder:default="168h"
	Window metav1.Duration `json:"window,omitempty"`

	// ChangeThreshold is the minimum percentage change between current and
	// recommended resources required to trigger an apply. Avoids churn
	// from insignificant fluctuations.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=10
	ChangeThreshold int32 `json:"changeThreshold,omitempty"`

	// MinApplyInterval is the minimum time between consecutive applies for a
	// workload. Prevents rapid-fire restarts when the recommendation keeps
	// changing during a load spike. Defaults to 5 minutes.
	// +optional
	MinApplyInterval metav1.Duration `json:"minApplyInterval,omitempty"`

	// MinObservationWindow is how long data must have been collected before
	// the first auto apply is allowed. Prevents premature right-sizing based
	// on an incomplete metrics window. Defaults to 24 hours.
	// +optional
	MinObservationWindow metav1.Duration `json:"minObservationWindow,omitempty"`

	// Suspended pauses all rightsizing activity for this policy.
	// +kubebuilder:default=false
	Suspended bool `json:"suspended,omitempty"`
}

type RightsizePolicySpec struct {
	CommonPolicySpec `json:",inline"`
}

// RightsizePolicyStatus defines the observed state of RightsizePolicy.
type RightsizePolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the RightsizePolicy resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TargetedWorkloads is the number of workloads matched by this policy.
	TargetedWorkloads int32 `json:"targetedWorkloads,omitempty"`

	// RecommendationsReady is the number of workloads with ready recommendations.
	RecommendationsReady int32 `json:"recommendationsReady,omitempty"`

	// LastReconcileTime is the timestamp of the last successful reconciliation.
	// +optional
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=`.spec.mode`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.kind`
// +kubebuilder:printcolumn:name="Workloads",type=integer,JSONPath=`.status.targetedWorkloads`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.recommendationsReady`
// +kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspended`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RightsizePolicy is the Schema for the rightsizepolicies API
type RightsizePolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of RightsizePolicy
	// +required
	Spec RightsizePolicySpec `json:"spec"`

	// status defines the observed state of RightsizePolicy
	// +optional
	Status RightsizePolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RightsizePolicyList contains a list of RightsizePolicy
type RightsizePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RightsizePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RightsizePolicy{}, &RightsizePolicyList{})
}
