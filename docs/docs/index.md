# Cairn

Cairn is a Kubernetes operator that automatically rightsizes workload resource requests based on actual observed usage. It is JVM-aware — for Java workloads it reads heap, non-heap, and GC metrics directly from a lightweight in-process agent to produce accurate recommendations and manage `-Xmx`/`-Xms` flags.

## Why Cairn?

Most workloads in Kubernetes are over-provisioned. CPU and memory requests are set conservatively at deploy time and rarely revisited. This wastes cluster capacity and drives up costs.

Generic VPA solutions treat Java workloads like any other process. The JVM manages its own heap — what the OS sees as memory usage is not the whole picture. Without understanding heap vs. non-heap, GC overhead, and the effect of `-Xmx`, recommendations are either too tight (causing OOMKills) or too loose (wasting memory).

Cairn solves both problems:

- **Continuous recommendations** based on a configurable rolling window of observed usage
- **JVM-aware memory sizing** using real heap metrics, not container working-set bytes
- **Automatic `-Xmx` management** so heap ceilings match recommendations and don't drift
- **Burst detection** with a hysteresis state machine to handle load spikes without thrashing
- **Observation window** ensures sufficient data is collected before the first auto apply
- **Configurable actuation** — recommend only, dry-run, or automatic apply with a change threshold and cooldown

## Key features

| Feature | Description |
|---|---|
| JVM-aware sizing | Uses heap P95, non-heap P95, GC overhead, and direct buffers from the cairn-agent |
| Automatic Xmx management | Sets `-Xmx`/`-Xms` via `JAVA_TOOL_OPTIONS` so the JVM heap ceiling tracks the recommendation |
| Burst detection | Normal → Bursting state machine with configurable thresholds and hysteresis |
| GC pressure scaling | High GC overhead inflates both CPU and heap target via a single `gcOverheadWeight` knob |
| Observation window | `minObservationWindow` (default 24h) prevents premature applies before sufficient data is collected |
| Apply cooldown | `minApplyInterval` prevents rapid re-applies during load spikes or JVM warmup restarts |
| Three modes | `recommend` (observe only), `dry-run` (log what would change), `auto` (apply) |
| Two update strategies | `restart` (rolling restart, works everywhere) and `in-place` (no restart, requires k8s 1.27+) |
| Cluster-wide policies | `ClusterRightsizePolicy` applies across namespaces with configurable namespace selectors |

## How it works

1. A **mutating webhook** detects Java pods and injects the cairn-agent JAR via `JAVA_TOOL_OPTIONS`.
2. The agent exposes JVM metrics (heap, non-heap, GC overhead, direct buffers) on a Prometheus-compatible endpoint.
3. The **policy controller** queries VictoriaMetrics and runs the recommender engine to produce a `RightsizeRecommendation` per workload.
4. The **recommendation controller** runs the actuator engine: waits for `minObservationWindow`, checks the change threshold, respects `minApplyInterval`, then applies the recommendation (resource requests + JVM flags) to the workload.

## Quick links

- [Getting Started](getting-started.md)
- [Architecture](architecture.md)
- [Policy Configuration](policies.md)
- [Java Detection & JVM Sizing](java-detection.md)
- [API Reference](api-reference.md)
