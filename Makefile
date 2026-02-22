APP_NAME    := akili
VERSION     ?= dev
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE  := $(shell date -u +%Y-%m-%d)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

# ── Build ────────────────────────────────────────────────────────────
.PHONY: build run clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/$(APP_NAME)

run: build
	./bin/$(APP_NAME)

clean:
	rm -rf bin/

# ── Quality ──────────────────────────────────────────────────────────
.PHONY: test test-integration lint fmt vet check

test:
	go test ./... -short -count=1

test-integration:
	go test ./... -count=1 -run Integration

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

check: vet build test
	@echo "All checks passed."

# ── Docker ───────────────────────────────────────────────────────────
.PHONY: docker-build docker-runtime up down restart logs ps

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(APP_NAME):latest \
		-f docker/Dockerfile .

docker-runtime:
	docker build -t $(APP_NAME)-runtime:latest -f docker/Dockerfile.runtime .

up:
	docker compose up -d

down:
	docker compose down

restart:
	docker compose restart akili

logs:
	docker compose logs -f

ps:
	docker compose ps

# ── Runbooks ─────────────────────────────────────────────────────────
.PHONY: validate-runbooks

validate-runbooks: build
	@echo "Validating runbook skill definitions..."
	@count=0; errors=0; \
	for f in runbooks/*.md; do \
		count=$$((count + 1)); \
		if ! head -1 "$$f" | grep -q '^---$$'; then \
			echo "FAIL: $$f (missing YAML frontmatter)"; \
			errors=$$((errors + 1)); \
		fi; \
	done; \
	echo "Checked $$count runbooks, $$errors errors."; \
	[ $$errors -eq 0 ] || exit 1

# ── Dev helpers ──────────────────────────────────────────────────────
.PHONY: dev dev-down dev-logs dev-restart version onboarding run-gateway run-agent

dev: docker-build up
	@echo "Akili is running at http://localhost:8080"
	@echo "Health: curl http://localhost:8080/healthz"

dev-down:
	docker compose down -v

dev-logs:
	docker compose logs -f akili

dev-restart: docker-build restart
	@echo "Akili container restarted."

version: build
	./bin/$(APP_NAME) version

onboarding: build
	./bin/$(APP_NAME) onboarding gateway

run-gateway: build
	./bin/$(APP_NAME) gateway --config ~/.akili/config.json

run-agent: build
	./bin/$(APP_NAME) agent --config configs/akili.json

# ── Help ─────────────────────────────────────────────────────────────
.PHONY: help
help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Build:"
	@echo "  build              Build the binary to bin/$(APP_NAME)"
	@echo "  run                Build and run locally (default gateway mode)"
	@echo "  clean              Remove build artifacts"
	@echo ""
	@echo "Quality:"
	@echo "  test               Run unit tests"
	@echo "  test-integration   Run integration tests"
	@echo "  lint               Run golangci-lint"
	@echo "  fmt                Format Go source files"
	@echo "  vet                Run go vet"
	@echo "  check              Run vet + build + test"
	@echo ""
	@echo "Docker:"
	@echo "  docker-build       Build the application Docker image"
	@echo "  docker-runtime     Build the sandbox runtime image"
	@echo "  up                 Start docker compose stack"
	@echo "  down               Stop docker compose stack"
	@echo "  restart            Restart the akili container"
	@echo "  logs               Tail docker compose logs"
	@echo "  ps                 Show running containers"
	@echo ""
	@echo "Runbooks:"
	@echo "  validate-runbooks  Validate runbook skill definitions"
	@echo ""
	@echo "Dev:"
	@echo "  dev                Build image and start full stack"
	@echo "  dev-down           Stop stack and remove volumes"
	@echo "  dev-logs           Tail akili container logs"
	@echo "  dev-restart        Rebuild image and restart akili container"
	@echo "  version            Print version info"
	@echo "  onboarding         Run interactive setup wizard"
	@echo "  run-gateway        Build and run in gateway mode (~/.akili/config.json)"
	@echo "  run-agent          Build and run in agent mode (~/.akili/config.json)"
