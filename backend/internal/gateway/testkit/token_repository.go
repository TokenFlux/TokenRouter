package testkit

import (
	"context"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// tokenRepositoryReader 只投影测试账号，刷新规则仍由账号模块执行。
type tokenRepositoryReader struct {
	source gatewayprovider.ExecutionAccountStore
}

func (r tokenRepositoryReader) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(v), err
}
func TokenRepository(repo gatewayprovider.ExecutionAccountStore) acctcore.RefreshRepository {
	if repo == nil {
		return nil
	}
	reader := tokenRepositoryReader{repo}
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
