// 旧健康入口仅投影账号与发布端口，规则和写入顺序由 account 唯一实现。
package service

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func (s *AntigravityGatewayService) antigravityHealth() *acctcore.AntigravityHealth {
	if s.nativeHealth != nil {
		return s.nativeHealth
	}
	core := &acctcore.AntigravityHealth{Store: s.accountRepo, Counter: s.internal500Cache, ModelKeys: antigravityModelRateLimitKeys, Error: slog.Error, Warn: slog.Warn, Info: slog.Info, Logf: func(f string, args ...any) { logging.LegacyPrintf("service.antigravity_gateway", f, args...) }}
	if s.schedulerSnapshot != nil {
		core.Publish = func(ctx context.Context, value *acctcore.Record) error {
			return s.schedulerSnapshot.UpdateAccountInCache(ctx, AccountFromRecord(value))
		}
	}
	return core
}

// BindAntigravityHealth 在构造阶段绑定 app 持有的唯一健康端口。
func (s *AntigravityGatewayService) BindAntigravityHealth(core *acctcore.AntigravityHealth) {
	s.nativeHealth = core
}
