package account

// CodexClientRestrictionDetectionResult 是 codex_cli_only 统一检测入口结果。
type CodexClientRestrictionDetectionResult struct {
	Enabled bool
	Matched bool
	Reason  string
	Policy  string
}

// DetectClient 只按需读取客户端元数据，后台探针不需要构造 Gin 请求。
func DetectCodexClient(options CodexClientOptions, readClient func() (string, string), account *Record, globalAllowedClients []string, routerMatched bool) CodexClientRestrictionDetectionResult {
	policy := OpenAIOAuthClientPolicyAny
	if account != nil {
		policy = account.GetOpenAIOAuthClientPolicy()
	}
	if account == nil || policy == OpenAIOAuthClientPolicyAny {
		return CodexClientRestrictionDetectionResult{
			Enabled: false,
			Matched: false,
			Reason:  CodexClientRestrictionReasonDisabled,
			Policy:  policy,
		}
	}

	if options.ForceCLI {
		return CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: true,
			Reason:  CodexClientRestrictionReasonForceCodexCLI,
			Policy:  policy,
		}
	}

	if policy == OpenAIOAuthClientPolicyTLSRouterMatchedOnly {
		if account.GetTLSFingerprintRouterID() <= 0 {
			return CodexClientRestrictionDetectionResult{
				Enabled: true,
				Matched: false,
				Reason:  CodexClientRestrictionReasonTLSRouterMissing,
				Policy:  policy,
			}
		}
		if routerMatched {
			return CodexClientRestrictionDetectionResult{
				Enabled: true,
				Matched: true,
				Reason:  CodexClientRestrictionReasonMatchedTLSRouter,
				Policy:  policy,
			}
		}
		return CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: false,
			Reason:  CodexClientRestrictionReasonNotMatchedTLSRouter,
			Policy:  policy,
		}
	}

	userAgent, originator := readClient()
	if options.OfficialUserAgent(userAgent) {
		return CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: true,
			Reason:  CodexClientRestrictionReasonMatchedUA,
			Policy:  policy,
		}
	}
	if options.OfficialOriginator(originator) {
		return CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: true,
			Reason:  CodexClientRestrictionReasonMatchedOriginator,
			Policy:  policy,
		}
	}

	// 官方客户端白名单未命中时，先尝试账号级额外放行的命名客户端预设（如 Claude Code codex 插件）。
	if allowed := account.GetCodexCLIOnlyAllowedClients(); len(allowed) > 0 &&
		options.AllowedClients(userAgent, originator, allowed) {
		return CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: true,
			Reason:  CodexClientRestrictionReasonMatchedAllowedClient,
			Policy:  policy,
		}
	}

	// 再尝试由更高作用域（全局设置）注入的额外放行客户端列表。
	if len(globalAllowedClients) > 0 &&
		options.AllowedClients(userAgent, originator, globalAllowedClients) {
		return CodexClientRestrictionDetectionResult{
			Enabled: true,
			Matched: true,
			Reason:  CodexClientRestrictionReasonMatchedGlobalAllowedClient,
			Policy:  policy,
		}
	}

	return CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: false,
		Reason:  CodexClientRestrictionReasonNotMatchedUA,
		Policy:  policy,
	}
}

const (
	// CodexClientRestrictionReasonDisabled 表示账号未开启客户端访问限制。
	CodexClientRestrictionReasonDisabled = "openai_oauth_client_policy_any"
	// CodexClientRestrictionReasonMatchedUA 表示请求命中官方客户端 UA 白名单。
	CodexClientRestrictionReasonMatchedUA = "official_client_user_agent_matched"
	// CodexClientRestrictionReasonMatchedOriginator 表示请求命中官方客户端 originator 白名单。
	CodexClientRestrictionReasonMatchedOriginator = "official_client_originator_matched"
	// CodexClientRestrictionReasonMatchedAllowedClient 表示请求命中账号级额外放行的命名客户端预设。
	CodexClientRestrictionReasonMatchedAllowedClient = "allowed_client_matched"
	// CodexClientRestrictionReasonMatchedGlobalAllowedClient 表示请求命中全局额外放行的命名客户端预设。
	CodexClientRestrictionReasonMatchedGlobalAllowedClient = "global_allowed_client_matched"
	// CodexClientRestrictionReasonNotMatchedUA 表示请求未命中官方客户端 UA 白名单。
	CodexClientRestrictionReasonNotMatchedUA = "official_client_user_agent_not_matched"
	// CodexClientRestrictionReasonForceCodexCLI 表示通过 ForceCodexCLI 配置兜底放行。
	CodexClientRestrictionReasonForceCodexCLI = "force_codex_cli_enabled"
	// CodexClientRestrictionReasonMatchedTLSRouter 表示请求命中账号绑定的 TLS 路由器。
	CodexClientRestrictionReasonMatchedTLSRouter = "tls_router_matched"
	// CodexClientRestrictionReasonNotMatchedTLSRouter 表示请求未命中账号绑定的 TLS 路由器。
	CodexClientRestrictionReasonNotMatchedTLSRouter = "tls_router_not_matched"
	// CodexClientRestrictionReasonTLSRouterMissing 表示账号策略要求 TLS 路由器命中，但账号未绑定路由器。
	CodexClientRestrictionReasonTLSRouterMissing = "tls_router_missing"
)

// CodexClientOptions 注入平台字符串识别，核心只决定账号客户端访问策略。
type CodexClientOptions struct {
	ForceCLI           bool
	OfficialUserAgent  func(string) bool
	OfficialOriginator func(string) bool
	AllowedClients     func(string, string, []string) bool
}

// ClientRestrictionDetector 只接收已读取的账号与传输匹配结果，HTTP 元数据仍按需读取。
type ClientRestrictionDetector interface {
	DetectClient(func() (string, string), *Record, []string, bool) CodexClientRestrictionDetectionResult
}

// CodexClientDetector 绑定平台解析选项，所有策略仍由 DetectCodexClient 唯一执行。
type CodexClientDetector struct{ Options CodexClientOptions }

func (d *CodexClientDetector) DetectClient(read func() (string, string), value *Record, allowed []string, matched bool) CodexClientRestrictionDetectionResult {
	return DetectCodexClient(d.Options, read, value, allowed, matched)
}
