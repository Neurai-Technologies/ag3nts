# =============================================================================
# ag3nts Setup Script (Windows)
# Auto-detects SSD, creates symlinks, syncs shared configs.
# Run in PowerShell as Administrator.
# =============================================================================

$ErrorActionPreference = "Stop"

# --- Helper Functions ---
function Write-Step($msg) { Write-Host "`n>> $msg" -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host "   OK: $msg" -ForegroundColor Green }
function Write-Skip($msg) { Write-Host "   SKIP: $msg" -ForegroundColor Yellow }
function Write-Fail($msg) { Write-Host "   FAIL: $msg" -ForegroundColor Red }

# --- Pre-flight Checks ---
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "  ag3nts Setup Script (Windows)" -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan

Write-Step "Checking administrator privileges..."
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Fail "This script must be run as Administrator (symlinks require it)."
    Write-Host "   Right-click PowerShell -> Run as Administrator, then re-run this script." -ForegroundColor Red
    exit 1
}
Write-Ok "Running as Administrator."

# --- Auto-detect SSD Drive Letter ---
Write-Step "Scanning for ag3nts folder across all drives (up to 3 levels deep)..."
$BASE_PATH = $null
$drives = Get-PSDrive -PSProvider FileSystem | Where-Object { $_.Root -ne "C:\" }
$allRoots = @($drives | ForEach-Object { $_.Root }) + @("C:\")

foreach ($root in $allRoots) {
    $found = Get-ChildItem -Path $root -Directory -Recurse -Depth 3 -Filter "ag3nts" -ErrorAction SilentlyContinue |
        Where-Object { Test-Path (Join-Path $_.FullName "shared\ag3nts.md") } |
        Select-Object -First 1
    if ($found) {
        $BASE_PATH = $found.FullName
        Write-Ok "Found ag3nts at $BASE_PATH (Drive: $root)"
        break
    }
}

if (-not $BASE_PATH) {
    Write-Fail "ag3nts folder not found on any drive."
    Write-Host "   Drives scanned (up to 3 levels deep):" -ForegroundColor Red
    $allRoots | ForEach-Object { Write-Host "   - $_" -ForegroundColor Red }
    Write-Host "   Make sure your SSD is connected and contains the ag3nts folder." -ForegroundColor Red
    exit 1
}

# --- Derived Paths ---
$PLATFORM = "$BASE_PATH\windows"
$SHARED = "$BASE_PATH\shared"

$CENTRAL_BIN_SSD = "$PLATFORM\bin"
$CLAUDE_CONFIG_SSD = "$PLATFORM\claude-code\config"
$GEMINI_CONFIG_SSD = "$PLATFORM\gemini-cli\config"
$CODEX_CONFIG_SSD = "$PLATFORM\codex-cli\config"

$CENTRAL_BIN_LOCAL = "$env:USERPROFILE\.local\bin"
$CLAUDE_CONFIG_LOCAL = "$env:USERPROFILE\.claude"
$GEMINI_CONFIG_LOCAL = "$env:USERPROFILE\.gemini"
$CODEX_CONFIG_LOCAL = "$env:USERPROFILE\.codex"

# --- Validate SSD Contents ---
Write-Step "Validating ag3nts folder structure..."
$missing = @()
if (-not (Test-Path $CENTRAL_BIN_SSD)) { $missing += "windows\bin" }
if (-not (Test-Path $CLAUDE_CONFIG_SSD)) { $missing += "windows\claude-code\config" }
if (-not (Test-Path $GEMINI_CONFIG_SSD)) { $missing += "windows\gemini-cli\config" }
if (-not (Test-Path $CODEX_CONFIG_SSD)) { $missing += "windows\codex-cli\config" }

if ($missing.Count -gt 0) {
    Write-Fail "Missing folders in $PLATFORM :"
    $missing | ForEach-Object { Write-Host "   - $_" -ForegroundColor Red }
    exit 1
}
Write-Ok "All expected folders found."

# --- Symlink Helper ---
function New-SymlinkSafe($localPath, $ssdTarget, $label) {
    Write-Step "Setting up $label symlink..."
    if (Test-Path $localPath) {
        $item = Get-Item $localPath -Force
        if ($item.Attributes -match "ReparsePoint") {
            $currentTarget = $item.Target
            if ($currentTarget -eq $ssdTarget) {
                Write-Skip "Symlink already correct: $localPath -> $ssdTarget"
            } else {
                Write-Host "   Updating symlink: $currentTarget -> $ssdTarget" -ForegroundColor Yellow
                Remove-Item $localPath -Force
                New-Item -ItemType SymbolicLink -Path $localPath -Target $ssdTarget | Out-Null
                Write-Ok "Updated symlink: $localPath -> $ssdTarget"
            }
        } else {
            Write-Fail "$localPath already exists and is NOT a symlink."
            Write-Host "   Back it up and remove it, then re-run this script." -ForegroundColor Red
            exit 1
        }
    } else {
        $parentDir = Split-Path $localPath -Parent
        if (-not (Test-Path $parentDir)) {
            New-Item -ItemType Directory -Path $parentDir -Force | Out-Null
        }
        New-Item -ItemType SymbolicLink -Path $localPath -Target $ssdTarget | Out-Null
        Write-Ok "Created symlink: $localPath -> $ssdTarget"
    }
}

# --- Create Symlinks ---
New-SymlinkSafe $CENTRAL_BIN_LOCAL $CENTRAL_BIN_SSD "Central bin (claude, gemini, codex)"
New-SymlinkSafe $CLAUDE_CONFIG_LOCAL $CLAUDE_CONFIG_SSD "Claude Code config"
New-SymlinkSafe $GEMINI_CONFIG_LOCAL $GEMINI_CONFIG_SSD "Gemini CLI config"
New-SymlinkSafe $CODEX_CONFIG_LOCAL $CODEX_CONFIG_SSD "Codex CLI config"

# --- PATH: Add central bin location ---
Write-Step "Checking PATH for ag3nts bin..."
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
$binPath = "$env:USERPROFILE\.local\bin"

if ($userPath -split ";" | Where-Object { $_ -eq $binPath }) {
    Write-Skip "PATH already contains $binPath"
} else {
    [Environment]::SetEnvironmentVariable("PATH", "$userPath;$binPath", [EnvironmentVariableTarget]::User)
    $env:PATH += ";$binPath"
    Write-Ok "Added $binPath to user PATH."
}

# --- Sync Shared Configs ---
Write-Step "Syncing shared configs to Windows platform..."
# Symlink shared Claude Code files (single source of truth, no copies)
function New-SharedSymlink($target, $link, $label) {
    if (Test-Path $link) {
        $item = Get-Item $link -Force
        if ($item.Attributes -match "ReparsePoint") {
            $currentTarget = $item.Target
            if ($currentTarget -eq $target) {
                Write-Skip "Symlink already correct: $label"
                return
            }
            Remove-Item $link -Force
        } else {
            Remove-Item $link -Recurse -Force
            Write-Ok "Replaced copy with symlink: $label"
        }
    }
    New-Item -ItemType SymbolicLink -Path $link -Target $target | Out-Null
    Write-Ok "Created symlink: $label"
}

New-SharedSymlink "$SHARED\ag3nts.md" "$CLAUDE_CONFIG_SSD\ag3nts.md" "ag3nts.md"
New-SharedSymlink "$SHARED\claude-code\CLAUDE.md" "$CLAUDE_CONFIG_SSD\CLAUDE.md" "CLAUDE.md"
New-SharedSymlink "$SHARED\claude-code\settings.json" "$CLAUDE_CONFIG_SSD\settings.json" "settings.json"
New-SharedSymlink "$SHARED\claude-code\statusline.sh" "$CLAUDE_CONFIG_SSD\statusline.sh" "statusline.sh"
New-SharedSymlink "$SHARED\claude-code\files\agents" "$CLAUDE_CONFIG_SSD\agents" "agents/"
New-SharedSymlink "$SHARED\claude-code\hooks" "$CLAUDE_CONFIG_SSD\hooks" "hooks/"

# Sync remaining shared configs (gemini, codex — still copy-based)
$syncPairs = @(
    @{ Shared = "$SHARED\claude-code"; Platform = $CLAUDE_CONFIG_SSD },
    @{ Shared = "$SHARED\gemini-cli"; Platform = $GEMINI_CONFIG_SSD },
    @{ Shared = "$SHARED\codex-cli"; Platform = $CODEX_CONFIG_SSD }
)

foreach ($pair in $syncPairs) {
    if (Test-Path $pair.Shared) {
        $files = Get-ChildItem $pair.Shared -File -ErrorAction SilentlyContinue
        foreach ($file in $files) {
            # Skip files that are now symlinked
            $dest = Join-Path $pair.Platform $file.Name
            if ((Test-Path $dest) -and ((Get-Item $dest -Force).Attributes -match "ReparsePoint")) {
                continue
            }
            $destExists = Test-Path $dest
            if ($destExists) {
                $srcHash = (Get-FileHash $file.FullName).Hash
                $dstHash = (Get-FileHash $dest).Hash
                if ($srcHash -ne $dstHash) {
                    Copy-Item $file.FullName $dest -Force
                    Write-Ok "Updated: $($file.Name) -> $($pair.Platform | Split-Path -Leaf)"
                }
            } else {
                Copy-Item $file.FullName $dest -Force
                Write-Ok "Copied: $($file.Name) -> $($pair.Platform | Split-Path -Leaf)"
            }
        }
    }
}

Write-Ok "Shared config sync complete."

# --- PowerShell Aliases ---
Write-Step "Setting up PowerShell aliases..."

$profilePath = $PROFILE.CurrentUserAllHosts
if (-not (Test-Path $profilePath)) {
    New-Item -ItemType File -Path $profilePath -Force | Out-Null
    Write-Ok "Created PowerShell profile at $profilePath"
}

if (Select-String -Path $profilePath -Pattern "claude-bare" -Quiet -ErrorAction SilentlyContinue) {
    Write-Skip "claude-bare alias already in PowerShell profile"
} else {
    Add-Content -Path $profilePath -Value @"

# ag3nts: Bare mode function for scripted Claude Code runs (~14% faster, no ambient config)
function claude-bare { claude --bare @args }
"@
    Write-Ok "Added claude-bare function to PowerShell profile"
}

# --- Verification ---
Write-Step "Verifying Claude Code..."
try {
    $claudeVer = & "$CENTRAL_BIN_LOCAL\claude.cmd" --version 2>&1
    Write-Ok "Claude Code $claudeVer"
} catch {
    Write-Fail "Claude Code verification failed: $_"
}

Write-Step "Verifying Gemini CLI..."
try {
    $geminiVer = & "$CENTRAL_BIN_LOCAL\gemini.cmd" --version 2>&1
    Write-Ok "Gemini CLI $geminiVer"
} catch {
    Write-Fail "Gemini CLI verification failed: $_"
}

Write-Step "Verifying Codex CLI..."
try {
    $ErrorActionPreference = "Continue"
    $codexOutput = & "$CENTRAL_BIN_LOCAL\codex.cmd" --version 2>&1
    $ErrorActionPreference = "Stop"
    $codexVer = ($codexOutput | Where-Object { $_ -match "codex-cli" }) -join ""
    if ($codexVer) {
        Write-Ok "$codexVer"
    } else {
        Write-Fail "Codex CLI did not return a version string"
    }
} catch {
    $ErrorActionPreference = "Stop"
    Write-Fail "Codex CLI verification failed: $_"
}

# --- Summary ---
Write-Host "`n=============================================" -ForegroundColor Cyan
Write-Host "  Setup Complete!" -ForegroundColor Green
Write-Host "  SSD detected at: $BASE_PATH" -ForegroundColor Green
Write-Host "=============================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  All tools available via simple commands:" -ForegroundColor White
Write-Host "    claude    - Claude Code" -ForegroundColor White
Write-Host "    gemini    - Gemini CLI" -ForegroundColor White
Write-Host "    codex     - Codex CLI" -ForegroundColor White
Write-Host ""
Write-Host "  Next steps:" -ForegroundColor White
Write-Host "  1. Close this terminal and open a new one" -ForegroundColor White
Write-Host "  2. Run 'claude' to authenticate" -ForegroundColor White
Write-Host "  3. Run 'gemini' to authenticate" -ForegroundColor White
Write-Host "  4. Run 'codex' to authenticate" -ForegroundColor White
Write-Host ""
