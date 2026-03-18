# Cairn Roadmap — Public Release Plan

> Current state: **Early Alpha** — Core pipeline works end-to-end (policy → recommendation → apply), JVM-aware rightsizing functional, Helm chart deployable. Several gaps remain before a public release is appropriate.

---

## Milestones

### M1 — Alpha Hardening (pre-public, internal only)
_Goal: Make what exists reliable and safe to run on real clusters._

- [ ] Implement `ClusterRightsizePolicy` controller
- [ ] Direction-aware applies (restart storm mitigation)
- [ ] Policy webhook defaulter
- [ ] Fill recommendation status gaps (LowerBound, UpperBound, SavingsEstimate, CurrentFlags)
- [ ] Controller unit tests

### M2 — Beta (limited public, invite-only)
_Goal: Safe for adventurous early adopters to try._

- [ ] StatefulSet and DaemonSet E2E tests
- [ ] Argocd / GitOps actuator (or remove stub)
- [ ] Admission validation webhook for RightsizePolicy
- [ ] `kubectl cairn` plugin or equivalent UX for inspecting recommendations
- [ ] Upgrade path documentation (CRD migrations)

### M3 — GA (public release)
_Goal: Stable, documented, trustworthy for production use._

- [ ] Stable API (graduate from v1alpha1 → v1beta1)
- [ ] Full test coverage (unit + integration + E2E)
- [ ] Security audit / responsible disclosure policy
- [ ] Operator compatibility matrix (k8s versions, Prometheus versions)
- [ ] SLO/support commitments documented

---

## Full Todo List

### Critical — Blocks any meaningful public use

- [ ] **Implement `ClusterRightsizePolicyReconciler`**
  - Evaluate NamespaceSelector (matchNames, excludeNames, labelSelector)
  - List matching namespaces; for each, enumerate target workloads via TargetRef
  - Create/sync `RightsizeRecommendation` objects (same as namespaced controller but cross-namespace)
  - Priority resolution when multiple cluster policies match the same workload (higher `.spec.priority` wins)
  - AllowOverride: if false, namespace-scoped policies cannot override cluster policy settings
  - Write status: targetedNamespaces, targetedWorkloads, recommendationsReady, lastReconcileTime
  - Add conditions (Ready, Degraded) to status

- [ ] **Direction-aware applies (restart storm mitigation)**
  - Scale-UP: apply immediately once stability window passes
  - Scale-DOWN: require burst phase = Normal + additional cooldown (e.g. 2x minApplyInterval)
  - Prevents: burst spike → apply → restart → spike → apply loop
  - Validate fix holds in lab-t before marking done

- [ ] **Populate missing recommendation status fields**
  - `LowerBound` — conservative recommendation (e.g. P50 + headroom)
  - `UpperBound` — aggressive recommendation (e.g. P99 + headroom)
  - `SavingsEstimate` — (current - recommended) in cores/GiB, expressed as percentage
  - `JVM.CurrentFlags` — parse and surface current JAVA_TOOL_OPTIONS flags from running pods
  - These fields are defined in the API and used in the Grafana dashboard but never written

### High — Required for a trustworthy public release

- [ ] **Policy webhook defaulter**
  - Set `mode: recommend` when mode is empty or invalid
  - Set `updateStrategy: restart` when empty
  - Set `changeThreshold: 10` when 0
  - Set `window: 168h` when zero
  - Set `java.gcOverheadWeight: 1.0` when java block is present but weight is nil
  - Currently the webhook is wired up but `Default()` is empty (no-op)

- [ ] **Admission validation webhook for RightsizePolicy**
  - Reject `mode: auto` with no `targetRef.name` — too broad without explicit targeting
  - Reject `updateStrategy: in-place` on k8s < 1.27 (detect server version)
  - Warn (not reject) when `changeThreshold: 0` (would apply on every reconcile)
  - Validate `minRequest <= maxRequest` when both are set
  - Reject `window` shorter than 1h (not enough data for recommendations)

- [ ] **Controller unit tests**
  - `RightsizePolicyReconciler`: workload discovery (by name, wildcard, label selector), recommendation create/update, status update
  - `RightsizeRecommendationReconciler`: actuator delegation, LastAppliedTime written on apply, skip when suspended
  - `ClusterRightsizePolicyReconciler`: namespace selection logic, priority resolution, override handling

- [ ] **Collector tests**
  - Prometheus query integration tests (mock Prometheus server)
  - JVM metrics aggregation tests
  - Behavior when no data is available (cold start)

- [ ] **Actuator tests**
  - `InPlaceActuator`: verify pod patch structure
  - `RestartActuator`: verify annotation written, workload not spec-patched

- [ ] **E2E test expansion**
  - StatefulSet lifecycle (create policy → recommendation → apply)
  - DaemonSet lifecycle
  - Java workload: agent injection, JVM flags applied at pod creation
  - In-place update strategy
  - Suspended policy: verify no apply occurs
  - ClusterRightsizePolicy cross-namespace targeting

### Medium — Quality and usability

- [ ] **RightsizePolicy status conditions**
  - `Ready=True` when recommendations are up to date
  - `Degraded=True` with message when Prometheus is unreachable or no metrics data
  - `Suspended=True` when `spec.suspended: true`
  - Currently `status.conditions` is defined in the type but never written by the controller

- [ ] **RightsizeRecommendation status conditions**
  - `DataSufficient=False` when lookback window has < N samples (cold start warning)
  - `BurstActive=True` when in Bursting phase
  - `Applied=True/False` for last apply result

- [ ] **Recommendation history / change log**
  - Surface at least the last 5 applied recommendations in status or via events
  - Helps operators understand what changed and when

- [ ] **GitOps actuator or remove the stub**
  - `internal/actuator/gitops.go` is an empty file today
  - Either implement (open a PR/commit with the updated resource YAML) or delete it
  - If implementing: target at least Flux HelmRelease or plain Deployment YAML in a git repo

- [ ] **API server**
  - `internal/api/handlers.go` and `server.go` are empty
  - Decide: implement (lightweight read API for kubectl plugin) or remove
  - If removing: clean up wiring in main.go

- [ ] **Helm chart improvements**
  - `values.yaml`: add `nodeSelector`, `tolerations`, `affinity` to manager pod
  - `values.yaml`: add `podAnnotations`, `podLabels`
  - `values.yaml`: configurable `reconcileInterval` per-policy (currently global only)
  - Test chart rendering in CI (`helm template | kubectl --dry-run=client`)
  - Add NetworkPolicy template (restrict egress to Prometheus, ingress to webhook)

- [ ] **Rollout CRD support**
  - `TargetRef.Kind` accepts "Rollout" (Argo Rollouts) but controller only handles Deployment/StatefulSet/DaemonSet
  - Either implement or remove from the enum validation

- [ ] **Pod security: agent image pinning**
  - `agentImage` in values defaults to `:latest` tag — should be pinned to a digest for supply-chain safety
  - Add `imagePullPolicy: IfNotPresent` as default for agent

### Low — Polish before public launch

- [ ] **Documentation updates**
  - Architecture diagram (shows the full pipeline from metrics → recommender → actuator → webhook)
  - Troubleshooting guide (no metrics, agent not injecting, recommendation not applying)
  - Upgrade guide (between Cairn versions)
  - ClusterRightsizePolicy usage guide (once implemented)
  - Values reference auto-generated from values.yaml

- [ ] **`kubectl cairn` plugin (or `cairn status` command)**
  - `cairn get policies` — table: policy name, mode, workloads covered, last reconcile
  - `cairn describe <workload>` — show current vs recommended resources, savings, burst state
  - Nice-to-have, not blocking

- [ ] **Metrics: add savings tracking**
  - Counter: estimated CPU cores saved / memory GiB saved since operator installed
  - Expose as Prometheus metrics so Grafana can graph cumulative savings
  - Dashboard panel for total savings

- [ ] **Alerting rules**
  - `CairnRecommendationStale` — recommendation not updated in > 2× window
  - `CairnApplyFailed` — apply error rate > 0 in last 15m
  - `CairnBurstFlapping` — workload entering/leaving burst > 3× per hour
  - Currently only a generic cairn-injected-pods PrometheusRule exists

- [ ] **SBOM and vulnerability scanning**
  - Add `trivy` or `grype` to release workflow to scan the operator image
  - Publish SBOM as release artifact (syft)

- [ ] **Responsible disclosure / security policy**
  - Add `SECURITY.md` with contact and disclosure timeline

- [ ] **Compatibility matrix**
  - Declare minimum Kubernetes version (1.27 for in-place; 1.31 for ImageVolume agent injection)
  - Declare minimum Prometheus version tested
  - Declare Argo Rollouts compatibility (if Rollout kind is kept)

---

## Known Issues (fix before public)

| # | Issue | Severity | Notes |
|---|-------|----------|-------|
| 1 | Restart storm | High | Burst → apply → restart → burst loop possible in auto+restart mode |
| 2 | ClusterRightsizePolicy no-op | High | Controller stub, creates resource but does nothing |
| 3 | Missing status conditions | Medium | conditions[] never written by any controller |
| 4 | LowerBound/UpperBound/SavingsEstimate never populated | Medium | API fields defined but always nil |
| 5 | Policy defaulter empty | Medium | No server-side defaults for missing optional fields |
| 6 | Rollout kind advertised but unimplemented | Low | Causes misleading validation to pass |
| 7 | Agent image uses :latest | Low | Supply-chain risk, breaks reproducibility |
| 8 | `cairn_reconciles_total` may double-count | Low | Both policy and recommendation controllers emit; labels differentiate but dashboard math needs verification |

---

## Not Planned (explicitly out of scope)

- VPA (Vertical Pod Autoscaler) compatibility shim — Cairn replaces VPA, not wraps it
- Multi-cluster support — each cluster runs its own operator
- Non-Prometheus metric backends — Prometheus is the only supported collector
