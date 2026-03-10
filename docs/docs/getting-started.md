# Getting Started

## Prerequisites

- Kubernetes 1.24+
- [Helm](https://helm.sh) 3.10+
- [cert-manager](https://cert-manager.io) 1.12+ (for webhook TLS)

## Install

The Cairn chart bundles [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack) (Prometheus + Grafana) by default. A single install gives you the operator, metrics collection, and a pre-provisioned dashboard.

```bash
helm install cairn oci://ghcr.io/rebertim/charts/cairn \
  --namespace cairn-system \
  --create-namespace
```

Grafana is available at the `cairn-grafana` service (port 80). The Cairn dashboard is provisioned automatically.

### Using an existing Prometheus instance

If you already have Prometheus running, disable the bundled stack and point Cairn at your instance:

```bash
helm install cairn oci://ghcr.io/rebertim/charts/cairn \
  --namespace cairn-system \
  --create-namespace \
  --set kube-prometheus-stack.enabled=false \
  --set controllerManager.manager.env.prometheusUrl=http://<YOUR_PROMETHEUS_HOST>:9090
```

Or from source:

```bash
git clone https://github.com/rebertim/cairn
helm install cairn ./cairn/charts/cairn \
  --namespace cairn-system \
  --create-namespace \
  --set kube-prometheus-stack.enabled=false \
  --set controllerManager.manager.env.prometheusUrl=<YOUR_PROMETHEUS_URL>
```

### Without cert-manager (webhook disabled)

If you don't have cert-manager, you can disable the webhook. Java agent injection won't work, but Cairn will still produce CPU and memory recommendations using container-level OS metrics.

```bash
helm install cairn ./charts/cairn \
  --namespace cairn-system \
  --create-namespace \
  --set controllerManager.manager.env.prometheusUrl=<YOUR_PROMETHEUS_URL> \
  --set controllerManager.manager.args[0]="--metrics-bind-address=:8080" \
  --set controllerManager.manager.args[1]="--leader-elect" \
  --set controllerManager.manager.args[2]="--health-probe-bind-address=:8081"
```

## Create your first policy

Start in `recommend` mode to observe what Cairn would suggest before applying anything.

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
```

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

Once you're comfortable with the recommendations, switch to `auto` mode:

```yaml
spec:
  mode: auto
  updateStrategy: restart   # or in-place (requires k8s 1.27+)
  changeThreshold: 10       # only apply if change > 10%
  minApplyInterval: 10m     # minimum time between applies
```

See [Policy Configuration](policies.md) for the full reference.

## Enable JVM-aware sizing for Java workloads

Add the `java` section to your policy:

```yaml
spec:
  mode: recommend
  java:
    enabled: true
    injectAgent: true        # inject cairn-agent JAR via webhook
    manageJvmFlags: true     # set -Xmx/-Xms on apply
    heapHeadroomPercent: 15
    gcOverheadWeight: "1.0"
```

The mutating webhook will automatically inject the cairn-agent into new Java pods. Existing pods need to be restarted once to pick up the agent.

Check that the agent is running:

```bash
# Look for cairn.io/agent-injected label on pods
kubectl get pods -n my-app -l cairn.io/agent-injected=true
```

## Grafana dashboard

When using the bundled `kube-prometheus-stack`, the Cairn dashboard is **automatically provisioned** — no extra steps needed. Open Grafana and look for the **Cairn** dashboard.

### Using an external Grafana instance

If you disabled the bundled stack, import the dashboard manually. The JSON is available at `docs/grafana/cairn-dashboard.json` in the repo, or provision it as a ConfigMap labeled for your Grafana sidecar:

```bash
kubectl create configmap cairn-dashboard \
  --from-file=cairn-dashboard.json=docs/grafana/cairn-dashboard.json \
  -n monitoring \
  --dry-run=client -o yaml | \
  kubectl annotate --local -f - grafana_dashboard=1 --dry-run=client -o yaml | \
  kubectl apply -f -
```

The dashboard shows provisioned vs recommended resources, the waste area, JVM metrics, apply events, and burst detections — filterable by namespace, workload, and container.

## Uninstall

```bash
helm uninstall cairn -n cairn-system
```

!!! note
    The CRDs and any `RightsizePolicy`/`RightsizeRecommendation` resources created in your namespaces are **not** deleted on uninstall. Remove them manually if needed:
    ```bash
    kubectl delete crd rightsizepolicies.rightsizing.cairn.io \
                       rightsizerecommendations.rightsizing.cairn.io \
                       clusterrightsizepolicies.rightsizing.cairn.io
    ```
