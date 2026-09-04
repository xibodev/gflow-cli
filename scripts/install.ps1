# gflow-cli Windows Installer
$ErrorActionPreference = 'Stop'

$Repo = "xibodev/gflow-cli"
$InstallDir = "$HOME\.gflow\bin"
$BinaryName = "gflow.exe"

Write-Host "⚡ Installing gflow-cli for Windows..." -ForegroundColor Cyan

# Create install directory
if (!(Test-Path -Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

# Determine architecture
$Arch = if ([Environment]::Is64BitOperatingSystem) { "windows_amd64" } else { "windows_386" }

# Download latest release
$ReleasesUrl = "https://api.github.com/repos/$Repo/releases/latest"
Write-Host "Finding latest release from $Repo..." -ForegroundColor Gray

try {
    $Release = Invoke-RestMethod -Uri $ReleasesUrl
    $Asset = $Release.assets | Where-Object { $_.name -like "*$Arch*.zip" } | Select-Object -First 1
    if (!$Asset) {
        $Asset = $Release.assets | Where-Object { $_.name -like "*windows*.zip" } | Select-Object -First 1
    }

    if ($Asset) {
        $ZipPath = "$env:TEMP\gflow.zip"
        Write-Host "Downloading $($Asset.name)..." -ForegroundColor Gray
        Invoke-WebRequest -Uri $Asset.browser_download_url -OutFile $ZipPath
        Expand-Archive -Path $ZipPath -DestinationPath $InstallDir -Force
        Remove-Item $ZipPath -Force
    } else {
        Write-Host "No prebuilt release archive found. Building from source via 'go install'..." -ForegroundColor Yellow
        go install github.com/$Repo/cmd/gflow@latest
    }
} catch {
    Write-Host "Could not fetch release directly: $_" -ForegroundColor Yellow
    Write-Host "Falling back to go install..." -ForegroundColor Gray
    go install github.com/$Repo/cmd/gflow@latest
}

# Add to user PATH if not present
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "Added $InstallDir to User PATH." -ForegroundColor Green
}

Write-Host "`n✔ gflow installed successfully!" -ForegroundColor Green
Write-Host "To finish setup, run:" -ForegroundColor Cyan
Write-Host "  gflow setup`n" -ForegroundColor White
