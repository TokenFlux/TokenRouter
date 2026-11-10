<template>
  <SettingsCard
    :title="t('admin.settings.registration.title')"
    :description="t('admin.settings.registration.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="registration-enabled"
        v-model="form.registration_enabled"
        :label="t('admin.settings.registration.enableRegistration')"
        :hint="t('admin.settings.registration.enableRegistrationHint')"
      />
      <SettingToggleRow
        id="email-verify-enabled"
        v-model="form.email_verify_enabled"
        :label="t('admin.settings.registration.emailVerification')"
        :hint="t('admin.settings.registration.emailVerificationHint')"
      />
      <!-- Password reset controls are available when email verification is enabled. -->
      <Collapse :open="form.email_verify_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <SettingToggleRow
            id="password-reset-enabled"
            v-model="form.password_reset_enabled"
            :label="t('admin.settings.registration.passwordReset')"
            :hint="t('admin.settings.registration.passwordResetHint')"
          />
        </SettingsSubpanel>
      </Collapse>
      <SettingToggleRow
        id="promo-code-enabled"
        v-model="form.promo_code_enabled"
        :label="t('admin.settings.registration.promoCode')"
        :hint="t('admin.settings.registration.promoCodeHint')"
      />
      <SettingToggleRow
        id="invitation-code-enabled"
        v-model="form.invitation_code_enabled"
        :label="t('admin.settings.registration.invitationCode')"
        :hint="t('admin.settings.registration.invitationCodeHint')"
      />
    </SettingsSection>

    <SettingsSection>
      <div>
        <label for="registration-email-suffix-whitelist" class="input-label">
          {{ t('admin.settings.registration.emailSuffixWhitelist') }}
        </label>
        <p class="input-hint mb-2">
          {{ t('admin.settings.registration.emailSuffixWhitelistHint') }}
        </p>
        <SettingsTagInput
          id="registration-email-suffix-whitelist"
          v-model:draft="registrationEmailSuffixWhitelistDraft"
          :tags="registrationEmailSuffixWhitelistTags"
          :placeholder="t('admin.settings.registration.emailSuffixWhitelistPlaceholder')"
          @remove="removeRegistrationEmailSuffixWhitelistTag"
          @input="handleRegistrationEmailSuffixWhitelistDraftInput"
          @keydown="handleRegistrationEmailSuffixWhitelistDraftKeydown"
          @blur="commitRegistrationEmailSuffixWhitelistDraft"
          @paste="handleRegistrationEmailSuffixWhitelistPaste"
        />
        <p class="input-hint">
          {{ t('admin.settings.registration.emailSuffixWhitelistInputHint') }}
        </p>
      </div>
      <SettingToggleRow
        id="registration-email-domain-quota"
        v-model="form.registration_email_domain_quota_enabled"
        :label="t('admin.settings.registration.emailDomainQuota')"
        :hint="t('admin.settings.registration.emailDomainQuotaHint')"
      />
      <SettingToggleRow
        id="registration-email-normalization"
        v-model="form.registration_email_normalization"
        :label="t('admin.settings.registration.emailNormalization')"
        :hint="t('admin.settings.registration.emailNormalizationHint')"
      />
      <SettingToggleRow
        id="user-email-change-enabled"
        v-model="form.user_email_change_enabled"
        data-testid="user-email-change-setting"
        :label="t('admin.settings.registration.userEmailChange')"
        :hint="t('admin.settings.registration.userEmailChangeHint')"
      />
    </SettingsSection>

    <!-- The frontend address remains editable when email verification is disabled because other email links also use it. -->
    <SettingsSection>
      <div>
        <label for="frontend-url" class="input-label">
          {{ t('admin.settings.registration.frontendUrl') }}
        </label>
        <input
          id="frontend-url"
          v-model="form.frontend_url"
          data-testid="frontend-url-input"
          type="url"
          class="input"
          :placeholder="t('admin.settings.registration.frontendUrlPlaceholder')"
        />
        <p class="input-hint">
          {{ t('admin.settings.registration.frontendUrlHint') }}
        </p>
      </div>
    </SettingsSection>

    <SettingsSection>
      <!-- An absent encryption key blocks enabling TOTP; an already enabled setting stays interactive so administrators can disable it. -->
      <SettingToggleRow
        id="totp-enabled"
        v-model="form.totp_enabled"
        :label="t('admin.settings.registration.totp')"
        :hint="t('admin.settings.registration.totpHint')"
        :disabled="!form.totp_encryption_key_configured && !form.totp_enabled"
      >
        <template v-if="!form.totp_encryption_key_configured" #hint>
          <SettingsNotice tone="warning" class="mt-2">
            {{ t('admin.settings.registration.totpKeyNotConfigured') }}
          </SettingsNotice>
        </template>
      </SettingToggleRow>
      <SettingToggleRow
        id="step-up-enabled"
        v-model="form.step_up_enabled"
        :label="t('admin.settings.security.stepUp')"
        :hint="t('admin.settings.security.stepUpHint')"
      />
      <SettingToggleRow
        id="session-binding-enabled"
        v-model="form.session_binding_enabled"
        :label="t('admin.settings.security.sessionBinding')"
        :hint="t('admin.settings.security.sessionBindingHint')"
      />
      <SettingRow
        id="audit-log-retention-days"
        field
        label-for="audit-log-retention-days"
        :label="t('admin.settings.security.auditRetention')"
        :hint="t('admin.settings.security.auditRetentionHint')"
      >
        <input
          id="audit-log-retention-days"
          v-model.number="form.audit_log_retention_days"
          type="number"
          min="0"
          class="input"
        />
      </SettingRow>
    </SettingsSection>
  </SettingsCard>

  <!-- API key IP access rules. -->
  <SettingsCard
    :title="t('admin.settings.apiKeyAcl.title')"
    :description="t('admin.settings.apiKeyAcl.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="api-key-acl-trust-forwarded-ip"
        v-model="form.api_key_acl_trust_forwarded_ip"
        :label="t('admin.settings.apiKeyAcl.trustForwardedIp')"
        :hint="t('admin.settings.apiKeyAcl.trustForwardedIpHint')"
      />
      <Collapse :open="form.api_key_acl_trust_forwarded_ip" unmount-on-hide>
        <SettingsSubpanel>
          <div>
            <label for="forwarded-client-ip-headers" class="input-label">
              {{ t('admin.settings.apiKeyAcl.forwardedClientIpHeaders') }}
            </label>
            <p class="input-hint mb-2">
              {{ t('admin.settings.apiKeyAcl.forwardedClientIpHeadersHint') }}
            </p>
            <SettingsTagInput
              id="forwarded-client-ip-headers"
              v-model:draft="forwardedClientIpHeaderDraft"
              :tags="form.forwarded_client_ip_headers"
              :placeholder="t('admin.settings.apiKeyAcl.forwardedClientIpHeadersPlaceholder')"
              :remove-label="
                (header) => t('admin.settings.apiKeyAcl.removeForwardedClientIpHeader', { header })
              "
              tag-testid="forwarded-client-ip-header-tag"
              input-testid="forwarded-client-ip-headers-input"
              @remove="removeForwardedClientIpHeader"
              @keydown="handleForwardedClientIpHeaderKeydown"
              @blur="commitForwardedClientIpHeaderDraft"
              @paste="handleForwardedClientIpHeaderPaste"
            />
          </div>
          <SettingsNotice tone="warning">
            {{ t('admin.settings.apiKeyAcl.forwardedClientIpHeadersRiskHint') }}
          </SettingsNotice>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- Panel API rate limits. -->
  <SettingsCard
    :title="t('admin.settings.panelRateLimit.title')"
    :description="t('admin.settings.panelRateLimit.description')"
  >
    <ContentSkeleton v-if="panelRateLimitLoading" variant="form" :rows="4" />
    <SettingsSection v-else>
      <!-- Authenticated limits use user identity; the separate public limit below applies to client IP addresses. -->
      <SettingsNotice>
        {{ t('admin.settings.panelRateLimit.proxySafeNote') }}
      </SettingsNotice>
      <SettingToggleRow
        id="panel-rate-limit-enabled"
        v-model="panelRateLimitForm.enabled"
        :label="t('admin.settings.panelRateLimit.enabled')"
        :hint="t('admin.settings.panelRateLimit.enabledHint')"
      />
      <Collapse :open="panelRateLimitForm.enabled" unmount-on-hide>
        <SettingsSubpanel>
          <SettingRow
            id="panel-rate-limit-user-rpm"
            field
            label-for="panel-rate-limit-user-rpm"
            :label="t('admin.settings.panelRateLimit.userRpm')"
            :hint="t('admin.settings.panelRateLimit.userRpmHint')"
          >
            <div class="flex items-center gap-2">
              <input
                id="panel-rate-limit-user-rpm"
                v-model.number="panelRateLimitForm.user_rpm"
                data-testid="panel-rate-limit-user-rpm"
                type="number"
                min="0"
                max="100000"
                class="input"
              />
              <span class="shrink-0 text-sm text-gray-500 dark:text-dark-400">
                {{ t('admin.settings.panelRateLimit.perMinute') }}
              </span>
            </div>
          </SettingRow>
          <SettingRow
            id="panel-rate-limit-heavy-rpm"
            field
            label-for="panel-rate-limit-heavy-rpm"
            :label="t('admin.settings.panelRateLimit.heavyRpm')"
            :hint="t('admin.settings.panelRateLimit.heavyRpmHint')"
          >
            <div class="flex items-center gap-2">
              <input
                id="panel-rate-limit-heavy-rpm"
                v-model.number="panelRateLimitForm.heavy_rpm"
                type="number"
                min="0"
                max="100000"
                class="input"
              />
              <span class="shrink-0 text-sm text-gray-500 dark:text-dark-400">
                {{ t('admin.settings.panelRateLimit.perMinute') }}
              </span>
            </div>
          </SettingRow>
          <SettingRow
            id="panel-rate-limit-public-ip-rpm"
            field
            label-for="panel-rate-limit-public-ip-rpm"
            :label="t('admin.settings.panelRateLimit.publicIpRpm')"
            :hint="t('admin.settings.panelRateLimit.publicIpRpmHint')"
          >
            <div class="flex items-center gap-2">
              <input
                id="panel-rate-limit-public-ip-rpm"
                v-model.number="panelRateLimitForm.public_ip_rpm"
                type="number"
                min="0"
                max="100000"
                class="input"
              />
              <span class="shrink-0 text-sm text-gray-500 dark:text-dark-400">
                {{ t('admin.settings.panelRateLimit.perMinute') }}
              </span>
            </div>
          </SettingRow>
          <SettingToggleRow
            id="panel-rate-limit-exempt-admin"
            v-model="panelRateLimitForm.exempt_admin"
            :label="t('admin.settings.panelRateLimit.exemptAdmin')"
            :hint="t('admin.settings.panelRateLimit.exemptAdminHint')"
          />
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- CAPTCHA provider selection. -->
  <SettingsCard
    :title="t('admin.settings.captcha.title')"
    :description="t('admin.settings.captcha.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="captcha-enabled"
        v-model="captchaMasterEnabled"
        :label="t('admin.settings.captcha.enable')"
        :hint="t('admin.settings.captcha.enableHint')"
        testid="captcha-enabled-toggle"
      />
      <Collapse :open="captchaMasterEnabled" unmount-on-hide>
        <SettingsSubpanel>
          <div>
            <p class="input-label">{{ t('admin.settings.captcha.provider') }}</p>
            <SettingsSegmented
              :model-value="captchaProviderSelection"
              :options="captchaProviderOptions"
              :ariaLabel="t('admin.settings.captcha.provider')"
              block
              @update:model-value="selectCaptchaProvider($event as CaptchaProviderSelection)"
            />
          </div>

          <!-- Cloudflare Turnstile -->
          <template v-if="captchaProviderSelection === 'turnstile'">
            <div>
              <label for="turnstile-site-key" class="input-label">
                {{ t('admin.settings.turnstile.siteKey') }}
              </label>
              <input
                id="turnstile-site-key"
                v-model="form.turnstile_site_key"
                type="text"
                class="input font-mono text-sm"
                placeholder="0x4AAAAAAA..."
              />
              <p class="input-hint">
                {{ t('admin.settings.turnstile.siteKeyHint') }}
                <a href="https://dash.cloudflare.com/" target="_blank" class="text-link">{{
                  t('admin.settings.turnstile.cloudflareDashboard')
                }}</a>
              </p>
            </div>
            <div>
              <label for="turnstile-secret-key" class="input-label">
                {{ t('admin.settings.turnstile.secretKey') }}
              </label>
              <input
                id="turnstile-secret-key"
                v-model="form.turnstile_secret_key"
                type="password"
                class="input font-mono text-sm"
                placeholder="0x4AAAAAAA..."
              />
              <p class="input-hint">
                {{
                  form.turnstile_secret_key_configured
                    ? t('admin.settings.turnstile.secretKeyConfiguredHint')
                    : t('admin.settings.turnstile.secretKeyHint')
                }}
              </p>
            </div>
          </template>

          <!-- Tencent CAPTCHA credentials. -->
          <template v-else-if="captchaProviderSelection === 'tencent'">
            <div>
              <p class="input-label">{{ t('admin.settings.tencentCaptcha.region') }}</p>
              <SettingsSegmented
                :model-value="form.tencent_captcha_region === 'intl' ? 'intl' : 'cn'"
                :options="tencentCaptchaRegionOptions"
                :ariaLabel="t('admin.settings.tencentCaptcha.region')"
                @update:model-value="form.tencent_captcha_region = $event as string"
              />
              <p class="input-hint">{{ t('admin.settings.tencentCaptcha.regionHint') }}</p>
            </div>
            <SettingsSection
              :title="t('admin.settings.tencentCaptcha.appCredentialsTitle')"
              :hint="t('admin.settings.tencentCaptcha.appCredentialsHint')"
            >
              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="tencent-captcha-app-id" class="input-label">
                    {{ t('admin.settings.tencentCaptcha.appId') }}
                  </label>
                  <input
                    id="tencent-captcha-app-id"
                    v-model="form.tencent_captcha_app_id"
                    type="text"
                    inputmode="numeric"
                    class="input font-mono text-sm"
                    placeholder="123456789"
                  />
                </div>
                <div>
                  <label for="tencent-captcha-app-secret" class="input-label">
                    {{ t('admin.settings.tencentCaptcha.appSecretKey') }}
                  </label>
                  <input
                    id="tencent-captcha-app-secret"
                    v-model="form.tencent_captcha_app_secret_key"
                    type="password"
                    autocomplete="new-password"
                    class="input font-mono text-sm"
                    :placeholder="t('admin.settings.tencentCaptcha.keepExisting')"
                  />
                  <p class="input-hint">
                    {{
                      form.tencent_captcha_app_secret_key_configured
                        ? t('admin.settings.tencentCaptcha.configured')
                        : t('admin.settings.tencentCaptcha.required')
                    }}
                  </p>
                </div>
              </div>
            </SettingsSection>
            <SettingsSection
              :title="t('admin.settings.tencentCaptcha.cloudCredentialsTitle')"
              :hint="t('admin.settings.tencentCaptcha.cloudCredentialsHint')"
            >
              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="tencent-captcha-cloud-secret-id" class="input-label">
                    {{ t('admin.settings.tencentCaptcha.cloudSecretId') }}
                  </label>
                  <input
                    id="tencent-captcha-cloud-secret-id"
                    v-model="form.tencent_captcha_cloud_secret_id"
                    type="password"
                    autocomplete="new-password"
                    class="input font-mono text-sm"
                    :placeholder="t('admin.settings.tencentCaptcha.keepExisting')"
                  />
                  <p class="input-hint">
                    {{
                      form.tencent_captcha_cloud_secret_id_configured
                        ? t('admin.settings.tencentCaptcha.configured')
                        : t('admin.settings.tencentCaptcha.required')
                    }}
                  </p>
                </div>
                <div>
                  <label for="tencent-captcha-cloud-secret-key" class="input-label">
                    {{ t('admin.settings.tencentCaptcha.cloudSecretKey') }}
                  </label>
                  <input
                    id="tencent-captcha-cloud-secret-key"
                    v-model="form.tencent_captcha_cloud_secret_key"
                    type="password"
                    autocomplete="new-password"
                    class="input font-mono text-sm"
                    :placeholder="t('admin.settings.tencentCaptcha.keepExisting')"
                  />
                  <p class="input-hint">
                    {{
                      form.tencent_captcha_cloud_secret_key_configured
                        ? t('admin.settings.tencentCaptcha.configured')
                        : t('admin.settings.tencentCaptcha.required')
                    }}
                  </p>
                </div>
              </div>
              <div class="space-y-1">
                <p class="input-hint">
                  {{ t('admin.settings.tencentCaptcha.camPermissionHint') }}
                </p>
                <p class="input-hint">
                  {{ t('admin.settings.tencentCaptcha.aidEncryptedHint') }}
                </p>
              </div>
              <div class="flex flex-wrap gap-x-4 gap-y-2 text-sm">
                <a
                  :href="tencentCaptchaLinks.console"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-link"
                >
                  {{ t('admin.settings.tencentCaptcha.openCaptchaConsole') }}
                </a>
                <a
                  :href="tencentCaptchaLinks.cloudKeys"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-link"
                >
                  {{ t('admin.settings.tencentCaptcha.createCloudKeys') }}
                </a>
                <a
                  :href="tencentCaptchaLinks.webDocs"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-link"
                >
                  {{ t('admin.settings.tencentCaptcha.openWebDocs') }}
                </a>
              </div>
            </SettingsSection>
          </template>

          <!-- Aliyun CAPTCHA 2.0 credentials. -->
          <template v-else>
            <div>
              <p class="input-label">{{ t('admin.settings.aliyunCaptcha.region') }}</p>
              <SettingsSegmented
                :model-value="form.aliyun_captcha_region === 'sgp' ? 'sgp' : 'cn'"
                :options="aliyunCaptchaRegionOptions"
                :ariaLabel="t('admin.settings.aliyunCaptcha.region')"
                @update:model-value="form.aliyun_captcha_region = $event as string"
              />
              <p class="input-hint">{{ t('admin.settings.aliyunCaptcha.regionHint') }}</p>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="aliyun-captcha-prefix" class="input-label">
                  {{ t('admin.settings.aliyunCaptcha.prefix') }}
                </label>
                <input
                  id="aliyun-captcha-prefix"
                  v-model="form.aliyun_captcha_prefix"
                  type="text"
                  class="input font-mono text-sm"
                  placeholder="14xxxxx"
                />
                <p class="input-hint">{{ t('admin.settings.aliyunCaptcha.prefixHint') }}</p>
              </div>
              <div>
                <label for="aliyun-captcha-scene-id" class="input-label">
                  {{ t('admin.settings.aliyunCaptcha.sceneId') }}
                </label>
                <input
                  id="aliyun-captcha-scene-id"
                  v-model="form.aliyun_captcha_scene_id"
                  type="text"
                  class="input font-mono text-sm"
                  placeholder="1cxxxxxx"
                />
                <p class="input-hint">
                  {{ t('admin.settings.aliyunCaptcha.sceneIdHint') }}
                </p>
              </div>
              <div>
                <label for="aliyun-captcha-access-key-id" class="input-label">
                  {{ t('admin.settings.aliyunCaptcha.accessKeyId') }}
                </label>
                <input
                  id="aliyun-captcha-access-key-id"
                  v-model="form.aliyun_captcha_access_key_id"
                  type="text"
                  class="input font-mono text-sm"
                  placeholder="LTAI..."
                />
                <p class="input-hint">
                  {{ t('admin.settings.aliyunCaptcha.accessKeyIdHint') }}
                </p>
              </div>
              <div>
                <label for="aliyun-captcha-access-key-secret" class="input-label">
                  {{ t('admin.settings.aliyunCaptcha.accessKeySecret') }}
                </label>
                <input
                  id="aliyun-captcha-access-key-secret"
                  v-model="form.aliyun_captcha_access_key_secret"
                  type="password"
                  autocomplete="new-password"
                  class="input font-mono text-sm"
                  placeholder="••••••••"
                />
                <p class="input-hint">
                  {{
                    form.aliyun_captcha_access_key_secret_configured
                      ? t('admin.settings.aliyunCaptcha.accessKeySecretConfiguredHint')
                      : t('admin.settings.aliyunCaptcha.accessKeySecretHint')
                  }}
                </p>
              </div>
            </div>
          </template>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Collapse from '@/components/common/Collapse.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import SettingsTagInput from '@/components/common/settings/SettingsTagInput.vue'
import { useI18n } from 'vue-i18n'
import type {
  CaptchaProviderSelection,
  useSecuritySettings,
  SecuritySettingsForm,
} from './useSecuritySettings'

/**
 * SecuritySettings.vue presents registration, account security, forwarded IP
 * headers, panel rate limits, and CAPTCHA controls. Bulk fields and rate limits use
 * their respective page save targets, while conditional subpanels edit the same
 * parent-owned drafts whenever their controls are mounted.
 */

/**
 * props supplies the page-owned controller and mutable draft used by this panel
 * throughout its mounted lifetime.
 */
const props = defineProps<{
  /** controller retains reactive fields and actions created once by the parent during page setup. */
  controller: ReturnType<typeof useSecuritySettings>
  /** form shares editable settings with the parent; field writes enter its bulk dirty snapshot. */
  form: SecuritySettingsForm
}>()
/**
 * Controller bindings retain Vue refs and action functions so template edits and
 * buttons use the parent instance.
 */
const {
  registrationEmailSuffixWhitelistTags,
  registrationEmailSuffixWhitelistDraft,
  forwardedClientIpHeaderDraft,
  panelRateLimitLoading,
  panelRateLimitForm,
  captchaProviderSelection,
  captchaMasterEnabled,
  captchaProviderOptions,
  tencentCaptchaRegionOptions,
  aliyunCaptchaRegionOptions,
  selectCaptchaProvider,
  tencentCaptchaLinks,
  removeRegistrationEmailSuffixWhitelistTag,
  commitRegistrationEmailSuffixWhitelistDraft,
  handleRegistrationEmailSuffixWhitelistDraftInput,
  handleRegistrationEmailSuffixWhitelistDraftKeydown,
  handleRegistrationEmailSuffixWhitelistPaste,
  removeForwardedClientIpHeader,
  commitForwardedClientIpHeaderDraft,
  handleForwardedClientIpHeaderKeydown,
  handleForwardedClientIpHeaderPaste,
} = props.controller
/**
 * form retains the shared draft object so nested field edits reach the page save
 * target.
 */
const { form } = props
/**
 * t resolves settings labels and hints from the active interface locale during
 * rendering.
 */
const { t } = useI18n()
</script>
