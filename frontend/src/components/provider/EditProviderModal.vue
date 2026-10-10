<template>
  <BaseDialog
    :show="show"
    :title="t('admin.providers.editProvider')"
    width="wide"
    :body-scroll="false"
    @close="handleClose"
  >
    <form
      v-if="provider"
      id="edit-provider-form"
      novalidate
      class="flex min-h-0 flex-1 flex-col"
      @submit.prevent="handleSubmit"
    >
      <SettingsTabs
        ref="tabsRef"
        id-prefix="edit-provider"
        :tabs="formTabs"
        :label="t('admin.providers.tabs.label')"
      >
        <template #basic>
          <SettingsSection :title="t('admin.providers.sections.identity')">
            <div class="grid gap-4 md:grid-cols-2">
              <div class="md:col-span-2">
                <label for="edit-provider-name" class="input-label">{{ t('common.name') }}</label>
                <input
                  id="edit-provider-name"
                  v-model="form.name"
                  type="text"
                  required
                  class="input"
                  data-tour="edit-provider-form-name"
                />
              </div>
              <div class="md:col-span-2">
                <label for="edit-provider-notes" class="input-label">{{ t('admin.providers.notes') }}</label>
                <textarea
                  id="edit-provider-notes"
                  v-model="form.notes"
                  rows="3"
                  class="input"
                  :placeholder="t('admin.providers.notesPlaceholder')"
                ></textarea>
                <p class="input-hint">{{ t('admin.providers.notesHint') }}</p>
              </div>
              <div data-provider-field="status">
                <label for="edit-provider-status" class="input-label">{{ t('common.status') }}</label>
                <Select id="edit-provider-status" v-model="form.status" :options="statusOptions" />
              </div>
              <div>
                <label for="edit-provider-expires-at" class="input-label">{{ t('admin.providers.expiresAt') }}</label>
                <input id="edit-provider-expires-at" v-model="expiresAtInput" type="datetime-local" class="input" />
                <p class="input-hint">{{ t('admin.providers.expiresAtHint') }}</p>
              </div>
            </div>
          </SettingsSection>

          <!-- 编辑站点用于设置请求路由上下文，令牌来源由凭据记录。 -->
          <SettingsSection v-if="isQoderCosyProvider" :title="t('admin.providers.qoder.site.label')">
            <SettingsSegmented
              v-model="qoderSite"
              block
              :ariaLabel="t('admin.providers.qoder.site.label')"
              :options="qoderSiteOptions"
            />
            <SettingsNotice v-if="qoderSiteChanged" tone="warning">
              {{ t('admin.providers.qoder.site.changeWarning') }}
            </SettingsNotice>
          </SettingsSection>

          <SettingsSection v-if="showCredentialsSection" :title="t('admin.providers.sections.credentials')">
            <!-- API Key 账号 -->
            <template v-if="provider.type === 'apikey'">
              <!-- 国产供应商提供商模式 -->
              <div v-if="isCNApiKeyProvider">
                <span class="input-label">{{ t('admin.providers.cnProviders.providerMode.title') }}</span>
                <SettingsSegmented
                  v-model="editProviderMode"
                  :ariaLabel="t('admin.providers.cnProviders.providerMode.title')"
                  :options="cnProviderModeSegmentOptions"
                />
                <p class="input-hint">{{ t(`admin.providers.cnProviders.providerMode.${editProviderMode}Desc`) }}</p>
              </div>
              <div v-if="provider.platform === 'gemini'">
                <label for="edit-gemini-provider-type" class="input-label">{{ t('admin.providers.gemini.connectionSource.label') }}</label>
                <Select
                  id="edit-gemini-provider-type"
                  v-model="geminiProviderType"
                  :options="geminiProviderTypeOptions"
                  data-testid="edit-gemini-provider-type"
                />
                <p class="input-hint">{{ geminiProviderTypeHint }}</p>
              </div>
              <div v-if="!isCNApiKeyProvider || editApiProtocol !== 'adaptive'" data-provider-field="base-url">
                <label for="edit-provider-base-url" class="input-label">{{ t('admin.providers.baseUrl') }}</label>
                <input
                  id="edit-provider-base-url"
                  v-model="editBaseUrl"
                  type="text"
                  class="input"
                  data-testid="edit-provider-base-url"
                  :placeholder="apiKeyBaseUrlPlaceholder"
                />
                <p v-if="baseUrlHint" class="input-hint">{{ baseUrlHint }}</p>
                <GrokBaseUrlPresets
                  v-if="provider.platform === 'grok'"
                  class="mt-2"
                  @select="editBaseUrl = $event"
                />
                <CnBaseUrlPresets
                  v-if="isCNApiKeyProvider"
                  class="mt-2"
                  :platform="cnPresetPlatform"
                  :mode="editProviderMode"
                  :protocol="editApiProtocol"
                  :current-url="editBaseUrl"
                  @select="onCnPresetSelect"
                />
              </div>
              <div v-else class="space-y-4">
                <p class="input-label mb-0">{{ t('admin.providers.cnProviders.apiProtocol.endpoints') }}</p>
                <div v-for="item in editAdaptiveProtocolOptions" :key="item.value">
                  <label :for="`edit-provider-endpoint-${item.value}`" class="input-label">
                    {{ t(`admin.providers.cnProviders.apiProtocol.${item.labelKey}`) }}
                  </label>
                  <input
                    :id="`edit-provider-endpoint-${item.value}`"
                    v-model="editAdaptiveBaseUrls[item.value]"
                    type="text"
                    class="input"
                  />
                </div>
                <p v-if="!cnSupportsNativeResponses(provider.platform)" class="input-hint">
                  {{ t('admin.providers.cnProviders.apiProtocol.responsesFallbackDesc') }}
                </p>
              </div>
              <!-- 智谱团队版 Coding Plan：组织/项目 ID 可选，清空组织 ID 即回到个人额度端点。 -->
              <div v-if="provider.platform === 'zhipu' && editProviderMode === 'coding'" class="space-y-2">
                <div class="flex items-center gap-1">
                  <span class="text-sm font-medium text-primary-900 dark:text-dark-50">{{ t('admin.providers.cnProviders.zhipuTeam.title') }}</span>
                  <HelpTooltip trigger="click" width-class="w-80">
                    <p class="mb-1 font-medium">{{ t('admin.providers.cnProviders.zhipuTeam.help.title') }}</p>
                    <ol class="list-decimal space-y-1 pl-4">
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step1') }}</li>
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step2') }}</li>
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step3') }}</li>
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step4') }}</li>
                    </ol>
                    <p class="mt-2 break-all rounded-compact bg-black/20 p-1.5 font-mono text-xs leading-relaxed">
                      {{ t('admin.providers.cnProviders.zhipuTeam.help.example') }}
                    </p>
                  </HelpTooltip>
                </div>
                <div class="grid gap-4 md:grid-cols-2">
                  <div>
                    <label for="edit-zhipu-organization" class="input-label">{{ t('admin.providers.cnProviders.zhipuTeam.organization') }}</label>
                    <input
                      id="edit-zhipu-organization"
                      v-model="editZhipuOrganization"
                      type="text"
                      class="input"
                      :placeholder="t('admin.providers.cnProviders.zhipuTeam.organizationPlaceholder')"
                    />
                  </div>
                  <div>
                    <label for="edit-zhipu-project" class="input-label">{{ t('admin.providers.cnProviders.zhipuTeam.project') }}</label>
                    <input
                      id="edit-zhipu-project"
                      v-model="editZhipuProject"
                      type="text"
                      class="input"
                      :placeholder="t('admin.providers.cnProviders.zhipuTeam.projectPlaceholder')"
                    />
                  </div>
                </div>
                <p class="input-hint">{{ t('admin.providers.cnProviders.zhipuTeam.hint') }}</p>
              </div>
              <div class="grid gap-4 md:grid-cols-2">
                <div data-provider-field="api-key" :class="{ 'md:col-span-2': !showGeminiTier }">
                  <label for="edit-provider-api-key" class="input-label">{{ t('admin.providers.apiKey') }}</label>
                  <input
                    id="edit-provider-api-key"
                    v-model="editApiKey"
                    type="password"
                    class="input font-mono"
                    autocomplete="new-password"
                    data-1p-ignore
                    data-lpignore="true"
                    data-bwignore="true"
                    :placeholder="apiKeyPlaceholder"
                  />
                  <p class="input-hint">{{ t('admin.providers.leaveEmptyToKeep') }}</p>
                </div>
                <div v-if="showGeminiTier" data-testid="edit-gemini-tier">
                  <label for="edit-gemini-tier" class="input-label">{{ t('admin.providers.gemini.tier.label') }}</label>
                  <Select
                    id="edit-gemini-tier"
                    v-model="geminiAIStudioTier"
                    :options="geminiAIStudioTierOptions"
                    data-testid="edit-gemini-tier-select"
                  />
                  <p class="input-hint">{{ t('admin.providers.gemini.tier.aiStudioHint') }}</p>
                </div>
              </div>
            </template>

            <!-- Vertex Service Account -->
            <div v-if="isServiceAccountProvider" class="grid gap-4 md:grid-cols-2">
              <div data-provider-field="vertex-project-id">
                <label for="edit-vertex-project-id" class="input-label">{{ t('admin.providers.vertexProjectId') }}</label>
                <input
                  id="edit-vertex-project-id"
                  v-model="editVertexProjectId"
                  type="text"
                  class="input font-mono"
                  readonly
                  :placeholder="t('admin.providers.vertexProjectIdPlaceholder')"
                />
                <p class="input-hint">{{ t('admin.providers.vertexSaJsonEditHint') }}</p>
              </div>
              <div data-provider-field="vertex-location">
                <label for="edit-vertex-location" class="input-label">{{ t('admin.providers.vertexLocation') }}</label>
                <Select
                  id="edit-vertex-location"
                  v-model="editVertexLocation"
                  :options="vertexLocationOptions"
                  class="font-mono"
                  searchable
                />
                <p class="input-hint">{{ t('admin.providers.vertexLocationHint') }}</p>
              </div>
            </div>

            <!-- Bedrock：SigV4 与 API Key 两种鉴权 -->
            <template v-if="provider.type === 'bedrock'">
              <div v-if="!isBedrockAPIKeyMode" class="grid gap-4 md:grid-cols-2">
                <div class="md:col-span-2">
                  <label for="edit-bedrock-access-key-id" class="input-label">{{ t('admin.providers.bedrockAccessKeyId') }}</label>
                  <input
                    id="edit-bedrock-access-key-id"
                    v-model="editBedrockAccessKeyId"
                    type="text"
                    class="input font-mono"
                    placeholder="AKIA..."
                  />
                </div>
                <div>
                  <label for="edit-bedrock-secret" class="input-label">{{ t('admin.providers.bedrockSecretAccessKey') }}</label>
                  <input
                    id="edit-bedrock-secret"
                    v-model="editBedrockSecretAccessKey"
                    type="password"
                    class="input font-mono"
                    :placeholder="t('admin.providers.bedrockSecretKeyLeaveEmpty')"
                  />
                  <p class="input-hint">{{ t('admin.providers.bedrockSecretKeyLeaveEmpty') }}</p>
                </div>
                <div>
                  <label for="edit-bedrock-session-token" class="input-label">{{ t('admin.providers.bedrockSessionToken') }}</label>
                  <input
                    id="edit-bedrock-session-token"
                    v-model="editBedrockSessionToken"
                    type="password"
                    class="input font-mono"
                    :placeholder="t('admin.providers.bedrockSecretKeyLeaveEmpty')"
                  />
                  <p class="input-hint">{{ t('admin.providers.bedrockSessionTokenHint') }}</p>
                </div>
              </div>
              <div v-else>
                <label for="edit-bedrock-api-key" class="input-label">{{ t('admin.providers.bedrockApiKeyInput') }}</label>
                <input
                  id="edit-bedrock-api-key"
                  v-model="editBedrockApiKeyValue"
                  type="password"
                  class="input font-mono"
                  :placeholder="t('admin.providers.bedrockApiKeyLeaveEmpty')"
                />
                <p class="input-hint">{{ t('admin.providers.bedrockApiKeyLeaveEmpty') }}</p>
              </div>
              <div>
                <label for="edit-bedrock-region" class="input-label">{{ t('admin.providers.bedrockRegion') }}</label>
                <input
                  id="edit-bedrock-region"
                  v-model="editBedrockRegion"
                  type="text"
                  class="input"
                  placeholder="us-east-1"
                />
                <p class="input-hint">{{ t('admin.providers.bedrockRegionHint') }}</p>
              </div>
              <SettingToggleRow
                id="edit-bedrock-force-global"
                v-model="editBedrockForceGlobal"
                :label="t('admin.providers.bedrockForceGlobal')"
                :hint="t('admin.providers.bedrockForceGlobalHint')"
              />
            </template>

            <div v-if="provider.platform === 'antigravity' && provider.type === 'oauth'">
              <label for="edit-antigravity-project-id" class="input-label">{{ t('admin.providers.antigravityProjectIdLabel') }}</label>
              <input
                id="edit-antigravity-project-id"
                v-model="antigravityProjectId"
                data-testid="antigravity-project-id-input"
                type="text"
                class="input font-mono"
                :placeholder="t('admin.providers.antigravityProjectIdPlaceholder')"
              />
              <p class="input-hint">{{ t('admin.providers.antigravityProjectIdHint') }}</p>
            </div>
          </SettingsSection>

          <SettingsSection :title="t('admin.providers.sections.groupsAndNetwork')">
            <GroupSelector
              v-model="form.group_ids"
              :groups="groups"
              data-tour="provider-form-groups"
            />
            <SettingToggleRow
              v-if="provider.platform === 'antigravity'"
              id="edit-provider-allow-overages"
              v-model="allowOverages"
              :label="t('admin.providers.allowOverages')"
              :help="t('admin.providers.allowOveragesTooltip')"
            />
            <div v-if="!isSparkShadow">
              <span class="input-label">{{ t('admin.providers.proxy') }}</span>
              <ProxySelector v-model="form.proxy_id" :proxies="proxies" />
            </div>
          </SettingsSection>
        </template>

        <template #models>
          <ModelRestrictionFields
            v-if="showModelRestriction"
            v-model:mode="modelRestrictionMode"
            :allowed-models="allowedModels"
            v-model:mappings="modelMappings"
            :platform="provider.type === 'bedrock' ? 'anthropic' : provider.platform"
            :provider-id="provider.type === 'bedrock' ? undefined : provider.id"
            :source-placeholder="provider.type === 'bedrock' ? t('admin.providers.fromModel') : undefined"
            :target-placeholder="provider.type === 'bedrock' ? t('admin.providers.toModel') : undefined"
            @update:allowed-models="setAllowedModels"
            @add="touchQoderModelRestriction"
            @remove="touchQoderModelRestriction"
          />

          <!-- Antigravity 白名单与映射分别控制最终范围和请求改写。 -->
          <SettingsSection
            v-if="provider.platform === 'antigravity'"
            :title="t('admin.providers.modelRestriction')"
            :hint="t('admin.providers.selectAllowedModels')"
          >
            <ModelWhitelistSelector v-model="antigravityWhitelistModels" platform="antigravity" />
            <ProviderModelMappingEditor
              v-model="antigravityModelMappings"
              wildcard-validation
            >
              <template #header-actions>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="isSyncingAntigravityUpstream || !provider?.id"
                  @click="syncAntigravityUpstreamModels"
                >
                  {{ isSyncingAntigravityUpstream ? t('admin.providers.syncUpstreamModelsLoading') : t('admin.providers.syncUpstreamModels') }}
                </button>
              </template>
            </ProviderModelMappingEditor>
          </SettingsSection>
        </template>

        <template #scheduling>
          <SettingsSection :title="t('admin.providers.sections.scheduling')">
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="edit-provider-concurrency" class="input-label">{{ t('admin.providers.concurrency') }}</label>
                <!-- 输入过程中允许先清空再录入新值，提交时由后端继续校验。 -->
                <input
                  id="edit-provider-concurrency"
                  v-model.number="form.concurrency"
                  type="number"
                  min="1"
                  class="input"
                  data-testid="edit-provider-concurrency"
                />
              </div>
              <div>
                <label for="edit-provider-load-factor" class="input-label">{{ t('admin.providers.loadFactor') }}</label>
                <input
                  id="edit-provider-load-factor"
                  v-model.number="form.load_factor"
                  type="number"
                  min="1"
                  class="input"
                  :placeholder="String(form.concurrency || 1)"
                  data-testid="edit-provider-load-factor"
                />
                <p class="input-hint">{{ t('admin.providers.loadFactorHint') }}</p>
              </div>
              <div>
                <label for="edit-provider-priority" class="input-label">{{ t('admin.providers.priority') }}</label>
                <input
                  id="edit-provider-priority"
                  v-model.number="form.priority"
                  type="number"
                  min="1"
                  class="input"
                  data-tour="provider-form-priority"
                />
                <p class="input-hint">{{ t('admin.providers.priorityHint') }}</p>
              </div>
              <div>
                <label for="edit-provider-rate-multiplier" class="input-label">{{ t('admin.providers.billingRateMultiplier') }}</label>
                <input
                  id="edit-provider-rate-multiplier"
                  v-model.number="form.rate_multiplier"
                  type="number"
                  min="0"
                  step="0.001"
                  class="input"
                />
                <p class="input-hint">{{ t('admin.providers.billingRateMultiplierHint') }}</p>
              </div>
            </div>
            <template v-if="supportsProviderSchedulingThresholdOverride">
              <SettingToggleRow
                id="edit-provider-scheduling-threshold"
                v-model="providerSchedulingThresholdOverrideEnabled"
                :label="t('admin.providers.providerSchedulingThresholdOverride')"
                :hint="t('admin.providers.providerSchedulingThresholdOverrideHint')"
                testid="provider-scheduling-threshold-override-enabled"
                data-testid="provider-scheduling-threshold-section"
              />
              <Collapse :open="providerSchedulingThresholdOverrideEnabled" unmount-on-hide>
                <SettingsSubpanel>
                  <div>
                    <label for="edit-provider-scheduling-threshold-value" class="input-label">{{ t('admin.providers.providerSchedulingThresholdOverrideValue') }}</label>
                    <input
                      id="edit-provider-scheduling-threshold-value"
                      v-model.number="providerSchedulingThresholdOverrideValue"
                      data-testid="provider-scheduling-threshold-override-value"
                      type="number"
                      min="1"
                      max="100"
                      class="input"
                    />
                    <p class="input-hint">{{ t('admin.providers.providerSchedulingThresholdOverrideDisabledHint') }}</p>
                  </div>
                </SettingsSubpanel>
              </Collapse>
            </template>
          </SettingsSection>

          <TempUnschedFields v-model:enabled="tempUnschedEnabled" v-model:rules="tempUnschedRules" />

          <PoolModeFields
            v-if="provider.type === 'apikey' || provider.type === 'bedrock'"
            v-model:enabled="poolModeEnabled"
            v-model:retry-count="poolModeRetryCount"
            v-model:retry-status-codes="poolModeRetryStatusCodesInput"
          />

          <CustomErrorCodesFields
            v-if="provider.type === 'apikey'"
            v-model:enabled="customErrorCodesEnabled"
            v-model:codes="selectedErrorCodes"
          />

          <SettingsSection :title="t('admin.providers.sections.autoPause')">
            <SettingToggleRow
              v-if="provider.platform === 'anthropic' || provider.platform === 'antigravity'"
              id="edit-provider-intercept-warmup"
              v-model="interceptWarmupRequests"
              :label="t('admin.providers.interceptWarmupRequests')"
              :hint="t('admin.providers.interceptWarmupRequestsDesc')"
            />
            <SettingToggleRow
              id="edit-provider-auto-pause-expired"
              v-model="autoPauseOnExpired"
              :label="t('admin.providers.autoPauseOnExpired')"
              :hint="t('admin.providers.autoPauseOnExpiredDesc')"
            />
            <template v-if="provider.platform === 'openai'">
              <SettingToggleRow
                id="edit-provider-auto-pause-5h-disabled"
                v-model="autoPause5hDisabled"
                :label="t('admin.providers.autoPause5hDisabled')"
                :hint="t('admin.providers.autoPauseDisabledHint')"
                testid="auto-pause-5h-disabled"
              />
              <SettingToggleRow
                id="edit-provider-auto-pause-7d-disabled"
                v-model="autoPause7dDisabled"
                :label="t('admin.providers.autoPause7dDisabled')"
                :hint="t('admin.providers.autoPauseDisabledHint')"
                testid="auto-pause-7d-disabled"
              />
              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="edit-provider-auto-pause-5h" class="input-label">{{ t('admin.providers.autoPause5hThreshold') }}</label>
                  <input
                    id="edit-provider-auto-pause-5h"
                    v-model.number="autoPause5hThreshold"
                    type="number"
                    min="0"
                    max="100"
                    step="0.1"
                    class="input"
                    :disabled="autoPause5hDisabled"
                    data-testid="auto-pause-5h-threshold"
                  />
                  <p class="input-hint">{{ t('admin.providers.autoPauseThresholdHint') }}</p>
                </div>
                <div>
                  <label for="edit-provider-auto-pause-7d" class="input-label">{{ t('admin.providers.autoPause7dThreshold') }}</label>
                  <input
                    id="edit-provider-auto-pause-7d"
                    v-model.number="autoPause7dThreshold"
                    type="number"
                    min="0"
                    max="100"
                    step="0.1"
                    class="input"
                    :disabled="autoPause7dDisabled"
                    data-testid="auto-pause-7d-threshold"
                  />
                  <p class="input-hint">{{ t('admin.providers.autoPauseThresholdHint') }}</p>
                </div>
              </div>
            </template>
          </SettingsSection>
        </template>

        <template #quota>
          <QuotaLimitFields
            v-if="provider.type === 'apikey' || provider.type === 'bedrock'"
            :limits="quotaLimits"
            :notify="quotaNotifyState"
            :notify-global-enabled="quotaNotifyGlobalEnabled"
            :hint="provider.platform === 'anthropic' ? t('admin.providers.quotaControl.hint') : t('admin.providers.quotaLimitHint')"
            @update:limit="setQuotaLimit"
            @update:notify="setQuotaNotifyField"
          />

          <AnthropicOAuthLimitFields
            v-if="isAnthropicOAuthLikeProvider"
            v-model:window-cost-enabled="windowCostEnabled"
            v-model:window-cost-limit="windowCostLimit"
            v-model:window-cost-sticky-reserve="windowCostStickyReserve"
            v-model:session-limit-enabled="sessionLimitEnabled"
            v-model:max-sessions="maxSessions"
            v-model:session-idle-timeout="sessionIdleTimeout"
            v-model:rpm-limit-enabled="rpmLimitEnabled"
            v-model:base-rpm="baseRpm"
            v-model:rpm-strategy="rpmStrategy"
            v-model:rpm-sticky-buffer="rpmStickyBuffer"
            v-model:user-msg-queue-mode="userMsgQueueMode"
          />

          <UpstreamUsageConfigEditor
            v-if="provider.type === 'apikey'"
            :enabled="upstreamUsageEnabled"
            :adapter="upstreamUsageAdapter"
            :base-url="upstreamUsageBaseUrl"
            :wallet-access-token="upstreamUsageWalletAccessToken"
            :wallet-user-id="upstreamUsageWalletUserId"
            :automatic-adapter="isCNApiKeyProvider"
            @update:enabled="upstreamUsageEnabled = $event"
            @update:adapter="upstreamUsageAdapter = $event"
            @update:base-url="upstreamUsageBaseUrl = $event"
            @update:wallet-access-token="upstreamUsageWalletAccessToken = $event"
            @update:wallet-user-id="upstreamUsageWalletUserId = $event"
          />

          <OllamaCloudUsageSettings
            v-if="provider.ollama_cloud_usage?.eligible"
            :provider="provider"
            @updated="handleOllamaCloudUsageUpdated"
          />
        </template>

        <template #request>
          <ProviderProtocolSelector
            v-if="!provider.parent_provider_id"
            v-model="upstreamProtocols"
            :platform="provider.platform"
            :type="provider.type"
            :auth-mode="String(provider.credentials?.auth_mode ?? provider.credentials?.openai_auth_mode ?? '')"
          />

          <SettingsSection :title="t('admin.providers.sections.requestHeaders')">
            <UpstreamRequestIdHeaderField
              v-model="upstreamRequestIdHeader"
              :platform="provider.platform"
              :type="provider.type"
            />
          </SettingsSection>

          <HeaderOverrideFields
            v-if="headerOverrideCapable"
            v-model:enabled="headerOverrideEnabled"
            v-model:rows="headerOverrideRows"
            data-provider-field="header-override"
          />

          <!-- Grok OAuth 的自定义地址用于请求转发，授权与刷新使用各自的端点。 -->
          <SettingsSection v-if="provider.platform === 'grok' && provider.type === 'oauth'" :title="t('admin.providers.sections.grok')">
            <SettingToggleRow
              id="edit-grok-custom-base-url"
              v-model="grokOAuthCustomBaseUrlEnabled"
              :label="t('admin.providers.grokCustomBaseUrl.title')"
              :hint="t('admin.providers.grokCustomBaseUrl.hint')"
              testid="grok-custom-base-url-toggle"
            />
            <Collapse :open="grokOAuthCustomBaseUrlEnabled" unmount-on-hide>
              <SettingsSubpanel data-provider-field="grok-base-url">
                <input
                  v-model="grokOAuthBaseUrl"
                  type="text"
                  class="input"
                  data-testid="grok-custom-base-url-input"
                  :aria-label="t('admin.providers.grokCustomBaseUrl.title')"
                  :placeholder="t('admin.providers.grokCustomBaseUrl.placeholder')"
                />
                <GrokBaseUrlPresets @select="grokOAuthBaseUrl = $event" />
              </SettingsSubpanel>
            </Collapse>
            <SettingToggleRow
              id="edit-grok-client-tool-cache"
              v-model="grokClientToolCacheEnabled"
              :label="t('admin.providers.grokClientToolCache.title')"
              :hint="t('admin.providers.grokClientToolCache.hint')"
              testid="grok-client-tool-cache-toggle"
            />
          </SettingsSection>

          <template v-if="isOpenAIOAuthOrAPIKey">
            <SettingsSection :title="t('admin.providers.sections.openaiCompatibility')">
              <SettingToggleRow
                id="edit-openai-passthrough"
                v-model="openaiPassthroughEnabled"
                :label="t('admin.providers.openai.oauthPassthrough')"
                :hint="t('admin.providers.openai.oauthPassthroughDesc')"
              />
              <SettingToggleRow
                v-if="provider.type === 'oauth' && !isSparkShadow"
                id="edit-openai-flatten-namespaces"
                v-model="openaiFlattenNamespacesEnabled"
                :label="t('admin.providers.openai.flattenNamespaces')"
                :hint="t('admin.providers.openai.flattenNamespacesDesc')"
                testid="edit-openai-flatten-namespaces-toggle"
              />
              <template v-if="provider.type === 'apikey'">
                <SettingToggleRow
                  id="edit-openai-continuation-supported"
                  v-model="openAIResponsesContinuationSupported"
                  :label="t('admin.providers.openai.responsesContinuationSupported')"
                  :hint="t('admin.providers.openai.responsesContinuationSupportedDesc')"
                  testid="edit-openai-continuation-supported"
                />
                <SettingToggleRow
                  id="edit-openai-images-url-to-b64-json"
                  v-model="openAIImagesURLToB64JSON"
                  :label="t('admin.providers.openai.imagesURLToB64JSON')"
                  :hint="t('admin.providers.openai.imagesURLToB64JSONDesc')"
                  testid="edit-openai-images-url-to-b64-json"
                />
              </template>
            </SettingsSection>

            <SettingsSection v-if="provider.type === 'oauth'" :title="t('admin.providers.sections.openaiClient')">
              <SettingRow
                id="edit-openai-client-policy"
                label-for="edit-openai-client-policy-select"
                :label="t('admin.providers.openai.clientPolicy')"
                :hint="t('admin.providers.openai.clientPolicyDesc')"
                field
              >
                <Select id="edit-openai-client-policy-select" v-model="openAIOAuthClientPolicy" :options="openAIOAuthClientPolicyOptions" />
              </SettingRow>
              <Collapse :open="openAIOAuthClientPolicy === 'codex_only'" unmount-on-hide>
                <SettingsSubpanel>
                  <SettingToggleRow
                    id="edit-openai-codex-allow-claude-code"
                    v-model="codexCLIOnlyAllowClaudeCodeEnabled"
                    :label="t('admin.providers.openai.codexCLIOnlyAllowClaudeCode')"
                    :hint="t('admin.providers.openai.codexCLIOnlyAllowClaudeCodeDesc')"
                  />
                </SettingsSubpanel>
              </Collapse>
              <template v-if="!isSparkShadow">
                <SettingRow
                  id="edit-codex-fingerprint-mode"
                  label-for="edit-codex-fingerprint-mode-select"
                  :label="t('admin.providers.openai.codexFingerprintMode')"
                  :hint="t('admin.providers.openai.codexFingerprintModeDesc')"
                  field
                >
                  <Select
                    id="edit-codex-fingerprint-mode-select"
                    v-model="codexFingerprintMode"
                    data-testid="edit-codex-fingerprint-mode-select"
                    :options="codexFingerprintModeOptions"
                  />
                </SettingRow>
                <SettingRow
                  id="edit-openai-plan-type"
                  label-for="edit-openai-plan-type-select"
                  :label="t('admin.providers.openai.planType')"
                  :hint="t('admin.providers.openai.planTypeDesc')"
                  field
                >
                  <Select
                    id="edit-openai-plan-type-select"
                    v-model="editPlanType"
                    data-testid="openai-plan-type-select"
                    :options="planTypeOptions"
                  />
                </SettingRow>
              </template>
            </SettingsSection>

            <CodexImageToolModeSelector v-model="codexImageToolMode" />

            <ResponsesWSConnectionModeSelector
              v-if="upstreamProtocols?.includes('openai_responses_websocket')"
              v-model="responsesWSConnectionMode"
              test-id-prefix="edit-openai-ws-connection"
            />

            <SettingsSection :title="t('admin.providers.sections.compaction')">
              <OpenAICompactionToggle
                v-model="openAINativeCompactionV2Mode"
                test-id="edit-openai-native-compaction-v2-mode"
                :label="t('admin.providers.openai.nativeCompactV2Mode')"
                :hint="t('admin.providers.openai.nativeCompactV2ModeDesc')"
              />
              <OpenAICompactionToggle
                v-model="openAICompactMode"
                test-id="edit-openai-compact-mode"
                :label="t('admin.providers.openai.compactMode')"
                :hint="t('admin.providers.openai.compactModeDesc')"
              />
              <ProviderModelMappingEditor
                v-if="openAICompactMode !== 'force_off'"
                v-model="openAICompactModelMappings"
                :title="t('admin.providers.openai.compactModelMapping')"
                :hint="t('admin.providers.openai.compactModelMappingDesc')"
                :source-placeholder="t('admin.providers.fromModel')"
                :target-placeholder="t('admin.providers.toModel')"
              />
            </SettingsSection>
          </template>

          <SettingsSection
            v-if="provider.platform === 'anthropic' && provider.type === 'apikey'"
            :title="t('admin.providers.sections.anthropicCompatibility')"
          >
            <SettingToggleRow
              id="edit-anthropic-passthrough"
              v-model="anthropicPassthroughEnabled"
              :label="t('admin.providers.anthropic.apiKeyPassthrough')"
              :hint="t('admin.providers.anthropic.apiKeyPassthroughDesc')"
            />
            <SettingRow
              id="edit-anthropic-auth-scheme"
              label-for="edit-anthropic-auth-scheme-select"
              :label="t('admin.providers.anthropic.apiKeyAuthScheme')"
              :hint="t('admin.providers.anthropic.apiKeyAuthSchemeDesc')"
              field
            >
              <Select id="edit-anthropic-auth-scheme-select" v-model="anthropicAPIKeyAuthScheme" :options="anthropicAPIKeyAuthSchemeOptions" />
            </SettingRow>
            <!-- 全局关闭网页搜索模拟时隐藏提供商级覆盖。 -->
            <SettingRow
              v-if="webSearchGlobalEnabled"
              id="edit-anthropic-web-search"
              label-for="edit-anthropic-web-search-select"
              :label="t('admin.providers.anthropic.webSearchEmulation')"
              :hint="t('admin.providers.anthropic.webSearchEmulationDesc')"
              field
            >
              <Select id="edit-anthropic-web-search-select" v-model="webSearchEmulationMode" :options="webSearchEmulationOptions" />
            </SettingRow>
          </SettingsSection>

          <TLSFingerprintFields
            v-if="supportsTLSFingerprint(provider)"
            v-model:enabled="tlsFingerprintEnabled"
            v-model:profile-id="tlsFingerprintProfileId"
            v-model:router-id="tlsFingerprintRouterId"
            :profile-options="tlsFingerprintProfileOptions"
            :router-options="supportsTLSFingerprintRouter ? tlsFingerprintRouterOptions : undefined"
            test-id-prefix="edit-openai-tls-fingerprint"
          />

          <AnthropicOAuthRequestFields
            v-if="isAnthropicOAuthLikeProvider"
            v-model:session-id-masking-enabled="sessionIdMaskingEnabled"
            v-model:cache-ttl-enabled="cacheTTLOverrideEnabled"
            v-model:cache-ttl-target="cacheTTLOverrideTarget"
            v-model:custom-base-url-enabled="customBaseUrlEnabled"
            v-model:custom-base-url="customBaseUrl"
          />
        </template>
      </SettingsTabs>
    </form>

    <template #footer>
      <div v-if="provider" class="flex justify-end gap-3">
        <button @click="handleClose" type="button" class="btn btn-secondary">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="edit-provider-form"
          :disabled="submitting"
          class="btn btn-primary"
          data-tour="provider-form-submit"
        >
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="submitting"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
          />
          {{ submitting ? t('admin.providers.updating') : t('common.update') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import Collapse from '@/components/common/Collapse.vue'

// 协议选择保存提供商支持的协议集合。
const upstreamProtocols = ref<ProtocolID[] | undefined>(undefined)

import { normalizeLegacyOpenAIExtra, normalizeOpenAICompactMode } from '@/utils/openaiLegacyConfiguration'
import ProviderProtocolSelector from './ProviderProtocolSelector.vue'
import { loadProtocolCatalog, nativeProtocolOptions } from '@/api/admin/protocolCapabilities'
import type { ProtocolID } from '@/types'
import OpenAICompactionToggle from './OpenAICompactionToggle.vue'
import { ref, reactive, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { useQuotaNotifyState } from '@/composables/useQuotaNotifyState'
import type {
  Provider,
  Proxy,
  AdminGroup,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
  OllamaCloudUsageState,
  UpstreamUsageAdapter
} from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import UpstreamRequestIdHeaderField from '@/components/provider/UpstreamRequestIdHeaderField.vue'
import Icon from '@/components/icons/Icon.vue'
import ProviderModelMappingEditor from '@/components/provider/ProviderModelMappingEditor.vue'
import type { TempUnschedRuleForm } from '@/components/provider/TempUnschedRulesEditor.vue'
import type { ModelMappingRow } from '@/utils/modelMappingRules'
import ProxySelector from '@/components/common/ProxySelector.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import CodexImageToolModeSelector from '@/components/provider/CodexImageToolModeSelector.vue'
import ModelWhitelistSelector from '@/components/provider/ModelWhitelistSelector.vue'
import ResponsesWSConnectionModeSelector from '@/components/provider/ResponsesWSConnectionModeSelector.vue'
import GrokBaseUrlPresets from '@/components/provider/GrokBaseUrlPresets.vue'
import CnBaseUrlPresets from '@/components/provider/CnBaseUrlPresets.vue'
import OllamaCloudUsageSettings from '@/components/provider/OllamaCloudUsageSettings.vue'
import UpstreamUsageConfigEditor from '@/components/provider/UpstreamUsageConfigEditor.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import SettingsTabs from '@/components/common/settings/SettingsTabs.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import AnthropicOAuthLimitFields from '@/components/provider/form/AnthropicOAuthLimitFields.vue'
import AnthropicOAuthRequestFields from '@/components/provider/form/AnthropicOAuthRequestFields.vue'
import CustomErrorCodesFields from '@/components/provider/form/CustomErrorCodesFields.vue'
import HeaderOverrideFields from '@/components/provider/form/HeaderOverrideFields.vue'
import ModelRestrictionFields from '@/components/provider/form/ModelRestrictionFields.vue'
import PoolModeFields from '@/components/provider/form/PoolModeFields.vue'
import QuotaLimitFields from '@/components/provider/form/QuotaLimitFields.vue'
import TempUnschedFields from '@/components/provider/form/TempUnschedFields.vue'
import TLSFingerprintFields from '@/components/provider/form/TLSFingerprintFields.vue'
import { bindQuotaLimits } from '@/components/provider/form/quotaLimit'
import {
  DEFAULT_POOL_MODE_RETRY_COUNT,
  formatPoolModeRetryStatusCodes,
  normalizePoolModeRetryCount,
  parsePoolModeRetryStatusCodes
} from '@/components/provider/form/poolMode'
import {
  useAnthropicAPIKeyAuthSchemeOptions,
  useCodexFingerprintModeOptions,
  useOpenAIOAuthClientPolicyOptions,
  useWebSearchEmulationOptions,
  type AnthropicAPIKeyAuthScheme,
  type CodexFingerprintMode,
  type RpmStrategy
} from '@/components/provider/form/providerFormOptions'
import {
  ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY,
  applyAntigravityProjectID,
  applyHeaderOverride,
  applyInterceptWarmup,
  applyPlanType,
  buildPlanTypeOptions,
  readPlanType,
  isCustomGrokBaseUrl,
  isHeaderOverrideCapable,
  splitHeaderOverridesObject,
  validateHeaderOverrideRows,
  cnSupportsNativeResponses,
  defaultCNAdaptiveBaseUrls,
  defaultCNBaseUrl,
  HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY,
  HEADER_OVERRIDES_CREDENTIAL_KEY,
  type CnProviderMode,
  type CnApiProtocol,
  type CnNativeApiProtocol,
  type HeaderOverrideRow
} from '@/components/provider/credentialsBuilder'
import { formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import {
  applyCodexImageToolMode,
  readCodexImageToolMode,
  type CodexImageToolMode
} from '@/utils/codexImageToolMode'
import {
  VERTEX_LOCATION_OPTIONS,
  groupedProviderSelectOptions
} from '@/constants/provider'
import {
  clearLegacyResponsesWSSettings,
  RESPONSES_WS_POOLED,
  type ResponsesWSConnectionMode,
  readResponsesWSConnectionMode
} from '@/utils/responsesWsConnection'
import {
  buildModelMappingObject,
  buildPersistedModelRestriction,
  splitQoderPersistedModelRestriction,
  splitPersistedModelRestriction,
  type QoderSite
} from '@/composables/useModelWhitelist'

interface Props {
  show: boolean
  provider: Provider | null
  proxies: Proxy[]
  groups: AdminGroup[]
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
  updated: [provider: Provider]
}>()

const { t } = useI18n()
const appStore = useAppStore()

// Spark 影子提供商(parent_provider_id 非空):代理恒继承母提供商,不可独立编辑(外审 B/P1),
// 故隐藏代理选择器。
const isSparkShadow = computed(() => props.provider?.parent_provider_id != null)

const handleOllamaCloudUsageUpdated = (state: OllamaCloudUsageState) => {
  if (props.provider) emit('updated', { ...props.provider, ollama_cloud_usage: state })
}

// Platform-specific hint for Base URL
const baseUrlHint = computed(() => {
  if (!props.provider) return t('admin.providers.baseUrlHint')
  if (props.provider.platform === 'openai') return t('admin.providers.openai.baseUrlHint')
  if (props.provider.platform === 'gemini' && geminiProviderType.value === 'third_party') {
    return t('admin.providers.gemini.connectionSource.thirdPartyBaseUrlHint')
  }
  if (props.provider.platform === 'gemini') return t('admin.providers.gemini.baseUrlHint')
  if (props.provider.platform === 'grok') return ''
  return t('admin.providers.baseUrlHint')
})

// Model mapping type
// State
const submitting = ref(false)
const editBaseUrl = ref('https://api.anthropic.com')
const editApiKey = ref('')
const upstreamUsageEnabled = ref(true)
const upstreamUsageAdapter = ref<UpstreamUsageAdapter>('sub2api')
const upstreamUsageBaseUrl = ref('')
const upstreamUsageWalletAccessToken = ref('')
const upstreamUsageWalletUserId = ref('')
type GeminiProviderType = 'official' | 'third_party'
const geminiProviderType = ref<GeminiProviderType>('official')
const geminiAIStudioTier = ref<'aistudio_free' | 'aistudio_paid'>('aistudio_free')
const geminiProviderTypeOptions = computed(() => [
  { value: 'official', label: t('admin.providers.gemini.connectionSource.official') },
  { value: 'third_party', label: t('admin.providers.gemini.connectionSource.thirdParty') }
])
const geminiAIStudioTierOptions = computed(() => [
  { value: 'aistudio_free', label: t('admin.providers.gemini.tier.aiStudio.free') },
  { value: 'aistudio_paid', label: t('admin.providers.gemini.tier.aiStudio.paid') }
])
const geminiProviderTypeHint = computed(() =>
  geminiProviderType.value === 'third_party'
    ? t('admin.providers.gemini.connectionSource.thirdPartyHint')
    : t('admin.providers.gemini.connectionSource.officialHint')
)

// 国产供应商提供商允许修正历史数据中的模式、协议和自定义端点。
const isCNApiKeyProvider = computed(
  () =>
    props.provider?.type === 'apikey' &&
    (props.provider.platform === 'kimi' ||
      props.provider.platform === 'zhipu' ||
      props.provider.platform === 'deepseek')
)
// 模板不支持联合类型断言，因此在脚本中收窄预设组件的平台类型。
const cnPresetPlatform = computed<'kimi' | 'zhipu' | 'deepseek'>(() => {
  const platform = props.provider?.platform
  if (platform === 'kimi' || platform === 'zhipu' || platform === 'deepseek') {
    return platform
  }
  return 'kimi'
})
const editApiProtocol = ref<CnApiProtocol>('adaptive')
const editProviderMode = ref<CnProviderMode>('payg')
// 智谱团队版 Coding Plan 的组织/项目 ID，清空后随完整凭据更新一并移除。
const editZhipuOrganization = ref('')
const editZhipuProject = ref('')
const editAdaptiveBaseUrls = ref<Record<CnNativeApiProtocol, string>>({
  chat_completions: '',
  anthropic: '',
  responses: ''
})
// 回填窗口标志：syncFormFromProvider 会同步改写 editProviderMode / editApiProtocol，
// watcher（pre-flush）在同步代码执行完之后才触发，若未暂停，会把刚恢复的
// 存储版 base_url（可能是用户自定义/中转地址）覆盖为官方预设并在下次保存时持久化。
// nextTick 后解除，此后用户主动切换模式/协议仍正常联动重置。
const syncingForm = ref(false)
const cnProviderModeSegmentOptions = computed(() => {
  const modes: CnProviderMode[] = props.provider?.platform === 'deepseek' ? ['payg'] : ['payg', 'coding']
  return modes.map(value => ({ value, label: t(`admin.providers.cnProviders.providerMode.${value}`) }))
})
const editAdaptiveProtocolOptions = computed<Array<{ value: CnNativeApiProtocol; labelKey: string }>>(() => {
  const opts: Array<{ value: CnNativeApiProtocol; labelKey: string }> = [
    { value: 'chat_completions', labelKey: 'chatCompletions' },
    { value: 'anthropic', labelKey: 'anthropic' }
  ]
  if (cnSupportsNativeResponses(props.provider?.platform ?? '')) opts.push({ value: 'responses', labelKey: 'responses' })
  return opts
})
watch(editApiProtocol, (protocol, previousProtocol) => {
  if (!isCNApiKeyProvider.value || syncingForm.value) return
  if (protocol === 'adaptive') {
    const defaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, editProviderMode.value)
    for (const item of editAdaptiveProtocolOptions.value) {
      if (!editAdaptiveBaseUrls.value[item.value]) editAdaptiveBaseUrls.value[item.value] = defaults[item.value]
    }
    if (previousProtocol !== 'adaptive' && editBaseUrl.value.trim()) {
      editAdaptiveBaseUrls.value[previousProtocol] = editBaseUrl.value.trim()
    }
    editBaseUrl.value = editAdaptiveBaseUrls.value.chat_completions
    return
  }
  if (previousProtocol === 'adaptive') {
    editBaseUrl.value = editAdaptiveBaseUrls.value[protocol] ||
      defaultCNBaseUrl(props.provider!.platform, editProviderMode.value, protocol)
    return
  }
  editBaseUrl.value = defaultCNBaseUrl(props.provider!.platform, editProviderMode.value, protocol)
})
watch(editProviderMode, (mode, previousMode) => {
  if (!isCNApiKeyProvider.value || syncingForm.value) return
  const effectiveMode = props.provider!.platform === 'deepseek' && mode === 'coding' ? 'payg' : mode
  if (effectiveMode !== mode) {
    editProviderMode.value = effectiveMode
    return
  }
  if (editApiProtocol.value === 'adaptive') {
    const previousDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, previousMode)
    const nextDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, mode)
    for (const item of editAdaptiveProtocolOptions.value) {
      if (!editAdaptiveBaseUrls.value[item.value] || editAdaptiveBaseUrls.value[item.value] === previousDefaults[item.value]) {
        editAdaptiveBaseUrls.value[item.value] = nextDefaults[item.value]
      }
    }
    editBaseUrl.value = editAdaptiveBaseUrls.value.chat_completions
    return
  }
  editBaseUrl.value = defaultCNBaseUrl(props.provider!.platform, mode, editApiProtocol.value)
})

// 端点预设同时更新模式和协议，保持表单字段一致。
function onCnPresetSelect(preset: { mode: CnProviderMode; protocol: CnApiProtocol; url: string }) {
  editProviderMode.value = preset.mode
  editApiProtocol.value = 'adaptive'
  if (preset.protocol !== 'adaptive') editAdaptiveBaseUrls.value[preset.protocol] = preset.url
  editBaseUrl.value = editAdaptiveBaseUrls.value.chat_completions
}

const isGeminiThirdPartyBaseUrl = (value: string) => {
  const normalized = value.trim()
  if (!normalized) return false
  try {
    return new URL(normalized).hostname.toLowerCase() !== 'generativelanguage.googleapis.com'
  } catch {
    return false
  }
}
// Bedrock credentials
const editBedrockAccessKeyId = ref('')
const editBedrockSecretAccessKey = ref('')
const editBedrockSessionToken = ref('')
const editBedrockRegion = ref('')
const editBedrockForceGlobal = ref(false)
const editBedrockApiKeyValue = ref('')
const editVertexProjectId = ref('')
const editVertexClientEmail = ref('')
const editVertexLocation = ref('us-central1')
const isBedrockAPIKeyMode = computed(() =>
  props.provider?.type === 'bedrock' &&
  (props.provider?.credentials as Record<string, unknown>)?.auth_mode === 'apikey'
)
const modelMappings = ref<ModelMappingRow[]>([])
const openAICompactModelMappings = ref<ModelMappingRow[]>([])
const modelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const allowedModels = ref<string[]>([])
const qoderModelRestrictionConfigured = ref(false)
const qoderModelRestrictionTouched = ref(false)
const qoderModelWhitelistConfigured = ref(false)
const qoderModelWhitelistTouched = ref(false)
const qoderSite = ref<QoderSite>('global')
const GROK_CLIENT_TOOL_CACHE_EXTRA_KEY = 'grok_client_tool_cache_enabled'
const poolModeEnabled = ref(false)
const poolModeRetryCount = ref(DEFAULT_POOL_MODE_RETRY_COUNT)
const poolModeRetryStatusCodesInput = ref('')

const customErrorCodesEnabled = ref(false)
const selectedErrorCodes = ref<number[]>([])
const headerOverrideEnabled = ref(false)
const headerOverrideRows = ref<HeaderOverrideRow[]>([])

const headerOverrideCapable = computed(
  () => !!props.provider && isHeaderOverrideCapable(props.provider.platform, props.provider.type)
)

// Grok OAuth 自定义转发地址，授权与刷新使用各自的端点。
const grokOAuthCustomBaseUrlEnabled = ref(false)
const grokOAuthBaseUrl = ref('')
// Grok Free OAuth 提供商默认使用客户端工具提示缓存；extra 中的显式 false 作为退出信号。
const grokClientToolCacheEnabled = ref(true)

const interceptWarmupRequests = ref(false)
const autoPauseOnExpired = ref(false)
const autoPause5hThreshold = ref<number | null>(null)
const autoPause7dThreshold = ref<number | null>(null)
const autoPause5hDisabled = ref(false)
const autoPause7dDisabled = ref(false)
// 上游ID：直接上游声明请求标识的响应头名，留空不记录。
const upstreamRequestIdHeader = ref('')
const readUpstreamRequestIdHeader = (extra: unknown): string => {
  const value = (extra as Record<string, unknown> | undefined)?.upstream_request_id_header
  return typeof value === 'string' ? value : ''
}
const allowOverages = ref(false) // For antigravity providers: enable AI Credits overages
const antigravityProjectId = ref('')
const antigravityModelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const antigravityWhitelistModels = ref<string[]>([])
const antigravityModelMappings = ref<ModelMappingRow[]>([])
const isSyncingAntigravityUpstream = ref(false)
const tempUnschedEnabled = ref(false)
const providerSchedulingThresholdOverrideEnabled = ref(false)
const providerSchedulingThresholdOverrideValue = ref(100)
const PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY = 'provider_scheduling_threshold'
const supportsProviderSchedulingThresholdOverride = computed(() =>
  supportsProviderSchedulingThresholdOverridePlatform(props.provider?.platform)
)
const tempUnschedRules = ref<TempUnschedRuleForm[]>([])

// Quota control state (Anthropic OAuth/SetupToken only)
const windowCostEnabled = ref(false)
const windowCostLimit = ref<number | null>(null)
const windowCostStickyReserve = ref<number | null>(null)
const sessionLimitEnabled = ref(false)
const maxSessions = ref<number | null>(null)
const sessionIdleTimeout = ref<number | null>(null)
const rpmLimitEnabled = ref(false)
const baseRpm = ref<number | null>(null)
const rpmStrategy = ref<RpmStrategy>('tiered')
const rpmStickyBuffer = ref<number | null>(null)
const userMsgQueueMode = ref('')
const tlsFingerprintEnabled = ref(false)
const tlsFingerprintProfileId = ref<number | null>(null)
const tlsFingerprintProfiles = ref<{ id: number; name: string }[]>([])
const tlsFingerprintRouterId = ref<number | null>(null)
const tlsFingerprintRouters = ref<{ id: number; name: string }[]>([])
const tlsFingerprintProfileOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.defaultProfile') },
  ...(tlsFingerprintProfiles.value.length > 0
    ? [{ value: -1, label: t('admin.providers.quotaControl.tlsFingerprint.randomProfile') }]
    : []),
  ...tlsFingerprintProfiles.value.map((profile) => ({ value: profile.id, label: profile.name }))
])
const tlsFingerprintRouterOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.noRouter') },
  ...tlsFingerprintRouters.value.map((router) => ({ value: router.id, label: router.name }))
])
const sessionIdMaskingEnabled = ref(false)
const cacheTTLOverrideEnabled = ref(false)
const cacheTTLOverrideTarget = ref<string>('5m')
const customBaseUrlEnabled = ref(false)
const customBaseUrl = ref('')

// OpenAI 自动透传开关（OAuth/API Key）
const openaiPassthroughEnabled = ref(false)
// OpenAI OAuth namespace 工具摊平兼容开关，缺省关闭即原样保留。
const openaiFlattenNamespacesEnabled = ref(false)
// OpenAI 订阅档位（Plus/Pro/Free）手动覆盖值,存于 credentials.plan_type;'' 表示清空/自动识别
const editPlanType = ref<string>('')
const openAICompactMode = ref<OpenAICompactMode>('force_on')
const openAINativeCompactionV2Mode = ref<OpenAICompactMode>('force_on')
// HTTP continuation 缺省关闭，只有管理员确认上游支持时才发送 previous_response_id。
const openAIResponsesContinuationSupported = ref(false)
// 图片回填默认关闭，只对 OpenAI API Key 提供商生效。
const openAIImagesURLToB64JSON = ref(false)
const responsesWSConnectionMode = ref<ResponsesWSConnectionMode>(RESPONSES_WS_POOLED)
const codexCLIOnlyAllowClaudeCodeEnabled = ref(false)
const openAIOAuthClientPolicy = ref<OpenAIOAuthClientPolicy>('any')
const codexFingerprintMode = ref<CodexFingerprintMode>('off')
const codexImageToolMode = ref<CodexImageToolMode>('inherit')
const anthropicPassthroughEnabled = ref(false)
const anthropicAPIKeyAuthScheme = ref<AnthropicAPIKeyAuthScheme>('x_api_key')
const webSearchEmulationMode = ref('default')
const webSearchEmulationOptions = useWebSearchEmulationOptions()
const anthropicAPIKeyAuthSchemeOptions = useAnthropicAPIKeyAuthSchemeOptions()
const webSearchGlobalEnabled = ref(false)
const {
  globalEnabled: quotaNotifyGlobalEnabled,
  state: quotaNotifyState,
  loadGlobalState: loadQuotaNotifyGlobal,
  loadFromExtra: loadQuotaNotifyFromExtra,
  writeToExtra: writeQuotaNotifyToExtra,
  setField: setQuotaNotifyField,
  reset: resetQuotaNotify,
} = useQuotaNotifyState()

const supportsTLSFingerprint = (provider: Provider | null | undefined) => {
  // TLS 指纹伪装开放给实际走 OAuth/COSY 客户端模拟链路的提供商。
  return !!provider && (
    (provider.platform === 'anthropic' && (provider.type === 'oauth' || provider.type === 'setup-token')) ||
    (provider.platform === 'openai' && provider.type === 'oauth') ||
    (provider.platform === 'qoder' && provider.type === 'cosy')
  )
}

const isQoderCosyProvider = computed(() =>
  props.provider?.platform === 'qoder' && props.provider?.type === 'cosy'
)
const originalQoderSite = computed<QoderSite>(() => {
  const credentials = props.provider?.credentials as Record<string, unknown> | undefined
  return credentials?.site === 'cn' ? 'cn' : 'global'
})
const qoderSiteChanged = computed(() => isQoderCosyProvider.value && qoderSite.value !== originalQoderSite.value)

const supportsOAuthLikeModelRestriction = computed(() =>
  (props.provider?.platform === 'openai' && props.provider?.type === 'oauth') ||
  (props.provider?.platform === 'grok' && props.provider?.type === 'oauth') ||
  (props.provider?.platform === 'gemini' && props.provider?.type === 'oauth') ||
  isQoderCosyProvider.value
)
const isAnthropicOAuthLikeProvider = computed(() =>
  props.provider?.platform === 'anthropic' &&
  (props.provider?.type === 'oauth' || props.provider?.type === 'setup-token')
)
const supportsTLSFingerprintRouter = computed(() =>
  props.provider?.platform === 'openai' && props.provider?.type === 'oauth'
)
const isServiceAccountProvider = computed(() =>
  (props.provider?.platform === 'gemini' || props.provider?.platform === 'anthropic') &&
  props.provider?.type === 'service_account'
)
const isOpenAIOAuthOrAPIKey = computed(() =>
  props.provider?.platform === 'openai' &&
  (props.provider?.type === 'oauth' || props.provider?.type === 'apikey')
)
// 白名单/映射在 API Key（Antigravity 除外）、OAuth 类、Vertex 和 Bedrock 账号中互斥出现，共用一个区域。
const showModelRestriction = computed(() => {
  const provider = props.provider
  if (!provider) return false
  return (provider.type === 'apikey' && provider.platform !== 'antigravity') ||
    supportsOAuthLikeModelRestriction.value ||
    isServiceAccountProvider.value ||
    provider.type === 'bedrock'
})
const showCredentialsSection = computed(() => {
  const provider = props.provider
  if (!provider) return false
  return ['apikey', 'bedrock'].includes(provider.type) ||
    isServiceAccountProvider.value ||
    (provider.platform === 'antigravity' && provider.type === 'oauth')
})
const showGeminiTier = computed(() =>
  props.provider?.platform === 'gemini' && geminiProviderType.value === 'official'
)
const apiKeyBaseUrlPlaceholder = computed(() => {
  switch (props.provider?.platform) {
    case 'jev': return 'https://api.typesafe.ai'
    case 'openai': return 'https://api.openai.com'
    case 'gemini': return 'https://generativelanguage.googleapis.com'
    case 'antigravity': return 'https://cloudcode-pa.googleapis.com'
    case 'grok': return 'https://api.x.ai/v1'
    default: return 'https://api.anthropic.com'
  }
})
const apiKeyPlaceholder = computed(() => {
  switch (props.provider?.platform) {
    case 'jev': return 'api-key-...'
    case 'openai': return 'sk-proj-...'
    case 'gemini': return geminiProviderType.value === 'third_party' ? 'api-key-...' : 'AIza...'
    case 'antigravity': return 'sk-...'
    case 'grok': return 'xai-...'
    default: return 'sk-ant-...'
  }
})
const qoderSiteOptions = computed(() => [
  { value: 'global' as QoderSite, label: t('admin.providers.qoder.site.global'), testid: 'edit-qoder-site-global' },
  { value: 'cn' as QoderSite, label: t('admin.providers.qoder.site.cn'), testid: 'edit-qoder-site-cn' }
])

// 页签按账号类型隐藏没有内容的分类，基本信息、调度与请求协议始终存在。
const tabsRef = ref<InstanceType<typeof SettingsTabs> | null>(null)
const formTabs = computed(() => {
  const provider = props.provider
  const hasQuota = !!provider && (
    provider.type === 'apikey' ||
    provider.type === 'bedrock' ||
    isAnthropicOAuthLikeProvider.value ||
    !!provider.ollama_cloud_usage?.eligible
  )
  const hasModels = showModelRestriction.value || provider?.platform === 'antigravity'
  return [
    { key: 'basic', label: t('admin.providers.tabs.basic') },
    { key: 'models', label: t('admin.providers.tabs.models'), hidden: !hasModels },
    { key: 'scheduling', label: t('admin.providers.tabs.scheduling') },
    { key: 'quota', label: t('admin.providers.tabs.quota'), hidden: !hasQuota },
    { key: 'request', label: t('admin.providers.tabs.request') }
  ]
})

// 业务校验仍用 toast 提示，同时切到字段所在页签并聚焦。
function failAt(message: string, field: string) {
  appStore.showError(message)
  void tabsRef.value?.revealField(`[data-provider-field="${field}"]`)
}

// Load global feature states once
adminAPI.settings.getWebSearchEmulationConfig().then(cfg => {
  webSearchGlobalEnabled.value = cfg?.enabled === true && (cfg?.providers?.length ?? 0) > 0
}).catch(() => { webSearchGlobalEnabled.value = false })

loadQuotaNotifyGlobal()
const editQuotaLimit = ref<number | null>(null)
const editQuotaDailyLimit = ref<number | null>(null)
const editQuotaWeeklyLimit = ref<number | null>(null)
const editDailyResetMode = ref<'rolling' | 'fixed' | null>(null)
const editDailyResetHour = ref<number | null>(null)
const editWeeklyResetMode = ref<'rolling' | 'fixed' | null>(null)
const editWeeklyResetDay = ref<number | null>(null)
const editWeeklyResetHour = ref<number | null>(null)
const editResetTimezone = ref<string | null>(null)
const { limits: quotaLimits, setLimit: setQuotaLimit } = bindQuotaLimits({
  totalLimit: editQuotaLimit,
  dailyLimit: editQuotaDailyLimit,
  weeklyLimit: editQuotaWeeklyLimit,
  dailyResetMode: editDailyResetMode,
  dailyResetHour: editDailyResetHour,
  weeklyResetMode: editWeeklyResetMode,
  weeklyResetDay: editWeeklyResetDay,
  weeklyResetHour: editWeeklyResetHour,
  resetTimezone: editResetTimezone
})
const codexFingerprintModeOptions = useCodexFingerprintModeOptions()
// OpenAI 订阅档位手动覆盖选项(清空 + Plus/Pro/Free;别名/自定义值友好显示且保留 canonical)
const planTypeOptions = computed(() =>
  buildPlanTypeOptions(editPlanType.value, t('admin.providers.openai.planTypeClear'))
)
const openAIOAuthClientPolicyOptions = useOpenAIOAuthClientPolicyOptions()

// Computed: default base URL based on platform
const defaultBaseUrl = computed(() => {
  if (props.provider?.platform === 'openai') return 'https://api.openai.com'
  if (props.provider?.platform === 'gemini') return 'https://generativelanguage.googleapis.com'
  if (props.provider?.platform === 'grok') return 'https://api.x.ai/v1'
  // CN 供应商：按当前模式/协议回落到官方预设（清空输入框提交时使用），
  // 不能落到 anthropic 默认值（会被当 CC base 拼出错误端点）。
  if (
    props.provider?.platform === 'kimi' ||
    props.provider?.platform === 'zhipu' ||
    props.provider?.platform === 'deepseek'
  ) {
    return defaultCNBaseUrl(props.provider.platform, editProviderMode.value, editApiProtocol.value)
  }
  return 'https://api.anthropic.com'
})

watch(geminiProviderType, (providerType) => {
  if (props.provider?.platform !== 'gemini' || providerType !== 'third_party') return
  // 切换到第三方来源时，不能把官方默认端点误当成第三方地址提交。
  if (!isGeminiThirdPartyBaseUrl(editBaseUrl.value)) {
    editBaseUrl.value = ''
  }
})

const form = reactive({
  name: '',
  notes: '',
  proxy_id: null as number | null,
  concurrency: 1,
  load_factor: null as number | null,
  priority: 1,
  rate_multiplier: 1,
  status: 'active' as 'active' | 'inactive' | 'error',
  group_ids: [] as number[],
  expires_at: null as number | null
})

const statusOptions = computed(() => {
  const options = [
    { value: 'active', label: t('common.active') },
    { value: 'inactive', label: t('common.inactive') }
  ]
  if (form.status === 'error') {
    options.push({ value: 'error', label: t('admin.providers.status.error') })
  }
  return options
})
const vertexLocationOptions = groupedProviderSelectOptions(VERTEX_LOCATION_OPTIONS)

const expiresAtInput = computed({
  get: () => formatDateTimeLocal(form.expires_at),
  set: (value: string) => {
    form.expires_at = parseDateTimeLocal(value)
  }
})

// Watchers

const hydrateModelRestrictionFromMapping = (
  existingMappings?: Record<string, string>,
  rawWhitelist?: unknown
) => {
  const parsed = splitPersistedModelRestriction(existingMappings, rawWhitelist)
  allowedModels.value = parsed.allowedModels
  modelMappings.value = parsed.modelMappings
  modelRestrictionMode.value = parsed.modelMappings.length > 0 ? 'mapping' : 'whitelist'
}

const hasConfiguredModelRestriction = (
  existingMappings?: Record<string, string>,
  rawWhitelist?: unknown
) =>
  (!!existingMappings && typeof existingMappings === 'object' && Object.keys(existingMappings).length > 0) ||
  Array.isArray(rawWhitelist)

const hydrateQoderModelRestrictionFromMapping = (
  existingMappings?: Record<string, string>,
  rawWhitelist?: unknown
) => {
  qoderModelRestrictionConfigured.value = hasConfiguredModelRestriction(existingMappings, rawWhitelist)
  qoderModelWhitelistConfigured.value = Array.isArray(rawWhitelist)
  qoderModelRestrictionTouched.value = false
  qoderModelWhitelistTouched.value = false
  if (!qoderModelRestrictionConfigured.value) {
    allowedModels.value = []
    modelMappings.value = []
    modelRestrictionMode.value = 'mapping'
    return
  }

  const parsed = splitQoderPersistedModelRestriction(existingMappings, rawWhitelist)
  allowedModels.value = parsed.allowedModels
  modelMappings.value = parsed.modelMappings
  modelRestrictionMode.value = modelMappings.value.length > 0 ? 'mapping' : 'whitelist'
}

const applyPersistedModelRestriction = (credentials: Record<string, unknown>) => {
  // 普通提供商将请求侧映射与最终白名单分开持久化。
  // 空白名单也写入 []，后端据此使用空列表；字段缺失时后端会解析自映射白名单。
  const persisted = buildPersistedModelRestriction(allowedModels.value, modelMappings.value)
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  credentials.model_whitelist = persisted.modelWhitelist
}

const applyQoderModelRestriction = (credentials: Record<string, unknown>) => {
  if (!qoderModelRestrictionConfigured.value && !qoderModelRestrictionTouched.value) {
    delete credentials.model_mapping
    delete credentials.model_whitelist
    return
  }

  const persisted = buildPersistedModelRestriction(
    qoderModelWhitelistConfigured.value || qoderModelWhitelistTouched.value ? allowedModels.value : [],
    modelMappings.value
  )
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  credentials.model_whitelist = persisted.modelWhitelist
}

const applyOpenAIModelMappingCredentials = (credentials: Record<string, unknown>) => {
  const shouldApplyModelMapping = true

  if (shouldApplyModelMapping) {
    if (isSparkShadow.value) {
      // Spark 影子提供商持久化请求侧映射。
      const modelMapping = buildModelMappingObject(modelRestrictionMode.value, allowedModels.value, modelMappings.value)
      if (modelMapping) {
        credentials.model_mapping = modelMapping
      } else {
        delete credentials.model_mapping
      }
    } else {
      // OpenAI OAuth 与普通提供商一样，将请求映射和最终白名单分开保存。
      applyPersistedModelRestriction(credentials)
    }
  } else if (!credentials.model_mapping) {
    delete credentials.model_mapping
  }

  const compactModelMapping = buildModelMappingObject('mapping', [], openAICompactModelMappings.value)
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  } else {
    delete credentials.compact_model_mapping
  }
}

const syncFormFromProvider = (newProvider: Provider | null) => {
  upstreamProtocols.value = Array.isArray(newProvider?.credentials?.upstream_protocols) ? [...newProvider.credentials.upstream_protocols] as ProtocolID[] : undefined

  if (!newProvider) {
    return
  }
  // 进入回填窗口：抑制 CN 模式/协议 watcher 联动重置 base_url（见 syncingForm 注释）。
  syncingForm.value = true
  void nextTick(() => {
    syncingForm.value = false
  })
  form.name = newProvider.name
  form.notes = newProvider.notes || ''
  form.proxy_id = newProvider.proxy_id
  form.concurrency = newProvider.concurrency
  form.load_factor = newProvider.load_factor ?? null
  form.priority = newProvider.priority
  form.rate_multiplier = newProvider.rate_multiplier ?? 1
  form.status = (newProvider.status === 'active' || newProvider.status === 'inactive' || newProvider.status === 'error')
    ? newProvider.status
    : 'active'
  form.group_ids = newProvider.group_ids || []
  form.expires_at = newProvider.expires_at ?? null

  // Load intercept warmup requests setting (applies to all provider types)
  const credentials = newProvider.credentials as Record<string, unknown> | undefined
  editZhipuOrganization.value = ''
  editZhipuProject.value = ''
  geminiProviderType.value = 'official'
  geminiAIStudioTier.value = 'aistudio_free'
  if (newProvider.platform === 'gemini' && newProvider.type === 'apikey') {
    geminiProviderType.value = credentials?.provider_type === 'third_party' ? 'third_party' : 'official'
    if (credentials?.tier_id === 'aistudio_paid') {
      geminiAIStudioTier.value = 'aistudio_paid'
    }
  }
  qoderSite.value = newProvider.platform === 'qoder' && credentials?.site === 'cn' ? 'cn' : 'global'
  interceptWarmupRequests.value = credentials?.intercept_warmup_requests === true
  autoPauseOnExpired.value = newProvider.auto_pause_on_expired === true
  editVertexProjectId.value = ''
  editVertexClientEmail.value = ''
  editVertexLocation.value = 'us-central1'

  // Load mixed scheduling setting (only for antigravity providers)
  allowOverages.value = false
  const extra = newProvider.extra as Record<string, unknown> | undefined
  upstreamUsageEnabled.value = newProvider.platform !== 'jev'
  upstreamUsageAdapter.value = 'sub2api'
  upstreamUsageBaseUrl.value = ''
  upstreamUsageWalletAccessToken.value = ''
  upstreamUsageWalletUserId.value = ''
  if (newProvider.type === 'apikey') {
    const rawUsageConfig = extra?.upstream_usage_query as Record<string, unknown> | undefined
    if (rawUsageConfig && typeof rawUsageConfig === 'object') {
      upstreamUsageEnabled.value = typeof rawUsageConfig.enabled === 'boolean' ? rawUsageConfig.enabled : newProvider.platform !== 'jev'
      if (rawUsageConfig.adapter === 'new_api' || rawUsageConfig.adapter === 'zivv') {
        upstreamUsageAdapter.value = rawUsageConfig.adapter
      }
      if (typeof rawUsageConfig.base_url === 'string') upstreamUsageBaseUrl.value = rawUsageConfig.base_url
    }
    if (typeof credentials?.new_api_user_id === 'string' || typeof credentials?.new_api_user_id === 'number') {
      upstreamUsageWalletUserId.value = String(credentials.new_api_user_id)
    }
  }
  allowOverages.value = extra?.allow_overages === true
  autoPause5hThreshold.value = typeof extra?.auto_pause_5h_threshold === 'number' ? extra.auto_pause_5h_threshold * 100 : null
  autoPause7dThreshold.value = typeof extra?.auto_pause_7d_threshold === 'number' ? extra.auto_pause_7d_threshold * 100 : null
  autoPause5hDisabled.value = extra?.auto_pause_5h_disabled === true
  autoPause7dDisabled.value = extra?.auto_pause_7d_disabled === true
	upstreamRequestIdHeader.value = readUpstreamRequestIdHeader(extra)

  // 加载 OpenAI OAuth、SetupToken 和 API Key 提供商的透传设置。
  openaiPassthroughEnabled.value = false
  openaiFlattenNamespacesEnabled.value = false
  editPlanType.value = ''
  openAICompactMode.value = 'force_on'
  openAINativeCompactionV2Mode.value = 'force_on'
  openAIResponsesContinuationSupported.value = false
  openAIImagesURLToB64JSON.value = false
  openAICompactModelMappings.value = []
  responsesWSConnectionMode.value = RESPONSES_WS_POOLED
  codexCLIOnlyAllowClaudeCodeEnabled.value = false
  openAIOAuthClientPolicy.value = 'any'
  codexFingerprintMode.value = 'off'
  codexImageToolMode.value = 'inherit'
  anthropicPassthroughEnabled.value = false
  anthropicAPIKeyAuthScheme.value = 'x_api_key'
  webSearchEmulationMode.value = 'default'
  if (newProvider.platform === 'openai' && (newProvider.type === 'oauth' || newProvider.type === 'setup-token' || newProvider.type === 'apikey')) {
    openaiPassthroughEnabled.value = extra?.openai_passthrough === true || extra?.openai_oauth_passthrough === true
    openaiFlattenNamespacesEnabled.value =
      newProvider.type === 'oauth' && extra?.openai_responses_flatten_namespaces === true
    // plan_type 手动覆盖仅 OAuth 有实际调度语义(IsOpenAIChatGPTSubscription 要求 oauth),故只对 oauth 回填
    editPlanType.value = newProvider.type === 'oauth'
      ? readPlanType(newProvider.credentials as Record<string, unknown> | undefined)
      : ''
    openAICompactMode.value = normalizeOpenAICompactMode(extra?.openai_compact_mode)
    openAINativeCompactionV2Mode.value = normalizeOpenAICompactMode(extra?.openai_native_compaction_v2_mode)
    if (newProvider.type === 'apikey') {
      openAIResponsesContinuationSupported.value = extra?.openai_responses_continuation_supported === true
      openAIImagesURLToB64JSON.value = extra?.images_url_to_b64_json === true
    }
    codexImageToolMode.value = readCodexImageToolMode(extra)
    responsesWSConnectionMode.value = readResponsesWSConnectionMode(extra)
    if (newProvider.type === 'oauth') {
      openAIOAuthClientPolicy.value = normalizeOpenAIOAuthClientPolicy(extra?.openai_oauth_client_policy, extra?.codex_cli_only)
      codexCLIOnlyAllowClaudeCodeEnabled.value =
        Array.isArray(extra?.codex_cli_only_allowed_clients) &&
        (extra.codex_cli_only_allowed_clients as unknown[]).includes('claude_code')
    }
    if (newProvider.type === 'oauth') {
      const fpMode = extra?.codex_fingerprint_mode as string | undefined
      codexFingerprintMode.value = (['off', 'device', 'session', 'full'].includes(fpMode || '')
        ? fpMode as CodexFingerprintMode
        : 'off')
    }
    const credentials = newProvider.credentials as Record<string, unknown> | undefined
    const compactMappings = credentials?.compact_model_mapping as Record<string, string> | undefined
    if (compactMappings && typeof compactMappings === 'object') {
      openAICompactModelMappings.value = Object.entries(compactMappings).map(([from, to]) => ({ from, to }))
    }
  }
  if (newProvider.platform === 'anthropic' && newProvider.type === 'apikey') {
    anthropicPassthroughEnabled.value = extra?.anthropic_passthrough === true
    anthropicAPIKeyAuthScheme.value = extra?.anthropic_apikey_auth_scheme === 'authorization_bearer'
      ? 'authorization_bearer'
      : 'x_api_key'
    // 三态：string "default"/"enabled"/"disabled"，向后兼容旧 bool
    const wsVal = extra?.web_search_emulation
    if (wsVal === 'enabled' || wsVal === 'disabled') {
      webSearchEmulationMode.value = wsVal
    } else if (wsVal === true) {
      webSearchEmulationMode.value = 'enabled'
    } else {
      webSearchEmulationMode.value = 'default'
    }
  }

  // Load quota limit for apikey/bedrock providers (bedrock quota is also loaded in its own branch above)
  if (newProvider.type === 'apikey' || newProvider.type === 'bedrock') {
    const quotaVal = extra?.quota_limit as number | undefined
    editQuotaLimit.value = (quotaVal && quotaVal > 0) ? quotaVal : null
    const dailyVal = extra?.quota_daily_limit as number | undefined
    editQuotaDailyLimit.value = (dailyVal && dailyVal > 0) ? dailyVal : null
    const weeklyVal = extra?.quota_weekly_limit as number | undefined
    editQuotaWeeklyLimit.value = (weeklyVal && weeklyVal > 0) ? weeklyVal : null
    // Load quota reset mode config
    editDailyResetMode.value = (extra?.quota_daily_reset_mode as 'rolling' | 'fixed') || null
    editDailyResetHour.value = (extra?.quota_daily_reset_hour as number) ?? null
    editWeeklyResetMode.value = (extra?.quota_weekly_reset_mode as 'rolling' | 'fixed') || null
    editWeeklyResetDay.value = (extra?.quota_weekly_reset_day as number) ?? null
    editWeeklyResetHour.value = (extra?.quota_weekly_reset_hour as number) ?? null
    editResetTimezone.value = (extra?.quota_reset_timezone as string) || null
    // Load quota notify config
    loadQuotaNotifyFromExtra(extra)
  } else {
    editQuotaLimit.value = null
    editQuotaDailyLimit.value = null
    editQuotaWeeklyLimit.value = null
    editDailyResetMode.value = null
    editDailyResetHour.value = null
    editWeeklyResetMode.value = null
    editWeeklyResetDay.value = null
    editWeeklyResetHour.value = null
    editResetTimezone.value = null
    resetQuotaNotify()
  }

  // Load antigravity model mapping (Antigravity 只支持映射模式)
  if (newProvider.platform === 'antigravity') {
    const credentials = newProvider.credentials as Record<string, unknown> | undefined
    const configuredProjectID = credentials?.[ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY]
    antigravityProjectId.value = typeof configuredProjectID === 'string' ? configuredProjectID : ''

    // Antigravity 始终使用映射模式
    antigravityModelRestrictionMode.value = 'mapping'
    antigravityWhitelistModels.value = Array.isArray(credentials?.model_whitelist) ? [...credentials.model_whitelist as string[]] : []

    // 从 model_mapping 读取映射配置
    const rawAgMapping = credentials?.model_mapping as Record<string, string> | undefined
    if (rawAgMapping && typeof rawAgMapping === 'object') {
      const entries = Object.entries(rawAgMapping)
      // 自映射（key===value）和不同模型之间的映射都转换为映射列表。
      antigravityModelMappings.value = entries.map(([from, to]) => ({ from, to }))
    } else {
      // 兼容旧数据：从 model_whitelist 读取，转换为映射格式
      const rawWhitelist = credentials?.model_whitelist
      if (Array.isArray(rawWhitelist) && rawWhitelist.length > 0) {
        antigravityModelMappings.value = rawWhitelist
          .map((v) => String(v).trim())
          .filter((v) => v.length > 0)
          .map((m) => ({ from: m, to: m }))
      } else {
        antigravityModelMappings.value = []
      }
    }
  } else {
    antigravityProjectId.value = ''
    antigravityModelRestrictionMode.value = 'mapping'
    antigravityWhitelistModels.value = []
    antigravityModelMappings.value = []
  }

  // Load quota control settings (Anthropic OAuth/SetupToken only)
  loadQuotaControlSettings(newProvider)

  loadTempUnschedRules(credentials)
  loadProviderSchedulingThresholdOverride(newProvider.platform, credentials)

  // 加载支持的平台 API Key 与 Grok API Key/OAuth 提供商的请求头覆写状态。
  headerOverrideEnabled.value = false
  headerOverrideRows.value = []
  if (newProvider.credentials && isHeaderOverrideCapable(newProvider.platform, newProvider.type)) {
    const overrideCreds = newProvider.credentials as Record<string, unknown>
    headerOverrideEnabled.value = overrideCreds[HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY] === true
    headerOverrideRows.value = splitHeaderOverridesObject(
      overrideCreds[HEADER_OVERRIDES_CREDENTIAL_KEY]
    )
  }

  // 加载 Grok OAuth 自定义上游地址状态（存储的官方地址视同未定制）。
  grokOAuthCustomBaseUrlEnabled.value = false
  grokOAuthBaseUrl.value = ''
  const grokClientToolCacheSetting =
    newProvider.platform === 'grok' && newProvider.type === 'oauth'
      ? newProvider.extra?.[GROK_CLIENT_TOOL_CACHE_EXTRA_KEY]
      : undefined
  grokClientToolCacheEnabled.value =
    newProvider.platform === 'grok' &&
    newProvider.type === 'oauth' &&
    (grokClientToolCacheSetting === undefined || grokClientToolCacheSetting === true)
  if (newProvider.platform === 'grok' && newProvider.type === 'oauth' && newProvider.credentials) {
    const grokCreds = newProvider.credentials as Record<string, unknown>
    if (isCustomGrokBaseUrl(grokCreds.base_url)) {
      grokOAuthCustomBaseUrlEnabled.value = true
      grokOAuthBaseUrl.value = (grokCreds.base_url as string).trim()
    }
  }

  // Initialize API Key fields for apikey type
  if (newProvider.type === 'apikey' && newProvider.credentials) {
    const credentials = newProvider.credentials as Record<string, unknown>
    // 国产供应商：读取 provider_mode 与 api_protocol 作为可编辑初始值
    // （编辑弹窗允许修正两者，用于修复早期存错默认值的提供商）。
    if (newProvider.platform === 'kimi' || newProvider.platform === 'zhipu' || newProvider.platform === 'deepseek') {
      editProviderMode.value = credentials.provider_mode === 'coding' ? 'coding' : 'payg'
      const storedProtocol = credentials.upstream_protocols !== undefined ? 'adaptive' : credentials.api_protocol
      editApiProtocol.value =
        storedProtocol === 'adaptive' ||
        storedProtocol === 'chat_completions' ||
        storedProtocol === 'anthropic' ||
        storedProtocol === 'responses'
          ? storedProtocol
          : 'chat_completions'
      if (!cnSupportsNativeResponses(newProvider.platform) && editApiProtocol.value === 'responses') {
        editApiProtocol.value = 'chat_completions'
      }
      const adaptiveDefaults = defaultCNAdaptiveBaseUrls(newProvider.platform, editProviderMode.value)
      const storedBaseUrls = (credentials.api_base_urls as Record<string, unknown> | undefined) || {}
      const legacyBaseUrl = typeof credentials.base_url === 'string' ? credentials.base_url.trim() : ''
      const storedChatBaseUrl = typeof storedBaseUrls.chat_completions === 'string'
        ? storedBaseUrls.chat_completions.trim()
        : ''
      const storedAnthropicBaseUrl = typeof storedBaseUrls.anthropic === 'string'
        ? storedBaseUrls.anthropic.trim()
        : ''
      const storedResponsesBaseUrl = typeof storedBaseUrls.responses === 'string'
        ? storedBaseUrls.responses.trim()
        : ''
      const nextAdaptiveBaseUrls: Record<CnNativeApiProtocol, string> = {
        chat_completions: storedChatBaseUrl || adaptiveDefaults.chat_completions,
        anthropic: storedAnthropicBaseUrl || adaptiveDefaults.anthropic,
        responses: storedResponsesBaseUrl || adaptiveDefaults.responses
      }
      const legacyProtocol: CnNativeApiProtocol = editApiProtocol.value === 'anthropic'
        ? 'anthropic'
        : editApiProtocol.value === 'responses'
          ? 'responses'
          : 'chat_completions'
      const storedLegacyBaseUrl = legacyProtocol === 'anthropic'
        ? storedAnthropicBaseUrl
        : legacyProtocol === 'responses'
          ? storedResponsesBaseUrl
          : storedChatBaseUrl
      if (legacyBaseUrl && !storedLegacyBaseUrl) {
        nextAdaptiveBaseUrls[legacyProtocol] = legacyBaseUrl
      }
      editAdaptiveBaseUrls.value = nextAdaptiveBaseUrls
      if (newProvider.platform === 'zhipu') {
        editZhipuOrganization.value = typeof credentials.zhipu_organization === 'string'
          ? credentials.zhipu_organization
          : ''
        editZhipuProject.value = typeof credentials.zhipu_project === 'string'
          ? credentials.zhipu_project
          : ''
      }
    }
    const platformDefaultUrl =
      newProvider.platform === 'openai'
        ? 'https://api.openai.com'
        : newProvider.platform === 'gemini'
          ? 'https://generativelanguage.googleapis.com'
          : newProvider.platform === 'grok'
            ? 'https://api.x.ai/v1'
            : newProvider.platform === 'kimi' ||
                newProvider.platform === 'zhipu' ||
                newProvider.platform === 'deepseek'
              ? defaultCNBaseUrl(newProvider.platform, editProviderMode.value, editApiProtocol.value)
              : 'https://api.anthropic.com'
    editBaseUrl.value = isCNApiKeyProvider.value && editApiProtocol.value === 'adaptive'
      ? editAdaptiveBaseUrls.value.chat_completions
      : (credentials.base_url as string) || platformDefaultUrl

    // 从 model_mapping 恢复白名单与映射视图。
    const existingMappings = credentials.model_mapping as Record<string, string> | undefined
    hydrateModelRestrictionFromMapping(existingMappings, credentials.model_whitelist)

    // Load pool mode
    poolModeEnabled.value = credentials.pool_mode === true
    poolModeRetryCount.value = normalizePoolModeRetryCount(
      Number(credentials.pool_mode_retry_count ?? DEFAULT_POOL_MODE_RETRY_COUNT)
    )
    poolModeRetryStatusCodesInput.value = formatPoolModeRetryStatusCodes(credentials.pool_mode_retry_status_codes)

    // Load custom error codes
    customErrorCodesEnabled.value = credentials.custom_error_codes_enabled === true
    const existingErrorCodes = credentials.custom_error_codes as number[] | undefined
    if (existingErrorCodes && Array.isArray(existingErrorCodes)) {
      selectedErrorCodes.value = [...existingErrorCodes]
    } else {
      selectedErrorCodes.value = []
    }

  } else if (newProvider.type === 'bedrock' && newProvider.credentials) {
    const bedrockCreds = newProvider.credentials as Record<string, unknown>
    const authMode = (bedrockCreds.auth_mode as string) || 'sigv4'
    editBedrockRegion.value = (bedrockCreds.aws_region as string) || ''
    editBedrockForceGlobal.value = (bedrockCreds.aws_force_global as string) === 'true'

    if (authMode === 'apikey') {
      editBedrockApiKeyValue.value = ''
    } else {
      editBedrockAccessKeyId.value = (bedrockCreds.aws_access_key_id as string) || ''
      editBedrockSecretAccessKey.value = ''
      editBedrockSessionToken.value = ''
    }

    // Load pool mode for bedrock
    poolModeEnabled.value = bedrockCreds.pool_mode === true
    const retryCount = bedrockCreds.pool_mode_retry_count
    poolModeRetryCount.value = (typeof retryCount === 'number' && retryCount >= 0) ? retryCount : DEFAULT_POOL_MODE_RETRY_COUNT
    poolModeRetryStatusCodesInput.value = formatPoolModeRetryStatusCodes(bedrockCreds.pool_mode_retry_status_codes)

    // Load quota limits for bedrock
    const bedrockExtra = (newProvider.extra as Record<string, unknown>) || {}
    editQuotaLimit.value = typeof bedrockExtra.quota_limit === 'number' ? bedrockExtra.quota_limit : null
    editQuotaDailyLimit.value = typeof bedrockExtra.quota_daily_limit === 'number' ? bedrockExtra.quota_daily_limit : null
    editQuotaWeeklyLimit.value = typeof bedrockExtra.quota_weekly_limit === 'number' ? bedrockExtra.quota_weekly_limit : null
    // Load quota notify for bedrock
    loadQuotaNotifyFromExtra(bedrockExtra)

    // Load model mappings for bedrock
    const existingMappings = bedrockCreds.model_mapping as Record<string, string> | undefined
    hydrateModelRestrictionFromMapping(existingMappings, bedrockCreds.model_whitelist)
  } else if ((newProvider.platform === 'gemini' || newProvider.platform === 'anthropic') && newProvider.type === 'service_account' && newProvider.credentials) {
    const credentials = newProvider.credentials as Record<string, unknown>
    editVertexProjectId.value = (credentials.project_id as string) || ''
    editVertexClientEmail.value = (credentials.client_email as string) || ''
    editVertexLocation.value = (credentials.location as string) || (credentials.vertex_location as string) || 'us-central1'

    // 服务账号也分别回显请求映射与最终白名单。
    hydrateModelRestrictionFromMapping(credentials.model_mapping as Record<string, string> | undefined, credentials.model_whitelist)

  } else {
    const platformDefaultUrl =
      newProvider.platform === 'openai'
        ? 'https://api.openai.com'
        : newProvider.platform === 'gemini'
          ? 'https://generativelanguage.googleapis.com'
          : newProvider.platform === 'grok'
            ? 'https://api.x.ai/v1'
            : 'https://api.anthropic.com'
    editBaseUrl.value = platformDefaultUrl

    // 加载 OpenAI/Grok OAuth 和 Qoder COSY 提供商的模型映射。
    if (
      ((newProvider.platform === 'openai' && newProvider.type === 'oauth') ||
        (newProvider.platform === 'grok' && newProvider.type === 'oauth') ||
        (newProvider.platform === 'gemini' && newProvider.type === 'oauth') ||
        (newProvider.platform === 'qoder' && newProvider.type === 'cosy')) &&
      newProvider.credentials
    ) {
      const oauthCredentials = newProvider.credentials as Record<string, unknown>
      const existingMappings = oauthCredentials.model_mapping as Record<string, string> | undefined
      if (newProvider.platform === 'qoder') {
        hydrateQoderModelRestrictionFromMapping(existingMappings, oauthCredentials.model_whitelist)
      } else {
        hydrateModelRestrictionFromMapping(existingMappings, oauthCredentials.model_whitelist)
      }
    } else {
      hydrateModelRestrictionFromMapping()
    }
    poolModeEnabled.value = false
    poolModeRetryCount.value = DEFAULT_POOL_MODE_RETRY_COUNT
    poolModeRetryStatusCodesInput.value = ''
    customErrorCodesEnabled.value = false
    selectedErrorCodes.value = []
  }
  editApiKey.value = ''
}

async function loadTLSProfiles() {
  try {
    const profiles = await adminAPI.tlsFingerprintProfiles.list()
    tlsFingerprintProfiles.value = profiles.map(p => ({ id: p.id, name: p.name }))
  } catch {
    tlsFingerprintProfiles.value = []
  }
}

async function loadTLSRouters() {
  try {
    const routers = await adminAPI.tlsFingerprintRouters.list()
    tlsFingerprintRouters.value = routers.map(router => ({ id: router.id, name: router.name }))
  } catch {
    tlsFingerprintRouters.value = []
  }
}

const normalizeOpenAIOAuthClientPolicy = (policy: unknown, legacyCodexOnly?: unknown): OpenAIOAuthClientPolicy => {
  if (policy === 'codex_only' || policy === 'tls_router_matched_only' || policy === 'any') {
    return policy
  }
  return legacyCodexOnly === true ? 'codex_only' : 'any'
}

watch(
  [() => props.show, () => props.provider],
  ([show, newProvider], [wasShow, previousProvider]) => {
    if (!show || !newProvider) {
      return
    }
    if (!wasShow || newProvider !== previousProvider) {
      syncFormFromProvider(newProvider)
      loadTLSProfiles()
      loadTLSRouters()
    }
  },
  { immediate: true }
)

// Model mapping helpers
const touchQoderModelRestriction = () => {
  if (isQoderCosyProvider.value) {
    qoderModelRestrictionTouched.value = true
  }
}

const setAllowedModels = (models: string[]) => {
  touchQoderModelRestriction()
  if (isQoderCosyProvider.value) {
    qoderModelWhitelistTouched.value = true
  }
  allowedModels.value = models
}

const syncAntigravityUpstreamModels = async () => {
  if (!props.provider?.id || isSyncingAntigravityUpstream.value) return

  isSyncingAntigravityUpstream.value = true
  try {
    const result = await adminAPI.providers.syncUpstreamModels(props.provider.id)
    const upstreamModels = result.models.map((model) => model.trim()).filter(Boolean)
    if (upstreamModels.length === 0) {
      appStore.showInfo(t('admin.providers.syncUpstreamModelsEmpty'))
      return
    }

    let addedCount = 0
    for (const model of upstreamModels) {
      const exists = antigravityModelMappings.value.some((mapping) => mapping.from === model)
      if (!exists) {
        antigravityModelMappings.value.push({ from: model, to: model })
        addedCount += 1
      }
    }

    if (addedCount > 0) {
      appStore.showSuccess(t('admin.providers.syncUpstreamModelsSuccess', { count: addedCount, total: upstreamModels.length }))
    } else {
      appStore.showInfo(t('admin.providers.syncUpstreamModelsNoChanges', { count: upstreamModels.length }))
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : t('admin.providers.syncUpstreamModelsFailed')
    appStore.showError(t('admin.providers.syncUpstreamModelsError', { message }))
  } finally {
    isSyncingAntigravityUpstream.value = false
  }
}

const buildTempUnschedRules = (rules: TempUnschedRuleForm[]) => {
  const out: Array<{
    error_code: number
    keywords: string[]
    duration_minutes: number
    description: string
  }> = []

  for (const rule of rules) {
    const errorCode = Number(rule.error_code)
    const duration = Number(rule.duration_minutes)
    const keywords = splitTempUnschedKeywords(rule.keywords)
    if (!Number.isFinite(errorCode) || errorCode < 100 || errorCode > 599) {
      continue
    }
    if (!Number.isFinite(duration) || duration <= 0) {
      continue
    }
    if (keywords.length === 0) {
      continue
    }
    out.push({
      error_code: Math.trunc(errorCode),
      keywords,
      duration_minutes: Math.trunc(duration),
      description: rule.description.trim()
    })
  }

  return out
}

const applyTempUnschedConfig = (credentials: Record<string, unknown>) => {
  if (!tempUnschedEnabled.value) {
    delete credentials.temp_unschedulable_enabled
    delete credentials.temp_unschedulable_rules
    return true
  }

  const rules = buildTempUnschedRules(tempUnschedRules.value)
  if (rules.length === 0) {
    failAt(t('admin.providers.tempUnschedulable.rulesInvalid'), 'temp-unsched')
    return false
  }

  credentials.temp_unschedulable_enabled = true
  credentials.temp_unschedulable_rules = rules
  return true
}

function supportsProviderSchedulingThresholdOverridePlatform(
  platform: Provider['platform'] | undefined
) {
  return platform === 'openai' || platform === 'anthropic' || platform === 'grok'
}

function normalizeProviderSchedulingThresholdOverride(value: unknown): number | null {
  if (value === null || value === undefined || value === '') {
    return null
  }
  const numeric = Number(value)
  if (!Number.isFinite(numeric)) {
    return null
  }
  const integer = Math.trunc(numeric)
  return integer >= 1 && integer <= 100 ? integer : null
}

function clampProviderSchedulingThresholdOverride(value: unknown): number {
  return Math.min(100, Math.max(1, Math.trunc(Number(value) || 100)))
}

// loadProviderSchedulingThresholdOverride 从提供商凭据恢复单提供商阈值覆盖。
function loadProviderSchedulingThresholdOverride(
  platform: Provider['platform'] | undefined,
  credentials: Record<string, unknown> | undefined
) {
  if (!supportsProviderSchedulingThresholdOverridePlatform(platform)) {
    providerSchedulingThresholdOverrideEnabled.value = false
    providerSchedulingThresholdOverrideValue.value = 100
    return
  }
  const value = normalizeProviderSchedulingThresholdOverride(
    credentials?.[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY]
  )
  providerSchedulingThresholdOverrideEnabled.value = value !== null
  providerSchedulingThresholdOverrideValue.value = value ?? 100
}

// applyProviderSchedulingThresholdOverridePatch 仅在值变化时写入凭据补丁。
const applyProviderSchedulingThresholdOverridePatch = (
  credentials: Record<string, unknown>,
  currentCredentials: Record<string, unknown>,
  platform: Provider['platform'] | undefined = props.provider?.platform
) => {
  if (!supportsProviderSchedulingThresholdOverridePlatform(platform)) {
    return
  }
  const current = normalizeProviderSchedulingThresholdOverride(
    currentCredentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY]
  )
  if (!providerSchedulingThresholdOverrideEnabled.value) {
    if (current !== null) {
      credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] = null
    }
    return
  }
  const next = clampProviderSchedulingThresholdOverride(
    providerSchedulingThresholdOverrideValue.value
  )
  if (current !== next) {
    credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] = next
  }
}

const applyTLSFingerprintExtra = (extra: Record<string, unknown>) => {
  if (tlsFingerprintEnabled.value) {
    extra.enable_tls_fingerprint = true
    if (tlsFingerprintProfileId.value) {
      extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
    } else {
      delete extra.tls_fingerprint_profile_id
    }
    if (supportsTLSFingerprintRouter.value && tlsFingerprintRouterId.value) {
      extra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
    } else {
      delete extra.tls_fingerprint_router_id
    }
  } else {
    delete extra.enable_tls_fingerprint
    delete extra.tls_fingerprint_profile_id
    delete extra.tls_fingerprint_router_id
  }
}

function loadTempUnschedRules(credentials?: Record<string, unknown>) {
  tempUnschedEnabled.value = credentials?.temp_unschedulable_enabled === true
  const rawRules = credentials?.temp_unschedulable_rules
  if (!Array.isArray(rawRules)) {
    tempUnschedRules.value = []
    return
  }

  tempUnschedRules.value = rawRules.map((rule) => {
    const entry = rule as Record<string, unknown>
    return {
      error_code: toPositiveNumber(entry.error_code),
      keywords: formatTempUnschedKeywords(entry.keywords),
      duration_minutes: toPositiveNumber(entry.duration_minutes),
      description: typeof entry.description === 'string' ? entry.description : ''
    }
  })
}

// 从提供商加载配额控制配置（TLS 支持 OpenAI OAuth，其余配额控制仍仅限 Anthropic）
function loadQuotaControlSettings(provider: Provider) {
  // Reset all quota control state first
  windowCostEnabled.value = false
  windowCostLimit.value = null
  windowCostStickyReserve.value = null
  sessionLimitEnabled.value = false
  maxSessions.value = null
  sessionIdleTimeout.value = null
  rpmLimitEnabled.value = false
  baseRpm.value = null
  rpmStrategy.value = 'tiered'
  rpmStickyBuffer.value = null
  userMsgQueueMode.value = ''
  tlsFingerprintEnabled.value = false
  tlsFingerprintProfileId.value = null
  tlsFingerprintRouterId.value = null
  sessionIdMaskingEnabled.value = false
  cacheTTLOverrideEnabled.value = false
  cacheTTLOverrideTarget.value = '5m'
  customBaseUrlEnabled.value = false
  customBaseUrl.value = ''

  // TLS 指纹伪装跨 Anthropic OAuth/SetupToken 与 OpenAI OAuth 复用同一组字段。
  if (supportsTLSFingerprint(provider)) {
    const extra = provider.extra as Record<string, unknown> | undefined
    tlsFingerprintEnabled.value =
      provider.enable_tls_fingerprint === true || extra?.enable_tls_fingerprint === true
    tlsFingerprintProfileId.value =
      provider.tls_fingerprint_profile_id ?? toPositiveOrSpecialID(extra?.tls_fingerprint_profile_id)
    tlsFingerprintRouterId.value =
      provider.tls_fingerprint_router_id ?? toPositiveOrSpecialID(extra?.tls_fingerprint_router_id)
  }

  // Remaining quota control settings only apply to Anthropic providers
  if (provider.platform !== 'anthropic') {
    return
  }

  // Window cost / session limit only apply to Anthropic OAuth/SetupToken providers
  if (provider.type !== 'oauth' && provider.type !== 'setup-token') {
    return
  }

  // Load from extra field (via backend DTO fields)
  if (provider.window_cost_limit != null && provider.window_cost_limit > 0) {
    windowCostEnabled.value = true
    windowCostLimit.value = provider.window_cost_limit
    windowCostStickyReserve.value = provider.window_cost_sticky_reserve ?? 10
  }

  if (provider.max_sessions != null && provider.max_sessions > 0) {
    sessionLimitEnabled.value = true
    maxSessions.value = provider.max_sessions
    sessionIdleTimeout.value = provider.session_idle_timeout_minutes ?? 5
  }

  // RPM limit
  if (provider.base_rpm != null && provider.base_rpm > 0) {
    rpmLimitEnabled.value = true
    baseRpm.value = provider.base_rpm
    rpmStrategy.value = (provider.rpm_strategy as 'tiered' | 'sticky_exempt') || 'tiered'
    rpmStickyBuffer.value = provider.rpm_sticky_buffer ?? null
  }

  // UMQ mode（独立于 RPM 加载，防止编辑无 RPM 提供商时丢失已有配置）
  userMsgQueueMode.value = provider.user_msg_queue_mode ?? ''

  // Load session ID masking setting
  if (provider.session_id_masking_enabled === true) {
    sessionIdMaskingEnabled.value = true
  }

  // Load cache TTL override setting
  if (provider.cache_ttl_override_enabled === true) {
    cacheTTLOverrideEnabled.value = true
    cacheTTLOverrideTarget.value = provider.cache_ttl_override_target || '5m'
  }

  // Load custom base URL setting
  if (provider.custom_base_url_enabled === true) {
    customBaseUrlEnabled.value = true
    customBaseUrl.value = provider.custom_base_url || ''
  }
}

function formatTempUnschedKeywords(value: unknown) {
  if (Array.isArray(value)) {
    return value
      .filter((item): item is string => typeof item === 'string')
      .map((item) => item.trim())
      .filter((item) => item.length > 0)
      .join(', ')
  }
  if (typeof value === 'string') {
    return value
  }
  return ''
}

const splitTempUnschedKeywords = (value: string) => {
  return value
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

function toPositiveNumber(value: unknown) {
  const num = Number(value)
  if (!Number.isFinite(num) || num <= 0) {
    return null
  }
  return Math.trunc(num)
}

function toPositiveOrSpecialID(value: unknown) {
  const num = Number(value)
  if (!Number.isFinite(num) || num === 0) {
    return null
  }
  return Math.trunc(num)
}

const formatDateTimeLocal = formatDateTimeLocalInput
const parseDateTimeLocal = parseDateTimeLocalInput

// Methods
const handleClose = () => {
  emit('close')
}

const submitUpdateProvider = async (providerID: number, updatePayload: Record<string, unknown>) => {
  submitting.value = true
  try {
    if (props.provider && !props.provider.parent_provider_id) {
      await loadProtocolCatalog()
      const credentials = { ...(updatePayload.credentials as Record<string, unknown> ?? props.provider.credentials ?? {}), upstream_protocols: upstreamProtocols.value ?? nativeProtocolOptions(props.provider.platform, props.provider.type, String(props.provider.credentials?.auth_mode ?? props.provider.credentials?.openai_auth_mode ?? '')) } as Record<string, unknown>
      delete credentials.api_protocol
      delete credentials.openai_workload_capabilities
      updatePayload.credentials = credentials
      const extra = updatePayload.extra as Record<string, unknown> | undefined
      if (extra) delete extra.openai_text_route_mode
    }
    const updatedProvider = await adminAPI.providers.update(providerID, updatePayload)
    appStore.showSuccess(t('admin.providers.providerUpdated'))
    emit('updated', updatedProvider)
    handleClose()
  } catch (error: any) {
    appStore.showError(error.message || t('admin.providers.failedToUpdate'))
  } finally {
    submitting.value = false
  }
}

const handleSubmit = async () => {
  if (!props.provider) return
  const providerID = props.provider.id
  // 表单关闭了浏览器自带校验，隐藏页签中的必填项由页签组件定位后报告。
  if (tabsRef.value && !(await tabsRef.value.validate())) return

  if (form.status !== 'active' && form.status !== 'inactive' && form.status !== 'error') {
    failAt(t('admin.providers.pleaseSelectStatus'), 'status')
    return
  }

  const updatePayload: Record<string, unknown> = { ...form }
  try {
    // 后端用 proxy_id: 0 表示清除代理。
    if (updatePayload.proxy_id === null) {
      updatePayload.proxy_id = 0
    }
    if (form.expires_at === null) {
      updatePayload.expires_at = 0
    }
    // load_factor: 空值/NaN/0/负数 时发送 0（后端约定 <= 0 = 清除）
    const lf = form.load_factor
    if (lf == null || Number.isNaN(lf) || lf <= 0) {
      updatePayload.load_factor = 0
    }
    updatePayload.auto_pause_on_expired = autoPauseOnExpired.value

    // For apikey type, handle credentials update
    if (props.provider.type === 'apikey') {
      const currentCredentials = (props.provider.credentials as Record<string, unknown>) || {}
      const enteredBaseUrl = editBaseUrl.value.trim()
      if (
        props.provider.platform === 'gemini' &&
        geminiProviderType.value === 'third_party' &&
        !isGeminiThirdPartyBaseUrl(enteredBaseUrl)
      ) {
        failAt(t('admin.providers.gemini.connectionSource.thirdPartyBaseUrlRequired'), 'base-url')
        return
      }
      const newBaseUrl = enteredBaseUrl || defaultBaseUrl.value
      const shouldApplyModelMapping = true

      // API Key 类型始终提交 credentials，以便同步模型映射变更。
      const newCredentials: Record<string, unknown> = {
        ...currentCredentials,
        base_url: newBaseUrl
      }

      // 国产供应商：模式与协议写入凭据（决定额度/余额探测与转发端点/格式）。
      if (isCNApiKeyProvider.value) {
        newCredentials.provider_mode = editProviderMode.value
        newCredentials.api_protocol = editApiProtocol.value
        if (editApiProtocol.value === 'adaptive') {
          const defaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, editProviderMode.value)
          const protocolBaseUrls: Record<string, string> = {}
          for (const item of editAdaptiveProtocolOptions.value) {
            protocolBaseUrls[item.value] = (editAdaptiveBaseUrls.value[item.value] || defaults[item.value]).trim()
          }
          newCredentials.api_base_urls = protocolBaseUrls
          newCredentials.base_url = protocolBaseUrls.chat_completions
        } else {
          delete newCredentials.api_base_urls
        }
        if (props.provider.platform === 'zhipu') {
          const organization = editZhipuOrganization.value.trim()
          const project = editZhipuProject.value.trim()
          if (editProviderMode.value === 'coding' && organization) {
            newCredentials.zhipu_organization = organization
            if (project) newCredentials.zhipu_project = project
            else delete newCredentials.zhipu_project
          } else {
            delete newCredentials.zhipu_organization
            delete newCredentials.zhipu_project
          }
        }
      }

      // 处理 API Key。
      // 后端响应已脱敏：currentCredentials 不会再包含 api_key 原文。
      // 用户填入新值则覆盖；留空时优先看 credentials_status.has_api_key；
      // 若后端尚未升级（无 credentials_status），回退读旧结构 currentCredentials.api_key。
      // 两者都无才报错。
      const hasExistingApiKey =
        props.provider.credentials_status?.has_api_key ?? Boolean(currentCredentials.api_key)
      if (editApiKey.value.trim()) {
        newCredentials.api_key = editApiKey.value.trim()
      } else if (!hasExistingApiKey) {
        failAt(t('admin.providers.apiKeyIsRequired'), 'api-key')
        return
      }

      // New API 用户访问令牌属于敏感凭据；留空表示沿用后端已保存的值。
      if (upstreamUsageWalletAccessToken.value.trim()) {
        newCredentials.new_api_user_access_token = upstreamUsageWalletAccessToken.value.trim()
      }
      if (upstreamUsageWalletUserId.value.trim()) {
        newCredentials.new_api_user_id = upstreamUsageWalletUserId.value.trim()
      } else {
        delete newCredentials.new_api_user_id
      }

      if (props.provider.platform === 'gemini') {
        newCredentials.provider_type = geminiProviderType.value
        if (geminiProviderType.value === 'third_party') {
          // tier_id 不是敏感字段，删除后会随本次完整凭据更新一起清除。
          delete newCredentials.tier_id
        } else {
          newCredentials.tier_id = geminiAIStudioTier.value
        }
      }

      // 保存配置的模型映射，OpenAI 开启自动透传时使用已有映射。
      if (shouldApplyModelMapping) {
        if (props.provider.platform === 'qoder') {
          applyQoderModelRestriction(newCredentials)
        } else {
          applyPersistedModelRestriction(newCredentials)
        }
      } else if (currentCredentials.model_mapping) {
        newCredentials.model_mapping = currentCredentials.model_mapping
        if ('model_whitelist' in currentCredentials) {
          newCredentials.model_whitelist = currentCredentials.model_whitelist
        }
      } else if ('model_whitelist' in currentCredentials) {
        newCredentials.model_whitelist = currentCredentials.model_whitelist
      }
      if (props.provider.platform === 'openai') {
        const compactModelMapping = buildModelMappingObject('mapping', [], openAICompactModelMappings.value)
        if (compactModelMapping) {
          newCredentials.compact_model_mapping = compactModelMapping
        } else {
          delete newCredentials.compact_model_mapping
        }
      }

      // Add pool mode if enabled
      if (poolModeEnabled.value) {
        newCredentials.pool_mode = true
        newCredentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
        const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(poolModeRetryStatusCodesInput.value)
        if (parsedRetryStatusCodes.length > 0) {
          newCredentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
        } else {
          delete newCredentials.pool_mode_retry_status_codes
        }
      } else {
        delete newCredentials.pool_mode
        delete newCredentials.pool_mode_retry_count
        delete newCredentials.pool_mode_retry_status_codes
      }

      // Add custom error codes if enabled
      if (customErrorCodesEnabled.value) {
        newCredentials.custom_error_codes_enabled = true
        newCredentials.custom_error_codes = [...selectedErrorCodes.value]
      } else {
        delete newCredentials.custom_error_codes_enabled
        delete newCredentials.custom_error_codes
      }

      // 为支持该功能的平台 API Key 提供商写入请求头覆写。
      if (isHeaderOverrideCapable(props.provider.platform, 'apikey')) {
        if (headerOverrideEnabled.value) {
          const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
          if (headerError) {
            failAt(t(`admin.providers.headerOverride.${headerError}`), 'header-override')
            return
          }
        }
        applyHeaderOverride(newCredentials, headerOverrideEnabled.value, headerOverrideRows.value, 'edit')
      }

      // Add intercept warmup requests setting
      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    } else if ((props.provider.platform === 'gemini' || props.provider.platform === 'anthropic') && props.provider.type === 'service_account') {
      const currentCredentials = (props.provider.credentials as Record<string, unknown>) || {}
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      if (!editVertexProjectId.value.trim()) {
        failAt(t('admin.providers.vertexSaJsonMissingProjectId'), 'vertex-project-id')
        return
      }
      if (!editVertexClientEmail.value.trim()) {
        failAt(t('admin.providers.vertexSaJsonMissingClientEmail'), 'vertex-project-id')
        return
      }
      if (!editVertexLocation.value.trim()) {
        failAt(t('admin.providers.vertexLocationRequired'), 'vertex-location')
        return
      }

      // 服务端对 SA JSON 脱敏，是否已配置优先读取 credentials_status。
      // 若后端尚未升级（无 credentials_status），回退读旧结构 service_account_json / service_account。
      const credentialsStatus = props.provider.credentials_status
      const hasExistingServiceAccountJson = credentialsStatus
        ? Boolean(
            credentialsStatus.has_service_account_json || credentialsStatus.has_service_account
          )
        : Boolean(currentCredentials.service_account_json || currentCredentials.service_account)
      if (!hasExistingServiceAccountJson) {
        failAt(t('admin.providers.vertexSaJsonRequired'), 'vertex-project-id')
        return
      }
      newCredentials.project_id = editVertexProjectId.value.trim()
      newCredentials.client_email = editVertexClientEmail.value.trim()
      newCredentials.location = editVertexLocation.value.trim()
      newCredentials.tier_id = 'vertex'

      applyPersistedModelRestriction(newCredentials)

      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    } else if (props.provider.type === 'bedrock') {
      const currentCredentials = (props.provider.credentials as Record<string, unknown>) || {}
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      newCredentials.aws_region = editBedrockRegion.value.trim()
      if (editBedrockForceGlobal.value) {
        newCredentials.aws_force_global = 'true'
      } else {
        delete newCredentials.aws_force_global
      }

      if (isBedrockAPIKeyMode.value) {
        // API Key mode: only update api_key if user provided new value
        if (editBedrockApiKeyValue.value.trim()) {
          newCredentials.api_key = editBedrockApiKeyValue.value.trim()
        }
      } else {
        // SigV4 mode
        newCredentials.aws_access_key_id = editBedrockAccessKeyId.value.trim()
        if (editBedrockSecretAccessKey.value.trim()) {
          newCredentials.aws_secret_access_key = editBedrockSecretAccessKey.value.trim()
        }
        if (editBedrockSessionToken.value.trim()) {
          newCredentials.aws_session_token = editBedrockSessionToken.value.trim()
        }
      }

      // Pool mode
      if (poolModeEnabled.value) {
        newCredentials.pool_mode = true
        newCredentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
        const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(poolModeRetryStatusCodesInput.value)
        if (parsedRetryStatusCodes.length > 0) {
          newCredentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
        } else {
          delete newCredentials.pool_mode_retry_status_codes
        }
      } else {
        delete newCredentials.pool_mode
        delete newCredentials.pool_mode_retry_count
        delete newCredentials.pool_mode_retry_status_codes
      }

      // Model mapping
      applyPersistedModelRestriction(newCredentials)

      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    } else {
      // For oauth/setup-token types, only update intercept_warmup_requests if changed
      const currentCredentials = (props.provider.credentials as Record<string, unknown>) || {}
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    }

    // OpenAI/Grok OAuth 与 Qoder COSY：将模型映射保存到 credentials。
    if (supportsOAuthLikeModelRestriction.value) {
      const currentCredentials = props.provider.platform === 'openai' && isSparkShadow.value
        ? {}
        : (updatePayload.credentials as Record<string, unknown>) ||
          ((props.provider.credentials as Record<string, unknown>) || {})
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      if (props.provider.platform === 'qoder') {
        applyQoderModelRestriction(newCredentials)
        newCredentials.site = qoderSite.value
      } else if (props.provider.platform === 'openai') {
        applyOpenAIModelMappingCredentials(newCredentials)
      } else {
        applyPersistedModelRestriction(newCredentials)
      }

      updatePayload.credentials = newCredentials
    }

    // Grok OAuth：保存自定义上游地址与请求头覆写。base_url 仅改写转发端点，
    // OAuth 授权与令牌刷新链路不读取该值；关闭开关即恢复默认官方网关。
    if (props.provider.platform === 'grok' && props.provider.type === 'oauth') {
      const currentCredentials =
        (updatePayload.credentials as Record<string, unknown>) ||
        ((props.provider.credentials as Record<string, unknown>) || {})
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      if (grokOAuthCustomBaseUrlEnabled.value) {
        const trimmedBaseUrl = grokOAuthBaseUrl.value.trim()
        if (!trimmedBaseUrl) {
          failAt(t('admin.providers.grokCustomBaseUrl.required'), 'grok-base-url')
          return
        }
        if (!/^https?:\/\//i.test(trimmedBaseUrl)) {
          failAt(t('admin.providers.grokCustomBaseUrl.invalid'), 'grok-base-url')
          return
        }
        newCredentials.base_url = trimmedBaseUrl
      } else {
        delete newCredentials.base_url
      }

      if (headerOverrideEnabled.value) {
        const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
        if (headerError) {
          failAt(t(`admin.providers.headerOverride.${headerError}`), 'header-override')
          return
        }
      }
      applyHeaderOverride(newCredentials, headerOverrideEnabled.value, headerOverrideRows.value, 'edit')

      updatePayload.credentials = newCredentials

      const newExtra: Record<string, unknown> = {
        ...((props.provider.extra as Record<string, unknown>) || {})
      }
      // 开关的 true 和 false 均持久化，字段缺失会触发后端默认启用。
      newExtra[GROK_CLIENT_TOOL_CACHE_EXTRA_KEY] = grokClientToolCacheEnabled.value
      updatePayload.extra = newExtra
    }

    // OpenAI: 手动覆盖订阅档位 plan_type（Plus/Pro/Free）。仅 OAuth 非影子提供商：
    // 影子提供商凭据由母提供商管理(且后端会 sanitize),setup-token 无订阅调度语义。
    if (props.provider.platform === 'openai' && props.provider.type === 'oauth' && !isSparkShadow.value) {
      const currentCredentials = (updatePayload.credentials as Record<string, unknown>) ||
        ((props.provider.credentials as Record<string, unknown>) || {})
      updatePayload.credentials = applyPlanType({ ...currentCredentials }, editPlanType.value)
    }

    // Antigravity: persist model mapping to credentials (applies to all antigravity types)
    // Antigravity 只支持映射模式
    if (props.provider.platform === 'antigravity') {
      const currentCredentials = (updatePayload.credentials as Record<string, unknown>) ||
        ((props.provider.credentials as Record<string, unknown>) || {})
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      // 移除旧字段
      newCredentials.model_whitelist = [...antigravityWhitelistModels.value]
      delete newCredentials.model_mapping

      // 只使用映射模式
      const antigravityModelMapping = buildModelMappingObject(
        'mapping',
        [],
        antigravityModelMappings.value
      )
      if (antigravityModelMapping) {
        newCredentials.model_mapping = antigravityModelMapping
      }
      if (props.provider.type === 'oauth') {
        applyAntigravityProjectID(newCredentials, antigravityProjectId.value, 'edit')
      }

      updatePayload.credentials = newCredentials
    }

    // Antigravity 提供商单独保存上游超额额度策略。
    if (props.provider.platform === 'antigravity') {
      const currentExtra = (props.provider.extra as Record<string, unknown>) || {}
      const newExtra: Record<string, unknown> = { ...currentExtra }
      delete newExtra.mixed_scheduling
      if (allowOverages.value) {
        newExtra.allow_overages = true
      } else {
        delete newExtra.allow_overages
      }
      updatePayload.extra = newExtra
    }

    // For Anthropic OAuth/SetupToken providers, handle quota control settings in extra
    if (props.provider.platform === 'anthropic' && (props.provider.type === 'oauth' || props.provider.type === 'setup-token')) {
      const currentExtra = (updatePayload.extra as Record<string, unknown>) || (props.provider.extra as Record<string, unknown>) || {}
      const newExtra: Record<string, unknown> = { ...currentExtra }

      // Window cost limit settings
      if (windowCostEnabled.value && windowCostLimit.value != null && windowCostLimit.value > 0) {
        newExtra.window_cost_limit = windowCostLimit.value
        newExtra.window_cost_sticky_reserve = windowCostStickyReserve.value ?? 10
      } else {
        delete newExtra.window_cost_limit
        delete newExtra.window_cost_sticky_reserve
      }

      // Session limit settings
      if (sessionLimitEnabled.value && maxSessions.value != null && maxSessions.value > 0) {
        newExtra.max_sessions = maxSessions.value
        newExtra.session_idle_timeout_minutes = sessionIdleTimeout.value ?? 5
      } else {
        delete newExtra.max_sessions
        delete newExtra.session_idle_timeout_minutes
      }

      // RPM limit settings
      if (rpmLimitEnabled.value) {
        const DEFAULT_BASE_RPM = 15
        newExtra.base_rpm = (baseRpm.value != null && baseRpm.value > 0)
          ? baseRpm.value
          : DEFAULT_BASE_RPM
        newExtra.rpm_strategy = rpmStrategy.value
        if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
          newExtra.rpm_sticky_buffer = rpmStickyBuffer.value
        } else {
          delete newExtra.rpm_sticky_buffer
        }
      } else {
        delete newExtra.base_rpm
        delete newExtra.rpm_strategy
        delete newExtra.rpm_sticky_buffer
      }

      // UMQ mode（独立于 RPM 保存）
      if (userMsgQueueMode.value) {
        newExtra.user_msg_queue_mode = userMsgQueueMode.value
      } else {
        delete newExtra.user_msg_queue_mode
      }
      delete newExtra.user_msg_queue_enabled  // 清理旧字段

      // TLS fingerprint setting
      if (tlsFingerprintEnabled.value) {
        newExtra.enable_tls_fingerprint = true
        if (tlsFingerprintProfileId.value) {
          newExtra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
        } else {
          delete newExtra.tls_fingerprint_profile_id
        }
      } else {
        delete newExtra.enable_tls_fingerprint
        delete newExtra.tls_fingerprint_profile_id
        delete newExtra.tls_fingerprint_router_id
      }

      // Session ID masking setting
      if (sessionIdMaskingEnabled.value) {
        newExtra.session_id_masking_enabled = true
      } else {
        delete newExtra.session_id_masking_enabled
      }

      // Cache TTL override setting
      if (cacheTTLOverrideEnabled.value) {
        newExtra.cache_ttl_override_enabled = true
        newExtra.cache_ttl_override_target = cacheTTLOverrideTarget.value
      } else {
        delete newExtra.cache_ttl_override_enabled
        delete newExtra.cache_ttl_override_target
      }

      // Custom base URL relay setting
      if (customBaseUrlEnabled.value && customBaseUrl.value.trim()) {
        newExtra.custom_base_url_enabled = true
        newExtra.custom_base_url = customBaseUrl.value.trim()
      } else {
        delete newExtra.custom_base_url_enabled
        delete newExtra.custom_base_url
      }

      updatePayload.extra = newExtra
    }

    // For Anthropic API Key providers, handle passthrough mode + web search emulation in extra
    if (props.provider.platform === 'anthropic' && props.provider.type === 'apikey') {
      const currentExtra = (updatePayload.extra as Record<string, unknown>) || (props.provider.extra as Record<string, unknown>) || {}
      const newExtra: Record<string, unknown> = { ...currentExtra }
      if (anthropicPassthroughEnabled.value) {
        newExtra.anthropic_passthrough = true
      } else {
        delete newExtra.anthropic_passthrough
      }
      if (anthropicAPIKeyAuthScheme.value === 'authorization_bearer') {
        newExtra.anthropic_apikey_auth_scheme = 'authorization_bearer'
      } else {
        delete newExtra.anthropic_apikey_auth_scheme
      }
      if (webSearchEmulationMode.value === 'default') {
        delete newExtra.web_search_emulation
      } else {
        newExtra.web_search_emulation = webSearchEmulationMode.value
      }
      updatePayload.extra = newExtra
    }

    if (isQoderCosyProvider.value) {
      const currentExtra = (updatePayload.extra as Record<string, unknown>) || (props.provider.extra as Record<string, unknown>) || {}
      const newExtra: Record<string, unknown> = { ...currentExtra }
      applyTLSFingerprintExtra(newExtra)
      updatePayload.extra = newExtra
    }

    // OpenAI OAuth、SetupToken 和 API Key 提供商：更新透传与计费设置。
    if (props.provider.platform === 'openai' && (props.provider.type === 'oauth' || props.provider.type === 'setup-token' || props.provider.type === 'apikey')) {
      const currentExtra = (props.provider.extra as Record<string, unknown>) || {}
      const newExtra = normalizeLegacyOpenAIExtra(currentExtra)
      const hadCodexCLIOnlyEnabled = currentExtra.codex_cli_only === true
      newExtra.responses_ws_connection_mode = responsesWSConnectionMode.value
      clearLegacyResponsesWSSettings(newExtra)
      delete newExtra.openai_ws_enabled
      delete newExtra.openai_long_context_billing_enabled
      if (openaiPassthroughEnabled.value) {
        newExtra.openai_passthrough = true
      } else {
        delete newExtra.openai_passthrough
        delete newExtra.openai_oauth_passthrough
      }
      // 关闭时删除 extra 中的默认项。
      if (props.provider.type === 'oauth' && openaiFlattenNamespacesEnabled.value) {
        newExtra.openai_responses_flatten_namespaces = true
      } else {
        delete newExtra.openai_responses_flatten_namespaces
      }
      newExtra.openai_compact_mode = openAICompactMode.value
      newExtra.openai_native_compaction_v2_mode = openAINativeCompactionV2Mode.value
      if (props.provider.type === 'apikey' && openAIImagesURLToB64JSON.value) {
        newExtra.images_url_to_b64_json = true
      } else {
        delete newExtra.images_url_to_b64_json
      }
      if (props.provider.type === 'apikey') {
		delete newExtra.openai_responses_mode
		newExtra.openai_responses_continuation_supported = openAIResponsesContinuationSupported.value
	  }
		if (autoPause5hThreshold.value != null && autoPause5hThreshold.value > 0) {
			newExtra.auto_pause_5h_threshold = autoPause5hThreshold.value / 100
		} else {
			delete newExtra.auto_pause_5h_threshold
		}
		if (autoPause7dThreshold.value != null && autoPause7dThreshold.value > 0) {
			newExtra.auto_pause_7d_threshold = autoPause7dThreshold.value / 100
		} else {
			delete newExtra.auto_pause_7d_threshold
		}
		if (autoPause5hDisabled.value) {
			newExtra.auto_pause_5h_disabled = true
		} else {
			delete newExtra.auto_pause_5h_disabled
		}
		if (autoPause7dDisabled.value) {
			newExtra.auto_pause_7d_disabled = true
		} else {
			delete newExtra.auto_pause_7d_disabled
		}

      applyCodexImageToolMode(newExtra, codexImageToolMode.value)

      if (props.provider.type === 'oauth') {
        newExtra.openai_oauth_client_policy = openAIOAuthClientPolicy.value
        if (openAIOAuthClientPolicy.value === 'codex_only') {
          newExtra.codex_cli_only = true
        } else if (hadCodexCLIOnlyEnabled || currentExtra.openai_oauth_client_policy != null) {
          // 关闭时写入 false，后端合并 extra 时据此清除已启用状态。
          newExtra.codex_cli_only = false
        } else {
          delete newExtra.codex_cli_only
        }
        // 仅当 codex_cli_only 开启且子开关开启时写入 Claude Code 插件白名单，否则清除避免孤立字段
        if (openAIOAuthClientPolicy.value === 'codex_only' && codexCLIOnlyAllowClaudeCodeEnabled.value) {
          newExtra.codex_cli_only_allowed_clients = ['claude_code']
        } else {
          delete newExtra.codex_cli_only_allowed_clients
        }

        // OpenAI OAuth 复用 Anthropic 的 TLS 指纹伪装字段。
        if (tlsFingerprintEnabled.value) {
          newExtra.enable_tls_fingerprint = true
          if (tlsFingerprintProfileId.value) {
            newExtra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
          } else {
            delete newExtra.tls_fingerprint_profile_id
          }
          if (tlsFingerprintRouterId.value) {
            newExtra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
          } else {
            delete newExtra.tls_fingerprint_router_id
          }
        } else {
          delete newExtra.enable_tls_fingerprint
          delete newExtra.tls_fingerprint_profile_id
          delete newExtra.tls_fingerprint_router_id
        }
      }

      // 指纹收敛模式：默认 off，不写入；其它模式必须显式写入。
      if (props.provider.type === 'oauth') {
        if (codexFingerprintMode.value !== 'off') {
          newExtra.codex_fingerprint_mode = codexFingerprintMode.value
        } else {
          delete newExtra.codex_fingerprint_mode
        }
      }

      updatePayload.extra = newExtra
    }

    // For apikey/bedrock providers, handle quota_limit in extra
    if (props.provider.type === 'apikey' || props.provider.type === 'bedrock') {
      const currentExtra = (updatePayload.extra as Record<string, unknown>) ||
        (props.provider.extra as Record<string, unknown>) || {}
      const newExtra: Record<string, unknown> = { ...currentExtra }
      // Total quota
      if (editQuotaLimit.value != null && editQuotaLimit.value > 0) {
        newExtra.quota_limit = editQuotaLimit.value
      } else {
        delete newExtra.quota_limit
      }
      // Daily quota
      if (editQuotaDailyLimit.value != null && editQuotaDailyLimit.value > 0) {
        newExtra.quota_daily_limit = editQuotaDailyLimit.value
      } else {
        delete newExtra.quota_daily_limit
        delete newExtra.quota_daily_used
        delete newExtra.quota_daily_start
      }
      // Weekly quota
      if (editQuotaWeeklyLimit.value != null && editQuotaWeeklyLimit.value > 0) {
        newExtra.quota_weekly_limit = editQuotaWeeklyLimit.value
      } else {
        delete newExtra.quota_weekly_limit
        delete newExtra.quota_weekly_used
        delete newExtra.quota_weekly_start
      }
      // Quota reset mode config
      if (editDailyResetMode.value === 'fixed') {
        newExtra.quota_daily_reset_mode = 'fixed'
        newExtra.quota_daily_reset_hour = editDailyResetHour.value ?? 0
      } else {
        delete newExtra.quota_daily_reset_mode
        delete newExtra.quota_daily_reset_hour
      }
      if (editWeeklyResetMode.value === 'fixed') {
        newExtra.quota_weekly_reset_mode = 'fixed'
        newExtra.quota_weekly_reset_day = editWeeklyResetDay.value ?? 1
        newExtra.quota_weekly_reset_hour = editWeeklyResetHour.value ?? 0
      } else {
        delete newExtra.quota_weekly_reset_mode
        delete newExtra.quota_weekly_reset_day
        delete newExtra.quota_weekly_reset_hour
      }
      if (editDailyResetMode.value === 'fixed' || editWeeklyResetMode.value === 'fixed') {
        newExtra.quota_reset_timezone = editResetTimezone.value || 'UTC'
      } else {
        delete newExtra.quota_reset_timezone
      }
      // Quota notify config
      writeQuotaNotifyToExtra(newExtra, 'update')
      if (props.provider.type === 'apikey') {
        const upstreamConfig: Record<string, unknown> = {
          enabled: upstreamUsageEnabled.value,
          adapter: upstreamUsageAdapter.value
        }
        if (upstreamUsageBaseUrl.value.trim()) {
          upstreamConfig.base_url = upstreamUsageBaseUrl.value.trim()
        }
        newExtra.upstream_usage_query = upstreamConfig
      }
      updatePayload.extra = newExtra
    }

    // 上游 ID 头名发生改动后写回 extra，其他运行状态沿用当前值。
    const nextUpstreamRequestIdHeader = upstreamRequestIdHeader.value.trim()
    if (nextUpstreamRequestIdHeader !== readUpstreamRequestIdHeader(props.provider.extra)) {
      const currentExtra = (updatePayload.extra as Record<string, unknown>) || (props.provider.extra as Record<string, unknown>) || {}
      const newExtra: Record<string, unknown> = { ...currentExtra }
      if (nextUpstreamRequestIdHeader) {
        newExtra.upstream_request_id_header = nextUpstreamRequestIdHeader
      } else {
        delete newExtra.upstream_request_id_header
      }
      updatePayload.extra = newExtra
    }

    await submitUpdateProvider(providerID, updatePayload)
  } catch (error: any) {
    appStore.showError(error.message || t('admin.providers.failedToUpdate'))
  }
}
</script>
