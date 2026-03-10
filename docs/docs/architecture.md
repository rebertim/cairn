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
│  ┌──────────┐             ┌─────────────────────────────────┐   │
│  │Prometheus│◄────────────│       Policy Controller         │   │
│  │          │  scrapes    │  - reads RightsizePolicy        │   │
│  └──────────┘             │  - collects metrics             │   │
│                           │  - runs recommender engine      │   │
│                           │  - writes RightsizeRecommendation│  │
│                           └────────────────┬────────────────┘   │
│                                            │ writes             │
│                           ┌────────────────▼────────────────┐   │
│                           │  RightsizeRecommendation (CRD)  │   │
│                           │  - per-container recommendations │   │
│                           │  - JVM flags (Xmx, Xms)        │   │
│                           │  - burst state                  │   │
│                           └────────────────┬────────────────┘   │
│                                            │ watches            │
│                           ┌────────────────▼────────────────┐   │
│                           │    Recommendation Controller    │   │
│                           │  - reads policy + recommendation│   │
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

The webhook is fail-open — if it errors, the pod is admitted without instrumentation.

### cairn-agent (JVM agent)

A lightweight Java agent that runs inside the JVM and exposes Prometheus metrics on port `9404`:

- `jvm_heap_used_bytes` / `jvm_heap_max_bytes`
- `jvm_non_heap_used_bytes`
- `jvm_gc_overhead_percent` (fraction of time spent in GC)
- `jvm_direct_buffer_used_bytes`

The agent is also fail-open: if it fails to start (e.g. missing module, classloading error), it catches `Throwable` and lets the JVM continue normally.

### Policy Controller

Reconciles `RightsizePolicy` resources. On each reconcile cycle (default every 2 minutes):

1. Discovers target workloads from `spec.targetRef`
2. Reads the `cairn.io/container-type` annotation from running pods to select the right recommender
3. Queries Prometheus for CPU, memory, and JVM metrics over the configured `window`
4. Runs the **recommender engine** for each container
5. Creates or updates `RightsizeRecommendation` objects with the results

### Recommender Engine

Produces per-container resource recommendations. Operates in two paths:

**Standard path** (non-Java): `cpu.p95 + headroom%`, `memory.p95 * overhead_factor`

**JVM-aware path** (Java with agent metrics):

```
cpu    = cpu.p95 * (1 + gcOverhead% * gcOverheadWeight)
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
2. In `auto` mode: checks the **change gate** — if the recommendation is within `changeThreshold%` of current resources, no action
3. If the change is significant: calls the appropriate actuator (`restart` or `in-place`) and writes `lastAppliedTime` to status

### Actuator Engine

**Restart actuator**: patches container resources and sets `kubectl.kubernetes.io/restartedAt` on the pod template, triggering a rolling restart. Works on all Kubernetes versions.

**In-place actuator**: patches container resources only, no restart. Requires Kubernetes 1.27+ with `InPlacePodVerticalScaling` feature gate.

When `manageJvmFlags: true`, both actuators also update `JAVA_TOOL_OPTIONS` on the container to set `-Xmx` and `-Xms`. Existing flags are preserved — only `-Xmx`/`-Xms` entries are replaced.

## Data flow

```
Prometheus metrics
      │
      ▼
Policy Controller ──► Recommender Engine ──► RightsizeRecommendation
                                                      │
                                          Recommendation Controller
                                                      │
                                          Actuator Engine
                                                      │
                                    ┌─────────────────┴──────────────────┐
                                    │                                    │
                              Restart Actuator                   In-Place Actuator
                          (resources + restartedAt)           (resources only)
                          (+ JAVA_TOOL_OPTIONS if Java)       (+ JAVA_TOOL_OPTIONS if Java)
```
