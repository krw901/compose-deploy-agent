#!/usr/bin/env bash
# Template for the business pipeline's script task. This project's own
# deploy-agent credential (DEPLOY_AGENT_CREDENTIAL) is expected to already
# be injected as an environment variable by the pipeline's credential
# binding, using the multi-line PEM bundle output by `deploy-agent ca issue`.
#
# Required environment variables:
#   DEPLOY_AGENT_CREDENTIAL   multi-line PEM bundle (client cert + key + CA)
#   DEPLOY_AGENT_ENDPOINT     e.g. https://10.0.0.5:8866
#   DEPLOY_AGENT_SERVICE      logical service name from services.yaml
#   DEPLOY_AGENT_TAG          immutable tag, e.g. commit SHA or build number
#
# Business pipelines MUST pin DEPLOY_AGENT_CLIENT_VERSION to a fixed
# version - never "latest" - so that an agent-side release never silently
# changes client behavior underneath an unrelated pipeline.
set -euo pipefail

: "${DEPLOY_AGENT_CREDENTIAL:?DEPLOY_AGENT_CREDENTIAL is required}"
: "${DEPLOY_AGENT_ENDPOINT:?DEPLOY_AGENT_ENDPOINT is required}"
: "${DEPLOY_AGENT_SERVICE:?DEPLOY_AGENT_SERVICE is required}"
: "${DEPLOY_AGENT_TAG:?DEPLOY_AGENT_TAG is required}"
: "${DEPLOY_AGENT_CLIENT_VERSION:?DEPLOY_AGENT_CLIENT_VERSION is required, e.g. 1.0.0 (never 'latest')}"

BASE_URL="https://github.com/krwu/compose-deploy-agent/releases/download"
bin="$(mktemp -d)/deploy-agent"

# GitHub release assets redirect to release-assets.githubusercontent.com;
# --location is required or curl silently "succeeds" with an empty body.
curl --fail --location --silent --show-error -o "$bin" "${BASE_URL}/${DEPLOY_AGENT_CLIENT_VERSION}/deploy-agent"
chmod 0755 "$bin"

"$bin" deploy \
  --endpoint "$DEPLOY_AGENT_ENDPOINT" \
  --service "$DEPLOY_AGENT_SERVICE" \
  --tag "$DEPLOY_AGENT_TAG"
# deploy-agent deploy exits non-zero on any non-success/noop result, and
# credentials are read only from DEPLOY_AGENT_CREDENTIAL and never echoed -
# this script must not print $DEPLOY_AGENT_CREDENTIAL for the same reason.
