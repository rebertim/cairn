package actuator

import (
	"context"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// DryRunActuator logs what would be applied without making any changes.
type DryRunActuator struct{}

func NewDryRunActuator() *DryRunActuator { return &DryRunActuator{} }

func (a *DryRunActuator) Apply(ctx context.Context, input ApplyInput) error {
	log := logf.FromContext(ctx)
	for _, c := range input.Containers {
		log.Info("[dry-run] would apply resources",
			"kind", input.Kind,
			"workload", input.Name,
			"namespace", input.Namespace,
			"container", c.Name,
			"cpu", c.Resources.Requests.Cpu().String(),
			"memory", c.Resources.Requests.Memory().String(),
		)
	}
	return nil
}
