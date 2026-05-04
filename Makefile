.PHONY: build build-go run run-dev dev dev-down worktree-init test test-integration lint tidy clean compose-up compose-down compose-logs compose-app compose-auth docker-build frontend-install frontend-build frontend-dev proto proto-lint e2e e2e-up e2e-test e2e-down e2e-clean help

BIN := bin/kafkito
PKG := ./...
VERSION ?= 0.0.0-dev
IMAGE ?= ghcr.io/finkeflo/kafkito:dev
# Pinned air version. Bump deliberately; keep .air.toml's reference in sync.
AIR_VERSION ?= v1.65.1

help:
	@echo "Targets:"
	@echo "  build              - build frontend then Go binary into $(BIN)"
	@echo "  build-go           - build only the Go binary (skip frontend)"
	@echo "  run                - build and run the binary"
	@echo "  run-dev            - run with -tags devauth (auth disabled, dev only)"
	@echo "  dev                - full local loop: Compose + backend (air) + frontend (Vite)"
	@echo "  dev-down           - tear down the Compose dev stack"
	@echo "  worktree-init      - write per-worktree .env.dev with a free port pair"
	@echo "  test               - go test -race ./..."
	@echo "  test-integration   - integration tests (requires Docker)"
	@echo "  lint               - golangci-lint run"
	@echo "  tidy               - go mod tidy"
	@echo "  proto              - buf generate"
	@echo "  proto-lint         - buf lint"
	@echo "  frontend-install   - bun install in frontend/"
	@echo "  frontend-build     - bun run build in frontend/"
	@echo "  frontend-dev       - bun run dev in frontend/"
	@echo "  docker-build       - docker build -t $(IMAGE)"
	@echo "  compose-up/down    - docker compose lifecycle"
	@echo "  e2e                - opt-in Playwright walks against a local fixture stack"

frontend-install:
	cd frontend && bun install

frontend-build:
	cd frontend && bun run build

frontend-dev:
	cd frontend && bun run dev

build: frontend-build
	mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/kafkito

# go-only build (assumes frontend already built or placeholder is sufficient)
build-go:
	mkdir -p bin
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/kafkito

run: build
	$(BIN)

run-dev:
	go run -tags devauth ./cmd/kafkito

test:
	go test -race -count=1 $(PKG)

# Integration tests require Docker (Testcontainers-Go). Skipped otherwise.
test-integration:
	go test -race -count=1 -tags=integration -timeout=10m ./pkg/kafka/...

lint:
	golangci-lint run

tidy:
	go mod tidy

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

compose-up:
	docker compose up -d

compose-app:
	docker compose --profile app up -d --build

compose-auth:
	docker compose --profile auth up -d

compose-logs:
	docker compose logs -f --tail=100

compose-down:
	docker compose --profile app --profile auth down --remove-orphans

clean:
	rm -rf bin frontend/dist/assets

# --- e2e harness (Q-001 / PLAN.md § 3.14) -------------------------------
# `make e2e` runs Playwright walks against a hermetic local stack:
# Kafka via docker compose (existing kafkito-kafka container) +
# kafkito as a Go subprocess on a non-default port (E2E_PORT, default
# 47421) so it does NOT conflict with a running `make dev` stack.
# Frontend is served from the kafkito-embedded assets — no Vite needed.
#
# Opt-in: NOT part of the canonical hard gate. CI runs the same flow
# in .github/workflows/e2e.yml; behaviour parity matters.
#
# Requires Docker, Bun, Go, and a one-time `bunx playwright install
# chromium` under frontend/. See frontend/e2e/README.md.

E2E_PORT ?= 47421
E2E_PID := /tmp/kafkito-e2e.pid
E2E_LOG := /tmp/kafkito-e2e.log

e2e: e2e-up e2e-test e2e-down

e2e-up:
	@if lsof -nP -iTCP:$(E2E_PORT) -sTCP:LISTEN >/dev/null 2>&1; then \
		echo "e2e: port $(E2E_PORT) is in use — kill the listener and retry"; \
		exit 1; \
	fi
	docker compose up -d --wait kafka
	cd frontend && bun run build
	go build -tags devauth -ldflags "-X main.version=e2e-dev" -o bin/kafkito-e2e ./cmd/kafkito
	@echo "e2e: starting kafkito-e2e on port $(E2E_PORT)"
	@KAFKITO_KAFKA_BROKERS=localhost:39092 PORT=$(E2E_PORT) KAFKITO_AUTH_MODE=off \
		./bin/kafkito-e2e > $(E2E_LOG) 2>&1 & echo $$! > $(E2E_PID)
	@ok=0; for i in $$(seq 1 30); do \
		if curl -fsS http://localhost:$(E2E_PORT)/api/v1/me >/dev/null 2>&1; then \
			ok=$$((ok + 1)); \
			if [ "$$ok" -ge 3 ]; then \
				echo "e2e: kafkito ready on $(E2E_PORT) (3 consecutive /api/v1/me)"; \
				break; \
			fi; \
		else \
			ok=0; \
		fi; \
		sleep 1; \
	done
	@if ! curl -fsS http://localhost:$(E2E_PORT)/api/v1/me >/dev/null 2>&1; then \
		echo "e2e: kafkito did not become ready in 30s — check $(E2E_LOG)"; \
		cat $(E2E_LOG); \
		exit 1; \
	fi
	@curl -fsS -o /dev/null \
		-H 'X-Kafkito-Cluster: {"id":"warmup","name":"warmup","brokers":["localhost:1"],"auth":{"type":"none"},"tls":{"enabled":false},"created_at":0,"updated_at":0}' \
		http://localhost:$(E2E_PORT)/api/v1/clusters/__private__/topics 2>/dev/null || true
	@echo "e2e: warmup adhoc-cluster probe issued (pre-triggers dial-and-fail goroutines)"
	bash frontend/e2e/fixtures/seed.sh

e2e-test:
	cd frontend && KAFKITO_E2E_BASE_URL=http://localhost:$(E2E_PORT) bunx playwright test

e2e-down:
	@if [ -f $(E2E_PID) ]; then \
		kill $$(cat $(E2E_PID)) 2>/dev/null || true; \
		rm -f $(E2E_PID); \
		echo "e2e: kafkito-e2e stopped"; \
	fi

e2e-clean:
	@pids=$$(lsof -nP -iTCP:$(E2E_PORT) -sTCP:LISTEN -t 2>/dev/null); \
	if [ -n "$$pids" ]; then kill -9 $$pids 2>/dev/null || true; fi
	@rm -rf frontend/test-results/.playwright-artifacts-*
	@rm -f $(E2E_PID) $(E2E_LOG)
	@echo "e2e: cleaned port $(E2E_PORT) and stale state"


proto:
	buf generate

proto-lint:
	buf lint

# --- Dev iteration loop -------------------------------------------------
# `worktree-init` writes a per-worktree .env.dev with a free port pair.
# Idempotent: if .env.dev exists, it prints the contents and exits 0.
# The whole recipe runs as ONE shell (chained with `; \`), so the early
# `exit 0` in the idempotent branch is load-bearing — without it, the
# port scan below would still run and overwrite .env.dev.
worktree-init:
	@if [ -f .env.dev ]; then \
		echo ".env.dev already exists in this worktree:"; \
		cat .env.dev; \
		exit 0; \
	fi; \
	p=37421; \
	while [ $$((p+1)) -le 37499 ]; do \
		if ! lsof -nP -iTCP:$$p -sTCP:LISTEN >/dev/null 2>&1 \
		&& ! lsof -nP -iTCP:$$((p+1)) -sTCP:LISTEN >/dev/null 2>&1; then \
			break; \
		fi; \
		p=$$((p+2)); \
	done; \
	if [ $$((p+1)) -gt 37499 ]; then \
		echo "no free port pair in 37421-37499" >&2; exit 1; \
	fi; \
	{ \
		echo "# Per-worktree dev config - gitignored, regenerate with 'make worktree-init'."; \
		echo "PORT=$$p"; \
		echo "KAFKITO_BACKEND_PORT=$$p"; \
		echo "KAFKITO_FRONTEND_PORT=$$((p+1))"; \
		echo "KAFKITO_KAFKA_BROKERS=localhost:39092"; \
	} > .env.dev; \
	echo "wrote .env.dev:"; \
	cat .env.dev

# `make dev` — full local loop in one process tree:
#   - Compose stack (Kafka + Schema Registry) up & healthy
#   - Backend with air hot-reload
#   - Frontend with Vite HMR
# Sources .env.dev so both children see PORT, KAFKITO_BACKEND_PORT,
# KAFKITO_FRONTEND_PORT, KAFKITO_KAFKA_BROKERS. Falls back to defaults
# if .env.dev is missing.
# Stop with Ctrl-C in the foreground terminal, or `kill -INT <concurrently-pid>`.
# `kill -INT` on the make process does NOT propagate to children on macOS.
dev:
	@if [ ! -d frontend/node_modules ]; then \
		echo "frontend/node_modules missing — running 'bun install' first"; \
		cd frontend && bun install; \
	fi
	@if [ ! -f .env.dev ]; then \
		echo "no .env.dev — run 'make worktree-init' first to pick free ports."; \
		echo "falling back to defaults: PORT=37421 KAFKITO_FRONTEND_PORT=37422"; \
	fi
	docker compose up -d --wait kafka schema-registry
	@set -a; if [ -f .env.dev ]; then . ./.env.dev; fi; set +a; \
	bunx --bun concurrently@^9 \
		--names backend,frontend \
		--prefix-colors blue,magenta \
		--kill-others \
		--kill-signal SIGTERM \
		--kill-timeout 5000 \
		"go run github.com/air-verse/air@$(AIR_VERSION) -c .air.toml" \
		"cd frontend && bun run dev"

# Tear down the dev Compose stack — scope mirrors `make dev`
# (kafka + schema-registry, no profile filter). For the broader
# teardown including --profile app/auth, use `make compose-down`.
dev-down:
	docker compose down --remove-orphans
