<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { getFloatingPanelPosition, type FloatingPanelPosition } from '@/utils/floatingPanel'

// PluginActionsMenu.vue presents lifecycle actions beside one installed plugin.
// It reuses native menu classes and viewport positioning, with a teleported panel
// that remains outside the table's clipping area. Keyboard navigation moves among
// available actions, and dismissal restores focus to the originating row control.
//
// Scroll and resize observation exists only while the menu is open. If its trigger
// disappears, positioning closes the panel and releases listeners. The transition
// retains exit rendering while semantic visibility and keyboard handling follow the
// current open state, preventing a hidden menu from continuing to receive actions.

// Props applies the parent operation lock and emergency execution switch to each
// available action; uninstall remains selectable when only execution is disabled.
const props = defineProps<{
  disabled?: boolean // Parent operation locks all actions.
  updateAvailable?: boolean // Indexed update can be reviewed.
  previousVersion?: string // Accepted manual rollback release version.
  changesDisabled?: boolean // Emergency switch blocks release changes.
  executionDisabled?: boolean // Emergency switch blocks process actions.
}>()
const emit = defineEmits<{
  update: [] // Opens reviewed indexed update selection.
  rollback: [] // Opens previous release confirmation.
  restart: [] // Requests the reviewed restart action.
  uninstall: [] // Opens the parent uninstall confirmation.
}>()
const { t } = useI18n()
const trigger = ref<HTMLButtonElement | null>(null)
const menu = ref<HTMLElement | null>(null)
const open = ref(false)
const position = ref<FloatingPanelPosition | null>(null)
const style = computed(() =>
  position.value
    ? {
        top: position.value.top === null ? undefined : `${position.value.top}px`,
        bottom: position.value.bottom === null ? undefined : `${position.value.bottom}px`,
        left: `${position.value.left}px`,
        width: `${position.value.width}px`,
        maxHeight: `${position.value.maxHeight}px`,
      }
    : {},
)

/** reposition keeps the teleported menu aligned with its current row trigger.
 * Missing triggers close the menu. Native positioning clamps its width and
 * chooses the available viewport side while the panel supplies its own scrolling.
 */
function reposition(): void {
  if (!open.value) return
  if (!trigger.value?.isConnected) {
    open.value = false
    return
  }
  position.value = getFloatingPanelPosition(
    trigger.value.getBoundingClientRect(),
    window.innerWidth,
    window.innerHeight,
    { maxWidth: 208, pinLeftOnMobile: false },
  )
}

/** close dismisses the menu and restores keyboard focus to its row action control. */
function close(): void {
  open.value = false
  trigger.value?.focus()
}

/** toggle opens one row menu and focuses its first available action after rendering.
 * A disabled parent ignores activation. If the menu closes before the render tick,
 * focus remains with the control that handled that later interaction.
 */
async function toggle(): Promise<void> {
  if (props.disabled) return
  if (open.value) {
    close()
    return
  }
  open.value = true
  reposition()
  await nextTick()
  if (open.value) menu.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
}

/** navigate handles native menu arrow, boundary, Escape, and Tab dismissal keys.
 * Only enabled buttons participate in wrapped movement. Tab returns focus to
 * the trigger before continuing ordinary document navigation outside the closed menu.
 */
function navigate(event: KeyboardEvent): void {
  if (!open.value) return
  if (event.key === 'Escape' || event.key === 'Tab') {
    if (event.key === 'Escape') event.preventDefault()
    close()
    return
  }
  const buttons = [
    ...(menu.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? []),
  ]
  if (!buttons.length || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement)
  const next =
    event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? buttons.length - 1
        : (current + (event.key === 'ArrowUp' ? -1 : 1) + buttons.length) % buttons.length
  buttons[next]?.focus()
}

/** select forwards one action only while this menu is open and unlocked.
 * It closes and restores focus before emitting, allowing a parent dialog to
 * capture the originating row control while an exiting menu ignores further activation.
 */
function select(action: 'restart' | 'uninstall' | 'update' | 'rollback'): void {
  if (!open.value || props.disabled) return
  if (
    (action === 'restart' && props.executionDisabled) ||
    (action === 'update' && (!props.updateAvailable || props.changesDisabled)) ||
    (action === 'rollback' && (!props.previousVersion || props.changesDisabled))
  )
    return
  close()
  if (action === 'update') emit('update')
  else if (action === 'rollback') emit('rollback')
  else if (action === 'restart') emit('restart')
  else emit('uninstall')
}

/** stopPositioning releases the listeners installed for a visible floating menu. */
function stopPositioning(): void {
  window.removeEventListener('resize', reposition)
  window.removeEventListener('scroll', reposition, true)
}
watch(open, (shown) => {
  stopPositioning()
  if (shown) {
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
  }
})
watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) open.value = false
  },
)
onBeforeUnmount(stopPositioning)
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="btn btn-ghost btn-icon btn-sm"
    :aria-label="t('admin.plugins.actions')"
    :title="t('admin.plugins.actions')"
    aria-haspopup="menu"
    :aria-expanded="open"
    :disabled="disabled"
    @click="toggle"
    @keydown.down.prevent="toggle"
    ><Icon name="more" size="sm"
  /></button>
  <Teleport to="body">
    <div v-if="open" class="fixed inset-0 z-menu-overlay" @click="close" />
    <MotionTransition name="dropdown-fade">
      <div
        v-if="open"
        ref="menu"
        role="menu"
        class="action-menu overflow-y-auto py-1"
        :style="style"
        :aria-label="t('admin.plugins.actions')"
        @keydown="navigate"
      >
        <button
          type="button"
          role="menuitem"
          class="dropdown-item"
          :disabled="!updateAvailable || changesDisabled"
          @click="select('update')"
          ><Icon name="download" size="sm" />{{ t('admin.plugins.update') }}</button
        >
        <button
          type="button"
          role="menuitem"
          class="dropdown-item"
          :disabled="!previousVersion || changesDisabled"
          @click="select('rollback')"
          ><Icon name="history" size="sm" />{{
            previousVersion
              ? t('admin.plugins.rollbackTo', { version: previousVersion })
              : t('admin.plugins.rollback')
          }}</button
        >
        <button
          type="button"
          role="menuitem"
          class="dropdown-item"
          :disabled="executionDisabled"
          @click="select('restart')"
          ><Icon name="refresh" size="sm" />{{ t('admin.plugins.restart') }}</button
        >
        <button
          type="button"
          role="menuitem"
          class="dropdown-item text-red-600 dark:text-red-400"
          @click="select('uninstall')"
          ><Icon name="trash" size="sm" />{{ t('admin.plugins.uninstall') }}</button
        >
      </div>
    </MotionTransition>
  </Teleport>
</template>
