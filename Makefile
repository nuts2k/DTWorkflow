.DEFAULT_GOAL := help

APP_NAME    := dtworkflow
MODULE      := otws19.zicp.vip/kelin/dtworkflow
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME  := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS     := -X '$(MODULE)/internal/cmd.version=$(VERSION)' \
               -X '$(MODULE)/internal/cmd.gitCommit=$(GIT_COMMIT)' \
               -X '$(MODULE)/internal/cmd.buildTime=$(BUILD_TIME)'
DTW_LDFLAGS := -X '$(MODULE)/internal/dtw/cmd.dtwVersion=$(VERSION)' \
               -X '$(MODULE)/internal/dtw/cmd.dtwCommit=$(GIT_COMMIT)' \
               -X '$(MODULE)/internal/dtw/cmd.dtwBuildTime=$(BUILD_TIME)'

.PHONY: build build-linux build-dtw build-dtw-linux build-all install lint test fmt clean dev-up dev-down docker-build build-worker build-worker-full release deploy rollback help

## build: 构建本平台二进制到 bin/
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/dtworkflow

## install: 安装到 $GOPATH/bin
install:
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" ./cmd/dtworkflow

## build-linux: 交叉编译 Linux amd64 二进制
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME)-linux-amd64 ./cmd/dtworkflow

## build-dtw: 构建 dtw 瘦客户端到 bin/
build-dtw:
	CGO_ENABLED=0 go build -ldflags "$(DTW_LDFLAGS)" -o bin/dtw ./cmd/dtw

## build-dtw-linux: 交叉编译 dtw Linux amd64
build-dtw-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(DTW_LDFLAGS)" -o bin/dtw-linux-amd64 ./cmd/dtw

## build-all: 构建所有二进制
build-all: build build-dtw

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

## build-worker: 构建轻量 Worker 镜像
build-worker:
	docker build -f build/docker/Dockerfile.worker \
		-t dtworkflow-worker:latest .

## build-worker-full: 构建执行 Worker 镜像（依赖 build-worker）
build-worker-full: build-worker
	docker build -f build/docker/Dockerfile.worker-full \
		-t dtworkflow-worker-full:latest .

## release: 构建发布包（用法：make release VERSION=v0.2.0）
release:
ifndef VERSION
	$(error 请指定版本号: make release VERSION=v0.2.0)
endif
	scripts/build-release.sh $(VERSION)

## deploy: 部署到测试服务器（用法：make deploy VERSION=v0.2.0，HOST 默认取自 deploy/local.env）
deploy:
ifndef VERSION
	$(error 请指定版本号: make deploy VERSION=v0.2.0)
endif
	scripts/deploy.sh $(VERSION) $(HOST)

## rollback: 回滚测试服务器（用法：make rollback，HOST 默认取自 deploy/local.env）
rollback:
	scripts/rollback.sh $(HOST)

## help: 显示帮助信息
help:
	@echo "可用目标："
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/  /'
