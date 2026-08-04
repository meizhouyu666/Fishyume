param(
  [ValidateSet('windows-amd64','linux-amd64')][string]$Target = 'windows-amd64',
  [string]$OutputDir = "$PSScriptRoot\..\artifacts"
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path "$PSScriptRoot\..\..\wf-engine").Path
$out = (New-Item -ItemType Directory -Force -Path $OutputDir).FullName
$parts = $Target.Split('-')
$goos = if ($parts[0] -eq 'windows') { 'windows' } else { 'linux' }
$binary = if ($goos -eq 'windows') { 'fishyume-engine.exe' } else { 'fishyume-engine' }
$dest = Join-Path $out "$Target\$binary"
New-Item -ItemType Directory -Force -Path (Split-Path $dest) | Out-Null
Push-Location $root
$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$previousCgo = $env:CGO_ENABLED
try {
  $env:GOOS = $goos
  $env:GOARCH = 'amd64'
  $env:CGO_ENABLED = '0'
  go build -trimpath -ldflags='-s -w' -o $dest ./cmd/wf-engine
} finally {
  $env:GOOS = $previousGoos
  $env:GOARCH = $previousGoarch
  $env:CGO_ENABLED = $previousCgo
  Pop-Location
}
$hash = (Get-FileHash -Algorithm SHA256 $dest).Hash.ToLowerInvariant()
"$hash  $binary" | Set-Content -NoNewline -Encoding ascii (Join-Path (Split-Path $dest) 'SHA256SUMS')
$packageName = if ($goos -eq 'windows') { 'fishyume-engine-win32-x64' } else { 'fishyume-engine-linux-x64' }
$packageBin = Join-Path $PSScriptRoot "..\packages\$packageName\bin"
New-Item -ItemType Directory -Force -Path $packageBin | Out-Null
Copy-Item -Force $dest (Join-Path $packageBin $binary)
$archive = if ($goos -eq 'windows') { Join-Path $out "$Target.zip" } else { Join-Path $out "$Target.tar.gz" }
$format = if ($goos -eq 'windows') { 'zip' } else { 'targz' }
go run (Join-Path $PSScriptRoot 'archive-engine.go') -format $format -input $dest -checksum (Join-Path (Split-Path $dest) 'SHA256SUMS') -output $archive
$archiveNames = @('linux-amd64.tar.gz', 'windows-amd64.zip')
$manifestLines = foreach ($archiveName in $archiveNames) {
  $archivePath = Join-Path $out $archiveName
  if (Test-Path -LiteralPath $archivePath) {
    $currentHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    "$currentHash  $archiveName"
  }
}
($manifestLines -join "`n") + "`n" | Set-Content -NoNewline -Encoding ascii (Join-Path $out 'SHA256SUMS')
Write-Output "Built $dest"
Write-Output "Archived $archive"
