#!/usr/bin/env bash
set -euo pipefail

NATS_URL="${NATS_URL:-nats://localhost:4222}"
export NATS_URL

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -r "$TMP_DIR"' EXIT

for file in "$SCRIPT_DIR"/streams/*.json; do
  # --- Create stream ---
  stream=$(jq -r '.stream.name' "$file")
  if nats stream info "$stream" >/dev/null 2>&1; then
    echo "Stream $stream already exists, skipping"
    continue
  fi
  jq '.stream' "$file" > "$TMP_DIR/stream.json"
  nats stream add "$stream" --config "$TMP_DIR/stream.json"

  # --- Create consumers ---
  jq -c '.consumers // [] | .[]' "$file" | while IFS= read -r consumer; do
    name=$(echo "$consumer" | jq -r '.durable_name')
    echo "$consumer" | jq 'del(.seed_ack)' > "$TMP_DIR/consumer.json"
    nats consumer add "$stream" "$name" --config "$TMP_DIR/consumer.json"
  done

  # --- Publish messages ---
  jq -c '.messages // [] | .[]' "$file" | while IFS= read -r msg; do
    subject=$(echo "$msg" | jq -r '.subject')
    body=$(echo "$msg" | jq -c '.body')
    header_args=()
    while IFS= read -r header; do
      [ -n "$header" ] && header_args+=(-H "$header")
    done < <(echo "$msg" | jq -r '.headers // {} | to_entries[] | "\(.key):\(.value)"')
    nats pub "$subject" "$body" --jetstream "${header_args[@]+"${header_args[@]}"}"
  done

  # --- Acknowledge messages, so consumers show delivery progress ---
  jq -r '.consumers // [] | .[] | select(.seed_ack > 0) | "\(.durable_name) \(.seed_ack)"' "$file" | while read -r name count; do
    nats consumer next "$stream" "$name" --count "$count" --ack --raw </dev/null >/dev/null
  done
done
