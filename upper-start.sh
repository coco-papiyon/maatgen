#!/usr/bin/env bash
# Starts Maatgen as an upper node: same as start.sh, plus a
# dedicated --relay-listen so lower nodes on other machines can dial in.
# Usage: ./upper-start.sh [port] [relay-port]
set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir"

port="${1:-3200}"
relay_port="${2:-$((port + 1))}"

stop_port_listener() {
  local target_port="$1"
  local pids=""
  local pid

  listener_is_alive() {
    for pid in $pids; do
      if kill -0 "$pid" 2>/dev/null; then
        return 0
      fi
    done
    return 1
  }

  if command -v lsof >/dev/null 2>&1; then
    pids="$(lsof -nP -tiTCP:"$target_port" -sTCP:LISTEN 2>/dev/null || true)"
  elif command -v fuser >/dev/null 2>&1; then
    pids="$(fuser "${target_port}/tcp" 2>/dev/null || true)"
  else
    echo "Cannot check port ${target_port}: install lsof or fuser." >&2
    return 1
  fi

  if [[ -z "${pids//[[:space:]]/}" ]]; then
    return 0
  fi

  echo "Port ${target_port} is in use. Stopping listener PID(s): ${pids//$'\n'/ }"
  # shellcheck disable=SC2086 # pids intentionally expands to separate numeric arguments.
  if ! kill $pids 2>/dev/null; then
    echo "Failed to stop the process using port ${target_port}." >&2
    return 1
  fi

  for _ in {1..20}; do
    if ! listener_is_alive; then
      return 0
    fi
    sleep 0.25
  done

  echo "Listener did not stop gracefully; sending SIGKILL."
  for pid in $pids; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
  done
  sleep 0.1
  if listener_is_alive; then
    echo "Failed to kill the process using port ${target_port}." >&2
    return 1
  fi
}

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
if ! stop_port_listener "$port" || ! stop_port_listener "$relay_port"; then
  echo "Maatgen was not started."
  exit 1
fi
echo "Open http://127.0.0.1:${port}/ in your browser."
echo "Lower nodes should use: --upstream-url ws://<this-host>:${relay_port}/api/relay/connect"
exec "$script_dir/apps/agent-manager/agent-manager" \
  --config "config/providers.json" \
  --static-dir "$script_dir/apps/web/dist" \
  --data-dir "$script_dir/.maatgen" \
  --port "$port" \
  --relay-listen "0.0.0.0:${relay_port}"
