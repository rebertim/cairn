package collector

import (
	"context"
	"time"
)

type ContainerKey struct {
	Namespace     string
	WorkloadKind  string
	WorkloadName  string
	ContainerName string
	ContainerType string // e.g. "standard", "java"
}

type ContainerMetrics struct {
	Key ContainerKey

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
	CollectContainer(ctx context.Context, key ContainerKey, window time.Duration) (*ContainerMetrics, error)
}
