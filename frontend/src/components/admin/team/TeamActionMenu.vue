<template>
  <Teleport to="body">
    <div v-if="show && team && position">
      <div class="fixed inset-0 z-menu-overlay" @click="emit('close')"></div>
      <div
        class="action-menu w-52 overflow-hidden"
        :style="{ top: `${position.top}px`, left: `${position.left}px` }"
        @click.stop
      >
        <div class="py-1">
          <button class="dropdown-item" @click="emitAction('details')">
            <Icon name="eye" size="sm" class="text-gray-400" :stroke-width="2" />
            {{ t('team.viewDetails') }}
          </button>
          <button class="dropdown-item" @click="emitAction('statistics')">
            <Icon name="chart" size="sm" class="text-indigo-500" :stroke-width="2" />
            {{ t('team.viewStatistics') }}
          </button>
          <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>
          <button class="dropdown-item text-red-600 dark:text-red-400" @click="emitAction('dissolve')">
            <Icon name="trash" size="sm" :stroke-width="2" />
            {{ t('team.dissolve') }}
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
import type { AdminTeam } from '@/api/admin/teams'

const props = defineProps<{
  show: boolean
  team: AdminTeam | null
  position: { top: number; left: number } | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'details', team: AdminTeam): void
  (event: 'statistics', team: AdminTeam): void
  (event: 'dissolve', team: AdminTeam): void
}>()

const { t } = useI18n()

// 菜单动作统一先关闭浮层，避免打开弹窗后仍残留透明遮罩。
const emitAction = (event: 'details' | 'statistics' | 'dissolve') => {
  if (!props.team) return
  if (event === 'details') emit('details', props.team)
  else if (event === 'statistics') emit('statistics', props.team)
  else emit('dissolve', props.team)
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

