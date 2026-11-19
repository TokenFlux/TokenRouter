package config

import "github.com/spf13/viper"

// defaults.go registers version 2 startup defaults on isolated Viper instances
// before YAML unmarshaling. Keeping defaults in one registration pass fills
// omitted keys without activating implicit environment bindings or altering
// explicit file-presence tracking for client-IP and OIDC flags.

// setDefaults registers current process defaults on the caller's Viper instance.
// Call before decoding the selected YAML document. Defaults fill omitted values
// without enabling implicit environment lookup or marking file-presence flags.
func setDefaults(v *viper.Viper) {
	v.SetDefault("config_version", CurrentConfigVersion)
	v.SetDefault("auth.jwt.access_token_ttl_minutes", 1440)
	v.SetDefault("gateway.openai.proxy_stream_circuit.enabled", true)
	v.SetDefault("provider.antigravity.fallback_cooldown_seconds", 60)
	v.SetDefault("provider.antigravity.forward_endpoint", "auto")
	v.SetDefault("ops.websocket.trust_proxy", true)
	v.SetDefault("ops.websocket.trusted_proxies", []string{"127.0.0.0/8", "::1/128"})
	v.SetDefault("ops.websocket.origin_policy", "permissive")
	v.SetDefault("ops.websocket.max_conns", 100)
	v.SetDefault("ops.websocket.max_conns_per_ip", 20)

	setPluginDefaults(v)
	// Server
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.mode", "release")
	v.SetDefault("server.enable_server_timing", false)
	v.SetDefault("site.frontend_url", "")
	v.SetDefault("server.read_header_timeout_seconds", 10) // 10-second header read timeout.
	v.SetDefault("server.max_header_bytes", 64*1024)
	v.SetDefault("server.idle_timeout_seconds", 120) // 120-second idle timeout.
	v.SetDefault("server.max_request_body_bytes", int64(256*1024*1024))
	// H2C defaults.
	v.SetDefault("server.h2c.enabled", false)
	v.SetDefault("server.h2c.max_concurrent_streams", uint32(50))            // 50 concurrent streams.
	v.SetDefault("server.h2c.idle_timeout_seconds", 75)                      // 75 seconds.
	v.SetDefault("server.h2c.max_read_frame_bytes", 1<<20)                   // 1 MiB.
	v.SetDefault("server.h2c.max_upload_buffer_per_connection_bytes", 2<<20) // 2 MiB
	v.SetDefault("server.h2c.max_upload_buffer_per_stream_bytes", 512<<10)   // 512 KiB
	// The collector starts from the UI; these values supply its listen settings.
	v.SetDefault("egress.tls_fingerprint_collector.host", "0.0.0.0")
	v.SetDefault("egress.tls_fingerprint_collector.port", 8443)
	v.SetDefault("egress.tls_fingerprint_collector.public_base_url", "")
	v.SetDefault("egress.tls_fingerprint_collector.cert_file", "")
	v.SetDefault("egress.tls_fingerprint_collector.key_file", "")
	v.SetDefault("egress.tls_fingerprint_collector.session_ttl_seconds", 1800)
	v.SetDefault("egress.tls_fingerprint_collector.max_records_per_session", 20)

	// Log
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
	v.SetDefault("log.service_name", "tokenrouter")
	v.SetDefault("environment", "production")
	v.SetDefault("log.caller", true)
	v.SetDefault("log.stacktrace_level", "error")
	v.SetDefault("log.output.to_stdout", true)
	v.SetDefault("log.output.to_file", true)
	v.SetDefault("log.output.file_path", "")
	v.SetDefault("log.rotation.max_size_mb", 100)
	v.SetDefault("log.rotation.max_backups", 10)
	v.SetDefault("log.rotation.max_age_days", 7)
	v.SetDefault("log.rotation.compress", true)
	v.SetDefault("log.rotation.local_time", true)
	v.SetDefault("log.sampling.enabled", false)
	v.SetDefault("log.sampling.initial", 100)
	v.SetDefault("log.sampling.thereafter", 100)

	// CORS
	v.SetDefault("security.cors.allowed_origins", []string{})
	v.SetDefault("security.cors.allow_credentials", true)

	// Passkeys are disabled by default; enabling requires a relying-party domain
	// and trusted browser origins.
	v.SetDefault("auth.webauthn.enabled", false)
	v.SetDefault("auth.webauthn.rp_display_name", "TokenRouter")
	v.SetDefault("auth.webauthn.rp_id", "")
	v.SetDefault("auth.webauthn.rp_origins", []string{})

	// Security
	v.SetDefault("security.url_allowlist.enabled", false)
	v.SetDefault("security.url_allowlist.upstream_hosts", []string{
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
	})
	v.SetDefault("security.url_allowlist.model_catalog_hosts", []string{
		"models.dev",
		"raw.githubusercontent.com",
	})
	v.SetDefault("security.url_allowlist.crs_sync_hosts", []string{})
	v.SetDefault("security.url_allowlist.allow_private_hosts", true)
	v.SetDefault("security.url_allowlist.allow_insecure_http", true)
	v.SetDefault("gateway.upstream_response_headers.enabled", true)
	v.SetDefault("gateway.upstream_response_headers.additional_allowed", []string{})
	v.SetDefault("gateway.upstream_response_headers.force_remove", []string{})
	v.SetDefault("security.csp.enabled", true)
	v.SetDefault("security.csp.policy", DefaultCSPPolicy)
	v.SetDefault("server.client_ip.trust_forwarded_headers", true)

	// Security - disable direct fallback on proxy error
	v.SetDefault("outbound_proxy.allow_direct_on_error", false)

	// Billing
	v.SetDefault("billing.balance_check_circuit_breaker.enabled", true)
	v.SetDefault("billing.balance_check_circuit_breaker.failure_threshold", 5)
	v.SetDefault("billing.balance_check_circuit_breaker.reset_timeout_seconds", 30)
	v.SetDefault("billing.balance_check_circuit_breaker.half_open_requests", 3)
	v.SetDefault("billing.minimum_balance_reserve", 0.000001)

	// Turnstile
	v.SetDefault("auth.require_captcha", false)

	// LinuxDo Connect OAuth login.
	v.SetDefault("auth.oauth.linuxdo.enabled", false)
	v.SetDefault("auth.oauth.linuxdo.client_id", "")
	v.SetDefault("auth.oauth.linuxdo.client_secret", "")
	v.SetDefault("auth.oauth.linuxdo.authorize_url", "https://connect.linux.do/oauth2/authorize")
	v.SetDefault("auth.oauth.linuxdo.token_url", "https://connect.linux.do/oauth2/token")
	v.SetDefault("auth.oauth.linuxdo.userinfo_url", "https://connect.linux.do/api/user")
	v.SetDefault("auth.oauth.linuxdo.scopes", "user")
	v.SetDefault("auth.oauth.linuxdo.redirect_url", "")
	v.SetDefault("auth.oauth.linuxdo.frontend_redirect_url", "/auth/linuxdo/callback")
	v.SetDefault("auth.oauth.linuxdo.token_auth_method", "client_secret_post")
	v.SetDefault("auth.oauth.linuxdo.use_pkce", false)
	v.SetDefault("auth.oauth.linuxdo.userinfo_email_path", "")
	v.SetDefault("auth.oauth.linuxdo.userinfo_id_path", "")
	v.SetDefault("auth.oauth.linuxdo.userinfo_username_path", "")

	// WeChat Connect OAuth login.
	v.SetDefault("auth.oauth.wechat.enabled", false)
	v.SetDefault("auth.oauth.wechat.open_app_id", "")
	v.SetDefault("auth.oauth.wechat.open_app_secret", "")
	v.SetDefault("auth.oauth.wechat.mp_app_id", "")
	v.SetDefault("auth.oauth.wechat.mp_app_secret", "")
	v.SetDefault("auth.oauth.wechat.mobile_app_id", "")
	v.SetDefault("auth.oauth.wechat.mobile_app_secret", "")
	v.SetDefault("auth.oauth.wechat.open_enabled", false)
	v.SetDefault("auth.oauth.wechat.mp_enabled", false)
	v.SetDefault("auth.oauth.wechat.mobile_enabled", false)
	v.SetDefault("auth.oauth.wechat.mode", defaultWeChatConnectMode)
	v.SetDefault("auth.oauth.wechat.scopes", defaultWeChatConnectScopes)
	v.SetDefault("auth.oauth.wechat.redirect_url", "")
	v.SetDefault("auth.oauth.wechat.frontend_redirect_url", defaultWeChatConnectFrontendRedirect)

	// Generic OIDC login.
	v.SetDefault("auth.oauth.oidc.enabled", false)
	v.SetDefault("auth.oauth.oidc.provider_name", "OIDC")
	v.SetDefault("auth.oauth.oidc.client_id", "")
	v.SetDefault("auth.oauth.oidc.client_secret", "")
	v.SetDefault("auth.oauth.oidc.issuer_url", "")
	v.SetDefault("auth.oauth.oidc.discovery_url", "")
	v.SetDefault("auth.oauth.oidc.authorize_url", "")
	v.SetDefault("auth.oauth.oidc.token_url", "")
	v.SetDefault("auth.oauth.oidc.userinfo_url", "")
	v.SetDefault("auth.oauth.oidc.jwks_url", "")
	v.SetDefault("auth.oauth.oidc.scopes", "openid email profile")
	v.SetDefault("auth.oauth.oidc.redirect_url", "")
	v.SetDefault("auth.oauth.oidc.frontend_redirect_url", "/auth/oidc/callback")
	v.SetDefault("auth.oauth.oidc.token_auth_method", "client_secret_post")
	v.SetDefault("auth.oauth.oidc.use_pkce", true)
	v.SetDefault("auth.oauth.oidc.validate_id_token", true)
	v.SetDefault("auth.oauth.oidc.allowed_signing_algs", "RS256,ES256,PS256")
	v.SetDefault("auth.oauth.oidc.clock_skew_seconds", 120)
	v.SetDefault("auth.oauth.oidc.require_email_verified", false)
	v.SetDefault("auth.oauth.oidc.userinfo_email_path", "")
	v.SetDefault("auth.oauth.oidc.userinfo_id_path", "")
	v.SetDefault("auth.oauth.oidc.userinfo_username_path", "")

	// DingTalk Connect OAuth login.
	v.SetDefault("auth.oauth.dingtalk.enabled", false)
	v.SetDefault("auth.oauth.dingtalk.authorize_url", "https://login.dingtalk.com/oauth2/auth")
	v.SetDefault("auth.oauth.dingtalk.token_url", "https://api.dingtalk.com/v1.0/oauth2/userAccessToken")
	v.SetDefault("auth.oauth.dingtalk.userinfo_url", "https://api.dingtalk.com/v1.0/contact/users/me")
	v.SetDefault("auth.oauth.dingtalk.scopes", "openid")
	v.SetDefault("auth.oauth.dingtalk.frontend_redirect_url", "/auth/dingtalk/callback")
	v.SetDefault("auth.oauth.dingtalk.dingtalk_app_kind", "internal_app")
	v.SetDefault("auth.oauth.dingtalk.app_type", "public")
	v.SetDefault("auth.oauth.dingtalk.corp_restriction_policy", "none")
	v.SetDefault("auth.oauth.dingtalk.require_email", true)

	// Database
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "postgres")
	v.SetDefault("database.password", "postgres")
	v.SetDefault("database.dbname", "tokenrouter")
	v.SetDefault("database.sslmode", "prefer")
	v.SetDefault("database.max_open_conns", 256)
	v.SetDefault("database.max_idle_conns", 128)
	v.SetDefault("database.conn_max_lifetime_minutes", 30)
	v.SetDefault("database.conn_max_idle_time_minutes", 5)

	// Redis
	v.SetDefault("redis.host", "localhost")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.username", "")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.dial_timeout_seconds", 5)
	v.SetDefault("redis.read_timeout_seconds", 3)
	v.SetDefault("redis.write_timeout_seconds", 3)
	v.SetDefault("redis.pool_size", 1024)
	v.SetDefault("redis.min_idle_conns", 128)
	v.SetDefault("redis.enable_tls", false)

	// Batch Image queue
	v.SetDefault("batch_image.max_items_per_job", 200)
	v.SetDefault("batch_image.max_output_images_per_job", 200)
	v.SetDefault("batch_image.max_output_images_per_item", 4)
	v.SetDefault("batch_image.max_prompt_bytes_per_item", 8000)
	v.SetDefault("batch_image.max_reference_images_per_job", 1000)
	v.SetDefault("batch_image.max_reference_inline_bytes_per_job", 134217728)
	v.SetDefault("batch_image.default_response_mime_type", "image/png")
	v.SetDefault("batch_image.default_image_size", "1K")
	v.SetDefault("batch_image.max_download_items_zip", 200)
	v.SetDefault("batch_image.max_download_bytes_per_request", 536870912)
	v.SetDefault("batch_image.max_download_duration_seconds", 600)
	v.SetDefault("batch_image.max_download_concurrency_per_user", 1)
	v.SetDefault("batch_image.input_retention_after_terminal_hours", 24)
	v.SetDefault("batch_image.output_retention_after_terminal_hours", 72)
	v.SetDefault("batch_image.cleanup_interval_minutes", 30)
	v.SetDefault("batch_image.cleanup_batch_size", 100)
	v.SetDefault("batch_image.queue.ready_key", "batch_image:queue:ready")
	v.SetDefault("batch_image.queue.delayed_key", "batch_image:queue:delayed")
	v.SetDefault("batch_image.queue.active_key", "batch_image:queue:active")
	v.SetDefault("batch_image.queue.inflight_key_prefix", "batch_image:queue:inflight:")
	v.SetDefault("batch_image.queue.lock_key_prefix", "batch_image:queue:lock:")
	v.SetDefault("batch_image.queue.inflight_ttl_seconds", 604800)
	v.SetDefault("batch_image.queue.job_lock_ttl_seconds", 300)
	v.SetDefault("batch_image.queue.default_requeue_delay_seconds", 30)
	v.SetDefault("batch_image.queue.error_retry_delay_seconds", 60)
	v.SetDefault("batch_image.queue.lock_conflict_delay_seconds", 5)
	v.SetDefault("batch_image.queue.stale_active_after_seconds", 600)
	v.SetDefault("batch_image.queue.delayed_mover_interval_seconds", 5)
	v.SetDefault("batch_image.queue.recovery_interval_seconds", 300)
	v.SetDefault("batch_image.queue.delayed_move_limit", 100)
	v.SetDefault("batch_image.queue.recover_limit", 100)
	v.SetDefault("batch_image.vertex.project_id", "")
	v.SetDefault("batch_image.vertex.location", "global")
	v.SetDefault("batch_image.vertex.managed_gcs_bucket", "")
	v.SetDefault("batch_image.vertex.managed_gcs_prefix", "batch-image/{env}/{batch_id}")
	v.SetDefault("batch_image.vertex.batch_prediction_base_url", "")
	v.SetDefault("batch_image.vertex.gcs_base_url", "")

	// Creative Studio.
	v.SetDefault("creative.enabled", true)
	v.SetDefault("creative.queue_worker_enabled", true)
	v.SetDefault("creative.transient_ttl_seconds", 1800)
	v.SetDefault("creative.max_asset_bytes", 33554432)
	v.SetDefault("creative.max_total_input_bytes", 67108864)
	v.SetDefault("creative.max_prompt_chars", 8000)
	v.SetDefault("creative.default_image_size", "1K")
	v.SetDefault("creative.queue.ready_key", "creative:queue:ready")
	v.SetDefault("creative.queue.delayed_key", "creative:queue:delayed")
	v.SetDefault("creative.queue.active_key", "creative:queue:active")
	v.SetDefault("creative.queue.inflight_key_prefix", "creative:queue:inflight:")
	v.SetDefault("creative.queue.lock_key_prefix", "creative:queue:lock:")
	v.SetDefault("creative.queue.inflight_ttl_seconds", 604800)
	v.SetDefault("creative.queue.job_lock_ttl_seconds", 300)
	v.SetDefault("creative.queue.default_requeue_delay_seconds", 30)
	v.SetDefault("creative.queue.error_retry_delay_seconds", 60)
	v.SetDefault("creative.queue.lock_conflict_delay_seconds", 5)
	v.SetDefault("creative.queue.stale_active_after_seconds", 600)
	v.SetDefault("creative.queue.delayed_mover_interval_seconds", 5)
	v.SetDefault("creative.queue.recovery_interval_seconds", 300)
	v.SetDefault("creative.queue.delayed_move_limit", 100)
	v.SetDefault("creative.queue.recover_limit", 100)
	v.SetDefault("creative.execute_timeout_seconds", 300)
	v.SetDefault("creative.max_execute_attempts", 3)

	// Ops
	v.SetDefault("ops.enabled", true)
	v.SetDefault("ops.cleanup.enabled", true)
	v.SetDefault("ops.cleanup.schedule", "0 3 * * *")
	v.SetDefault("ops.cleanup.batch_size", 1000)
	v.SetDefault("ops.cleanup.batch_pause_ms", 200)
	// Retain 30 days across Ops datasets by default.
	v.SetDefault("request_log.retention_days", 30)
	v.SetDefault("ops.cleanup.error_log_retention_days", 30)
	v.SetDefault("ops.cleanup.system_log_retention_days", 30)
	v.SetDefault("ops.cleanup.minute_metrics_retention_days", 30)
	v.SetDefault("ops.cleanup.hourly_metrics_retention_days", 30)
	v.SetDefault("ops.aggregation.enabled", true)
	// Register the accepted collector-cache lifetime copied into ops.Options. The
	// current MetricsQueries reader does not consume it; sixty-five seconds
	// exceeds the collector's default/minimum one-minute interval, while runtime
	// cadence can be longer.

	// JWT
	v.SetDefault("auth.jwt.secret", "")
	v.SetDefault("auth.jwt.refresh_token_ttl_days", 30) // 30-day refresh-token lifetime.

	// TOTP
	v.SetDefault("security.secret_encryption_key", "")

	// Default
	// Admin credentials are created via the setup flow (web wizard / CLI / AUTO_SETUP).
	// Do not ship fixed defaults here to avoid insecure "known credentials" in
	// production.
	v.SetDefault("user_defaults.concurrency", 5)
	v.SetDefault("user_defaults.balance", 0)
	v.SetDefault("api_key.prefix", "sk-")
	v.SetDefault("billing.fallback_rate_multiplier", 1.0)

	// RateLimit
	v.SetDefault("provider.cooldown.overload_minutes", 10)
	v.SetDefault("provider.cooldown.oauth_401_minutes", 10)

	// Pricing uses the models.dev catalog and local supplements for additional
	// billing dimensions.
	v.SetDefault("model_catalog.catalog_url", "https://models.dev/experimental/v2.0/api.json")
	v.SetDefault("storage.dir", "./data")
	v.SetDefault("model_catalog.supplement_file", "")
	v.SetDefault("model_catalog.check_interval_minutes", 10)

	// Default time zone for local-day statistics.
	v.SetDefault("timezone", "Asia/Shanghai")
	v.SetDefault("team.enabled", true)
	v.SetDefault("team.self_service_enabled", true)
	v.SetDefault("team.default_max_members", 10)

	// API Key auth cache
	v.SetDefault("api_key.auth_cache.l1_size", 65535)
	v.SetDefault("api_key.auth_cache.l1_ttl_seconds", 15)
	v.SetDefault("api_key.auth_cache.l2_ttl_seconds", 300)
	v.SetDefault("api_key.auth_cache.negative_ttl_seconds", 30)
	v.SetDefault("api_key.auth_cache.jitter_percent", 10)
	v.SetDefault("api_key.auth_cache.singleflight", true)
	v.SetDefault("api_key.lookup_concurrency", 64)
	v.SetDefault("api_key.invalid_key_abuse.enabled", true)
	v.SetDefault("api_key.invalid_key_abuse.threshold", 120)
	v.SetDefault("api_key.invalid_key_abuse.window_seconds", 60)
	v.SetDefault("api_key.invalid_key_abuse.block_seconds", 60)
	v.SetDefault("api_key.invalid_key_abuse.capacity", 16384)

	// Subscription auth L1 cache

	// Dashboard cache
	v.SetDefault("usage.dashboard_cache.enabled", true)
	v.SetDefault("usage.dashboard_cache.key_prefix", "tokenrouter:")
	v.SetDefault("usage.dashboard_cache.stats_fresh_ttl_seconds", 15)
	v.SetDefault("usage.dashboard_cache.stats_ttl_seconds", 30)
	v.SetDefault("usage.dashboard_cache.stats_refresh_timeout_seconds", 30)

	// Dashboard aggregation
	v.SetDefault("usage.aggregation.enabled", true)
	v.SetDefault("usage.aggregation.interval_seconds", 60)
	v.SetDefault("usage.aggregation.lookback_seconds", 120)
	v.SetDefault("usage.aggregation.backfill_enabled", false)
	v.SetDefault("usage.aggregation.backfill_max_days", 31)
	v.SetDefault("usage.retention.logs_days", 90)
	v.SetDefault("usage.retention.billing_dedup_days", 365)
	v.SetDefault("usage.aggregation.hourly_retention_days", 180)
	v.SetDefault("usage.aggregation.daily_retention_days", 730)
	v.SetDefault("usage.aggregation.recompute_days", 2)

	// Usage cleanup task
	v.SetDefault("usage.cleanup_tasks.enabled", true)
	v.SetDefault("usage.cleanup_tasks.max_range_days", 31)
	v.SetDefault("usage.cleanup_tasks.batch_size", 5000)
	v.SetDefault("usage.cleanup_tasks.worker_interval_seconds", 10)
	v.SetDefault("usage.cleanup_tasks.task_timeout_seconds", 1800)

	// Usage export task
	v.SetDefault("usage.export_tasks.enabled", true)
	v.SetDefault("usage.export_tasks.dir", "")
	v.SetDefault("usage.export_tasks.retention_hours", 24)
	v.SetDefault("usage.export_tasks.max_active_per_user", 2)
	v.SetDefault("usage.export_tasks.max_active_per_admin", 5)
	v.SetDefault("usage.export_tasks.batch_size", 2000)
	v.SetDefault("usage.export_tasks.worker_concurrency", 2)
	v.SetDefault("usage.export_tasks.worker_interval_seconds", 10)
	v.SetDefault("usage.export_tasks.task_timeout_seconds", 3600)

	// Idempotency
	v.SetDefault("idempotency.observe_only", true)
	v.SetDefault("idempotency.default_ttl_seconds", 86400)
	v.SetDefault("idempotency.system_operation_ttl_seconds", 3600)
	v.SetDefault("idempotency.processing_timeout_seconds", 30)
	v.SetDefault("idempotency.failed_retry_backoff_seconds", 5)
	v.SetDefault("idempotency.max_stored_response_bytes", 64*1024)
	v.SetDefault("idempotency.cleanup_interval_seconds", 60)
	v.SetDefault("idempotency.cleanup_batch_size", 500)

	// Gateway
	v.SetDefault("gateway.transport.response_header_timeout_seconds", 600) // Allow ten minutes for upstream LLM queueing before response headers.
	v.SetDefault("gateway.openai.response_header_timeout_seconds", 0)
	v.SetDefault("gateway.openai.compact_model", "")
	v.SetDefault("gateway.grok.response_header_timeout_seconds", 120)
	v.SetDefault("gateway.openai.first_output_timeout_seconds", 0)
	v.SetDefault("gateway.openai.high_effort_first_output_timeout_seconds", 0)
	v.SetDefault("gateway.upstream_error_body_log.enabled", true)
	v.SetDefault("gateway.upstream_error_body_log.max_bytes", 2048)
	v.SetDefault("gateway.anthropic.inject_beta_for_api_key", false)
	v.SetDefault("gateway.failover.messages_on_400", false)
	v.SetDefault("gateway.failover.max_provider_switches", 10)
	v.SetDefault("gateway.failover.max_provider_switches_gemini", 3)
	v.SetDefault("gateway.openai.codex.force_cli", false)
	v.SetDefault("gateway.openai.codex.image_generation_bridge_enabled", false)
	v.SetDefault("gateway.openai.passthrough_allow_timeout_headers", false)
	v.SetDefault("gateway.openai.live.max_session_duration_seconds", 3600)
	// OpenAI Responses WebSocket defaults.
	v.SetDefault("gateway.openai.responses_ws.client_first_message_timeout_seconds", DefaultOpenAIWSClientFirstMessageTimeoutSeconds)
	v.SetDefault("gateway.openai.responses_ws.ingress_inter_turn_idle_timeout_seconds", 300)
	v.SetDefault("gateway.openai.responses_ws.max_ingress_connections_per_api_key", 64)
	v.SetDefault("gateway.openai.responses_ws.ingress_previous_response_recovery_enabled", true)
	v.SetDefault("gateway.openai.responses_ws.client_read_limit_bytes", 64*1024*1024)
	v.SetDefault("gateway.openai.responses_ws.http_bridge_threshold_bytes", 15*1024*1024)
	v.SetDefault("gateway.openai.responses_ws.max_conns_per_provider", 128)
	v.SetDefault("gateway.openai.responses_ws.min_idle_per_provider", 4)
	v.SetDefault("gateway.openai.responses_ws.max_idle_per_provider", 12)
	v.SetDefault("gateway.openai.responses_ws.dynamic_max_conns_by_provider_concurrency_enabled", true)
	v.SetDefault("gateway.openai.responses_ws.oauth_max_conns_factor", 1.0)
	v.SetDefault("gateway.openai.responses_ws.api_key_max_conns_factor", 1.0)
	v.SetDefault("gateway.openai.responses_ws.dial_timeout_seconds", 10)
	v.SetDefault("gateway.openai.responses_ws.read_timeout_seconds", 900)
	v.SetDefault("gateway.openai.responses_ws.write_timeout_seconds", 120)
	v.SetDefault("gateway.openai.responses_ws.pool_target_utilization", 0.7)
	v.SetDefault("gateway.openai.responses_ws.queue_limit_per_conn", 64)
	v.SetDefault("gateway.openai.responses_ws.prewarm_cooldown_ms", 300)
	v.SetDefault("scheduling.advanced.lb_top_k", 7)
	v.SetDefault("scheduling.advanced.ewma_error_rate_alpha", 0.2)
	v.SetDefault("scheduling.advanced.ewma_ttft_alpha", 0.2)
	v.SetDefault("scheduling.session_affinity.ttl_seconds", 3600)
	v.SetDefault("scheduling.session_affinity.read_legacy_hash", true)
	v.SetDefault("scheduling.session_affinity.dual_write_legacy_hash", true)
	v.SetDefault("scheduling.session_affinity.response_id_ttl_seconds", 3600)
	v.SetDefault("scheduling.advanced.score_weights.priority", 1.0)
	v.SetDefault("scheduling.advanced.score_weights.load", 1.0)
	v.SetDefault("scheduling.advanced.score_weights.queue", 0.7)
	v.SetDefault("scheduling.advanced.score_weights.error_rate", 0.8)
	v.SetDefault("scheduling.advanced.score_weights.ttft", 0.5)
	v.SetDefault("scheduling.advanced.score_weights.reset", 0.0)
	v.SetDefault("scheduling.advanced.score_weights.quota_headroom", 0.0)
	v.SetDefault("scheduling.advanced.score_weights.previous_response", 5.0)
	v.SetDefault("scheduling.advanced.score_weights.session_sticky", 3.0)
	// OpenAI upstream HTTP version policy.
	v.SetDefault("gateway.openai.http2.enabled", true)
	v.SetDefault("gateway.openai.http2.allow_proxy_fallback_to_http1", true)
	v.SetDefault("gateway.openai.http2.fallback_error_threshold", 2)
	v.SetDefault("gateway.openai.http2.fallback_window_seconds", 60)
	v.SetDefault("gateway.openai.http2.fallback_ttl_seconds", 600)
	v.SetDefault("gateway.openai.proxy_stream_circuit.failure_threshold", 2)
	v.SetDefault("gateway.openai.proxy_stream_circuit.window_seconds", 60)
	v.SetDefault("gateway.openai.proxy_stream_circuit.ttl_seconds", 600)
	// The Grok quota gate applies to scheduling; admin QueryQuota bypasses it.
	// Only providers marked free are gated, so unknown and paid tiers are unaffected.
	v.SetDefault("scheduling.grok_free_quota.enabled", true)
	v.SetDefault("provider.grok.password_auth_enabled", false)
	// Default nominal allowance: 500,000 tokens in a rolling 24-hour window.
	v.SetDefault("scheduling.grok_free_quota.token_limit", int64(500_000))
	v.SetDefault("scheduling.grok_free_quota.threshold_percent", 95)
	v.SetDefault("scheduling.grok_free_quota.window_hours", 24)
	v.SetDefault("scheduling.grok_free_quota.stats_cache_seconds", 60)
	// Supplier monitoring is disabled by default; manual admin queries remain available.
	v.SetDefault("provider.usage_monitor.enabled", false)
	v.SetDefault("provider.usage_monitor.pause_below_balance", 0.5)
	v.SetDefault("provider.usage_monitor.interval_minutes", 10)
	v.SetDefault("provider.usage_monitor.concurrency", 4)
	v.SetDefault("provider.usage_monitor.probe_timeout_seconds", 20)
	v.SetDefault("provider.usage_monitor.round_timeout_seconds", 300)
	v.SetDefault("gateway.images.concurrency.enabled", false)
	v.SetDefault("gateway.images.concurrency.max_concurrent_requests", 0)
	v.SetDefault("gateway.images.concurrency.overflow_mode", ImageConcurrencyOverflowModeReject)
	v.SetDefault("gateway.images.concurrency.wait_timeout_seconds", 30)
	v.SetDefault("gateway.images.concurrency.max_waiting_requests", 100)
	v.SetDefault("gateway.max_request_body_bytes", int64(256*1024*1024))
	v.SetDefault("gateway.text_max_request_body_bytes", int64(32*1024*1024))
	v.SetDefault("gateway.upstream_response_max_bytes", DefaultUpstreamResponseReadMaxBytes)
	v.SetDefault("gateway.models_list_response_max_bytes", DefaultModelsListReadMaxBytes)
	v.SetDefault("egress.proxy_probe.response_max_bytes", int64(1024*1024))
	v.SetDefault("gateway.gemini.debug_response_headers", false)
	v.SetDefault("gateway.transport.connection_pool_isolation", ConnectionPoolIsolationProviderProxy)
	// Upstream HTTP connection-pool defaults.
	v.SetDefault("gateway.transport.max_idle_conns", 2560)          // Total idle connection limit.
	v.SetDefault("gateway.transport.max_idle_conns_per_host", 120)  // Idle connection limit per host.
	v.SetDefault("gateway.transport.max_conns_per_host", 1024)      // Active and idle connection limit per host.
	v.SetDefault("gateway.transport.idle_conn_timeout_seconds", 90) // Idle timeout, in seconds.
	v.SetDefault("gateway.transport.client_cache.max_clients", 5000)
	v.SetDefault("gateway.transport.client_cache.idle_ttl_seconds", 900)
	v.SetDefault("scheduling.concurrency_slot_ttl_minutes", 30) // Slot lifetime accommodates long requests.
	v.SetDefault("gateway.streaming.data_idle_timeout_seconds", 180)
	v.SetDefault("gateway.streaming.keepalive_interval_seconds", 10)
	v.SetDefault("gateway.images.stream_data_idle_timeout_seconds", 900)
	v.SetDefault("gateway.images.stream_keepalive_interval_seconds", 10)
	v.SetDefault("gateway.images.nonstream_keepalive_interval_seconds", 0)
	v.SetDefault("gateway.max_sse_line_bytes", 500*1024*1024)
	v.SetDefault("scheduling.sticky_provider_wait.max_waiting", 3)
	v.SetDefault("scheduling.sticky_provider_wait.timeout_seconds", 120)
	v.SetDefault("scheduling.alternate_provider_wait.timeout_seconds", 30)
	v.SetDefault("scheduling.alternate_provider_wait.max_waiting", 100)
	v.SetDefault("scheduling.alternate_selection_mode", "last_used")
	v.SetDefault("scheduling.prefer_soonest_reset", false)
	v.SetDefault("scheduling.load_batch.enabled", true)
	v.SetDefault("scheduling.load_batch.cache_ttl_ms", 200)
	v.SetDefault("scheduling.snapshot.mget_chunk_size", 128)
	v.SetDefault("scheduling.snapshot.write_chunk_size", 256)
	v.SetDefault("scheduling.slot_cleanup_interval_seconds", 30)
	v.SetDefault("scheduling.snapshot.db_fallback_enabled", true)
	v.SetDefault("scheduling.snapshot.db_fallback_timeout_seconds", 0)
	v.SetDefault("scheduling.snapshot.db_fallback_max_qps", 0)
	v.SetDefault("scheduling.snapshot.outbox_poll_interval_seconds", 1)
	v.SetDefault("scheduling.snapshot.outbox_lag_warn_seconds", 5)
	v.SetDefault("scheduling.snapshot.outbox_lag_rebuild_seconds", 10)
	v.SetDefault("scheduling.snapshot.outbox_lag_rebuild_failures", 3)
	v.SetDefault("scheduling.snapshot.outbox_backlog_rebuild_rows", 10000)
	v.SetDefault("scheduling.snapshot.full_rebuild_interval_seconds", 300)
	v.SetDefault("usage.record_queue.worker_count", 128)
	v.SetDefault("usage.record_queue.queue_size", 16384)
	v.SetDefault("usage.record_queue.task_timeout_seconds", 5)
	// The default sync policy runs overflow tasks inline after the response is written.
	// sample and drop can discard billing tasks, creating charge and usage_logs
	// reconciliation gaps (issue #3656).
	// Use those policies only when the deployment accepts that loss.
	v.SetDefault("usage.record_queue.overflow_policy", UsageRecordOverflowPolicySync)
	v.SetDefault("usage.record_queue.overflow_sample_percent", 10)
	v.SetDefault("usage.record_queue.auto_scale_enabled", true)
	v.SetDefault("usage.record_queue.auto_scale_min_workers", 128)
	v.SetDefault("usage.record_queue.auto_scale_max_workers", 512)
	v.SetDefault("usage.record_queue.auto_scale_up_queue_percent", 70)
	v.SetDefault("usage.record_queue.auto_scale_down_queue_percent", 15)
	v.SetDefault("usage.record_queue.auto_scale_up_step", 32)
	v.SetDefault("usage.record_queue.auto_scale_down_step", 16)
	v.SetDefault("usage.record_queue.auto_scale_check_interval_seconds", 3)
	v.SetDefault("usage.record_queue.auto_scale_cooldown_seconds", 10)
	v.SetDefault("billing.group_rate_cache_ttl_seconds", 30)
	// User-message queue defaults.
	v.SetDefault("gateway.anthropic.user_message_queue.lock_ttl_ms", 120000)
	v.SetDefault("gateway.anthropic.user_message_queue.wait_timeout_ms", 30000)
	v.SetDefault("gateway.anthropic.user_message_queue.min_delay_ms", 200)
	v.SetDefault("gateway.anthropic.user_message_queue.max_delay_ms", 2000)
	v.SetDefault("gateway.anthropic.user_message_queue.cleanup_interval_seconds", 60)

	// Enable TLS fingerprint support by default.
	v.SetDefault("gateway.streaming.wait_ping_interval_seconds", 10)

	// TokenRefresh
	v.SetDefault("provider.token_refresh.enabled", true)
	v.SetDefault("provider.token_refresh.check_interval_minutes", 5)        // Check every five minutes.
	v.SetDefault("provider.token_refresh.refresh_before_expiry_hours", 0.5) // Half-hour proactive refresh window.
	v.SetDefault("provider.token_refresh.max_attempts", 3)                  // Three total exchange attempts, including the first.
	v.SetDefault("provider.token_refresh.retry_backoff_seconds", 2)         // Two-second base backoff.
	v.SetDefault("provider.token_refresh.candidate_page_size", 200)
	v.SetDefault("provider.token_refresh.platform_concurrency", 4)
	v.SetDefault("provider.token_refresh.platform_qps", 2)
	v.SetDefault("provider.token_refresh.platform_failure_threshold", 3)
	v.SetDefault("provider.token_refresh.attempt_timeout_seconds", 15)
	v.SetDefault("provider.token_refresh.cycle_timeout_seconds", 240)

	// Gemini OAuth credential and scope inputs remain empty by default.
	// Application construction passes them to the OAuth consumer, which selects
	// its effective client configuration.
	v.SetDefault("provider.gemini.oauth.client_id", "")
	v.SetDefault("provider.gemini.oauth.client_secret", "")
	v.SetDefault("provider.gemini.oauth.scopes", "")
	v.SetDefault("provider.gemini.quota.policy", "")

	v.SetDefault("gateway.openai.codex.forced_instructions_template_file", "")
	v.SetDefault("scheduling.default_session_idle_timeout_minutes", 0)
	v.SetDefault("gateway.anthropic.user_message_queue.default_mode", "")
	v.SetDefault("outbound_proxy.url", "")

	// Register sticky-escape defaults as decode keys so supplied zero error rate
	// stays meaningful and zero TTFT reaches validation. YAML and usable
	// file values can replace these defaults.
	v.SetDefault("scheduling.advanced.sticky_escape_enabled", true)
	v.SetDefault("scheduling.advanced.sticky_escape_error_rate", 0.5)
	v.SetDefault("scheduling.advanced.sticky_escape_ttft_ms", 15000)

	// Register OAuth client secrets and login defaults for sparse version 2 files.
	// Explicit references resolve before this instance decodes the selected document.
	for _, provider := range []string{"auth.oauth.github", "auth.oauth.google"} {
		v.SetDefault(provider+".enabled", false)
		v.SetDefault(provider+".client_id", "")
		v.SetDefault(provider+".client_secret", "")
		v.SetDefault(provider+".authorize_url", "")
		v.SetDefault(provider+".token_url", "")
		v.SetDefault(provider+".userinfo_url", "")
		v.SetDefault(provider+".emails_url", "")
		v.SetDefault(provider+".scopes", "")
		v.SetDefault(provider+".redirect_url", "")
		v.SetDefault(provider+".frontend_redirect_url", "")
	}

	v.SetDefault("auth.oauth.dingtalk.client_id", "")
	v.SetDefault("auth.oauth.dingtalk.client_secret", "")
	v.SetDefault("auth.oauth.dingtalk.internal_corp_id", "")
	v.SetDefault("auth.oauth.dingtalk.redirect_url", "")
	v.SetDefault("auth.oauth.dingtalk.bypass_registration", false)
	v.SetDefault("auth.oauth.dingtalk.sync_display_name", false)
	v.SetDefault("auth.oauth.dingtalk.sync_display_name_attr_key", "")
	v.SetDefault("auth.oauth.dingtalk.sync_display_name_attr_name", "")
	v.SetDefault("auth.oauth.dingtalk.sync_dept", false)
	v.SetDefault("auth.oauth.dingtalk.sync_dept_attr_key", "")
	v.SetDefault("auth.oauth.dingtalk.sync_dept_attr_name", "")
	v.SetDefault("auth.oauth.dingtalk.sync_corp_email", false)
	v.SetDefault("auth.oauth.dingtalk.sync_corp_email_attr_key", "")
	v.SetDefault("auth.oauth.dingtalk.sync_corp_email_attr_name", "")
}
