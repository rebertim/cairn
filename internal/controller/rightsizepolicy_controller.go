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
	"strings"
	"time"

	"github.com/sempex/cairn/internal/collector"
	cairnmetrics "github.com/sempex/cairn/internal/metrics"
	"github.com/sempex/cairn/internal/recommender"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
)

// RightsizePolicyReconciler reconciles a RightsizePolicy object
type RightsizePolicyReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Collector         collector.Collector
	Recommender       recommender.Recommender
	ReconcileInterval time.Duration // how often to poll for burst detection
}

// workloadInfo holds the resolved information for a discovered workload.
type workloadInfo struct {
	Kind           string
	Name           string
	Namespace      string
	PodSpec        corev1.PodSpec
	PodAnnotations map[string]string
	PodSelector    map[string]string // label selector to find running pods
}

// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizepolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizerecommendations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizerecommendations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RightsizePolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/reconcile
func (r *RightsizePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	policy := &rightsizingv1alpha1.RightsizePolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	defer cairnmetrics.ReconcileTimer(req.Namespace, req.Name)()

	// skip if suspended
	if policy.Spec.Suspended {
		log.Info("policy is suspended, skipping")
		return ctrl.Result{}, nil
	}

	workloads, err := r.discoverWorkloads(ctx, policy)
	if err != nil {
		log.Error(err, "failed to discover target workloads")
		return ctrl.Result{}, err
	}

	log.Info("discovered target workloads", "count", len(workloads))

	// Reconcile a RightsizeReccomendation for each workload.
	readyCount := int32(0)
	for _, wl := range workloads {
		if err := r.reconcileRecommendation(ctx, policy, wl); err != nil {
			log.Error(err, "failed to reconcile recommendation", "kind", wl.Kind, "workload", wl.Name)
			return ctrl.Result{}, err
		}
		readyCount++
	}
	cairnmetrics.RecordManagedWorkloads(req.Namespace, req.Name, len(workloads))

	// Update policy status.
	patch := client.MergeFrom(policy.DeepCopy())
	now := metav1.Now()
	policy.Status.TargetedWorkloads = int32(len(workloads))
	policy.Status.RecommendationsReady = readyCount
	policy.Status.LastReconcileTime = &now

	if err := r.Status().Patch(ctx, policy, patch); err != nil {
		log.Error(err, "failed to update policy status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RightsizePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.RightsizePolicy{}).
		Owns(&rightsizingv1alpha1.RightsizeRecommendation{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("rightsizepolicy").
		Complete(r)
}

// recommendationName generates a deterministic name for a recommendation based
// on the workload kind and name.
func recommendationName(kind, name string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(kind), name)
}

func (r *RightsizePolicyReconciler) reconcileRecommendation(ctx context.Context, policy *rightsizingv1alpha1.RightsizePolicy, wl workloadInfo) error {
	log := logf.FromContext(ctx)

	rec := &rightsizingv1alpha1.RightsizeRecommendation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      recommendationName(wl.Kind, wl.Name),
			Namespace: wl.Namespace,
		},
	}
	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, rec, func() error {
		if err := controllerutil.SetControllerReference(policy, rec, r.Scheme); err != nil {
			return err
		}

		rec.Spec.TargetRef = rightsizingv1alpha1.TargetRef{
			Kind: wl.Kind,
			Name: wl.Name,
		}
		rec.Spec.PolicyRef = rightsizingv1alpha1.PolicyReference{
			Kind:      "RightsizePolicy",
			Name:      policy.Name,
			Namespace: policy.Namespace,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile recommendation: %s: %w", rec.Name, err)
	}
	if result == controllerutil.OperationResultCreated {
		cairnmetrics.InitAppliesTotal(wl.Namespace, wl.Name, wl.Kind)
	}

	recPatch := client.MergeFrom(rec.DeepCopy())
	rec.Status.Containers = r.buildContainerRecomendations(ctx, wl, policy, rec.Status.Containers)
	now := metav1.Now()
	rec.Status.LastRecommendationTime = &now
	if err := r.Status().Patch(ctx, rec, recPatch); err != nil {
		return fmt.Errorf("failed to update recommendation status: %s: %w", rec.Name, err)
	}

	log.Info("reconciled recommendation", "recommendation", rec.Name, "result", result, "kind", wl.Kind, "workload", wl.Name)
	return nil
}

// containerTypeAnnotation is the pod template annotation that signals which
// recommender to use for all containers in the pod.
const containerTypeAnnotation = "cairn.io/container-type"

func (r *RightsizePolicyReconciler) buildContainerRecomendations(ctx context.Context, wl workloadInfo, policy *rightsizingv1alpha1.RightsizePolicy, existing []rightsizingv1alpha1.ContainerRecommendation) []rightsizingv1alpha1.ContainerRecommendation {
	log := logf.FromContext(ctx)

	// Read containerType from a running pod's annotations (set by the webhook).
	// This is more reliable than the deployment template annotations, which the
	// mutating webhook cannot update.
	containerType := r.containerTypeFromPod(ctx, wl)
	log.Info("resolved container type", "containerType", containerType, "workload", wl.Name)

	// Index the previous reconcile's burst state by container name so the
	// engine can continue the state machine rather than starting fresh.
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
			ContainerType: containerType,
		}
		metrics, err := r.Collector.Collect(ctx, key, policy.Spec.Window.Duration)
		if err != nil {
			log.Error(err, "failed to collect container metrics")
			continue
		}

		result, err := r.Recommender.Recommend(ctx, recommender.RecommendInput{
			Metrics:         metrics,
			BurstConfig:     recommender.DefaultBurstConfig(),
			ContainerPolicy: policy.Spec.Containers,
			JavaPolicy:      policy.Spec.Java,
			CurrentBurst:    previousBurst[c.Name],
		})
		if err != nil {
			log.Error(err, "failed to produce recommendation", "container", c.Name)
			continue
		}

		containerRec := rightsizingv1alpha1.ContainerRecommendation{
			ContainerName: c.Name,
			Current:       c.Resources,
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
			c.Resources, result.Resources,
			previousBurst[c.Name], result.BurstState,
		)
	}
	return recs
}

func (r *RightsizePolicyReconciler) discoverWorkloads(ctx context.Context, policy *rightsizingv1alpha1.RightsizePolicy) ([]workloadInfo, error) {
	ref := policy.Spec.TargetRef

	if ref.Name != "" && ref.Name != "*" {
		return r.getWorkloadByName(ctx, ref.Kind, ref.Name, policy.Namespace)
	}

	return r.listWorkloads(ctx, ref, policy.Namespace)
}

func (r *RightsizePolicyReconciler) getWorkloadByName(ctx context.Context, kind, name, namespace string) ([]workloadInfo, error) {
	key := types.NamespacedName{Name: name, Namespace: namespace}

	switch kind {
	case "Deployment":
		obj := &appsv1.Deployment{}
		if err := r.Get(ctx, key, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []workloadInfo{{Kind: kind, Name: obj.Name, Namespace: obj.Namespace, PodSpec: obj.Spec.Template.Spec, PodAnnotations: obj.Spec.Template.Annotations, PodSelector: obj.Spec.Selector.MatchLabels}}, nil
	case "StatefulSet":
		obj := &appsv1.StatefulSet{}
		if err := r.Get(ctx, key, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []workloadInfo{{Kind: kind, Name: obj.Name, Namespace: obj.Namespace, PodSpec: obj.Spec.Template.Spec, PodAnnotations: obj.Spec.Template.Annotations, PodSelector: obj.Spec.Selector.MatchLabels}}, nil
	case "DaemonSet":
		obj := &appsv1.DaemonSet{}
		if err := r.Get(ctx, key, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []workloadInfo{{Kind: kind, Name: obj.Name, Namespace: obj.Namespace, PodSpec: obj.Spec.Template.Spec, PodAnnotations: obj.Spec.Template.Annotations, PodSelector: obj.Spec.Selector.MatchLabels}}, nil
	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}
}

func (r *RightsizePolicyReconciler) listWorkloads(
	ctx context.Context,
	ref rightsizingv1alpha1.TargetRef,
	namespace string,
) ([]workloadInfo, error) {
	opts := []client.ListOption{client.InNamespace(namespace)}

	// Convert metav1.LabelSelector to a labels.Selector for filtering.
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
		if err := r.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		result := make([]workloadInfo, 0, len(list.Items))
		for i := range list.Items {
			result = append(result, workloadInfo{
				Kind:           "Deployment",
				Name:           list.Items[i].Name,
				Namespace:      list.Items[i].Namespace,
				PodSpec:        list.Items[i].Spec.Template.Spec,
				PodAnnotations: list.Items[i].Spec.Template.Annotations,
				PodSelector:    list.Items[i].Spec.Selector.MatchLabels,
			})
		}
		return result, nil

	case "StatefulSet":
		list := &appsv1.StatefulSetList{}
		if err := r.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		result := make([]workloadInfo, 0, len(list.Items))
		for i := range list.Items {
			result = append(result, workloadInfo{
				Kind:           "StatefulSet",
				Name:           list.Items[i].Name,
				Namespace:      list.Items[i].Namespace,
				PodSpec:        list.Items[i].Spec.Template.Spec,
				PodAnnotations: list.Items[i].Spec.Template.Annotations,
				PodSelector:    list.Items[i].Spec.Selector.MatchLabels,
			})
		}
		return result, nil

	case "DaemonSet":
		list := &appsv1.DaemonSetList{}
		if err := r.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		result := make([]workloadInfo, 0, len(list.Items))
		for i := range list.Items {
			result = append(result, workloadInfo{
				Kind:           "DaemonSet",
				Name:           list.Items[i].Name,
				Namespace:      list.Items[i].Namespace,
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

// containerTypeFromPod looks up a running pod for the workload and reads the
// cairn.io/container-type annotation set by the mutating webhook at admission.
// Falls back to empty string (standard recommender) if no pod is found.
func (r *RightsizePolicyReconciler) containerTypeFromPod(ctx context.Context, wl workloadInfo) string {
	if len(wl.PodSelector) == 0 {
		return ""
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(wl.Namespace),
		client.MatchingLabels(wl.PodSelector),
	); err != nil || len(pods.Items) == 0 {
		return ""
	}
	return pods.Items[0].Annotations[containerTypeAnnotation]
}
