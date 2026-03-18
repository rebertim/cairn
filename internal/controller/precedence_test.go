package controller

import (
	"context"
	"testing"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// precedenceScheme builds a scheme that covers both core Kubernetes types
// (needed for Namespace lists) and Cairn CRDs.
func precedenceScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = rightsizingv1alpha1.AddToScheme(s)
	return s
}

func ns(name string, lbls map[string]string) corev1.Namespace {
	return corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: lbls,
		},
	}
}

// ── matchesNamespaceSelector ──────────────────────────────────────────────────

func TestMatchesNamespaceSelector_NilSelector_MatchesAll(t *testing.T) {
	got, err := matchesNamespaceSelector(ns("default", nil), nil)
	if err != nil || !got {
		t.Errorf("nil selector should match all, got=%v err=%v", got, err)
	}
}

func TestMatchesNamespaceSelector_ExcludeNames(t *testing.T) {
	sel := &rightsizingv1alpha1.NamespaceSelector{
		ExcludeNames: []string{"kube-system", "cert-manager"},
	}
	cases := []struct {
		name   string
		nsName string
		want   bool
	}{
		{"excluded", "kube-system", false},
		{"excluded2", "cert-manager", false},
		{"not excluded", "default", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchesNamespaceSelector(ns(tc.nsName, nil), sel)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchesNamespaceSelector_MatchNames(t *testing.T) {
	sel := &rightsizingv1alpha1.NamespaceSelector{
		MatchNames: []string{"team-a", "team-b"},
	}
	cases := []struct {
		name   string
		nsName string
		want   bool
	}{
		{"in list", "team-a", true},
		{"in list 2", "team-b", true},
		{"not in list", "default", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchesNamespaceSelector(ns(tc.nsName, nil), sel)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchesNamespaceSelector_ExcludeTakesPriorityOverMatchNames(t *testing.T) {
	sel := &rightsizingv1alpha1.NamespaceSelector{
		MatchNames:   []string{"team-a"},
		ExcludeNames: []string{"team-a"}, // same name in both lists — exclude wins
	}
	got, err := matchesNamespaceSelector(ns("team-a", nil), sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("exclude should take priority over matchNames")
	}
}

func TestMatchesNamespaceSelector_LabelSelector_Matches(t *testing.T) {
	sel := &rightsizingv1alpha1.NamespaceSelector{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"env": "prod"},
		},
	}
	matched, err := matchesNamespaceSelector(ns("prod-ns", map[string]string{"env": "prod"}), sel)
	if err != nil || !matched {
		t.Errorf("labeled namespace should match, got=%v err=%v", matched, err)
	}
	notMatched, err := matchesNamespaceSelector(ns("staging-ns", map[string]string{"env": "staging"}), sel)
	if err != nil || notMatched {
		t.Errorf("non-matching label should not match, got=%v err=%v", notMatched, err)
	}
}

func TestMatchesNamespaceSelector_EmptySelectorMatchesAll(t *testing.T) {
	sel := &rightsizingv1alpha1.NamespaceSelector{} // no constraints at all
	got, err := matchesNamespaceSelector(ns("anything", nil), sel)
	if err != nil || !got {
		t.Errorf("empty selector should match all, got=%v err=%v", got, err)
	}
}

// ── namespacePolicyCoversWorkload ─────────────────────────────────────────────

func makeRightsizePolicy(kind, targetName string, suspended bool) *rightsizingv1alpha1.RightsizePolicy {
	return &rightsizingv1alpha1.RightsizePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: rightsizingv1alpha1.RightsizePolicySpec{
			CommonPolicySpec: rightsizingv1alpha1.CommonPolicySpec{
				Suspended: suspended,
				TargetRef: rightsizingv1alpha1.TargetRef{Kind: kind, Name: targetName},
			},
		},
	}
}

func newFakeClient(objs ...runtime.Object) *fake.ClientBuilder {
	return fake.NewClientBuilder().WithScheme(precedenceScheme()).WithRuntimeObjects(objs...)
}

func wl(name string, lbls map[string]string) workloadInfo {
	return workloadInfo{Kind: "Deployment", Name: name, Namespace: "default", Labels: lbls}
}

func TestNamespacePolicyCoversWorkload_NoPolicies_False(t *testing.T) {
	c := newFakeClient().Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("app", nil))
	if err != nil || covered {
		t.Errorf("no policies should not cover, got=%v err=%v", covered, err)
	}
}

func TestNamespacePolicyCoversWorkload_ExactMatch_True(t *testing.T) {
	p := makeRightsizePolicy("Deployment", "app", false)
	c := newFakeClient(p).Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("app", nil))
	if err != nil || !covered {
		t.Errorf("exact name match should cover, got=%v err=%v", covered, err)
	}
}

func TestNamespacePolicyCoversWorkload_DifferentKind_False(t *testing.T) {
	p := makeRightsizePolicy("StatefulSet", "app", false)
	c := newFakeClient(p).Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("app", nil))
	if err != nil || covered {
		t.Errorf("different kind should not cover, got=%v err=%v", covered, err)
	}
}

func TestNamespacePolicyCoversWorkload_Wildcard_True(t *testing.T) {
	p := makeRightsizePolicy("Deployment", "*", false)
	c := newFakeClient(p).Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("anything", nil))
	if err != nil || !covered {
		t.Errorf("wildcard should cover, got=%v err=%v", covered, err)
	}
}

func TestNamespacePolicyCoversWorkload_WildcardLabelSelector_Matches_True(t *testing.T) {
	p := makeRightsizePolicy("Deployment", "*", false)
	p.Spec.TargetRef.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"team": "backend"},
	}
	c := newFakeClient(p).Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("api", map[string]string{"team": "backend"}))
	if err != nil || !covered {
		t.Errorf("matching label selector should cover, got=%v err=%v", covered, err)
	}
}

func TestNamespacePolicyCoversWorkload_WildcardLabelSelector_NoMatch_False(t *testing.T) {
	p := makeRightsizePolicy("Deployment", "*", false)
	p.Spec.TargetRef.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"team": "backend"},
	}
	c := newFakeClient(p).Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("frontend", map[string]string{"team": "frontend"}))
	if err != nil || covered {
		t.Errorf("non-matching label selector should not cover, got=%v err=%v", covered, err)
	}
}

func TestNamespacePolicyCoversWorkload_SuspendedExact_StillCovers(t *testing.T) {
	// A suspended policy acts as a safety lock — it still prevents cluster policies
	// from taking over the workload.
	p := makeRightsizePolicy("Deployment", "app", true /* suspended */)
	c := newFakeClient(p).Build()
	covered, err := namespacePolicyCoversWorkload(context.Background(), c, wl("app", nil))
	if err != nil || !covered {
		t.Errorf("suspended exact policy should still cover (safety lock), got=%v err=%v", covered, err)
	}
}

// ── higherPriorityClusterPolicyClaims ────────────────────────────────────────

func makeCRSP(name string, priority int32, kind string, enabled bool) *rightsizingv1alpha1.ClusterRightsizePolicy {
	return &rightsizingv1alpha1.ClusterRightsizePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: rightsizingv1alpha1.ClusterRightsizePolicySpec{
			Enabled:  enabled,
			Priority: priority,
			CommonPolicySpec: rightsizingv1alpha1.CommonPolicySpec{
				TargetRef: rightsizingv1alpha1.TargetRef{Kind: kind, Name: "*"},
			},
		},
	}
}

func makeRec(name, namespace, ownerPolicy string) *rightsizingv1alpha1.RightsizeRecommendation {
	return &rightsizingv1alpha1.RightsizeRecommendation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{clusterPolicyLabel: ownerPolicy},
		},
	}
}

func TestHigherPriorityClusterPolicyClaims_NoCandidates_False(t *testing.T) {
	current := makeCRSP("current", 0, "Deployment", true)
	nsObj := ns("default", nil)
	c := newFakeClient(current, &nsObj).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || claimed {
		t.Errorf("no other policies, should not be claimed, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_HigherPriority_True(t *testing.T) {
	current := makeCRSP("current", 0, "Deployment", true)
	higher := makeCRSP("higher", 10, "Deployment", true)
	nsObj := ns("default", nil)
	c := newFakeClient(current, higher, &nsObj).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || !claimed {
		t.Errorf("higher priority policy should claim, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_LowerPriority_False(t *testing.T) {
	current := makeCRSP("current", 10, "Deployment", true)
	lower := makeCRSP("lower", 0, "Deployment", true)
	nsObj := ns("default", nil)
	c := newFakeClient(current, lower, &nsObj).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || claimed {
		t.Errorf("lower priority policy should not claim, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_EqualPriority_OtherOwnsRec_True(t *testing.T) {
	current := makeCRSP("current", 5, "Deployment", true)
	other := makeCRSP("other", 5, "Deployment", true)
	nsObj := ns("default", nil)
	rec := makeRec(recommendationName("Deployment", "app"), "default", "other")
	c := newFakeClient(current, other, &nsObj, rec).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || !claimed {
		t.Errorf("equal priority other policy that owns rec should claim, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_EqualPriority_CurrentOwnsRec_False(t *testing.T) {
	current := makeCRSP("current", 5, "Deployment", true)
	other := makeCRSP("other", 5, "Deployment", true)
	nsObj := ns("default", nil)
	// Rec is owned by current, not other
	rec := makeRec(recommendationName("Deployment", "app"), "default", "current")
	c := newFakeClient(current, other, &nsObj, rec).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || claimed {
		t.Errorf("current already owns the rec, other should not claim, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_DisabledOther_False(t *testing.T) {
	current := makeCRSP("current", 0, "Deployment", true)
	disabled := makeCRSP("disabled", 99, "Deployment", false /* enabled=false */)
	nsObj := ns("default", nil)
	c := newFakeClient(current, disabled, &nsObj).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || claimed {
		t.Errorf("disabled policy should not claim even with higher priority, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_OtherKind_False(t *testing.T) {
	current := makeCRSP("current", 0, "Deployment", true)
	other := makeCRSP("other", 99, "StatefulSet", true) // different kind
	nsObj := ns("default", nil)
	c := newFakeClient(current, other, &nsObj).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || claimed {
		t.Errorf("policy targeting different kind should not claim, got=%v err=%v", claimed, err)
	}
}

func TestHigherPriorityClusterPolicyClaims_NamespaceExcluded_False(t *testing.T) {
	current := makeCRSP("current", 0, "Deployment", true)
	// other has higher priority but excludes the workload's namespace
	other := makeCRSP("other", 10, "Deployment", true)
	other.Spec.NamespaceSelector = &rightsizingv1alpha1.NamespaceSelector{
		ExcludeNames: []string{"default"},
	}
	nsObj := ns("default", nil)
	c := newFakeClient(current, other, &nsObj).Build()
	claimed, err := higherPriorityClusterPolicyClaims(context.Background(), c, wl("app", nil), current)
	if err != nil || claimed {
		t.Errorf("other policy excludes this namespace, should not claim, got=%v err=%v", claimed, err)
	}
}
