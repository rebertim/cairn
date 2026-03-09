package actuator

import (
	"context"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RestartActuator patches container resources and triggers a rolling restart by
// writing a timestamp to spec.template.annotations. Safe default for all
// Kubernetes versions; use InPlaceActuator on clusters with k8s 1.27+ and the
// InPlacePodVerticalScaling feature gate enabled.
type RestartActuator struct {
	client client.Client
}

func NewRestartActuator(c client.Client) *RestartActuator {
	return &RestartActuator{client: c}
}

func (a *RestartActuator) Apply(ctx context.Context, input ApplyInput) error {
	return patchWorkload(ctx, a.client, input, time.Now().UTC().Format(time.RFC3339))
}
