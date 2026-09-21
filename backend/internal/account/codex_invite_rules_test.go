package account

import (
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCodexInviteResetGrantType(t *testing.T) {
	hasRewards := true
	hasNoRewards := false
	require.Equal(t, "none", normalizeCodexInviteResetGrantType(&hasNoRewards, "workspace_credits"))
	require.Equal(t, "rate_limit_reset", normalizeCodexInviteResetGrantType(&hasRewards, "rate_limit_reset_credit"))
	require.Equal(t, "workspace_credits", normalizeCodexInviteResetGrantType(&hasRewards, "workspace_credits"))
	require.Equal(t, "unknown", normalizeCodexInviteResetGrantType(&hasRewards, "future_reward"))
	require.Equal(t, "unknown", normalizeCodexInviteResetGrantType(nil, ""))
}

func TestNormalizeCodexInviteEmailsRejectsInvalidAndTooMany(t *testing.T) {
	_, err := normalizeCodexInviteEmails([]string{"bad-email"})
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))

	_, err = normalizeCodexInviteEmails([]string{"a@x.com,b@x.com,c@x.com,d@x.com,e@x.com,f@x.com"})
	require.Error(t, err)
	require.Equal(t, "CODEX_INVITE_RESET_EMAIL_LIMIT", apperror.Reason(err))
}
