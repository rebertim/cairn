# Architecture

## Overview

Cairn consists of four main components that work together to observe, recommend, and apply resource changes to Kubernetes workloads.

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kubernetes Cluster                        │
│                                                                  │
│  ┌──────────┐   admits    ┌─────────────────────────────────┐   │
│  │   Pod    │◄────────────│      Mutating Webhook           │   │
│  │(Java app)│             │  - detects Java containers      │   │
│  │          │             │  - injects cairn-agent JAR      │   │
│  │ :9404    │             │  - sets cairn.io/container-type │   │
│  └────┬─────┘             └─────────────────────────────────┘   │
│       │ metrics                                                  │
│       ▼                                                          │
│  ┌──────────────┐         ┌─────────────────────────────────┐   │
│  │VictoriaMetrics│◄───────│       Policy Controller         │   │
│  │  (VMSingle)  │ queries │  - reads RightsizePolicy        │   │
│  └──────────────┘         │  - collects metrics             │   │
│                           │  - runs recommender engine      │   │
│                           │  - writes RightsizeRecommendation│  │
│                           └────────────────┬────────────────┘   │
│                                            │ writes             │
│                           ┌────────────────▼────────────────┐   │
│                           │  RightsizeRecommendation (CRD)  │   │
│                           │  - per-container recommendations │   │
│                           │  - JVM flags (Xmx, Xms)        │   │
│                           │  - burst state                  │   │
│                           │  - dataReadySince               │   │
│                           └────────────────┬────────────────┘   │
│                                            │ watches            │
│                           ┌────────────────▼────────────────┐   │
│                           │    Recommendation Controller    │   │
│                           │  - checks observation window    │   │
│                           │  - checks change threshold      │   │
│                           │  - runs actuator engine         │   │
│                           │  - patches Deployment/resources │   │
│                           │  - updates JAVA_TOOL_OPTIONS    │   │
│                           └─────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Components

### Mutating Webhook

Intercepts pod creation. For Java containers (detected by image or annotation), it:

- Mounts the cairn-agent JAR from an init container
- Appends `-javaagent:/agent/cairn-agent.jar` to `JAVA_TOOL_OPTIONS`
- Adds the `cairn.io/container-type: java` annotation to the pod
- Applies the latest `RightsizeRecommendation` (resources + JVM flags) to the pod at admission time

The webhook is fail-open — if it errors, the pod is admitted without instrumentation.

### cairn-agent (JVM agent)

A lightweight Java agent that runs inside the JVM and exposes Prometheus-compatible metrics on port `9404`:

- `cairn_jvm_memory_heap_used_bytes` / `cairn_jvm_memory_heap_max_bytes`
- `cairn_jvm_memory_nonheap_used_bytes`
- `cairn_jvm_gc_overhead_percent` (fraction of time spent in GC)
- `cairn_jvm_buffer_memory_used_bytes` (direct buffers)
- `cairn_jvm_memory_pool_used_bytes` (per memory pool, including Metaspace)

The agent is fail-open: if it fails to start (e.g. missing module, classloading error), it catches `Throwable` and lets the JVM continue normally.

### Policy Controller

Reconciles `RightsizePolicy` and `ClusterRightsizePolicy` resources. On each reconcile cycle (default every 2 minutes):

1. Discovers target workloads from `spec.targetRef`
2. Reads the `cairn.io/container-type` annotation from running pods to select the right recommender
3. Queries VictoriaMetrics for CPU, memory, and JVM metrics over the configured `window`
4. Runs the **recommender engine** for each container
5. Creates or updates `RightsizeRecommendation` objects with the results
6. Sets `status.dataReadySince` on the first reconcile that produces non-empty container data

### Recommender Engine

Produces per-container resource recommendations. Operates in two paths:

**Standard path** (non-Java): `cpu.p95 + headroom%`, `memory.p95 * overhead_factor`

**JVM-aware path** (Java with agent metrics):

```
cpu    = cpu.p95 * (1 + gcOverhead% * gcOverheadWeight) * (1 + headroomPercent/100)
memory = heapTarget + nonHeap.p95 * 1.10 + directBuffer.p95

where:
  heapTarget = heap.p95 * (1 + heapHeadroomPercent/100)
                        * (1 + gcOverhead% * gcOverheadWeight)
```

GC overhead inflates both CPU and heap: high GC pressure signals the heap ceiling is too tight.

The engine also runs the **burst state machine** on top of the baseline recommendation.

#### Burst State Machine

```
         live > baseline * 1.5
Normal ─────────────────────────► Bursting
  ▲                                   │
  │  live <= baseline * 1.5           │
  └───────────────────────────────────┘
```

During **Bursting**: recommendation = `max(live, baseline) * 1.3` — tracks the actual spike with no artificial ceiling.

When the spike ends, the machine returns directly to **Normal**. The change threshold gates whether a downscale apply is triggered.

### Recommendation Controller

Reconciles `RightsizeRecommendation` resources. Runs the **actuator engine**:

1. Checks the policy `mode` — `recommend` returns immediately, `dry-run` logs and returns
2. In `auto` mode: checks the **observation window** — if `minObservationWindow` has not elapsed since `status.dataReadySince`, no action
3. Checks the **change gate** — if the recommendation is within `changeThreshold%` of current resources, no action
4. Checks the **cooldown** — if `minApplyInterval` has not elapsed since `status.lastAppliedTime`, no action
5. Calls the appropriate actuator (`restart` or `in-place`) and writes `lastAppliedTime` to status

### Actuator Engine

**Restart actuator**: patches container resources and sets `kubectl.kubernetes.io/restartedAt` on the pod template, triggering a rolling restart. Works on all Kubernetes versions.

**In-place actuator**: patches container resources only, no restart. Requires Kubernetes 1.27+ with `InPlacePodVerticalScaling` feature gate.

When `manageJvmFlags: true`, both actuators also update `JAVA_TOOL_OPTIONS` on the container to set `-Xmx` and `-Xms`. Existing flags are preserved — only `-Xmx`/`-Xms` entries are replaced.

## Metrics stack

Cairn bundles [VictoriaMetrics](https://victoriametrics.com) via the `victoria-metrics-k8s-stack` Helm dependency. This provides:

- **VMSingle** — single-node time series storage, queried by the Cairn controller on port 8428
- **VMAgent** — metrics scraper that collects cAdvisor, kubelet, kube-state-metrics, and cairn-agent metrics
- **Grafana** — pre-provisioned Cairn dashboard

The `VMAgent` scrapes the cairn-agent metrics endpoint on pods labeled `cairn.io/agent-injected: "true"` via a `VMPodScrape` resource. cAdvisor and kubelet metrics are collected via `VMNodeScrape` resources.

## Data flow

```
VictoriaMetrics metrics
      │
      ▼
Policy Controller ──► Recommender Engine ──► RightsizeRecommendation
                                                      │
                                          Recommendation Controller
                                                      │
                                          Actuator Engine
                                           (obs window + change gate + cooldown)
                                                      │
                                    ┌─────────────────┴──────────────────┐
                                    │                                    │
                              Restart Actuator                   In-Place Actuator
                          (resources + restartedAt)           (resources only)
                          (+ JAVA_TOOL_OPTIONS if Java)       (+ JAVA_TOOL_OPTIONS if Java)
```
