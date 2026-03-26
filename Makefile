SHELL := /bin/bash

GO_BIN ?= /opt/homebrew/bin/go
GOFLAGS ?= -tags sqlite_fts5
TMP_BASE ?= /tmp/crdt-agent-memory-dev
TOOLS_DIR ?= $(CURDIR)/.tools
CRSQLITE_DIR := $(TOOLS_DIR)/crsqlite
SQLITE_VEC_DIR := $(TOOLS_DIR)/sqlite-vec
PEER_A_CONFIG := $(TMP_BASE)/peer-a/config.yaml
PEER_B_CONFIG := $(TMP_BASE)/peer-b/config.yaml

.PHONY: help bootstrap-dev check-deps test setup-dev-configs migrate-peer-a migrate-peer-b diag-peer-a diag-peer-b serve-peer-a serve-peer-b index-peer-a index-peer-b sync-peer-a sync-peer-b smoke-sync smoke-e2e-manual clean-dev

help:
	@printf "%s\n" \
	"make bootstrap-dev     Download cr-sqlite and sqlite-vec into .tools/" \
	"make check-deps        Verify Go and extension files" \
	"make test              Run all Go tests" \
	"make setup-dev-configs Copy sample configs into $(TMP_BASE)" \
	"make migrate-peer-a    Run migrations for peer-a" \
	"make migrate-peer-b    Run migrations for peer-b" \
	"make diag-peer-a       Show metadata for peer-a" \
	"make diag-peer-b       Show metadata for peer-b" \
	"make serve-peer-a      Start memoryd for peer-a" \
	"make serve-peer-b      Start memoryd for peer-b" \
	"make index-peer-a      Start indexd for peer-a" \
	"make index-peer-b      Start indexd for peer-b" \
	"make sync-peer-a       Start syncd for peer-a" \
	"make sync-peer-b       Start syncd for peer-b" \
	"make smoke-sync        Run a one-shot 2-peer sync smoke test" \
	"make smoke-e2e-manual  Run full manual smoke flow (store/sync/recall/status)" \
	"make clean-dev         Remove $(TMP_BASE)"

bootstrap-dev:
	mkdir -p "$(CRSQLITE_DIR)" "$(SQLITE_VEC_DIR)"
	curl -fsSL https://github.com/vlcn-io/cr-sqlite/releases/download/v0.16.3/crsqlite-darwin-aarch64.zip -o "$(CRSQLITE_DIR)/crsqlite.zip"
	unzip -oq "$(CRSQLITE_DIR)/crsqlite.zip" -d "$(CRSQLITE_DIR)"
	curl -fsSL https://github.com/asg017/sqlite-vec/releases/download/v0.1.6/sqlite-vec-0.1.6-loadable-macos-aarch64.tar.gz -o "$(SQLITE_VEC_DIR)/sqlite-vec.tar.gz"
	tar -xzf "$(SQLITE_VEC_DIR)/sqlite-vec.tar.gz" -C "$(SQLITE_VEC_DIR)"

check-deps:
	test -x "$(GO_BIN)"
	test -f "$(CRSQLITE_DIR)/crsqlite.dylib"
	test -f "$(SQLITE_VEC_DIR)/vec0.dylib"

test: check-deps
	PATH=/opt/homebrew/bin:$$PATH CRSQLITE_PATH="$(CRSQLITE_DIR)/crsqlite.dylib" SQLITE_VEC_PATH="$(SQLITE_VEC_DIR)/vec0.dylib" "$(GO_BIN)" test $(GOFLAGS) ./...

setup-dev-configs: bootstrap-dev
	mkdir -p "$(TMP_BASE)/peer-a" "$(TMP_BASE)/peer-b"
	cp configs/peer-a.yaml.example "$(PEER_A_CONFIG)"
	cp configs/peer-b.yaml.example "$(PEER_B_CONFIG)"
	bash scripts/setup-keys.sh "$(TMP_BASE)"

migrate-peer-a: setup-dev-configs
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_A_CONFIG)" --cmd migrate

migrate-peer-b: setup-dev-configs
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_B_CONFIG)" --cmd migrate

diag-peer-a: setup-dev-configs
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_A_CONFIG)" --cmd diag

diag-peer-b: setup-dev-configs
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_B_CONFIG)" --cmd diag

serve-peer-a: setup-dev-configs migrate-peer-a
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_A_CONFIG)"

serve-peer-b: setup-dev-configs migrate-peer-b
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_B_CONFIG)"

index-peer-a: setup-dev-configs migrate-peer-a
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/indexd --config "$(PEER_A_CONFIG)"

index-peer-b: setup-dev-configs migrate-peer-b
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/indexd --config "$(PEER_B_CONFIG)"

sync-peer-a: setup-dev-configs migrate-peer-a
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/syncd --config "$(PEER_A_CONFIG)"

sync-peer-b: setup-dev-configs migrate-peer-b
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/syncd --config "$(PEER_B_CONFIG)"

smoke-sync: migrate-peer-a migrate-peer-b
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/syncd --config "$(PEER_A_CONFIG)" --once
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/syncd --config "$(PEER_B_CONFIG)" --once

smoke-e2e-manual: clean-dev setup-dev-configs migrate-peer-a migrate-peer-b
	@set -euo pipefail; \
	pick_free_port() { \
		local p="$$1"; \
		while lsof -nP -iTCP:"$$p" -sTCP:LISTEN >/dev/null 2>&1; do \
			p=$$((p+1)); \
		done; \
		echo "$$p"; \
	}; \
	API_A_PORT=$$(pick_free_port 4101); \
	API_B_PORT=$$(pick_free_port $$((API_A_PORT+1))); \
	SYNC_A_PORT=$$(pick_free_port 4201); \
	SYNC_B_PORT=$$(pick_free_port $$((SYNC_A_PORT+1))); \
	sed -i '' -e "s#127.0.0.1:3101#127.0.0.1:$$API_A_PORT#g" -e "s#127.0.0.1:3201#127.0.0.1:$$SYNC_A_PORT#g" -e "s#127.0.0.1:3202#127.0.0.1:$$SYNC_B_PORT#g" "$(PEER_A_CONFIG)"; \
	sed -i '' -e "s#127.0.0.1:3102#127.0.0.1:$$API_B_PORT#g" -e "s#127.0.0.1:3202#127.0.0.1:$$SYNC_B_PORT#g" -e "s#127.0.0.1:3201#127.0.0.1:$$SYNC_A_PORT#g" "$(PEER_B_CONFIG)"; \
	sed -i '' -E 's#signing_public_key: ".*"#signing_public_key: "c96c5a7dcbe46299db6d31f5bbdd9e2aad4d8cf2c255f9249b79f246ab703c5d"#' "$(PEER_A_CONFIG)"; \
	sed -i '' -E 's#signing_public_key: ".*"#signing_public_key: "94e1db860da5fd064970847a5e13b54d2548e62881e66ef17414a4a16c43b605"#' "$(PEER_B_CONFIG)"; \
	mkdir -p "$(TMP_BASE)/logs"; \
	printf 'crdt-agent-memory/peer-a' | shasum -a 256 | awk '{print $$1}' >"$(TMP_BASE)/peer-a/peer.key"; \
	printf 'crdt-agent-memory/peer-b' | shasum -a 256 | awk '{print $$1}' >"$(TMP_BASE)/peer-b/peer.key"; \
	chmod 600 "$(TMP_BASE)/peer-a/peer.key" "$(TMP_BASE)/peer-b/peer.key"; \
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_A_CONFIG)" >"$(TMP_BASE)/logs/memoryd-peer-a.log" 2>&1 & \
	PID_MEM_A=$$!; \
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/memoryd --config "$(PEER_B_CONFIG)" >"$(TMP_BASE)/logs/memoryd-peer-b.log" 2>&1 & \
	PID_MEM_B=$$!; \
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/syncd --config "$(PEER_B_CONFIG)" >"$(TMP_BASE)/logs/syncd-peer-b.log" 2>&1 & \
	PID_SYNC_B=$$!; \
	cleanup() { \
		kill $$PID_SYNC_B $$PID_MEM_B $$PID_MEM_A >/dev/null 2>&1 || true; \
		wait $$PID_SYNC_B $$PID_MEM_B $$PID_MEM_A >/dev/null 2>&1 || true; \
	}; \
	trap cleanup EXIT INT TERM; \
	wait_http() { \
		local url="$$1"; \
		local attempts=40; \
		until curl -fsS "$$url" >/dev/null 2>&1; do \
			attempts=$$((attempts-1)); \
			if [ $$attempts -le 0 ]; then \
				echo "timeout waiting for $$url"; \
				return 1; \
			fi; \
			sleep 0.5; \
		done; \
	}; \
	wait_http "http://127.0.0.1:$$API_A_PORT/healthz"; \
	wait_http "http://127.0.0.1:$$API_B_PORT/healthz"; \
	curl -sS -X POST "http://127.0.0.1:$$API_A_PORT/v1/memory/store" \
		-H 'Content-Type: application/json' \
		-d '{"visibility":"shared","namespace":"team/dev","subject":"shared","body":"shared fact from peer a","origin_peer_id":"peer-a","author_agent_id":"agent-a"}' >/dev/null; \
	curl -sS -X POST "http://127.0.0.1:$$API_A_PORT/v1/memory/store" \
		-H 'Content-Type: application/json' \
		-d '{"visibility":"private","namespace":"local/dev","subject":"private","body":"private fact from peer a","origin_peer_id":"peer-a","author_agent_id":"agent-a"}' >/dev/null; \
	PATH=/opt/homebrew/bin:$$PATH "$(GO_BIN)" run $(GOFLAGS) ./cmd/syncd --config "$(PEER_A_CONFIG)" --once >/dev/null; \
	RECALL_JSON="$(TMP_BASE)/logs/recall-peer-b.json"; \
	STATUS_JSON="$(TMP_BASE)/logs/sync-status-peer-b.json"; \
	curl -sS -X POST "http://127.0.0.1:$$API_B_PORT/v1/memory/recall" \
		-H 'Content-Type: application/json' \
		-d '{"query":"peer","include_private":true,"limit":10}' >"$$RECALL_JSON"; \
	curl -sS "http://127.0.0.1:$$API_B_PORT/v1/sync/status?namespace=team/dev" >"$$STATUS_JSON"; \
	grep -q 'shared fact from peer a' "$$RECALL_JSON"; \
	! grep -q 'private fact from peer a' "$$RECALL_JSON"; \
	grep -Eq '"state"[[:space:]]*:[[:space:]]*"healthy"' "$$STATUS_JSON"; \
	grep -Eq '"schema_fenced"[[:space:]]*:[[:space:]]*false' "$$STATUS_JSON"; \
	grep -Eq '"peer_id"[[:space:]]*:[[:space:]]*"peer-a"' "$$STATUS_JSON"; \
	grep -Eq '"last_success_at_ms"[[:space:]]*:[[:space:]]*[1-9][0-9]*' "$$STATUS_JSON"; \
	echo "smoke-e2e-manual: PASS"; \
	echo "logs: $(TMP_BASE)/logs"

clean-dev:
	rm -rf "$(TMP_BASE)"
