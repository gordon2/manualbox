.DEFAULT_GOAL := help
SHELL := /bin/bash

BIN        := bin/manualbox
PKG        := ./...
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## ---- build ----

.PHONY: build
build: web-build ## Build the single binary with the SPA embedded
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/manualbox

.PHONY: build-go
build-go: ## Build the binary only (skip the frontend)
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/manualbox

.PHONY: run
run: build-go ## Build and run the server
	$(BIN) serve

.PHONY: dev
dev: ## Run backend and frontend with live reload
	@command -v air >/dev/null || { echo "air not found: go install github.com/air-verse/air@latest"; exit 1; }
	@trap 'kill 0' EXIT; air & (cd web && npm run dev); wait

.PHONY: doctor
doctor: build-go ## Report which optional local tools are available
	$(BIN) doctor

## ---- frontend ----

.PHONY: web-install
web-install: ## Install frontend dependencies
	cd web && npm ci

.PHONY: web-build
web-build: ## Build the frontend into web/dist
	cd web && npm run build

.PHONY: web-typecheck
web-typecheck: ## Typecheck the frontend
	cd web && npm run typecheck

# `node --test`, which runs the TypeScript directly by stripping the types. No test
# framework and no new dependency: the alternative was adding a runner to assert
# rules that are a few lines each.
.PHONY: web-test
web-test: ## Run frontend tests
	cd web && npm test

## ---- quality ----

.PHONY: check
check: test lint web-typecheck web-test ## Everything CI runs

.PHONY: test
test: ## Run Go tests with race detector
	go test -race -shuffle=on $(PKG)

.PHONY: cover
cover: ## Run tests and open an HTML coverage report
	go test -coverprofile=coverage.out $(PKG)
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: fmt
fmt: ## Format Go and frontend sources
	golangci-lint fmt
	cd web && npm run format

.PHONY: tidy
tidy: ## Tidy both modules
	go mod tidy
	cd tools && go mod tidy

## ---- codegen ----
# Build tools live in ./tools as a separate module so sqlc's database-driver
# dependency tree stays out of the main module. Compile them to ./bin once,
# then invoke from the repo root so relative paths behave normally.

TOOLS := bin/sqlc bin/goose

bin/sqlc: tools/go.mod tools/go.sum
	go -C tools build -o ../$@ github.com/sqlc-dev/sqlc/cmd/sqlc

bin/goose: tools/go.mod tools/go.sum
	go -C tools build -o ../$@ github.com/pressly/goose/v3/cmd/goose

.PHONY: tools
tools: $(TOOLS) ## Build the build-time tools into ./bin

.PHONY: generate
generate: sqlc api-client ## Run all code generation

.PHONY: sqlc
sqlc: bin/sqlc ## Generate typed DB code from SQL
	./bin/sqlc generate

.PHONY: api-client
api-client: ## Generate the TS API client from the OpenAPI spec
	cd web && npm run generate:api

.PHONY: migration
migration: bin/goose ## Create a migration: make migration name=add_devices
	@test -n "$(name)" || { echo "usage: make migration name=add_devices"; exit 1; }
	./bin/goose -dir internal/db/migrations create $(name) sql

## ---- docker ----

.PHONY: docker
docker: ## Build the Docker image
	docker build -f deploy/Dockerfile -t manualbox:$(VERSION) .

.PHONY: up
up: ## Start via docker compose
	docker compose -f deploy/docker-compose.yml up --build

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist web/dist coverage.out coverage.html
