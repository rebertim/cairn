package recommender

import (
	"context"
	"time"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	corev1 "k8s.io/api/core/v1"
)

// BurstConfig holds the parameters for burst detection and reaction.
type BurstConfig struct {
	// Threshold triggers burst mode when live > baseline * Threshold.
	Threshold float64
	// Multiplier sets the burst recommendation to live * Multiplier.
	Multiplier float64
	// MaxBurstMultiplier caps the burst recommendation at baseline * MaxBurstMultiplier.
	MaxBurstMultiplier float64
	// CooldownWindow is how long to spend in Recovering before returning to Normal.
	CooldownWindow time.Duration
}

func DefaultBurstConfig() BurstConfig {
	return BurstConfig{
		Threshold:          1.5,
		Multiplier:         1.3,
		MaxBurstMultiplier: 3.0,
		CooldownWindow:     15 * time.Minute,
	}
}

// RecommendInput bundles everything the engine needs to produce a recommendation.
type RecommendInput struct {
	Metrics         *collector.ContainerMetrics
	BurstConfig     BurstConfig
	CurrentBurst    *v1alpha1.BurstState        // nil is treated as Normal
	ContainerPolicy *v1alpha1.ContainerPolicies // nil uses defaults
}

// RecommendResult is returned by Recommend.
type RecommendResult struct {
	Resources  corev1.ResourceRequirements
	BurstState *v1alpha1.BurstState
}

// Recommender is the interface the controller depends on.
type Recommender interface {
	Recommend(ctx context.Context, input RecommendInput) (RecommendResult, error)
}
