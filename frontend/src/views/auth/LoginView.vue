<template>
  <GoogleOneTap
    ref="googleOneTapRef"
    :enabled="googleOneTapEligible"
    :client-id="googleOneTapClientID"
  />

  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('auth.welcomeBack') }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('auth.signInToAccount') }}
        </p>
      </div>

      <form @submit.prevent="handleLogin" :novalidate="agreementGateActive" class="space-y-5">
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
              :disabled="authActionDisabled"
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
              autocomplete="current-password"
              :disabled="authActionDisabled"
              class="input input-has-icon input-has-icon-right"
              :class="{ 'input-error': errors.password }"
              :placeholder="t('auth.passwordPlaceholder')"
            />
            <button
              type="button"
              @click="showPassword = !showPassword"
              :disabled="authActionDisabled"
              class="input-icon-right text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-dark-300"
            >
              <Icon v-if="showPassword" name="eyeOff" size="sm" />
              <Icon v-else name="eye" size="sm" />
            </button>
          </div>
          <div class="mt-1 flex items-center justify-between">
            <span></span>
            <router-link
              v-if="passwordResetEnabled && !backendModeEnabled"
              to="/forgot-password"
              class="text-sm font-medium text-link"
            >
              {{ t('auth.forgotPassword') }}
            </router-link>
          </div>
        </div>

        <div v-if="captchaEnabled">
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
            authActionDisabled || (!agreementGateActive && turnstileEnabled && !turnstileToken)
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
          <Icon v-else name="login" size="sm" class="mr-2" />
          {{ isLoading ? t('auth.signingIn') : t('auth.signIn') }}
        </button>
      </form>

      <div v-if="showPasskeyLogin || showOAuthLogin" class="space-y-4">
        <div class="flex items-center gap-3">
          <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
          <span class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('auth.oauthOrContinue') }}
          </span>
          <div class="h-px flex-1 bg-gray-200 dark:bg-dark-700"></div>
        </div>
        <button
          v-if="showPasskeyLogin"
          type="button"
          class="btn btn-secondary w-full"
          :disabled="authActionDisabled"
          @click="handlePasskeyLogin"
        >
          <Icon name="key" size="sm" class="mr-2" />
          {{ passkeyLoading ? t('auth.passkeySigningIn') : t('auth.passkeySignIn') }}
        </button>
        <EmailOAuthButtons
          v-if="githubOAuthEnabled || googleOAuthEnabled"
          :disabled="authActionDisabled"
          :github-enabled="githubOAuthEnabled"
          :google-enabled="googleOAuthEnabled"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <LinuxDoOAuthSection
          v-if="linuxdoOAuthEnabled"
          :disabled="authActionDisabled"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <DingTalkOAuthSection
          v-if="dingtalkOAuthEnabled"
          :disabled="authActionDisabled"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <WechatOAuthSection
          v-if="wechatOAuthEnabled"
          :disabled="authActionDisabled"
          :show-divider="false"
          @start="handleOAuthStart"
        />
        <OidcOAuthSection
          v-if="oidcOAuthEnabled"
          :disabled="authActionDisabled"
          :provider-name="oidcOAuthProviderName"
          :show-divider="false"
          @start="handleOAuthStart"
        />
      </div>
    </div>

    <template v-if="!backendModeEnabled" #footer>
      <p class="text-gray-500 dark:text-dark-400">
        {{ t('auth.dontHaveAccount') }}
        <router-link to="/register" class="font-medium text-link">
          {{ t('auth.signUp') }}
        </router-link>
      </p>
    </template>
  </AuthLayout>

  <TotpLoginModal
    v-if="totpPresent"
    :show="show2FAModal"
    @after-leave="totpAfterLeave"
    ref="totpModalRef"
    :temp-token="totpTempToken"
    :user-email-masked="totpUserEmailMasked"
    @verify="handle2FAVerify"
    @cancel="handle2FACancel"
  />
</template>

<script setup lang="ts">
import { useLeavingPresence } from '@/composables/useLeavingPresence'
import { computed, ref, reactive, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import EmailOAuthButtons from '@/components/auth/EmailOAuthButtons.vue'
import GoogleOneTap from '@/components/auth/GoogleOneTap.vue'
import LinuxDoOAuthSection from '@/components/auth/LinuxDoOAuthSection.vue'
import DingTalkOAuthSection from '@/components/auth/DingTalkOAuthSection.vue'
import OidcOAuthSection from '@/components/auth/OidcOAuthSection.vue'
import WechatOAuthSection from '@/components/auth/WechatOAuthSection.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import TotpLoginModal from '@/components/auth/TotpLoginModal.vue'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/auth/CaptchaChallenge.vue'
import { useAuthStore, useAppStore } from '@/stores'
import {
  buildOAuthLoginStartURL,
  getPublicSettings,
  isTotp2FARequired,
  isWeChatWebOAuthEnabled,
  startOAuthLogin,
  type OAuthLoginStart,
} from '@/api/auth'
import type { ActionCaptchaRequestProof } from '@/api/auth.types'
import type { LoginAgreementDocument } from '@/api/settings.types'
import type { TotpLoginResponse } from '@/api/totp.types'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { clearAllAffiliateCodes } from '@/utils/oauthAffiliate'
import {
  hasAcceptedLoginAgreement,
  LOGIN_AGREEMENT_STORAGE_KEY,
  revokeLoginAgreement,
} from '@/utils/loginAgreement'
import { isGoogleOneTapEligible, isGoogleOneTapOriginSupported } from '@/utils/googleIdentity'

// LoginView.vue coordinates password, passkey, and provider sign-in from the login
// page. Public settings control visible methods, captcha, and the current
// legal-agreement revision. Checkbox consent permits editing before acceptance but
// blocks authentication actions; modal consent disables those actions until
// accepted.
//
// Password entry and explicit actions cancel the Google prompt, while One Tap
// eligibility also accounts for authentication and consent. Password login can
// stop at a TOTP challenge whose temporary token stays in page state. Action
// captcha proof is acquired before the corresponding authentication request and
// reset afterward, and accepted agreement revisions are persisted before the local
// gate opens.

// t resolves the authentication messages displayed by this component. Nearby store
// and navigation services deliver those messages and handle the requested
// authentication transition.
const { t } = useI18n() // translated authentication message resolver

const router = useRouter() // application navigation service
const authStore = useAuthStore() // shared authentication state owner
const appStore = useAppStore() // public settings and toast owner

// The request and display state controls authentication progress, failure
// feedback, and input visibility. Handlers publish successful completion
// separately from merely starting or settling a request.
const isLoading = ref<boolean>(false) // current form request busy flag
const passkeyLoading = ref<boolean>(false) // passkey authentication busy flag
const errorMessage = ref<string>('') // current action failure message
const showPassword = ref<boolean>(false) // password input visibility toggle
// publicSettingsLoaded records completion of the login settings request, including
// failure. Its true value allows the page readiness gate to evaluate the retained
// settings.
const publicSettingsLoaded = ref<boolean>(false) // login settings request completion flag

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
// Provider enable flags determine which sign-in entries the page displays.
// Backend-only mode on the login page can hide the provider section even when
// individual methods are configured.
const linuxdoOAuthEnabled = ref<boolean>(false) // public LinuxDo login enable flag
const dingtalkOAuthEnabled = ref<boolean>(false) // public DingTalk login enable flag
const wechatOAuthEnabled = ref<boolean>(false) // public WeChat web-login availability
const backendModeEnabled = ref<boolean>(false) // backend-only provider visibility policy
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
const googleOneTapEnabled = ref<boolean>(false) // public Google prompt enable flag
// googleOneTapClientID holds the configured browser OAuth client identifier.
// Prompt eligibility rejects an empty value before asking the Google identity SDK
// to display sign-in.
const googleOneTapClientID = ref<string>('') // public Google prompt client identifier
// Password-reset and passkey flags control optional login actions. Passkey
// visibility also requires the browser credential API before that action is
// offered to the user.
const passwordResetEnabled = ref<boolean>(false) // password-recovery link availability
const passkeyEnabled = ref<boolean>(false) // public passkey login availability
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

// show2FAModal requests the login challenge dialog. The leaving-presence
// composable retains its rendered component until the exit transition finishes and
// invokes its cleanup callback.
const show2FAModal = ref<boolean>(false) // login second-factor dialog visibility
// totpPresent and totpAfterLeave keep the challenge component available through
// its leave transition. They separate visual presence from the show2FAModal
// request to close it.
const { present: totpPresent, afterLeave: totpAfterLeave } = useLeavingPresence(
  () => show2FAModal.value,
) // TOTP leave-transition presence controls
// These values track the pending one-time-code challenge and its input or dialog
// state. Verification handlers consume the current draft and clear it on the
// documented reset paths.
const totpTempToken = ref<string>('') // pending second-factor challenge token
const totpUserEmailMasked = ref<string>('') // masked second-factor account address
const totpModalRef = ref<InstanceType<typeof TotpLoginModal> | null>(null) // parent-driven OTP modal controls
// googleOneTapRef exposes prompt cancellation to password entry and explicit login
// actions. Cancelling the prompt does not itself cancel an exchange already
// started by the child.
const googleOneTapRef = ref<InstanceType<typeof GoogleOneTap> | null>(null) // parent-driven Google prompt cancellation

const formData = reactive({
  email: '', // entered account email
  password: '', // entered account password
}) // editable authentication form draft

const errors = reactive({
  email: '', // email field validation message
  password: '', // password field validation message
  turnstile: '', // turnstile field validation message
}) // field validation message collection

// validationToastMessage selects the first currently active validation error for
// the toast watcher. Empty values clear the selection without emitting a
// notification.
const validationToastMessage = computed(
  () => errors.email || errors.password || errors.turnstile || '',
) // first active validation toast message

// agreementGateActive combines the configured requirement with current acceptance.
// The action readiness computation uses that result differently for modal and
// checkbox consent presentation.
const agreementGateActive = computed(() => loginAgreementEnabled.value && !agreementAccepted.value) // unaccepted current agreement gate

// The action readiness flags combine request progress, settings completion, and
// consent presentation. Provider and One Tap visibility use their own capability
// rules before exposing optional login entry points.
const authActionDisabled = computed(
  () =>
    isLoading.value ||
    passkeyLoading.value ||
    !publicSettingsLoaded.value ||
    (agreementGateActive.value && loginAgreementMode.value !== 'checkbox'),
) // login action readiness gate

const showPasskeyLogin = computed(
  () => passkeyEnabled.value && typeof window.PublicKeyCredential !== 'undefined',
) // browser-supported passkey action visibility

const showOAuthLogin = computed(
  () =>
    !backendModeEnabled.value &&
    (linuxdoOAuthEnabled.value ||
      dingtalkOAuthEnabled.value ||
      wechatOAuthEnabled.value ||
      oidcOAuthEnabled.value ||
      githubOAuthEnabled.value ||
      googleOAuthEnabled.value),
) // configured provider section visibility

const googleOneTapEligible = computed(() =>
  isGoogleOneTapEligible({
    publicSettingsLoaded: publicSettingsLoaded.value,
    isAuthenticated: authStore.isAuthenticated,
    oneTapEnabled: googleOAuthEnabled.value && googleOneTapEnabled.value,
    clientID: googleOneTapClientID.value,
    backendModeEnabled: backendModeEnabled.value,
    tencentCaptchaEnabled: tencentCaptchaEnabled.value,
    aliyunCaptchaEnabled: aliyunCaptchaEnabled.value,
    loginAgreementEnabled: loginAgreementEnabled.value,
    loginAgreementAccepted: !agreementGateActive.value,
    originSupported: isGoogleOneTapOriginSupported(),
  }),
) // combined Google prompt policy eligibility

// Only changed nonempty validation messages reach the app toast, preventing an
// unchanged reactive error from producing repeated notifications during field
// updates.
watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// Once the user starts entering password-login credentials, cancel the optional
// Google prompt so the explicit form action can proceed without another visible
// sign-in prompt.
watch(
  () => [formData.email, formData.password],
  ([email, password]) => {
    if (email.trim() || password.trim()) {
      googleOneTapRef.value?.cancelPrompt()
    }
  },
)

// Initialize this page from public settings before user actions depend on its
// configured authentication requirements, retaining the documented defaults when
// that request fails.
onMounted(async () => {
  const expiredFlag = sessionStorage.getItem('auth_expired')
  if (expiredFlag) {
    sessionStorage.removeItem('auth_expired')
    const message = t('auth.reloginRequired')
    errorMessage.value = message
    appStore.showWarning(message)
  }

  try {
    const settings = await getPublicSettings()
    turnstileEnabled.value = settings.turnstile_enabled
    turnstileSiteKey.value = settings.turnstile_site_key || ''
    tencentCaptchaEnabled.value = settings.tencent_captcha_enabled === true
    tencentCaptchaAppId.value = settings.tencent_captcha_app_id || ''
    tencentCaptchaRegion.value = settings.tencent_captcha_region || 'cn'
    aliyunCaptchaEnabled.value = settings.aliyun_captcha_enabled === true
    aliyunCaptchaSceneId.value = settings.aliyun_captcha_scene_id || ''
    aliyunCaptchaPrefix.value = settings.aliyun_captcha_prefix || ''
    aliyunCaptchaRegion.value = settings.aliyun_captcha_region || 'cn'
    linuxdoOAuthEnabled.value = settings.linuxdo_oauth_enabled
    dingtalkOAuthEnabled.value = settings.dingtalk_oauth_enabled ?? false
    wechatOAuthEnabled.value = isWeChatWebOAuthEnabled(settings)
    backendModeEnabled.value = settings.backend_mode_enabled
    oidcOAuthEnabled.value = settings.oidc_oauth_enabled
    oidcOAuthProviderName.value = settings.oidc_oauth_provider_name || 'OIDC'
    githubOAuthEnabled.value = settings.github_oauth_enabled
    googleOAuthEnabled.value = settings.google_oauth_enabled
    googleOneTapEnabled.value = settings.google_one_tap_enabled === true
    googleOneTapClientID.value = settings.google_oauth_client_id || ''
    backendModeEnabled.value = settings.backend_mode_enabled
    passwordResetEnabled.value = settings.password_reset_enabled
    passkeyEnabled.value = settings.passkey_enabled === true
    applyLoginAgreementSettings(settings)
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
    publicSettingsLoaded.value = true
  }
})

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

// validateForm checks the current inputs and updates their validation messages. It
// returns whether all required fields and prepared Turnstile proof pass, allowing
// submission handlers to stop before action captcha or network work.
function validateForm(): boolean {
  errors.email = ''
  errors.password = ''
  errors.turnstile = ''

  let isValid = true

  if (!ensureAgreementAccepted()) return false

  if (!formData.email.trim()) {
    errors.email = t('auth.emailRequired')
    isValid = false
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(formData.email)) {
    errors.email = t('auth.invalidEmail')
    isValid = false
  }

  if (!formData.password) {
    errors.password = t('auth.passwordRequired')
    isValid = false
  } else if (formData.password.length < 6) {
    errors.password = t('auth.passwordMinLength')
    isValid = false
  }

  if (turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

// handleLogin validates consent and password inputs before requesting action
// captcha and authentication. It cancels One Tap, opens the TOTP modal for a
// challenge response, and navigates only after ordinary login succeeds; settled
// requests consume their proof.
async function handleLogin(): Promise<void> {
  if (authActionDisabled.value) return
  googleOneTapRef.value?.cancelPrompt()

  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  if (!(await acquireActionProof())) {
    return
  }

  isLoading.value = true

  try {
    // The login API receives Aliyun verification parameters through
    // turnstile_token, while Tencent uses its ticket and random-string fields for
    // the same guarded action.
    const response = await authStore.login({
      email: formData.email,
      password: formData.password,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? tencentCaptchaRandstr.value
        : undefined,
    })

    if (isTotp2FARequired(response)) {
      const totpResponse = response as TotpLoginResponse
      totpTempToken.value = totpResponse.temp_token || ''
      totpUserEmailMasked.value = totpResponse.user_email_masked || ''
      show2FAModal.value = true
      isLoading.value = false
      return
    }

    appStore.showSuccess(t('auth.loginSuccess'))

    const redirectTo = (router.currentRoute.value.query.redirect as string) || '/dashboard'
    await router.push(redirectTo)
  } catch (error: unknown) {
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', t('auth.loginFailed'))

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

// handlePasskeyLogin starts passkey authentication after the consent gate and
// optional action captcha. It cancels One Tap first, distinguishes browser
// cancellation in fallback copy, and clears action proof after success or failure.
async function handlePasskeyLogin(): Promise<void> {
  googleOneTapRef.value?.cancelPrompt()
  if (authActionDisabled.value || !ensureAgreementAccepted()) return

  passkeyLoading.value = true
  try {
    let proof: ActionCaptchaRequestProof | undefined
    if (actionCaptchaEnabled.value) {
      const result = await turnstileRef.value?.verifyAction()
      if (!result) return
      proof = tencentCaptchaEnabled.value
        ? {
            tencent_captcha_ticket: result.token,
            tencent_captcha_randstr: result.randstr,
          }
        : { turnstile_token: result.token }
    }

    await authStore.loginWithPasskey(proof)
    // This path has completed authentication or account binding. Clear referral
    // attribution before navigation so a later signup does not reuse the finished
    // flow context.
    clearAllAffiliateCodes()
    appStore.showSuccess(t('auth.loginSuccess'))
    const redirectTo = (router.currentRoute.value.query.redirect as string) || '/dashboard'
    await router.push(redirectTo)
  } catch (error: unknown) {
    const fallback =
      error instanceof DOMException && error.name === 'NotAllowedError'
        ? t('auth.passkeyCancelled')
        : t('auth.passkeyFailed')
    errorMessage.value = extractI18nErrorMessage(error, t, 'auth.errors', fallback)
    appStore.showError(errorMessage.value)
  } finally {
    // The passkey attempt has settled after acquiring action proof. Clear that
    // proof so a subsequent authentication attempt opens its own challenge.
    if (actionCaptchaEnabled.value) {
      resetCaptchaProof()
    }
    passkeyLoading.value = false
  }
}

// handleOAuthStart applies consent and loading gates before provider navigation.
// Without action captcha it opens the built start URL directly; otherwise it
// obtains proof, calls the start endpoint, and consumes proof after the request
// settles.
async function handleOAuthStart(request: OAuthLoginStart): Promise<void> {
  googleOneTapRef.value?.cancelPrompt()
  if (authActionDisabled.value || !ensureAgreementAccepted()) return

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

// handle2FAVerify authenticates the temporary login challenge with the supplied
// code. It marks the modal busy, closes it after success, and resets its input and
// busy state when verification fails.
async function handle2FAVerify(code: string): Promise<void> {
  if (totpModalRef.value) {
    totpModalRef.value.setVerifying(true)
  }

  try {
    await authStore.login2FA(totpTempToken.value, code)

    show2FAModal.value = false
    appStore.showSuccess(t('auth.loginSuccess'))

    const redirectTo = (router.currentRoute.value.query.redirect as string) || '/dashboard'
    await router.push(redirectTo)
  } catch (error: unknown) {
    const err = error as {
      message?: string // reported failure message
      response?: {
        data?: {
          message?: string // reported failure message
        } // HTTP response payload
      } // optional HTTP error response
    }
    const message = err.response?.data?.message || err.message || t('profile.totp.loginFailed')

    if (totpModalRef.value) {
      totpModalRef.value.setError(message)
      totpModalRef.value.setVerifying(false)
    }
  }
}

// handle2FACancel hides the second-factor modal and discards its challenge token.
// The masked email is cleared with the token so a later password login starts with
// its own challenge context.
function handle2FACancel(): void {
  show2FAModal.value = false
  totpTempToken.value = ''
  totpUserEmailMasked.value = ''
}
</script>

<style scoped></style>
