// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	gin "github.com/gin-gonic/gin"
)

func ClearOAuthLoginCookies(c *gin.Context) {
	secureCookie := IsRequestHTTPS(c)

	ClearOAuthPendingSessionCookie(c, secureCookie)
	ClearOAuthPendingBrowserCookie(c, secureCookie)
	ClearOAuthBindAccessTokenCookie(c, secureCookie)

	LinuxDoClearCookie(c, LinuxDoOAuthStateCookieName, secureCookie)
	LinuxDoClearCookie(c, LinuxDoOAuthVerifierCookie, secureCookie)
	LinuxDoClearCookie(c, LinuxDoOAuthRedirectCookie, secureCookie)
	LinuxDoClearCookie(c, LinuxDoOAuthIntentCookieName, secureCookie)
	LinuxDoClearCookie(c, LinuxDoOAuthBindUserCookieName, secureCookie)

	OIDCClearCookie(c, OidcOAuthStateCookieName, secureCookie)
	OIDCClearCookie(c, OidcOAuthVerifierCookie, secureCookie)
	OIDCClearCookie(c, OidcOAuthRedirectCookie, secureCookie)
	OIDCClearCookie(c, OidcOAuthNonceCookie, secureCookie)
	OIDCClearCookie(c, OidcOAuthIntentCookieName, secureCookie)
	OIDCClearCookie(c, OidcOAuthBindUserCookieName, secureCookie)

	WeChatClearCookie(c, WechatOAuthStateCookieName, secureCookie)
	WeChatClearCookie(c, WechatOAuthRedirectCookieName, secureCookie)
	WeChatClearCookie(c, WechatOAuthIntentCookieName, secureCookie)
	WeChatClearCookie(c, WechatOAuthModeCookieName, secureCookie)
	WeChatClearCookie(c, WechatOAuthBindUserCookieName, secureCookie)

}
