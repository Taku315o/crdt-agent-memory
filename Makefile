SHELL := /bin/bash

GO_BIN ?= /opt/homebrew/bin/go
GOFLAGS ?= -tags sqlite_fts5
TMP_BASE ?= /tmp/crdt-agent-memory-dev
TOOLS_DIR ?= $(CURDIR)/.tools
CRSQLITE_DIR := $(TOOLS_DIR)/crsqlite
SQLITE_VEC_DIR := $(TOOLS_DIR)/sqlite-vec
PEER_A_CONFIG := $(TMP_BASE)/peer-a/config.yaml
PEER_B_CONFIG := $(TMP_BASE)/peer-b/config.yaml

.PHONY: help bootstrap-dev check-deps test setup-dev-configs migrate-peer-a migrate-peer-b diag-peer-a diag-peer-b serve-peer-a serve-peer-b index-peer-a index-peer-b sync-peer-a sync-peer-b smoke-sync smoke-sync-confirm smoke-recall-confirm smoke-e2e-manual clean-dev

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
	"make smoke-sync-confirm   Run smoke sync confirmation (DB direct)" \
	"make smoke-recall-confirm Run smoke recall confirmation (API recall)" \
	"make smoke-e2e-manual  Run full manual smoke flow (sync + recall)" \
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

smoke-sync-confirm:
	./scripts/smoke-e2e-manual.sh sync

smoke-recall-confirm:
	./scripts/smoke-e2e-manual.sh recall

smoke-e2e-manual:
	./scripts/smoke-e2e-manual.sh all

clean-dev:
	rm -rf "$(TMP_BASE)"
