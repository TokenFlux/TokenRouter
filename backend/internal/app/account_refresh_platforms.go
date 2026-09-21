package app

import (
	"fmt"
	"log"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// accountRefreshRegistrations 区分平台注册集合，避免装配重复创建执行器。
type accountRefreshRegistrations []account.RefreshRegistration

func provideRefreshPlatforms(claude *account.ClaudeAuthorization, openai *account.OpenAIAuthorization, gemini *account.GeminiAuthorization, antigravity *account.AntigravityAuthorization, qoder *provider.QoderAuthorization, grok *account.GrokAuthorization, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles) accountRefreshRegistrations {
	claudeRefresh := &account.ClaudeTokenRefresher{Authorization: claude}
	openaiRefresh := &account.OpenAITokenRefresher{Authorization: openai}
	geminiRefresh := &account.GeminiTokenRefresher{Authorization: gemini, Key: provider.GeminiTokenCacheKey}
	agRefresh := &account.AntigravityRefreshRules{
		RefreshAccountToken:     antigravity.RefreshAccountToken,
		BuildAccountCredentials: antigravity.BuildAccountCredentials,
		Printf:                  func(format string, args ...any) { _, _ = fmt.Printf(format, args...) },
		Logf:                    log.Printf,
	}
	qoderRefresh := provider.NewQoderTokenRefresher(provider.QoderRefreshOptions{
		Transport: transport, Profiles: profiles, BuildCredentials: qoder.Core.BuildAccountCredentials,
	})
	grokRefresh := account.NewGrokTokenRefresher(grok)
	return accountRefreshRegistrations{
		{Platform: account.PlatformAnthropic, Refresher: claudeRefresh, Executor: claudeRefresh},
		{Platform: account.PlatformOpenAI, Refresher: openaiRefresh, Executor: openaiRefresh},
		{Platform: account.PlatformGemini, Refresher: geminiRefresh, Executor: geminiRefresh},
		{Platform: account.PlatformAntigravity, Refresher: agRefresh, Executor: agRefresh},
		{Platform: account.PlatformQoder, Refresher: qoderRefresh, Executor: qoderRefresh},
		{Platform: account.PlatformGrok, Refresher: grokRefresh, Executor: grokRefresh},
	}
}
