package actuator

import (
	"context"
	"fmt"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// InPlaceActuator patches running pod resources directly without touching the
// workload spec. Requires InPlacePodVerticalScaling feature gate (k8s 1.27+).
// JVM flag changes are skipped — env vars cannot be mutated in-place; they are
// applied by the mutating webhook on the next pod creation.
type InPlaceActuator struct {
	client client.Client
}

func NewInPlaceActuator(c client.Client) *InPlaceActuator {
	return &InPlaceActuator{client: c}
}

func (a *InPlaceActuator) Apply(ctx context.Context, input ApplyInput) error {
	log := logf.FromContext(ctx).WithValues("workload", input.Name, "namespace", input.Namespace)

	selector, err := workloadSelector(ctx, a.client, input)
	if err != nil {
		return err
	}

	podList := &corev1.PodList{}
	if err := a.client.List(ctx, podList,
		client.InNamespace(input.Namespace),
		client.MatchingLabels(selector),
	); err != nil {
		return fmt.Errorf("list pods: %w", err)
	}

	idx := make(map[string]ContainerPatch, len(input.Containers))
	for _, p := range input.Containers {
		idx[p.Name] = p
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		original := pod.DeepCopy()
		for j := range pod.Spec.Containers {
			patch, ok := idx[pod.Spec.Containers[j].Name]
			if !ok {
				continue
			}
			res := patch.Resources
			if res.Requests != nil {
				if pod.Spec.Containers[j].Resources.Requests == nil {
					pod.Spec.Containers[j].Resources.Requests = make(corev1.ResourceList)
				}
				maps.Copy(pod.Spec.Containers[j].Resources.Requests, res.Requests)
			}
			if res.Limits != nil {
				if pod.Spec.Containers[j].Resources.Limits == nil {
					pod.Spec.Containers[j].Resources.Limits = make(corev1.ResourceList)
				}
				maps.Copy(pod.Spec.Containers[j].Resources.Limits, res.Limits)
			}
		}
		if err := a.client.Patch(ctx, pod, client.MergeFrom(original)); err != nil {
			log.Error(err, "failed to patch pod in-place, skipping", "pod", pod.Name)
			continue
		}
		log.Info("patched pod in-place", "pod", pod.Name)
	}
	return nil
}

// workloadSelector returns the pod label selector for the given workload.
func workloadSelector(ctx context.Context, c client.Client, input ApplyInput) (map[string]string, error) {
	switch input.Kind {
	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return nil, fmt.Errorf("get deployment: %w", err)
		}
		return obj.Spec.Selector.MatchLabels, nil
	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return nil, fmt.Errorf("get statefulset: %w", err)
		}
		return obj.Spec.Selector.MatchLabels, nil
	case "DaemonSet":
		obj := &appsv1.DaemonSet{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return nil, fmt.Errorf("get daemonset: %w", err)
		}
		return obj.Spec.Selector.MatchLabels, nil
	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", input.Kind)
	}
}

// patchWorkload writes the restartedAt annotation on the workload's pod template
// to trigger a rolling restart. Resources are not modified here — the mutating
// webhook applies the latest recommendation to each pod at creation time.
func patchWorkload(ctx context.Context, c client.Client, input ApplyInput, restartAnnotation string) error {
	switch input.Kind {
	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		patch := client.MergeFrom(obj.DeepCopy())
		setRestartAnnotation(&obj.Spec.Template.Annotations, restartAnnotation)
		return c.Patch(ctx, obj, patch)

	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return fmt.Errorf("get statefulset: %w", err)
		}
		patch := client.MergeFrom(obj.DeepCopy())
		setRestartAnnotation(&obj.Spec.Template.Annotations, restartAnnotation)
		return c.Patch(ctx, obj, patch)

	case "DaemonSet":
		obj := &appsv1.DaemonSet{}
		if err := c.Get(ctx, types.NamespacedName{Name: input.Name, Namespace: input.Namespace}, obj); err != nil {
			return fmt.Errorf("get daemonset: %w", err)
		}
		patch := client.MergeFrom(obj.DeepCopy())
		setRestartAnnotation(&obj.Spec.Template.Annotations, restartAnnotation)
		return c.Patch(ctx, obj, patch)

	default:
		return fmt.Errorf("unsupported workload kind: %s", input.Kind)
	}
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
