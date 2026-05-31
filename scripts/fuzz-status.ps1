#!/usr/bin/env pwsh
#
# Summarize the latest (or a specified) fuzz-parallel.ps1 run.
#
# By default, follows tests/fuzz/results/latest.txt to find the most recent run
# directory. Pass -RunDir to inspect a specific older run.
#
# Reports, per target:
#   * Current elapsed time and execs count (from the last "fuzz: elapsed:" line)
#   * Whether the underlying go test process is still running (PID check)
#   * Any FAIL / panic lines already present in the per-target log
#
# Useful while a long fuzz soak is in flight.

[CmdletBinding()]
param(
    [string]$RunDir = ""
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -LiteralPath $PSScriptRoot
Set-Location -LiteralPath $repoRoot

if ([string]::IsNullOrEmpty($RunDir)) {
    $latestFile = Join-Path $repoRoot "tests/fuzz/results/latest.txt"
    if (-not (Test-Path -LiteralPath $latestFile)) {
        Write-Error "no tests/fuzz/results/latest.txt -- specify -RunDir explicitly"
        exit 2
    }
    $RunDir = (Get-Content -LiteralPath $latestFile -Raw).Trim()
}

if (-not (Test-Path -LiteralPath $RunDir)) {
    Write-Error "run dir does not exist: $RunDir"
    exit 2
}

Write-Host "Run dir: $RunDir"

$runJsonPath = Join-Path $RunDir "run.json"
if (Test-Path -LiteralPath $runJsonPath) {
    $runMeta = Get-Content -LiteralPath $runJsonPath -Raw | ConvertFrom-Json
    Write-Host "  started:        $($runMeta.started)"
    Write-Host "  fuzztime/target: $($runMeta.fuzztime)"
    Write-Host "  workers/target:  $($runMeta.workers_target)"
    Write-Host "  total workers:   $($runMeta.total_workers) / $($runMeta.cpu_count) cores"
}

$logs = Get-ChildItem -LiteralPath $RunDir -Filter "Fuzz*.log" | Sort-Object Name
if (-not $logs) {
    Write-Host "(no Fuzz*.log files yet)"
    exit 0
}

# Build header and per-target rows
Write-Host ""
Write-Host ("{0,-40} {1,-8} {2,12} {3,12} {4,10} {5,10}" -f "TARGET","STATE","EXECS","RATE/SEC","CORPUS","ELAPSED")
Write-Host ("-" * 100)

$anyFail = $false
$anyRunning = $false
$totalExecs = [int64]0

foreach ($log in $logs) {
    $tail = Get-Content -LiteralPath $log.FullName -Tail 80
    $tailJoined = [string]::Join("`n", $tail)

    $execs = 0
    $rate = 0
    $elapsed = "?"
    $corpus = 0

    $progressLines = $tail | Select-String -Pattern 'fuzz:\s+elapsed:\s+(\S+),\s+execs:\s+(\d+)\s+\((\d+)/sec\),\s+new\s+interesting:\s+\d+\s+\(total:\s+(\d+)\)' -AllMatches
    if ($progressLines) {
        $last = $progressLines | Select-Object -Last 1
        $elapsed = $last.Matches[0].Groups[1].Value
        $execs   = [int64]$last.Matches[0].Groups[2].Value
        $rate    = [int]$last.Matches[0].Groups[3].Value
        $corpus  = [int]$last.Matches[0].Groups[4].Value
    }
    $totalExecs += $execs

    # State: did the underlying `go test` (and child fuzz workers) exit? The log
    # ends with "PASS" / "FAIL" / "exit status N" once the process is done.
    $state = "RUN"
    if ($tailJoined -match '--- FAIL:' -or $tailJoined -match '^FAIL') {
        $state = "FAIL"
        $anyFail = $true
    } elseif ($tailJoined -match '^PASS' -or $tailJoined -match 'ok\s+\S+\s+\d+\.\d+s') {
        $state = "PASS"
    } else {
        $anyRunning = $true
    }

    Write-Host ("{0,-40} {1,-8} {2,12} {3,12} {4,10} {5,10}" -f $log.BaseName, $state, $execs, $rate, $corpus, $elapsed)

    # Surface any failure lines inline
    if ($state -eq "FAIL") {
        $failLines = $tail | Select-String -Pattern '--- FAIL:|mysql_fuzz_test\.go:|manager_fuzz_test\.go:|cache_fuzz_test\.go:|Failing input written to|To re-run:' | Select-Object -First 5
        foreach ($f in $failLines) {
            Write-Host "    -> $($f.Line.Trim())"
        }
    }
}

Write-Host ""
Write-Host ("Aggregate execs across all targets: {0:N0}" -f $totalExecs)
if ($anyFail) {
    Write-Host "STATUS: at least one target reported a FAILURE -- see lines above" -ForegroundColor Red
} elseif ($anyRunning) {
    Write-Host "STATUS: still running (no crashes so far)"
} else {
    Write-Host "STATUS: all targets completed successfully"
}
