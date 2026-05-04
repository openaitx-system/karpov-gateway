# Music Gateway Makefile
# Cross-platform: works in Git Bash / WSL / PowerShell (with make installed)

GO ?= go
BIN_DIR := bin
COVERAGE_FILE := coverage.txt

PROTO_DIR := api/proto/v1
OPENAPI_DIR := api/openapi
MIGRATIONS_DIR := migrations

# Tool versions (pinned)
BUF_VERSION         := v1.50.0
SQLC_VERSION        := v1.27.0
GOLANGCI_VERSION    := v1.62.2
MIGRATE_VERSION     := v4.18.1
GOSEC_VERSION       := v2.21.4
GOVULNCHECK_VERSION := latest

PG_DSN ?= postgres://postgres:postgres@localhost:5432/musicgw?sslmode=disable

.PHONY: help
help: ## 显示所有可用目标
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z_-]+:.*##/{printf "  %-20s %s\n",$$1,$$2}' $(MAKEFILE_LIST)

# ---------- 依赖与工具 ----------

.PHONY: tools
tools: ## 安装工具链：buf / sqlc / golangci-lint / migrate / gosec / govulncheck
	$(GO) install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_VERSION)
	$(GO) install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)
	$(GO) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	$(GO) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	$(GO) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

.PHONY: deps
deps: ## go mod download
	$(GO) mod download

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

# ---------- 代码生成 ----------

.PHONY: proto
proto: ## 用 buf 生成 gRPC + grpc-gateway + OpenAPI 代码
	cd $(PROTO_DIR) && buf lint
	cd $(PROTO_DIR) && buf generate

.PHONY: sqlc
sqlc: ## 用 sqlc 生成 DB query 代码
	sqlc generate

# ---------- 检查与测试 ----------

.PHONY: lint
lint: ## golangci-lint
	golangci-lint run ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: test
test: ## 单元测试
	$(GO) test -race -timeout 120s ./...

.PHONY: cover
cover: ## 测试覆盖率
	$(GO) test -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	$(GO) tool cover -func=$(COVERAGE_FILE) | tail -1

.PHONY: integration-test
integration-test: ## 集成测试（需要 PG/Redis 容器）
	$(GO) test -race -tags=integration -timeout 300s ./...

.PHONY: golden
golden: ## 重生成 Python 黄金 fixture（跨语言对拍）
	cd .. && uv sync && uv run python tools/dump_python_golden.py

.PHONY: gosec
gosec: ## 安全静态扫描
	gosec -severity=high -confidence=medium ./...

.PHONY: vuln
vuln: ## 已知漏洞扫描
	govulncheck ./...

# ---------- 数据库迁移 ----------

.PHONY: migrate-up
migrate-up: ## 应用所有 schema 迁移
	@for s in auth quota billing pool; do \
	  echo ">>> migrate up [$$s]"; \
	  migrate -path $(MIGRATIONS_DIR)/$$s -database "$(PG_DSN)&search_path=$$s" up; \
	done

.PHONY: migrate-down
migrate-down: ## 回滚一步迁移
	@for s in auth quota billing pool; do \
	  echo ">>> migrate down [$$s]"; \
	  migrate -path $(MIGRATIONS_DIR)/$$s -database "$(PG_DSN)&search_path=$$s" down 1; \
	done

# ---------- 构建与运行 ----------

.PHONY: build
build: build-monolith build-multi ## 构建统一二进制 + 7 个独立二进制（默认）

.PHONY: build-monolith
build-monolith: ## 仅构建统一二进制 qqmusic-gateway（推荐：单镜像多 entrypoint）
	@mkdir -p $(BIN_DIR)
	@echo ">>> build qqmusic-gateway (unified)"
	$(GO) build -o $(BIN_DIR)/qqmusic-gateway ./cmd/qqmusic-gateway

.PHONY: build-multi
build-multi: ## 仅构建 7 个独立 binary（向后兼容）
	@mkdir -p $(BIN_DIR)
	@for s in gateway auth music pool quota billing worker; do \
	  echo ">>> build $$s"; \
	  $(GO) build -o $(BIN_DIR)/$$s ./cmd/$$s; \
	done

.PHONY: run-gateway
run-gateway: ## 本地启动 Edge Gateway（统一二进制 subcommand 形式）
	$(GO) run ./cmd/qqmusic-gateway gateway

.PHONY: run-pool
run-pool: ## 本地启动 Pool Service
	$(GO) run ./cmd/qqmusic-gateway pool

.PHONY: run-music
run-music: ## 本地启动 Music Service
	$(GO) run ./cmd/qqmusic-gateway music

.PHONY: run-auth
run-auth: ## 本地启动 Auth Service
	$(GO) run ./cmd/qqmusic-gateway auth

.PHONY: run-worker
run-worker: ## 本地启动 health-check worker
	$(GO) run ./cmd/qqmusic-gateway worker

.PHONY: clean
clean: ## 清理构建产物
	rm -rf $(BIN_DIR) $(COVERAGE_FILE) coverage.html

# ---------- 一键质量门禁 ----------

.PHONY: ci
ci: tidy vet lint test ## 模拟 CI 全套（lint+test，不含集成）

.PHONY: ci-full
ci-full: ci gosec vuln ## 完整门禁（含安全扫描）
