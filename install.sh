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
# ~/.local/bin. Both binaries are downloaded and verified before either is
# replaced, so the CLI and the daemon always match. It never escalates and never
# edits a shell profile; where PATH needs a line added, it prints the line for
# you to add. The internal release tool (pasture-release) is never fetched.
#
#   PASTURE_YES=1        install without the prompt (for CI and scripts)
#   PASTURE_VERSION=vX   install a specific release instead of the newest
#
# The whole script is a set of functions with `main` called on the very last
# line. Piped into a shell, a script executes whatever bytes have arrived — so a
# connection that drops midway through would otherwise run half an install. This
# way a truncated transfer defines some functions and does nothing at all.

set -euo pipefail

REPO="dayvidpham/pasture"
BIN_DIR="${HOME}/.local/bin"

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

# Everything that is about to happen, before any of it does. Reading a plan is
# the only way to consent to one, and an installer that has already started is
# not asking.
show_plan() {
  local tag="$1" os="$2" arch="$3" sums_url="$4"
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
  say ""
  say "  here are the steps we will take to install on your machine:"
  say "    1. download  pasture-${os}-${arch} and pastured-${os}-${arch}"
  say "    2. verify    both against checksums.txt published with the release"
  say "    3. install   ${BIN_DIR}/pasture and ${BIN_DIR}/pastured"
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
stage_binary() {
  local dir="$1" asset="$2" b="$3"
  local staged="${BIN_DIR}/.pasture-install.$$.${b}"
  cp "${dir}/${asset}" "$staged"
  chmod 0755 "$staged"
  STAGED+=("$staged")
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

  local os arch tag sums_url b asset sum
  os="$(detect_os)"
  arch="$(detect_arch)"

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
  show_plan "$tag" "$os" "$arch" "$sums_url"
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

  show_result
}

main "$@"
