package v1alpha1

import (
	"context"
	"testing"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func rsScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = rightsizingv1alpha1.AddToScheme(s)
	return s
}

func rsPolicy(name, ns, kind, targetName string) *rightsizingv1alpha1.RightsizePolicy {
	return &rightsizingv1alpha1.RightsizePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: rightsizingv1alpha1.RightsizePolicySpec{
			CommonPolicySpec: rightsizingv1alpha1.CommonPolicySpec{
				TargetRef: rightsizingv1alpha1.TargetRef{Kind: kind, Name: targetName},
			},
		},
	}
}

func rsPolicyWithSelector(name string) *rightsizingv1alpha1.RightsizePolicy {
	p := rsPolicy(name, "default", "Deployment", "*")
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}}
	p.Spec.TargetRef.LabelSelector = sel
	return p
}

func rsValidator(existing ...*rightsizingv1alpha1.RightsizePolicy) *RightsizePolicyCustomValidator {
	objs := make([]runtime.Object, len(existing))
	for i, p := range existing {
		objs[i] = p
	}
	c := fake.NewClientBuilder().WithScheme(rsScheme()).WithRuntimeObjects(objs...).Build()
	return &RightsizePolicyCustomValidator{Client: c}
}

// --- Create: no conflict ---

func TestRSValidateCreate_NoExisting_Allowed(t *testing.T) {
	v := rsValidator()
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// --- Create: exact name conflict ---

func TestRSValidateCreate_SameExactTarget_Rejected(t *testing.T) {
	existing := rsPolicy("existing", "default", "Deployment", "myapp")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "myapp"))
	if err == nil {
		t.Error("expected conflict error for duplicate exact target")
	}
}

func TestRSValidateCreate_SameTargetDifferentKind_Allowed(t *testing.T) {
	existing := rsPolicy("existing", "default", "StatefulSet", "myapp")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("different kinds should not conflict, got: %v", err)
	}
}

func TestRSValidateCreate_SameTargetDifferentNamespace_Allowed(t *testing.T) {
	existing := rsPolicy("existing", "other-ns", "Deployment", "myapp")
	v := rsValidator(existing)
	// The validator lists within the policy's namespace; fake client lists all by default,
	// but our code passes client.InNamespace, so we need objects to be in the same namespace to conflict.
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("different namespaces should not conflict, got: %v", err)
	}
}

// --- Create: wildcard conflicts ---

func TestRSValidateCreate_TwoWildcardsNoSelector_Rejected(t *testing.T) {
	existing := rsPolicy("existing", "default", "Deployment", "*")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "*"))
	if err == nil {
		t.Error("two catch-all wildcards should conflict")
	}
}

func TestRSValidateCreate_TwoWildcardsWithSelectors_Allowed(t *testing.T) {
	existing := rsPolicyWithSelector("existing")
	v := rsValidator(existing)
	newPol := rsPolicyWithSelector("new")
	_, err := v.ValidateCreate(context.Background(), newPol)
	if err != nil {
		t.Errorf("two wildcards with selectors should be allowed, got: %v", err)
	}
}

func TestRSValidateCreate_NewWildcardNoSelector_ExistingExact_Rejected(t *testing.T) {
	existing := rsPolicy("existing", "default", "Deployment", "myapp")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "*"))
	if err == nil {
		t.Error("catch-all wildcard should conflict with existing exact-name policy")
	}
}

func TestRSValidateCreate_NewWildcardWithSelector_ExistingExact_Allowed(t *testing.T) {
	existing := rsPolicy("existing", "default", "Deployment", "myapp")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicyWithSelector("new"))
	if err != nil {
		t.Errorf("wildcard with selector should be allowed alongside exact-name policy, got: %v", err)
	}
}

func TestRSValidateCreate_NewExact_ExistingWildcardNoSelector_Allowed(t *testing.T) {
	// Exact-name policies are more specific and take precedence over wildcards;
	// the controller transfers recommendation ownership on the next cycle.
	existing := rsPolicy("existing", "default", "Deployment", "*")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("exact-name policy should be allowed alongside catch-all wildcard, got: %v", err)
	}
}

func TestRSValidateCreate_NewExact_ExistingWildcardWithSelector_Allowed(t *testing.T) {
	existing := rsPolicyWithSelector("existing")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("exact-name policy should be allowed alongside wildcard-with-selector, got: %v", err)
	}
}

// --- Update: self exclusion ---

func TestRSValidateUpdate_SelfExcluded_Allowed(t *testing.T) {
	existing := rsPolicy("mypolicy", "default", "Deployment", "myapp")
	v := rsValidator(existing)
	// Updating "mypolicy" to itself — should not conflict with itself
	_, err := v.ValidateUpdate(context.Background(), nil, existing)
	if err != nil {
		t.Errorf("update should not conflict with itself, got: %v", err)
	}
}

func TestRSValidateUpdate_ConflictWithOther_Rejected(t *testing.T) {
	other := rsPolicy("other", "default", "Deployment", "myapp")
	updating := rsPolicy("mypolicy", "default", "Deployment", "myapp")
	v := rsValidator(other)
	_, err := v.ValidateUpdate(context.Background(), nil, updating)
	if err == nil {
		t.Error("update that conflicts with another policy should be rejected")
	}
}

// --- Delete ---

func TestRSValidateDelete_AlwaysAllowed(t *testing.T) {
	existing := rsPolicy("mypolicy", "default", "Deployment", "myapp")
	v := rsValidator(existing)
	_, err := v.ValidateDelete(context.Background(), existing)
	if err != nil {
		t.Errorf("delete should always be allowed, got: %v", err)
	}
}

// --- Empty-name wildcard (empty string treated as wildcard) ---

func TestRSValidateCreate_EmptyNameTreatedAsWildcard(t *testing.T) {
	existing := rsPolicy("existing", "default", "Deployment", "")
	v := rsValidator(existing)
	_, err := v.ValidateCreate(context.Background(), rsPolicy("new", "default", "Deployment", ""))
	if err == nil {
		t.Error("two empty-name (wildcard) policies should conflict")
	}
}
