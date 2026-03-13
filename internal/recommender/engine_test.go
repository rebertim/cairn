package recommender

import (
	"context"
	"testing"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var testBurstCfg = BurstConfig{Threshold: 1.5, Multiplier: 1.3}

func newTestEngine() *Engine {
	return NewEngine(NewStandardRecommender(), NewJavaRecommender())
}

// stdMetrics builds a standard ContainerMetrics with the given live/p95 values.
func stdMetrics(cpuLive, cpuP95, memLive, memP95 float64) *collector.ContainerMetrics {
	return &collector.ContainerMetrics{
		Key:        collector.ContainerKey{ContainerType: ContainerTypeStandard},
		CPULive:    cpuLive,
		CPUP95:     cpuP95,
		MemoryLive: memLive,
		MemoryP95:  memP95,
	}
}

func doRecommend(t *testing.T, m *collector.ContainerMetrics, burst *v1alpha1.BurstState) RecommendResult {
	t.Helper()
	result, err := newTestEngine().Recommend(context.Background(), RecommendInput{
		Metrics:      m,
		BurstConfig:  testBurstCfg,
		CurrentBurst: burst,
	})
	if err != nil {
		t.Fatalf("Recommend() error: %v", err)
	}
	return result
}

func normalBurst() *v1alpha1.BurstState {
	return &v1alpha1.BurstState{Phase: v1alpha1.BurstPhaseNormal}
}
func burstingBurst() *v1alpha1.BurstState {
	return &v1alpha1.BurstState{Phase: v1alpha1.BurstPhaseBursting}
}

func assertPhase(t *testing.T, want, got v1alpha1.BurstPhase) {
	t.Helper()
	if got != want {
		t.Errorf("phase: want %s, got %s", want, got)
	}
}

// --- Normal phase ---

func TestNormal_StaysNormal_WhenUsageBelowThreshold(t *testing.T) {
	// baseline CPU = 1.0 * 1.15 = 1.15; trigger = 1.15 * 1.5 = 1.725; live=1.0 < 1.725
	result := doRecommend(t, stdMetrics(1.0, 1.0, 100, 100), nil)
	assertPhase(t, v1alpha1.BurstPhaseNormal, result.BurstState.Phase)
}

func TestNormal_TransitionsToBursting_OnCPUSpike(t *testing.T) {
	// cpuLive=2.0 > trigger≈1.725 → Bursting
	result := doRecommend(t, stdMetrics(2.0, 1.0, 50, 100), normalBurst())
	assertPhase(t, v1alpha1.BurstPhaseBursting, result.BurstState.Phase)
}

func TestNormal_TransitionsToBursting_OnMemorySpike(t *testing.T) {
	// baseline mem = 100*1.15 = 115; trigger = 172.5; live=200 > 172.5 → Bursting
	result := doRecommend(t, stdMetrics(0.1, 0.1, 200, 100), normalBurst())
	assertPhase(t, v1alpha1.BurstPhaseBursting, result.BurstState.Phase)
}

func TestNilCurrentBurst_TreatedAsNormal(t *testing.T) {
	result := doRecommend(t, stdMetrics(0.1, 0.1, 10, 10), nil)
	assertPhase(t, v1alpha1.BurstPhaseNormal, result.BurstState.Phase)
}

func TestNormal_ResourcesReflectBaseline(t *testing.T) {
	// p95 CPU = 0.5; baseline = 0.5 * 1.15 = 0.575 cores = 575m
	result := doRecommend(t, stdMetrics(0.1, 0.5, 10, 50), nil)
	cpu := result.Resources.Requests[corev1.ResourceCPU]
	if cpu.MilliValue() < 550 || cpu.MilliValue() > 600 {
		t.Errorf("expected baseline CPU ≈575m, got %dm", cpu.MilliValue())
	}
}

// --- Bursting phase ---

func TestBursting_StaysBursting_WhenUsageStillHigh(t *testing.T) {
	result := doRecommend(t, stdMetrics(2.0, 1.0, 50, 100), burstingBurst())
	assertPhase(t, v1alpha1.BurstPhaseBursting, result.BurstState.Phase)
}

func TestBursting_TransitionsToNormal_AfterRecovery(t *testing.T) {
	// live well below threshold
	result := doRecommend(t, stdMetrics(0.3, 1.0, 30, 100), burstingBurst())
	assertPhase(t, v1alpha1.BurstPhaseNormal, result.BurstState.Phase)
}

func TestBursting_RecommendationUsesMultiplier(t *testing.T) {
	// burstCPU = max(live=2.0, baseline≈1.15) * 1.3 = 2.6 cores = 2600m
	result := doRecommend(t, stdMetrics(2.0, 1.0, 50, 100), normalBurst())
	cpu := result.Resources.Requests[corev1.ResourceCPU]
	if cpu.MilliValue() < 2500 || cpu.MilliValue() > 2700 {
		t.Errorf("expected burst CPU ≈2600m, got %dm", cpu.MilliValue())
	}
}

func TestBursting_PeakCPUTracked(t *testing.T) {
	result := doRecommend(t, stdMetrics(2.0, 1.0, 50, 100), normalBurst())
	if result.BurstState.BurstPeakCPU == nil {
		t.Fatal("expected BurstPeakCPU to be set")
	}
}

func TestBursting_PeakNotDecreased(t *testing.T) {
	// Record a high peak; subsequent lower usage must not reduce it.
	prev := burstingBurst()
	highPeak := resource.MustParse("3") // 3 cores
	prev.BurstPeakCPU = &highPeak

	result := doRecommend(t, stdMetrics(2.0, 1.0, 50, 100), prev)
	if result.BurstState.BurstPeakCPU.Cmp(highPeak) < 0 {
		t.Errorf("burst peak decreased: was %s, now %s",
			highPeak.String(), result.BurstState.BurstPeakCPU.String())
	}
}

func TestBursting_PeakUpdatedWhenNewHigher(t *testing.T) {
	prev := burstingBurst()
	lowPeak := resource.MustParse("1")
	prev.BurstPeakCPU = &lowPeak

	// cpuLive=5.0 → burstCPU = 5.0*1.3 = 6.5 cores > 1 core old peak
	result := doRecommend(t, stdMetrics(5.0, 1.0, 50, 100), prev)
	if result.BurstState.BurstPeakCPU.Cmp(lowPeak) <= 0 {
		t.Errorf("expected peak to increase from %s, got %s",
			lowPeak.String(), result.BurstState.BurstPeakCPU.String())
	}
}
