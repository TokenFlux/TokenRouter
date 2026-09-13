// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	log "log"
	time "time"
)

// provideAccountTests 只绑定唯一用例与旧平台执行句柄，后台与 HTTP 共用同一入口。
func provideAccountTests(source *service.AccountTestService) *account.TestService {
	core := account.NewTestService(legacybridge.AccountTestLoader{Source: source}, account.TestOptions{Now: time.Now, Error: func(message string) { log.Printf("Account test error: %s", message) }, WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) }})
	source.SetTester(core)
	return core
}

// provideAccountTestHTTP 与后台复用唯一测试用例，成功恢复仍调用原健康端口。
func provideAccountTestHTTP(core *account.TestService, recovery *account.RecoveryService) *accounthttp.TestHandler {
	return accounthttp.NewTestHandler(core, func(ctx context.Context, id int64) error {
		_, err := recovery.RecoverAccountAfterSuccessfulTest(ctx, id)
		return err
	})
}
