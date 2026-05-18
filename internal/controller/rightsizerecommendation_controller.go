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
	"time"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/actuator"
	"github.com/sempex/cairn/internal/collector"
	cairnmetrics "github.com/sempex/cairn/internal/metrics"
	"github.com/sempex/cairn/internal/recommender"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// RightsizeRecommendationReconciler reconciles a RightsizeRecommendation object.
// It owns the full recommendation lifecycle: collecting metrics, computing
// recommendations, writing status, and applying changes via the actuator engine.
// Each recommendation reconciles independently, so the work queue naturally
// spreads metric queries across the cluster rather than bursting them all at once.
type RightsizeRecommendationReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Collector   collector.Collector
	Recommender recommender.Recommender
	Engine      *actuator.Engine

	// ReconcileInterval controls how often each recommendation is re-evaluated.
	// Because every recommendation requeues itself independently, this also
	// controls the steady-state query rate against VictoriaMetrics.
	ReconcileInterval time.Duration
}

// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizerecommendations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizerecommendations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=rightsizerecommendations/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch;update;patch

func (r *RightsizeRecommendationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	rec := &rightsizingv1alpha1.RightsizeRecommendation{}
	if err := r.Get(ctx, req.NamespacedName, rec); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	policy, err := r.resolvePolicy(ctx, rec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if policy == nil {
		// Policy deleted; recommendation will be GC'd via owner reference.
		return ctrl.Result{}, nil
	}
	if policy.Spec.Suspended {
		return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
	}

	// Resolve the live workload so we have the current container specs and pod selector.
	workloads, err := getWorkloadByName(ctx, r.Client, rec.Spec.TargetRef.Kind, rec.Spec.TargetRef.Name, rec.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolve workload: %w", err)
	}
	if len(workloads) == 0 {
		// Workload gone; recommendation will be GC'd by the policy controller.
		return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
	}
	wl := workloads[0]

	// Compute fresh recommendations from VictoriaMetrics.
	newContainers := buildContainerRecommendations(ctx, r.Client, r.Collector, r.Recommender, wl, policy.Spec.CommonPolicySpec, rec.Status.Containers)
	contentChanged := !equality.Semantic.DeepEqual(rec.Status.Containers, newContainers)
	needsDataReadySet := rec.Status.DataReadySince == nil && len(newContainers) > 0

	if contentChanged || needsDataReadySet {
		patch := client.MergeFrom(rec.DeepCopy())
		rec.Status.Containers = newContainers
		now := metav1.Now()
		if needsDataReadySet {
			rec.Status.DataReadySince = &now
		}
		if contentChanged {
			rec.Status.LastRecommendationTime = &now
		}
		if err := r.Status().Patch(ctx, rec, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Run the actuator engine — checks observation window, change threshold,
	// cooldown, and applies if all conditions are met.
	result, err := r.Engine.Apply(ctx, actuator.EngineInput{
		Recommendation: rec,
		Policy:         policy,
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if result.Applied {
		cairnmetrics.RecordApply(
			rec.Namespace,
			rec.Spec.TargetRef.Name,
			rec.Spec.TargetRef.Kind,
			string(policy.Spec.UpdateStrategy),
		)
		applyPatch := client.MergeFrom(rec.DeepCopy())
		now := metav1.Now()
		rec.Status.LastAppliedTime = &now
		if err := r.Status().Patch(ctx, rec, applyPatch); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
}

// resolvePolicy fetches the policy referenced by the recommendation. If the
// policy kind is ClusterRightsizePolicy it is synthesised into a RightsizePolicy
// so the actuator engine can be called unchanged.
func (r *RightsizeRecommendationReconciler) resolvePolicy(
	ctx context.Context,
	rec *rightsizingv1alpha1.RightsizeRecommendation,
) (*rightsizingv1alpha1.RightsizePolicy, error) {
	ref := rec.Spec.PolicyRef

	switch ref.Kind {
	case "ClusterRightsizePolicy":
		cp := &rightsizingv1alpha1.ClusterRightsizePolicy{}
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name}, cp); err != nil {
			return nil, client.IgnoreNotFound(err)
		}
		return synthPolicyFromCluster(cp), nil

	default: // "RightsizePolicy" or unset (backwards compat)
		policy := &rightsizingv1alpha1.RightsizePolicy{}
		policyKey := types.NamespacedName{
			Name:      ref.Name,
			Namespace: ref.Namespace,
		}
		if err := r.Get(ctx, policyKey, policy); err != nil {
			return nil, fmt.Errorf("fetch policy %s: %w", policyKey, err)
		}
		return policy, nil
	}
}

// synthPolicyFromCluster converts a ClusterRightsizePolicy into a RightsizePolicy
// by copying the fields that overlap. This lets the actuator.Engine be called
// without any changes.
func synthPolicyFromCluster(cp *rightsizingv1alpha1.ClusterRightsizePolicy) *rightsizingv1alpha1.RightsizePolicy {
	return &rightsizingv1alpha1.RightsizePolicy{
		Spec: rightsizingv1alpha1.RightsizePolicySpec{
			CommonPolicySpec: cp.Spec.CommonPolicySpec,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RightsizeRecommendationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// GenerationChangedPredicate prevents the controller from reacting to its
	// own status patches (LastAppliedTime, LastRecommendationTime etc.) which
	// would otherwise cause tight write loops. Periodic requeue drives work.
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.RightsizeRecommendation{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("rightsizerecommendation").
		Complete(r)
}
