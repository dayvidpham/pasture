#!/usr/bin/env bash
#
# pasture installer — served at the repository root and fetched via the GitHub
# raw content path:
#
#   curl -fsSL https://raw.githubusercontent.com/dayvidpham/pasture/main/install.sh | bash
#
# Says what it is about to do and waits for a yes before doing any of it. Then
# downloads the release builds for this machine, verifies each against the
# release's own checksums.txt, and installs pasture and pastured to
# ~/.local/bin. It also creates the daemon's minimal config at
# ~/.config/pasture/config.yaml when one does not exist, and on Linux with a
# user systemd session installs, enables and starts the pastured user unit at
# ~/.config/systemd/user/pastured.service. The unit runs
# ~/.local/bin/pastured, so `pasture upgrade` replaces the binaries without
# rewriting the unit; restart the unit after an upgrade. Both binaries are
# downloaded and verified before either is replaced, so the CLI and the daemon
# always match. It never escalates and never edits a shell profile; where PATH
# needs a line added, it prints the line for you to add. The internal release
# tool (pasture-release) is never fetched.
#
#   PASTURE_YES=1         install without the prompt (for CI and scripts)
#   PASTURE_VERSION=vX    install a specific release instead of the newest
#   PASTURE_NO_SERVICE=1  skip the user unit (binaries and config still install)
#
# The whole script is a set of functions with `main` called on the very last
# line. Piped into a shell, a script executes whatever bytes have arrived — so a
# connection that drops midway through would otherwise run half an install. This
# way a truncated transfer defines some functions and does nothing at all.

set -euo pipefail

REPO="dayvidpham/pasture"
BIN_DIR="${HOME}/.local/bin"
CONFIG_DIR="${HOME}/.config/pasture"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
# systemd reads its user units under an absolute XDG_CONFIG_HOME; a relative
# value is ignored, so fall back to ~/.config. The config file itself stays at
# ~/.config/pasture because pastured's default is fixed there and does not
# consult XDG_CONFIG_HOME.
config_home="${XDG_CONFIG_HOME:-}"
case "$config_home" in
  /*) config_home="${config_home%/}" ;;
  *) config_home="${HOME}/.config" ;;
esac
SYSTEMD_USER_DIR="${config_home}/systemd/user"
SERVICE_FILE="${SYSTEMD_USER_DIR}/pastured.service"
SERVICE_MARKER="# Managed by the Pasture installer."

# The co-located pair, installed together so the CLI and daemon never diverge.
# pasture-release is an internal tool and is deliberately absent.
BINARIES=(pasture pastured)

# Global rather than locals of `main`: the cleanup trap runs after `main` has
# returned and its locals are gone, so a local here would leave the trap reading
# an unset name — an "unbound variable" error on the way out of a good install,
# and a temp directory left behind every time.
WORK_DIR=""
STAGED=()
VERIFIED=()
CONFIG_CREATED=0
CONFIG_USABLE=1
SERVICE_STATUS=""

say() { printf '%s\n' "$*"; }
err() { printf '%s\n' "$*" >&2; }

need() {
  command -v "$1" >/dev/null 2>&1 || {
    err "pasture: $1 is required but was not found on PATH"
    exit 1
  }
}

# Remove the temp download directory and any staged install file on the way out,
# whether the install succeeded or failed.
cleanup() {
  if [ -n "${WORK_DIR:-}" ]; then
    rm -rf "$WORK_DIR"
  fi
  local staged
  for staged in "${STAGED[@]:-}"; do
    if [ -n "$staged" ]; then
      rm -f "$staged"
    fi
  done
}

detect_os() {
  case "$(uname -s)" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    # Git Bash, MSYS2 and Cygwin all report a Windows kernel here. Such a shell
    # can run this script perfectly well, but pasture publishes no native
    # Windows build, so there is nothing correct to fetch.
    MINGW* | MSYS* | CYGWIN*)
      err "pasture: this looks like Windows, and pasture publishes no native Windows build."
      err ""
      err "to install the linux build, run this inside WSL; to build from source, see"
      err "https://github.com/${REPO}"
      exit 1
      ;;
    *)
      err "pasture: unsupported operating system: $(uname -s)"
      err "pasture publishes linux and macOS builds — see https://github.com/${REPO}/releases"
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf 'amd64' ;;
    aarch64 | arm64) printf 'arm64' ;;
    *)
      err "pasture: unsupported architecture: $(uname -m)"
      err "pasture publishes amd64 and arm64 builds"
      exit 1
      ;;
  esac
}

# The newest STABLE release tag.
#
# /releases/latest redirects to /releases/tag/<tag>, which costs one HEAD and is
# not rate limited. GitHub excludes drafts and pre-releases from that redirect
# by definition, so it can never select a pre-release. The API endpoint
# /releases/latest is the fallback and is stable-only for the same reason; the
# /releases index, which can list pre-releases newest first, is deliberately not
# used. The API allows 60 unauthenticated requests per hour per IP, which is why
# it is not the default.
latest_tag() {
  local url
  url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" 2>/dev/null || true)"

  case "$url" in
    */releases/tag/*)
      printf '%s' "${url##*/tag/}"
      return 0
      ;;
  esac

  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
    grep -m1 '"tag_name"' |
    sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
}

# A user systemd session is the only supervisor the installer knows how to
# wire. `systemctl --user show-environment` is the cheapest probe that works
# without starting anything; a container or a WSL image without systemd says
# no here, and the install continues without a unit.
systemd_user_available() {
  command -v systemctl >/dev/null 2>&1 || return 1
  systemctl --user show-environment >/dev/null 2>&1
}

# One decision, used by the plan, the installer, and the result. The plan is
# the consent surface: it must not promise a start that install_service will
# deliberately skip.
#
# `keep-running` is the manual-daemon case. Two pastured processes on one
# pasture.db share the same DBOS executor identity and recovery cohort, so the
# installer refuses to add a unit-managed one behind a manual one.
service_disposition() {
  local os="$1"

  if [ "$os" != "linux" ]; then
    printf 'skip-nonlinux'
    return 0
  fi
  if [ "${PASTURE_NO_SERVICE:-}" = "1" ]; then
    printf 'skip-optout'
    return 0
  fi
  if [ -L "$CONFIG_FILE" ] && [ ! -e "$CONFIG_FILE" ]; then
    printf 'skip-config'
    return 0
  fi
  if [ -e "$CONFIG_FILE" ] && { [ ! -f "$CONFIG_FILE" ] || [ ! -r "$CONFIG_FILE" ]; }; then
    printf 'skip-config'
    return 0
  fi
  if ! systemd_user_available; then
    printf 'skip-no-systemd'
    return 0
  fi
  if [ -L "$SERVICE_FILE" ] || { [ -e "$SERVICE_FILE" ] && ! grep -qF "$SERVICE_MARKER" "$SERVICE_FILE" 2>/dev/null; }; then
    printf 'keep-foreign'
    return 0
  fi
  if ! systemctl --user is-active --quiet pastured >/dev/null 2>&1; then
    if command -v pgrep >/dev/null 2>&1 && pgrep -x pastured >/dev/null 2>&1; then
      printf 'keep-running'
      return 0
    fi
  fi
  printf 'install'
}

# Everything that is about to happen, before any of it does. Reading a plan is
# the only way to consent to one, and an installer that has already started is
# not asking.
show_plan() {
  local tag="$1" os="$2" arch="$3" sums_url="$4" disposition="$5"
  local b verb

  say ""
  say "  pasture ${tag}"
  say ""
  say "  home found     ${HOME}"
  say "  machine        ${os}/${arch}"
  say "  checksum       ${sums_url}"
  for b in "${BINARIES[@]}"; do
    say "  source         https://github.com/${REPO}/releases/download/${tag}/${b}-${os}-${arch}"
  done
  say "  download to    ${TMPDIR:-/tmp}  (temporary, removed when finished)"
  for b in "${BINARIES[@]}"; do
    verb="install"
    [ -e "${BIN_DIR}/${b}" ] && verb="replace"
    say "  ${verb}     ${BIN_DIR}/${b}"
  done
  say "  config         ${CONFIG_FILE}  (create if absent)"
  case "$disposition" in
    install)
      say "  service        ${SERVICE_FILE}  (install, enable, start; set PASTURE_NO_SERVICE=1 to skip)"
      ;;
    keep-foreign)
      say "  service        ${SERVICE_FILE}  (kept: existing unit is not installer-managed)"
      ;;
    keep-running)
      say "  service        ${SERVICE_FILE}  (kept: a pastured process already runs outside the installer)"
      ;;
    skip-optout)
      say "  service        skipped (PASTURE_NO_SERVICE=1)"
      ;;
    skip-nonlinux)
      say "  service        skipped (systemd is Linux-only)"
      ;;
    skip-no-systemd)
      say "  service        skipped (no user systemd session)"
      ;;
    skip-config)
      say "  service        skipped (the daemon config is not readable)"
      ;;
  esac
  say ""
  say "  here are the steps we will take to install on your machine:"
  say "    1. download  pasture-${os}-${arch} and pastured-${os}-${arch}"
  say "    2. verify    both against checksums.txt published with the release"
  say "    3. install   ${BIN_DIR}/pasture and ${BIN_DIR}/pastured"
  say "    4. create    ${CONFIG_FILE} if it does not exist"
  if [ "$disposition" = "install" ]; then
    say "    5. install   ${SERVICE_FILE}, then enable and start pastured"
  fi
  say ""
}

# A yes, from the person at the keyboard.
#
# stdin is the script itself when this is piped into bash, so the answer has to
# come from the terminal directly. When there is no terminal — CI, a nested
# pipe, a cron job — there is nobody to ask, so it stops rather than assuming
# consent it never got. `-t 1` is the signal: piping the script in leaves stdin
# a pipe but stdout still a terminal.
confirm() {
  if [ "${PASTURE_YES:-}" = "1" ]; then
    say "  continuing without asking (PASTURE_YES=1)"
    say ""
    return 0
  fi

  if [ ! -t 1 ] || [ ! -r /dev/tty ]; then
    err "  no terminal to ask for confirmation, so nothing was changed."
    err "  re-run with PASTURE_YES=1 to install without the prompt."
    exit 1
  fi

  local reply=""
  printf '  continue? [y/N] '
  read -r reply < /dev/tty || true
  say ""

  case "$reply" in
    y | Y | yes | YES) return 0 ;;
    *)
      say "  nothing was changed."
      exit 0
      ;;
  esac
}

# Refuse to install anything whose hash we have not matched against the one
# published alongside it. HTTPS covers the hop; this covers the artifact. The
# checksums file is downloaded once and reused across both binaries.
verify() {
  local dir="$1" asset="$2" tag="$3" sums_url="$4"
  local sums="${dir}/checksums.txt" want got

  if [ ! -f "$sums" ]; then
    curl -fsSL "$sums_url" -o "$sums" || {
      err "pasture: could not download checksums.txt for ${tag}"
      err "refusing to install an unverified binary"
      exit 1
    }
  fi

  # The workflow writes "<sha256>  <filename>"; compare the name as a whole
  # field rather than as a pattern, since it is full of regex metacharacters.
  # The leading "*" is sha256sum's binary-mode marker — not what the workflow
  # emits, but cheap to tolerate and otherwise a silent no-match.
  want="$(awk -v want="$asset" \
    '{ name = $2; sub(/^\*/, "", name); if (name == want) print $1 }' "$sums")"
  [ -n "$want" ] || {
    err "pasture: checksums.txt for ${tag} does not list ${asset}"
    exit 1
  }

  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "${dir}/${asset}" | awk '{ print $1 }')"
  elif command -v shasum >/dev/null 2>&1; then
    got="$(shasum -a 256 "${dir}/${asset}" | awk '{ print $1 }')"
  else
    err "pasture: no sha256sum or shasum available to verify the download"
    exit 1
  fi

  [ "$want" = "$got" ] || {
    err "pasture: checksum mismatch for ${asset}"
    err "  expected ${want}"
    err "  got      ${got}"
    exit 1
  }

  printf '%s' "$got"
}

# Copy a verified download beside its destination and mark it executable. A raw
# download carries no exec bit, and a same-directory rename later avoids
# ETXTBSY when the running binary is the one being replaced.
#
# The staging path is recorded in STAGED BEFORE cp runs: a failure in cp or in
# chmod must still leave the path known to the EXIT trap, or the partially
# created file survives the run. rm -f tolerates a path cp never created.
stage_binary() {
  local dir="$1" asset="$2" b="$3"
  local staged="${BIN_DIR}/.pasture-install.$$.${b}"
  STAGED+=("$staged")
  cp "${dir}/${asset}" "$staged"
  chmod 0755 "$staged"
}

# Create the daemon's minimal config only when the user has none. An existing
# file, including a symlink managed by a dotfiles system, is left untouched:
# the daemon only needs a readable YAML file, and the installer must not
# clobber a config the user owns.
#
# The DB path is deliberately not a config key here: pastured takes it from
# --db / PASTURE_DB_PATH and overwrites the config value with that resolved
# path, so advertising `db_path` would document a key the daemon ignores.
install_config() {
  if [ -L "$CONFIG_FILE" ] && [ ! -e "$CONFIG_FILE" ]; then
    CONFIG_USABLE=0
    return 0
  fi
  if [ -e "$CONFIG_FILE" ]; then
    if [ -f "$CONFIG_FILE" ] && [ -r "$CONFIG_FILE" ]; then
      CONFIG_USABLE=1
    else
      CONFIG_USABLE=0
    fi
    return 0
  fi

  mkdir -p "$CONFIG_DIR" || return 1
  local staged="${CONFIG_FILE}.pasture-install.$$"
  STAGED+=("$staged")
  cat > "$staged" <<'YAML' || return 1
# Pasture configuration. Defaults apply for anything not set here.
# The daemon's DB path comes from --db / PASTURE_DB_PATH (default:
# ~/.local/share/pasture/pasture.db); it is not read from this file.
audit_trail: sqlite
YAML
  chmod 0600 "$staged" || return 1
  # Re-check immediately before the rename: if another owner created the file
  # while we were staging, keep theirs rather than clobbering it.
  if [ -e "$CONFIG_FILE" ] || [ -L "$CONFIG_FILE" ]; then
    if [ -f "$CONFIG_FILE" ] && [ -r "$CONFIG_FILE" ]; then
      CONFIG_USABLE=1
    else
      CONFIG_USABLE=0
    fi
    return 0
  fi
  mv "$staged" "$CONFIG_FILE" || return 1
  CONFIG_CREATED=1
  CONFIG_USABLE=1
}

# Install the user unit that keeps the daemon running. The unit names the
# stable BIN_DIR path, not a versioned store path, so `pasture upgrade` can
# replace the binary without touching the unit; restarting the unit is the
# only step an upgrade needs. An optional EnvironmentFile lets a user put
# PASTURE_DB_PATH (or another variable) in ~/.config/pasture/pastured.env
# without editing the installer-owned unit.
#
# The disposition was decided once before the plan, so the plan, the action,
# and the result cannot drift. An existing unit is adopted only when it carries
# this installer's marker; a symlink (Home Manager, for example) or a
# hand-written unit is left alone. Marked units are installer-owned: local
# edits to the unit are replaced on the next run; use a drop-in for local
# additions.
install_service() {
  local disposition="$1"

  if [ "$CONFIG_USABLE" -eq 0 ]; then
    disposition="skip-config"
  fi

  case "$disposition" in
    install) ;;
    keep-foreign)
      SERVICE_STATUS="kept (existing unit is not installer-managed)"
      return 0
      ;;
    keep-running)
      SERVICE_STATUS="kept (a pastured process is already running outside the installer)"
      err "  warning: a pastured process is already running outside the installer."
      err "  stop it, or set PASTURE_NO_SERVICE=1, before re-running: two daemons on one pasture.db share the same executor cohort."
      return 0
      ;;
    skip-optout)
      SERVICE_STATUS="skipped (PASTURE_NO_SERVICE=1)"
      return 0
      ;;
    skip-nonlinux)
      SERVICE_STATUS="skipped (systemd is Linux-only)"
      return 0
      ;;
    skip-no-systemd)
      SERVICE_STATUS="skipped (no user systemd session)"
      return 0
      ;;
    skip-config)
      SERVICE_STATUS="skipped (the daemon config is not readable; fix ${CONFIG_FILE} and re-run)"
      return 0
      ;;
    *)
      SERVICE_STATUS="skipped (unknown disposition: ${disposition})"
      return 0
      ;;
  esac

  mkdir -p "$SYSTEMD_USER_DIR" || return 1
  local staged="${SERVICE_FILE}.pasture-install.$$"
  STAGED+=("$staged")
  cat > "$staged" <<UNIT || return 1
${SERVICE_MARKER}
[Unit]
Description=Pasture DBOS durable engine and lifecycle write lane

[Service]
Type=simple
ExecStart="${BIN_DIR}/pastured"
EnvironmentFile=-%h/.config/pasture/pastured.env
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
UNIT
  chmod 0644 "$staged" || return 1
  # Re-check immediately before the rename for the same reason as the config:
  # a foreign unit created during staging must not be clobbered.
  if [ -L "$SERVICE_FILE" ] || { [ -e "$SERVICE_FILE" ] && ! grep -qF "$SERVICE_MARKER" "$SERVICE_FILE" 2>/dev/null; }; then
    SERVICE_STATUS="kept (existing unit is not installer-managed)"
    return 0
  fi
  mv "$staged" "$SERVICE_FILE" || return 1

  if ! systemctl --user daemon-reload; then
    SERVICE_STATUS="installed, but the user systemd manager did not reload"
    err "  warning: ${SERVICE_FILE} was installed, but systemd did not reload."
    err "  run: systemctl --user daemon-reload"
    return 0
  fi
  if ! systemctl --user enable pastured; then
    SERVICE_STATUS="installed, but could not be enabled"
    err "  warning: ${SERVICE_FILE} was installed, but pastured.service could not be enabled."
    err "  run: systemctl --user enable pastured"
    return 0
  fi
  if ! systemctl --user restart pastured; then
    SERVICE_STATUS="installed, but could not be started"
    err "  warning: ${SERVICE_FILE} was installed, but pastured.service could not be started."
    err "  run: systemctl --user restart pastured"
    return 0
  fi
  if ! systemctl --user is-active --quiet pastured; then
    SERVICE_STATUS="installed and enabled, but not active (check: journalctl --user -u pastured)"
    err "  warning: pastured.service was installed and enabled, but it is not active."
    err "  run: systemctl --user status pastured; journalctl --user -u pastured -n 50"
    return 0
  fi
  SERVICE_STATUS="installed, enabled and running"
}

# What happened, in the same shape as what was promised.
show_result() {
  local b size sum

  say ""
  say "  done."
  say ""
  for b in "${BINARIES[@]}"; do
    size="$(du -h "${BIN_DIR}/${b}" 2>/dev/null | awk '{ print $1 }' || true)"
    say "  installed  ${BIN_DIR}/${b}${size:+  (${size})}"
  done
  for sum in "${VERIFIED[@]:-}"; do
    [ -n "$sum" ] && say "  verified   sha256 ${sum}"
  done
  if [ "$CONFIG_CREATED" -eq 1 ]; then
    say "  created    ${CONFIG_FILE}"
  elif [ "$CONFIG_USABLE" -eq 1 ]; then
    say "  kept       ${CONFIG_FILE}"
  else
    say "  kept       ${CONFIG_FILE} (not readable)"
  fi
  if [ -n "$SERVICE_STATUS" ]; then
    say "  service    pastured.service: ${SERVICE_STATUS}"
  fi
  say ""

  case ":${PATH}:" in
    *":${BIN_DIR}:"*)
      say "  next:  pasture --help"
      ;;
    *)
      local profile=""
      case "${SHELL:-}" in
        */zsh) profile="${HOME}/.zshrc" ;;
        */bash) profile="${HOME}/.bashrc" ;;
        *) profile="" ;;
      esac

      say "  ${BIN_DIR} is not on your PATH yet."
      if [ -n "$profile" ]; then
        say "  run these commands:"
        say ""
        say "      printf '\\nexport PATH=\"\$HOME/.local/bin:\$PATH\"\\n' >> \"${profile}\""
        say "      source \"${profile}\""
      else
        say "  add this line to your shell configuration file:"
        say ""
        say "      export PATH=\"\$HOME/.local/bin:\$PATH\""
        say ""
        say "  then run it in this terminal:"
        say ""
        say "      export PATH=\"\$HOME/.local/bin:\$PATH\""
      fi
      say ""
      say "  next:  pasture --help"
      ;;
  esac
  say ""
}

main() {
  need curl
  need uname

  local os arch tag sums_url b asset sum disposition
  os="$(detect_os)"
  arch="$(detect_arch)"
  disposition="$(service_disposition "$os")"

  # `|| true` is load-bearing. Under `set -e`, a command substitution that fails
  # inside an assignment exits the shell then and there — so when GitHub rate
  # limits us or is simply unreachable, the script would die silently on this
  # line and never reach the message below it.
  if [ -n "${PASTURE_VERSION:-}" ]; then
    tag="$PASTURE_VERSION"
  else
    tag="$(latest_tag || true)"
  fi

  [ -n "$tag" ] || {
    err "pasture: could not determine the latest release of ${REPO}"
    err "pin one with PASTURE_VERSION=v0.0.16"
    exit 1
  }

  sums_url="https://github.com/${REPO}/releases/download/${tag}/checksums.txt"
  show_plan "$tag" "$os" "$arch" "$sums_url" "$disposition"
  confirm

  WORK_DIR="$(mktemp -d)"
  trap cleanup EXIT

  say "  downloading…"
  for b in "${BINARIES[@]}"; do
    asset="${b}-${os}-${arch}"
    curl -fsSL "https://github.com/${REPO}/releases/download/${tag}/${asset}" -o "${WORK_DIR}/${asset}" || {
      err "pasture: could not download ${asset}"
      err "  https://github.com/${REPO}/releases/download/${tag}/${asset}"
      err "if the release is not public yet, this is expected — try again shortly"
      exit 1
    }
  done

  # Verify BOTH downloads before either binary is replaced.
  say "  verifying…"
  for b in "${BINARIES[@]}"; do
    asset="${b}-${os}-${arch}"
    sum="$(verify "$WORK_DIR" "$asset" "$tag" "$sums_url")"
    VERIFIED+=("${b}:${sum:0:16}…")
  done

  mkdir -p "$BIN_DIR"
  # Stage both verified binaries beside their targets, then rename both.
  for b in "${BINARIES[@]}"; do
    asset="${b}-${os}-${arch}"
    stage_binary "$WORK_DIR" "$asset" "$b"
  done
  for b in "${BINARIES[@]}"; do
    mv -f "${BIN_DIR}/.pasture-install.$$.${b}" "${BIN_DIR}/${b}"
  done

  if ! install_config; then
    err "pasture: both binaries were installed, but the daemon config at ${CONFIG_FILE} could not be created."
    err "  fix the path and re-run install.sh."
    exit 1
  fi
  if ! install_service "$disposition"; then
    err "pasture: both binaries and the daemon config were installed, but the user service step failed."
    err "  fix the error above and re-run install.sh."
    exit 1
  fi
  show_result
}

main "$@"
