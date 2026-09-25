//go:build unit

package provider_test

import (
	"context"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 为网关测试提供账号记录和存储参与能力，刷新逻辑由账号模块执行。
type tokenSourceFixtureReader struct {
	source gatewayprovider.ExecutionAccountStore
}

func (r tokenSourceFixtureReader) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(v), err
}
func tokenSourceFixtureRepository(repo gatewayprovider.ExecutionAccountStore) acctcore.RefreshRepository {
	if repo == nil {
		return nil
	}
	reader := tokenSourceFixtureReader{repo}
	writer, hasWriter := repo.(acctcore.CredentialRefreshWriter)
	grok, hasGrok := repo.(acctcore.GrokRefreshSuccessWriter)
	if hasWriter && hasGrok {
		return struct {
			acctcore.RefreshRepository
			acctcore.CredentialRefreshWriter
			acctcore.GrokRefreshSuccessWriter
		}{reader, writer, grok}
	}
	if hasWriter {
		return struct {
			acctcore.RefreshRepository
			acctcore.CredentialRefreshWriter
		}{reader, writer}
	}
	if hasGrok {
		return struct {
			acctcore.RefreshRepository
			acctcore.GrokRefreshSuccessWriter
		}{reader, grok}
	}
	return reader
}
