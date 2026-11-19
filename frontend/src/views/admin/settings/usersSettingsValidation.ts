import { useI18n } from 'vue-i18n'
import { normalizeDefaultSubscriptionSettings } from './settingsFormHelpers'
import { useAppStore } from '@/stores'
import { MAX_USER_API_KEY_LIMIT } from '@/constants/user'
import type { useUsersSettings } from './useUsersSettings'
import type { SettingsForm } from './settingsState'
import type { DefaultSubscriptionSetting } from '@/api/admin/settings.types'

/**
 * usersSettingsValidation.ts validates the default API key limit and prepares
 * subscription grants for the ordered bulk save. Each operation runs when the page
 * reaches that domain phase, so earlier validation failures prevent later draft
 * edits. The API key limit becomes a number after its integer and range checks
 * succeed.
 *
 * Global subscription normalization returns a separate array. Authentication-source
 * subscriptions are normalized into their shared drafts one source at a time, and
 * duplicate detection stops at the first failing list. Those earlier source edits
 * remain when a later source fails, making invocation order part of the
 * administrator-visible draft behavior.
 */

/**
 * createUsersSettingsValidation binds user-default checks to one shared draft and
 * its source-grant controller. Create it during component setup, then invoke the
 * returned operations in the bulk-save sequence so errors and normalization occur at
 * their assigned phases.
 */
export function createUsersSettingsValidation(
  form: Pick<SettingsForm, 'default_user_api_key_limit' | 'default_subscriptions'>,
  actions: Pick<
    ReturnType<typeof useUsersSettings>,
    'authSourceDefaults' | 'authSourceDefaultsMeta' | 'findDuplicateDefaultSubscription'
  >,
) {
  /**
   * t and appStore report field errors, while the source drafts, display order, and
   * duplicate finder come from the existing users controller.
   */
  const { t } = useI18n()
  const appStore = useAppStore()
  const { authSourceDefaults, authSourceDefaultsMeta, findDuplicateDefaultSubscription } = actions

  /**
   * validateApiKeyLimit accepts a nonblank safe integer between zero and
   * MAX_USER_API_KEY_LIMIT, inclusive. It writes the numeric value into the draft
   * after success; invalid input reports an error and leaves the field unchanged.
   */
  function validateApiKeyLimit(): boolean {
    const rawDefaultUserAPIKeyLimit = String(form.default_user_api_key_limit).trim()

    const normalizedDefaultUserAPIKeyLimit = Number(rawDefaultUserAPIKeyLimit)

    if (
      rawDefaultUserAPIKeyLimit === '' ||
      !Number.isSafeInteger(normalizedDefaultUserAPIKeyLimit) ||
      normalizedDefaultUserAPIKeyLimit < 0 ||
      normalizedDefaultUserAPIKeyLimit > MAX_USER_API_KEY_LIMIT
    ) {
      appStore.showError(t('admin.settings.defaults.defaultUserApiKeyLimitInvalid'))
      return false
    }

    form.default_user_api_key_limit = normalizedDefaultUserAPIKeyLimit
    return true
  }

  /**
   * normalizeSubscriptions returns normalized global subscriptions or null after
   * reporting the first duplicate plan. The global list is normalized into a
   * separate array, while each authentication-source list is replaced in its shared
   * draft before that source is checked.
   *
   * Sources follow authSourceDefaultsMeta order, including sources whose signup
   * grant is disabled. If a later list contains duplicates, earlier source
   * normalizations and the failing source replacement remain visible. Callers stop
   * payload construction on null and use the returned global array when every list
   * passes.
   */
  function normalizeSubscriptions(): DefaultSubscriptionSetting[] | null {
    // Normalize compatibility identifiers before duplicate detection so entries that
    // resolve to the same current plan cannot both pass validation.
    const normalizedDefaultSubscriptions = normalizeDefaultSubscriptionSettings(
      form.default_subscriptions,
    )

    const duplicateDefaultSubscription = findDuplicateDefaultSubscription(
      normalizedDefaultSubscriptions,
    )

    if (duplicateDefaultSubscription) {
      appStore.showError(
        t('admin.settings.defaults.defaultSubscriptionsDuplicate', {
          planId: duplicateDefaultSubscription.plan_id,
        }),
      )
      return null
    }

    // Replace source drafts as validation advances; a later duplicate returns null
    // without undoing already normalized source lists or the failing replacement.
    for (const authSource of authSourceDefaultsMeta.value) {
      authSourceDefaults[authSource.source].subscriptions = normalizeDefaultSubscriptionSettings(
        authSourceDefaults[authSource.source].subscriptions,
      )
      const duplicate = findDuplicateDefaultSubscription(
        authSourceDefaults[authSource.source].subscriptions,
      )
      if (duplicate) {
        appStore.showError(
          `${authSource.title}: ${t('admin.settings.defaults.defaultSubscriptionsDuplicate', {
            planId: duplicate.plan_id,
          })}`,
        )
        return null
      }
    }
    return normalizedDefaultSubscriptions
  }
  /**
   * The returned checks let bulk-save orchestration preserve first-error ordering
   * while controlling when numeric and subscription draft normalization happens.
   */
  return { validateApiKeyLimit, normalizeSubscriptions }
}
