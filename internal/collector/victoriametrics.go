package collector

import (
	"context"
	"fmt"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type VictoriaMetricsCollector struct {
	api promv1.API
}

func NewVictoriaMetricsCollector(api promv1.API) *VictoriaMetricsCollector {
	return &VictoriaMetricsCollector{api: api}
}

func (p *VictoriaMetricsCollector) Collect(ctx context.Context, key ContainerKey, window time.Duration) (*ContainerMetrics, error) {
	if key.ContainerType == "java" {
		return p.collectJava(ctx, key, window)
	}
	return p.collectStandard(ctx, key, window)
}

func (p *VictoriaMetricsCollector) collectStandard(ctx context.Context, key ContainerKey, window time.Duration) (*ContainerMetrics, error) {
	metrics := &ContainerMetrics{Key: key}

	// Aggregate across pods with max by (namespace, container) before any time
	// aggregation. This reduces N pod series to one, so terminated pods
	// naturally age out when VictoriaMetrics marks their series stale — preventing
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

func (p *VictoriaMetricsCollector) collectJava(ctx context.Context, key ContainerKey, window time.Duration) (*ContainerMetrics, error) {
	metrics := &ContainerMetrics{Key: key, JVMMetrics: &JVMMetrics{}}
	w := formatDuration(window)

	// CPU — same cAdvisor metrics as CollectContainer
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
		query := fmt.Sprintf(`quantile_over_time(%.2f, %s[%s:])`, pct.quantile, cpuBase, w)
		result, err := p.queryScalar(ctx, query)
		if err != nil {
			return nil, err
		}
		*pct.target = result
	}

	cpuMaxQuery := fmt.Sprintf(`max_over_time(%s[%s:])`, cpuBase, w)
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

	// Heap — agent metrics don't have image!="" label
	heapBase := fmt.Sprintf(
		`max by (namespace, container) (cairn_jvm_memory_heap_used_bytes{namespace="%s", pod=~"%s-.+", container="%s"})`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)
	for _, pct := range []struct {
		quantile float64
		target   *float64
	}{
		{0.50, &metrics.JVMMetrics.HeapUsedP50},
		{0.95, &metrics.JVMMetrics.HeapUsedP95},
		{0.99, &metrics.JVMMetrics.HeapUsedP99},
	} {
		query := fmt.Sprintf(`quantile_over_time(%.2f, %s[%s:])`, pct.quantile, heapBase, w)
		result, err := p.queryScalar(ctx, query)
		if err != nil {
			return nil, err
		}
		*pct.target = result
	}

	heapUsedMaxQuery := fmt.Sprintf(`max_over_time(%s[%s:])`, heapBase, w)
	heapUsedMax, err := p.queryScalar(ctx, heapUsedMaxQuery)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.HeapUsedMax = heapUsedMax

	// Instant heap usage — used for burst detection
	heapLive, err := p.queryScalar(ctx, heapBase)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.HeapLive = heapLive

	// HeapMaxBytes uses the heap_max metric (i.e. -Xmx), not heap_used
	heapMaxBase := fmt.Sprintf(
		`max by (namespace, container) (cairn_jvm_memory_heap_max_bytes{namespace="%s", pod=~"%s-.+", container="%s"})`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)
	heapMaxBytes, err := p.queryScalar(ctx, fmt.Sprintf(`last_over_time(%s[%s:])`, heapMaxBase, w))
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.HeapMaxBytes = heapMaxBytes

	nonHeapBase := fmt.Sprintf(
		`max by (namespace, container) (cairn_jvm_memory_nonheap_used_bytes{namespace="%s", pod=~"%s-.+", container="%s"})`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)

	nonHeapUsedP95Query := fmt.Sprintf(
		`max by (namespace, container) (quantile_over_time(0.95, cairn_jvm_memory_nonheap_used_bytes{namespace="%s", pod=~"%s-.+", container="%s"}[%s:]))`,
		key.Namespace, key.WorkloadName, key.ContainerName, w,
	)
	nonHeapUsedP95, err := p.queryScalar(ctx, nonHeapUsedP95Query)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.NonHeapUsedP95 = nonHeapUsedP95

	// Instant non-heap usage — used for burst detection
	nonHeapLive, err := p.queryScalar(ctx, nonHeapBase)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.NonHeapLive = nonHeapLive

	metaspaceUsedP95Query := fmt.Sprintf(
		`max by (namespace, container) (quantile_over_time(0.95, cairn_jvm_memory_pool_used_bytes{namespace="%s", pod=~"%s-.+", container="%s"}[%s:]))`,
		key.Namespace, key.WorkloadName, key.ContainerName, w,
	)
	metaspaceUsedP95, err := p.queryScalar(ctx, metaspaceUsedP95Query)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.MetaspaceUsedP95 = metaspaceUsedP95

	directBufferP95Query := fmt.Sprintf(
		`max by (namespace, container) (quantile_over_time(0.95, cairn_jvm_buffer_memory_used_bytes{namespace="%s", pod=~"%s-.+", container="%s"}[%s:]))`,
		key.Namespace, key.WorkloadName, key.ContainerName, w,
	)
	directBufferP95, err := p.queryScalar(ctx, directBufferP95Query)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.DirectBufferP95 = directBufferP95

	// GC overhead — use WorkloadName for pod=~, write to JVMMetrics fields
	gcOverheadBase := fmt.Sprintf(
		`max by (namespace, container) (cairn_jvm_gc_overhead_percent{namespace="%s", pod=~"%s-.+", container="%s"})`,
		key.Namespace, key.WorkloadName, key.ContainerName,
	)
	for _, pct := range []struct {
		quantile float64
		target   *float64
	}{
		{0.50, &metrics.JVMMetrics.GCOverheadP50},
		{0.95, &metrics.JVMMetrics.GCOverheadP95},
	} {
		query := fmt.Sprintf(`quantile_over_time(%.2f, %s[%s:])`, pct.quantile, gcOverheadBase, w)
		result, err := p.queryScalar(ctx, query)
		if err != nil {
			return nil, err
		}
		*pct.target = result
	}

	gcOverheadMaxQuery := fmt.Sprintf(`max_over_time(%s[%s:])`, gcOverheadBase, w)
	gcOverheadMax, err := p.queryScalar(ctx, gcOverheadMaxQuery)
	if err != nil {
		return nil, err
	}
	metrics.JVMMetrics.GCOverheadMax = gcOverheadMax

	// MemoryLive is set to the JVM-aware live value so burst detection in the
	// engine compares apples to apples against the JVM-aware baseline.
	// OS working-set is intentionally not used here — it includes JVM runtime
	// overhead (~20-30 MiB) that our baseline doesn't account for, which would
	// cause permanent false-positive burst triggers.
	metrics.MemoryLive = metrics.JVMMetrics.HeapLive +
		metrics.JVMMetrics.NonHeapLive +
		metrics.JVMMetrics.DirectBufferP95

	return metrics, nil
}

func (p *VictoriaMetricsCollector) queryScalar(ctx context.Context, query string) (float64, error) {
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
