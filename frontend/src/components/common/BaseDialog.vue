<template>
  <Teleport to="body">
    <MotionTransition name="modal" @after-leave="handleAfterLeave">
      <div
        v-if="show"
        :inert="!show || undefined"
        class="modal-overlay h-[100dvh] w-[100dvw] min-w-0 overflow-hidden"
        :style="zIndexStyle"
        :aria-labelledby="dialogId"
        role="dialog"
        aria-modal="true"
        @click.self="handleClose"
      >
        <!--
          Dynamic viewport units account for mobile browser toolbars; shared vh/vw
          rules provide dimensions when those units are unsupported.
        -->
        <div
          ref="dialogRef"
          tabindex="-1"
          :class="['modal-content min-h-0 min-w-0 max-h-[95dvh] sm:max-h-[90dvh]', widthClasses]"
          @click.stop
        >
          <!-- Header -->
          <div class="modal-header min-w-0 max-w-full gap-3">
            <div class="flex min-w-0 flex-1 items-center gap-3">
              <slot name="header-icon"></slot>
              <div class="min-w-0">
                <h3 :id="dialogId" class="modal-title min-w-0 break-words">
                  {{ title }}
                </h3>
                <p v-if="subtitle" class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-400">
                  {{ subtitle }}
                </p>
              </div>
            </div>
            <slot name="header-actions"></slot>
            <button
              @click="emit('close')"
              class="-mr-2 rounded-control p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-black/10 focus-visible:ring-offset-2 dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300 dark:focus-visible:ring-primary-500/30 dark:focus-visible:ring-offset-dark-900"
              aria-label="Close modal"
            >
              <Icon name="x" size="sm" />
            </button>
          </div>

          <!-- Body content -->
          <div
            ref="modalBodyRef"
            class="modal-body min-h-0 min-w-0 max-w-full"
            :class="{ 'modal-body-contained': !bodyScroll, 'modal-body-flush': flush }"
          >
            <slot></slot>
          </div>

          <!-- Footer -->
          <div v-if="$slots.footer" class="modal-footer min-w-0 max-w-full">
            <slot name="footer"></slot>
          </div>
        </div>
      </div>
    </MotionTransition>
  </Teleport>
</template>

<script lang="ts">
// dialogIdCounter is shared across dialog instances so each title receives a distinct
// label target.
let dialogIdCounter = 0
</script>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, watch, onMounted, onUnmounted, ref, nextTick } from 'vue'
import { useDialogLifecycle } from '@/composables/useDialogLifecycle'
import Icon from '@/components/icons/Icon.vue'
import { Z_INDEX } from '@/constants/overlay'

// BaseDialog.vue renders teleported dialogs with shared headers, content, and footer
// slots. The lifecycle composable owns scroll locking and focus restoration, while
// this shell checks topmost status for Escape, leaves composition keystrokes with the
// input method, forwards dismissal requests, and resets the body scroll position when
// opened.

// dialogId connects this dialog to its own title element, keeping simultaneous
// accessible labels distinct.
// eslint-disable-next-line no-useless-assignment -- Later dialog instances read the incremented title counter.
const dialogId = `modal-title-${++dialogIdCounter}`

// dialogRef identifies the focusable panel for lifecycle management; modalBodyRef
// identifies the scroll container reset when the dialog opens.
const dialogRef = ref<HTMLElement | null>(null)
const modalBodyRef = ref<HTMLElement | null>(null)

// DialogWidth names the responsive width recipes supported by BaseDialog shells.
type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

// Props describes the values and options supplied by the parent component.
interface Props {
  show: boolean // Parent-controlled visible state.
  title: string // Dialog or section heading.

  subtitle?: string // Optional truncated subtitle text.
  width?: DialogWidth // Responsive dialog width recipe.
  // Controls outer body scrolling; false allocates the available height to content
  // that manages its own scrolling.
  bodyScroll?: boolean

  // Removes body padding when embedded content needs to lay out panes against the
  // dialog edges.
  flush?: boolean
  closeOnEscape?: boolean // Allows topmost Escape dismissal.
  closeOnClickOutside?: boolean // Allows backdrop dismissal requests.
  // Overrides the default modal layer when this shell needs to appear above another
  // visible dialog.
  zIndex?: number
}

// Emits defines the component notifications delivered to its parent listeners.
interface Emits {
  // Requests parent dismissal.
  (e: 'close'): void
  // Reports completed shell exit.
  (e: 'after-leave'): void
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  width: 'normal',
  bodyScroll: true,
  flush: false,
  closeOnEscape: true,
  closeOnClickOutside: false,
  zIndex: Z_INDEX.MODAL,
})

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<Emits>()

// zIndexStyle and widthClasses translate caller presentation options into the modal
// layer and responsive width classes used by the shell.
const zIndexStyle = computed(() => {
  return props.zIndex !== Z_INDEX.MODAL ? { zIndex: props.zIndex } : undefined
})

const widthClasses = computed(() => {
  // Each width recipe retains a mobile viewport margin, then expands at named
  // breakpoints to fit the requested desktop dialog size.
  const widths: Record<DialogWidth, string> = {
    narrow: 'max-w-[calc(100vw-1rem)] sm:max-w-md',
    normal: 'max-w-[calc(100vw-1rem)] sm:max-w-lg',
    wide: 'max-w-[calc(100vw-1rem)] sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
    'extra-wide': 'max-w-[calc(100vw-1rem)] sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
    full: 'max-w-[calc(100vw-1rem)] sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl',
  }
  return widths[props.width]
})

// handleClose requests dismissal when closeOnClickOutside permits backdrop
// interaction. The parent remains responsible for changing show, and the header close
// button publishes its own dismissal request independently.
const handleClose = () => {
  if (props.closeOnClickOutside) {
    emit('close')
  }
}

// handleEscape requests dismissal for Escape on the currently topmost visible dialog.
// Keystrokes during composition stay with the input method. A caller can disable Escape
// closing, and lower dialogs defer to the active lifecycle stack entry.
const handleEscape = (event: KeyboardEvent) => {
  // Some engines clear isComposing before dispatching the composition keydown and use
  // keyCode 229 as the remaining IME signal.
  if (event.isComposing || event.keyCode === 229) return

  if (props.show && isTop() && props.closeOnEscape && event.key === 'Escape') {
    emit('close')
  }
}

// afterLeave and isTop delegate resource release and Escape precedence to the shared
// dialog lifecycle stack.
const { afterLeave, isTop } = useDialogLifecycle(() => props.show, dialogRef)

// handleAfterLeave releases dialog lifecycle resources after the visual exit. It emits
// completion only while show remains false, allowing callers to retain a shell that
// reopened during its transition.
function handleAfterLeave() {
  // Release the shared dialog lifecycle before notifying the parent, since that
  // notification may unmount this shell immediately after exit.
  afterLeave()
  if (!props.show) emit('after-leave')
}

// Reopening waits for the body DOM before resetting its scroll offset; a second
// visibility check avoids scrolling a shell that closed during that tick.
watch(
  () => props.show,
  async (open) => {
    if (!open) return
    await nextTick()
    if (props.show && modalBodyRef.value) modalBodyRef.value.scrollTop = 0
  },
  { immediate: true },
)

// The mounted dialog observes document Escape events even when focus is outside its
// panel; topmost checks keep lower dialogs from responding.
onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

// Remove this instance keyboard listener at unmount so detached shells do not keep
// receiving document events or retain component state.
onUnmounted(() => {
  document.removeEventListener('keydown', handleEscape)
})
</script>

<style scoped>
/*
  Contained forms own scrolling; this flex body allocates the remaining height
  between the header and footer.
*/
.modal-body-contained {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* Flush panes own their content spacing, so the outer dialog body contributes no padding. */
.modal-body-flush {
  padding: 0;
}
</style>
