# Node IO Manager Makefile

.PHONY: all build test clean docker deploy

VERSION ?= v0.1.0
REGISTRY ?= docker.io/library
IMAGE_NAME ?= node-io-manager

# Go 参数
GOOS ?= linux
GOARCH ?= amd64
CGO_ENABLED ?= 0

all: build

# 构建
build:
	@echo "Building node-io-manager..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/node-io-manager ./cmd/manager
	@echo "Building iocli..."
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/iocli ./cmd/iocli

# 本地构建 (当前平台)
build-local:
	go build -o bin/node-io-manager ./cmd/manager
	go build -o bin/iocli ./cmd/iocli

# 测试
test:
	go test -v ./...

# 覆盖率测试
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# 代码检查
lint:
	golangci-lint run ./...

# 格式化
fmt:
	go fmt ./...

# 清理
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Docker 构建
docker:
	docker build -t $(REGISTRY)/$(IMAGE_NAME):$(VERSION) .
	docker tag $(REGISTRY)/$(IMAGE_NAME):$(VERSION) $(REGISTRY)/$(IMAGE_NAME):latest

# Docker 推送
docker-push: docker
	docker push $(REGISTRY)/$(IMAGE_NAME):$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_NAME):latest

# 部署到 Kubernetes
deploy:
	kubectl apply -f deploy/daemonset.yaml
	kubectl apply -f deploy/business-priority.yaml

# 卸载
undeploy:
	kubectl delete -f deploy/daemonset.yaml --ignore-not-found
	kubectl delete -f deploy/business-priority.yaml --ignore-not-found

# 查看日志
logs:
	kubectl logs -n node-io-manager -l app.kubernetes.io/name=node-io-manager -f

# 本地运行 (开发用)
run:
	go run ./cmd/manager --config=config/default.yaml --log-level=debug

# 生成 API 文档
docs:
	@echo "Generating API documentation..."
	swag init -g cmd/manager/main.go -o docs/swagger

# 帮助
help:
	@echo "Node IO Manager Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make build        - 构建二进制文件"
	@echo "  make build-local  - 本地构建 (当前平台)"
	@echo "  make test         - 运行测试"
	@echo "  make lint         - 代码检查"
	@echo "  make docker       - 构建 Docker 镜像"
	@echo "  make deploy       - 部署到 Kubernetes"
	@echo "  make undeploy     - 从 Kubernetes 卸载"
	@echo "  make logs         - 查看日志"
	@echo "  make clean        - 清理构建产物"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION           - 版本号 (default: $(VERSION))"
	@echo "  REGISTRY          - 镜像仓库 (default: $(REGISTRY))"
