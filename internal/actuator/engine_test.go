package actuator

import (
	"context"
	"math"
	"testing"
	"time"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mockActuator records invocations for assertion in tests.
type mockActuator struct {
	calls     int
	lastInput *ApplyInput
	err       error
}

func (m *mockActuator) Apply(_ context.Context, in ApplyInput) error {
	m.calls++
	cp := in
	m.lastInput = &cp
	return m.err
}

// helpers

func recWithContainers(containers []rightsizingv1alpha1.ContainerRecommendation) *rightsizingv1alpha1.RightsizeRecommendation {
	return &rightsizingv1alpha1.RightsizeRecommendation{
		Spec: rightsizingv1alpha1.RightsizeRecommendationSpec{
			TargetRef: rightsizingv1alpha1.TargetRef{Kind: "Deployment", Name: "test"},
		},
		Status: rightsizingv1alpha1.RightsizeRecommendationStatus{
			Containers: containers,
		},
	}
}

func containerRec(recCPU string) rightsizingv1alpha1.ContainerRecommendation {
	cur := resource.MustParse("100m")
	rec := resource.MustParse(recCPU)
	return rightsizingv1alpha1.ContainerRecommendation{
		ContainerName: "app",
		Current: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: cur},
		},
		Recommended: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: rec},
		},
	}
}

func autoPolicy(threshold int32, strategy rightsizingv1alpha1.UpdateStrategy) *rightsizingv1alpha1.RightsizePolicy {
	return &rightsizingv1alpha1.RightsizePolicy{
		Spec: rightsizingv1alpha1.RightsizePolicySpec{
			CommonPolicySpec: rightsizingv1alpha1.CommonPolicySpec{
				Mode:            rightsizingv1alpha1.PolicyModeAuto,
				ChangeThreshold: threshold,
				UpdateStrategy:  strategy,
			},
		},
	}
}

func modePolicy(mode rightsizingv1alpha1.PolicyMode) *rightsizingv1alpha1.RightsizePolicy {
	return &rightsizingv1alpha1.RightsizePolicy{
		Spec: rightsizingv1alpha1.RightsizePolicySpec{
			CommonPolicySpec: rightsizingv1alpha1.CommonPolicySpec{
				Mode:            mode,
				ChangeThreshold: 5,
				UpdateStrategy:  rightsizingv1alpha1.UpdateStrategyRestart,
			},
		},
	}
}

func newTrioEngine() (dry, inplace, restart *mockActuator, engine *Engine) {
	dry = &mockActuator{}
	inplace = &mockActuator{}
	restart = &mockActuator{}
	engine = NewEngine(dry, inplace, restart)
	return
}

// --- mode dispatch ---

func TestEngine_DryRun_CallsDryRunActuator(t *testing.T) {
	dry, ip, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	policy := modePolicy(rightsizingv1alpha1.PolicyModeDryRun)

	result, err := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied {
		t.Error("dry-run should return Applied=false")
	}
	if dry.calls != 1 {
		t.Errorf("expected 1 dry-run call, got %d", dry.calls)
	}
	if ip.calls+rst.calls != 0 {
		t.Errorf("unexpected actuator calls: inplace=%d, restart=%d", ip.calls, rst.calls)
	}
}

func TestEngine_RecommendMode_NoActuatorCalled(t *testing.T) {
	dry, ip, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	policy := modePolicy(rightsizingv1alpha1.PolicyModeRecommended)

	result, err := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied {
		t.Error("recommend mode should not apply")
	}
	if dry.calls+ip.calls+rst.calls != 0 {
		t.Error("no actuator should be called in recommend mode")
	}
}

// --- change gate ---

func TestEngine_Auto_EmptyContainers_NoApply(t *testing.T) {
	_, _, rst, e := newTrioEngine()
	rec := recWithContainers(nil)
	policy := autoPolicy(10, rightsizingv1alpha1.UpdateStrategyRestart)

	result, _ := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if result.Applied {
		t.Error("empty containers should not apply")
	}
	if rst.calls != 0 {
		t.Errorf("expected 0 actuator calls, got %d", rst.calls)
	}
}

func TestEngine_Auto_BelowThreshold_NoApply(t *testing.T) {
	// 100m → 108m = 8% change, threshold=10% → skip
	_, _, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("108m")})
	policy := autoPolicy(10, rightsizingv1alpha1.UpdateStrategyRestart)

	result, _ := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if result.Applied {
		t.Error("below threshold should not apply")
	}
	if rst.calls != 0 {
		t.Errorf("expected 0 actuator calls, got %d", rst.calls)
	}
}

func TestEngine_Auto_AboveThreshold_NoCooldown_Applies(t *testing.T) {
	// 100m → 200m = 100% change, threshold=10% → apply; no prior LastAppliedTime
	_, _, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	policy := autoPolicy(10, rightsizingv1alpha1.UpdateStrategyRestart)

	result, err := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if rst.calls != 1 {
		t.Errorf("expected 1 restart call, got %d", rst.calls)
	}
}

// --- cooldown ---

func TestEngine_Auto_WithinCooldown_NoApply(t *testing.T) {
	// LastAppliedTime = just now → within default 5m cooldown
	_, _, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	now := metav1.Now()
	rec.Status.LastAppliedTime = &now
	policy := autoPolicy(5, rightsizingv1alpha1.UpdateStrategyRestart)

	result, _ := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if result.Applied {
		t.Error("within cooldown should not apply")
	}
	if rst.calls != 0 {
		t.Errorf("expected 0 actuator calls, got %d", rst.calls)
	}
}

func TestEngine_Auto_CooldownExpired_Applies(t *testing.T) {
	// LastAppliedTime = 10 minutes ago → beyond default 5m cooldown
	_, _, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	tenMinAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	rec.Status.LastAppliedTime = &tenMinAgo
	policy := autoPolicy(5, rightsizingv1alpha1.UpdateStrategyRestart)

	result, err := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Error("expected Applied=true after cooldown")
	}
	if rst.calls != 1 {
		t.Errorf("expected 1 restart call, got %d", rst.calls)
	}
}

func TestEngine_Auto_CustomCooldown_Respected(t *testing.T) {
	// MinApplyInterval=1m; LastAppliedTime=30s ago → still in cooldown
	_, _, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	thirtySecAgo := metav1.NewTime(time.Now().Add(-30 * time.Second))
	rec.Status.LastAppliedTime = &thirtySecAgo
	policy := autoPolicy(5, rightsizingv1alpha1.UpdateStrategyRestart)
	policy.Spec.MinApplyInterval = metav1.Duration{Duration: 1 * time.Minute}

	result, _ := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if result.Applied {
		t.Error("should be within custom 1m cooldown")
	}
	if rst.calls != 0 {
		t.Errorf("expected 0 actuator calls, got %d", rst.calls)
	}
}

// --- strategy dispatch ---

func TestEngine_Auto_InPlaceStrategy_CallsInPlaceActuator(t *testing.T) {
	_, ip, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	tenMinAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	rec.Status.LastAppliedTime = &tenMinAgo
	policy := autoPolicy(5, rightsizingv1alpha1.UpdateStrategyInPlace)

	result, err := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if ip.calls != 1 {
		t.Errorf("expected 1 in-place call, got %d", ip.calls)
	}
	if rst.calls != 0 {
		t.Errorf("restart actuator should not be called for in-place, got %d", rst.calls)
	}
}

func TestEngine_Auto_RestartStrategy_CallsRestartActuator(t *testing.T) {
	_, ip, rst, e := newTrioEngine()
	rec := recWithContainers([]rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")})
	tenMinAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	rec.Status.LastAppliedTime = &tenMinAgo
	policy := autoPolicy(5, rightsizingv1alpha1.UpdateStrategyRestart)

	result, err := e.Apply(context.Background(), EngineInput{Recommendation: rec, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied {
		t.Error("expected Applied=true")
	}
	if rst.calls != 1 {
		t.Errorf("expected 1 restart call, got %d", rst.calls)
	}
	if ip.calls != 0 {
		// Resources are applied to pods by the webhook on creation, not by an
		// actuator. The restart actuator only writes the restartedAt annotation
		// to trigger a rolling restart; in-place patching is never part of that.
		t.Errorf("in-place actuator must not be called for restart strategy, got %d", ip.calls)
	}
}

// --- hasSignificantChange ---

func TestHasSignificantChange_AboveThreshold(t *testing.T) {
	containers := []rightsizingv1alpha1.ContainerRecommendation{containerRec("200m")}
	if !hasSignificantChange(containers, 0.10) {
		t.Error("100m→200m (100%) should exceed 10% threshold")
	}
}

func TestHasSignificantChange_BelowThreshold(t *testing.T) {
	containers := []rightsizingv1alpha1.ContainerRecommendation{containerRec("105m")}
	if hasSignificantChange(containers, 0.10) {
		t.Error("100m→105m (5%) should be below 10% threshold")
	}
}

func TestHasSignificantChange_AtThreshold_NotSignificant(t *testing.T) {
	// exactly 10% change → not strictly greater than threshold → no change
	containers := []rightsizingv1alpha1.ContainerRecommendation{containerRec("110m")}
	if hasSignificantChange(containers, 0.10) {
		t.Error("exactly 10% change should not exceed 10% threshold")
	}
}

func TestHasSignificantChange_NilRecommended_Skipped(t *testing.T) {
	containers := []rightsizingv1alpha1.ContainerRecommendation{
		{ContainerName: "app", Recommended: nil},
	}
	if hasSignificantChange(containers, 0.05) {
		t.Error("nil Recommended should be skipped")
	}
}

// --- resourceChangePct ---

func TestResourceChangePct_50PercentIncrease(t *testing.T) {
	cur := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}
	rec := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")}
	pct := resourceChangePct(cur, rec)
	if math.Abs(pct-0.5) > 0.001 {
		t.Errorf("expected 50%% change, got %.4f", pct)
	}
}

func TestResourceChangePct_50PercentDecrease(t *testing.T) {
	cur := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}
	rec := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
	pct := resourceChangePct(cur, rec)
	if math.Abs(pct-0.5) > 0.001 {
		t.Errorf("expected 50%% change (decrease), got %.4f", pct)
	}
}

func TestResourceChangePct_ZeroCurrent_Ignored(t *testing.T) {
	// zero current → skip (avoid division by zero)
	cur := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("0")}
	rec := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}
	pct := resourceChangePct(cur, rec)
	if pct != 0 {
		t.Errorf("zero current should be skipped, got %.4f", pct)
	}
}
