{{/*
Expand the name of the chart.
*/}}
{{- define "node-io-manager.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "node-io-manager.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "node-io-manager.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "node-io-manager.labels" -}}
helm.sh/chart: {{ include "node-io-manager.chart" . }}
{{ include "node-io-manager.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "node-io-manager.selectorLabels" -}}
app.kubernetes.io/name: {{ include "node-io-manager.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "node-io-manager.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "node-io-manager.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Get API Key from secret or value
*/}}
{{- define "node-io-manager.apiKeyEnv" -}}
{{- if .Values.aiAgent.provider.apiKeySecretRef.name }}
- name: LLM_API_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.aiAgent.provider.apiKeySecretRef.name }}
      key: {{ .Values.aiAgent.provider.apiKeySecretRef.key | default "api-key" }}
{{- else if .Values.aiAgent.provider.apiKey }}
- name: LLM_API_KEY
  value: {{ .Values.aiAgent.provider.apiKey | quote }}
{{- end }}
{{- end }}
