#!/usr/bin/env bash
#
# Hermetic proof for the installer's config and user-service behavior:
#
#   - config created only when absent; existing regular and symlinked configs
#     are preserved byte-for-byte
#   - marked unit installed with the stable ExecStart and an optional
#     EnvironmentFile; marked units are refreshed on re-run
#   - symlinked and unmarked foreign units are left alone
#   - no unit when systemd is unavailable, when the user opts out, on a
#     non-Linux platform, or when the config is dangling/unreadable
#   - daemon-reload/enable/restart/is-active failures warn and continue
#   - a manual pastured process is not joined by a second daemon
#   - XDG_CONFIG_HOME relocates the unit, while the config stays at
#     ~/.config/pasture
#
# The release tree is served from a local directory through file:// URLs and
# systemctl/pgrep/uname shims are put on PATH, so the check needs no network,
# no systemd, no running daemon, and no real release.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

assets="$work/assets/v0.0.16"
mkdir -p "$assets"
for os in linux darwin; do
  printf 'pasture-%s-bytes\n' "$os" > "$assets/pasture-${os}-amd64"
  printf 'pastured-%s-bytes\n' "$os" > "$assets/pastured-${os}-amd64"
done
( cd "$assets" && sha256sum pasture-*-amd64 pastured-*-amd64 > checksums.txt )

installer="$work/install-local.sh"
cp "$repo_root/install.sh" "$installer"
sed -i "s#https://github.com/\${REPO}/releases/download#file://${assets%/*}#g" "$installer"
grep -qF "file://${assets%/*}" "$installer" || {
  echo "FAIL: the local release rewrite did not apply" >&2
  exit 1
}
if grep -qF 'https://github.com/dayvidpham/pasture/releases/download' "$installer"; then
  echo "FAIL: the installer still reaches the real release URL" >&2
  exit 1
fi

shim="$work/shim"
mkdir -p "$shim"

cat > "$shim/systemctl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${SYSTEMCTL_LOG:?}"
sub="${2:-}"
case ":${SYSTEMCTL_FAIL:-}:" in
  *":${sub}:"*) exit 1 ;;
esac
if [ "$sub" = "is-active" ]; then
  [ "${SYSTEMCTL_ACTIVE:-1}" = "1" ] && exit 0
  exit 3
fi
exit 0
STUB

cat > "$shim/pgrep" <<'STUB'
#!/usr/bin/env bash
exit "${PGREP_STATUS:-1}"
STUB

cat > "$shim/uname" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
  -s) printf '%s\n' "${UNAME_S:-Linux}" ;;
  -m) printf '%s\n' "${UNAME_M:-x86_64}" ;;
  *) printf '%s\n' "${UNAME_S:-Linux}" ;;
esac
STUB

chmod +x "$shim/systemctl" "$shim/pgrep" "$shim/uname"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

run_install() {
  local home="$1" log="$2"
  shift 2
  mkdir -p "$home"
  : > "$log"
  env PATH="$shim:$PATH" HOME="$home" XDG_CONFIG_HOME="$home/.config" \
    PASTURE_YES=1 PASTURE_VERSION=v0.0.16 SYSTEMCTL_LOG="$log" \
    SHELL=/bin/bash "$@" bash "$installer" > "$home/install.log" 2>&1 ||
    { cat "$home/install.log"; fail "installer exited non-zero"; }
}

assert_installed() {
  local home="$1"
  [ -x "$home/.local/bin/pasture" ] || fail "pasture was not installed"
  [ -x "$home/.local/bin/pastured" ] || fail "pastured was not installed"
}

assert_no_staged() {
  local home="$1" found
  found="$(find "$home" -name '.pasture-install.*' -print)"
  [ -z "$found" ] || fail "staging leftovers: $found"
}

assert_no_service_actions() {
  local log="$1"
  if grep -qE -- '--user (daemon-reload|enable|restart)' "$log"; then
    fail "unexpected service action: $(grep -E -- '--user (daemon-reload|enable|restart)' "$log" | tr '\n' ' ')"
  fi
}

# Fresh install: config created, marked unit installed, enabled and running.
home="$work/home-fresh"
log="$work/fresh.log"
run_install "$home" "$log"
assert_installed "$home"
assert_no_staged "$home"

config="$home/.config/pasture/config.yaml"
[ -f "$config" ] || fail "config was not created"
grep -qx 'audit_trail: sqlite' "$config" || fail "config does not set audit_trail: sqlite"
if grep -qx 'db_path:.*' "$config"; then fail "config advertises a key pastured ignores"; fi
[ "$(stat -c %a "$config")" = "600" ] || fail "config mode is not 600"

unit="$home/.config/systemd/user/pastured.service"
[ -f "$unit" ] || fail "user unit was not installed"
grep -qF '# Managed by the Pasture installer.' "$unit" || fail "unit lacks the installer marker"
grep -qF "ExecStart=\"$home/.local/bin/pastured\"" "$unit" || fail "unit does not run the installed binary"
grep -qF 'EnvironmentFile=-%h/.config/pasture/pastured.env' "$unit" || fail "unit does not read the optional env file"
[ "$(stat -c %a "$unit")" = "644" ] || fail "unit mode is not 644"
[ "$(grep -c '^After=' "$unit")" = "0" ] || fail "unit keeps a no-op After=default.target"

grep -qF -- '--user daemon-reload' "$log" || fail "systemd was not reloaded"
grep -qF -- '--user enable pastured' "$log" || fail "pastured was not enabled"
grep -qF -- '--user restart pastured' "$log" || fail "pastured was not started"
grep -qF -- '--user is-active --quiet pastured' "$log" || fail "the running state was not checked"
grep -qF 'created    ' "$home/install.log" || fail "the result did not report the config"
grep -qF 'installed, enabled and running' "$home/install.log" || fail "the result did not report the service"
grep -qF 'set PASTURE_NO_SERVICE=1 to skip' "$home/install.log" || fail "the plan did not offer the opt-out"

# Existing config is user-owned and must survive byte-for-byte.
home="$work/home-existing-config"
mkdir -p "$home/.config/pasture"
printf 'audit_trail: memory\n' > "$home/.config/pasture/config.yaml"
cp "$home/.config/pasture/config.yaml" "$work/existing-config.expected"
run_install "$home" "$work/existing-config.log"
cmp -s "$work/existing-config.expected" "$home/.config/pasture/config.yaml" ||
  fail "an existing config was overwritten"

# A marked unit is installer-owned and is refreshed on re-run.
home="$work/home-marked-unit"
mkdir -p "$home/.config/systemd/user"
cat > "$home/.config/systemd/user/pastured.service" <<'UNIT'
# Managed by the Pasture installer.
[Service]
ExecStart=/stale/pastured
UNIT
run_install "$home" "$work/marked-unit.log"
grep -qF "ExecStart=\"$home/.local/bin/pastured\"" "$home/.config/systemd/user/pastured.service" ||
  fail "a marked unit was not refreshed"
grep -qF -- '--user restart pastured' "$work/marked-unit.log" ||
  fail "a refreshed unit was not restarted"

# An unmarked unit belongs to another manager; leave it alone.
home="$work/home-unmanaged-unit"
mkdir -p "$home/.config/systemd/user"
printf '[Service]\nExecStart=/custom/pastured\n' > "$home/.config/systemd/user/pastured.service"
cp "$home/.config/systemd/user/pastured.service" "$work/unmanaged-unit.expected"
run_install "$home" "$work/unmanaged-unit.log"
cmp -s "$work/unmanaged-unit.expected" "$home/.config/systemd/user/pastured.service" ||
  fail "an unmanaged unit was overwritten"
assert_no_service_actions "$work/unmanaged-unit.log"
grep -qF 'existing unit is not installer-managed' "$home/install.log" ||
  fail "the result did not report the kept foreign unit"

# A symlinked unit is another manager's file; never replace the link.
home="$work/home-symlink-unit"
mkdir -p "$home/dotfiles" "$home/.config/systemd/user"
printf '[Service]\nExecStart=/dotfiles/pastured\n' > "$home/dotfiles/pastured.service"
ln -s "$home/dotfiles/pastured.service" "$home/.config/systemd/user/pastured.service"
cp "$home/dotfiles/pastured.service" "$work/symlink-unit.expected"
run_install "$home" "$work/symlink-unit.log"
[ -L "$home/.config/systemd/user/pastured.service" ] || fail "a symlinked unit was replaced"
cmp -s "$work/symlink-unit.expected" "$home/dotfiles/pastured.service" ||
  fail "a symlinked unit's target was changed"
assert_no_service_actions "$work/symlink-unit.log"

# A symlinked config pointing at a real file is preserved.
home="$work/home-symlink-config"
mkdir -p "$home/dotfiles" "$home/.config/pasture"
printf 'audit_trail: memory\n' > "$home/dotfiles/config.yaml"
ln -s "$home/dotfiles/config.yaml" "$home/.config/pasture/config.yaml"
run_install "$home" "$work/symlink-config.log"
[ -L "$home/.config/pasture/config.yaml" ] || fail "a symlinked config was replaced"
cmp -s <(printf 'audit_trail: memory\n') "$home/dotfiles/config.yaml" ||
  fail "a symlinked config's target was changed"

# A dangling config symlink cannot feed the daemon; do not start a crash loop.
home="$work/home-dangling-config"
mkdir -p "$home/.config/pasture"
ln -s "$home/dotfiles/missing.yaml" "$home/.config/pasture/config.yaml"
run_install "$home" "$work/dangling-config.log"
[ -L "$home/.config/pasture/config.yaml" ] || fail "a dangling config symlink was replaced"
[ ! -e "$home/dotfiles/missing.yaml" ] || fail "a dangling config target was created"
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "a unit was installed for a dangling config"
assert_no_service_actions "$work/dangling-config.log"
grep -qF 'not readable' "$home/install.log" || fail "the result did not report the unreadable config"

# A directory at the config path is not a readable config either.
home="$work/home-config-directory"
mkdir -p "$home/.config/pasture/config.yaml"
run_install "$home" "$work/config-directory.log"
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "a unit was installed for a directory config"
assert_no_service_actions "$work/config-directory.log"
grep -qF 'not readable' "$home/install.log" || fail "the result did not report the directory config"

# No user systemd session: binaries and config still install, no unit.
home="$work/home-no-systemd"
run_install "$home" "$work/no-systemd.log" SYSTEMCTL_FAIL=show-environment
assert_installed "$home"
[ -f "$home/.config/pasture/config.yaml" ] || fail "no-systemd skipped the config"
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "no-systemd installed a unit"
assert_no_service_actions "$work/no-systemd.log"
grep -qF 'no user systemd session' "$home/install.log" || fail "the result did not report the missing session"

# Each service step failure warns, exits 0, and does not attempt later steps.
for step in daemon-reload enable restart; do
  home="$work/home-fail-$step"
  run_install "$home" "$work/fail-$step.log" SYSTEMCTL_FAIL="$step"
  [ -f "$home/.config/systemd/user/pastured.service" ] || fail "fail-$step did not install the unit"
  grep -qF 'warning:' "$home/install.log" || fail "fail-$step did not warn"
done
if grep -qF -- '--user restart pastured' "$work/fail-enable.log"; then
  fail "enable failure still attempted restart"
fi

# A restart that leaves the unit inactive is reported honestly.
home="$work/home-inactive"
run_install "$home" "$work/inactive.log" SYSTEMCTL_ACTIVE=0
grep -qF 'not active' "$home/install.log" || fail "an inactive unit was reported as running"

# A manual pastured process must not be joined by a unit-managed second one.
home="$work/home-manual"
run_install "$home" "$work/manual.log" PGREP_STATUS=0 SYSTEMCTL_ACTIVE=0
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "a second daemon was installed"
grep -qF 'already running outside the installer' "$home/install.log" ||
  fail "the manual-daemon warning was not printed"
assert_no_service_actions "$work/manual.log"

# PASTURE_NO_SERVICE=1 installs binaries and config but no unit.
home="$work/home-no-service"
run_install "$home" "$work/no-service.log" PASTURE_NO_SERVICE=1
assert_installed "$home"
config="$home/.config/pasture/config.yaml"
grep -qx 'audit_trail: sqlite' "$config" || fail "opt-out skipped the config"
[ "$(stat -c %a "$config")" = "600" ] || fail "opt-out config mode is not 600"
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "opt-out installed a unit"
[ ! -s "$work/no-service.log" ] || fail "opt-out invoked systemctl"

# XDG_CONFIG_HOME moves the unit; the config stays at ~/.config/pasture.
home="$work/home-xdg"
xdg="$home/xdg"
run_install "$home" "$work/xdg.log" XDG_CONFIG_HOME="$xdg"
[ -f "$xdg/systemd/user/pastured.service" ] || fail "the unit did not follow XDG_CONFIG_HOME"
[ -f "$home/.config/pasture/config.yaml" ] || fail "the config did not stay at ~/.config/pasture"
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "the unit was written to the non-XDG path"

# macOS: binaries and config only; systemd is not probed.
home="$work/home-darwin"
run_install "$home" "$work/darwin.log" UNAME_S=Darwin
assert_installed "$home"
[ -f "$home/.config/pasture/config.yaml" ] || fail "darwin skipped the config"
[ ! -e "$home/.config/systemd/user/pastured.service" ] || fail "darwin installed a systemd unit"
[ ! -s "$work/darwin.log" ] || fail "darwin invoked systemctl"
grep -qF 'systemd is Linux-only' "$home/install.log" || fail "darwin did not report the platform limit"

echo "installer config and user service: all cases passed"
