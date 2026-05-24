{{- range . -}}
- **{{ .Name }}** — {{ .LicenseName }}. Source: <{{ .LicenseURL }}>
{{ end -}}
