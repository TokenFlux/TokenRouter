<template>
  <BaseDialog
    :show="show"
    :title="t(isDecisionProvider ? 'admin.providers.decisionTest.title' : 'admin.providers.testDialog.title', { name: provider?.name ?? '' })"
    width="wide"
    :body-scroll="false"
    flush
    @close="handleClose"
  >
    <template #header-icon>
      <!-- 与模型广场一致使用模型品牌图标，单色品牌跟随文字色显示为黑/白 -->
      <span
        v-if="provider"
        class="flex h-10 w-10 shrink-0 items-center justify-center rounded-control border border-gray-200 bg-gray-50 text-gray-900 dark:border-dark-600 dark:bg-dark-950 dark:text-white"
      >
        <ProviderIcon v-if="hasProviderBrandIcon" :brand="provider.platform" size="22px" />
        <PlatformIcon v-else :platform="provider.platform" size="lg" :class="platformIconClass(provider.platform)" />
      </span>
    </template>

    <template #header-actions>
      <SettingsSegmented
        v-model="testScope"
        :options="scopeOptions"
        :ariaLabel="t('admin.providers.testDialog.scope')"
        :disabled="busy"
        class="hidden shrink-0 sm:inline-flex"
      />
    </template>

    <div class="flex min-h-0 flex-1 flex-col overflow-y-auto md:h-[38rem] md:flex-initial md:flex-row md:overflow-hidden">
      <!-- 左侧：本次测试的参数，不写回提供商配置 -->
      <aside
        :aria-label="t('admin.providers.testDialog.settings')"
        :class="isDecisionProvider ? 'md:w-96' : 'md:w-72'"
        class="flex shrink-0 flex-col gap-5 border-b border-gray-200 bg-gray-50/70 px-4 py-5 dark:border-dark-600 dark:bg-dark-950 sm:px-6 md:overflow-y-auto md:border-b-0 md:border-r"
      >
        <SettingsSegmented
          v-model="testScope"
          :options="scopeOptions"
          :ariaLabel="t('admin.providers.testDialog.scope')"
          :disabled="busy"
          block
          class="sm:hidden"
        />

        <div class="text-sm font-semibold text-gray-900 dark:text-dark-50">
          {{ t('admin.providers.testDialog.settings') }}
        </div>

        <div v-if="imageTestAvailable" class="space-y-1.5">
          <div class="input-label">{{ t('admin.providers.testDialog.type') }}</div>
          <SettingsSegmented
            v-model="testType"
            :options="testTypeOptions"
            :ariaLabel="t('admin.providers.testDialog.type')"
            :disabled="busy"
            block
          />
        </div>

        <div v-if="testScope === 'single'" class="space-y-1.5">
          <label class="input-label" :for="modelFieldId">{{ t('admin.providers.testDialog.model') }}</label>
          <Select
            :id="modelFieldId"
            data-testid="provider-test-model"
            v-model="selectedModelId"
            :options="availableModels"
            :disabled="loadingModels || busy"
            value-key="id"
            label-key="id"
            creatable
            :placeholder="loadingModels ? t('common.loading') : t('admin.providers.testDialog.modelPlaceholder')"
          />
        </div>

        <p v-if="!loadingModels && availableModels.length === 0" class="input-hint" data-testid="provider-test-models-empty">
          {{ t('admin.providers.testDialog.modelsEmpty') }}
        </p>

        <div v-if="!isDecisionProvider && testType === 'text'" class="space-y-1.5">
          <label class="input-label" :for="protocolFieldId">{{ t('admin.providers.testDialog.protocol') }}</label>
          <Select
            :id="protocolFieldId"
            v-model="testProtocol"
            :options="protocolOptions"
            :disabled="busy || !protocolPlan.selectable || isCompactTestMode"
            :placeholder="t('admin.providers.testDialog.protocolNone')"
            data-testid="provider-test-protocol"
          />
          <p v-if="protocolHint" class="input-hint">{{ protocolHint }}</p>
        </div>

        <div v-if="isOpenAIProvider && testType === 'text'" class="space-y-1.5">
          <label class="input-label" :for="modeFieldId">{{ t('admin.providers.openai.testMode') }}</label>
          <Select
            :id="modeFieldId"
            v-model="testMode"
            :options="openAITestModeOptions"
            :disabled="busy"
          />
        </div>

        <div v-if="testScope === 'batch'" class="space-y-1.5">
          <label class="input-label" :for="concurrencyFieldId">{{ t('admin.providers.testDialog.batch.concurrency') }}</label>
          <Select
            :id="concurrencyFieldId"
            v-model="batch.concurrency"
            :options="concurrencyOptions"
            :disabled="busy"
            data-testid="provider-batch-concurrency"
          />
        </div>

        <SystemOneTestForm v-if="isDecisionProvider" :key="`${provider?.id}-${show}`" :disabled="busy" @change="decisionPayload = $event" />
        <TextArea
          v-else-if="!isCompactTestMode"
          v-model="testPrompt"
          :label="promptInputLabel"
          :placeholder="promptInputPlaceholder"
          :disabled="busy"
          data-testid="provider-test-prompt"
          rows="4"
        />
      </aside>

      <!-- 右侧展示单模型结果或批量模型列表，窄屏按内容撑高并随左栏滚动。 -->
      <section
        :aria-label="t('admin.providers.testDialog.results')"
        class="flex min-w-0 flex-col px-4 py-5 sm:px-6 md:min-h-0 md:flex-1"
      >
        <component :is="isDecisionProvider ? SystemOneTestResult : ProviderTestResultView" v-if="testScope === 'single'" :run="singleRun" />
        <ProviderTestBatchPanel v-else :batch="batch" :decision="isDecisionProvider" />
      </section>
    </div>

    <footer
      class="flex shrink-0 items-center justify-end gap-3 rounded-b-surface border-t border-gray-200 bg-gray-50/70 px-4 py-3 dark:border-dark-600 dark:bg-dark-950 sm:rounded-b-dialog sm:px-6"
    >
      <button type="button" class="btn btn-secondary" @click="handleClose">
        {{ t('common.close') }}
      </button>

      <button
        v-if="testScope === 'single'"
        type="button"
        class="btn btn-primary"
        data-testid="provider-test-start"
        :disabled="!canTestSingle"
        @click="startTest"
      >
        <Icon v-if="singleRunning" name="loader" size="sm" class="animate-spin" :animate-on-hover="false" />
        <Icon v-else-if="singleRun.status === 'idle'" name="play" size="sm" />
        <Icon v-else name="refresh" size="sm" />
        {{ singleButtonLabel }}
      </button>

      <template v-else-if="batch.running">
        <button
          type="button"
          class="btn btn-secondary"
          data-testid="provider-batch-stop"
          :disabled="batch.stopping"
          @click="batch.stop()"
        >
          {{ batch.stopping ? t('admin.providers.testDialog.batch.stoppingShort') : t('admin.providers.testDialog.batch.stop') }}
        </button>
      </template>

      <template v-else>
        <button
          v-if="batch.failedModels.length > 0"
          type="button"
          class="btn btn-secondary"
          data-testid="provider-batch-retry"
          :disabled="isDecisionProvider && !decisionPayload"
          @click="startBatch(batch.failedModels, true)"
        >
          {{ t('admin.providers.testDialog.batch.retry', { count: batch.failedModels.length }) }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          data-testid="provider-batch-start"
          :disabled="batch.selected.size === 0 || (isDecisionProvider && !decisionPayload)"
          @click="startBatch(selectedBatchModels, false)"
        >
          <Icon name="play" size="sm" />
          {{ t('admin.providers.testDialog.batch.start', { count: batch.selected.size }) }}
        </button>
      </template>
    </footer>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { platformIconClass } from '@/utils/platformColors'
import { resolveProviderBrand } from '@/utils/providerBrand'
import type { Provider, ClaudeModel } from '@/types'
import ProviderTestBatchPanel from './ProviderTestBatchPanel.vue'
import ProviderTestResultView from './ProviderTestResultView.vue'
import SystemOneTestForm from './SystemOneTestForm.vue'
import SystemOneTestResult from './SystemOneTestResult.vue'
import type { SystemOneTestPayload } from './systemOneTest'
import {
  defaultProviderTestProtocol,
  providerTestProtocolPlan,
  type ProviderTestProtocol
} from './providerTestProtocols'
import {
  createProviderTestRun,
  executeProviderTest,
  resetProviderTestRun,
  type ProviderTestRequestBody
} from './providerTestRun'
import { BATCH_CONCURRENCY_OPTIONS, useProviderBatchTest } from './useProviderBatchTest'

const props = defineProps<{
  show: boolean
  provider: Provider | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()

const modelFieldId = 'provider-test-model'
const protocolFieldId = 'provider-test-protocol'
const modeFieldId = 'provider-test-mode'
const concurrencyFieldId = 'provider-test-concurrency'

const testScope = ref<'single' | 'batch'>('single')
const scopeOptions = computed(() => [
  { value: 'single' as const, label: t('admin.providers.testDialog.scopeSingle') },
  { value: 'batch' as const, label: t('admin.providers.testDialog.scopeBatch') }
])

const singleRun = createProviderTestRun()
const singleRunning = computed(() => singleRun.status === 'connecting')
let singleController: AbortController | null = null

const batch = reactive(useProviderBatchTest(t))
const busy = computed(() => singleRunning.value || batch.running)

const availableModels = ref<ClaudeModel[]>([])
const selectedModelId = ref('')
const loadingModels = ref(false)
const testPrompt = ref('')
let lastDefaultPrompt = ''

// Antigravity、Qoder 等未配置品牌图形的平台使用 PlatformIcon。
const hasProviderBrandIcon = computed(() => Boolean(resolveProviderBrand(props.provider?.platform).iconKey))

const isDecisionProvider = computed(() => props.provider?.platform === 'jev')
const decisionPayload = ref<SystemOneTestPayload | null>(null)

const isOpenAIProvider = computed(() => props.provider?.platform === 'openai')
const isCNProvider = computed(() => ['kimi', 'zhipu', 'deepseek'].includes(props.provider?.platform ?? ''))

// 协议选择用于构造本次测试请求。
const protocolPlan = computed(() => providerTestProtocolPlan(props.provider))
const testProtocol = ref<ProviderTestProtocol | 'native' | 'all'>('native')
const protocolOptions = computed(() => {
  const options: Array<{ value: typeof testProtocol.value; label: string }> = protocolPlan.value.options.map((item) => ({
    value: item.value,
    label: `${item.label} · ${item.path}`
  }))
  // 国产平台启用了多个协议时，保留按顺序全部验证的选项。
  if (isCNProvider.value && protocolPlan.value.selectable) {
    options.push({ value: 'all', label: t('admin.providers.testDialog.protocolAll') })
  }
  return options
})
// 弹窗打开期间切换提供商时，原协议不在新列表里就回到默认值。
watch(protocolPlan, (plan) => {
  if (!protocolOptions.value.some((item) => item.value === testProtocol.value)) {
    testProtocol.value = defaultProviderTestProtocol(props.provider, plan)
  }
})

const testMode = ref<'default' | 'compact' | 'legacy_compact'>('default')
const testType = ref<'text' | 'image'>('text')
// Compact 连接测试使用固定载荷和 Responses 端点，不显示可编辑提示词。
const isCompactTestMode = computed(() => isOpenAIProvider.value && testMode.value !== 'default')
watch(testMode, (mode) => {
  if (mode !== 'default' && protocolPlan.value.selectable) testProtocol.value = 'responses'
})
const openAITestModeOptions = computed(() => [
  { value: 'default', label: t('admin.providers.openai.testModeDefault') },
  { value: 'compact', label: t('admin.providers.openai.testModeCompact') },
  { value: 'legacy_compact', label: t('admin.providers.openai.testModeLegacyCompact') }
])
const protocolHint = computed(() => {
  if (isCompactTestMode.value) return t('admin.providers.testDialog.protocolCompact')
  if (protocolPlan.value.options.length === 0) return ''
  if (!protocolPlan.value.selectable) return t('admin.providers.testDialog.protocolFixed')
  return ''
})

const concurrencyOptions = BATCH_CONCURRENCY_OPTIONS.map((value) => ({ value, label: String(value) }))

// 实际发给后端的协议：固定端点、全部协议和图片测试都不携带该字段。
const requestProtocol = computed<ProviderTestProtocol | undefined>(() => {
  if (testType.value !== 'text' || !protocolPlan.value.selectable) return undefined
  if (isCompactTestMode.value) return 'responses'
  if (testProtocol.value === 'native' || testProtocol.value === 'all') return undefined
  return testProtocol.value
})

// 图片或文字请求类型由管理员选择。
const imageTestAvailable = computed(() => {
  const platform = props.provider?.platform
  return platform === 'openai' || platform === 'gemini' || platform === 'grok'
})

const testTypeOptions = computed(() => [
  { value: 'text' as const, label: t('admin.providers.testDialog.typeText'), icon: 'modalityText' as const },
  { value: 'image' as const, label: t('admin.providers.testDialog.typeImage'), icon: 'modalityImage' as const }
])

const promptInputLabel = computed(() =>
  testType.value === 'image'
    ? t('admin.providers.imagePromptLabel')
    : t('admin.providers.textPromptLabel')
)
const promptInputPlaceholder = computed(() =>
  testType.value === 'image'
    ? t('admin.providers.imagePromptPlaceholder')
    : t('admin.providers.textPromptPlaceholder')
)

const canTestSingle = computed(() => !busy.value && !!selectedModelId.value && (!isDecisionProvider.value || !!decisionPayload.value))

const singleButtonLabel = computed(() => {
  if (singleRunning.value) return t('admin.providers.testDialog.running')
  if (singleRun.status === 'idle') return t('admin.providers.startTest')
  return t('admin.providers.testDialog.rerun')
})

// 批量测试按模型列表的顺序执行已选模型。
const selectedBatchModels = computed(() => batch.models.filter((model) => batch.selected.has(model)))

// 打开弹窗时重置参数并加载可测试模型。
watch(
  () => [props.show, props.provider?.id] as const,
  async ([open]) => {
    abortAll()
    if (open && props.provider) {
      testScope.value = 'single'
      testPrompt.value = ''
      lastDefaultPrompt = ''
      testMode.value = 'default'
      testType.value = 'text'
      updateDefaultPrompt()
      testProtocol.value = defaultProviderTestProtocol(props.provider, protocolPlan.value)
      resetProviderTestRun(singleRun)
      batch.reset([])
      await loadAvailableModels()
    }
  }
)

// 提示词未被手动修改时，跟随测试类型切换默认值。
function updateDefaultPrompt() {
  const nextDefaultPrompt = testType.value === 'image'
    ? t('admin.providers.imagePromptDefault')
    : t('admin.providers.textPromptDefault')
  if (!testPrompt.value.trim() || testPrompt.value === lastDefaultPrompt) {
    testPrompt.value = nextDefaultPrompt
    lastDefaultPrompt = nextDefaultPrompt
  }
}
watch([selectedModelId, testType], updateDefaultPrompt)

watch(testType, (nextType) => {
  if (nextType === 'image') {
    testMode.value = 'default'
  }
})

const loadAvailableModels = async () => {
  if (!props.provider) return

  loadingModels.value = true
  // 测试型号由管理员从提供商目录选择，也可直接输入 ID。
  selectedModelId.value = props.provider.platform === 'jev' ? 'jev-latest' : ''
  try {
    const models = await adminAPI.providers.getAvailableModels(props.provider.id)
    availableModels.value = models
  } catch (error) {
    console.error('Failed to load available models:', error)
    availableModels.value = []
    selectedModelId.value = props.provider.platform === 'jev' ? 'jev-latest' : ''
  } finally {
    loadingModels.value = false
    batch.reset(availableModels.value.map((model) => model.id))
  }
}

// buildRequestBody 按当前左侧参数生成单次测试请求，批量测试每个模型共用同一套参数。
const buildRequestBody = (model: string): ProviderTestRequestBody => {
  if (isDecisionProvider.value && decisionPayload.value) {
    return { model_id: model, test_type: 'decision', systemone: decisionPayload.value }
  }
  const body: ProviderTestRequestBody = {
    model_id: model,
    prompt: isCompactTestMode.value ? '' : testPrompt.value.trim(),
    test_type: testType.value
  }
  if (isOpenAIProvider.value) {
    body.mode = testMode.value
  }
  if (requestProtocol.value) {
    body.protocol = requestProtocol.value
  }
  return body
}

const abortAll = () => {
  singleController?.abort()
  singleController = null
  batch.abort()
}

const handleClose = () => {
  abortAll()
  emit('close')
}

const startTest = async () => {
  if (!props.provider || !canTestSingle.value) return
  singleController?.abort()
  singleController = new AbortController()
  await executeProviderTest({
    providerId: props.provider.id,
    providerName: props.provider.name,
    body: buildRequestBody(selectedModelId.value),
    run: singleRun,
    signal: singleController.signal,
    t
  })
}

const startBatch = (targets: string[], retry: boolean) => {
  if (!props.provider || busy.value || (isDecisionProvider.value && !decisionPayload.value)) return
  void batch.start({
    targets: [...targets],
    retry,
    providerId: props.provider.id,
    providerName: props.provider.name,
    buildBody: buildRequestBody
  })
}
onBeforeUnmount(abortAll)
</script>
