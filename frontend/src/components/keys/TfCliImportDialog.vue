<template>
  <!-- @project-doc docs/interfaces/tf_cli_web_import.md#user_confirmation -->
  <BaseDialog
    :show="show"
    :title="t('keys.tfImport.title')"
    width="narrow"
    @close="requestClose"
  >
    <div class="min-w-0 space-y-5" data-test="tf-cli-import-dialog">
      <div class="flex min-w-0 items-center gap-3 border-b border-gray-100 pb-4 dark:border-dark-700">
        <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-dark-200">
          <Icon name="terminal" size="md" />
        </div>
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-white">
            {{ keyLabel }}
          </p>
          <p class="truncate text-xs text-gray-500 dark:text-dark-400">
            {{ pageOrigin }}
          </p>
        </div>
      </div>

      <div
        v-if="phase === 'discovering'"
        class="flex min-h-36 flex-col items-center justify-center gap-3 py-4 text-center"
        role="status"
      >
        <Icon name="refresh" size="lg" class="animate-spin text-primary-500" />
        <div>
          <p class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('keys.tfImport.discoveringTitle') }}
          </p>
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('keys.tfImport.discoveringDescription') }}
          </p>
        </div>
      </div>

      <template v-else-if="phase === 'ready' && target">
        <div
          v-if="target.verified"
          class="flex gap-3 rounded-control border border-emerald-200 bg-emerald-50 p-3 text-emerald-800 dark:border-emerald-800/60 dark:bg-emerald-950/30 dark:text-emerald-300"
          data-test="tf-cli-verified"
          role="status"
        >
          <Icon name="shield" size="md" class="mt-0.5 shrink-0" />
          <div class="min-w-0">
            <p class="text-sm font-medium">{{ t('keys.tfImport.verifiedTitle') }}</p>
            <p class="mt-1 text-xs leading-5">{{ t('keys.tfImport.verifiedDescription') }}</p>
          </div>
        </div>
        <div
          v-else
          class="flex gap-3 rounded-control border border-amber-200 bg-amber-50 p-3 text-amber-900 dark:border-amber-700/60 dark:bg-amber-950/30 dark:text-amber-200"
          data-test="tf-cli-unverified"
          role="alert"
        >
          <Icon name="exclamationTriangle" size="md" class="mt-0.5 shrink-0" />
          <div class="min-w-0">
            <p class="text-sm font-medium">{{ t('keys.tfImport.unverifiedTitle') }}</p>
            <p class="mt-1 text-xs leading-5">{{ t('keys.tfImport.unverifiedDescription') }}</p>
          </div>
        </div>

        <dl class="divide-y divide-gray-100 border-y border-gray-100 text-sm dark:divide-dark-700 dark:border-dark-700">
          <div class="flex min-w-0 items-center justify-between gap-4 py-3">
            <dt class="shrink-0 text-gray-500 dark:text-dark-400">{{ t('keys.tfImport.destination') }}</dt>
            <dd class="truncate font-mono text-xs text-gray-900 dark:text-dark-100">
              127.0.0.1:{{ target.port }}
            </dd>
          </div>
          <div v-if="groupLabel" class="flex min-w-0 items-center justify-between gap-4 py-3">
            <dt class="shrink-0 text-gray-500 dark:text-dark-400">{{ t('keys.tfImport.group') }}</dt>
            <dd class="truncate text-gray-900 dark:text-dark-100">{{ groupLabel }}</dd>
          </div>
        </dl>

        <p class="text-xs leading-5 text-gray-500 dark:text-dark-400">
          {{ t('keys.tfImport.confirmDescription') }}
        </p>
      </template>

      <div
        v-else-if="phase === 'sending'"
        class="flex min-h-36 flex-col items-center justify-center gap-3 py-4 text-center"
        role="status"
      >
        <Icon name="terminal" size="lg" class="text-primary-500" />
        <div>
          <p class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('keys.tfImport.waitingTitle') }}
          </p>
          <p class="mt-1 max-w-sm text-xs leading-5 text-gray-500 dark:text-dark-400">
            {{ t('keys.tfImport.waitingDescription') }}
          </p>
        </div>
      </div>

      <div
        v-else-if="phase === 'accepted'"
        class="flex gap-3 rounded-control border border-emerald-200 bg-emerald-50 p-4 text-emerald-800 dark:border-emerald-800/60 dark:bg-emerald-950/30 dark:text-emerald-300"
        data-test="tf-cli-accepted"
        role="status"
      >
        <Icon name="checkCircle" size="lg" class="shrink-0" />
        <div class="min-w-0">
          <p class="text-sm font-medium">{{ t('keys.tfImport.acceptedTitle') }}</p>
          <p class="mt-1 text-xs leading-5">{{ t('keys.tfImport.acceptedDescription') }}</p>
        </div>
      </div>

      <div
        v-else-if="phase === 'notFound'"
        class="flex gap-3 rounded-control border border-gray-200 bg-gray-50 p-4 text-gray-700 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-200"
        data-test="tf-cli-not-found"
        role="alert"
      >
        <Icon name="xCircle" size="lg" class="shrink-0 text-gray-500 dark:text-dark-400" />
        <div class="min-w-0">
          <p class="text-sm font-medium">{{ t('keys.tfImport.notFoundTitle') }}</p>
          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">
            {{ t('keys.tfImport.notFoundDescription') }}
          </p>
        </div>
      </div>

      <div
        v-else-if="phase === 'error'"
        class="flex gap-3 rounded-control border border-red-200 bg-red-50 p-4 text-red-800 dark:border-red-800/60 dark:bg-red-950/30 dark:text-red-300"
        data-test="tf-cli-import-error"
        role="alert"
      >
        <Icon name="exclamationCircle" size="lg" class="shrink-0" />
        <div class="min-w-0">
          <p class="text-sm font-medium">{{ t('keys.tfImport.errorTitle') }}</p>
          <p class="mt-1 text-xs leading-5">{{ importErrorMessage }}</p>
        </div>
      </div>
    </div>

    <template #footer>
      <template v-if="phase === 'ready'">
        <button type="button" class="btn btn-secondary" @click="requestClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary inline-flex items-center gap-2"
          data-test="tf-cli-send"
          @click="sendKey"
        >
          <Icon name="upload" size="sm" />
          {{ t('keys.tfImport.send') }}
        </button>
      </template>
      <template v-else-if="phase === 'discovering'">
        <button type="button" class="btn btn-secondary" @click="requestClose">
          {{ t('common.cancel') }}
        </button>
      </template>
      <template v-else-if="phase === 'sending'">
        <button type="button" class="btn btn-primary inline-flex cursor-wait items-center gap-2" disabled>
          <Icon name="refresh" size="sm" class="animate-spin" />
          {{ t('keys.tfImport.waiting') }}
        </button>
      </template>
      <template v-else-if="phase === 'accepted'">
        <button type="button" class="btn btn-primary" @click="requestClose">
          {{ t('common.close') }}
        </button>
      </template>
      <template v-else>
        <button type="button" class="btn btn-secondary" @click="requestClose">
          {{ t('common.close') }}
        </button>
        <button
          type="button"
          class="btn btn-primary inline-flex items-center gap-2"
          data-test="tf-cli-retry"
          @click="startDiscovery"
        >
          <Icon name="refresh" size="sm" />
          {{ t('keys.tfImport.retry') }}
        </button>
      </template>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import type { ApiKey } from '@/types'
import {
  findTfCli,
  importKeyToTf,
  type TfCliTarget,
} from '@/utils/tfCliImport'

type Phase = 'discovering' | 'ready' | 'sending' | 'accepted' | 'notFound' | 'error'

interface Props {
  show: boolean
  apiKey: ApiKey | null
}

interface Emits {
  (event: 'close'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()
const pageOrigin = window.location.origin

const phase = ref<Phase>('discovering')
const target = shallowRef<TfCliTarget | null>(null)
const importError = ref('')
let operationController: AbortController | null = null

const keyLabel = computed(() => props.apiKey?.name || `#${props.apiKey?.id ?? ''}`)
const groupLabel = computed(() => {
  const key = props.apiKey
  if (!key || key.is_composite || key.group_id == null) return ''
  return key.group?.name || String(key.group_id)
})

const importErrorMessage = computed(() => {
  switch (importError.value) {
    case 'rejected':
      return t('keys.tfImport.errors.rejected')
    case 'cancelled':
      return t('keys.tfImport.errors.cancelled')
    case 'busy':
      return t('keys.tfImport.errors.busy')
    case 'origin_not_allowed':
    case 'host_mismatch':
      return t('keys.tfImport.errors.originNotAllowed')
    case 'invalid_key':
    case 'invalid_metadata':
    case 'invalid_json':
    case 'content_type':
      return t('keys.tfImport.errors.invalidPayload')
    case 'unsupported_protocol':
      return t('keys.tfImport.errors.unsupportedVersion')
    case 'network_error':
      return t('keys.tfImport.errors.network')
    default:
      return t('keys.tfImport.errors.unexpected')
  }
})

function cancelOperation(): void {
  operationController?.abort()
  operationController = null
}

async function startDiscovery(): Promise<void> {
  if (!props.show || !props.apiKey) return

  cancelOperation()
  const controller = new AbortController()
  operationController = controller
  phase.value = 'discovering'
  target.value = null
  importError.value = ''

  const discovered = await findTfCli(controller.signal)
  if (controller.signal.aborted || operationController !== controller || !props.show) return

  operationController = null
  target.value = discovered
  phase.value = discovered ? 'ready' : 'notFound'
}

async function sendKey(): Promise<void> {
  const key = props.apiKey
  const destination = target.value
  if (!key || !destination || phase.value !== 'ready') return

  cancelOperation()
  const controller = new AbortController()
  operationController = controller
  phase.value = 'sending'

  const payload = {
    key: key.key,
    host: pageOrigin,
    key_name: key.name,
    ...(!key.is_composite && key.group_id != null
      ? { group_id: key.group_id, group_name: key.group?.name }
      : {}),
  }
  const result = await importKeyToTf(destination, payload, controller.signal)
  if (controller.signal.aborted || operationController !== controller || !props.show) return

  operationController = null
  if (result.ok) {
    phase.value = 'accepted'
    return
  }

  if (result.error === 'session_expired' || result.error === 'session_proof_unavailable') {
    target.value = { ...destination, verified: false }
    phase.value = 'ready'
    return
  }

  importError.value = result.error
  phase.value = 'error'
}

function requestClose(): void {
  cancelOperation()
  emit('close')
}

watch(
  () => [props.show, props.apiKey?.id] as const,
  ([show]) => {
    if (show) void startDiscovery()
    else cancelOperation()
  },
  { immediate: true },
)

onUnmounted(cancelOperation)
</script>
