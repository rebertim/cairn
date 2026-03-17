#!/usr/bin/env bash
# hack/sync-helm.sh
#
# Re-generates Helm chart templates from controller-gen output in config/.
# Called automatically by 'make manifests'; no external tools required.
#
# What it syncs:
#   config/crd/bases/*.yaml          → charts/cairn/templates/*-crd.yaml
#   config/rbac/role.yaml            → charts/cairn/templates/manager-rbac.yaml
#   config/webhook/manifests.yaml    → charts/cairn/templates/validating-webhook-configuration.yaml

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATES="${REPO_ROOT}/charts/cairn/templates"
CONFIG="${REPO_ROOT}/config"

# ── CRDs ──────────────────────────────────────────────────────────────────────
# Each generated CRD becomes a Helm template with resource-policy: keep and
# standard cairn labels.  The trailing status: block from controller-gen is
# preserved verbatim so the schema is identical.

for crd_file in "${CONFIG}/crd/bases"/*.yaml; do
  [ -f "${crd_file}" ] || continue

  crd_name=$(awk '/^  name:/{print $2; exit}' "${crd_file}")
  singular=$(awk '/    singular:/{print $2; exit}' "${crd_file}")
  cg_version=$(awk -F': ' '/controller-gen.kubebuilder.io\/version/{print $2; exit}' "${crd_file}")

  # Everything from spec: to end-of-file (includes the status: scaffold block)
  spec_onwards=$(awk '/^spec:/{found=1} found{print}' "${crd_file}")

  {
    cat <<EOF
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ${crd_name}
  annotations:
    "helm.sh/resource-policy": keep
    controller-gen.kubebuilder.io/version: ${cg_version}
  labels:
  {{- include "cairn.labels" . | nindent 4 }}
EOF
    printf '%s\n' "${spec_onwards}"
  } > "${TEMPLATES}/${singular}-crd.yaml"

  echo "  synced ${singular}-crd.yaml"
done

# ── manager-rbac.yaml ─────────────────────────────────────────────────────────
# The rules section is fully owned by controller-gen (+kubebuilder:rbac markers).
# We write the static Helm boilerplate and embed the generated rules verbatim.

rules=$(awk '/^rules:/{found=1; next} found{print}' "${CONFIG}/rbac/role.yaml")

{
  cat <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "cairn.fullname" . }}-manager-role
  labels:
  {{- include "cairn.labels" . | nindent 4 }}
rules:
EOF
  printf '%s\n' "${rules}"
  cat <<'EOF'
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ include "cairn.fullname" . }}-manager-rolebinding
  labels:
  {{- include "cairn.labels" . | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: '{{ include "cairn.fullname" . }}-manager-role'
subjects:
- kind: ServiceAccount
  name: '{{ include "cairn.serviceAccountName" . }}'
  namespace: '{{ .Release.Namespace }}'
EOF
} > "${TEMPLATES}/manager-rbac.yaml"

echo "  synced manager-rbac.yaml"

# ── validating-webhook-configuration.yaml ─────────────────────────────────────
# Extract the webhooks list from the ValidatingWebhookConfiguration document in
# config/webhook/manifests.yaml.  Static service name and namespace are replaced
# with Helm template expressions via sed.

webhooks=$(awk '
  /^kind: ValidatingWebhookConfiguration/ { found=1 }
  found && /^webhooks:/                   { p=1; next }
  p && /^---/                             { p=0; found=0 }
  p                                       { print }
' "${CONFIG}/webhook/manifests.yaml")

{
  cat <<'EOF'
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: {{ include "cairn.fullname" . }}-validating-webhook-configuration
  annotations:
    cert-manager.io/inject-ca-from: {{ .Release.Namespace }}/{{ include "cairn.fullname" . }}-serving-cert
  labels:
  {{- include "cairn.labels" . | nindent 4 }}
webhooks:
EOF
  printf '%s\n' "${webhooks}"
} | sed \
  -e "s|      name: webhook-service|      name: '{{ include \"cairn.fullname\" . }}-webhook-service'|g" \
  -e "s|      namespace: system|      namespace: '{{ .Release.Namespace }}'|g" \
  > "${TEMPLATES}/validating-webhook-configuration.yaml"

echo "  synced validating-webhook-configuration.yaml"
echo "Helm chart templates up to date."
