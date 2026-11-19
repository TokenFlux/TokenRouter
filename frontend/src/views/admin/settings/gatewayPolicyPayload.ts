import type { UpdateSettingsRequest } from '@/api/admin/settings.types'
import { sanitizeProviderSchedulingThresholdsMap } from './settingsFormHelpers'
import type { SettingsForm } from './settingsState'
import type { useGatewaySettings } from './useGatewaySettings'

/**
 * gatewayPolicyPayload.ts serializes loaded Fast policy rules and platform
 * scheduling thresholds for the administrator bulk update. A policy field that
 * has not loaded remains omitted, allowing another settings tab to save while
 * the server retains its current rules. A loaded policy can deliberately submit
 * an empty rule list, which remains distinct from an unknown configuration.
 *
 * Each rule trims empty model patterns before selecting whitelist-dependent
 * fallback fields. User identifiers are copied, and block messages follow their
 * corresponding action. Scheduling thresholds pass through the API owner's
 * sanitizer after policy construction, retaining its platform-specific optional
 * field rules in the final request object.
 */

/**
 * buildGatewayPolicyPayload returns known Fast policy fields and sanitized
 * scheduling thresholds for the bulk settings request. Call it after domain
 * validation so shared draft values already reflect accepted control input.
 * An unloaded policy is omitted; a loaded empty list is serialized as empty.
 * Whitelist-dependent fallback fields are included when nonempty model patterns
 * remain, and their error message is included when the fallback action blocks.
 * User identifiers are copied so the request cannot share the draft selection array.
 */
export function buildGatewayPolicyPayload(
  form: Pick<SettingsForm, 'provider_scheduling_thresholds'>,
  actions: Pick<
    ReturnType<typeof useGatewaySettings>,
    'openaiFastPolicyForm' | 'openaiFastPolicyLoaded'
  >,
): Pick<UpdateSettingsRequest, 'openai_fast_policy_settings' | 'provider_scheduling_thresholds'> {
  const { openaiFastPolicyForm, openaiFastPolicyLoaded } = actions
  const payload: UpdateSettingsRequest = {}

  // A missing initial policy response leaves its draft unknown. Omit that
  // field until loaded so saving another tab retains the server policy rules.
  if (openaiFastPolicyLoaded.value) {
    payload.openai_fast_policy_settings = {
      rules: openaiFastPolicyForm.rules.map((rule) => {
        const whitelist = (rule.model_whitelist || []).map((p) => p.trim()).filter((p) => p !== '')
        // Empty patterns disable whitelist-dependent behavior, so omit both
        // fallback fields unless a nonempty target list survives trimming here.
        const hasWhitelist = whitelist.length > 0
        return {
          service_tier: rule.service_tier,
          action: rule.action,
          scope: rule.scope,
          user_ids: rule.user_ids && rule.user_ids.length > 0 ? [...rule.user_ids] : undefined,
          error_message: rule.action === 'block' ? rule.error_message : undefined,
          model_whitelist: hasWhitelist ? whitelist : undefined,
          fallback_action: hasWhitelist ? rule.fallback_action || 'pass' : undefined,
          fallback_error_message:
            hasWhitelist && rule.fallback_action === 'block'
              ? rule.fallback_error_message
              : undefined,
        }
      }),
    }
  }

  payload.provider_scheduling_thresholds = sanitizeProviderSchedulingThresholdsMap(
    form.provider_scheduling_thresholds,
  )
  return payload
}
