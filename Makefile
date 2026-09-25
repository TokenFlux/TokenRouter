.PHONY: build build-backend build-frontend build-datamanagementd test test-backend test-frontend test-frontend-critical test-datamanagementd secret-scan fmt-go-changed check-fmt-go-changed

PNPM ?= npx --yes pnpm@9

FRONTEND_CRITICAL_VITEST := \
	src/api/__tests__/client.spec.ts \
	src/api/__tests__/tokenRefresh.spec.ts \
	src/views/admin/orders/__tests__/AdminOrdersView.spec.ts \
	src/views/auth/__tests__/LinuxDoCallbackView.spec.ts \
	src/views/auth/__tests__/WechatCallbackView.spec.ts \
	src/views/user/__tests__/PaymentView.spec.ts \
	src/views/user/__tests__/PaymentResultView.spec.ts \
	src/components/user/profile/__tests__/ProfileEditForm.spec.ts \
	src/components/user/profile/__tests__/ProfileInfoCard.spec.ts \
	src/views/admin/__tests__/SettingsView.spec.ts \
	src/components/admin/usage/__tests__/UsageStatsCards.spec.ts \
	src/composables/__tests__/useQoderOAuth.spec.ts \
	src/components/account/__tests__/CreateAccountModal.qoder.spec.ts \
	src/views/admin/__tests__/AccountsView.qoderCreate.spec.ts

# 一键编译前后端
build: build-backend build-frontend

# 编译后端（复用 backend/Makefile）
build-backend:
	@$(MAKE) -C backend build

# 编译前端（需要已安装依赖）
build-frontend:
	@$(PNPM) --dir frontend run build

# 编译 datamanagementd（宿主机数据管理进程）
build-datamanagementd:
	@cd datamanagement && go build -o datamanagementd ./cmd/datamanagementd

# 运行测试（后端 + 前端）
test: test-backend test-frontend

test-backend:
	@$(MAKE) -C backend test

test-frontend:
	@$(PNPM) --dir frontend run lint:check
	@$(PNPM) --dir frontend run typecheck
	@$(MAKE) test-frontend-critical

test-frontend-critical:
	@$(PNPM) --dir frontend exec vitest run $(FRONTEND_CRITICAL_VITEST)

test-datamanagementd:
	@cd datamanagement && go test ./...

secret-scan:
	@python3 tools/secret_scan.py

# 提交前只格式化改动的手写 Go 文件，复用 backend/.golangci.yml。
fmt-go-changed:
	@python3 tools/format_go.py

check-fmt-go-changed:
	@python3 tools/format_go.py --check $(if $(FMT_BASE),--base "$(FMT_BASE)")
