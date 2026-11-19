<template>
  <BaseDialog
    :show="keyRow !== null"
    :title="t('keys.ccsClientSelect.title')"
    width="narrow"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-400">
        {{ t('keys.ccsClientSelect.description') }}
      </p>
      <Select v-model="ccClient" :options="ccClientOptions" />
      <Select
        v-model="ccModel"
        :options="ccModelOptions"
        searchable
        :placeholder="t('keys.useKeyModal.selectModel')"
      />
      <p v-if="!ccModelOptions.length" class="input-hint">{{ t('keys.useKeyModal.noModels') }}</p>
      <button type="button" class="btn btn-primary" :disabled="!ccModel" @click="handleConfirm">
        {{ t('common.confirm') }}
      </button>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useAppStore } from '@/stores/app'
import type { Group } from '@/api/groups.types'
import type { ApiKey } from '@/api/keys.types'
import type { PublicSettings } from '@/api/settings.types'
import {
  buildCcSwitchImportDeeplink,
  buildCcSwitchUsageScript,
  type CcSwitchClientType,
} from '@/utils/ccswitchImport'
import {
  availableClients,
  CLIENT_LABELS,
  clientProtocol,
  modelsForProtocol,
} from '@/utils/clientConfig'
import { resolveKeyGroupWithModels } from './keysPresentation'

/**
 * KeyCcSwitchImportModal.vue owns CC-Switch client and model selection,
 * deeplink construction, and focus-probe installation feedback for a selected
 * API key so KeysView does not carry external client-import state.
 */

const props = defineProps<{
  keyRow: ApiKey | null // Target API key selected for CC-Switch import or null when closed.
  groups: Group[] // Loaded routing groups supplying allowed protocols and models.
  publicSettings: PublicSettings | null // Public site settings supplying base URL and site name.
}>()

const emit = defineEmits<{
  close: [] // Closes the CC-Switch import modal and clears pending selection.
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { balanceUnitName } = useBalanceDisplay()

const ccClient = ref<CcSwitchClientType>('claude')
const ccModel = ref('')
let focusProbeTimer: ReturnType<typeof setTimeout> | null = null

/**
 * clearFocusProbeTimer cancels any pending custom-protocol focus check on
 * unmount.
 */
const clearFocusProbeTimer = () => {
  if (focusProbeTimer !== null) {
    clearTimeout(focusProbeTimer)
    focusProbeTimer = null
  }
}

const ccGroup = computed(() => resolveKeyGroupWithModels(props.keyRow, props.groups))

const ccClientOptions = computed(() =>
  availableClients(ccGroup.value?.allowed_protocols ?? [])
    .filter((client) => client !== 'opencode')
    .map((client) => ({ value: client, label: CLIENT_LABELS[client] })),
)

const ccModelOptions = computed(() => {
  const protocol = clientProtocol(ccClient.value, ccGroup.value?.allowed_protocols ?? [])
  return protocol
    ? modelsForProtocol(ccGroup.value, protocol).map((model) => ({ value: model, label: model }))
    : []
})

watch(
  ccClientOptions,
  (options) => {
    if (!options.some((option) => option.value === ccClient.value)) {
      ccClient.value = (options[0]?.value as CcSwitchClientType) ?? 'claude'
    }
  },
  { immediate: true },
)

watch(
  ccModelOptions,
  (options) => {
    if (!options.some((option) => option.value === ccModel.value)) {
      ccModel.value = options[0]?.value ?? ''
    }
  },
  { immediate: true },
)

/**
 * executeCcsImport opens the CC-Switch custom protocol deeplink and probes
 * document focus after 100ms to hint when the desktop handler is not installed.
 */
const executeCcsImport = (row: ApiKey, clientType: CcSwitchClientType) => {
  const baseUrl = props.publicSettings?.api_base_url || window.location.origin
  const usageScript = buildCcSwitchUsageScript(baseUrl, balanceUnitName.value)
  const providerName = (props.publicSettings?.site_name || 'tokenrouter').trim() || 'tokenrouter'
  const deeplink = buildCcSwitchImportDeeplink({
    baseUrl,
    model: ccModel.value,
    clientType,
    providerName,
    apiKey: row.key,
    usageScript,
  })

  try {
    window.open(deeplink, '_self')
    clearFocusProbeTimer()
    // Probe browser focus after custom-protocol dispatch as a heuristic for
    // whether an external desktop handler intercepted the navigation.
    focusProbeTimer = setTimeout(() => {
      focusProbeTimer = null
      if (document.hasFocus()) {
        appStore.showError(t('keys.ccSwitchNotInstalled'))
      }
    }, 100)
  } catch {
    appStore.showError(t('keys.ccSwitchNotInstalled'))
  }
}

/**
 * handleConfirm dispatches CC-Switch import when a valid model is selected and
 * closes the dialog.
 */
const handleConfirm = () => {
  if (props.keyRow && ccModelOptions.value.some((option) => option.value === ccModel.value)) {
    executeCcsImport(props.keyRow, ccClient.value)
  }
  emit('close')
}

onUnmounted(() => {
  clearFocusProbeTimer()
})
</script>
