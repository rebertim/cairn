package recommender

import (
	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
)

// DefaultJVMOverheadFactor is used when JVMMetrics are unavailable and we fall
// back to OS working-set memory. It accounts for non-heap regions that
// container_memory_working_set_bytes doesn't capture directly.
const DefaultJVMOverheadFactor = 1.25

// nonHeapOverhead is the safety margin added on top of observed non-heap and
// direct buffer usage (metaspace, code cache, etc.).
const nonHeapOverhead = 1.10

type JavaRecommender struct {
	OverheadFactor float64
}

func NewJavaRecommender() *JavaRecommender {
	return &JavaRecommender{OverheadFactor: DefaultJVMOverheadFactor}
}

func (r *JavaRecommender) baseline(metrics *collector.ContainerMetrics, cpuPolicy, memPolicy *v1alpha1.ContainerResourcePolicy, javaPolicy *v1alpha1.JavaPolicy) (cpuCores, memBytes float64) {
	if metrics.JVMMetrics != nil && javaPolicy != nil && javaPolicy.Enabled {
		return r.jvmBaseline(metrics, metrics.JVMMetrics, cpuPolicy, memPolicy, javaPolicy)
	}
	// Fallback: OS working-set metrics with a flat overhead factor.
	cpu := applyHeadroomAndClamp(selectPercentile(metrics.CPUP50, metrics.CPUP95, metrics.CPUP99, cpuPolicy), cpuPolicy)
	rawMem := selectPercentile(metrics.MemoryP50, metrics.MemoryP95, metrics.MemoryP99, memPolicy) * r.OverheadFactor
	mem := applyHeadroomAndClamp(rawMem, memPolicy)
	return cpu, mem
}

func (r *JavaRecommender) jvmBaseline(
	metrics *collector.ContainerMetrics,
	jvm *collector.JVMMetrics,
	cpuPolicy, memPolicy *v1alpha1.ContainerResourcePolicy,
	jp *v1alpha1.JavaPolicy,
) (cpuCores, memBytes float64) {
	// --- CPU ---
	// Start with the standard percentile + headroom, then inflate by GC pressure.
	baseCPU := applyHeadroomAndClamp(selectPercentile(metrics.CPUP50, metrics.CPUP95, metrics.CPUP99, cpuPolicy), cpuPolicy)

	gcWeight := 1.0
	if jp.GCOverheadWeight != nil {
		gcWeight = jp.GCOverheadWeight.AsApproximateFloat64()
	}
	// cpu * (1 + gcOverheadP95% * weight)
	// e.g. 10% GC overhead at weight 1.0 → inflate CPU by 10%
	cpu := baseCPU * (1 + jvm.GCOverheadP95/100*gcWeight)

	// --- Memory ---
	// Heap: observed P95 + JVM-specific headroom (HeapHeadroomPercent).
	heapTarget := jvm.HeapUsedP95 * (1 + float64(jp.HeapHeadroomPercent)/100)

	// Non-heap (metaspace, code cache, etc.) and direct buffers: add a fixed
	// overhead margin since these regions grow incrementally and are harder to
	// predict than heap.
	nonHeap := jvm.NonHeapUsedP95 * nonHeapOverhead
	directBuf := jvm.DirectBufferP95

	rawMem := heapTarget + nonHeap + directBuf

	// Only clamp to policy bounds — headroom is already applied JVM-specifically
	// above, so we don't double-count via applyHeadroomAndClamp.
	mem := clampToPolicy(rawMem, memPolicy)

	return cpu, mem
}
