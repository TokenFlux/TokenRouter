import { apiClient } from '../client'
import type {
  AdminApiKeyStatus,
  BetaPolicySettings,
  CreativeModelCandidate,
  CreativeWorkerStatus,
  EmailTemplateDetail,
  EmailTemplateListResponse,
  EmailTemplatePreviewResponse,
  OpenAI403CooldownSettings,
  OverloadCooldownSettings,
  PanelRateLimitSettings,
  PreAggregationSettingsResponse,
  PreviewEmailTemplateRequest,
  RateLimit429CooldownSettings,
  RectifierSettings,
  SendTestEmailRequest,
  StreamTimeoutSettings,
  SystemSettings,
  TestSmtpRequest,
  UpdateEmailTemplateRequest,
  UpdateSettingsRequest,
  WebSearchEmulationConfig,
  WebSearchTestResult,
} from './settings.types'

/**
 * settings.ts sends administrator settings requests across the composite
 * settings endpoint and its immediate sub-resource endpoints. DTO records live
 * in settings.types.ts while pure form state normalizers live in the settings
 * view directory, keeping this module limited to HTTP transport and array-shape
 * response guards.
 */

export type * from './settings.types'

/**
 * getSettings retrieves all system settings and returns the server result.
 */
export async function getSettings(): Promise<SystemSettings> {
  const { data } = await apiClient.get<SystemSettings>('/admin/settings')
  return data
}

/**
 * getCreativeModelCandidates retrieves administrator model candidates across
 * groups, including groups outside the current user's ordinary access. A
 * non-array response becomes an empty array for the model selector.
 */
export async function getCreativeModelCandidates(): Promise<CreativeModelCandidate[]> {
  const { data } = await apiClient.get<CreativeModelCandidate[]>(
    '/admin/settings/creative-model-candidates',
  )
  // A missing candidate collection should render an empty model selector, so
  // callers receive an array even when the response contains another shape.
  return Array.isArray(data) ? data : []
}

/**
 * getCreativeWorkerStatus retrieves the Creative Studio worker-pool snapshot for
 * the administrator activity display.
 */
export async function getCreativeWorkerStatus(): Promise<CreativeWorkerStatus> {
  const { data } = await apiClient.get<CreativeWorkerStatus>(
    '/admin/settings/creative-worker-status',
  )
  return data
}

/**
 * updateSettings updates system settings and returns the server result.
 */
export async function updateSettings(settings: UpdateSettingsRequest): Promise<SystemSettings> {
  const { data } = await apiClient.put<SystemSettings>('/admin/settings', settings)
  return data
}

/**
 * testSmtpConnection tests SMTP connection with provided config and returns the
 * server result.
 */
export async function testSmtpConnection(config: TestSmtpRequest): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.post<{
    message: string // Server-provided result message.
  }>('/admin/settings/test-smtp', config)
  return data
}

/**
 * sendTestEmail sends test email with provided SMTP config.
 */
export async function sendTestEmail(request: SendTestEmailRequest): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.post<{
    message: string // Server-provided result message.
  }>('/admin/settings/send-test-email', request)
  return data
}

/**
 * getEmailTemplates retrieves email templates and returns the server response
 * data.
 */
export async function getEmailTemplates(): Promise<EmailTemplateListResponse> {
  const { data } = await apiClient.get<EmailTemplateListResponse>('/admin/settings/email-templates')
  return data
}

/**
 * getEmailTemplate retrieves email template and returns the server response
 * data.
 */
export async function getEmailTemplate(
  event: string,
  locale: string,
): Promise<EmailTemplateDetail> {
  const { data } = await apiClient.get<EmailTemplateDetail>(
    `/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}`,
  )
  return data
}

/**
 * updateEmailTemplate updates email template and returns the server response
 * data.
 */
export async function updateEmailTemplate(
  event: string,
  locale: string,
  request: UpdateEmailTemplateRequest,
): Promise<EmailTemplateDetail> {
  const { data } = await apiClient.put<EmailTemplateDetail>(
    `/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}`,
    request,
  )
  return data
}

/**
 * restoreOfficialEmailTemplate submits restore official email template and
 * returns the server response data.
 */
export async function restoreOfficialEmailTemplate(
  event: string,
  locale: string,
): Promise<EmailTemplateDetail> {
  const { data } = await apiClient.post<EmailTemplateDetail>(
    `/admin/settings/email-templates/${encodeURIComponent(event)}/${encodeURIComponent(locale)}/restore-official`,
  )
  return data
}

/**
 * previewEmailTemplate submits preview email template and returns the server
 * response data.
 */
export async function previewEmailTemplate(
  request: PreviewEmailTemplateRequest,
): Promise<EmailTemplatePreviewResponse> {
  const { data } = await apiClient.post<EmailTemplatePreviewResponse>(
    '/admin/settings/email-template-preview',
    request,
  )
  return data
}

/**
 * getAdminApiKey retrieves admin API key status and returns the server result.
 */
export async function getAdminApiKey(): Promise<AdminApiKeyStatus> {
  const { data } = await apiClient.get<AdminApiKeyStatus>('/admin/settings/admin-api-key')
  return data
}

/**
 * regenerateAdminApiKey submits regenerate admin api key and returns the server
 * response data.
 */
export async function regenerateAdminApiKey(): Promise<{
  key: string // Returned administrator API secret.
}> {
  const { data } = await apiClient.post<{
    key: string // Returned administrator API secret.
  }>('/admin/settings/admin-api-key/regenerate')
  return data
}

/**
 * deleteAdminApiKey deletes admin API key and returns the server result.
 */
export async function deleteAdminApiKey(): Promise<{
  message: string // Server-provided result message.
}> {
  const { data } = await apiClient.delete<{
    message: string // Server-provided result message.
  }>('/admin/settings/admin-api-key')
  return data
}

/**
 * getOverloadCooldownSettings retrieves overload cooldown settings and returns
 * the server response data.
 */
export async function getOverloadCooldownSettings(): Promise<OverloadCooldownSettings> {
  const { data } = await apiClient.get<OverloadCooldownSettings>(
    '/admin/settings/overload-cooldown',
  )
  return data
}

/**
 * updateOverloadCooldownSettings updates overload cooldown settings and returns
 * the server response data.
 */
export async function updateOverloadCooldownSettings(
  settings: OverloadCooldownSettings,
): Promise<OverloadCooldownSettings> {
  const { data } = await apiClient.put<OverloadCooldownSettings>(
    '/admin/settings/overload-cooldown',
    settings,
  )
  return data
}

/**
 * getOpenAI403CooldownSettings retrieves open ai403 cooldown settings and
 * returns the server response data.
 */
export async function getOpenAI403CooldownSettings(): Promise<OpenAI403CooldownSettings> {
  const { data } = await apiClient.get<OpenAI403CooldownSettings>(
    '/admin/settings/openai-403-cooldown',
  )
  return data
}

/**
 * updateOpenAI403CooldownSettings updates open ai403 cooldown settings and
 * returns the server response data.
 */
export async function updateOpenAI403CooldownSettings(
  settings: OpenAI403CooldownSettings,
): Promise<OpenAI403CooldownSettings> {
  const { data } = await apiClient.put<OpenAI403CooldownSettings>(
    '/admin/settings/openai-403-cooldown',
    settings,
  )
  return data
}

/**
 * getRateLimit429CooldownSettings retrieves rate limit429 cooldown settings and
 * returns the server response data.
 */
export async function getRateLimit429CooldownSettings(): Promise<RateLimit429CooldownSettings> {
  const { data } = await apiClient.get<RateLimit429CooldownSettings>(
    '/admin/settings/rate-limit-429-cooldown',
  )
  return data
}

/**
 * updateRateLimit429CooldownSettings updates rate limit429 cooldown settings and
 * returns the server response data.
 */
export async function updateRateLimit429CooldownSettings(
  settings: RateLimit429CooldownSettings,
): Promise<RateLimit429CooldownSettings> {
  const { data } = await apiClient.put<RateLimit429CooldownSettings>(
    '/admin/settings/rate-limit-429-cooldown',
    settings,
  )
  return data
}

/**
 * getPanelRateLimitSettings retrieves panel rate limit settings and returns the
 * server response data.
 */
export async function getPanelRateLimitSettings(): Promise<PanelRateLimitSettings> {
  const { data } = await apiClient.get<PanelRateLimitSettings>('/admin/settings/panel-rate-limit')
  return data
}

/**
 * updatePanelRateLimitSettings updates panel rate limit settings and returns the
 * server response data.
 */
export async function updatePanelRateLimitSettings(
  settings: PanelRateLimitSettings,
): Promise<PanelRateLimitSettings> {
  const { data } = await apiClient.put<PanelRateLimitSettings>(
    '/admin/settings/panel-rate-limit',
    settings,
  )
  return data
}

/**
 * getStreamTimeoutSettings retrieves stream timeout settings and returns the
 * server result.
 */
export async function getStreamTimeoutSettings(): Promise<StreamTimeoutSettings> {
  const { data } = await apiClient.get<StreamTimeoutSettings>('/admin/settings/stream-timeout')
  return data
}

/**
 * updateStreamTimeoutSettings updates stream timeout settings and returns the
 * server result.
 */
export async function updateStreamTimeoutSettings(
  settings: StreamTimeoutSettings,
): Promise<StreamTimeoutSettings> {
  const { data } = await apiClient.put<StreamTimeoutSettings>(
    '/admin/settings/stream-timeout',
    settings,
  )
  return data
}

/**
 * getRectifierSettings retrieves rectifier settings and returns the server
 * result.
 */
export async function getRectifierSettings(): Promise<RectifierSettings> {
  const { data } = await apiClient.get<RectifierSettings>('/admin/settings/rectifier')
  return data
}

/**
 * updateRectifierSettings updates rectifier settings and returns the server
 * result.
 */
export async function updateRectifierSettings(
  settings: RectifierSettings,
): Promise<RectifierSettings> {
  const { data } = await apiClient.put<RectifierSettings>('/admin/settings/rectifier', settings)
  return data
}

/**
 * getBetaPolicySettings retrieves beta policy settings and returns the server
 * result.
 */
export async function getBetaPolicySettings(): Promise<BetaPolicySettings> {
  const { data } = await apiClient.get<BetaPolicySettings>('/admin/settings/beta-policy')
  return data
}

/**
 * updateBetaPolicySettings updates beta policy settings and returns the server
 * result.
 */
export async function updateBetaPolicySettings(
  settings: BetaPolicySettings,
): Promise<BetaPolicySettings> {
  const { data } = await apiClient.put<BetaPolicySettings>('/admin/settings/beta-policy', settings)
  return data
}

/**
 * getWebSearchEmulationConfig retrieves web search emulation config and returns
 * the server response data.
 */
export async function getWebSearchEmulationConfig(): Promise<WebSearchEmulationConfig> {
  const { data } = await apiClient.get<WebSearchEmulationConfig>(
    '/admin/settings/web-search-emulation',
  )
  return data
}

/**
 * updateWebSearchEmulationConfig updates web search emulation config and returns
 * the server response data.
 */
export async function updateWebSearchEmulationConfig(
  config: WebSearchEmulationConfig,
): Promise<WebSearchEmulationConfig> {
  const { data } = await apiClient.put<WebSearchEmulationConfig>(
    '/admin/settings/web-search-emulation',
    config,
  )
  return data
}

/**
 * testWebSearchEmulation submits web search emulation and returns the server
 * response data.
 */
export async function testWebSearchEmulation(query: string): Promise<WebSearchTestResult> {
  const { data } = await apiClient.post<WebSearchTestResult>(
    '/admin/settings/web-search-emulation/test',
    { query },
  )
  return data
}

/**
 * resetWebSearchUsage submits web search usage and returns after the request
 * completes.
 */
export async function resetWebSearchUsage(payload: {
  provider_type: string // Provider credential type.
}): Promise<void> {
  await apiClient.post('/admin/settings/web-search-emulation/reset-usage', payload)
}

/**
 * getPreAggregationSettings retrieves pre aggregation settings and returns the
 * server response data.
 */
export async function getPreAggregationSettings(): Promise<PreAggregationSettingsResponse> {
  const { data } = await apiClient.get<PreAggregationSettingsResponse>(
    '/admin/settings/pre-aggregation',
  )
  return data
}

/**
 * updatePreAggregationSettings updates pre aggregation settings and returns the
 * server response data.
 */
export async function updatePreAggregationSettings(
  payload: PreAggregationSettingsResponse['settings'],
): Promise<PreAggregationSettingsResponse> {
  const { data } = await apiClient.put<PreAggregationSettingsResponse>(
    '/admin/settings/pre-aggregation',
    payload,
  )
  return data
}

/**
 * backfillPreAggregation submits backfill pre aggregation and returns the server
 * response data.
 */
export async function backfillPreAggregation(days: number): Promise<{
  status: string // Current server-reported status.
  days: number // Requested interval days.
}> {
  const { data } = await apiClient.post<{
    status: string // Current server-reported status.
    days: number // Requested interval days.
  }>('/admin/settings/pre-aggregation/backfill', { days })
  return data
}

/**
 * settingsAPI groups administrator settings endpoints for callers using the
 * module's named or default API object.
 */
export const settingsAPI = {
  getSettings,
  getCreativeModelCandidates,
  getCreativeWorkerStatus,
  updateSettings,
  testSmtpConnection,
  sendTestEmail,
  getEmailTemplates,
  getEmailTemplate,
  updateEmailTemplate,
  restoreOfficialEmailTemplate,
  previewEmailTemplate,
  getAdminApiKey,
  regenerateAdminApiKey,
  deleteAdminApiKey,
  getOverloadCooldownSettings,
  updateOverloadCooldownSettings,
  getOpenAI403CooldownSettings,
  updateOpenAI403CooldownSettings,
  getRateLimit429CooldownSettings,
  updateRateLimit429CooldownSettings,
  getPanelRateLimitSettings,
  updatePanelRateLimitSettings,
  getStreamTimeoutSettings,
  updateStreamTimeoutSettings,
  getRectifierSettings,
  updateRectifierSettings,
  getBetaPolicySettings,
  updateBetaPolicySettings,
  getWebSearchEmulationConfig,
  updateWebSearchEmulationConfig,
  testWebSearchEmulation,
  resetWebSearchUsage,
  getPreAggregationSettings,
  updatePreAggregationSettings,
  backfillPreAggregation,
}

export default settingsAPI
