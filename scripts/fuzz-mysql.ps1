#!/usr/bin/env pwsh
#
# Run every native Go fuzz target that exercises a MySQL-specific code path.
#
# Each ``FuzzXxx`` target is invoked for ``$FuzzTime`` seconds (default 10s).
# Crashes are auto-saved as new seed-corpus entries under
# ``<pkg>/testdata/fuzz/<FuzzName>/`` by the ``go test -fuzz`` framework
# itself; the runner exits non-zero if any target produces a crash.
#
# Usage:
#   pwsh scripts/fuzz-mysql.ps1                 # default 10s per target
#   pwsh scripts/fuzz-mysql.ps1 -FuzzTime 60s   # longer soak
#   pwsh scripts/fuzz-mysql.ps1 -FuzzTime 5m    # heavy fuzz before release
#
# Output is tee'd to ``tests/fuzz/results/fuzz-summary.log``.

[CmdletBinding()]
param(
    [string]$FuzzTime = "10s",
    [string]$ResultsDir = "tests/fuzz/results"
)

$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

# Discover every fuzz target across the MySQL-touching packages.
# A target is any "func FuzzX(f *testing.F)" in a _test.go file.
# We scope to the four packages whose MySQL paths we want covered.
$packages = @(
    "./db/mysql/...",
    "./user/...",
    "./message/...",
    "./webpush/..."
)

$repoRoot = Split-Path -LiteralPath $PSScriptRoot
Set-Location $repoRoot

if (-not (Test-Path $ResultsDir)) {
    New-Item -ItemType Directory -Force -Path $ResultsDir | Out-Null
}
$summary = Join-Path $ResultsDir "fuzz-summary.log"
Remove-Item -Force $summary -ErrorAction SilentlyContinue

function Write-Tee {
    param([string]$Msg)
    Write-Host $Msg
    Add-Content -Path $summary -Value $Msg
}

Write-Tee "fuzz-mysql.ps1 start: $(Get-Date -Format 'o')"
Write-Tee "FuzzTime per target: $FuzzTime"
Write-Tee ""

# Discover targets via `go test -list 'Fuzz.*'` per package.
$allTargets = @()
foreach ($pkg in $packages) {
    Write-Host "[discover] $pkg"
    $listed = & go test -list "Fuzz.*" $pkg 2>$null
    if ($LASTEXITCODE -ne 0) {
        Write-Tee "[warn] go test -list failed for $pkg (no test files?)"
        continue
    }
    $pkgPath = $null
    foreach ($line in $listed) {
        if ($line -match "^Fuzz[A-Za-z0-9_]+$") {
            if (-not $pkgPath) {
                # The package path is printed by `go test` as part of the result line ("ok <pkg>...");
                # but on success it doesn't always appear before the fuzz list. Re-resolve via go list.
                $pkgPath = (& go list $pkg 2>$null | Select-Object -First 1)
            }
            $allTargets += [PSCustomObject]@{
                Package = $pkg
                Name    = $line.Trim()
            }
        }
    }
}

if ($allTargets.Count -eq 0) {
    Write-Tee "[fatal] no FuzzXxx targets discovered in: $($packages -join ', ')"
    exit 2
}

Write-Tee "Discovered $($allTargets.Count) fuzz targets:"
foreach ($t in $allTargets) {
    Write-Tee "  - $($t.Package) :: $($t.Name)"
}
Write-Tee ""

$failures = @()
foreach ($t in $allTargets) {
    $hdr = "=== [$($t.Package)] $($t.Name) (fuzztime=$FuzzTime) ==="
    Write-Tee $hdr
    $logFile = Join-Path $ResultsDir ("fuzz-{0}.log" -f $t.Name)

    # -run '^$' disables the regular (non-fuzz) test selection,
    # so only the fuzz target runs.
    & go test -run "^$" -fuzz ("^" + $t.Name + "$") -fuzztime $FuzzTime $t.Package *>&1 |
        Tee-Object -FilePath $logFile

    $exit = $LASTEXITCODE
    if ($exit -ne 0) {
        Write-Tee "[fail] $($t.Name) exit=$exit (log: $logFile)"
        $failures += $t
    } else {
        Write-Tee "[pass] $($t.Name)"
    }
    Write-Tee ""
}

Write-Tee ""
Write-Tee "=== Summary ==="
Write-Tee "Total targets: $($allTargets.Count)"
Write-Tee "Passed:        $($allTargets.Count - $failures.Count)"
Write-Tee "Failed:        $($failures.Count)"
if ($failures.Count -gt 0) {
    Write-Tee ""
    Write-Tee "Failed targets (check the per-target log under $ResultsDir):"
    foreach ($f in $failures) {
        Write-Tee "  - $($f.Package) :: $($f.Name)"
    }
    Write-Tee ""
    Write-Tee "Inspect crashes under each package's testdata/fuzz/<TargetName>/ directory."
    Write-Tee "Each file there is a seed corpus entry that reproduces the crash."
    exit 1
}

Write-Tee ""
Write-Tee "All fuzz targets clean for $FuzzTime each."
exit 0
