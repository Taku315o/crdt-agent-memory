SHELL := /bin/bash

TMP_BASE ?= /tmp/crdt-agent-memory-dev
PEER_A_CONFIG := $(TMP_BASE)/peer-a/config.yaml
PEER_B_CONFIG := $(TMP_BASE)/peer-b/config.yaml

.PHONY: help test setup-dev-configs migrate-peer-a migrate-peer-b diag-peer-a diag-peer-b smoke-sync clean-dev

help:
	@printf "%s\n" \
	"make test               Run all Go tests" \
	"make setup-dev-configs  Copy sample configs into $(TMP_BASE)" \
	"make migrate-peer-a     Run migrations for peer-a" \
	"make migrate-peer-b     Run migrations for peer-b" \
	"make diag-peer-a        Show metadata for peer-a" \
	"make diag-peer-b        Show metadata for peer-b" \
	"make smoke-sync         Run a minimal 2-peer sync smoke test" \
	"make clean-dev          Remove $(TMP_BASE)"

test:
	go test ./...

setup-dev-configs:
	mkdir -p "$(TMP_BASE)/peer-a" "$(TMP_BASE)/peer-b"
	cp configs/peer-a.yaml.example "$(PEER_A_CONFIG)"
	cp configs/peer-b.yaml.example "$(PEER_B_CONFIG)"

migrate-peer-a: setup-dev-configs
	go run ./cmd/memoryd --config "$(PEER_A_CONFIG)" --cmd migrate

migrate-peer-b: setup-dev-configs
	go run ./cmd/memoryd --config "$(PEER_B_CONFIG)" --cmd migrate

diag-peer-a: setup-dev-configs
	go run ./cmd/memoryd --config "$(PEER_A_CONFIG)" --cmd diag

diag-peer-b: setup-dev-configs
	go run ./cmd/memoryd --config "$(PEER_B_CONFIG)" --cmd diag

smoke-sync: migrate-peer-a migrate-peer-b
	go run ./cmd/syncd --config "$(PEER_A_CONFIG)" --peer-config "$(PEER_B_CONFIG)" --namespace team/dev

clean-dev:
	rm -rf "$(TMP_BASE)"
