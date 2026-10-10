#!/usr/bin/env bash
# Installs or upgrades deploy-agent from GitHub Releases. Run on
# the target VPS as root.
#
# Self-contained: this script does not read any sibling file, so copying
# just this one file to the VPS (scp, or fetching its raw content from the
# git remote) is enough - no need to clone the whole repository. E.g.:
#
#   scp deploy/install.sh vps:/tmp/install.sh
#   ssh -t vps bash /tmp/install.sh {version}
#
# (the systemd unit it installs, deploy/deploy-agent.service in this repo,
# is embedded below as a heredoc; keep the two in sync when editing either)
#
# Usage:
#   install.sh [version] [setup flags...]
#
# If version is omitted, "latest" is installed. Business pipelines must
# never rely on "latest" - this script is for installing/upgrading the
# agent itself, run by a human on the VPS.
#
# After installing, the script hands over to `deploy-agent setup`, the
# interactive wizard that writes serve.env, initializes the CA and can
# generate services.yaml; any remaining arguments are passed to it, e.g.
#
#   install.sh 1.3.0 --yes --ip 10.0.0.5 --extra-ips 10.0.0.6
#
# Without a terminal (ssh without -t) and without --yes, setup only prints
# the next steps.
set -euo pipefail

REPO_URL="https://github.com/krwu/compose-deploy-agent"
VERSION="latest"
if [[ $# -gt 0 && "$1" != -* ]]; then
  VERSION="$1"
  shift
fi
if [[ "$VERSION" == "latest" ]]; then
  ASSET_BASE="${REPO_URL}/releases/latest/download"
else
  ASSET_BASE="${REPO_URL}/releases/download/${VERSION}"
fi
BIN_PATH="/usr/local/bin/deploy-agent"
UNIT_PATH="/etc/systemd/system/deploy-agent.service"

if [[ "$(id -u)" -ne 0 ]]; then
  echo "install.sh must be run as root" >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

echo "downloading deploy-agent ${VERSION} ..."
# GitHub release assets redirect to release-assets.githubusercontent.com;
# --location is required or curl silently "succeeds" with an empty body.
curl --fail --location --silent --show-error -o "${tmp_dir}/deploy-agent" "${ASSET_BASE}/deploy-agent"

# Verify the download against the sha256sum file published next to the binary.
echo "verifying checksum ..."
curl --fail --location --silent --show-error -o "${tmp_dir}/deploy-agent.sha256" "${ASSET_BASE}/deploy-agent.sha256"
expected_sum="$(awk '{print tolower($1)}' "${tmp_dir}/deploy-agent.sha256")"
if [[ ! "$expected_sum" =~ ^[0-9a-f]{64}$ ]]; then
  echo "invalid checksum file for ${ASSET_BASE}/deploy-agent" >&2
  exit 1
fi
actual_sum="$(sha256sum "${tmp_dir}/deploy-agent" | awk '{print $1}')"
if [[ "$expected_sum" != "$actual_sum" ]]; then
  echo "checksum mismatch: expected ${expected_sum}, got ${actual_sum}" >&2
  exit 1
fi

chmod 0755 "${tmp_dir}/deploy-agent"
mv "${tmp_dir}/deploy-agent" "$BIN_PATH"
echo "installed ${BIN_PATH} ($("$BIN_PATH" version))"

mkdir -p /etc/deploy-agent /var/lib/deploy-agent
chmod 0700 /var/lib/deploy-agent

if [[ ! -f "$UNIT_PATH" ]]; then
  # Keep in sync with deploy/deploy-agent.service in the repository. The
  # 'EOF' is quoted so ${DEPLOY_AGENT_BIND} is written verbatim, to be
  # expanded later by systemd's EnvironmentFile, not by this script.
  cat > "$UNIT_PATH" <<'EOF'
[Unit]
Description=deploy-agent - compose service auto-update agent
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/deploy-agent serve --bind ${DEPLOY_AGENT_BIND} --manifest /etc/deploy-agent/services.yaml --data-dir /var/lib/deploy-agent
EnvironmentFile=-/etc/deploy-agent/serve.env
Restart=on-failure
RestartSec=5
# Private 0700 directory for the local unix socket used by
# `deploy-agent update` (/run/deploy-agent/deploy-agent.sock).
RuntimeDirectory=deploy-agent
RuntimeDirectoryMode=0700
# agent needs docker.sock and full compose-project directory access, both
# equivalent to root on the host - it is not
# sandboxed further.
User=root

[Install]
WantedBy=multi-user.target
EOF
  echo "installed systemd unit at ${UNIT_PATH}"
  unit_is_new=1
else
  echo "systemd unit already present at ${UNIT_PATH}"
  unit_is_new=0
fi

# Versions before `setup` existed: keep the manual instructions. The help
# text is captured first: under pipefail, grep -q exiting early could fail
# the pipeline with SIGPIPE.
help_text="$("$BIN_PATH" help 2>&1 || true)"
if ! grep -q '^  setup' <<<"$help_text"; then
  if [[ "$unit_is_new" -eq 1 ]]; then
    echo "edit /etc/deploy-agent/serve.env and services.yaml, then:"
    echo "  systemctl daemon-reload && systemctl enable --now deploy-agent"
  else
    echo "restart to pick up the new binary:"
    echo "  systemctl restart deploy-agent"
  fi
  exit 0
fi

# exec replaces this shell, so the EXIT trap would never run.
rm -rf "$tmp_dir"
trap - EXIT
# With `curl ... | bash` stdin is the script itself; answer the wizard's
# questions from the controlling terminal instead, when there is one.
if [[ ! -t 0 ]] && { : </dev/tty; } 2>/dev/null; then
  exec "$BIN_PATH" setup "$@" </dev/tty
fi
exec "$BIN_PATH" setup "$@"
