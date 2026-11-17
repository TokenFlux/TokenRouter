import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { SystemSettings, UpdateSettingsRequest } from '@/api/admin/settings'
import { useAppStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import {
  useStepUp,
  isStepUpCancelled,
  isStepUpBlocked,
  stepUpBlockReason,
} from '@/composables/useStepUp'
import { extractApiErrorMessage } from '@/utils/apiError'

/**
 * settingsPersistence.ts coordinates the administrator bulk settings request and
 * its separate web search update. The bulk request completes through step-up
 * before search saves, and both settings stores refresh before clean snapshots
 * are recorded. A failed search write leaves its own draft dirty while the
 * successful bulk draft becomes clean, allowing the next save to retry search.
 *
 * Loading keeps the page skeleton visible until bulk hydration, search loading,
 * and creative model discovery complete. Silent reloads retain mounted controls
 * during discard. Domain modules own field normalization and request validation;
 * this coordinator owns their ordering, visible loading state, and error handling.
 */

/** SettingsSnapshotKey identifies each independently refreshed settings draft baseline. */
export type SettingsSnapshotKey =
  | 'settings' // bulk settings draft
  | 'webSearch' // search configuration draft
  | 'overloadCooldown' // overload cooldown draft
  | 'openAI403Cooldown' // OpenAI forbidden cooldown
  | 'rateLimit429Cooldown' // upstream throttling cooldown
  | 'streamTimeout' // stream timeout draft
  | 'rectifier' // request rectifier draft
  | 'betaPolicy' // Anthropic beta policy
  | 'ollamaCloudUsage' // Ollama quota probing
  | 'panelRateLimit' // panel API limits

/** SettingsPersistenceActions connects domain conversion and snapshot ownership to ordered API work. */
export interface SettingsPersistenceActions {
  /** buildPayload validates fields and returns null when the administrator needs to correct a value. */
  buildPayload: () => UpdateSettingsRequest | null
  /** applyLoadedSettings applies initial response compatibility rules to the existing shared reactive draft. */
  applyLoadedSettings: (settings: SystemSettings) => void
  /** applyUpdatedSettings applies successful bulk responses and clears the submitted secret input fields. */
  applyUpdatedSettings: (settings: SystemSettings) => void
  /** loadWebSearchConfig loads the separately stored search draft before the bulk baseline is captured. */
  loadWebSearchConfig: () => Promise<void>
  /** saveWebSearchConfig returns false on search failure, retaining that draft for the administrator's next save. */
  saveWebSearchConfig: () => Promise<boolean>
  /** loadCreativeModelCandidates refreshes selectable model capabilities before the loaded form becomes clean. */
  loadCreativeModelCandidates: () => Promise<void>
  /** markSettingsClean captures selected draft baselines after mounted controls have normalized their values. */
  markSettingsClean: (...keys: SettingsSnapshotKey[]) => void
  /** afterLoad positions the active tabs once the root has scheduled its loading state update. */
  afterLoad: () => void
}

/**
 * useSettingsPersistence creates the bulk loading and saving actions for one
 * settings page. Invoke it during setup so step-up UI and loading state belong
 * to that page, and register saveSettings as the first page save target.
 */
export function useSettingsPersistence(actions: SettingsPersistenceActions) {
  /**
   * Page-scoped services handle translated feedback, step-up verification, and the two stores refreshed after a successful bulk write.
   */
  const { t } = useI18n()
  const appStore = useAppStore()
  const adminSettingsStore = useAdminSettingsStore()
  const settingsStepUp = useStepUp()
  /**
   * Loading state controls the root skeleton, while failure state suppresses the save bar after an unsuccessful bulk read.
   */
  const loading = ref(true)
  const loadFailed = ref(false)

  /**
   * loadSettings loads bulk fields, search configuration, and creative model choices
   * before recording the editable baselines. Silent reloads keep already mounted
   * panels visible during discard, while ordinary loads show the root skeleton.
   * Failures report the bulk loading error and suppress the save bar. After a
   * successful read, the next Vue tick lets child controls normalize their values
   * before both bulk and search snapshots become clean.
   */
  async function loadSettings({ silent = false } = {}) {
    if (!silent) loading.value = true
    loadFailed.value = false
    try {
      const settings = await adminAPI.settings.getSettings()
      actions.applyLoadedSettings(settings)

      // Search has a separate endpoint; load it before model discovery so both
      // auxiliary drafts are available when the root records clean snapshots.
      await actions.loadWebSearchConfig()
      await actions.loadCreativeModelCandidates()
    } catch (error: unknown) {
      loadFailed.value = true
      appStore.showError(extractApiErrorMessage(error, t('admin.settings.failedToLoad')))
    } finally {
      loading.value = false
      // Wait for the editable panels to render so active tab positioning can
      // include the gateway content anchor when that section is selected.
      actions.afterLoad()
    }
    if (!loadFailed.value) {
      // Mounted controls may normalize loaded values. Snapshot after their
      // update tick so untouched fields do not appear dirty on first display.
      await nextTick()
      actions.markSettingsClean('settings', 'webSearch')
    }
  }

  /**
   * saveSettings validates and writes bulk fields through step-up, then saves the
   * separate search configuration and refreshes public and administrator stores.
   * Validation failure or an API error returns false to the registry, which may
   * continue with later targets. A successful bulk write followed by failed search
   * marks only the bulk snapshot clean after the refreshes complete. Step-up
   * cancellation returns false quietly so the draft remains available for another
   * save attempt.
   */
  async function saveSettings(): Promise<boolean> {
    try {
      const payload = actions.buildPayload()
      if (!payload) return false

      const updated = await settingsStepUp.run(() => adminAPI.settings.updateSettings(payload))
      actions.applyUpdatedSettings(updated)

      // Bulk persistence has succeeded. Search reports failure as a boolean so
      // cached bulk settings can still refresh and receive a clean snapshot.
      const wsOk = await actions.saveWebSearchConfig()

      // Refresh public consumers before administrator consumers so sidebar and
      // header state observe the completed bulk write before the snapshot is accepted.
      await appStore.fetchPublicSettings(true)

      await adminSettingsStore.fetch(true)

      await nextTick()

      // Search failure leaves its baseline untouched, while the successfully
      // persisted bulk draft becomes clean and remains clean on the next retry.
      if (wsOk) {
        actions.markSettingsClean('settings', 'webSearch')
      } else {
        actions.markSettingsClean('settings')
      }

      return wsOk
    } catch (error: unknown) {
      // Cancellation is a dismissed verification request, so keep the draft
      // dirty and return failure to the registry without adding an error toast.
      if (isStepUpCancelled(error)) {
        return false
      }
      if (isStepUpBlocked(error)) {
        appStore.showError(
          stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
            ? t('stepUp.adminApiKeyForbidden')
            : t('stepUp.notEnabled'),
        )
        return false
      }
      // Enabling step-up requires the current administrator to configure TOTP;
      // this server reason selects the dedicated corrective message for that prerequisite.
      if ((error as { reason?: string })?.reason === 'STEP_UP_ENABLE_REQUIRES_TOTP') {
        appStore.showError(t('admin.settings.security.stepUpEnableRequiresTotp'))
        return false
      }
      appStore.showError(extractApiErrorMessage(error, t('admin.settings.failedToSave')))
      return false
    }
  }
  return { loading, loadFailed, settingsStepUp, loadSettings, saveSettings }
}
