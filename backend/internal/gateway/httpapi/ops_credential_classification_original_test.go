package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsClassificationTreatsCredentialFailureAsAuthNotInference(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(OpsUpstreamStatusCodeKey, http.StatusForbidden)
	c.Set(OpsUpstreamErrorMessageKey, "stale inference message")
	c.Set(OpsUpstreamErrorDetailKey, "stale inference detail")
	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
		{Stage: "inference", UpstreamStatusCode: http.StatusForbidden, Message: "stale inference message", Detail: "stale inference detail"},
		{
			Stage:              opscore.ErrorPhaseAccountAuth,
			Scope:              "account",
			Reason:             "grok_oauth_credential_revoked",
			UpstreamStatusCode: 0,
			Message:            "Grok OAuth credentials require account action",
		},
	})

	phase, _, owner, source := classifyOpsErrorLog(c, "upstream_error", "No healthy Grok OAuth account is currently available", "", http.StatusServiceUnavailable)
	require.Equal(t, "account_auth", phase)
	require.Equal(t, "provider", owner)
	require.Equal(t, "gateway", source)

	entry := &opscore.OpsInsertErrorLogInput{}
	applyOpsUpstreamFieldsFromContext(c, entry)
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Zero(t, *entry.UpstreamStatusCode)
	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, "Grok OAuth credentials require account action", *entry.UpstreamErrorMessage)
	require.Nil(t, entry.UpstreamErrorDetail)
	require.Len(t, entry.UpstreamErrors, 2)
	require.Equal(t, http.StatusForbidden, entry.UpstreamErrors[0].UpstreamStatusCode)
}
