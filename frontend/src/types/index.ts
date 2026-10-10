import type { LocalizedUpdate } from '@/i18n/content'
import type { ModelAttributes } from './modelAttributes'
/**
 * Core Type Definitions for TokenRouter Frontend
 */

import type { SubscriptionPlan } from './payment'

// ==================== Common Types ====================

export interface SelectOption {
  value: string | number | boolean | null
  label: string
  [key: string]: any // Support extra properties for custom templates
}

export interface BasePaginationResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface FetchOptions {
  signal?: AbortSignal
}

// ==================== Notification Types ====================

/** Notification email entry with enable/disable and verification state.
 *  email="" is a placeholder for the primary email (user's registration email or admin email). */
export interface NotifyEmailEntry {
  email: string
  disabled: boolean
  verified: boolean
}

// ==================== User & Auth Types ====================

export type UserAuthProvider = 'email' | 'linuxdo' | 'oidc' | 'wechat' | 'github' | 'google' | 'dingtalk'

export interface UserAuthBindingStatus {
  bound?: boolean
  bound_count?: number
  provider?: UserAuthProvider | string
  provider_key?: string | null
  provider_subject?: string | null
  issuer?: string | null
  label?: string | null
  provider_label?: string | null
  display_name?: string | null
  subject_hint?: string | null
  verified_at?: string | null
  bind_start_path?: string | null
  can_bind?: boolean
  can_unbind?: boolean
  note_key?: string | null
  note?: string | null
  metadata?: Record<string, unknown>
}

export interface UserProfileSourceContext {
  provider?: UserAuthProvider | string
  source?: string | null
  label?: string | null
  provider_label?: string | null
}

export interface User {
  preferred_locale?: string | null
  id: number
  username: string
  email: string
  avatar_url?: string | null
  avatar_source?: string | UserProfileSourceContext | null
  username_source?: string | UserProfileSourceContext | null
  display_name_source?: string | UserProfileSourceContext | null
  nickname_source?: string | UserProfileSourceContext | null
  profile_sources?: {
    avatar?: string | UserProfileSourceContext | null
    username?: string | UserProfileSourceContext | null
    display_name?: string | UserProfileSourceContext | null
    nickname?: string | UserProfileSourceContext | null
  }
  auth_bindings?: Partial<Record<UserAuthProvider, boolean | UserAuthBindingStatus>>
  identity_bindings?: Partial<Record<UserAuthProvider, boolean | UserAuthBindingStatus>>
  email_bound?: boolean
  linuxdo_bound?: boolean
  oidc_bound?: boolean
  wechat_bound?: boolean
  role: 'admin' | 'user' // 用户角色
  balance: number // 用户余额
  frozen_balance?: number // 异步批量任务当前冻结的余额
  concurrency: number // 允许的并发请求数
  rpm_limit?: number // 用户级 RPM 上限（0 表示无限制）；分组未配置时作为兜底
  api_key_limit: number // 用户可创建的 API Key 数量上限，0 表示不限制
  status: 'active' | 'disabled' // 账号状态
  allowed_groups: number[] | null // 允许的专属分组 ID
  disabled_public_groups?: number[] | null // 被禁用的公开分组 ID
  balance_notify_enabled: boolean
  balance_notify_threshold: number | null
  balance_notify_extra_emails: NotifyEmailEntry[]
  subscriptions?: UserSubscription[] // User's active subscriptions
  last_active_at?: string | null
  created_at: string
  updated_at: string
  deleted_at?: string | null
}

export interface AdminUser extends User {
  // 管理员备注（普通用户接口不返回）
  notes: string
  last_used_at?: string | null
  // 用户专属分组倍率配置 (group_id -> rate_multiplier)
  group_rates?: Record<number, number>
  // 当前并发数（仅管理员列表接口返回）
  current_concurrency?: number
}

export interface LoginRequest {
  email: string
  password: string
  turnstile_token?: string
  tencent_captcha_ticket?: string
  tencent_captcha_randstr?: string
}

export interface TencentCaptchaRequestProof {
  tencent_captcha_ticket: string
  tencent_captcha_randstr: string
}

// 动作触发式验证码（OAuth 启动、passkey 等入口）的请求凭据：
// 腾讯填 tencent_captcha_*，阿里云的 captchaVerifyParam 复用 turnstile_token 字段
export interface ActionCaptchaRequestProof extends Partial<TencentCaptchaRequestProof> {
  turnstile_token?: string
}

export interface RegisterRequest {
  email: string
  password: string
  verify_code?: string
  turnstile_token?: string
  tencent_captcha_ticket?: string
  tencent_captcha_randstr?: string
  promo_code?: string
  invitation_code?: string
  aff_code?: string
}

export interface AffiliateInvitee {
  user_id: number
  email: string
  username: string
  created_at?: string
  total_rebate: number
}

export interface UserAffiliateDetail {
  user_id: number
  aff_code: string
  inviter_id?: number | null
  aff_count: number
  aff_quota: number
  aff_frozen_quota: number
  aff_history_quota: number
  // 当前用户作为邀请人时实际生效的返利比例，专属比例优先于全局比例。
  effective_rebate_rate_percent: number
  invitees: AffiliateInvitee[]
}

export interface AffiliateTransferResponse {
  transferred_quota: number
  balance: number
}

export interface SendVerifyCodeRequest {
  email: string
  turnstile_token?: string
  tencent_captcha_ticket?: string
  tencent_captcha_randstr?: string
  pending_auth_token?: string
  pending_oauth_token?: string
}

export interface SendVerifyCodeResponse {
  message: string
  countdown: number
}

export interface CustomMenuItem {
  localization?: LocalizedUpdate<{ label: string; url: string }>
  id: string
  label: string
  icon_svg: string
  url: string
  page_slug?: string
  visibility: 'user' | 'admin'
  sort_order: number
}

export interface CustomEndpoint {
  id?: string
  localization?: LocalizedUpdate<{ name: string; description: string }>
  name: string
  endpoint: string
  description: string
}

export interface FooterLink {
  id?: string
  localization?: LocalizedUpdate<{ label: string; url: string }>
  label: string
  url: string
}

export interface FooterLinkGroup {
  id?: string
  localization?: LocalizedUpdate<string>
  title: string
  links: FooterLink[]
}

export interface LoginAgreementDocument {
  localization?: LocalizedUpdate<{ title: string; content_md: string }>
  id: string
  title: string
  content_md: string
}

export interface PublicSettings {
  locale?: string
  site_text_overrides?: string[]
  text_languages?: Record<string, { locale: string | null; fallback: boolean }>
  default_locale?: string
  site_title?: string
  registration_enabled: boolean
  email_verify_enabled: boolean
  force_email_on_third_party_signup: boolean
  registration_email_suffix_whitelist: string[]
  registration_email_domain_quota_enabled: boolean
  user_email_change_enabled: boolean // 是否允许已有邮箱的用户换绑主邮箱
  promo_code_enabled: boolean
  password_reset_enabled: boolean
  invitation_code_enabled: boolean
  affiliate_enabled: boolean
  login_agreement_enabled?: boolean
  login_agreement_mode?: 'modal' | 'checkbox' | string
  login_agreement_updated_at?: string
  login_agreement_revision?: string
  login_agreement_documents?: LoginAgreementDocument[]
  turnstile_enabled: boolean
  tencent_captcha_enabled?: boolean
  tencent_captcha_app_id?: string
  tencent_captcha_region?: string
  passkey_enabled?: boolean
  turnstile_site_key: string
  aliyun_captcha_enabled?: boolean
  aliyun_captcha_scene_id?: string
  aliyun_captcha_prefix?: string
  aliyun_captcha_region?: string
  site_name: string
  site_logo: string
  site_subtitle: string
  api_base_url: string
  contact_info: string
  doc_url: string
  home_content: string
  hide_ccs_import_button: boolean
  payment_enabled: boolean
  team_enabled?: boolean
  team_self_service_enabled?: boolean
  // 旧版公开设置可能缺少该字段，调用方应仅在明确为 false 时关闭创作台入口。
  creative_enabled?: boolean
  table_default_page_size: number
  table_page_size_options: number[]
  usage_ranking_limit: number
  // 旧版公开设置可能缺少以下字段，调用方应仅在明确为 false 时关闭入口或字段。
  usage_ranking_enabled?: boolean
  usage_ranking_sort_by?: 'total_tokens' | 'requests' | 'actual_cost'
  usage_ranking_show_total_tokens?: boolean
  usage_ranking_show_requests?: boolean
  usage_ranking_show_actual_cost?: boolean
  custom_menu_items: CustomMenuItem[]
  custom_endpoints: CustomEndpoint[]
  footer_links?: FooterLinkGroup[]
  footer_text?: string
  home_featured_models?: string[]
  linuxdo_oauth_enabled: boolean
  dingtalk_oauth_enabled?: boolean
  wechat_oauth_enabled: boolean
  wechat_oauth_open_enabled?: boolean
  wechat_oauth_mp_enabled?: boolean
  wechat_oauth_mobile_enabled?: boolean
  oidc_oauth_enabled: boolean
  oidc_oauth_provider_name: string
  github_oauth_enabled: boolean
  google_oauth_enabled: boolean
  // 旧版 HTML 注入缓存可能缺少 One Tap 字段，调用方必须按关闭处理。
  google_one_tap_enabled?: boolean
  google_oauth_client_id?: string
  backend_mode_enabled: boolean
  version: string
  // 服务器全局时区与当前 UTC 偏移；旧注入缓存可能缺失。
  server_timezone?: string
  server_utc_offset?: string
  balance_unit_name: string
  balance_unit_symbol: string
  balance_icon_svg: string
  balance_low_notify_enabled: boolean
  provider_quota_notify_enabled: boolean
  risk_control_enabled: boolean
  service_quota_enabled?: boolean
  balance_low_notify_threshold: number
  balance_low_notify_recharge_url?: string
  allow_user_view_error_requests?: boolean
}

export interface AuthResponse {
  access_token: string
  refresh_token?: string  // New: Refresh Token for token renewal
  expires_in?: number     // New: Access Token expiry time in seconds
  token_type: string
  user: User
}

export type CurrentUserResponse = User

// ==================== Announcement Types ====================

export type AnnouncementStatus = 'draft' | 'active' | 'archived'
export type AnnouncementNotifyMode = 'silent' | 'popup'

export type AnnouncementConditionType = 'subscription' | 'balance'

export type AnnouncementOperator = 'in' | 'gt' | 'gte' | 'lt' | 'lte' | 'eq'

export interface AnnouncementCondition {
  type: AnnouncementConditionType
  operator: AnnouncementOperator
  plan_ids?: number[]
  value?: number
}

export interface AnnouncementConditionGroup {
  all_of?: AnnouncementCondition[]
}

export interface AnnouncementTargeting {
  any_of?: AnnouncementConditionGroup[]
}

export interface Announcement {
  localization: LocalizedUpdate<{ title: string; content: string }>
  id: number
  title: string
  content: string
  status: AnnouncementStatus
  notify_mode: AnnouncementNotifyMode
  targeting: AnnouncementTargeting
  starts_at?: string
  ends_at?: string
  created_by?: number
  updated_by?: number
  created_at: string
  updated_at: string
}

export interface UserAnnouncement {
  id: number
  title: string
  content: string
  notify_mode: AnnouncementNotifyMode
  starts_at?: string
  ends_at?: string
  read_at?: string
  created_at: string
  updated_at: string
}

export interface CreateAnnouncementRequest {
  localization: LocalizedUpdate<{ title: string; content: string }>
  title: string
  content: string
  status?: AnnouncementStatus
  notify_mode?: AnnouncementNotifyMode
  targeting: AnnouncementTargeting
  starts_at?: number
  ends_at?: number
}

export interface UpdateAnnouncementRequest {
  localization?: LocalizedUpdate<{ title: string; content: string }>
  title?: string
  content?: string
  status?: AnnouncementStatus
  notify_mode?: AnnouncementNotifyMode
  targeting?: AnnouncementTargeting
  starts_at?: number
  ends_at?: number
}

export interface AnnouncementUserReadStatus {
  user_id: number
  email: string
  username: string
  balance: number
  eligible: boolean
  read_at?: string
}

// ==================== API Response Types ====================

export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data: T
}

export interface PaginatedResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

// ==================== UI State Types ====================

export type ToastType = 'success' | 'error' | 'info' | 'warning'

export interface Toast {
  id: string
  type: ToastType
  message: string
  title?: string
  duration?: number // in milliseconds, undefined means no auto-dismiss
}

// ==================== API Key & Group Types ====================

export type GroupSchedulerType = 'basic' | 'advanced'

// 分组高级调度器的稀疏覆盖；未出现的字段继承网关通用设置。
export interface GroupAdvancedSchedulerOverrides {
  sticky_weighted_enabled?: boolean
  subscription_priority_enabled?: boolean
  ewma_error_rate_alpha?: number
  ewma_ttft_alpha?: number
  sticky_escape_enabled?: boolean
  sticky_escape_ttft_ms?: number
  sticky_escape_error_rate?: number
  lb_top_k?: number
  weight_priority?: number
  weight_load?: number
  weight_queue?: number
  weight_error_rate?: number
  weight_ttft?: number
  weight_reset?: number
  weight_quota_headroom?: number
  weight_previous_response?: number
  weight_session_sticky?: number
}
// 提供商原生能力和分组客户端入口共用完整协议标识。
export type ProtocolID =
  | 'anthropic_messages'
  | 'openai_responses'
  | 'openai_chat_completions'
  | 'gemini_generate_content'
  | 'systemone'
  | 'openai_embeddings'
  | 'openai_images_generations'
  | 'openai_images_edits'
  | 'image_batches'
  | 'grok_videos_generations'
  | 'grok_videos_edits'
  | 'grok_videos_extensions'
  | 'grok_tts'
  | 'grok_stt'
  | 'grok_custom_voices'
  | 'grok_voice_realtime'
  | 'openai_responses_websocket'
  | 'openai_live'
  | 'openai_responses_compact'
  | 'openai_alpha_search'
  | 'grok_web_search'
  | 'grok_x_search'
  | 'qoder_chat'
  | 'gemini_batch_generate_content'
  | 'vertex_batch_prediction'
export type MarketplacePricingMode = 'token' | 'image' | 'unknown'
export type MarketplacePriceStatus = 'priced' | 'unpriced'

export interface MarketplacePricingInterval {
  min_tokens: number
  max_tokens?: number | null
  input_price_per_token?: number
  image_input_price_per_token?: number
  output_price_per_token?: number
  cache_write_price_per_token?: number
  cache_write_1h_price_per_token?: number
  cache_read_price_per_token?: number
  image_output_price_per_token?: number
  fast_input_price_per_token?: number
  fast_image_input_price_per_token?: number
  fast_output_price_per_token?: number
  fast_cache_write_price_per_token?: number
  fast_cache_write_1h_price_per_token?: number
  fast_cache_read_price_per_token?: number
  fast_image_output_price_per_token?: number
}

// MarketplaceTimePricing 是价格配置的分时倍率，时段之外按 1x 计费；结束时间 00:00 表示当天 24:00。
export interface MarketplaceTimePricing {
  timezone: string
  weekdays_only: boolean
  periods: Array<{
    start_time: string
    end_time: string
    multiplier: number
  }>
}

export interface MarketplaceModelPricing {
  pricing_mode: MarketplacePricingMode
  price_status: MarketplacePriceStatus
  input_price_per_token?: number
  image_input_price_per_token?: number
  output_price_per_token?: number
  cache_write_price_per_token?: number
  cache_write_1h_price_per_token?: number
  cache_read_price_per_token?: number
  image_output_price_per_token?: number
  fast_input_price_per_token?: number
  fast_image_input_price_per_token?: number
  fast_output_price_per_token?: number
  fast_cache_write_price_per_token?: number
  fast_cache_write_1h_price_per_token?: number
  fast_cache_read_price_per_token?: number
  fast_image_output_price_per_token?: number
  context_intervals?: MarketplacePricingInterval[]
  // 最终推理档位为 max 时乘到全部 token 单价上的倍率。
  max_reasoning_effort_multiplier?: number
  // 按请求时刻乘到 token 单价上的分时规则，上面的单价按 1x 计算。
  time_pricing?: MarketplaceTimePricing
  image_price_1k?: number
  image_price_2k?: number
  image_price_4k?: number
}

// 模型能力模态：模型广场接口从定价元数据下发，缺省时前端按模型 ID 规则兜底。
export type ModelModality = 'text' | 'image' | 'audio' | 'video' | 'pdf'

export interface MarketplaceModel {
  attributes?: ModelAttributes
  id: string
  display_name: string
  pricing: MarketplaceModelPricing
  input_modalities?: ModelModality[]
  output_modalities?: ModelModality[]
  // protocols 是该模型在分组里可以请求的客户端协议。
  protocols?: ProtocolID[]
  // native_protocols 是 protocols 里不经过协议转换的那部分。
  native_protocols?: ProtocolID[]
}

// 用户与市场接口可携带的分组容量快照，仅包含聚合后的负载数字。
export interface MarketplaceGroupCapacity {
  concurrency_used: number
  concurrency_max: number
  sessions_used: number
  sessions_max: number
  rpm_used: number
  rpm_max: number
}

export interface MarketplaceGroupAvailabilityDay {
  date: string
  success_count: number
  total_count: number
  availability_rate?: number | null
}

export interface MarketplaceGroupAvailability {
  window_days: number
  bucket_minutes?: number
  success_count: number
  total_count: number
  availability_rate?: number | null
  last_status?: string
  last_checked_at?: string | null
  days: MarketplaceGroupAvailabilityDay[]
}

export interface MarketplaceGroup {
  search_terms?: string[]
  id: number
  name: string
  description: string
  display_brand: string
  sort_order: number
  rate_multiplier: number
  official_price_ratio?: number
  official_price_rmb_equivalent?: number
  capacity?: MarketplaceGroupCapacity
  availability?: MarketplaceGroupAvailability
  model_count: number
  models: MarketplaceModel[]
}

export interface MarketplaceStats {
  today_tokens: number
  total_tokens: number
  total_users: number
}

export interface GroupAvailabilityProbeConfig {
  enabled: boolean
  interval_minutes?: number
  model_id?: string
  prompt?: string
  timeout_seconds?: number
  // 首次探测失败后允许重试的最大次数，缺失时由服务端使用默认值。
  max_retries?: number
  user_agent?: string
}
export type ReasoningEffortMatchType = 'exact' | 'prefix' | 'suffix'

export interface ReasoningEffortMapping {
  from: string
  to: string
  match_type?: ReasoningEffortMatchType
  model?: string
}

export interface Group {
  search_terms?: string[]
  display_name?: string
  // 后端按组内提供商能力解析的可请求模型。
  models?: string[]
  model_attributes?: Record<string, ModelAttributes>
  model_protocols?: Record<string, ProtocolID[]>
  id: number
  name: string
  description: string | null
  display_brand?: string
  rate_multiplier: number
  capacity?: MarketplaceGroupCapacity
  rpm_limit?: number // 分组级 RPM 上限（0 表示不限制），设置后覆盖用户级 rpm_limit 兜底值
  max_reasoning_effort?: string // OpenAI/Codex 推理强度上限，空字符串表示不限制
  max_reasoning_effort_over_limit?: string // 超过上限时 downgrade 或 deny
  reasoning_effort_mappings?: ReasoningEffortMapping[]
  is_exclusive: boolean
  session_isolation_enabled: boolean
  status: 'active' | 'inactive'

  // 图片生成权限
  allow_image_generation: boolean
  allow_batch_image_generation: boolean

  // Claude Code 客户端限制
  claude_code_only: boolean
  fallback_group_id: number | null
  fallback_group_id_on_invalid_request: number | null
  unavailable_fallback_group_id: number | null
  // 分组允许客户端使用的文本生成协议，顺序由服务端固定。
  protocol_fallbacks?: Partial<Record<ProtocolID, ProtocolID[]>>
  responses_image_policy?: 'inherit' | 'enabled' | 'disabled' | 'block'
  allowed_protocols: ProtocolID[]
  // OpenAI Messages 调度开关（弃用兼容字段，新代码读取 allowed_protocols）
  allow_messages_dispatch?: boolean
  // OpenAI Live 接口开关
  allow_live: boolean
  default_mapped_model?: string
  availability_probe_config?: GroupAvailabilityProbeConfig
  require_oauth_only: boolean
  require_privacy_set: boolean
  created_at: string
  updated_at: string
}

// 分组加速策略，新字段优先于兼容布尔开关。
export type GroupOpenAIFastPolicy = "follow_request" | "force_priority" | "force_ultrafast" | "force_off"

export interface GroupRoutingPolicy {
  /** 兼容历史停用策略，管理表单保存时固定为 true。 */
  enabled: boolean
  model_mapping: Record<string, string>
  restrict_models: boolean
  restriction_model_source: 'requested' | 'group_mapped' | 'upstream'
  allowed_models: string[]
  features: string
  features_config: Record<string, unknown>
}

export interface AdminGroup extends Group {
  localization?: LocalizedUpdate<{ display_name: string; description: string }>
  routing_policy: GroupRoutingPolicy
  // 该策略由管理端配置，在管理员分组接口中返回。
  force_openai_fast?: boolean
  openai_fast_policy?: GroupOpenAIFastPolicy
  // 该计费策略由管理端配置，在管理员分组接口中返回。

  // 调度器模式由管理端配置，在管理员分组接口中返回。
  scheduler_type: GroupSchedulerType
  advanced_scheduler_overrides?: GroupAdvancedSchedulerOverrides

  // 模型路由配置（仅管理员可见，内部信息）
  model_routing: Record<string, number[]> | null
  model_routing_enabled: boolean

  // MCP XML 协议注入（仅 antigravity 平台使用）
  mcp_xml_inject: boolean

  // 支持的模型系列（仅 antigravity 平台使用）
  supported_model_scopes?: string[]

  // 分组下提供商数量（仅管理员可见）
  provider_count?: number
  active_provider_count?: number
  rate_limited_provider_count?: number

  // OpenAI Messages 调度配置（仅 openai 平台使用）
  default_mapped_model?: string
  models_list_config?: ModelsListConfig

  // 分组排序
  sort_order: number
}

export interface ModelsListConfig {
  enabled: boolean
  models: string[]
}

// 单个 API Key 的 Fast 模式策略，系统级策略拥有更高优先级。
export type ApiKeyFastModePolicy = 'follow_request' | 'force_on' | 'force_off'

// API Key 的资金来源策略；auto 保持订阅优先、余额兜底的历史行为。
export type ApiKeyBillingMode = 'auto' | 'subscription' | 'balance'

// 供 API Key 配置页选择指定订阅的安全摘要。
export interface ApiKeyBillingSubscriptionOption {
  id: number
  plan_id: number
  plan_name: string
  expires_at: string
  groups_restricted: boolean
  applicable_groups: number[]
}

// ApiKeyCompositeGroup 表示一个复合 Key 的分组前缀映射。
export interface ApiKeyCompositeGroup {
  group_id: number
  prefix: string
  group?: Group
}

export interface ApiKey {
  id: number
  user_id: number
  team_id?: number | null
  scope?: 'personal' | 'team'
  team_owner_disabled?: boolean // 团队管理员锁定后，成员不能自行恢复该 Key。
  key: string
  name: string
  group_id: number | null
  is_composite?: boolean
  composite_groups?: ApiKeyCompositeGroup[]
  status: 'active' | 'inactive' | 'disabled' | 'quota_exhausted' | 'expired'
  fast_mode_policy: ApiKeyFastModePolicy
  billing_mode?: ApiKeyBillingMode
  preferred_subscription_id?: number | null
  model_mapping: Record<string, string>
  ip_whitelist: string[]
  ip_blacklist: string[]
  last_used_at: string | null
  last_used_ip: string | null // 最近一条带 IP 的用量日志。
  quota: number // Quota limit in USD (0 = unlimited)
  quota_used: number // Used quota amount in USD
  expires_at: string | null // Expiration time (null = never expires)
  created_at: string
  updated_at: string
  current_concurrency: number
  group?: Group
  // Key 请求上限，0 表示不限制。
  concurrency_limit?: number
  rpm_limit?: number
  rate_limit_5h: number
  rate_limit_1d: number
  rate_limit_7d: number
  usage_5h: number
  usage_1d: number
  usage_7d: number
  window_5h_start: string | null
  window_1d_start: string | null
  window_7d_start: string | null
  reset_5h_at: string | null
  reset_1d_at: string | null
  reset_7d_at: string | null
  fallback_when_group_unavailable?: boolean
}

export interface CreateApiKeyRequest {
  name: string
  scope?: 'personal' | 'team'
  group_id?: number | null
  is_composite?: boolean
  composite_groups?: Array<{ group_id: number; prefix: string }>
  fast_mode_policy?: ApiKeyFastModePolicy
  billing_mode?: ApiKeyBillingMode
  preferred_subscription_id?: number | null
  model_mapping?: Record<string, string>
  custom_key?: string // Optional custom API Key
  ip_whitelist?: string[]
  ip_blacklist?: string[]
  quota?: number // Quota limit in USD (0 = unlimited)
  expires_in_days?: number // Days until expiry (null = never expires)
  concurrency_limit?: number
  rpm_limit?: number
  rate_limit_5h?: number
  rate_limit_1d?: number
  rate_limit_7d?: number
  fallback_when_group_unavailable?: boolean
}

export interface UpdateApiKeyRequest {
  name?: string
  group_id?: number | null
  is_composite?: boolean
  composite_groups?: Array<{ group_id: number; prefix: string }>
  status?: 'active' | 'inactive'
  fast_mode_policy?: ApiKeyFastModePolicy
  billing_mode?: ApiKeyBillingMode
  preferred_subscription_id?: number | null
  model_mapping?: Record<string, string>
  ip_whitelist?: string[]
  ip_blacklist?: string[]
  quota?: number // Quota limit in USD (null = no change, 0 = unlimited)
  expires_at?: string | null // Expiration time (null = no change)
  reset_quota?: boolean // Reset quota_used to 0
  concurrency_limit?: number
  rpm_limit?: number
  rate_limit_5h?: number
  rate_limit_1d?: number
  rate_limit_7d?: number
  reset_rate_limit_usage?: boolean
  fallback_when_group_unavailable?: boolean
}

export interface CreateGroupRequest {
  localization?: LocalizedUpdate<{ display_name: string; description: string }>
  name: string
  description?: string | null
  scheduler_type?: GroupSchedulerType
  advanced_scheduler_overrides?: GroupAdvancedSchedulerOverrides
  display_brand?: string
  sort_order?: number
  rate_multiplier?: number
  is_exclusive?: boolean
  session_isolation_enabled?: boolean

  force_openai_fast?: boolean
  openai_fast_policy?: GroupOpenAIFastPolicy

  routing_policy?: GroupRoutingPolicy

  allow_image_generation?: boolean
  allow_batch_image_generation?: boolean

  claude_code_only?: boolean
  fallback_group_id?: number | null
  fallback_group_id_on_invalid_request?: number | null
  unavailable_fallback_group_id?: number | null
  mcp_xml_inject?: boolean
  supported_model_scopes?: string[]
  models_list_config?: ModelsListConfig
  availability_probe_config?: GroupAvailabilityProbeConfig
  protocol_fallbacks?: Partial<Record<ProtocolID, ProtocolID[]>>
  responses_image_policy?: 'inherit' | 'enabled' | 'disabled' | 'block'
  allowed_protocols?: ProtocolID[]
  allow_messages_dispatch?: boolean
  allow_live?: boolean
  default_mapped_model?: string
  model_routing?: Record<string, number[]> | null
  model_routing_enabled?: boolean
  rpm_limit?: number
  max_reasoning_effort?: string
  max_reasoning_effort_over_limit?: string
  reasoning_effort_mappings?: ReasoningEffortMapping[]
  require_oauth_only?: boolean
  require_privacy_set?: boolean
  // 从指定分组复制提供商
  copy_providers_from_group_ids?: number[]
}

export interface UpdateGroupRequest {
  localization?: LocalizedUpdate<{ display_name: string; description: string }>
  name?: string
  description?: string | null
  scheduler_type?: GroupSchedulerType
  advanced_scheduler_overrides?: GroupAdvancedSchedulerOverrides
  display_brand?: string
  sort_order?: number
  rate_multiplier?: number
  is_exclusive?: boolean
  session_isolation_enabled?: boolean
  status?: 'active' | 'inactive'

  force_openai_fast?: boolean
  openai_fast_policy?: GroupOpenAIFastPolicy

  routing_policy?: GroupRoutingPolicy

  allow_image_generation?: boolean
  allow_batch_image_generation?: boolean

  claude_code_only?: boolean
  fallback_group_id?: number | null
  fallback_group_id_on_invalid_request?: number | null
  unavailable_fallback_group_id?: number | null
  mcp_xml_inject?: boolean
  supported_model_scopes?: string[]
  models_list_config?: ModelsListConfig
  availability_probe_config?: GroupAvailabilityProbeConfig
  protocol_fallbacks?: Partial<Record<ProtocolID, ProtocolID[]>>
  responses_image_policy?: 'inherit' | 'enabled' | 'disabled' | 'block'
  allowed_protocols?: ProtocolID[]
  allow_messages_dispatch?: boolean
  allow_live?: boolean
  default_mapped_model?: string
  model_routing?: Record<string, number[]> | null
  model_routing_enabled?: boolean
  rpm_limit?: number
  max_reasoning_effort?: string
  max_reasoning_effort_over_limit?: string
  reasoning_effort_mappings?: ReasoningEffortMapping[]
  require_oauth_only?: boolean
  require_privacy_set?: boolean
  copy_providers_from_group_ids?: number[]
}

// ==================== Provider & Proxy Types ====================

export type ProviderPlatform =
  | 'anthropic'
  | 'openai'
  | 'gemini'
  | 'antigravity'
  | 'qoder'
  | 'grok'
  | 'kimi'
  | 'zhipu'
  | 'deepseek'
  | 'jev'
export type ProviderType = 'oauth' | 'setup-token' | 'apikey' | 'upstream' | 'bedrock' | 'service_account' | 'cosy'
export type ProxyProtocol = 'http' | 'https' | 'socks5' | 'socks5h'

// Claude Model type (returned by /v1/models and provider models API)
export interface ClaudeModel {
  id: string
  type: string
  display_name: string
  created_at: string
}

export interface Proxy {
  id: number
  name: string
  protocol: ProxyProtocol
  host: string
  port: number
  username: string | null
  password?: string | null
  status: 'active' | 'inactive' | 'expired'
  provider_count?: number // Number of providers using this proxy
  latency_ms?: number
  latency_status?: 'success' | 'failed'
  latency_message?: string
  ip_address?: string
  country?: string
  country_code?: string
  region?: string
  city?: string
  quality_status?: 'healthy' | 'warn' | 'challenge' | 'failed'
  quality_score?: number
  quality_grade?: string
  quality_summary?: string
  quality_checked?: number
  expires_at: string | null
  fallback_mode: 'none' | 'proxy' | 'direct'
  backup_proxy_id?: number | null
  expiry_warn_days: number
  created_at: string
  updated_at: string
}

export interface ProxyProviderSummary {
  id: number
  name: string
  platform: ProviderPlatform
  type: ProviderType
  notes?: string | null
}

export interface ProxyQualityCheckItem {
  target: string
  status: 'pass' | 'warn' | 'fail' | 'challenge'
  http_status?: number
  latency_ms?: number
  message?: string
  cf_ray?: string
}

export interface ProxyQualityCheckResult {
  proxy_id: number
  score: number
  grade: string
  summary: string
  exit_ip?: string
  country?: string
  country_code?: string
  base_latency_ms?: number
  passed_count: number
  warn_count: number
  failed_count: number
  challenge_count: number
  checked_at: number
  items: ProxyQualityCheckItem[]
}

// Gemini credentials structure for OAuth and API Key authentication
export interface GeminiCredentials {
  // API Key authentication
  api_key?: string
  // Gemini API Key 的接入来源；缺失值兼容历史官方 AI Studio 提供商。
  provider_type?: 'official' | 'third_party'

  // OAuth authentication
  access_token?: string
  refresh_token?: string
  oauth_type?: 'code_assist' | 'google_one' | 'ai_studio' | string
  tier_id?:
    | 'google_one_free'
    | 'google_ai_pro'
    | 'google_ai_ultra'
    | 'gcp_standard'
    | 'gcp_enterprise'
    | 'aistudio_free'
    | 'aistudio_paid'
    | 'LEGACY'
    | 'PRO'
    | 'ULTRA'
    | string
  project_id?: string
  token_type?: string
  scope?: string
  expires_at?: string
  model_mapping?: Record<string, string>
}

export interface TempUnschedulableState {
  until_unix: number
  triggered_at_unix: number
  status_code: number
  matched_keyword: string
  rule_index: number
  error_message: string
  trigger_count?: number
  trigger_threshold?: number
  trigger_window_minutes?: number
}

export interface TempUnschedulableStatus {
  active: boolean
  state?: TempUnschedulableState
}

export type OllamaCloudUsageStatus = 'ok' | 'unauthorized' | 'failed'

export interface OllamaCloudUsageWindow {
  used_percent: number
  reset_at?: string
  reset_text?: string
}

export interface OllamaCloudUsageModel {
  model: string
  window: 'five_hour' | 'seven_day'
  requests: number
}

export interface OllamaCloudUsageData {
  plan?: string
  five_hour?: OllamaCloudUsageWindow
  seven_day?: OllamaCloudUsageWindow
  balance?: string
  models?: OllamaCloudUsageModel[]
}

export interface OllamaCloudUsageSnapshot {
  status: OllamaCloudUsageStatus
  data?: OllamaCloudUsageData
  fetched_at?: string
  last_attempt_at: string
  next_refresh_at: string
  failure_count?: number
  http_status?: number
  last_error?: string
}

export interface OllamaCloudUsageState {
  provider_id: number
  eligible: boolean
  configured: boolean
  auto_refresh_enabled: boolean
  encryption_key_configured: boolean
  snapshot?: OllamaCloudUsageSnapshot
}

export interface OllamaCloudUsageSettings {
  enabled: boolean
  /** 模型请求持续到达时的最大等待时间（分钟）。 */
  interval_minutes: number
  /** 最近一次模型请求后的尾随静默期（分钟）。 */
  debounce_minutes: number
}

export interface Provider {
  id: number
  name: string
  notes?: string | null
  platform: ProviderPlatform
  type: ProviderType
  // 后端响应里 credentials 已脱敏：access_token / refresh_token / id_token /
  // api_key / session_key / cookie / aws_secret_access_key / aws_session_token /
  // service_account_json / service_account / private_key /
  // new_api_user_access_token 不会出现，
  // 改为通过 credentials_status.has_<key> 暴露存在性。
  credentials?: Record<string, unknown>
  credentials_status?: Record<string, boolean>
  ollama_cloud_usage?: OllamaCloudUsageState
  // Extra 包含 Codex 用量、OpenAI 文本协议、两类 Compact 能力和模型级限流状态等扩展字段。
  extra?: (CodexUsageSnapshot & OpenAITextProtocolState & OpenAICompactState & OpenAINativeCompactionV2State & {
    model_rate_limits?: Record<string, { rate_limited_at: string; rate_limit_reset_at: string }>
    antigravity_credits_overages?: Record<string, { activated_at: string; active_until: string }>
    codex_reset_credit_snapshot?: {
      available_count?: number
      credits?: { expires_at?: string }[]
    }
  } & Record<string, unknown>)
  proxy_id: number | null
  proxy_fallback_origin_id?: number | null
  proxy_fallback_origin_name?: string | null
  concurrency: number
  load_factor?: number | null
  current_concurrency?: number // Real-time concurrency count from Redis
  scheduler_score?: {
    base_score: number
    sticky_score?: number
    sticky_score_infinity?: boolean
    sticky_weighted_enabled: boolean
  } | null
  scheduler_scores?: ProviderSchedulerGroupScore[] | null
  priority: number
  rate_multiplier?: number // Provider billing multiplier (>=0, 0 means free)
  status: 'active' | 'inactive' | 'error'
  error_message: string | null
  last_used_at: string | null
  expires_at: number | null
  auto_pause_on_expired: boolean
  created_at: string
  updated_at: string
  proxy?: Proxy
  group_ids?: number[] // Groups this provider belongs to
  groups?: Group[] // Preloaded group objects

  // Rate limit & scheduling fields
  schedulable: boolean
  rate_limited_at: string | null
  rate_limit_reset_at: string | null
  overload_until: string | null
  temp_unschedulable_until: string | null
  temp_unschedulable_reason: string | null
  quota_auto_paused?: boolean

  // Session window fields (5-hour window)
  session_window_start: string | null
  session_window_end: string | null
  session_window_status: 'allowed' | 'allowed_warning' | 'rejected' | null

  // 5h窗口费用控制（仅 Anthropic OAuth/SetupToken 提供商有效）
  window_cost_limit?: number | null
  window_cost_sticky_reserve?: number | null

  // 会话数量控制（仅 Anthropic OAuth/SetupToken 提供商有效）
  max_sessions?: number | null
  session_idle_timeout_minutes?: number | null

  // RPM 限制（仅 Anthropic OAuth/SetupToken 提供商有效）
  base_rpm?: number | null
  rpm_strategy?: string | null
  rpm_sticky_buffer?: number | null
  user_msg_queue_mode?: string | null  // "serialize" | "throttle" | null

  // TLS指纹伪装（仅 Anthropic OAuth/SetupToken 与 OpenAI OAuth 提供商有效）
  enable_tls_fingerprint?: boolean | null
  tls_fingerprint_profile_id?: number | null
  tls_fingerprint_router_id?: number | null

  // OpenAI OAuth 客户端访问策略
  openai_oauth_client_policy?: OpenAIOAuthClientPolicy | null

  // 会话ID伪装（仅 Anthropic OAuth/SetupToken 提供商有效）
  // 启用后将在15分钟内固定 metadata.user_id 中的 session ID
  session_id_masking_enabled?: boolean | null

  // 缓存 TTL 强制替换（仅 Anthropic OAuth/SetupToken 提供商有效）
  cache_ttl_override_enabled?: boolean | null
  cache_ttl_override_target?: string | null

  // 自定义 Base URL 中继转发（仅 Anthropic OAuth/SetupToken 提供商有效）
  custom_base_url_enabled?: boolean | null
  custom_base_url?: string | null

  // API Key 提供商配额限制
  quota_limit?: number | null
  quota_used?: number | null
  quota_daily_limit?: number | null
  quota_daily_used?: number | null
  quota_weekly_limit?: number | null
  quota_weekly_used?: number | null

  // 配额固定时间重置配置
  quota_daily_reset_mode?: 'rolling' | 'fixed' | null
  quota_daily_reset_hour?: number | null
  quota_weekly_reset_mode?: 'rolling' | 'fixed' | null
  quota_weekly_reset_day?: number | null
  quota_weekly_reset_hour?: number | null
  quota_reset_timezone?: string | null
  quota_daily_reset_at?: string | null
  quota_weekly_reset_at?: string | null

  // 运行时状态（仅当启用对应限制时返回）
  current_window_cost?: number | null // 当前窗口费用
  active_sessions?: number | null // 当前活跃会话数
  current_rpm?: number | null // 当前分钟 RPM 计数

  // 影子提供商关系（spark 维度影子）
  parent_provider_id?: number | null
  quota_dimension?: string
  // 影子提供商回填的母提供商信息（仅影子非空）
  parent_email?: string
  parent_plan_type?: string
  parent_privacy_mode?: string
  parent_subscription_expires_at?: string
  parent_chatgpt_account_id?: string
}

export interface ProviderSchedulerGroupScore {
  group_id?: number | null
  group_name?: string
  base_score: number
  sticky_score?: number
  sticky_score_infinity?: boolean
  sticky_weighted_enabled: boolean
}

// Provider Usage types
export interface WindowStats {
  requests: number
  tokens: number
  cost: number // Provider cost (provider multiplier)
  standard_cost?: number
  user_cost?: number
}

export interface UsageProgress {
  utilization: number // Percentage (0-100+, 100 = 100%)
  resets_at: string | null
  remaining_seconds: number
  window_stats?: WindowStats | null // 窗口期统计（从窗口开始到当前的使用量）
  used_requests?: number
  limit_requests?: number
}

// Antigravity 单个模型的配额信息
export interface AntigravityModelQuota {
  utilization: number // 使用率 0-100
  reset_time: string  // 重置时间 ISO8601
}

export interface GrokQuotaWindow {
  limit?: number | null
  remaining?: number | null
  reset_unix?: number | null
  reset_at?: string | null
}

export interface GrokBillingProductUsage {
  product: string
  usage_percent?: number | null
}

export interface GrokBillingSummary {
  period_type?: string
  usage_percent?: number | null
  period_start?: string
  period_end?: string
  product_usage?: GrokBillingProductUsage[]
  monthly_limit_cents?: number | null
  used_cents?: number | null
  included_used_cents?: number | null
  billing_period_start?: string
  billing_period_end?: string
  used_percent?: number | null
  /** 账单探测返回的美元绝对金额。 */
  prepaid_balance?: number | null
  monthly_limit?: number | null
  monthly_used?: number | null
  on_demand_cap?: number | null
  on_demand_used?: number | null
  top_up_method?: string
  is_unified_billing_user?: boolean
  plan?: string
  status_code?: number
  source?: string
  fetched_at?: string
  updated_at?: string
  weekly_updated_at?: string
  monthly_updated_at?: string
  partial?: boolean
  failed_windows?: string[]
}

export interface ProviderUsageInfo {
  source?: 'passive' | 'active'
  updated_at: string | null
  five_hour: UsageProgress | null
  seven_day: UsageProgress | null
  seven_day_sonnet: UsageProgress | null
  seven_day_fable?: UsageProgress | null
  thirty_day?: UsageProgress | null
  gemini_shared_daily?: UsageProgress | null
  gemini_pro_daily?: UsageProgress | null
  gemini_flash_daily?: UsageProgress | null
  gemini_shared_minute?: UsageProgress | null
  gemini_pro_minute?: UsageProgress | null
  gemini_flash_minute?: UsageProgress | null
  quota_auto_paused?: boolean
  antigravity_quota?: Record<string, AntigravityModelQuota> | null
  grok_request_quota?: GrokQuotaWindow | null
  grok_token_quota?: GrokQuotaWindow | null
  grok_retry_after_seconds?: number | null
  grok_entitlement_status?: string
  grok_quota_snapshot_state?: string
  grok_last_quota_probe_at?: string
  grok_last_headers_seen_at?: string
  grok_last_status_code?: number
  grok_free_token_limit?: number
  grok_local_usage?: WindowStats | null
  grok_local_usage_24h?: WindowStats | null
  grok_local_usage_7d?: WindowStats | null
  grok_local_usage_monthly?: WindowStats | null
  grok_billing?: GrokBillingSummary | null
  subscription_tier?: string
  subscription_tier_raw?: string
  ai_credits?: Array<{
    credit_type?: string
    amount?: number
    minimum_balance?: number
  }> | null
  qoder_quota?: {
    user_type?: string
    usage_type?: string
    total_usage_percentage?: number
    is_quota_exceeded?: boolean
    expires_at?: string | null
    upgrade_url?: string
    user_quota?: {
      total?: number
      used?: number
      remaining?: number
      percentage?: number
      unit?: string
      detail_url?: string
      cap?: number
      available?: boolean
    } | null
    add_on_quota?: {
      total?: number
      used?: number
      remaining?: number
      percentage?: number
      unit?: string
      detail_url?: string
      cap?: number
      available?: boolean
    } | null
    org_resource_package?: {
      total?: number
      used?: number
      remaining?: number
      percentage?: number
      unit?: string
      detail_url?: string
      cap?: number
      available?: boolean
    } | null
    is_plan_quota_prorated?: boolean
    last_updated_at?: string | null
    snapshot_from_provider?: boolean
  } | null
  // Antigravity 403 forbidden 状态
  is_forbidden?: boolean
  forbidden_reason?: string
  forbidden_type?: string   // "validation" | "violation" | "forbidden"
  validation_url?: string   // 验证/申诉链接

  // 状态标记（后端自动推导）
  needs_verify?: boolean    // 需要人工验证（forbidden_type=validation）
  is_banned?: boolean       // 提供商被封（forbidden_type=violation）
  needs_reauth?: boolean    // token 失效需重新授权（401）

  // 机器可读错误码：forbidden / unauthenticated / rate_limited / network_error
  error_code?: string

  error?: string            // usage 获取失败时的错误信息
}

// API Key 提供商的上游用量查询协议配置与归一化结果。
export type UpstreamUsageAdapter =
  | 'sub2api'
  | 'new_api'
  | 'zivv'
  | 'kimi_balance'
  | 'kimi_coding'
  | 'zhipu_coding'
  | 'deepseek_balance'

export interface UpstreamUsageAmount {
  used?: number
  total?: number
  remaining?: number
}

export interface UpstreamUsageBalanceEntry {
  currency: string
  remaining: number
}

export interface UpstreamUsageLimit {
  name: string
  used?: number
  limit?: number
  remaining?: number
  reset_at?: string | null
}

export interface UpstreamUsageSubscription {
  plan_name: string
  unlimited?: boolean
  remaining?: number
  expires_at?: string | null
  limits?: UpstreamUsageLimit[]
}

export interface UpstreamUsageInfo {
  provider: string
  mode: 'balance' | 'quota' | 'limits' | 'subscription' | string
  unit?: string
  /** New API/Zivv 的 balance 表示用户钱包；Key quota 通过 limits/subscription 表示。 */
  balance?: UpstreamUsageAmount
  balances?: UpstreamUsageBalanceEntry[]
  available?: boolean
  limits?: UpstreamUsageLimit[]
  subscription?: UpstreamUsageSubscription
  expires_at?: string | null
}

export interface UpstreamUsageQueryResult {
  provider_id: number
  adapter: UpstreamUsageAdapter | string
  observed_at: string
  provider?: string
  mode?: string
  unit?: string
  balance?: UpstreamUsageAmount
  balances?: UpstreamUsageBalanceEntry[]
  available?: boolean
  limits?: UpstreamUsageLimit[]
  subscription?: UpstreamUsageSubscription
  expires_at?: string | null
  /** 兼容旧缓存读取；新接口响应使用上面的扁平字段。 */
  usage?: UpstreamUsageInfo
}

export interface UpstreamUsageQueryError {
  code?: string
  message?: string
  status?: number
}

export interface BatchUpstreamUsageResponse {
  usage: Record<string, UpstreamUsageQueryResult>
  errors: Record<string, UpstreamUsageQueryError>
}

// OpenAI Codex usage snapshot (from response headers)
export interface CodexUsageSnapshot {
  // Legacy fields (kept for backwards compatibility)
  // NOTE: The naming is ambiguous - actual window type is determined by window_minutes value
  codex_primary_used_percent?: number // Usage percentage (check window_minutes for actual window type)
  codex_primary_reset_after_seconds?: number // Seconds until reset
  codex_primary_window_minutes?: number // Window in minutes
  codex_secondary_used_percent?: number // Usage percentage (check window_minutes for actual window type)
  codex_secondary_reset_after_seconds?: number // Seconds until reset
  codex_secondary_window_minutes?: number // Window in minutes
  codex_primary_over_secondary_percent?: number // Overflow ratio

  // Canonical fields (normalized by backend, use these preferentially)
  codex_5h_used_percent?: number // 5-hour window usage percentage
  codex_5h_reset_after_seconds?: number // Seconds until 5h window reset
  codex_5h_reset_at?: string // 5-hour window absolute reset time (RFC3339)
  codex_5h_window_minutes?: number // 5h window in minutes (should be ~300)
  codex_7d_used_percent?: number // 7-day window usage percentage
  codex_7d_reset_after_seconds?: number // Seconds until 7d window reset
  codex_7d_reset_at?: string // 7-day window absolute reset time (RFC3339)
  codex_7d_window_minutes?: number // 7d window in minutes (should be ~10080)

  codex_usage_updated_at?: string // Last update timestamp
}

export type OpenAICompactMode = 'force_on' | 'force_off'
export type OpenAIOAuthClientPolicy = 'any' | 'codex_only' | 'tls_router_matched_only'
export type OpenAITextRouteMode =
  | 'preserve_client_protocol'
  | 'force_responses'
  | 'force_chat_completions'

export interface OpenAICompactState {
  openai_compact_mode?: OpenAICompactMode
}

export interface OpenAINativeCompactionV2State {
  openai_native_compaction_v2_mode?: OpenAICompactMode
}

export interface OpenAITextProtocolState {
  openai_text_route_mode?: OpenAITextRouteMode
  openai_responses_continuation_supported?: boolean
}

export interface CreateProviderRequest {
  name: string
  notes?: string | null
  platform: ProviderPlatform
  type: ProviderType
  credentials: Record<string, unknown>
  extra?: Record<string, unknown>
  proxy_id?: number | null
  concurrency?: number
  load_factor?: number | null
  priority?: number
  rate_multiplier?: number // Provider billing multiplier (>=0, 0 means free)
  group_ids?: number[]
  expires_at?: number | null
  auto_pause_on_expired?: boolean
}

export interface UpdateProviderRequest {
  name?: string
  notes?: string | null
  type?: ProviderType
  credentials?: Record<string, unknown>
  extra?: Record<string, unknown>
  proxy_id?: number | null
  concurrency?: number
  load_factor?: number | null
  priority?: number
  rate_multiplier?: number // Provider billing multiplier (>=0, 0 means free)
  schedulable?: boolean
  status?: 'active' | 'inactive' | 'error'
  group_ids?: number[]
  expires_at?: number | null
  auto_pause_on_expired?: boolean
}

export interface CreateProxyRequest {
  name: string
  protocol: ProxyProtocol
  host: string
  port: number
  username?: string | null
  password?: string | null
  expires_at?: number | null   // unix 秒；null/0 = 永不过期
  fallback_mode?: 'none' | 'proxy' | 'direct'
  backup_proxy_id?: number | null
  expiry_warn_days?: number
}

export interface UpdateProxyRequest {
  name?: string
  protocol?: ProxyProtocol
  host?: string
  port?: number
  username?: string | null
  password?: string | null
  status?: 'active' | 'inactive'
  expires_at?: number | null   // unix 秒；null/0 = 永不过期
  fallback_mode?: 'none' | 'proxy' | 'direct'
  backup_proxy_id?: number | null
  expiry_warn_days?: number
}

export interface AdminDataPayload {
  type?: string
  version?: number
  exported_at: string
  proxies: AdminDataProxy[]
  providers: AdminDataProvider[]
  // 导出时被排除的 spark 影子提供商数量(影子不持凭据、其调度配置不在备份范围)。
  skipped_shadows?: number
}

export interface AdminDataProxy {
  proxy_key: string
  name: string
  protocol: ProxyProtocol
  host: string
  port: number
  username?: string | null
  password?: string | null
  status: 'active' | 'inactive'
}

export interface AdminDataProvider {
  name: string
  notes?: string | null
  platform: ProviderPlatform
  type: ProviderType
  credentials: Record<string, unknown>
  extra?: Record<string, unknown>
  proxy_key?: string | null
  concurrency: number
  priority: number
  rate_multiplier?: number | null
  expires_at?: number | null
  auto_pause_on_expired?: boolean
}

export interface AdminDataImportError {
  kind: 'proxy' | 'provider'
  name?: string
  proxy_key?: string
  message: string
}

export interface AdminDataImportResult {
  proxy_created: number
  proxy_reused: number
  proxy_failed: number
  provider_created: number
  provider_failed: number
  errors?: AdminDataImportError[]
}

export interface CodexSessionImportRequest {
  content?: string
  contents?: string[]
  name?: string
  notes?: string | null
  group_ids?: number[]
  proxy_id?: number | null
  concurrency?: number
  priority?: number
  rate_multiplier?: number
  load_factor?: number | null
  expires_at?: number | null
  auto_pause_on_expired?: boolean
  credential_extras?: Record<string, unknown>
  extra?: Record<string, unknown>
  update_existing?: boolean
}

export interface OpenAICodexPATCreateRequest {
  access_token: string
  name?: string
  notes?: string | null
  group_ids?: number[]
  proxy_id?: number | null
  concurrency?: number
  priority?: number
  rate_multiplier?: number
  load_factor?: number | null
  expires_at?: number | null
  auto_pause_on_expired?: boolean
  credential_extras?: Record<string, unknown>
  extra?: Record<string, unknown>
}

export interface CodexSessionImportMessage {
  index: number
  name?: string
  message: string
}

export interface CodexSessionImportItem {
  index: number
  name?: string
  action: 'created' | 'updated' | 'skipped' | 'failed'
  provider_id?: number
  message?: string
}

export interface CodexSessionImportResult {
  total: number
  created: number
  updated: number
  skipped: number
  failed: number
  items?: CodexSessionImportItem[]
  warnings?: CodexSessionImportMessage[]
  errors?: CodexSessionImportMessage[]
}

// ==================== Usage & Redeem Types ====================

export type RedeemCodeType =
  | 'balance'
  | 'admin_balance'
  | 'concurrency'
  | 'admin_concurrency'
  | 'subscription'
  | 'invitation'
  | 'affiliate_balance'
export type UsageRequestType = 'unknown' | 'sync' | 'stream' | 'ws_v2' | 'cyber' | 'live'
export type ImageSizeSource = 'output' | 'input' | 'default' | 'legacy'
export type ImageSizeBreakdown = Record<string, number>

export interface UsageLog {
  // 实际执行提供商的平台快照，历史记录由迁移固化。
  platform: string
  id: number
  user_id: number
  team_id?: number | null
  api_key_id: number
  provider_id: number | null
  request_id: string
  model: string
  service_tier?: string | null
  reasoning_effort?: string | null
  requested_reasoning_effort?: string | null
  inbound_endpoint?: string | null
  upstream_endpoint?: string | null

  group_id: number | null
  subscription_id: number | null

  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  cache_creation_5m_tokens: number
  cache_creation_1h_tokens: number

  input_cost: number
  output_cost: number
  cache_creation_cost: number
  cache_read_cost: number
  total_cost: number
  actual_cost: number
  rate_multiplier: number
  long_context_billing_applied: boolean
  billing_type: number

  request_type?: UsageRequestType
  stream: boolean
  openai_ws_mode?: boolean
  native_compaction_v2?: boolean
  duration_ms: number | null
  first_token_ms: number | null

  // 图片生成字段
  image_count: number
  image_size: string | null
  image_input_size: string | null
  image_output_size: string | null
  image_input_tokens: number
  image_input_cost: number
  image_output_tokens: number
  image_output_cost: number
  image_size_source: ImageSizeSource | null
  image_size_breakdown: ImageSizeBreakdown | null

  // User-Agent
  user_agent: string | null
  ip_address?: string | null

  // Cache TTL Override
  cache_ttl_overridden: boolean

  // 计费模式
  billing_mode?: string | null

  created_at: string

  user?: User
  api_key?: ApiKey
  group?: Group
  subscription?: UserSubscription
}

export interface UsageLogProviderSummary {
  id: number
  name: string
}

export interface AdminUsageLog extends UsageLog {
  detailed_timing?: UsageLogTiming | null
  upstream_model?: string | null
  upstream_response_model?: string | null
  upstream_model_mismatch?: boolean | null
  model_mapping_chain?: string | null
  upstream_request_id?: string | null

  // 提供商计费倍率（仅管理员可见）
  provider_rate_multiplier?: number | null
  // 自定义定价规则计算的提供商统计费用（nil 时使用 total_cost * multiplier）
  provider_stats_cost?: number | null

  // 共享价格配置 ID 和计费等级（仅管理员可见）
  pricing_config_id?: number | null
  billing_tier?: string | null

  // 最小提供商信息（仅管理员接口返回）
  provider?: UsageLogProviderSummary
}

export interface UsageLogTiming {
  request_content_length?: number | null
  provider_slot_acquired_ms?: number | null
  upstream_get_conn_ms?: number | null
  upstream_got_conn_ms?: number | null
  upstream_wrote_request_ms?: number | null
  upstream_first_response_byte_ms?: number | null
  upstream_first_sse_data_ms?: number | null
  first_visible_output_ms?: number | null
  first_downstream_flush_ms?: number | null
  upstream_get_conn_count?: number | null
  upstream_got_conn_count?: number | null
  upstream_attempt_count?: number | null
  upstream_first_response_byte_count?: number | null
  upstream_connection_reused?: boolean
  upstream_wrote_request_error?: boolean
}

export interface RedeemCode {
  /** 领取者需要有成功付款记录。 */
  requires_payment?: boolean
  id: number
  code: string
  type: RedeemCodeType
  value: number
  status: 'active' | 'used' | 'expired' | 'unused' | 'disabled'
  max_uses: number
  used_count: number
  expires_at: string | null
  used_by: number | null
  used_at: string | null
  created_at: string
  updated_at?: string
  plan_id?: number | null
  notes?: string | null
  user?: User
  plan?: SubscriptionPlan
}

export interface GenerateRedeemCodesRequest {
  /** 领取者需要有成功付款记录。 */
  requires_payment?: boolean
  code?: string
  count: number
  type: RedeemCodeType
  value: number
  max_uses?: number
  expires_at?: number | null
  expires_in_days?: number
  plan_id?: number | null
}

export interface UpdateRedeemCodeRequest {
  /** 领取者需要有成功付款记录。 */
  requires_payment?: boolean
  value?: number
  max_uses?: number
  expires_at?: number | null
  plan_id?: number | null
}

export interface BatchUpdateRedeemCodeFields {
  status?: 'unused' | 'disabled'
  expires_at?: string | null
  notes?: string
}

export interface RedeemCodeRequest {
  code: string
}

// ==================== Dashboard & Statistics ====================

export interface DashboardStats {
  // 用户统计
  total_users: number
  today_new_users: number // 今日新增用户数
  active_users: number // 今日有请求的用户数
  hourly_active_users: number // 当前小时活跃用户数（UTC）
  stats_updated_at: string // 统计更新时间（UTC RFC3339）
  stats_stale: boolean // 统计是否过期

  // API Key 统计
  total_api_keys: number
  active_api_keys: number // 状态为 active 的 API Key 数

  // 提供商统计
  total_providers: number
  normal_providers: number // 正常提供商数
  error_providers: number // 异常提供商数
  ratelimit_providers: number // 限流提供商数
  overload_providers: number // 过载提供商数

  // 累计 Token 使用统计
  total_requests: number
  total_input_tokens: number
  total_output_tokens: number
  total_cache_creation_tokens: number
  total_cache_read_tokens: number
  total_tokens: number
  total_cost: number // 累计标准计费
  total_actual_cost: number // 累计实际扣除
  total_provider_cost: number // 累计提供商成本

  // 今日 Token 使用统计
  today_requests: number
  today_input_tokens: number
  today_output_tokens: number
  today_cache_creation_tokens: number
  today_cache_read_tokens: number
  today_tokens: number
  today_cost: number // 今日标准计费
  today_actual_cost: number // 今日实际扣除
  today_provider_cost: number // 今日提供商成本

  // 系统运行统计
  average_duration_ms: number // 平均响应时间
  uptime: number // 系统运行时间(秒)

  // 性能指标
  rpm: number // 近5分钟平均每分钟请求数
  tpm: number // 近5分钟平均每分钟Token数
}

export interface UsageStatsResponse {
  period?: string
  total_requests: number
  total_input_tokens: number
  total_output_tokens: number
  total_cache_tokens: number
  total_cache_creation_tokens: number
  total_cache_read_tokens: number
  total_tokens: number
  total_cost: number // 标准计费
  total_actual_cost: number // 实际扣除
  average_duration_ms: number
  models?: Record<string, number>
  endpoints?: EndpointStat[]
  upstream_endpoints?: EndpointStat[]
  endpoint_paths?: EndpointStat[]
}

// ==================== Trend & Chart Types ====================

export interface TrendDataPoint {
  date: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  total_tokens: number
  cost: number // 标准计费
  actual_cost: number // 实际扣除
}

export interface ModelStat {
  model: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  total_tokens: number
  cost: number // 标准计费
  actual_cost: number // 实际扣除
  provider_cost?: number // 提供商成本（仅管理员接口返回）
}

export interface EndpointStat {
  endpoint: string
  requests: number
  total_tokens: number
  cost: number
  actual_cost: number
}

export interface GroupStat {
  group_id: number
  group_name: string
  requests: number
  total_tokens: number
  cost: number // 标准计费
  actual_cost: number // 实际扣除
  provider_cost?: number // 提供商成本（仅管理员接口返回）
}

export interface UserBreakdownItem {
  user_id: number
  email: string
  requests: number
  input_tokens: number
  output_tokens: number
  cache_tokens: number
  total_tokens: number
  cost: number
  actual_cost: number
  provider_cost: number
}

export interface UserUsageTrendPoint {
  date: string
  user_id: number
  email: string
  username: string
  requests: number
  tokens: number
  cost: number // 标准计费
  actual_cost: number // 实际扣除
}

export interface UserSpendingRankingItem {
  user_id: number
  email: string
  username: string
  actual_cost: number
  requests: number
  tokens: number
}

export interface UserSpendingRankingResponse {
  ranking: UserSpendingRankingItem[]
  total_actual_cost: number
  total_requests: number
  total_tokens: number
  start_date: string
  end_date: string
}

// ==================== Admin User Management ====================

export interface UpdateUserRequest {
  email?: string
  password?: string
  username?: string
  notes?: string
  role?: 'admin' | 'user'
  balance?: number
  concurrency?: number
  rpm_limit?: number
  api_key_limit?: number
  status?: 'active' | 'disabled'
  allowed_groups?: number[] | null
  disabled_public_groups?: number[] | null
  // 用户专属分组倍率配置 (group_id -> rate_multiplier | null)
  // null 表示删除该分组的专属倍率
  group_rates?: Record<number, number | null>
}

export interface ChangePasswordRequest {
  old_password: string
  new_password: string
}

// ==================== User Subscription Types ====================

export interface UserSubscription {
  id: number
  user_id: number
  plan_id: number
  starts_at: string
  expires_at: string
  status: 'active' | 'pending' | 'expired' | 'suspended' | 'revoked'
  daily_limit_usd: number | null
  weekly_limit_usd: number | null
  monthly_limit_usd: number | null
  daily_usage_usd: number
  weekly_usage_usd: number
  monthly_usage_usd: number
  daily_window_start: string | null
  weekly_window_start: string | null
  monthly_window_start: string | null
  created_at: string
  updated_at: string
  revoked_at?: string | null
  user?: User
  plan?: SubscriptionPlan
}

export interface AssignSubscriptionRequest {
  user_id: number
  plan_id: number
  validity_days?: number
  notes?: string
}

export interface ExtendSubscriptionRequest {
  days: number
}

// ==================== Query Parameters ====================

export interface UserErrorRequest {
  request_id?: string
  id: number
  created_at: string
  model: string
  inbound_endpoint: string
  status_code: number
  category: string
  platform: string
  message: string
  key_name: string
  key_deleted: boolean
  client_ip?: string
  group_name?: string
  request_type?: number
  stream?: boolean
  user_agent?: string
}

export interface UserErrorRequestDetail extends UserErrorRequest {
  error_body: string
  upstream_status_code?: number
}

export interface UserErrorListParams {
  request_id?: string
  page?: number
  page_size?: number
  start_date?: string
  end_date?: string
  timezone?: string
  model?: string
  status_code?: number
  category?: string
  api_key_id?: number
  // 服务端排序,列白名单见后端 opsErrorLogsOrderBy(created_at/model/status_code)
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

export interface UsageQueryParams {
  request_id?: string
  page?: number
  page_size?: number
  api_key_id?: number
  user_id?: number
  provider_id?: number
  group_id?: number
  model?: string
  request_type?: UsageRequestType
  stream?: boolean
  billing_type?: number | null
  billing_mode?: string | null
  native_compaction_v2?: boolean | null
  start_date?: string
  end_date?: string
  timezone?: string
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

// ==================== Provider Usage Statistics ====================

export interface ProviderUsageHistory {
  date: string
  label: string
  requests: number
  tokens: number
  cost: number
  actual_cost: number // Provider cost (provider multiplier)
  user_cost: number // User/API key billed cost (group multiplier)
}

export interface ProviderUsageSummary {
  days: number
  actual_days_used: number
  total_cost: number // Provider cost (provider multiplier)
  total_user_cost: number
  total_standard_cost: number
  total_requests: number
  total_tokens: number
  avg_daily_cost: number // Provider cost
  avg_daily_user_cost: number
  avg_daily_requests: number
  avg_daily_tokens: number
  avg_duration_ms: number
  today: {
    date: string
    cost: number
    user_cost: number
    requests: number
    tokens: number
  } | null
  highest_cost_day: {
    date: string
    label: string
    cost: number
    user_cost: number
    requests: number
  } | null
  highest_request_day: {
    date: string
    label: string
    requests: number
    cost: number
    user_cost: number
  } | null
}

export interface ProviderUsageStatsResponse {
  history: ProviderUsageHistory[]
  summary: ProviderUsageSummary
  models: ModelStat[]
  endpoints: EndpointStat[]
  upstream_endpoints: EndpointStat[]
}

// ==================== User Attribute Types ====================

export type UserAttributeType = 'text' | 'textarea' | 'number' | 'email' | 'url' | 'date' | 'select' | 'multi_select'

export interface UserAttributeOption {
  value: string
  label: string
  [key: string]: unknown
}

export interface UserAttributeValidation {
  min_length?: number
  max_length?: number
  min?: number
  max?: number
  pattern?: string
  message?: string
}

export interface UserAttributeDefinition {
  id: number
  key: string
  name: string
  description: string
  type: UserAttributeType
  options: UserAttributeOption[]
  required: boolean
  validation: UserAttributeValidation
  placeholder: string
  display_order: number
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface UserAttributeValue {
  id: number
  user_id: number
  attribute_id: number
  value: string
  created_at: string
  updated_at: string
}

export interface CreateUserAttributeRequest {
  key: string
  name: string
  description?: string
  type: UserAttributeType
  options?: UserAttributeOption[]
  required?: boolean
  validation?: UserAttributeValidation
  placeholder?: string
  display_order?: number
  enabled?: boolean
}

export interface UpdateUserAttributeRequest {
  key?: string
  name?: string
  description?: string
  type?: UserAttributeType
  options?: UserAttributeOption[]
  required?: boolean
  validation?: UserAttributeValidation
  placeholder?: string
  display_order?: number
  enabled?: boolean
}

export interface UserAttributeValuesMap {
  [attributeId: number]: string
}

// ==================== Promo Code Types ====================

export interface PromoCode {
  id: number
  code: string
  bonus_amount: number
  max_uses: number
  used_count: number
  status: 'active' | 'disabled'
  expires_at: string | null
  notes: string | null
  created_at: string
  updated_at: string
}

export interface PromoCodeUsage {
  id: number
  promo_code_id: number
  user_id: number
  bonus_amount: number
  used_at: string
  user?: User
}

export interface CreatePromoCodeRequest {
  code?: string
  bonus_amount: number
  max_uses?: number
  expires_at?: number | null
  notes?: string
}

export interface UpdatePromoCodeRequest {
  code?: string
  bonus_amount?: number
  max_uses?: number
  status?: 'active' | 'disabled'
  expires_at?: number | null
  notes?: string
}

// ==================== TOTP (2FA) Types ====================

export interface TotpStatus {
  enabled: boolean
  enabled_at: number | null  // Unix timestamp in seconds
  feature_enabled: boolean
}

export interface TotpSetupRequest {
  email_code?: string
  password?: string
}

export interface TotpSetupResponse {
  secret: string
  qr_code_url: string
  setup_token: string
  countdown: number
}

export interface TotpEnableRequest {
  totp_code: string
  setup_token: string
}

export interface TotpEnableResponse {
  success: boolean
}

export interface TotpDisableRequest {
  email_code?: string
  password?: string
}

export interface TotpVerificationMethod {
  method: 'email' | 'password'
}

export interface TotpLoginResponse {
  requires_2fa: boolean
  temp_token?: string
  user_email_masked?: string
}

export interface TotpLogin2FARequest {
  temp_token: string
  totp_code: string
}

// ==================== Scheduled Test Types ====================

export interface ScheduledTestPlan {
  id: number
  provider_id: number
  model_id: string
  cron_expression: string
  enabled: boolean
  max_results: number
  auto_recover: boolean
  last_run_at: string | null
  next_run_at: string | null
  created_at: string
  updated_at: string
}

export interface ScheduledTestResult {
  id: number
  plan_id: number
  status: string
  response_text: string
  error_message: string
  latency_ms: number
  started_at: string
  finished_at: string
  created_at: string
}

export interface CreateScheduledTestPlanRequest {
  provider_id: number
  model_id: string
  cron_expression: string
  enabled?: boolean
  max_results?: number
  auto_recover?: boolean
}

export interface UpdateScheduledTestPlanRequest {
  model_id?: string
  cron_expression?: string
  enabled?: boolean
  max_results?: number
  auto_recover?: boolean
}

// Payment types
export type { SubscriptionPlan, PaymentOrder, CheckoutInfoResponse } from './payment'

export type {
} from '@/api/admin/users'
