{{/*
Expand the name of the chart.
*/}}
{{- define "cairn.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "cairn.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart label.
*/}}
{{- define "cairn.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "cairn.labels" -}}
helm.sh/chart: {{ include "cairn.chart" . }}
{{ include "cairn.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "cairn.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cairn.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "cairn.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "cairn.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Webhook TLS secret name.
*/}}
{{- define "cairn.webhookTLSSecret" -}}
{{- printf "%s-webhook-tls" (include "cairn.fullname" .) }}
{{- end }}

{{/*
Webhook service name.
*/}}
{{- define "cairn.webhookServiceName" -}}
{{- printf "%s-webhook" (include "cairn.fullname" .) }}
{{- end }}

{{/*
cert-manager Certificate name (also used as the inject-ca-from reference).
*/}}
{{- define "cairn.certificateName" -}}
{{- printf "%s-webhook" (include "cairn.fullname" .) }}
{{- end }}

{{/*
Self-signed issuer name (created when certManager.issuerName is empty).
*/}}
{{- define "cairn.issuerName" -}}
{{- if .Values.webhook.certManager.issuerName }}
{{- .Values.webhook.certManager.issuerName }}
{{- else }}
{{- printf "%s-selfsigned" (include "cairn.fullname" .) }}
{{- end }}
{{- end }}
