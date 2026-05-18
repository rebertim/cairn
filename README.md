# 🪨 Cairn

**JVM-aware resource rightsizing for Kubernetes.**

Cairn is an open-source Kubernetes operator that continuously rightsizes pod resource requests based on real observed usage — with deep JVM awareness for Java workloads. It injects a lightweight agent into Java pods, reads heap, non-heap, GC overhead, and direct buffer metrics, and produces accurate resource and `-Xmx`/`-Xms` recommendations that generic VPA solutions cannot.

> A cairn is a carefully balanced stack of stones, each placed with precision. Cairn brings that same precision to your cluster's resource allocation.

---

## The Problem

Kubernetes resource management is broken at scale. Teams either over-provision (wasting money) or under-provision (causing OOMKills and CPU throttling). Existing solutions like VPA are blind to JVM internals — what the OS sees as memory usage is not what the JVM is actually doing. Setting `-Xmx` too high wastes memory; setting it too low causes constant GC pressure and eventually OOMKills.

## What Cairn Does

- **Rightsizes resource requests** based on actual usage percentiles over a configurable rolling window
- **Detects Java containers automatically** — by image name, command, or environment variables — no labels or annotations required
- **Injects a lightweight JVM agent** that exposes heap, non-heap, GC overhead, and direct buffer metrics
- **Uses JVM internals** for accurate memory recommendations (heap + non-heap + direct buffers, not OS working set)
- **Manages JVM flags** (`-Xmx`, `-Xms`) alongside Kubernetes resources so heap ceilings never drift
- **Burst detection** with a hysteresis state machine handles load spikes without thrashing
- **Scales to any cluster size** — namespace-scoped `RightsizePolicy` or cluster-wide `ClusterRightsizePolicy`
- **containerType targeting** — policies can target Java workloads (`containerType: java`) or non-Java workloads (`containerType: standard`) separately, enabling different update strategies per type
- **Immediate reconciliation** — the policy controller signals the recommendation controller instantly when new data arrives via a `cairn.io/pending-apply` annotation, eliminating timer lag

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
    name: "*" # target all Deployments in this namespace
  mode: recommend # observe first, never apply
  window: 168h # 7-day rolling window
```

After data has been collected, enable automatic rightsizing. For clusters with mixed Java and non-Java workloads, use two complementary `ClusterRightsizePolicy` resources:

```yaml
# Java workloads — restart strategy (required for JVM flag changes)
apiVersion: rightsizing.cairn.io/v1alpha1
kind: ClusterRightsizePolicy
metadata:
  name: java-deployments
spec:
  mode: auto
  updateStrategy: restart
  targetRef:
    kind: Deployment
    name: "*"
    containerType: java   # only pods detected as Java
  changeThreshold: 10
  minObservationWindow: 24h
  java:
    enabled: true
    injectAgent: true
    manageJvmFlags: true
---
# Standard workloads — in-place update (no restart needed)
apiVersion: rightsizing.cairn.io/v1alpha1
kind: ClusterRightsizePolicy
metadata:
  name: standard-deployments
spec:
  mode: auto
  updateStrategy: inPlace
  targetRef:
    kind: Deployment
    name: "*"
    containerType: standard   # only non-Java pods
  changeThreshold: 10
  minObservationWindow: 24h
```

The webhook automatically labels every managed pod with `cairn.io/container-type: java` or `cairn.io/container-type: standard` at creation time — detection is based on image name, environment variables, and command heuristics, requiring no manual annotation.

## Key Features

| Feature                  | Description                                                                                   |
| ------------------------ | --------------------------------------------------------------------------------------------- |
| JVM-aware sizing         | Uses heap P95, non-heap P95, GC overhead, and direct buffers from the cairn-agent             |
| Automatic Xmx management | Sets `-Xmx`/`-Xms` via `JAVA_TOOL_OPTIONS` so the JVM heap ceiling tracks the recommendation  |
| Burst detection          | Normal → Bursting state machine with configurable thresholds and hysteresis                   |
| GC pressure scaling      | High GC overhead inflates both CPU and heap target via a single `gcOverheadWeight` knob       |
| Observation window       | `minObservationWindow` prevents premature applies before sufficient data is collected         |
| Apply cooldown           | `minApplyInterval` prevents rapid re-applies during load spikes or JVM warmup restarts        |
| Three modes              | `recommend` (observe only), `dry-run` (log what would change), `auto` (apply)                 |
| Two update strategies    | `restart` (rolling restart, works everywhere) and `inPlace` (no restart, requires k8s 1.27+) |
| containerType targeting  | Target Java or non-Java workloads separately within the same cluster (`containerType: java\|standard`) |
| Pod labels               | Webhook sets `cairn.io/container-type` on every managed pod for observability and filtering   |
| Cluster-wide policies    | `ClusterRightsizePolicy` applies across namespaces with configurable namespace selectors      |
| Instant reconciliation   | `cairn.io/pending-apply` annotation wakes the recommendation controller immediately on new data |

## Documentation

📖 [docs](https://rebertim.github.io/cairn/)

- [Getting Started](https://rebertim.github.io/cairn/getting-started/)
- [Architecture](https://rebertim.github.io/cairn/architecture/)
- [Policy Configuration](https://rebertim.github.io/cairn/policies/)
- [Java Detection & JVM Sizing](https://rebertim.github.io/cairn/java-detection/)
- [API Reference](https://rebertim.github.io/cairn/api-reference/)

## Status

⚗️ **Alpha** — core functionality is working and tested. APIs may change before v1.0.

Known limitations:

- No e2e test suite yet

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE).
