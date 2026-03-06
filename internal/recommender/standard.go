package recommender

import (
	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
)

type StandardRecommender struct{}

func NewStandardRecommender() *StandardRecommender {
	return &StandardRecommender{}
}

// baseline returns CPU (cores) and memory (bytes) adjusted for the configured
// percentile, headroom, and min/max bounds from the policy.
func (r *StandardRecommender) baseline(metrics *collector.ContainerMetrics, cpuPolicy, memPolicy *v1alpha1.ContainerResourcePolicy, _ *v1alpha1.JavaPolicy) (cpuCores, memBytes float64) {
	cpu := applyHeadroomAndClamp(selectPercentile(metrics.CPUP50, metrics.CPUP95, metrics.CPUP99, cpuPolicy), cpuPolicy)
	mem := applyHeadroomAndClamp(selectPercentile(metrics.MemoryP50, metrics.MemoryP95, metrics.MemoryP99, memPolicy), memPolicy)
	return cpu, mem
}
