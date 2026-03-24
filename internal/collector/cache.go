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
	CPULive, CPUP50, CPUP95, CPUP99, CPUMax float64
	MemoryLive, MemoryP50, MemoryP95, MemoryP99, MemoryMax float64
	SampleCount int
	// JVM (zero means absent)
	JVM *JVMMetrics
}

// MetricsCache holds a cluster-wide snapshot of container metrics, refreshed
// periodically in the background by collectClusterWide.
type MetricsCache struct {
	mu       sync.RWMutex
	entries  map[podContainerKey]*rawEntry
	collector *VictoriaMetricsCollector
	window   time.Duration
	interval time.Duration
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

	entries, err := c.collector.collectClusterWide(ctx, c.window)
	if err != nil {
		log.Error(err, "Failed to refresh metrics cache")
		return
	}

	c.mu.Lock()
	c.entries = entries
	c.mu.Unlock()

	log.Info("Metrics cache refreshed", "entries", len(entries))
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

		// Max-aggregate standard fields.
		if e.CPULive > agg.CPULive {
			agg.CPULive = e.CPULive
		}
		if e.CPUP50 > agg.CPUP50 {
			agg.CPUP50 = e.CPUP50
		}
		if e.CPUP95 > agg.CPUP95 {
			agg.CPUP95 = e.CPUP95
		}
		if e.CPUP99 > agg.CPUP99 {
			agg.CPUP99 = e.CPUP99
		}
		if e.CPUMax > agg.CPUMax {
			agg.CPUMax = e.CPUMax
		}
		if e.MemoryLive > agg.MemoryLive {
			agg.MemoryLive = e.MemoryLive
		}
		if e.MemoryP50 > agg.MemoryP50 {
			agg.MemoryP50 = e.MemoryP50
		}
		if e.MemoryP95 > agg.MemoryP95 {
			agg.MemoryP95 = e.MemoryP95
		}
		if e.MemoryP99 > agg.MemoryP99 {
			agg.MemoryP99 = e.MemoryP99
		}
		if e.MemoryMax > agg.MemoryMax {
			agg.MemoryMax = e.MemoryMax
		}
		if e.SampleCount > agg.SampleCount {
			agg.SampleCount = e.SampleCount
		}

		// Max-aggregate JVM fields when present.
		if e.JVM != nil {
			if jvm == nil {
				jvm = &JVMMetrics{}
			}
			if e.JVM.HeapLive > jvm.HeapLive {
				jvm.HeapLive = e.JVM.HeapLive
			}
			if e.JVM.HeapUsedP50 > jvm.HeapUsedP50 {
				jvm.HeapUsedP50 = e.JVM.HeapUsedP50
			}
			if e.JVM.HeapUsedP95 > jvm.HeapUsedP95 {
				jvm.HeapUsedP95 = e.JVM.HeapUsedP95
			}
			if e.JVM.HeapUsedP99 > jvm.HeapUsedP99 {
				jvm.HeapUsedP99 = e.JVM.HeapUsedP99
			}
			if e.JVM.HeapUsedMax > jvm.HeapUsedMax {
				jvm.HeapUsedMax = e.JVM.HeapUsedMax
			}
			if e.JVM.HeapMaxBytes > jvm.HeapMaxBytes {
				jvm.HeapMaxBytes = e.JVM.HeapMaxBytes
			}
			if e.JVM.NonHeapLive > jvm.NonHeapLive {
				jvm.NonHeapLive = e.JVM.NonHeapLive
			}
			if e.JVM.NonHeapUsedP95 > jvm.NonHeapUsedP95 {
				jvm.NonHeapUsedP95 = e.JVM.NonHeapUsedP95
			}
			if e.JVM.MetaspaceUsedP95 > jvm.MetaspaceUsedP95 {
				jvm.MetaspaceUsedP95 = e.JVM.MetaspaceUsedP95
			}
			if e.JVM.DirectBufferP95 > jvm.DirectBufferP95 {
				jvm.DirectBufferP95 = e.JVM.DirectBufferP95
			}
			if e.JVM.GCOverheadP50 > jvm.GCOverheadP50 {
				jvm.GCOverheadP50 = e.JVM.GCOverheadP50
			}
			if e.JVM.GCOverheadP95 > jvm.GCOverheadP95 {
				jvm.GCOverheadP95 = e.JVM.GCOverheadP95
			}
			if e.JVM.GCOverheadMax > jvm.GCOverheadMax {
				jvm.GCOverheadMax = e.JVM.GCOverheadMax
			}
		}
	}

	if !found {
		return nil, false
	}

	m := &ContainerMetrics{
		Key:        key,
		CPULive:    agg.CPULive,
		CPUP50:     agg.CPUP50,
		CPUP95:     agg.CPUP95,
		CPUP99:     agg.CPUP99,
		CPUMax:     agg.CPUMax,
		MemoryLive: agg.MemoryLive,
		MemoryP50:  agg.MemoryP50,
		MemoryP95:  agg.MemoryP95,
		MemoryP99:  agg.MemoryP99,
		MemoryMax:  agg.MemoryMax,
		SampleCount: agg.SampleCount,
		JVMMetrics: jvm,
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
