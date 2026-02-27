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

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var rightsizepolicylog = logf.Log.WithName("rightsizepolicy-resource")

// SetupRightsizePolicyWebhookWithManager registers the webhook for RightsizePolicy in the manager.
func SetupRightsizePolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &rightsizingv1alpha1.RightsizePolicy{}).
		WithDefaulter(&RightsizePolicyCustomDefaulter{}).
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
