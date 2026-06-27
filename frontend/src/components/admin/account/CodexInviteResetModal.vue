<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.inviteResetTitle')"
    width="wide"
    @close="handleClose"
  >
    <div v-if="account" class="space-y-5">
      <div class="flex flex-col gap-3 rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700 sm:flex-row sm:items-center sm:justify-between">
        <div class="flex items-center gap-3">
          <div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-emerald-500 text-white">
            <Icon name="gift" size="md" :stroke-width="2" />
          </div>
          <div class="min-w-0">
            <div class="truncate font-semibold text-gray-900 dark:text-white">{{ account.name }}</div>
            <div class="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.inviteResetSubtitle') }}
            </div>
          </div>
        </div>
        <button type="button" class="btn btn-secondary shrink-0" :disabled="loading" @click="loadStatus()">
          <Icon name="refresh" size="sm" :class="loading && 'animate-spin'" />
          {{ t('admin.accounts.inviteResetRefresh') }}
        </button>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
          <section class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <div class="flex items-center justify-between gap-3">
              <div>
                <div class="text-sm font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.inviteResetAvailable') }}
                </div>
                <div class="mt-2 flex items-end gap-2">
                  <span class="text-4xl font-bold text-gray-900 dark:text-white">{{ availableCount }}</span>
                  <span class="pb-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.inviteResetAvailableUnit') }}</span>
                </div>
              </div>
              <div class="flex h-12 w-12 items-center justify-center rounded-lg bg-cyan-100 text-cyan-700 dark:bg-cyan-500/15 dark:text-cyan-300">
                <Icon name="refresh" size="md" :stroke-width="2" />
              </div>
            </div>

            <div class="grid grid-cols-1 gap-2 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">
              <div class="flex items-center justify-between gap-3">
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.inviteResetRewardType') }}</span>
                <span class="text-right font-medium text-gray-900 dark:text-white">{{ rewardTypeLabel }}</span>
              </div>
              <div class="flex items-center justify-between gap-3">
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.inviteResetRewardAmount') }}</span>
                <span class="text-right font-medium text-gray-900 dark:text-white">{{ rewardAmountLabel }}</span>
              </div>
              <div class="flex items-center justify-between gap-3">
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.inviteResetShouldShow') }}</span>
                <span :class="['rounded-full px-2 py-0.5 text-xs font-medium', shouldShowClass]">{{ shouldShowLabel }}</span>
              </div>
              <div v-if="showGrantAction" class="flex items-center justify-between gap-3">
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.inviteResetGrantAction') }}</span>
                <span class="max-w-[12rem] truncate text-right font-mono text-xs text-gray-700 dark:text-gray-200">{{ status?.grant_action }}</span>
              </div>
            </div>

            <div v-if="availableCredits.length > 0" class="space-y-3">
              <label class="input-label">{{ t('admin.accounts.inviteResetSelectedCredit') }}</label>
              <Select
                v-model="selectedCreditId"
                :options="creditOptions"
                :placeholder="t('admin.accounts.inviteResetSelectCredit')"
                :searchable="false"
              />
              <div v-if="selectedCredit" class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                <div class="font-medium text-gray-900 dark:text-white">{{ creditTitle(selectedCredit) }}</div>
                <div class="mt-1">{{ creditDescription(selectedCredit) }}</div>
              </div>
            </div>

            <div v-else class="rounded-lg border border-dashed border-gray-300 p-4 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
              {{ t('admin.accounts.inviteResetNoCredits') }}
            </div>

            <button
              type="button"
              class="btn btn-primary w-full"
              :disabled="!canConsume"
              @click="handleConsume"
            >
              <Icon name="refresh" size="sm" :class="consuming && 'animate-spin'" />
              {{ consuming ? t('admin.accounts.inviteResetUsing') : t('admin.accounts.inviteResetUseReset') }}
            </button>

            <button
              type="button"
              class="flex w-full items-center justify-between rounded-lg border border-gray-200 px-3 py-2 text-left text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-200 dark:hover:bg-dark-700"
              @click="showRules = !showRules"
            >
              <span>{{ t('admin.accounts.inviteResetRules') }}</span>
              <Icon name="chevronDown" size="sm" :class="['transition-transform', showRules && 'rotate-180']" />
            </button>
            <div v-if="showRules" class="rounded-lg bg-gray-50 p-3 text-sm text-gray-600 dark:bg-dark-800 dark:text-gray-300">
              <ul v-if="rules.length > 0" class="list-disc space-y-1 pl-5">
                <li v-for="rule in rules" :key="rule">{{ rule }}</li>
              </ul>
              <p v-else>{{ t('admin.accounts.inviteResetRulesEmpty') }}</p>
            </div>
          </section>

          <section class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <div>
              <label class="input-label" for="codex-invite-reset-emails">
                {{ t('admin.accounts.inviteResetInviteEmails') }}
              </label>
              <textarea
                id="codex-invite-reset-emails"
                v-model="emailInput"
                rows="8"
                class="input mt-2 min-h-[180px] resize-y font-mono text-sm leading-6"
                :placeholder="t('admin.accounts.inviteResetPlaceholder')"
              ></textarea>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.inviteResetEmailHint', { max: maxEmails }) }}
              </p>
            </div>

            <label class="flex items-start gap-2 rounded-lg border border-gray-200 p-3 text-sm text-gray-700 dark:border-dark-600 dark:text-gray-300">
              <input
                v-model="consentConfirmed"
                type="checkbox"
                class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              />
              <span>{{ t('admin.accounts.inviteResetConsent') }}</span>
            </label>

            <div v-if="inviteUnavailableMessage" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-500/10 dark:text-amber-300">
              {{ inviteUnavailableMessage }}
            </div>

            <div v-if="message" :class="['rounded-lg p-3 text-sm', messageClass]">
              {{ message }}
            </div>

            <button
              type="button"
              class="btn btn-secondary w-full justify-center"
              :disabled="!canSendInvite"
              @click="handleSendInvite"
            >
              <Icon name="mail" size="sm" :class="sendingInvite && 'animate-pulse'" />
              {{ sendingInvite ? t('admin.accounts.inviteResetSending') : t('admin.accounts.inviteResetSendInvite') }}
            </button>
          </section>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { Account } from '@/types'
import type { CodexInviteResetCredit, CodexInviteResetStatus } from '@/api/admin/accounts'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'

const maxEmails = 5
const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  close: []
  updated: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const sendingInvite = ref(false)
const consuming = ref(false)
const status = ref<CodexInviteResetStatus | null>(null)
const selectedCreditId = ref<string | null>(null)
const emailInput = ref('')
const consentConfirmed = ref(false)
const message = ref('')
const messageType = ref<'success' | 'error' | ''>('')
const showRules = ref(false)

const availableCredits = computed(() => {
  return (status.value?.credits ?? []).filter((credit) => {
    const state = credit.status?.toLowerCase()
    return !state || state === 'available'
  })
})

const availableCount = computed(() => status.value?.available_count ?? availableCredits.value.length)

const rules = computed(() => status.value?.eligibility_rules ?? [])

// 管理端始终展示上游资格信息，should_show 只作为 Codex Desktop 主动展示建议。
const rewardTypeLabel = computed(() => {
  if (status.value?.grant_type === 'rate_limit_reset') {
    return t('admin.accounts.inviteResetGrantTypeRateLimitReset')
  }
  if (status.value?.grant_type === 'workspace_credits') {
    return t('admin.accounts.inviteResetGrantTypeWorkspaceCredits')
  }
  return t('admin.accounts.inviteResetGrantTypeUnknown')
})

const rewardAmountLabel = computed(() => {
  const amount = status.value?.grant_amount
  return typeof amount === 'number' ? String(amount) : t('admin.accounts.inviteResetRewardAmountUnknown')
})

const shouldShowLabel = computed(() => {
  if (status.value?.should_show === true) return t('admin.accounts.inviteResetShouldShowTrue')
  if (status.value?.should_show === false) return t('admin.accounts.inviteResetShouldShowFalse')
  return t('admin.accounts.inviteResetShouldShowUnknown')
})

const shouldShowClass = computed(() => {
  if (status.value?.should_show === true) {
    return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
  }
  if (status.value?.should_show === false) {
    return 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'
  }
  return 'bg-gray-200 text-gray-600 dark:bg-dark-600 dark:text-gray-300'
})

const showGrantAction = computed(() => Boolean(status.value?.grant_action) && status.value?.grant_type === 'unknown')

const creditOptions = computed<SelectOption[]>(() => {
  return availableCredits.value.map((credit, index) => ({
    value: credit.id,
    label: `${creditTitle(credit)} #${index + 1}`
  }))
})

const selectedCredit = computed(() => {
  return availableCredits.value.find((credit) => credit.id === selectedCreditId.value) ?? null
})

const canConsume = computed(() => {
  return Boolean(selectedCreditId.value) && !loading.value && !consuming.value && availableCredits.value.length > 0
})

const inviteUnavailableMessage = computed(() => {
  if (!status.value || status.value.invite_available !== false) return ''
  return status.value.invite_unavailable_message || t('admin.accounts.inviteResetInviteUnavailable')
})

const canSendInvite = computed(() => {
  return Boolean(status.value) && !loading.value && !sendingInvite.value && status.value?.invite_available !== false
})

const messageClass = computed(() => {
  if (messageType.value === 'success') {
    return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300'
  }
  if (messageType.value === 'error') {
    return 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300'
  }
  return 'bg-gray-50 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
})

const creditTitle = (credit: CodexInviteResetCredit) => {
  return credit.title || t('admin.accounts.inviteResetCreditFallbackTitle')
}

const creditDescription = (credit: CodexInviteResetCredit) => {
  return credit.description || t('admin.accounts.inviteResetCreditFallbackDescription')
}

// 邀请接口支持逗号、分号、空白和换行分隔，这里先在前端做同样的校验。
const parseEmails = () => {
  const emails = emailInput.value
    .split(/[,\s;]+/)
    .map((item) => item.trim())
    .filter(Boolean)
  const unique = [...new Map(emails.map((email) => [email.toLowerCase(), email])).values()]
  if (unique.length === 0) {
    throw new Error(t('admin.accounts.inviteResetEmailsRequired'))
  }
  if (unique.length > maxEmails) {
    throw new Error(t('admin.accounts.inviteResetEmailLimit', { max: maxEmails }))
  }
  const invalid = unique.find((email) => !emailPattern.test(email))
  if (invalid) {
    throw new Error(t('admin.accounts.inviteResetInvalidEmail', { email: invalid }))
  }
  return unique
}

const setMessage = (type: 'success' | 'error', text: string) => {
  messageType.value = type
  message.value = text
}

const selectDefaultCredit = () => {
  if (!availableCredits.value.length) {
    selectedCreditId.value = null
    return
  }
  if (!availableCredits.value.some((credit) => credit.id === selectedCreditId.value)) {
    selectedCreditId.value = availableCredits.value[0].id
  }
}

const loadStatus = async (clearMessage = true) => {
  if (!props.account) return
  loading.value = true
  if (clearMessage) {
    message.value = ''
    messageType.value = ''
  }
  try {
    status.value = await adminAPI.accounts.getCodexInviteResetStatus(props.account.id)
    selectDefaultCredit()
  } catch (error: any) {
    status.value = null
    setMessage('error', error?.message || t('admin.accounts.inviteResetLoadFailed'))
    appStore.showError(error?.message || t('admin.accounts.inviteResetLoadFailed'))
  } finally {
    loading.value = false
  }
}

const handleSendInvite = async () => {
  if (!props.account || sendingInvite.value) return
  try {
    if (!status.value) {
      throw new Error(t('admin.accounts.inviteResetLoadFailed'))
    }
    if (inviteUnavailableMessage.value) {
      setMessage('error', inviteUnavailableMessage.value)
      return
    }
    const emails = parseEmails()
    if ((status.value?.requires_consent ?? true) && !consentConfirmed.value) {
      throw new Error(t('admin.accounts.inviteResetConsentRequired'))
    }
    sendingInvite.value = true
    const result = await adminAPI.accounts.sendCodexInviteResetInvite(props.account.id, emails)
    const failed = result.failed_emails?.filter(Boolean) ?? []
    if (failed.length > 0) {
      setMessage('error', t('admin.accounts.inviteResetInvitePartialFailed', { emails: failed.join(', ') }))
      return
    }
    emailInput.value = ''
    setMessage('success', result.message || t('admin.accounts.inviteResetInviteSuccess'))
    appStore.showSuccess(t('admin.accounts.inviteResetInviteSuccess'))
  } catch (error: any) {
    setMessage('error', error?.message || t('admin.accounts.inviteResetInviteFailed'))
  } finally {
    sendingInvite.value = false
  }
}

const consumeSuccessMessage = (code?: string) => {
  if (code === 'nothing_to_reset') return t('admin.accounts.inviteResetNothingToReset')
  if (code === 'already_redeemed') return t('admin.accounts.inviteResetAlreadyRedeemed')
  if (code === 'no_credit') return t('admin.accounts.inviteResetNoCredit')
  return t('admin.accounts.inviteResetConsumeSuccess')
}

const handleConsume = async () => {
  if (!props.account || !selectedCreditId.value || consuming.value) return
  consuming.value = true
  try {
    const result = await adminAPI.accounts.consumeCodexInviteReset(props.account.id, selectedCreditId.value)
    const text = consumeSuccessMessage(result.code)
    setMessage(result.code === 'reset' || !result.code ? 'success' : 'error', text)
    if (result.code === 'reset' || !result.code) {
      appStore.showSuccess(text)
      emit('updated')
    }
    await loadStatus(false)
  } catch (error: any) {
    setMessage('error', error?.message || t('admin.accounts.inviteResetConsumeFailed'))
  } finally {
    consuming.value = false
  }
}

const resetLocalState = () => {
  status.value = null
  selectedCreditId.value = null
  emailInput.value = ''
  consentConfirmed.value = false
  message.value = ''
  messageType.value = ''
  showRules.value = false
}

const handleClose = () => {
  emit('close')
}

watch(
  () => [props.show, props.account?.id],
  ([visible]) => {
    if (visible && props.account) {
      loadStatus()
      return
    }
    resetLocalState()
  }
)

watch(availableCredits, selectDefaultCredit)
</script>
