# 🪨 Cairn

**Balanced resource placement for Kubernetes.**

Cairn is an open-source Kubernetes operator that rightsizes pod resource requests and limits based on real metrics — with deep JVM awareness for Java workloads.

> A cairn is a carefully balanced stack of stones, each placed with precision. Cairn brings that same precision to your cluster's resource allocation.

---

## The Problem

Kubernetes resource management is broken at scale. Teams either over-provision (wasting money) or under-provision (causing OOMKills and throttling). Existing tools like VPA are blind to JVM internals, leading to wildly inaccurate recommendations for Java workloads.

## What Cairn Does

- **Rightsizes resource requests** based on actual usage percentiles, not guesswork
- **Detects Java containers** automatically and injects a lightweight metrics agent
- **Uses JVM internals** (heap, metaspace, GC pressure, thread count) for accurate memory and CPU recommendations
- **Recommends JVM flags** (`-Xmx`, `-XX:MaxMetaspaceSize`, etc.) alongside Kubernetes resources
- **Applies changes safely** via GitOps PRs, in-place resize, or rolling restarts
- **Scales to thousands of namespaces** with policy-based configuration

## Quick Start

```bash
helm install cairn oci://ghcr.io/cairn-io/charts/cairn \
  --namespace cairn-system --create-namespace \
  --set prometheus.url=http://prometheus.monitoring:9090
```

```yaml
apiVersion: rightsizing.cairn.io/v1alpha1
kind: RightsizePolicy
metadata:
  name: default
  namespace: my-app
spec:
  targetRef:
    kind: Deployment
    name: "*"
  mode: recommend
  java:
    enabled: true
```

## Documentation

📖 [docs.cairn.io](https://cairn-io.github.io/cairn/)

## Status

🚧 **Early development** — not yet ready for production use.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE).