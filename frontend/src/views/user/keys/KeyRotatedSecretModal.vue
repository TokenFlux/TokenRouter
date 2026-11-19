<template>
  <BaseDialog
    :show="rotatedKey !== null"
    :title="t('keys.keyRotatedSuccess')"
    width="narrow"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('keys.rotatedKeyHint') }}</p>
      <code
        class="block break-all rounded-surface bg-gray-50 p-4 text-sm text-gray-900 dark:bg-dark-800 dark:text-gray-100"
      >
        {{ rotatedKey?.key }}
      </code>
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.close') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          @click="rotatedKey && emit('copy', rotatedKey.key, rotatedKey.id)"
        >
          {{ t('common.copy') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { ApiKey } from '@/api/keys.types'

/**
 * KeyRotatedSecretModal.vue displays the newly issued secret after API-key
 * rotation so the user can copy it before dismissing the confirmation dialog.
 */

defineProps<{
  rotatedKey: ApiKey | null // Newly rotated API key record or null when hidden.
}>()

const emit = defineEmits<{
  close: [] // Dismisses the rotated-secret dialog.
  copy: [secret: string, id: number] // Copies the new secret through the parent's clipboard controller.
}>()

const { t } = useI18n()
</script>
