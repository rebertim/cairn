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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	Labels         map[string]string // workload's own labels (not pod labels)
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

	// Only patch policy status when one of the count fields changed. Combined
	// with GenerationChangedPredicate on For(), this prevents a self-loop where
	// LastReconcileTime updates would re-trigger this reconciler.
	countsChanged := policy.Status.TargetedWorkloads != int32(len(workloads)) ||
		policy.Status.RecommendationsReady != readyCount
	if countsChanged {
		patch := client.MergeFrom(policy.DeepCopy())
		now := metav1.Now()
		policy.Status.TargetedWorkloads = int32(len(workloads))
		policy.Status.RecommendationsReady = readyCount
		policy.Status.LastReconcileTime = &now

		if err := r.Status().Patch(ctx, policy, patch); err != nil {
			log.Error(err, "failed to update policy status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RightsizePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// GenerationChangedPredicate on For() prevents this controller from reacting
	// to its own status patches. The Owns predicate already filters out status
	// changes on owned recommendations. Periodic requeue drives the polling.
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.RightsizePolicy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
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
		// Transfer ownership from ClusterRightsizePolicy to this namespace policy.
		// The cluster controller will stop managing this workload on its next reconcile
		// once it detects a namespace policy covers it.
		clearClusterPolicyOwnerRef(rec)

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

	// Compute fresh recommendations and only patch when content actually changed
	// or when DataReadySince needs to be set for the first time. See
	// clusterrightsizepolicy_controller.go for the rationale.
	newContainers := buildContainerRecommendations(ctx, r.Client, r.Collector, r.Recommender, wl, policy.Spec.CommonPolicySpec, rec.Status.Containers)
	contentChanged := !equality.Semantic.DeepEqual(rec.Status.Containers, newContainers)
	needsDataReadySet := rec.Status.DataReadySince == nil && len(newContainers) > 0

	if contentChanged || needsDataReadySet {
		recPatch := client.MergeFrom(rec.DeepCopy())
		rec.Status.Containers = newContainers
		now := metav1.Now()
		if needsDataReadySet {
			rec.Status.DataReadySince = &now
		}
		if contentChanged {
			rec.Status.LastRecommendationTime = &now
		}
		if err := r.Status().Patch(ctx, rec, recPatch); err != nil {
			return fmt.Errorf("failed to update recommendation status: %s: %w", rec.Name, err)
		}

		// Nudge the recommendation controller to evaluate immediately rather
		// than waiting up to ReconcileInterval. Writing a unique annotation
		// value bumps resourceVersion and triggers AnnotationChangedPredicate.
		annBase := rec.DeepCopy()
		if rec.Annotations == nil {
			rec.Annotations = make(map[string]string)
		}
		rec.Annotations["cairn.io/pending-apply"] = fmt.Sprintf("%d", now.UnixNano())
		if err := r.Patch(ctx, rec, client.MergeFrom(annBase)); err != nil {
			log.Error(err, "failed to set pending-apply annotation, will retry on next reconcile")
		}
	}

	log.Info("reconciled recommendation", "recommendation", rec.Name, "result", result, "kind", wl.Kind, "workload", wl.Name)
	return nil
}

// containerTypeAnnotation is the pod template annotation that signals which
// recommender to use for all containers in the pod.
const containerTypeAnnotation = "cairn.io/container-type"

func (r *RightsizePolicyReconciler) discoverWorkloads(ctx context.Context, policy *rightsizingv1alpha1.RightsizePolicy) ([]workloadInfo, error) {
	ref := policy.Spec.TargetRef

	var workloads []workloadInfo
	var err error
	if ref.Name != "" && ref.Name != "*" {
		workloads, err = getWorkloadByName(ctx, r.Client, ref.Kind, ref.Name, policy.Namespace)
	} else {
		workloads, err = listWorkloadsByRef(ctx, r.Client, ref, policy.Namespace)
	}
	if err != nil {
		return nil, err
	}
	return filterByContainerType(workloads, ref.ContainerType), nil
}
