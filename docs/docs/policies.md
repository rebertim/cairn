# Policies

A `RightsizePolicy` defines which workloads to rightsize and how Cairn should behave. For cluster-wide coverage, use `ClusterRightsizePolicy` — it supports all the same fields plus a `namespaceSelector`.

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
  minApplyInterval: 5m
  minObservationWindow: 24h
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
| `auto` | Apply recommendations when they pass all gates (observation window, change threshold, cooldown). |

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

The lookback duration for VictoriaMetrics metrics aggregation. Percentiles are computed over this window.

Default: `168h` (7 days)

Shorter windows react faster to workload changes but are more sensitive to temporary spikes.

### `spec.changeThreshold`

Minimum percentage change between current and recommended resources required to trigger an apply. Changes smaller than this are treated as insignificant.

Default: `10` (10%)

### `spec.minApplyInterval`

Minimum time that must pass between consecutive applies for a workload. Prevents rapid-fire rolling restarts when the recommendation keeps changing during a load spike (e.g. JVM warmup after a restart).

Default: `5m`

Example: to allow at most one apply per hour:

```yaml
spec:
  minApplyInterval: 1h
```

### `spec.minObservationWindow`

Minimum duration that data must have been collected for a workload before the first automatic apply is allowed. Prevents premature rightsizing based on an incomplete metrics window.

Cairn tracks when it first receives non-empty metrics for a workload (`status.dataReadySince` on the `RightsizeRecommendation`). In `auto` mode, no apply is made until `minObservationWindow` has elapsed since that timestamp.

Default: `24h`

Example: require at least 7 days of data before applying:

```yaml
spec:
  minObservationWindow: 168h
```

!!! tip
    Always use `recommend` mode first to validate the recommendations before switching to `auto`. `minObservationWindow` is a safety net for new workloads or fresh installs, not a substitute for reviewing recommendations.

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

## ClusterRightsizePolicy

`ClusterRightsizePolicy` is a cluster-scoped resource that applies rightsizing across multiple namespaces with a single policy. It supports all the same fields as `RightsizePolicy` and additionally accepts a `namespaceSelector`.

```yaml
apiVersion: rightsizing.cairn.io/v1alpha1
kind: ClusterRightsizePolicy
metadata:
  name: cluster-default
spec:
  enabled: true
  namespaceSelector:
    excludeNames:
      - kube-system
      - kube-public
      - cert-manager
  targetRef:
    kind: Deployment
    name: "*"
  mode: recommend
  window: 168h
  changeThreshold: 10
  minObservationWindow: 24h
```

### `spec.namespaceSelector`

| Field | Type | Description |
|---|---|---|
| `matchNames` | []string | Only include these namespaces |
| `excludeNames` | []string | Exclude these namespaces |
| `labelSelector` | LabelSelector | Select namespaces by label |

A namespace-scoped `RightsizePolicy` always takes precedence over a `ClusterRightsizePolicy` for the same workload. The admission webhook prevents most conflicting cluster policies at creation time; `priority` is the tiebreaker when two wildcard policies with different `labelSelector`s happen to match the same workload.

## Policy precedence

When more than one policy could apply to a workload, Cairn uses a strict precedence chain to decide which one wins.

### The full chain

```
RightsizePolicy (namespace-scoped)
    ↑ always wins
ClusterRightsizePolicy with higher priority
    ↑ wins over
ClusterRightsizePolicy with lower (or same) priority + existing ownership
```

#### 1. Namespace policy always wins

A `RightsizePolicy` in the same namespace as the workload **always takes precedence** over any `ClusterRightsizePolicy`, even if the cluster policy has a higher `priority` value. This is intentional: namespace teams own their own workloads.

A suspended `RightsizePolicy` still counts as covering the workload — it acts as a safety lock that prevents any cluster policy from touching the workload while the namespace policy is paused.

#### 2. Cluster policy priority

`ClusterRightsizePolicy` has a `priority` field (integer, higher wins). In almost all cases, only one cluster policy can legally match a workload — the admission webhook rejects:

- Two exact-name policies targeting the same workload and kind
- Two catch-all wildcards (`name: "*"`) for the same kind in overlapping namespaces
- A catch-all wildcard combined with a label-selector wildcard for the same kind in overlapping namespaces

The **one legal exception** is two label-selector wildcards (`name: "*"` with a `targetRef.labelSelector`) that target different subsets of workloads. In this case they can both be created, and `priority` resolves the conflict at runtime: the policy with the higher `priority` value claims the workload.

```yaml
# High-priority policy: claims Java workloads
apiVersion: rightsizing.cairn.io/v1alpha1
kind: ClusterRightsizePolicy
metadata:
  name: java-policy
spec:
  enabled: true
  priority: 10
  targetRef:
    kind: Deployment
    name: "*"
    labelSelector:
      matchLabels:
        runtime: java
  java:
    enabled: true

---
# Lower-priority policy: claims everything else
apiVersion: rightsizing.cairn.io/v1alpha1
kind: ClusterRightsizePolicy
metadata:
  name: default-policy
spec:
  enabled: true
  priority: 0          # default
  targetRef:
    kind: Deployment
    name: "*"
```

At runtime, `java-policy` (priority 10) claims Deployments with `runtime: java`; `default-policy` (priority 0) gets the rest.

#### 3. Equal-priority tiebreaker

When two cluster policies have the **same** `priority` and both match a workload, the one that **already owns the `RightsizeRecommendation`** keeps it. This prevents flapping — Cairn checks whether the recommendation is labeled with the other policy's name, and if so, yields.

### Why `RightsizePolicy` has no `priority` field

Within a single namespace, the admission webhook prevents all conflicts:

- Two policies with the same exact `name` target are rejected.
- A wildcard (`name: "*"`) plus any other policy for the same kind are rejected.

Because no two namespace-scoped policies can legally coexist for the same workload, there is nothing to resolve at runtime and no `priority` is needed.

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
minObservationWindow: 24h
```

Cairn will apply changes once `minObservationWindow` has elapsed and the recommendation exceeds `changeThreshold`. The `status.dataReadySince` field on the `RightsizeRecommendation` shows when the observation window started counting, and `status.lastAppliedTime` shows when the last apply happened.

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
