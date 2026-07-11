# linkpulse-link — Makefile

MIGRATE_VERSION := v4.19.1
POSTGRES_DSN ?= postgres://linkpulse:linkpulse@localhost:5432/linkpulse_link?sslmode=disable

.DEFAULT_GOAL := help

.PHONY: help
help: ## Показать список целей
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Собрать бинарник в bin/link
	CGO_ENABLED=0 go build -o bin/link ./cmd/link

.PHONY: run
run: ## Запустить локально (нужны Postgres и Redis из linkpulse-infra)
	POSTGRES_DSN="$(POSTGRES_DSN)" go run ./cmd/link

.PHONY: test
test: ## Юнит-тесты с гонками
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Тесты с отчётом покрытия
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## golangci-lint (конфиг копируется из linkpulse-infra)
	golangci-lint run

.PHONY: tools
tools: ## Установить golang-migrate CLI
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)

.PHONY: migrate-up
migrate-up: ## Накатить миграции
	migrate -path migrations -database "$(POSTGRES_DSN)" up

.PHONY: migrate-down
migrate-down: ## Откатить последнюю миграцию
	migrate -path migrations -database "$(POSTGRES_DSN)" down 1

.PHONY: docker
docker: ## Собрать Docker-образ
	docker build -t linkpulse-link .
