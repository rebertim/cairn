# Policies

A `RightsizePolicy` defines which workloads to rightsize and how Cairn should behave.

## Example

```yaml
apiVersion: rightsizing.cairn.io/v1alpha1
kind: RightsizePolicy
metadata:
  name: my-java-app
  namespace: default
spec:
  targetRef:
    kind: Deployment
    name: my-java-app
  mode: auto
  updateStrategy: restart
  window: 168h
  changeThreshold: 10
  containers:
    cpu:
      percentile: 95
      headroomPercent: 15
    memory:
      percentile: 95
      headroomPercent: 10
  java:
    enabled: true
    injectAgent: true
    manageJvmFlags: true
    heapHeadroomPercent: 15
    pinHeapMinMax: true
    gcOverheadWeight: "1.0"
```

## Field reference

### `spec.targetRef`

Identifies the workload(s) this policy applies to.

| Field | Type | Description |
|---|---|---|
| `kind` | string | `Deployment`, `StatefulSet`, or `DaemonSet` |
| `name` | string | Name of the workload, or `*` to match all workloads of the given kind in the namespace |
| `labelSelector` | LabelSelector | Further filters when `name` is `*` |

### `spec.mode`

Controls what Cairn does with recommendations.

| Value | Behaviour |
|---|---|
| `recommend` | Compute and store recommendations. Never apply. Safe starting point. |
| `dry-run` | Log what would be applied on each reconcile. Never apply. |
| `auto` | Apply recommendations when they pass the change gate. |

Default: `recommend`

### `spec.updateStrategy`

How changes are applied when `mode: auto`.

| Value | Behaviour | Requirements |
|---|---|---|
| `restart` | Patches resources + sets `kubectl.kubernetes.io/restartedAt` → rolling restart | All Kubernetes versions |
| `in-place` | Patches resources only, no restart | Kubernetes 1.27+ with `InPlacePodVerticalScaling` feature gate |

Default: `restart`

!!! note "Java and in-place"
    For Java workloads, memory changes require a JVM restart to take effect (heap size is fixed at startup). In-place is useful for CPU-only changes or when `manageJvmFlags` is disabled.

### `spec.window`

The lookback duration for Prometheus metrics aggregation. Metrics P95/P99 are computed over this window.

Default: `168h` (7 days)

Shorter windows react faster to workload changes but are more sensitive to temporary spikes.

### `spec.changeThreshold`

Minimum percentage change between current and recommended resources required to trigger an apply. Changes smaller than this are treated as insignificant.

Default: `10` (10%)

### `spec.suspended`

Set to `true` to pause all activity for this policy. Recommendations stop being updated and no applies are made.

Default: `false`

### `spec.containers`

Optional resource policies for CPU and memory.

```yaml
containers:
  cpu:
    percentile: 95         # which percentile to use (default: 95)
    headroomPercent: 15    # headroom added on top of the percentile (default: 15)
    minRequest: 10m        # floor for the recommendation
    maxRequest: "4"        # ceiling for the recommendation
  memory:
    percentile: 95
    headroomPercent: 10
    minRequest: 64Mi
    maxRequest: 4Gi
  limitRequestRatio: 2     # if set, limits = requests * ratio
```

### `spec.java`

JVM-specific configuration. Only applied to containers identified as Java.

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `true` | Enable JVM-aware sizing for detected Java containers |
| `injectAgent` | bool | `true` | Inject the cairn-agent JAR into Java pods via the mutating webhook |
| `heapHeadroomPercent` | int | `15` | Headroom added on top of observed `heap.p95` before computing Xmx |
| `pinHeapMinMax` | bool | `true` | Set `-Xms` equal to `-Xmx` for predictable startup memory behaviour |
| `gcOverheadWeight` | quantity | `"1.0"` | Controls how much GC pressure inflates CPU and heap recommendations. Set to `"0"` to disable. |
| `manageJvmFlags` | bool | `false` | When enabled, Cairn writes `-Xmx`/`-Xms` to `JAVA_TOOL_OPTIONS` on every apply |
| `flagMethod` | string | `env` | How JVM flags are delivered. Currently only `env` (via `JAVA_TOOL_OPTIONS`) is supported |

## Modes in practice

### Starting out

```yaml
mode: recommend
```

Run in `recommend` mode for at least one `window` duration (default 7 days) to collect baseline metrics. Check `kubectl get rightsizerecommendations` to see what Cairn would recommend.

### Validating

```yaml
mode: dry-run
```

Switch to `dry-run` and watch the controller logs:

```bash
kubectl logs -n cairn-system deploy/cairn-controller-manager | grep dry-run
```

You will see lines like:

```
[dry-run] would apply resources  workload=my-app  container=app  cpu=50m  memory=256Mi
```

### Applying

```yaml
mode: auto
```

Cairn will apply changes whenever the recommendation exceeds `changeThreshold`. The `lastAppliedTime` field on the `RightsizeRecommendation` status shows when the last apply happened.

## Tuning for volatile workloads

For workloads with frequent traffic spikes, increase headroom and the change threshold to reduce churn:

```yaml
spec:
  changeThreshold: 20
  java:
    heapHeadroomPercent: 30
    gcOverheadWeight: "1.5"
```

## Suspending a policy

```bash
kubectl patch rightsizepolicy my-app -n default \
  --type=merge -p '{"spec":{"suspended":true}}'
```
