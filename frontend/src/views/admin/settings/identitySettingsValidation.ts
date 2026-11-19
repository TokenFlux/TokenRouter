import { useI18n } from 'vue-i18n'
import { deriveWeChatConnectStoredMode } from './settingsFormHelpers'
import { useAppStore } from '@/stores'
import type { useIdentitySettings } from './useIdentitySettings'
import type { SettingsForm } from './settingsState'
import type { WeChatConnectMode } from '@/api/admin/settings.types'

/**
 * identitySettingsValidation.ts exposes the WeChat capability check and stored-mode
 * normalization used by ordered bulk saving. The page checks conflicting public-
 * account and mobile switches before invoking normalization, so an invalid
 * combination reports its error before the normalizer can reconcile those switches.
 */

/**
 * createIdentitySettingsValidation binds WeChat validation to the shared page draft
 * and its controller. Create it during component setup, then call the returned check
 * and normalization separately at their assigned positions in bulk-save validation.
 */
export function createIdentitySettingsValidation(
  form: Pick<
    SettingsForm,
    | 'wechat_connect_mp_enabled'
    | 'wechat_connect_mobile_enabled'
    | 'wechat_connect_open_enabled'
    | 'wechat_connect_mode'
  >,
  actions: Pick<ReturnType<typeof useIdentitySettings>, 'syncWeChatConnectMode'>,
) {
  /**
   * t, appStore, and syncWeChatConnectMode capture translated feedback and the draft
   * synchronizer needed by the ordered validation steps.
   */
  const { t } = useI18n()
  const appStore = useAppStore()
  const { syncWeChatConnectMode } = actions

  /**
   * validateCapabilities reports whether public-account and mobile WeChat
   * capabilities can be saved together. A conflicting pair shows a translated error
   * and returns false; this check does not normalize or mutate the switches.
   */
  function validateCapabilities(): boolean {
    if (form.wechat_connect_mp_enabled && form.wechat_connect_mobile_enabled) {
      appStore.showError(t('admin.settings.wechatConnect.mpMobileConflict'))
      return false
    }
    return true
  }

  /**
   * normalizeStoredMode updates the shared WeChat capability, mode, and scope draft
   * and returns its derived mode. Invoke it after validateCapabilities succeeds, at
   * the page save phase where normalization may become visible.
   */
  function normalizeStoredMode(): WeChatConnectMode {
    syncWeChatConnectMode()

    const wechatStoredMode = deriveWeChatConnectStoredMode(
      form.wechat_connect_open_enabled,
      form.wechat_connect_mp_enabled,
      form.wechat_connect_mobile_enabled,
      form.wechat_connect_mode,
    )
    return wechatStoredMode
  }
  /**
   * The returned operations stay separate so the page can preserve error ordering
   * and delay draft normalization until preceding checks succeed.
   */
  return { validateCapabilities, normalizeStoredMode }
}
