# dsh-ee 构建入口。产物都在 dist/。
#
#   make docs               从 docs/ 重新生成站内开发文档（web build 会自动跑）
#   make docs-check         校验站内文档生成物与 docs/ 一致
#   make build              前端 + 嵌入前端的单文件服务端（本机平台）
#   make release            linux/amd64 与 linux/arm64 两个静态二进制
#   make test               Go 集成测试（需要 DSH_EE_TEST_DSN）
#   make e2e                浏览器端到端测试（需要已启动的服务与 ADMIN_PW）
#   make image              docker 镜像 dsh-ee:$(VERSION)
#   make bundle ARCH=amd64  离线安装包（镜像 + compose + 安装脚本）

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO      ?= go
PNPM    ?= corepack pnpm
LDFLAGS := -s -w -X main.version=$(VERSION)
EMBED   := server/internal/webui/build

.PHONY: docs docs-check web plugin embed build release test e2e image bundle clean

# 站内开发文档：docs/ 是单一事实源，生成 web/src/generated/dev-docs.ts。
# web 的 build 会经 prebuild 自动跑一遍；这个目标用于只改文档后单独重生成。
docs:
	node scripts/docs/build.mjs

# 校验生成物与 docs/ 一致（CI 的 web job 与 `make docs` 后都可跑）。
docs-check:
	node scripts/docs/build.mjs --check

web:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build

plugin:
	cd dsh-plugin && npm ci --no-audit --no-fund && npm test && npm run build

# 把 web/dist 换进嵌入目录；保留目录里的 .gitignore
embed: web plugin
	find $(EMBED) -mindepth 1 ! -name .gitignore -exec rm -rf {} +
	cp -R web/dist/. $(EMBED)/

build: embed
	mkdir -p dist
	cd server && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o ../dist/dsh-ee ./cmd/dsh-ee
	@echo "dist/dsh-ee ($(VERSION))"

release: embed
	mkdir -p dist
	for arch in amd64 arm64; do \
	  (cd server && CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o ../dist/dsh-ee-linux-$$arch ./cmd/dsh-ee) || exit 1; \
	done

test:
	cd server && $(GO) vet ./... && $(GO) test ./...

e2e:
	cd e2e && npm ci --no-audit --no-fund && node run.mjs

image:
	docker build --build-arg VERSION=$(VERSION) -t dsh-ee:$(VERSION) .

bundle:
	deploy/make-bundle.sh $(VERSION) $(or $(ARCH),amd64)

clean:
	rm -rf dist web/dist
	find $(EMBED) -mindepth 1 ! -name .gitignore -exec rm -rf {} +
