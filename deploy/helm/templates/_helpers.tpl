{{/*
sextant.gib renders a Kubernetes quantity as a number of GiB, so a template can
compare a size against a floor. Helm has no unit parser, and a guard that only
understood "8Gi" would pass silently on "500Mi" - which is the case it exists
to catch.

Binary and decimal suffixes both, because a values file written by hand carries
whichever the author had in mind, and treating 1G as 1Gi would only ever err
towards allowing a volume that is too small.
*/}}
{{- define "sextant.gib" -}}
{{- $q := . | toString -}}
{{- $n := regexFind "^[0-9]+(\\.[0-9]+)?" $q | float64 -}}
{{- $u := regexFind "[A-Za-z]*$" $q -}}
{{- if eq $u "Ti" -}}{{ mulf $n 1024.0 }}
{{- else if eq $u "Gi" -}}{{ $n }}
{{- else if eq $u "Mi" -}}{{ divf $n 1024.0 }}
{{- else if eq $u "Ki" -}}{{ divf $n 1048576.0 }}
{{- else if eq $u "T" -}}{{ divf (mulf $n 1000000000000.0) 1073741824.0 }}
{{- else if eq $u "G" -}}{{ divf (mulf $n 1000000000.0) 1073741824.0 }}
{{- else if eq $u "M" -}}{{ divf (mulf $n 1000000.0) 1073741824.0 }}
{{- else -}}{{ divf $n 1073741824.0 }}
{{- end -}}
{{- end -}}

{{/*
sextant.dbMode resolves database.mode with the pre-0.93 behaviour as the
fallback, so values files that only know cnpg.enabled keep rendering the same.
*/}}
{{- define "sextant.dbMode" -}}
{{- if .Values.database.mode -}}{{ .Values.database.mode }}
{{- else if .Values.cnpg.enabled -}}cnpg
{{- else -}}none
{{- end -}}
{{- end -}}

{{/*
sextant.routeKind resolves route.kind the same way from ingress.enabled.
*/}}
{{- define "sextant.routeKind" -}}
{{- if .Values.route.kind -}}{{ .Values.route.kind }}
{{- else if .Values.ingress.enabled -}}ingress
{{- else -}}none
{{- end -}}
{{- end -}}

{{/*
sextant.pgSelector is the label that selects the database pods, for the
network policies: CNPG labels its instances, the embedded StatefulSet is ours.
*/}}
{{- define "sextant.pgSelector" -}}
{{- if eq (include "sextant.dbMode" .) "cnpg" -}}
cnpg.io/cluster: {{ .Values.cnpg.name }}
{{- else -}}
app: {{ .Release.Name }}-pg
{{- end -}}
{{- end -}}
