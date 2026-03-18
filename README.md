# 🪨 Cairn

**JVM-aware resource rightsizing for Kubernetes.**

Cairn is an open-source Kubernetes operator that continuously rightsizes pod resource requests based on real observed usage — with deep JVM awareness for Java workloads. It injects a lightweight agent into Java pods, reads heap, non-heap, GC overhead, and direct buffer metrics, and produces accurate resource and `-Xmx`/`-Xms` recommendations that generic VPA solutions cannot.

> A cairn is a carefully balanced stack of stones, each placed with precision. Cairn brings that same precision to your cluster's resource allocation.

---

## The Problem

Kubernetes resource management is broken at scale. Teams either over-provision (wasting money) or under-provision (causing OOMKills and CPU throttling). Existing solutions like VPA are blind to JVM internals — what the OS sees as memory usage is not what the JVM is actually doing. Setting `-Xmx` too high wastes memory; setting it too low causes constant GC pressure and eventually OOMKills.

## What Cairn Does

- **Rightsizes resource requests** based on actual usage percentiles over a configurable rolling window
- **Detects Java containers automatically** — by image name, command, or environment variables
- **Injects a lightweight JVM agent** that exposes heap, non-heap, GC overhead, and direct buffer metrics
- **Uses JVM internals** for accurate memory recommendations (heap + non-heap + direct buffers, not OS working set)
- **Manages JVM flags** (`-Xmx`, `-Xms`) alongside Kubernetes resources so heap ceilings never drift
- **Burst detection** with a hysteresis state machine handles load spikes without thrashing
- **Scales to any cluster size** — namespace-scoped `RightsizePolicy` or cluster-wide `ClusterRightsizePolicy`

## Quick Start

Install with bundled [VictoriaMetrics](https://victoriametrics.com) and Grafana:

```bash
helm install cairn oci://ghcr.io/rebertim/charts/cairn \
  --namespace cairn-system --create-namespace
```

Create your first policy:

```yaml
apiVersion: rightsizing.cairn.io/v1alpha1
kind: RightsizePolicy
metadata:
  name: my-app
  namespace: my-namespace
spec:
  targetRef:
    kind: Deployment
    name: "*"         # target all Deployments in this namespace
  mode: recommend     # observe first, never apply
  window: 168h        # 7-day rolling window
```

After data has been collected, enable automatic rightsizing:

```yaml
spec:
  mode: auto
  updateStrategy: restart
  changeThreshold: 10         # only apply if change > 10%
  minObservationWindow: 24h   # wait 24h for data before first apply
  java:
    enabled: true
    manageJvmFlags: true
```

## Key Features

| Feature | Description |
|---|---|
| JVM-aware sizing | Uses heap P95, non-heap P95, GC overhead, and direct buffers from the cairn-agent |
| Automatic Xmx management | Sets `-Xmx`/`-Xms` via `JAVA_TOOL_OPTIONS` so the JVM heap ceiling tracks the recommendation |
| Burst detection | Normal → Bursting state machine with configurable thresholds and hysteresis |
| GC pressure scaling | High GC overhead inflates both CPU and heap target via a single `gcOverheadWeight` knob |
| Observation window | `minObservationWindow` prevents premature applies before sufficient data is collected |
| Apply cooldown | `minApplyInterval` prevents rapid re-applies during load spikes or JVM warmup restarts |
| Three modes | `recommend` (observe only), `dry-run` (log what would change), `auto` (apply) |
| Two update strategies | `restart` (rolling restart, works everywhere) and `in-place` (no restart, requires k8s 1.27+) |
| Cluster-wide policies | `ClusterRightsizePolicy` applies across namespaces with configurable namespace selectors |

## Documentation

📖 [docs.cairn.io](https://cairn-io.github.io/cairn/)

- [Getting Started](docs/docs/getting-started.md)
- [Architecture](docs/docs/architecture.md)
- [Policy Configuration](docs/docs/policies.md)
- [Java Detection & JVM Sizing](docs/docs/java-detection.md)
- [API Reference](docs/docs/api-reference.md)

## Status

⚗️ **Alpha** — core functionality is working and tested. APIs may change before v1.0.

Known limitations:
- No e2e test suite yet
- Restart-storm mitigation (direction-aware applies) not yet implemented

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE).
