package recommender

import (
	"math"
	"testing"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	"k8s.io/apimachinery/pkg/api/resource"
)

// --- selectPercentile ---

func TestSelectPercentile_NilPolicy_ReturnsP95(t *testing.T) {
	if got := selectPercentile(50, 95, 99, nil); got != 95 {
		t.Errorf("want 95, got %v", got)
	}
}

func TestSelectPercentile_Percentile50_ReturnsP50(t *testing.T) {
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 50}
	if got := selectPercentile(50, 95, 99, pol); got != 50 {
		t.Errorf("want 50, got %v", got)
	}
}

func TestSelectPercentile_Percentile30_MapsToP50(t *testing.T) {
	// anything ≤50 maps to p50
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 30}
	if got := selectPercentile(50, 95, 99, pol); got != 50 {
		t.Errorf("want p50=50, got %v", got)
	}
}

func TestSelectPercentile_Percentile99_ReturnsP99(t *testing.T) {
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 99}
	if got := selectPercentile(50, 95, 99, pol); got != 99 {
		t.Errorf("want 99, got %v", got)
	}
}

func TestSelectPercentile_Percentile100_MapsToP99(t *testing.T) {
	// ≥98 maps to p99
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 100}
	if got := selectPercentile(50, 95, 99, pol); got != 99 {
		t.Errorf("want p99=99, got %v", got)
	}
}

func TestSelectPercentile_Percentile75_ReturnsP95(t *testing.T) {
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 75}
	if got := selectPercentile(50, 95, 99, pol); got != 95 {
		t.Errorf("want p95=95, got %v", got)
	}
}

// --- clampToPolicy ---

func TestClampToPolicy_NilPolicy_ReturnsValue(t *testing.T) {
	if got := clampToPolicy(1.0, nil); got != 1.0 {
		t.Errorf("want 1.0, got %v", got)
	}
}

func TestClampToPolicy_AboveMax_ClampsToMax(t *testing.T) {
	maxQ := resource.MustParse("500m")
	pol := &v1alpha1.ContainerResourcePolicy{MaxRequest: &maxQ}
	// 1.0 core (1000m) > 500m → clamp to 500m
	got := clampToPolicy(1.0, pol)
	max := maxQ.AsApproximateFloat64()
	if math.Abs(got-max) > 1e-9 {
		t.Errorf("want %v (max), got %v", max, got)
	}
}

func TestClampToPolicy_BelowMin_ClampsToMin(t *testing.T) {
	minQ := resource.MustParse("200m")
	pol := &v1alpha1.ContainerResourcePolicy{MinRequest: &minQ}
	// 0.1 core (100m) < 200m → clamp to 200m
	got := clampToPolicy(0.1, pol)
	min := minQ.AsApproximateFloat64()
	if math.Abs(got-min) > 1e-9 {
		t.Errorf("want %v (min), got %v", min, got)
	}
}

func TestClampToPolicy_WithinBounds_ReturnsUnchanged(t *testing.T) {
	minQ := resource.MustParse("100m")
	maxQ := resource.MustParse("1000m")
	pol := &v1alpha1.ContainerResourcePolicy{MinRequest: &minQ, MaxRequest: &maxQ}
	// 0.5 core (500m) is within [100m, 1000m]
	if got := clampToPolicy(0.5, pol); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("want 0.5, got %v", got)
	}
}

func TestClampToPolicy_NilMinMax_ReturnsValue(t *testing.T) {
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 95}
	if got := clampToPolicy(0.42, pol); math.Abs(got-0.42) > 1e-9 {
		t.Errorf("want 0.42, got %v", got)
	}
}

// --- applyHeadroomAndClamp ---

func TestApplyHeadroomAndClamp_NilPolicy_UsesDefault15Pct(t *testing.T) {
	// 1.0 * 1.15 = 1.15
	got := applyHeadroomAndClamp(1.0, nil)
	if math.Abs(got-1.15) > 0.001 {
		t.Errorf("want 1.15, got %v", got)
	}
}

func TestApplyHeadroomAndClamp_CustomHeadroom25Pct(t *testing.T) {
	pol := &v1alpha1.ContainerResourcePolicy{HeadroomPercent: 25}
	// 2.0 * 1.25 = 2.5
	got := applyHeadroomAndClamp(2.0, pol)
	if math.Abs(got-2.5) > 0.001 {
		t.Errorf("want 2.5, got %v", got)
	}
}

func TestApplyHeadroomAndClamp_ZeroHeadroom_ReturnsValue(t *testing.T) {
	pol := &v1alpha1.ContainerResourcePolicy{HeadroomPercent: 0}
	// 0.8 * 1.0 = 0.8
	got := applyHeadroomAndClamp(0.8, pol)
	if math.Abs(got-0.8) > 0.001 {
		t.Errorf("want 0.8, got %v", got)
	}
}

func TestApplyHeadroomAndClamp_ClampsToMax(t *testing.T) {
	maxQ := resource.MustParse("1") // 1 CPU core
	pol := &v1alpha1.ContainerResourcePolicy{HeadroomPercent: 50, MaxRequest: &maxQ}
	// 1.0 * 1.5 = 1.5 → clamped to 1.0
	got := applyHeadroomAndClamp(1.0, pol)
	max := maxQ.AsApproximateFloat64()
	if math.Abs(got-max) > 0.001 {
		t.Errorf("want %v (max), got %v", max, got)
	}
}

func TestApplyHeadroomAndClamp_ClampsToMin(t *testing.T) {
	minQ := resource.MustParse("500m")
	pol := &v1alpha1.ContainerResourcePolicy{HeadroomPercent: 10, MinRequest: &minQ}
	// 0.1 * 1.10 = 0.11 cores → below 500m (0.5 cores) → clamped
	got := applyHeadroomAndClamp(0.1, pol)
	min := minQ.AsApproximateFloat64()
	if math.Abs(got-min) > 0.001 {
		t.Errorf("want %v (min), got %v", min, got)
	}
}

// --- StandardRecommender.baseline ---

func TestStandardRecommender_Baseline_DefaultHeadroomAndPercentile(t *testing.T) {
	r := NewStandardRecommender()
	m := &collector.ContainerMetrics{
		CPUP50: 0.2, CPUP95: 0.5, CPUP99: 0.8,
		MemoryP50: 50e6, MemoryP95: 100e6, MemoryP99: 150e6,
	}
	cpu, mem := r.baseline(m, nil, nil, nil)
	// nil policy → p95, 15% headroom
	wantCPU := 0.5 * 1.15
	wantMem := 100e6 * 1.15
	if math.Abs(cpu-wantCPU) > 0.001 {
		t.Errorf("cpu: want %.4f, got %.4f", wantCPU, cpu)
	}
	if math.Abs(mem-wantMem) > 1 {
		t.Errorf("mem: want %.0f, got %.0f", wantMem, mem)
	}
}

func TestStandardRecommender_Baseline_P99WithZeroHeadroom(t *testing.T) {
	r := NewStandardRecommender()
	m := &collector.ContainerMetrics{
		CPUP50: 0.1, CPUP95: 0.5, CPUP99: 1.0,
	}
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 99, HeadroomPercent: 0}
	cpu, _ := r.baseline(m, pol, nil, nil)
	// p99=1.0, 0% headroom → 1.0 cores
	if math.Abs(cpu-1.0) > 0.001 {
		t.Errorf("want 1.0, got %v", cpu)
	}
}

func TestStandardRecommender_Baseline_MinClamping(t *testing.T) {
	r := NewStandardRecommender()
	m := &collector.ContainerMetrics{CPUP95: 0.01}
	minQ := resource.MustParse("100m")
	pol := &v1alpha1.ContainerResourcePolicy{HeadroomPercent: 10, MinRequest: &minQ}
	// 0.01 * 1.10 = 0.011 cores → below 100m → clamped
	cpu, _ := r.baseline(m, pol, nil, nil)
	min := minQ.AsApproximateFloat64()
	if math.Abs(cpu-min) > 0.001 {
		t.Errorf("want min=%v, got %v", min, cpu)
	}
}

func TestStandardRecommender_Baseline_MaxClamping(t *testing.T) {
	r := NewStandardRecommender()
	m := &collector.ContainerMetrics{MemoryP95: 2e9} // 2 GB
	maxQ := resource.MustParse("1Gi")
	// Percentile: 95 so p95 (2e9) is selected, then 2e9*1.20=2.4e9 → clamped to 1Gi
	pol := &v1alpha1.ContainerResourcePolicy{Percentile: 95, HeadroomPercent: 20, MaxRequest: &maxQ}
	_, mem := r.baseline(m, nil, pol, nil)
	max := maxQ.AsApproximateFloat64()
	if math.Abs(mem-max) > 1 {
		t.Errorf("want max=%.0f, got %.0f", max, mem)
	}
}
