# Getting Started

## Prerequisites

- Kubernetes 1.24+
- [Helm](https://helm.sh) 3.10+
- [cert-manager](https://cert-manager.io) 1.12+ (for webhook TLS and Java agent injection)

## Install

The Cairn chart bundles [VictoriaMetrics](https://victoriametrics.com) (via `victoria-metrics-k8s-stack`) and Grafana. A single install gives you the operator, metrics collection, and a pre-provisioned dashboard.

```bash
helm install cairn oci://ghcr.io/rebertim/charts/cairn \
  --namespace cairn-system \
  --create-namespace
```

Grafana is available at the `cairn-grafana` service (port 80). The Cairn dashboard is provisioned automatically.

To disable the bundled node exporter (if you already have one):

```bash
helm install cairn oci://ghcr.io/rebertim/charts/cairn \
  --namespace cairn-system \
  --create-namespace \
  --set victoria-metrics-k8s-stack.prometheus-node-exporter.enabled=false
```

````
## Create your first policy

Start in `recommend` mode to observe what Cairn would suggest without applying anything.

```yaml
apiVersion: rightsizing.cairn.io/v1alpha1
kind: RightsizePolicy
metadata:
  name: default
  namespace: my-app
spec:
  targetRef:
    kind: Deployment
    name: "*"   # target all Deployments in this namespace
  mode: recommend
  window: 168h
````

Apply it:

```bash
kubectl apply -f policy.yaml
```

After a few minutes, check the recommendations:

```bash
kubectl get rightsizerecommendations -n my-app
kubectl describe rightsizerecommendation deployment-my-app -n my-app
```

The `status.containers` field shows `current` (what is set today) and `recommended` (what Cairn suggests).

## Enable automatic apply

Once you're comfortable with the recommendations, switch to `auto` mode. Set `minObservationWindow` to ensure Cairn has collected enough data before making its first apply:

```yaml
spec:
  mode: auto
  updateStrategy: restart # or in-place (requires k8s 1.27+)
  changeThreshold: 10 # only apply if change > 10%
  minApplyInterval: 10m # minimum time between applies
  minObservationWindow: 24h # wait for 24h of data before first apply
```

`minObservationWindow` starts counting from when the first metrics are received for a workload. Cairn will not apply in `auto` mode until this window has elapsed.

See [Policy Configuration](policies.md) for the full reference.

## Enable JVM-aware sizing for Java workloads

Add the `java` section to your policy:

```yaml
spec:
  mode: recommend
  java:
    enabled: true
    injectAgent: true # inject cairn-agent JAR via webhook
    manageJvmFlags: true # set -Xmx/-Xms on apply
    heapHeadroomPercent: 15
    gcOverheadWeight: "1.0"
```

The mutating webhook automatically injects the cairn-agent into new Java pods. Existing pods need to be restarted once to pick up the agent.

Check that the agent is running:

```bash
# Look for cairn.io/agent-injected annotation on pods
kubectl get pods -n my-app -l cairn.io/agent-injected=true
```

## Cluster-wide policies

To rightsize workloads across all namespaces with a single policy, use `ClusterRightsizePolicy`:

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
  targetRef:
    kind: Deployment
    name: "*"
  mode: recommend
  window: 168h
```

`ClusterRightsizePolicy` supports all the same fields as `RightsizePolicy` (mode, updateStrategy, java, minObservationWindow, etc.) and additionally accepts a `namespaceSelector` to include or exclude specific namespaces.

## Grafana dashboard

The Cairn dashboard is **automatically provisioned** when using the bundled VictoriaMetrics stack. Open Grafana (default credentials: `admin` / check the `cairn-grafana` secret) and look for the **Cairn** dashboard.

The dashboard shows:

- Current vs. recommended resources per workload and container
- Waste area (over-provisioned capacity)
- JVM metrics (heap, non-heap, GC overhead) for Java workloads
- Apply events and burst detections

## Uninstall

```bash
helm uninstall cairn -n cairn-system
```

!!! note
The CRDs and any `RightsizePolicy`/`RightsizeRecommendation` resources created in your namespaces are **not** deleted on uninstall. Remove them manually if needed:
`bash
    kubectl delete crd rightsizepolicies.rightsizing.cairn.io \
                       rightsizerecommendations.rightsizing.cairn.io \
                       clusterrightsizepolicies.rightsizing.cairn.io
    `
