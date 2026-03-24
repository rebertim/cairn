package collector

import (
	"context"
	"strings"
	"sync"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type podContainerKey struct {
	Namespace string
	Pod       string
	Container string
}

type rawEntry struct {
	// Standard
	CPULive, CPUP50, CPUP95, CPUP99, CPUMax                float64
	MemoryLive, MemoryP50, MemoryP95, MemoryP99, MemoryMax float64
	SampleCount                                            int
	// JVM (zero means absent)
	JVM *JVMMetrics
}

// MetricsCache holds a cluster-wide snapshot of container metrics, refreshed
// periodically in the background by collectClusterWide.
type MetricsCache struct {
	mu        sync.RWMutex
	entries   map[podContainerKey]*rawEntry
	collector *VictoriaMetricsCollector
	window    time.Duration
	interval  time.Duration
}

// NewMetricsCache creates a MetricsCache that uses collector to fetch metrics
// over the given window and refreshes every interval.
func NewMetricsCache(collector *VictoriaMetricsCollector, window, interval time.Duration) *MetricsCache {
	return &MetricsCache{
		entries:   make(map[podContainerKey]*rawEntry),
		collector: collector,
		window:    window,
		interval:  interval,
	}
}

// Start performs an immediate synchronous cache refresh so the cache is warm
// before the first reconcile cycle, then launches a background goroutine that
// re-refreshes every c.interval. The goroutine exits when ctx is done.
func (c *MetricsCache) Start(ctx context.Context) {
	log := logf.Log.WithName("metrics-cache")
	log.Info("Starting metrics cache background refresh")
	c.refresh(ctx)

	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refresh(ctx)
			}
		}
	}()
}

// refresh fetches a fresh cluster-wide snapshot and atomically swaps the entries map.
func (c *MetricsCache) refresh(ctx context.Context) {
	log := logf.Log.WithName("metrics-cache")
	log.Info("Refreshing metrics cache")

	entries := c.collector.collectClusterWide(ctx, c.window)

	c.mu.Lock()
	c.entries = entries
	c.mu.Unlock()

	log.Info("Metrics cache refreshed", "entries", len(entries))
}

func maxf(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

func aggregateRaw(agg *rawEntry, e *rawEntry) {
	agg.CPULive = maxf(agg.CPULive, e.CPULive)
	agg.CPUP50 = maxf(agg.CPUP50, e.CPUP50)
	agg.CPUP95 = maxf(agg.CPUP95, e.CPUP95)
	agg.CPUP99 = maxf(agg.CPUP99, e.CPUP99)
	agg.CPUMax = maxf(agg.CPUMax, e.CPUMax)
	agg.MemoryLive = maxf(agg.MemoryLive, e.MemoryLive)
	agg.MemoryP50 = maxf(agg.MemoryP50, e.MemoryP50)
	agg.MemoryP95 = maxf(agg.MemoryP95, e.MemoryP95)
	agg.MemoryP99 = maxf(agg.MemoryP99, e.MemoryP99)
	agg.MemoryMax = maxf(agg.MemoryMax, e.MemoryMax)
	if e.SampleCount > agg.SampleCount {
		agg.SampleCount = e.SampleCount
	}
}

func aggregateJVM(agg *JVMMetrics, e *JVMMetrics) *JVMMetrics {
	if e == nil {
		return agg
	}
	if agg == nil {
		agg = &JVMMetrics{}
	}
	agg.HeapLive = maxf(agg.HeapLive, e.HeapLive)
	agg.HeapUsedP50 = maxf(agg.HeapUsedP50, e.HeapUsedP50)
	agg.HeapUsedP95 = maxf(agg.HeapUsedP95, e.HeapUsedP95)
	agg.HeapUsedP99 = maxf(agg.HeapUsedP99, e.HeapUsedP99)
	agg.HeapUsedMax = maxf(agg.HeapUsedMax, e.HeapUsedMax)
	agg.HeapMaxBytes = maxf(agg.HeapMaxBytes, e.HeapMaxBytes)
	agg.NonHeapLive = maxf(agg.NonHeapLive, e.NonHeapLive)
	agg.NonHeapUsedP95 = maxf(agg.NonHeapUsedP95, e.NonHeapUsedP95)
	agg.MetaspaceUsedP95 = maxf(agg.MetaspaceUsedP95, e.MetaspaceUsedP95)
	agg.DirectBufferP95 = maxf(agg.DirectBufferP95, e.DirectBufferP95)
	agg.GCOverheadP50 = maxf(agg.GCOverheadP50, e.GCOverheadP50)
	agg.GCOverheadP95 = maxf(agg.GCOverheadP95, e.GCOverheadP95)
	agg.GCOverheadMax = maxf(agg.GCOverheadMax, e.GCOverheadMax)
	return agg
}

// Lookup aggregates cached metrics for all pods belonging to the given workload
// container. Pods are matched by namespace, container name, and pod name prefix
// (workloadName + "-"). Numeric fields are aggregated by max.
func (c *MetricsCache) Lookup(key ContainerKey) (*ContainerMetrics, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	prefix := key.WorkloadName + "-"

	var (
		found bool
		agg   rawEntry
		jvm   *JVMMetrics
	)

	for k, e := range c.entries {
		if k.Namespace != key.Namespace {
			continue
		}
		if k.Container != key.ContainerName {
			continue
		}
		if !strings.HasPrefix(k.Pod, prefix) {
			continue
		}

		found = true
		aggregateRaw(&agg, e)
		jvm = aggregateJVM(jvm, e.JVM)
	}

	if !found {
		return nil, false
	}

	m := &ContainerMetrics{
		Key:         key,
		CPULive:     agg.CPULive,
		CPUP50:      agg.CPUP50,
		CPUP95:      agg.CPUP95,
		CPUP99:      agg.CPUP99,
		CPUMax:      agg.CPUMax,
		MemoryLive:  agg.MemoryLive,
		MemoryP50:   agg.MemoryP50,
		MemoryP95:   agg.MemoryP95,
		MemoryP99:   agg.MemoryP99,
		MemoryMax:   agg.MemoryMax,
		SampleCount: agg.SampleCount,
		JVMMetrics:  jvm,
	}

	return m, true
}

// CachingCollector implements the Collector interface by serving metrics from
// a pre-populated MetricsCache instead of issuing per-workload Prometheus
// queries.
type CachingCollector struct {
	cache *MetricsCache
}

// NewCachingCollector wraps the given MetricsCache as a Collector.
func NewCachingCollector(cache *MetricsCache) *CachingCollector {
	return &CachingCollector{cache: cache}
}

// Collect satisfies the Collector interface. The window parameter is ignored
// because the cache is pre-fetched with a fixed window configured at creation
// time. A cache miss returns nil, nil — callers should skip the container.
func (c *CachingCollector) Collect(ctx context.Context, key ContainerKey, _ time.Duration) (*ContainerMetrics, error) {
	m, ok := c.cache.Lookup(key)
	if !ok {
		logf.FromContext(ctx).V(1).Info("no cached metrics yet, skipping container",
			"namespace", key.Namespace, "workload", key.WorkloadName, "container", key.ContainerName)
		return nil, nil
	}
	return m, nil
}
