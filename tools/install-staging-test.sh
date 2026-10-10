#!/usr/bin/env bash
#
# Hermetic proof that install.sh removes the staging file it created when the
# copy or the chmod step fails, and installs neither binary. The release tree is
# served from a local directory through file:// URLs and a failing shim is put
# on PATH, so the check needs no network and no real release.
#
# The defect this pins: the staging path must be recorded for the EXIT trap
# BEFORE cp runs, or a cp/chmod failure leaves `.pasture-install.<pid>.<binary>`
# behind in the bin directory.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

assets="$work/assets/v0.0.16"
mkdir -p "$assets"
printf 'pasture-bytes\n' > "$assets/pasture-linux-amd64"
printf 'pastured-bytes\n' > "$assets/pastured-linux-amd64"
( cd "$assets" && sha256sum pasture-linux-amd64 pastured-linux-amd64 > checksums.txt )

# install.sh with the release download base pointed at the local tree.
installer="$work/install-local.sh"
cp "$repo_root/install.sh" "$installer"
sed -i "s#https://github.com/\${REPO}/releases/download#file://${assets%/*}#g" "$installer"

real_cp="$(command -v cp)"
real_chmod="$(command -v chmod)"

run_case() {
  local name="$1" fail_command="$2"
  local shim="$work/shim-$name" home="$work/home-$name"
  mkdir -p "$shim" "$home/.local/bin"

  local real_cmd
  case "$fail_command" in
    cp) real_cmd="$real_cp" ;;
    chmod) real_cmd="$real_chmod" ;;
    *)
      echo "FAIL: unknown shim command $fail_command"
      exit 1
      ;;
  esac

  # A shim that fails only for the installer's staging file and delegates
  # everything else to the real command. The real path is baked in at
  # generation time because the shim runs as its own process.
  cat > "$shim/$fail_command" <<EOF
#!/usr/bin/env bash
for arg in "\$@"; do
  case "\$arg" in
    *.pasture-install.*) exit 1 ;;
  esac
done
exec "$real_cmd" "\$@"
EOF
  chmod +x "$shim/$fail_command"

  set +e
  PATH="$shim:$PATH" PASTURE_VERSION=v0.0.16 PASTURE_YES=1 HOME="$home" SHELL=/bin/bash \
    bash "$installer" > "$work/$name.log" 2>&1
  local rc=$?
  set -e

  if [ "$rc" -eq 0 ]; then
    echo "FAIL: $name expected a non-zero exit"
    cat "$work/$name.log"
    exit 1
  fi

  local leftover
  leftover="$(find "$home/.local/bin" -maxdepth 1 -name '.pasture-install.*' -print)"
  if [ -n "$leftover" ]; then
    echo "FAIL: $name left a staging file behind: $leftover"
    exit 1
  fi
  if [ -e "$home/.local/bin/pasture" ] || [ -e "$home/.local/bin/pastured" ]; then
    echo "FAIL: $name installed a binary despite the failure"
    exit 1
  fi

  echo "ok: $name (exit $rc, no staging leftovers, neither binary installed)"
}

run_case copy cp
run_case chmod chmod

echo "installer staging cleanup: all cases passed"
