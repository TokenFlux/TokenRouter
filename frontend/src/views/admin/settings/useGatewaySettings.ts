import { ref, reactive, computed, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { SCHEDULING_THRESHOLD_PLATFORMS } from './settingsFormHelpers'
import type { OpenAIFastPolicyRule, UserPromptReplacementConfig } from '@/api/admin/settings.types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores'
import type { SettingsForm } from './settingsState'
import type { SettingsSnapshotKey } from './settingsPersistence'
import {
  defaultUserPromptReplacementConfig,
  normalizeUserPromptReplacementConfig,
  normalizeUserPromptReplacementType,
  createUserPromptReplacementRule,
} from './settingsDefaults'

/**
 * useGatewaySettings.ts owns editable gateway policy state and dedicated endpoint
 * actions for the administrator settings page. The root page schedules loads and
 * saves, keeps this state alive across category navigation, and supplies the
 * snapshot callback. Bulk settings consume the shared form, while cooldown,
 * rectifier, beta policy, and Ollama controls maintain separate reactive drafts.
 *
 * A dedicated save applies the server response before waiting for field components
 * to settle and recording a clean snapshot. Initial read failures retain the current
 * local values and still establish a snapshot. Fast policy state additionally
 * distinguishes an unavailable initial response from a loaded empty rules array,
 * because the bulk serializer omits unavailable rules to retain the server
 * configuration.
 */

/** GatewaySettingsForm selects the bulk draft fields read or edited by gateway controls. */
export type GatewaySettingsForm = Pick<
  SettingsForm,
  | 'advanced_scheduler_effective_ewma_error_rate_alpha'
  | 'advanced_scheduler_effective_ewma_ttft_alpha'
  | 'advanced_scheduler_effective_lb_top_k'
  | 'advanced_scheduler_effective_sticky_escape_enabled'
  | 'advanced_scheduler_effective_sticky_escape_error_rate'
  | 'advanced_scheduler_effective_sticky_escape_ttft_ms'
  | 'advanced_scheduler_effective_weight_error_rate'
  | 'advanced_scheduler_effective_weight_load'
  | 'advanced_scheduler_effective_weight_previous_response'
  | 'advanced_scheduler_effective_weight_priority'
  | 'advanced_scheduler_effective_weight_queue'
  | 'advanced_scheduler_effective_weight_quota_headroom'
  | 'advanced_scheduler_effective_weight_reset'
  | 'advanced_scheduler_effective_weight_session_sticky'
  | 'advanced_scheduler_effective_weight_ttft'
  | 'advanced_scheduler_ewma_error_rate_alpha'
  | 'advanced_scheduler_ewma_ttft_alpha'
  | 'advanced_scheduler_lb_top_k'
  | 'advanced_scheduler_sticky_escape_enabled'
  | 'advanced_scheduler_sticky_escape_error_rate'
  | 'advanced_scheduler_sticky_escape_ttft_ms'
  | 'advanced_scheduler_sticky_weighted_enabled'
  | 'advanced_scheduler_subscription_priority_enabled'
  | 'advanced_scheduler_weight_error_rate'
  | 'advanced_scheduler_weight_load'
  | 'advanced_scheduler_weight_previous_response'
  | 'advanced_scheduler_weight_priority'
  | 'advanced_scheduler_weight_queue'
  | 'advanced_scheduler_weight_quota_headroom'
  | 'advanced_scheduler_weight_reset'
  | 'advanced_scheduler_weight_session_sticky'
  | 'advanced_scheduler_weight_ttft'
  | 'allow_user_view_error_requests'
  | 'antigravity_user_agent_version'
  | 'claude_oauth_system_prompt'
  | 'claude_oauth_system_prompt_blocks'
  | 'enable_anthropic_cache_ttl_1h_injection'
  | 'enable_claude_oauth_system_prompt_injection'
  | 'enable_client_dateline_normalization'
  | 'enable_fingerprint_unification'
  | 'enable_metadata_passthrough'
  | 'grok_default_base_url_mode'
  | 'grok_default_text_model'
  | 'max_claude_code_version'
  | 'min_claude_code_version'
  | 'openai_allow_claude_code_codex_plugin'
  | 'openai_codex_user_agent'
  | 'openai_provider_quota_auto_pause'
  | 'provider_scheduling_thresholds'
  | 'rewrite_message_cache_control'
  | 'ttft_mode'
  | 'user_prompt_replacement_config'
>

/** AdvancedSchedulerOverrideKey identifies editable numeric scheduler overrides in the shared form. */
export type AdvancedSchedulerOverrideKey =
  | 'advanced_scheduler_lb_top_k'
  | 'advanced_scheduler_weight_priority'
  | 'advanced_scheduler_weight_load'
  | 'advanced_scheduler_weight_queue'
  | 'advanced_scheduler_weight_error_rate'
  | 'advanced_scheduler_weight_ttft'
  | 'advanced_scheduler_weight_reset'
  | 'advanced_scheduler_weight_quota_headroom'
  | 'advanced_scheduler_weight_previous_response'
  | 'advanced_scheduler_weight_session_sticky'

/** AdvancedSchedulerEffectiveKey identifies server effective values displayed as scheduler field placeholders. */
export type AdvancedSchedulerEffectiveKey =
  | 'advanced_scheduler_effective_lb_top_k'
  | 'advanced_scheduler_effective_weight_priority'
  | 'advanced_scheduler_effective_weight_load'
  | 'advanced_scheduler_effective_weight_queue'
  | 'advanced_scheduler_effective_weight_error_rate'
  | 'advanced_scheduler_effective_weight_ttft'
  | 'advanced_scheduler_effective_weight_reset'
  | 'advanced_scheduler_effective_weight_quota_headroom'
  | 'advanced_scheduler_effective_weight_previous_response'
  | 'advanced_scheduler_effective_weight_session_sticky'
  | 'advanced_scheduler_effective_ewma_error_rate_alpha'
  | 'advanced_scheduler_effective_ewma_ttft_alpha'
  | 'advanced_scheduler_effective_sticky_escape_enabled'
  | 'advanced_scheduler_effective_sticky_escape_ttft_ms'
  | 'advanced_scheduler_effective_sticky_escape_error_rate'
/**
 * useGatewaySettings creates gateway drafts and operations during the settings page
 * setup. Pass the shared bulk form and a snapshot callback bound to the page dirty
 * tracker. The page coordinator owns endpoint ordering and registers each save
 * target. Dedicated reads and successful writes wait for Vue field normalization
 * before recording a clean snapshot, while failures preserve the current draft
 * values for continued editing or retry.
 */
export function useGatewaySettings(
  form: GatewaySettingsForm,
  markSettingsClean: (key: SettingsSnapshotKey) => void,
) {
  // Translations and notification services are captured during setup for the returned
  // gateway actions.
  const { t } = useI18n()
  const appStore = useAppStore()

  // The global latency selector exposes event and output timing with labels reactive to
  // the current locale.
  const ttftModeOptions = computed(() => [
    {
      value: 'first_event',
      label: t('admin.settings.gatewayForwarding.ttftModeFirstEvent'),
    },
    {
      value: 'first_output',
      label: t('admin.settings.gatewayForwarding.ttftModeFirstOutput'),
    },
  ])

  // Ollama polling has a dedicated endpoint and loading state; its initial draft keeps
  // polling disabled until configured.
  const ollamaCloudUsageLoading = ref(true)

  const ollamaCloudUsageForm = reactive({
    enabled: false, // Enables this endpoint policy.
    interval_minutes: 60, // Scheduled polling interval in minutes.
    debounce_minutes: 1, // Usage-triggered refresh delay in minutes.
  })

  // Overload handling tracks a separate draft and request status for the provider
  // cooldown settings endpoint.
  const overloadCooldownLoading = ref(true)

  const overloadCooldownForm = reactive({
    enabled: true, // Enables this endpoint policy.
    cooldown_minutes: 10, // Temporary exclusion duration in minutes.
  })

  // OpenAI permission failures combine a cooldown with optional escalation after
  // repeated errors within the configured window.
  const openAI403CooldownLoading = ref(true)

  const openAI403CooldownForm = reactive({
    enabled: true, // Enables this endpoint policy.
    cooldown_minutes: 10, // Temporary exclusion duration in minutes.
    error_on_threshold_enabled: true, // Escalates repeated permission failures.
    threshold_count: 3, // Failures required before escalation.
    threshold_window_minutes: 180, // Failure counting window in minutes.
  })

  // Rate-limit handling keeps its dedicated endpoint draft separate from the bulk
  // gateway settings object and snapshot.
  const rateLimit429CooldownLoading = ref(true)

  const rateLimit429CooldownForm = reactive({
    enabled: true, // Enables this endpoint policy.
    cooldown_seconds: 5, // Rate-limit exclusion duration in seconds.
  })

  // Stream timeout controls select the provider action and repeated-timeout threshold,
  // with loading owned by its dedicated read.
  const streamTimeoutLoading = ref(true)

  const streamTimeoutForm = reactive({
    enabled: true, // Enables this endpoint policy.
    action: 'temp_unsched' as 'temp_unsched' | 'error' | 'none', // Selected provider or request action.
    temp_unsched_minutes: 5, // Temporary exclusion duration in minutes.
    threshold_count: 3, // Failures required before escalation.
    threshold_window_minutes: 10, // Failure counting window in minutes.
  })

  // Timeout action labels describe temporary exclusion, provider error marking, or no
  // provider state action for the dropdown.
  const streamTimeoutActionOptions = computed(() => [
    {
      value: 'temp_unsched',
      label: t('admin.settings.streamTimeout.actionTempUnsched'),
    },
    { value: 'error', label: t('admin.settings.streamTimeout.actionError') },
    { value: 'none', label: t('admin.settings.streamTimeout.actionNone') },
  ])

  // Request rectification keeps signature and budget repair switches together with the
  // custom API Key signature match patterns.
  const rectifierLoading = ref(true)

  const rectifierForm = reactive({
    enabled: true, // Enables this endpoint policy.
    thinking_signature_enabled: true, // Enables thinking signature repair.
    thinking_budget_enabled: true, // Enables thinking budget repair.
    apikey_signature_enabled: false, // Enables API Key signature matching.
    apikey_signature_patterns: [] as string[], // Custom API Key signature patterns.
  })

  // Beta rules load through their own endpoint; the draft preserves the server token
  // ordering used by the rule editor.
  const betaPolicyLoading = ref(true)

  const betaPolicyForm = reactive({
    // Ordered policy rule drafts.
    rules: [] as Array<{
      beta_token: string // Exact upstream beta token.
      action: 'pass' | 'filter' | 'block' // Selected provider or request action.
      scope: 'all' | 'oauth' | 'apikey' | 'bedrock' // Authentication types targeted by rule.
      // The beta rule editor shows this message for block actions; its dedicated
      // serializer sends the stored text for every action, leaving interpretation to the server.
      error_message?: string // Client message for blocked requests.
      // Empty patterns are removed on save; when no patterns remain, the request omits
      // both the whitelist and its unmatched-model fallback fields for this rule.
      model_whitelist?: string[] // Optional target model match patterns.
      // A populated whitelist enables this action for unmatched models; omission then uses
      // pass in the outgoing payload, regardless of the main beta-token action.
      fallback_action?: 'pass' | 'filter' | 'block' // Action for unmatched model names.
      // This text is sent when a usable whitelist remains and its fallback action
      // blocks; other fallback actions omit the message from the outgoing request.
      fallback_error_message?: string // Client message for fallback blocks.
    }>,
  })

  // Fast rules belong to the bulk save and retain draft rows even when their policy card is hidden.
  const openaiFastPolicyForm = reactive({
    rules: [] as OpenAIFastPolicyRule[], // Ordered policy rule drafts.
  })

  // The hydration owner sets this flag only after receiving the Fast policy field; until then
  // the payload builder omits the draft so a failed initial read cannot erase saved rules.
  const openaiFastPolicyLoaded = ref(false)

  // These API-owned platform names determine which provider pause thresholds the
  // settings page renders in its shared form.
  const schedulingThresholdPlatforms = SCHEDULING_THRESHOLD_PLATFORMS

  /**
   * quotaThresholdToPercent converts a positive API fraction to a displayed
   * percentage. Zero, negative, and missing thresholds become null so disabled
   * thresholds appear empty; positive values round to one decimal place.
   */
  function quotaThresholdToPercent(value: number | undefined): number | null {
    if (!value || value <= 0) return null
    // Round display percentages to tenths while retaining the stored fractional
    // threshold in the shared form.
    return Math.round(value * 1000) / 10
  }

  /**
   * percentToQuotaThreshold converts a positive displayed percentage to an API
   * fraction. Empty, zero, and negative inputs produce zero, which the quota
   * configuration uses as the disabled threshold value.
   */
  function percentToQuotaThreshold(value: number | null): number {
    // Positive percentages become fractions; zero also represents a disabled threshold
    // in the persisted quota configuration.
    return value != null && value > 0 ? value / 100 : 0
  }

  // Both quota controls bridge stored fractions and displayed percentages; null inputs write zero,
  // and reading a nonpositive threshold produces an empty control for the disabled state.
  const openAIQuotaAutoPause5hPercent = computed<number | null>({
    /**
     * get reads the quota fraction as a display percentage using the shared
     * conversion rule. A disabled threshold produces null so the numeric
     * editor renders an empty field.
     */
    get() {
      return quotaThresholdToPercent(form.openai_provider_quota_auto_pause?.default_threshold_5h)
    },
    /**
     * set converts the displayed percentage into the shared quota fraction.
     * Null and nonpositive input become zero, allowing the bulk settings
     * serializer to send the disabled threshold value.
     */
    set(value) {
      form.openai_provider_quota_auto_pause.default_threshold_5h = percentToQuotaThreshold(value)
    },
  })

  const openAIQuotaAutoPause7dPercent = computed<number | null>({
    /**
     * get reads the quota fraction as a display percentage using the shared
     * conversion rule. A disabled threshold produces null so the numeric
     * editor renders an empty field.
     */
    get() {
      return quotaThresholdToPercent(form.openai_provider_quota_auto_pause?.default_threshold_7d)
    },
    /**
     * set converts the displayed percentage into the shared quota fraction.
     * Null and nonpositive input become zero, allowing the bulk settings
     * serializer to send the disabled threshold value.
     */
    set(value) {
      form.openai_provider_quota_auto_pause.default_threshold_7d = percentToQuotaThreshold(value)
    },
  })

  // Grok default endpoint modes use stable API values while their displayed labels track
  // the active application locale.
  const grokDefaultBaseURLOptions = computed(() => [
    {
      value: 'cli',
      label: t('admin.settings.gatewayForwarding.grokBaseURLModeCLI'),
    },
    {
      value: 'api',
      label: t('admin.settings.gatewayForwarding.grokBaseURLModeAPI'),
    },
    {
      value: 'us-east-1',
      label: t('admin.settings.gatewayForwarding.grokBaseURLModeUSEast1'),
    },
    {
      value: 'us-west-2',
      label: t('admin.settings.gatewayForwarding.grokBaseURLModeUSWest2'),
    },
    {
      value: 'eu-west-1',
      label: t('admin.settings.gatewayForwarding.grokBaseURLModeEUWest1'),
    },
  ])

  // Replacement modes expose static text, timezone names, and formatted current time
  // through the shared prompt rule editor.
  const userPromptReplacementTypeOptions = computed(() => [
    {
      value: 'static',
      label: t('admin.settings.userPromptReplacement.typeStatic'),
    },
    {
      value: 'timezone_name',
      label: t('admin.settings.userPromptReplacement.typeTimezoneName'),
    },
    {
      value: 'current_time',
      label: t('admin.settings.userPromptReplacement.typeCurrentTime'),
    },
  ])

  // Timezone suggestions populate the prompt rule selector with IANA names and UTC
  // accepted by the backend formatter.
  const userPromptReplacementTimezoneOptions = [
    { value: 'Asia/Tokyo', label: 'Asia/Tokyo' },
    { value: 'Asia/Shanghai', label: 'Asia/Shanghai' },
    { value: 'UTC', label: 'UTC' },
    { value: 'America/Los_Angeles', label: 'America/Los_Angeles' },
    { value: 'America/New_York', label: 'America/New_York' },
    { value: 'Europe/London', label: 'Europe/London' },
  ]

  /**
   * addUserPromptReplacementRule appends a default replacement rule to the shared
   * draft.
   */
  function addUserPromptReplacementRule(): void {
    form.user_prompt_replacement_config.rules.push(createUserPromptReplacementRule())
  }

  /**
   * removeUserPromptReplacementRule removes the selected replacement rule from the
   * shared draft.
   */
  function removeUserPromptReplacementRule(index: number): void {
    form.user_prompt_replacement_config.rules.splice(index, 1)
  }

  /**
   * resetUserPromptReplacementRules replaces the configuration with the default
   * replacement rule set.
   */
  function resetUserPromptReplacementRules(): void {
    form.user_prompt_replacement_config = defaultUserPromptReplacementConfig()
  }

  /**
   * advancedSchedulerPlaceholder formats a server effective scheduler value for an
   * empty override input. It trims the effective value and uses the supplied display
   * default when the server value is missing or blank.
   */
  const advancedSchedulerPlaceholder = (
    effectiveKey: AdvancedSchedulerEffectiveKey,
    fallbackValue: string,
  ) => {
    // Blank server values use the supplied display default so an unset override still shows
    // a usable example while leaving the actual draft empty for deployment-default inheritance.
    const effectiveValue = String((form as Record<string, unknown>)[effectiveKey] ?? '').trim()
    return t('admin.settings.scheduling.defaultPlaceholder', {
      value: effectiveValue || fallbackValue,
    })
  }

  // Each scheduler field binds an override key to its translated label and server
  // effective placeholder for blank inputs.
  const advancedSchedulerWeightFields = computed<
    Array<{
      key: AdvancedSchedulerOverrideKey // Editable scheduler override field.
      label: string // Translated control display label.
      placeholder: string // Formatted effective scheduler default.
    }>
  >(() => {
    return [
      {
        key: 'advanced_scheduler_lb_top_k',
        label: t('admin.settings.scheduling.topKLabel'),
        placeholder: advancedSchedulerPlaceholder('advanced_scheduler_effective_lb_top_k', '7'),
      },
      {
        key: 'advanced_scheduler_weight_priority',
        label: t('admin.settings.scheduling.priorityWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_priority',
          '1',
        ),
      },
      {
        key: 'advanced_scheduler_weight_load',
        label: t('admin.settings.scheduling.loadWeight'),
        placeholder: advancedSchedulerPlaceholder('advanced_scheduler_effective_weight_load', '1'),
      },
      {
        key: 'advanced_scheduler_weight_queue',
        label: t('admin.settings.scheduling.queueWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_queue',
          '0.7',
        ),
      },
      {
        key: 'advanced_scheduler_weight_error_rate',
        label: t('admin.settings.scheduling.errorRateWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_error_rate',
          '0.8',
        ),
      },
      {
        key: 'advanced_scheduler_weight_ttft',
        label: t('admin.settings.scheduling.ttftWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_ttft',
          '0.5',
        ),
      },
      {
        key: 'advanced_scheduler_weight_reset',
        label: t('admin.settings.scheduling.resetWeight'),
        placeholder: advancedSchedulerPlaceholder('advanced_scheduler_effective_weight_reset', '0'),
      },
      {
        key: 'advanced_scheduler_weight_quota_headroom',
        label: t('admin.settings.scheduling.quotaHeadroomWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_quota_headroom',
          '0',
        ),
      },
      {
        key: 'advanced_scheduler_weight_previous_response',
        label: t('admin.settings.scheduling.previousResponseWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_previous_response',
          '5',
        ),
      },
      {
        key: 'advanced_scheduler_weight_session_sticky',
        label: t('admin.settings.scheduling.sessionStickyWeight'),
        placeholder: advancedSchedulerPlaceholder(
          'advanced_scheduler_effective_weight_session_sticky',
          '3',
        ),
      },
    ]
  })

  /**
   * normalizeUserPromptReplacementConfigForSave returns normalized replacement rules
   * for the bulk payload. It first applies the shared draft normalizer, then trims
   * patterns, floors capture-group indices to nonnegative integers, and supplies
   * missing identifiers and replacement defaults. The returned configuration is
   * separate from the shared draft, so save construction does not rewrite the visible
   * rule fields. Regex validity and timezone interpretation remain server validation
   * responsibilities.
   */
  function normalizeUserPromptReplacementConfigForSave(): UserPromptReplacementConfig {
    const config = normalizeUserPromptReplacementConfig(form.user_prompt_replacement_config)
    // Normalize copied rules for transmission: missing identities use stable row positions and
    // capture groups become nonnegative integers before the backend validates regex and
    // timezone input.
    return {
      enabled: config.enabled,
      rules: config.rules.map((rule, index) => ({
        id: rule.id || `rule-${index + 1}`,
        name: rule.name || rule.id || `Rule ${index + 1}`,
        enabled: rule.enabled !== false,
        pattern: rule.pattern.trim(),
        target_group: Math.max(0, Math.floor(Number(rule.target_group) || 0)),
        replacement_type: normalizeUserPromptReplacementType(rule.replacement_type),
        scope: rule.scope || '',
        static_text: rule.static_text || '',
        timezone: rule.timezone || 'Asia/Tokyo',
        time_format: rule.time_format || '2006-01-02',
      })),
    }
  }

  /**
   * loadOllamaCloudUsageSettings reads Ollama Cloud polling settings into the existing
   * draft. Read failure retains the current local values and is silent. After
   * releasing the loading indicator, it waits for field normalization before recording
   * the ollamaCloudUsage snapshot used by the page dirty tracker.
   */
  async function loadOllamaCloudUsageSettings() {
    ollamaCloudUsageLoading.value = true
    try {
      Object.assign(ollamaCloudUsageForm, await adminAPI.providers.getOllamaCloudUsageSettings())
    } catch (_error: unknown) {
      // Keep the current polling draft on failure; its initial disabled state lets the
      // administrator continue editing other settings while this optional endpoint is unavailable.
    } finally {
      ollamaCloudUsageLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('ollamaCloudUsage')
  }

  /**
   * saveOllamaCloudUsageSettings writes the polling switch, periodic interval, and
   * usage debounce from the Ollama draft. The server response updates the same
   * reactive object displayed by the controls. After Vue applies any field
   * normalization, the polling snapshot becomes clean, allowing the page registry to
   * distinguish this successful target from another failed settings save. An API error
   * displays the polling-specific message and returns false while retaining the draft.
   */
  async function saveOllamaCloudUsageSettings(): Promise<boolean> {
    try {
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.providers.updateOllamaCloudUsageSettings({
        ...ollamaCloudUsageForm,
      })
      Object.assign(ollamaCloudUsageForm, updated)
      await nextTick()
      markSettingsClean('ollamaCloudUsage')
      return true
    } catch (error: unknown) {
      appStore.showError(
        extractApiErrorMessage(error, t('admin.settings.ollamaCloudUsage.saveFailed')),
      )
      return false
    }
  }

  /**
   * loadOverloadCooldownSettings reads overload cooldown settings into the existing
   * draft. Read failure retains the current local values and is silent. After
   * releasing the loading indicator, it waits for field normalization before recording
   * the overloadCooldown snapshot used by the page dirty tracker.
   */
  async function loadOverloadCooldownSettings() {
    overloadCooldownLoading.value = true
    try {
      const settings = await adminAPI.settings.getOverloadCooldownSettings()
      Object.assign(overloadCooldownForm, settings)
    } catch (_error: unknown) {
      // This optional read leaves the current draft available on failure; initial calls
      // retain local defaults and later calls retain the values already being edited.
    } finally {
      overloadCooldownLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('overloadCooldown')
  }

  /**
   * saveOverloadCooldownSettings persists the overload switch and cooldown duration in
   * minutes. The response replaces those values in the existing draft before its
   * snapshot is recorded. Waiting for Vue normalization makes the clean state describe
   * the displayed controls, so the page registry can continue saving other targets
   * without leaving this confirmed write dirty. Failure displays the overload-specific
   * error, retains the editable values, and returns false.
   */
  async function saveOverloadCooldownSettings(): Promise<boolean> {
    try {
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.settings.updateOverloadCooldownSettings({
        enabled: overloadCooldownForm.enabled,
        cooldown_minutes: overloadCooldownForm.cooldown_minutes,
      })
      Object.assign(overloadCooldownForm, updated)
      await nextTick()
      markSettingsClean('overloadCooldown')
      return true
    } catch (error: unknown) {
      appStore.showError(
        extractApiErrorMessage(error, t('admin.settings.overloadCooldown.saveFailed')),
      )
      return false
    }
  }

  /**
   * loadOpenAI403CooldownSettings reads OpenAI OAuth permission cooldown settings into
   * the existing draft. Read failure retains the current local values and is silent.
   * After releasing the loading indicator, it waits for field normalization before
   * recording the openAI403Cooldown snapshot used by the page dirty tracker.
   */
  async function loadOpenAI403CooldownSettings() {
    openAI403CooldownLoading.value = true
    try {
      const settings = await adminAPI.settings.getOpenAI403CooldownSettings()
      Object.assign(openAI403CooldownForm, settings)
    } catch (_error: unknown) {
      // This optional read leaves the current draft available on failure; initial calls
      // retain local defaults and later calls retain the values already being edited.
    } finally {
      openAI403CooldownLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('openAI403Cooldown')
  }

  /**
   * saveOpenAI403CooldownSettings writes the OpenAI permission cooldown and repeated-
   * failure escalation configuration together. The request includes the escalation
   * switch, threshold count, and counting window even while the switch hides those
   * controls. A confirmed response updates the draft before Vue settles and the
   * snapshot becomes clean. The boolean result lets the page registry track this
   * endpoint independently; errors display the permission-cooldown message and keep
   * current values editable.
   */
  async function saveOpenAI403CooldownSettings(): Promise<boolean> {
    try {
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.settings.updateOpenAI403CooldownSettings({
        enabled: openAI403CooldownForm.enabled,
        cooldown_minutes: openAI403CooldownForm.cooldown_minutes,
        error_on_threshold_enabled: openAI403CooldownForm.error_on_threshold_enabled,
        threshold_count: openAI403CooldownForm.threshold_count,
        threshold_window_minutes: openAI403CooldownForm.threshold_window_minutes,
      })
      Object.assign(openAI403CooldownForm, updated)
      await nextTick()
      markSettingsClean('openAI403Cooldown')
      return true
    } catch (error: unknown) {
      appStore.showError(
        extractApiErrorMessage(error, t('admin.settings.openAI403Cooldown.saveFailed')),
      )
      return false
    }
  }

  /**
   * loadRateLimit429CooldownSettings reads rate-limit cooldown settings into the
   * existing draft. Read failure retains the current local values and is silent. After
   * releasing the loading indicator, it waits for field normalization before recording
   * the rateLimit429Cooldown snapshot used by the page dirty tracker.
   */
  async function loadRateLimit429CooldownSettings() {
    rateLimit429CooldownLoading.value = true
    try {
      const settings = await adminAPI.settings.getRateLimit429CooldownSettings()
      Object.assign(rateLimit429CooldownForm, settings)
    } catch (_error: unknown) {
      // This optional read leaves the current draft available on failure; initial calls
      // retain local defaults and later calls retain the values already being edited.
    } finally {
      rateLimit429CooldownLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('rateLimit429Cooldown')
  }

  /**
   * saveRateLimit429CooldownSettings writes the rate-limit switch and cooldown
   * measured in seconds. The endpoint response updates the reactive draft that remains
   * mounted across gateway categories. Snapshot marking follows a Vue tick so numeric
   * input normalization participates in the confirmed baseline used by the page save
   * registry. The coordinator receives true after that baseline is recorded, or false
   * with a translated rate-limit error when the write fails.
   */
  async function saveRateLimit429CooldownSettings(): Promise<boolean> {
    try {
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.settings.updateRateLimit429CooldownSettings({
        enabled: rateLimit429CooldownForm.enabled,
        cooldown_seconds: rateLimit429CooldownForm.cooldown_seconds,
      })
      Object.assign(rateLimit429CooldownForm, updated)
      await nextTick()
      markSettingsClean('rateLimit429Cooldown')
      return true
    } catch (error: unknown) {
      appStore.showError(
        extractApiErrorMessage(error, t('admin.settings.rateLimit429Cooldown.saveFailed')),
      )
      return false
    }
  }

  /**
   * loadStreamTimeoutSettings reads stream timeout escalation settings into the
   * existing draft. Read failure retains the current local values and is silent. After
   * releasing the loading indicator, it waits for field normalization before recording
   * the streamTimeout snapshot used by the page dirty tracker.
   */
  async function loadStreamTimeoutSettings() {
    streamTimeoutLoading.value = true
    try {
      const settings = await adminAPI.settings.getStreamTimeoutSettings()
      Object.assign(streamTimeoutForm, settings)
    } catch (_error: unknown) {
      // This optional read leaves the current draft available on failure; initial calls
      // retain local defaults and later calls retain the values already being edited.
    } finally {
      streamTimeoutLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('streamTimeout')
  }

  /**
   * saveStreamTimeoutSettings submits the timeout switch, selected action, pause
   * duration, and escalation threshold window together. Values hidden by the current
   * action remain included in the request. The confirmed server response is adopted
   * before Vue field normalization and snapshot marking, so the page registry tracks
   * the saved editor state. When the endpoint fails, the timeout-specific error is
   * displayed and false keeps this target unsuccessful while its fields remain
   * editable.
   */
  async function saveStreamTimeoutSettings(): Promise<boolean> {
    try {
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.settings.updateStreamTimeoutSettings({
        enabled: streamTimeoutForm.enabled,
        action: streamTimeoutForm.action,
        temp_unsched_minutes: streamTimeoutForm.temp_unsched_minutes,
        threshold_count: streamTimeoutForm.threshold_count,
        threshold_window_minutes: streamTimeoutForm.threshold_window_minutes,
      })
      Object.assign(streamTimeoutForm, updated)
      await nextTick()
      markSettingsClean('streamTimeout')
      return true
    } catch (error: unknown) {
      appStore.showError(
        extractApiErrorMessage(error, t('admin.settings.streamTimeout.saveFailed')),
      )
      return false
    }
  }

  /**
   * loadRectifierSettings reads request rectification settings into the existing
   * draft. Read failure retains the current local values and is silent. After
   * releasing the loading indicator, it waits for field normalization before recording
   * the rectifier snapshot used by the page dirty tracker.
   */
  async function loadRectifierSettings() {
    rectifierLoading.value = true
    try {
      const settings = await adminAPI.settings.getRectifierSettings()
      Object.assign(rectifierForm, settings)
      // Older responses can represent an absent pattern list as null; normalize that
      // shape before the list editor and future saves invoke array operations on it.
      if (!Array.isArray(rectifierForm.apikey_signature_patterns)) {
        rectifierForm.apikey_signature_patterns = []
      }
    } catch (_error: unknown) {
      // This optional read leaves the current draft available on failure; initial calls
      // retain local defaults and later calls retain the values already being edited.
    } finally {
      rectifierLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('rectifier')
  }

  /**
   * saveRectifierSettings submits repair switches and nonblank custom signature
   * patterns to the rectifier endpoint. Blank rows are omitted from the request, while
   * retained patterns keep their entered text. A confirmed response replaces draft
   * values, and a non-array response pattern list becomes empty before the editor
   * renders it. Snapshot marking waits for field normalization; errors retain the
   * draft, display the rectifier message, and return false to the save coordinator.
   */
  async function saveRectifierSettings(): Promise<boolean> {
    try {
      // Empty editor rows do not express a match pattern; filter them from the
      // request while retaining the text of populated patterns for server interpretation.
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.settings.updateRectifierSettings({
        enabled: rectifierForm.enabled,
        thinking_signature_enabled: rectifierForm.thinking_signature_enabled,
        thinking_budget_enabled: rectifierForm.thinking_budget_enabled,
        apikey_signature_enabled: rectifierForm.apikey_signature_enabled,
        apikey_signature_patterns: rectifierForm.apikey_signature_patterns.filter(
          (p) => p.trim() !== '',
        ),
      })
      Object.assign(rectifierForm, updated)
      // Older responses can represent an absent pattern list as null; normalize that
      // shape before the list editor and future saves invoke array operations on it.
      if (!Array.isArray(rectifierForm.apikey_signature_patterns)) {
        rectifierForm.apikey_signature_patterns = []
      }
      await nextTick()
      markSettingsClean('rectifier')
      return true
    } catch (error: unknown) {
      appStore.showError(extractApiErrorMessage(error, t('admin.settings.rectifier.saveFailed')))
      return false
    }
  }

  // Beta policy action and scope choices use server values while computed labels follow
  // changes to the application locale.
  const betaPolicyActionOptions = computed(() => [
    { value: 'pass', label: t('admin.settings.betaPolicy.actionPass') },
    { value: 'filter', label: t('admin.settings.betaPolicy.actionFilter') },
    { value: 'block', label: t('admin.settings.betaPolicy.actionBlock') },
  ])

  const betaPolicyScopeOptions = computed(() => [
    { value: 'all', label: t('admin.settings.betaPolicy.scopeAll') },
    { value: 'oauth', label: t('admin.settings.betaPolicy.scopeOAuth') },
    { value: 'apikey', label: t('admin.settings.betaPolicy.scopeAPIKey') },
    { value: 'bedrock', label: t('admin.settings.betaPolicy.scopeBedrock') },
  ])

  // Known beta tokens receive short labels, while getBetaDisplayName displays unknown tokens
  // literally so server-provided rules remain identifiable in the policy editor.
  const betaDisplayNames: Record<string, string> = {
    'fast-mode-2026-02-01': 'Fast Mode',
    'context-1m-2025-08-07': 'Context 1M',
  }

  // betaPresets groups action and model suggestions by beta token; applyBetaPreset copies
  // model_whitelist into the selected rule so later edits leave the suggestion reusable.
  const betaPresets: Record<
    string,
    Array<{
      label: string // Translated control display label.
      description: string // Translated preset behavior explanation.
      action: 'pass' | 'filter' | 'block' // Preset beta token action.
      model_whitelist: string[] // Preset target model patterns.
      fallback_action: 'pass' | 'filter' | 'block' // Preset unmatched model action.
    }>
  > = {
    'context-1m-2025-08-07': [
      {
        label: t('admin.settings.betaPolicy.presetOpusOnly'),
        description: t('admin.settings.betaPolicy.presetOpusOnlyDesc'),
        action: 'pass',
        model_whitelist: ['claude-opus-4-6'],
        fallback_action: 'filter',
      },
    ],
  }

  // These suggested Claude patterns are appended through addQuickPattern, which prevents
  // duplicate entries within the target rule whitelist.
  const commonModelPatterns = [
    'claude-opus-4-6',
    'claude-sonnet-4-6',
    'claude-opus-*',
    'claude-sonnet-*',
  ]

  /**
   * getBetaDisplayName returns a known beta label or the token itself. Unknown server
   * tokens remain editable because their literal identifier supplies a display name
   * when no friendly label exists.
   */
  function getBetaDisplayName(token: string): string {
    return betaDisplayNames[token] || token
  }

  /**
   * applyBetaPreset assigns a preset action and fallback to an existing beta rule. Its
   * whitelist is copied so later edits affect that rule and cannot change the reusable
   * preset array.
   */
  function applyBetaPreset(
    rule: (typeof betaPolicyForm.rules)[number],
    preset: {
      action: 'pass' | 'filter' | 'block' // Preset beta token action.
      // applyBetaPreset copies these patterns before assigning the rule so subsequent
      // editor changes cannot mutate the source preset's reusable model list.
      model_whitelist: string[] // Preset target model patterns.
      fallback_action: 'pass' | 'filter' | 'block' // Preset unmatched model action.
    },
  ) {
    rule.action = preset.action
    rule.model_whitelist = [...preset.model_whitelist]
    rule.fallback_action = preset.fallback_action
  }

  /**
   * addQuickPattern appends a suggested model pattern when the rule lacks it. A
   * missing whitelist is initialized before insertion, and existing exact pattern
   * matches keep their current position in the list.
   */
  function addQuickPattern(rule: (typeof betaPolicyForm.rules)[number], pattern: string) {
    if (!rule.model_whitelist) rule.model_whitelist = []
    if (!rule.model_whitelist.includes(pattern)) {
      rule.model_whitelist.push(pattern)
    }
  }

  /**
   * loadBetaPolicySettings reads Anthropic beta policy settings into the existing
   * draft. Read failure retains the current local values and is silent. After
   * releasing the loading indicator, it waits for field normalization before recording
   * the betaPolicy snapshot used by the page dirty tracker.
   */
  async function loadBetaPolicySettings() {
    betaPolicyLoading.value = true
    try {
      const settings = await adminAPI.settings.getBetaPolicySettings()
      betaPolicyForm.rules = settings.rules
    } catch (_error: unknown) {
      // This optional read leaves the current draft available on failure; initial calls
      // retain local defaults and later calls retain the values already being edited.
    } finally {
      betaPolicyLoading.value = false
    }
    // Controls can normalize values after the response assignment; defer the baseline until
    // Vue applies those changes so initial loading does not appear as an unsaved edit.
    await nextTick()
    markSettingsClean('betaPolicy')
  }

  // Fast policy tier and action menus use stable API values with translated labels for
  // the editable rule rows.
  const openaiFastPolicyTierOptions = computed(() => [
    { value: 'all', label: t('admin.settings.openaiFastPolicy.tierAll') },
    {
      value: 'priority',
      label: t('admin.settings.openaiFastPolicy.tierPriority'),
    },
    {
      value: 'ultrafast',
      label: t('admin.settings.openaiFastPolicy.tierUltrafast'),
    },
    { value: 'flex', label: t('admin.settings.openaiFastPolicy.tierFlex') },
  ])

  const openaiFastPolicyActionOptions = computed(() => [
    { value: 'pass', label: t('admin.settings.openaiFastPolicy.actionPass') },
    { value: 'filter', label: t('admin.settings.openaiFastPolicy.actionFilter') },
    {
      value: 'force_priority',
      label: t('admin.settings.openaiFastPolicy.actionForcePriority'),
    },
    { value: 'force_ultrafast', label: t('admin.settings.openaiFastPolicy.actionForceUltrafast') },
    { value: 'block', label: t('admin.settings.openaiFastPolicy.actionBlock') },
  ])

  /**
   * openaiFastPolicyActionSummary translates the selected action for a collapsed rule
   * summary.
   */
  function openaiFastPolicyActionSummary(action: OpenAIFastPolicyRule['action']) {
    return t(`admin.settings.openaiFastPolicy.summaryAction.${action}`)
  }

  /**
   * hasOpenAIFastPolicyTargetModels reports whether any model pattern contains
   * non-whitespace text.
   */
  function hasOpenAIFastPolicyTargetModels(rule: OpenAIFastPolicyRule) {
    return Boolean(rule.model_whitelist?.some((pattern) => pattern.trim() !== ''))
  }

  // Authentication scope choices apply to each Fast rule and keep the saved values
  // independent of translated dropdown labels.
  const openaiFastPolicyScopeOptions = computed(() => [
    { value: 'all', label: t('admin.settings.openaiFastPolicy.scopeAll') },
    { value: 'oauth', label: t('admin.settings.openaiFastPolicy.scopeOAuth') },
    { value: 'apikey', label: t('admin.settings.openaiFastPolicy.scopeAPIKey') },
    {
      value: 'bedrock',
      label: t('admin.settings.openaiFastPolicy.scopeBedrock'),
    },
  ])

  /**
   * addOpenAIFastPolicyRule appends a priority-tier filter rule with unrestricted
   * scope. User and model lists begin empty, and fallback behavior starts as pass for
   * later whitelist editing in the policy panel.
   */
  function addOpenAIFastPolicyRule() {
    openaiFastPolicyForm.rules.push({
      service_tier: 'priority',
      action: 'filter',
      scope: 'all',
      user_ids: [],
      error_message: '',
      model_whitelist: [],
      fallback_action: 'pass',
      fallback_error_message: '',
    })
  }

  /**
   * removeOpenAIFastPolicyRule removes the indexed rule from the editable policy list.
   */
  function removeOpenAIFastPolicyRule(index: number) {
    openaiFastPolicyForm.rules.splice(index, 1)
  }

  /**
   * addOpenAIFastPolicyModelPattern appends an empty editable whitelist row to a rule.
   * A missing whitelist becomes an array, and the payload builder later omits rows
   * left empty by the administrator.
   */
  function addOpenAIFastPolicyModelPattern(rule: OpenAIFastPolicyRule) {
    if (!rule.model_whitelist) rule.model_whitelist = []
    rule.model_whitelist.push('')
  }

  /**
   * removeOpenAIFastPolicyModelPattern removes an indexed pattern when the rule has a
   * whitelist.
   */
  function removeOpenAIFastPolicyModelPattern(rule: OpenAIFastPolicyRule, idx: number) {
    rule.model_whitelist?.splice(idx, 1)
  }

  /**
   * saveBetaPolicySettings serializes beta rules and updates their dedicated settings
   * endpoint. Empty model patterns are removed, and fallback fields appear only when
   * usable whitelist entries remain. A fallback block message additionally requires
   * the block action. On success, the server rules replace the draft before field
   * normalization and snapshot marking; on failure, the current rules stay editable
   * and false lets the coordinator report this target unsuccessful.
   */
  async function saveBetaPolicySettings(): Promise<boolean> {
    try {
      // Empty patterns leave no target model set; omit whitelist-dependent fields in that
      // case so the server does not retain a fallback action detached from its condition.
      const cleanedRules = betaPolicyForm.rules.map((rule) => {
        const whitelist = rule.model_whitelist?.filter((p) => p.trim() !== '')
        const hasWhitelist = whitelist && whitelist.length > 0
        return {
          beta_token: rule.beta_token,
          action: rule.action,
          scope: rule.scope,
          error_message: rule.error_message,
          model_whitelist: hasWhitelist ? whitelist : undefined,
          fallback_action: hasWhitelist ? rule.fallback_action || 'pass' : undefined,
          fallback_error_message:
            hasWhitelist && rule.fallback_action === 'block'
              ? rule.fallback_error_message
              : undefined,
        }
      })
      // Persist before adopting the response and marking the snapshot; waiting for Vue
      // normalization makes the clean baseline describe the confirmed values displayed by controls.
      const updated = await adminAPI.settings.updateBetaPolicySettings({
        rules: cleanedRules,
      })
      betaPolicyForm.rules = updated.rules
      await nextTick()
      markSettingsClean('betaPolicy')
      return true
    } catch (error: unknown) {
      appStore.showError(extractApiErrorMessage(error, t('admin.settings.betaPolicy.saveFailed')))
      return false
    }
  }
  return {
    ttftModeOptions,
    ollamaCloudUsageLoading,
    ollamaCloudUsageForm,
    overloadCooldownLoading,
    overloadCooldownForm,
    openAI403CooldownLoading,
    openAI403CooldownForm,
    rateLimit429CooldownLoading,
    rateLimit429CooldownForm,
    streamTimeoutLoading,
    streamTimeoutForm,
    streamTimeoutActionOptions,
    rectifierLoading,
    rectifierForm,
    betaPolicyLoading,
    betaPolicyForm,
    openaiFastPolicyForm,
    openaiFastPolicyLoaded,
    schedulingThresholdPlatforms,
    quotaThresholdToPercent,
    percentToQuotaThreshold,
    openAIQuotaAutoPause5hPercent,
    openAIQuotaAutoPause7dPercent,
    grokDefaultBaseURLOptions,
    userPromptReplacementTypeOptions,
    userPromptReplacementTimezoneOptions,
    addUserPromptReplacementRule,
    removeUserPromptReplacementRule,
    resetUserPromptReplacementRules,
    advancedSchedulerPlaceholder,
    advancedSchedulerWeightFields,
    normalizeUserPromptReplacementConfigForSave,
    loadOllamaCloudUsageSettings,
    saveOllamaCloudUsageSettings,
    loadOverloadCooldownSettings,
    saveOverloadCooldownSettings,
    loadOpenAI403CooldownSettings,
    saveOpenAI403CooldownSettings,
    loadRateLimit429CooldownSettings,
    saveRateLimit429CooldownSettings,
    loadStreamTimeoutSettings,
    saveStreamTimeoutSettings,
    loadRectifierSettings,
    saveRectifierSettings,
    betaPolicyActionOptions,
    betaPolicyScopeOptions,
    betaDisplayNames,
    betaPresets,
    commonModelPatterns,
    getBetaDisplayName,
    applyBetaPreset,
    addQuickPattern,
    loadBetaPolicySettings,
    openaiFastPolicyTierOptions,
    openaiFastPolicyActionOptions,
    openaiFastPolicyActionSummary,
    hasOpenAIFastPolicyTargetModels,
    openaiFastPolicyScopeOptions,
    addOpenAIFastPolicyRule,
    removeOpenAIFastPolicyRule,
    addOpenAIFastPolicyModelPattern,
    removeOpenAIFastPolicyModelPattern,
    saveBetaPolicySettings,
  }
}
