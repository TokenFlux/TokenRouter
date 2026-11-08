<template>
  <BaseDialog
    :show="show"
    :title="t('admin.providers.bulkEdit.title')"
    width="wide"
    :body-scroll="false"
    @close="handleClose"
  >
    <form
      id="bulk-edit-provider-form"
      novalidate
      class="flex min-h-0 flex-1 flex-col"
      @submit.prevent="() => handleSubmit()"
    >
      <div class="shrink-0 space-y-2 pb-2">
        <SettingsNotice>
          {{
            t('admin.providers.bulkEdit.selectionInfo', {
              count: targetMode === 'filtered' ? targetPreviewCount : providerIds.length,
            })
          }}
        </SettingsNotice>
        <SettingsNotice v-if="isMixedPlatform" tone="warning">
          {{
            t('admin.providers.bulkEdit.mixedPlatformWarning', {
              platforms: targetSelectedPlatforms.join(', '),
            })
          }}
        </SettingsNotice>
      </div>

      <SettingsTabs
        id-prefix="bulk-edit-provider"
        :tabs="formTabs"
        :label="t('admin.providers.tabs.label')"
      >
        <template #basic>
          <BulkApplyField id="bulk-edit-status" v-model="enableStatus" :label="t('common.status')">
            <Select
              v-model="status"
              :options="statusOptions"
              aria-labelledby="bulk-edit-status-label"
            />
          </BulkApplyField>

          <BulkApplyField id="bulk-edit-groups" v-model="enableGroups" :label="t('nav.groups')">
            <GroupSelector
              v-model="groupIds"
              :groups="groups"
              aria-labelledby="bulk-edit-groups-label"
            />
          </BulkApplyField>

          <BulkApplyField
            id="bulk-edit-proxy"
            v-model="enableProxy"
            :label="t('admin.providers.proxy')"
          >
            <ProxySelector
              v-model="proxyId"
              :proxies="proxies"
              aria-labelledby="bulk-edit-proxy-label"
            />
          </BulkApplyField>

          <!-- Base URL applies to API-key, upstream, and Grok OAuth targets. -->
          <BulkApplyField
            v-if="allBaseUrlCapable"
            id="bulk-edit-base-url"
            v-model="enableBaseUrl"
            :label="t('admin.providers.baseUrl')"
            :hint="t('admin.providers.bulkEdit.baseUrlNotice')"
          >
            <input
              id="bulk-edit-base-url"
              v-model="baseUrl"
              type="text"
              class="input"
              :placeholder="t('admin.providers.bulkEdit.baseUrlPlaceholder')"
              aria-labelledby="bulk-edit-base-url-label"
            />
            <GrokBaseUrlPresets
              v-if="allTargetsGrok"
              @select="
                baseUrl = $event;
                enableBaseUrl = true;
              "
            />
          </BulkApplyField>
        </template>

        <template #models>
          <BulkApplyField
            id="bulk-edit-model-restriction"
            v-model="enableModelRestriction"
            :label="t('admin.providers.modelRestriction')"
            :hint="t('admin.providers.modelRestrictionCombinedHint')"
          >
            <SettingsSegmented
              v-model="modelRestrictionMode"
              block
              :ariaLabel="t('admin.providers.modelRestriction')"
              :options="modelRestrictionModeOptions"
            />
            <div v-if="modelRestrictionMode === 'whitelist'" v-content-reveal class="space-y-2">
              <SettingsNotice>{{ t('admin.providers.selectAllowedModels') }}</SettingsNotice>
              <ModelWhitelistSelector
                v-model="allowedModels"
                :platforms="targetSelectedPlatforms"
              />
              <p class="input-hint">
                {{ t('admin.providers.selectedModels', { count: allowedModels.length }) }}
                <span v-if="allowedModels.length === 0">{{
                  t('admin.providers.supportsAllModels')
                }}</span>
              </p>
            </div>
            <ProviderModelMappingEditor v-else v-model="modelMappings" />
          </BulkApplyField>
        </template>

        <template #scheduling>
          <SettingsSection :title="t('admin.providers.sections.scheduling')">
            <div class="grid gap-4 md:grid-cols-2">
              <BulkApplyField
                id="bulk-edit-concurrency"
                v-model="enableConcurrency"
                :label="t('admin.providers.concurrency')"
                plain
              >
                <input
                  id="bulk-edit-concurrency"
                  v-model.number="concurrency"
                  type="number"
                  min="1"
                  class="input"
                  aria-labelledby="bulk-edit-concurrency-label"
                  @input="concurrency = Math.max(1, concurrency || 1)"
                />
              </BulkApplyField>
              <BulkApplyField
                id="bulk-edit-load-factor"
                v-model="enableLoadFactor"
                :label="t('admin.providers.loadFactor')"
                plain
              >
                <input
                  id="bulk-edit-load-factor"
                  v-model.number="loadFactor"
                  type="number"
                  min="1"
                  class="input"
                  aria-labelledby="bulk-edit-load-factor-label"
                  @input="loadFactor = loadFactor && loadFactor >= 1 ? loadFactor : null"
                />
                <p class="input-hint">{{ t('admin.providers.loadFactorHint') }}</p>
              </BulkApplyField>
              <BulkApplyField
                id="bulk-edit-priority"
                v-model="enablePriority"
                :label="t('admin.providers.priority')"
                plain
              >
                <input
                  id="bulk-edit-priority"
                  v-model.number="priority"
                  type="number"
                  min="1"
                  class="input"
                  aria-labelledby="bulk-edit-priority-label"
                />
                <p class="input-hint">{{ t('admin.providers.priorityHint') }}</p>
              </BulkApplyField>
              <BulkApplyField
                id="bulk-edit-rate-multiplier"
                v-model="enableRateMultiplier"
                :label="t('admin.providers.billingRateMultiplier')"
                plain
              >
                <input
                  id="bulk-edit-rate-multiplier"
                  v-model.number="rateMultiplier"
                  type="number"
                  min="0"
                  step="0.001"
                  class="input"
                  aria-labelledby="bulk-edit-rate-multiplier-label"
                />
                <p class="input-hint">{{ t('admin.providers.billingRateMultiplierHint') }}</p>
              </BulkApplyField>
            </div>
          </SettingsSection>

          <BulkApplyField
            id="bulk-edit-custom-error-codes"
            v-model="enableCustomErrorCodes"
            :label="t('admin.providers.customErrorCodes')"
            :hint="t('admin.providers.customErrorCodesHint')"
          >
            <CustomErrorCodesFields v-model:codes="selectedErrorCodes" hide-toggle />
          </BulkApplyField>

          <!-- Warmup interception applies to Anthropic and Antigravity targets. -->
          <BulkApplyField
            v-if="allInterceptWarmupCapable"
            id="bulk-edit-intercept-warmup"
            v-model="enableInterceptWarmup"
            :label="t('admin.providers.interceptWarmupRequests')"
            :hint="t('admin.providers.interceptWarmupRequestsDesc')"
          >
            <template #control>
              <Toggle
                id="bulk-edit-intercept-warmup-toggle"
                v-model="interceptWarmupRequests"
                :aria-label="t('admin.providers.interceptWarmupRequests')"
              />
            </template>
          </BulkApplyField>

          <SettingsSection
            v-if="allOpenAIOAuth"
            :title="t('admin.providers.sections.autoPause')"
            :hint="t('admin.providers.autoPauseThresholdHint')"
          >
            <div class="grid gap-4 md:grid-cols-2">
              <BulkApplyField
                id="bulk-edit-openai-auto-pause-5h-disabled"
                v-model="enableAutoPause5hDisabled"
                :label="t('admin.providers.autoPause5hDisabled')"
                :hint="t('admin.providers.autoPauseDisabledHint')"
                plain
              >
                <template #control>
                  <Toggle
                    id="bulk-edit-openai-auto-pause-5h-disabled-toggle"
                    v-model="autoPause5hDisabled"
                    :aria-label="t('admin.providers.autoPause5hDisabled')"
                  />
                </template>
              </BulkApplyField>
              <BulkApplyField
                id="bulk-edit-openai-auto-pause-7d-disabled"
                v-model="enableAutoPause7dDisabled"
                :label="t('admin.providers.autoPause7dDisabled')"
                :hint="t('admin.providers.autoPauseDisabledHint')"
                plain
              >
                <template #control>
                  <Toggle
                    id="bulk-edit-openai-auto-pause-7d-disabled-toggle"
                    v-model="autoPause7dDisabled"
                    :aria-label="t('admin.providers.autoPause7dDisabled')"
                  />
                </template>
              </BulkApplyField>
              <BulkApplyField
                id="bulk-edit-openai-auto-pause-5h-threshold"
                v-model="enableAutoPause5hThreshold"
                :label="t('admin.providers.autoPause5hThreshold')"
                plain
              >
                <input
                  id="bulk-edit-openai-auto-pause-5h-threshold"
                  v-model.number="autoPause5hThreshold"
                  type="number"
                  min="0"
                  max="100"
                  step="0.1"
                  class="input"
                  aria-labelledby="bulk-edit-openai-auto-pause-5h-threshold-label"
                  @input="
                    autoPause5hThreshold = normalizeAutoPauseThresholdInput(autoPause5hThreshold)
                  "
                />
              </BulkApplyField>
              <BulkApplyField
                id="bulk-edit-openai-auto-pause-7d-threshold"
                v-model="enableAutoPause7dThreshold"
                :label="t('admin.providers.autoPause7dThreshold')"
                plain
              >
                <input
                  id="bulk-edit-openai-auto-pause-7d-threshold"
                  v-model.number="autoPause7dThreshold"
                  type="number"
                  min="0"
                  max="100"
                  step="0.1"
                  class="input"
                  aria-labelledby="bulk-edit-openai-auto-pause-7d-threshold-label"
                  @input="
                    autoPause7dThreshold = normalizeAutoPauseThresholdInput(autoPause7dThreshold)
                  "
                />
              </BulkApplyField>
            </div>
          </SettingsSection>
        </template>

        <template #quota>
          <!-- The RPM application flag also governs the message-queue override; unchecked RPM settings leave that override out of the patch. -->
          <BulkApplyField
            v-if="allAnthropicOAuthOrSetupToken"
            id="bulk-edit-rpm-limit"
            v-model="enableRpmLimit"
            :label="t('admin.providers.quotaControl.rpmLimit.label')"
            :hint="t('admin.providers.quotaControl.rpmLimit.hint')"
          >
            <template #control>
              <Toggle
                id="bulk-edit-rpm-limit-toggle"
                v-model="rpmLimitEnabled"
                :aria-label="t('admin.providers.quotaControl.rpmLimit.label')"
              />
            </template>
            <Collapse :open="rpmLimitEnabled" unmount-on-hide>
              <RpmLimitPanel
                v-model:base-rpm="bulkBaseRpm"
                v-model:strategy="bulkRpmStrategy"
                v-model:sticky-buffer="bulkRpmStickyBuffer"
              />
            </Collapse>
            <div class="space-y-2">
              <div>
                <span class="text-sm font-medium text-primary-900 dark:text-dark-50">{{
                  t('admin.providers.quotaControl.rpmLimit.userMsgQueue')
                }}</span>
                <p class="input-hint">
                  {{ t('admin.providers.quotaControl.rpmLimit.userMsgQueueHint') }}
                </p>
              </div>
              <SettingsSegmented
                v-model="userMsgQueueMode"
                deselectable
                :ariaLabel="t('admin.providers.quotaControl.rpmLimit.userMsgQueue')"
                :options="umqModeOptions"
              />
            </div>
          </BulkApplyField>
        </template>

        <template #request>
          <!-- Mount the protocol selector after opting in because its initial catalog selection would otherwise populate a draft the administrator has not chosen to apply. -->
          <BulkApplyField
            v-if="targetSelectedPlatforms.length === 1 && targetSelectedTypes.length === 1"
            id="bulk-native-protocols"
            v-model="enableUpstreamProtocols"
            :label="t('admin.protocols.nativeTitle')"
          >
            <ProviderProtocolSelector
              v-if="enableUpstreamProtocols"
              v-model="upstreamProtocols"
              :platform="targetSelectedPlatforms[0] ?? ''"
              :type="targetSelectedTypes[0] ?? ''"
              auth-mode="*"
              hide-title
            />
          </BulkApplyField>

          <template
            v-if="
              targetSelectedPlatforms.length === 1 &&
              targetSelectedPlatforms[0] === 'openai' &&
              targetSelectedTypes.length === 1 &&
              targetSelectedTypes[0] === 'apikey'
            "
          >
            <BulkApplyField
              v-for="field in IMAGE_MODE_FIELDS"
              :id="`bulk-${field.key}`"
              :key="field.key"
              v-model="applyImageModes[field.key]"
              :label="t(`admin.protocols.imageModes.${field.label}`)"
              :hint="t('admin.protocols.imageModes.hint')"
            >
              <SettingsSegmented
                :model-value="imageModes[field.key]"
                :options="imageModeOptions"
                :ariaLabel="t(`admin.protocols.imageModes.${field.label}`)"
                @update:model-value="imageModes[field.key] = $event ?? 'both'"
              />
            </BulkApplyField>
          </template>

          <BulkApplyField
            v-if="allHeaderOverrideCapable"
            id="bulk-edit-header-override"
            v-model="enableHeaderOverride"
            :label="t('admin.providers.headerOverride.title')"
            :hint="t('admin.providers.headerOverride.hint')"
          >
            <template #control>
              <Toggle
                id="bulk-edit-header-override-toggle"
                v-model="headerOverrideEnabled"
                :aria-label="t('admin.providers.headerOverride.title')"
              />
            </template>
            <template v-if="headerOverrideEnabled">
              <SettingsNotice>
                <p>{{ t('admin.providers.headerOverride.info') }}</p>
              </SettingsNotice>
              <SettingsNotice tone="warning">{{
                t('admin.providers.headerOverride.bulkReplaceHint')
              }}</SettingsNotice>
              <HeaderOverrideEditor
                :rows="headerOverrideRows"
                @update:rows="headerOverrideRows = $event"
              />
            </template>
            <p v-else class="input-hint">
              {{ t('admin.providers.headerOverride.bulkDisableHint') }}
            </p>
          </BulkApplyField>

          <template v-if="allOpenAIPassthroughCapable">
            <BulkApplyField
              id="bulk-edit-openai-passthrough"
              v-model="enableOpenAIPassthrough"
              :label="t('admin.providers.openai.oauthPassthrough')"
              :hint="t('admin.providers.openai.oauthPassthroughDesc')"
            >
              <template #control>
                <Toggle
                  id="bulk-edit-openai-passthrough-toggle"
                  v-model="openaiPassthroughEnabled"
                  :aria-label="t('admin.providers.openai.oauthPassthrough')"
                />
              </template>
            </BulkApplyField>
          </template>

          <BulkApplyField
            v-if="allOpenAIOAuth"
            id="bulk-edit-openai-flatten-namespaces"
            v-model="enableOpenAIFlattenNamespaces"
            :label="t('admin.providers.openai.flattenNamespaces')"
            :hint="t('admin.providers.openai.flattenNamespacesDesc')"
          >
            <template #control>
              <Toggle
                id="bulk-edit-openai-flatten-namespaces-toggle"
                v-model="openaiFlattenNamespacesEnabled"
                :aria-label="t('admin.providers.openai.flattenNamespaces')"
              />
            </template>
          </BulkApplyField>

          <BulkApplyField
            v-if="allOpenAIAPIKey"
            id="bulk-edit-openai-continuation-supported"
            v-model="enableOpenAIResponsesContinuationSupported"
            :label="t('admin.providers.openai.responsesContinuationSupported')"
            :hint="t('admin.providers.openai.responsesContinuationSupportedDesc')"
            apply-testid="bulk-edit-openai-continuation-supported-apply"
          >
            <template #control>
              <Toggle
                v-model="openAIResponsesContinuationSupported"
                data-testid="bulk-edit-openai-continuation-supported"
                :aria-label="t('admin.providers.openai.responsesContinuationSupportedEnabled')"
              />
            </template>
          </BulkApplyField>

          <BulkApplyField
            v-if="allOpenAIOAuth"
            id="bulk-edit-openai-ws-mode"
            v-model="enableResponsesWSConnectionMode"
            :label="t('admin.providers.openai.wsMode')"
            :hint="`${t('admin.providers.openai.wsModeDesc')} ${t(openAIWSModeConcurrencyHintKey)}`"
          >
            <Select
              v-model="openaiOAuthResponsesWebSocketV2Mode"
              data-testid="bulk-edit-openai-ws-mode-select"
              :options="openAIWSModeOptions"
              aria-labelledby="bulk-edit-openai-ws-mode-label"
            />
          </BulkApplyField>

          <BulkApplyField
            v-if="allOpenAIAPIKey"
            id="bulk-edit-openai-apikey-ws-mode"
            v-model="enableOpenAIAPIKeyWSMode"
            :label="t('admin.providers.openai.wsMode')"
            :hint="`${t('admin.providers.openai.wsModeDesc')} ${t(openAIAPIKeyWSModeConcurrencyHintKey)}`"
          >
            <Select
              v-model="openaiAPIKeyResponsesWebSocketV2Mode"
              data-testid="bulk-edit-openai-apikey-ws-mode-select"
              :options="openAIWSModeOptions"
              aria-labelledby="bulk-edit-openai-apikey-ws-mode-label"
            />
          </BulkApplyField>

          <template v-if="allOpenAIOAuth">
            <BulkApplyField
              id="bulk-edit-openai-codex-cli-only"
              v-model="enableCodexCLIOnly"
              :label="t('admin.providers.openai.clientPolicy')"
              :hint="t('admin.providers.openai.clientPolicyDesc')"
            >
              <Select
                v-model="openAIOAuthClientPolicy"
                data-testid="bulk-edit-openai-client-policy-select"
                :options="openAIOAuthClientPolicyOptions"
                aria-labelledby="bulk-edit-openai-codex-cli-only-label"
              />
            </BulkApplyField>

            <!-- Disable the Claude exception when the same patch selects a policy other than Codex-only, matching the submitted allowed-client behavior. -->
            <BulkApplyField
              id="bulk-edit-openai-codex-allow-claude-code"
              v-model="enableCodexCLIOnlyAllowClaudeCode"
              :label="t('admin.providers.openai.codexCLIOnlyAllowClaudeCode')"
              :hint="
                codexAllowClaudeCodeLocked
                  ? `${t('admin.providers.openai.codexCLIOnlyAllowClaudeCodeDesc')} ${t('admin.providers.openai.clientPolicyClaudeCodeHint')}`
                  : t('admin.providers.openai.codexCLIOnlyAllowClaudeCodeDesc')
              "
              :locked="codexAllowClaudeCodeLocked"
            >
              <template #control>
                <Toggle
                  id="bulk-edit-openai-codex-allow-claude-code-toggle"
                  v-model="codexCLIOnlyAllowClaudeCodeEnabled"
                  :aria-label="t('admin.providers.openai.codexCLIOnlyAllowClaudeCode')"
                />
              </template>
            </BulkApplyField>

            <BulkApplyField
              id="bulk-edit-codex-fingerprint-mode"
              v-model="enableCodexFingerprintMode"
              :label="t('admin.providers.openai.codexFingerprintMode')"
              :hint="t('admin.providers.openai.codexFingerprintModeDesc')"
            >
              <Select
                v-model="codexFingerprintMode"
                data-testid="bulk-codex-fingerprint-mode-select"
                :options="codexFingerprintModeOptions"
                aria-labelledby="bulk-edit-codex-fingerprint-mode-label"
              />
            </BulkApplyField>
          </template>

          <BulkApplyField
            v-if="allOpenAIPassthroughCapable"
            id="bulk-edit-images-gif"
            v-model="enableImagesGIF"
            :label="t('admin.providers.openai.imagesGIFEnabled')"
            :hint="t('admin.providers.openai.imagesGIFEnabledDesc')"
          >
            <Toggle
              v-model="imagesGIFEnabled"
              :label="t('admin.providers.openai.imagesGIFEnabled')"
            />
          </BulkApplyField>

          <BulkApplyField
            v-if="allOpenAIOAuth"
            id="bulk-edit-image-responses-model"
            v-model="enableCodexImageResponsesModel"
            :label="t('admin.providers.openai.imageResponsesModel')"
            :hint="t('admin.providers.openai.imageResponsesModelDesc')"
          >
            <input
              v-model="codexImageResponsesModel"
              data-testid="bulk-edit-image-responses-model-input"
              aria-labelledby="bulk-edit-image-responses-model-label"
              type="text"
              class="input"
              :placeholder="t('admin.providers.openai.imageResponsesModelDefault')"
            />
          </BulkApplyField>

          <template v-if="allOpenAIPassthroughCapable">
            <BulkApplyField
              id="bulk-edit-codex-image-tool"
              v-model="enableCodexImageToolMode"
              :label="t('admin.protocols.imagePolicy')"
              :hint="t('admin.providers.openai.codexImageToolDesc')"
            >
              <CodexImageToolModeSelector
                v-model="codexImageToolMode"
                test-id-prefix="bulk-edit-codex-image-tool"
                hide-title
              />
            </BulkApplyField>

            <BulkApplyField
              id="bulk-edit-openai-native-compaction-v2-mode"
              v-model="enableOpenAINativeCompactionV2Mode"
              :label="t('admin.providers.openai.nativeCompactV2Mode')"
              :hint="t('admin.providers.openai.nativeCompactV2ModeDesc')"
            >
              <template #control>
                <Toggle
                  :model-value="openAINativeCompactionV2Mode === 'force_on'"
                  data-testid="bulk-edit-openai-native-compaction-v2-mode-select"
                  :aria-label="t('admin.providers.openai.nativeCompactV2Mode')"
                  @update:model-value="
                    openAINativeCompactionV2Mode = $event ? 'force_on' : 'force_off'
                  "
                />
              </template>
            </BulkApplyField>

            <BulkApplyField
              id="bulk-edit-openai-compact-mode"
              v-model="enableOpenAICompactMode"
              :label="t('admin.providers.openai.compactMode')"
              :hint="t('admin.providers.openai.compactModeDesc')"
            >
              <template #control>
                <Toggle
                  :model-value="openAICompactMode === 'force_on'"
                  data-testid="bulk-edit-openai-compact-mode-select"
                  :aria-label="t('admin.providers.openai.compactMode')"
                  @update:model-value="openAICompactMode = $event ? 'force_on' : 'force_off'"
                />
              </template>
            </BulkApplyField>

            <BulkApplyField
              v-if="enableOpenAICompactMode && openAICompactMode !== 'force_off'"
              id="bulk-edit-openai-compact-model-mapping"
              v-model="enableOpenAICompactModelMapping"
              :label="t('admin.providers.openai.compactModelMapping')"
              :hint="t('admin.providers.openai.compactModelMappingDesc')"
            >
              <ProviderModelMappingEditor
                v-model="openAICompactModelMappings"
                :hint="''"
                :source-placeholder="t('admin.providers.fromModel')"
                :target-placeholder="t('admin.providers.toModel')"
                test-id="bulk-edit-openai-compact-model-mapping"
              />
            </BulkApplyField>
          </template>

          <BulkApplyField
            v-if="allTLSFingerprintCapable"
            id="bulk-edit-tls-fingerprint"
            v-model="enableTLSFingerprint"
            :label="t('admin.providers.quotaControl.tlsFingerprint.label')"
            :hint="t('admin.providers.quotaControl.tlsFingerprint.hint')"
          >
            <template #control>
              <Toggle
                id="bulk-edit-tls-fingerprint-toggle"
                v-model="tlsFingerprintEnabled"
                :aria-label="t('admin.providers.quotaControl.tlsFingerprint.label')"
              />
            </template>
            <Collapse :open="tlsFingerprintEnabled" unmount-on-hide>
              <SettingsSubpanel>
                <div>
                  <label for="bulk-edit-tls-fingerprint-profile" class="input-label">{{
                    t('admin.providers.quotaControl.tlsFingerprint.profile')
                  }}</label>
                  <Select
                    id="bulk-edit-tls-fingerprint-profile"
                    v-model="tlsFingerprintProfileId"
                    data-testid="bulk-edit-tls-fingerprint-profile"
                    :options="tlsFingerprintProfileOptions"
                  />
                </div>
                <div v-if="allOpenAIOAuth">
                  <label for="bulk-edit-tls-fingerprint-router" class="input-label">{{
                    t('admin.providers.quotaControl.tlsFingerprint.router')
                  }}</label>
                  <Select
                    id="bulk-edit-tls-fingerprint-router"
                    v-model="tlsFingerprintRouterId"
                    data-testid="bulk-edit-tls-fingerprint-router"
                    :options="tlsFingerprintRouterOptions"
                  />
                  <p class="input-hint">
                    {{ t('admin.providers.quotaControl.tlsFingerprint.routerHint') }}
                  </p>
                </div>
              </SettingsSubpanel>
            </Collapse>
          </BulkApplyField>
        </template>
      </SettingsTabs>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="bulk-edit-provider-form"
          :disabled="submitting"
          class="btn btn-primary"
        >
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="submitting"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
          />
          {{
            submitting
              ? t('admin.providers.bulkEdit.updating')
              : t('admin.providers.bulkEdit.submit')
          }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import Collapse from '@/components/common/Collapse.vue'

import ProviderProtocolSelector from '../../../../components/provider/ProviderProtocolSelector.vue'
import {
  IMAGE_MODE_FIELDS,
  imageStreamModeOptions,
  readImageStreamModes,
} from '@/components/provider/form/imageStreamModes'
import type { ProtocolID } from '@/api/protocols.types'
import { reactive, ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { Proxy as ProxyConfig } from '@/api/admin/proxies.types'
import type { AdminGroup } from '@/api/groups.types'
import type {
  ProviderPlatform,
  ProviderType,
  Provider,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
} from '@/api/admin/providers.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import ProxySelector from '@/components/admin/proxy/ProxySelector.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import CodexImageToolModeSelector from '@/components/provider/CodexImageToolModeSelector.vue'
import ModelWhitelistSelector from '@/components/provider/ModelWhitelistSelector.vue'
import Icon from '@/components/icons/Icon.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import SettingsTabs from '@/components/common/settings/SettingsTabs.vue'
import BulkApplyField from '@/components/provider/form/BulkApplyField.vue'
import CustomErrorCodesFields from '@/components/provider/form/CustomErrorCodesFields.vue'
import RpmLimitPanel from '@/components/provider/form/RpmLimitPanel.vue'
import {
  useCodexFingerprintModeOptions,
  useOpenAIOAuthClientPolicyOptions,
  useResponsesWSConnectionModeOptions,
  useUserMsgQueueModeOptions,
  type CodexFingerprintMode,
  type RpmStrategy,
} from '@/components/provider/form/providerFormOptions'
import ProviderModelMappingEditor from '@/components/provider/ProviderModelMappingEditor.vue'
import type { ModelMappingRow } from '@/utils/modelMappingRules'
import {
  buildModelMappingObject,
  buildPersistedModelRestriction,
  normalizeModelWhitelist,
  splitQoderPersistedModelRestriction,
  splitModelMappingObject,
  splitPersistedModelRestriction,
} from '@/composables/useModelWhitelist'
import HeaderOverrideEditor from '@/components/provider/HeaderOverrideEditor.vue'
import {
  buildHeaderOverridesObject,
  isHeaderOverrideCapable,
  validateHeaderOverrideRows,
  HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY,
  HEADER_OVERRIDES_CREDENTIAL_KEY,
  type HeaderOverrideRow,
} from '@/components/provider/form/headerOverrides'
import GrokBaseUrlPresets from '@/components/provider/GrokBaseUrlPresets.vue'
import { RESPONSES_WS_POOLED, responsesWSConnectionHint } from '@/utils/responsesWsConnection'
import type { ResponsesWSConnectionMode } from '@/utils/responsesWsConnection'
import {
  applyCodexImageToolMode,
  applyCodexImageResponsesModel,
  type CodexImageToolMode,
} from '@/utils/codexImageToolMode'

/**
 * BulkEditProviderModal.vue constructs opt-in patches for explicitly selected providers or a
 * server-filtered target set. Field application flags are separate from draft values, so an
 * unchecked field is omitted even when its editor has a default. Platform and authentication
 * summaries control which request, quota, and transport settings are offered and submitted.
 *
 * Model-restriction prefill reads selected providers, normalizes their saved representations,
 * and publishes a shared draft only when all results agree and the administrator has not edited
 * it. A request sequence rejects obsolete prefill responses. Submission preserves
 * endpoint-specific clear values such as zero, null, empty objects, and empty arrays because
 * merged credential or extra records require explicit replacement values. Partial writes notify
 * and close whenever at least one provider succeeds.
 */
/**
 * Props describes explicit or filtered bulk targets and available proxy and group choices.
 * providerIds, selectedPlatforms, and selectedTypes describe the explicit selection; target can
 * replace the displayed capability summaries and supply server filters for submission.
 */
interface Props {
  show: boolean // parent-controlled bulk editor visibility
  providerIds: number[] // explicit provider target identifiers
  selectedPlatforms: ProviderPlatform[] // summarized target provider platforms
  selectedTypes: ProviderType[] // summarized target authentication types
  /**
   * Optional filtered-target metadata overrides displayed capability summaries and count;
   * submission uses its filters when filtered mode provides them.
   */
  target?: {
    mode: 'selected' | 'filtered' // selected or filtered target mode
    filters?: Record<string, unknown> // server-side provider selection criteria
    previewCount?: number // previewed matching provider count
    selectedPlatforms?: ProviderPlatform[] // summarized target provider platforms
    selectedTypes?: ProviderType[] // summarized target authentication types
  }
  proxies: ProxyConfig[] // available administrator proxy choices
  groups: AdminGroup[] // available administrator group choices
}

/**
 * props supplies explicit identifiers or filtered selection metadata, plus proxy and group
 * choices used by opt-in bulk update controls.
 */
const props = defineProps<Props>()
/**
 * emit reports persisted changes before closure whenever a bulk response succeeds for at least
 * one target, including partial failures.
 */
const emit = defineEmits<{
  close: [] // request parent dialog closure
  /**
   * Notify the parent when at least one provider update succeeded, including a partially
   * successful response.
   */
  updated: []
}>()

/**
 * Translation and application toasts label bulk applicability, field validation, full success,
 * partial success, and failed update responses.
 */
const { t } = useI18n()
const appStore = useAppStore()

/**
 * Target summaries select explicit versus filtered submission and provide the displayed count,
 * platform set, and authentication set for capability checks.
 */
const targetMode = computed(() => props.target?.mode ?? 'selected')
const targetPreviewCount = computed(() => props.target?.previewCount ?? props.providerIds.length)
const targetSelectedPlatforms = computed(
  () => props.target?.selectedPlatforms ?? props.selectedPlatforms,
)
const targetSelectedTypes = computed(() => props.target?.selectedTypes ?? props.selectedTypes)

/**
 * allTargetsGrok enables Grok endpoint presets only when the target platform summary is
 * nonempty and exclusively Grok.
 */
const allTargetsGrok = computed(
  () =>
    targetSelectedPlatforms.value.length > 0 &&
    targetSelectedPlatforms.value.every((p) => p === 'grok'),
)
/**
 * Platform and authentication predicates gate OpenAI passthrough, OAuth-only settings, and
 * API-key-only continuation behavior for the summarized target set.
 */
const isMixedPlatform = computed(() => targetSelectedPlatforms.value.length > 1)

const allOpenAIPassthroughCapable = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every((t) => t === 'oauth' || t === 'apikey')
  )
})

const allOpenAIOAuth = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every((t) => t === 'oauth')
  )
})

const allOpenAIAPIKey = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedTypes.value.every((t) => t === 'apikey')
  )
})

/**
 * Header override eligibility checks every platform/type combination in the target summaries,
 * conservatively requiring support even for combinations absent from actual selected records.
 */
const allHeaderOverrideCapable = computed(() => {
  return (
    targetSelectedPlatforms.value.length > 0 &&
    targetSelectedTypes.value.length > 0 &&
    targetSelectedPlatforms.value.every((p) =>
      targetSelectedTypes.value.every((ty) => isHeaderOverrideCapable(p, ty)),
    )
  )
})

/**
 * allAnthropicOAuthOrSetupToken gates RPM and quota controls for an Anthropic-only platform
 * summary whose authentication types are OAuth or setup-token.
 */
const allAnthropicOAuthOrSetupToken = computed(() => {
  return (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'anthropic' &&
    targetSelectedTypes.value.every((t) => t === 'oauth' || t === 'setup-token')
  )
})

/**
 * isTLSFingerprintCapableTarget identifies supported platform/authentication pairs for
 * fingerprint configuration in the bulk editor.
 */
const isTLSFingerprintCapableTarget = (platform: ProviderPlatform, type: ProviderType) => {
  // The bulk fingerprint control supports the listed authentication pairs; API-key OpenAI
  // records remain outside this transport configuration selector.
  return (
    (platform === 'anthropic' && (type === 'oauth' || type === 'setup-token')) ||
    (platform === 'openai' && type === 'oauth') ||
    (platform === 'qoder' && type === 'cosy')
  )
}

/**
 * TLS capability accepts supported single-platform combinations and the recognized mixed
 * Anthropic/OpenAI OAuth selection, excluding unsupported platform or type summaries.
 */
const allTLSFingerprintCapable = computed(() => {
  const platforms = targetSelectedPlatforms.value
  const types = targetSelectedTypes.value
  if (platforms.length === 0 || types.length === 0) return false
  if (
    !platforms.every(
      (platform) => platform === 'anthropic' || platform === 'openai' || platform === 'qoder',
    )
  )
    return false
  if (!types.every((type) => type === 'oauth' || type === 'setup-token' || type === 'cosy'))
    return false
  if (platforms.length === 1) {
    return types.every((type) => isTLSFingerprintCapableTarget(platforms[0], type))
  }
  return (
    platforms.every((platform) => platform === 'anthropic' || platform === 'openai') &&
    platforms.includes('openai') &&
    platforms.includes('anthropic') &&
    types.every((type) => type === 'oauth' || type === 'setup-token')
  )
})

/**
 * Base URL applicability includes API keys, upstream accounts, and all-Grok OAuth targets;
 * warmup interception applies only to Anthropic and Antigravity target platforms.
 */
const allBaseUrlCapable = computed(() => {
  const types = targetSelectedTypes.value
  return (
    types.length > 0 &&
    types.every(
      (type) =>
        type === 'apikey' || type === 'upstream' || (type === 'oauth' && allTargetsGrok.value),
    )
  )
})

const allInterceptWarmupCapable = computed(() => {
  const platforms = targetSelectedPlatforms.value
  return (
    platforms.length > 0 &&
    platforms.every((platform) => platform === 'anthropic' || platform === 'antigravity')
  )
})

/**
 * Model restriction controls switch between whitelist and mapping views while both underlying
 * draft collections remain available to shared persistence helpers.
 */
const modelRestrictionModeOptions = computed(() => [
  {
    value: 'whitelist' as const,
    label: t('admin.providers.modelWhitelist'),
    icon: 'checkCircle' as const,
  },
  { value: 'mapping' as const, label: t('admin.providers.modelMapping'), icon: 'swap' as const },
])

/**
 * codexAllowClaudeCodeLocked disables the Claude exception when this patch also changes the
 * client policy away from Codex-only acceptance.
 */
const codexAllowClaudeCodeLocked = computed(
  () => enableCodexCLIOnly.value && openAIOAuthClientPolicy.value !== 'codex_only',
)

/**
 * formTabs hides quota and request sections when target summaries expose no applicable
 * controls, while basic, model, and scheduling sections remain available.
 */
const formTabs = computed(() => {
  const hasRequest =
    (targetSelectedPlatforms.value.length === 1 && targetSelectedTypes.value.length === 1) ||
    allHeaderOverrideCapable.value ||
    allOpenAIPassthroughCapable.value ||
    allTLSFingerprintCapable.value
  return [
    { key: 'basic', label: t('admin.providers.tabs.basic') },
    { key: 'models', label: t('admin.providers.tabs.models') },
    { key: 'scheduling', label: t('admin.providers.tabs.scheduling') },
    {
      key: 'quota',
      label: t('admin.providers.tabs.quota'),
      hidden: !allAnthropicOAuthOrSetupToken.value,
    },
    { key: 'request', label: t('admin.providers.tabs.request'), hidden: !hasRequest },
  ]
})

/**
 * OptionalNumberInputValue represents a numeric editor value or its cleared input states.
 */
type OptionalNumberInputValue = number | null | ''

/**
 * ParsedModelRestrictionState holds normalized model rules for comparison and shared draft
 * prefill.
 */
interface ParsedModelRestrictionState {
  mode: 'whitelist' | 'mapping' // whitelist or mapping editor mode
  allowedModels: string[] // normalized allowed model identifiers
  modelMappings: ModelMappingRow[] // normalized source-target mapping rows
}

/**
 * Application flags decide which draft values participate in the patch; leaving a flag false
 * preserves that field on every targeted provider.
 */
const enableBaseUrl = ref(false)
const enableModelRestriction = ref(false)
const enableCustomErrorCodes = ref(false)
const enableInterceptWarmup = ref(false)
const enableHeaderOverride = ref(false)
const enableProxy = ref(false)
const enableConcurrency = ref(false)
const enableLoadFactor = ref(false)
const enablePriority = ref(false)
const enableRateMultiplier = ref(false)
const enableStatus = ref(false)
const enableGroups = ref(false)
const enableOpenAIPassthrough = ref(false)
const enableOpenAIFlattenNamespaces = ref(false)
const enableCodexImageToolMode = ref(false)
// These drafts apply an image driver only when selected OAuth targets opt into the patch.
const enableCodexImageResponsesModel = ref(false)
/** GIF bulk changes remain opt-in while their proposed value defaults on. */
const enableImagesGIF = ref(false)
const imagesGIFEnabled = ref(true)
const codexImageResponsesModel = ref('')
const enableUpstreamProtocols = ref(false)
const upstreamProtocols = ref<ProtocolID[] | undefined>(undefined)
// Image modes and their application flags are independent per endpoint. Selection
// changes clear application flags so hidden fields cannot affect another provider type.
const imageModes = ref(readImageStreamModes())
const applyImageModes = reactive({ images_generations_mode: false, images_edits_mode: false })
const imageModeOptions = computed(() => imageStreamModeOptions(t))
watch(
  () => [targetSelectedPlatforms.value.join(','), targetSelectedTypes.value.join(',')],
  () => {
    enableUpstreamProtocols.value = false
    upstreamProtocols.value = undefined
    imageModes.value = readImageStreamModes()
    applyImageModes.images_generations_mode = false
    applyImageModes.images_edits_mode = false
  },
)
/**
 * OpenAI request, auto-pause, compact, RPM, and TLS application flags remain independent so
 * bulk updates can select each field separately.
 */
const enableOpenAIResponsesContinuationSupported = ref(false)
const enableResponsesWSConnectionMode = ref(false)
const enableOpenAIAPIKeyWSMode = ref(false)
const enableCodexCLIOnly = ref(false)
const enableCodexCLIOnlyAllowClaudeCode = ref(false)
const enableAutoPause5hThreshold = ref(false)
const enableAutoPause7dThreshold = ref(false)
const enableAutoPause5hDisabled = ref(false)
const enableAutoPause7dDisabled = ref(false)
const enableOpenAICompactMode = ref(false)
const enableOpenAINativeCompactionV2Mode = ref(false)
const enableOpenAICompactModelMapping = ref(false)
const enableRpmLimit = ref(false)
const enableTLSFingerprint = ref(false)

/**
 * Draft values retain editor defaults independently from application flags; their values become
 * updates only when the matching opt-in control is enabled.
 */
const submitting = ref(false)
const baseUrl = ref('')
const modelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const allowedModels = ref<string[]>([])
const modelMappings = ref<ModelMappingRow[]>([])
const selectedErrorCodes = ref<number[]>([])
const interceptWarmupRequests = ref(false)
const headerOverrideEnabled = ref(false)
const headerOverrideRows = ref<HeaderOverrideRow[]>([])
const proxyId = ref<number | null>(null)
const concurrency = ref(1)
const loadFactor = ref<number | null>(null)
const priority = ref(1)
const rateMultiplier = ref(1)
const status = ref<'active' | 'inactive'>('active')
const groupIds = ref<number[]>([])
const openaiPassthroughEnabled = ref(false)

/**
 * Namespace flattening defaults off in the draft and reaches the extra patch only when
 * explicitly applied to eligible OpenAI OAuth targets.
 */
const openaiFlattenNamespacesEnabled = ref(false)
const codexImageToolMode = ref<CodexImageToolMode>('inherit')

/**
 * Continuation support, WebSocket modes, client policy, and auto-pause drafts remain distinct
 * values, each governed by its corresponding bulk application flag.
 */
const openAIResponsesContinuationSupported = ref(false)
const openaiOAuthResponsesWebSocketV2Mode = ref<ResponsesWSConnectionMode>(RESPONSES_WS_POOLED)
const openaiAPIKeyResponsesWebSocketV2Mode = ref<ResponsesWSConnectionMode>(RESPONSES_WS_POOLED)
const openAIOAuthClientPolicy = ref<OpenAIOAuthClientPolicy>('any')
const codexCLIOnlyAllowClaudeCodeEnabled = ref(false)
const autoPause5hThreshold = ref<OptionalNumberInputValue>(null)
const autoPause7dThreshold = ref<OptionalNumberInputValue>(null)
const autoPause5hDisabled = ref(false)
const autoPause7dDisabled = ref(false)
/**
 * Fingerprint mode and compact settings retain independent application flags and drafts,
 * including explicit off values that replace saved merged configuration.
 */
const enableCodexFingerprintMode = ref(false)
const codexFingerprintMode = ref<CodexFingerprintMode>('off')
const codexFingerprintModeOptions = useCodexFingerprintModeOptions()
const openAICompactMode = ref<OpenAICompactMode>('force_on')
const openAINativeCompactionV2Mode = ref<OpenAICompactMode>('force_on')
const openAICompactModelMappings = ref<ModelMappingRow[]>([])
/**
 * RPM draft values separate enablement, base request rate, strategy, sticky buffer, and
 * optional message-queue override used when the RPM section is applied.
 */
const rpmLimitEnabled = ref(false)
const bulkBaseRpm = ref<number | null>(null)
const bulkRpmStrategy = ref<RpmStrategy>('tiered')
const bulkRpmStickyBuffer = ref<number | null>(null)
/**
 * userMsgQueueMode uses null to leave the override unchanged and an empty string to clear the
 * provider-level override when RPM changes are applied.
 */
const userMsgQueueMode = ref<string | null>(null)
/**
 * TLS drafts select enablement, a built-in or saved profile, and an optional router; loaded
 * choice lists supply display names for those identifiers.
 */
const tlsFingerprintEnabled = ref(false)
const tlsFingerprintProfileId = ref(0)
const tlsFingerprintProfiles = ref<
  {
    id: number // saved profile or router identifier
    name: string // saved profile or router label
  }[]
>([])
const tlsFingerprintRouterId = ref<number | null>(null)
const tlsFingerprintRouters = ref<
  {
    id: number // saved profile or router identifier
    name: string // saved profile or router label
  }[]
>([])
/**
 * TLS selectors reserve zero for the built-in profile, negative one for random selection when
 * profiles exist, and null for no router.
 */
const tlsFingerprintProfileOptions = computed(() => [
  { value: 0, label: t('admin.providers.quotaControl.tlsFingerprint.defaultProfile') },
  ...(tlsFingerprintProfiles.value.length > 0
    ? [{ value: -1, label: t('admin.providers.quotaControl.tlsFingerprint.randomProfile') }]
    : []),
  ...tlsFingerprintProfiles.value.map((profile) => ({ value: profile.id, label: profile.name })),
])
const tlsFingerprintRouterOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.noRouter') },
  ...tlsFingerprintRouters.value.map((router) => ({ value: router.id, label: router.name })),
])
/**
 * modelRestrictionPrefillSeq invalidates earlier selected-provider loads; queue-mode and status
 * options provide the remaining shared editor choice lists.
 */
const modelRestrictionPrefillSeq = ref(0)
const umqModeOptions = useUserMsgQueueModeOptions()

const statusOptions = computed(() => [
  { value: 'active', label: t('common.active') },
  { value: 'inactive', label: t('common.inactive') },
])

/**
 * Shared WebSocket and client-policy choices determine the editor options, while concurrency
 * hints follow the selected OAuth and API-key connection modes.
 */
const openAIWSModeOptions = useResponsesWSConnectionModeOptions()
const openAIOAuthClientPolicyOptions = useOpenAIOAuthClientPolicyOptions()
const openAIWSModeConcurrencyHintKey = computed(() =>
  responsesWSConnectionHint(openaiOAuthResponsesWebSocketV2Mode.value),
)
const openAIAPIKeyWSModeConcurrencyHintKey = computed(() =>
  responsesWSConnectionHint(openaiAPIKeyResponsesWebSocketV2Mode.value),
)

/**
 * cloneModelMappings copies source and target strings into fresh mapping rows. Callers can edit
 * the returned array and its rows without mutating the row objects supplied by the provider
 * defaults.
 */
const cloneModelMappings = (mappings: ModelMappingRow[]) =>
  mappings.map(({ from, to }) => ({ from, to }))

/**
 * normalizeModelMappings copies, trims, filters, and sorts mapping rows for stable comparison.
 * Rows missing either side are discarded, and source then target ordering makes equivalent
 * model restrictions comparable across providers.
 */
const normalizeModelMappings = (mappings: ModelMappingRow[]) => {
  return cloneModelMappings(mappings)
    .map(({ from, to }) => ({
      from: from.trim(),
      to: to.trim(),
    }))
    .filter(({ from, to }) => from.length > 0 && to.length > 0)
    .sort((a, b) => {
      const fromCmp = a.from.localeCompare(b.from)
      if (fromCmp !== 0) {
        return fromCmp
      }
      return a.to.localeCompare(b.to)
    })
}

/**
 * resetModelRestrictionDraft clears both model lists and restores whitelist editing mode.
 */
const resetModelRestrictionDraft = () => {
  modelRestrictionMode.value = 'whitelist'
  allowedModels.value = []
  modelMappings.value = []
}

/**
 * isModelRestrictionDraftPristine reports whether model mode and lists retain their empty
 * initial state.
 */
const isModelRestrictionDraftPristine = () =>
  modelRestrictionMode.value === 'whitelist' &&
  allowedModels.value.length === 0 &&
  modelMappings.value.length === 0

/**
 * getModelRestrictionSignature serializes normalized restriction state for equality comparison
 * across provider defaults.
 */
const getModelRestrictionSignature = (state: ParsedModelRestrictionState) => {
  return JSON.stringify({
    mode: state.mode,
    allowedModels: state.allowedModels,
    modelMappings: state.modelMappings,
  })
}

/**
 * parseProviderModelRestriction reads saved provider restrictions and returns a normalized
 * state suitable for cross-provider comparison. Antigravity reads its mapping representation
 * before a legacy whitelist, Qoder uses its specialized compatibility reader, and other
 * providers use the general persisted restriction reader.
 *
 * Allowed models are trimmed, deduplicated, and sorted, while mapping rows are copied and
 * normalized separately. A nonempty normalized mapping chooses mapping mode; otherwise the
 * draft uses whitelist mode. The normalized representation lets equivalent saved rules compare
 * independently from their input order.
 */
const parseProviderModelRestriction = (provider: Provider): ParsedModelRestrictionState => {
  const credentials = (provider.credentials as Record<string, unknown>) || {}

  let allowedModels: string[]
  let modelMappings: ModelMappingRow[] = []

  if (provider.platform === 'antigravity') {
    const rawMapping = credentials.model_mapping as Record<string, string> | undefined
    if (rawMapping && typeof rawMapping === 'object') {
      const parsed = splitModelMappingObject(rawMapping)
      allowedModels = parsed.allowedModels
      modelMappings = parsed.modelMappings
    } else {
      allowedModels = normalizeModelWhitelist(credentials.model_whitelist)
    }
  } else if (provider.platform === 'qoder') {
    const parsed = splitQoderPersistedModelRestriction(
      credentials.model_mapping as Record<string, string> | undefined,
      credentials.model_whitelist,
    )
    allowedModels = parsed.allowedModels
    modelMappings = parsed.modelMappings
  } else {
    const parsed = splitPersistedModelRestriction(
      credentials.model_mapping as Record<string, string> | undefined,
      credentials.model_whitelist,
    )
    allowedModels = parsed.allowedModels
    modelMappings = parsed.modelMappings
  }

  const normalizedAllowedModels = Array.from(
    new Set(allowedModels.map((model) => model.trim()).filter((model) => model.length > 0)),
  ).sort((a, b) => a.localeCompare(b))
  const normalizedModelMappings = normalizeModelMappings(modelMappings)

  return {
    mode: normalizedModelMappings.length > 0 ? 'mapping' : 'whitelist',
    allowedModels: normalizedAllowedModels,
    modelMappings: normalizedModelMappings,
  }
}

/**
 * hydrateModelRestrictionDraftFromProviders fills the model draft only when the selected
 * platform summary is singular and every provider has the same normalized restriction. Empty
 * input or mixed platform selection restores the empty whitelist draft. Different normalized
 * signatures also reset the draft instead of choosing one provider as representative.
 *
 * Matching results copy their whitelist array and mapping rows into editable state, keeping
 * future draft edits separate from parsed provider defaults. The caller checks request
 * generation and whether administrator input remains pristine before invoking this operation.
 */
const hydrateModelRestrictionDraftFromProviders = (providers: Provider[]) => {
  if (props.selectedPlatforms.length !== 1 || providers.length === 0) {
    // An absent or inconsistent shared restriction has no representative provider value, so use
    // an empty draft until the administrator chooses one.
    resetModelRestrictionDraft()
    return
  }

  const parsedStates = providers.map(parseProviderModelRestriction)
  const firstState = parsedStates[0]
  const firstSignature = getModelRestrictionSignature(firstState)

  if (!parsedStates.every((state) => getModelRestrictionSignature(state) === firstSignature)) {
    // An absent or inconsistent shared restriction has no representative provider value, so use
    // an empty draft until the administrator chooses one.
    resetModelRestrictionDraft()
    return
  }

  modelRestrictionMode.value = firstState.mode
  allowedModels.value = [...firstState.allowedModels]
  modelMappings.value = cloneModelMappings(firstState.modelMappings)
}

/**
 * loadTLSFingerprintProfiles fetches compact saved-profile choices for eligible bulk targets.
 * Failure clears the choices, leaving built-in defaults available and preventing a stale
 * profile list from surviving the failed request.
 */
const loadTLSFingerprintProfiles = async () => {
  try {
    const profiles = await adminAPI.tlsFingerprintProfiles.list()
    tlsFingerprintProfiles.value = profiles.map((p) => ({ id: p.id, name: p.name }))
  } catch {
    // A failed lookup clears saved profile choices so the editor cannot offer stale records as
    // currently available transport settings.
    tlsFingerprintProfiles.value = []
  }
}

/**
 * loadTLSFingerprintRouters fetches compact router choices for eligible OpenAI OAuth targets.
 * Failure clears the choices so the selector retains only its no-router option until a later
 * successful load.
 */
const loadTLSFingerprintRouters = async () => {
  try {
    const routers = await adminAPI.tlsFingerprintRouters.list()
    tlsFingerprintRouters.value = routers.map((router) => ({ id: router.id, name: router.name }))
  } catch {
    // A failed lookup retains only the no-router choice, preventing stale router records from
    // appearing available after the request fails.
    tlsFingerprintRouters.value = []
  }
}

/**
 * loadSelectedProviderDefaults reads selected provider records to prefill a common model
 * restriction. Each call increments a sequence before checking visibility and selection,
 * invalidating earlier requests even when the new selection requires no load. Mixed selected
 * platforms immediately clear the model draft.
 *
 * After all provider reads resolve, publication requires the latest sequence, an open dialog,
 * and a pristine model draft. This prevents obsolete selection results from replacing a newer
 * load or administrator edits. Failed current loads clear only a still-pristine draft and log
 * the error, leaving user-entered restrictions intact.
 */
const loadSelectedProviderDefaults = async () => {
  const requestSeq = ++modelRestrictionPrefillSeq.value
  if (!props.show || props.providerIds.length === 0) {
    return
  }
  if (props.selectedPlatforms.length !== 1) {
    // An absent or inconsistent shared restriction has no representative provider value, so use
    // an empty draft until the administrator chooses one.
    resetModelRestrictionDraft()
    return
  }

  try {
    const providers = await Promise.all(
      props.providerIds.map((id) => adminAPI.providers.getById(id)),
    )
    // A newer selection or hidden dialog owns the draft now; discard this completed read before
    // checking whether administrator input remains pristine.
    if (requestSeq !== modelRestrictionPrefillSeq.value || !props.show) {
      return
    }
    if (!isModelRestrictionDraftPristine()) {
      return
    }
    hydrateModelRestrictionDraftFromProviders(providers)
  } catch (error) {
    if (requestSeq !== modelRestrictionPrefillSeq.value) {
      return
    }
    if (!isModelRestrictionDraftPristine()) {
      return
    }
    resetModelRestrictionDraft()
    console.error('Failed to load bulk edit provider defaults:', error)
  }
}

/**
 * buildOpenAICompactModelMapping serializes compact-model mapping rows through the shared
 * mapping builder and returns null when no valid rows remain. The submit caller converts null
 * to an empty object to clear the mapping in merged provider credentials.
 */
const buildOpenAICompactModelMapping = (): Record<string, string> | null => {
  return buildModelMappingObject('mapping', [], openAICompactModelMappings.value)
}

/**
 * normalizeAutoPauseThresholdInput maps empty, nonfinite, or nonpositive input to null and caps
 * positive percentages at one hundred. This draft representation keeps cleared overrides
 * distinct from an enabled positive threshold.
 */
const normalizeAutoPauseThresholdInput = (
  value: OptionalNumberInputValue,
): OptionalNumberInputValue => {
  if (value === '' || value === null) {
    return null
  }

  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return null
  }
  // Cap the editable threshold at complete quota utilization.
  return Math.min(parsed, 100)
}

/**
 * buildAutoPauseThresholdRatio converts a valid percentage draft into the backend zero-to-one
 * ratio. Cleared or invalid drafts become zero, which removes the provider override and uses
 * the global default.
 */
const buildAutoPauseThresholdRatio = (value: OptionalNumberInputValue): number => {
  // Convert the percent editor to the backend ratio; zero clears the provider override so the
  // global auto-pause threshold applies.
  const normalized = normalizeAutoPauseThresholdInput(value)
  return typeof normalized === 'number' ? normalized / 100 : 0
}

/**
 * buildUpdatePayload builds a provider patch from enabled application flags and current
 * eligible draft values. It returns null when no field contributes an update. Credential and
 * extra objects are attached only when their corresponding settings are changed, leaving
 * unchecked fields absent from the patch.
 *
 * Clear operations use the endpoint representation: proxy zero, empty mapping objects, explicit
 * false flags, empty allowlists, and zero or empty-string extra values replace previously
 * merged fields. Platform predicates are reapplied where implemented for request-specific
 * settings. Callers validate base URLs and header rows before invoking submission, since this
 * builder itself only assembles the selected patch.
 */
const buildUpdatePayload = (): Record<string, unknown> | null => {
  const updates: Record<string, unknown> = {}
  const credentials: Record<string, unknown> = {}
  let credentialsChanged = false
  /**
   * ensureExtra creates the extra patch object on first use and returns it.
   */
  const ensureExtra = (): Record<string, unknown> => {
    if (!updates.extra) {
      updates.extra = {}
    }
    return updates.extra as Record<string, unknown>
  }

  if (enableProxy.value) {
    // The bulk endpoint uses proxy_id zero as the clear instruction, so an empty proxy selector
    // must still contribute that explicit value.
    updates.proxy_id = proxyId.value === null ? 0 : proxyId.value
  }

  if (enableConcurrency.value) {
    updates.concurrency = concurrency.value
  }

  if (enableLoadFactor.value) {
    // Cleared or unusable load-factor input becomes zero, which the bulk endpoint interprets as
    // removing the provider-specific load-factor override.
    const lf = loadFactor.value
    updates.load_factor = lf != null && !Number.isNaN(lf) && lf > 0 ? lf : 0
  }

  if (enablePriority.value) {
    updates.priority = priority.value
  }

  if (enableRateMultiplier.value) {
    updates.rate_multiplier = rateMultiplier.value
  }

  if (enableStatus.value) {
    updates.status = status.value
  }

  if (enableGroups.value) {
    updates.group_ids = groupIds.value
  }

  if (enableBaseUrl.value && allBaseUrlCapable.value) {
    const baseUrlValue = baseUrl.value.trim()
    if (baseUrlValue) {
      credentials.base_url = baseUrlValue
      credentialsChanged = true
    }
  }

  if (enableOpenAIPassthrough.value) {
    const extra = ensureExtra()
    extra.openai_passthrough = openaiPassthroughEnabled.value
    if (!openaiPassthroughEnabled.value) {
      extra.openai_oauth_passthrough = false
    }
  }

  // Recheck OAuth eligibility during payload construction because changing target filters can
  // make a previously enabled platform-specific field inapplicable.
  if (enableOpenAIFlattenNamespaces.value && allOpenAIOAuth.value) {
    const extra = ensureExtra()
    extra.openai_responses_flatten_namespaces = openaiFlattenNamespacesEnabled.value
  }

  if (enableImagesGIF.value && allOpenAIPassthroughCapable.value) {
    ensureExtra().images_gif_enabled = imagesGIFEnabled.value
  }
  if (enableCodexImageResponsesModel.value && allOpenAIOAuth.value) {
    applyCodexImageResponsesModel(ensureExtra(), codexImageResponsesModel.value)
  }

  if (enableCodexImageToolMode.value) {
    const extra = ensureExtra()
    applyCodexImageToolMode(extra, codexImageToolMode.value, 'null')
  }

  if (enableOpenAIResponsesContinuationSupported.value && allOpenAIAPIKey.value) {
    const extra = ensureExtra()
    extra.openai_responses_continuation_supported = openAIResponsesContinuationSupported.value
  }

  if (enableModelRestriction.value) {
    // Write both model representations together so clearing the whitelist or mapping replaces
    // saved restrictions and allows the default catalog behavior.
    const persisted = buildPersistedModelRestriction(allowedModels.value, modelMappings.value)
    credentials.model_mapping = persisted.modelMapping ?? {}
    credentials.model_whitelist = persisted.modelWhitelist
    credentialsChanged = true
  }

  if (enableCustomErrorCodes.value) {
    credentials.custom_error_codes_enabled = true
    credentials.custom_error_codes = [...selectedErrorCodes.value]
    credentialsChanged = true
  }

  if (enableInterceptWarmup.value && allInterceptWarmupCapable.value) {
    credentials.intercept_warmup_requests = interceptWarmupRequests.value
    credentialsChanged = true
  }

  if (enableHeaderOverride.value) {
    // Credential patches merge stored keys, so disabling overrides writes both false and an
    // empty object to replace the prior enabled configuration.
    credentials[HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY] = headerOverrideEnabled.value
    credentials[HEADER_OVERRIDES_CREDENTIAL_KEY] = headerOverrideEnabled.value
      ? buildHeaderOverridesObject(headerOverrideRows.value)
      : {}
    credentialsChanged = true
  }

  if (enableResponsesWSConnectionMode.value) {
    const extra = ensureExtra()
    extra.responses_ws_connection_mode = openaiOAuthResponsesWebSocketV2Mode.value
  }

  if (enableOpenAIAPIKeyWSMode.value) {
    const extra = ensureExtra()
    extra.responses_ws_connection_mode = openaiAPIKeyResponsesWebSocketV2Mode.value
  }

  if (enableCodexCLIOnly.value) {
    const extra = ensureExtra()
    extra.openai_oauth_client_policy = openAIOAuthClientPolicy.value
    // Replace the legacy policy boolean alongside the named policy so a saved true value cannot
    // survive a change away from Codex-only mode.
    extra.codex_cli_only = openAIOAuthClientPolicy.value === 'codex_only'
    if (openAIOAuthClientPolicy.value !== 'codex_only') {
      extra.codex_cli_only_allowed_clients = []
    }
  }

  if (enableCodexCLIOnlyAllowClaudeCode.value) {
    const extra = ensureExtra()
    extra.codex_cli_only_allowed_clients =
      (!enableCodexCLIOnly.value || openAIOAuthClientPolicy.value === 'codex_only') &&
      codexCLIOnlyAllowClaudeCodeEnabled.value
        ? ['claude_code']
        : []
  }

  if (enableAutoPause5hThreshold.value) {
    const extra = ensureExtra()
    extra.auto_pause_5h_threshold = buildAutoPauseThresholdRatio(autoPause5hThreshold.value)
  }

  if (enableAutoPause7dThreshold.value) {
    const extra = ensureExtra()
    extra.auto_pause_7d_threshold = buildAutoPauseThresholdRatio(autoPause7dThreshold.value)
  }

  if (enableAutoPause5hDisabled.value) {
    const extra = ensureExtra()
    extra.auto_pause_5h_disabled = autoPause5hDisabled.value
  }

  if (enableAutoPause7dDisabled.value) {
    const extra = ensureExtra()
    extra.auto_pause_7d_disabled = autoPause7dDisabled.value
  }

  if (enableTLSFingerprint.value) {
    const extra = ensureExtra()
    extra.enable_tls_fingerprint = tlsFingerprintEnabled.value
    extra.tls_fingerprint_profile_id = tlsFingerprintEnabled.value
      ? tlsFingerprintProfileId.value
      : 0
    if (allOpenAIOAuth.value) {
      // Disabling fingerprint handling explicitly clears the stored OpenAI OAuth router
      // identifier, because omission would retain it during the extra merge.
      extra.tls_fingerprint_router_id = tlsFingerprintEnabled.value
        ? (tlsFingerprintRouterId.value ?? 0)
        : 0
    }
  }

  if (enableCodexFingerprintMode.value) {
    const extra = ensureExtra()
    // Extra updates merge saved keys, so the off value must be sent explicitly to replace a
    // previously configured fingerprint mode.
    extra.codex_fingerprint_mode = codexFingerprintMode.value
  }

  if (enableOpenAICompactMode.value) {
    const extra = ensureExtra()
    extra.openai_compact_mode = openAICompactMode.value
  }

  if (enableOpenAINativeCompactionV2Mode.value) {
    const extra = ensureExtra()
    extra.openai_native_compaction_v2_mode = openAINativeCompactionV2Mode.value
  }

  if (
    enableOpenAICompactModelMapping.value &&
    enableOpenAICompactMode.value &&
    openAICompactMode.value !== 'force_off'
  ) {
    credentials.compact_model_mapping = buildOpenAICompactModelMapping() ?? {}
    credentialsChanged = true
  }

  // RPM configuration is stored in merged provider extra fields, so disabling it also clears
  // strategy and sticky-buffer overrides.
  if (enableRpmLimit.value) {
    const extra = ensureExtra()
    if (rpmLimitEnabled.value && bulkBaseRpm.value != null && bulkBaseRpm.value > 0) {
      extra.base_rpm = bulkBaseRpm.value
      extra.rpm_strategy = bulkRpmStrategy.value
      if (bulkRpmStickyBuffer.value != null && bulkRpmStickyBuffer.value > 0) {
        extra.rpm_sticky_buffer = bulkRpmStickyBuffer.value
      }
    } else {
      // Merged extra values survive omission; write cleared base, strategy, and buffer values
      // together so disabled RPM cannot retain an old override.
      extra.base_rpm = 0
      extra.rpm_strategy = ''
      extra.rpm_sticky_buffer = 0
    }
    updates.extra = extra
  }

  // The message-queue override follows the RPM application flag, while null leaves its saved
  // value untouched and empty text requests clearing.
  if (enableRpmLimit.value && userMsgQueueMode.value !== null) {
    const umqExtra = ensureExtra()
    umqExtra.user_msg_queue_mode = userMsgQueueMode.value // Empty text clears the provider override.
    umqExtra.user_msg_queue_enabled = false // Replace the legacy merged flag.
  }

  if (enableUpstreamProtocols.value && upstreamProtocols.value !== undefined) {
    credentials.upstream_protocols = [...upstreamProtocols.value]
    credentialsChanged = true
  }

  if (
    targetSelectedPlatforms.value.length === 1 &&
    targetSelectedPlatforms.value[0] === 'openai' &&
    targetSelectedTypes.value.length === 1 &&
    targetSelectedTypes.value[0] === 'apikey'
  ) {
    for (const field of IMAGE_MODE_FIELDS) {
      if (applyImageModes[field.key]) {
        credentials[field.key] = imageModes.value[field.key]
        credentialsChanged = true
      }
    }
  }

  if (credentialsChanged) {
    updates.credentials = credentials
  }

  return Object.keys(updates).length > 0 ? updates : null
}

/**
 * handleClose requests parent-controlled dismissal of the bulk provider editor.
 */
const handleClose = () => {
  emit('close')
}

/**
 * handleSubmit validates target selection, enabled fields, applicable base URLs, and enabled
 * header overrides before building a patch. An empty patch or invalid draft reports an error;
 * valid updates delegate to submitBulkUpdate.
 */
const handleSubmit = async () => {
  if (targetMode.value === 'selected' && props.providerIds.length === 0) {
    appStore.showError(t('admin.providers.bulkEdit.noSelection'))
    return
  }

  const hasAnyFieldEnabled =
    enableImagesGIF.value ||
    applyImageModes.images_generations_mode ||
    applyImageModes.images_edits_mode ||
    enableUpstreamProtocols.value ||
    (enableBaseUrl.value && allBaseUrlCapable.value) ||
    enableOpenAIPassthrough.value ||
    enableOpenAIFlattenNamespaces.value ||
    enableCodexImageToolMode.value ||
    (enableCodexImageResponsesModel.value && allOpenAIOAuth.value) ||
    enableOpenAIResponsesContinuationSupported.value ||
    enableModelRestriction.value ||
    enableCustomErrorCodes.value ||
    (enableInterceptWarmup.value && allInterceptWarmupCapable.value) ||
    enableHeaderOverride.value ||
    enableProxy.value ||
    enableConcurrency.value ||
    enableLoadFactor.value ||
    enablePriority.value ||
    enableRateMultiplier.value ||
    enableStatus.value ||
    enableGroups.value ||
    enableResponsesWSConnectionMode.value ||
    enableOpenAIAPIKeyWSMode.value ||
    enableCodexCLIOnly.value ||
    enableCodexCLIOnlyAllowClaudeCode.value ||
    enableAutoPause5hThreshold.value ||
    enableAutoPause7dThreshold.value ||
    enableAutoPause5hDisabled.value ||
    enableAutoPause7dDisabled.value ||
    enableTLSFingerprint.value ||
    enableCodexFingerprintMode.value ||
    enableOpenAICompactMode.value ||
    enableOpenAINativeCompactionV2Mode.value ||
    enableOpenAICompactModelMapping.value ||
    enableRpmLimit.value

  if (!hasAnyFieldEnabled) {
    appStore.showError(t('admin.providers.bulkEdit.noFieldsSelected'))
    return
  }

  // Applied endpoint text reaches forwarding configuration, so require an HTTP scheme before
  // submitting a nonempty base URL for these targets.
  if (enableBaseUrl.value && allBaseUrlCapable.value) {
    const trimmedBaseUrl = baseUrl.value.trim()
    if (trimmedBaseUrl && !/^https?:\/\//i.test(trimmedBaseUrl)) {
      appStore.showError(t('admin.providers.grokCustomBaseUrl.invalid'))
      return
    }
  }

  if (enableHeaderOverride.value && headerOverrideEnabled.value) {
    // Enabled header overrides replace the stored object, so reject an empty enabled editor and
    // reserve deliberate clearing for the disabled-switch path.
    if (!headerOverrideRows.value.some((row) => row.name.trim())) {
      appStore.showError(t('admin.providers.headerOverride.bulkEmptyRows'))
      return
    }
    const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
    if (headerError) {
      appStore.showError(t(`admin.providers.headerOverride.${headerError}`))
      return
    }
  }

  const built = buildUpdatePayload()
  if (!built) {
    appStore.showError(t('admin.providers.bulkEdit.noFieldsSelected'))
    return
  }

  await submitBulkUpdate(built)
}

/**
 * submitBulkUpdate submits a patch against server filters when supplied for filtered mode,
 * otherwise explicit provider identifiers. Any successful update emits updated and closes,
 * including partial success; complete failure leaves the dialog open.
 */
const submitBulkUpdate = async (baseUpdates: Record<string, unknown>) => {
  const updates = baseUpdates

  submitting.value = true

  try {
    const res =
      targetMode.value === 'filtered' && props.target?.filters
        ? await adminAPI.providers.bulkUpdate({
            filters: props.target.filters,
            ...updates,
          })
        : await adminAPI.providers.bulkUpdate(props.providerIds, updates)
    const success = res.success || 0
    const failed = res.failed || 0

    if (success > 0 && failed === 0) {
      appStore.showSuccess(t('admin.providers.bulkEdit.success', { count: success }))
    } else if (success > 0) {
      appStore.showError(t('admin.providers.bulkEdit.partialSuccess', { success, failed }))
    } else {
      appStore.showError(t('admin.providers.bulkEdit.failed'))
    }

    if (success > 0) {
      emit('updated')
      handleClose()
    }
  } catch (error: any) {
    appStore.showError(error.message || t('admin.providers.bulkEdit.failed'))
    console.error('Error bulk updating providers:', error)
  } finally {
    submitting.value = false
  }
}

/**
 * resetBulkEditFormState clears all application flags and restores editor defaults, including
 * absent protocol selection and empty model restrictions. It runs after the dialog hides so a
 * later opening starts with no field selected.
 */
const resetBulkEditFormState = () => {
  enableBaseUrl.value = false
  enableModelRestriction.value = false
  enableCustomErrorCodes.value = false
  enableInterceptWarmup.value = false
  enableHeaderOverride.value = false
  enableProxy.value = false
  enableConcurrency.value = false
  enableLoadFactor.value = false
  enablePriority.value = false
  enableRateMultiplier.value = false
  enableStatus.value = false
  enableGroups.value = false
  enableOpenAIPassthrough.value = false
  enableOpenAIFlattenNamespaces.value = false
  enableCodexImageToolMode.value = false
  enableCodexImageResponsesModel.value = false
  enableImagesGIF.value = false
  imagesGIFEnabled.value = true
  codexImageResponsesModel.value = ''
  enableUpstreamProtocols.value = false
  upstreamProtocols.value = undefined
  imageModes.value = readImageStreamModes()
  applyImageModes.images_generations_mode = false
  applyImageModes.images_edits_mode = false
  enableOpenAIResponsesContinuationSupported.value = false
  enableResponsesWSConnectionMode.value = false
  enableOpenAIAPIKeyWSMode.value = false
  enableCodexCLIOnly.value = false
  enableCodexCLIOnlyAllowClaudeCode.value = false
  enableAutoPause5hThreshold.value = false
  enableAutoPause7dThreshold.value = false
  enableAutoPause5hDisabled.value = false
  enableAutoPause7dDisabled.value = false
  enableCodexFingerprintMode.value = false
  enableOpenAICompactMode.value = false
  enableOpenAINativeCompactionV2Mode.value = false
  enableOpenAICompactModelMapping.value = false
  enableRpmLimit.value = false
  enableTLSFingerprint.value = false

  baseUrl.value = ''
  openaiPassthroughEnabled.value = false
  openaiFlattenNamespacesEnabled.value = false
  codexImageToolMode.value = 'inherit'
  openAIResponsesContinuationSupported.value = false
  resetModelRestrictionDraft()
  selectedErrorCodes.value = []
  interceptWarmupRequests.value = false
  headerOverrideEnabled.value = false
  headerOverrideRows.value = []
  proxyId.value = null
  concurrency.value = 1
  loadFactor.value = null
  priority.value = 1
  rateMultiplier.value = 1
  status.value = 'active'
  groupIds.value = []
  openaiOAuthResponsesWebSocketV2Mode.value = RESPONSES_WS_POOLED
  openaiAPIKeyResponsesWebSocketV2Mode.value = RESPONSES_WS_POOLED
  openAIOAuthClientPolicy.value = 'any'
  codexCLIOnlyAllowClaudeCodeEnabled.value = false
  autoPause5hThreshold.value = null
  autoPause7dThreshold.value = null
  autoPause5hDisabled.value = false
  autoPause7dDisabled.value = false
  codexFingerprintMode.value = 'off'
  openAICompactMode.value = 'force_on'
  openAINativeCompactionV2Mode.value = 'force_on'
  openAICompactModelMappings.value = []
  rpmLimitEnabled.value = false
  bulkBaseRpm.value = null
  bulkRpmStrategy.value = 'tiered'
  bulkRpmStickyBuffer.value = null
  userMsgQueueMode.value = null
  tlsFingerprintEnabled.value = false
  tlsFingerprintProfileId.value = 0
  tlsFingerprintRouterId.value = null
}

watch(
  [
    () => props.show,
    () => props.providerIds.join(','),
    () => props.selectedPlatforms.join(','),
    () => props.selectedTypes.join(','),
  ],
  ([newShow]) => {
    if (newShow) {
      if (allTLSFingerprintCapable.value) {
        void loadTLSFingerprintProfiles()
      }
      if (allOpenAIOAuth.value) {
        void loadTLSFingerprintRouters()
      }
      void loadSelectedProviderDefaults()
      return
    }

    // Invalidate in-flight prefill before resetting drafts on hide so a late provider lookup
    // cannot repopulate the next dialog lifecycle.
    modelRestrictionPrefillSeq.value++
    resetBulkEditFormState()
  },
  { immediate: true },
)
</script>
