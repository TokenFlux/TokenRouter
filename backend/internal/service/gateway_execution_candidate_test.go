package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestCapturedCandidatePlanDoesNotResolveMissingOrChangedCandidate(t *testing.T) {
	_, ok := CapturedAccountCandidatePlan(nil)
	require.False(t, ok)
	account := &Account{ID: 3}
	_, ok = CapturedAccountCandidatePlan(account)
	require.False(t, ok)
	account.resolvedCandidate = &routing.CandidatePlan{AccountID: 3, GroupID: 7}
	captured, ok := CapturedAccountCandidatePlan(account)
	require.True(t, ok)
	account.resolvedCandidate.GroupID = 8
	account.resolvedCandidate = nil
	require.Equal(t, int64(7), captured.GroupID)
	require.Equal(t, int64(3), captured.AccountID)
}
