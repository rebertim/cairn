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
	"slices"
	"time"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	cairnmetrics "github.com/sempex/cairn/internal/metrics"
	"github.com/sempex/cairn/internal/recommender"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const clusterPolicyFinalizer = "clusterrightsizepolicy.cairn.io/finalizer"
const clusterPolicyLabel = "cairn.io/cluster-policy"

// ClusterRightsizePolicyReconciler reconciles a ClusterRightsizePolicy object
type ClusterRightsizePolicyReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Collector         collector.Collector
	Recommender       recommender.Recommender
	ReconcileInterval time.Duration
}

// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=clusterrightsizepolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=clusterrightsizepolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rightsizing.cairn.io,resources=clusterrightsizepolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *ClusterRightsizePolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	policy := &rightsizingv1alpha1.ClusterRightsizePolicy{}
	if err := r.Get(ctx, client.ObjectKey{Name: req.Name}, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion: clean up all owned recommendations then remove finalizer.
	if !policy.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(policy, clusterPolicyFinalizer) {
			if err := r.deleteOwnedRecommendations(ctx, policy.Name); err != nil {
				return ctrl.Result{}, err
			}
			patch := client.MergeFrom(policy.DeepCopy())
			controllerutil.RemoveFinalizer(policy, clusterPolicyFinalizer)
			if err := r.Patch(ctx, policy, patch); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the finalizer is present.
	if !controllerutil.ContainsFinalizer(policy, clusterPolicyFinalizer) {
		patch := client.MergeFrom(policy.DeepCopy())
		controllerutil.AddFinalizer(policy, clusterPolicyFinalizer)
		if err := r.Patch(ctx, policy, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Skip if disabled or suspended.
	if !policy.Spec.Enabled {
		log.Info("cluster policy not enabled, skipping")
		return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
	}
	if policy.Spec.Suspended {
		log.Info("cluster policy is suspended, skipping")
		return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
	}

	// List all namespaces and filter by selector.
	nsList := &corev1.NamespaceList{}
	if err := r.List(ctx, nsList); err != nil {
		return ctrl.Result{}, fmt.Errorf("list namespaces: %w", err)
	}

	matchedNamespaces := make([]corev1.Namespace, 0)
	for i := range nsList.Items {
		matched, err := matchesNamespaceSelector(nsList.Items[i], policy.Spec.NamespaceSelector)
		if err != nil {
			log.Error(err, "failed to evaluate namespace selector", "namespace", nsList.Items[i].Name)
			continue
		}
		if matched {
			matchedNamespaces = append(matchedNamespaces, nsList.Items[i])
		}
	}

	// Track which recommendations we still own after this reconcile pass.
	activeRecNames := make(map[string]struct{})

	totalWorkloads := int32(0)
	readyCount := int32(0)

	for _, ns := range matchedNamespaces {
		ref := policy.Spec.TargetRef
		var workloads []workloadInfo
		var err error

		if ref.Name != "" && ref.Name != "*" {
			workloads, err = getWorkloadByName(ctx, r.Client, ref.Kind, ref.Name, ns.Name)
		} else {
			workloads, err = listWorkloadsByRef(ctx, r.Client, ref, ns.Name)
		}
		if err != nil {
			log.Error(err, "failed to discover workloads", "namespace", ns.Name)
			continue
		}

		for _, wl := range workloads {
			// Check whether a namespace-scoped policy already covers this workload.
			covered, err := namespacePolicyCoversWorkload(ctx, r.Client, wl)
			if err != nil {
				log.Error(err, "failed to check namespace policy coverage", "workload", wl.Name)
				continue
			}
			if covered {
				log.V(1).Info("workload already covered by namespace policy, skipping", "workload", wl.Name, "namespace", wl.Namespace)
				continue
			}

			// Check whether a higher-priority cluster policy already claims this workload.
			claimed, err := higherPriorityClusterPolicyClaims(ctx, r.Client, wl, policy)
			if err != nil {
				log.Error(err, "failed to check cluster policy priority", "workload", wl.Name)
				continue
			}
			if claimed {
				log.V(1).Info("workload claimed by higher-priority cluster policy, skipping", "workload", wl.Name, "namespace", wl.Namespace)
				continue
			}

			// Reconcile the recommendation for this workload.
			recName := recommendationName(wl.Kind, wl.Name)
			activeRecNames[wl.Namespace+"/"+recName] = struct{}{}

			if err := r.reconcileClusterRecommendation(ctx, policy, wl); err != nil {
				log.Error(err, "failed to reconcile cluster recommendation", "workload", wl.Name, "namespace", wl.Namespace)
				continue
			}
			totalWorkloads++
			readyCount++
		}
	}

	// Delete orphaned recommendations that are no longer matched.
	if err := r.deleteOrphanedRecommendations(ctx, policy.Name, activeRecNames); err != nil {
		log.Error(err, "failed to delete orphaned recommendations")
	}

	cairnmetrics.RecordManagedWorkloads("", policy.Name, int(totalWorkloads))

	// Update status only when one of the count fields actually changed.
	// LastReconcileTime is intentionally bumped on every change so operators
	// have a heartbeat without it being a write source on idle clusters.
	countsChanged := policy.Status.TargetedNamespaces != int32(len(matchedNamespaces)) ||
		policy.Status.TargetedWorkloads != totalWorkloads ||
		policy.Status.RecommendationsReady != readyCount
	if countsChanged {
		statusPatch := client.MergeFrom(policy.DeepCopy())
		now := metav1.Now()
		policy.Status.TargetedNamespaces = int32(len(matchedNamespaces))
		policy.Status.TargetedWorkloads = totalWorkloads
		policy.Status.RecommendationsReady = readyCount
		policy.Status.LastReconcileTime = &now
		if err := r.Status().Patch(ctx, policy, statusPatch); err != nil {
			log.Error(err, "failed to update cluster policy status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: r.ReconcileInterval}, nil
}

// reconcileClusterRecommendation creates or updates a RightsizeRecommendation
// owned by the given ClusterRightsizePolicy.
func (r *ClusterRightsizePolicyReconciler) reconcileClusterRecommendation(
	ctx context.Context,
	policy *rightsizingv1alpha1.ClusterRightsizePolicy,
	wl workloadInfo,
) error {
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

		// Ensure label for cleanup queries.
		if rec.Labels == nil {
			rec.Labels = make(map[string]string)
		}
		rec.Labels[clusterPolicyLabel] = policy.Name

		rec.Spec.TargetRef = rightsizingv1alpha1.TargetRef{
			Kind: wl.Kind,
			Name: wl.Name,
		}
		rec.Spec.PolicyRef = rightsizingv1alpha1.PolicyReference{
			Kind:      "ClusterRightsizePolicy",
			Name:      policy.Name,
			Namespace: "", // cluster-scoped
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile cluster recommendation %s: %w", rec.Name, err)
	}
	if result == controllerutil.OperationResultCreated {
		cairnmetrics.InitAppliesTotal(wl.Namespace, wl.Name, wl.Kind)
	}

	// Compute fresh recommendations and only patch status when content actually
	// changed, or when DataReadySince needs to be set for the first time. This
	// is the dominant write source on the cluster, so the equality check is
	// what stops cairn from generating constant patch traffic against etcd.
	// Logic that depends on timestamps (observation window via DataReadySince,
	// apply cooldown via LastAppliedTime, burst hysteresis via BurstStartTime)
	// remains correct because those timestamps are still set/preserved when
	// they actually need to change.
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
			return fmt.Errorf("failed to update cluster recommendation status %s: %w", rec.Name, err)
		}
	}

	log.Info("reconciled cluster recommendation", "recommendation", rec.Name, "result", result, "kind", wl.Kind, "workload", wl.Name, "namespace", wl.Namespace)
	return nil
}

// deleteOwnedRecommendations removes all RightsizeRecommendations labeled
// with the given cluster policy name, across all namespaces.
func (r *ClusterRightsizePolicyReconciler) deleteOwnedRecommendations(ctx context.Context, policyName string) error {
	recList := &rightsizingv1alpha1.RightsizeRecommendationList{}
	if err := r.List(ctx, recList, client.MatchingLabels{clusterPolicyLabel: policyName}); err != nil {
		return fmt.Errorf("list owned recommendations: %w", err)
	}
	for i := range recList.Items {
		if err := r.Delete(ctx, &recList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("delete recommendation %s/%s: %w", recList.Items[i].Namespace, recList.Items[i].Name, err)
		}
	}
	return nil
}

// deleteOrphanedRecommendations removes recommendations labeled with the given
// cluster policy name that are no longer in the active set.
func (r *ClusterRightsizePolicyReconciler) deleteOrphanedRecommendations(ctx context.Context, policyName string, active map[string]struct{}) error {
	recList := &rightsizingv1alpha1.RightsizeRecommendationList{}
	if err := r.List(ctx, recList, client.MatchingLabels{clusterPolicyLabel: policyName}); err != nil {
		return fmt.Errorf("list owned recommendations: %w", err)
	}
	for i := range recList.Items {
		key := recList.Items[i].Namespace + "/" + recList.Items[i].Name
		if _, ok := active[key]; !ok {
			if err := r.Delete(ctx, &recList.Items[i]); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("delete orphaned recommendation %s: %w", key, err)
			}
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterRightsizePolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// GenerationChangedPredicate filters watch events to spec/generation changes
	// only. Without it, this controller would react to its own status patches
	// (LastReconcileTime, count fields) and form a tight self-loop. Periodic
	// requeue (RequeueAfter: r.ReconcileInterval) drives the polling work
	// instead.
	return ctrl.NewControllerManagedBy(mgr).
		For(&rightsizingv1alpha1.ClusterRightsizePolicy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("clusterrightsizepolicy").
		Complete(r)
}

// matchesNamespaceSelector returns true if the namespace matches the selector.
// A nil selector means "match all namespaces".
func matchesNamespaceSelector(ns corev1.Namespace, sel *rightsizingv1alpha1.NamespaceSelector) (bool, error) {
	if sel == nil {
		return true, nil
	}

	// Check explicit exclusions first.
	if slices.Contains(sel.ExcludeNames, ns.Name) {
		return false, nil
	}

	// If MatchNames is set, the namespace must be in the list.
	if len(sel.MatchNames) > 0 {
		if !slices.Contains(sel.MatchNames, ns.Name) {
			return false, nil
		}
		return true, nil
	}

	// If LabelSelector is set, evaluate it.
	if sel.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(sel.LabelSelector)
		if err != nil {
			return false, fmt.Errorf("invalid namespace label selector: %w", err)
		}
		return selector.Matches(labels.Set(ns.Labels)), nil
	}

	// No positive filter specified — match all (that weren't excluded).
	return true, nil
}

// namespacePolicyCoversWorkload returns true if any RightsizePolicy in the
// workload's namespace covers this workload (exact name match or wildcard).
// A suspended policy still counts as covering the workload — it acts as a
// safety lock preventing any cluster policy from touching the workload.
func namespacePolicyCoversWorkload(ctx context.Context, clt client.Client, wl workloadInfo) (bool, error) {
	policyList := &rightsizingv1alpha1.RightsizePolicyList{}
	if err := clt.List(ctx, policyList, client.InNamespace(wl.Namespace)); err != nil {
		return false, fmt.Errorf("list policies in namespace %s: %w", wl.Namespace, err)
	}
	for _, p := range policyList.Items {
		if p.Spec.TargetRef.Kind != wl.Kind {
			continue
		}

		isWildcard := p.Spec.TargetRef.Name == "*" || p.Spec.TargetRef.Name == ""
		isExactMatch := !isWildcard && p.Spec.TargetRef.Name == wl.Name

		if isExactMatch {
			// Exact match always covers (even if suspended — safety lock).
			return true, nil
		}

		if isWildcard {
			if p.Spec.TargetRef.LabelSelector != nil {
				// Evaluate selector against the workload's own labels.
				selector, err := metav1.LabelSelectorAsSelector(p.Spec.TargetRef.LabelSelector)
				if err != nil {
					return false, fmt.Errorf("invalid label selector on policy %s: %w", p.Name, err)
				}
				if !selector.Matches(labels.Set(wl.Labels)) {
					continue // wildcard with selector that doesn't match this workload
				}
			}
			return true, nil // wildcard covers this workload
		}
	}
	return false, nil
}

// higherPriorityClusterPolicyClaims returns true if another ClusterRightsizePolicy
// with a strictly higher Priority than current also matches this workload.
func higherPriorityClusterPolicyClaims(
	ctx context.Context,
	clt client.Client,
	wl workloadInfo,
	current *rightsizingv1alpha1.ClusterRightsizePolicy,
) (bool, error) {
	cpList := &rightsizingv1alpha1.ClusterRightsizePolicyList{}
	if err := clt.List(ctx, cpList); err != nil {
		return false, fmt.Errorf("list cluster policies: %w", err)
	}
	for _, cp := range cpList.Items {
		if cp.Name == current.Name {
			continue
		}
		if !cp.Spec.Enabled || cp.Spec.Suspended {
			continue
		}
		if cp.Spec.TargetRef.Kind != wl.Kind {
			continue
		}

		// Determine whether this other policy matches the workload.
		cpMatchesWl := false
		if cp.Spec.TargetRef.Name != "" && cp.Spec.TargetRef.Name != "*" {
			cpMatchesWl = cp.Spec.TargetRef.Name == wl.Name
		} else {
			// Wildcard — check whether the other policy's namespace selector covers
			// the workload's namespace.
			nsList := &corev1.NamespaceList{}
			if err := clt.List(ctx, nsList); err != nil {
				return false, fmt.Errorf("list namespaces: %w", err)
			}
			for _, ns := range nsList.Items {
				if ns.Name != wl.Namespace {
					continue
				}
				matched, err := matchesNamespaceSelector(ns, cp.Spec.NamespaceSelector)
				if err != nil {
					return false, err
				}
				if matched {
					cpMatchesWl = true
					break
				}
			}
		}

		if !cpMatchesWl {
			continue
		}

		// Strictly higher priority — current policy must yield.
		if cp.Spec.Priority > current.Spec.Priority {
			return true, nil
		}

		// Equal priority — check if the other policy already owns the recommendation.
		if cp.Spec.Priority == current.Spec.Priority {
			recName := recommendationName(wl.Kind, wl.Name)
			existing := &rightsizingv1alpha1.RightsizeRecommendation{}
			if err := clt.Get(ctx, types.NamespacedName{Name: recName, Namespace: wl.Namespace}, existing); err == nil {
				if owner := existing.Labels[clusterPolicyLabel]; owner != "" && owner != current.Name {
					return true, nil // already owned by a different cluster policy of same priority
				}
			}
		}
	}
	return false, nil
}
