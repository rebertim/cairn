# API Reference

Cairn defines three custom resources: `RightsizePolicy`, `ClusterRightsizePolicy`, and `RightsizeRecommendation`.

## RightsizePolicy

**Group**: `rightsizing.cairn.io/v1alpha1`
**Scope**: Namespaced

A `RightsizePolicy` defines which workloads in a namespace to rightsize and how.

### Spec

```yaml
spec:
  targetRef:                    # required
    kind: Deployment            # Deployment | StatefulSet | DaemonSet
    name: my-app                # workload name, or "*" for all
    labelSelector: {}           # optional, used when name is "*"

  mode: recommend               # recommend | dry-run | auto
  updateStrategy: restart       # restart | in-place
  suspended: false

  window: 168h                  # metrics lookback window (default: 168h)
  changeThreshold: 10           # min % change to trigger apply (default: 10)
  minApplyInterval: 5m          # min time between applies (default: 5m)
  minObservationWindow: 24h     # min data age before first apply (default: 24h)

  containers:                   # optional
    cpu:
      percentile: 95
      headroomPercent: 15
      minRequest: 10m
      maxRequest: "4"
    memory:
      percentile: 95
      headroomPercent: 10
      minRequest: 64Mi
      maxRequest: 4Gi
    limitRequestRatio: 2        # limits = requests * ratio

  java:                         # optional, JVM-aware settings
    enabled: true
    injectAgent: true
    heapHeadroomPercent: 15
    pinHeapMinMax: true
    gcOverheadWeight: "1.0"
    manageJvmFlags: false
    flagMethod: env             # env only (annotation planned)
```

### Status

```yaml
status:
  targetedWorkloads: 1
  recommendationsReady: 1
  lastReconcileTime: "2026-03-10T07:30:00Z"
  conditions: []
```

---

## ClusterRightsizePolicy

**Group**: `rightsizing.cairn.io/v1alpha1`
**Scope**: Cluster

A `ClusterRightsizePolicy` applies rightsizing across multiple namespaces. It supports all the same fields as `RightsizePolicy` plus `namespaceSelector` and `priority`. A namespace-scoped `RightsizePolicy` always takes precedence over a `ClusterRightsizePolicy` for the same workload.

The admission webhook prevents most conflicting combinations — two exact-name policies targeting the same workload, or two catch-all wildcards for the same kind, are rejected at creation time. The one case where two cluster policies can legally overlap is when both use a `labelSelector` on `targetRef` (potentially targeting different subsets of workloads). In that case, `priority` determines which policy claims the workload at runtime.

### Spec

```yaml
spec:
  enabled: true                 # must be explicitly set to true

  namespaceSelector:            # optional
    matchNames: []              # only include these namespaces
    excludeNames:               # exclude these namespaces
      - kube-system
    labelSelector: {}           # select namespaces by label

  # All RightsizePolicy fields are supported inline:
  targetRef:
    kind: Deployment
    name: "*"
  mode: recommend
  window: 168h
  changeThreshold: 10
  minObservationWindow: 24h
  java:
    enabled: true
    injectAgent: true
```

### Status

```yaml
status:
  targetedNamespaces: 5
  targetedWorkloads: 23
  recommendationsReady: 23
  lastReconcileTime: "2026-03-10T07:30:00Z"
  conditions: []
```

---

## RightsizeRecommendation

**Group**: `rightsizing.cairn.io/v1alpha1`
**Scope**: Namespaced

Created and owned by the policy controller. One recommendation is created per workload matched by a `RightsizePolicy` or `ClusterRightsizePolicy`. Read-only from a user perspective.

### Spec

```yaml
spec:
  targetRef:
    kind: Deployment
    name: my-app
  policyRef:
    kind: RightsizePolicy       # or ClusterRightsizePolicy
    name: my-policy
    namespace: default
```

### Status

```yaml
status:
  lastRecommendationTime: "2026-03-10T07:30:00Z"
  lastAppliedTime: "2026-03-10T07:30:57Z"    # nil if never applied
  dataReadySince: "2026-03-10T07:00:00Z"     # when first non-empty data was received

  containers:
    - containerName: app

      # Resources currently set on the workload
      current:
        requests:
          cpu: 100m
          memory: 256Mi
        limits:
          cpu: 500m
          memory: 512Mi

      # What Cairn recommends
      recommended:
        requests:
          cpu: 36m
          memory: "77070336"   # bytes

      # Burst state machine state
      burst:
        phase: Normal          # Normal | Bursting
        burstPeakCPU: 9m       # set during Bursting
        burstPeakMemory: "129281253"
        burstStartTime: "2026-03-10T07:10:00Z"

      # JVM-specific data (only for Java containers)
      jvm:
        detected: true
        agentInjected: true
        recommendedFlags:
          xmx: 59m
          xms: 59m
```

### `status.dataReadySince`

Set by the policy controller on the first reconcile that produces non-empty container data. Never reset unless all container data disappears. The actuator engine uses this field together with `spec.minObservationWindow` to gate the first automatic apply.

### `containers[].burst.phase`

| Value | Meaning |
|---|---|
| `Normal` | Usage is within normal range. Recommendation is the steady-state baseline. |
| `Bursting` | Live usage has spiked above `baseline * 1.5`. Recommendation is `max(live, baseline) * 1.3`. Returns to `Normal` when spike ends. |

---

## kubectl output

```bash
# List all recommendations across all namespaces
kubectl get rightsizerecommendations -A

# Typical output:
NAMESPACE   NAME                     TARGET       WORKLOAD   POLICY     CPU SAVINGS   MEM SAVINGS (MiB)   AGE
default     deployment-my-java-app   Deployment   my-app     my-policy                                    3d
```

```bash
# List policies
kubectl get rightsizepolicies -A

# Typical output:
NAMESPACE   NAME      MODE    TARGET       WORKLOADS   READY   SUSPENDED   AGE
default     my-app    auto    Deployment   1           1       false       3d
```

```bash
# List cluster policies
kubectl get clusterrightsizepolicies

# Typical output:
NAME              ENABLED   MODE        NAMESPACES   WORKLOADS   READY   SUSPENDED   AGE
cluster-default   true      recommend   5            23          23      false       7d
```
