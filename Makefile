.DEFAULT_GOAL := help

APP_NAME    := dtworkflow
MODULE      := otws19.zicp.vip/kelin/dtworkflow
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME  := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS     := -X '$(MODULE)/internal/cmd.version=$(VERSION)' \
               -X '$(MODULE)/internal/cmd.gitCommit=$(GIT_COMMIT)' \
               -X '$(MODULE)/internal/cmd.buildTime=$(BUILD_TIME)'

.PHONY: build build-linux install lint test fmt clean dev-up dev-down docker-build help

## build: 构建本平台二进制到 bin/
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/dtworkflow

## install: 安装到 $GOPATH/bin
install:
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" ./cmd/dtworkflow

## build-linux: 交叉编译 Linux amd64 二进制
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME)-linux-amd64 ./cmd/dtworkflow

## lint: 运行 golangci-lint 静态检查
lint:
	golangci-lint run ./...

## test: 运行全部测试
test:
	go test -v -race -cover ./...

## fmt: 格式化代码
fmt:
	goimports -w -local $(MODULE) .

## clean: 清理构建产物
clean:
	rm -rf bin/

## dev-up: 启动开发依赖（Redis）
dev-up:
	docker compose up -d

## dev-down: 停止开发依赖
dev-down:
	docker compose down

## docker-build: 构建生产 Docker 镜像
docker-build:
	docker build -f build/Dockerfile \
		--build-arg LDFLAGS="$(LDFLAGS)" \
		-t $(APP_NAME):$(VERSION) .

## help: 显示帮助信息
help:
	@echo "可用目标："
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/  /'
