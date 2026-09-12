# gflow-cli Windows Installer
$ErrorActionPreference = 'Stop'

$Repo = "xibodev/gflow-cli"
$InstallDir = "$HOME\.gflow\bin"

Write-Host "Installing gflow-cli for Windows..." -ForegroundColor Cyan

if (!(Test-Path -Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

# Determine architecture (GoReleaser publishes windows_amd64 and windows_arm64).
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "ARM64" { "windows_arm64" }
    "AMD64" { "windows_amd64" }
    default { Write-Error "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (supported: AMD64, ARM64)"; exit 1 }
}

$ZipPath = Join-Path ([System.IO.Path]::GetTempPath()) ("gflow-" + [System.Guid]::NewGuid().ToString() + ".zip")
$cleanup = { if (Test-Path -Path $ZipPath) { Remove-Item $ZipPath -Force -ErrorAction SilentlyContinue } }
trap { &$cleanup } # ensure temp cleanup on terminating errors

function Install-FromGo {
    Write-Host "Building from source via 'go install'..." -ForegroundColor Gray
    $env:GOBIN = $InstallDir
    go install github.com/$Repo/cmd/gflow@latest
    if ($LASTEXITCODE -ne 0) { Write-Error "'go install' failed with exit code $LASTEXITCODE"; exit $LASTEXITCODE }
    Write-Host "Installed via 'go install' to $InstallDir." -ForegroundColor Green
}

try {
    if (Get-Command gh -ErrorAction SilentlyContinue) {
        # Authenticated gh is preferred for private releases.
        & gh release download -R $Repo --pattern "*$Arch*.zip" -D ([System.IO.Path]::GetTempPath())
        if ($LASTEXITCODE -ne 0) { throw "gh release download found no $Arch asset" }
        $Downloaded = Get-ChildItem ([System.IO.Path]::GetTempPath()) -Filter "*$Arch*.zip" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        if (!$Downloaded) { throw "gh download succeeded but asset not found" }
        Move-Item $Downloaded.FullName $ZipPath -Force
    }
    else {
        $ReleasesUrl = "https://api.github.com/repos/$Repo/releases/latest"
        Write-Host "Finding latest release from $Repo..." -ForegroundColor Gray
        $Release = Invoke-RestMethod -Uri $ReleasesUrl
        $Asset = $Release.assets | Where-Object { $_.name -like "*$Arch*.zip" } | Select-Object -First 1
        if (!$Asset) { throw "No prebuilt $Arch asset in latest release" }
        Write-Host "Downloading $($Asset.name)..." -ForegroundColor Gray
        Invoke-WebRequest -Uri $Asset.browser_download_url -OutFile $ZipPath
        if ($LASTEXITCODE -ne 0 -and (Test-Path -Path $ZipPath) -eq $false) { throw "Download failed" }
    }

    Expand-Archive -Path $ZipPath -DestinationPath $InstallDir -Force
}
catch {
    Write-Host "Prebuilt install unavailable: $_" -ForegroundColor Yellow
    Install-FromGo
}
finally {
    &$cleanup
}

# Add to user PATH if not present
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$InstallDir", "User")
    $env:Path = "$env:Path;$InstallDir"
    Write-Host "Added $InstallDir to User PATH." -ForegroundColor Green
}

Write-Host "`n[OK] gflow installed successfully!" -ForegroundColor Green
Write-Host "Location: $InstallDir\gflow.exe" -ForegroundColor Gray

Write-Host "`nQuickstart & Getting Started:" -ForegroundColor Cyan
Write-Host "  1. Check provider status:" -ForegroundColor White
Write-Host "     gflow status" -ForegroundColor Yellow
Write-Host "  2. Test chat in terminal (Gemini Flash):" -ForegroundColor White
Write-Host "     gflow chat `"Hello Gemini`"" -ForegroundColor Yellow
Write-Host "  3. Generate an image (Imagen 3):" -ForegroundColor White
Write-Host "     gflow image `"cyberpunk cat on a neon roof`"" -ForegroundColor Yellow
Write-Host "  4. Generate a video (Veo / MiniMax H3):" -ForegroundColor White
Write-Host "     gflow video `"ocean waves crashing against cliffs`"" -ForegroundColor Yellow

Write-Host "`nSupported Providers:" -ForegroundColor Cyan
Write-Host "  - Gemini (default): Imagen 3 images, Veo video, Lyria music, Flash chat." -ForegroundColor Gray
Write-Host "  - MiniMax: H3 video ('gflow video ... -P minimax')." -ForegroundColor Gray
Write-Host "  - Google Flow: Flow Imagen 4 & Veo 3.1 ('gflow image ... -P flow')." -ForegroundColor Gray

Write-Host "`nMCP for AI Coding Assistants (Cursor / Claude / OpenCode / Windsurf):" -ForegroundColor Cyan
Write-Host "  Command: gflow" -ForegroundColor Gray
Write-Host "  Args:    [`"mcp`"]`n" -ForegroundColor Gray
