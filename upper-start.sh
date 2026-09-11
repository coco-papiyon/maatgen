#!/usr/bin/env bash
# Starts Maatgen as an upper node (ADR-009): same as start.sh, plus a
# dedicated --relay-listen so lower nodes on other machines can dial in.
# Usage: ./upper-start.sh [port] [relay-port]
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir"

port="${1:-3100}"
relay_port="${2:-$((port + 1))}"

echo "=== Building Maatgen ==="
if ! corepack pnpm build; then
  echo
  echo "Build failed. Maatgen was not started."
  exit 1
fi

echo
echo "=== Building Agent Manager executable ==="
if ! go -C "$script_dir/apps/agent-manager" build -o "$script_dir/apps/agent-manager/agent-manager" ./cmd/agent-manager; then
  echo
  echo "Agent Manager build failed. Maatgen was not started."
  exit 1
fi

echo
echo "=== Starting Maatgen (upper node) ==="
echo "Open http://127.0.0.1:${port}/ in your browser."
echo "Lower nodes should use: --upstream-url ws://<this-host>:${relay_port}/api/relay/connect"
exec "$script_dir/apps/agent-manager/agent-manager" \
  --config "config/providers.json" \
  --static-dir "$script_dir/apps/web/dist" \
  --data-dir "$script_dir/.maatgen" \
  --port "$port" \
  --relay-listen "0.0.0.0:${relay_port}"
