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
	"context"
	"fmt"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SetupClusterRightsizePolicyWebhookWithManager registers the validating webhook
// for ClusterRightsizePolicy in the manager.
func SetupClusterRightsizePolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &rightsizingv1alpha1.ClusterRightsizePolicy{}).
		WithValidator(&ClusterRightsizePolicyCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-rightsizing-cairn-io-v1alpha1-clusterrightsizepolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=rightsizing.cairn.io,resources=clusterrightsizepolicies,verbs=create;update,versions=v1alpha1,name=vclusterrightsizepolicy-v1alpha1.kb.io,admissionReviewVersions=v1

// ClusterRightsizePolicyCustomValidator validates ClusterRightsizePolicy resources
// to prevent conflicting policies across the cluster.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type ClusterRightsizePolicyCustomValidator struct {
	Client client.Client
}

// ValidateCreate validates a new ClusterRightsizePolicy.
func (v *ClusterRightsizePolicyCustomValidator) ValidateCreate(ctx context.Context, obj *rightsizingv1alpha1.ClusterRightsizePolicy) (admission.Warnings, error) {
	return nil, v.validatePolicy(ctx, obj, "")
}

// ValidateUpdate validates an updated ClusterRightsizePolicy.
func (v *ClusterRightsizePolicyCustomValidator) ValidateUpdate(ctx context.Context, _, newObj *rightsizingv1alpha1.ClusterRightsizePolicy) (admission.Warnings, error) {
	return nil, v.validatePolicy(ctx, newObj, newObj.Name)
}

// ValidateDelete does not restrict deletion.
func (v *ClusterRightsizePolicyCustomValidator) ValidateDelete(_ context.Context, _ *rightsizingv1alpha1.ClusterRightsizePolicy) (admission.Warnings, error) {
	return nil, nil
}

// validatePolicy checks the new/updated policy against all existing ClusterRightsizePolicies
// (excluding self by selfName on update).
func (v *ClusterRightsizePolicyCustomValidator) validatePolicy(ctx context.Context, policy *rightsizingv1alpha1.ClusterRightsizePolicy, selfName string) error {
	existing := &rightsizingv1alpha1.ClusterRightsizePolicyList{}
	if err := v.Client.List(ctx, existing); err != nil {
		return fmt.Errorf("failed to list ClusterRightsizePolicies: %w", err)
	}

	newKind := policy.Spec.TargetRef.Kind
	newName := policy.Spec.TargetRef.Name
	newIsWildcard := newName == "*" || newName == ""
	newHasSelector := policy.Spec.TargetRef.LabelSelector != nil

	var allErrs field.ErrorList

	for _, ep := range existing.Items {
		if ep.Name == selfName {
			continue
		}
		if ep.Spec.TargetRef.Kind != newKind {
			continue
		}

		epName := ep.Spec.TargetRef.Name
		epIsWildcard := epName == "*" || epName == ""
		epHasSelector := ep.Spec.TargetRef.LabelSelector != nil

		if !newIsWildcard && !epIsWildcard {
			// Both have exact names.
			if newName == epName {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("a ClusterRightsizePolicy already targets %s/%s", newKind, newName),
				))
			}
		} else if newIsWildcard && epIsWildcard {
			// Both are wildcards.
			if !newHasSelector && !epHasSelector {
				// Two catch-all wildcards conflict.
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("a catch-all ClusterRightsizePolicy for %s already exists", newKind),
				))
			} else if newHasSelector && !epHasSelector {
				// New has selector but existing is catch-all — existing catches everything.
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("a catch-all ClusterRightsizePolicy for %s already exists and would also cover this policy's selector", newKind),
				))
			} else if !newHasSelector && epHasSelector {
				// New is catch-all but existing has selector — new catches everything including existing's scope.
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("this catch-all ClusterRightsizePolicy for %s would conflict with existing ClusterRightsizePolicy %s which has a labelSelector", newKind, ep.Name),
				))
			}
			// Both have selectors — allowed (potentially different subsets).
		}
		// exact vs wildcard combinations are not restricted at the cluster level
		// (priority field handles which one wins at runtime).
	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}
