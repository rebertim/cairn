# cairn — Kubernetes Resource Rightsizer

> An open-source, Java-aware Kubernetes operator that automatically rightsizes pod resource requests and limits based on real metrics — including JVM internals.

---

## Problem Statement

Kubernetes resource management is fundamentally broken at scale. Teams either over-provision (wasting capacity and money) or under-provision (causing OOMKills and throttling). The built-in VPA has critical limitations:

- **No JVM awareness**: Java apps report OS-level memory, not heap/metaspace — leading to wildly inaccurate recommendations
- **Disruptive restacking**: VPA evicts pods to apply new requests, causing downtime
- **No multi-signal intelligence**: Ignores JVM GC pressure, thread pool saturation, class loading overhead
- **Poor multi-tenant UX**: Difficult to operate across hundreds of namespaces with different policies

`cairn` solves these by combining OS-level metrics with deep JVM introspection, operating as a non-disruptive Kubernetes operator with a GitOps-first approach.

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│                        cairn                                │
│                                                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐  ┌───────────┐ │
│  │  Collector   │  │  Recommender │  │  Actuator   │  │    API    │ │
│  │  (metrics)   │──│  (engine)    │──│  (applier)  │  │  Server   │ │
│  └──────┬──────┘  └──────────────┘  └──────┬──────┘  └─────┬─────┘ │
│         │                                   │                │       │
│  ┌──────┴──────┐                    ┌───────┴──────┐  ┌─────┴─────┐ │
│  │ JVM Agent   │                    │  GitOps /    │  │ Dashboard │ │
│  │ Injector    │                    │  In-Place    │  │   (UI)    │ │
│  │ (mutating   │                    │  Resize      │  │           │ │
│  │  webhook)   │                    │              │  │           │ │
│  └─────────────┘                    └──────────────┘  └───────────┘ │
└──────────────────────────────────────────────────────────────────────┘
         │                                    │
    ┌────┴────┐                        ┌──────┴──────┐
    │Prometheus│                        │ Kubernetes  │
    │  / VPA   │                        │    API      │
    └─────────┘                        └─────────────┘
```

---

## Core Components

### 1. Metrics Collector

Aggregates resource usage signals from multiple sources into a unified time-series per workload.

**Data Sources:**
- **Prometheus/Thanos/Mimir** — CPU, memory, network I/O via `container_*` metrics
- **Kubernetes Metrics API** — real-time resource consumption from metrics-server
- **VPA Recommender** (optional) — import existing VPA recommendations as one signal among many
- **Custom Metrics** — JVM metrics exposed by the injected agent (see below)

**Key Design Decisions:**
- Pull-based collection on configurable intervals (default: 30s scrape, 24h recommendation window)
- Stores aggregated histograms (P50, P95, P99, max) per workload — not raw time-series
- Pluggable backend: in-memory for small clusters, Redis/SQLite for persistence at scale

### 2. JVM Agent Injector (Mutating Admission Webhook)

Automatically detects Java containers and injects a lightweight JVM metrics agent.

**Detection Heuristic:**
1. Image name contains `java`, `jdk`, `jre`, `openjdk`, `eclipse-temurin`, `amazoncorretto`, `graalvm`
2. Container command starts with `java` or known entrypoint wrappers
3. `JAVA_TOOL_OPTIONS` or `JAVA_OPTS` env vars present
4. Annotation `cairn.io/runtime: java` explicitly set

**Injection Mechanism:**
- Mutating webhook adds an init container that copies the agent JAR to a shared `emptyDir` volume
- Appends `-javaagent:/cairn/agent.jar` to `JAVA_TOOL_OPTIONS` env var
- The agent is a lightweight Java agent (compiled from the `agent/` submodule) exposing metrics via Prometheus format on a sidecar-free HTTP endpoint (configurable port, default `:9404`)

**Exposed JVM Metrics:**
```
# Heap
jvm_memory_heap_used_bytes
jvm_memory_heap_committed_bytes
jvm_memory_heap_max_bytes

# Non-heap (Metaspace, Code Cache, Compressed Class Space)
jvm_memory_nonheap_used_bytes
jvm_memory_metaspace_used_bytes
jvm_memory_codecache_used_bytes

# GC
jvm_gc_pause_seconds_total
jvm_gc_pause_seconds_count
jvm_gc_overhead_percent      # time spent in GC as % of wall time

# Threads
jvm_threads_live_count
jvm_threads_peak_count
jvm_threads_deadlocked_count

# Class Loading
jvm_classes_loaded_total
jvm_classes_unloaded_total

# Direct / Mapped Buffers (off-heap)
jvm_buffer_memory_used_bytes{type="direct"}
jvm_buffer_memory_used_bytes{type="mapped"}
```

**Opt-out:** Workloads can disable injection via annotation:
```yaml
cairn.io/inject: "false"
```

### 3. Recommendation Engine

Produces resource recommendations by combining OS-level and JVM-level signals.

**Algorithm (per workload):**

```
For standard containers:
  cpu_request    = P95(cpu_usage, window) * (1 + cpu_headroom%)
  memory_request = P99(memory_usage, window) * (1 + memory_headroom%)

For Java containers (enhanced):
  # Memory: sum of JVM regions + headroom for native/OS overhead
  heap_rec       = max(P99(heap_used), committed_heap) * (1 + heap_headroom%)
  nonheap_rec    = P99(metaspace + codecache + compressed_class) * 1.2
  offheap_rec    = P99(direct_buffers + mapped_buffers) * 1.1
  native_rec     = P95(rss - heap_committed - nonheap_committed)  # native memory estimate
  thread_stack   = peak_threads * thread_stack_size  # default -Xss1m

  memory_request = heap_rec + nonheap_rec + offheap_rec + native_rec + thread_stack

  # CPU: factor in GC overhead — high GC pressure = needs more CPU
  gc_factor      = 1 + (gc_overhead * gc_weight)     # e.g., 15% GC → 1.15x multiplier
  cpu_request    = P95(cpu_usage, window) * gc_factor * (1 + cpu_headroom%)

  # JVM Flags Recommendation:
  -Xms/-Xmx     = heap_rec (pin min=max for predictable behavior)
  -XX:MaxMetaspaceSize = ceil(P99(metaspace_used) * 1.3)
  -XX:ReservedCodeCacheSize = ceil(P99(codecache_used) * 1.5)
  -XX:MaxDirectMemorySize = ceil(P99(direct_buffer_used) * 1.2)
```

**Configurable via CRD:**
```yaml
apiVersion: cairn.io/v1alpha1
kind: RightsizePolicy
metadata:
  name: default
  namespace: my-app
spec:
  targetRef:
    kind: Deployment                    # or DaemonSet, StatefulSet, Rollout
    name: "*"                           # wildcard = all in namespace
  mode: recommend | dry-run | auto      # escalating trust levels
  updateStrategy: in-place | restart    # prefer in-place if InPlacePodVerticalScaling enabled
  containers:
    cpu:
      percentile: 95
      headroomPercent: 15
      minRequest: 50m
      maxRequest: "4"
    memory:
      percentile: 99
      headroomPercent: 20
      minRequest: 128Mi
      maxRequest: 8Gi
  java:
    enabled: true
    heapHeadroomPercent: 15
    pinHeapMinMax: true                 # set -Xms = -Xmx
    gcOverheadWeight: 1.0               # how much GC pressure inflates CPU rec
    manageJvmFlags: true                # inject optimized -Xmx, -XX flags
    jvmFlagMethod: env | annotation     # how to deliver JVM flag changes
  window: 168h                          # recommendation lookback window (7 days)
  cooldown: 24h                         # min time between auto-applies
  schedule: "0 3 * * 1"                 # cron for when auto-apply runs (e.g. Monday 3 AM)
```

### 4. Actuator (Change Applier)

Applies recommendations through multiple strategies:

| Strategy | How | When |
|----------|-----|------|
| **Recommend** | Writes to `RightsizeRecommendation` status, emits events | Default — observe first |
| **Dry-Run** | Same as recommend + creates PR/MR to GitOps repo | Build confidence |
| **GitOps** | Pushes changes to Git (Kustomize patches or Helm value overrides) | ArgoCD/Flux workflows |
| **In-Place** | Uses `InPlacePodVerticalScaling` feature gate (K8s 1.27+) | Zero-downtime apply |
| **Restart** | Patches Deployment spec → triggers rolling update | Fallback |

**GitOps Integration:**
- Generates Kustomize patches or Helm `values.yaml` overrides
- Opens PR/MR via GitHub/GitLab API with diff summary
- PR description includes before/after comparison, projected savings, risk assessment

### 5. API Server & Dashboard

- **REST API** (Go, chi router) for programmatic access to recommendations, history, savings projections
- **Web Dashboard** (React + TypeScript) for visual cluster-wide rightsizing overview
- **Grafana Dashboards** shipped as ConfigMaps for native Prometheus/Grafana integration

---

## Tech Stack

### Core (Go Ecosystem)

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| **Language** | Go 1.23+ | Kubernetes-native, excellent concurrency, operator-sdk ecosystem |
| **Operator Framework** | [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) | Industry standard for building K8s operators, used by Crossplane, Cert-Manager |
| **CRD Code Generation** | [controller-gen](https://github.com/kubernetes-sigs/controller-tools) | Auto-generates CRD manifests, DeepCopy, RBAC from Go types |
| **Kubernetes Client** | client-go + dynamic client | Standard K8s API interaction |
| **Admission Webhook** | controller-runtime webhook server | For the mutating webhook (JVM agent injection) |
| **CLI** | [cobra](https://github.com/spf13/cobra) + [viper](https://github.com/spf13/viper) | CLI tool for ad-hoc recommendations, policy validation |
| **HTTP Router** | [chi](https://github.com/go-chi/chi) | Lightweight, idiomatic Go router for the API server |
| **Logging** | [slog](https://pkg.go.dev/log/slog) (stdlib) | Structured logging, zero dependencies, Go 1.21+ |
| **Metrics** | [prometheus/client_golang](https://github.com/prometheus/client_golang) | Operator self-metrics (recommendation drift, apply latency, etc.) |
| **Testing** | [envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest) + [testify](https://github.com/stretchr/testify) | Integration tests with real etcd+apiserver, assertions |
| **E2E Testing** | [chainsaw](https://kyverno.github.io/chainsaw/) | Declarative K8s E2E tests (YAML-driven) |
| **Linting** | [golangci-lint](https://golangci-lint.run/) | Comprehensive Go linting |
| **Build** | [ko](https://ko.build/) | Build Go container images without Dockerfile |
| **Task Runner** | [task](https://taskfile.dev/) (Taskfile.yml) | Replaces Makefile with a more readable YAML-based runner |

### JVM Agent (Java)

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| **Language** | Java 11+ (target) | Minimum JVM version for broad compatibility |
| **Agent Type** | `java.lang.instrument` premain agent | Non-invasive, works with any JVM |
| **Metrics Exposure** | Embedded HTTP server (com.sun.net.httpserver) | Zero dependencies — no Micrometer/Spring needed |
| **Metrics Format** | OpenMetrics / Prometheus exposition format | Universal scraping compatibility |
| **Build** | Gradle (single JAR, no dependencies) | Minimal agent footprint (<500KB) |

### Infrastructure & CI/CD

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| **Container Registry** | GitHub Container Registry (ghcr.io) | Free for open source |
| **CI** | GitHub Actions | Standard for OSS, free for public repos |
| **Helm Charts** | Helm 3 | Standard K8s packaging |
| **Release** | [goreleaser](https://goreleaser.com/) | Automates Go binary + Helm + container image releases |
| **Documentation** | [MkDocs Material](https://squidfunnel.github.io/mkdocs-material/) | Beautiful docs site, Markdown-native |
| **API Docs** | OpenAPI 3.0 spec + [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) | Generate Go server stubs and TypeScript client from spec |

### Dashboard (TypeScript)

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| **Framework** | React 19 + TypeScript | Industry standard |
| **Bundler** | Vite | Fast builds |
| **UI Components** | shadcn/ui + Tailwind CSS | Clean, accessible, non-opinionated |
| **Charts** | Recharts | React-native charting for resource graphs |
| **API Client** | Generated from OpenAPI spec via oapi-codegen | Type-safe, always in sync |

---

## Custom Resource Definitions

### RightsizePolicy (cluster-scoped or namespace-scoped)

Defines *how* rightsizing should work for a set of workloads.

```yaml
apiVersion: cairn.io/v1alpha1
kind: RightsizePolicy
# ... (see Recommendation Engine section above)
```

### RightsizeRecommendation (namespace-scoped, auto-generated)

The operator creates one per targeted workload, containing current vs. recommended resources.

```yaml
apiVersion: cairn.io/v1alpha1
kind: RightsizeRecommendation
metadata:
  name: my-app-deployment
  namespace: my-app
  ownerReferences:
    - kind: RightsizePolicy
      name: default
spec:
  targetRef:
    kind: Deployment
    name: my-app
status:
  conditions:
    - type: RecommendationReady
      status: "True"
      lastTransitionTime: "2026-02-27T10:00:00Z"
  containers:
    - name: app
      current:
        requests:
          cpu: "2"
          memory: 4Gi
        limits:
          cpu: "4"
          memory: 8Gi
      recommended:
        requests:
          cpu: 850m
          memory: 2200Mi
        limits:
          cpu: 1700m
          memory: 4400Mi
      jvm:
        detected: true
        currentFlags:
          xmx: 2g
          xms: 512m
        recommendedFlags:
          xmx: 1400m
          xms: 1400m
          maxMetaspaceSize: 256m
          reservedCodeCacheSize: 180m
          maxDirectMemorySize: 128m
      savings:
        cpuMillis: 1150
        memoryMiB: 1848
        estimatedMonthlySavingsUSD: 42.50    # if cloud cost integration enabled
  lastApplied: "2026-02-20T03:00:00Z"
  nextEligibleApply: "2026-02-27T03:00:00Z"
```

---

## Project Structure

```
cairn/
├── cmd/
│   ├── operator/           # Main operator entrypoint
│   │   └── main.go
│   └── cli/                # CLI tool (krs)
│       └── main.go
├── api/
│   └── v1alpha1/           # CRD Go types
│       ├── types.go
│       ├── rightsizepolicy_types.go
│       ├── rightsizerecommendation_types.go
│       ├── groupversion_info.go
│       └── zz_generated.deepcopy.go
├── internal/
│   ├── controller/         # Reconciliation loops
│   │   ├── policy_controller.go
│   │   └── recommendation_controller.go
│   ├── collector/          # Metrics collection
│   │   ├── prometheus.go
│   │   ├── metrics_api.go
│   │   ├── jvm.go
│   │   └── aggregator.go
│   ├── recommender/        # Recommendation engine
│   │   ├── engine.go
│   │   ├── standard.go     # OS-level algorithm
│   │   └── java.go         # JVM-aware algorithm
│   ├── actuator/           # Change application
│   │   ├── actuator.go
│   │   ├── inplace.go
│   │   ├── restart.go
│   │   ├── gitops.go
│   │   └── dryrun.go
│   ├── webhook/            # Mutating admission webhook
│   │   ├── injector.go
│   │   └── detector.go     # Java container detection
│   ├── store/              # Metrics storage backends
│   │   ├── store.go        # Interface
│   │   ├── memory.go
│   │   └── redis.go
│   └── api/                # REST API server
│       ├── server.go
│       ├── handlers.go
│       └── openapi.yaml
├── agent/                  # JVM agent (Java subproject)
│   ├── build.gradle
│   └── src/main/java/
│       └── io/cairn/agent/
│           ├── Agent.java          # premain entrypoint
│           ├── MetricsCollector.java
│           └── MetricsServer.java
├── dashboard/              # Web UI (React + TypeScript)
│   ├── package.json
│   ├── src/
│   └── vite.config.ts
├── charts/                 # Helm chart
│   └── cairn/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
├── config/                 # Kustomize base manifests
│   ├── crd/
│   ├── rbac/
│   ├── webhook/
│   └── default/
├── hack/                   # Development scripts
│   ├── setup-kind.sh
│   └── generate.sh
├── test/
│   ├── e2e/                # Chainsaw E2E tests
│   └── integration/
├── docs/                   # MkDocs documentation
│   ├── mkdocs.yml
│   └── docs/
├── Taskfile.yml
├── go.mod
├── go.sum
├── .goreleaser.yml
├── .github/
│   └── workflows/
│       ├── ci.yml
│       ├── release.yml
│       └── docs.yml
├── LICENSE                 # Apache 2.0
├── CONTRIBUTING.md
└── README.md
```

---

## JVM Agent Deep Dive

### How the Injection Works

```
1. Pod created → API server intercepts via MutatingWebhookConfiguration
2. Webhook checks: Is this a Java container? (detection heuristic)
3. If yes:
   a. Add init container: copies agent.jar to emptyDir volume
   b. Mount emptyDir volume to app container at /cairn/
   c. Append to JAVA_TOOL_OPTIONS: -javaagent:/cairn/agent.jar=port=9404
   d. Add prometheus.io/scrape annotation + port annotation
4. Pod starts → JVM loads agent via premain → agent starts HTTP server
5. Prometheus scrapes :9404/metrics → collector aggregates JVM metrics
```

### Agent Design Principles

- **Zero dependencies**: Uses only JDK built-in classes (`java.lang.management`, `com.sun.net.httpserver`)
- **Minimal footprint**: <500KB JAR, <5MB additional RSS, <0.1% CPU overhead
- **Safe defaults**: If agent fails to start, JVM continues normally (fail-open)
- **Version compatibility**: Targets Java 11+ but gracefully degrades on Java 8

### JVM Flag Management

When `manageJvmFlags: true`, the operator can recommend and apply JVM tuning via two methods:

1. **`env` method** (default): Patches `JAVA_TOOL_OPTIONS` or `JAVA_OPTS` on the container spec
2. **`annotation` method**: Sets annotations that a custom entrypoint script reads

The operator tracks JVM flags as part of the `RightsizeRecommendation` status to provide a unified view of both Kubernetes resources and JVM configuration.

---

## Deployment Topology

### Minimal (Single Cluster)

```yaml
# Helm install
helm install cairn oci://ghcr.io/cairn/charts/cairn \
  --namespace cairn-system \
  --set prometheus.url=http://prometheus.monitoring:9090 \
  --set mode=recommend
```

Installs:
- 1× Operator Deployment (controller + webhook + API server)
- CRDs
- RBAC (ClusterRole, ServiceAccount)
- MutatingWebhookConfiguration
- Service (for webhook + API)

### Multi-Cluster (Hub-Spoke)

For setups like PostFinance's 30 clusters:
- **Spoke**: Lightweight agent per cluster (collector + webhook + actuator)
- **Hub**: Central recommender + dashboard + Git integration
- Communication via Prometheus remote-write or Thanos sidecar

---

## Key Differentiators vs. Existing Tools

| Feature | VPA | Goldilocks | cairn |
|---------|-----|------------|----------------|
| JVM-aware recommendations | ❌ | ❌ | ✅ Deep JVM introspection |
| JVM flag management | ❌ | ❌ | ✅ -Xmx, Metaspace, etc. |
| Auto Java detection + agent injection | ❌ | ❌ | ✅ Mutating webhook |
| In-place resize (no restart) | ❌ | ❌ | ✅ K8s 1.27+ |
| GitOps-native (PR-based) | ❌ | ❌ | ✅ GitHub/GitLab PRs |
| Multi-cluster | ❌ | ❌ | ✅ Hub-spoke model |
| GC pressure → CPU scaling | ❌ | ❌ | ✅ GC overhead factor |
| Savings projection | ❌ | ✅ | ✅ Cloud cost integration |
| Policy-based (per-ns, per-workload) | Partial | ❌ | ✅ Full CRD policy |
| Dry-run / audit mode | ❌ | ✅ (view only) | ✅ Tiered trust model |

---

## Roadmap

### v0.1 — Foundation (MVP)
- [ ] CRDs + controller scaffolding
- [ ] Prometheus collector (CPU + memory)
- [ ] Basic percentile-based recommendation engine
- [ ] `RightsizeRecommendation` status output
- [ ] Helm chart + basic RBAC

### v0.2 — Java Awareness
- [ ] JVM agent (heap, GC, threads, metaspace)
- [ ] Mutating webhook for auto-injection
- [ ] Java container detection heuristic
- [ ] JVM-aware recommendation algorithm
- [ ] JVM flag recommendations in status

### v0.3 — Actuation
- [ ] In-place resize support
- [ ] Restart-based apply
- [ ] Dry-run mode with event emission
- [ ] Cooldown + scheduling
- [ ] JVM flag application via env patching

### v0.4 — GitOps & Observability
- [ ] GitOps actuator (GitHub/GitLab PR generation)
- [ ] Kustomize patch + Helm values generation
- [ ] Grafana dashboard ConfigMaps
- [ ] Operator self-metrics
- [ ] CLI tool (`krs`) for ad-hoc queries

### v0.5 — Dashboard & Multi-Cluster
- [ ] REST API server
- [ ] Web dashboard (cluster overview, workload detail, savings)
- [ ] Hub-spoke multi-cluster support
- [ ] Cloud cost integration (AWS, GCP pricing APIs)

### v1.0 — Production Ready
- [ ] Comprehensive E2E test suite
- [ ] Security audit
- [ ] Performance benchmarks (tested at 2500+ namespaces)
- [ ] Full documentation site
- [ ] CNCF Sandbox submission

---

## Open Source Setup

- **License**: Apache 2.0 (standard for CNCF ecosystem)
- **Governance**: MAINTAINERS.md with lazy consensus model
- **Code of Conduct**: Contributor Covenant v2.1
- **CI**: GitHub Actions (lint, test, build, release)
- **Releases**: SemVer via GoReleaser (binaries + Helm chart + container images)
- **Documentation**: MkDocs Material hosted on GitHub Pages
- **Issue Templates**: Bug report, Feature request, RightsizePolicy RFC
- **Community**: GitHub Discussions + (optional) CNCF Slack channel

---

## Getting Involved

```bash
# Clone
git clone https://github.com/cairn/cairn.git
cd cairn

# Setup dev cluster
task dev:setup    # Creates kind cluster with Prometheus

# Run operator locally
task run

# Run tests
task test
task test:e2e

# Build
task build        # Go binary via ko
task agent:build  # JVM agent JAR
```
