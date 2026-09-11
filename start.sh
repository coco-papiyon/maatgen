#!/usr/bin/env bash
# Linux/macOS equivalent of start.bat. Usage: ./start.sh [port]
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir"

port="${1:-3100}"

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
echo "=== Starting Maatgen ==="
echo "Open http://127.0.0.1:${port}/ in your browser."
exec "$script_dir/apps/agent-manager/agent-manager" \
  --config "config/providers.json" \
  --static-dir "$script_dir/apps/web/dist" \
  --data-dir "$script_dir/.maatgen" \
  --port "$port"
