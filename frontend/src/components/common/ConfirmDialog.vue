<template>
  <BaseDialog :show="show" :title="title" width="narrow" :z-index="zIndex" @close="handleCancel">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-400">{{ message }}</p>
      <slot></slot>
    </div>

    <template #footer>
      <div class="flex justify-end space-x-3">
        <button @click="handleCancel" type="button" :disabled="loading" class="btn btn-secondary">
          {{ cancelText }}
        </button>
        <button
          @click="handleConfirm"
          type="button"
          :disabled="loading"
          :class="['btn', danger ? 'btn-danger' : 'btn-primary']"
        >
          {{ loading ? t('common.processing') : confirmText }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Z_INDEX } from '@/constants/overlay'
import BaseDialog from './BaseDialog.vue'

// ConfirmDialog.vue presents confirmation and cancellation actions in a narrow
// BaseDialog. Parent components own visibility and asynchronous work; loading disables
// the footer buttons, danger selects the confirmation color, zIndex selects the modal
// stacking layer for nested confirmations, and translated labels supply missing button
// text.

// t resolves the component action and status labels using the active application
// locale.
const { t } = useI18n()

// Props describes the values and options supplied by the parent component.
interface Props {
  show: boolean // Parent-controlled visible state.
  title: string // Dialog or section heading.
  message: string // Confirmation message body text.
  confirmText?: string // Confirmation action label override.
  cancelText?: string // Cancellation action label override.
  danger?: boolean // Destructive confirmation color flag.

  // Disables the footer buttons during parent work and displays the translated
  // processing label on confirmation.
  loading?: boolean
  // Overrides the shared modal layer when this confirmation opens above another dialog.
  zIndex?: number
}

// Emits defines the component notifications delivered to its parent listeners.
interface Emits {
  // Requests parent confirmation.
  (e: 'confirm'): void
  // Requests parent cancellation.
  (e: 'cancel'): void
}

// props holds parent-supplied component options consumed by the rendering and
// interaction handlers below.
const props = withDefaults(defineProps<Props>(), {
  danger: false,
  loading: false,
  zIndex: Z_INDEX.MODAL,
})

// confirmText and cancelText resolve caller labels or translated defaults for the two
// footer action buttons.
const confirmText = computed(() => props.confirmText || t('common.confirm'))
const cancelText = computed(() => props.cancelText || t('common.cancel'))

// emit publishes the declared component notifications so the parent can update its
// owned state.
const emit = defineEmits<Emits>()

// handleConfirm publishes a confirmation request for the parent action handler.
const handleConfirm = () => {
  emit('confirm')
}

// handleCancel publishes cancellation for the parent dialog visibility handler.
const handleCancel = () => {
  emit('cancel')
}
</script>
