{{/* Expand the name of the chart. */}}
{{- define "loadify.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified app name. */}}
{{- define "loadify.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "loadify.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "loadify.labels" -}}
helm.sh/chart: {{ include "loadify.chart" . }}
app.kubernetes.io/name: {{ include "loadify.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{ if .Chart.AppVersion }}app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}{{ end }}
{{- end -}}

{{/* Selector labels for a component. Usage: include "loadify.selectorLabels" (dict "ctx" . "component" "apisrv") */}}
{{- define "loadify.selectorLabels" -}}
app.kubernetes.io/name: {{ include "loadify.name" .ctx }}
app.kubernetes.io/instance: {{ .ctx.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* Image reference for a component. Usage: include "loadify.image" (dict "ctx" . "component" "apisrv") */}}
{{- define "loadify.image" -}}
{{- $img := .ctx.Values.image -}}
{{- printf "%s/%s-%s:%s" $img.registry $img.repository .component $img.tag -}}
{{- end -}}

{{- define "loadify.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "loadify.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "loadify.secretName" -}}
{{- if .Values.secret.existingSecret -}}
{{- .Values.secret.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "loadify.fullname" .) -}}
{{- end -}}
{{- end -}}
