package recommender

import (
	"context"

	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	"github.com/sempex/cairn/internal/collector"
	corev1 "k8s.io/api/core/v1"
)

// BurstConfig holds the parameters for burst detection and reaction.
type BurstConfig struct {
	// Threshold triggers burst mode when live > baseline * Threshold.
	Threshold float64
	// Multiplier sets the burst recommendation to max(live, baseline) * Multiplier.
	Multiplier float64
}

func DefaultBurstConfig() BurstConfig {
	return BurstConfig{
		Threshold:  1.5,
		Multiplier: 1.3,
	}
}

// RecommendInput bundles everything the engine needs to produce a recommendation.
type RecommendInput struct {
	Metrics         *collector.ContainerMetrics
	BurstConfig     BurstConfig
	CurrentBurst    *v1alpha1.BurstState        // nil is treated as Normal
	ContainerPolicy *v1alpha1.ContainerPolicies // nil uses defaults
	JavaPolicy      *v1alpha1.JavaPolicy        // nil disables JVM-aware logic
}

// RecommendResult is returned by Recommend.
type RecommendResult struct {
	Resources  corev1.ResourceRequirements
	BurstState *v1alpha1.BurstState
	// JVMFlags holds recommended JVM flags (-Xmx, -Xms). Non-nil only for
	// Java containers when ManageJVMFlags is enabled on the policy.
	JVMFlags *v1alpha1.JVMFlags
}

// Recommender is the interface the controller depends on.
type Recommender interface {
	Recommend(ctx context.Context, input RecommendInput) (RecommendResult, error)
}
