//go:build unit

package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

func (s *accountRepoStub) ListOAuthRefreshCandidates(context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	panic("unexpected ListOAuthRefreshCandidates call")
}

func (m *groupAwareMockAccountRepo) ListOAuthRefreshCandidates(context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	panic("unexpected ListOAuthRefreshCandidates call")
}

func (m *mockAccountRepoForPlatform) ListOAuthRefreshCandidates(context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	panic("unexpected ListOAuthRefreshCandidates call")
}

func (m *mockAccountRepoForGemini) ListOAuthRefreshCandidates(context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListActive(context.Background())
}
