<template>
  <BaseDialog
    :show="show"
    :title="t('team.inviteActionTitle')"
    width="narrow"
    @close="emit('close')"
  >
    <div v-if="error && !loading" class="py-4" data-testid="invitation-error">
      <div
        class="flex items-start gap-3 rounded-control bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300"
      >
        <Icon name="exclamationTriangle" size="md" class="mt-0.5 shrink-0" />
        <p class="min-w-0 text-sm leading-6">{{ error }}</p>
      </div>
    </div>

    <div
      v-else-if="loading || preview"
      :data-testid="loading ? 'invitation-loading' : 'invitation-details'"
      :aria-busy="loading"
    >
      <span v-if="loading" class="sr-only" role="status">{{ t('common.loading') }}</span>
      <div class="pb-5 text-center">
        <div
          class="mx-auto flex h-12 w-12 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
        >
          <Icon name="users" size="lg" />
        </div>
        <p class="mt-3 text-sm leading-6 text-gray-500 dark:text-gray-400">
          {{ t('team.inviteActionDescription') }}
        </p>
      </div>

      <!-- A description list keeps invitation source labels with their values while allowing natural wrapping on narrow screens. -->
      <dl
        class="divide-y divide-gray-100 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700"
      >
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.invitedTeam') }}</dt>
          <dd
            class="min-w-0 break-words text-sm font-medium text-gray-900 sm:text-right dark:text-white"
          >
            <Skeleton
              v-if="loading"
              :width="118"
              :height="14"
              class="my-[3px] max-w-full sm:ml-auto"
            />
            <template v-else>{{ preview?.team_name }}</template>
          </dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.inviter') }}</dt>
          <dd
            class="min-w-0 break-words text-sm font-medium text-gray-900 sm:text-right dark:text-white"
          >
            <Skeleton
              v-if="loading"
              :width="112"
              :height="14"
              class="my-[3px] max-w-full sm:ml-auto"
            />
            <template v-else>{{ preview?.inviter_name }}</template>
          </dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">{{ t('team.inviterEmail') }}</dt>
          <dd
            class="min-w-0 break-all text-sm font-medium text-gray-900 sm:text-right dark:text-white"
          >
            <Skeleton
              v-if="loading"
              :width="158"
              :height="14"
              class="my-[3px] max-w-full sm:ml-auto"
            />
            <template v-else>{{ preview?.inviter_email }}</template>
          </dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[7rem_minmax(0,1fr)] sm:gap-4">
          <dt class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('team.invitationExpiresAt') }}
          </dt>
          <dd
            class="min-w-0 break-words text-sm font-medium text-gray-900 sm:text-right dark:text-white"
          >
            <Skeleton
              v-if="loading"
              :width="145"
              :height="14"
              class="my-[3px] max-w-full sm:ml-auto"
            />
            <template v-else>{{ preview ? formatDateTime(preview.expires_at) : '' }}</template>
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
            <Icon name="check" size="sm" :animate-on-hover="false" />
            {{ t('team.accept') }}
          </button>
        </template>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import { useI18n } from 'vue-i18n'
import type { TeamInvitationPreview } from '@/api/team'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'

// TeamInvitationDialog.vue presents a team invitation preview and its acceptance or decline
// actions. Loading, failure, and resolving state come from the parent, which receives the
// chosen resolution and performs the server update before replacing the displayed state.

// Props carries invitation loading, resolution progress, and the server preview shown by the
// dialog.
interface Props {
  show: boolean // Invitation dialog visibility.
  loading: boolean // Preview request in progress.
  resolving: boolean // Resolution request in progress.
  preview: TeamInvitationPreview | null // Loaded invitation preview record.
  error: string // Preview failure display text.
}

// Emits reports invitation acceptance or rejection to the page that owns persistence.
interface Emits {
  (event: 'close'): void // Parent dismissal request.
  (event: 'resolve', resolution: 'accepted' | 'declined'): void // Requested invitation resolution.
}

// Props, resolution events, and translations keep the invitation shell presentational while
// its page owns fetching and mutation requests.
defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()
</script>
