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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// nolint:unused
// log is for logging in this package.
var rightsizepolicylog = logf.Log.WithName("rightsizepolicy-resource")

// SetupRightsizePolicyWebhookWithManager registers the webhook for RightsizePolicy in the manager.
func SetupRightsizePolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &rightsizingv1alpha1.RightsizePolicy{}).
		WithDefaulter(&RightsizePolicyCustomDefaulter{}).
		WithValidator(&RightsizePolicyCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// +kubebuilder:webhook:path=/mutate-rightsizing-cairn-io-v1alpha1-rightsizepolicy,mutating=true,failurePolicy=fail,sideEffects=None,groups=rightsizing.cairn.io,resources=rightsizepolicies,verbs=create;update,versions=v1alpha1,name=mrightsizepolicy-v1alpha1.kb.io,admissionReviewVersions=v1

// RightsizePolicyCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind RightsizePolicy when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type RightsizePolicyCustomDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind RightsizePolicy.
func (d *RightsizePolicyCustomDefaulter) Default(_ context.Context, obj *rightsizingv1alpha1.RightsizePolicy) error {
	rightsizepolicylog.Info("Defaulting for RightsizePolicy", "name", obj.GetName())

	// TODO(user): fill in your defaulting logic.

	return nil
}

// +kubebuilder:webhook:path=/validate-rightsizing-cairn-io-v1alpha1-rightsizepolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=rightsizing.cairn.io,resources=rightsizepolicies,verbs=create;update,versions=v1alpha1,name=vrightsizepolicy-v1alpha1.kb.io,admissionReviewVersions=v1

// RightsizePolicyCustomValidator validates RightsizePolicy resources to prevent
// conflicting policies in the same namespace.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type RightsizePolicyCustomValidator struct {
	Client client.Client
}

// ValidateCreate validates a new RightsizePolicy.
func (v *RightsizePolicyCustomValidator) ValidateCreate(ctx context.Context, obj *rightsizingv1alpha1.RightsizePolicy) (admission.Warnings, error) {
	return nil, v.validatePolicy(ctx, obj, "")
}

// ValidateUpdate validates an updated RightsizePolicy.
func (v *RightsizePolicyCustomValidator) ValidateUpdate(ctx context.Context, _, newObj *rightsizingv1alpha1.RightsizePolicy) (admission.Warnings, error) {
	return nil, v.validatePolicy(ctx, newObj, newObj.Name)
}

// ValidateDelete does not restrict deletion.
func (v *RightsizePolicyCustomValidator) ValidateDelete(_ context.Context, _ *rightsizingv1alpha1.RightsizePolicy) (admission.Warnings, error) {
	return nil, nil
}

// validatePolicy checks the new/updated policy against all existing policies in
// the same namespace (excluding self by selfName on update).
func (v *RightsizePolicyCustomValidator) validatePolicy(ctx context.Context, policy *rightsizingv1alpha1.RightsizePolicy, selfName string) error {
	existing := &rightsizingv1alpha1.RightsizePolicyList{}
	if err := v.Client.List(ctx, existing, client.InNamespace(policy.Namespace)); err != nil {
		return fmt.Errorf("failed to list RightsizePolicies: %w", err)
	}

	newKind := policy.Spec.TargetRef.Kind
	newName := policy.Spec.TargetRef.Name
	newIsWildcard := newName == "*" || newName == ""

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

		if !newIsWildcard && !epIsWildcard {
			// Both have exact names.
			if newName == epName {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("a RightsizePolicy already targets %s/%s in this namespace", newKind, newName),
				))
			}
		} else if newIsWildcard && epIsWildcard {
			// Both are wildcards.
			newSel := policy.Spec.TargetRef.LabelSelector
			epSel := ep.Spec.TargetRef.LabelSelector
			newCT := policy.Spec.TargetRef.ContainerType
			epCT := ep.Spec.TargetRef.ContainerType
			// Two wildcards with complementary container types target disjoint
			// workload sets and are always allowed (e.g. java + standard).
			complementary := newCT != "" && epCT != "" && newCT != epCT
			// Two wildcards with no label selector conflict unless complementary.
			if newSel == nil && epSel == nil && !complementary {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("a wildcard RightsizePolicy for %s already exists in this namespace", newKind),
				))
			}
			// If both have selectors, they are allowed (potentially different subsets).
		} else if newIsWildcard && !epIsWildcard {
			// New is wildcard, existing is exact. A wildcard with no selector
			// conflicts unless they have different container types.
			newCT := policy.Spec.TargetRef.ContainerType
			epCT := ep.Spec.TargetRef.ContainerType
			complementary := newCT != "" && epCT != "" && newCT != epCT
			if policy.Spec.TargetRef.LabelSelector == nil && !complementary {
				allErrs = append(allErrs, field.Invalid(
					field.NewPath("spec", "targetRef", "name"),
					newName,
					fmt.Sprintf("this wildcard policy would conflict with existing RightsizePolicy %s/%s", epName, newKind),
				))
			}
		}
		// New is exact, existing is wildcard: always allowed.
		// Exact-name policies are more specific and take precedence; the
		// reconciler transfers recommendation ownership on the next cycle.

	}

	if len(allErrs) > 0 {
		return allErrs.ToAggregate()
	}
	return nil
}
