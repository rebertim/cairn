package actuator

import (
	"context"
	"math"
	"time"

	rightsizingv1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultMinApplyInterval = 5 * time.Minute

// EngineInput bundles everything the engine needs to decide whether and how to
// apply a recommendation.
type EngineInput struct {
	Recommendation *rightsizingv1alpha1.RightsizeRecommendation
	Policy         *rightsizingv1alpha1.RightsizePolicy
}

// EngineResult is returned by Engine.Apply.
type EngineResult struct {
	// Applied is true when resources were patched on the workload.
	Applied bool
}

// Engine dispatches recommendations to the right actuator after running the
// change gate and respecting the policy mode.
type Engine struct {
	dryRun  Actuator
	inPlace Actuator
	restart Actuator
}

func NewEngine(dryRun, inPlace, restart Actuator) *Engine {
	return &Engine{dryRun: dryRun, inPlace: inPlace, restart: restart}
}

// Apply evaluates the recommendation against the policy and applies it if all
// conditions are met. The controller writes the returned EngineResult back to
// the recommendation status.
func (e *Engine) Apply(ctx context.Context, input EngineInput) (EngineResult, error) {
	rec := input.Recommendation
	policy := input.Policy
	log := logf.FromContext(ctx).WithValues(
		"workload", rec.Spec.TargetRef.Name,
		"kind", rec.Spec.TargetRef.Kind,
		"namespace", rec.Namespace,
	)

	if len(rec.Status.Containers) == 0 {
		return EngineResult{}, nil
	}

	switch policy.Spec.Mode {
	case rightsizingv1alpha1.PolicyModeDryRun:
		if err := e.dryRun.Apply(ctx, buildApplyInput(rec)); err != nil {
			log.Error(err, "dry-run failed")
		}
		return EngineResult{}, nil

	case rightsizingv1alpha1.PolicyModeAuto:
		threshold := float64(policy.Spec.ChangeThreshold) / 100.0
		if !hasSignificantChange(rec.Status.Containers, threshold) {
			return EngineResult{}, nil
		}

		cooldown := policy.Spec.MinApplyInterval.Duration
		if cooldown == 0 {
			cooldown = defaultMinApplyInterval
		}
		if rec.Status.LastAppliedTime != nil && time.Since(rec.Status.LastAppliedTime.Time) < cooldown {
			log.Info("skipping apply — within cooldown",
				"lastApplied", rec.Status.LastAppliedTime.Time,
				"cooldown", cooldown,
				"remaining", cooldown-time.Since(rec.Status.LastAppliedTime.Time),
			)
			return EngineResult{}, nil
		}

		act := e.pickActuator(policy.Spec.UpdateStrategy)
		if err := act.Apply(ctx, buildApplyInput(rec)); err != nil {
			return EngineResult{}, err
		}
		log.Info("applied recommendation", "updateStrategy", policy.Spec.UpdateStrategy)
		return EngineResult{Applied: true}, nil

	default:
		// recommend mode — nothing to apply.
		return EngineResult{}, nil
	}
}

func (e *Engine) pickActuator(strategy rightsizingv1alpha1.UpdateStrategy) Actuator {
	if strategy == rightsizingv1alpha1.UpdateStrategyInPlace {
		return e.inPlace
	}
	return e.restart
}

// hasSignificantChange returns true if any container's recommended resources
// differ from current by more than the given fractional threshold.
func hasSignificantChange(containers []rightsizingv1alpha1.ContainerRecommendation, threshold float64) bool {
	for _, c := range containers {
		if c.Recommended == nil {
			continue
		}
		if resourceChangePct(c.Current.Requests, c.Recommended.Requests) > threshold {
			return true
		}
	}
	return false
}

// resourceChangePct returns the maximum fractional change across CPU and memory.
func resourceChangePct(current, recommended corev1.ResourceList) float64 {
	maxPct := 0.0
	for _, res := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		cur := current[res]
		rec := recommended[res]
		if cur.IsZero() {
			continue
		}
		pct := math.Abs(float64(rec.MilliValue())-float64(cur.MilliValue())) / float64(cur.MilliValue())
		if pct > maxPct {
			maxPct = pct
		}
	}
	return maxPct
}

// buildApplyInput converts a recommendation into the actuator input type.
func buildApplyInput(rec *rightsizingv1alpha1.RightsizeRecommendation) ApplyInput {
	patches := make([]ContainerPatch, 0, len(rec.Status.Containers))
	for _, c := range rec.Status.Containers {
		if c.Recommended == nil {
			continue
		}
		patch := ContainerPatch{
			Name:      c.ContainerName,
			Resources: *c.Recommended,
		}
		if c.JVM != nil {
			patch.JVMFlags = c.JVM.RecommendedFlags
		}
		patches = append(patches, patch)
	}
	return ApplyInput{
		Kind:       rec.Spec.TargetRef.Kind,
		Name:       rec.Spec.TargetRef.Name,
		Namespace:  rec.Namespace,
		Containers: patches,
	}
}
