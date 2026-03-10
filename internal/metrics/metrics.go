package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	v1alpha1 "github.com/sempex/cairn/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	containerCPURequestCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cairn",
		Name:      "container_cpu_request_cores",
		Help:      "CPU request currently set on the container (cores).",
	}, []string{"namespace", "workload", "kind", "container"})

	containerMemoryRequestBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cairn",
		Name:      "container_memory_request_bytes",
		Help:      "Memory request currently set on the container (bytes).",
	}, []string{"namespace", "workload", "kind", "container"})

	containerRecommendedCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cairn",
		Name:      "container_recommended_cpu_cores",
		Help:      "CPU recommended by Cairn for the container (cores).",
	}, []string{"namespace", "workload", "kind", "container"})

	containerRecommendedMemoryBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cairn",
		Name:      "container_recommended_memory_bytes",
		Help:      "Memory recommended by Cairn for the container (bytes).",
	}, []string{"namespace", "workload", "kind", "container"})

	containerBurstActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cairn",
		Name:      "container_burst_active",
		Help:      "1 if the container is currently in the Bursting phase, 0 otherwise.",
	}, []string{"namespace", "workload", "kind", "container"})

	managedWorkloads = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "cairn",
		Name:      "managed_workloads",
		Help:      "Number of workloads currently managed by a RightsizePolicy.",
	}, []string{"namespace", "policy"})

	appliesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cairn",
		Name:      "applies_total",
		Help:      "Total number of resource patches applied to workloads.",
	}, []string{"namespace", "workload", "kind", "strategy"})

	burstDetectionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "cairn",
		Name:      "burst_detections_total",
		Help:      "Total number of burst events detected (Normal → Bursting transitions).",
	}, []string{"namespace", "workload", "kind", "container"})

	policyReconcileDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "cairn",
		Name:      "policy_reconcile_duration_seconds",
		Help:      "Duration of RightsizePolicy reconcile cycles.",
		Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30},
	}, []string{"namespace", "policy"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		containerCPURequestCores,
		containerMemoryRequestBytes,
		containerRecommendedCPUCores,
		containerRecommendedMemoryBytes,
		containerBurstActive,
		managedWorkloads,
		appliesTotal,
		burstDetectionsTotal,
		policyReconcileDuration,
	)
}

// RecordContainerRecommendation updates all per-container gauges and detects
// burst transitions. Called once per container per policy reconcile cycle.
func RecordContainerRecommendation(
	namespace, workload, kind, container string,
	current corev1.ResourceRequirements,
	recommended corev1.ResourceRequirements,
	prevBurst, newBurst *v1alpha1.BurstState,
) {
	lbls := []string{namespace, workload, kind, container}

	if cpu := current.Requests.Cpu(); cpu != nil {
		containerCPURequestCores.WithLabelValues(lbls...).Set(float64(cpu.MilliValue()) / 1000)
	}
	if mem := current.Requests.Memory(); mem != nil {
		containerMemoryRequestBytes.WithLabelValues(lbls...).Set(float64(mem.Value()))
	}
	if cpu := recommended.Requests.Cpu(); cpu != nil {
		containerRecommendedCPUCores.WithLabelValues(lbls...).Set(float64(cpu.MilliValue()) / 1000)
	}
	if mem := recommended.Requests.Memory(); mem != nil {
		containerRecommendedMemoryBytes.WithLabelValues(lbls...).Set(float64(mem.Value()))
	}

	burstActive := 0.0
	if newBurst != nil && newBurst.Phase == v1alpha1.BurstPhaseBursting {
		burstActive = 1.0
	}
	containerBurstActive.WithLabelValues(lbls...).Set(burstActive)

	// Count Normal → Bursting transitions.
	prevPhase := v1alpha1.BurstPhaseNormal
	if prevBurst != nil {
		prevPhase = prevBurst.Phase
	}
	if prevPhase == v1alpha1.BurstPhaseNormal && burstActive == 1.0 {
		burstDetectionsTotal.WithLabelValues(namespace, workload, kind, container).Inc()
	}
}

// RecordManagedWorkloads sets the managed workloads gauge for a policy.
func RecordManagedWorkloads(namespace, policy string, count int) {
	managedWorkloads.WithLabelValues(namespace, policy).Set(float64(count))
}

// RecordApply increments the apply counter for a workload.
func RecordApply(namespace, workload, kind, strategy string) {
	appliesTotal.WithLabelValues(namespace, workload, kind, strategy).Inc()
}

// ReconcileTimer returns a function that, when called, records the elapsed
// duration for a policy reconcile. Use with defer:
//
//	defer metrics.ReconcileTimer(namespace, policy)()
func ReconcileTimer(namespace, policy string) func() {
	start := time.Now()
	return func() {
		policyReconcileDuration.WithLabelValues(namespace, policy).
			Observe(time.Since(start).Seconds())
	}
}
