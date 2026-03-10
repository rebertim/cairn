# API Reference

Cairn defines two custom resources: `RightsizePolicy` and `RightsizeRecommendation`.

## RightsizePolicy

**Group**: `rightsizing.cairn.io/v1alpha1`
**Scope**: Namespaced

A `RightsizePolicy` defines which workloads to rightsize and how.

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

  window: 168h                  # metrics lookback window
  stabilityWindow: 5m           # recommendation must be stable before apply
  changeThreshold: 10           # min % change to trigger apply

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
    flagMethod: env             # env | annotation
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

## RightsizeRecommendation

**Group**: `rightsizing.cairn.io/v1alpha1`
**Scope**: Namespaced

Created and owned by the policy controller. One recommendation is created per workload matched by a `RightsizePolicy`. Read-only from a user perspective.

### Spec

```yaml
spec:
  targetRef:
    kind: Deployment
    name: my-app
  policyRef:
    kind: RightsizePolicy
    name: my-policy
    namespace: default
```

### Status

```yaml
status:
  lastRecommendationTime: "2026-03-10T07:30:00Z"
  lastAppliedTime: "2026-03-10T07:30:57Z"    # nil if never applied
  stableSince: "2026-03-10T07:25:57Z"        # nil if not yet stable

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
          cpu: 6m
          memory: "99089416"   # bytes

      # Burst state machine state
      burst:
        phase: Normal          # Normal | Bursting | Recovering
        burstPeakCPU: 9m       # set during Bursting/Recovering
        burstPeakMemory: "129281253"
        burstStartTime: "2026-03-10T07:10:00Z"
        recoveryStartTime: "2026-03-10T07:12:00Z"

      # JVM-specific data (only for Java containers)
      jvm:
        detected: true
        agentInjected: true
        recommendedFlags:
          xmx: 59m
          xms: 59m
```

### `containers[].burst.phase`

| Value | Meaning |
|---|---|
| `Normal` | Usage is within normal range. Recommendation is the steady-state baseline. |
| `Bursting` | Live usage has spiked above `baseline * burstThreshold`. Recommendation is inflated. |
| `Recovering` | Spike has ended. Recommendation linearly steps down from peak to baseline over the cooldown window. |

### `status.stableSince`

Set when the recommendation first becomes significantly different from current resources. Reset to nil on apply or when the change drops below `changeThreshold`. The actuator applies when `now - stableSince >= stabilityWindow`.

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
