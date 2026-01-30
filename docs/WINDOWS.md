# Polis CLI on Windows

Polis CLI can run on Windows using **Git Bash** (included with Git for Windows). This provides a bash-compatible environment with minimal setup.

## Quick Start

### Prerequisites

1. **Git for Windows** (includes Git Bash)
   - Download: https://git-scm.com/download/win
   - Or install via: `winget install Git.Git`

2. **jq** (JSON processor)
   - Install via: `winget install jqlang.jq`
   - Or: `choco install jq`
   - Or: `scoop install jq`

3. **pandoc** (for `polis render` command)
   - Install via: `winget install JohnMacFarlane.Pandoc`
   - Or: `choco install pandoc`
   - Or: `scoop install pandoc`

### Automated Setup

Use the provided setup script:

```powershell
.\scripts\setup-windows.ps1
```

This script will:
- Verify Git Bash is installed
- Install missing dependencies (jq, pandoc)
- Configure Git Bash PATH
- Set up polis-cli for use

### Manual Setup

1. **Clone polis-cli**:
   ```bash
   git clone https://github.com/vdibart/polis-cli.git
   cd polis-cli
   ```

2. **Add to Git Bash PATH**:
   
   Edit `~/.bashrc` (or create it) and add:
   ```bash
   # Polis CLI
   export PATH="$PATH:/c/path/to/polis-cli/bin"
   
   # jq (if installed via winget)
   export PATH="$PATH:/c/Users/$USER/AppData/Local/Microsoft/WinGet/Packages/jqlang.jq_*/"
   
   # pandoc (if installed via winget)
   export PATH="$PATH:/c/Program Files/Pandoc"
   ```
   
   Replace `/c/path/to/polis-cli/bin` with your actual path (use forward slashes, convert drive letters: `D:\` → `/d/`)

3. **Reload Git Bash**:
   ```bash
   source ~/.bashrc
   ```

4. **Test installation**:
   ```bash
   polis --help
   jq --version
   pandoc --version
   ```

## Usage

All polis commands work the same in Git Bash:

```bash
# Initialize a site
polis init --site-title "My Site"

# Create a post
polis post my-post.md

# Render HTML
polis render

# All other commands work normally
polis comment https://example.com/post.md
polis follow https://example.com
```

## Path Conversion

When working with Windows paths in Git Bash:

- **Windows path**: `D:\Studio13\online\polis-cli`
- **Git Bash path**: `/d/Studio13/online/polis-cli`

Git Bash automatically converts paths, but when configuring PATH in `.bashrc`, use Unix-style paths.

## Troubleshooting

### "polis: command not found"

- Make sure `polis-cli/bin` is in your PATH
- Reload Git Bash: `source ~/.bashrc`
- Or restart Git Bash completely

### "jq: command not found"

- Install jq (see Prerequisites above)
- Add jq to Git Bash PATH in `~/.bashrc`
- Find jq location: `where.exe jq` (in PowerShell)
- Convert to Git Bash path format

### "pandoc is required for rendering"

- Install pandoc (see Prerequisites above)
- Add pandoc to Git Bash PATH in `~/.bashrc`
- Default location: `/c/Program Files/Pandoc`

### Path Issues

If you encounter path-related errors:

1. Use forward slashes in `.bashrc` PATH entries
2. Convert drive letters: `C:\` → `/c/`, `D:\` → `/d/`
3. Use quotes if paths contain spaces: `"/c/Program Files/Pandoc"`

## Alternative: WSL2

For a fully Linux-compatible environment, you can use WSL2:

```powershell
# Install WSL2
wsl --install

# In WSL2 Ubuntu terminal
sudo apt-get update
sudo apt-get install openssh-client jq curl pandoc git

# Clone and use polis-cli
git clone https://github.com/vdibart/polis-cli.git
export PATH="$PATH:$(pwd)/polis-cli/bin"
```

WSL2 provides 100% compatibility but requires WSL2 setup.

## See Also

- [USAGE.md](USAGE.md) - Complete command reference
- [CONTRIBUTING.md](../CONTRIBUTING.md) - Contributing guidelines
