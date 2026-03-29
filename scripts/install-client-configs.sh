#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

SERVER_NAME="memory-mcp"
CONFIG_PATH="$ROOT_DIR/mcp-dev.yaml"
BINARY_PATH="$ROOT_DIR/bin/memory-mcp"
CRSQLITE_PATH="${CRSQLITE_PATH:-$ROOT_DIR/.tools/crsqlite/crsqlite.dylib}"
SQLITE_VEC_PATH="${SQLITE_VEC_PATH:-$ROOT_DIR/.tools/sqlite-vec/vec0.dylib}"
TARGETS="local,claude,codex"

usage() {
  cat <<'EOF'
Usage: scripts/install-client-configs.sh [options]

Options:
  --targets <list>        Comma-separated targets: local,claude,codex
  --server-name <name>    MCP server name to register
  --config <path>         Path to config yaml passed to memory-mcp
  --binary <path>         Path to memory-mcp binary
  --crsqlite <path>       Path to crsqlite extension
  --sqlite-vec <path>     Path to sqlite-vec extension
  --help                  Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --targets)
      TARGETS="${2:?missing value for --targets}"
      shift 2
      ;;
    --server-name)
      SERVER_NAME="${2:?missing value for --server-name}"
      shift 2
      ;;
    --config)
      CONFIG_PATH="${2:?missing value for --config}"
      shift 2
      ;;
    --binary)
      BINARY_PATH="${2:?missing value for --binary}"
      shift 2
      ;;
    --crsqlite)
      CRSQLITE_PATH="${2:?missing value for --crsqlite}"
      shift 2
      ;;
    --sqlite-vec)
      SQLITE_VEC_PATH="${2:?missing value for --sqlite-vec}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      printf 'unknown option: %s\n' "$1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ ! -x "$BINARY_PATH" ]]; then
  printf 'binary not found or not executable: %s\n' "$BINARY_PATH" >&2
  printf 'build it first with: make build-mcp\n' >&2
  exit 1
fi

if [[ ! -f "$CONFIG_PATH" ]]; then
  printf 'config file not found: %s\n' "$CONFIG_PATH" >&2
  exit 1
fi

export ROOT_DIR SERVER_NAME CONFIG_PATH BINARY_PATH CRSQLITE_PATH SQLITE_VEC_PATH TARGETS

python3 <<'PY'
import json
import os
import platform
import re
from pathlib import Path


def ensure_parent(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)


def write_json(path: Path, payload: dict) -> None:
    ensure_parent(path)
    path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")


def load_json(path: Path) -> dict:
    if not path.exists():
        return {}
    text = path.read_text(encoding="utf-8").strip()
    if not text:
        return {}
    return json.loads(text)


def quoted(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def toml_array(values: list[str]) -> str:
    return "[" + ", ".join(quoted(v) for v in values) + "]"


def toml_inline_table(entries: dict[str, str]) -> str:
    body = ", ".join(f"{key} = {quoted(value)}" for key, value in entries.items())
    return "{ " + body + " }"


def upsert_managed_block(path: Path, block: str, marker_name: str) -> None:
    ensure_parent(path)
    begin = f"# BEGIN managed by crdt-agent-memory: {marker_name}"
    end = f"# END managed by crdt-agent-memory: {marker_name}"
    wrapped = f"{begin}\n{block.rstrip()}\n{end}\n"

    existing = path.read_text(encoding="utf-8") if path.exists() else ""
    pattern = re.compile(re.escape(begin) + r"\n.*?" + re.escape(end) + r"\n?", re.DOTALL)
    if pattern.search(existing):
        updated = pattern.sub(wrapped, existing, count=1)
    else:
        if existing and not existing.endswith("\n"):
            existing += "\n"
        updated = existing + ("\n" if existing else "") + wrapped
    path.write_text(updated, encoding="utf-8")


root_dir = Path(os.environ["ROOT_DIR"]).resolve()
server_name = os.environ["SERVER_NAME"]
config_path = str(Path(os.environ["CONFIG_PATH"]).resolve())
binary_path = str(Path(os.environ["BINARY_PATH"]).resolve())
crsqlite_path = str(Path(os.environ["CRSQLITE_PATH"]).resolve())
sqlite_vec_path = str(Path(os.environ["SQLITE_VEC_PATH"]).resolve())
targets = {
    target.strip()
    for target in os.environ["TARGETS"].split(",")
    if target.strip()
}

unknown_targets = targets.difference({"local", "claude", "codex"})
if unknown_targets:
    raise SystemExit(f"unsupported targets: {', '.join(sorted(unknown_targets))}")

entry = {
    "command": binary_path,
    "args": ["--config", config_path],
    "env": {
        "CRSQLITE_PATH": crsqlite_path,
        "SQLITE_VEC_PATH": sqlite_vec_path,
    },
}

if "local" in targets:
    local_path = root_dir / ".mcp" / "config.json"
    write_json(local_path, {"mcpServers": {server_name: entry}})
    print(f"updated local MCP config: {local_path}")

if "claude" in targets:
    system = platform.system().lower()
    home = Path.home()
    if system == "darwin":
        claude_path = home / "Library" / "Application Support" / "Claude" / "claude_desktop_config.json"
    else:
        claude_path = home / ".config" / "Claude" / "claude_desktop_config.json"
    payload = load_json(claude_path)
    mcp_servers = payload.setdefault("mcpServers", {})
    mcp_servers[server_name] = entry
    write_json(claude_path, payload)
    print(f"updated Claude Desktop config: {claude_path}")

if "codex" in targets:
    codex_path = Path.home() / ".codex" / "config.toml"
    block = "\n".join(
        [
            f'[mcp_servers.{quoted(server_name)}]',
            f'command = {quoted(binary_path)}',
            f'args = {toml_array(["--config", config_path])}',
            f'env = {toml_inline_table({"CRSQLITE_PATH": crsqlite_path, "SQLITE_VEC_PATH": sqlite_vec_path})}',
            "startup_timeout_ms = 60000",
        ]
    )
    upsert_managed_block(codex_path, block, server_name)
    print(f"updated Codex config: {codex_path}")
PY
