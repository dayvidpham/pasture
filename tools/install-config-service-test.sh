#!/usr/bin/env bash
#
# Hermetic proof that install.sh creates the daemon config when it is absent,
# never overwrites an existing config, installs a marked user unit that runs the
# stable installed binary, and skips the unit when asked or when another owner
# already supplies one. The release tree is served from a local directory
# through file:// URLs and a recording `systemctl` shim is put on PATH, so the
# check needs no network, no systemd, and no real release.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

assets="$work/assets/v0.0.16"
mkdir -p "$assets"
printf 'pasture-bytes\n' > "$assets/pasture-linux-amd64"
printf 'pastured-bytes\n' > "$assets/pastured-linux-amd64"
( cd "$assets" && sha256sum pasture-linux-amd64 pastured-linux-amd64 > checksums.txt )

installer="$work/install-local.sh"
cp "$repo_root/install.sh" "$installer"
sed -i "s#https://github.com/\${REPO}/releases/download#file://${assets%/*}#g" "$installer"

shim="$work/shim"
mkdir -p "$shim"
cat > "$shim/systemctl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${SYSTEMCTL_LOG:?}"
exit 0
STUB
chmod +x "$shim/systemctl"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

run_install() {
  local home="$1" log="$2"
  shift 2
  mkdir -p "$home"
  : > "$log"
  env PATH="$shim:$PATH" HOME="$home" PASTURE_YES=1 PASTURE_VERSION=v0.0.16 \
    SYSTEMCTL_LOG="$log" SHELL=/bin/bash "$@" \
    bash "$installer" > "$home/install.log" 2>&1 ||
    { cat "$home/install.log"; fail "installer exited non-zero"; }
}

assert_installed() {
  local home="$1"
  [ -x "$home/.local/bin/pasture" ] || fail "pasture was not installed"
  [ -x "$home/.local/bin/pastured" ] || fail "pastured was not installed"
}

# Fresh install: config created, marked unit installed, enabled and started.
home="$work/home-fresh"
log="$work/fresh.log"
run_install "$home" "$log"
assert_installed "$home"

config="$home/.config/pasture/config.yaml"
[ -f "$config" ] || fail "config was not created"
grep -qx 'audit_trail: sqlite' "$config" || fail "config does not set audit_trail: sqlite"
[ "$(stat -c %a "$config")" = "600" ] || fail "config mode is not 600"

unit="$home/.config/systemd/user/pastured.service"
[ -f "$unit" ] || fail "user unit was not installed"
grep -qF '# Managed by the Pasture installer.' "$unit" || fail "unit lacks the installer marker"
grep -qF "ExecStart=\"$home/.local/bin/pastured\"" "$unit" || fail "unit does not run the installed binary"
grep -qF -- '--user daemon-reload' "$log" || fail "systemd was not reloaded"
grep -qF -- '--user enable pastured' "$log" || fail "pastured was not enabled"
grep -qF -- '--user restart pastured' "$log" || fail "pastured was not started"

# Existing config is user-owned and must survive byte-for-byte.
home="$work/home-existing-config"
mkdir -p "$home/.config/pasture"
printf 'audit_trail: memory\n' > "$home/.config/pasture/config.yaml"
run_install "$home" "$work/existing-config.log"
[ "$(cat "$home/.config/pasture/config.yaml")" = "audit_trail: memory" ] ||
  fail "an existing config was overwritten"

# An existing unmarked unit belongs to another manager; leave it alone.
home="$work/home-unmanaged-unit"
mkdir -p "$home/.config/systemd/user"
printf '[Service]\nExecStart=/custom/pastured\n' > "$home/.config/systemd/user/pastured.service"
run_install "$home" "$work/unmanaged-unit.log"
grep -qF 'ExecStart=/custom/pastured' "$home/.config/systemd/user/pastured.service" ||
  fail "an unmanaged unit was overwritten"
if grep -qF -- '--user enable pastured' "$work/unmanaged-unit.log"; then
  fail "an unmanaged unit was enabled"
fi

# PASTURE_NO_SERVICE=1 installs the binaries and config but no unit.
home="$work/home-no-service"
run_install "$home" "$work/no-service.log" PASTURE_NO_SERVICE=1
assert_installed "$home"
[ -f "$home/.config/pasture/config.yaml" ] || fail "opt-out skipped the config"
if [ -e "$home/.config/systemd/user/pastured.service" ]; then
  fail "opt-out still installed a unit"
fi
if grep -q -- '--user' "$work/no-service.log"; then
  fail "opt-out still invoked systemctl"
fi

echo "installer config and user service: all cases passed"
