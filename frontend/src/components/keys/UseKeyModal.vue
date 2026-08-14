<template>
  <BaseDialog
    :show="show"
    :title="t('keys.useKeyModal.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- 复合 Key 直接展示所有前缀及可复制调用示例。 -->
      <div v-if="compositeExamples.length" class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ t('keys.useKeyModal.compositeDescription') }}
        </p>
        <div
          v-for="(item, index) in compositeExamples"
          :key="`${item.groupId}-${item.prefix}`"
          class="flex min-w-0 items-center justify-between gap-3 rounded-md border border-gray-200 px-3 py-2.5 dark:border-dark-600"
        >
          <div class="min-w-0">
            <div class="flex min-w-0 items-center gap-2">
              <span class="shrink-0 rounded bg-primary-50 px-1.5 py-0.5 font-mono text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ item.prefix }}</span>
              <span class="truncate text-sm text-gray-600 dark:text-dark-300">{{ item.groupName }}</span>
            </div>
            <code class="mt-1 block truncate text-xs text-gray-500 dark:text-dark-400">{{ item.example }}</code>
          </div>
          <button type="button" class="btn btn-secondary shrink-0 px-2.5" @click="copyContent(item.example, 1000 + index)">
            <Icon :name="copiedIndex === 1000 + index ? 'check' : 'clipboard'" size="sm" />
            <span class="ml-1.5">{{ copiedIndex === 1000 + index ? t('keys.useKeyModal.copied') : t('keys.useKeyModal.copy') }}</span>
          </button>
        </div>
      </div>

      <!-- No Group Assigned Warning -->
      <div v-else-if="!platform" class="flex items-start gap-3 p-4 rounded-lg bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800">
        <svg class="w-5 h-5 text-yellow-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
        </svg>
        <div>
          <p class="text-sm font-medium text-yellow-800 dark:text-yellow-200">
            {{ t('keys.useKeyModal.noGroupTitle') }}
          </p>
          <p class="text-sm text-yellow-700 dark:text-yellow-300 mt-1">
            {{ t('keys.useKeyModal.noGroupDescription') }}
          </p>
        </div>
      </div>

      <!-- Platform-specific content -->
      <template v-else>
        <!-- Description -->
        <p v-if="clientTabs.length" class="text-sm text-gray-600 dark:text-gray-400">
          {{ platformDescription }}
        </p>
        <div
          v-else
          data-testid="no-text-protocols"
          class="flex items-start gap-3 rounded-md border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800"
        >
          <Icon name="ban" size="md" class="mt-0.5 flex-shrink-0 text-gray-500 dark:text-gray-400" />
          <div>
            <p class="text-sm font-medium text-gray-800 dark:text-gray-200">
              {{ t('keys.useKeyModal.noTextProtocolsTitle') }}
            </p>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">
              {{ t('keys.useKeyModal.noTextProtocolsDescription') }}
            </p>
          </div>
        </div>

        <!-- Client Tabs -->
        <div v-if="clientTabs.length" class="overflow-x-auto border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex min-w-max gap-4 sm:gap-6" aria-label="Client">
            <button
              v-for="tab in clientTabs"
              :key="tab.id"
              type="button"
              @click="activeClientTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeClientTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- Codex 认证模式 -->
        <div
          v-if="showCodexAuthMode"
          class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"
        >
          <div class="mb-2">
            <p class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('keys.useKeyModal.openai.authModeTitle') }}
            </p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('keys.useKeyModal.openai.authModeDescription') }}
            </p>
          </div>
          <div
            class="grid grid-cols-2 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-700"
            role="radiogroup"
            :aria-label="t('keys.useKeyModal.openai.authModeTitle')"
          >
            <button
              type="button"
              role="radio"
              data-testid="codex-auth-mode-legacy"
              :aria-checked="codexAuthMode === 'legacy'"
              :class="[
                'rounded-md px-3 py-2 text-sm font-medium transition-colors',
                codexAuthMode === 'legacy'
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              ]"
              @click="codexAuthMode = 'legacy'"
            >
              {{ t('keys.useKeyModal.openai.authModeLegacy') }}
            </button>
            <button
              type="button"
              role="radio"
              data-testid="codex-auth-mode-api-key"
              :aria-checked="codexAuthMode === 'api-key'"
              :class="[
                'rounded-md px-3 py-2 text-sm font-medium transition-colors',
                codexAuthMode === 'api-key'
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              ]"
              @click="codexAuthMode = 'api-key'"
            >
              {{ t('keys.useKeyModal.openai.authModeApiKey') }}
            </button>
          </div>
          <div
            v-if="codexAuthMode === 'api-key'"
            data-testid="codex-api-key-restart-notice"
            class="mt-3 flex items-start gap-2 border-l-2 border-amber-400 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:border-amber-500 dark:bg-amber-950/30 dark:text-amber-200"
          >
            <Icon name="exclamationCircle" size="sm" class="mt-0.5 flex-shrink-0" />
            <p>{{ t('keys.useKeyModal.openai.authModeApiKeyRestartNotice') }}</p>
          </div>
        </div>

        <!-- Codex WebSocket 连接模式 -->
        <div
          v-if="showCodexWebSocketMode"
          class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"
        >
          <div class="mb-2">
            <p class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('keys.useKeyModal.openai.websocketTitle') }}
            </p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('keys.useKeyModal.openai.websocketDescription') }}
            </p>
          </div>
          <div
            class="grid grid-cols-2 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-700"
            role="radiogroup"
            :aria-label="t('keys.useKeyModal.openai.websocketTitle')"
          >
            <button
              type="button"
              role="radio"
              data-testid="codex-websocket-disabled"
              :aria-checked="!codexWebSocketEnabled"
              :class="[
                'rounded-md px-3 py-2 text-sm font-medium transition-colors',
                !codexWebSocketEnabled
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              ]"
              @click="codexWebSocketEnabled = false"
            >
              {{ t('keys.useKeyModal.openai.websocketDisabled') }}
            </button>
            <button
              type="button"
              role="radio"
              data-testid="codex-websocket-enabled"
              :aria-checked="codexWebSocketEnabled"
              :class="[
                'rounded-md px-3 py-2 text-sm font-medium transition-colors',
                codexWebSocketEnabled
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              ]"
              @click="codexWebSocketEnabled = true"
            >
              {{ t('keys.useKeyModal.openai.websocketEnabled') }}
            </button>
          </div>
        </div>

        <!-- OS/Shell Tabs -->
        <div v-if="showShellTabs" class="overflow-x-auto border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex min-w-max gap-4" aria-label="Tabs">
            <button
              v-for="tab in currentTabs"
              :key="tab.id"
              type="button"
              @click="activeTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- Code Blocks (Stacked for multi-file platforms) -->
        <div v-if="clientTabs.length" class="space-y-4">
          <div
            v-for="(file, index) in currentFiles"
            :key="index"
            class="relative"
          >
            <!-- File Hint (if exists) -->
            <p v-if="file.hint" class="text-xs text-amber-600 dark:text-amber-400 mb-1.5 flex items-center gap-1">
              <Icon name="exclamationCircle" size="sm" class="flex-shrink-0" />
              {{ file.hint }}
            </p>
            <div class="bg-gray-900 dark:bg-dark-900 rounded-xl overflow-hidden">
              <!-- Code Header -->
              <div class="flex items-center justify-between px-4 py-2 bg-gray-800 dark:bg-dark-800 border-b border-gray-700 dark:border-dark-700">
                <span class="min-w-0 truncate text-xs text-gray-400 font-mono">{{ file.path }}</span>
                <button
                  type="button"
                  @click="copyContent(file.content, index)"
                  class="flex flex-shrink-0 items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors"
                  :class="copiedIndex === index
                    ? 'bg-green-500/20 text-green-400'
                    : 'bg-gray-700 hover:bg-gray-600 text-gray-300 hover:text-white'"
                >
                  <svg v-if="copiedIndex === index" class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
                  </svg>
                  {{ copiedIndex === index ? t('keys.useKeyModal.copied') : t('keys.useKeyModal.copy') }}
                </button>
              </div>
              <!-- Code Content -->
              <pre class="p-4 text-sm font-mono text-gray-100 overflow-x-auto"><code v-if="file.highlighted" v-html="file.highlighted"></code><code v-else v-text="file.content"></code></pre>
            </div>
          </div>
        </div>

        <!-- Usage Note -->
        <div v-if="showPlatformNote" class="flex items-start gap-3 p-3 rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-100 dark:border-blue-800">
          <Icon name="infoCircle" size="md" class="text-blue-500 flex-shrink-0 mt-0.5" />
          <p class="text-sm text-blue-700 dark:text-blue-300">
            {{ platformNote }}
          </p>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button
          @click="emit('close')"
          class="btn btn-secondary"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, h, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { OPENAI_CODEX_DEFAULT_MODEL } from '@/constants/openai'
import type { ApiKeyCompositeGroup, GroupClientProtocol, GroupPlatform } from '@/types'
import {
  effectiveGroupClientProtocols,
  hasGroupClientProtocol
} from '@/utils/groupClientProtocols'

interface Props {
  show: boolean
  apiKey: string
  baseUrl: string
  platform: GroupPlatform | null
  allowedClientProtocols?: GroupClientProtocol[] | null
  compositeGroups?: ApiKeyCompositeGroup[]
}

interface Emits {
  (e: 'close'): void
}

interface TabConfig {
  id: string
  label: string
  icon: Component
}

interface FileConfig {
  path: string
  content: string
  hint?: string  // Optional hint message for this file
  highlighted?: string
}

// joinConfigPath 根据目标终端选择配置文件路径分隔符。
function joinConfigPath(dir: string, file: string, windows: boolean): string {
  return windows ? `${dir}\\${file}` : `${dir}/${file}`
}

type OpenCodeProfile = GroupPlatform | 'antigravity-claude' | 'antigravity-gemini'

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const { copyToClipboard: clipboardCopy } = useClipboard()

const copiedIndex = ref<number | null>(null)
const activeTab = ref<string>('unix')
const activeClientTab = ref<string>('claude')

// compositeExamples 为每个平台提供一个可直接复制后替换模型 ID 的示例。
const compositeExamples = computed(() => (props.compositeGroups || []).map((binding) => {
  const platform = binding.group?.platform
  const model = platform === 'openai'
    ? 'gpt-5'
    : platform === 'gemini'
      ? 'gemini-2.5-pro'
      : platform === 'grok'
        ? 'grok-4'
        : platform === 'qoder'
          ? 'auto'
          : 'claude-sonnet-4'
  return {
    groupId: binding.group_id,
    groupName: binding.group?.name || `#${binding.group_id}`,
    prefix: binding.prefix,
    example: `${binding.prefix}/${model}`
  }
}))
type CodexAuthMode = 'legacy' | 'api-key'
const codexAuthMode = ref<CodexAuthMode>('legacy')
const codexWebSocketEnabled = ref(false)

const allowedProtocols = computed(() => {
  if (!props.platform) return []
  return effectiveGroupClientProtocols(props.platform, props.allowedClientProtocols)
})

const allowsProtocol = (protocol: GroupClientProtocol) =>
  hasGroupClientProtocol(allowedProtocols.value, protocol)

// 保留各平台原有 OpenCode 体验，同时保证最终选择的传输协议已经显式启用。
const openCodeProtocolPriority: Record<GroupPlatform, readonly GroupClientProtocol[]> = {
  anthropic: ['anthropic_messages', 'openai_responses', 'openai_chat_completions'],
  openai: ['openai_responses', 'openai_chat_completions', 'anthropic_messages'],
  gemini: ['gemini_generate_content', 'openai_responses', 'openai_chat_completions', 'anthropic_messages'],
  antigravity: ['openai_responses', 'openai_chat_completions', 'anthropic_messages', 'gemini_generate_content'],
  qoder: ['openai_chat_completions', 'openai_responses', 'anthropic_messages'],
  grok: ['openai_responses', 'openai_chat_completions', 'anthropic_messages']
}

function preferredOpenCodeProtocol(
  platform: GroupPlatform,
  protocols: readonly GroupClientProtocol[]
): GroupClientProtocol | null {
  return openCodeProtocolPriority[platform].find((protocol) => protocols.includes(protocol)) ?? null
}

// Icon components
const AppleIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z' })
    ])
  }
}

const WindowsIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M3 12V6.75l6-1.32v6.48L3 12zm17-9v8.75l-10 .15V5.21L20 3zM3 13l6 .09v6.81l-6-1.15V13zm7 .25l10 .15V21l-10-1.91v-5.84z' })
    ])
  }
}

// Terminal icon for Claude Code
const TerminalIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'm6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z'
      })
    ])
  }
}

// Sparkle icon for Gemini
const SparkleIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09ZM18.259 8.715 18 9.75l-.259-1.035a3.375 3.375 0 0 0-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 0 0 2.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 0 0 2.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 0 0-2.456 2.456ZM16.894 20.567 16.5 21.75l-.394-1.183a2.25 2.25 0 0 0-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 0 0 1.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 0 0 1.423 1.423l1.183.394-1.183.394a2.25 2.25 0 0 0-1.423 1.423Z'
      })
    ])
  }
}

const clientTabs = computed((): TabConfig[] => {
  if (!props.platform) return []
  const tabs = new Map<string, TabConfig>()
  if (props.platform === 'grok' && allowsProtocol('openai_responses')) {
    tabs.set('grok', { id: 'grok', label: t('keys.useKeyModal.cliTabs.grokCli'), icon: TerminalIcon })
  }
  if (allowsProtocol('anthropic_messages')) {
    tabs.set('claude', { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon })
  }
  if (allowsProtocol('openai_responses')) {
    tabs.set('codex', { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: TerminalIcon })
  }
  if (allowsProtocol('gemini_generate_content')) {
    tabs.set('gemini', { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon })
  }
  if (allowedProtocols.value.length > 0) {
    tabs.set('opencode', { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon })
  }
  return [...tabs.values()]
})

const defaultClientTab = computed(() => {
  const ids = new Set(clientTabs.value.map((tab) => tab.id))
  const preferred = props.platform === 'openai'
    ? ['codex', 'claude', 'opencode']
    : props.platform === 'grok'
      ? ['grok', 'codex', 'claude', 'opencode']
      : props.platform === 'gemini'
        ? ['gemini', 'claude', 'codex', 'opencode']
        : props.platform === 'antigravity'
          ? ['claude', 'gemini', 'codex', 'opencode']
          : ['claude', 'codex', 'gemini', 'opencode']
  return preferred.find((id) => ids.has(id)) || clientTabs.value[0]?.id || ''
})

// 平台或协议集合变化后回到首选的可用客户端，避免保留已禁用标签。
watch(
  [
    () => props.platform,
    () => props.allowedClientProtocols
  ],
  () => {
    activeTab.value = 'unix'
    activeClientTab.value = defaultClientTab.value
    codexAuthMode.value = 'legacy'
    codexWebSocketEnabled.value = false
  },
  { immediate: true, deep: true }
)

// 每次重新打开弹窗都恢复默认配置，避免沿用上一次密钥的临时选择。
watch(() => props.show, (show) => {
  if (show) {
    activeClientTab.value = defaultClientTab.value
    codexAuthMode.value = 'legacy'
    codexWebSocketEnabled.value = false
  }
})

// 客户端变化时统一回到该客户端的首个系统标签。
watch(activeClientTab, () => {
  activeTab.value = 'unix'
})

// Shell tabs (3 types for environment variable based configs)
const shellTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'cmd', label: 'Windows CMD', icon: WindowsIcon },
  { id: 'powershell', label: 'PowerShell', icon: WindowsIcon }
]

function withOpenCodeToolCalling(models: Record<string, any>) {
  return Object.fromEntries(
    Object.entries(models).map(([model, config]) => [
      model,
      {
        ...config,
        tool_call: true
      }
    ])
  )
}

function buildQoderOpenCodeModels() {
  return withOpenCodeToolCalling({
    'claude-opus-4-6': {
      name: 'Claude Opus 4.6',
      limit: {
        context: 200000,
        output: 128000
      }
    },
    auto: {
      name: 'Qoder Auto',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    performance: {
      name: 'Qoder Performance',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    efficient: {
      name: 'Qoder Efficient',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    lite: {
      name: 'Qoder Lite',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'qwen3.7-max': {
      name: 'Qwen3.7-Max',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'qwen3.7-plus': {
      name: 'Qwen3.7-Plus',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'deepseek-v4-pro': {
      name: 'DeepSeek-V4-Pro',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'deepseek-v4-flash': {
      name: 'DeepSeek-V4-Flash',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'glm-5.2': {
      name: 'GLM-5.2',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    // Kimi-K3 使用 Qoder 新增的独立路由，不替换仍可用的 Kimi-K2.7-Code。
    'kimi-k3': {
      name: 'Kimi-K3',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'kimi-k2.7-code': {
      name: 'Kimi-K2.7-Code',
      limit: {
        context: 400000,
        output: 128000
      }
    },
    'minimax-m3': {
      name: 'MiniMax-M3',
      limit: {
        context: 400000,
        output: 128000
      }
    }
  })
}

// OpenAI tabs (2 OS types)
const openaiTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'windows', label: 'Windows', icon: WindowsIcon }
]

const showShellTabs = computed(() =>
  clientTabs.value.length > 0 && activeClientTab.value !== 'opencode'
)

const showCodexAuthMode = computed(() =>
  props.platform === 'openai' &&
  activeClientTab.value === 'codex'
)

const showCodexWebSocketMode = computed(() =>
  props.platform === 'openai' && activeClientTab.value === 'codex'
)

const currentTabs = computed(() => {
  if (!showShellTabs.value) return []
  if (activeClientTab.value === 'codex' || activeClientTab.value === 'grok') {
    return openaiTabs
  }
  return shellTabs
})

const platformDescription = computed(() => {
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.description')
      }
      return t('keys.useKeyModal.openai.description')
    case 'gemini':
      return t('keys.useKeyModal.gemini.description')
    case 'antigravity':
      return t('keys.useKeyModal.antigravity.description')
    case 'grok':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.grok.claudeDescription')
      }
      if (activeClientTab.value === 'codex') {
        return t('keys.useKeyModal.grok.codexDescription')
      }
      return t('keys.useKeyModal.grok.description')
    default:
      return t('keys.useKeyModal.description')
  }
})

const platformNote = computed(() => {
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.note')
      }
      return activeTab.value === 'windows'
        ? t('keys.useKeyModal.openai.noteWindows')
        : t('keys.useKeyModal.openai.note')
    case 'gemini':
      return t('keys.useKeyModal.gemini.note')
    case 'antigravity':
      return activeClientTab.value === 'claude'
        ? t('keys.useKeyModal.antigravity.claudeNote')
        : t('keys.useKeyModal.antigravity.geminiNote')
    case 'grok':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.grok.claudeNote')
      }
      if (activeClientTab.value === 'codex') {
        return activeTab.value === 'windows'
          ? t('keys.useKeyModal.grok.codexNoteWindows')
          : t('keys.useKeyModal.grok.codexNote')
      }
      // Grok CLI 根据 Shell 展示环境变量与 ~/.grok/config.toml 路径说明。
      if (activeClientTab.value === 'grok' && (activeTab.value === 'cmd' || activeTab.value === 'powershell')) {
        return t('keys.useKeyModal.grok.noteWindows')
      }
      if (activeClientTab.value === 'grok' && activeTab.value === 'windows') {
        return t('keys.useKeyModal.grok.noteWindows')
      }
      return t('keys.useKeyModal.grok.note')
    default:
      return t('keys.useKeyModal.note')
  }
})

const showPlatformNote = computed(() =>
  clientTabs.value.length > 0 && activeClientTab.value !== 'opencode'
)

const escapeHtml = (value: string) => value
  .replace(/&/g, '&amp;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;')
  .replace(/'/g, '&#39;')

const wrapToken = (className: string, value: string) =>
  `<span class="${className}">${escapeHtml(value)}</span>`

const keyword = (value: string) => wrapToken('text-emerald-300', value)
const variable = (value: string) => wrapToken('text-sky-200', value)
const operator = (value: string) => wrapToken('text-slate-400', value)
const string = (value: string) => wrapToken('text-amber-200', value)
const comment = (value: string) => wrapToken('text-slate-500', value)

// Syntax highlighting helpers
// Generate file configs based on platform and active tab
const currentFiles = computed((): FileConfig[] => {
  if (!props.platform) return []
  const baseUrl = props.baseUrl || window.location.origin
  const apiKey = props.apiKey
  const baseRoot = baseUrl.replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  const ensureV1 = (value: string) => {
    const trimmed = value.replace(/\/+$/, '')
    return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`
  }
  const apiBase = ensureV1(baseRoot)
  const antigravityBase = ensureV1(`${baseRoot}/antigravity`)
  const antigravityGeminiBase = (() => {
    const trimmed = `${baseRoot}/antigravity`.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()
  const geminiBase = (() => {
    const trimmed = baseRoot.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()

  if (activeClientTab.value === 'opencode') {
    if (props.platform === 'antigravity') {
      const nativeConfigs: FileConfig[] = []
      if (allowsProtocol('anthropic_messages')) {
        nativeConfigs.push(generateOpenCodeConfig(
          'antigravity-claude',
          'anthropic_messages',
          antigravityBase,
          apiKey,
          'opencode.json (Claude)'
        ))
      }
      if (allowsProtocol('gemini_generate_content')) {
        nativeConfigs.push(generateOpenCodeConfig(
          'antigravity-gemini',
          'gemini_generate_content',
          antigravityGeminiBase,
          apiKey,
          'opencode.json (Gemini)'
        ))
      }
      if (nativeConfigs.length > 0) return nativeConfigs

      // 原生协议全部关闭时，两类模型都通过同一个已启用的 OpenAI 兼容入口访问。
      const protocol = preferredOpenCodeProtocol(props.platform, allowedProtocols.value)
      if (!protocol) return []
      return [
        generateOpenCodeConfig('antigravity-claude', protocol, apiBase, apiKey, 'opencode.json (Claude)'),
        generateOpenCodeConfig('antigravity-gemini', protocol, apiBase, apiKey, 'opencode.json (Gemini)')
      ]
    }

    const protocol = preferredOpenCodeProtocol(props.platform, allowedProtocols.value)
    if (!protocol) return []
    const providerBase = protocol === 'gemini_generate_content' ? geminiBase : apiBase
    return [generateOpenCodeConfig(props.platform, protocol, providerBase, apiKey)]
  }

  if (activeClientTab.value === 'claude') {
    if (props.platform === 'grok') {
      return generateGrokClaudeFiles(baseRoot, apiKey)
    }
    return props.platform === 'antigravity'
      ? generateAnthropicFiles(`${baseRoot}/antigravity`, apiKey)
      : generateAnthropicFiles(baseRoot, apiKey)
  }

  if (activeClientTab.value === 'codex') {
    if (props.platform === 'openai') {
      return generateOpenAIFiles(baseUrl, apiKey)
    }
    if (props.platform === 'grok') {
      return generateGrokCodexFiles(apiBase, apiKey)
    }
    return generateCompatibleCodexFiles(apiBase, apiKey, props.platform)
  }

  if (activeClientTab.value === 'gemini') {
    return props.platform === 'antigravity'
      ? [generateGeminiCliContent(`${baseRoot}/antigravity`, apiKey)]
      : [generateGeminiCliContent(baseRoot, apiKey)]
  }

  if (activeClientTab.value === 'grok') {
    return generateGrokFiles(apiBase, apiKey)
  }

  return []
})

function generateAnthropicFiles(baseUrl: string, apiKey: string): FileConfig[] {
  let path: string
  let content: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="${apiKey}"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export CLAUDE_CODE_ATTRIBUTION_HEADER=0`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set ANTHROPIC_BASE_URL=${baseUrl}
set ANTHROPIC_AUTH_TOKEN=${apiKey}
set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
set CLAUDE_CODE_ATTRIBUTION_HEADER=0`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:ANTHROPIC_BASE_URL="${baseUrl}"
$env:ANTHROPIC_AUTH_TOKEN="${apiKey}"
$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
$env:CLAUDE_CODE_ATTRIBUTION_HEADER=0`
      break
    default:
      path = 'Terminal'
      content = ''
  }

  const vscodeSettingsPath = activeTab.value === 'unix'
    ? '~/.claude/settings.json'
    : '%USERPROFILE%\\.claude\\settings.json'

  const vscodeContent = `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "env": {
    "ANTHROPIC_BASE_URL": "${baseUrl}",
    "ANTHROPIC_AUTH_TOKEN": "${apiKey}",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0"
  }
}`

  return [
    { path, content },
    {
      path: vscodeSettingsPath,
      content: vscodeContent,
      hint: t('keys.useKeyModal.claudeSettingsHint')
    }
  ]
}

function generateGrokClaudeFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const environment = {
    ANTHROPIC_BASE_URL: baseUrl,
    ANTHROPIC_AUTH_TOKEN: apiKey,
    ANTHROPIC_MODEL: 'grok-4.5',
    ANTHROPIC_DEFAULT_OPUS_MODEL: 'grok-4.5',
    ANTHROPIC_DEFAULT_SONNET_MODEL: 'grok-4.5',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: 'grok-4.5',
    ANTHROPIC_DEFAULT_FABLE_MODEL: 'grok-4.5',
    CLAUDE_CODE_SUBAGENT_MODEL: 'grok-4.5',
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
    CLAUDE_CODE_ATTRIBUTION_HEADER: '0'
  }
  let path: string
  let content: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = Object.entries(environment)
        .map(([name, value]) => `export ${name}="${value}"`)
        .join('\n')
      break
    case 'cmd':
      path = 'Command Prompt'
      content = Object.entries(environment)
        .map(([name, value]) => `set ${name}=${value}`)
        .join('\n')
      break
    case 'powershell':
      path = 'PowerShell'
      content = Object.entries(environment)
        .map(([name, value]) => `$env:${name}="${value}"`)
        .join('\n')
      break
    default:
      path = 'Terminal'
      content = ''
  }

  const settingsPath = activeTab.value === 'unix'
    ? '~/.claude/settings.json'
    : '%USERPROFILE%\\.claude\\settings.json'

  return [
    { path, content },
    {
      path: settingsPath,
      content: JSON.stringify({
        $schema: 'https://json.schemastore.org/claude-code-settings.json',
        env: environment
      }, null, 2),
      hint: t('keys.useKeyModal.claudeSettingsHint')
    }
  ]
}

function generateGeminiCliContent(baseUrl: string, apiKey: string): FileConfig {
  const model = 'gemini-2.0-flash'
  const modelComment = t('keys.useKeyModal.gemini.modelComment')
  let path: string
  let content: string
  let highlighted: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export GOOGLE_GEMINI_BASE_URL="${baseUrl}"
export GEMINI_API_KEY="${apiKey}"
export GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('export')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('export')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('export')} ${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set GOOGLE_GEMINI_BASE_URL=${baseUrl}
set GEMINI_API_KEY=${apiKey}
set GEMINI_MODEL=${model}`
      highlighted = `${keyword('set')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(baseUrl)}
${keyword('set')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(apiKey)}
${keyword('set')} ${variable('GEMINI_MODEL')}${operator('=')}${string(model)}
${comment(`REM ${modelComment}`)}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:GOOGLE_GEMINI_BASE_URL="${baseUrl}"
$env:GEMINI_API_KEY="${apiKey}"
$env:GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('$env:')}${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('$env:')}${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('$env:')}${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    default:
      path = 'Terminal'
      content = ''
      highlighted = ''
  }

  return { path, content, highlighted }
}

function generateOpenAIFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'
  const websocketProviderConfig = codexWebSocketEnabled.value
    ? '\nsupports_websockets = true'
    : ''
  const websocketFeatureConfig = codexWebSocketEnabled.value
    ? 'responses_websockets_v2 = true\n'
    : ''

  // config.toml content
  const configContent = `model_provider = "OpenAI"
model = "${OPENAI_CODEX_DEFAULT_MODEL}"
review_model = "${OPENAI_CODEX_DEFAULT_MODEL}"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.OpenAI]
name = "OpenAI"
base_url = "${baseUrl}"
wire_api = "responses"${websocketProviderConfig}
${generateCodexProviderAuthConfig()}

[features]
${websocketFeatureConfig}goals = true`

  // auth.json content
  const authContent = `{
  "OPENAI_API_KEY": "${apiKey}"
}`

  return [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hint: t('keys.useKeyModal.openai.configTomlHint')
    },
    {
      path: `${configDir}/auth.json`,
      content: authContent
    }
  ]
}

// 兼容 Responses 的非 OpenAI 平台使用独立 provider，避免触发 OpenAI 官方登录流程。
function generateCompatibleCodexFiles(
  baseUrl: string,
  apiKey: string,
  platform: GroupPlatform
): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'
  const platformConfig: Record<GroupPlatform, {
    provider: string
    name: string
    model: string
    contextWindow: number
  }> = {
    anthropic: {
      provider: 'tokenrouter_anthropic',
      name: 'TokenRouter Anthropic',
      model: 'claude-sonnet-4-6',
      contextWindow: 200000
    },
    openai: {
      provider: 'tokenrouter_openai',
      name: 'TokenRouter OpenAI',
      model: OPENAI_CODEX_DEFAULT_MODEL,
      contextWindow: 1050000
    },
    gemini: {
      provider: 'tokenrouter_gemini',
      name: 'TokenRouter Gemini',
      model: 'gemini-2.5-pro',
      contextWindow: 2097152
    },
    antigravity: {
      provider: 'tokenrouter_antigravity',
      name: 'TokenRouter Antigravity',
      model: 'claude-sonnet-4-6',
      contextWindow: 200000
    },
    qoder: {
      provider: 'tokenrouter_qoder',
      name: 'TokenRouter Qoder',
      model: 'auto',
      contextWindow: 400000
    },
    grok: {
      provider: 'tokenrouter_grok',
      name: 'TokenRouter Grok',
      model: 'grok-4.5',
      contextWindow: 1000000
    }
  }
  const config = platformConfig[platform]
  const configContent = `model_provider = "${config.provider}"
model = "${config.model}"
model_context_window = ${config.contextWindow}
disable_response_storage = true

[model_providers.${config.provider}]
name = "${config.name}"
base_url = "${baseUrl}"
env_key = "TOKENROUTER_API_KEY"
wire_api = "responses"
requires_openai_auth = false

[features]
goals = true`
  const environmentContent = isWindows
    ? `$env:TOKENROUTER_API_KEY="${apiKey}"`
    : `export TOKENROUTER_API_KEY="${apiKey}"`

  return [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hint: t('keys.useKeyModal.openai.configTomlHint')
    },
    {
      path: isWindows ? 'PowerShell' : 'Terminal',
      content: environmentContent
    }
  ]
}

// generateCodexProviderAuthConfig 根据用户选择生成最小的 Codex provider 授权配置。
function generateCodexProviderAuthConfig(): string {
  if (codexAuthMode.value === 'api-key') {
    return `requires_openai_auth = false
http_headers = { "x-openai-actor-authorization" = "local-image-extension" }`
  }

  return 'requires_openai_auth = true'
}

// 生成 Grok Build CLI 的完整端点、认证和模型配置。
function generateGrokFiles(baseUrl: string, apiKey: string): FileConfig[] {
  // 显示 Shell 标签页时优先选择 unix、cmd 或 powershell，否则回退到 windows 标签页。
  const shell = activeTab.value
  const isWindowsPath = shell === 'windows' || shell === 'cmd' || shell === 'powershell'
  const configDir = isWindowsPath ? '%userprofile%\\.grok' : '~/.grok'

  let envPath: string
  let envContent: string
  switch (shell) {
    case 'cmd':
      envPath = 'Command Prompt'
      envContent = `set GROK_MODELS_BASE_URL=${baseUrl}
set XAI_API_KEY=${apiKey}`
      break
    case 'powershell':
    case 'windows':
      envPath = 'PowerShell'
      envContent = `$env:GROK_MODELS_BASE_URL="${baseUrl}"
$env:XAI_API_KEY="${apiKey}"`
      break
    default:
      envPath = 'Terminal'
      envContent = `export GROK_MODELS_BASE_URL="${baseUrl}"
export XAI_API_KEY="${apiKey}"`
  }

  // 配置结构遵循 Grok Build 用户指南中的 ~/.grok/docs 与 custom-models，并兼容生产环境设置。
  // 此处只配置 Responses 文本模型；图片和视频使用媒体端点或功能覆盖中的 Imagine 模型 ID。
  // 凭证优先级依次为 api_key 字段、env_key、已登录会话和全局 XAI_API_KEY 回退值。
  const modelsListUrl = `${baseUrl.replace(/\/+$/, '')}/models`
  const configContent = `# Grok Build CLI → Sub2API Grok group (API key auth).
# Docs: ~/.grok/docs/user-guide/05-configuration.md + 11-custom-models.md
# Verify after save: grok inspect
#
# IMPORTANT: api_backend must be "responses" for Sub2API Grok (POST /v1/responses).
# If omitted, Grok Build defaults to chat_completions (/v1/chat/completions).
# Keep api_backend = "responses" on every model entry.
#
# Prefer env_key over hardcoding api_key (never commit secrets).
# Also export GROK_MODELS_BASE_URL + XAI_API_KEY in the shell block above.

# Global inference / catalog endpoints (same role as env GROK_MODELS_BASE_URL).
# When models_base_url is set, Grok uses API-key Bearer auth (no grok login required).
[endpoints]
models_base_url = "${baseUrl}"              # inference base; model list defaults to {base}/models
models_list_url = "${modelsListUrl}"        # optional override (env: GROK_MODELS_LIST_URL)
xai_api_base_url = "${baseUrl}"             # public xAI API base override for gateway routing
cli_chat_proxy_base_url = "${baseUrl}"      # CLI chat-proxy base (env: GROK_CLI_CHAT_PROXY_BASE_URL)

# Prefer API key when using a custom gateway (matches Sub2API).
# Requires XAI_API_KEY env or per-model env_key / api_key.
[auth]
preferred_method = "api_key"

[model."grok-4.5"]
model = "grok-4.5"                          # id sent to the API
name = "Grok 4.5"                           # shown in /model picker
description = "Grok 4.5 via Sub2API (Responses)"
# base_url inherits from [endpoints].models_base_url; override only if needed:
# base_url = "${baseUrl}"
env_key = "XAI_API_KEY"                     # or: api_key = "${apiKey}"  (not recommended)
api_backend = "responses"                   # chat_completions | responses | messages
context_window = 500000                     # drives auto-compaction timing
# Optional sampling (global defaults can live under [models] instead):
# temperature = 0.7
# top_p = 0.95
# max_completion_tokens = 8192
# Server-side (backend) web_search tools — only if your gateway exposes them:
supports_backend_search = true

[model."grok-build-0.1"]
model = "grok-build-0.1"
name = "Grok Build"
description = "Coding / agent sessions (xAI recommends grok-build* for coding)"
env_key = "XAI_API_KEY"
api_backend = "responses"
context_window = 256000
supports_backend_search = true

# Text multi-agent / client web_search sub-agent (NOT Imagine image/video).
[model."grok-4.20-multi-agent-0309"]
model = "grok-4.20-multi-agent-0309"
name = "Grok 4.20 Multi Agent (text / web_search)"
description = "Text multi-agent; use for web_search sub-agent, not image/video"
env_key = "XAI_API_KEY"
api_backend = "responses"
context_window = 1000000
supports_backend_search = true

[model."grok-4.3"]
model = "grok-4.3"
name = "Grok 4.3"
env_key = "XAI_API_KEY"
api_backend = "responses"
context_window = 1000000
supports_backend_search = true

# Optional short alias for /model grok:
# [model."grok"]
# model = "grok-4.5"
# name = "Grok"
# env_key = "XAI_API_KEY"
# api_backend = "responses"
# context_window = 1000000
# supports_backend_search = true

[models]
# xAI recommends grok-build* for coding/agent sessions; use grok-4.5 for general chat.
default = "grok-4.5"
web_search = "grok-4.5"                     # client-side web_search tool model (must exist as [model.*])
image_description = "grok-4.5"              # vision/describe-image helper model
# Optional environment-wide sampling defaults (per-model values win):
# temperature = 0.7
# top_p = 0.95
# max_completion_tokens = 8192
# max_retries = 8

[session]
auto_compact_threshold_percent = 80         # auto-compact at this % of context_window (default 85)

# Imagine tools: model IDs go to Sub2API media endpoints (not the text [model.*] catalog).
# Enable only if the Grok group allows image/video generation.
[features]
image_gen = true
video_gen = true
image_gen_model_override = "grok-imagine-image-quality"   # or grok-imagine-image
image_edit_model_override = "grok-imagine-edit"
# Optional feature flags (defaults shown in docs):
# telemetry = false
# remote_fetch = true                         # set false for air-gapped / pure-gateway catalogs
# lsp_tools = false`

  return [
    { path: envPath, content: envContent },
    {
      path: joinConfigPath(configDir, 'config.toml', isWindowsPath),
      content: configContent,
      hint: t('keys.useKeyModal.grok.configTomlHint')
    }
  ]
}

function generateGrokCodexFiles(baseUrl: string, apiKey: string): FileConfig[] {
	const shell = activeTab.value
	const isWindowsPath = shell === 'windows' || shell === 'cmd' || shell === 'powershell'
	const configDir = isWindowsPath ? '%USERPROFILE%\\.codex' : '~/.codex'

	let envPath: string
	let envContent: string
	switch (shell) {
		case 'cmd':
			envPath = 'Command Prompt'
			envContent = `set TOKENROUTER_API_KEY=${apiKey}`
			break
		case 'powershell':
		case 'windows':
			envPath = 'PowerShell'
			envContent = `$env:TOKENROUTER_API_KEY="${apiKey}"`
			break
		default:
			envPath = 'Terminal'
			envContent = `export TOKENROUTER_API_KEY="${apiKey}"`
	}

	const configContent = `model_provider = "tokenrouter_grok"
model = "grok-4.5"
review_model = "grok-4.5"
model_reasoning_effort = "xhigh"
model_context_window = 1000000

[model_providers.tokenrouter_grok]
name = "TokenRouter Grok"
base_url = "${baseUrl}"
env_key = "TOKENROUTER_API_KEY"
wire_api = "responses"
# API-key providers: do not require ChatGPT OAuth login
requires_openai_auth = false
# Grok/Sub2API path is HTTP/SSE; disable WS (Codex may otherwise try WebSocket first)
supports_websockets = false

[features]
responses_websockets_v2 = false`

	return [
		{ path: envPath, content: envContent },
    {
      path: joinConfigPath(configDir, 'config.toml', isWindowsPath),
      content: configContent,
      hint: t('keys.useKeyModal.grok.codexConfigTomlHint')
    }
  ]
}

function generateOpenCodeConfig(
  profile: OpenCodeProfile,
  protocol: GroupClientProtocol,
  baseUrl: string,
  apiKey: string,
  pathLabel?: string
): FileConfig {
  const provider: Record<string, any> = {
    [profile]: {
      options: {
        baseURL: baseUrl,
        apiKey
      }
    }
  }
  const openaiModels = {
    'gpt-5.2': {
      name: 'GPT-5.2',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.6': {
      name: 'GPT-5.6 (Sol)',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {},
        max: {}
      }
    },
    'gpt-5.6-sol': {
      name: 'GPT-5.6 Sol',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {},
        max: {}
      }
    },
    'gpt-5.6-terra': {
      name: 'GPT-5.6 Terra',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {},
        max: {}
      }
    },
    'gpt-5.6-luna': {
      name: 'GPT-5.6 Luna',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {},
        max: {}
      }
    },
    'gpt-5.5': {
      name: 'GPT-5.5',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.5-pro': {
      name: 'GPT-5.5 Pro',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.4': {
      name: 'GPT-5.4',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.4-mini': {
      name: 'GPT-5.4 Mini',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.3-codex-spark': {
      name: 'GPT-5.3 Codex Spark',
      limit: {
        context: 128000,
        output: 32000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'codex-mini-latest': {
      name: 'Codex Mini',
      limit: {
        context: 200000,
        output: 100000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {}
      }
    }
  }
  const geminiModels = {
    'gemini-2.0-flash': {
      name: 'Gemini 2.0 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-pro': {
      name: 'Gemini 2.5 Pro',
      limit: {
        context: 2097152,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.5-flash': {
      name: 'Gemini 3.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-flash-preview': {
      name: 'Gemini 3 Flash Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-pro-preview': {
      name: 'Gemini 3 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-preview': {
      name: 'Gemini 3.1 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }

  const antigravityGeminiModels = {
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'disable'
        }
      }
    },
    'gemini-2.5-flash-lite': {
      name: 'Gemini 2.5 Flash Lite',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-thinking': {
      name: 'Gemini 2.5 Flash (Thinking)',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3-flash': {
      name: 'Gemini 3 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-low': {
      name: 'Gemini 3.1 Pro Low',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-high': {
      name: 'Gemini 3.1 Pro High',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-image': {
      name: 'Gemini 2.5 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-flash-image': {
      name: 'Gemini 3.1 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }
  const claudeModels = {
    'claude-fable-5': {
      name: 'Claude Fable 5',
      limit: {
        context: 1048576,
        output: 128000
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          type: 'adaptive'
        }
      }
    },
    'claude-opus-4-6-thinking': {
      name: 'Claude 4.6 Opus (Thinking)',
      limit: {
        context: 200000,
        output: 128000
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'claude-sonnet-4-6': {
      name: 'Claude 4.6 Sonnet',
      limit: {
        context: 200000,
        output: 64000
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }
  // 已知模型的 context_window 与 Grok Build 官方示例 docs.x.ai/build/settings 保持一致。
  // 图片和视频模型只用于媒体端点，不列入文本模型清单。
  const grokModels = {
    'grok-4.5': {
      name: 'Grok 4.5',
      limit: { context: 500000, output: 64000 }
    },
    'grok-build-0.1': {
      name: 'Grok Build 0.1',
      limit: { context: 256000, output: 64000 }
    },
    'grok-4.20-multi-agent-0309': {
      name: 'Grok 4.20 Multi Agent (text / web_search)',
      limit: { context: 1000000, output: 64000 }
    },
    'grok-4.3': {
      name: 'Grok 4.3',
      limit: { context: 1000000, output: 64000 }
    },
    'grok-composer-2.5-fast': {
      name: 'Grok Composer 2.5 Fast',
      limit: { context: 500000, output: 64000 }
    }
  }

  // 协议决定实际 wire provider，平台 profile 只负责展示模型与平台元数据。
  switch (protocol) {
    case 'anthropic_messages':
      provider[profile].npm = '@ai-sdk/anthropic'
      break
    case 'openai_responses':
      provider[profile].npm = '@ai-sdk/openai'
      break
    case 'openai_chat_completions':
      provider[profile].npm = '@ai-sdk/openai-compatible'
      break
    case 'gemini_generate_content':
      provider[profile].npm = '@ai-sdk/google'
      break
  }

  if (profile === 'gemini') {
    provider[profile].models = withOpenCodeToolCalling(geminiModels)
  } else if (profile === 'qoder') {
    provider[profile].name = 'Qoder'
    provider[profile].models = buildQoderOpenCodeModels()
  } else if (profile === 'antigravity-claude') {
    provider[profile].name = 'Antigravity (Claude)'
    provider[profile].models = withOpenCodeToolCalling(claudeModels)
  } else if (profile === 'antigravity-gemini') {
    provider[profile].name = 'Antigravity (Gemini)'
    provider[profile].models = withOpenCodeToolCalling(antigravityGeminiModels)
  } else if (profile === 'openai') {
    provider[profile].models = withOpenCodeToolCalling(openaiModels)
  } else if (profile === 'grok') {
    provider[profile].name = 'Grok'
    provider[profile].models = withOpenCodeToolCalling(grokModels)
  }

  const agent =
    profile === 'openai' && protocol === 'openai_responses'
      ? {
          build: {
            options: {
              store: false
            }
          },
          plan: {
            options: {
              store: false
            }
          }
        }
      : undefined

  const content = JSON.stringify(
    {
      provider,
      ...(agent ? { agent } : {}),
      $schema: 'https://opencode.ai/config.json'
    },
    null,
    2
  )

  return {
    path: pathLabel ?? 'opencode.json',
    content,
    hint: t('keys.useKeyModal.opencode.hint')
  }
}

const copyContent = async (content: string, index: number) => {
  const success = await clipboardCopy(content, t('keys.copied'))
  if (success) {
    copiedIndex.value = index
    setTimeout(() => {
      copiedIndex.value = null
    }, 2000)
  }
}
</script>
