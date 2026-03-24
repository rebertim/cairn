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

package controller

import (
	"context"
	"fmt"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	cairnmetrics "github.com/sempex/cairn/internal/metrics"
	"github.com/sempex/cairn/internal/recommender"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// clearClusterPolicyOwnerRef removes any owner reference pointing to a
// ClusterRightsizePolicy so a namespace-scoped policy can take ownership.
func clearClusterPolicyOwnerRef(rec *rightsizingv1alpha1.RightsizeRecommendation) {
	filtered := rec.OwnerReferences[:0]
	for _, ref := range rec.OwnerReferences {
		if ref.Kind != "ClusterRightsizePolicy" {
			filtered = append(filtered, ref)
		}
	}
	rec.OwnerReferences = filtered
	// Also remove the cluster policy label if present
	delete(rec.Labels, clusterPolicyLabel)
}

// buildContainerRecommendations computes per-container recommendations for a
// workload using the given collector and recommender. Both RightsizePolicyReconciler
// and ClusterRightsizePolicyReconciler pass their policy's CommonPolicySpec directly.
func buildContainerRecommendations(
	ctx context.Context,
	clt client.Client,
	col collector.Collector,
	rec recommender.Recommender,
	wl workloadInfo,
	policy rightsizingv1alpha1.CommonPolicySpec,
	existing []rightsizingv1alpha1.ContainerRecommendation,
) []rightsizingv1alpha1.ContainerRecommendation {
	log := logf.FromContext(ctx)

	snap := snapshotFromPod(ctx, clt, wl)
	log.Info("resolved container type", "containerType", snap.containerType, "workload", wl.Name)

	previousBurst := make(map[string]*rightsizingv1alpha1.BurstState, len(existing))
	for i := range existing {
		previousBurst[existing[i].ContainerName] = existing[i].Burst
	}

	recs := make([]rightsizingv1alpha1.ContainerRecommendation, 0, len(wl.PodSpec.Containers))
	for _, c := range wl.PodSpec.Containers {
		key := collector.ContainerKey{
			Namespace:     wl.Namespace,
			WorkloadKind:  wl.Kind,
			WorkloadName:  wl.Name,
			ContainerName: c.Name,
			ContainerType: snap.containerType,
		}
		metrics, err := col.Collect(ctx, key, policy.Window.Duration)
		if err != nil {
			log.Error(err, "failed to collect container metrics")
			continue
		}
		if metrics == nil {
			continue
		}

		result, err := rec.Recommend(ctx, recommender.RecommendInput{
			Metrics:         metrics,
			BurstConfig:     recommender.DefaultBurstConfig(),
			ContainerPolicy: policy.Containers,
			JavaPolicy:      policy.Java,
			CurrentBurst:    previousBurst[c.Name],
		})
		if err != nil {
			log.Error(err, "failed to produce recommendation", "container", c.Name)
			continue
		}

		// Use actual pod resources as "current" so metrics reflect what is
		// truly running (inplace patches and webhook-applied values differ
		// from the Deployment spec, which we never modify).
		currentResources := c.Resources
		if r, ok := snap.resources[c.Name]; ok {
			currentResources = r
		}

		containerRec := rightsizingv1alpha1.ContainerRecommendation{
			ContainerName: c.Name,
			Current:       currentResources,
			Recommended:   &result.Resources,
			Burst:         result.BurstState,
		}
		if result.JVMFlags != nil {
			containerRec.JVM = &rightsizingv1alpha1.JVMRecommendation{
				Detected:         true,
				RecommendedFlags: result.JVMFlags,
			}
		}
		recs = append(recs, containerRec)
		cairnmetrics.RecordContainerRecommendation(
			wl.Namespace, wl.Name, wl.Kind, c.Name,
			currentResources, result.Resources,
			previousBurst[c.Name], result.BurstState,
		)
	}
	return recs
}

// podSnapshot holds per-workload data read from a single running pod.
type podSnapshot struct {
	containerType string
	// resources maps container name to its actual current ResourceRequirements
	// as seen on the running pod (may differ from the Deployment spec when
	// inplace resizing or webhook injection has been applied).
	resources map[string]corev1.ResourceRequirements
}

// snapshotFromPod looks up one running pod for the workload and extracts the
// container-type annotation and per-container resource requests. Falls back to
// zero value (empty type, nil resources map) if no pod is found.
func snapshotFromPod(ctx context.Context, clt client.Client, wl workloadInfo) podSnapshot {
	if len(wl.PodSelector) == 0 {
		return podSnapshot{}
	}
	pods := &corev1.PodList{}
	if err := clt.List(ctx, pods,
		client.InNamespace(wl.Namespace),
		client.MatchingLabels(wl.PodSelector),
	); err != nil || len(pods.Items) == 0 {
		return podSnapshot{}
	}
	pod := pods.Items[0]
	res := make(map[string]corev1.ResourceRequirements, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		res[c.Name] = c.Resources
	}
	return podSnapshot{
		containerType: pod.Annotations[containerTypeAnnotation],
		resources:     res,
	}
}

// getWorkloadByName fetches a single workload by kind/name/namespace.
func getWorkloadByName(ctx context.Context, clt client.Client, kind, name, namespace string) ([]workloadInfo, error) {
	key := types.NamespacedName{Name: name, Namespace: namespace}

	switch kind {
	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := clt.Get(ctx, key, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []workloadInfo{{Kind: kind, Name: obj.Name, Namespace: obj.Namespace, Labels: obj.Labels, PodSpec: obj.Spec.Template.Spec, PodAnnotations: obj.Spec.Template.Annotations, PodSelector: obj.Spec.Selector.MatchLabels}}, nil
	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := clt.Get(ctx, key, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []workloadInfo{{Kind: kind, Name: obj.Name, Namespace: obj.Namespace, Labels: obj.Labels, PodSpec: obj.Spec.Template.Spec, PodAnnotations: obj.Spec.Template.Annotations, PodSelector: obj.Spec.Selector.MatchLabels}}, nil
	case "DaemonSet":
		obj := &appsv1.DaemonSet{}
		if err := clt.Get(ctx, key, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []workloadInfo{{Kind: kind, Name: obj.Name, Namespace: obj.Namespace, Labels: obj.Labels, PodSpec: obj.Spec.Template.Spec, PodAnnotations: obj.Spec.Template.Annotations, PodSelector: obj.Spec.Selector.MatchLabels}}, nil
	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}
}

// listWorkloadsByRef lists all workloads matching the given TargetRef in the
// specified namespace.
func listWorkloadsByRef(ctx context.Context, clt client.Client, ref rightsizingv1alpha1.TargetRef, namespace string) ([]workloadInfo, error) {
	opts := []client.ListOption{client.InNamespace(namespace)}

	if ref.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(ref.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid label selector: %w", err)
		}
		opts = append(opts, client.MatchingLabelsSelector{Selector: selector})
	}

	switch ref.Kind {
	case "Deployment":
		list := &appsv1.DeploymentList{}
		if err := clt.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		result := make([]workloadInfo, 0, len(list.Items))
		for i := range list.Items {
			result = append(result, workloadInfo{
				Kind:           "Deployment",
				Name:           list.Items[i].Name,
				Namespace:      list.Items[i].Namespace,
				Labels:         list.Items[i].Labels,
				PodSpec:        list.Items[i].Spec.Template.Spec,
				PodAnnotations: list.Items[i].Spec.Template.Annotations,
				PodSelector:    list.Items[i].Spec.Selector.MatchLabels,
			})
		}
		return result, nil

	case "StatefulSet":
		list := &appsv1.StatefulSetList{}
		if err := clt.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		result := make([]workloadInfo, 0, len(list.Items))
		for i := range list.Items {
			result = append(result, workloadInfo{
				Kind:           "StatefulSet",
				Name:           list.Items[i].Name,
				Namespace:      list.Items[i].Namespace,
				Labels:         list.Items[i].Labels,
				PodSpec:        list.Items[i].Spec.Template.Spec,
				PodAnnotations: list.Items[i].Spec.Template.Annotations,
				PodSelector:    list.Items[i].Spec.Selector.MatchLabels,
			})
		}
		return result, nil

	case "DaemonSet":
		list := &appsv1.DaemonSetList{}
		if err := clt.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		result := make([]workloadInfo, 0, len(list.Items))
		for i := range list.Items {
			result = append(result, workloadInfo{
				Kind:           "DaemonSet",
				Name:           list.Items[i].Name,
				Namespace:      list.Items[i].Namespace,
				Labels:         list.Items[i].Labels,
				PodSpec:        list.Items[i].Spec.Template.Spec,
				PodAnnotations: list.Items[i].Spec.Template.Annotations,
				PodSelector:    list.Items[i].Spec.Selector.MatchLabels,
			})
		}
		return result, nil

	default:
		return nil, fmt.Errorf("unsupported kind: %s", ref.Kind)
	}
}
