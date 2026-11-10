<template>
  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('auth.createAccount') }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('auth.signUpToStart', { siteName }) }}
        </p>
      </div>

      <div
        v-if="!registrationEnabled && settingsLoaded"
        class="rounded-surface border border-amber-200 bg-amber-50 p-4 dark:border-amber-800/50 dark:bg-amber-900/20"
      >
        <div class="flex items-start gap-3">
          <div class="flex-shrink-0">
            <Icon name="exclamationCircle" size="md" class="text-amber-500" />
          </div>
          <p class="text-sm text-amber-700 dark:text-amber-400">
            {{ t('auth.registrationDisabled') }}
          </p>
        </div>
      </div>

      <form
        v-else
        @submit.prevent="handleRegister"
        :novalidate="agreementGateActive"
        class="space-y-5"
      >
        <div>
          <label for="email" class="input-label">
            {{ t('auth.emailLabel') }}
          </label>
          <div class="input-icon-wrap input-icon-lg">
            <div class="input-icon">
              <Icon name="mail" size="md" class="text-gray-400 dark:text-dark-500" />
            </div>
            <input
              id="email"
              v-model="formData.email"
              type="email"
              required
              autofocus
              autocomplete="email"
              :disabled="registrationActionDisabled"
              class="input input-has-icon"
              :class="{ 'input-error': errors.email }"
              :placeholder="t('auth.emailPlaceholder')"
            />
          </div>
        </div>

        <div>
          <label for="password" class="input-label">
            {{ t('auth.passwordLabel') }}
          </label>
          <div class="input-icon-wrap input-icon-lg">
            <div class="input-icon">
              <Icon name="lock" size="md" class="text-gray-400 dark:text-dark-500" />
            </div>
            <input
              id="password"
              v-model="formData.password"
              :type="showPassword ? 'text' : 'password'"
              required
              autocomplete="new-password"
              :disabled="registrationActionDisabled"
              class="input input-has-icon input-has-icon-right"
              :class="{ 'input-error': errors.password }"
              :placeholder="t('auth.createPasswordPlaceholder')"
            />
            <button
              type="button"
              :disabled="registrationActionDisabled"
              @click="showPassword = !showPassword"
              class="input-icon-right text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-dark-300"
            >
              <Icon v-if="showPassword" name="eyeOff" size="sm" />
              <Icon v-else name="eye" size="sm" />
            </button>
          </div>
          <p class="input-hint">
            {{ t('auth.passwordHint') }}
          </p>
        </div>

        <div v-if="invitationCodeEnabled">
          <label for="invitation_code" class="input-label">
            {{ t('auth.invitationCodeLabel') }}
          </label>
          <div class="input-icon-wrap input-icon-lg">
            <div class="input-icon">
              <Icon
                name="key"
                size="md"
                :class="
                  invitationValidation.valid ? 'text-green-500' : 'text-gray-400 dark:text-dark-500'
                "
              />
            </div>
            <input
              id="invitation_code"
              v-model="formData.invitation_code"
              type="text"
              :disabled="registrationActionDisabled"
              class="input input-has-icon input-has-icon-right"
              :class="{
                'border-green-500 focus:border-green-500 focus:ring-green-500':
                  invitationValidation.valid,
                'border-red-500 focus:border-red-500 focus:ring-red-500':
                  invitationValidation.invalid || errors.invitation_code,
              }"
              :placeholder="t('auth.invitationCodePlaceholder')"
              @input="handleInvitationCodeInput"
            />

            <div v-if="invitationValidating" class="input-icon-right">
              <Icon
                name="loader"
                size="sm"
                :animate-on-hover="false"
                class="h-4 w-4 animate-spin text-gray-400"
              />
            </div>
            <div v-else-if="invitationValidation.valid" class="input-icon-right">
              <Icon name="checkCircle" size="md" class="text-green-500" :animate-on-hover="false" />
            </div>
            <div
              v-else-if="invitationValidation.invalid || errors.invitation_code"
              class="input-icon-right"
            >
              <Icon name="exclamationCircle" size="md" class="text-red-500" />
            </div>
          </div>

          <MotionTransition name="fade-slow">
            <div
              v-if="invitationValidation.valid"
              class="mt-2 flex items-center gap-2 rounded-control bg-green-50 px-3 py-2 dark:bg-green-900/20"
            >
              <Icon
                name="checkCircle"
                size="sm"
                class="text-green-600 dark:text-green-400"
                :animate-on-hover="false"
              />
              <span class="text-sm text-green-700 dark:text-green-400">
                {{ t('auth.invitationCodeValid') }}
              </span>
            </div>
          </MotionTransition>
        </div>

        <div v-else-if="affiliateEnabled" data-testid="affiliate-invitation-field">
          <label for="affiliate_code" class="input-label">
            {{ t('auth.invitationCodeLabel') }}
            <span class="ml-1 text-xs font-normal text-gray-400 dark:text-dark-500"
              >({{ t('common.optional') }})</span
            >
          </label>
          <div class="input-icon-wrap input-icon-lg">
            <div class="input-icon">
              <Icon name="key" size="md" class="text-gray-400 dark:text-dark-500" />
            </div>
            <input
              id="affiliate_code"
              v-model="formData.aff_code"
              type="text"
              :disabled="registrationActionDisabled"
              class="input input-has-icon"
              :placeholder="t('auth.invitationCodePlaceholder')"
            />
          </div>
        </div>

        <div v-if="promoCodeEnabled">
          <label for="promo_code" class="input-label">
            {{ t('auth.promoCodeLabel') }}
            <span class="ml-1 text-xs font-normal text-gray-400 dark:text-dark-500"
              >({{ t('common.optional') }})</span
            >
          </label>
          <div class="input-icon-wrap input-icon-lg">
            <div class="input-icon">
              <Icon
                name="gift"
                size="md"
                :class="
                  promoValidation.valid ? 'text-green-500' : 'text-gray-400 dark:text-dark-500'
                "
              />
            </div>
            <input
              id="promo_code"
              v-model="formData.promo_code"
              type="text"
              :disabled="registrationActionDisabled"
              class="input input-has-icon input-has-icon-right"
              :class="{
                'border-green-500 focus:border-green-500 focus:ring-green-500':
                  promoValidation.valid,
                'border-red-500 focus:border-red-500 focus:ring-red-500': promoValidation.invalid,
              }"
              :placeholder="t('auth.promoCodePlaceholder')"
              @input="handlePromoCodeInput"
            />

            <div v-if="promoValidating" class="input-icon-right">
              <Icon
                name="loader"
                size="sm"
                :animate-on-hover="false"
                class="h-4 w-4 animate-spin text-gray-400"
              />
            </div>
            <div v-else-if="promoValidation.valid" class="input-icon-right">
              <Icon name="checkCircle" size="md" class="text-green-500" :animate-on-hover="false" />
            </div>
            <div v-else-if="promoValidation.invalid" class="input-icon-right">
              <Icon name="exclamationCircle" size="md" class="text-red-500" />
            </div>
          </div>

          <MotionTransition name="fade-slow">
            <div
              v-if="promoValidation.valid"
              class="mt-2 flex items-center gap-2 rounded-control bg-green-50 px-3 py-2 dark:bg-green-900/20"
            >
              <Icon name="gift" size="sm" class="text-green-600 dark:text-green-400" />
              <span class="text-sm text-green-700 dark:text-green-400">
                {{
                  t('auth.promoCodeValid', {
                    amount: formatBalanceAmount(promoValidation.bonusAmount, { fractionDigits: 2 }),
                  })
                }}
              </span>
            </div>
          </MotionTransition>
        </div>

        <div v-if="captchaEnabled" data-testid="registration-turnstile">
          <TurnstileWidget
            ref="turnstileRef"
            :turnstile-enabled="turnstileEnabled"
            :turnstile-site-key="turnstileSiteKey"
            :tencent-enabled="tencentCaptchaEnabled"
            :tencent-app-id="tencentCaptchaAppId"
            :tencent-region="tencentCaptchaRegion"
            :aliyun-enabled="aliyunCaptchaEnabled"
            :aliyun-scene-id="aliyunCaptchaSceneId"
            :aliyun-prefix="aliyunCaptchaPrefix"
            :aliyun-region="aliyunCaptchaRegion"
            @verify="onTurnstileVerify"
            @expire="onTurnstileExpire"
            @error="onTurnstileError"
          />
        </div>

        <LoginAgreementPrompt
          v-if="loginAgreementEnabled"
          :accepted="agreementAccepted"
          :documents="loginAgreementDocuments"
          :mode="loginAgreementMode"
          :updated-at="loginAgreementUpdatedAt"
          :visible="showAgreementModal"
          v-model:hint-visible="showAgreementHint"
          @accept="acceptLoginAgreement"
          @reject="rejectLoginAgreement"
          @open="showAgreementModal = true"
        />

        <button
          type="submit"
          :disabled="
            registrationActionDisabled ||
            (!agreementGateActive && turnstileEnabled && !turnstileToken)
          "
          class="btn btn-primary w-full"
        >
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="isLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
          />
          <Icon v-else name="userPlus" size="sm" class="mr-2" />
          {{
            isLoading
              ? t('auth.processing')
              : emailVerifyEnabled
                ? t('auth.continue')
                : t('auth.createAccount')
          }}
        </button>
      </form>

      <div
        v-if="
          linuxdoOAuthEnabled ||
          wechatOAuthEnabled ||
          oidcOAuthEnabled ||
          githubOAuthEnabled ||
          googleOAuthEnabled
        "
        class="space-y-4"
      >
        <div class="flex items-center gap-3">
          <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('auth.oauthOrContinue') }}
          </span>
          <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
        </div>
        <EmailOAuthButtons
          v-if="githubOAuthEnabled || googleOAuthEnabled"
          :disabled="registrationActionDisabled"
          :promo-code="formData.promo_code"
          :github-enabled="githubOAuthEnabled"
          :google-enabled="googleOAuthEnabled"
          :aff-code="formData.aff_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <LinuxDoOAuthSection
          v-if="linuxdoOAuthEnabled"
          :disabled="registrationActionDisabled"
          :aff-code="formData.aff_code"
          :promo-code="formData.promo_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <WechatOAuthSection
          v-if="wechatOAuthEnabled"
          :disabled="registrationActionDisabled"
          :aff-code="formData.aff_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <OidcOAuthSection
          v-if="oidcOAuthEnabled"
          :disabled="registrationActionDisabled"
          :provider-name="oidcOAuthProviderName"
          :aff-code="formData.aff_code"
          :show-divider="false"
          @start="handleOAuthStart"
        />
      </div>
    </div>

    <template #footer>
      <p class="text-gray-500 dark:text-dark-400">
        {{ t('auth.alreadyHaveAccount') }}
        <router-link to="/login" class="font-medium text-link">
          {{ t('auth.signIn') }}
        </router-link>
      </p>
    </template>
  </AuthLayout>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, ref, reactive, onMounted, onUnmounted, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import WechatOAuthSection from '@/components/auth/WechatOAuthSection.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/auth/CaptchaChallenge.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useAuthStore, useAppStore } from '@/stores'
import {
  buildOAuthLoginStartURL,
  getPublicSettings,
  isWeChatWebOAuthEnabled,
  startOAuthLogin,
  type OAuthLoginStart,
  validatePromoCode,
  validateInvitationCode,
} from '@/api/auth'
import { buildAuthErrorMessage } from '@/utils/authError'
import {
  LOGIN_AGREEMENT_STORAGE_KEY,
  hasAcceptedLoginAgreement,
  revokeLoginAgreement,
} from '@/utils/loginAgreement'
import { extractApiErrorCode, extractI18nErrorMessage } from '@/utils/apiError'
import {
  formatRegistrationEmailSuffixWhitelistForMessage,
  isRegistrationEmailSuffixAllowed,
  normalizeRegistrationEmailSuffixWhitelist,
} from '@/utils/registrationEmailPolicy'
import { clearAffiliateCode, loadAffiliateCode, resolveAffiliateCode } from '@/utils/oauthAffiliate'
import type { LoginAgreementDocument, PublicSettings } from '@/api/settings.types'

// RegisterView.vue coordinates local signup, provider signup, agreement consent,
// and promotional input validation. Public settings decide whether email
// verification, invitations, and captcha are required. A signup waiting for
// promotion or invitation validation uses the active promise before making its
// final eligibility decision.
//
// Validation results compare their requested code with the current draft, while
// request symbols protect ownership of stored promises. Email verification
// transfers the credentials and attribution together through session storage
// before routing to the next page. Direct signup instead waits for the auth store
// to finish, then clears referral attribution and navigates; agreement acceptance
// is persisted before opening its local gate.

// t supplies authentication copy and locale controls formatting of permitted-email
// suffix lists. Route and store services own navigation and completion of the
// current registration action.
const { t, locale } = useI18n() // message resolver and active locale

const router = useRouter() // application navigation service
const route = useRoute() // current callback query context
const authStore = useAuthStore() // shared authentication state owner
const appStore = useAppStore() // public settings and toast owner
const { formatBalanceAmount } = useBalanceDisplay() // promotion bonus display formatter

// The request and display state controls authentication progress, failure
// feedback, and input visibility. Handlers publish successful completion
// separately from merely starting or settling a request.
const isLoading = ref<boolean>(false) // current form request busy flag
// settingsLoaded records completion of the registration settings request,
// including failure. It is separate from whether the requested settings were
// successfully retrieved.
const settingsLoaded = ref<boolean>(false) // registration settings completion flag
// errorMessage holds the latest page-level failure while the nearby draft retains
// user input. Toast delivery occurs in the corresponding watcher or request
// handler.
const errorMessage = ref<string>('') // current action failure message
const showPassword = ref<boolean>(false) // password input visibility toggle

// Signup configuration controls registration availability, email verification,
// promotions, invitations, and referrals. Each flag determines its own form field
// or subsequent submission path.
const registrationEnabled = ref<boolean>(true) // public signup availability flag
const emailVerifyEnabled = ref<boolean>(false) // public email verification requirement
const promoCodeEnabled = ref<boolean>(true) // public promotion input availability
const invitationCodeEnabled = ref<boolean>(false) // public invitation requirement flag
const affiliateEnabled = ref<boolean>(false) // public referral input availability
// These values carry public challenge configuration and local verification state
// into the widget and request handlers. Provider-specific token and random-string
// fields travel together when Tencent is enabled.
const turnstileEnabled = ref<boolean>(false) // public Turnstile enable flag
const turnstileSiteKey = ref<string>('') // public Turnstile widget identifier
const tencentCaptchaEnabled = ref<boolean>(false) // public Tencent captcha enable flag
const tencentCaptchaAppId = ref<string>('') // public Tencent application identifier
const tencentCaptchaRegion = ref<string>('cn') // Tencent SDK region selector
const aliyunCaptchaEnabled = ref<boolean>(false) // public Aliyun captcha enable flag
const aliyunCaptchaSceneId = ref<string>('') // public Aliyun scene identifier
const aliyunCaptchaPrefix = ref<string>('') // public Aliyun SDK prefix
const aliyunCaptchaRegion = ref<string>('cn') // Aliyun SDK region selector
// publicSettings retains the fetched public configuration used by the site-name
// formatter. A missing snapshot leaves that formatter to select its documented
// branding fallback.
const publicSettings = ref<PublicSettings | null>(null) // loaded public configuration snapshot
// siteName resolves the registration success label from public settings. The
// adjacent email policy values determine whether local suffix checks or backend
// quota decisions apply.
const siteName = computed(() => resolveLocalizedSiteName(publicSettings.value)) // registration success display name
// Provider enable flags determine which sign-in entries the page displays.
// Backend-only mode on the login page can hide the provider section even when
// individual methods are configured.
const linuxdoOAuthEnabled = ref<boolean>(false) // public LinuxDo login enable flag
const wechatOAuthEnabled = ref<boolean>(false) // public WeChat web-login availability
const oidcOAuthEnabled = ref<boolean>(false) // public OIDC login enable flag
// oidcOAuthProviderName supplies the configured display label for the OIDC entry.
// The provider identifier sent to authentication remains oidc independently of
// that label.
const oidcOAuthProviderName = ref<string>('OIDC') // configured OIDC button label
// GitHub and Google flags control the email-provider buttons. Google prompt
// eligibility additionally uses its separate One Tap enable flag and client
// identifier.
const githubOAuthEnabled = ref<boolean>(false) // public GitHub login enable flag
const googleOAuthEnabled = ref<boolean>(false) // public Google login enable flag
// registrationEmailSuffixWhitelist contains normalized allowed suffixes for local
// admission checks. Backend quota policy can delegate unmatched domains rather
// than rejecting them in the browser.
const registrationEmailSuffixWhitelist = ref<string[]>([]) // normalized permitted signup email suffixes
// emailDomainQuotaEnabled delegates non-whitelisted address admission to backend
// domain quotas. Without that policy, the signup form applies the configured
// suffix whitelist locally.
const emailDomainQuotaEnabled = ref<boolean>(false) // backend email-domain quota policy
// Agreement settings connect the current document revision to saved acceptance.
// Prompt visibility remains separate from acceptance so checkbox mode can allow
// editing while authentication stays gated.
const loginAgreementEnabled = ref<boolean>(false) // active legal consent requirement
const loginAgreementMode = ref<'modal' | 'checkbox' | string>('modal') // configured legal consent presentation
const loginAgreementUpdatedAt = ref<string>('') // agreement update display date
// loginAgreementRevision uses the server revision when present, otherwise an
// update-date and document fingerprint. Stored consent is checked against that
// exact selected value.
const loginAgreementRevision = ref<string>('') // current consent revision identifier
const loginAgreementDocuments = ref<LoginAgreementDocument[]>([]) // current legal document collection
const agreementAccepted = ref<boolean>(false) // current revision consent decision
const showAgreementModal = ref<boolean>(false) // agreement dialog display flag
const showAgreementHint = ref(false) // checkbox agreement reminder flag

// These values carry public challenge configuration and local verification state
// into the widget and request handlers. Provider-specific token and random-string
// fields travel together when Tencent is enabled.
const turnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null) // captcha widget action handle
const turnstileToken = ref<string>('') // prepared challenge proof value
const tencentCaptchaRandstr = ref<string>('') // paired Tencent proof random string
const aliyunCaptchaReady = computed(
  () =>
    aliyunCaptchaEnabled.value &&
    Boolean(aliyunCaptchaSceneId.value) &&
    Boolean(aliyunCaptchaPrefix.value),
) // complete Aliyun configuration flag

const actionCaptchaEnabled = computed(
  () =>
    (tencentCaptchaEnabled.value && Boolean(tencentCaptchaAppId.value)) || aliyunCaptchaReady.value,
) // Tencent or Aliyun action gate
const captchaEnabled = computed(
  () => (turnstileEnabled.value && Boolean(turnstileSiteKey.value)) || actionCaptchaEnabled.value,
) // available challenge provider flag

// Promotion and invitation validation each retain feedback, a debounce timer, an
// active promise, and its owner token. Submission can await the promise while
// input comparisons reject responses for changed text.
const promoValidating = ref<boolean>(false) // promotion validation busy flag
const promoValidation = reactive({
  valid: false, // successful code validation flag
  invalid: false, // failed code validation flag
  bonusAmount: null as number | null, // validated promotion bonus amount
  message: '', // code validation feedback text
}) // promotion validity and bonus result
// promoValidateTimeout holds the delayed promotion check until it starts. Nonempty
// edits replace it and unmount clears it; empty edits return before cancellation.
let promoValidateTimeout: ReturnType<typeof setTimeout> | null = null // pending promotion debounce handle
// promoValidationPromise exposes the current promotion check to registration
// submission. Its owner symbol protects the reference from being cleared by an
// older request.
let promoValidationPromise: Promise<void> | null = null // active promotion validation promise
// promoValidationToken identifies the owner of the stored promise. Response
// acceptance separately compares input text, so this symbol does not reject every
// stale result.
let promoValidationToken: symbol | null = null // promotion promise ownership symbol

const invitationValidating = ref<boolean>(false) // invitation validation busy flag
const invitationValidation = reactive({
  valid: false, // successful code validation flag
  invalid: false, // failed code validation flag
  message: '', // code validation feedback text
}) // invitation validity feedback result
// invitationValidateTimeout holds the delayed invitation check until it starts.
// Nonempty edits replace it and unmount clears it; empty edits return before
// cancellation.
let invitationValidateTimeout: ReturnType<typeof setTimeout> | null = null // pending invitation debounce handle
// invitationValidationPromise exposes the current invitation check to registration
// submission. Its owner symbol protects the reference from being cleared by an
// older request.
let invitationValidationPromise: Promise<void> | null = null // active invitation validation promise
// invitationValidationToken identifies the owner of the stored promise. Response
// acceptance separately compares input text, so this symbol does not reject every
// stale result.
let invitationValidationToken: symbol | null = null // invitation promise ownership symbol

// formData owns the editable authentication values read by submission. errors
// records local validation feedback without replacing the draft, allowing the user
// to correct a failed attempt.
const formData = reactive({
  email: '', // entered account email
  password: '', // entered account password
  promo_code: '', // optional signup promotion code
  invitation_code: '', // required signup invitation code
  aff_code: '', // signup referral attribution code
}) // editable authentication form draft

const errors = reactive({
  email: '', // email field validation message
  password: '', // password field validation message
  turnstile: '', // turnstile field validation message
  invitation_code: '', // invitation_code field validation message
}) // field validation message collection

// validationToastMessage selects the first currently active validation error for
// the toast watcher. Empty values clear the selection without emitting a
// notification.
const validationToastMessage = computed(
  () =>
    errors.email ||
    errors.password ||
    (invitationValidation.invalid ? invitationValidation.message : '') ||
    errors.invitation_code ||
    (promoValidation.invalid ? promoValidation.message : '') ||
    errors.turnstile ||
    '',
) // first active validation toast message

// agreementGateActive combines the configured requirement with current acceptance.
// The action readiness computation uses that result differently for modal and
// checkbox consent presentation.
const agreementGateActive = computed(() => loginAgreementEnabled.value && !agreementAccepted.value) // unaccepted current agreement gate

// registrationActionDisabled blocks signup while loading, before settings settle,
// or behind unaccepted modal consent. Checkbox consent remains editable and is
// checked by the action handler.
const registrationActionDisabled = computed(
  () =>
    isLoading.value ||
    !settingsLoaded.value ||
    (agreementGateActive.value && loginAgreementMode.value !== 'checkbox'),
) // signup action readiness gate

// Only changed nonempty validation messages reach the app toast, preventing an
// unchanged reactive error from producing repeated notifications during field
// updates.
watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// syncAffiliateCode resolves referral query parameters and updates the
// registration draft when nonempty. An absent code leaves the existing draft
// unchanged, preserving a previously supplied referral during query updates.
function syncAffiliateCode(): string {
  const code = resolveAffiliateCode(route.query.aff, route.query.aff_code)
  if (code) {
    formData.aff_code = code
  }
  return code
}

// Initialize this page from public settings before user actions depend on its
// configured authentication requirements, retaining the documented defaults when
// that request fails.
onMounted(async () => {
  syncAffiliateCode()

  try {
    const settings = await getPublicSettings()
    registrationEnabled.value = settings.registration_enabled
    emailVerifyEnabled.value = settings.email_verify_enabled
    promoCodeEnabled.value = settings.promo_code_enabled
    invitationCodeEnabled.value = settings.invitation_code_enabled
    affiliateEnabled.value = settings.affiliate_enabled === true
    turnstileEnabled.value = settings.turnstile_enabled
    turnstileSiteKey.value = settings.turnstile_site_key || ''
    tencentCaptchaEnabled.value = settings.tencent_captcha_enabled === true
    tencentCaptchaAppId.value = settings.tencent_captcha_app_id || ''
    tencentCaptchaRegion.value = settings.tencent_captcha_region || 'cn'
    aliyunCaptchaEnabled.value = settings.aliyun_captcha_enabled === true
    aliyunCaptchaSceneId.value = settings.aliyun_captcha_scene_id || ''
    aliyunCaptchaPrefix.value = settings.aliyun_captcha_prefix || ''
    aliyunCaptchaRegion.value = settings.aliyun_captcha_region || 'cn'
    publicSettings.value = settings
    linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled
    wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
    oidcOAuthEnabled.value = settings.oidc_oauth_enabled
    oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
    githubOAuthEnabled.value = settings.github_oauth_enabled
    googleOAuthEnabled.value = settings.google_oauth_enabled
    registrationEmailSuffixWhitelist.value = normalizeRegistrationEmailSuffixWhitelist(
      settings.registration_email_suffix_whitelist || [],
    )
    emailDomainQuotaEnabled.value = settings.registration_email_domain_quota_enabled === true
    applyLoginAgreementSettings(settings)

    if (promoCodeEnabled.value) {
      const promoParam = route.query.promo as string
      if (promoParam) {
        formData.promo_code = promoParam

        await validatePromoCodeDebounced(promoParam)
      }
    }
    syncAffiliateCode()
  } catch (error) {
    // A settings fetch failure leaves the current page defaults available. The
    // catch path reports the diagnostic rather than rejecting component
    // initialization.
    console.error('Failed to load public settings:', error)
    // When public-settings loading fails, this page disables the consent gate and
    // marks acceptance locally. Subsequent readiness uses these fallback values.
    loginAgreementEnabled.value = false
    agreementAccepted.value = true
  } finally {
    settingsLoaded.value = true
  }
})

// Refresh referral attribution when either supported query parameter changes,
// keeping the registration draft synchronized with the current referral link.
watch(
  () => [route.query.aff, route.query.aff_code],
  () => {
    syncAffiliateCode()
  },
)

// Cancel queued code-validation timers when registration unmounts. Requests that
// have already started keep their existing completion behavior and are not
// cancelled by this cleanup.
onUnmounted(() => {
  // Cancel the previously scheduled promotion check before replacing it or leaving
  // the page, so that timer does not start obsolete delayed work.
  if (promoValidateTimeout) {
    clearTimeout(promoValidateTimeout)
  }
  // Cancel the previously scheduled invitation check before replacing it or
  // leaving the page, so that timer does not start obsolete delayed work.
  if (invitationValidateTimeout) {
    clearTimeout(invitationValidateTimeout)
  }
})

// resolveLocalizedSiteName returns the configured trimmed site name for success
// messages. Missing or blank settings use TokenRouter, including the period before
// the public-settings request has completed.
function resolveLocalizedSiteName(settings: PublicSettings | null): string {
  return settings?.site_name?.trim() || 'TokenRouter'
}

// applyLoginAgreementSettings derives the active consent gate from public
// documents and revision metadata. Empty document titles are removed before the
// enabled state is evaluated, and a missing explicit revision is reconstructed
// from the update date and document identities. Saved consent applies only to the
// selected revision. Modal mode opens immediately for missing consent, while
// checkbox mode leaves the form available and prompts at the authentication
// action.
function applyLoginAgreementSettings(settings: {
  login_agreement_enabled?: boolean // agreement feature enabled flag

  login_agreement_mode?: string // agreement presentation mode

  login_agreement_updated_at?: string // agreement update display date

  login_agreement_revision?: string // persisted agreement revision identifier

  login_agreement_documents?: LoginAgreementDocument[] // configured agreement document list
}): void {
  const documents = Array.isArray(settings.login_agreement_documents)
    ? settings.login_agreement_documents.filter((doc) => doc.title?.trim())
    : []
  loginAgreementDocuments.value = documents
  loginAgreementEnabled.value = settings.login_agreement_enabled === true && documents.length > 0
  loginAgreementMode.value = settings.login_agreement_mode === 'checkbox' ? 'checkbox' : 'modal'
  loginAgreementUpdatedAt.value = settings.login_agreement_updated_at || ''
  loginAgreementRevision.value =
    settings.login_agreement_revision ||
    `${loginAgreementUpdatedAt.value}:${documents.map((doc) => `${doc.id}:${doc.title}`).join('|')}`

  agreementAccepted.value =
    !loginAgreementEnabled.value || hasAcceptedLoginAgreement(loginAgreementRevision.value)
  showAgreementModal.value =
    loginAgreementEnabled.value &&
    !agreementAccepted.value &&
    loginAgreementMode.value !== 'checkbox'
}

// acceptLoginAgreement saves the current revision before marking consent accepted
// in memory. It then hides both prompt variants; storage failures interrupt
// acceptance because the persistent write occurs before those state changes.
function acceptLoginAgreement(): void {
  if (loginAgreementRevision.value) {
    // Persist the accepted revision before opening the in-memory consent gate, so
    // a storage failure does not mark this page as accepted.
    localStorage.setItem(
      LOGIN_AGREEMENT_STORAGE_KEY,
      JSON.stringify({
        revision: loginAgreementRevision.value,
        accepted_at: new Date().toISOString(),
      }),
    )
  }
  agreementAccepted.value = true
  showAgreementHint.value = false
  showAgreementModal.value = false
}

// rejectLoginAgreement revokes saved consent and clears the local accepted state.
// It closes both prompts and shows a warning only in modal mode, leaving checkbox
// users free to continue editing.
function rejectLoginAgreement(): void {
  revokeLoginAgreement()
  agreementAccepted.value = false
  showAgreementModal.value = false
  showAgreementHint.value = false
  if (loginAgreementMode.value !== 'checkbox') appStore.showWarning(t('auth.agreementRequired'))
}

// handlePromoCodeInput clears the displayed promo validation result after editing.
// Nonempty input replaces the pending debounce timer and schedules validation
// after 500 milliseconds; empty input returns before cancelling an older timer.
function handlePromoCodeInput(): void {
  const code = formData.promo_code.trim()

  promoValidation.valid = false
  promoValidation.invalid = false
  promoValidation.bonusAmount = null
  promoValidation.message = ''

  if (!code) {
    promoValidating.value = false
    return
  }

  // Cancel the previously scheduled promotion check before replacing it or leaving
  // the page, so that timer does not start obsolete delayed work.
  if (promoValidateTimeout) {
    clearTimeout(promoValidateTimeout)
  }

  // Wait half a second after the edit before contacting promotion validation,
  // allowing a later nonempty edit to replace the queued check.
  promoValidateTimeout = setTimeout(() => {
    validatePromoCodeDebounced(code)
  }, 500)
}

// validatePromoCodeDebounced validates a nonempty trimmed promo code and exposes
// the active promise to submission. Responses update feedback only when the
// current input still matches the requested code. A per-request symbol prevents an
// older completion from clearing a newer promise, while the busy flag is cleared
// by every settling request. Matching input values can therefore accept
// overlapping results; the token guards promise ownership rather than response
// ordering.
async function validatePromoCodeDebounced(code: string): Promise<void> {
  code = code.trim()
  if (!code) return

  const currentToken = Symbol('promo-validation')
  const task = (async () => {
    promoValidating.value = true
    promoValidationToken = currentToken

    try {
      const result = await validatePromoCode(code)
      // The user may have edited the promotion while validation was pending.
      // Discard this result when it refers to a different current code.
      if (formData.promo_code.trim() !== code) {
        return
      }

      if (result.valid) {
        promoValidation.valid = true
        promoValidation.invalid = false
        promoValidation.bonusAmount = result.bonus_amount || 0
        promoValidation.message = ''
      } else {
        promoValidation.valid = false
        promoValidation.invalid = true
        promoValidation.bonusAmount = null

        promoValidation.message = getPromoErrorMessage(result.error_code)
      }
    } catch (error) {
      console.error('Failed to validate promo code:', error)
      // The user may have edited the promotion while validation was pending.
      // Discard this result when it refers to a different current code.
      if (formData.promo_code.trim() !== code) {
        return
      }
      promoValidation.valid = false
      promoValidation.invalid = true
      promoValidation.message = t('auth.promoCodeInvalid')
    } finally {
      promoValidating.value = false
      // Only the current owner clears the stored validation promise, preventing an
      // older request from removing a newer request that submission may await.
      if (promoValidationToken === currentToken) {
        promoValidationToken = null
        promoValidationPromise = null
      }
    }
  })()

  promoValidationPromise = task
  await task
}

// getPromoErrorMessage translates known promotion rejection codes into field
// feedback. Missing and unrecognized codes use the generic invalid-promotion
// message, so callers can display every negative validation response.
function getPromoErrorMessage(errorCode?: string): string {
  switch (errorCode) {
    case 'PROMO_CODE_NOT_FOUND':
      return t('auth.promoCodeNotFound')
    case 'PROMO_CODE_EXPIRED':
      return t('auth.promoCodeExpired')
    case 'PROMO_CODE_DISABLED':
      return t('auth.promoCodeDisabled')
    case 'PROMO_CODE_MAX_USED':
      return t('auth.promoCodeMaxUsed')
    case 'PROMO_CODE_ALREADY_USED':
      return t('auth.promoCodeAlreadyUsed')
    default:
      return t('auth.promoCodeInvalid')
  }
}

// handleInvitationCodeInput clears the displayed invitation validation result
// after editing. Nonempty input replaces the pending debounce timer and schedules
// validation after 500 milliseconds; empty input returns before cancelling an
// older timer.
function handleInvitationCodeInput(): void {
  const code = formData.invitation_code.trim()

  invitationValidation.valid = false
  invitationValidation.invalid = false
  invitationValidation.message = ''
  errors.invitation_code = ''

  if (!code) {
    return
  }

  // Cancel the previously scheduled invitation check before replacing it or
  // leaving the page, so that timer does not start obsolete delayed work.
  if (invitationValidateTimeout) {
    clearTimeout(invitationValidateTimeout)
  }

  // Wait half a second after the edit before contacting invitation validation,
  // allowing a later nonempty edit to replace the queued check.
  invitationValidateTimeout = setTimeout(() => {
    validateInvitationCodeDebounced(code)
  }, 500)
}

// validateInvitationCodeDebounced validates a nonempty trimmed invitation code and
// exposes the active promise to submission. Responses update feedback only when
// the current input still matches the requested code. A per-request symbol
// prevents an older completion from clearing a newer promise, while the busy flag
// is cleared by every settling request. Matching input values can therefore accept
// overlapping results; the token guards promise ownership rather than response
// ordering.
async function validateInvitationCodeDebounced(code: string): Promise<void> {
  code = code.trim()
  if (!code) return

  const currentToken = Symbol('invitation-validation')
  const task = (async () => {
    invitationValidating.value = true
    invitationValidationToken = currentToken

    try {
      const result = await validateInvitationCode(code)
      // The user may have edited the invitation while validation was pending.
      // Discard this result when it refers to a different current code.
      if (formData.invitation_code.trim() !== code) {
        return
      }

      if (result.valid) {
        invitationValidation.valid = true
        invitationValidation.invalid = false
        invitationValidation.message = ''
      } else {
        invitationValidation.valid = false
        invitationValidation.invalid = true
        invitationValidation.message = getInvitationErrorMessage(result.error_code)
      }
    } catch {
      // The user may have edited the invitation while validation was pending.
      // Discard this result when it refers to a different current code.
      if (formData.invitation_code.trim() !== code) {
        return
      }
      invitationValidation.valid = false
      invitationValidation.invalid = true
      invitationValidation.message = t('auth.invitationCodeInvalid')
    } finally {
      invitationValidating.value = false
      // Only the current owner clears the stored invitation promise, preventing an
      // older completion from removing a newer request that submission may await.
      if (invitationValidationToken === currentToken) {
        invitationValidationToken = null
        invitationValidationPromise = null
      }
    }
  })()

  invitationValidationPromise = task
  await task
}

// getInvitationErrorMessage returns the generic invalid-invitation message for
// validation failures. The listed server codes and the default branch deliberately
// share that copy rather than exposing different rejection reasons.
function getInvitationErrorMessage(errorCode?: string): string {
  switch (errorCode) {
    case 'INVITATION_CODE_NOT_FOUND':
      return t('auth.invitationCodeInvalid')
    case 'INVITATION_CODE_INVALID':
      return t('auth.invitationCodeInvalid')
    case 'INVITATION_CODE_USED':
      return t('auth.invitationCodeInvalid')
    case 'INVITATION_CODE_DISABLED':
      return t('auth.invitationCodeInvalid')
    default:
      return t('auth.invitationCodeInvalid')
  }
}

// onTurnstileVerify stores the challenge token and optional Tencent random string.
// It clears the captcha error so the current proof can enable the next guarded
// form action.
function onTurnstileVerify(token: string, randstr = ''): void {
  turnstileToken.value = token
  tencentCaptchaRandstr.value = randstr
  errors.turnstile = ''
}

// onTurnstileExpire clears both challenge proof fields after widget expiration. It
// publishes the translated expiry error, requiring a later verification callback
// or action challenge before guarded submission can proceed.
function onTurnstileExpire(): void {
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  errors.turnstile = t('auth.turnstileExpired')
}

// onTurnstileError clears both challenge proof fields after widget failure. It
// publishes the translated verification error so a failed proof cannot remain
// available for a later form submission.
function onTurnstileError(): void {
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  errors.turnstile = t('auth.turnstileFailed')
}

// resetCaptchaProof resets the challenge widget and clears both local proof
// fields. It also clears the captcha validation message, preparing subsequent user
// actions to obtain a fresh challenge result.
function resetCaptchaProof(): void {
  turnstileRef.value?.reset()
  turnstileToken.value = ''
  tencentCaptchaRandstr.value = ''
  errors.turnstile = ''
}

// acquireActionProof obtains a fresh proof only for enabled action-based captcha
// providers. It returns true immediately otherwise; cancellation or an unavailable
// widget returns false, and successful proof fields replace the local values.
async function acquireActionProof(): Promise<boolean> {
  if (!actionCaptchaEnabled.value) return true

  const proof = await turnstileRef.value?.verifyAction()
  if (!proof) return false

  turnstileToken.value = proof.token
  tencentCaptchaRandstr.value = proof.randstr
  return true
}

// handleOAuthStart applies consent and loading gates before provider navigation.
// Without action captcha it opens the built start URL directly; otherwise it
// obtains proof, calls the start endpoint, and consumes proof after the request
// settles.
async function handleOAuthStart(request: OAuthLoginStart): Promise<void> {
  if (registrationActionDisabled.value || !ensureAgreementAccepted()) return

  if (!actionCaptchaEnabled.value) {
    window.location.href = buildOAuthLoginStartURL(request)
    return
  }

  isLoading.value = true
  try {
    const proof = await turnstileRef.value?.verifyAction()
    if (!proof) return

    const result = await startOAuthLogin(
      request,
      tencentCaptchaEnabled.value
        ? {
            tencent_captcha_ticket: proof.token,
            tencent_captcha_randstr: proof.randstr,
          }
        : { turnstile_token: proof.token },
    )
    window.location.href = result.authorize_url
  } catch (error: unknown) {
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', t('auth.turnstileFailed'))
    appStore.showError(errorMessage.value)
  } finally {
    resetCaptchaProof()
    isLoading.value = false
  }
}

// ensureAgreementAccepted allows authentication only when the current consent gate
// is satisfied. Otherwise it opens the checkbox hint or modal and returns false,
// letting callers stop before captcha, requests, or navigation.
function ensureAgreementAccepted(): boolean {
  if (!agreementGateActive.value) return true
  if (loginAgreementMode.value === 'checkbox') {
    showAgreementHint.value = true
  } else {
    showAgreementModal.value = true
  }
  return false
}

// validateEmail checks basic email syntax with the local address pattern.
function validateEmail(email: string): boolean {
  const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
  return emailRegex.test(email)
}

// buildEmailSuffixNotAllowedMessage formats the current allowed email suffixes for
// validation feedback. An empty normalized whitelist uses generic copy; Chinese
// locales use their list separator and all locales use the translated overflow
// label.
function buildEmailSuffixNotAllowedMessage(): string {
  const normalizedWhitelist = normalizeRegistrationEmailSuffixWhitelist(
    registrationEmailSuffixWhitelist.value,
  )
  if (normalizedWhitelist.length === 0) {
    return t('auth.emailSuffixNotAllowed')
  }
  const separator = String(locale.value || '')
    .toLowerCase()
    .startsWith('zh')
    ? '、'
    : ', '
  return t('auth.emailSuffixNotAllowedWithAllowed', {
    suffixes: formatRegistrationEmailSuffixWhitelistForMessage(normalizedWhitelist, {
      separator,
      // more formats the translated count of additional allowed email suffixes.
      more: (count) => t('auth.emailSuffixAllowedMore', { count }),
    }),
  })
}

// validateForm checks the current inputs and updates their validation messages. It
// returns whether all required fields and prepared Turnstile proof pass, allowing
// submission handlers to stop before action captcha or network work.
function validateForm(): boolean {
  errors.email = ''
  errors.password = ''
  errors.turnstile = ''
  errors.invitation_code = ''

  let isValid = true

  if (!ensureAgreementAccepted()) return false

  if (!formData.email.trim()) {
    errors.email = t('auth.emailRequired')
    isValid = false
  } else if (!validateEmail(formData.email)) {
    errors.email = t('auth.invalidEmail')
    isValid = false
  } else if (
    !emailDomainQuotaEnabled.value &&
    !isRegistrationEmailSuffixAllowed(formData.email, registrationEmailSuffixWhitelist.value)
  ) {
    // Without domain quota policy, the local whitelist rejects unmatched
    // addresses. With quota enabled, the backend decides admission so permitted
    // non-whitelisted domains can proceed.
    errors.email = buildEmailSuffixNotAllowedMessage()
    isValid = false
  }

  if (!formData.password) {
    errors.password = t('auth.passwordRequired')
    isValid = false
  } else if (formData.password.length < 6) {
    errors.password = t('auth.passwordMinLength')
    isValid = false
  }

  if (invitationCodeEnabled.value) {
    if (!formData.invitation_code.trim()) {
      errors.invitation_code = t('auth.invitationCodeRequired')
      isValid = false
    }
  }

  if (turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

// handleRegister validates consent and registration inputs before completing
// signup or handing the draft to email verification. It waits for outstanding code
// validation and avoids duplicating errors already sent by the validation watcher.
// Only after those checks does it acquire action proof. Email verification stores
// credentials and attribution together in session storage before navigation;
// direct signup clears referral attribution after the auth store succeeds. Both
// paths reset enabled captcha afterward.
async function handleRegister(): Promise<void> {
  if (registrationActionDisabled.value) return

  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  isLoading.value = true

  const promoCode = formData.promo_code.trim()
  let promoValidationHandledByWatcher = false
  if (promoCode) {
    if (promoValidating.value && promoValidationPromise) {
      promoValidationHandledByWatcher = true
      await promoValidationPromise
    }

    if (!promoValidation.valid && !promoValidation.invalid) {
      promoValidationHandledByWatcher = true
      await validatePromoCodeDebounced(promoCode)
    }

    if (promoValidation.invalid) {
      errorMessage.value = t('auth.promoCodeInvalidCannotRegister')
      if (!promoValidationHandledByWatcher) {
        appStore.showError(errorMessage.value)
      }
      isLoading.value = false
      return
    }
  }

  if (invitationCodeEnabled.value) {
    const invitationCode = formData.invitation_code.trim()
    let invitationValidationHandledByWatcher = false
    if (invitationValidating.value && invitationValidationPromise) {
      invitationValidationHandledByWatcher = true
      await invitationValidationPromise
    }

    if (invitationCode && !invitationValidation.valid) {
      invitationValidationHandledByWatcher = true
      await validateInvitationCodeDebounced(invitationCode)
    }

    if (!invitationValidation.valid) {
      errorMessage.value = t('auth.invitationCodeInvalidCannotRegister')
      if (!invitationValidationHandledByWatcher) {
        appStore.showError(errorMessage.value)
      }
      isLoading.value = false
      return
    }
  }

  if (!(await acquireActionProof())) {
    isLoading.value = false
    return
  }
  try {
    const affCode = formData.aff_code.trim() || loadAffiliateCode()
    if (affCode) {
      formData.aff_code = affCode
    }

    if (emailVerifyEnabled.value) {
      // Persist the credential draft before navigation because EmailVerifyView
      // restores register_data on mount; a failed write must keep this page from
      // handing off to a consumer that cannot restore the submitted credentials.
      sessionStorage.setItem(
        'register_data',
        JSON.stringify({
          email: formData.email,
          password: formData.password,
          turnstile_token:
            turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
          tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
          tencent_captcha_randstr: tencentCaptchaEnabled.value
            ? tencentCaptchaRandstr.value
            : undefined,
          promo_code: formData.promo_code || undefined,
          invitation_code: formData.invitation_code || undefined,
          aff_code: affCode || undefined,
        }),
      )

      await router.push('/email-verify')
      return
    }

    await authStore.register({
      email: formData.email,
      password: formData.password,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? tencentCaptchaRandstr.value
        : undefined,
      promo_code: formData.promo_code || undefined,
      invitation_code: formData.invitation_code || undefined,
      aff_code: affCode || undefined,
    })
    clearAffiliateCode()

    appStore.showSuccess(t('auth.accountCreatedSuccess', { siteName: siteName.value }))

    await router.push('/dashboard')
  } catch (error: unknown) {
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.registrationFailed'))

    appStore.showError(errorMessage.value)
  } finally {
    // A submitted request or reported server error can consume this proof. Reset
    // the widget and local values before the user retries the action.
    if (captchaEnabled.value) {
      resetCaptchaProof()
    }
    isLoading.value = false
  }
}

// buildRegistrationErrorMessage localizes the backend email-domain quota
// rejection. Other failures use the shared authentication formatter with the
// caller fallback, keeping send-code and final registration errors consistent for
// the user.
function buildRegistrationErrorMessage(error: unknown, fallback: string): string {
  if (extractApiErrorCode(error) === 'EMAIL_DOMAIN_REGISTRATION_LIMIT') {
    return t('auth.emailDomainRegistrationLimit')
  }
  return buildAuthErrorMessage(error, { fallback })
}
</script>

<style scoped></style>
