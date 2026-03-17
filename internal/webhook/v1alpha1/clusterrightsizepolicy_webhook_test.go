package v1alpha1

import (
	"context"
	"testing"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func crspPolicy(name, kind, targetName string) *rightsizingv1alpha1.ClusterRightsizePolicy {
	return &rightsizingv1alpha1.ClusterRightsizePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: rightsizingv1alpha1.ClusterRightsizePolicySpec{
			CommonPolicySpec: rightsizingv1alpha1.CommonPolicySpec{
				TargetRef: rightsizingv1alpha1.TargetRef{Kind: kind, Name: targetName},
			},
		},
	}
}

func crspPolicyWithSelector(name string) *rightsizingv1alpha1.ClusterRightsizePolicy {
	p := crspPolicy(name, "Deployment", "*")
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"env": "prod"}}
	p.Spec.TargetRef.LabelSelector = sel
	return p
}

func crspValidator(existing ...*rightsizingv1alpha1.ClusterRightsizePolicy) *ClusterRightsizePolicyCustomValidator {
	objs := make([]runtime.Object, len(existing))
	for i, p := range existing {
		objs[i] = p
	}
	c := fake.NewClientBuilder().WithScheme(rsScheme()).WithRuntimeObjects(objs...).Build()
	return &ClusterRightsizePolicyCustomValidator{Client: c}
}

// --- Create: no conflict ---

func TestCRSPValidateCreate_NoExisting_Allowed(t *testing.T) {
	v := crspValidator()
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// --- Create: exact name conflict ---

func TestCRSPValidateCreate_SameExactTarget_Rejected(t *testing.T) {
	existing := crspPolicy("existing", "Deployment", "myapp")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "myapp"))
	if err == nil {
		t.Error("expected conflict error for duplicate exact cluster target")
	}
}

func TestCRSPValidateCreate_SameTargetDifferentKind_Allowed(t *testing.T) {
	existing := crspPolicy("existing", "StatefulSet", "myapp")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "myapp"))
	if err != nil {
		t.Errorf("different kinds should not conflict, got: %v", err)
	}
}

// --- Create: catch-all wildcard conflicts ---

func TestCRSPValidateCreate_TwoCatchAllWildcards_Rejected(t *testing.T) {
	existing := crspPolicy("existing", "Deployment", "*")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "*"))
	if err == nil {
		t.Error("two catch-all wildcards should conflict")
	}
}

func TestCRSPValidateCreate_TwoWildcardsWithSelectors_Allowed(t *testing.T) {
	existing := crspPolicyWithSelector("existing")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicyWithSelector("new"))
	if err != nil {
		t.Errorf("two wildcards with different selectors should be allowed, got: %v", err)
	}
}

func TestCRSPValidateCreate_NewWildcardWithSelector_ExistingCatchAll_Rejected(t *testing.T) {
	existing := crspPolicy("existing", "Deployment", "*") // catch-all, no selector
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicyWithSelector("new"))
	if err == nil {
		t.Error("wildcard-with-selector should conflict when a catch-all already exists")
	}
}

func TestCRSPValidateCreate_NewCatchAll_ExistingWildcardWithSelector_Rejected(t *testing.T) {
	existing := crspPolicyWithSelector("existing")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "*"))
	if err == nil {
		t.Error("new catch-all should conflict with existing wildcard-with-selector")
	}
}

// --- Create: exact vs wildcard (allowed at cluster level) ---

func TestCRSPValidateCreate_NewExact_ExistingCatchAll_Allowed(t *testing.T) {
	// At cluster level, exact+wildcard combinations are handled by priority at runtime
	existing := crspPolicy("existing", "Deployment", "*")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "specific-app"))
	if err != nil {
		t.Errorf("exact-name policy alongside catch-all should be allowed at cluster level, got: %v", err)
	}
}

func TestCRSPValidateCreate_NewCatchAll_NoExisting_Allowed(t *testing.T) {
	v := crspValidator()
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", "*"))
	if err != nil {
		t.Errorf("first catch-all wildcard should be allowed, got: %v", err)
	}
}

// --- Update: self exclusion ---

func TestCRSPValidateUpdate_SelfExcluded_Allowed(t *testing.T) {
	existing := crspPolicy("mypolicy", "Deployment", "myapp")
	v := crspValidator(existing)
	_, err := v.ValidateUpdate(context.Background(), nil, existing)
	if err != nil {
		t.Errorf("update should not conflict with itself, got: %v", err)
	}
}

func TestCRSPValidateUpdate_ConflictWithOther_Rejected(t *testing.T) {
	other := crspPolicy("other", "Deployment", "myapp")
	updating := crspPolicy("mypolicy", "Deployment", "myapp")
	v := crspValidator(other)
	_, err := v.ValidateUpdate(context.Background(), nil, updating)
	if err == nil {
		t.Error("update conflicting with another cluster policy should be rejected")
	}
}

// --- Delete ---

func TestCRSPValidateDelete_AlwaysAllowed(t *testing.T) {
	existing := crspPolicy("mypolicy", "Deployment", "myapp")
	v := crspValidator(existing)
	_, err := v.ValidateDelete(context.Background(), existing)
	if err != nil {
		t.Errorf("delete should always be allowed, got: %v", err)
	}
}

// --- Empty-name treated as wildcard ---

func TestCRSPValidateCreate_EmptyNameTreatedAsCatchAll(t *testing.T) {
	existing := crspPolicy("existing", "Deployment", "")
	v := crspValidator(existing)
	_, err := v.ValidateCreate(context.Background(), crspPolicy("new", "Deployment", ""))
	if err == nil {
		t.Error("two empty-name cluster policies should conflict as catch-alls")
	}
}
