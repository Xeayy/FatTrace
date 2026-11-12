# Build release binaries for Windows amd64/arm64 and write SHA256SUMS.txt.
# Usage: powershell -ExecutionPolicy Bypass -File scripts\release.ps1 -Version 0.1.0
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[0-9A-Za-z._-]+$')]
    [string]$Version,
    [switch]$SkipInstaller
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force $dist | Out-Null

$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$artifacts = @()

foreach ($arch in @("amd64", "arm64")) {
    $env:GOARCH = $arch
    $name = "fat-$Version-windows-$arch.exe"
    $out = Join-Path $dist $name
    Write-Host "building $name"
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out ./cmd/fat
    if ($LASTEXITCODE -ne 0) { throw "go build failed for windows/$arch" }
    $artifacts += $name

    $guiName = "fatgui-$Version-windows-$arch.exe"
    $guiOut = Join-Path $dist $guiName
    Write-Host "building $guiName"
    go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=$Version" -o $guiOut ./cmd/fatgui
    if ($LASTEXITCODE -ne 0) { throw "go build failed for windows/$arch (fatgui)" }
    $artifacts += $guiName
}

# Optional installer build via Inno Setup
if (-not $SkipInstaller) {
    $isccCmd = Get-Command iscc -ErrorAction SilentlyContinue
    $iscc = if ($isccCmd) { $isccCmd.Source } else { $null }
    if (-not $iscc) {
        $candidates = @(
            "D:\Work_Space_for_AI\Qoder_CN\tools\innosetup\ISCC.exe",
            "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
            "${env:ProgramFiles}\Inno Setup 6\ISCC.exe",
            "${env:ProgramFiles}\Inno Setup 7\ISCC.exe",
            "${env:LOCALAPPDATA}\Programs\Inno Setup 6\ISCC.exe",
            "${env:LOCALAPPDATA}\Programs\Inno Setup 7\ISCC.exe"
        )
        foreach ($c in $candidates) {
            if (Test-Path $c) {
                $iscc = $c
                break
            }
        }
    }
    if ($iscc) {
        Write-Host "building installer via Inno Setup ($iscc)"
        & $iscc "/dMyAppVersion=$Version" "/dMyAppArch=amd64" (Join-Path $PSScriptRoot "installer.iss")
        if ($LASTEXITCODE -ne 0) { throw "Inno Setup compilation failed" }
        $installerName = "FatTrace-Setup-$Version-windows-amd64.exe"
        if (Test-Path (Join-Path $dist $installerName)) {
            $artifacts += $installerName
        }
    } else {
        Write-Warning "ISCC.exe not found; skipping installer package generation"
    }
}

$sums = Join-Path $dist "SHA256SUMS.txt"
Remove-Item -Force $sums -ErrorAction SilentlyContinue
foreach ($name in $artifacts) {
    $hash = (Get-FileHash -Algorithm SHA256 (Join-Path $dist $name)).Hash.ToLowerInvariant()
    "$hash  $name" | Add-Content -Encoding ascii $sums
}

Write-Host ""
Write-Host "artifacts in $dist"
Get-ChildItem $dist | Select-Object Name, Length | Format-Table -AutoSize
