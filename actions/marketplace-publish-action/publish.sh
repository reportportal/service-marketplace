#!/usr/bin/env bash
set -euo pipefail

TOKEN=$(curl -s -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
  "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=service-marketplace" | jq -r .value)

JAR=$(ls -1 ${JAR_PATH} 2>/dev/null | head -1)
if [[ -z "${JAR}" || ! -f "${JAR}" ]]; then
  echo "jar not found: ${JAR_PATH}" >&2
  exit 1
fi

URL="${REGISTRY_URL%/}/api/v1/plugins"
if [[ "${FIRST_VERSION}" != "true" ]]; then
  if [[ -z "${PLUGIN_ID}" ]]; then
    echo "plugin-id required for version publish" >&2
    exit 1
  fi
  URL="${URL}/${PLUGIN_ID}/versions"
fi

ARGS=(-f "jar=@${JAR}")
if [[ -n "${CHANGELOG_PATH:-}" && -f "${CHANGELOG_PATH}" ]]; then
  ARGS+=(-F "changelog=@${CHANGELOG_PATH}")
fi

HTTP=$(curl -sS -o /tmp/mp-response.json -w '%{http_code}' \
  -H "Authorization: Bearer ${TOKEN}" \
  "${ARGS[@]}" \
  "${URL}")

echo "HTTP ${HTTP}"
cat /tmp/mp-response.json
if [[ "${HTTP}" != "201" ]]; then
  exit 1
fi
