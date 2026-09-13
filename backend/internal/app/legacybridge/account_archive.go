package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountArchiveDefaults 只转交当前动态模板，缺省和配置规则仍由原设置用例拥有。
type AccountArchiveDefaults struct{ Source *service.SettingService }

func (s AccountArchiveDefaults) Read(ctx context.Context) (*transfer.OpenAIOAuthImportDefaults, error) {
	return s.Source.GetOpenAIOAuthImportDefaults(ctx)
}

// AccountImportQuotaProbe 只投影供应商观测，队列和资格规则由 account 拥有。
type AccountImportQuotaProbe struct{ Source *service.GrokQuotaService }

func (s AccountImportQuotaProbe) QueryQuota(ctx context.Context, id int64) (*account.GrokImportProbeResult, error) {
	v, err := s.Source.QueryQuota(ctx, id)
	if v == nil {
		return nil, err
	}
	return &account.GrokImportProbeResult{Model: v.Model, StatusCode: v.StatusCode, HeadersObserved: v.HeadersObserved}, err
}
