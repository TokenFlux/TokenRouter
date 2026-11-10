<template>
  <SettingsCard
    v-show="activeGatewaySection === 'anthropic'"
    v-content-reveal="activeGatewaySection === 'anthropic'"
    data-testid="gateway-card-web-search-emulation"
    :title="t('admin.settings.webSearchEmulation.title')"
    :description="t('admin.settings.webSearchEmulation.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="web-search-emulation-enabled"
        v-model="webSearchConfig.enabled"
        :label="t('admin.settings.webSearchEmulation.enabled')"
        :hint="t('admin.settings.webSearchEmulation.enabledHint')"
      />
      <Collapse :open="webSearchConfig.enabled" unmount-on-hide>
        <RuleListEditor
          :items="webSearchConfig.providers"
          variant="card"
          :title="t('admin.settings.webSearchEmulation.providers')"
          :add-label="t('admin.settings.webSearchEmulation.addProvider')"
          :remove-label="t('admin.settings.webSearchEmulation.removeProvider')"
          :empty-text="t('admin.settings.webSearchEmulation.noProviders')"
          test-id="web-search-providers"
          @add="addWebSearchProvider"
          @remove="removeWebSearchProvider"
        >
          <template #row="{ item: provider, index: pIdx }">
            <!-- The title button toggles the details for this provider row. -->
            <div
              data-icon-trigger
              class="flex cursor-pointer flex-wrap items-center gap-3"
              @click="toggleProviderExpand(pIdx)"
            >
              <Icon
                name="chevronRight"
                size="sm"
                :animate-on-hover="false"
                class="h-4 w-4 text-gray-400 transition-transform"
                :class="{ 'rotate-90': expandedProviders[pIdx] }"
              />
              <Select
                v-model="provider.type"
                :options="[
                  { value: 'brave', label: 'Brave Search' },
                  { value: 'tavily', label: 'Tavily' },
                ]"
                :aria-label="t('admin.settings.webSearchEmulation.providers')"
                class="w-36"
                @click.stop
              />
              <span class="text-xs text-gray-500 dark:text-dark-400">
                {{ provider.quota_used ?? 0 }} /
                {{
                  provider.quota_limit != null && provider.quota_limit > 0
                    ? provider.quota_limit
                    : '∞'
                }}
              </span>
              <span
                v-if="!expandedProviders[pIdx] && provider.api_key_configured"
                class="text-xs text-green-600 dark:text-green-400"
              >
                {{ t('admin.settings.webSearchEmulation.apiKeyConfigured') }}
              </span>
            </div>
            <Collapse :open="expandedProviders[pIdx]" unmount-on-hide>
              <div class="mt-4 space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600">
                <div>
                  <label :for="`web-search-api-key-${pIdx}`" class="input-label">
                    {{ t('admin.settings.webSearchEmulation.apiKey') }}
                  </label>
                  <div class="relative">
                    <input
                      :id="`web-search-api-key-${pIdx}`"
                      v-model="provider.api_key"
                      :type="apiKeyVisible[pIdx] ? 'text' : 'password'"
                      class="input"
                      :class="provider.api_key || provider.api_key_configured ? 'pr-16' : ''"
                      :placeholder="
                        provider.api_key_configured
                          ? '••••••••'
                          : t('admin.settings.webSearchEmulation.apiKeyPlaceholder')
                      "
                    />
                    <div
                      v-if="provider.api_key || provider.api_key_configured"
                      class="absolute inset-y-0 right-0 flex items-center pr-1.5"
                    >
                      <button
                        type="button"
                        class="rounded-compact p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                        :title="
                          apiKeyVisible[pIdx]
                            ? t('admin.settings.webSearchEmulation.hideApiKey')
                            : t('admin.settings.webSearchEmulation.showApiKey')
                        "
                        @click="apiKeyVisible[pIdx] = !apiKeyVisible[pIdx]"
                      >
                        <Icon v-if="!apiKeyVisible[pIdx]" name="eye" size="sm" class="h-4 w-4" />
                        <Icon v-else name="eyeOff" size="sm" class="h-4 w-4" />
                      </button>
                      <button
                        type="button"
                        class="rounded-compact p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                        :class="{ 'cursor-not-allowed opacity-30': !provider.api_key }"
                        :title="t('admin.settings.webSearchEmulation.copyApiKey')"
                        :disabled="!provider.api_key"
                        @click="copyApiKey(pIdx)"
                      >
                        <Icon name="copy" size="sm" class="h-4 w-4" />
                      </button>
                    </div>
                  </div>
                </div>
                <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <div>
                    <label :for="`web-search-quota-${pIdx}`" class="input-label">
                      {{ t('admin.settings.webSearchEmulation.quotaLimit') }}
                    </label>
                    <input
                      :id="`web-search-quota-${pIdx}`"
                      v-model="provider.quota_limit"
                      type="number"
                      min="1"
                      class="input"
                      :placeholder="'∞'"
                    />
                    <p class="input-hint">
                      {{ t('admin.settings.webSearchEmulation.quotaLimitHint') }}
                    </p>
                  </div>
                  <div>
                    <label :for="`web-search-subscribed-${pIdx}`" class="input-label">
                      {{ t('admin.settings.webSearchEmulation.subscribedAt') }}
                    </label>
                    <input
                      :id="`web-search-subscribed-${pIdx}`"
                      :value="formatSubscribedAt(provider.subscribed_at)"
                      type="date"
                      class="input"
                      @input="
                        provider.subscribed_at = parseSubscribedAt(
                          ($event.target as HTMLInputElement).value,
                        )
                      "
                    />
                    <p class="input-hint">
                      {{ t('admin.settings.webSearchEmulation.subscribedAtHint') }}
                    </p>
                  </div>
                </div>
                <div class="flex items-center gap-2">
                  <span class="text-xs text-gray-500 dark:text-dark-400"
                    >{{ t('admin.settings.webSearchEmulation.quotaUsage') }}:</span
                  >
                  <div
                    v-if="provider.quota_limit != null && provider.quota_limit > 0"
                    class="h-1.5 flex-1 rounded-full bg-gray-200 dark:bg-dark-600"
                  >
                    <!-- Warning colors begin above 70% and 90%; cap only bar width at 100%, while neighboring values retain actual usage. -->
                    <div
                      class="h-full rounded-full transition-[width,background-color]"
                      :class="
                        quotaPercentage(provider) > 90
                          ? 'bg-red-500'
                          : quotaPercentage(provider) > 70
                            ? 'bg-yellow-500'
                            : 'bg-green-500'
                      "
                      :style="{ width: Math.min(quotaPercentage(provider), 100) + '%' }"
                    />
                  </div>
                  <div v-else class="flex-1" />
                  <span class="text-xs text-gray-500 dark:text-dark-400"
                    >{{ provider.quota_used ?? 0 }} /
                    {{
                      provider.quota_limit != null && provider.quota_limit > 0
                        ? provider.quota_limit
                        : '∞'
                    }}</span
                  >
                  <button
                    v-if="(provider.quota_used ?? 0) > 0"
                    type="button"
                    class="text-xs text-primary-600 hover:text-primary-700 dark:text-primary-500"
                    @click="resetWebSearchUsage(pIdx)"
                  >
                    {{ t('admin.settings.webSearchEmulation.resetUsage') }}
                  </button>
                </div>
                <div class="flex items-end gap-3">
                  <div class="min-w-0 flex-1">
                    <p class="input-label">
                      {{ t('admin.settings.webSearchEmulation.proxy') }}
                    </p>
                    <ProxySelector v-model="provider.proxy_id" :proxies="webSearchProxies" />
                  </div>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm h-9 whitespace-nowrap"
                    @click="openTestDialog()"
                  >
                    {{ t('admin.settings.webSearchEmulation.test') }}
                  </button>
                </div>
              </div>
            </Collapse>
          </template>
        </RuleListEditor>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- Probe results remain available until the dialog is reopened or another probe starts. -->
  <BaseDialog
    :show="wsTestDialogOpen"
    :title="t('admin.settings.webSearchEmulation.testResultTitle')"
    width="normal"
    close-on-click-outside
    @close="wsTestDialogOpen = false"
  >
    <div class="flex items-center gap-2">
      <input
        v-model="wsTestQuery"
        type="text"
        class="input flex-1 text-sm"
        :placeholder="t('admin.settings.webSearchEmulation.testDefaultQuery')"
        @keyup.enter="testWebSearchProvider()"
      />
      <button
        type="button"
        class="btn btn-primary btn-sm h-9"
        :disabled="wsTestLoading"
        @click="testWebSearchProvider()"
      >
        {{
          wsTestLoading
            ? t('admin.settings.webSearchEmulation.testing')
            : t('admin.settings.webSearchEmulation.test')
        }}
      </button>
    </div>
    <div
      v-if="wsTestResult"
      class="mt-4 max-h-80 overflow-y-auto rounded-control bg-gray-50 p-4 dark:bg-dark-700"
    >
      <p class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
        {{ t('admin.settings.webSearchEmulation.testResultProvider') }}:
        {{ wsTestResult.provider }}
      </p>
      <div v-if="wsTestResult.results.length === 0" class="text-sm text-gray-400">
        {{ t('admin.settings.webSearchEmulation.testNoResults') }}
      </div>
      <div
        v-for="(r, rIdx) in wsTestResult.results"
        :key="rIdx"
        class="mt-2 border-t border-gray-200 pt-2 first:mt-0 first:border-0 first:pt-0 dark:border-dark-600"
      >
        <a :href="r.url" target="_blank" class="text-sm font-medium text-link">{{ r.title }}</a>
        <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
          {{ r.snippet }}
        </p>
      </div>
    </div>
    <div class="mt-4 flex justify-end">
      <button type="button" class="btn btn-secondary btn-sm h-9" @click="wsTestDialogOpen = false">
        {{ t('common.close') }}
      </button>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import Collapse from '@/components/common/Collapse.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import ProxySelector from '@/components/admin/proxy/ProxySelector.vue'
import type { useWebSearchSettings } from './useWebSearchSettings'
import { toRef } from 'vue'
import type { GatewaySection } from './useNavigationSettings'

/**
 * WebSearchSettings.vue renders the search emulation provider list and its probe
 * dialog. The page-owned controller supplies editable credentials, quota dates,
 * proxy choices, and results; hiding the gateway category retains these drafts and
 * the dialog state until a later edit or page action changes them.
 */
// controller holds the page-owned search draft and probe actions. activeGatewaySection
// controls card visibility while the provider editor and probe dialog keep their
// current state for the lifetime of the settings page.
const props = defineProps<{
  controller: ReturnType<typeof useWebSearchSettings> // Page-owned search actions and state.
  activeGatewaySection: GatewaySection // Current visible gateway category.
}>()
// Template bindings retain the controller refs, so edits participate in the page save snapshots.
const {
  webSearchProxies,
  webSearchConfig,
  expandedProviders,
  apiKeyVisible,
  wsTestQuery,
  wsTestLoading,
  wsTestResult,
  wsTestDialogOpen,
  openTestDialog,
  toggleProviderExpand,
  removeWebSearchProvider,
  addWebSearchProvider,
  formatSubscribedAt,
  parseSubscribedAt,
  quotaPercentage,
  resetWebSearchUsage,
  copyApiKey,
  testWebSearchProvider,
} = props.controller
// Navigation remains reactive when the parent changes category, including while this wrapper is hidden.
const activeGatewaySection = toRef(props, 'activeGatewaySection')
// Translation lookup follows locale changes for the labels and hints rendered by this panel.
const { t } = useI18n()
</script>
