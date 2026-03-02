package recommender

import (
	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
)

// DefaultJVMOverheadFactor accounts for non-heap memory (metaspace, JIT code
// cache, direct buffers) that container_memory_working_set_bytes does not
// fully capture.
const DefaultJVMOverheadFactor = 1.25

type JavaRecommender struct {
	OverheadFactor float64
}

func NewJavaRecommender() *JavaRecommender {
	return &JavaRecommender{OverheadFactor: DefaultJVMOverheadFactor}
}

// baseline returns CPU (cores) and memory (bytes). Follows the same percentile
// and headroom logic as StandardRecommender, but applies the JVM overhead
// factor to raw memory before headroom is added.
// JavaPolicy integration (HeapHeadroomPercent, GCOverheadWeight, etc.) is
// deferred to a later stage.
func (r *JavaRecommender) baseline(metrics *collector.ContainerMetrics, cpuPolicy, memPolicy *v1alpha1.ContainerResourcePolicy) (cpuCores, memBytes float64) {
	cpu := applyHeadroomAndClamp(selectPercentile(metrics.CPUP50, metrics.CPUP95, metrics.CPUP99, cpuPolicy), cpuPolicy)
	rawMem := selectPercentile(metrics.MemoryP50, metrics.MemoryP95, metrics.MemoryP99, memPolicy) * r.OverheadFactor
	mem := applyHeadroomAndClamp(rawMem, memPolicy)
	return cpu, mem
}
