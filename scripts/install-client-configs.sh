#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

SERVER_NAME="memory-mcp"
CONFIG_PATH="$ROOT_DIR/mcp-dev.yaml"
BINARY_PATH="$ROOT_DIR/bin/memory-mcp"
TARGETS="local,claude,codex"
CREATE_MISSING_DIRS=0
GO_BIN="${GO_BIN:-}"

case "$(uname -s)" in
  Darwin)
    DEFAULT_CRSQLITE_PATH="$ROOT_DIR/.tools/crsqlite/crsqlite.dylib"
    DEFAULT_SQLITE_VEC_PATH="$ROOT_DIR/.tools/sqlite-vec/vec0.dylib"
    ;;
  Linux)
    DEFAULT_CRSQLITE_PATH="$ROOT_DIR/.tools/crsqlite/crsqlite.so"
    DEFAULT_SQLITE_VEC_PATH="$ROOT_DIR/.tools/sqlite-vec/vec0.so"
    ;;
  *)
    DEFAULT_CRSQLITE_PATH="$ROOT_DIR/.tools/crsqlite/crsqlite"
    DEFAULT_SQLITE_VEC_PATH="$ROOT_DIR/.tools/sqlite-vec/vec0"
    ;;
esac

CRSQLITE_PATH="${CRSQLITE_PATH:-$DEFAULT_CRSQLITE_PATH}"
SQLITE_VEC_PATH="${SQLITE_VEC_PATH:-$DEFAULT_SQLITE_VEC_PATH}"

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
  --create-missing-dirs   Create client config directories if missing
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
    --create-missing-dirs)
      CREATE_MISSING_DIRS=1
      shift
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

if [[ -z "$GO_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  elif [[ -x /opt/homebrew/bin/go ]]; then
    GO_BIN="/opt/homebrew/bin/go"
  else
    printf 'go binary not found; set GO_BIN or use make install-mcp-clients\n' >&2
    exit 1
  fi
fi

if [[ ! -x "$GO_BIN" ]]; then
  printf 'go binary not executable: %s\n' "$GO_BIN" >&2
  exit 1
fi

if [[ ! -f "$CRSQLITE_PATH" ]]; then
  printf 'crsqlite extension not found: %s\n' "$CRSQLITE_PATH" >&2
  exit 1
fi

if [[ ! -f "$SQLITE_VEC_PATH" ]]; then
  printf 'sqlite-vec extension not found: %s\n' "$SQLITE_VEC_PATH" >&2
  exit 1
fi

export ROOT_DIR SERVER_NAME CONFIG_PATH BINARY_PATH CRSQLITE_PATH SQLITE_VEC_PATH TARGETS CREATE_MISSING_DIRS GO_BIN

python3 <<'PY'
import json
import os
import platform
import re
import shutil
import subprocess
import tempfile
from pathlib import Path


def warn(message: str) -> None:
    print(f"warning: {message}")


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


def ensure_target_dir(path: Path, create_missing_dirs: bool) -> bool:
    parent = path.parent
    if parent.exists():
        return True
    if not create_missing_dirs:
        warn(f"skip {path}: parent directory does not exist")
        return False
    parent.mkdir(parents=True, exist_ok=True)
    return True


def backup_path_for(path: Path) -> Path:
    return path.with_name(path.name + ".bak")


def validate_json_file(path: Path) -> None:
    json.loads(path.read_text(encoding="utf-8"))


def validate_toml_file(path: Path, go_bin: str, root_dir: Path) -> None:
    proc = subprocess.run(
        [go_bin, "run", "./scripts/validate_toml.go", str(path)],
        cwd=root_dir,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip() or "TOML validation failed")


def atomic_write(path: Path, content: str, validator) -> None:
    fd, tmp_name = tempfile.mkstemp(prefix=path.name + ".", suffix=".tmp", dir=path.parent)
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(content)
        validator(tmp_path)
        if path.exists():
            shutil.copy2(path, backup_path_for(path))
        os.replace(tmp_path, path)
    except Exception:
        tmp_path.unlink(missing_ok=True)
        raise


def write_json(path: Path, payload: dict, create_missing_dirs: bool) -> bool:
    if not ensure_target_dir(path, create_missing_dirs):
        return False
    content = json.dumps(payload, indent=2) + "\n"
    atomic_write(path, content, validate_json_file)
    return True


def upsert_managed_block(existing: str, block: str, marker_name: str) -> str:
    begin = f"# BEGIN managed by crdt-agent-memory: {marker_name}"
    end = f"# END managed by crdt-agent-memory: {marker_name}"
    wrapped = f"{begin}\n{block.rstrip()}\n{end}\n"
    pattern = re.compile(re.escape(begin) + r"\n.*?" + re.escape(end) + r"\n?", re.DOTALL)
    if pattern.search(existing):
        return pattern.sub(wrapped, existing, count=1)
    if existing and not existing.endswith("\n"):
        existing += "\n"
    return existing + ("\n" if existing else "") + wrapped


root_dir = Path(os.environ["ROOT_DIR"]).resolve()
server_name = os.environ["SERVER_NAME"]
config_path = str(Path(os.environ["CONFIG_PATH"]).resolve())
binary_path = str(Path(os.environ["BINARY_PATH"]).resolve())
crsqlite_path = str(Path(os.environ["CRSQLITE_PATH"]).resolve())
sqlite_vec_path = str(Path(os.environ["SQLITE_VEC_PATH"]).resolve())
go_bin = os.environ["GO_BIN"]
create_missing_dirs = os.environ["CREATE_MISSING_DIRS"] == "1"
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
    payload = load_json(local_path)
    mcp_servers = payload.setdefault("mcpServers", {})
    mcp_servers[server_name] = entry
    if write_json(local_path, payload, True):
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
    if write_json(claude_path, payload, create_missing_dirs):
        print(f"updated Claude Desktop config: {claude_path}")

if "codex" in targets:
    codex_path = Path.home() / ".codex" / "config.toml"
    if ensure_target_dir(codex_path, create_missing_dirs):
        block = "\n".join(
            [
                f'[mcp_servers.{quoted(server_name)}]',
                f'command = {quoted(binary_path)}',
                f'args = {toml_array(["--config", config_path])}',
                f'env = {toml_inline_table({"CRSQLITE_PATH": crsqlite_path, "SQLITE_VEC_PATH": sqlite_vec_path})}',
                "startup_timeout_ms = 60000",
            ]
        )
        existing = codex_path.read_text(encoding="utf-8") if codex_path.exists() else ""
        updated = upsert_managed_block(existing, block, server_name)
        atomic_write(codex_path, updated, lambda path: validate_toml_file(path, go_bin, root_dir))
        print(f"updated Codex config: {codex_path}")
PY
