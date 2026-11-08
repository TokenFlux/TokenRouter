import { describe, expect, it, vi } from 'vitest'
import type { Provider } from '@/api/admin/providers.types'
import { createProviderEditDraft } from '../providerEditDraft'
import { hydrateProviderEditDraft } from '../providerEditHydration'
import {
  applyProviderExtraPatch,
  applyPoolModeCredentials,
  applyProviderProtocolPatch,
  applyQoderModelRestriction,
  buildProviderBasePatch,
  type ProviderEditPatch,
} from '../providerEditPatch'

// providerEditPatch.spec.ts verifies representation-sensitive provider updates
// independently of mounted controls. Draft factories, hydration, and patch
// builders are exercised together so empty selections, inherited credentials,
// mutable collections, and unrelated persisted settings retain their meanings
// across the edit workflow.

// provider creates a minimal administration response for draft and patch tests.
// Callers override platform-specific values explicitly; its credential object
// contains no secret text so assertions can distinguish omission from replacement.
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

describe('provider edit patch representations', () => {
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
})
