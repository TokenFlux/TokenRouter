<template>
  <div class="card overflow-hidden">
    <div
      class="border-b border-gray-100 bg-gradient-to-r from-emerald-500/10 to-primary-500/5 px-6 py-4 dark:border-dark-700 dark:from-emerald-500/15 dark:to-primary-500/10"
    >
      <div class="flex items-center gap-3">
        <div
          class="flex h-11 w-11 items-center justify-center rounded-2xl bg-emerald-500/15 text-emerald-600 dark:bg-emerald-500/20 dark:text-emerald-300"
        >
          <Icon name="link" size="lg" />
        </div>
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('profile.referral.title') }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('profile.referral.description') }}
          </p>
        </div>
      </div>
    </div>

    <div class="space-y-6 px-6 py-6">
      <div v-if="loading" class="flex items-center justify-center py-6">
        <div class="h-6 w-6 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <template v-else-if="referralInfo">
        <div class="rounded-2xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('profile.referral.inviteLink') }}
              </p>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('profile.referral.inviteLinkHint') }}
              </p>
            </div>
            <span
              class="rounded-full bg-emerald-100 px-3 py-1 text-xs font-semibold text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300"
            >
              {{ t('profile.referral.codeLabel') }} {{ referralInfo.referral_code }}
            </span>
          </div>

          <div class="mt-4 flex flex-col gap-3 lg:flex-row">
            <code
              class="flex-1 break-all rounded-xl border border-gray-200 bg-white px-4 py-3 text-sm text-gray-700 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-200"
            >
              {{ inviteLink }}
            </code>
            <button type="button" class="btn btn-primary" @click="copyInviteLink">
              <Icon :name="copied ? 'checkCircle' : 'copy'" size="sm" class="mr-2" />
              {{ copied ? t('profile.referral.copied') : t('profile.referral.copy') }}
            </button>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div class="rounded-2xl border border-gray-200 p-4 dark:border-dark-600">
            <div class="flex items-center gap-3">
              <div
                class="flex h-10 w-10 items-center justify-center rounded-xl bg-blue-100 text-blue-600 dark:bg-blue-500/15 dark:text-blue-300"
              >
                <Icon name="users" size="md" />
              </div>
              <div>
                <p class="text-sm text-gray-500 dark:text-gray-400">
                  {{ t('profile.referral.totalInvited') }}
                </p>
                <p class="text-2xl font-semibold text-gray-900 dark:text-white">
                  {{ referralInfo.invited_count }}
                </p>
              </div>
            </div>
          </div>

          <div class="rounded-2xl border border-gray-200 p-4 dark:border-dark-600">
            <div class="flex items-center gap-3">
              <div
                class="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-100 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-300"
              >
                <Icon name="dollar" size="md" />
              </div>
              <div>
                <p class="text-sm text-gray-500 dark:text-gray-400">
                  {{ t('profile.referral.totalReward') }}
                </p>
                <p class="text-2xl font-semibold text-gray-900 dark:text-white">
                  ${{ referralInfo.reward_total.toFixed(2) }}
                </p>
              </div>
            </div>
          </div>
        </div>
      </template>

      <div
        v-else
        class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/40 dark:bg-red-900/15 dark:text-red-300"
      >
        {{ errorMessage || t('profile.referral.loadFailed') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { userAPI } from '@/api'
import { useAppStore } from '@/stores'
import Icon from '@/components/icons/Icon.vue'
import type { UserReferralInfo } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const copied = ref(false)
const errorMessage = ref('')
const referralInfo = ref<UserReferralInfo | null>(null)

const inviteLink = computed(() => {
  if (!referralInfo.value?.referral_code) {
    return ''
  }

  const url = new URL('/register', window.location.origin)
  url.searchParams.set('ref', referralInfo.value.referral_code)
  return url.toString()
})

async function loadReferralInfo(): Promise<void> {
  loading.value = true
  errorMessage.value = ''

  try {
    referralInfo.value = await userAPI.getReferralInfo()
  } catch (error) {
    console.error('Failed to load referral info:', error)
    referralInfo.value = null
    errorMessage.value = t('profile.referral.loadFailed')
  } finally {
    loading.value = false
  }
}

async function copyInviteLink(): Promise<void> {
  if (!inviteLink.value) {
    return
  }

  try {
    await navigator.clipboard.writeText(inviteLink.value)
    copied.value = true
    appStore.showSuccess(t('profile.referral.copySuccess'))
    window.setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (error) {
    console.error('Failed to copy referral link:', error)
    appStore.showError(t('profile.referral.copyFailed'))
  }
}

onMounted(() => {
  loadReferralInfo()
})
</script>
