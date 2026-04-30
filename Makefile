.PHONY: build build-go run run-dev test test-integration lint tidy clean compose-up compose-down compose-logs compose-app compose-auth docker-build frontend-install frontend-build frontend-dev proto proto-lint help

BIN := bin/kafkito
PKG := ./...
VERSION ?= 0.0.0-dev
IMAGE ?= ghcr.io/finkeflo/kafkito:dev

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
