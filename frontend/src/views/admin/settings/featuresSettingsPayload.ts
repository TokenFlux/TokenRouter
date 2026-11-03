import type { SettingsForm } from './settingsState'
import type { UpdateSettingsRequest, CreativeModelSetting } from '@/api/admin/settings'

/**
 * featuresSettingsPayload.ts builds feature flags, affiliate values, creative selections,
 * bStats reporting controls, and session-block expiry for bulk saving. The coordinator
 * provides validated creative rows and worker count separately, while this serializer clamps affiliate numeric values
 * and floors session-block expiry while clamping its minimum to one second.
 */

/**
 * buildFeaturesSettingsPayload returns feature request fields with bounded affiliate
 * values and normalized session expiry. Supply the creative rows and worker count accepted
 * by earlier validation; normalization here affects returned values and leaves the draft
 * fields unchanged.
 */
export function buildFeaturesSettingsPayload(
  form: Pick<
    SettingsForm,
    | 'affiliate_enabled'
    | 'affiliate_rebate_rate'
    | 'affiliate_rebate_freeze_hours'
    | 'affiliate_rebate_duration_days'
    | 'affiliate_rebate_per_invitee_cap'
    | 'affiliate_admin_recharge_enabled'
    | 'bstats_enabled'
    | 'bstats_log_errors'
    | 'bstats_log_sent_data'
    | 'bstats_log_response_status_text'
    | 'team_enabled'
    | 'creative_enabled'
    | 'risk_control_enabled'
    | 'cyber_session_block_enabled'
    | 'cyber_session_block_ttl_seconds'
  >,
  normalizedCreativeModelSettings: CreativeModelSetting[],
  normalizedCreativeWorkerCount: number,
): Pick<
  UpdateSettingsRequest,
  | 'affiliate_enabled'
  | 'affiliate_rebate_rate'
  | 'affiliate_rebate_freeze_hours'
  | 'affiliate_rebate_duration_days'
  | 'affiliate_rebate_per_invitee_cap'
  | 'affiliate_admin_recharge_enabled'
  | 'bstats_enabled'
  | 'bstats_log_errors'
  | 'bstats_log_sent_data'
  | 'bstats_log_response_status_text'
  | 'team_enabled'
  | 'creative_enabled'
  | 'creative_model_settings'
  | 'creative_worker_count'
  | 'risk_control_enabled'
  | 'cyber_session_block_enabled'
  | 'cyber_session_block_ttl_seconds'
> {
  return {
    affiliate_enabled: form.affiliate_enabled,

    // Affiliate percentages are bounded to the control range, with invalid
    // numeric input interpreted as zero for this request rather than persisted raw.
    affiliate_rebate_rate: Math.min(100, Math.max(0, Number(form.affiliate_rebate_rate) || 0)),

    // Freeze hours and rebate days use whole bounded durations; flooring and
    // clamping here matches the ranges offered by the feature panel controls.
    affiliate_rebate_freeze_hours: Math.max(
      0,
      Math.min(720, Math.floor(Number(form.affiliate_rebate_freeze_hours) || 0)),
    ),

    affiliate_rebate_duration_days: Math.max(
      0,
      Math.min(3650, Math.floor(Number(form.affiliate_rebate_duration_days) || 0)),
    ),

    // Rebate caps accept fractional amounts but cannot be negative.
    affiliate_rebate_per_invitee_cap: Math.max(
      0,
      Number(form.affiliate_rebate_per_invitee_cap) || 0,
    ),

    affiliate_admin_recharge_enabled: form.affiliate_admin_recharge_enabled,

    // Feature visibility and reporting controls share the same bulk request,
    // allowing the coordinator to persist their edited values in one save attempt.
    team_enabled: form.team_enabled,
    bstats_enabled: form.bstats_enabled,
    bstats_log_errors: form.bstats_log_errors,
    bstats_log_sent_data: form.bstats_log_sent_data,
    bstats_log_response_status_text: form.bstats_log_response_status_text,

    creative_enabled: form.creative_enabled,

    creative_model_settings: normalizedCreativeModelSettings,

    creative_worker_count: normalizedCreativeWorkerCount,

    risk_control_enabled: form.risk_control_enabled,

    cyber_session_block_enabled: form.cyber_session_block_enabled,

    // Missing, nonnumeric, or zero expiry selects one hour. Finite negative
    // values clamp to one second, matching the minimum offered by the expiry input.
    cyber_session_block_ttl_seconds: Math.max(
      1,
      Math.floor(Number(form.cyber_session_block_ttl_seconds) || 3600),
    ),
  }
}
