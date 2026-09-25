<template>
  <BaseDialog
    :show="show"
    :title="t('team.inviteActionTitle')"
    width="narrow"
    @close="emit('close')"
  >
    <div v-if="loading" class="flex min-h-48 items-center justify-center" data-testid="invitation-loading">
      <LoadingSpinner />
    </div>

    <div v-else-if="error" class="py-4" data-testid="invitation-error">
      <div class="flex items-start gap-3 rounded-control bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300">
        <Icon name="exclamationTriangle" size="md" class="mt-0.5 shrink-0" />
        <p class="min-w-0 text-sm leading-6">{{ error }}</p>
      </div>
    </div>

    <div v-else-if="preview" data-testid="invitation-details">
      <div class="pb-5 text-center">
        <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400">
          <Icon name="users" size="lg" />
        </div>
        <p class="mt-3 text-sm leading-6 text-gray-500 dark:text-gray-400">
          {{ t('team.inviteActionDescription') }}
        </p>
      </div>

      <!-- 使用定义列表呈现邀请来源，移动端标签和值会自然换行。 -->
      <dl class="divide-y divide-gray-100 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700">
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.invitedTeam') }}</dt>
          <dd class="min-w-0 break-words text-sm font-medium text-gray-900 sm:text-right dark:text-white">
            {{ preview.team_name }}
          </dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.inviter') }}</dt>
          <dd class="min-w-0 break-words text-sm font-medium text-gray-900 sm:text-right dark:text-white">
            {{ preview.inviter_name }}
          </dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.inviterEmail') }}</dt>
          <dd class="min-w-0 break-all text-sm font-medium text-gray-900 sm:text-right dark:text-white">
            {{ preview.inviter_email }}
          </dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.invitationExpiresAt') }}</dt>
          <dd class="min-w-0 break-words text-sm font-medium text-gray-900 sm:text-right dark:text-white">
            {{ formatDateTime(preview.expires_at) }}
          </dd>
        </div>
      </dl>
    </div>

    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-3">
        <button v-if="error" type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.close') }}
        </button>
        <template v-else>
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="loading || resolving || !preview"
            @click="emit('resolve', 'declined')"
          >
            <Icon name="x" size="sm" />
            {{ t('team.decline') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="loading || resolving || !preview"
            @click="emit('resolve', 'accepted')"
          >
            <Icon name="check" size="sm" />
            {{ t('team.accept') }}
          </button>
        </template>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TeamInvitationPreview } from '@/api/team'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'

interface Props {
  show: boolean
  loading: boolean
  resolving: boolean
  preview: TeamInvitationPreview | null
  error: string
}

interface Emits {
  (event: 'close'): void
  (event: 'resolve', resolution: 'accepted' | 'declined'): void
}

defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()
</script>
