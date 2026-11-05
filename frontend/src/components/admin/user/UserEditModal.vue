<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.editUser')"
    width="normal"
    @close="$emit('close')"
  >
    <form v-if="user" id="edit-user-form" @submit.prevent="handleUpdateUser" class="space-y-5">
      <div>
        <label class="input-label">{{ t('admin.users.email') }}</label>
        <input v-model="form.email" type="email" class="input" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.password') }}</label>
        <div class="flex gap-2">
          <div class="relative flex-1">
            <input
              v-model="form.password"
              type="text"
              class="input pr-10"
              :placeholder="t('admin.users.enterNewPassword')"
            />
            <button
              v-if="form.password"
              type="button"
              @click="copyPassword"
              class="absolute right-2 top-1/2 -translate-y-1/2 rounded-control p-1 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
              :class="passwordCopied ? 'text-green-500' : 'text-gray-400'"
            >
              <Icon
                name="check"
                size="sm"
                :animate-on-hover="false"
                v-if="passwordCopied"
                class="h-4 w-4"
              />
              <Icon name="clipboard" size="sm" v-else class="h-4 w-4" />
            </button>
          </div>
          <button type="button" @click="generatePassword" class="btn btn-secondary px-3">
            <Icon name="refresh" size="sm" />
          </button>
        </div>
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.username') }}</label>
        <input v-model="form.username" type="text" class="input" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.form.roleLabel') }}</label>
        <Select v-model="form.role" :options="roleOptions" />
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.notes') }}</label>
        <textarea v-model="form.notes" rows="3" class="input"></textarea>
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.columns.concurrency') }}</label>
        <input
          v-model.number="form.concurrency"
          type="number"
          min="0"
          step="1"
          class="input"
          :placeholder="t('admin.users.form.concurrencyPlaceholder')"
          data-test="concurrency-input"
        />
        <p class="input-hint">{{ t('admin.users.form.concurrencyHint') }}</p>
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.form.rpmLimit') }}</label>
        <input
          v-model.number="form.rpm_limit"
          type="number"
          min="0"
          :max="MAX_USER_API_KEY_LIMIT"
          step="1"
          class="input"
          :placeholder="t('admin.users.form.rpmLimitPlaceholder')"
        />
        <p class="input-hint">{{ t('admin.users.form.rpmLimitHint') }}</p>
      </div>
      <div>
        <label class="input-label">{{ t('admin.users.form.apiKeyLimit') }}</label>
        <input
          v-model.number="form.api_key_limit"
          type="number"
          min="0"
          step="1"
          class="input"
          data-test="api-key-limit-input"
          :placeholder="t('admin.users.form.apiKeyLimitEditPlaceholder')"
        />
        <p class="input-hint">{{ t('admin.users.form.apiKeyLimitEditHint') }}</p>
      </div>
      <div>
        <label for="user-recharge-multiplier" class="input-label">{{
          t('admin.users.form.balanceRechargeMultiplier')
        }}</label>
        <input
          id="user-recharge-multiplier"
          v-model="form.balance_recharge_multiplier"
          type="number"
          step="any"
          class="input"
          data-test="recharge-multiplier-input"
          aria-describedby="user-recharge-multiplier-hint"
          :placeholder="t('admin.users.form.balanceRechargeMultiplierHint')"
        />
        <p id="user-recharge-multiplier-hint" class="input-hint">{{
          t('admin.users.form.balanceRechargeMultiplierHint')
        }}</p>
      </div>
      <UserAttributeForm v-model="form.customAttributes" :user-id="user?.id" />
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button @click="$emit('close')" type="button" class="btn btn-secondary">
          {{ t('common.cancel') }}
        </button>
        <button type="submit" form="edit-user-form" :disabled="submitting" class="btn btn-primary">
          {{ submitting ? t('admin.users.updating') : t('common.update') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <!-- The step-up dialog supplies TOTP verification when role promotion requires it before retrying the request. -->
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { COPY_FEEDBACK_MS } from '@/constants/ui'
import { adminAPI } from '@/api/admin'
import type { AdminUser, UpdateUserRequest } from '@/api/admin/users.types'
import type { UserAttributeValuesMap } from '@/api/admin/userAttributes.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import UserAttributeForm from '@/components/user/UserAttributeForm.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  useStepUp,
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
} from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { MAX_USER_API_KEY_LIMIT } from '@/constants/user'

// UserEditModal.vue edits user identity, limits, recharge terms, password, and custom attributes. The
// selected user seeds the draft, while a blank password means no password update and an
// unchanged role is omitted from the user request. Role changes can require step-up
// verification before persistence.
//
// Saving first updates the user and then writes custom attributes when the draft
// contains any entries. These requests are separate: an attribute failure can follow a
// successful profile update and leaves the dialog open with an error. The captured user
// ID keeps both requests directed at the same user throughout this ordered save
// sequence.

// props and emit connect the selected user and modal visibility with parent refresh and
// close events after both save phases finish.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  // user supplies the selected account; null leaves identity-dependent operations waiting
  // until the parent chooses an account.
  user: AdminUser | null
}>()
const emit = defineEmits(['close', 'success'])
// The translation, notification, and clipboard services supply edit labels, save
// feedback, and password-copy feedback for the administrator's user draft.
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

// roleOptions supplies translated user and administrator choices, with the actual role
// change submitted only when it differs from the selected user.
const roleOptions = computed(() => [
  {
    value: 'user', // Stored option value.
    label: t('admin.users.roles.user'), // Displayed option label.
  },
  {
    value: 'admin', // Stored option value.
    label: t('admin.users.roles.admin'), // Displayed option label.
  },
])

// submitting tracks the ordered user and attribute save; passwordCopied separately
// controls temporary clipboard feedback after a successful password copy.
const submitting = ref(false)
const passwordCopied = ref(false)
// form stores the selected user's editable identity and limits plus custom attribute
// changes, with an empty password representing no password replacement.
const form = reactive({
  email: '', // User email address.
  // password starts blank when selecting a user; only nonblank trimmed input is
  // included in the profile update.
  password: '',
  username: '', // User display name.
  notes: '', // Administrator account notes.
  role: 'user' as AdminUser['role'], // Draft account role.
  concurrency: 1, // Concurrent request limit.
  rpm_limit: 0, // Requests per minute.
  api_key_limit: 100, // API-key creation limit.
  // An empty draft clears the override; unchanged values are omitted from the update.
  balance_recharge_multiplier: '' as number | string,
  customAttributes: {} as UserAttributeValuesMap, // Edited custom attribute values.
})

// A selected-user change reseeds the editable fields and clears password feedback
// before edits can be submitted for that user's captured identity.
watch(
  () => props.user,
  (u) => {
    if (u) {
      Object.assign(form, {
        email: u.email, // User email address.
        password: '', // Draft account password.
        username: u.username || '', // User display name.
        notes: u.notes || '', // Administrator account notes.
        role: u.role || 'user', // Draft account role.
        concurrency: u.concurrency, // Concurrent request limit.
        rpm_limit: u.rpm_limit ?? 0, // Requests per minute.
        api_key_limit: u.api_key_limit ?? 100, // API-key creation limit.
        balance_recharge_multiplier: u.balance_recharge_multiplier ?? '', // Personal recharge rate.
        customAttributes: {}, // Edited custom attribute values.
      })
      passwordCopied.value = false
    }
  },
  {
    immediate: true, // Runs on watcher registration.
  },
)

// generatePassword fills the password draft with sixteen randomly selected allowed
// characters.
const generatePassword = () => {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%^&*'
  let p = ''
  // Generate sixteen characters from the configured password alphabet.
  for (let i = 0; i < 16; i++) p += chars.charAt(Math.floor(Math.random() * chars.length))
  form.password = p
}
// copyPassword copies a nonempty password and marks successful clipboard feedback
// temporarily. A delayed callback clears the feedback after COPY_FEEDBACK_MS, while
// unsuccessful copies leave the flag unchanged.
const copyPassword = async () => {
  if (form.password && (await copyToClipboard(form.password, t('admin.users.passwordCopied')))) {
    passwordCopied.value = true
    // Expire successful clipboard feedback after the shared display interval so the
    // copy control returns to its normal state for another copy.
    setTimeout(() => (passwordCopied.value = false), COPY_FEEDBACK_MS)
  }
}
// stepUp owns the sensitive-role verification flow used by handleUpdateUser before the
// profile request can proceed with elevated verification.
const stepUp = useStepUp()

// handleUpdateUser validates email, concurrency, API-key limits, and optional recharge
// terms before capturing the selected user ID. Changed recharge terms send a positive
// number or null for clearing; unchanged terms are omitted. It sends changed role and
// nonblank password fields through step-up verification with the profile update.
//
// Custom attributes are written afterward when their draft map has entries, so failure
// in that second request can leave the profile already saved. Success and close events
// follow both phases. Cancelled verification keeps the form open quietly, blocked
// verification receives a specific message, and finally clears the pending flag.
const handleUpdateUser = async () => {
  if (!props.user) return
  if (!form.email.trim()) {
    appStore.showError(t('admin.users.emailRequired'))
    return
  }

  // Zero is an accepted unlimited concurrency setting, so validation rejects negative
  // or fractional values while allowing other profile fields to save.
  if (!Number.isInteger(form.concurrency) || form.concurrency < 0) {
    appStore.showError(t('admin.users.concurrencyNonNegative'))
    return
  }
  if (
    !Number.isSafeInteger(form.api_key_limit) ||
    form.api_key_limit < 0 ||
    form.api_key_limit > MAX_USER_API_KEY_LIMIT
  ) {
    appStore.showError(t('admin.users.form.apiKeyLimitInvalid'))
    return
  }
  const rechargeDraft = String(form.balance_recharge_multiplier).trim()
  const rechargeMultiplier = rechargeDraft === '' ? null : Number(rechargeDraft)
  if (
    rechargeMultiplier !== null &&
    (!Number.isFinite(rechargeMultiplier) || rechargeMultiplier <= 0)
  ) {
    appStore.showError(t('admin.users.form.balanceRechargeMultiplierInvalid'))
    return
  }
  const userId = props.user.id
  submitting.value = true
  try {
    const data: UpdateUserRequest = {
      email: form.email, // User email address.
      username: form.username, // User display name.
      notes: form.notes, // Administrator account notes.
      concurrency: form.concurrency, // Concurrent request limit.
      rpm_limit: form.rpm_limit, // Requests per minute.
      api_key_limit: form.api_key_limit, // API-key creation limit.
    }

    // Omit unchanged overrides so an unrelated edit does not restore a stale personal
    // rate. An emptied draft deliberately sends null to resume following global settings.
    if (rechargeMultiplier !== (props.user.balance_recharge_multiplier ?? null)) {
      data.balance_recharge_multiplier = rechargeMultiplier
    }

    // Include role only when it changes, since the backend treats role mutation as
    // sensitive and ordinary profile edits should not request that operation.
    if (form.role !== props.user.role) data.role = form.role
    if (form.password.trim()) data.password = form.password.trim()

    // Persist the profile through step-up verification before sending attributes; an
    // attribute failure can leave the profile saved because the requests are separate.
    await stepUp.run(() => adminAPI.users.update(userId, data))
    if (Object.keys(form.customAttributes).length > 0)
      await adminAPI.userAttributes.updateUserAttributeValues(userId, form.customAttributes)
    appStore.showSuccess(t('admin.users.userUpdated'))
    emit('success')
    emit('close')
  } catch (e: any) {
    if (isStepUpCancelled(e)) {
      // Cancelled verification keeps the selected user's draft open for editing, so the
      // component suppresses the ordinary update-failure notification for that outcome.
    } else if (isStepUpBlocked(e)) {
      appStore.showError(
        stepUpBlockReason(e) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled'),
      )
    } else {
      appStore.showError(e?.message || t('admin.users.failedToUpdate'))
    }
  } finally {
    submitting.value = false
  }
}
</script>
