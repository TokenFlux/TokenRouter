<template>
  <AuthLayout>
    <div class="space-y-6">
      <div class="text-center">
        <h2 class="text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('auth.resetPasswordTitle') }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
          {{ t('auth.resetPasswordHint') }}
        </p>
      </div>

      <div v-if="isInvalidLink" class="space-y-6">
        <div
          class="rounded-surface border border-amber-200 bg-amber-50 p-6 dark:border-amber-800/50 dark:bg-amber-900/20"
        >
          <div class="flex flex-col items-center gap-4 text-center">
            <div
              class="flex h-12 w-12 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-800/50"
            >
              <Icon name="exclamationCircle" size="lg" class="text-amber-600 dark:text-amber-400" />
            </div>
            <div>
              <h3 class="text-lg font-semibold text-amber-800 dark:text-amber-200">
                {{ t('auth.invalidResetLink') }}
              </h3>
              <p class="mt-2 text-sm text-amber-700 dark:text-amber-300">
                {{ t('auth.invalidResetLinkHint') }}
              </p>
            </div>
          </div>
        </div>

        <div class="text-center">
          <router-link
            to="/forgot-password"
            class="inline-flex items-center gap-2 font-medium text-link"
          >
            {{ t('auth.requestNewResetLink') }}
          </router-link>
        </div>
      </div>

      <div v-else-if="isSuccess" class="space-y-6">
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
                {{ t('auth.passwordResetSuccess') }}
              </h3>
              <p class="mt-2 text-sm text-green-700 dark:text-green-300">
                {{ t('auth.passwordResetSuccessHint') }}
              </p>
            </div>
          </div>
        </div>

        <div class="text-center">
          <router-link to="/login" class="btn btn-primary inline-flex items-center gap-2">
            <Icon name="login" size="sm" />
            {{ t('auth.signIn') }}
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
              :value="email"
              type="email"
              readonly
              disabled
              class="input input-has-icon bg-gray-50 dark:bg-dark-700"
            />
          </div>
        </div>

        <div>
          <label for="password" class="input-label">
            {{ t('auth.newPassword') }}
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
              :disabled="isLoading"
              class="input input-has-icon input-has-icon-right"
              :class="{ 'input-error': errors.password }"
              :placeholder="t('auth.newPasswordPlaceholder')"
            />
            <button
              type="button"
              @click="showPassword = !showPassword"
              class="input-icon-right text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-dark-300"
            >
              <Icon v-if="showPassword" name="eyeOff" size="sm" />
              <Icon v-else name="eye" size="sm" />
            </button>
          </div>
        </div>

        <div>
          <label for="confirmPassword" class="input-label">
            {{ t('auth.confirmPassword') }}
          </label>
          <div class="input-icon-wrap input-icon-lg">
            <div class="input-icon">
              <Icon name="lock" size="md" class="text-gray-400 dark:text-dark-500" />
            </div>
            <input
              id="confirmPassword"
              v-model="formData.confirmPassword"
              :type="showConfirmPassword ? 'text' : 'password'"
              required
              autocomplete="new-password"
              :disabled="isLoading"
              class="input input-has-icon input-has-icon-right"
              :class="{ 'input-error': errors.confirmPassword }"
              :placeholder="t('auth.confirmPasswordPlaceholder')"
            />
            <button
              type="button"
              @click="showConfirmPassword = !showConfirmPassword"
              class="input-icon-right text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-dark-300"
            >
              <Icon v-if="showConfirmPassword" name="eyeOff" size="sm" />
              <Icon v-else name="eye" size="sm" />
            </button>
          </div>
        </div>

        <button type="submit" :disabled="isLoading" class="btn btn-primary w-full">
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="isLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin text-white"
          />
          <Icon v-else name="checkCircle" size="sm" class="mr-2" :animate-on-hover="false" />
          {{ isLoading ? t('auth.resettingPassword') : t('auth.resetPassword') }}
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
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { AuthLayout } from '@/components/layout'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { resetPassword } from '@/api/auth'

// ResetPasswordView.vue changes a password using the email and reset token from
// the route query. The page validates the replacement and confirmation locally,
// reports expired-token failures with dedicated copy, and switches to success
// content after the endpoint accepts the request.

// t resolves the authentication messages displayed by this component. Nearby store
// and navigation services deliver those messages and handle the requested
// authentication transition.
const { t } = useI18n() // translated authentication message resolver

const route = useRoute() // current callback query context
const appStore = useAppStore() // public settings and toast owner

// Password-reset state distinguishes an in-flight request from successful
// completion. The route credentials, replacement draft, visibility toggles, and
// validation feedback feed the corresponding template states.
const isLoading = ref<boolean>(false) // current form request busy flag
const isSuccess = ref<boolean>(false) // completed password-reset screen flag
const errorMessage = ref<string>('') // current action failure message
const showPassword = ref<boolean>(false) // password input visibility toggle
const showConfirmPassword = ref<boolean>(false) // confirmation input visibility toggle

const email = ref<string>('') // route-derived readonly account email
const token = ref<string>('') // route-provided password reset token

const formData = reactive({
  password: '', // entered account password
  confirmPassword: '', // matching password confirmation
}) // editable authentication form draft

const errors = reactive({
  password: '', // password field validation message
  confirmPassword: '', // confirmPassword field validation message
}) // field validation message collection

// validationToastMessage selects the first currently active validation error for
// the toast watcher. Empty values clear the selection without emitting a
// notification.
const validationToastMessage = computed(() => errors.password || errors.confirmPassword || '') // first active validation toast message

// Only changed nonempty validation messages reach the app toast, preventing an
// unchanged reactive error from producing repeated notifications during field
// updates.
watch(validationToastMessage, (value, previousValue) => {
  if (value && value !== previousValue) {
    appStore.showError(value)
  }
})

// isInvalidLink detects missing reset-query credentials before the reset form can
// proceed. The route email and token remain the values sent with a validated
// replacement password.
const isInvalidLink = computed(() => !email.value || !token.value) // missing reset-query credential flag

// Read the reset email and token from the route on mount. Missing credentials show
// invalid-link feedback and keep the replacement-password form unavailable.
onMounted(() => {
  email.value = (route.query.email as string) || ''
  token.value = (route.query.token as string) || ''

  if (!email.value || !token.value) {
    appStore.showError(t('auth.invalidResetLink'))
  }
})

// validateForm checks the replacement password and its confirmation for the reset
// request. It clears both previous errors first, requires at least six characters,
// and rejects a confirmation that differs from the password.
function validateForm(): boolean {
  errors.password = ''
  errors.confirmPassword = ''

  let isValid = true

  if (!formData.password) {
    errors.password = t('auth.passwordRequired')
    isValid = false
  } else if (formData.password.length < 6) {
    errors.password = t('auth.passwordMinLength')
    isValid = false
  }

  if (!formData.confirmPassword) {
    errors.confirmPassword = t('auth.confirmPasswordRequired')
    isValid = false
  } else if (formData.password !== formData.confirmPassword) {
    errors.confirmPassword = t('auth.passwordsDoNotMatch')
    isValid = false
  }

  return isValid
}

// handleSubmit sends the replacement password with the route email and token after
// validation. It shows dedicated expired-token feedback when the endpoint
// identifies that failure, and otherwise reports detail or fallback text.
async function handleSubmit(): Promise<void> {
  errorMessage.value = ''

  if (!validateForm()) {
    return
  }

  isLoading.value = true

  try {
    await resetPassword({
      email: email.value,
      token: token.value,
      new_password: formData.password,
    })

    isSuccess.value = true
    appStore.showSuccess(t('auth.passwordResetSuccess'))
  } catch (error: unknown) {
    const err = error as {
      message?: string // reported failure message

      response?: {
        data?: {
          detail?: string // server detailed failure message
          code?: string // server failure code
        } // HTTP response payload
      } // optional HTTP error response
    }

    if (err.response?.data?.code === 'INVALID_RESET_TOKEN') {
      errorMessage.value = t('auth.invalidOrExpiredToken')
    } else if (err.response?.data?.detail) {
      errorMessage.value = err.response.data.detail
    } else if (err.message) {
      errorMessage.value = err.message
    } else {
      errorMessage.value = t('auth.resetPasswordFailed')
    }

    appStore.showError(errorMessage.value)
  } finally {
    isLoading.value = false
  }
}
</script>

<style scoped></style>
