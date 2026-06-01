---
name: install-cli
description: Install Pasture binaries (pastured, pasture-msg, pasture-release) from GitHub Releases, go install, or Nix
---

# /pasture:install-cli

Claude Code skill for automated Pasture binary installation.

## Usage

Invoke via Claude Code:
```
/pasture:install-cli
```

## Behavior

The skill performs the following steps in order:

### 1. Detect platform

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')   # linux or darwin
ARCH=$(uname -m)                               # x86_64, aarch64, or arm64
```

Map `ARCH` to Go-style suffix:
- `x86_64`  → `amd64`
- `aarch64` → `arm64`
- `arm64`   → `arm64`

Compose the binary suffix: `${OS}-${ARCH}` (e.g. `linux-amd64`, `darwin-arm64`).

### 2. Fetch latest release tag

```bash
TAG=$(gh api repos/dayvidpham/pasture/releases/latest --jq '.tag_name')
```

Requires the GitHub CLI (`gh`) to be authenticated. If unavailable, fall back to
curl:

```bash
TAG=$(curl -fsSL https://api.github.com/repos/dayvidpham/pasture/releases/latest \
      | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'])")
```

### 3. Download binaries

```bash
SUFFIX="${OS}-${ARCH}"
BASE_URL="https://github.com/dayvidpham/pasture/releases/download/${TAG}"
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

for cmd in pastured pasture-msg pasture-release; do
  curl -fsSL "${BASE_URL}/${cmd}-${SUFFIX}" -o "${INSTALL_DIR}/${cmd}"
  chmod +x "${INSTALL_DIR}/${cmd}"
done
```

### 4. Verify installation

```bash
pastured --version
pasture-msg --version
pasture-release --version
```

If `~/.local/bin` is not in `PATH`, the skill should print a reminder:
```
Add ~/.local/bin to your PATH:
  export PATH="$HOME/.local/bin:$PATH"
```

## Fallback: go install

If GitHub Releases are unavailable (network restriction, air-gapped environment,
or the repository is not yet public), install from source with Go:

```bash
go install github.com/dayvidpham/pasture/cmd/pastured@latest
go install github.com/dayvidpham/pasture/cmd/pasture-msg@latest
go install github.com/dayvidpham/pasture/cmd/pasture-release@latest
```

Requires Go 1.24+ and access to the Go module proxy (proxy.golang.org).

## Fallback: Nix

If a Nix-enabled system is available:

```bash
nix profile install github:dayvidpham/pasture#pastured
nix profile install github:dayvidpham/pasture#pasture-msg
nix profile install github:dayvidpham/pasture#pasture-release
# or install all at once:
nix profile install github:dayvidpham/pasture
```

## Supported Platforms

| OS    | Architecture | Binary suffix   |
|-------|-------------|-----------------|
| Linux | x86-64      | linux-amd64     |
| Linux | ARM64       | linux-arm64     |
| macOS | x86-64      | darwin-amd64    |
| macOS | Apple M*    | darwin-arm64    |

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `permission denied` on download | Insufficient write perms to `~/.local/bin` | `mkdir -p ~/.local/bin && chmod 755 ~/.local/bin` |
| `pastured: command not found` after install | `~/.local/bin` not in `PATH` | Add to shell profile: `export PATH="$HOME/.local/bin:$PATH"` |
| `gh: command not found` | GitHub CLI not installed | Use the curl fallback or install `gh` from https://cli.github.com |
| Wrong binary (SIGILL / exec format error) | Wrong platform suffix detected | Verify `uname -s` and `uname -m` output match a supported platform |
