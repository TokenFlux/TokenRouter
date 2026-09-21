package identityhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"

	"github.com/TokenFlux/TokenRouter/ent/pendingauthsession"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLogoutClearsOAuthStateCookiesAndConsumesPendingSession(t *testing.T) {
	handler, client := newOAuthPendingFlowTestHandler(t, false)
	ctx := context.Background()

	session, err := client.PendingAuthSession.Create().
		SetSessionToken("logout-pending-session-token").
		SetIntent("login").
		SetProviderType("oidc").
		SetProviderKey("https://issuer.example").
		SetProviderSubject("logout-subject-123").
		SetBrowserSessionKey("logout-browser-session-key").
		SetResolvedEmail("logout@example.com").
		SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).
		Save(ctx)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingSessionCookieName, Value: identityhttp.EncodeCookieValue(session.SessionToken)})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthPendingBrowserCookieName, Value: identityhttp.EncodeCookieValue("logout-browser-session-key")})
	req.AddCookie(&http.Cookie{Name: identityhttp.OauthBindAccessTokenCookieName, Value: "bind-access-token"})
	req.AddCookie(&http.Cookie{Name: identityhttp.LinuxDoOAuthStateCookieName, Value: identityhttp.EncodeCookieValue("linuxdo-state")})
	req.AddCookie(&http.Cookie{Name: identityhttp.OidcOAuthStateCookieName, Value: identityhttp.EncodeCookieValue("oidc-state")})
	req.AddCookie(&http.Cookie{Name: identityhttp.WechatOAuthStateCookieName, Value: identityhttp.EncodeCookieValue("wechat-state")})
	req.AddCookie(&http.Cookie{Name: paymenthttp.WechatPaymentOAuthStateName, Value: identityhttp.EncodeCookieValue("wechat-payment-state")})
	ginCtx.Request = req

	handler.Logout(ginCtx)

	require.Equal(t, http.StatusOK, recorder.Code)

	cookies := recorder.Result().Cookies()
	for _, name := range []string{identityhttp.OauthPendingSessionCookieName, identityhttp.OauthPendingBrowserCookieName, identityhttp.OauthBindAccessTokenCookieName, identityhttp.LinuxDoOAuthStateCookieName, identityhttp.OidcOAuthStateCookieName, identityhttp.WechatOAuthStateCookieName, paymenthttp.WechatPaymentOAuthStateName} {
		cookie := findCookie(cookies, name)
		require.NotNil(t, cookie, name)
		require.Equal(t, -1, cookie.MaxAge, name)
		require.True(t, cookie.HttpOnly, name)
	}

	storedSession, err := client.PendingAuthSession.Query().
		Where(pendingauthsession.IDEQ(session.ID)).
		Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, storedSession.ConsumedAt)
}
