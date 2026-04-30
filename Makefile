.PHONY: build build-go run run-dev dev dev-down worktree-init test test-integration lint tidy clean compose-up compose-down compose-logs compose-app compose-auth docker-build frontend-install frontend-build frontend-dev proto proto-lint help

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
