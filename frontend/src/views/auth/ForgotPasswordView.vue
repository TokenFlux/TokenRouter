<template>
  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('auth.forgotPasswordTitle') }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('auth.forgotPasswordHint') }}
        </p>
      </div>

      <div v-if="isSubmitted" class="space-y-6">
        <div
          class="rounded-surface border border-green-200 bg-green-50 p-6 dark:border-green-800/50 dark:bg-green-900/20"
        >
          <div class="flex flex-col items-center gap-4 text-center">
            <div
              class="flex h-12 w-12 items-center justify-center rounded-full bg-green-100 dark:bg-green-800/50"
            >
              <Icon
                name="checkCircle"
                size="lg"
                class="text-green-600 dark:text-green-400"
                :animate-on-hover="false"
              />
            </div>
            <div>
              <h3 class="text-lg font-semibold text-green-800 dark:text-green-200">
                {{ t('auth.resetEmailSent') }}
              </h3>
              <p class="mt-2 text-sm text-green-700 dark:text-green-300">
                {{ t('auth.resetEmailSentHint') }}
              </p>
            </div>
          </div>
        </div>

        <div class="text-center">
          <router-link to="/login" class="inline-flex items-center gap-2 font-medium text-link">
            <Icon name="arrowLeft" size="sm" />
            {{ t('auth.backToLogin') }}
          </router-link>
        </div>
      </div>

      <form v-else @submit.prevent="handleSubmit" class="space-y-5">
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
              :disabled="isLoading"
              class="input input-has-icon"
              :class="{ 'input-error': errors.email }"
              :placeholder="t('auth.emailPlaceholder')"
            />
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

        <button
          type="submit"
          :disabled="isLoading || (turnstileEnabled && !turnstileToken)"
          class="btn btn-primary w-full"
        >
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="isLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
          />
          <Icon v-else name="mail" size="sm" class="mr-2" />
          {{ isLoading ? t('auth.sendingResetLink') : t('auth.sendResetLink') }}
        </button>
      </form>
    </div>

    <template #footer>
      <p class="text-gray-500 dark:text-dark-400">
        {{ t('auth.rememberedPassword') }}
        <router-link to="/login" class="font-medium text-link">
          {{ t('auth.signIn') }}
        </router-link>
      </p>
    </template>
  </AuthLayout>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import Icon from '@/components/icons/Icon.vue'
import TurnstileWidget from '@/components/auth/CaptchaChallenge.vue'
import { useAppStore } from '@/stores'
import { getPublicSettings, forgotPassword } from '@/api/auth'

// ForgotPasswordView.vue requests a password-reset email using the configured
// captcha provider. It validates the address before acquiring action proof,
// displays endpoint failures through the app toast, and clears consumed proof
// after each request settles.

// t resolves the authentication messages displayed by this component. Nearby store
// and navigation services deliver those messages and handle the requested
// authentication transition.
const { t } = useI18n() // translated authentication message resolver

const appStore = useAppStore() // public settings and toast owner

// The request and display state controls authentication progress, failure
// feedback, and input visibility. Handlers publish successful completion
// separately from merely starting or settling a request.
const isLoading = ref<boolean>(false) // current form request busy flag
const isSubmitted = ref<boolean>(false) // recovery email confirmation screen flag
const errorMessage = ref<string>('') // current action failure message

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

// formData owns the editable authentication values read by submission. errors
// records local validation feedback without replacing the draft, allowing the user
// to correct a failed attempt.
const formData = reactive({
  email: '', // entered account email
}) // editable authentication form draft

const errors = reactive({
  email: '', // email field validation message
  turnstile: '', // turnstile field validation message
}) // field validation message collection

// validationToastMessage selects the first currently active validation error for
// the toast watcher. Empty values clear the selection without emitting a
// notification.
const validationToastMessage = computed(() => errors.email || errors.turnstile || '') // first active validation toast message

// Only changed nonempty validation messages reach the app toast, preventing an
// unchanged reactive error from producing repeated notifications during field
// updates.
watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// Initialize this page from public settings before user actions depend on its
// configured authentication requirements, retaining the documented defaults when
// that request fails.
onMounted(async () => {
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
  } catch (error) {
    // A settings fetch failure leaves the current page defaults available. The
    // catch path reports the diagnostic rather than rejecting component
    // initialization.
    console.error('Failed to load public settings:', error)
  }
})

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

// validateForm checks the current inputs and updates their validation messages. It
// returns whether all required fields and prepared Turnstile proof pass, allowing
// submission handlers to stop before action captcha or network work.
function validateForm(): boolean {
  errors.email = ''
  errors.turnstile = ''

  let isValid = true

  if (!formData.email.trim()) {
    errors.email = t('auth.emailRequired')
    isValid = false
  } else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(formData.email)) {
    errors.email = t('auth.invalidEmail')
    isValid = false
  }

  if (turnstileEnabled.value && !turnstileToken.value) {
    errors.turnstile = t('auth.completeVerification')
    isValid = false
  }

  return isValid
}

// handleSubmit requests a password-reset email after local validation and action
// captcha. Success switches to confirmation content; endpoint detail or a fallback
// message is toasted on failure, and enabled proof is reset afterward.
async function handleSubmit(): Promise<void> {
  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  if (!(await acquireActionProof())) {
    return
  }

  isLoading.value = true

  try {
    await forgotPassword({
      email: formData.email,
      turnstile_token:
        turnstileEnabled.value || aliyunCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_ticket: tencentCaptchaEnabled.value ? turnstileToken.value : undefined,
      tencent_captcha_randstr: tencentCaptchaEnabled.value
        ? tencentCaptchaRandstr.value
        : undefined,
    })

    isSubmitted.value = true
    appStore.showSuccess(t('auth.resetEmailSent'))
  } catch (error: unknown) {
    const err = error as {
      message?: string // reported failure message
      response?: {
        data?: {
          detail?: string // server detailed failure message
        } // HTTP response payload
      } // optional HTTP error response
    }

    if (err.response?.data?.detail) {
      errorMessage.value = err.response.data.detail
    } else if (err.message) {
      errorMessage.value = err.message
    } else {
      errorMessage.value = t('auth.sendResetLinkFailed')
    }

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
</script>

<style scoped></style>
