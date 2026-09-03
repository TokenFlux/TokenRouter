<template>
  <Teleport to="body">
    <div v-if="show && apiKey && position">
      <div class="fixed inset-0 z-[9998]" aria-hidden="true" @click="emit('close')"></div>
      <div
        :id="`key-action-menu-${apiKey.id}`"
        class="fixed z-[9999] w-48 overflow-hidden rounded-control bg-white shadow-lg ring-1 ring-black/5 dark:bg-dark-800 dark:ring-white/10"
        :style="{ top: `${position.top}px`, left: `${position.left}px` }"
        role="menu"
        :aria-label="t('common.actions')"
        @click.stop
      >
        <div class="py-1">
          <button type="button" class="menu-item" role="menuitem" @click="emitAction('use')">
            <Icon name="terminal" size="sm" class="text-gray-400" :stroke-width="2" />
            {{ t('keys.useKey') }}
          </button>
          <button type="button" class="menu-item" role="menuitem" @click="emitAction('import-tf')">
            <Icon name="upload" size="sm" class="text-blue-500" :stroke-width="2" />
            {{ t('keys.importToTf') }}
          </button>
          <button v-if="allowImport" type="button" class="menu-item" role="menuitem" @click="emitAction('import')">
            <Icon name="upload" size="sm" class="text-blue-500" :stroke-width="2" />
            {{ t('keys.importToCcSwitch') }}
          </button>
          <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
          <button type="button" class="menu-item text-red-600 dark:text-red-400" role="menuitem" @click="emitAction('delete')">
            <Icon name="trash" size="sm" :stroke-width="2" />
            {{ t('common.delete') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { ApiKey } from '@/types'

const props = defineProps<{
  show: boolean
  apiKey: ApiKey | null
  position: { top: number; left: number } | null
  allowImport: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'use', apiKey: ApiKey): void
  (event: 'import-tf', apiKey: ApiKey): void
  (event: 'import', apiKey: ApiKey): void
  (event: 'delete', apiKey: ApiKey): void
}>()

const { t } = useI18n()

// 菜单动作先传递当前 Key，再关闭浮层，确保后续弹窗不被透明遮罩拦截。
const emitAction = (event: 'use' | 'import-tf' | 'import' | 'delete') => {
  if (!props.apiKey) return
  if (event === 'use') emit('use', props.apiKey)
  else if (event === 'import-tf') emit('import-tf', props.apiKey)
  else if (event === 'import') emit('import', props.apiKey)
  else emit('delete', props.apiKey)
  emit('close')
}

const handleEscape = (event: KeyboardEvent) => {
  if (props.show && event.key === 'Escape') emit('close')
}

watch(
  () => props.show,
  (visible) => {
    if (visible) window.addEventListener('keydown', handleEscape)
    else window.removeEventListener('keydown', handleEscape)
  },
  { immediate: true },
)

onUnmounted(() => window.removeEventListener('keydown', handleEscape))
</script>

<style scoped>
.menu-item {
  @apply flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700;
}
</style>
