package collector

import (
	"context"
	"time"
)

type JVMMetrics struct {
	HeapLive                                           float64
	HeapUsedP50, HeapUsedP95, HeapUsedP99, HeapUsedMax float64
	HeapMaxBytes                                       float64
	NonHeapLive                                        float64
	NonHeapUsedP95                                     float64
	MetaspaceUsedP95                                   float64
	DirectBufferP95                                    float64
	GCOverheadP50, GCOverheadP95, GCOverheadMax        float64
}
type ContainerKey struct {
	Namespace     string
	WorkloadKind  string
	WorkloadName  string
	ContainerName string
	ContainerType string // e.g. "standard", "java"
}

type ContainerMetrics struct {
	Key ContainerKey

	JVMMetrics *JVMMetrics

	CPULive float64
	CPUP50  float64
	CPUP95  float64
	CPUP99  float64
	CPUMax  float64

	MemoryLive float64
	MemoryP50  float64
	MemoryP95  float64
	MemoryP99  float64
	MemoryMax  float64

	SampleCount int
}

type Collector interface {
	Collect(ctx context.Context, key ContainerKey, window time.Duration) (*ContainerMetrics, error)
}
