import { reactive, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { SystemSettings } from '@/api/admin/settings.types'
import { buildAuthSourceDefaultsState } from '../settingsFormHelpers'
import { createSettingsHydration } from '../settingsHydration'
import { createSettingsForm } from '../settingsState'

/**
 * settingsHydration.spec.ts verifies shared normalization and secret-clearing
 * rules across initial settings loads and post-save updates. Assertions protect
 * Tencent captcha, SMTP, Turnstile, Aliyun captcha, and OAuth secrets from
 * lingering in reactive form state after persistence while keeping nested
 * subscription and prompt-replacement collections normalized.
 */

describe('createSettingsHydration', () => {
  /**
   * Saving bulk settings with populated captcha, SMTP, and OAuth secrets must
   * clear every write-only secret input when applyUpdatedSettings processes the
   * server response, matching initial load behavior.
   */
  it('clears Tencent captcha and other secret inputs after applyUpdatedSettings', () => {
    const form = createSettingsForm()
    const tablePageSizeOptionsInput = ref('')
    const registrationEmailSuffixWhitelistTags = ref<string[]>([])
    const registrationEmailSuffixWhitelistDraft = ref('pending.example.com')
    const forwardedClientIpHeaderDraft = ref('X-Pending-Ip')
    const authSourceDefaults = reactive(buildAuthSourceDefaultsState({}))
    const openaiFastPolicyForm = reactive({ rules: [] })
    const openaiFastPolicyLoaded = ref(false)
    const smtpPasswordManuallyEdited = ref(true)

    const { applyUpdatedSettings } = createSettingsHydration(form, {
      general: {
        tablePageSizeOptionsInput,
        formatTablePageSizeOptions: (items: number[]) => items.join(','),
      },
      security: {
        registrationEmailSuffixWhitelistTags,
        registrationEmailSuffixWhitelistDraft,
        forwardedClientIpHeaderDraft,
        syncCaptchaProviderSelection: vi.fn(),
        normalizeForwardedClientIpHeaders: (headers?: string[]) => headers ?? [],
      },
      users: { authSourceDefaults },
      gateway: { openaiFastPolicyForm, openaiFastPolicyLoaded },
      email: { smtpPasswordManuallyEdited },
    })

    form.smtp_password = 'smtp-secret'
    form.turnstile_secret_key = 'turnstile-secret'
    form.tencent_captcha_app_secret_key = 'tencent-app-secret'
    form.tencent_captcha_cloud_secret_id = 'tencent-cloud-id'
    form.tencent_captcha_cloud_secret_key = 'tencent-cloud-secret'
    form.aliyun_captcha_access_key_secret = 'aliyun-secret'
    form.github_oauth_client_secret = 'gh-secret'

    applyUpdatedSettings({
      tencent_captcha_enabled: true,
      tencent_captcha_app_id: '123456',
      tencent_captcha_app_secret_key_configured: true,
      tencent_captcha_cloud_secret_id_configured: true,
      tencent_captcha_cloud_secret_key_configured: true,
      default_subscriptions: [{ group_id: 9 } as unknown as { plan_id: number }],
    } as unknown as SystemSettings)

    expect(form.smtp_password).toBe('')
    expect(smtpPasswordManuallyEdited.value).toBe(false)
    expect(form.turnstile_secret_key).toBe('')
    expect(form.tencent_captcha_app_secret_key).toBe('')
    expect(form.tencent_captcha_cloud_secret_id).toBe('')
    expect(form.tencent_captcha_cloud_secret_key).toBe('')
    expect(form.aliyun_captcha_access_key_secret).toBe('')
    expect(form.github_oauth_client_secret).toBe('')
    expect(form.default_subscriptions).toEqual([{ plan_id: 9 }])
    expect(registrationEmailSuffixWhitelistDraft.value).toBe('')
    expect(forwardedClientIpHeaderDraft.value).toBe('')
  })
})
