import { buildGatewayPolicyPayload } from './gatewayPolicyPayload'
import { createFeaturesSettingsValidation } from './featuresSettingsValidation'
import { createUsersSettingsValidation } from './usersSettingsValidation'
import { createGeneralSettingsValidation } from './generalSettingsValidation'
import { createGatewaySettingsValidation } from './gatewaySettingsValidation'
import { createAgreementSettingsValidation } from './agreementSettingsValidation'
import { createIdentitySettingsValidation } from './identitySettingsValidation'
import { buildSecuritySettingsPayload } from './securitySettingsPayload'
import { buildAgreementSettingsPayload } from './agreementSettingsPayload'
import { buildUsersSettingsPayload } from './usersSettingsPayload'
import { buildGeneralSettingsPayload } from './generalSettingsPayload'
import { buildIdentitySettingsPayload } from './identitySettingsPayload'
import { buildGatewaySettingsPayload } from './gatewaySettingsPayload'
import { buildFeaturesSettingsPayload } from './featuresSettingsPayload'
import { buildPaymentSettingsPayload } from './paymentSettingsPayload'
import { buildEmailSettingsPayload } from './emailSettingsPayload'
import { appendAuthSourceDefaultsToUpdateRequest } from './settingsFormHelpers'
import type { UpdateSettingsRequest } from '@/api/admin/settings.types'
import type { useGeneralSettings } from './useGeneralSettings'
import type { useAgreementSettings } from './useAgreementSettings'
import type { useSecuritySettings } from './useSecuritySettings'
import type { useIdentitySettings } from './useIdentitySettings'
import type { useUsersSettings } from './useUsersSettings'
import type { useFeaturesSettings } from './useFeaturesSettings'
import type { useGatewaySettings } from './useGatewaySettings'
import type { SettingsForm } from './settingsState'

/**
 * settingsPayload.ts validates the bulk settings draft and assembles domain
 * request fields for the persistence coordinator. Validation stops at the first
 * invalid value in a fixed order, so worker count, account defaults, display
 * limits, model choices, agreements, subscriptions, and identity settings produce
 * predictable feedback when several tabs contain errors. Earlier normalization
 * steps can update the draft before a later validation failure returns.
 *
 * Domain serializers own their field conversions and secret omission rules. The
 * assembler includes Fast policy only after its loaded flag establishes a known
 * configuration, then appends platform thresholds and authentication-source
 * defaults. Returning null prevents the persistence coordinator from starting
 * either the bulk request or its subsequent search write.
 */

/** SettingsPayloadControllers selects the domain actions needed by the bulk settings request conversion. */
export interface SettingsPayloadControllers {
  /** general supplies page-size text, ranking synchronization, and collection normalization consumed while validating and serializing display settings. */
  general: Pick<
    ReturnType<typeof useGeneralSettings>,
    | 'tablePageSizeOptionsInput'
    | 'ensureUsageRankingSortMetricVisible'
    | 'normalizeFooterLinksForSave'
    | 'normalizeHomeFeaturedModelsForSave'
    | 'parseTablePageSizeOptionsInput'
  >
  /** agreement supplies document normalization and duplicate-slug detection before the bulk request includes the editable legal documents. */
  agreement: Pick<
    ReturnType<typeof useAgreementSettings>,
    'normalizeLoginAgreementDocumentsForSave' | 'findDuplicateLoginAgreementDocumentId'
  >
  /** security supplies whitelist draft tags and forwarded-header normalization used to serialize registration and client-address settings. */
  security: Pick<
    ReturnType<typeof useSecuritySettings>,
    'registrationEmailSuffixWhitelistTags' | 'normalizeForwardedClientIpHeaders'
  >
  /** identity synchronizes mutually dependent WeChat mode fields before the request derives the server-facing stored login mode. */
  identity: Pick<ReturnType<typeof useIdentitySettings>, 'syncWeChatConnectMode'>
  /** users supplies authentication-source defaults and subscription duplicate detection before those defaults are appended to the request. */
  users: Pick<
    ReturnType<typeof useUsersSettings>,
    'authSourceDefaults' | 'authSourceDefaultsMeta' | 'findDuplicateDefaultSubscription'
  >
  /** features normalizes creative model selections against available capabilities before a successful request includes worker and model settings. */
  features: Pick<ReturnType<typeof useFeaturesSettings>, 'normalizeCreativeModelSettingsForSave'>
  /** gateway supplies prompt-rule normalization and the separately loaded Fast policy draft needed to decide policy field inclusion. */
  gateway: Pick<
    ReturnType<typeof useGatewaySettings>,
    | 'openaiFastPolicyForm'
    | 'openaiFastPolicyLoaded'
    | 'normalizeUserPromptReplacementConfigForSave'
  >
}

/**
 * createSettingsPayload binds validation and serialization to a page draft.
 * Invoke this factory during setup so translated error feedback is captured
 * there; call its returned builder each time the administrator saves current edits.
 */
export function createSettingsPayload(form: SettingsForm, controllers: SettingsPayloadControllers) {
  /**
   * Selected controller actions serialize editor drafts after the domain validators accept each phase of the bulk update.
   */
  const { normalizeFooterLinksForSave, normalizeHomeFeaturedModelsForSave } = controllers.general
  const { registrationEmailSuffixWhitelistTags, normalizeForwardedClientIpHeaders } =
    controllers.security
  const { authSourceDefaults } = controllers.users
  const { normalizeUserPromptReplacementConfigForSave } = controllers.gateway

  // Each validator captures setup-time services and operates on the same draft.
  // Invoke the returned actions below in sequence so validation remains short-circuiting.
  const featuresValidation = createFeaturesSettingsValidation(form, controllers.features)
  const usersValidation = createUsersSettingsValidation(form, controllers.users)
  const generalValidation = createGeneralSettingsValidation(form, controllers.general)
  const gatewayValidation = createGatewaySettingsValidation(form)
  const agreementValidation = createAgreementSettingsValidation(form, controllers.agreement)
  const identityValidation = createIdentitySettingsValidation(form, controllers.identity)

  /**
   * buildSettingsPayload validates current fields and returns the bulk request,
   * or null after reporting the first invalid value. Validation order is stable
   * across domains and some successful normalization steps update the draft before
   * a later error stops construction. Domain serializers run after all checks
   * succeed. Fast policy is included only when its separate draft was loaded,
   * and authentication-source subscriptions and platform thresholds are appended
   * after the main field object is assembled.
   */
  function buildSettingsPayload(): UpdateSettingsRequest | null {
    const normalizedCreativeWorkerCount = featuresValidation.normalizeWorkerCount()
    if (normalizedCreativeWorkerCount === null) return null
    if (!usersValidation.validateApiKeyLimit()) return null
    if (!generalValidation.normalizeDisplaySettings()) return null
    const normalizedCreativeModelSettings = featuresValidation.normalizeModelSettings()
    if (normalizedCreativeModelSettings === null) return null
    if (!gatewayValidation.validateQuotaThresholds()) return null
    if (!agreementValidation.normalizeDocuments()) return null
    form.forwarded_client_ip_headers = normalizeForwardedClientIpHeaders(
      form.forwarded_client_ip_headers,
    )
    const normalizedDefaultSubscriptions = usersValidation.normalizeSubscriptions()
    if (normalizedDefaultSubscriptions === null) return null
    if (!identityValidation.validateCapabilities()) return null
    generalValidation.normalizeOptionalUrls()
    const wechatStoredMode = identityValidation.normalizeStoredMode()

    const payload: UpdateSettingsRequest = {
      ...buildSecuritySettingsPayload(form, { registrationEmailSuffixWhitelistTags }),
      ...buildAgreementSettingsPayload(form),
      ...buildUsersSettingsPayload(form, normalizedDefaultSubscriptions),
      ...buildGeneralSettingsPayload(form, {
        normalizeFooterLinksForSave,
        normalizeHomeFeaturedModelsForSave,
      }),
      ...buildIdentitySettingsPayload(form, wechatStoredMode),
      ...buildGatewaySettingsPayload(form, { normalizeUserPromptReplacementConfigForSave }),
      ...buildFeaturesSettingsPayload(
        form,
        normalizedCreativeModelSettings,
        normalizedCreativeWorkerCount,
      ),
      ...buildPaymentSettingsPayload(form),
      ...buildEmailSettingsPayload(form),
    }
    Object.assign(payload, buildGatewayPolicyPayload(form, controllers.gateway))

    appendAuthSourceDefaultsToUpdateRequest(payload, authSourceDefaults)
    return payload
  }
  return buildSettingsPayload
}
