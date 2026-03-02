package collector

import (
	"context"
	"fmt"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type PrometheusCollector struct {
	api promv1.API
}

func NewPrometheusCollector(api promv1.API) *PrometheusCollector {
	return &PrometheusCollector{
		api: api,
	}
}

func (p *PrometheusCollector) CollectContainer(ctx context.Context, key ContainerKey, window time.Duration) (*ContainerMetrics, error) {
	metrics := &ContainerMetrics{Key: key}

	// Aggregate across pods with max by (namespace, container) before any time
	// aggregation. This reduces N pod series to one, so terminated pods
	// naturally age out when Prometheus marks their series stale — preventing
	// dead-pod burst events from contaminating historical percentiles.

	cpuBase := fmt.Sprintf(
		`max by (namespace, container) (rate(container_cpu_usage_seconds_total{namespace="%s", pod=~"%s-.+", container="%s", image!=""}[5m]))`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)

	for _, pct := range []struct {
		quantile float64
		target   *float64
	}{
		{0.50, &metrics.CPUP50},
		{0.95, &metrics.CPUP95},
		{0.99, &metrics.CPUP99},
	} {
		query := fmt.Sprintf(`quantile_over_time(%.2f, %s[%s:])`, pct.quantile, cpuBase, formatDuration(window))
		result, err := p.queryScalar(ctx, query)
		if err != nil {
			return nil, err
		}
		*pct.target = result
	}

	cpuMaxQuery := fmt.Sprintf(`max_over_time(%s[%s:])`, cpuBase, formatDuration(window))
	cpuMax, err := p.queryScalar(ctx, cpuMaxQuery)
	if err != nil {
		return nil, err
	}
	metrics.CPUMax = cpuMax

	cpuLiveQuery := fmt.Sprintf(
		`max by (namespace, container) (rate(container_cpu_usage_seconds_total{namespace="%s", pod=~"%s-.+", container="%s", image!=""}[1m]))`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)
	cpuLive, err := p.queryScalar(ctx, cpuLiveQuery)
	if err != nil {
		return nil, err
	}
	metrics.CPULive = cpuLive

	memBase := fmt.Sprintf(
		`max by (namespace, container) (container_memory_working_set_bytes{namespace="%s", pod=~"%s-.+", container="%s", image!=""})`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)

	for _, pct := range []struct {
		quantile float64
		target   *float64
	}{
		{0.50, &metrics.MemoryP50},
		{0.95, &metrics.MemoryP95},
		{0.99, &metrics.MemoryP99},
	} {
		query := fmt.Sprintf(`quantile_over_time(%.2f, %s[%s:])`, pct.quantile, memBase, formatDuration(window))
		result, err := p.queryScalar(ctx, query)
		if err != nil {
			return nil, err
		}
		*pct.target = result
	}

	memMaxQuery := fmt.Sprintf(`max_over_time(%s[%s:])`, memBase, formatDuration(window))
	memMax, err := p.queryScalar(ctx, memMaxQuery)
	if err != nil {
		return nil, fmt.Errorf("memory max: %w", err)
	}
	metrics.MemoryMax = memMax

	memLiveQuery := fmt.Sprintf(
		`max by (namespace, container) (container_memory_working_set_bytes{namespace="%s", pod=~"%s-.+", container="%s", image!=""})`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)
	memLive, err := p.queryScalar(ctx, memLiveQuery)
	if err != nil {
		return nil, err
	}
	metrics.MemoryLive = memLive

	countQuery := fmt.Sprintf(`count_over_time(%s[%s:])`, memBase, formatDuration(window))
	count, err := p.queryScalar(ctx, countQuery)
	if err != nil {
		metrics.SampleCount = 0
	} else {
		metrics.SampleCount = int(count)
	}

	return metrics, nil
}

func (p *PrometheusCollector) queryScalar(ctx context.Context, query string) (float64, error) {
	result, _, err := p.api.Query(ctx, query, time.Now())
	if err != nil {
		return 0, err
	}

	switch v := result.(type) {
	case model.Vector:
		if len(v) == 0 {
			return 0, fmt.Errorf("empty result for query: %s", query)
		}
		max := float64(v[0].Value)
		for _, sample := range v {
			if float64(sample.Value) > max {
				max = float64(sample.Value)
			}
		}
		return max, nil
	case *model.Scalar:
		return float64(v.Value), nil
	default:
		return 0, fmt.Errorf("unexpected result type %T for query: %s", result, query)
	}
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	if hours >= 24 && hours%24 == 0 {
		return fmt.Sprintf("%dd", hours/24)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	minutes := int(d.Minutes())
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
