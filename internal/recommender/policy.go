package recommender

import v1alpha1 "github.com/sempex/cairn/api/v1alpha1"

const defaultHeadroomPercent = 15

// selectPercentile picks the metric value matching the configured percentile.
// Only p50, p95, and p99 are available; anything ≤50 maps to p50, ≥98 maps to
// p99, and everything else (including the default of 95) maps to p95.
func selectPercentile(p50, p95, p99 float64, policy *v1alpha1.ContainerResourcePolicy) float64 {
	if policy == nil {
		return p95
	}
	switch {
	case policy.Percentile <= 50:
		return p50
	case policy.Percentile >= 98:
		return p99
	default:
		return p95
	}
}

// applyHeadroomAndClamp multiplies value by (1 + headroom%) then enforces
// the configured MinRequest / MaxRequest bounds.
func applyHeadroomAndClamp(value float64, policy *v1alpha1.ContainerResourcePolicy) float64 {
	headroom := float64(defaultHeadroomPercent) / 100.0
	if policy != nil {
		headroom = float64(policy.HeadroomPercent) / 100.0
	}
	result := value * (1 + headroom)

	if policy == nil {
		return result
	}
	if policy.MinRequest != nil {
		if min := policy.MinRequest.AsApproximateFloat64(); result < min {
			result = min
		}
	}
	if policy.MaxRequest != nil {
		if max := policy.MaxRequest.AsApproximateFloat64(); result > max {
			result = max
		}
	}
	return result
}
