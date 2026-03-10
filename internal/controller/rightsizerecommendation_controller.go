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
	"github.com/sempex/cairn/internal/actuator"
	cairnmetrics "github.com/sempex/cairn/internal/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RightsizeRecommendationReconciler reconciles a RightsizeRecommendation object.
// It delegates all decision-making to the actuator.Engine; the controller only
// fetches the relevant objects and writes the engine result back to status.
type RightsizeRecommendationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Engine *actuator.Engine
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

	policy := &rightsizingv1alpha1.RightsizePolicy{}
	policyKey := types.NamespacedName{
		Name:      rec.Spec.PolicyRef.Name,
		Namespace: rec.Spec.PolicyRef.Namespace,
	}
	if err := r.Get(ctx, policyKey, policy); err != nil {
		return ctrl.Result{}, fmt.Errorf("fetch policy %s: %w", policyKey, err)
	}

	if policy.Spec.Suspended {
		return ctrl.Result{}, nil
	}

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
		patch := client.MergeFrom(rec.DeepCopy())
		now := metav1.Now()
		rec.Status.LastAppliedTime = &now
		if err := r.Status().Patch(ctx, rec, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RightsizeRecommendationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.RightsizeRecommendation{}).
		Named("rightsizerecommendation").
		Complete(r)
}
