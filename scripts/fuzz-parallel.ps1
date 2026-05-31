#!/usr/bin/env pwsh
#
# Run every MySQL-touching Go fuzz target in PARALLEL for ``-FuzzTime`` duration.
#
# Each target gets its own ``go test -fuzz`` process and its own log file so
# concurrent processes never share a writer:
#
#   * Per-target stdout+stderr -> tests/fuzz/results/run-<stamp>/<FuzzName>.log
#   * Roll-up plain-text       -> tests/fuzz/results/run-<stamp>/summary.log
#   * Roll-up JSON             -> tests/fuzz/results/run-<stamp>/summary.json
#   * "Latest run" pointer     -> tests/fuzz/results/latest.txt (path of last run)
#
# Crash-found inputs are persisted by ``go test -fuzz`` itself under each
# package's ``testdata/fuzz/<FuzzName>/`` directory; since every target has a
# unique directory, parallel processes never collide on corpus writes.
#
# Total parallel worker count is capped via ``GOMAXPROCS`` per child process
# so 11 simultaneous targets x N workers stays close to ``$env:NUMBER_OF_PROCESSORS``.
#
# Usage:
#   pwsh scripts/fuzz-parallel.ps1                              # 10m default
#   pwsh scripts/fuzz-parallel.ps1 -FuzzTime 1h                 # 1h soak
#   pwsh scripts/fuzz-parallel.ps1 -FuzzTime 6h -WorkersPerTarget 3
#
# Exit codes:
#   0  all targets passed
#   1  at least one target reported a crash or non-zero exit

[CmdletBinding()]
param(
    [string]$FuzzTime = "10m",
    [int]$WorkersPerTarget = 3,
    [string]$RunDir = "",
    [switch]$Quiet
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -LiteralPath $PSScriptRoot
Set-Location -LiteralPath $repoRoot

if ([string]::IsNullOrEmpty($RunDir)) {
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $RunDir = Join-Path "tests/fuzz/results" "run-$stamp"
}
New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
$RunDirAbs = (Resolve-Path -LiteralPath $RunDir).Path

# (Pkg, Name) tuples discovered manually -- kept static so adding a new fuzz
# target requires an intentional edit here (rather than silent inclusion when
# someone drops a new FuzzXxx into an unrelated _test.go).
$targets = @(
    @{Pkg="./db/mysql/..."; Name="FuzzOpen_URL"},
    @{Pkg="./db/mysql/..."; Name="FuzzBuildDriverDSN"},
    @{Pkg="./db/mysql/..."; Name="FuzzParseMySQLVersion"},
    @{Pkg="./db/mysql/..."; Name="FuzzSplitHostPort"},
    @{Pkg="./db/mysql/..."; Name="FuzzExtractIntParam"},
    @{Pkg="./db/mysql/..."; Name="FuzzExtractDurationParam"},
    @{Pkg="./db/mysql/..."; Name="FuzzCensorPassword"},
    @{Pkg="./db/mysql/..."; Name="FuzzRegisterTLSConfig_PEM"},
    @{Pkg="./user/...";     Name="FuzzIsUniqueConstraintError"},
    @{Pkg="./message/...";  Name="FuzzActionsJSONRoundTrip"},
    @{Pkg="./message/...";  Name="FuzzMessageHeaderFieldsRoundTrip"}
)

$runMeta = [ordered]@{
    started          = (Get-Date).ToString('o')
    fuzztime         = $FuzzTime
    workers_target   = $WorkersPerTarget
    targets_count    = $targets.Count
    run_dir          = $RunDirAbs
    repo_root        = $repoRoot
    cpu_count        = [int]$env:NUMBER_OF_PROCESSORS
    total_workers    = $targets.Count * $WorkersPerTarget
}
$runMeta | ConvertTo-Json -Depth 3 | Set-Content -Path (Join-Path $RunDirAbs "run.json")

if (-not $Quiet) {
    Write-Host "fuzz-parallel: $($targets.Count) targets, fuzztime=$FuzzTime, GOMAXPROCS=$WorkersPerTarget/target (total workers ~ $($runMeta.total_workers) / $($runMeta.cpu_count) cores)"
    Write-Host "results dir : $RunDirAbs"
    Write-Host "started     : $($runMeta.started)"
    Write-Host ""
}

$started = @()
foreach ($t in $targets) {
    $logFile = Join-Path $RunDirAbs "$($t.Name).log"

    # Each child pwsh enters the repo root, caps its own GOMAXPROCS, runs the
    # one fuzz target, tees output into the per-target log, and exits with the
    # go test exit code so the parent can collect it.
    $childCmd = @"
Set-Location -LiteralPath '$repoRoot'
`$env:GOMAXPROCS = '$WorkersPerTarget'
& go test -run '^`$' -fuzz '^$($t.Name)`$' -fuzztime '$FuzzTime' '$($t.Pkg)' *>&1 |
    Tee-Object -FilePath '$logFile'
exit `$LASTEXITCODE
"@

    $p = Start-Process -FilePath "pwsh" `
        -ArgumentList @("-NoProfile","-NonInteractive","-Command",$childCmd) `
        -NoNewWindow -PassThru

    $started += [PSCustomObject]@{
        Target    = $t
        Process   = $p
        LogFile   = $logFile
        StartTime = Get-Date
    }
    if (-not $Quiet) { Write-Host ("  [start] PID={0,-6} {1}" -f $p.Id, $t.Name) }
    Start-Sleep -Milliseconds 150
}

if (-not $Quiet) {
    Write-Host ""
    Write-Host "All $($targets.Count) targets launched; waiting for completion (this will take >= $FuzzTime)..."
}

foreach ($e in $started) {
    $e.Process.WaitForExit()
    $e | Add-Member -NotePropertyName ExitCode -NotePropertyValue $e.Process.ExitCode -Force
    $e | Add-Member -NotePropertyName EndTime  -NotePropertyValue (Get-Date)            -Force
}
$endStamp = (Get-Date).ToString('o')

$results = @()
$anyFailed = $false
foreach ($e in $started) {
    $duration = ($e.EndTime - $e.StartTime).TotalSeconds
    $tail = if (Test-Path -LiteralPath $e.LogFile) { Get-Content -LiteralPath $e.LogFile -Tail 60 } else { @() }

    $execs = 0
    $corpusTotal = 0
    $execLines = $tail | Select-String -Pattern 'execs:\s+(\d+)' -AllMatches
    if ($execLines) {
        $last = $execLines | Select-Object -Last 1
        $execs = [int64]$last.Matches[0].Groups[1].Value
    }
    $covLines = $tail | Select-String -Pattern 'new interesting:\s+(\d+)\s+\(total:\s+(\d+)\)' -AllMatches
    if ($covLines) {
        $last = $covLines | Select-Object -Last 1
        $corpusTotal = [int]$last.Matches[0].Groups[2].Value
    }
    $crashLine = ($tail | Select-String -Pattern 'failure while testing seed corpus entry|got a panic|--- FAIL:' | Select-Object -Last 1)
    $crashSummary = if ($crashLine) { $crashLine.Line.Trim() } else { "" }

    $status = if ($e.ExitCode -eq 0) { "PASS" } else { "FAIL" }
    if ($e.ExitCode -ne 0) { $anyFailed = $true }

    $results += [ordered]@{
        target           = $e.Target.Name
        package          = $e.Target.Pkg
        status           = $status
        exit_code        = $e.ExitCode
        duration_seconds = [math]::Round($duration, 1)
        execs            = $execs
        corpus_total     = $corpusTotal
        crash_summary    = $crashSummary
        log              = $e.LogFile
    }
}

$summaryLines = @()
$summaryLines += "fuzz-parallel summary"
$summaryLines += "  started:    $($runMeta.started)"
$summaryLines += "  finished:   $endStamp"
$summaryLines += "  fuzztime:   $FuzzTime"
$summaryLines += "  workers:    $WorkersPerTarget per target ($($runMeta.total_workers) total / $($runMeta.cpu_count) cores)"
$summaryLines += "  run_dir:    $RunDirAbs"
$summaryLines += ""
$summaryLines += ("{0,-40} {1,-6} {2,12} {3,10} {4,10}" -f "TARGET","STATUS","EXECS","CORPUS","SEC")
$summaryLines += ("-" * 88)
foreach ($r in $results) {
    $summaryLines += ("{0,-40} {1,-6} {2,12} {3,10} {4,10}" -f $r.target, $r.status, $r.execs, $r.corpus_total, $r.duration_seconds)
    if ($r.crash_summary) {
        $summaryLines += ("    -> " + $r.crash_summary)
    }
}
$summaryLines += ""
$pass = @($results | Where-Object { $_.status -eq 'PASS' }).Count
$fail = @($results | Where-Object { $_.status -eq 'FAIL' }).Count
$summaryLines += "Passed: $pass / $($results.Count)"
$summaryLines += "Failed: $fail / $($results.Count)"

$summaryLines | Set-Content -Path (Join-Path $RunDirAbs "summary.log")
$results | ConvertTo-Json -Depth 4 | Set-Content -Path (Join-Path $RunDirAbs "summary.json")

# Persistent "latest run" pointer so other tooling can find the most recent run
# without re-discovering the timestamp.
$latestFile = Join-Path $repoRoot "tests/fuzz/results/latest.txt"
Set-Content -Path $latestFile -Value $RunDirAbs

if (-not $Quiet) {
    Write-Host ""
    foreach ($l in $summaryLines) { Write-Host $l }
}

if ($anyFailed) { exit 1 }
exit 0
