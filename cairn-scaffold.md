# Cairn — Scaffold Guide

> **Cairn** — Balanced resource placement for Kubernetes.
>
> A cairn is a carefully balanced stack of stones, each placed with precision.
> Cairn guides your clusters to the right path — no stone left unturned.

---

## Prerequisites

```bash
# Go 1.23+
go version

# kubebuilder (scaffolds operator, CRDs, webhooks, RBAC)
curl -L -o kubebuilder "https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)"
chmod +x kubebuilder && sudo mv kubebuilder /usr/local/bin/

# task (Taskfile runner — replaces Makefile)
go install github.com/go-task/task/v3/cmd/task@latest

# ko (build Go containers without Dockerfile)
go install github.com/google/ko@latest

# goreleaser
go install github.com/goreleaser/goreleaser/v2@latest

# golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# controller-gen (CRD/RBAC generation — kubebuilder installs this too)
go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

# oapi-codegen (OpenAPI → Go server/client)
go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

# chainsaw (E2E testing)
go install github.com/kyverno/chainsaw@latest

# kind (local dev cluster)
go install sigs.k8s.io/kind@latest
```

---

## 1. Initialize the Go Module + Kubebuilder Project

```bash
mkdir cairn && cd cairn
git init

# Initialize kubebuilder project
# --domain    = your CRD API group suffix (e.g. cairn.io)
# --repo      = Go module path
# --owner     = copyright header in generated files
kubebuilder init \
  --domain cairn.io \
  --repo github.com/cairn-io/cairn \
  --owner "The Cairn Authors" \
  --project-name cairn
```

This creates:
```
cairn/
├── cmd/main.go              # operator entrypoint
├── config/                   # kustomize manifests (RBAC, manager, etc.)
├── hack/boilerplate.go.txt   # license header for codegen
├── Dockerfile
├── Makefile                  # we'll replace with Taskfile later
├── PROJECT                   # kubebuilder metadata
├── go.mod
└── go.sum
```

---

## 2. Scaffold the CRD APIs

```bash
# RightsizePolicy — defines HOW rightsizing works for a set of workloads
kubebuilder create api \
  --group rightsizing \
  --version v1alpha1 \
  --kind RightsizePolicy \
  --resource --controller

# RightsizeRecommendation — auto-generated per workload with current vs. recommended
kubebuilder create api \
  --group rightsizing \
  --version v1alpha1 \
  --kind RightsizeRecommendation \
  --resource --controller
```

This creates:
```
api/v1alpha1/
├── rightsizepolicy_types.go           # ← you define your spec/status structs here
├── rightsizerecommendation_types.go   # ← same
├── groupversion_info.go
└── zz_generated.deepcopy.go          # auto-generated, don't touch

internal/controller/
├── rightsizepolicy_controller.go          # ← reconcile loop
├── rightsizepolicy_controller_test.go
├── rightsizerecommendation_controller.go  # ← reconcile loop
├── rightsizerecommendation_controller_test.go
└── suite_test.go                          # envtest setup
```

---

## 3. Scaffold the Mutating Webhook (JVM Agent Injector)

```bash
# Mutating webhook for RightsizePolicy
# --defaulting = mutating webhook (injects agent into pods)
kubebuilder create webhook \
  --group rightsizing \
  --version v1alpha1 \
  --kind RightsizePolicy \
  --defaulting

# IMPORTANT: The above scaffolds a webhook on the CRD itself.
# For pod injection, we need a separate webhook that intercepts Pod creation.
# Kubebuilder doesn't scaffold "external resource" webhooks directly.
# You'll manually create the pod mutating webhook — scaffold the file:
mkdir -p internal/webhook
touch internal/webhook/pod_injector.go
touch internal/webhook/java_detector.go
```

The pod injector webhook will need a manual `MutatingWebhookConfiguration` in:
```bash
mkdir -p config/webhook/manifests
touch config/webhook/manifests/pod-mutating-webhook.yaml
```

---

## 4. Generate CRDs, RBAC, DeepCopy

```bash
# Generate all the things
# - CRD manifests from Go types (into config/crd/bases/)
# - DeepCopy implementations
# - RBAC ClusterRole from +kubebuilder:rbac markers
make generate   # runs controller-gen object (deepcopy)
make manifests  # runs controller-gen crd rbac:roleName=cairn-manager webhook
```

Verify CRDs were generated:
```bash
ls config/crd/bases/
# rightsizing.cairn.io_rightsizepolicies.yaml
# rightsizing.cairn.io_rightsizerecommendations.yaml
```

---

## 5. Create the Extended Directory Structure

```bash
# ── Core operator internals ──
mkdir -p internal/collector      # metrics collection (prometheus, metrics-api, jvm)
mkdir -p internal/recommender    # recommendation algorithms
mkdir -p internal/actuator       # change appliers (in-place, restart, gitops)
mkdir -p internal/store          # metrics storage backends
mkdir -p internal/api            # REST API server

touch internal/collector/prometheus.go
touch internal/collector/metrics_api.go
touch internal/collector/jvm.go
touch internal/collector/aggregator.go

touch internal/recommender/engine.go
touch internal/recommender/standard.go
touch internal/recommender/java.go

touch internal/actuator/actuator.go
touch internal/actuator/inplace.go
touch internal/actuator/restart.go
touch internal/actuator/gitops.go
touch internal/actuator/dryrun.go

touch internal/store/store.go
touch internal/store/memory.go
touch internal/store/redis.go

touch internal/api/server.go
touch internal/api/handlers.go
touch internal/api/openapi.yaml

# ── CLI tool ──
mkdir -p cmd/cli
touch cmd/cli/main.go

# ── JVM Agent (Java subproject) ──
mkdir -p agent/src/main/java/io/cairn/agent
touch agent/src/main/java/io/cairn/agent/Agent.java
touch agent/src/main/java/io/cairn/agent/MetricsCollector.java
touch agent/src/main/java/io/cairn/agent/MetricsServer.java
touch agent/build.gradle
touch agent/settings.gradle

# ── Dashboard (React + TypeScript) ──
# (initialize later with: cd dashboard && npm create vite@latest . -- --template react-ts)
mkdir -p dashboard

# ── Helm chart ──
mkdir -p charts/cairn
touch charts/cairn/Chart.yaml
touch charts/cairn/values.yaml
mkdir -p charts/cairn/templates
touch charts/cairn/templates/deployment.yaml
touch charts/cairn/templates/service.yaml
touch charts/cairn/templates/serviceaccount.yaml
touch charts/cairn/templates/clusterrole.yaml
touch charts/cairn/templates/clusterrolebinding.yaml
touch charts/cairn/templates/mutatingwebhookconfiguration.yaml
touch charts/cairn/templates/_helpers.tpl

# ── E2E tests ──
mkdir -p test/e2e
touch test/e2e/.chainsaw.yaml

# ── Docs ──
mkdir -p docs/docs
touch docs/mkdocs.yml
touch docs/docs/index.md
touch docs/docs/getting-started.md
touch docs/docs/architecture.md
touch docs/docs/java-detection.md
touch docs/docs/policies.md

# ── Grafana dashboards ──
mkdir -p deploy/grafana
touch deploy/grafana/cairn-overview.json
touch deploy/grafana/cairn-jvm-detail.json

# ── Dev tooling ──
mkdir -p hack
touch hack/setup-kind.sh
touch hack/install-prometheus.sh
```

---

## 6. Replace Makefile with Taskfile

```bash
rm Makefile
touch Taskfile.yml
```

Starter `Taskfile.yml` structure (fill in yourself):
```yaml
# Taskfile.yml
version: "3"

vars:
  IMG: ghcr.io/cairn-io/cairn
  AGENT_IMG: ghcr.io/cairn-io/cairn-agent

tasks:
  # ── Code Generation ──
  generate:       # controller-gen object (deepcopy)
  manifests:      # controller-gen crd rbac webhook
  api-gen:        # oapi-codegen from openapi.yaml

  # ── Development ──
  run:            # go run cmd/main.go (against local kubeconfig)
  dev:setup:      # kind create cluster + install prometheus
  dev:teardown:   # kind delete cluster
  install-crds:   # kubectl apply -k config/crd

  # ── Build ──
  build:          # ko build for operator
  build:cli:      # go build cmd/cli
  build:agent:    # gradle build in agent/
  build:dashboard: # npm run build in dashboard/

  # ── Test ──
  test:           # go test ./...
  test:e2e:       # chainsaw test
  lint:           # golangci-lint run

  # ── Release ──
  release:        # goreleaser release
```

---

## 7. Configure GoReleaser

```bash
touch .goreleaser.yml
```

---

## 8. GitHub Setup

```bash
mkdir -p .github/workflows
touch .github/workflows/ci.yml
touch .github/workflows/release.yml
touch .github/workflows/docs.yml

mkdir -p .github/ISSUE_TEMPLATE
touch .github/ISSUE_TEMPLATE/bug_report.md
touch .github/ISSUE_TEMPLATE/feature_request.md
touch .github/PULL_REQUEST_TEMPLATE.md
```

---

## 9. Root Files

```bash
touch LICENSE              # Apache 2.0
touch README.md
touch CONTRIBUTING.md
touch MAINTAINERS.md
touch CODE_OF_CONDUCT.md
touch .golangci.yml
touch .ko.yaml
```

---

## 10. Verify the Scaffold

```bash
# Ensure everything compiles
go mod tidy
go build ./...

# Ensure CRDs generate cleanly
make generate
make manifests

# Run scaffolded tests (envtest)
go test ./internal/controller/... -v

# Install CRDs into a local cluster
kind create cluster --name cairn-dev
kubectl apply -k config/crd
kubectl get crds | grep cairn

# Verify CRDs
kubectl explain rightsizepolicy.spec
kubectl explain rightsizerecommendation.status
```

---

## Final Scaffold Tree

```
cairn/
├── .github/
│   ├── workflows/
│   │   ├── ci.yml
│   │   ├── release.yml
│   │   └── docs.yml
│   ├── ISSUE_TEMPLATE/
│   │   ├── bug_report.md
│   │   └── feature_request.md
│   └── PULL_REQUEST_TEMPLATE.md
├── agent/                          # JVM agent (Java)
│   ├── build.gradle
│   ├── settings.gradle
│   └── src/main/java/io/cairn/agent/
│       ├── Agent.java
│       ├── MetricsCollector.java
│       └── MetricsServer.java
├── api/v1alpha1/                   # CRD Go types (kubebuilder generated)
│   ├── groupversion_info.go
│   ├── rightsizepolicy_types.go
│   ├── rightsizerecommendation_types.go
│   └── zz_generated.deepcopy.go
├── charts/cairn/                  # Helm chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
├── cmd/
│   ├── main.go                     # operator entrypoint (kubebuilder generated)
│   └── cli/main.go                 # CLI tool
├── config/                         # kustomize (kubebuilder generated)
│   ├── crd/bases/
│   ├── rbac/
│   ├── webhook/
│   ├── manager/
│   └── default/
├── dashboard/                      # React + TypeScript (init with vite later)
├── deploy/grafana/                 # Grafana dashboard JSON
├── docs/                           # MkDocs
│   ├── mkdocs.yml
│   └── docs/
├── hack/
│   ├── setup-kind.sh
│   └── install-prometheus.sh
├── internal/
│   ├── controller/                 # reconcilers (kubebuilder generated)
│   │   ├── rightsizepolicy_controller.go
│   │   ├── rightsizerecommendation_controller.go
│   │   └── suite_test.go
│   ├── webhook/                    # pod mutating webhook
│   │   ├── pod_injector.go
│   │   └── java_detector.go
│   ├── collector/                  # metrics ingestion
│   │   ├── prometheus.go
│   │   ├── metrics_api.go
│   │   ├── jvm.go
│   │   └── aggregator.go
│   ├── recommender/                # algorithms
│   │   ├── engine.go
│   │   ├── standard.go
│   │   └── java.go
│   ├── actuator/                   # change appliers
│   │   ├── actuator.go
│   │   ├── inplace.go
│   │   ├── restart.go
│   │   ├── gitops.go
│   │   └── dryrun.go
│   ├── store/                      # metrics storage
│   │   ├── store.go
│   │   ├── memory.go
│   │   └── redis.go
│   └── api/                        # REST API
│       ├── server.go
│       ├── handlers.go
│       └── openapi.yaml
├── test/e2e/                       # chainsaw E2E tests
├── .github/
├── .golangci.yml
├── .goreleaser.yml
├── .ko.yaml
├── CODE_OF_CONDUCT.md
├── CONTRIBUTING.md
├── Dockerfile                      # kubebuilder generated (optional, ko replaces)
├── LICENSE
├── MAINTAINERS.md
├── PROJECT                         # kubebuilder metadata
├── README.md
├── Taskfile.yml
├── go.mod
└── go.sum
```

---

## Quick Reference: Key Commands You'll Use Daily

```bash
# After editing _types.go files:
make generate          # regenerate deepcopy
make manifests         # regenerate CRDs + RBAC

# Run operator against local cluster:
task run               # or: go run cmd/main.go

# Apply CRDs to cluster:
kubectl apply -k config/crd

# Build container image:
ko build ./cmd/        # outputs ghcr.io/cairn-io/cairn:latest

# Run tests:
go test ./... -v
task test:e2e

# Lint:
golangci-lint run
```
