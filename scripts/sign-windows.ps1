# sign-windows.ps1 — sign a Wails-built Windows .exe with a code-signing cert.
# Run after `wails build -platform windows/amd64`.
#
# Three modes:
#
#   1. PFX file on disk:
#        $env:WIN_CERT_PATH = "C:\path\to\cert.pfx"
#        $env:WIN_CERT_PASSWORD = "secret"
#        ./scripts/sign-windows.ps1
#
#   2. Cert in Windows cert store (by thumbprint):
#        $env:WIN_CERT_THUMBPRINT = "ABCD1234..."
#        ./scripts/sign-windows.ps1
#
#   3. Microsoft Trusted Signing (Azure-hosted; preferred for CI):
#        See https://learn.microsoft.com/azure/trusted-signing/.
#        Use the azure/trusted-signing-action in GH Actions; this script
#        handles only the local PFX / cert-store paths.
#
# Args:
#   -ExePath  : path to the .exe (default: auto-detect under build/bin)
#   -Timestamp: RFC3161 timestamp URL (default: http://timestamp.digicert.com)

param(
  [string]$ExePath = "",
  [string]$Timestamp = "http://timestamp.digicert.com"
)

$ErrorActionPreference = 'Stop'

if (-not $ExePath) {
  $ExePath = Get-ChildItem -Path build/bin -Recurse -Filter *.exe `
    | Select-Object -First 1 -ExpandProperty FullName
}
if (-not $ExePath -or -not (Test-Path $ExePath)) {
  Write-Error "Cannot find .exe under build/bin. Run 'wails build' first or pass -ExePath."
}

# Resolve signtool. Newest Windows SDK lives under x64; fall back gracefully.
$signtool = (Get-ChildItem `
  "${env:ProgramFiles(x86)}\Windows Kits\10\bin" `
  -Recurse -Filter signtool.exe `
  -ErrorAction SilentlyContinue `
  | Where-Object { $_.FullName -match 'x64' } `
  | Sort-Object FullName -Descending `
  | Select-Object -First 1 -ExpandProperty FullName)
if (-not $signtool) {
  Write-Error "signtool.exe not found. Install the Windows 10/11 SDK."
}

Write-Host "→ Signing $ExePath"
Write-Host "  signtool: $signtool"

if ($env:WIN_CERT_PATH) {
  if (-not $env:WIN_CERT_PASSWORD) {
    Write-Error "WIN_CERT_PATH is set but WIN_CERT_PASSWORD is missing."
  }
  & $signtool sign `
    /f $env:WIN_CERT_PATH `
    /p $env:WIN_CERT_PASSWORD `
    /tr $Timestamp /td sha256 /fd sha256 `
    /v $ExePath
}
elseif ($env:WIN_CERT_THUMBPRINT) {
  & $signtool sign `
    /sha1 $env:WIN_CERT_THUMBPRINT `
    /tr $Timestamp /td sha256 /fd sha256 `
    /v $ExePath
}
else {
  Write-Error @"
No cert configured. Set one of:
  WIN_CERT_PATH      + WIN_CERT_PASSWORD  (PFX file)
  WIN_CERT_THUMBPRINT                     (cert in Windows store)

Or use Microsoft Trusted Signing in CI via the azure/trusted-signing-action.
"@
}

Write-Host "→ Verifying signature"
& $signtool verify /pa /v $ExePath

Write-Host "✓ done: $ExePath"
