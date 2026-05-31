#!/usr/bin/env pwsh
#
# Orchestration script for the MySQL integration test suite.
#
# Brings up the podman-compose stack, waits for both services to be
# healthy, installs the pytest dependencies into a local virtualenv,
# runs the suite with JUnit XML output, then tears everything down.
#
# Usage:
#   pwsh tests/integration/mysql/run.ps1                  # full cycle, tear down at end
#   pwsh tests/integration/mysql/run.ps1 -NoTeardown      # leave stack up for debugging
#   pwsh tests/integration/mysql/run.ps1 -SkipBuild       # reuse existing ntfy image
#   pwsh tests/integration/mysql/run.ps1 -Rebuild         # force rebuild of ntfy image

[CmdletBinding()]
param(
    [switch]$NoTeardown,
    [switch]$SkipBuild,
    [switch]$Rebuild
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

# Force UTF-8 for any non-ASCII output from MySQL / ntfy / pytest
$env:PYTHONIOENCODING = "utf-8"
$env:PYTHONUTF8 = "1"

$here = Split-Path -LiteralPath $PSCommandPath
$composeFile = Join-Path $here "podman-compose.yml"
$resultsDir = Join-Path $here "results"
$venvDir = Join-Path $here ".venv"
$junitPath = Join-Path $resultsDir "junit.xml"

# Locate compose driver. We prefer `podman compose` but treat the
# whole invocation as a single string to avoid PowerShell parameter
# parsing eating short flags like `-d`.
$composeExe = $null
$composePrefix = @()
if (Get-Command podman -ErrorAction SilentlyContinue) {
    $composeExe = (Get-Command podman).Source
    $composePrefix = @("compose")
} elseif (Get-Command docker -ErrorAction SilentlyContinue) {
    $composeExe = (Get-Command docker).Source
    $composePrefix = @("compose")
} elseif (Get-Command docker-compose -ErrorAction SilentlyContinue) {
    $composeExe = (Get-Command docker-compose).Source
    $composePrefix = @()
} else {
    throw "no podman / docker / docker-compose found on PATH"
}
Write-Host "[run] using compose driver: $composeExe $($composePrefix -join ' ')"

# Run a compose subcommand and stream output to the console.
# We deliberately do NOT wrap this in a function with named params,
# because short flags like `-d` would be intercepted by PowerShell's
# parameter binder.
function Run-Compose {
    [CmdletBinding()]
    param([string[]]$Argv)
    $all = $composePrefix + @("-f", $composeFile) + $Argv
    Write-Host "[run] + $composeExe $($all -join ' ')"
    & $composeExe @all
    return $LASTEXITCODE
}

# Capture compose stdout (no live streaming) — used by `ps --format json`.
function Get-ComposeOutput {
    [CmdletBinding()]
    param([string[]]$Argv)
    $all = $composePrefix + @("-f", $composeFile) + $Argv
    $out = & $composeExe @all 2>$null
    return $out
}

function Wait-ContainerHealthy {
    param([string]$ServiceName, [int]$TimeoutSeconds = 180)
    Write-Host "[run] waiting for service '$ServiceName' to become healthy (timeout ${TimeoutSeconds}s)..."
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $raw = Get-ComposeOutput -Argv @("ps", "--format", "json")
        if ($raw) {
            # docker-compose emits one JSON object per service per line
            $entries = @()
            foreach ($line in ($raw -split "`n")) {
                $trim = $line.Trim()
                if (-not $trim) { continue }
                if ($trim[0] -ne '{') { continue }
                try { $entries += ($trim | ConvertFrom-Json) } catch { }
            }
            $svc = $entries | Where-Object { $_.Service -eq $ServiceName } | Select-Object -First 1
            if ($svc) {
                $state = $svc.State
                $health = $svc.Health
                if ($health -eq "healthy") {
                    Write-Host "[run] '$ServiceName' is healthy."
                    return
                }
                if ($state -eq "running" -and ([string]::IsNullOrEmpty($health))) {
                    Write-Host "[run] '$ServiceName' is running (no healthcheck reported)."
                    return
                }
                Write-Host "[run]   '$ServiceName' state=$state health=$health"
            } else {
                Write-Host "[run]   '$ServiceName' not yet present in compose ps"
            }
        }
        Start-Sleep -Seconds 2
    }
    throw "service '$ServiceName' never became healthy within $TimeoutSeconds s"
}

New-Item -ItemType Directory -Force -Path $resultsDir | Out-Null

$testExit = 1
try {
    if ($Rebuild) {
        Write-Host "[run] removing prior ntfy image to force rebuild..."
        Run-Compose -Argv @("down", "-v") | Out-Null
        & $composeExe @($composePrefix + @("image", "rm", "ntfy-mysql-int:local")) 2>&1 | Out-Null
    }

    Write-Host "[run] bringing stack up (detached)..."
    if ($SkipBuild) {
        $rc = Run-Compose -Argv @("up", "--detach")
    } else {
        $rc = Run-Compose -Argv @("up", "--detach", "--build")
    }
    if ($rc -ne 0) { throw "compose up failed (exit $rc)" }

    Wait-ContainerHealthy -ServiceName "mysql" -TimeoutSeconds 180
    Wait-ContainerHealthy -ServiceName "ntfy"  -TimeoutSeconds 180

    # Provision the Python venv
    if (-not (Test-Path $venvDir)) {
        Write-Host "[run] creating Python venv at $venvDir..."
        python -m venv $venvDir
        if ($LASTEXITCODE -ne 0) { throw "python -m venv failed" }
    }
    $venvPython = Join-Path $venvDir "Scripts\python.exe"
    if (-not (Test-Path $venvPython)) {
        # Unix layout (PowerShell on Linux/macOS)
        $venvPython = Join-Path $venvDir "bin/python"
    }

    Write-Host "[run] installing test deps..."
    & $venvPython -m pip install --quiet --upgrade pip
    & $venvPython -m pip install --quiet -r (Join-Path $here "requirements.txt")
    if ($LASTEXITCODE -ne 0) { throw "pip install failed" }

    Write-Host "[run] running pytest..."
    $env:NTFY_INT_URL = "http://127.0.0.1:8888"
    $env:NTFY_INT_COMPOSE_DIR = $here
    Push-Location $here
    try {
        & $venvPython -m pytest --junitxml="$junitPath"
        $testExit = $LASTEXITCODE
    } finally {
        Pop-Location
    }

    Write-Host "[run] pytest exit code: $testExit"
    Write-Host "[run] junit report: $junitPath"
}
finally {
    if (-not $NoTeardown) {
        Write-Host "[run] tearing stack down..."
        Run-Compose -Argv @("down", "-v") | Out-Null
    } else {
        Write-Host "[run] -NoTeardown: leaving stack up for inspection."
        Write-Host "[run]   tear down with: $composeExe $($composePrefix -join ' ') -f `"$composeFile`" down -v"
    }
}

exit $testExit
