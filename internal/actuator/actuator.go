package actuator

import (
	"context"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// ContainerPatch describes the resource changes to apply to a single container.
type ContainerPatch struct {
	Name      string
	Resources corev1.ResourceRequirements
	// JVMFlags, when non-nil, are applied to JAVA_TOOL_OPTIONS on the container.
	JVMFlags *rightsizingv1alpha1.JVMFlags
}

// ApplyInput bundles the workload identity and per-container resource patches.
type ApplyInput struct {
	Kind       string
	Name       string
	Namespace  string
	Containers []ContainerPatch
}

// Actuator applies resource patches to a workload.
type Actuator interface {
	Apply(ctx context.Context, input ApplyInput) error
}
