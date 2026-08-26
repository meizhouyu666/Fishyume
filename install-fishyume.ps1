[CmdletBinding()]
param(
  [string]$Prefix,
  [string]$Proxy,
  [switch]$SkipDependencyInstall
)

$ErrorActionPreference = 'Stop'
$repoRoot = $PSScriptRoot
$wfRoot = Join-Path $repoRoot 'wf'
$engineRoot = Join-Path $repoRoot 'wf-engine'
$temporary = Join-Path ([IO.Path]::GetTempPath()) ("fishyume-preview-{0}" -f [guid]::NewGuid().ToString('N'))
$packs = Join-Path $temporary 'packs'
$stagedPlatform = Join-Path $temporary 'platform-package'
$previousProxy = $env:npm_config_proxy
$previousHttpsProxy = $env:npm_config_https_proxy

function Assert-NativeSuccess([string]$Label) {
  if ($LASTEXITCODE -ne 0) { throw "$Label failed with exit code $LASTEXITCODE" }
}

function Invoke-Pack([string]$Directory) {
  Push-Location $Directory
  try {
    $json = & npm pack --json --pack-destination $packs | Out-String
    Assert-NativeSuccess 'npm pack'
    $report = $json | ConvertFrom-Json
    if (-not $report.filename) { throw "npm pack returned no filename for $Directory" }
    return Join-Path $packs $report.filename
  } finally { Pop-Location }
}

function Stop-IdleControlPlaneForUpgrade {
  $stateRoot = if ($env:FISHYUME_STATE_DIR) { [IO.Path]::GetFullPath($env:FISHYUME_STATE_DIR) } else { [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'fishyume')) }
  $metadataPath = Join-Path $stateRoot 'control-plane.json'
  if (-not (Test-Path -LiteralPath $metadataPath -PathType Leaf)) { return }
  $owner = Get-Content -Raw -LiteralPath $metadataPath | ConvertFrom-Json
  if (-not $owner.pid -or [IO.Path]::GetFullPath([string]$owner.stateDir) -ne $stateRoot) { throw 'refusing to stop an unverified Fishyume Control Plane owner' }
  $running = Get-CimInstance Win32_Process -Filter "ProcessId = $($owner.pid)" -ErrorAction SilentlyContinue
  if (-not $running) { return }
  if ((Split-Path $running.ExecutablePath -Leaf) -ne 'fishyume-engine.exe' -or [string]$running.CommandLine -notmatch '\bserve\b') { throw "PID $($owner.pid) is not a verified Fishyume Control Plane" }

  $currentGlobalRoot = (& npm root --global | Out-String).Trim()
  Assert-NativeSuccess 'npm global root lookup'
  $currentCli = Join-Path $currentGlobalRoot 'fishyume\dist\cli.js'
  if (-not (Test-Path -LiteralPath $currentCli -PathType Leaf)) { throw 'a Fishyume Control Plane is running but the current global CLI cannot be inspected' }
  foreach ($phase in @('created', 'running', 'waiting', 'paused', 'cancelling')) {
    # Windows PowerShell strips unescaped quotes when forwarding native arguments.
    $params = '{\"filter\":{\"phase\":\"' + $phase + '\"},\"limit\":1}'
    $activity = & node $currentCli machine run.list --params $params | Out-String
    Assert-NativeSuccess 'running Fishyume activity check'
    $response = $activity | ConvertFrom-Json
    if ($response.apiVersion -ne 'fishyume.application/v1' -or $null -eq $response.items) {
      throw 'could not verify whether the running Fishyume Control Plane has active Runs'
    }
    if (@($response.items).Count -gt 0) {
      throw "Fishyume has an active $phase Run; wait or cancel it before upgrading"
    }
  }

  Stop-Process -Id ([int]$owner.pid)
  Wait-Process -Id ([int]$owner.pid) -Timeout 10 -ErrorAction SilentlyContinue
  if (Get-Process -Id ([int]$owner.pid) -ErrorAction SilentlyContinue) { throw 'idle Fishyume Control Plane did not stop for upgrade' }
  Write-Output 'Stopped idle Fishyume Control Plane for safe Engine upgrade.'
}

try {
  if ($Proxy) {
    $env:npm_config_proxy = $Proxy
    $env:npm_config_https_proxy = $Proxy
  }
  New-Item -ItemType Directory -Force -Path $packs | Out-Null

  $nodeMajor = [int](& node -p "Number(process.versions.node.split('.')[0])")
  Assert-NativeSuccess 'Node.js version check'
  if ($nodeMajor -lt 24) { throw "Fishyume requires Node.js 24 or newer; found major version $nodeMajor" }
  $goVersion = & go version | Out-String
  Assert-NativeSuccess 'Go version check'
  if ($goVersion -notmatch 'go1\.(2[6-9]|[3-9][0-9])(?:\.|\s)') { throw "Fishyume preview source install requires Go 1.26 or newer; found $($goVersion.Trim())" }

  if (-not $SkipDependencyInstall) {
    & npm --prefix $wfRoot ci --ignore-scripts
    Assert-NativeSuccess 'npm ci'
  }
  & npm --prefix $wfRoot run build
  Assert-NativeSuccess 'Fishyume CLI build'

  $runningOnWindows = $env:OS -eq 'Windows_NT'
  $platformName = if ($runningOnWindows) { 'fishyume-engine-win32-x64' } else { 'fishyume-engine-linux-x64' }
  $goos = if ($runningOnWindows) { 'windows' } else { 'linux' }
  $binary = if ($runningOnWindows) { 'fishyume-engine.exe' } else { 'fishyume-engine' }
  $platformSource = Join-Path $wfRoot "packages\$platformName"
  Copy-Item -LiteralPath $platformSource -Destination $stagedPlatform -Recurse

  $previousGoos = $env:GOOS
  $previousGoarch = $env:GOARCH
  $previousCgo = $env:CGO_ENABLED
  try {
    $env:GOOS = $goos
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    Push-Location $engineRoot
    try {
      & go build -trimpath '-ldflags=-s -w' -o (Join-Path $stagedPlatform "bin\$binary") ./cmd/wf-engine
      Assert-NativeSuccess 'Fishyume Engine build'
    } finally { Pop-Location }
  } finally {
    $env:GOOS = $previousGoos
    $env:GOARCH = $previousGoarch
    $env:CGO_ENABLED = $previousCgo
  }

  $cliTarball = Invoke-Pack $wfRoot
  $engineTarball = Invoke-Pack $stagedPlatform
  if ($Prefix) {
    $resolvedPrefix = [IO.Path]::GetFullPath($Prefix)
    & npm install --prefix $resolvedPrefix $cliTarball $engineTarball --ignore-scripts --no-audit --no-fund
    Assert-NativeSuccess 'Fishyume prefix install'
    $cli = Join-Path $resolvedPrefix 'node_modules\fishyume\dist\cli.js'
  } else {
    Stop-IdleControlPlaneForUpgrade
    & npm install --global $cliTarball $engineTarball --ignore-scripts --no-audit --no-fund
    Assert-NativeSuccess 'Fishyume global install'
    $globalRoot = (& npm root --global | Out-String).Trim()
    Assert-NativeSuccess 'npm global root lookup'
    $cli = Join-Path $globalRoot 'fishyume\dist\cli.js'
  }
  if (-not (Test-Path -LiteralPath $cli -PathType Leaf)) { throw "installed Fishyume CLI is missing: $cli" }

  $help = & node $cli --help | Out-String
  Assert-NativeSuccess 'installed Fishyume help'
  if ($help -notmatch 'dashboard') { throw 'installed Fishyume help does not expose Dashboard' }
  $setup = (& node $cli setup codex --print | Out-String).Trim()
  Assert-NativeSuccess 'installed Fishyume Codex setup check'
  if ($setup -notmatch '^codex mcp add fishyume -- ".+node(?:\.exe)?" ".+[\\/]fishyume[\\/]dist[\\/]cli\.js" "mcp"$') { throw 'installed Fishyume Codex setup command is not canonical and copyable' }

  Write-Output 'Fishyume Developer Preview installed successfully.'
  if ($Prefix) { Write-Output "Prefix: $resolvedPrefix" }
  Write-Output 'Next: fishyume setup codex'
  Write-Output 'Then: fishyume doctor --project "E:\path\to\project"'
  Write-Output 'Open Dashboard: fishyume'
} finally {
  $env:npm_config_proxy = $previousProxy
  $env:npm_config_https_proxy = $previousHttpsProxy
  $resolvedTemporary = [IO.Path]::GetFullPath($temporary)
  $expectedRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
  if ($resolvedTemporary.StartsWith($expectedRoot, [StringComparison]::OrdinalIgnoreCase) -and (Split-Path $resolvedTemporary -Leaf) -like 'fishyume-preview-*') {
    Remove-Item -LiteralPath $resolvedTemporary -Recurse -Force -ErrorAction SilentlyContinue
  } else {
    throw "refusing to clean unexpected temporary path $resolvedTemporary"
  }
}
