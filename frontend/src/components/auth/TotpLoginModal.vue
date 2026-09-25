<template>
  <div class="fixed inset-0 z-50 overflow-y-auto">
    <div class="flex min-h-full items-center justify-center p-4">
      <div class="fixed inset-0 bg-black/50 transition-opacity"></div>

      <div class="relative w-full max-w-md transform rounded-surface bg-white p-6 shadow-xl transition-all dark:bg-dark-800 sm:rounded-dialog">
        <!-- Header -->
        <div class="mb-6 text-center">
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
            <svg class="h-6 w-6 text-primary-600 dark:text-primary-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z" />
            </svg>
          </div>
          <h3 class="mt-4 text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('profile.totp.loginTitle') }}
          </h3>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('profile.totp.loginHint') }}
          </p>
          <p v-if="userEmailMasked" class="mt-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ userEmailMasked }}
          </p>
        </div>

        <!-- 验证码输入 -->
        <div class="mb-6">
          <!-- 隐藏输入框用于兼容系统一次性验证码填充，1Password 使用首个可见输入框。 -->
          <input
            ref="hiddenOtpInputRef"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            class="pointer-events-none absolute left-0 top-0 h-px w-px opacity-0"
            aria-hidden="true"
            tabindex="-1"
            data-1p-ignore
            @input="handleHiddenOtpInput"
            @change="handleHiddenOtpInput"
          />
          <div class="flex justify-center gap-2">
            <input
              v-for="(_, index) in 6"
              :key="index"
              :ref="(el) => setInputRef(el, index)"
              data-testid="totp-digit-input"
              type="text"
              :maxlength="index === 0 ? 6 : 1"
              inputmode="numeric"
              :pattern="index === 0 ? '[0-9]{1,6}' : '[0-9]'"
              :autocomplete="index === 0 ? 'one-time-code' : 'off'"
              :name="index === 0 ? 'totp_login_code' : undefined"
              class="h-12 w-10 rounded-control border border-gray-300 text-center text-lg font-semibold focus:border-primary-500 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-700"
              :disabled="verifying"
              @input="handleCodeInput($event, index)"
              @change="handleCodeInput($event, index)"
              @keydown="handleKeydown($event, index)"
              @paste="handlePaste"
            />
          </div>
          <!-- 验证中提示 -->
          <div v-if="verifying" class="mt-3 flex items-center justify-center gap-2 text-sm text-gray-500">
            <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-primary-500"></div>
            {{ t('common.verifying') }}
          </div>
        </div>

        <!-- 取消按钮 -->
        <button
          type="button"
          class="btn btn-secondary w-full"
          :disabled="verifying"
          @click="$emit('cancel')"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'

defineProps<{
  tempToken: string
  userEmailMasked?: string
}>()

const emit = defineEmits<{
  verify: [code: string]
  cancel: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const verifying = ref(false)
const code = ref<string[]>(['', '', '', '', '', ''])
const inputRefs = ref<(HTMLInputElement | null)[]>([])
const hiddenOtpInputRef = ref<HTMLInputElement | null>(null)
const lastSubmittedCode = ref('')

// 监听验证码变化，输入 6 位后自动提交。
watch(
  () => code.value.join(''),
  (newCode) => {
    if (!/^[0-9]{6}$/.test(newCode)) {
      lastSubmittedCode.value = ''
      return
    }
    if (!verifying.value && newCode !== lastSubmittedCode.value) {
      lastSubmittedCode.value = newCode
      emit('verify', newCode)
    }
  }
)

defineExpose({
  setVerifying: (value: boolean) => { verifying.value = value },
  setError: (message: string) => {
    if (message) {
      appStore.showError(message)
    }
    code.value = ['', '', '', '', '', '']
    lastSubmittedCode.value = ''
    // 清空可见输入框的 DOM 值。
    inputRefs.value.forEach(input => {
      if (input) input.value = ''
    })
    // 清空隐藏的自动填充输入框，避免下一次打开时残留旧验证码。
    if (hiddenOtpInputRef.value) {
      hiddenOtpInputRef.value.value = ''
    }
    nextTick(() => {
      inputRefs.value[0]?.focus()
    })
  }
})

const setInputRef = (el: any, index: number) => {
  inputRefs.value[index] = el as HTMLInputElement | null
}

const handleCodeInput = (event: Event, index: number) => {
  const input = event.target as HTMLInputElement
  const digits = input.value.replace(/[^0-9]/g, '').slice(0, 6)

  // 密码管理器可能把完整验证码一次写入当前分格，需要在这里拆回各格。
  if (digits.length > 1) {
    fillCodeDigits(digits.split(''))
    const focusIndex = Math.min(digits.length, 5)
    nextTick(() => inputRefs.value[focusIndex]?.focus())
    return
  }

  const value = digits[0] || ''
  code.value[index] = value
  input.value = value

  if (value && index < 5) {
    nextTick(() => {
      inputRefs.value[index + 1]?.focus()
    })
  }
}

const fillCodeDigits = (digits: string[]) => {
  const nextCode = ['', '', '', '', '', '']
  const availableDigits = digits.slice(0, 6)

  availableDigits.forEach((digit, offset) => {
    nextCode[offset] = digit
  })
  for (let index = availableDigits.length; index < 6; index++) {
    nextCode[index] = ''
  }

  code.value = nextCode
  inputRefs.value.forEach((visibleInput, index) => {
    if (visibleInput) visibleInput.value = nextCode[index]
  })
}

const handleHiddenOtpInput = (event: Event) => {
  const input = event.target as HTMLInputElement
  const digits = input.value.replace(/[^0-9]/g, '').slice(0, 6).split('')

  fillCodeDigits(digits)
}

const handleKeydown = (event: KeyboardEvent, index: number) => {
  if (event.key === 'Backspace') {
    const input = event.target as HTMLInputElement
    // 当前格为空时退格回到上一格。
    if (!input.value && index > 0) {
      event.preventDefault()
      inputRefs.value[index - 1]?.focus()
    }
    // 其他情况交给浏览器处理，input 事件会同步 code.value。
  }
}

const handlePaste = (event: ClipboardEvent) => {
  event.preventDefault()
  const pastedData = event.clipboardData?.getData('text') || ''
  const digits = pastedData.replace(/[^0-9]/g, '').slice(0, 6).split('')

  // 同步响应式验证码和可见输入框。
  fillCodeDigits(digits)

  const focusIndex = Math.min(digits.length, 5)
  nextTick(() => {
    inputRefs.value[focusIndex]?.focus()
  })
}

onMounted(() => {
  nextTick(() => {
    inputRefs.value[0]?.focus()
  })
})
</script>
