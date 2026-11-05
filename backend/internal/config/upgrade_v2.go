package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// upgrade_v2.go translates the complete version 1 configuration into version 2.
// Alias precedence is resolved before environment values are imported, then
// value transformations run against the historical paths. Removed and unknown
// inputs are reported by name. Moves operate on YAML nodes so deployment
// comments stay attached to the settings they explain.
//
// The tables are historical migration data, independent of future Config
// declarations. More specific paths precede their parent moves, allowing a
// former section to split across several new owners. Values created by a
// semantic transformation are recognized before unknown-key removal, and
// destination conflicts stop migration without changing the original file.

var (
	// v2Moves lists dotted path relocations, with specific children before parents.
	// Identity paths retain their existing spelling and do not need node movement.
	v2Moves = []configMove{
		{Old: "dashboard_aggregation.retention.usage_billing_dedup_days", New: "usage.retention.billing_dedup_days"},
		{Old: "gateway.openai_ws.session_hash_read_old_fallback", New: "scheduling.session_affinity.read_legacy_hash"},
		{Old: "gateway.openai_ws.sticky_response_id_ttl_seconds", New: "scheduling.session_affinity.response_id_ttl_seconds"},
		{Old: "dashboard_aggregation.retention.usage_logs_days", New: "usage.retention.logs_days"},
		{Old: "gateway.scheduling.sticky_session_wait_timeout", New: "scheduling.sticky_provider_wait.timeout_seconds"},
		{Old: "gateway.openai_ws.session_hash_dual_write_old", New: "scheduling.session_affinity.dual_write_legacy_hash"},
		{Old: "gateway.scheduling.sticky_session_max_waiting", New: "scheduling.sticky_provider_wait.max_waiting"},
		{Old: "security.proxy_fallback.allow_direct_on_error", New: "outbound_proxy.allow_direct_on_error"},
		{Old: "gateway.openai_ws.sticky_session_ttl_seconds", New: "scheduling.session_affinity.ttl_seconds"},
		{Old: "gateway.scheduling.snapshot_write_chunk_size", New: "scheduling.snapshot.write_chunk_size"},
		{Old: "dashboard_aggregation.retention.hourly_days", New: "usage.aggregation.hourly_retention_days"},
		{Old: "gateway.grok.free_quota_stats_cache_seconds", New: "scheduling.grok_free_quota.stats_cache_seconds"},
		{Old: "gateway.scheduling.snapshot_mget_chunk_size", New: "scheduling.snapshot.mget_chunk_size"},
		{Old: "server.h2c.max_upload_buffer_per_connection", New: "server.h2c.max_upload_buffer_per_connection_bytes"},
		{Old: "dashboard_aggregation.retention.daily_days", New: "usage.aggregation.daily_retention_days"},
		{Old: "gateway.scheduling.fallback_selection_mode", New: "scheduling.alternate_selection_mode"},
		{Old: "gateway.scheduling.load_batch_cache_ttl_ms", New: "scheduling.load_batch.cache_ttl_ms"},
		{Old: "gateway.grok.free_quota_soft_gate_enabled", New: "scheduling.grok_free_quota.enabled"},
		{Old: "gateway.grok.free_quota_soft_gate_percent", New: "scheduling.grok_free_quota.threshold_percent"},
		{Old: "gateway.openai_ws.apikey_max_conns_factor", New: "gateway.openai.responses_ws.api_key_max_conns_factor"},
		{Old: "gateway.scheduling.fallback_wait_timeout", New: "scheduling.alternate_provider_wait.timeout_seconds"},
		{Old: "gateway.scheduling.slot_cleanup_interval", New: "scheduling.slot_cleanup_interval_seconds"},
		{Old: "gateway.scheduling.fallback_max_waiting", New: "scheduling.alternate_provider_wait.max_waiting"},
		{Old: "gateway.scheduling.prefer_soonest_reset", New: "scheduling.prefer_soonest_reset"},
		{Old: "server.h2c.max_upload_buffer_per_stream", New: "server.h2c.max_upload_buffer_per_stream_bytes"},
		{Old: "gateway.cn_providers.balance_threshold", New: "provider.usage_monitor.pause_below_balance"},
		{Old: "gateway.scheduling.load_batch_enabled", New: "scheduling.load_batch.enabled"},
		{Old: "gateway.cn_providers.monitor_enabled", New: "provider.usage_monitor.enabled"},
		{Old: "gateway.grok.free_quota_window_hours", New: "scheduling.grok_free_quota.window_hours"},
		{Old: "security.url_allowlist.pricing_hosts", New: "security.url_allowlist.model_catalog_hosts"},
		{Old: "gateway.grok.free_quota_token_limit", New: "scheduling.grok_free_quota.token_limit"},
		{Old: "gateway.grok.password_auth_enabled", New: "provider.grok.password_auth_enabled"},
		{Old: "security.url_allowlist.crs_hosts", New: "security.url_allowlist.crs_sync_hosts"},
		{Old: "gateway.user_message_queue.mode", New: "gateway.anthropic.user_message_queue.default_mode"},
		{Old: "server.h2c.max_read_frame_size", New: "server.h2c.max_read_frame_bytes"},
		{Old: "security.proxy_probe.urls", New: "egress.proxy_probe.urls"},
		{Old: "server.h2c.idle_timeout", New: "server.h2c.idle_timeout_seconds"},
		{Old: "gateway.openai_high_effort_first_output_timeout_seconds", New: "gateway.openai.high_effort_first_output_timeout_seconds"},
		{Old: "gateway.openai_passthrough_allow_timeout_headers", New: "gateway.openai.passthrough_allow_timeout_headers"},
		{Old: "gateway.forced_codex_instructions_template_file", New: "gateway.openai.codex.forced_instructions_template_file"},
		{Old: "gateway.codex_image_generation_bridge_enabled", New: "gateway.openai.codex.image_generation_bridge_enabled"},
		{Old: "batch_image.vertex_batch_prediction_base_url", New: "batch_image.vertex.batch_prediction_base_url"},
		{Old: "gateway.openai_first_output_timeout_seconds", New: "gateway.openai.first_output_timeout_seconds"},
		{Old: "gateway.proxy_probe_response_read_max_bytes", New: "egress.proxy_probe.response_max_bytes"},
		{Old: "security.trust_forwarded_ip_for_api_key_acl", New: "server.client_ip.trust_forwarded_headers"},
		{Old: "batch_image.delayed_mover_interval_seconds", New: "batch_image.queue.delayed_mover_interval_seconds"},
		{Old: "gateway.image_nonstream_keepalive_interval", New: "gateway.images.nonstream_keepalive_interval_seconds"},
		{Old: "gateway.image_stream_data_interval_timeout", New: "gateway.images.stream_data_idle_timeout_seconds"},
		{Old: "batch_image.default_requeue_delay_seconds", New: "batch_image.queue.default_requeue_delay_seconds"},
		{Old: "gateway.log_upstream_error_body_max_bytes", New: "gateway.upstream_error_body_log.max_bytes"},
		{Old: "gateway.user_group_rate_cache_ttl_seconds", New: "billing.group_rate_cache_ttl_seconds"},
		{Old: "gateway.upstream_response_read_max_bytes", New: "gateway.upstream_response_max_bytes"},
		{Old: "token_refresh.provider_failure_threshold", New: "provider.token_refresh.platform_failure_threshold"},
		{Old: "batch_image.lock_conflict_delay_seconds", New: "batch_image.queue.lock_conflict_delay_seconds"},
		{Old: "creative.delayed_mover_interval_seconds", New: "creative.queue.delayed_mover_interval_seconds"},
		{Old: "gateway.image_stream_keepalive_interval", New: "gateway.images.stream_keepalive_interval_seconds"},
		{Old: "batch_image.stale_active_after_seconds", New: "batch_image.queue.stale_active_after_seconds"},
		{Old: "creative.default_requeue_delay_seconds", New: "creative.queue.default_requeue_delay_seconds"},
		{Old: "gateway.openai_response_header_timeout", New: "gateway.openai.response_header_timeout_seconds"},
		{Old: "api_key_auth_cache.lookup_concurrency", New: "api_key.lookup_concurrency"},
		{Old: "batch_image.error_retry_delay_seconds", New: "batch_image.queue.error_retry_delay_seconds"},
		{Old: "batch_image.max_items_per_job_default", New: "batch_image.max_items_per_job"},
		{Old: "batch_image.max_prompt_chars_per_item", New: "batch_image.max_prompt_bytes_per_item"},
		{Old: "batch_image.recovery_interval_seconds", New: "batch_image.queue.recovery_interval_seconds"},
		{Old: "batch_image.vertex_managed_gcs_bucket", New: "batch_image.vertex.managed_gcs_bucket"},
		{Old: "batch_image.vertex_managed_gcs_prefix", New: "batch_image.vertex.managed_gcs_prefix"},
		{Old: "gateway.gemini_debug_response_headers", New: "gateway.gemini.debug_response_headers"},
		{Old: "rate_limit.oauth_401_cooldown_minutes", New: "provider.cooldown.oauth_401_minutes"},
		{Old: "creative.lock_conflict_delay_seconds", New: "creative.queue.lock_conflict_delay_seconds"},
		{Old: "gateway.concurrency_slot_ttl_minutes", New: "scheduling.concurrency_slot_ttl_minutes"},
		{Old: "gateway.grok_response_header_timeout", New: "gateway.grok.response_header_timeout_seconds"},
		{Old: "gateway.max_provider_switches_gemini", New: "gateway.failover.max_provider_switches_gemini"},
		{Old: "gateway.session_idle_timeout_minutes", New: "scheduling.default_session_idle_timeout_minutes"},
		{Old: "gateway.stream_data_interval_timeout", New: "gateway.streaming.data_idle_timeout_seconds"},
		{Old: "rate_limit.overload_cooldown_minutes", New: "provider.cooldown.overload_minutes"},
		{Old: "security.forwarded_client_ip_headers", New: "server.client_ip.forwarded_headers"},
		{Old: "creative.stale_active_after_seconds", New: "creative.queue.stale_active_after_seconds"},
		{Old: "gateway.openai_proxy_stream_circuit", New: "gateway.openai.proxy_stream_circuit"},
		{Old: "idempotency.max_stored_response_len", New: "idempotency.max_stored_response_bytes"},
		{Old: "creative.error_retry_delay_seconds", New: "creative.queue.error_retry_delay_seconds"},
		{Old: "creative.recovery_interval_seconds", New: "creative.queue.recovery_interval_seconds"},
		{Old: "gateway.models_list_read_max_bytes", New: "gateway.models_list_response_max_bytes"},
		{Old: "token_refresh.provider_concurrency", New: "provider.token_refresh.platform_concurrency"},
		{Old: "gateway.connection_pool_isolation", New: "gateway.transport.connection_pool_isolation"},
		{Old: "gateway.idle_conn_timeout_seconds", New: "gateway.transport.idle_conn_timeout_seconds"},
		{Old: "gateway.stream_keepalive_interval", New: "gateway.streaming.keepalive_interval_seconds"},
		{Old: "api_key_auth_cache.invalid_abuse", New: "api_key.invalid_key_abuse"},
		{Old: "batch_image.inflight_ttl_seconds", New: "batch_image.queue.inflight_ttl_seconds"},
		{Old: "batch_image.job_lock_ttl_seconds", New: "batch_image.queue.job_lock_ttl_seconds"},
		{Old: "server.tls_fingerprint_collector", New: "egress.tls_fingerprint_collector"},
		{Old: "batch_image.inflight_key_prefix", New: "batch_image.queue.inflight_key_prefix"},
		{Old: "batch_image.vertex_gcs_base_url", New: "batch_image.vertex.gcs_base_url"},
		{Old: "gateway.client_idle_ttl_seconds", New: "gateway.transport.client_cache.idle_ttl_seconds"},
		{Old: "gateway.log_upstream_error_body", New: "gateway.upstream_error_body_log.enabled"},
		{Old: "gateway.max_idle_conns_per_host", New: "gateway.transport.max_idle_conns_per_host"},
		{Old: "gateway.response_header_timeout", New: "gateway.transport.response_header_timeout_seconds"},
		{Old: "batch_image.delayed_move_limit", New: "batch_image.queue.delayed_move_limit"},
		{Old: "gateway.inject_beta_for_apikey", New: "gateway.anthropic.inject_beta_for_api_key"},
		{Old: "pricing.check_interval_minutes", New: "model_catalog.check_interval_minutes"},
		{Old: "batch_image.queue_delayed_key", New: "batch_image.queue.delayed_key"},
		{Old: "batch_image.vertex_project_id", New: "batch_image.vertex.project_id"},
		{Old: "creative.inflight_ttl_seconds", New: "creative.queue.inflight_ttl_seconds"},
		{Old: "creative.job_lock_ttl_seconds", New: "creative.queue.job_lock_ttl_seconds"},
		{Old: "gateway.max_provider_switches", New: "gateway.failover.max_provider_switches"},
		{Old: "jwt.refresh_token_expire_days", New: "auth.jwt.refresh_token_ttl_days"},
		{Old: "batch_image.queue_active_key", New: "batch_image.queue.active_key"},
		{Old: "creative.inflight_key_prefix", New: "creative.queue.inflight_key_prefix"},
		{Old: "gateway.max_upstream_clients", New: "gateway.transport.client_cache.max_clients"},
		{Old: "gateway.openai_compact_model", New: "gateway.openai.compact_model"},
		{Old: "server.max_request_body_size", New: "server.max_request_body_bytes"},
		{Old: "batch_image.lock_key_prefix", New: "batch_image.queue.lock_key_prefix"},
		{Old: "batch_image.queue_ready_key", New: "batch_image.queue.ready_key"},
		{Old: "batch_image.vertex_location", New: "batch_image.vertex.location"},
		{Old: "creative.delayed_move_limit", New: "creative.queue.delayed_move_limit"},
		{Old: "creative.queue_delayed_key", New: "creative.queue.delayed_key"},
		{Old: "gateway.advanced_scheduler", New: "scheduling.advanced"},
		{Old: "gateway.max_conns_per_host", New: "gateway.transport.max_conns_per_host"},
		{Old: "gateway.text_max_body_size", New: "gateway.text_max_request_body_bytes"},
		{Old: "gateway.user_message_queue", New: "gateway.anthropic.user_message_queue"},
		{Old: "server.read_header_timeout", New: "server.read_header_timeout_seconds"},
		{Old: "token_refresh.provider_qps", New: "provider.token_refresh.platform_qps"},
		{Old: "batch_image.recover_limit", New: "batch_image.queue.recover_limit"},
		{Old: "concurrency.ping_interval", New: "gateway.streaming.wait_ping_interval_seconds"},
		{Old: "creative.queue_active_key", New: "creative.queue.active_key"},
		{Old: "gateway.image_concurrency", New: "gateway.images.concurrency"},
		{Old: "security.response_headers", New: "gateway.upstream_response_headers"},
		{Old: "team.default_member_limit", New: "team.default_max_members"},
		{Old: "token_refresh.max_retries", New: "provider.token_refresh.max_attempts"},
		{Old: "creative.lock_key_prefix", New: "creative.queue.lock_key_prefix"},
		{Old: "creative.queue_ready_key", New: "creative.queue.ready_key"},
		{Old: "default.user_concurrency", New: "user_defaults.concurrency"},
		{Old: "billing.circuit_breaker", New: "billing.balance_check_circuit_breaker"},
		{Old: "default.rate_multiplier", New: "billing.fallback_rate_multiplier"},
		{Old: "gateway.failover_on_400", New: "gateway.failover.messages_on_400"},
		{Old: "gateway.force_codex_cli", New: "gateway.openai.codex.force_cli"},
		{Old: "creative.queue_enabled", New: "creative.queue_worker_enabled"},
		{Old: "creative.recover_limit", New: "creative.queue.recover_limit"},
		{Old: "default.api_key_prefix", New: "api_key.prefix"},
		{Old: "gateway.max_idle_conns", New: "gateway.transport.max_idle_conns"},
		{Old: "server.trusted_proxies", New: "server.client_ip.trusted_proxies"},
		{Old: "gateway.max_body_size", New: "gateway.max_request_body_bytes"},
		{Old: "gateway.max_line_size", New: "gateway.max_sse_line_bytes"},
		{Old: "pricing.fallback_file", New: "model_catalog.supplement_file"},
		{Old: "default.user_balance", New: "user_defaults.balance"},
		{Old: "gateway.cn_providers", New: "provider.usage_monitor"},
		{Old: "gateway.openai_http2", New: "gateway.openai.http2"},
		{Old: "gateway.usage_record", New: "usage.record_queue"},
		{Old: "server.frontend_url", New: "site.frontend_url"},
		{Old: "server.idle_timeout", New: "server.idle_timeout_seconds"},
		{Old: "totp.encryption_key", New: "security.secret_encryption_key"},
		{Old: "gateway.scheduling", New: "scheduling.snapshot"},
		{Old: "pricing.remote_url", New: "model_catalog.catalog_url"},
		{Old: "turnstile.required", New: "auth.require_captcha"},
		{Old: "gateway.openai_ws", New: "gateway.openai.responses_ws"},
		{Old: "pricing.data_dir", New: "storage.dir"},
		{Old: "update.proxy_url", New: "outbound_proxy.url"},
		{Old: "gateway.live", New: "gateway.openai.live"},
		{Old: "jwt.secret", New: "auth.jwt.secret"},
		{Old: "log.env", New: "environment"},
		{Old: "dashboard_aggregation", New: "usage.aggregation"},
		{Old: "api_key_auth_cache", New: "api_key.auth_cache"},
		{Old: "dingtalk_connect", New: "auth.oauth.dingtalk"},
		{Old: "dashboard_cache", New: "usage.dashboard_cache"},
		{Old: "linuxdo_connect", New: "auth.oauth.linuxdo"},
		{Old: "wechat_connect", New: "auth.oauth.wechat"},
		{Old: "token_refresh", New: "provider.token_refresh"},
		{Old: "usage_cleanup", New: "usage.cleanup_tasks"},
		{Old: "github_oauth", New: "auth.oauth.github"},
		{Old: "google_oauth", New: "auth.oauth.google"},
		{Old: "oidc_connect", New: "auth.oauth.oidc"},
		{Old: "webauthn", New: "auth.webauthn"},
		{Old: "gemini", New: "provider.gemini"},
		{Old: "cors", New: "security.cors"},
		{Old: "batch_image.cleanup_batch_size", New: "batch_image.cleanup_batch_size"},
		{Old: "batch_image.cleanup_interval_minutes", New: "batch_image.cleanup_interval_minutes"},
		{Old: "batch_image.default_image_size", New: "batch_image.default_image_size"},
		{Old: "batch_image.default_response_mime_type", New: "batch_image.default_response_mime_type"},
		{Old: "batch_image.input_retention_after_terminal_hours", New: "batch_image.input_retention_after_terminal_hours"},
		{Old: "batch_image.max_download_bytes_per_request", New: "batch_image.max_download_bytes_per_request"},
		{Old: "batch_image.max_download_concurrency_per_user", New: "batch_image.max_download_concurrency_per_user"},
		{Old: "batch_image.max_download_duration_seconds", New: "batch_image.max_download_duration_seconds"},
		{Old: "batch_image.max_download_items_zip", New: "batch_image.max_download_items_zip"},
		{Old: "batch_image.max_output_images_per_item", New: "batch_image.max_output_images_per_item"},
		{Old: "batch_image.max_output_images_per_job", New: "batch_image.max_output_images_per_job"},
		{Old: "batch_image.max_reference_images_per_job", New: "batch_image.max_reference_images_per_job"},
		{Old: "batch_image.max_reference_inline_bytes_per_job", New: "batch_image.max_reference_inline_bytes_per_job"},
		{Old: "batch_image.output_retention_after_terminal_hours", New: "batch_image.output_retention_after_terminal_hours"},
		{Old: "billing.minimum_balance_reserve", New: "billing.minimum_balance_reserve"},
		{Old: "creative.default_image_size", New: "creative.default_image_size"},
		{Old: "creative.enabled", New: "creative.enabled"},
		{Old: "creative.execute_timeout_seconds", New: "creative.execute_timeout_seconds"},
		{Old: "creative.max_asset_bytes", New: "creative.max_asset_bytes"},
		{Old: "creative.max_execute_attempts", New: "creative.max_execute_attempts"},
		{Old: "creative.max_prompt_chars", New: "creative.max_prompt_chars"},
		{Old: "creative.max_total_input_bytes", New: "creative.max_total_input_bytes"},
		{Old: "creative.transient_ttl_seconds", New: "creative.transient_ttl_seconds"},
		{Old: "database.conn_max_idle_time_minutes", New: "database.conn_max_idle_time_minutes"},
		{Old: "database.conn_max_lifetime_minutes", New: "database.conn_max_lifetime_minutes"},
		{Old: "database.dbname", New: "database.dbname"},
		{Old: "database.host", New: "database.host"},
		{Old: "database.max_idle_conns", New: "database.max_idle_conns"},
		{Old: "database.max_open_conns", New: "database.max_open_conns"},
		{Old: "database.password", New: "database.password"},
		{Old: "database.port", New: "database.port"},
		{Old: "database.sslmode", New: "database.sslmode"},
		{Old: "database.user", New: "database.user"},
		{Old: "idempotency.cleanup_batch_size", New: "idempotency.cleanup_batch_size"},
		{Old: "idempotency.cleanup_interval_seconds", New: "idempotency.cleanup_interval_seconds"},
		{Old: "idempotency.default_ttl_seconds", New: "idempotency.default_ttl_seconds"},
		{Old: "idempotency.failed_retry_backoff_seconds", New: "idempotency.failed_retry_backoff_seconds"},
		{Old: "idempotency.observe_only", New: "idempotency.observe_only"},
		{Old: "idempotency.processing_timeout_seconds", New: "idempotency.processing_timeout_seconds"},
		{Old: "idempotency.system_operation_ttl_seconds", New: "idempotency.system_operation_ttl_seconds"},
		{Old: "log.caller", New: "log.caller"},
		{Old: "log.format", New: "log.format"},
		{Old: "log.level", New: "log.level"},
		{Old: "log.output.file_path", New: "log.output.file_path"},
		{Old: "log.output.to_file", New: "log.output.to_file"},
		{Old: "log.output.to_stdout", New: "log.output.to_stdout"},
		{Old: "log.rotation.compress", New: "log.rotation.compress"},
		{Old: "log.rotation.local_time", New: "log.rotation.local_time"},
		{Old: "log.rotation.max_age_days", New: "log.rotation.max_age_days"},
		{Old: "log.rotation.max_backups", New: "log.rotation.max_backups"},
		{Old: "log.rotation.max_size_mb", New: "log.rotation.max_size_mb"},
		{Old: "log.sampling.enabled", New: "log.sampling.enabled"},
		{Old: "log.sampling.initial", New: "log.sampling.initial"},
		{Old: "log.sampling.thereafter", New: "log.sampling.thereafter"},
		{Old: "log.service_name", New: "log.service_name"},
		{Old: "log.stacktrace_level", New: "log.stacktrace_level"},
		{Old: "ops.aggregation.enabled", New: "ops.aggregation.enabled"},
		{Old: "ops.cleanup.batch_pause_ms", New: "ops.cleanup.batch_pause_ms"},
		{Old: "ops.cleanup.batch_size", New: "ops.cleanup.batch_size"},
		{Old: "ops.cleanup.enabled", New: "ops.cleanup.enabled"},
		{Old: "ops.cleanup.error_log_retention_days", New: "ops.cleanup.error_log_retention_days"},
		{Old: "ops.cleanup.hourly_metrics_retention_days", New: "ops.cleanup.hourly_metrics_retention_days"},
		{Old: "ops.cleanup.minute_metrics_retention_days", New: "ops.cleanup.minute_metrics_retention_days"},
		{Old: "ops.cleanup.schedule", New: "ops.cleanup.schedule"},
		{Old: "ops.cleanup.system_log_retention_days", New: "ops.cleanup.system_log_retention_days"},
		{Old: "ops.enabled", New: "ops.enabled"},
		{Old: "plugins.dev_dirs", New: "plugins.dev_dirs"},
		{Old: "plugins.dev_mode", New: "plugins.dev_mode"},
		{Old: "plugins.dir", New: "plugins.dir"},
		{Old: "plugins.download_proxy", New: "plugins.download_proxy"},
		{Old: "plugins.download_url_prefix", New: "plugins.download_url_prefix"},
		{Old: "plugins.enabled", New: "plugins.enabled"},
		{Old: "plugins.max_running", New: "plugins.max_running"},
		{Old: "plugins.official_index_url", New: "plugins.official_index_url"},
		{Old: "plugins.plugin_http_proxy", New: "plugins.plugin_http_proxy"},
		{Old: "redis.db", New: "redis.db"},
		{Old: "redis.dial_timeout_seconds", New: "redis.dial_timeout_seconds"},
		{Old: "redis.enable_tls", New: "redis.enable_tls"},
		{Old: "redis.host", New: "redis.host"},
		{Old: "redis.min_idle_conns", New: "redis.min_idle_conns"},
		{Old: "redis.password", New: "redis.password"},
		{Old: "redis.pool_size", New: "redis.pool_size"},
		{Old: "redis.port", New: "redis.port"},
		{Old: "redis.read_timeout_seconds", New: "redis.read_timeout_seconds"},
		{Old: "redis.username", New: "redis.username"},
		{Old: "redis.write_timeout_seconds", New: "redis.write_timeout_seconds"},
		{Old: "request_log.retention_days", New: "request_log.retention_days"},
		{Old: "security.csp.enabled", New: "security.csp.enabled"},
		{Old: "security.csp.policy", New: "security.csp.policy"},
		{Old: "security.url_allowlist.allow_insecure_http", New: "security.url_allowlist.allow_insecure_http"},
		{Old: "security.url_allowlist.allow_private_hosts", New: "security.url_allowlist.allow_private_hosts"},
		{Old: "security.url_allowlist.enabled", New: "security.url_allowlist.enabled"},
		{Old: "security.url_allowlist.upstream_hosts", New: "security.url_allowlist.upstream_hosts"},
		{Old: "server.enable_server_timing", New: "server.enable_server_timing"},
		{Old: "server.h2c.enabled", New: "server.h2c.enabled"},
		{Old: "server.h2c.max_concurrent_streams", New: "server.h2c.max_concurrent_streams"},
		{Old: "server.host", New: "server.host"},
		{Old: "server.max_header_bytes", New: "server.max_header_bytes"},
		{Old: "server.mode", New: "server.mode"},
		{Old: "server.port", New: "server.port"},
		{Old: "team.enabled", New: "team.enabled"},
		{Old: "team.self_service_enabled", New: "team.self_service_enabled"},
		{Old: "timezone", New: "timezone"},
	}

	// v2Removed identifies settings whose process configuration no longer has a
	// consumer, plus obsolete values consolidated by the version 1 semantic rules.
	v2Removed = map[string]string{
		"gateway.tls_fingerprint":                                "TLS profiles are managed in the database",
		"gateway.openai_ws.metadata_bridge_enabled":              "no consumer",
		"gateway.antigravity_extra_retries":                      "no decoded field",
		"gateway.models_list_cache_ttl_seconds":                  "no longer controls caching",
		"creative.default_response_mime_type":                    "no consumer",
		"security.proxy_probe.insecure_skip_verify":              "true prevents proxy probes",
		"subscription_cache":                                     "no consumer",
		"default.admin_email":                                    "setup reads ADMIN_EMAIL",
		"default.admin_password":                                 "setup reads ADMIN_PASSWORD",
		"jwt.refresh_window_minutes":                             "no consumer",
		"ops.metrics_collector_cache":                            "no consumer",
		"pricing.override_file":                                  "retired; configure prices in Pricing Management",
		"pricing.hash_url":                                       "retired; configure prices in Pricing Management",
		"pricing.update_interval_hours":                          "retired; configure prices in Pricing Management",
		"dingtalk_connect.username_overwrite_policy":             "no consumer",
		"dingtalk_connect.username_attribute_key":                "no consumer",
		"dingtalk_connect.enable_attribute_matching":             "no consumer",
		"dingtalk_connect.enable_attribute_sync":                 "no consumer",
		"dingtalk_connect.attribute_sync_overwrite_policy":       "no consumer",
		"dingtalk_connect.attribute_sync_fields":                 "no consumer",
		"wechat_connect.app_id":                                  "merged into auth.oauth.wechat.open_app_id",
		"wechat_connect.app_secret":                              "merged into auth.oauth.wechat.open_app_secret",
		"gateway.user_message_queue.enabled":                     "merged into gateway.anthropic.user_message_queue.default_mode",
		"gateway.openai_ws.sticky_previous_response_ttl_seconds": "merged into scheduling.session_affinity.response_id_ttl_seconds",
		"gateway.openai_proxy_stream_circuit.disabled":           "merged into gateway.openai.proxy_stream_circuit.enabled",
		"jwt.expire_hour":                                        "merged into auth.jwt.access_token_ttl_minutes",
		"jwt.access_token_expire_minutes":                        "merged into auth.jwt.access_token_ttl_minutes",
		"gateway.antigravity_fallback_cooldown_minutes":          "merged into provider.antigravity.fallback_cooldown_seconds",
	}

	// v1Aliases preserves the old loader's explicit current-name precedence when
	// both spellings appear, including deliberately supplied zero and false values.
	v1Aliases = []configMove{
		{Old: "pricing.hash_check_interval_minutes", New: "pricing.check_interval_minutes"},
		{Old: "gateway.max_account_switches", New: "gateway.max_provider_switches"},
		{Old: "gateway.max_account_switches_gemini", New: "gateway.max_provider_switches_gemini"},
		{Old: "gateway.openai_ws.max_conns_per_account", New: "gateway.openai_ws.max_conns_per_provider"},
		{Old: "gateway.openai_ws.min_idle_per_account", New: "gateway.openai_ws.min_idle_per_provider"},
		{Old: "gateway.openai_ws.max_idle_per_account", New: "gateway.openai_ws.max_idle_per_provider"},
		{Old: "gateway.openai_ws.dynamic_max_conns_by_account_concurrency_enabled", New: "gateway.openai_ws.dynamic_max_conns_by_provider_concurrency_enabled"},
		{Old: "gateway.openai_ws.lb_top_k", New: "gateway.advanced_scheduler.lb_top_k"},
		{Old: "gateway.openai_ws.scheduler_score_weights.priority", New: "gateway.advanced_scheduler.score_weights.priority"},
		{Old: "gateway.openai_ws.scheduler_score_weights.load", New: "gateway.advanced_scheduler.score_weights.load"},
		{Old: "gateway.openai_ws.scheduler_score_weights.queue", New: "gateway.advanced_scheduler.score_weights.queue"},
		{Old: "gateway.openai_ws.scheduler_score_weights.error_rate", New: "gateway.advanced_scheduler.score_weights.error_rate"},
		{Old: "gateway.openai_ws.scheduler_score_weights.ttft", New: "gateway.advanced_scheduler.score_weights.ttft"},
		{Old: "gateway.openai_ws.scheduler_score_weights.reset", New: "gateway.advanced_scheduler.score_weights.reset"},
		{Old: "gateway.openai_ws.scheduler_score_weights.quota_headroom", New: "gateway.advanced_scheduler.score_weights.quota_headroom"},
		{Old: "gateway.openai_ws.scheduler_score_weights.previous_response", New: "gateway.advanced_scheduler.score_weights.previous_response"},
		{Old: "gateway.openai_ws.scheduler_score_weights.session_sticky", New: "gateway.advanced_scheduler.score_weights.session_sticky"},
		{Old: "gateway.openai_scheduler.sticky_escape_enabled", New: "gateway.advanced_scheduler.sticky_escape_enabled"},
		{Old: "gateway.openai_scheduler.sticky_escape_ttft_ms", New: "gateway.advanced_scheduler.sticky_escape_ttft_ms"},
		{Old: "gateway.openai_scheduler.sticky_escape_error_rate", New: "gateway.advanced_scheduler.sticky_escape_error_rate"},
	}
)

// configMove identifies an old path and its replacement in a version transition.
type configMove struct {
	Old string // historical dotted key
	New string // replacement dotted key
}

// upgradeV1ToV2 imports the historical environment and reorganizes version 1 keys.
// It mutates the supplied document only; persistence and backup belong to the
// framework. Alias resolution runs before import so current YAML fields retain
// precedence over their retired spellings, while eligible environment overrides
// still win over either file spelling.
// Null fields inherit defaults; null sections retain their historical effect on
// environment selection before they are removed from the migrated document.
//
// Semantic rules run before deletion and movement. Unknown legacy keys are
// removed because the old decoder ignored them, but known version 2 destinations
// cannot silently replace a moved value. Reports include paths and environment
// names only, and returned documents retain explicit secret references.
// @project-doc docs/interfaces/configuration.md#config_versioning
func upgradeV1ToV2(doc *yaml.Node, env upgradeEnvironment) (upgradeReport, error) {
	var report upgradeReport
	expanded, err := expandConfigAliases(doc, map[*yaml.Node]bool{})
	if err != nil {
		return report, err
	}
	*doc = *expanded
	root := doc.Content[0]
	if err := canonicalizeV1Paths(root, ""); err != nil {
		return report, err
	}
	// Null scalar fields did not count as configured in Viper. Remove them before
	// alias precedence and transforms, retaining null sections until environment selection.
	for _, key := range v1Keys {
		if node := configPath(root, key.Path); node != nil && node.Tag == "!!null" {
			removeConfigPath(root, key.Path)
		}
	}
	// Version 1 treated reference-looking strings literally. Escape those source
	// values before importing new references so existing passwords survive loading.
	if err = visitConfigStrings(doc, "", func(_ string, n *yaml.Node) error { n.Value = strings.ReplaceAll(n.Value, "${", "$${"); return nil }); err != nil {
		return report, err
	}
	for _, alias := range v1Aliases {
		if node := configPath(root, alias.Old); node != nil && node.Tag == "!!null" {
			removeConfigPath(root, alias.Old)
		}
		if configPath(root, alias.New) != nil {
			removeConfigPath(root, alias.Old)
			continue
		}
		if err = moveConfigPath(root, alias.Old, alias.New, &report); err != nil {
			return report, err
		}
	}
	if err = importV1Environment(root, env.LookupEnv, &report); err != nil {
		return report, err
	}
	pruneNullV1Mappings(root)
	if err = transformV1Values(root, env, &report); err != nil {
		return report, err
	}
	pruneUnknownV1Keys(root, "", &report)
	for key, reason := range v2Removed {
		if configPath(root, key) != nil {
			removeConfigPath(root, key)
			report.Dropped = append(report.Dropped, key+": "+reason)
		}
	}
	for _, move := range v2Moves {
		if err = moveConfigPath(root, move.Old, move.New, &report); err != nil {
			return report, err
		}
	}
	if err = importExtendedEnvironment(root, env.LookupEnv, &report); err != nil {
		return report, err
	}
	pruneEmptyConfigMappings(root)
	slices.Sort(report.Dropped)
	slices.Sort(report.ImportedEnv)
	return report, nil
}

// pruneNullV1Mappings removes unset mapping entries after historical environment selection.
// Null sections can suppress implicit Viper bindings, so callers must finish
// importing eligible environment values first. Remaining nulls inherit defaults
// without entering scalar transformations or leaving retired empty sections behind.
//
// Sequence elements retain their exact contents because the old decoder handled
// each list as one value. False, zero, empty strings, and empty sequences remain
// explicit inputs, preserving policies that distinguish them from omitted settings.
func pruneNullV1Mappings(root *yaml.Node) {
	if root.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index < len(root.Content); {
		value := root.Content[index+1]
		if value.Tag == "!!null" {
			root.Content = append(root.Content[:index], root.Content[index+2:]...)
			continue
		}
		pruneNullV1Mappings(value)
		index += 2
	}
}

// pruneUnknownV1Keys removes only paths the old decoder could not consume.
// Map leaves accept arbitrary children. Semantic destinations and consolidated
// temporary fields survive until the move table produces the version 2 document.
func pruneUnknownV1Keys(root *yaml.Node, prefix string, report *upgradeReport) {
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(root.Content); {
		key := root.Content[i].Value
		if prefix != "" {
			key = prefix + "." + key
		}
		known, container := false, false
		for _, entry := range v1Keys {
			if entry.Path == key {
				known = true
				break
			}
			if strings.HasPrefix(entry.Path, key+".") {
				container = true
			}
		}
		for removed := range v2Removed {
			if key == removed {
				known = true
			}
			if strings.HasPrefix(removed, key+".") {
				container = true
			}
		}
		for _, extra := range []string{"config_version", "auth.jwt.access_token_ttl_minutes", "provider.antigravity.fallback_cooldown_seconds", "gateway.openai_proxy_stream_circuit.enabled"} {
			if key == extra {
				known = true
			}
			if strings.HasPrefix(extra, key+".") {
				container = true
			}
		}
		if !known && !container {
			report.Dropped = append(report.Dropped, key+": unknown in config_version 1")
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			continue
		}
		if !known {
			pruneUnknownV1Keys(root.Content[i+1], key, report)
		} else if key == "security.proxy_probe.urls" && root.Content[i+1].Kind == yaml.SequenceNode {
			for index, entry := range root.Content[i+1].Content {
				if entry.Kind != yaml.MappingNode {
					continue
				}
				for offset := 0; offset < len(entry.Content); {
					field := strings.ToLower(entry.Content[offset].Value)
					if field == "url" || field == "parser" {
						entry.Content[offset].Value = field
						offset += 2
						continue
					}
					report.Dropped = append(report.Dropped, fmt.Sprintf("%s[%d].%s: unknown in config_version 1", key, index, entry.Content[offset].Value))
					entry.Content = append(entry.Content[:offset], entry.Content[offset+2:]...)
				}
			}
		}
		i += 2
	}
}

// transformV1Values preserves old effective values before moving their paths.
// It handles coupled lifetimes and feature flags before obsolete inputs disappear.
// Invalid durations fail with a key-only diagnostic; numeric legacy durations
// retain their old nanosecond interpretation and receive an operator warning.
//
// Explicit values are distinguished from missing inputs so sparse configurations
// continue to inherit defaults. Credentials use cloned YAML nodes when expanded
// into multiple WeChat modes, keeping secret references intact. Derived targets
// reject supplied destinations because mixing versions would make precedence
// ambiguous and could change the effective setting during an automatic upgrade.
func transformV1Values(root *yaml.Node, env upgradeEnvironment, report *upgradeReport) error {
	put := func(key string, value any) error { return setConfigPath(root, key, configValueNode(value)) }
	text := func(key string) string {
		n := configPath(root, key)
		if n == nil {
			return ""
		}
		return strings.TrimSpace(n.Value)
	}
	integer := func(key string, fallback int64) (int64, error) {
		n := configPath(root, key)
		if n == nil {
			return fallback, nil
		}
		v, e := strconv.ParseInt(strings.TrimSpace(n.Value), 0, 64)
		if e != nil {
			return 0, fmt.Errorf("%s must be an integer during configuration upgrade", key)
		}
		return v, nil
	}
	boolean := func(key string) bool { v, _ := strconv.ParseBool(text(key)); return v }
	// Retired pool aliases were exact enum spellings. Do not trim invalid values
	// into accepted aliases, because the current validator must still reject them.
	isolation := ""
	if node := configPath(root, "gateway.connection_pool_isolation"); node != nil {
		isolation = node.Value
	}
	switch isolation {
	case "account":
		if err := put("gateway.connection_pool_isolation", "provider"); err != nil {
			return err
		}
	case "account_proxy":
		if err := put("gateway.connection_pool_isolation", "provider_proxy"); err != nil {
			return err
		}
	}
	// The old loader normalized server mode before validation, including an empty
	// supplied value selecting debug; preserve that choice before v2 becomes strict.
	if node := configPath(root, "server.mode"); node != nil {
		if node.Kind != yaml.ScalarNode {
			return fmt.Errorf("server.mode must be a scalar during configuration upgrade")
		}
		mode := strings.ToLower(text("server.mode"))
		if mode == "" {
			mode = "debug"
		}
		if err := put("server.mode", mode); err != nil {
			return err
		}
	}
	for _, key := range []string{"gateway.scheduling.sticky_session_wait_timeout", "gateway.scheduling.fallback_wait_timeout", "gateway.scheduling.slot_cleanup_interval"} {
		node := configPath(root, key)
		if node == nil {
			continue
		}
		seconds, err := legacyDurationSeconds(node, key, report)
		if err != nil {
			return err
		}
		if err = put(key, seconds); err != nil {
			return err
		}
	}
	// The effective affinity lifetime prefers a positive current value, then the
	// retired positive value; missing fields retain their historical one-hour defaults.
	current := "gateway.openai_ws.sticky_response_id_ttl_seconds"
	previous := "gateway.openai_ws.sticky_previous_response_ttl_seconds"
	if configPath(root, current) != nil || configPath(root, previous) != nil {
		cur, err := integer(current, 3600)
		if err != nil {
			return err
		}
		prev, err := integer(previous, 3600)
		if err != nil {
			return err
		}
		if cur <= 0 && prev > 0 {
			cur = prev
		}
		if err = put(current, cur); err != nil {
			return err
		}
	}
	modeKey := "gateway.user_message_queue.mode"
	enabledKey := "gateway.user_message_queue.enabled"
	if configPath(root, modeKey) != nil || configPath(root, enabledKey) != nil {
		// Only exact mode names were accepted by the old queue selector. Preserve
		// its enabled-flag fallback for whitespace or other unsupported spellings.
		mode := ""
		if node := configPath(root, modeKey); node != nil {
			if node.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s must be a scalar during configuration upgrade", modeKey)
			}
			mode = node.Value
		}
		enabled, err := legacyBoolean(root, enabledKey)
		if err != nil {
			return err
		}
		if mode != "serialize" && mode != "throttle" {
			if mode != "" {
				report.Warnings = append(report.Warnings, modeKey+": invalid mode replaced using the legacy enabled flag")
			}
			mode = ""
			if enabled {
				mode = "serialize"
			}
		}
		if err := put(modeKey, mode); err != nil {
			return err
		}
	}
	// Version 2 expresses circuit availability positively, so invert the supplied
	// retired disable switch while leaving an absent switch to current defaults.
	disabled := "gateway.openai_proxy_stream_circuit.disabled"
	if configPath(root, disabled) != nil {
		disabledValue, err := legacyBoolean(root, disabled)
		if err != nil {
			return err
		}
		if err := put("gateway.openai_proxy_stream_circuit.enabled", !disabledValue); err != nil {
			return err
		}
	}
	// The positive minute override previously won over hours. Consolidate that
	// decision before deleting both historical fields so token lifetimes remain unchanged.
	access, err := integer("jwt.access_token_expire_minutes", 0)
	if err != nil {
		return err
	}
	if access > 0 || configPath(root, "jwt.expire_hour") != nil {
		if access <= 0 {
			hours, err := integer("jwt.expire_hour", 24)
			if err != nil {
				return err
			}
			if hours > math.MaxInt64/60 || hours < math.MinInt64/60 {
				return fmt.Errorf("jwt.expire_hour is out of range")
			}
			// Convert the old hour setting to the new minute-based token lifetime.
			access = hours * 60
		}
		if configPath(root, "auth.jwt.access_token_ttl_minutes") != nil {
			return fmt.Errorf("configuration mixes jwt and auth.jwt lifetime settings")
		}
		if err = put("auth.jwt.access_token_ttl_minutes", access); err != nil {
			return err
		}
	}
	// A valid direct seconds override wins over minutes. Otherwise retain the
	// old observer fallback for nonpositive minutes instead of applying the new default.
	cooldown, err := integer("gateway.antigravity_fallback_cooldown_minutes", 0)
	if err != nil {
		return err
	}
	raw, present := env.LookupEnv("GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS")
	seconds, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if parseErr == nil && seconds > 0 {
		report.ImportedEnv = append(report.ImportedEnv, "GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS")
	} else {
		present = false
		if cooldown > math.MaxInt64/60 {
			return fmt.Errorf("gateway.antigravity_fallback_cooldown_minutes is out of range")
		}
		// Nonpositive legacy minutes used the upstream thirty-second fallback.
		seconds = 30
		if cooldown > 0 {
			seconds = cooldown * 60
		}
	}
	if present || configPath(root, "gateway.antigravity_fallback_cooldown_minutes") != nil {
		if configPath(root, "provider.antigravity.fallback_cooldown_seconds") != nil {
			return fmt.Errorf("configuration mixes Antigravity cooldown versions")
		}
		if err = put("provider.antigravity.fallback_cooldown_seconds", seconds); err != nil {
			return err
		}
	}
	if boolean("security.proxy_probe.insecure_skip_verify") {
		report.Warnings = append(report.Warnings, "security.proxy_probe.insecure_skip_verify: true prevented probes in version 1; removed")
	}
	if err = transformV1WeChat(root, env.LookupEnv); err != nil {
		return err
	}
	return transformV1Catalog(root, env)
}

// transformV1WeChat expands generic credentials into absent per-mode inputs.
// Each credential member selects its first nonblank source independently. A
// legacy enabled-only configuration activates its chosen mode before generic
// fields disappear, preserving existing login availability and secret references.
// Numeric channel flags use the historical nonzero rule, and malformed flags
// fail before the framework can persist a changed login configuration.
//
// Credential selection compares resolved nonblank values but copies the original
// nodes, so environment secrets stay references on disk. Only an enabled
// configuration with no active channel receives a mode-derived capability;
// existing channel choices retain priority over the legacy aggregate flag.
func transformV1WeChat(root *yaml.Node, lookup func(string) (string, bool)) error {
	// The legacy normalizer replaced explicit blank callback destinations. Keep
	// that behavior in migration while current files use registered defaults for omission.
	if node := configPath(root, "wechat_connect.frontend_redirect_url"); node != nil && node.Kind == yaml.ScalarNode && strings.TrimSpace(node.Value) == "" {
		node.Value = defaultWeChatConnectFrontendRedirect
		node.Tag = "!!str"
	}

	// Resolve references only to choose nonblank credential sources. Copy their
	// original nodes afterward so imported secret values never enter persisted YAML.
	for _, suffix := range []string{"app_id", "app_secret"} {
		var selected *yaml.Node
		for _, mode := range []string{"", "open_", "mp_", "mobile_"} {
			candidate := configPath(root, "wechat_connect."+mode+suffix)
			if candidate != nil && migrationWeChatValue(candidate, lookup) != "" {
				selected = candidate
				break
			}
		}
		if selected == nil {
			continue
		}
		for _, mode := range []string{"open_", "mp_", "mobile_"} {
			key := "wechat_connect." + mode + suffix
			candidate := configPath(root, key)
			if candidate == nil || migrationWeChatValue(candidate, lookup) == "" {
				if err := setConfigPath(root, key, cloneConfigNode(selected)); err != nil {
					return err
				}
			}
		}
	}
	enabled, err := legacyBoolean(root, "wechat_connect.enabled")
	if err != nil {
		return err
	}
	hasChannel := false
	for _, mode := range []string{"open", "mp", "mobile"} {
		active, err := legacyBoolean(root, "wechat_connect."+mode+"_enabled")
		if err != nil {
			return err
		}
		hasChannel = hasChannel || active
	}
	if enabled && !hasChannel {
		mode := "open"
		if n := configPath(root, "wechat_connect.mode"); n != nil {
			switch strings.ToLower(strings.TrimSpace(n.Value)) {
			case "mp":
				mode = "mp"
			case "mobile":
				mode = "mobile"
			}
		}
		// Legacy installations could select a mode with only the aggregate flag.
		// Preserve that enabled login path before removing the generic credential fields.
		return setConfigPath(root, "wechat_connect."+mode+"_enabled", configValueNode(true))
	}
	return nil
}

// transformV1Catalog recognizes historical public catalog URLs and packaged paths.
// Custom hosts, userinfo, and existing files retain their supplied spelling.
// Host admission expands only when a recognized legacy URL actually migrates.
func transformV1Catalog(root *yaml.Node, env upgradeEnvironment) error {
	node := configPath(root, "pricing.remote_url")
	if node != nil {
		raw := strings.TrimSpace(node.Value)
		source, err := url.Parse(raw)
		migrate := raw == "https://models.dev/catalog.json"
		legacy := false
		if err == nil && strings.EqualFold(source.Host, "raw.githubusercontent.com") && source.User == nil && (source.Scheme == "http" || source.Scheme == "https") {
			normalized := strings.ToLower(path.Clean(source.Path))
			legacy = (strings.HasPrefix(normalized, "/wei-shaw/model-price-repo/") || strings.HasPrefix(normalized, "/berriai/litellm/")) && strings.HasSuffix(normalized, "/model_prices_and_context_window.json")
		}
		if migrate || legacy {
			node.Value = "https://models.dev/catalog.json?type=all"
		}
		if legacy {
			hosts := configPath(root, "security.url_allowlist.pricing_hosts")
			if hosts != nil && hosts.Kind == yaml.SequenceNode {
				present := false
				for _, host := range hosts.Content {
					if host.Value == "models.dev" {
						present = true
					}
				}
				if !present {
					hosts.Content = append(hosts.Content, configValueNode("models.dev"))
				}
			}
		}
	}
	fallback := configPath(root, "pricing.fallback_file")
	if fallback == nil {
		return nil
	}
	cleaned := filepath.Clean(fallback.Value)
	if cleaned != "resources/model-pricing/model_prices_and_context_window.json" && cleaned != "/app/resources/model-pricing/model_prices_and_context_window.json" {
		return nil
	}
	// Only confirmed absence permits changing a packaged filename. Permission and
	// other stat failures preserve the configured path for normal loading diagnostics.
	if _, err := env.Stat(cleaned); os.IsNotExist(err) {
		fallback.Value = filepath.Join(filepath.Dir(cleaned), "model_pricing_supplements.json")
	}
	return nil
}

// migrationWeChatValue resolves imported references for legacy nonblank selection.
// Source file literals were escaped before import, and generated reference names
// are valid and present. The returned text is used only in memory for precedence.
func migrationWeChatValue(node *yaml.Node, lookup func(string) (string, bool)) string {
	value, _, err := expandConfigString("wechat_connect", node.Value, lookup)
	// Migration creates only valid references; retain unexpected text if parsing
	// fails so credential selection does not silently discard supplied source input.
	if err != nil {
		return strings.TrimSpace(node.Value)
	}
	return strings.TrimSpace(value)
}

// legacyBoolean reads a transformed flag without hiding invalid source values.
// Missing, null, and empty-string fields select false. Numeric YAML scalars retain
// the old weak decoder's nonzero rule; other text requires a valid boolean spelling.
func legacyBoolean(root *yaml.Node, key string) (bool, error) {
	node := configPath(root, key)
	if node == nil || node.Tag == "!!null" || node.Tag == "!!str" && node.Value == "" {
		return false, nil
	}
	if node.Kind != yaml.ScalarNode {
		return false, fmt.Errorf("%s must be a boolean during configuration upgrade", key)
	}
	// The historical weak decoder treated numeric flags as nonzero tests.
	// Preserve that rule when choosing effective modes or consolidating retired switches.
	if node.Tag == "!!int" || node.Tag == "!!float" {
		value, err := strconv.ParseFloat(node.Value, 64)
		if node.Tag == "!!int" {
			integer, intErr := strconv.ParseInt(node.Value, 0, 64)
			value = float64(integer)
			err = intErr
		}
		if err == nil {
			return value != 0, nil
		}
	} else if value, err := strconv.ParseBool(strings.TrimSpace(node.Value)); err == nil {
		return value, nil
	}
	return false, fmt.Errorf("%s must be a boolean during configuration upgrade", key)
}

// legacyDurationSeconds converts historical durations without losing integer precision.
// Strings use Go duration syntax and numeric YAML values represent nanoseconds.
// Integer and parsed-duration inputs use quotient and remainder so a nanosecond
// above a whole second still rounds upward after large elapsed intervals.
// Floating inputs retain their decoded floating-point representation.
//
// Negative or nonfinite input fails, and a rounded result must fit a Go duration
// after application consumers multiply whole seconds back into nanoseconds.
// Numeric inputs add a value-free warning only after a valid conversion.
func legacyDurationSeconds(node *yaml.Node, key string, report *upgradeReport) (int64, error) {
	// Application adapters convert seconds back into signed nanoseconds.
	const maxSeconds = int64(math.MaxInt64) / int64(time.Second)
	var nanos, seconds int64
	switch node.Tag {
	case "!!str":
		value, err := time.ParseDuration(node.Value)
		if err != nil {
			return 0, fmt.Errorf("%s must contain a valid duration", key)
		}
		nanos = int64(value)
	case "!!int":
		value, err := strconv.ParseInt(node.Value, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("%s duration is out of range", key)
		}
		nanos = value
	case "!!float":
		value, err := strconv.ParseFloat(node.Value, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return 0, fmt.Errorf("%s must contain a finite non-negative duration", key)
		}
		rounded := math.Ceil(value / float64(time.Second))
		if rounded > float64(maxSeconds) {
			return 0, fmt.Errorf("%s cannot be represented as whole seconds", key)
		}
		seconds = int64(rounded)
	default:
		return 0, fmt.Errorf("%s must contain a duration string or numeric nanoseconds", key)
	}
	if node.Tag != "!!float" {
		if nanos < 0 {
			return 0, fmt.Errorf("%s duration must be non-negative", key)
		}
		// Quotient and remainder preserve exact integer boundaries without adding a
		// rounding offset that could overflow near the maximum nanosecond duration.
		seconds = nanos / int64(time.Second)
		if nanos%int64(time.Second) != 0 {
			seconds++
		}
		if seconds > maxSeconds {
			return 0, fmt.Errorf("%s cannot be represented as whole seconds", key)
		}
	}
	if node.Tag != "!!str" {
		report.Warnings = append(report.Warnings, key+": version 1 numeric durations were interpreted as nanoseconds")
	}
	return seconds, nil
}
