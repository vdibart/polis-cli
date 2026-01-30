# Polis CLI Windows Setup Script
# Automated setup for Polis CLI on Windows using Git Bash

param(
    [switch]$SkipDependencies,
    [string]$PolisPath = $PSScriptRoot
)

Write-Host "`n=== Polis CLI Windows Setup ===" -ForegroundColor Cyan
Write-Host ""

# Resolve polis-cli root directory (parent of scripts/)
if ($PolisPath -eq $PSScriptRoot) {
    $PolisPath = Split-Path $PSScriptRoot -Parent
}

# Check if Git Bash is installed
$gitBashPath = "C:\Program Files\Git\bin\bash.exe"
if (-not (Test-Path $gitBashPath)) {
    Write-Host "❌ Git Bash not found!" -ForegroundColor Red
    Write-Host ""
    Write-Host "Please install Git for Windows first:" -ForegroundColor Yellow
    Write-Host "  Download: https://git-scm.com/download/win" -ForegroundColor Yellow
    Write-Host "  Or install via: winget install Git.Git" -ForegroundColor Yellow
    exit 1
}

Write-Host "✓ Git Bash found at: $gitBashPath" -ForegroundColor Green

# Check and install dependencies
if (-not $SkipDependencies) {
    Write-Host ""
    Write-Host "Checking dependencies..." -ForegroundColor Cyan
    
    # Check for jq
    if (-not (Get-Command jq -ErrorAction SilentlyContinue)) {
        Write-Host "  jq not found. Installing..." -ForegroundColor Yellow
        
        if (Get-Command choco -ErrorAction SilentlyContinue) {
            choco install jq -y
        }
        elseif (Get-Command scoop -ErrorAction SilentlyContinue) {
            scoop install jq
        }
        elseif (Get-Command winget -ErrorAction SilentlyContinue) {
            winget install jqlang.jq --accept-package-agreements --accept-source-agreements
        }
        else {
            Write-Host "  ⚠️  Package manager not found. Please install jq manually:" -ForegroundColor Yellow
            Write-Host "     Download: https://github.com/jqlang/jq/releases" -ForegroundColor Yellow
        }
    } else {
        Write-Host "  ✓ jq found" -ForegroundColor Green
    }
    
    # Check for pandoc
    if (-not (Get-Command pandoc -ErrorAction SilentlyContinue)) {
        Write-Host "  pandoc not found. Installing..." -ForegroundColor Yellow
        
        if (Get-Command choco -ErrorAction SilentlyContinue) {
            choco install pandoc -y
        }
        elseif (Get-Command scoop -ErrorAction SilentlyContinue) {
            scoop install pandoc
        }
        elseif (Get-Command winget -ErrorAction SilentlyContinue) {
            winget install JohnMacFarlane.Pandoc --accept-package-agreements --accept-source-agreements
        }
        else {
            Write-Host "  ⚠️  Package manager not found. Please install pandoc manually:" -ForegroundColor Yellow
            Write-Host "     Download: https://pandoc.org/installing.html" -ForegroundColor Yellow
        }
    } else {
        Write-Host "  ✓ pandoc found" -ForegroundColor Green
    }
}

# Configure Git Bash PATH
Write-Host ""
Write-Host "Configuring Git Bash PATH..." -ForegroundColor Cyan

$bashrc = "$env:USERPROFILE\.bashrc"
# Convert Windows path to Git Bash Unix-style path (D:\Studio13\... -> /d/Studio13/...)
$driveLetter = $PolisPath.Substring(0, 1).ToLower()
$restOfPath = $PolisPath.Substring(3).Replace('\', '/')
$unixPath = "/$driveLetter/$restOfPath"
$polisPathLine = "export PATH=`"`$PATH:$unixPath/bin`""

# Create .bashrc if it doesn't exist
if (-not (Test-Path $bashrc)) {
    New-Item -ItemType File -Path $bashrc -Force | Out-Null
    Write-Host "  Created $bashrc" -ForegroundColor Yellow
}

# Add polis-cli to PATH if not already there
if (-not (Select-String -Path $bashrc -Pattern "polis-cli" -Quiet)) {
    Add-Content -Path $bashrc -Value "`n# Polis CLI`n$polisPathLine"
    Write-Host "  ✓ Added polis-cli to Git Bash PATH" -ForegroundColor Green
} else {
    Write-Host "  ✓ polis-cli already in Git Bash PATH" -ForegroundColor Green
}

# Add jq to Git Bash PATH if installed
Write-Host ""
Write-Host "Configuring jq for Git Bash..." -ForegroundColor Cyan

$jqPaths = @(
    "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\jqlang.jq_*\jq.exe",
    "$env:ProgramFiles\jq\jq.exe",
    "$env:ProgramFiles(x86)\jq\jq.exe"
)

$jqFound = $false
foreach ($pattern in $jqPaths) {
    $jqExe = Get-Item $pattern -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($jqExe) {
        $jqDir = $jqExe.DirectoryName
        $driveLetter = $jqDir.Substring(0, 1).ToLower()
        $restOfPath = $jqDir.Substring(3).Replace('\', '/')
        $jqUnixPath = "/$driveLetter/$restOfPath"
        $jqPathLine = "export PATH=`"`$PATH:$jqUnixPath`""
        
        if (-not (Select-String -Path $bashrc -Pattern "jqlang.jq" -Quiet)) {
            Add-Content -Path $bashrc -Value "`n# jq (JSON processor)`n$jqPathLine"
            Write-Host "  ✓ Added jq to Git Bash PATH" -ForegroundColor Green
            $jqFound = $true
            break
        } else {
            Write-Host "  ✓ jq already in Git Bash PATH" -ForegroundColor Green
            $jqFound = $true
            break
        }
    }
}

if (-not $jqFound) {
    Write-Host "  ⚠️  jq not found. Please install it:" -ForegroundColor Yellow
    Write-Host "     winget install jqlang.jq" -ForegroundColor White
}

# Add pandoc to Git Bash PATH if installed
Write-Host ""
Write-Host "Configuring pandoc for Git Bash..." -ForegroundColor Cyan

$pandocPaths = @(
    "C:\Program Files\Pandoc\pandoc.exe",
    "$env:LOCALAPPDATA\Pandoc\pandoc.exe"
)

$pandocFound = $false
foreach ($pandocPath in $pandocPaths) {
    if (Test-Path $pandocPath) {
        $pandocDir = Split-Path $pandocPath -Parent
        $driveLetter = $pandocDir.Substring(0, 1).ToLower()
        $restOfPath = $pandocDir.Substring(3).Replace('\', '/')
        $pandocUnixPath = "/$driveLetter/$restOfPath"
        $pandocPathLine = "export PATH=`"`$PATH:$pandocUnixPath`""
        
        if (-not (Select-String -Path $bashrc -Pattern "Program Files/Pandoc" -Quiet)) {
            Add-Content -Path $bashrc -Value "`n# pandoc (Markdown to HTML converter)`n$pandocPathLine"
            Write-Host "  ✓ Added pandoc to Git Bash PATH" -ForegroundColor Green
            $pandocFound = $true
            break
        } else {
            Write-Host "  ✓ pandoc already in Git Bash PATH" -ForegroundColor Green
            $pandocFound = $true
            break
        }
    }
}

if (-not $pandocFound) {
    Write-Host "  ⚠️  pandoc not found. Please install it:" -ForegroundColor Yellow
    Write-Host "     winget install JohnMacFarlane.Pandoc" -ForegroundColor White
}

# Test installation
Write-Host ""
Write-Host "Testing installation..." -ForegroundColor Cyan

$testResult = & $gitBashPath -c "cd '$PolisPath'; ./bin/polis --help 2>&1 | head -5"

if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Polis CLI is working!" -ForegroundColor Green
    Write-Host ""
    Write-Host "=== Setup Complete! ===" -ForegroundColor Green
    Write-Host ""
    Write-Host "To use Polis CLI:" -ForegroundColor Cyan
    Write-Host "  1. Open Git Bash" -ForegroundColor Yellow
    Write-Host "  2. Reload your session:" -ForegroundColor Yellow
    Write-Host "     source ~/.bashrc" -ForegroundColor White
    Write-Host "  3. Initialize a site:" -ForegroundColor Yellow
    Write-Host "     mkdir my-site && cd my-site" -ForegroundColor White
    Write-Host "     polis init --site-title 'My Site'" -ForegroundColor White
    Write-Host "  4. Set your base URL:" -ForegroundColor Yellow
    Write-Host "     export POLIS_BASE_URL='https://yourdomain.com'" -ForegroundColor White
    Write-Host "  5. Create your first post:" -ForegroundColor Yellow
    Write-Host "     echo '# Hello World' > hello.md" -ForegroundColor White
    Write-Host "     polis post hello.md" -ForegroundColor White
    Write-Host ""
} else {
    Write-Host "  ⚠️  Test failed. Please check the installation." -ForegroundColor Yellow
    Write-Host "  Error output:" -ForegroundColor Red
    Write-Host $testResult -ForegroundColor Red
}

Write-Host ""
Write-Host "For more information, see:" -ForegroundColor Cyan
Write-Host "  docs/WINDOWS.md" -ForegroundColor Yellow
Write-Host ""
