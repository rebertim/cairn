package actuator

import (
	"context"
	"fmt"
	"maps"
	"strings"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InPlaceActuator patches container resources without triggering a pod restart.
// Suitable for clusters with the InPlacePodVerticalScaling feature gate (k8s 1.27+).
type InPlaceActuator struct {
	client client.Client
}

func NewInPlaceActuator(c client.Client) *InPlaceActuator {
	return &InPlaceActuator{client: c}
}

func (a *InPlaceActuator) Apply(ctx context.Context, input ApplyInput) error {
	return patchWorkload(ctx, a.client, input, "")
}

// patchWorkload patches container resources on the target workload.
// If restartAnnotation is non-empty it is written to
// spec.template.annotations["kubectl.kubernetes.io/restartedAt"] to trigger
// a rolling restart.
func patchWorkload(ctx context.Context, c client.Client, input ApplyInput, restartAnnotation string) error {
	switch input.Kind {
	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		patch := client.MergeFrom(obj.DeepCopy())
		applyResourcePatches(obj.Spec.Template.Spec.Containers, input.Containers)
		setRestartAnnotation(&obj.Spec.Template.Annotations, restartAnnotation)
		return c.Patch(ctx, obj, patch)

	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return fmt.Errorf("get statefulset: %w", err)
		}
		patch := client.MergeFrom(obj.DeepCopy())
		applyResourcePatches(obj.Spec.Template.Spec.Containers, input.Containers)
		setRestartAnnotation(&obj.Spec.Template.Annotations, restartAnnotation)
		return c.Patch(ctx, obj, patch)

	case "DaemonSet":
		obj := &appsv1.DaemonSet{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return fmt.Errorf("get daemonset: %w", err)
		}
		patch := client.MergeFrom(obj.DeepCopy())
		applyResourcePatches(obj.Spec.Template.Spec.Containers, input.Containers)
		setRestartAnnotation(&obj.Spec.Template.Annotations, restartAnnotation)
		return c.Patch(ctx, obj, patch)

	default:
		return fmt.Errorf("unsupported workload kind: %s", input.Kind)
	}
}

// applyResourcePatches merges recommended resources and JVM flags into matching containers.
// Only the resource keys present in the patch are updated; others are untouched.
func applyResourcePatches(containers []corev1.Container, patches []ContainerPatch) {
	idx := make(map[string]ContainerPatch, len(patches))
	for _, p := range patches {
		idx[p.Name] = p
	}
	for i := range containers {
		patch, ok := idx[containers[i].Name]
		if !ok {
			continue
		}
		res := patch.Resources
		if res.Requests != nil {
			if containers[i].Resources.Requests == nil {
				containers[i].Resources.Requests = make(corev1.ResourceList)
			}
			maps.Copy(containers[i].Resources.Requests, res.Requests)
		}
		if res.Limits != nil {
			if containers[i].Resources.Limits == nil {
				containers[i].Resources.Limits = make(corev1.ResourceList)
			}
			maps.Copy(containers[i].Resources.Limits, res.Limits)
		}
		if patch.JVMFlags != nil {
			applyJVMFlags(&containers[i].Env, patch.JVMFlags)
		}
	}
}

// applyJVMFlags updates JAVA_TOOL_OPTIONS with the recommended -Xmx/-Xms flags.
// Existing -Xmx/-Xms values are replaced; all other flags are preserved.
func applyJVMFlags(env *[]corev1.EnvVar, flags *rightsizingv1alpha1.JVMFlags) {
	updated := updateJVMOpts(envValue(*env, "JAVA_TOOL_OPTIONS"), flags)
	setEnvVar(env, "JAVA_TOOL_OPTIONS", updated)
}

// updateJVMOpts strips any existing -Xmx/-Xms from the opts string and appends
// the new values, preserving all other flags (e.g. -javaagent paths).
func updateJVMOpts(existing string, flags *rightsizingv1alpha1.JVMFlags) string {
	parts := strings.Fields(existing)
	filtered := parts[:0]
	for _, p := range parts {
		if !strings.HasPrefix(p, "-Xmx") && !strings.HasPrefix(p, "-Xms") {
			filtered = append(filtered, p)
		}
	}
	if flags.Xmx != "" {
		filtered = append(filtered, "-Xmx"+flags.Xmx)
	}
	if flags.Xms != "" {
		filtered = append(filtered, "-Xms"+flags.Xms)
	}
	return strings.Join(filtered, " ")
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func setEnvVar(env *[]corev1.EnvVar, name, value string) {
	for i := range *env {
		if (*env)[i].Name == name {
			(*env)[i].Value = value
			return
		}
	}
	*env = append(*env, corev1.EnvVar{Name: name, Value: value})
}

func setRestartAnnotation(target *map[string]string, value string) {
	if value == "" {
		return
	}
	if *target == nil {
		*target = make(map[string]string)
	}
	(*target)["kubectl.kubernetes.io/restartedAt"] = value
}
