package config

// defaults.go initializes version 2 startup defaults on a fresh Config value
// before YAML node decoding. Constructing typed defaults in one place populates
// omitted keys without activating implicit environment bindings or altering
// explicit file-presence tracking for client-IP and OIDC flags.

// defaultConfig constructs a Config populated with current version 2 defaults.
// Call before decoding the selected YAML document so omitted keys retain their
// startup defaults without marking explicit file-presence flags.
func defaultConfig() Config {
	var cfg Config
	cfg.ConfigVersion = CurrentConfigVersion
	cfg.Auth.JWT.AccessTokenTTLMinutes = 1440
	cfg.Gateway.OpenAI.ProxyStreamCircuit.Enabled = true
	cfg.Provider.Antigravity.FallbackCooldownSeconds = 60
	cfg.Provider.Antigravity.ForwardEndpoint = "auto"
	cfg.Ops.WebSocket.TrustProxy = true
	cfg.Ops.WebSocket.TrustedProxies = []string{"127.0.0.0/8", "::1/128"}
	cfg.Ops.WebSocket.OriginPolicy = "permissive"
	cfg.Ops.WebSocket.MaxConns = 100
	cfg.Ops.WebSocket.MaxConnsPerIP = 20

	cfg.Plugins = defaultPluginsConfig()
	// Server
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 8080
	cfg.Server.Mode = "release"
	cfg.Server.EnableServerTiming = false
	cfg.Site.FrontendURL = ""
	cfg.Server.ReadHeaderTimeoutSeconds = 10 // 10-second header read timeout.
	cfg.Server.MaxHeaderBytes = 64 * 1024
	cfg.Server.IdleTimeoutSeconds = 120 // 120-second idle timeout.
	cfg.Server.MaxRequestBodyBytes = 256 * 1024 * 1024
	// H2C defaults.
	cfg.Server.H2C.Enabled = false
	cfg.Server.H2C.MaxConcurrentStreams = 50                   // 50 concurrent streams.
	cfg.Server.H2C.IdleTimeoutSeconds = 75                     // 75 seconds.
	cfg.Server.H2C.MaxReadFrameBytes = 1 << 20                 // 1 MiB.
	cfg.Server.H2C.MaxUploadBufferPerConnectionBytes = 2 << 20 // 2 MiB
	cfg.Server.H2C.MaxUploadBufferPerStreamBytes = 512 << 10   // 512 KiB
	// The collector starts from the UI; these values supply its listen settings.
	cfg.Egress.TLSFingerprintCollector.Host = "0.0.0.0"
	cfg.Egress.TLSFingerprintCollector.Port = 8443
	cfg.Egress.TLSFingerprintCollector.PublicBaseURL = ""
	cfg.Egress.TLSFingerprintCollector.CertFile = ""
	cfg.Egress.TLSFingerprintCollector.KeyFile = ""
	cfg.Egress.TLSFingerprintCollector.SessionTTLSeconds = 1800
	cfg.Egress.TLSFingerprintCollector.MaxRecordsPerSession = 20

	// Log
	cfg.Log.Level = "info"
	cfg.Log.Format = "console"
	cfg.Log.ServiceName = "tokenrouter"
	cfg.Environment = "production"
	cfg.Log.Caller = true
	cfg.Log.StacktraceLevel = "error"
	cfg.Log.Output.ToStdout = true
	cfg.Log.Output.ToFile = true
	cfg.Log.Output.FilePath = ""
	cfg.Log.Rotation.MaxSizeMB = 100
	cfg.Log.Rotation.MaxBackups = 10
	cfg.Log.Rotation.MaxAgeDays = 7
	cfg.Log.Rotation.Compress = true
	cfg.Log.Rotation.LocalTime = true
	cfg.Log.Sampling.Enabled = false
	cfg.Log.Sampling.Initial = 100
	cfg.Log.Sampling.Thereafter = 100

	// CORS
	cfg.Security.CORS.AllowedOrigins = []string{}
	cfg.Security.CORS.AllowCredentials = true

	// Passkeys are disabled by default; enabling requires a relying-party domain
	// and trusted browser origins.
	cfg.Auth.WebAuthn.Enabled = false
	cfg.Auth.WebAuthn.RPDisplayName = "TokenRouter"
	cfg.Auth.WebAuthn.RPID = ""
	cfg.Auth.WebAuthn.RPOrigins = []string{}

	// Security
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.UpstreamHosts = []string{
		"api.openai.com",
		"api.anthropic.com",
		"api.kimi.com",
		"api.moonshot.ai",
		"api.moonshot.cn",
		"open.bigmodel.cn",
		"api.z.ai",
		"api.deepseek.com",
		"api.typesafe.ai",
		"api.minimaxi.com",
		"generativelanguage.googleapis.com",
		"cloudcode-pa.googleapis.com",
		"*.openai.azure.com",
	}
	cfg.Security.URLAllowlist.ModelCatalogHosts = []string{
		"models.dev",
		"raw.githubusercontent.com",
	}
	cfg.Security.URLAllowlist.CrsSyncHosts = []string{}
	cfg.Security.URLAllowlist.AllowPrivateHosts = true
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.ResponseHeaders.Enabled = true
	cfg.Gateway.ResponseHeaders.AdditionalAllowed = []string{}
	cfg.Gateway.ResponseHeaders.ForceRemove = []string{}
	cfg.Security.CSP.Enabled = true
	cfg.Security.CSP.Policy = DefaultCSPPolicy
	cfg.Server.ClientIP.TrustForwardedHeaders = true

	// Security - disable direct fallback on proxy error
	cfg.OutboundProxy.AllowDirectOnError = false

	// Billing
	cfg.Billing.BalanceCheckCircuitBreaker.Enabled = true
	cfg.Billing.BalanceCheckCircuitBreaker.FailureThreshold = 5
	cfg.Billing.BalanceCheckCircuitBreaker.ResetTimeoutSeconds = 30
	cfg.Billing.BalanceCheckCircuitBreaker.HalfOpenRequests = 3
	cfg.Billing.MinimumBalanceReserve = 0.000001

	// Turnstile
	cfg.Auth.RequireCaptcha = false

	// LinuxDo Connect OAuth login.
	cfg.Auth.OAuth.LinuxDo.Enabled = false
	cfg.Auth.OAuth.LinuxDo.ClientID = ""
	cfg.Auth.OAuth.LinuxDo.ClientSecret = ""
	cfg.Auth.OAuth.LinuxDo.AuthorizeURL = "https://connect.linux.do/oauth2/authorize"
	cfg.Auth.OAuth.LinuxDo.TokenURL = "https://connect.linux.do/oauth2/token"
	cfg.Auth.OAuth.LinuxDo.UserInfoURL = "https://connect.linux.do/api/user"
	cfg.Auth.OAuth.LinuxDo.Scopes = "user"
	cfg.Auth.OAuth.LinuxDo.RedirectURL = ""
	cfg.Auth.OAuth.LinuxDo.FrontendRedirectURL = "/auth/linuxdo/callback"
	cfg.Auth.OAuth.LinuxDo.TokenAuthMethod = "client_secret_post"
	cfg.Auth.OAuth.LinuxDo.UsePKCE = false
	cfg.Auth.OAuth.LinuxDo.UserInfoEmailPath = ""
	cfg.Auth.OAuth.LinuxDo.UserInfoIDPath = ""
	cfg.Auth.OAuth.LinuxDo.UserInfoUsernamePath = ""

	// WeChat Connect OAuth login.
	cfg.Auth.OAuth.WeChat.Enabled = false
	cfg.Auth.OAuth.WeChat.OpenAppID = ""
	cfg.Auth.OAuth.WeChat.OpenAppSecret = ""
	cfg.Auth.OAuth.WeChat.MPAppID = ""
	cfg.Auth.OAuth.WeChat.MPAppSecret = ""
	cfg.Auth.OAuth.WeChat.MobileAppID = ""
	cfg.Auth.OAuth.WeChat.MobileAppSecret = ""
	cfg.Auth.OAuth.WeChat.OpenEnabled = false
	cfg.Auth.OAuth.WeChat.MPEnabled = false
	cfg.Auth.OAuth.WeChat.MobileEnabled = false
	cfg.Auth.OAuth.WeChat.Mode = defaultWeChatConnectMode
	cfg.Auth.OAuth.WeChat.Scopes = defaultWeChatConnectScopes
	cfg.Auth.OAuth.WeChat.RedirectURL = ""
	cfg.Auth.OAuth.WeChat.FrontendRedirectURL = defaultWeChatConnectFrontendRedirect

	// Generic OIDC login.
	cfg.Auth.OAuth.OIDC.Enabled = false
	cfg.Auth.OAuth.OIDC.ProviderName = "OIDC"
	cfg.Auth.OAuth.OIDC.ClientID = ""
	cfg.Auth.OAuth.OIDC.ClientSecret = ""
	cfg.Auth.OAuth.OIDC.IssuerURL = ""
	cfg.Auth.OAuth.OIDC.DiscoveryURL = ""
	cfg.Auth.OAuth.OIDC.AuthorizeURL = ""
	cfg.Auth.OAuth.OIDC.TokenURL = ""
	cfg.Auth.OAuth.OIDC.UserInfoURL = ""
	cfg.Auth.OAuth.OIDC.JWKSURL = ""
	cfg.Auth.OAuth.OIDC.Scopes = "openid email profile"
	cfg.Auth.OAuth.OIDC.RedirectURL = ""
	cfg.Auth.OAuth.OIDC.FrontendRedirectURL = "/auth/oidc/callback"
	cfg.Auth.OAuth.OIDC.TokenAuthMethod = "client_secret_post"
	cfg.Auth.OAuth.OIDC.UsePKCE = true
	cfg.Auth.OAuth.OIDC.ValidateIDToken = true
	cfg.Auth.OAuth.OIDC.AllowedSigningAlgs = "RS256,ES256,PS256"
	cfg.Auth.OAuth.OIDC.ClockSkewSeconds = 120
	cfg.Auth.OAuth.OIDC.RequireEmailVerified = false
	cfg.Auth.OAuth.OIDC.UserInfoEmailPath = ""
	cfg.Auth.OAuth.OIDC.UserInfoIDPath = ""
	cfg.Auth.OAuth.OIDC.UserInfoUsernamePath = ""

	// DingTalk Connect OAuth login.
	cfg.Auth.OAuth.DingTalk.Enabled = false
	cfg.Auth.OAuth.DingTalk.AuthorizeURL = "https://login.dingtalk.com/oauth2/auth"
	cfg.Auth.OAuth.DingTalk.TokenURL = "https://api.dingtalk.com/v1.0/oauth2/userAccessToken"
	cfg.Auth.OAuth.DingTalk.UserInfoURL = "https://api.dingtalk.com/v1.0/contact/users/me"
	cfg.Auth.OAuth.DingTalk.Scopes = "openid"
	cfg.Auth.OAuth.DingTalk.FrontendRedirectURL = "/auth/dingtalk/callback"
	cfg.Auth.OAuth.DingTalk.DingTalkAppKind = "internal_app"
	cfg.Auth.OAuth.DingTalk.AppType = "public"
	cfg.Auth.OAuth.DingTalk.CorpRestrictionPolicy = "none"
	cfg.Auth.OAuth.DingTalk.RequireEmail = true

	// Database
	cfg.Database.Host = "localhost"
	cfg.Database.Port = 5432
	cfg.Database.User = "postgres"
	cfg.Database.Password = "postgres"
	cfg.Database.DBName = "tokenrouter"
	cfg.Database.SSLMode = "prefer"
	cfg.Database.MaxOpenConns = 256
	cfg.Database.MaxIdleConns = 128
	cfg.Database.ConnMaxLifetimeMinutes = 30
	cfg.Database.ConnMaxIdleTimeMinutes = 5

	// Redis
	cfg.Redis.Host = "localhost"
	cfg.Redis.Port = 6379
	cfg.Redis.Username = ""
	cfg.Redis.Password = ""
	cfg.Redis.DB = 0
	cfg.Redis.DialTimeoutSeconds = 5
	cfg.Redis.ReadTimeoutSeconds = 3
	cfg.Redis.WriteTimeoutSeconds = 3
	cfg.Redis.PoolSize = 1024
	cfg.Redis.MinIdleConns = 128
	cfg.Redis.EnableTLS = false

	// Batch Image queue
	cfg.BatchImage.MaxItemsPerJob = 200
	cfg.BatchImage.MaxOutputImagesPerJob = 200
	cfg.BatchImage.MaxOutputImagesPerItem = 4
	cfg.BatchImage.MaxPromptBytesPerItem = 8000
	cfg.BatchImage.MaxReferenceImagesPerJob = 1000
	cfg.BatchImage.MaxReferenceInlineBytesPerJob = 134217728
	cfg.BatchImage.DefaultResponseMimeType = "image/png"
	cfg.BatchImage.DefaultImageSize = "1K"
	cfg.BatchImage.MaxDownloadItemsZip = 200
	cfg.BatchImage.MaxDownloadBytesPerRequest = 536870912
	cfg.BatchImage.MaxDownloadDurationSeconds = 600
	cfg.BatchImage.MaxDownloadConcurrencyPerUser = 1
	cfg.BatchImage.InputRetentionAfterTerminalHours = 24
	cfg.BatchImage.OutputRetentionAfterTerminalHours = 72
	cfg.BatchImage.CleanupIntervalMinutes = 30
	cfg.BatchImage.CleanupBatchSize = 100
	cfg.BatchImage.Queue.ReadyKey = "batch_image:queue:ready"
	cfg.BatchImage.Queue.DelayedKey = "batch_image:queue:delayed"
	cfg.BatchImage.Queue.ActiveKey = "batch_image:queue:active"
	cfg.BatchImage.Queue.InflightKeyPrefix = "batch_image:queue:inflight:"
	cfg.BatchImage.Queue.LockKeyPrefix = "batch_image:queue:lock:"
	cfg.BatchImage.Queue.InflightTTLSeconds = 604800
	cfg.BatchImage.Queue.JobLockTTLSeconds = 300
	cfg.BatchImage.Queue.DefaultRequeueDelaySeconds = 30
	cfg.BatchImage.Queue.ErrorRetryDelaySeconds = 60
	cfg.BatchImage.Queue.LockConflictDelaySeconds = 5
	cfg.BatchImage.Queue.StaleActiveAfterSeconds = 600
	cfg.BatchImage.Queue.DelayedMoverIntervalSeconds = 5
	cfg.BatchImage.Queue.RecoveryIntervalSeconds = 300
	cfg.BatchImage.Queue.DelayedMoveLimit = 100
	cfg.BatchImage.Queue.RecoverLimit = 100
	cfg.BatchImage.Vertex.ProjectID = ""
	cfg.BatchImage.Vertex.Location = "global"
	cfg.BatchImage.Vertex.ManagedGCSBucket = ""
	cfg.BatchImage.Vertex.ManagedGCSPrefix = "batch-image/{env}/{batch_id}"
	cfg.BatchImage.Vertex.BatchPredictionBaseURL = ""
	cfg.BatchImage.Vertex.GCSBaseURL = ""

	// Creative Studio.
	cfg.Creative.Enabled = true
	cfg.Creative.QueueWorkerEnabled = true
	cfg.Creative.TransientTTLSeconds = 1800
	cfg.Creative.MaxAssetBytes = 33554432
	cfg.Creative.MaxTotalInputBytes = 67108864
	cfg.Creative.MaxPromptChars = 8000
	cfg.Creative.DefaultImageSize = "1K"
	cfg.Creative.Queue.ReadyKey = "creative:queue:ready"
	cfg.Creative.Queue.DelayedKey = "creative:queue:delayed"
	cfg.Creative.Queue.ActiveKey = "creative:queue:active"
	cfg.Creative.Queue.InflightKeyPrefix = "creative:queue:inflight:"
	cfg.Creative.Queue.LockKeyPrefix = "creative:queue:lock:"
	cfg.Creative.Queue.InflightTTLSeconds = 604800
	cfg.Creative.Queue.JobLockTTLSeconds = 300
	cfg.Creative.Queue.DefaultRequeueDelaySeconds = 30
	cfg.Creative.Queue.ErrorRetryDelaySeconds = 60
	cfg.Creative.Queue.LockConflictDelaySeconds = 5
	cfg.Creative.Queue.StaleActiveAfterSeconds = 600
	cfg.Creative.Queue.DelayedMoverIntervalSeconds = 5
	cfg.Creative.Queue.RecoveryIntervalSeconds = 300
	cfg.Creative.Queue.DelayedMoveLimit = 100
	cfg.Creative.Queue.RecoverLimit = 100
	cfg.Creative.ExecuteTimeoutSeconds = 300
	cfg.Creative.MaxExecuteAttempts = 3

	// Ops
	cfg.Ops.Enabled = true
	cfg.Ops.Cleanup.Enabled = true
	cfg.Ops.Cleanup.Schedule = "0 3 * * *"
	cfg.Ops.Cleanup.BatchSize = 1000
	cfg.Ops.Cleanup.BatchPauseMS = 200
	// Retain 30 days across Ops datasets by default.
	cfg.RequestLog.RetentionDays = 30
	cfg.Ops.Cleanup.ErrorLogRetentionDays = 30
	cfg.Ops.Cleanup.SystemLogRetentionDays = 30
	cfg.Ops.Cleanup.MinuteMetricsRetentionDays = 30
	cfg.Ops.Cleanup.HourlyMetricsRetentionDays = 30
	cfg.Ops.Aggregation.Enabled = true
	// Register the accepted collector-cache lifetime copied into ops.Options. The
	// current MetricsQueries reader does not consume it; sixty-five seconds
	// exceeds the collector's default/minimum one-minute interval, while runtime
	// cadence can be longer.

	// JWT
	cfg.Auth.JWT.Secret = ""
	cfg.Auth.JWT.RefreshTokenTTLDays = 30 // 30-day refresh-token lifetime.

	// TOTP
	cfg.Security.SecretEncryptionKey = ""

	// Default
	// Admin credentials are created via the setup flow (web wizard / CLI / AUTO_SETUP).
	// Do not ship fixed defaults here to avoid insecure "known credentials" in
	// production.
	cfg.UserDefaults.Concurrency = 5
	cfg.UserDefaults.Balance = 0
	cfg.APIKey.Prefix = "sk-"
	cfg.Billing.FallbackRateMultiplier = 1.0

	// RateLimit
	cfg.Provider.Cooldown.OverloadMinutes = 10
	cfg.Provider.Cooldown.OAuth401Minutes = 10

	// Pricing uses the models.dev catalog and local supplements for additional
	// billing dimensions.
	cfg.ModelCatalog.CatalogURL = "https://models.dev/experimental/v2.0/api.json"
	cfg.Storage.Dir = "./data"
	cfg.ModelCatalog.SupplementFile = ""
	cfg.ModelCatalog.CheckIntervalMinutes = 10

	// Default time zone for local-day statistics.
	cfg.Timezone = "Asia/Shanghai"
	cfg.Team.Enabled = true
	cfg.Team.SelfServiceEnabled = true
	cfg.Team.DefaultMaxMembers = 10

	// API Key auth cache
	cfg.APIKey.AuthCache.L1Size = 65535
	cfg.APIKey.AuthCache.L1TTLSeconds = 15
	cfg.APIKey.AuthCache.L2TTLSeconds = 300
	cfg.APIKey.AuthCache.NegativeTTLSeconds = 30
	cfg.APIKey.AuthCache.JitterPercent = 10
	cfg.APIKey.AuthCache.Singleflight = true
	cfg.APIKey.LookupConcurrency = 64
	cfg.APIKey.InvalidKeyAbuse.Enabled = true
	cfg.APIKey.InvalidKeyAbuse.Threshold = 120
	cfg.APIKey.InvalidKeyAbuse.WindowSeconds = 60
	cfg.APIKey.InvalidKeyAbuse.BlockSeconds = 60
	cfg.APIKey.InvalidKeyAbuse.Capacity = 16384

	// Subscription auth L1 cache

	// Dashboard cache
	cfg.Usage.DashboardCache.Enabled = true
	cfg.Usage.DashboardCache.KeyPrefix = "tokenrouter:"
	cfg.Usage.DashboardCache.StatsFreshTTLSeconds = 15
	cfg.Usage.DashboardCache.StatsTTLSeconds = 30
	cfg.Usage.DashboardCache.StatsRefreshTimeoutSeconds = 30

	// Dashboard aggregation
	cfg.Usage.Aggregation.Enabled = true
	cfg.Usage.Aggregation.IntervalSeconds = 60
	cfg.Usage.Aggregation.LookbackSeconds = 120
	cfg.Usage.Aggregation.BackfillEnabled = false
	cfg.Usage.Aggregation.BackfillMaxDays = 31
	cfg.Usage.Retention.LogsDays = 90
	cfg.Usage.Retention.BillingDedupDays = 365
	cfg.Usage.Aggregation.HourlyRetentionDays = 180
	cfg.Usage.Aggregation.DailyRetentionDays = 730
	cfg.Usage.Aggregation.RecomputeDays = 2

	// Usage cleanup task
	cfg.Usage.CleanupTasks.Enabled = true
	cfg.Usage.CleanupTasks.MaxRangeDays = 31
	cfg.Usage.CleanupTasks.BatchSize = 5000
	cfg.Usage.CleanupTasks.WorkerIntervalSeconds = 10
	cfg.Usage.CleanupTasks.TaskTimeoutSeconds = 1800

	// Usage export task
	cfg.Usage.ExportTasks.Enabled = true
	cfg.Usage.ExportTasks.Dir = ""
	cfg.Usage.ExportTasks.RetentionHours = 24
	cfg.Usage.ExportTasks.MaxActivePerUser = 2
	cfg.Usage.ExportTasks.MaxActivePerAdmin = 5
	cfg.Usage.ExportTasks.BatchSize = 2000
	cfg.Usage.ExportTasks.WorkerConcurrency = 2
	cfg.Usage.ExportTasks.WorkerIntervalSeconds = 10
	cfg.Usage.ExportTasks.TaskTimeoutSeconds = 3600

	// Idempotency
	cfg.Idempotency.ObserveOnly = true
	cfg.Idempotency.DefaultTTLSeconds = 86400
	cfg.Idempotency.SystemOperationTTLSeconds = 3600
	cfg.Idempotency.ProcessingTimeoutSeconds = 30
	cfg.Idempotency.FailedRetryBackoffSeconds = 5
	cfg.Idempotency.MaxStoredResponseBytes = 64 * 1024
	cfg.Idempotency.CleanupIntervalSeconds = 60
	cfg.Idempotency.CleanupBatchSize = 500

	// Gateway
	cfg.Gateway.Transport.ResponseHeaderTimeoutSeconds = 600 // Allow ten minutes for upstream LLM queueing before response headers.
	cfg.Gateway.OpenAI.ResponseHeaderTimeoutSeconds = 0
	cfg.Gateway.OpenAI.CompactModel = ""
	cfg.Gateway.Grok.ResponseHeaderTimeoutSeconds = 120
	cfg.Gateway.OpenAI.FirstOutputTimeoutSeconds = 0
	cfg.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds = 0
	cfg.Gateway.UpstreamErrorBodyLog.Enabled = true
	cfg.Gateway.UpstreamErrorBodyLog.MaxBytes = 2048
	cfg.Gateway.Anthropic.InjectBetaForAPIKey = false
	cfg.Gateway.Failover.MessagesOn400 = false
	cfg.Gateway.Failover.MaxProviderSwitches = 10
	cfg.Gateway.Failover.MaxProviderSwitchesGemini = 3
	cfg.Gateway.OpenAI.Codex.ForceCLI = false
	cfg.Gateway.OpenAI.Codex.ImageGenerationBridgeEnabled = false
	cfg.Gateway.OpenAI.PassthroughAllowTimeoutHeaders = false
	cfg.Gateway.OpenAI.Live.MaxSessionDurationSeconds = 3600
	// OpenAI Responses WebSocket defaults.
	cfg.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds = DefaultOpenAIWSClientFirstMessageTimeoutSeconds
	cfg.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds = 300
	cfg.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey = 64
	cfg.Gateway.OpenAI.ResponsesWS.IngressPreviousResponseRecoveryEnabled = true
	cfg.Gateway.OpenAI.ResponsesWS.ClientReadLimitBytes = 64 * 1024 * 1024
	cfg.Gateway.OpenAI.ResponsesWS.HTTPBridgeThresholdBytes = 15 * 1024 * 1024
	cfg.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = 128
	cfg.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = 4
	cfg.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = 12
	cfg.Gateway.OpenAI.ResponsesWS.DynamicMaxConnsByProviderConcurrencyEnabled = true
	cfg.Gateway.OpenAI.ResponsesWS.OAuthMaxConnsFactor = 1.0
	cfg.Gateway.OpenAI.ResponsesWS.APIKeyMaxConnsFactor = 1.0
	cfg.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds = 10
	cfg.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds = 900
	cfg.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds = 120
	cfg.Gateway.OpenAI.ResponsesWS.PoolTargetUtilization = 0.7
	cfg.Gateway.OpenAI.ResponsesWS.QueueLimitPerConn = 64
	cfg.Gateway.OpenAI.ResponsesWS.PrewarmCooldownMS = 300
	cfg.Scheduling.Advanced.LBTopK = 7
	cfg.Scheduling.Advanced.EWMAErrorRateAlpha = 0.2
	cfg.Scheduling.Advanced.EWMATTFTAlpha = 0.2
	cfg.Scheduling.SessionAffinity.TTLSeconds = 3600
	cfg.Scheduling.SessionAffinity.ReadLegacyHash = true
	cfg.Scheduling.SessionAffinity.DualWriteLegacyHash = true
	cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds = 3600
	cfg.Scheduling.Advanced.ScoreWeights.Priority = 1.0
	cfg.Scheduling.Advanced.ScoreWeights.Load = 1.0
	cfg.Scheduling.Advanced.ScoreWeights.Queue = 0.7
	cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0.8
	cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0.5
	cfg.Scheduling.Advanced.ScoreWeights.Reset = 0.0
	cfg.Scheduling.Advanced.ScoreWeights.QuotaHeadroom = 0.0
	cfg.Scheduling.Advanced.ScoreWeights.PreviousResponse = 5.0
	cfg.Scheduling.Advanced.ScoreWeights.SessionSticky = 3.0
	// OpenAI upstream HTTP version policy.
	cfg.Gateway.OpenAI.HTTP2.Enabled = true
	cfg.Gateway.OpenAI.HTTP2.AllowProxyFallbackToHTTP1 = true
	cfg.Gateway.OpenAI.HTTP2.FallbackErrorThreshold = 2
	cfg.Gateway.OpenAI.HTTP2.FallbackWindowSeconds = 60
	cfg.Gateway.OpenAI.HTTP2.FallbackTTLSeconds = 600
	cfg.Gateway.OpenAI.ProxyStreamCircuit.FailureThreshold = 2
	cfg.Gateway.OpenAI.ProxyStreamCircuit.WindowSeconds = 60
	cfg.Gateway.OpenAI.ProxyStreamCircuit.TTLSeconds = 600
	// The Grok quota gate applies to scheduling; admin QueryQuota bypasses it.
	// Only providers marked free are gated, so unknown and paid tiers are unaffected.
	cfg.Scheduling.GrokFreeQuota.Enabled = true
	cfg.Provider.Grok.PasswordAuthEnabled = false
	// Default nominal allowance: 500,000 tokens in a rolling 24-hour window.
	cfg.Scheduling.GrokFreeQuota.TokenLimit = 500_000
	cfg.Scheduling.GrokFreeQuota.ThresholdPercent = 95
	cfg.Scheduling.GrokFreeQuota.WindowHours = 24
	cfg.Scheduling.GrokFreeQuota.StatsCacheSeconds = 60
	// Supplier monitoring is disabled by default; manual admin queries remain available.
	cfg.Provider.UsageMonitor.Enabled = false
	cfg.Provider.UsageMonitor.PauseBelowBalance = 0.5
	cfg.Provider.UsageMonitor.IntervalMinutes = 10
	cfg.Provider.UsageMonitor.Concurrency = 4
	cfg.Provider.UsageMonitor.ProbeTimeoutSeconds = 20
	cfg.Provider.UsageMonitor.RoundTimeoutSeconds = 300
	cfg.Gateway.Images.Concurrency.Enabled = false
	cfg.Gateway.Images.Concurrency.MaxConcurrentRequests = 0
	cfg.Gateway.Images.Concurrency.OverflowMode = ImageConcurrencyOverflowModeReject
	cfg.Gateway.Images.Concurrency.WaitTimeoutSeconds = 30
	cfg.Gateway.Images.Concurrency.MaxWaitingRequests = 100
	cfg.Gateway.MaxRequestBodyBytes = 256 * 1024 * 1024
	cfg.Gateway.TextMaxRequestBodyBytes = 32 * 1024 * 1024
	cfg.Gateway.UpstreamResponseMaxBytes = DefaultUpstreamResponseReadMaxBytes
	cfg.Gateway.ModelsListResponseMaxBytes = DefaultModelsListReadMaxBytes
	cfg.Egress.ProxyProbe.ResponseMaxBytes = 1024 * 1024
	cfg.Gateway.Gemini.DebugResponseHeaders = false
	cfg.Gateway.Transport.ConnectionPoolIsolation = ConnectionPoolIsolationProviderProxy
	// Upstream HTTP connection-pool defaults.
	cfg.Gateway.Transport.MaxIdleConns = 2560         // Total idle connection limit.
	cfg.Gateway.Transport.MaxIdleConnsPerHost = 120   // Idle connection limit per host.
	cfg.Gateway.Transport.MaxConnsPerHost = 1024      // Active and idle connection limit per host.
	cfg.Gateway.Transport.IdleConnTimeoutSeconds = 90 // Idle timeout, in seconds.
	cfg.Gateway.Transport.ClientCache.MaxClients = 5000
	cfg.Gateway.Transport.ClientCache.IdleTTLSeconds = 900
	cfg.Scheduling.ConcurrencySlotTTLMinutes = 30 // Slot lifetime accommodates long requests.
	cfg.Gateway.Streaming.DataIdleTimeoutSeconds = 180
	cfg.Gateway.Streaming.KeepaliveIntervalSeconds = 10
	cfg.Gateway.Images.StreamDataIdleTimeoutSeconds = 900
	cfg.Gateway.Images.StreamKeepaliveIntervalSeconds = 10
	cfg.Gateway.Images.NonstreamKeepaliveIntervalSeconds = 0
	cfg.Gateway.MaxSSELineBytes = 500 * 1024 * 1024
	cfg.Scheduling.StickyProviderWait.MaxWaiting = 3
	cfg.Scheduling.StickyProviderWait.TimeoutSeconds = 120
	cfg.Scheduling.AlternateProviderWait.TimeoutSeconds = 30
	cfg.Scheduling.AlternateProviderWait.MaxWaiting = 100
	cfg.Scheduling.AlternateSelectionMode = "last_used"
	cfg.Scheduling.PreferSoonestReset = false
	cfg.Scheduling.LoadBatch.Enabled = true
	cfg.Scheduling.LoadBatch.CacheTTLMS = 200
	cfg.Scheduling.Snapshot.MGetChunkSize = 128
	cfg.Scheduling.Snapshot.WriteChunkSize = 256
	cfg.Scheduling.SlotCleanupIntervalSeconds = 30
	cfg.Scheduling.Snapshot.DbFallbackEnabled = true
	cfg.Scheduling.Snapshot.DbFallbackTimeoutSeconds = 0
	cfg.Scheduling.Snapshot.DbFallbackMaxQPS = 0
	cfg.Scheduling.Snapshot.OutboxPollIntervalSeconds = 1
	cfg.Scheduling.Snapshot.OutboxLagWarnSeconds = 5
	cfg.Scheduling.Snapshot.OutboxLagRebuildSeconds = 10
	cfg.Scheduling.Snapshot.OutboxLagRebuildFailures = 3
	cfg.Scheduling.Snapshot.OutboxBacklogRebuildRows = 10000
	cfg.Scheduling.Snapshot.FullRebuildIntervalSeconds = 300
	cfg.Usage.RecordQueue.WorkerCount = 128
	cfg.Usage.RecordQueue.QueueSize = 16384
	cfg.Usage.RecordQueue.TaskTimeoutSeconds = 5
	// The default sync policy runs overflow tasks inline after the response is written.
	// sample and drop can discard billing tasks, creating charge and usage_logs
	// reconciliation gaps (issue #3656).
	// Use those policies only when the deployment accepts that loss.
	cfg.Usage.RecordQueue.OverflowPolicy = UsageRecordOverflowPolicySync
	cfg.Usage.RecordQueue.OverflowSamplePercent = 10
	cfg.Usage.RecordQueue.AutoScaleEnabled = true
	cfg.Usage.RecordQueue.AutoScaleMinWorkers = 128
	cfg.Usage.RecordQueue.AutoScaleMaxWorkers = 512
	cfg.Usage.RecordQueue.AutoScaleUpQueuePercent = 70
	cfg.Usage.RecordQueue.AutoScaleDownQueuePercent = 15
	cfg.Usage.RecordQueue.AutoScaleUpStep = 32
	cfg.Usage.RecordQueue.AutoScaleDownStep = 16
	cfg.Usage.RecordQueue.AutoScaleCheckIntervalSeconds = 3
	cfg.Usage.RecordQueue.AutoScaleCooldownSeconds = 10
	cfg.Billing.GroupRateCacheTTLSeconds = 30
	// User-message queue defaults.
	cfg.Gateway.Anthropic.UserMessageQueue.LockTTLMs = 120000
	cfg.Gateway.Anthropic.UserMessageQueue.WaitTimeoutMs = 30000
	cfg.Gateway.Anthropic.UserMessageQueue.MinDelayMs = 200
	cfg.Gateway.Anthropic.UserMessageQueue.MaxDelayMs = 2000
	cfg.Gateway.Anthropic.UserMessageQueue.CleanupIntervalSeconds = 60

	// Enable TLS fingerprint support by default.
	cfg.Gateway.Streaming.WaitPingIntervalSeconds = 10

	// TokenRefresh
	cfg.Provider.TokenRefresh.Enabled = true
	cfg.Provider.TokenRefresh.CheckIntervalMinutes = 5       // Check every five minutes.
	cfg.Provider.TokenRefresh.RefreshBeforeExpiryHours = 0.5 // Half-hour proactive refresh window.
	cfg.Provider.TokenRefresh.MaxAttempts = 3                // Three total exchange attempts, including the first.
	cfg.Provider.TokenRefresh.RetryBackoffSeconds = 2        // Two-second base backoff.
	cfg.Provider.TokenRefresh.CandidatePageSize = 200
	cfg.Provider.TokenRefresh.PlatformConcurrency = 4
	cfg.Provider.TokenRefresh.PlatformQPS = 2
	cfg.Provider.TokenRefresh.PlatformFailureThreshold = 3
	cfg.Provider.TokenRefresh.AttemptTimeoutSeconds = 15
	cfg.Provider.TokenRefresh.CycleTimeoutSeconds = 240

	// Gemini OAuth credential and scope inputs remain empty by default.
	// Application construction passes them to the OAuth consumer, which selects
	// its effective client configuration.
	cfg.Provider.Gemini.OAuth.ClientID = ""
	cfg.Provider.Gemini.OAuth.ClientSecret = ""
	cfg.Provider.Gemini.OAuth.Scopes = ""
	cfg.Provider.Gemini.Quota.Policy = ""

	cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile = ""
	cfg.Scheduling.DefaultSessionIdleTimeoutMinutes = 0
	cfg.Gateway.Anthropic.UserMessageQueue.DefaultMode = ""
	cfg.OutboundProxy.URL = ""

	// Register sticky-escape defaults as decode keys so supplied zero error rate
	// stays meaningful and zero TTFT reaches validation. YAML and usable
	// file values can replace these defaults.
	cfg.Scheduling.Advanced.StickyEscapeEnabled = true
	cfg.Scheduling.Advanced.StickyEscapeErrorRate = 0.5
	cfg.Scheduling.Advanced.StickyEscapeTTFTMs = 15000

	// Register OAuth client secrets and login defaults for sparse version 2 files.
	// Explicit references resolve before this instance decodes the selected document.
	cfg.Auth.OAuth.GitHub = EmailOAuthProviderConfig{}
	cfg.Auth.OAuth.Google = EmailOAuthProviderConfig{}

	cfg.Auth.OAuth.DingTalk.ClientID = ""
	cfg.Auth.OAuth.DingTalk.ClientSecret = ""
	cfg.Auth.OAuth.DingTalk.InternalCorpID = ""
	cfg.Auth.OAuth.DingTalk.RedirectURL = ""
	cfg.Auth.OAuth.DingTalk.BypassRegistration = false
	cfg.Auth.OAuth.DingTalk.SyncDisplayName = false
	cfg.Auth.OAuth.DingTalk.SyncDisplayNameAttrKey = ""
	cfg.Auth.OAuth.DingTalk.SyncDisplayNameAttrName = ""
	cfg.Auth.OAuth.DingTalk.SyncDept = false
	cfg.Auth.OAuth.DingTalk.SyncDeptAttrKey = ""
	cfg.Auth.OAuth.DingTalk.SyncDeptAttrName = ""
	cfg.Auth.OAuth.DingTalk.SyncCorpEmail = false
	cfg.Auth.OAuth.DingTalk.SyncCorpEmailAttrKey = ""
	cfg.Auth.OAuth.DingTalk.SyncCorpEmailAttrName = ""
	return cfg
}
