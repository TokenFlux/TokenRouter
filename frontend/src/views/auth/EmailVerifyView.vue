<template>
  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('auth.verifyYourEmail') }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('auth.sendCodeDesc') }}
          <span class="font-medium text-gray-700 dark:text-gray-300">{{ email }}</span>
        </p>
      </div>

      <div
        v-if="!hasRegisterData"
        class="rounded-surface border border-amber-200 bg-amber-50 p-4 dark:border-amber-800/50 dark:bg-amber-900/20"
      >
        <div class="flex items-start gap-3">
          <div class="flex-shrink-0">
            <Icon name="exclamationCircle" size="md" class="text-amber-500" />
          </div>
          <div class="text-sm text-amber-700 dark:text-amber-400">
            <p class="font-medium">{{ t('auth.sessionExpired') }}</p>
            <p class="mt-1">{{ t('auth.sessionExpiredDesc') }}</p>
          </div>
        </div>
      </div>

      <form v-else @submit.prevent="handleVerify" class="space-y-5">
        <div>
          <label for="code" class="input-label text-center">
            {{ t('auth.verificationCode') }}
          </label>
          <input
            id="code"
            v-model="verifyCode"
            type="text"
            required
            autocomplete="one-time-code"
            inputmode="numeric"
            maxlength="6"
            :disabled="isLoading"
            class="input py-3 text-center font-mono text-xl tracking-[0.5em]"
            :class="{ 'input-error': errors.code }"
            placeholder="000000"
          />
          <p class="input-hint text-center">{{ t('auth.verificationCodeHint') }}</p>
        </div>

        <div
          v-if="codeSent"
          class="rounded-surface border border-green-200 bg-green-50 p-4 dark:border-green-800/50 dark:bg-green-900/20"
        >
          <div class="flex items-start gap-3">
            <div class="flex-shrink-0">
              <Icon name="checkCircle" size="md" class="text-green-500" :animate-on-hover="false" />
            </div>
            <p class="text-sm text-green-700 dark:text-green-400">
              {{ t('auth.codeSentSuccess') }}
            </p>
          </div>
        </div>

        <div v-if="actionCaptchaEnabled || (turnstileEnabled && showResendTurnstile)">
          <TurnstileWidget
            ref="turnstileRef"
            :site-key="turnstileSiteKey"
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

        <div v-if="pendingOAuthCreateCaptchaEnabled" class="space-y-2">
          <TurnstileWidget
            ref="createAccountTurnstileRef"
            :site-key="turnstileSiteKey"
            :turnstile-enabled="turnstileEnabled"
            :turnstile-site-key="turnstileSiteKey"
            :tencent-enabled="tencentCaptchaEnabled"
            :tencent-app-id="tencentCaptchaAppId"
            :tencent-region="tencentCaptchaRegion"
            :aliyun-enabled="aliyunCaptchaEnabled"
            :aliyun-scene-id="aliyunCaptchaSceneId"
            :aliyun-prefix="aliyunCaptchaPrefix"
            :aliyun-region="aliyunCaptchaRegion"
            @verify="onCreateAccountTurnstileVerify"
            @expire="onCreateAccountTurnstileExpire"
            @error="onCreateAccountTurnstileError"
          />
        </div>

        <button
          type="submit"
          :disabled="
            isLoading ||
            !verifyCode ||
            (pendingOAuthCreateTurnstileRequired && !createAccountTurnstileToken)
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
          <Icon v-else name="checkCircle" size="sm" class="mr-2" :animate-on-hover="false" />
          {{ isLoading ? t('auth.verifying') : t('auth.verifyAndCreate') }}
        </button>

        <div class="text-center">
          <button
            v-if="countdown > 0"
            type="button"
            disabled
            class="cursor-not-allowed text-sm text-gray-400 dark:text-dark-500"
          >
            {{ t('auth.resendCountdown', { countdown }) }}
          </button>
          <button
            v-else
            type="button"
            @click="handleResendCode"
            :disabled="
              isSendingCode || (turnstileEnabled && showResendTurnstile && !resendTurnstileToken)
            "
            class="text-sm text-primary-600 transition-colors hover:text-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-400 dark:hover:text-primary-300"
          >
            <span v-if="isSendingCode">{{ t('auth.sendingCode') }}</span>
            <span v-else-if="captchaEnabled && !showResendTurnstile">
              {{ t('auth.clickToResend') }}
            </span>
            <span v-else>{{ t('auth.resendCode') }}</span>
          </button>
        </div>
      </form>
    </div>

    <template #footer>
      <button @click="handleBack" class="flex items-center gap-2 text-link">
        <Icon name="arrowLeft" size="sm" />
        {{ t('auth.backToRegistration') }}
      </button>
    </template>
  </AuthLayout>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/auth/CaptchaChallenge.vue'
import { useAuthStore, useAppStore } from '@/stores'
import {
  persistOAuthTokenContext,
  getPublicSettings,
  isOAuthLoginCompletion,
  type PendingOAuthSendVerifyCodeResponse,
  sendPendingOAuthVerifyCode,
  sendVerifyCode,
} from '@/api/auth'
import { apiClient } from '@/api/client'
import { buildAuthErrorMessage } from '@/utils/authError'
import { extractApiErrorCode } from '@/utils/apiError'
import {
  formatRegistrationEmailSuffixWhitelistForMessage,
  isRegistrationEmailSuffixAllowed,
  normalizeRegistrationEmailSuffixWhitelist,
} from '@/utils/registrationEmailPolicy'
import {
  clearAllAffiliateCodes,
  loadAffiliateCode,
  oauthAffiliatePayload,
} from '@/utils/oauthAffiliate'
import type { PublicSettings } from '@/api/settings.types'

// EmailVerifyView.vue restores a registration draft and verifies its email before
// completing signup. Ordinary registration and pending OAuth creation share the
// email code screen but use different endpoints and continuation metadata. Pending
// provider identity can come from saved registration data or the auth store,
// allowing an existing-account decision to return to its provider callback.
//
// Resend and account-creation captcha proofs remain separate. Sending a code
// attempts saved-proof cleanup before clearing memory, even on request failure.
// Suppressed parse or storage-write failures can leave the saved proof in place;
// pending account creation acquires its own proof. Completed registration removes
// stored credentials and attribution before navigation; unfinished OAuth responses
// retain only the pending continuation needed by the callback.

// t supplies authentication copy and locale controls formatting of permitted-email
// suffix lists. Route and store services own navigation and completion of the
// current registration action.
const { t, locale } = useI18n() // message resolver and active locale

const router = useRouter() // application navigation service
const authStore = useAuthStore() // shared authentication state owner
const appStore = useAppStore() // public settings and toast owner

// The request and display state controls authentication progress, failure
// feedback, and input visibility. Handlers publish successful completion
// separately from merely starting or settling a request.
const isLoading = ref<boolean>(false) // current form request busy flag
// isSendingCode gates email delivery controls while the request is pending.
// Delivery feedback and countdown values let the user distinguish sending from the
// server-required resend wait.
const isSendingCode = ref<boolean>(false) // email-code request busy flag
// errorMessage holds the latest page-level failure while the nearby draft retains
// user input. Toast delivery occurs in the corresponding watcher or request
// handler.
const errorMessage = ref<string>('') // current action failure message
// codeSent records successful verification delivery separately from request
// progress. The success content appears only after the code endpoint returns its
// delivery response.
const codeSent = ref<boolean>(false) // verification delivery success flag
// verifyCode holds the email code entered on the verification page. It remains
// independent from captcha proof and is validated before the registration request.
const verifyCode = ref<string>('') // entered email verification code
// countdown expresses the server resend delay in seconds. countdownTimer drives
// its visible decrease and is replaced or released as the verification page
// changes lifecycle.
const countdown = ref<number>(0) // remaining resend wait seconds
// countdownTimer owns the resend interval handle. Starting another countdown
// replaces its scheduled work, and component unmount releases the remaining
// interval.
let countdownTimer: ReturnType<typeof setInterval> | null = null // owned resend interval handle

// PendingAuthTokenField selects the backend field carrying a pending
// authentication token.
type PendingAuthTokenField = 'pending_auth_token' | 'pending_oauth_token'
// PendingAuthSessionSummary describes saved provider continuation metadata
// restored by email verification.
type PendingAuthSessionSummary = {
  token: string // pending authentication token

  token_field: PendingAuthTokenField // backend pending token field

  provider: string // authentication provider identifier

  redirect?: string // post-authentication destination path
}
// PendingOAuthCreateAccountResponse describes authenticated or unfinished
// responses from pending account creation.
type PendingOAuthCreateAccountResponse = {
  auth_result?: string // authentication completion discriminator

  access_token: string // authenticated account access token

  refresh_token?: string // refresh session token

  expires_in?: number // seconds

  token_type?: string // authorization token type

  provider?: string // authentication provider identifier

  redirect?: string // post-authentication destination path
}

// Restored registration credentials supply code delivery and final account
// submission. This page displays the email but exposes no credential editors;
// captcha proof and verification-code inputs have their own state below.
const email = ref<string>('') // restored registration account email
const password = ref<string>('') // restored registration account password
// initialTurnstileToken restores the proof carried from registration. After a
// request uses proof, sendCode attempts saved-proof cleanup before clearing this
// field. Suppressed storage write failures can retain consumed proof in the draft;
// a failed storage read interrupts cleanup before this local field is cleared.
const initialTurnstileToken = ref<string>('') // initial saved verification proof
// initialTencentCaptchaRandstr accompanies the saved Tencent ticket. sendCode
// clears this local field with the ticket after saved-proof cleanup returns;
// persistence failure can let a reload restore the consumed pair.
const initialTencentCaptchaRandstr = ref<string>('') // initial saved Tencent random string
// Saved promotion, invitation, and referral values accompany the restored
// credential draft. Pending token metadata and adoption choices select OAuth
// continuation when that draft came from a provider flow.
const promoCode = ref<string>('') // pending registration promotion code
const invitationCode = ref<string>('') // entered signup invitation code
const affCode = ref<string>('') // pending registration referral code
const pendingAuthToken = ref<string>('') // restored pending authentication token
// pendingAuthTokenField selects the request property paired with pendingAuthToken.
// Restored draft metadata takes precedence over the pending auth-store session
// field.
const pendingAuthTokenField = ref<PendingAuthTokenField>('pending_auth_token') // restored pending token field name
const pendingProvider = ref<string>('') // restored authentication provider identity
const pendingRedirect = ref<string>('') // restored authentication continuation destination
// pendingAdoptionDecision restores explicit name and avatar choices from the
// verification draft. Null leaves those request fields absent until a previous
// step supplied decisions.
const pendingAdoptionDecision = ref<{
  adoptDisplayName?: boolean // display name adoption choice

  adoptAvatar?: boolean // avatar adoption choice
} | null>(null) // restored explicit profile adoption choices
const hasRegisterData = ref<boolean>(false) // restored credential draft availability

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
const registrationEmailSuffixWhitelist = ref<string[]>([]) // normalized permitted signup email suffixes
// emailDomainQuotaEnabled delegates non-whitelisted address admission to backend
// domain quotas. Without that policy, the signup form applies the configured
// suffix whitelist locally.
const emailDomainQuotaEnabled = ref<boolean>(false) // backend email-domain quota policy

// These values carry public challenge configuration and local verification state
// into the widget and request handlers. Provider-specific token and random-string
// fields travel together when Tencent is enabled.
const turnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null) // captcha widget action handle
const createAccountTurnstileRef = ref<InstanceType<typeof TurnstileWidget> | null>(null) // account-creation challenge widget handle
// resendTurnstileToken takes precedence over initial saved proof during email
// delivery. Requests that dispatch proof clear it after clearStoredCaptchaProof
// returns; a failed storage read interrupts that cleanup sequence.
const resendTurnstileToken = ref<string>('') // fresh resend challenge proof
const resendTencentCaptchaRandstr = ref<string>('') // paired resend Tencent random string
// createAccountTurnstileToken belongs to pending OAuth account creation
// independently of resend proof. The final creation attempt resets its dedicated
// widget after settling.
const createAccountTurnstileToken = ref<string>('') // fresh account-creation challenge proof
const createAccountTencentCaptchaRandstr = ref<string>('') // paired creation Tencent random string
const showResendTurnstile = ref<boolean>(false) // resend challenge display flag
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

// errors holds per-field validation feedback for the current form. The derived
// toast selector chooses one active message so input validation has a single
// visible notification source.
const errors = ref({
  code: '', // code field validation message
  turnstile: '', // turnstile field validation message
}) // field validation message collection

// validationToastMessage selects the first currently active validation error for
// the toast watcher. Empty values clear the selection without emitting a
// notification.
const validationToastMessage = computed(() => errors.value.code || errors.value.turnstile || '') // first active validation toast message
// Pending OAuth creation has its own captcha requirements and widget proof. These
// computed flags enable that form gate independently from the resend-code
// challenge.
const pendingOAuthCreateTurnstileRequired = computed(
  () => isPendingOAuthFlow() && turnstileEnabled.value,
) // pending creation Turnstile requirement
const pendingOAuthCreateCaptchaEnabled = computed(
  () => isPendingOAuthFlow() && captchaEnabled.value,
) // pending creation captcha availability

// Only changed nonempty validation messages reach the app toast, preventing an
// unchanged reactive error from producing repeated notifications during field
// updates.
watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// Restore saved credentials and pending provider metadata before loading
// verification policy, so the initial email-code request selects the endpoint and
// proof belonging to this continuation.
onMounted(async () => {
  const activePendingSession = authStore.pendingAuthSession as PendingAuthSessionSummary | null

  const registerDataStr = sessionStorage.getItem('register_data')
  if (registerDataStr) {
    try {
      const registerData = JSON.parse(registerDataStr)
      email.value = registerData.email || ''
      password.value = registerData.password || ''
      initialTurnstileToken.value =
        registerData.tencent_captcha_ticket || registerData.turnstile_token || ''
      initialTencentCaptchaRandstr.value = registerData.tencent_captcha_randstr || ''
      promoCode.value = registerData.promo_code || ''
      invitationCode.value = registerData.invitation_code || ''
      affCode.value = registerData.aff_code || loadAffiliateCode()
      pendingAuthToken.value = registerData.pending_auth_token || activePendingSession?.token || ''
      pendingAuthTokenField.value =
        registerData.pending_auth_token_field ||
        activePendingSession?.token_field ||
        'pending_auth_token'
      pendingProvider.value = registerData.pending_provider || activePendingSession?.provider || ''
      pendingRedirect.value = registerData.pending_redirect || activePendingSession?.redirect || ''
      pendingAdoptionDecision.value = registerData.pending_adoption_decision
        ? {
            adoptDisplayName: registerData.pending_adoption_decision.adopt_display_name === true,
            adoptAvatar: registerData.pending_adoption_decision.adopt_avatar === true,
          }
        : null
      hasRegisterData.value = !!(email.value && password.value)
    } catch {
      // A malformed saved draft cannot supply valid registration credentials. Mark
      // it unavailable so mounted initialization skips the automatic
      // verification-code request.
      hasRegisterData.value = false
    }
  } else if (activePendingSession) {
    pendingAuthToken.value = activePendingSession.token
    pendingAuthTokenField.value = activePendingSession.token_field
    pendingProvider.value = activePendingSession.provider
    pendingRedirect.value = activePendingSession.redirect || ''
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
    publicSettings.value = settings
    registrationEmailSuffixWhitelist.value = normalizeRegistrationEmailSuffixWhitelist(
      settings.registration_email_suffix_whitelist || [],
    )
    emailDomainQuotaEnabled.value = settings.registration_email_domain_quota_enabled === true
  } catch (error) {
    // A settings fetch failure leaves the current page defaults available. The
    // catch path reports the diagnostic rather than rejecting component
    // initialization.
    console.error('Failed to load public settings:', error)
  }

  if (hasRegisterData.value) {
    await sendCode()
  }
})

// Release the component-owned timer or rendered resource when this view leaves, so
// detached UI does not retain its scheduled work.
onUnmounted(() => {
  // Release the existing resend interval before replacement or cleanup, preventing
  // an old timer from continuing to decrement this component countdown.
  if (countdownTimer) {
    clearInterval(countdownTimer)
    countdownTimer = null
  }
})

// resolveLocalizedSiteName returns the configured trimmed site name for success
// messages. Missing or blank settings use TokenRouter, including the period before
// the public-settings request has completed.
function resolveLocalizedSiteName(settings: PublicSettings | null): string {
  return settings?.site_name?.trim() || 'TokenRouter'
}

// startCountdown replaces the resend timer with a countdown in seconds. The
// interval updates the displayed remaining time once per second and clears its
// handle at expiry; component cleanup also releases it.
function startCountdown(seconds: number): void {
  countdown.value = seconds

  // Release the existing resend interval before replacement or cleanup, preventing
  // an old timer from continuing to decrement this component countdown.
  if (countdownTimer) {
    clearInterval(countdownTimer)
  }

  // Tick once per second to match the countdown units returned by the verification
  // endpoint, and retain the handle for replacement or component cleanup.
  countdownTimer = setInterval(() => {
    if (countdown.value > 0) {
      countdown.value--
    } else {
      // Release the existing resend interval before replacement or cleanup,
      // preventing an old timer from continuing to decrement this component
      // countdown.
      if (countdownTimer) {
        clearInterval(countdownTimer)
        countdownTimer = null
      }
    }
  }, 1000)
}

// onTurnstileVerify stores the challenge token and optional Tencent random string.
// It clears the captcha error so the current proof can enable the next guarded
// form action.
function onTurnstileVerify(token: string, randstr = ''): void {
  resendTurnstileToken.value = token
  resendTencentCaptchaRandstr.value = randstr
  errors.value.turnstile = ''
}

// onTurnstileExpire clears both challenge proof fields after widget expiration. It
// publishes the translated expiry error, requiring a later verification callback
// or action challenge before guarded submission can proceed.
function onTurnstileExpire(): void {
  resendTurnstileToken.value = ''
  resendTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileExpired')
}

// onTurnstileError clears both challenge proof fields after widget failure. It
// publishes the translated verification error so a failed proof cannot remain
// available for a later form submission.
function onTurnstileError(): void {
  resendTurnstileToken.value = ''
  resendTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileFailed')
}

// onCreateAccountTurnstileVerify stores the account-creation challenge token and
// optional Tencent random string. It clears the captcha error so the current proof
// can enable the next guarded form action.
function onCreateAccountTurnstileVerify(token: string, randstr = ''): void {
  createAccountTurnstileToken.value = token
  createAccountTencentCaptchaRandstr.value = randstr
  errors.value.turnstile = ''
}

// onCreateAccountTurnstileExpire clears both account-creation challenge proof
// fields after widget expiration. It publishes the translated expiry error,
// requiring a later verification callback or action account-creation challenge
// before guarded submission can proceed.
function onCreateAccountTurnstileExpire(): void {
  createAccountTurnstileToken.value = ''
  createAccountTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileExpired')
}

// onCreateAccountTurnstileError clears both account-creation challenge proof
// fields after widget failure. It publishes the translated verification error so a
// failed proof cannot remain available for a later form submission.
function onCreateAccountTurnstileError(): void {
  createAccountTurnstileToken.value = ''
  createAccountTencentCaptchaRandstr.value = ''
  errors.value.turnstile = t('auth.turnstileFailed')
}

// resetCreateAccountTurnstile clears the account-creation proof and resets its
// dedicated widget. Resend proof remains separate, so completing or failing
// account creation does not overwrite that other challenge context.
function resetCreateAccountTurnstile(): void {
  createAccountTurnstileToken.value = ''
  createAccountTencentCaptchaRandstr.value = ''
  createAccountTurnstileRef.value?.reset()
}

// acquireResendActionProof obtains a fresh proof only for enabled action-based
// captcha providers. It returns true immediately otherwise; cancellation or an
// unavailable widget returns false, and successful proof fields replace the resend
// values.
async function acquireResendActionProof(): Promise<boolean> {
  if (!actionCaptchaEnabled.value) return true

  const proof = await turnstileRef.value?.verifyAction()
  if (!proof) return false

  resendTurnstileToken.value = proof.token
  resendTencentCaptchaRandstr.value = proof.randstr
  return true
}

// acquireCreateAccountActionProof requests the dedicated proof only for pending
// OAuth account creation with action captcha enabled. Other flows return true
// immediately; missing proof returns false and leaves submission to the caller.
async function acquireCreateAccountActionProof(): Promise<boolean> {
  if (!isPendingOAuthFlow() || !actionCaptchaEnabled.value) return true

  const proof = await createAccountTurnstileRef.value?.verifyAction()
  if (!proof) return false

  createAccountTurnstileToken.value = proof.token
  createAccountTencentCaptchaRandstr.value = proof.randstr
  return true
}

// isPendingOAuthFlow reports whether a nonblank pending provider identifies OAuth
// registration.
function isPendingOAuthFlow(): boolean {
  return Boolean(pendingProvider.value.trim())
}

// shouldBypassRegistrationEmailPolicy delegates email eligibility to the backend
// for quota or pending-auth flows. Pending OAuth may resolve an existing account
// outside the signup whitelist, so those addresses reach backend identity
// resolution.
function shouldBypassRegistrationEmailPolicy(): boolean {
  return (
    emailDomainQuotaEnabled.value || isPendingOAuthFlow() || Boolean(pendingAuthToken.value.trim())
  )
}

// resolvePendingOAuthCallbackRoute maps normalized LinuxDo, OIDC, and WeChat names
// to their callbacks. All other names use the generic callback route, including
// empty provider values and unlisted provider types.
function resolvePendingOAuthCallbackRoute(provider: string): string {
  switch (provider.trim().toLowerCase()) {
    case 'linuxdo':
      return '/auth/linuxdo/callback'
    case 'oidc':
      return '/auth/oidc/callback'
    case 'wechat':
      return '/auth/wechat/callback'
    default:
      return '/auth/callback'
  }
}

// isPendingOAuthSessionResponse identifies an unfinished account action by
// auth_result.
function isPendingOAuthSessionResponse(data: PendingOAuthCreateAccountResponse): boolean {
  return data.auth_result === 'pending_session'
}

// getPendingOAuthSendCodeSessionResponse returns a send-code response only when it
// describes a pending session. Other responses return null, allowing sendCode to
// distinguish an account-action transition from a delivered verification code.
function getPendingOAuthSendCodeSessionResponse(
  data: PendingOAuthSendVerifyCodeResponse,
): PendingOAuthSendVerifyCodeResponse | null {
  return data.auth_result === 'pending_session' ? data : null
}

// persistPendingOAuthSession retains the pending token representation and provider
// for callback recovery. A supplied nonblank provider and truthy redirect take
// precedence over the current page metadata, preserving the continuation across
// navigation.
function persistPendingOAuthSession(provider: string, redirect?: string): void {
  authStore.setPendingAuthSession({
    token: pendingAuthToken.value,
    token_field: pendingAuthTokenField.value,
    provider: provider.trim() || pendingProvider.value.trim(),
    redirect: redirect || pendingRedirect.value || undefined,
  })
}

// sendCode sends email verification through ordinary or pending OAuth endpoints.
// It chooses resend proof before the initial proof and delegates pending account
// transitions back to the provider callback after removing registration data.
// Requests that include proof attempt saved-proof cleanup before clearing local
// fields, even after request failure. Suppressed parse or write errors can leave
// stored proof intact; a storage read error propagates before local cleanup.
// Failed Turnstile requests reopen the challenge, while a delivered code starts
// the server-provided resend countdown.
async function sendCode(): Promise<void> {
  isSendingCode.value = true
  errorMessage.value = ''
  let requestSucceeded = false
  let captchaProofUsed = false

  try {
    if (
      !shouldBypassRegistrationEmailPolicy() &&
      !isRegistrationEmailSuffixAllowed(email.value, registrationEmailSuffixWhitelist.value)
    ) {
      errorMessage.value = buildEmailSuffixNotAllowedMessage()
      appStore.showError(errorMessage.value)
      return
    }

    const requestPayload = {
      email: email.value,
      [pendingAuthTokenField.value]: pendingAuthToken.value || undefined,
      // Prefer newly acquired resend proof over the initial saved proof because
      // earlier code delivery may already have consumed the initial challenge
      // result.
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value
          ? resendTurnstileToken.value || initialTurnstileToken.value || undefined
          : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value
        ? resendTurnstileToken.value || initialTurnstileToken.value || undefined
        : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? resendTencentCaptchaRandstr.value || initialTencentCaptchaRandstr.value || undefined
        : undefined,
    } as Parameters<typeof sendVerifyCode>[0]
    captchaProofUsed = Boolean(
      requestPayload.turnstile_token || requestPayload.tencent_captcha_ticket,
    )
    const response = isPendingOAuthFlow()
      ? await sendPendingOAuthVerifyCode(requestPayload)
      : await sendVerifyCode(requestPayload)
    requestSucceeded = true

    const pendingSendCodeSession = isPendingOAuthFlow()
      ? getPendingOAuthSendCodeSessionResponse(response as PendingOAuthSendVerifyCodeResponse)
      : null
    // Code delivery can instead resolve an existing account. Preserve the returned
    // continuation and leave verification before treating that response as a
    // delivered code.
    if (pendingSendCodeSession) {
      // Remove the credential-bearing draft before leaving this registration step,
      // so the next route cannot accidentally resume the completed or abandoned
      // draft.
      sessionStorage.removeItem('register_data')
      persistPendingOAuthSession(
        pendingSendCodeSession.provider || pendingProvider.value,
        pendingSendCodeSession.redirect,
      )
      await router.push(
        resolvePendingOAuthCallbackRoute(pendingSendCodeSession.provider || pendingProvider.value),
      )
      return
    }

    codeSent.value = true
    startCountdown(response.countdown)

    showResendTurnstile.value = false
  } catch (error: unknown) {
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.sendCodeFailed'))

    appStore.showError(errorMessage.value)
  } finally {
    // Any dispatched proof may have been consumed even if the request failed.
    // Attempt saved-proof removal and clear local copies before exposing another
    // resend attempt; clearStoredCaptchaProof suppresses persistence failures.
    if (captchaProofUsed) {
      clearStoredCaptchaProof()
      initialTurnstileToken.value = ''
      initialTencentCaptchaRandstr.value = ''
      resendTurnstileToken.value = ''
      resendTencentCaptchaRandstr.value = ''
      turnstileRef.value?.reset()
      if (!requestSucceeded && turnstileEnabled.value) {
        showResendTurnstile.value = true
      }
    }
    isSendingCode.value = false
  }
}

// clearStoredCaptchaProof attempts to remove consumed captcha fields from saved
// registration data while retaining other draft fields. JSON parsing and storage
// writes share a suppressed error path, so a failed write can leave consumed
// proof persisted; the initial storage read occurs outside that catch.
function clearStoredCaptchaProof(): void {
  const registerDataStr = sessionStorage.getItem('register_data')
  if (!registerDataStr) return

  try {
    const registerData = JSON.parse(registerDataStr) as Record<string, unknown>
    delete registerData.turnstile_token
    delete registerData.tencent_captcha_ticket
    delete registerData.tencent_captcha_randstr
    sessionStorage.setItem('register_data', JSON.stringify(registerData))
  } catch {
    // Suppress parse and persistence failures so sendCode can finish its local
    // proof cleanup. Malformed data stays unreadable, and a failed storage write
    // can leave the consumed proof available in the saved registration draft.
  }
}

// handleResendCode prepares a fresh proof before repeating email verification.
// Turnstile first reveals its widget and waits for a token; action captcha runs on
// the click, and cancellation leaves the send endpoint untouched.
async function handleResendCode(): Promise<void> {
  // Turnstile needs its visible widget to prepare proof before sending, while
  // action captcha opens during this click and can cancel the request.
  if (turnstileEnabled.value && !showResendTurnstile.value) {
    showResendTurnstile.value = true
    return
  }

  if (turnstileEnabled.value && !resendTurnstileToken.value) {
    errors.value.turnstile = t('auth.completeVerification')
    return
  }

  if (!(await acquireResendActionProof())) {
    return
  }

  await sendCode()
}

// validateForm checks the trimmed email verification code and updates its
// validation message. Empty input and values other than six digits return false,
// stopping verification before any account endpoint is called.
function validateForm(): boolean {
  errors.value.code = ''

  if (!verifyCode.value.trim()) {
    errors.value.code = t('auth.codeRequired')
    return false
  }

  if (!/^\d{6}$/.test(verifyCode.value.trim())) {
    errors.value.code = t('auth.invalidCode')
    return false
  }

  return true
}

// handleVerify completes registration after validating the email code and current
// email policy. Pending OAuth uses its separate creation proof and can return to a
// provider callback without installing a token. Successful login persists token
// context before accepting the token and removing saved registration data.
// Ordinary signup delegates to the auth store, and both completed paths clear
// affiliate state before navigation; failure keeps the draft while consuming local
// proof.
async function handleVerify(): Promise<void> {
  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  if (
    !shouldBypassRegistrationEmailPolicy() &&
    !isRegistrationEmailSuffixAllowed(email.value, registrationEmailSuffixWhitelist.value)
  ) {
    errorMessage.value = buildEmailSuffixNotAllowedMessage()
    appStore.showError(errorMessage.value)
    return
  }

  if (!(await acquireCreateAccountActionProof())) {
    return
  }

  isLoading.value = true

  try {
    if (isPendingOAuthFlow()) {
      const payload: Record<string, unknown> = {
        email: email.value,
        password: password.value,
        verify_code: verifyCode.value.trim(),
        ...((turnstileEnabled.value || aliyunCaptchaEnabled.value) &&
        createAccountTurnstileToken.value
          ? { turnstile_token: createAccountTurnstileToken.value }
          : {}),
        ...(tencentCaptchaEnabled.value && createAccountTurnstileToken.value
          ? {
              tencent_captcha_ticket: createAccountTurnstileToken.value,
              tencent_captcha_randstr: createAccountTencentCaptchaRandstr.value,
            }
          : {}),
        ...oauthAffiliatePayload(affCode.value || loadAffiliateCode()),
      }
      if (invitationCode.value) {
        payload.invitation_code = invitationCode.value
      }
      if (pendingAdoptionDecision.value?.adoptDisplayName !== undefined) {
        payload.adopt_display_name = pendingAdoptionDecision.value.adoptDisplayName
      }
      if (pendingAdoptionDecision.value?.adoptAvatar !== undefined) {
        payload.adopt_avatar = pendingAdoptionDecision.value.adoptAvatar
      }

      const { data } = await apiClient.post<PendingOAuthCreateAccountResponse>(
        '/auth/oauth/pending/create-account',
        payload,
      )
      // Account creation can still require binding. Retain the returned
      // continuation before navigation and leave token installation for a later
      // authenticated response.
      if (isPendingOAuthSessionResponse(data)) {
        // Remove the credential-bearing draft before leaving this registration
        // step, so the next route cannot accidentally resume the completed or
        // abandoned draft.
        sessionStorage.removeItem('register_data')
        persistPendingOAuthSession(data.provider || pendingProvider.value, data.redirect)
        await router.push(resolvePendingOAuthCallbackRoute(data.provider || pendingProvider.value))
        return
      }
      if (!isOAuthLoginCompletion(data)) {
        throw new Error(t('auth.verifyFailed'))
      }

      // Install the returned refresh context before the access token so session
      // initialization observes the metadata belonging to this authenticated
      // response.
      persistOAuthTokenContext(data)
      await authStore.setToken(data.access_token)
      authStore.clearPendingAuthSession?.()
    } else {
      await authStore.register({
        email: email.value,
        password: password.value,
        verify_code: verifyCode.value.trim(),
        turnstile_token:
          turnstileEnabled.value || aliyunCaptchaEnabled.value
            ? initialTurnstileToken.value || undefined
            : undefined,
        tencent_captcha_ticket: tencentCaptchaEnabled.value
          ? initialTurnstileToken.value || undefined
          : undefined,
        tencent_captcha_randstr: tencentCaptchaEnabled.value
          ? initialTencentCaptchaRandstr.value || undefined
          : undefined,
        promo_code: promoCode.value || undefined,
        invitation_code: invitationCode.value || undefined,
        aff_code: affCode.value || undefined,
      })
    }

    // Remove the credential-bearing draft before leaving this registration step,
    // so the next route cannot accidentally resume the completed or abandoned
    // draft.
    sessionStorage.removeItem('register_data')
    // This path has completed authentication or account binding. Clear referral
    // attribution before navigation so a later signup does not reuse the finished
    // flow context.
    clearAllAffiliateCodes()

    appStore.showSuccess(t('auth.accountCreatedSuccess', { siteName: siteName.value }))

    await router.push(pendingRedirect.value || '/dashboard')
  } catch (error: unknown) {
    errorMessage.value = buildRegistrationErrorMessage(error, t('auth.verifyFailed'))

    appStore.showError(errorMessage.value)
  } finally {
    initialTurnstileToken.value = ''
    initialTencentCaptchaRandstr.value = ''
    // The account-creation attempt has settled, so reset its dedicated challenge
    // without replacing the separate proof state used by email resends.
    if (pendingOAuthCreateCaptchaEnabled.value) {
      resetCreateAccountTurnstile()
    }
    isLoading.value = false
  }
}

// handleBack discards the saved registration draft before opening the registration
// page. This also removes its stored password and initial proof from session
// storage when the user abandons verification.
function handleBack(): void {
  // Remove the credential-bearing draft before leaving this registration step, so
  // the next route cannot accidentally resume the completed or abandoned draft.
  sessionStorage.removeItem('register_data')

  router.push('/register')
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
</script>

<style scoped></style>
