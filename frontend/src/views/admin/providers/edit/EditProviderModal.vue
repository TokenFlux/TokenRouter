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
                <label for="edit-provider-notes" class="input-label">{{
                  t('admin.providers.notes')
                }}</label>
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
                <label for="edit-provider-status" class="input-label">{{
                  t('common.status')
                }}</label>
                <Select id="edit-provider-status" v-model="form.status" :options="statusOptions" />
              </div>
              <div>
                <label for="edit-provider-expires-at" class="input-label">{{
                  t('admin.providers.expiresAt')
                }}</label>
                <input
                  id="edit-provider-expires-at"
                  v-model="expiresAtInput"
                  type="datetime-local"
                  class="input"
                />
                <p class="input-hint">{{ t('admin.providers.expiresAtHint') }}</p>
              </div>
            </div>
          </SettingsSection>

          <!-- Changing the site updates request routing context; the credentials retain their token source. -->
          <SettingsSection
            v-if="isQoderCosyProvider"
            :title="t('admin.providers.qoder.site.label')"
          >
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

          <SettingsSection
            v-if="showCredentialsSection"
            :title="t('admin.providers.sections.credentials')"
          >
            <!-- API-key credential fields. -->
            <template v-if="provider.type === 'apikey'">
              <!-- Provider mode selection for platforms with billing or service modes. -->
              <div v-if="modeChoices.length > 0">
                <span class="input-label">{{
                  t('admin.providers.cnProviders.providerMode.title')
                }}</span>
                <SettingsSegmented
                  v-model="editProviderMode"
                  :ariaLabel="t('admin.providers.cnProviders.providerMode.title')"
                  :options="providerModeSegmentOptions"
                />
                <p v-if="selectedModeChoice" class="input-hint">
                  {{ t(selectedModeChoice.descriptionKey) }}
                </p>
              </div>
              <div v-if="provider.platform === 'gemini'">
                <label for="edit-gemini-provider-type" class="input-label">{{
                  t('admin.providers.gemini.connectionSource.label')
                }}</label>
                <Select
                  id="edit-gemini-provider-type"
                  v-model="geminiProviderType"
                  :options="geminiProviderTypeOptions"
                  data-testid="edit-gemini-provider-type"
                />
                <p class="input-hint">{{ geminiProviderTypeHint }}</p>
              </div>
              <div
                v-if="!isCNApiKeyProvider || editApiProtocol !== 'adaptive'"
                data-provider-field="base-url"
              >
                <label for="edit-provider-base-url" class="input-label">{{
                  t('admin.providers.baseUrl')
                }}</label>
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
              <div class="grid gap-4 md:grid-cols-2">
                <div data-provider-field="api-key" :class="{ 'md:col-span-2': !showGeminiTier }">
                  <label for="edit-provider-api-key" class="input-label">{{
                    t('admin.providers.apiKey')
                  }}</label>
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
                  <label for="edit-gemini-tier" class="input-label">{{
                    t('admin.providers.gemini.tier.label')
                  }}</label>
                  <Select
                    id="edit-gemini-tier"
                    v-model="geminiAIStudioTier"
                    :options="geminiAIStudioTierOptions"
                    data-testid="edit-gemini-tier-select"
                  />
                  <p class="input-hint">{{ t('admin.providers.gemini.tier.aiStudioHint') }}</p>
                </div>
              </div>
              <!-- Zhipu Coding Plan accepts optional organization and project IDs; clearing the organization selects personal quota endpoints. -->
              <div
                v-if="provider.platform === 'zhipu' && editProviderMode === 'coding'"
                class="space-y-2"
              >
                <div class="flex items-center gap-1">
                  <span class="text-sm font-medium text-primary-900 dark:text-dark-50">{{
                    t('admin.providers.cnProviders.zhipuTeam.title')
                  }}</span>
                  <HelpTooltip trigger="click" width-class="w-80">
                    <p class="mb-1 font-medium">
                      {{ t('admin.providers.cnProviders.zhipuTeam.help.title') }}
                    </p>
                    <ol class="list-decimal space-y-1 pl-4">
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step1') }}</li>
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step2') }}</li>
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step3') }}</li>
                      <li>{{ t('admin.providers.cnProviders.zhipuTeam.help.step4') }}</li>
                    </ol>
                    <p
                      class="mt-2 break-all rounded-compact bg-black/20 p-1.5 font-mono text-xs leading-relaxed"
                    >
                      {{ t('admin.providers.cnProviders.zhipuTeam.help.example') }}
                    </p>
                  </HelpTooltip>
                </div>
                <div class="grid gap-4 md:grid-cols-2">
                  <div>
                    <label for="edit-zhipu-organization" class="input-label">{{
                      t('admin.providers.cnProviders.zhipuTeam.organization')
                    }}</label>
                    <input
                      id="edit-zhipu-organization"
                      v-model="editZhipuOrganization"
                      type="text"
                      class="input"
                      :placeholder="
                        t('admin.providers.cnProviders.zhipuTeam.organizationPlaceholder')
                      "
                    />
                  </div>
                  <div>
                    <label for="edit-zhipu-project" class="input-label">{{
                      t('admin.providers.cnProviders.zhipuTeam.project')
                    }}</label>
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
            </template>

            <!-- Vertex Service Account -->
            <div v-if="isServiceAccountProvider" class="grid gap-4 md:grid-cols-2">
              <div data-provider-field="vertex-project-id">
                <label for="edit-vertex-project-id" class="input-label">{{
                  t('admin.providers.vertexProjectId')
                }}</label>
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
                <label for="edit-vertex-location" class="input-label">{{
                  t('admin.providers.vertexLocation')
                }}</label>
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

            <!-- Bedrock exposes separate SigV4 and API-key credentials. -->
            <template v-if="provider.type === 'bedrock'">
              <div v-if="!isBedrockAPIKeyMode" class="grid gap-4 md:grid-cols-2">
                <div class="md:col-span-2">
                  <label for="edit-bedrock-access-key-id" class="input-label">{{
                    t('admin.providers.bedrockAccessKeyId')
                  }}</label>
                  <input
                    id="edit-bedrock-access-key-id"
                    v-model="editBedrockAccessKeyId"
                    type="text"
                    class="input font-mono"
                    placeholder="AKIA..."
                  />
                </div>
                <div>
                  <label for="edit-bedrock-secret" class="input-label">{{
                    t('admin.providers.bedrockSecretAccessKey')
                  }}</label>
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
                  <label for="edit-bedrock-session-token" class="input-label">{{
                    t('admin.providers.bedrockSessionToken')
                  }}</label>
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
                <label for="edit-bedrock-api-key" class="input-label">{{
                  t('admin.providers.bedrockApiKeyInput')
                }}</label>
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
                <label for="edit-bedrock-region" class="input-label">{{
                  t('admin.providers.bedrockRegion')
                }}</label>
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
              <label for="edit-antigravity-project-id" class="input-label">{{
                t('admin.providers.antigravityProjectIdLabel')
              }}</label>
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
          <!-- Saved-provider sync reads stored credentials, so unsaved connection edits withhold it. -->
          <SettingsNotice
            v-if="showModelRestriction && connectionChanged"
            data-testid="model-sync-save-first"
          >
            {{ t('admin.providers.saveBeforeSync') }}
          </SettingsNotice>

          <ModelRestrictionFields
            v-if="showModelRestriction"
            v-model:mode="modelRestrictionMode"
            :allowed-models="allowedModels"
            v-model:mappings="modelMappings"
            :platform="provider.type === 'bedrock' ? 'anthropic' : provider.platform"
            :provider-id="
              provider.type === 'bedrock' || connectionChanged ? undefined : provider.id
            "
            :source-placeholder="
              provider.type === 'bedrock' ? t('admin.providers.fromModel') : undefined
            "
            :target-placeholder="
              provider.type === 'bedrock' ? t('admin.providers.toModel') : undefined
            "
            @update:allowed-models="setAllowedModels"
            @synced="storeModelProtocolSuggestions"
            @add="touchQoderModelRestriction"
            @remove="touchQoderModelRestriction"
          />

          <OpenCodeFields
            v-if="provider.platform === 'opencode'"
            v-model="openCodeProtocols"
            :mode="editProviderMode"
            :protocols="upstreamProtocols"
          />

          <!-- Antigravity uses the whitelist for final model eligibility and mappings for request rewriting. -->
          <SettingsSection
            v-if="provider.platform === 'antigravity'"
            :title="t('admin.providers.modelRestriction')"
            :hint="t('admin.providers.selectAllowedModels')"
          >
            <ModelWhitelistSelector v-model="antigravityWhitelistModels" platform="antigravity" />
            <ProviderModelMappingEditor v-model="antigravityModelMappings" wildcard-validation>
              <template #header-actions>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="isSyncingAntigravityUpstream || !provider?.id"
                  @click="syncAntigravityUpstreamModels"
                >
                  {{
                    isSyncingAntigravityUpstream
                      ? t('admin.providers.syncUpstreamModelsLoading')
                      : t('admin.providers.syncUpstreamModels')
                  }}
                </button>
              </template>
            </ProviderModelMappingEditor>
          </SettingsSection>
        </template>

        <template #scheduling>
          <SettingsSection :title="t('admin.providers.sections.scheduling')">
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="edit-provider-concurrency" class="input-label">{{
                  t('admin.providers.concurrency')
                }}</label>
                <!-- Numeric drafts allow clearing before replacement; the backend validates submitted values. -->
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
                <label for="edit-provider-load-factor" class="input-label">{{
                  t('admin.providers.loadFactor')
                }}</label>
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
                <label for="edit-provider-priority" class="input-label">{{
                  t('admin.providers.priority')
                }}</label>
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
                <label for="edit-provider-rate-multiplier" class="input-label">{{
                  t('admin.providers.billingRateMultiplier')
                }}</label>
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
                    <label for="edit-provider-scheduling-threshold-value" class="input-label">{{
                      t('admin.providers.providerSchedulingThresholdOverrideValue')
                    }}</label>
                    <input
                      id="edit-provider-scheduling-threshold-value"
                      v-model.number="providerSchedulingThresholdOverrideValue"
                      data-testid="provider-scheduling-threshold-override-value"
                      type="number"
                      min="1"
                      max="100"
                      class="input"
                    />
                    <p class="input-hint">
                      {{ t('admin.providers.providerSchedulingThresholdOverrideDisabledHint') }}
                    </p>
                  </div>
                </SettingsSubpanel>
              </Collapse>
            </template>
          </SettingsSection>

          <TempUnschedFields
            v-model:enabled="tempUnschedEnabled"
            v-model:rules="tempUnschedRules"
          />

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
                  <label for="edit-provider-auto-pause-5h" class="input-label">{{
                    t('admin.providers.autoPause5hThreshold')
                  }}</label>
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
                  <label for="edit-provider-auto-pause-7d" class="input-label">{{
                    t('admin.providers.autoPause7dThreshold')
                  }}</label>
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
            :hint="
              provider.platform === 'anthropic'
                ? t('admin.providers.quotaControl.hint')
                : t('admin.providers.quotaLimitHint')
            "
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
            v-if="resolvedUpstreamUsageAdapter"
            :enabled="upstreamUsageEnabled"
            :adapter="resolvedUpstreamUsageAdapter"
            :base-url="upstreamUsageBaseUrl"
            :wallet-access-token="upstreamUsageWalletAccessToken"
            :wallet-user-id="upstreamUsageWalletUserId"
            @update:enabled="upstreamUsageEnabled = $event"
            @update:adapter="upstreamUsageAdapter = $event"
            @update:base-url="upstreamUsageBaseUrl = $event"
            @update:wallet-access-token="upstreamUsageWalletAccessToken = $event"
            @update:wallet-user-id="upstreamUsageWalletUserId = $event"
          />
          <UpstreamBalanceAlertEditor
            v-if="provider.type === 'apikey'"
            v-model="balanceAlert"
            :supported="balanceAlertSupported"
            :query-enabled="upstreamUsageEnabled"
            :provider-id="provider.id"
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
            v-model:image-modes="imageModes"
            :platform="provider.platform"
            :type="provider.type"
            :auth-mode="
              String(
                provider.credentials?.auth_mode ?? provider.credentials?.openai_auth_mode ?? '',
              )
            "
          />

          <SettingsSection
            v-if="showCredentialsSection && provider.type === 'apikey' && endpointSlots.length > 0"
            :title="t('admin.providers.cnProviders.apiProtocol.endpoints')"
          >
            <div v-for="item in endpointSlots" :key="item.slot">
              <label :for="`edit-provider-endpoint-${item.slot}`" class="input-label">
                {{ item.label }}
              </label>
              <input
                :id="`edit-provider-endpoint-${item.slot}`"
                v-model="editAdaptiveBaseUrls[item.slot]"
                type="text"
                class="input"
                :placeholder="item.placeholder"
              />
            </div>
            <p
              v-if="isCNApiKeyProvider && !cnSupportsNativeResponses(provider.platform)"
              class="input-hint"
            >
              {{ t('admin.providers.cnProviders.apiProtocol.responsesFallbackDesc') }}
            </p>
          </SettingsSection>

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

          <!-- Grok custom addresses control request forwarding; authorization and refresh use their own endpoints. -->
          <SettingsSection
            v-if="provider.platform === 'grok' && provider.type === 'oauth'"
            :title="t('admin.providers.sections.grok')"
          >
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
              <SettingToggleRow
                v-if="provider.type === 'oauth' || provider.type === 'apikey'"
                id="edit-openai-images-gif"
                v-model="openAIImagesGIFEnabled"
                :label="t('admin.providers.openai.imagesGIFEnabled')"
                :hint="t('admin.providers.openai.imagesGIFEnabledDesc')"
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

            <SettingsSection
              v-if="provider.type === 'oauth'"
              :title="t('admin.providers.sections.openaiClient')"
            >
              <SettingRow
                id="edit-openai-client-policy"
                label-for="edit-openai-client-policy-select"
                :label="t('admin.providers.openai.clientPolicy')"
                :hint="t('admin.providers.openai.clientPolicyDesc')"
                field
              >
                <Select
                  id="edit-openai-client-policy-select"
                  v-model="openAIOAuthClientPolicy"
                  :options="openAIOAuthClientPolicyOptions"
                />
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
            <SettingsSection v-if="provider?.type === 'oauth'">
              <SettingRow
                id="edit-image-responses-model"
                label-for="edit-image-responses-model-input"
                :label="t('admin.providers.openai.imageResponsesModel')"
                :hint="t('admin.providers.openai.imageResponsesModelDesc')"
                field
              >
                <input
                  id="edit-image-responses-model-input"
                  v-model="codexImageResponsesModel"
                  data-testid="edit-image-responses-model-input"
                  type="text"
                  class="input"
                  :placeholder="t('admin.providers.openai.imageResponsesModelDefault')"
                />
              </SettingRow>
            </SettingsSection>

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
              <Select
                id="edit-anthropic-auth-scheme-select"
                v-model="anthropicAPIKeyAuthScheme"
                :options="anthropicAPIKeyAuthSchemeOptions"
              />
            </SettingRow>
            <!-- Global web-search availability controls visibility of this provider override. -->
            <SettingRow
              v-if="webSearchGlobalEnabled"
              id="edit-anthropic-web-search"
              label-for="edit-anthropic-web-search-select"
              :label="t('admin.providers.anthropic.webSearchEmulation')"
              :hint="t('admin.providers.anthropic.webSearchEmulationDesc')"
              field
            >
              <Select
                id="edit-anthropic-web-search-select"
                v-model="webSearchEmulationMode"
                :options="webSearchEmulationOptions"
              />
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
import {
  hydrateProviderEditDraft,
  supportsTLSFingerprint,
  supportsProviderSchedulingThresholdOverridePlatform,
  normalizeProviderSchedulingThresholdOverride,
} from './providerEditHydration'
import {
  buildProviderBasePatch,
  applyProviderProtocolPatch,
  applyProviderExtraPatch,
  applyPoolModeCredentials,
  applyPersistedModelRestriction,
  applyQoderModelRestriction,
  applyOpenAIModelMappingCredentials,
  type ProviderEditPatch,
} from './providerEditPatch'
import { createProviderEditDraft } from './providerEditDraft'
import Collapse from '@/components/common/Collapse.vue'

import OpenCodeFields from '@/components/provider/form/OpenCodeFields.vue'
import { openCodeDraft, openCodeCredentials } from '@/components/provider/form/openCodeDraft'
import ProviderProtocolSelector from '../../../../components/provider/ProviderProtocolSelector.vue'
import {
  loadProtocolCatalog,
  nativeProtocolOptions,
  protocolName,
} from '@/api/admin/protocolCapabilities'
import OpenAICompactionToggle from '../../../../components/provider/OpenAICompactionToggle.vue'
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { useQuotaNotifyState } from '@/composables/useQuotaNotifyState'
import type { Provider, OllamaCloudUsageState } from '@/api/admin/providers.types'
import type { Proxy } from '@/api/admin/proxies.types'
import type { AdminGroup } from '@/api/groups.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import UpstreamRequestIdHeaderField from '@/components/provider/UpstreamRequestIdHeaderField.vue'
import Icon from '@/components/icons/Icon.vue'
import ProviderModelMappingEditor from '@/components/provider/ProviderModelMappingEditor.vue'
import type { TempUnschedRuleForm } from '@/components/provider/TempUnschedRulesEditor.vue'
import ProxySelector from '@/components/admin/proxy/ProxySelector.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import CodexImageToolModeSelector from '@/components/provider/CodexImageToolModeSelector.vue'
import ModelWhitelistSelector from '@/components/provider/ModelWhitelistSelector.vue'
import ResponsesWSConnectionModeSelector from '@/components/provider/ResponsesWSConnectionModeSelector.vue'
import GrokBaseUrlPresets from '@/components/provider/GrokBaseUrlPresets.vue'
import CnBaseUrlPresets from '@/components/provider/CnBaseUrlPresets.vue'
import OllamaCloudUsageSettings from '@/components/provider/OllamaCloudUsageSettings.vue'
import UpstreamUsageConfigEditor from '@/components/provider/UpstreamUsageConfigEditor.vue'
import UpstreamBalanceAlertEditor from '@/components/provider/UpstreamBalanceAlertEditor.vue'
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
  useAnthropicAPIKeyAuthSchemeOptions,
  useCodexFingerprintModeOptions,
  useOpenAIOAuthClientPolicyOptions,
  useWebSearchEmulationOptions,
} from '@/components/provider/form/providerFormOptions'
import { applyInterceptWarmup } from '@/components/provider/credentialsBuilder'
import {
  applyHeaderOverride,
  isHeaderOverrideCapable,
  validateHeaderOverrideRows,
} from '@/components/provider/form/headerOverrides'
import { buildPlanTypeOptions } from '@/components/provider/form/planType'
import {
  apiKeyPreset,
  cnSupportsNativeResponses,
  defaultCNAdaptiveBaseUrls,
  defaultCNBaseUrl,
  derivedEndpointOverrides,
  derivedEndpointSlots,
  ENDPOINT_SLOT_PROTOCOLS,
  providerModeChoices,
  type CnProviderMode,
  type CnApiProtocol,
  type CnNativeApiProtocol,
} from '@/components/provider/form/providerEndpoints'
import { formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import { VERTEX_LOCATION_OPTIONS, groupedProviderSelectOptions } from '@/constants/provider'
import { supportsUpstreamBalanceAlert } from '@/utils/upstreamBalanceAlert'
import { resolveUpstreamUsageConfig } from '@/utils/upstreamUsage'
import type { SyncUpstreamModelsResult } from '@/api/admin/providers'
import { buildModelMappingObject, type QoderSite } from '@/composables/useModelWhitelist'

// EditProviderModal.vue presents provider settings, validates draft fields, and submits
// administration updates. Authentication type and platform determine the visible controls
// and validation requirements. The component owns request progress, translated notices,
// field revelation, and endpoint watchers, while typed drafts, response hydration, and
// platform patch construction live in adjacent modules.
//
// Opening the dialog or replacing its provider restores the server values. CN endpoint
// watchers remain suppressed through the next Vue tick so stored custom addresses survive
// hydration. Submission checks redacted-secret presence without manufacturing replacement
// secrets, loads protocol defaults only for independent providers, and reports the updated
// response before requesting closure. Shadow providers keep the restricted credential
// update selected by their model editor.

// draft owns the persistent form refs used by the template and serializers. UI request
// progress and loaded option lists stay in this component for its mounted lifetime.
const draft = createProviderEditDraft()
const {
  upstreamProtocols,
  imageModes,
  editBaseUrl,
  editApiKey,
  upstreamUsageEnabled,
  balanceAlert,
  upstreamUsageAdapter,
  upstreamUsageBaseUrl,
  upstreamUsageWalletAccessToken,
  upstreamUsageWalletUserId,
  geminiProviderType,
  geminiAIStudioTier,
  editApiProtocol,
  editProviderMode,
  editZhipuOrganization,
  editZhipuProject,
  editAdaptiveBaseUrls,
  editBedrockAccessKeyId,
  editBedrockSecretAccessKey,
  editBedrockSessionToken,
  editBedrockRegion,
  editBedrockForceGlobal,
  editBedrockApiKeyValue,
  editVertexProjectId,
  editVertexClientEmail,
  editVertexLocation,
  modelMappings,
  openAICompactModelMappings,
  modelRestrictionMode,
  allowedModels,
  qoderModelRestrictionTouched,
  qoderModelWhitelistTouched,
  qoderSite,
  poolModeEnabled,
  poolModeRetryCount,
  poolModeRetryStatusCodesInput,
  customErrorCodesEnabled,
  selectedErrorCodes,
  headerOverrideEnabled,
  headerOverrideRows,
  grokOAuthCustomBaseUrlEnabled,
  grokOAuthBaseUrl,
  grokClientToolCacheEnabled,
  interceptWarmupRequests,
  autoPauseOnExpired,
  autoPause5hThreshold,
  autoPause7dThreshold,
  autoPause5hDisabled,
  autoPause7dDisabled,
  upstreamRequestIdHeader,
  allowOverages,
  antigravityProjectId,
  antigravityWhitelistModels,
  antigravityModelMappings,
  tempUnschedEnabled,
  providerSchedulingThresholdOverrideEnabled,
  providerSchedulingThresholdOverrideValue,
  tempUnschedRules,
  windowCostEnabled,
  windowCostLimit,
  windowCostStickyReserve,
  sessionLimitEnabled,
  maxSessions,
  sessionIdleTimeout,
  rpmLimitEnabled,
  baseRpm,
  rpmStrategy,
  rpmStickyBuffer,
  userMsgQueueMode,
  tlsFingerprintEnabled,
  tlsFingerprintProfileId,
  tlsFingerprintRouterId,
  sessionIdMaskingEnabled,
  cacheTTLOverrideEnabled,
  cacheTTLOverrideTarget,
  customBaseUrlEnabled,
  customBaseUrl,
  openaiPassthroughEnabled,
  openaiFlattenNamespacesEnabled,
  editPlanType,
  openAICompactMode,
  openAINativeCompactionV2Mode,
  openAIResponsesContinuationSupported,
  openAIImagesGIFEnabled,
  openAIImagesURLToB64JSON,
  responsesWSConnectionMode,
  codexCLIOnlyAllowClaudeCodeEnabled,
  openAIOAuthClientPolicy,
  codexFingerprintMode,
  codexImageToolMode,
  codexImageResponsesModel,
  anthropicPassthroughEnabled,
  anthropicAPIKeyAuthScheme,
  webSearchEmulationMode,
  editQuotaLimit,
  editQuotaDailyLimit,
  editQuotaWeeklyLimit,
  editQuotaMonthlyLimit,
  editDailyResetMode,
  editDailyResetHour,
  editWeeklyResetMode,
  editMonthlyResetMode,
  editWeeklyResetDay,
  editMonthlyResetDay,
  editWeeklyResetHour,
  editMonthlyResetHour,
  editResetTimezone,
  form,
} = draft

// ProviderEditChoice identifies an administrator-loaded TLS selector option.
interface ProviderEditChoice {
  id: number // Persisted TLS option identifier.
  name: string // Administrator-visible option display name.
}

// CNProtocolOption names one native CN endpoint slot edited in adaptive mode.
interface CNProtocolOption {
  value: CnNativeApiProtocol // Native protocol endpoint key.
}

// Props supplies the visible provider and selectable administration records.
interface Props {
  show: boolean // Whether the parent opens the dialog.
  // A null provider hides the form; opening or replacing a non-null response
  // triggers hydration so drafts correspond to the parent-selected provider identity.
  provider: Provider | null // Provider response selected for editing.
  proxies: Proxy[] // Available outbound proxy records.
  groups: AdminGroup[] // Available provider group records.
}

// props holds the parent-owned provider response and option lists consumed by the mounted
// edit dialog.
const props = defineProps<Props>()

// emit publishes dialog closure and saved provider responses to the owning provider page
// after successful operations.
const emit = defineEmits<{
  close: [] // Parent closes the dialog.
  updated: [provider: Provider] // Parent replaces the saved provider.
}>()

// t resolves labels and notices through the active locale without changing the stored provider values.
const { t } = useI18n()

// appStore provides the notification actions used by validation, loading, and provider
// update results in this dialog.
const appStore = useAppStore()

// isSparkShadow reports whether forwarding credentials and proxy selection belong to a
// parent provider, hiding independent proxy editing.
const isSparkShadow = computed(() => props.provider?.parent_provider_id != null)

// handleOllamaCloudUsageUpdated publishes refreshed Ollama usage through the provider
// update event. When a provider remains selected, its existing fields accompany the new
// usage state so the parent can replace that row.
const handleOllamaCloudUsageUpdated = (state: OllamaCloudUsageState) => {
  if (props.provider) emit('updated', { ...props.provider, ollama_cloud_usage: state })
}

// baseUrlHint selects the translated forwarding-address hint for the current platform and
// Gemini endpoint source in this dialog.
const baseUrlHint = computed(() => {
  if (props.provider?.platform === 'gemini' && geminiProviderType.value === 'third_party') {
    return t('admin.providers.gemini.connectionSource.thirdPartyBaseUrlHint')
  }
  const hintKey = platformApiKeyPreset.value.baseUrlHintKey
  return hintKey ? t(hintKey) : ''
})

// submitting tracks the update request lifetime so the submit button remains disabled
// until completion or failure.
const submitting = ref(false)

// geminiProviderTypeOptions provides the translated official and third-party endpoint
// choices used by the Gemini source selector.
const geminiProviderTypeOptions = computed(() => [
  { value: 'official', label: t('admin.providers.gemini.connectionSource.official') },
  { value: 'third_party', label: t('admin.providers.gemini.connectionSource.thirdParty') },
])

// geminiAIStudioTierOptions provides the translated free and paid AI Studio tiers used by
// official Gemini accounts.
const geminiAIStudioTierOptions = computed(() => [
  { value: 'aistudio_free', label: t('admin.providers.gemini.tier.aiStudio.free') },
  { value: 'aistudio_paid', label: t('admin.providers.gemini.tier.aiStudio.paid') },
])

// geminiProviderTypeHint selects the explanatory source text displayed under the Gemini
// endpoint selector for its current value.
const geminiProviderTypeHint = computed(() =>
  geminiProviderType.value === 'third_party'
    ? t('admin.providers.gemini.connectionSource.thirdPartyHint')
    : t('admin.providers.gemini.connectionSource.officialHint'),
)

// isCNApiKeyProvider reports whether API-key credentials expose editable CN billing modes
// and protocol-specific forwarding endpoints in this form.
const isCNApiKeyProvider = computed(
  () =>
    props.provider?.type === 'apikey' &&
    (props.provider.platform === 'kimi' ||
      props.provider.platform === 'zhipu' ||
      props.provider.platform === 'deepseek'),
)

// cnPresetPlatform narrows the provider platform for the CN preset component. The fallback
// is used while that component is hidden for other providers, keeping its prop within the
// accepted platform union.
const cnPresetPlatform = computed<'kimi' | 'zhipu' | 'deepseek'>(() => {
  const platform = props.provider?.platform
  if (platform === 'kimi' || platform === 'zhipu' || platform === 'deepseek') {
    return platform
  }
  return 'kimi'
})

// balanceAlertSupported recomputes balance-alert eligibility from authentication type,
// platform, and the currently edited CN billing mode.
const balanceAlertSupported = computed(() =>
  supportsUpstreamBalanceAlert(
    props.provider?.type ?? '',
    props.provider?.platform ?? '',
    editProviderMode.value,
  ),
)
// syncingForm suppresses CN endpoint watchers during synchronous hydration and the
// subsequent Vue watcher flush, preserving stored relay addresses.
const syncingForm = ref(false)

// modeChoices lists the API-key provider modes of the edited platform; empty hides the
// selector. providerModeSegmentOptions and selectedModeChoice present them as a segmented
// control with the current mode's description.
const modeChoices = computed(() =>
  props.provider?.type === 'apikey' ? providerModeChoices(props.provider.platform) : [],
)
const providerModeSegmentOptions = computed(() =>
  modeChoices.value.map((choice) => ({ value: choice.value, label: t(choice.titleKey) })),
)
const selectedModeChoice = computed(() =>
  modeChoices.value.find((choice) => choice.value === editProviderMode.value),
)

// editAdaptiveProtocolOptions builds the CN endpoint fields supported by the platform,
// adding Responses only where its native endpoint exists.
const editAdaptiveProtocolOptions = computed<CNProtocolOption[]>(() => {
  const opts: CNProtocolOption[] = [{ value: 'chat_completions' }, { value: 'anthropic' }]
  if (cnSupportsNativeResponses(props.provider?.platform ?? '')) opts.push({ value: 'responses' })
  return opts
})
// endpointSlots lists the per-protocol address inputs with catalog protocol names. CN
// adaptive mode edits every native slot prefilled from presets; root-derived platforms
// edit optional overrides whose placeholders follow the entered or official root.
const endpointSlots = computed(() => {
  const slots =
    isCNApiKeyProvider.value && editApiProtocol.value === 'adaptive'
      ? editAdaptiveProtocolOptions.value.map(({ value }) => ({ slot: value, placeholder: '' }))
      : derivedEndpointSlots(
          props.provider?.type === 'apikey' ? props.provider.platform : '',
          editBaseUrl.value,
          editProviderMode.value,
        )
  return slots.map((item) => ({
    ...item,
    label: protocolName(ENDPOINT_SLOT_PROTOCOLS[item.slot]),
  }))
})
// Protocol changes translate the current endpoint into the adaptive map so changing editor
// mode does not discard an administrator-entered relay address.
watch(editApiProtocol, (protocol, previousProtocol) => {
  if (!isCNApiKeyProvider.value || syncingForm.value) return
  if (protocol === 'adaptive') {
    const defaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, editProviderMode.value)
    for (const item of editAdaptiveProtocolOptions.value) {
      if (!editAdaptiveBaseUrls.value[item.value])
        editAdaptiveBaseUrls.value[item.value] = defaults[item.value]
    }
    if (previousProtocol !== 'adaptive' && editBaseUrl.value.trim()) {
      editAdaptiveBaseUrls.value[previousProtocol] = editBaseUrl.value.trim()
    }
    editBaseUrl.value = editAdaptiveBaseUrls.value.chat_completions
    return
  }
  // Leaving adaptive mode selects the stored endpoint for the target protocol, using its
  // platform preset only when that entry is empty.
  if (previousProtocol === 'adaptive') {
    editBaseUrl.value =
      editAdaptiveBaseUrls.value[protocol] ||
      defaultCNBaseUrl(props.provider!.platform, editProviderMode.value, protocol)
    return
  }
  editBaseUrl.value = defaultCNBaseUrl(props.provider!.platform, editProviderMode.value, protocol)
})
// Billing-mode changes replace blank or previous-default adaptive endpoints while
// retaining custom entries; the hydration guard prevents this policy from rewriting saved
// URLs.
watch(editProviderMode, (mode, previousMode) => {
  if (!isCNApiKeyProvider.value || syncingForm.value) return
  // DeepSeek exposes pay-as-you-go endpoints only; normalize a coding-mode selection
  // before any preset rewrite to keep dependent fields consistent.
  const effectiveMode = props.provider!.platform === 'deepseek' && mode === 'coding' ? 'payg' : mode
  if (effectiveMode !== mode) {
    editProviderMode.value = effectiveMode
    return
  }
  if (editApiProtocol.value === 'adaptive') {
    const previousDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, previousMode)
    const nextDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, mode)
    for (const item of editAdaptiveProtocolOptions.value) {
      if (
        !editAdaptiveBaseUrls.value[item.value] ||
        editAdaptiveBaseUrls.value[item.value] === previousDefaults[item.value]
      ) {
        editAdaptiveBaseUrls.value[item.value] = nextDefaults[item.value]
      }
    }
    editBaseUrl.value = editAdaptiveBaseUrls.value.chat_completions
    return
  }
  editBaseUrl.value = defaultCNBaseUrl(props.provider!.platform, mode, editApiProtocol.value)
})

// onCnPresetSelect applies a CN preset to billing mode and endpoint drafts. The editor
// switches to adaptive mode, copies the selected native endpoint, and uses its chat
// endpoint for the shared base URL.
function onCnPresetSelect(preset: {
  mode: CnProviderMode // Selected CN billing mode.
  protocol: CnApiProtocol // Preset native protocol selection.
  url: string // Preset forwarding endpoint URL.
}) {
  editProviderMode.value = preset.mode
  editApiProtocol.value = 'adaptive'
  if (preset.protocol !== 'adaptive') editAdaptiveBaseUrls.value[preset.protocol] = preset.url
  editBaseUrl.value = editAdaptiveBaseUrls.value.chat_completions
}

// isGeminiThirdPartyBaseUrl checks whether an entered URL names a nonofficial Gemini host.
// Empty or malformed values return false, allowing the source watcher and submit
// validation to reject an unusable relay address.
const isGeminiThirdPartyBaseUrl = (value: string) => {
  const normalized = value.trim()
  if (!normalized) return false
  try {
    return new URL(normalized).hostname.toLowerCase() !== 'generativelanguage.googleapis.com'
  } catch {
    // URL parsing failures represent invalid relay addresses, so the caller uses its
    // normal field-validation path instead of surfacing a parser exception.
    return false
  }
}

// isBedrockAPIKeyMode selects the Bedrock API-key editor only when stored credentials
// explicitly identify that authentication mode.
const isBedrockAPIKeyMode = computed(
  () =>
    props.provider?.type === 'bedrock' &&
    (props.provider?.credentials as Record<string, unknown>)?.auth_mode === 'apikey',
)

// GROK_CLIENT_TOOL_CACHE_EXTRA_KEY names the persisted Grok client-tool preference written
// with both enabled and disabled values on save.
const GROK_CLIENT_TOOL_CACHE_EXTRA_KEY = 'grok_client_tool_cache_enabled'

// headerOverrideCapable reports whether the selected platform and authentication type
// expose editable outbound header overrides in the form.
const headerOverrideCapable = computed(
  () => !!props.provider && isHeaderOverrideCapable(props.provider.platform, props.provider.type),
)

// isSyncingAntigravityUpstream prevents overlapping model synchronization requests while
// the dialog appends the current response to its mapping editor.
const isSyncingAntigravityUpstream = ref(false)

// PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY names the optional saturation threshold
// written only when its effective value changes during an edit.
const PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY = 'provider_scheduling_threshold'

// supportsProviderSchedulingThresholdOverride controls visibility of the per-provider
// threshold switch using the platform support rule shared with hydration.
const supportsProviderSchedulingThresholdOverride = computed(() =>
  supportsProviderSchedulingThresholdOverridePlatform(props.provider?.platform),
)

// tlsFingerprintProfiles holds profile identifiers and labels loaded for the current
// dialog, with empty results after a failed request.
const tlsFingerprintProfiles = ref<ProviderEditChoice[]>([])

// tlsFingerprintRouters holds router identifiers and labels loaded for the current dialog,
// with empty results after a failed request.
const tlsFingerprintRouters = ref<ProviderEditChoice[]>([])
// tlsFingerprintProfileOptions combines the default, optional random, and loaded profile
// choices for the TLS simulation selector.
const tlsFingerprintProfileOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.defaultProfile') },
  ...(tlsFingerprintProfiles.value.length > 0
    ? [{ value: -1, label: t('admin.providers.quotaControl.tlsFingerprint.randomProfile') }]
    : []),
  ...tlsFingerprintProfiles.value.map((profile) => ({ value: profile.id, label: profile.name })),
])
// tlsFingerprintRouterOptions combines the no-router option with loaded router records for
// the OpenAI OAuth TLS selector.
const tlsFingerprintRouterOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.noRouter') },
  ...tlsFingerprintRouters.value.map((router) => ({ value: router.id, label: router.name })),
])

// webSearchEmulationOptions provides translated default, enabled, and disabled choices
// shared with the provider web-search override control.
const webSearchEmulationOptions = useWebSearchEmulationOptions()

// anthropicAPIKeyAuthSchemeOptions provides the supported authentication-header choices
// used by Anthropic API-key providers in this dialog.
const anthropicAPIKeyAuthSchemeOptions = useAnthropicAPIKeyAuthSchemeOptions()
// webSearchGlobalEnabled hides provider overrides until the global configuration confirms
// enabled search emulation with at least one provider.
const webSearchGlobalEnabled = ref(false)
// Quota notification drafts stay with their shared composable, which loads global
// availability and restores or serializes this provider policy.
const {
  globalEnabled: quotaNotifyGlobalEnabled,
  state: quotaNotifyState,
  loadGlobalState: loadQuotaNotifyGlobal,
  loadFromExtra: loadQuotaNotifyFromExtra,
  writeToExtra: writeQuotaNotifyToExtra,
  setField: setQuotaNotifyField,
  reset: resetQuotaNotify,
} = useQuotaNotifyState()

// isQoderCosyProvider reports whether the current credentials use Qoder COSY, enabling
// site editing and its model-restriction tracking.
const isQoderCosyProvider = computed(
  () => props.provider?.platform === 'qoder' && props.provider?.type === 'cosy',
)

// originalQoderSite reads the saved Qoder site for comparison with the draft. Missing and
// unknown site values select global, matching hydration and allowing the warning to
// reflect a local change.
const originalQoderSite = computed<QoderSite>(() => {
  const credentials = props.provider?.credentials as Record<string, unknown> | undefined
  return credentials?.site === 'cn' ? 'cn' : 'global'
})

// qoderSiteChanged shows the site-change notice only when the Qoder COSY draft differs
// from its saved site.
const qoderSiteChanged = computed(
  () => isQoderCosyProvider.value && qoderSite.value !== originalQoderSite.value,
)

// supportsOAuthLikeModelRestriction selects OpenAI, Grok, Gemini OAuth and Qoder COSY
// accounts for shared model-restriction editing and persistence.
const supportsOAuthLikeModelRestriction = computed(
  () =>
    (props.provider?.platform === 'openai' && props.provider?.type === 'oauth') ||
    (props.provider?.platform === 'grok' && props.provider?.type === 'oauth') ||
    (props.provider?.platform === 'gemini' && props.provider?.type === 'oauth') ||
    isQoderCosyProvider.value,
)

// isAnthropicOAuthLikeProvider reports whether the account uses Anthropic OAuth or setup-
// token authentication for quota and request controls.
const isAnthropicOAuthLikeProvider = computed(
  () =>
    props.provider?.platform === 'anthropic' &&
    (props.provider?.type === 'oauth' || props.provider?.type === 'setup-token'),
)
// supportsTLSFingerprintRouter limits router selection to OpenAI OAuth accounts while
// other supported accounts use profile selection alone.
const supportsTLSFingerprintRouter = computed(
  () => props.provider?.platform === 'openai' && props.provider?.type === 'oauth',
)

// isServiceAccountProvider selects Gemini and Anthropic service-account credentials for
// the Vertex project, email, and location validation path.
const isServiceAccountProvider = computed(
  () =>
    (props.provider?.platform === 'gemini' || props.provider?.platform === 'anthropic') &&
    props.provider?.type === 'service_account',
)

// isOpenAIOAuthOrAPIKey controls OpenAI options available to OAuth and API-key accounts in
// the request settings panel.
const isOpenAIOAuthOrAPIKey = computed(
  () =>
    props.provider?.platform === 'openai' &&
    (props.provider?.type === 'oauth' || props.provider?.type === 'apikey'),
)

// showModelRestriction shows the shared model editor for compatible authentication types
// while Antigravity uses its separate mapping panel.
const showModelRestriction = computed(() => {
  const provider = props.provider
  if (!provider) return false
  return (
    (provider.type === 'apikey' && provider.platform !== 'antigravity') ||
    supportsOAuthLikeModelRestriction.value ||
    isServiceAccountProvider.value ||
    provider.type === 'bedrock'
  )
})

// showCredentialsSection shows editable credential controls for API keys, Bedrock,
// supported service accounts, and Antigravity OAuth project settings.
const showCredentialsSection = computed(() => {
  const provider = props.provider
  if (!provider) return false
  return (
    ['apikey', 'bedrock'].includes(provider.type) ||
    isServiceAccountProvider.value ||
    (provider.platform === 'antigravity' && provider.type === 'oauth')
  )
})

// showGeminiTier shows AI Studio tier selection only while the provider uses the official
// Gemini source.
const showGeminiTier = computed(
  () => props.provider?.platform === 'gemini' && geminiProviderType.value === 'official',
)

// platformApiKeyPreset resolves the edited platform's API-key defaults for the current
// provider mode. The placeholders derived from it leave the draft values unchanged.
const platformApiKeyPreset = computed(() =>
  apiKeyPreset(props.provider?.platform ?? '', editProviderMode.value),
)
// apiKeyBaseUrlPlaceholder shows the preset forwarding address, which can depend on the mode.
const apiKeyBaseUrlPlaceholder = computed(() => platformApiKeyPreset.value.baseUrlPlaceholder)

// apiKeyPlaceholder selects an example key prefix for the current platform and Gemini
// source without exposing stored credentials.
const apiKeyPlaceholder = computed(() =>
  props.provider?.platform === 'gemini' && geminiProviderType.value === 'third_party'
    ? 'api-key-...'
    : platformApiKeyPreset.value.apiKeyPlaceholder,
)

// qoderSiteOptions provides the global and China site labels and test identifiers used by
// the Qoder selector.
const qoderSiteOptions = computed(() => [
  {
    value: 'global' as QoderSite,
    label: t('admin.providers.qoder.site.global'),
    testid: 'edit-qoder-site-global',
  },
  {
    value: 'cn' as QoderSite,
    label: t('admin.providers.qoder.site.cn'),
    testid: 'edit-qoder-site-cn',
  },
])

// tabsRef holds the mounted tab controller used to validate hidden fields and reveal
// business-validation failures to administrators.
const tabsRef = ref<InstanceType<typeof SettingsTabs> | null>(null)

// formTabs builds the dialog categories and hides panels without applicable controls.
// Basic, scheduling, and request tabs remain visible; quota and model tabs follow provider
// capabilities while mounted contents retain drafts.
const formTabs = computed(() => {
  const provider = props.provider
  const hasQuota =
    !!provider &&
    (provider.type === 'apikey' ||
      provider.type === 'bedrock' ||
      isAnthropicOAuthLikeProvider.value ||
      !!provider.ollama_cloud_usage?.eligible)
  const hasModels = showModelRestriction.value || provider?.platform === 'antigravity'
  return [
    { key: 'basic', label: t('admin.providers.tabs.basic') },
    { key: 'models', label: t('admin.providers.tabs.models'), hidden: !hasModels },
    { key: 'scheduling', label: t('admin.providers.tabs.scheduling') },
    { key: 'quota', label: t('admin.providers.tabs.quota'), hidden: !hasQuota },
    { key: 'request', label: t('admin.providers.tabs.request') },
  ]
})

// failAt reports a business-validation error and reveals its owning field. The tab
// controller locates the data-provider-field marker, so a failure in a hidden panel
// becomes visible before the administrator corrects it.
function failAt(message: string, field: string) {
  appStore.showError(message)
  void tabsRef.value?.revealField(`[data-provider-field="${field}"]`)
}
// Global search controls are optional to editing: a failed settings read hides the
// override until a later dialog instance loads valid availability.
adminAPI.settings
  .getWebSearchEmulationConfig()
  .then((cfg) => {
    webSearchGlobalEnabled.value = cfg?.enabled === true && (cfg?.providers?.length ?? 0) > 0
  })
  .catch(() => {
    webSearchGlobalEnabled.value = false
  })

loadQuotaNotifyGlobal()
// quotaLimits binds the persisted refs to shared quota controls; disabling a limit clears
// its schedule before the patch builder reads the refs.
const { limits: quotaLimits, setLimit: setQuotaLimit } = bindQuotaLimits({
  totalLimit: editQuotaLimit,
  dailyLimit: editQuotaDailyLimit,
  weeklyLimit: editQuotaWeeklyLimit,
  monthlyLimit: editQuotaMonthlyLimit,
  dailyResetMode: editDailyResetMode,
  dailyResetHour: editDailyResetHour,
  weeklyResetMode: editWeeklyResetMode,
  monthlyResetMode: editMonthlyResetMode,
  weeklyResetDay: editWeeklyResetDay,
  monthlyResetDay: editMonthlyResetDay,
  weeklyResetHour: editWeeklyResetHour,
  monthlyResetHour: editMonthlyResetHour,
  resetTimezone: editResetTimezone,
})

// codexFingerprintModeOptions provides translated choices for the OpenAI OAuth client
// fingerprint mode selector in the request panel.
const codexFingerprintModeOptions = useCodexFingerprintModeOptions()

// planTypeOptions builds translated subscription choices while retaining the current
// custom or aliased tier as an editable option.
const planTypeOptions = computed(() =>
  buildPlanTypeOptions(editPlanType.value, t('admin.providers.openai.planTypeClear')),
)

// openAIOAuthClientPolicyOptions provides translated OAuth client restrictions for the
// OpenAI request policy selector used by the dialog.
const openAIOAuthClientPolicyOptions = useOpenAIOAuthClientPolicyOptions()

// defaultBaseUrl selects the forwarding fallback used when an API-key address is blank. CN
// providers use their edited billing mode and protocol, preventing their empty address
// from selecting the Anthropic fallback; other platforms use their preset.
const defaultBaseUrl = computed(() => {
  if (
    props.provider?.platform === 'kimi' ||
    props.provider?.platform === 'zhipu' ||
    props.provider?.platform === 'deepseek'
  ) {
    return defaultCNBaseUrl(props.provider.platform, editProviderMode.value, editApiProtocol.value)
  }
  return platformApiKeyPreset.value.defaultBaseUrl
})

// A third-party source needs a relay address; clear the official Google host when
// switching sources so it cannot be submitted as a relay.
watch(geminiProviderType, (providerType) => {
  if (props.provider?.platform !== 'gemini' || providerType !== 'third_party') return
  if (!isGeminiThirdPartyBaseUrl(editBaseUrl.value)) {
    editBaseUrl.value = ''
  }
})

// statusOptions provides active and inactive status choices and retains an error option
// when the loaded provider currently has that status.
const statusOptions = computed(() => {
  const options = [
    { value: 'active', label: t('common.active') },
    { value: 'inactive', label: t('common.inactive') },
  ]
  if (form.status === 'error') {
    options.push({ value: 'error', label: t('admin.providers.status.error') })
  }
  return options
})
// vertexLocationOptions groups supported Vertex locations for the service-account region
// selector without changing the chosen location draft.
const vertexLocationOptions = groupedProviderSelectOptions(VERTEX_LOCATION_OPTIONS)

// expiresAtInput adapts the expiry timestamp to the local datetime input. Formatting and
// parsing delegate to shared helpers so empty input retains the nullable form value
// consumed by the update builder.
const expiresAtInput = computed({
  // get formats the provider expiry timestamp for the local datetime control.
  get: () => formatDateTimeLocal(form.expires_at),
  // set parses the local datetime value into the nullable expiry draft.
  // Empty input uses the shared parser's null result, which the base patch
  // later converts into the API clearing sentinel.
  set: (value: string) => {
    form.expires_at = parseDateTimeLocal(value)
  },
})

// OpenCode model protocol tables belong to this dialog and are saved with its credential payload.
const openCodeProtocols = ref(openCodeDraft())

// connectionFingerprint serializes the drafted settings that decide which upstream a
// saved-provider model sync reaches. savedConnectionFingerprint records the value right
// after hydration; a difference, or any replacement API key, means sync would mix the
// stored credentials with an unsaved destination, so the form withholds it until saving.
const connectionFingerprint = computed(() =>
  JSON.stringify([
    editBaseUrl.value.trim(),
    editProviderMode.value,
    editApiProtocol.value,
    editAdaptiveBaseUrls.value,
    geminiProviderType.value,
  ]),
)
const savedConnectionFingerprint = ref('')
const connectionChanged = computed(
  () =>
    props.provider?.type === 'apikey' &&
    (editApiKey.value.trim() !== '' ||
      connectionFingerprint.value !== savedConnectionFingerprint.value),
)

// storeModelProtocolSuggestions keeps per-model protocol suggestions returned by a model
// sync as the current mode's accepted catalog table. Responses without suggestions leave
// the table unchanged; manual overrides stay separate in either case.
function storeModelProtocolSuggestions(result: SyncUpstreamModelsResult) {
  if (result.model_protocols)
    openCodeProtocols.value.suggestions[editProviderMode.value] = { ...result.model_protocols }
}

// resolvedUpstreamUsageAdapter resolves the usage adapter for the edited provider draft,
// or null when its type and mode support no usage query and the editor stays hidden.
const resolvedUpstreamUsageAdapter = computed(() =>
  props.provider
    ? resolveUpstreamUsageConfig({
        type: props.provider.type,
        platform: props.provider.platform,
        credentials: { provider_mode: editProviderMode.value },
        extra: { upstream_usage_query: { adapter: upstreamUsageAdapter.value } },
      }).adapter
    : null,
)

// syncFormFromProvider restores editable settings from a provider response. Null input
// clears native protocols and leaves other refs unchanged. For a provider response, the
// component marks hydration active before delegating synchronous draft writes. The guard
// remains set through the next Vue tick because CN mode and protocol watchers run after
// those writes and would otherwise replace custom addresses with presets. Later
// administrator changes use their normal watcher behavior. The connection snapshot is
// taken after hydration so the restored values count as saved.
const syncFormFromProvider = (newProvider: Provider | null) => {
  if (!newProvider) {
    upstreamProtocols.value = undefined
    return
  }
  openCodeProtocols.value = openCodeDraft(newProvider.credentials as Record<string, unknown>)
  syncingForm.value = true
  // Keep the hydration guard through Vue pre-flush watchers; releasing it synchronously
  // would let mode restoration overwrite saved custom forwarding addresses.
  void nextTick(() => {
    syncingForm.value = false
  })
  hydrateProviderEditDraft(draft, newProvider, {
    loadFromExtra: loadQuotaNotifyFromExtra,
    reset: resetQuotaNotify,
  })
  savedConnectionFingerprint.value = connectionFingerprint.value
}

// loadTLSProfiles loads profile labels for the TLS selector in this dialog. Request
// failures clear the local list, leaving the default option available without blocking
// provider editing or submission.
async function loadTLSProfiles() {
  try {
    const profiles = await adminAPI.tlsFingerprintProfiles.list()
    tlsFingerprintProfiles.value = profiles.map((p) => ({ id: p.id, name: p.name }))
  } catch {
    // A failed optional profile request leaves only default selection available; provider
    // drafts and unrelated controls remain usable in the dialog.
    tlsFingerprintProfiles.value = []
  }
}

// loadTLSRouters loads router labels for the OpenAI OAuth TLS selector. Request failures
// clear the local list, leaving the no-router option available without blocking the rest
// of the provider form.
async function loadTLSRouters() {
  try {
    const routers = await adminAPI.tlsFingerprintRouters.list()
    tlsFingerprintRouters.value = routers.map((router) => ({ id: router.id, name: router.name }))
  } catch {
    // A failed optional router request leaves the no-router choice available; the rest of
    // the provider form can still be edited.
    tlsFingerprintRouters.value = []
  }
}

// Reopening the same provider discards unsaved drafts, while replacing the response object
// hydrates the new identity before refreshing TLS option labels.
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
  { immediate: true },
)

// touchQoderModelRestriction marks an explicit model editor interaction for Qoder COSY
// accounts. Other provider types need no tracking because their model restrictions are
// serialized on each ordinary save.
const touchQoderModelRestriction = () => {
  if (isQoderCosyProvider.value) {
    qoderModelRestrictionTouched.value = true
  }
}

// setAllowedModels records an explicit whitelist selection and replaces the draft. Qoder
// additionally records whitelist interaction, distinguishing administrator intent from
// model rows generated merely by opening its editor panel.
const setAllowedModels = (models: string[]) => {
  touchQoderModelRestriction()
  if (isQoderCosyProvider.value) {
    qoderModelWhitelistTouched.value = true
  }
  allowedModels.value = models
}

// syncAntigravityUpstreamModels fetches upstream models and appends missing request-side
// identities. Repeated model names leave existing mappings intact, and a request already
// in progress prevents another start. The response is applied to the current mapping draft
// when it arrives; the function does not reject results after provider replacement. Empty
// results and failures produce translated notices, and the in-progress flag is released in
// finally so another synchronization can be attempted.
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
      appStore.showSuccess(
        t('admin.providers.syncUpstreamModelsSuccess', {
          count: addedCount,
          total: upstreamModels.length,
        }),
      )
    } else {
      appStore.showInfo(
        t('admin.providers.syncUpstreamModelsNoChanges', { count: upstreamModels.length }),
      )
    }
  } catch (error) {
    const message =
      error instanceof Error ? error.message : t('admin.providers.syncUpstreamModelsFailed')
    appStore.showError(t('admin.providers.syncUpstreamModelsError', { message }))
  } finally {
    isSyncingAntigravityUpstream.value = false
  }
}

// buildTempUnschedRules converts editable suspension rules into credential values. Rules
// with invalid HTTP codes, nonpositive durations, or empty keyword lists are omitted;
// accepted numeric values are truncated to integer units before submission.
const buildTempUnschedRules = (rules: TempUnschedRuleForm[]) => {
  const out: Array<{
    error_code: number // Matched HTTP status code.
    keywords: string[] // Response substring match terms.
    duration_minutes: number // Suspension duration in minutes.
    description: string // Administrator rule description text.
  }> = []

  for (const rule of rules) {
    // HTTP error matching accepts status codes from 100 through 599 and positive
    // durations; malformed rows are omitted so empty rules can fail validation.
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
    // Suspension rules serialize integer HTTP codes and minute durations, matching the
    // backend fields after the editable numbers pass range checks.
    out.push({
      error_code: Math.trunc(errorCode),
      keywords,
      duration_minutes: Math.trunc(duration),
      description: rule.description.trim(),
    })
  }

  return out
}

// applyTempUnschedConfig writes validated temporary-suspension settings into a credential
// copy. Disabled configuration removes both keys; enabled configuration with no valid
// rules reveals the rule editor and stops submission by returning false.
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

// clampProviderSchedulingThresholdOverride clamps a submitted scheduling percentage to its
// accepted integer range. Empty or nonnumeric values use one hundred, and finite values
// remain between one and one hundred inclusive.
function clampProviderSchedulingThresholdOverride(value: unknown): number {
  // Scheduling thresholds are integer percentages; clamp submitted values to the accepted
  // range and use full saturation for empty input.
  return Math.min(100, Math.max(1, Math.trunc(Number(value) || 100)))
}

// applyProviderSchedulingThresholdOverridePatch writes a threshold change into mutable
// credentials for supported platforms. Disabling an existing valid override writes null,
// which tells the backend to clear it. Unchanged effective values leave the copied field
// untouched, and unsupported platforms return without modification. The comparison uses
// normalized stored values and the clamped draft so display defaults do not create a new
// override during unrelated provider edits.
const applyProviderSchedulingThresholdOverridePatch = (
  credentials: Record<string, unknown>,
  currentCredentials: Record<string, unknown>,
  platform: Provider['platform'] | undefined = props.provider?.platform,
) => {
  if (!supportsProviderSchedulingThresholdOverridePlatform(platform)) {
    return
  }
  const current = normalizeProviderSchedulingThresholdOverride(
    currentCredentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY],
  )
  if (!providerSchedulingThresholdOverrideEnabled.value) {
    if (current !== null) {
      // A disabled existing override needs an explicit null patch because omission would
      // retain the backend credential value during merging.
      credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] = null
    }
    return
  }
  const next = clampProviderSchedulingThresholdOverride(
    providerSchedulingThresholdOverrideValue.value,
  )
  if (current !== next) {
    credentials[PROVIDER_SCHEDULING_THRESHOLD_CREDENTIAL_KEY] = next
  }
}

// splitTempUnschedKeywords splits comma- or semicolon-separated keywords for suspension
// rule validation. Whitespace is trimmed and empty fragments are removed so blank input
// cannot produce a usable rule by itself.
const splitTempUnschedKeywords = (value: string) => {
  return value
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

// formatDateTimeLocal uses the shared local-time formatter for the expiry input, matching
// other provider forms in the application.
const formatDateTimeLocal = formatDateTimeLocalInput

// parseDateTimeLocal uses the shared local-time parser for the expiry input and retains
// its nullable empty-input result.
const parseDateTimeLocal = parseDateTimeLocalInput

// handleClose requests dialog closure through the owning provider page event.
const handleClose = () => {
  emit('close')
}

// submitUpdateProvider submits a validated patch and reports the server response.
// Independent providers load the protocol catalog before normalizing credentials; shadows
// bypass that work because their parent owns forwarding configuration. Success publishes
// the updated provider before requesting closure. API failures show an error and leave the
// dialog open. The submitting flag covers catalog loading and persistence and is reset in
// finally for either outcome.
const submitUpdateProvider = async (providerID: number, updatePayload: ProviderEditPatch) => {
  submitting.value = true
  try {
    if (props.provider && !props.provider.parent_provider_id) {
      // Catalog defaults are resolved before credential normalization; awaiting them keeps
      // unknown selections from being replaced by an uninitialized option list.
      await loadProtocolCatalog()
      applyProviderProtocolPatch(
        props.provider,
        updatePayload,
        upstreamProtocols.value,
        nativeProtocolOptions(
          props.provider.platform,
          props.provider.type,
          String(
            props.provider.credentials?.auth_mode ??
              props.provider.credentials?.openai_auth_mode ??
              '',
          ),
        ),
      )
    }
    if (
      props.provider?.platform === 'openai' &&
      props.provider.type === 'apikey' &&
      !props.provider.parent_provider_id
    ) {
      updatePayload.credentials = { ...updatePayload.credentials, ...imageModes.value }
    }
    const updatedProvider = await adminAPI.providers.update(providerID, updatePayload)
    appStore.showSuccess(t('admin.providers.providerUpdated'))
    // Publish the server response before requesting closure so the provider page can
    // replace its row using persisted values from this update.
    emit('updated', updatedProvider)
    handleClose()
  } catch (error: any) {
    appStore.showError(error.message || t('admin.providers.failedToUpdate'))
  } finally {
    submitting.value = false
  }
}

// handleSubmit validates visible and hidden fields before building a provider update.
// Authentication-specific checks distinguish masked stored secrets from missing
// credentials and preserve the first failing field for revelation. Credential copies
// retain unowned settings while model and platform serializers replace the fields
// controlled by this editor. Extras are built only after credential validation succeeds,
// and the submission helper owns the API request, success publication, error notice, and
// dialog closure.
const handleSubmit = async () => {
  if (!props.provider) return
  const providerID = props.provider.id
  // The native form skips automatic validation; SettingsTabs reveals invalid mounted
  // fields in hidden panels before reporting browser validation errors.
  if (tabsRef.value && !(await tabsRef.value.validate())) return

  if (form.status !== 'active' && form.status !== 'inactive' && form.status !== 'error') {
    failAt(t('admin.providers.pleaseSelectStatus'), 'status')
    return
  }

  const updatePayload = buildProviderBasePatch(draft)
  try {
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
      // Empty forwarding input restores the platform endpoint; CN defaults also use
      // the edited billing mode and protocol instead of the Anthropic fallback.
      const newBaseUrl = enteredBaseUrl || defaultBaseUrl.value
      const newCredentials: Record<string, unknown> = {
        ...currentCredentials,
        base_url: newBaseUrl,
      }
      if (modeChoices.value.length > 0) newCredentials.provider_mode = editProviderMode.value
      // Root-derived address slots store only administrator overrides, and OpenCode keeps
      // its per-mode model protocol tables beside them.
      const endpointOverrides = derivedEndpointOverrides(
        props.provider.platform,
        editAdaptiveBaseUrls.value,
      )
      if (endpointOverrides) newCredentials.api_base_urls = endpointOverrides
      if (props.provider.platform === 'opencode') {
        Object.assign(newCredentials, openCodeCredentials(openCodeProtocols.value))
      }
      // The endpoint map and shared URL represent the same CN forwarding settings;
      // update both from the edited native slots before serializing credentials.
      if (isCNApiKeyProvider.value) {
        newCredentials.api_protocol = editApiProtocol.value
        if (editApiProtocol.value === 'adaptive') {
          const defaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, editProviderMode.value)
          const protocolBaseUrls: Record<string, string> = {}
          for (const item of editAdaptiveProtocolOptions.value) {
            protocolBaseUrls[item.value] = (
              editAdaptiveBaseUrls.value[item.value] || defaults[item.value]
            ).trim()
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
      // Redacted responses omit secret text. Prefer the explicit presence flag, falling
      // back to legacy plaintext only when that flag is absent.
      const hasExistingApiKey =
        props.provider.credentials_status?.has_api_key ?? Boolean(currentCredentials.api_key)
      if (editApiKey.value.trim()) {
        newCredentials.api_key = editApiKey.value.trim()
      } else if (!hasExistingApiKey) {
        failAt(t('admin.providers.apiKeyIsRequired'), 'api-key')
        return
      }
      // An empty wallet token means keep the server-stored secret, while the separately
      // editable wallet user ID can be removed from credentials.
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
          // Third-party Gemini endpoints have no AI Studio tier; removing this nonsecret
          // field clears the previous official-source choice during credential
          // replacement.
          delete newCredentials.tier_id
        } else {
          newCredentials.tier_id = geminiAIStudioTier.value
        }
      }
      if (props.provider.platform === 'qoder') {
        applyQoderModelRestriction(draft, newCredentials)
      } else {
        applyPersistedModelRestriction(draft, newCredentials)
      }
      if (props.provider.platform === 'openai') {
        const compactModelMapping = buildModelMappingObject(
          'mapping',
          [],
          openAICompactModelMappings.value,
        )
        if (compactModelMapping) {
          newCredentials.compact_model_mapping = compactModelMapping
        } else {
          delete newCredentials.compact_model_mapping
        }
      }
      applyPoolModeCredentials(draft, newCredentials)
      if (customErrorCodesEnabled.value) {
        newCredentials.custom_error_codes_enabled = true
        newCredentials.custom_error_codes = [...selectedErrorCodes.value]
      } else {
        delete newCredentials.custom_error_codes_enabled
        delete newCredentials.custom_error_codes
      }
      if (isHeaderOverrideCapable(props.provider.platform, 'apikey')) {
        if (headerOverrideEnabled.value) {
          const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
          if (headerError) {
            failAt(t(`admin.providers.headerOverride.${headerError}`), 'header-override')
            return
          }
        }
        applyHeaderOverride(
          newCredentials,
          headerOverrideEnabled.value,
          headerOverrideRows.value,
          'edit',
        )
      }
      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    } else if (
      (props.provider.platform === 'gemini' || props.provider.platform === 'anthropic') &&
      props.provider.type === 'service_account'
    ) {
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
      // Service-account JSON is redacted by current servers. Presence flags decide
      // availability; legacy responses without flags use their saved credential fields
      // instead.
      const credentialsStatus = props.provider.credentials_status
      const hasExistingServiceAccountJson = credentialsStatus
        ? Boolean(
            credentialsStatus.has_service_account_json || credentialsStatus.has_service_account,
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

      applyPersistedModelRestriction(draft, newCredentials)

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

      // Bedrock replacement inputs start blank because secrets are redacted; write only
      // entered secret values so unrelated edits keep stored authentication.
      if (isBedrockAPIKeyMode.value) {
        if (editBedrockApiKeyValue.value.trim()) {
          newCredentials.api_key = editBedrockApiKeyValue.value.trim()
        }
      } else {
        newCredentials.aws_access_key_id = editBedrockAccessKeyId.value.trim()
        if (editBedrockSecretAccessKey.value.trim()) {
          newCredentials.aws_secret_access_key = editBedrockSecretAccessKey.value.trim()
        }
        if (editBedrockSessionToken.value.trim()) {
          newCredentials.aws_session_token = editBedrockSessionToken.value.trim()
        }
      }
      applyPoolModeCredentials(draft, newCredentials)
      applyPersistedModelRestriction(draft, newCredentials)

      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    } else {
      const currentCredentials = (props.provider.credentials as Record<string, unknown>) || {}
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      applyInterceptWarmup(newCredentials, interceptWarmupRequests.value, 'edit')
      applyProviderSchedulingThresholdOverridePatch(newCredentials, currentCredentials)
      if (!applyTempUnschedConfig(newCredentials)) {
        return
      }

      updatePayload.credentials = newCredentials
    }
    if (supportsOAuthLikeModelRestriction.value) {
      // Spark shadows begin with an empty credential patch so inherited parent secrets and
      // unrelated authentication fields cannot accompany their editable model mappings.
      const currentCredentials =
        props.provider.platform === 'openai' && isSparkShadow.value
          ? {}
          : (updatePayload.credentials as Record<string, unknown>) ||
            (props.provider.credentials as Record<string, unknown>) ||
            {}
      const newCredentials: Record<string, unknown> = { ...currentCredentials }

      if (props.provider.platform === 'qoder') {
        applyQoderModelRestriction(draft, newCredentials)
        newCredentials.site = qoderSite.value
      } else if (props.provider.platform === 'openai') {
        applyOpenAIModelMappingCredentials(draft, newCredentials, props.provider)
      } else {
        applyPersistedModelRestriction(draft, newCredentials)
      }

      updatePayload.credentials = newCredentials
    }
    if (props.provider.platform === 'grok' && props.provider.type === 'oauth') {
      const currentCredentials =
        (updatePayload.credentials as Record<string, unknown>) ||
        (props.provider.credentials as Record<string, unknown>) ||
        {}
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
      applyHeaderOverride(
        newCredentials,
        headerOverrideEnabled.value,
        headerOverrideRows.value,
        'edit',
      )

      updatePayload.credentials = newCredentials

      const newExtra: Record<string, unknown> = {
        ...((props.provider.extra as Record<string, unknown>) || {}),
      }
      // Persist both boolean values because missing Grok cache configuration enables its
      // default; omitting false would lose an administrator opt-out.
      newExtra[GROK_CLIENT_TOOL_CACHE_EXTRA_KEY] = grokClientToolCacheEnabled.value
      updatePayload.extra = newExtra
    }

    applyProviderExtraPatch(draft, props.provider, updatePayload, writeQuotaNotifyToExtra)

    await submitUpdateProvider(providerID, updatePayload)
  } catch (error: any) {
    appStore.showError(error.message || t('admin.providers.failedToUpdate'))
  }
}
</script>
