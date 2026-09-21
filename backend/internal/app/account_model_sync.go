package app

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
)

// provideAccountModelSync 直接绑定原生账号、凭据和请求端口，生命周期只持有一个目录查询实例。
func provideAccountModelSync(store *postgres.AccountStore, transport httpclient.UpstreamTransport, claude *account.ClaudeTokenSource, gemini *account.GeminiTokenSource, grok *account.GrokTokenSource, antigravity *account.AntigravityTokenSource, profiles *egressprovider.TLSProfiles, tasks *provider.ProbeTasks, settings *gateway.RuntimeSettings, cfg *config.Config, manager *lifecycle.Manager) *account.ModelSyncService {
	policy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	limit := resolveModelsListReadLimit(cfg)
	queries := &provider.ModelCatalogue{Transport: transport, Profiles: profiles, ClaudeTokens: claude, GeminiTokens: gemini, GrokTokens: grok, AntigravityTokens: antigravity, DefaultGrokBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings), Read: store.GetByID, EnsureTask: tasks.Ensure,
		Options: provider.ModelCatalogueOptions{ValidateURL: policy.Validate, OperatorValidator: policy.Validate, BodyLimit: limit, CodexModelsURL: provider.DefaultCodexModelsURL},
	}
	core := account.NewModelSyncService(queries.FetchUpstreamSupportedModels)
	manager.Register(lifecycle.Hook{Name: "AccountModelList", StopOrder: 26, Stop: core.StopContext})
	return core
}
