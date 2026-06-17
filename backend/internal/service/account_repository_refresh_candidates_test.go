package service

import "context"

func (m *sessionWindowMockRepo) ListOAuthRefreshCandidates(context.Context) ([]Account, error) {
	panic("unexpected ListOAuthRefreshCandidates call")
}
