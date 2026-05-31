#!/usr/bin/env pwsh
#
# Repeatedly run scripts/fuzz-parallel.ps1 with ``-FuzzTime`` per iteration until
# either one iteration passes cleanly or ``-MaxIterations`` is reached.
#
# Use this to soak-test fixes: run the loop with -FuzzTime 10m and -MaxIterations
# large enough to be confident; the loop exits as soon as a clean pass happens.
#
# Each iteration uses its own timestamped tests/fuzz/results/run-<stamp>/
# directory so historical runs are preserved for postmortem.

[CmdletBinding()]
param(
    [string]$FuzzTime = "10m",
    [int]$MaxIterations = 8,
    [int]$WorkersPerTarget = 3
)

$ErrorActionPreference = "Stop"
$scriptDir = $PSScriptRoot
$repoRoot  = Split-Path -LiteralPath $scriptDir
Set-Location -LiteralPath $repoRoot

$loopStart = Get-Date
Write-Host "fuzz-loop: target = clean $FuzzTime pass, max=$MaxIterations iters, workers=$WorkersPerTarget/target"
Write-Host "started: $($loopStart.ToString('o'))"

$iter = 0
while ($iter -lt $MaxIterations) {
    $iter++
    Write-Host ""
    Write-Host "============================================================"
    Write-Host "ITERATION $iter / $MaxIterations  (elapsed loop: $([math]::Round(((Get-Date)-$loopStart).TotalMinutes,1)) min)"
    Write-Host "============================================================"

    & pwsh -NoProfile -NonInteractive -File (Join-Path $scriptDir "fuzz-parallel.ps1") `
        -FuzzTime $FuzzTime `
        -WorkersPerTarget $WorkersPerTarget

    $code = $LASTEXITCODE
    if ($code -eq 0) {
        Write-Host ""
        Write-Host "ITERATION $iter CLEAN. Loop done (total time: $([math]::Round(((Get-Date)-$loopStart).TotalMinutes,1)) min)."
        exit 0
    }
    Write-Host ""
    Write-Host "ITERATION $iter FAILED (exit=$code). Continuing to iteration $($iter+1)."
}

Write-Host ""
Write-Host "fuzz-loop: reached MaxIterations=$MaxIterations without a clean pass."
exit 1
