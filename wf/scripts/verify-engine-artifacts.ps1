param(
  [string]$OutputDir = "$PSScriptRoot\..\artifacts"
)

$ErrorActionPreference = 'Stop'
$out = (Resolve-Path $OutputDir).Path
$manifestPath = Join-Path $out 'SHA256SUMS'
$expectedNames = @('windows-amd64.zip')
$lines = @(Get-Content -LiteralPath $manifestPath | Where-Object { $_.Trim() })

if ($lines.Count -ne $expectedNames.Count) {
  throw "root SHA256SUMS must contain exactly $($expectedNames.Count) entries; found $($lines.Count)"
}

$seen = @{}
for ($index = 0; $index -lt $expectedNames.Count; $index++) {
  if ($lines[$index] -notmatch '^([0-9a-f]{64})  (.+)$') {
    throw "invalid checksum line: $($lines[$index])"
  }
  $expectedName = $expectedNames[$index]
  $actualName = $Matches[2]
  if ($actualName -ne $expectedName) {
    throw "checksum order mismatch at index ${index}: got $actualName, want $expectedName"
  }
  if ($seen.ContainsKey($actualName)) {
    throw "duplicate checksum entry: $actualName"
  }
  $seen[$actualName] = $true
  $archivePath = Join-Path $out $actualName
  if (-not (Test-Path -LiteralPath $archivePath -PathType Leaf)) {
    throw "archive missing: $archivePath"
  }
  $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
  if ($actualHash -ne $Matches[1]) {
    throw "checksum mismatch for ${actualName}: got $actualHash, want $($Matches[1])"
  }
}

Write-Output "Verified root SHA256SUMS: $($expectedNames -join ', ')"
