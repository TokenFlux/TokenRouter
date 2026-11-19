import { describe, expect, it, vi } from 'vitest'
import type { Provider } from '@/api/admin/providers.types'
import {
  createProviderEditDraft,
  createProviderFormDraft,
  resetPlatformScopedDraft,
} from '../providerFormDraft'
import { hydrateProviderEditDraft } from '../providerFormHydration'
import {
  applyAntigravityPolicyCredentials,
  applyPoolModeCredentials,
  applyProviderProtocolPatch,
  applyProviderSchedulingThresholdCredentials,
  applyQoderModelRestriction,
  buildProviderBasePatch,
  type ProviderEditPatch,
} from '../providerFormCredentials'
import {
  applyProviderExtraPatch,
  buildAntigravityCreateExtra,
  buildGrokOAuthCreateExtra,
  buildOpenAICreateExtra,
  withUpstreamRequestIdHeader,
} from '../providerFormExtra'

// providerFormModel.spec.ts verifies shared provider draft factories, platform
// resets, hydration, and create/edit credential and Extra serializers. Tests
// protect clearing sentinels, Antigravity RT batch policies, Codex image-tool
// serialization, Grok cache preferences, and per-provider scheduling thresholds.

// provider creates a minimal administration response for draft and patch tests.
function provider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: 1,
    name: 'Provider',
    platform: 'openai',
    type: 'apikey',
    credentials: {},
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    group_ids: [],
    ...overrides,
  } as Provider
}

describe('provider form model representations', () => {
  // Each OpenAI authentication type hydrates a disabled GIF flag and can restore
  // its default. Editing must preserve unrelated metadata while retaining explicit false.
  it.each(['oauth', 'apikey'] as const)('preserves GIF policy for %s providers', (type) => {
    const source = provider({ type, extra: { images_gif_enabled: false, retained: true } })
    const draft = createProviderEditDraft()
    hydrateProviderEditDraft(draft, source, { loadFromExtra: vi.fn(), reset: vi.fn() })
    expect(draft.openAIImagesGIFEnabled.value).toBe(false)
    const disabled = buildProviderBasePatch(draft)
    applyProviderExtraPatch(draft, source, disabled, vi.fn())
    expect(disabled.extra?.images_gif_enabled).toBe(false)
    draft.openAIImagesGIFEnabled.value = true
    const enabled = buildProviderBasePatch(draft)
    applyProviderExtraPatch(draft, source, enabled, vi.fn())
    expect(enabled.extra).not.toHaveProperty('images_gif_enabled')
    expect(enabled.extra?.retained).toBe(true)
  })

  // Separate dialogs receive their own nested endpoint maps and model arrays.
  // Mutating one draft leaves the other draft's default values and collections unchanged.
  it('allocates independent draft refs and nested collections', () => {
    const first = createProviderEditDraft()
    const second = createProviderEditDraft()
    first.editAdaptiveBaseUrls.value.anthropic = 'https://relay.example/anthropic'
    first.allowedModels.value.push('selected-model')
    first.form.group_ids.push(7)
    expect(second.editAdaptiveBaseUrls.value.anthropic).toBe('')
    expect(second.allowedModels.value).toEqual([])
    expect(second.form.group_ids).toEqual([])
  })

  // Null clearing values and invalid load factors need numeric API sentinels.
  // Valid positive factors and explicit group selections remain unchanged in the outgoing base patch.
  it.each([null, Number.NaN, 0, -2])('encodes clearing values with factor %s', (factor) => {
    const draft = createProviderEditDraft()
    draft.form.load_factor = factor
    draft.form.group_ids = [7]
    expect(buildProviderBasePatch(draft)).toMatchObject({
      proxy_id: 0,
      expires_at: 0,
      load_factor: 0,
      group_ids: [7],
    })
    draft.form.load_factor = 3
    expect(buildProviderBasePatch(draft).load_factor).toBe(3)
    expect(draft.form.proxy_id).toBeNull()
    expect(draft.form.expires_at).toBeNull()
  })

  // API-key and Bedrock saves share retry-count and status-code normalization.
  // Enabled configuration filters invalid codes, while disabling pool mode removes all retry fields and retains unrelated credentials.
  it('normalizes and clears the shared credential retry configuration', () => {
    const draft = createProviderEditDraft()
    draft.poolModeEnabled.value = true
    draft.poolModeRetryCount.value = 15
    draft.poolModeRetryStatusCodesInput.value = '429, 401, 429, invalid, 99'
    const credentials: Record<string, unknown> = { api_key: 'retained' }
    applyPoolModeCredentials(draft, credentials)
    expect(credentials).toEqual({
      api_key: 'retained',
      pool_mode: true,
      pool_mode_retry_count: 10,
      pool_mode_retry_status_codes: [401, 429],
    })
    draft.poolModeEnabled.value = false
    applyPoolModeCredentials(draft, credentials)
    expect(credentials).toEqual({ api_key: 'retained' })
  })

  // Hydration copies a saved empty protocol set instead of interpreting it as unknown.
  // Protocol serialization preserves that empty selection while an undefined draft selects catalog defaults.
  it('distinguishes explicit empty protocol selections from unknown selections', () => {
    const source = provider({ credentials: { upstream_protocols: [], api_protocol: 'adaptive' } })
    const draft = createProviderEditDraft()
    hydrateProviderEditDraft(draft, source, { loadFromExtra: vi.fn(), reset: vi.fn() })
    const patch: ProviderEditPatch = { extra: { retained: true, openai_text_route_mode: 'old' } }
    applyProviderProtocolPatch(source, patch, draft.upstreamProtocols.value, ['openai_responses'])
    expect(patch.credentials?.upstream_protocols).toEqual([])
    expect(patch.credentials).not.toHaveProperty('api_protocol')
    expect(patch.extra).toEqual({ retained: true })
    expect(draft.upstreamProtocols.value).not.toBe(source.credentials.upstream_protocols)
    const unknown: ProviderEditPatch = {}
    applyProviderProtocolPatch(source, unknown, undefined, ['openai_responses'])
    expect(unknown.credentials?.upstream_protocols).toEqual(['openai_responses'])
    expect(source.credentials.api_protocol).toBe('adaptive')
  })

  // A Spark shadow has parent-owned credentials and receives a model-only patch.
  // Protocol normalization leaves that patch untouched, including unrelated extra fields supplied by the caller.
  it('bypasses protocol normalization for shadow providers', () => {
    const source = provider({ parent_provider_id: 8 })
    const patch: ProviderEditPatch = {
      credentials: { model_mapping: { alias: 'target' } },
      extra: { openai_text_route_mode: 'retained' },
    }
    const before = structuredClone(patch)
    applyProviderProtocolPatch(source, patch, [], ['openai_responses'])
    expect(patch).toEqual(before)
  })

  // Qoder rows generated by the editor are initially unconfigured and untouched.
  // Only an explicit edit persists their mapping, while a saved empty whitelist already counts as configuration.
  it('distinguishes untouched Qoder defaults from configured or edited restrictions', () => {
    const source = provider({ platform: 'qoder', type: 'cosy' })
    const draft = createProviderEditDraft()
    const notifications = { loadFromExtra: vi.fn(), reset: vi.fn() }
    hydrateProviderEditDraft(draft, source, notifications)
    draft.modelMappings.value = [{ from: 'alias', to: 'target' }]
    const untouched = { machine_id: 'retained' }
    applyQoderModelRestriction(draft, untouched)
    expect(untouched).toEqual({ machine_id: 'retained' })
    draft.qoderModelRestrictionTouched.value = true
    const edited = {}
    applyQoderModelRestriction(draft, edited)
    expect(edited).toEqual({ model_mapping: { alias: 'target' }, model_whitelist: [] })
    hydrateProviderEditDraft(
      draft,
      { ...source, credentials: { model_whitelist: [] } },
      notifications,
    )
    const configured = {}
    applyQoderModelRestriction(draft, configured)
    expect(configured).toEqual({ model_whitelist: [] })
    expect(draft.qoderModelRestrictionTouched.value).toBe(false)
  })

  // A provider carries unrelated runtime extras while quota and policy controls are edited.
  // The patch retains those extras, removes disabled quota fields, and leaves the source response unchanged.
  it('retains unrelated extras while clearing disabled quota settings', () => {
    const source = provider({
      extra: {
        retained: { source: 'server' },
        quota_monthly_limit: 25,
        quota_monthly_used: 3,
        quota_monthly_start: 'saved-start',
        quota_monthly_reset_mode: 'fixed',
        quota_monthly_reset_day: 4,
        quota_monthly_reset_hour: 2,
        quota_reset_timezone: 'UTC',
      },
    })
    const draft = createProviderEditDraft()
    const patch = buildProviderBasePatch(draft)
    const writeNotifications = vi.fn()
    applyProviderExtraPatch(draft, source, patch, writeNotifications)
    expect(patch.extra?.retained).toEqual({ source: 'server' })
    expect(Object.keys(patch.extra ?? {}).filter((key) => key.startsWith('quota_'))).toEqual([])
    expect(source.extra?.quota_monthly_limit).toBe(25)
    expect(writeNotifications).toHaveBeenCalledWith(patch.extra, 'update')
  })

  // A saved OAuth driver is hydrated, edited, and cleared through the same draft.
  // Both writes preserve unrelated metadata and clearing submits an explicit blank.
  it('hydrates and clears an OAuth image driver override', () => {
    const source = provider({
      type: 'oauth',
      extra: { codex_image_responses_model: 'gpt-6.1-sol', retained: true },
    })
    const draft = createProviderEditDraft()
    hydrateProviderEditDraft(draft, source, { loadFromExtra: vi.fn(), reset: vi.fn() })
    expect(draft.codexImageResponsesModel.value).toBe('gpt-6.1-sol')
    draft.codexImageResponsesModel.value = ''
    const patch = buildProviderBasePatch(draft)
    applyProviderExtraPatch(draft, source, patch, vi.fn())
    expect(patch.extra?.codex_image_responses_model).toBe('')
    expect(patch.extra?.retained).toBe(true)
  })

  // Switching platforms in create mode clears Grok's single-slot concurrency,
  // OpenAI compaction overrides, Anthropic OAuth limits, and quota schedules.
  it('resets platform-scoped create draft state without leaking Grok concurrency', () => {
    const draft = createProviderFormDraft('create')
    const resetNotify = vi.fn()
    resetPlatformScopedDraft(draft, 'grok', resetNotify)
    expect(draft.form.concurrency).toBe(1)

    draft.openAICompactMode.value = 'force_off'
    draft.windowCostEnabled.value = true
    draft.windowCostLimit.value = 50
    draft.providerSchedulingThresholdOverrideEnabled.value = true
    draft.providerSchedulingThresholdOverrideValue.value = 85

    resetPlatformScopedDraft(draft, 'gemini', resetNotify)
    expect(draft.form.concurrency).toBe(10)
    expect(draft.openAICompactMode.value).toBe('force_on')
    expect(draft.windowCostEnabled.value).toBe(false)
    expect(draft.windowCostLimit.value).toBeNull()
    expect(draft.providerSchedulingThresholdOverrideEnabled.value).toBe(false)
    expect(draft.providerSchedulingThresholdOverrideValue.value).toBe(100)
    expect(resetNotify).toHaveBeenCalled()
  })

  // Create builders serialize Codex image-tool mode, Grok client-tool cache
  // preference, Antigravity policies, request-ID headers, and scheduling thresholds.
  it('serializes unified create policies across OpenAI, Grok, and Antigravity', () => {
    const draft = createProviderFormDraft('create')
    draft.antigravityProjectId.value = 'proj-ag'
    draft.interceptWarmupRequests.value = true
    draft.antigravityModelMappings.value = [{ from: 'claude-*', to: 'gemini-2.5-pro' }]
    draft.antigravityWhitelistModels.value = ['gemini-2.5-pro']
    const agCreds: Record<string, unknown> = { refresh_token: 'rt-1' }
    applyAntigravityPolicyCredentials(draft, agCreds, 'create')
    expect(agCreds).toMatchObject({
      refresh_token: 'rt-1',
      antigravity_project_id: 'proj-ag',
      intercept_warmup_requests: true,
      model_mapping: { 'claude-*': 'gemini-2.5-pro' },
      model_whitelist: ['gemini-2.5-pro'],
    })
    expect(withUpstreamRequestIdHeader('X-Trace-Id', buildAntigravityCreateExtra(true))).toEqual({
      allow_overages: true,
      upstream_request_id_header: 'X-Trace-Id',
    })

    expect(
      withUpstreamRequestIdHeader('X-Grok-Req', buildGrokOAuthCreateExtra(false, { email: 'a@b' })),
    ).toEqual({
      email: 'a@b',
      grok_client_tool_cache_enabled: false,
      upstream_request_id_header: 'X-Grok-Req',
    })

    const openAIExtra = buildOpenAICreateExtra({
      platform: 'openai',
      type: 'oauth',
      providerCategory: 'oauth-based',
      responsesWSConnectionMode: 'pooled',
      openaiPassthroughEnabled: false,
      openaiFlattenNamespacesEnabled: false,
      openAIOAuthClientPolicy: 'any',
      codexCLIOnlyAllowClaudeCodeEnabled: false,
      autoPause5hThreshold: null,
      autoPause7dThreshold: null,
      autoPause5hDisabled: false,
      autoPause7dDisabled: false,
      tlsFingerprintEnabled: false,
      tlsFingerprintProfileId: null,
      tlsFingerprintRouterId: null,
      codexFingerprintMode: 'off',
      codexImageToolMode: 'enabled',
      openAICompactMode: 'force_on',
      openAINativeCompactionV2Mode: 'force_on',
      openAIImagesURLToB64JSON: false,
      openAIResponsesContinuationSupported: false,
    })
    expect(openAIExtra?.codex_image_generation_bridge).toBe(true)

    draft.providerSchedulingThresholdOverrideEnabled.value = true
    draft.providerSchedulingThresholdOverrideValue.value = 80
    const openAICreds: Record<string, unknown> = {}
    applyProviderSchedulingThresholdCredentials(draft, openAICreds, 'openai')
    expect(openAICreds.provider_scheduling_threshold).toBe(80)
  })
})
