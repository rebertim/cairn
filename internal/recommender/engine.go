package recommender

import (
	"context"
	"math"

	"github.com/go-logr/logr"
	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	ContainerTypeStandard = "standard"
	ContainerTypeJava     = "java"
)

// baselineRecommender computes the baseline (non-burst) resource recommendation.
type baselineRecommender interface {
	baseline(metrics *collector.ContainerMetrics, cpuPolicy, memPolicy *v1alpha1.ContainerResourcePolicy, javaPolicy *v1alpha1.JavaPolicy) (cpuCores, memBytes float64)
}

// Engine dispatches to the right baseline recommender and applies the burst
// state machine on top.
type Engine struct {
	recommenders map[string]baselineRecommender
	fallback     baselineRecommender
}

func NewEngine(standard *StandardRecommender, java *JavaRecommender) *Engine {
	return &Engine{
		recommenders: map[string]baselineRecommender{
			ContainerTypeStandard: standard,
			ContainerTypeJava:     java,
		},
		fallback: standard,
	}
}

func (e *Engine) Recommend(ctx context.Context, input RecommendInput) (RecommendResult, error) {
	metrics := input.Metrics
	cfg := input.BurstConfig
	now := metav1.Now()

	log := logf.FromContext(ctx).WithValues(
		"container", metrics.Key.ContainerName,
		"workload", metrics.Key.WorkloadName,
		"namespace", metrics.Key.Namespace,
	)

	base, ok := e.recommenders[metrics.Key.ContainerType]
	if !ok {
		base = e.fallback
	}

	jvmAware := metrics.JVMMetrics != nil && input.JavaPolicy != nil && input.JavaPolicy.Enabled
	log.Info("recommender selected",
		"containerType", metrics.Key.ContainerType,
		"jvmMetricsPresent", metrics.JVMMetrics != nil,
		"jvmAware", jvmAware,
	)

	var cpuPolicy, memPolicy *v1alpha1.ContainerResourcePolicy
	if input.ContainerPolicy != nil {
		cpuPolicy = input.ContainerPolicy.CPU
		memPolicy = input.ContainerPolicy.Memory
	}
	baselineCPU, baselineMem := base.baseline(metrics, cpuPolicy, memPolicy, input.JavaPolicy)

	log.Info("os metrics",
		"cpu.live", roundm(metrics.CPULive),
		"cpu.p95", roundm(metrics.CPUP95),
		"mem.live_mi", mib(metrics.MemoryLive),
		"mem.p99_mi", mib(metrics.MemoryP99),
	)

	if metrics.JVMMetrics != nil {
		jvm := metrics.JVMMetrics
		log.Info("jvm metrics",
			"heap.live_mi", mib(jvm.HeapLive),
			"heap.used.p95_mi", mib(jvm.HeapUsedP95),
			"heap.max_mi", mib(jvm.HeapMaxBytes),
			"nonHeap.live_mi", mib(jvm.NonHeapLive),
			"nonHeap.p95_mi", mib(jvm.NonHeapUsedP95),
			"directBuf.p95_mi", mib(jvm.DirectBufferP95),
			"gc.overhead.p95_pct", math.Round(jvm.GCOverheadP95*100)/100,
		)
	}

	log.Info("baseline computed",
		"cpu.baseline", roundm(baselineCPU),
		"mem.baseline_mi", mib(baselineMem),
	)

	// Compute JVM flags when the policy has ManageJVMFlags enabled.
	// We type-assert to *JavaRecommender so the formula stays in one place.
	var jvmFlags *v1alpha1.JVMFlags
	if jr, ok := base.(*JavaRecommender); ok &&
		input.JavaPolicy != nil && input.JavaPolicy.ManageJVMFlags &&
		metrics.JVMMetrics != nil {
		jvmFlags = jr.jvmFlagsFor(metrics.JVMMetrics, input.JavaPolicy, baselineMem)
		log.Info("jvm flags computed", "maxRAMPercentage", jvmFlags.MaxRAMPercentage, "initialRAMPercentage", jvmFlags.InitialRAMPercentage)
	}

	state := input.CurrentBurst
	if state == nil || state.Phase == "" {
		state = &v1alpha1.BurstState{Phase: v1alpha1.BurstPhaseNormal}
	}

	log.Info("burst state", "phase", state.Phase)

	var result RecommendResult
	switch state.Phase {
	case v1alpha1.BurstPhaseBursting:
		result = e.handleBursting(log, metrics, cfg, state, baselineCPU, baselineMem)
	default:
		result = e.handleNormal(log, metrics, cfg, baselineCPU, baselineMem, now)
	}
	result.JVMFlags = jvmFlags
	return result, nil
}

func (e *Engine) handleNormal(log logr.Logger, metrics *collector.ContainerMetrics, cfg BurstConfig, baselineCPU, baselineMem float64, now metav1.Time) RecommendResult {
	if metrics.CPULive > baselineCPU*cfg.Threshold || metrics.MemoryLive > baselineMem*cfg.Threshold {
		log.Info("SPIKE DETECTED — transitioning to Bursting",
			"cpu.live", roundm(metrics.CPULive),
			"cpu.threshold", roundm(baselineCPU*cfg.Threshold),
			"mem.live_mi", mib(metrics.MemoryLive),
			"mem.threshold_mi", mib(baselineMem*cfg.Threshold),
		)
		newState := &v1alpha1.BurstState{
			Phase:          v1alpha1.BurstPhaseBursting,
			BurstStartTime: &now,
		}
		return e.burstingResult(log, metrics, cfg, newState, baselineCPU, baselineMem)
	}

	result := RecommendResult{
		Resources:  buildResources(baselineCPU, baselineMem),
		BurstState: &v1alpha1.BurstState{Phase: v1alpha1.BurstPhaseNormal},
	}
	log.Info("recommendation (Normal)",
		"cpu", result.Resources.Requests.Cpu().String(),
		"memory", result.Resources.Requests.Memory().String(),
	)
	return result
}

func (e *Engine) handleBursting(log logr.Logger, metrics *collector.ContainerMetrics, cfg BurstConfig, state *v1alpha1.BurstState, baselineCPU, baselineMem float64) RecommendResult {
	if metrics.CPULive <= baselineCPU*cfg.Threshold && metrics.MemoryLive <= baselineMem*cfg.Threshold {
		log.Info("spike ended — transitioning to Normal",
			"peak.cpu", quantityStr(state.BurstPeakCPU),
			"peak.mem", quantityStr(state.BurstPeakMemory),
		)
		return RecommendResult{
			Resources:  buildResources(baselineCPU, baselineMem),
			BurstState: &v1alpha1.BurstState{Phase: v1alpha1.BurstPhaseNormal},
		}
	}
	return e.burstingResult(log, metrics, cfg, state, baselineCPU, baselineMem)
}

func (e *Engine) burstingResult(log logr.Logger, metrics *collector.ContainerMetrics, cfg BurstConfig, state *v1alpha1.BurstState, baselineCPU, baselineMem float64) RecommendResult {
	// Use live usage as the basis, floored at baseline, then apply headroom multiplier.
	// No upper cap — the recommendation must track the actual spike.
	burstCPU := math.Max(metrics.CPULive, baselineCPU) * cfg.Multiplier
	burstMem := math.Max(metrics.MemoryLive, baselineMem) * cfg.Multiplier

	cpuQ := resource.NewMilliQuantity(int64(burstCPU*1000), resource.DecimalSI)
	memQ := resource.NewQuantity(int64(burstMem), resource.BinarySI)

	newState := state.DeepCopy()
	newState.Phase = v1alpha1.BurstPhaseBursting
	if state.BurstPeakCPU == nil || cpuQ.Cmp(*state.BurstPeakCPU) > 0 {
		newState.BurstPeakCPU = cpuQ
	}
	if state.BurstPeakMemory == nil || memQ.Cmp(*state.BurstPeakMemory) > 0 {
		newState.BurstPeakMemory = memQ
	}

	result := RecommendResult{
		Resources:  buildResources(burstCPU, burstMem),
		BurstState: newState,
	}
	log.Info("recommendation (Bursting)",
		"cpu", result.Resources.Requests.Cpu().String(),
		"memory", result.Resources.Requests.Memory().String(),
		"peak.cpu", cpuQ.String(),
		"peak.mem", memQ.String(),
	)
	return result
}

func buildResources(cpuCores, memBytes float64) corev1.ResourceRequirements {
	cpuMillis := max(int64(cpuCores*1000), 1)
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(int64(memBytes), resource.BinarySI),
		},
	}
}

// roundm rounds a CPU core value to millicores precision for readable logs.
func roundm(v float64) float64 { return math.Round(v*1000) / 1000 }

// mib converts bytes to MiB rounded to 1 decimal place.
func mib(b float64) float64 { return math.Round(b/1024/1024*10) / 10 }

// quantityStr safely stringifies a nil-able Quantity.
func quantityStr(q *resource.Quantity) string {
	if q == nil {
		return "none"
	}
	return q.String()
}
