# ChannelHunter — developer entry points.
#
#   make help          list every target
#   make setup         install tooling and prepare .env
#   make up            start PostgreSQL (docker compose)
#   make migrate-up    apply migrations
#   make run-api       run the API on :8080
#
# The Makefile works both with Docker and with a locally installed toolchain
# (Go, psql) — pick whichever your machine has.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# --- Paths ------------------------------------------------------------------
BACKEND_DIR   := backend
DEPLOY_DIR    := deploy
ENV_FILE      ?= $(CURDIR)/.env
MIGRATIONS    := $(BACKEND_DIR)/migrations
BIN_DIR       := $(CURDIR)/bin

# Version stamped into the binaries.
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS       := -X github.com/akqqqw/channelhunter/backend/internal/app.Version=$(VERSION)

# Database URL for migration and test targets (override on the command line).
DATABASE_URL  ?= postgres://channelhunter:channelhunter@127.0.0.1:5432/channelhunter?sslmode=disable
TEST_DATABASE_URL ?= $(DATABASE_URL)

GO            ?= go
GOFLAGS       ?=
COVERAGE_FILE := $(CURDIR)/coverage.out

# docker compose wrapper that uses deploy/docker-compose.yml + root .env
COMPOSE       := docker compose --project-directory $(DEPLOY_DIR) --env-file $(ENV_FILE) -f $(DEPLOY_DIR)/docker-compose.yml

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nChannelHunter\n\nUsage: make \033[36m<target>\033[0m\n\n"} \
		/^[a-zA-Z0-9_.-]+:.*##/ {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)
	@echo ""

##@ Setup

.PHONY: setup
setup: ## Create .env, download Go dependencies, install tools
	@if [ ! -f $(ENV_FILE) ]; then cp .env.example $(ENV_FILE); echo "created $(ENV_FILE) — fill in the secrets"; fi
	cd $(BACKEND_DIR) && $(GO) mod download
	@echo "setup complete"

.PHONY: tools
tools: ## Install development tools (golangci-lint, migrate, sqlc) into ./bin
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	GOBIN=$(BIN_DIR) $(GO) install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	GOBIN=$(BIN_DIR) $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	@echo "tools installed in $(BIN_DIR)"

##@ Infrastructure

.PHONY: up
up: ## Start PostgreSQL (and the rest of the stack) with docker compose
	$(COMPOSE) up -d postgres

.PHONY: down
down: ## Stop the stack (keeps the database volume)
	$(COMPOSE) down

.PHONY: stack
stack: ## Build and start every service (postgres, migrate, api, bot, worker)
	$(COMPOSE) up -d --build

.PHONY: logs
logs: ## Follow container logs
	$(COMPOSE) logs -f --tail=100

.PHONY: ps
ps: ## Show container status
	$(COMPOSE) ps

.PHONY: destroy
destroy: ## Stop the stack AND delete the database volume (irreversible)
	@read -p "This deletes all ChannelHunter data. Type 'yes' to continue: " ans; \
	if [ "$$ans" = "yes" ]; then $(COMPOSE) down -v; else echo "aborted"; fi

.PHONY: db-shell
db-shell: ## Open psql against the local database
	psql "$(DATABASE_URL)"

##@ Database migrations

.PHONY: migrate-up
migrate-up: ## Apply all pending migrations (via the API binary)
	cd $(BACKEND_DIR) && $(GO) run ./cmd/api --migrate-only --env-file $(ENV_FILE)

.PHONY: migrate-up-cli
migrate-up-cli: ## Apply migrations with golang-migrate CLI (needs DATABASE_URL)
	$(BIN_DIR)/migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back one migration (development only)
	$(BIN_DIR)/migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" down 1

.PHONY: migrate-version
migrate-version: ## Print the current schema version
	$(BIN_DIR)/migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" version

.PHONY: migrate-create
migrate-create: ## Create a migration pair: make migrate-create NAME=add_reports
	@test -n "$(NAME)" || (echo "usage: make migrate-create NAME=add_reports" && exit 1)
	$(BIN_DIR)/migrate create -ext sql -dir $(MIGRATIONS) -seq $(NAME)

.PHONY: sqlc
sqlc: ## Regenerate typed queries from SQL (see backend/sqlc.yaml)
	cd $(BACKEND_DIR) && $(BIN_DIR)/sqlc generate

##@ Backend

.PHONY: run-api
run-api: ## Run the HTTP API locally
	cd $(BACKEND_DIR) && $(GO) run -ldflags "$(LDFLAGS)" ./cmd/api --env-file $(ENV_FILE)

.PHONY: run-bot
run-bot: ## Run the Telegram bot locally
	cd $(BACKEND_DIR) && $(GO) run -ldflags "$(LDFLAGS)" ./cmd/bot --env-file $(ENV_FILE)

.PHONY: run-worker
run-worker: ## Run the background worker locally
	cd $(BACKEND_DIR) && $(GO) run -ldflags "$(LDFLAGS)" ./cmd/worker --env-file $(ENV_FILE)

.PHONY: build
build: ## Build all three binaries into ./bin
	@mkdir -p $(BIN_DIR)
	cd $(BACKEND_DIR) && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(BIN_DIR)/channelhunter-api ./cmd/api
	cd $(BACKEND_DIR) && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(BIN_DIR)/channelhunter-bot ./cmd/bot
	cd $(BACKEND_DIR) && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $(BIN_DIR)/channelhunter-worker ./cmd/worker
	@echo "binaries in $(BIN_DIR)"

.PHONY: images
images: ## Build the Docker images for api, bot and worker
	docker build --build-arg TARGET=api    --build-arg VERSION=$(VERSION) -t channelhunter-api:$(VERSION)    $(BACKEND_DIR)
	docker build --build-arg TARGET=bot    --build-arg VERSION=$(VERSION) -t channelhunter-bot:$(VERSION)    $(BACKEND_DIR)
	docker build --build-arg TARGET=worker --build-arg VERSION=$(VERSION) -t channelhunter-worker:$(VERSION) $(BACKEND_DIR)

.PHONY: health
health: ## Check the local API health endpoints
	@curl -fsS http://localhost:8080/health | head -c 400; echo
	@curl -fsS http://localhost:8080/ready  | head -c 600; echo

##@ Quality

.PHONY: fmt
fmt: ## Format Go code
	cd $(BACKEND_DIR) && $(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-ed
	@cd $(BACKEND_DIR) && out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi
	@echo "formatting ok"

.PHONY: vet
vet: ## Run go vet
	cd $(BACKEND_DIR) && $(GO) vet ./...

.PHONY: lint
lint: ## Run golangci-lint (installs into ./bin if missing)
	@test -x $(BIN_DIR)/golangci-lint || $(MAKE) tools
	cd $(BACKEND_DIR) && $(BIN_DIR)/golangci-lint run --timeout 5m ./...

.PHONY: tidy
tidy: ## go mod tidy
	cd $(BACKEND_DIR) && $(GO) mod tidy

.PHONY: verify
verify: fmt-check vet test ## Everything CI runs for pull requests

##@ Tests

.PHONY: test
test: ## Run unit tests
	cd $(BACKEND_DIR) && $(GO) test ./...

.PHONY: test-race
test-race: ## Run unit tests with the race detector
	cd $(BACKEND_DIR) && $(GO) test -race ./...

.PHONY: test-integration
test-integration: ## Run integration tests (needs PostgreSQL + migrations)
	cd $(BACKEND_DIR) && TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test -tags=integration ./...

.PHONY: test-all
test-all: test-race test-integration ## Unit + integration tests

.PHONY: cover
cover: ## Run tests with coverage and print the total
	cd $(BACKEND_DIR) && $(GO) test -coverprofile $(COVERAGE_FILE) -covermode atomic ./... >/dev/null
	@cd $(BACKEND_DIR) && $(GO) tool cover -func $(COVERAGE_FILE) | tail -1

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	cd $(BACKEND_DIR) && $(GO) tool cover -html $(COVERAGE_FILE)

##@ Frontend (Mini App — later stage)

.PHONY: frontend-install
frontend-install: ## Install Mini App dependencies
	cd frontend && npm install

.PHONY: frontend-dev
frontend-dev: ## Run the Mini App dev server
	cd frontend && npm run dev

.PHONY: frontend-build
frontend-build: ## Type-check and build the Mini App
	cd frontend && npm run build

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(COVERAGE_FILE)
	cd $(BACKEND_DIR) && $(GO) clean -testcache

.PHONY: check-env
check-env: ## Validate .env by booting the config loader
	cd $(BACKEND_DIR) && $(GO) run ./cmd/api --migrate-only --env-file $(ENV_FILE)
