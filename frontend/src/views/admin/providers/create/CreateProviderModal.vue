<template>
  <BaseDialog
    :show="show"
    :title="t('admin.providers.createProvider')"
    width="wide"
    :body-scroll="false"
    @close="handleClose"
  >
    <!-- OAuth providers show the configuration and authorization steps. -->
    <div v-if="isOAuthFlow" class="mb-4 flex shrink-0 items-center justify-center">
      <div class="flex items-center gap-4">
        <div class="flex items-center gap-2">
          <span
            :class="[
              'flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold',
              step >= 1
                ? 'bg-primary-600 text-white'
                : 'bg-gray-200 text-gray-500 dark:bg-dark-600',
            ]"
            >1</span
          >
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
            t('admin.providers.oauth.authMethod')
          }}</span>
        </div>
        <div class="h-0.5 w-8 bg-gray-300 dark:bg-dark-600" />
        <div class="flex items-center gap-2">
          <span
            :class="[
              'flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold',
              step >= 2
                ? 'bg-primary-600 text-white'
                : 'bg-gray-200 text-gray-500 dark:bg-dark-600',
            ]"
            >2</span
          >
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
            oauthStepTitle
          }}</span>
        </div>
      </div>
    </div>

    <!-- Step one collects provider configuration. -->
    <form
      v-if="step === 1"
      v-content-reveal
      id="create-provider-form"
      novalidate
      class="flex min-h-0 flex-1 flex-col"
      @submit.prevent="handleSubmit"
    >
      <SettingsTabs
        ref="tabsRef"
        id-prefix="create-provider"
        :tabs="formTabs"
        :label="t('admin.providers.tabs.label')"
      >
        <template #basic>
          <SettingsSection :title="t('admin.providers.sections.identity')">
            <div class="grid gap-4 md:grid-cols-2">
              <div data-provider-field="name">
                <label for="create-provider-name" class="input-label">{{
                  t('admin.providers.providerName')
                }}</label>
                <input
                  id="create-provider-name"
                  v-model="form.name"
                  type="text"
                  :required="!isGrokSSOInputMethod"
                  class="input"
                  :placeholder="t('admin.providers.enterProviderName')"
                  data-tour="provider-form-name"
                />
              </div>
              <div>
                <label for="create-provider-expires-at" class="input-label">{{
                  t('admin.providers.expiresAt')
                }}</label>
                <input
                  id="create-provider-expires-at"
                  v-model="expiresAtInput"
                  type="datetime-local"
                  class="input"
                />
                <p class="input-hint">{{ t('admin.providers.expiresAtHint') }}</p>
              </div>
              <div class="md:col-span-2">
                <label for="create-provider-notes" class="input-label">{{
                  t('admin.providers.notes')
                }}</label>
                <textarea
                  id="create-provider-notes"
                  v-model="form.notes"
                  rows="3"
                  class="input"
                  :placeholder="t('admin.providers.notesPlaceholder')"
                ></textarea>
                <p class="input-hint">{{ t('admin.providers.notesHint') }}</p>
              </div>
            </div>
          </SettingsSection>

          <SettingsSection :title="t('admin.providers.sections.platformAndType')">
            <!-- Platform choices fill two balanced rows and wrap within each row on narrow screens. -->
            <div class="space-y-2">
              <span class="input-label">{{ t('admin.providers.platform') }}</span>
              <div
                v-for="(row, rowIndex) in platformRows"
                :key="rowIndex"
                v-segmented
                class="segmented flex w-full flex-wrap"
                role="radiogroup"
                :aria-label="t('admin.providers.platform')"
                :data-tour="rowIndex === 0 ? 'provider-form-platform' : undefined"
              >
                <button
                  v-for="item in row"
                  :key="item.value"
                  type="button"
                  role="radio"
                  :aria-checked="form.platform === item.value"
                  :data-testid="item.testid"
                  class="segmented-item flex h-9 flex-1 items-center justify-center gap-2 px-3 text-sm"
                  :class="{ 'segmented-item-active': form.platform === item.value }"
                  @click="selectPlatform(item.value)"
                >
                  <!-- Platform icons retain brand colors; selected text sets its color on the span so segmented-control hover styles do not override it. -->
                  <PlatformIcon
                    :platform="item.value"
                    size="sm"
                    :class="platformIconClass(item.value)"
                  />
                  <span :class="form.platform === item.value && platformTextClass(item.value)">{{
                    platformLabel(item.value)
                  }}</span>
                </button>
              </div>
            </div>

            <!-- Anthropic provider authentication category. -->
            <div v-if="form.platform === 'anthropic'" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.providerType') }}</span>
              <div
                class="grid grid-cols-1 gap-2 sm:grid-cols-2"
                role="radiogroup"
                data-tour="provider-form-type"
              >
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'oauth-based'"
                  icon="sparkles"
                  :title="t('admin.providers.claudeCode')"
                  :description="t('admin.providers.oauthSetupToken')"
                  @click="providerCategory = 'oauth-based'"
                />
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'apikey'"
                  icon="key"
                  :title="t('admin.providers.claudeConsole')"
                  :description="t('admin.providers.apiKey')"
                  @click="providerCategory = 'apikey'"
                />
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'bedrock'"
                  icon="cloud"
                  :title="t('admin.providers.bedrockLabel')"
                  :description="t('admin.providers.bedrockDesc')"
                  @click="providerCategory = 'bedrock'"
                />
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'service_account'"
                  icon="cloud"
                  title="Vertex"
                  description="Service Account"
                  @click="providerCategory = 'service_account'"
                />
              </div>
              <SettingsNotice v-if="providerCategory === 'service_account'">
                {{ t('admin.providers.vertexAnthropicHint') }}
              </SettingsNotice>
            </div>

            <!-- OpenAI provider authentication category. -->
            <div v-if="form.platform === 'openai'" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.providerType') }}</span>
              <div
                class="grid grid-cols-1 gap-2 sm:grid-cols-2"
                role="radiogroup"
                data-tour="provider-form-type"
              >
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'oauth-based'"
                  icon="key"
                  title="OAuth"
                  :description="t('admin.providers.types.chatgptOauth')"
                  @click="providerCategory = 'oauth-based'"
                />
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'apikey'"
                  icon="key"
                  title="API Key"
                  :description="t('admin.providers.types.responsesApi')"
                  @click="providerCategory = 'apikey'"
                />
              </div>
            </div>

            <!-- Grok provider authentication category. -->
            <div v-if="form.platform === 'grok'" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.providerType') }}</span>
              <div
                class="grid grid-cols-1 gap-2 sm:grid-cols-2"
                role="radiogroup"
                data-tour="provider-form-type"
              >
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'oauth-based'"
                  platform="grok"
                  title="OAuth"
                  :description="t('admin.providers.types.grokOauth')"
                  @click="providerCategory = 'oauth-based'"
                />
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerCategory === 'apikey'"
                  icon="key"
                  title="API Key"
                  :description="t('admin.providers.types.responsesApi')"
                  data-testid="grok-provider-type-api-key"
                  @click="providerCategory = 'apikey'"
                />
              </div>
            </div>

            <!-- Provider mode selection for platforms with billing or service modes. -->
            <div v-if="modeChoices.length > 0" class="space-y-2">
              <span class="input-label">{{
                t('admin.providers.cnProviders.providerMode.title')
              }}</span>
              <div
                class="grid grid-cols-1 gap-2 sm:grid-cols-2"
                role="radiogroup"
                data-tour="provider-form-mode"
              >
                <ProviderChoiceCard
                  v-for="choice in modeChoices"
                  :key="choice.value"
                  :accent="form.platform"
                  :selected="providerMode === choice.value"
                  :icon="choice.icon"
                  :title="t(choice.titleKey)"
                  :description="t(choice.descriptionKey)"
                  :data-testid="`provider-mode-${choice.value}`"
                  @click="providerMode = choice.value"
                />
              </div>
            </div>

            <!-- Zhipu team Coding Plan accepts optional organization and project IDs; organization identity selects the team quota endpoint. -->
            <div
              v-if="form.platform === 'zhipu' && providerMode === 'coding'"
              v-content-reveal
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
                  <label for="create-zhipu-organization" class="input-label">{{
                    t('admin.providers.cnProviders.zhipuTeam.organization')
                  }}</label>
                  <input
                    id="create-zhipu-organization"
                    v-model="zhipuOrganization"
                    type="text"
                    class="input"
                    :placeholder="
                      t('admin.providers.cnProviders.zhipuTeam.organizationPlaceholder')
                    "
                  />
                </div>
                <div>
                  <label for="create-zhipu-project" class="input-label">{{
                    t('admin.providers.cnProviders.zhipuTeam.project')
                  }}</label>
                  <input
                    id="create-zhipu-project"
                    v-model="zhipuProject"
                    type="text"
                    class="input"
                    :placeholder="t('admin.providers.cnProviders.zhipuTeam.projectPlaceholder')"
                  />
                </div>
              </div>
              <p class="input-hint">{{ t('admin.providers.cnProviders.zhipuTeam.hint') }}</p>
            </div>

            <!-- Gemini provider authentication category. -->
            <div v-if="form.platform === 'gemini'" v-content-reveal class="space-y-4">
              <div class="space-y-2">
                <div class="flex items-center justify-between">
                  <span class="input-label mb-0">{{ t('admin.providers.providerType') }}</span>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    @click="showGeminiHelpDialog = true"
                  >
                    <Icon name="questionCircle" size="sm" />
                    {{ t('admin.providers.gemini.helpButton') }}
                  </button>
                </div>
                <div
                  class="grid grid-cols-1 gap-2 sm:grid-cols-3"
                  role="radiogroup"
                  data-tour="provider-form-type"
                >
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="providerCategory === 'oauth-based'"
                    icon="key"
                    :title="t('admin.providers.gemini.providerType.oauthTitle')"
                    :description="t('admin.providers.gemini.providerType.oauthDesc')"
                    @click="providerCategory = 'oauth-based'"
                  />
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="providerCategory === 'apikey'"
                    icon="key"
                    :title="t('admin.providers.gemini.providerType.apiKeyTitle')"
                    :description="t('admin.providers.gemini.providerType.apiKeyDesc')"
                    data-testid="create-gemini-apikey-type"
                    @click="providerCategory = 'apikey'"
                  />
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="providerCategory === 'service_account'"
                    icon="cloud"
                    title="Vertex"
                    description="Service Account"
                    @click="providerCategory = 'service_account'"
                  />
                </div>
                <SettingsNotice
                  v-if="providerCategory === 'apikey' && geminiProviderType === 'official'"
                >
                  <p>{{ t('admin.providers.gemini.providerType.apiKeyNote') }}</p>
                  <a
                    :href="geminiHelpLinks.apiKey"
                    class="font-medium text-link"
                    target="_blank"
                    rel="noreferrer"
                  >
                    {{ t('admin.providers.gemini.providerType.apiKeyLink') }}
                  </a>
                </SettingsNotice>
                <SettingsNotice v-if="providerCategory === 'service_account'">
                  {{ t('admin.providers.vertexGeminiHint') }}
                </SettingsNotice>
              </div>

              <!-- Gemini OAuth authorization variant. -->
              <div v-if="providerCategory === 'oauth-based'" v-content-reveal class="space-y-2">
                <span class="input-label">{{
                  t('admin.providers.oauth.gemini.oauthTypeLabel')
                }}</span>
                <div class="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup">
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="geminiOAuthType === 'google_one'"
                    icon="user"
                    :title="t('admin.providers.gemini.oauthType.googleOneTitle')"
                    :description="t('admin.providers.gemini.oauthType.googleOneDesc')"
                    @click="handleSelectGeminiOAuthType('google_one')"
                  >
                    <span class="mt-2 flex flex-wrap gap-1">
                      <span class="provider-choice-badge">{{
                        t('admin.providers.gemini.oauthType.badges.personal')
                      }}</span>
                      <span class="provider-choice-badge">{{
                        t('admin.providers.gemini.oauthType.badges.noGcp')
                      }}</span>
                    </span>
                  </ProviderChoiceCard>
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="geminiOAuthType === 'code_assist'"
                    icon="cloud"
                    :title="t('admin.providers.gemini.oauthType.codeAssistTitle')"
                    :description="t('admin.providers.gemini.oauthType.codeAssistDesc')"
                    @click="handleSelectGeminiOAuthType('code_assist')"
                  >
                    <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
                      {{ t('admin.providers.gemini.oauthType.codeAssistRequirement') }}
                      <a
                        :href="geminiHelpLinks.gcpProject"
                        class="ml-1 text-link"
                        target="_blank"
                        rel="noreferrer"
                        @click.stop
                      >
                        {{ t('admin.providers.gemini.oauthType.gcpProjectLink') }}
                      </a>
                    </span>
                    <span class="mt-2 flex flex-wrap gap-1">
                      <span class="provider-choice-badge">{{
                        t('admin.providers.gemini.oauthType.badges.enterprise')
                      }}</span>
                      <span class="provider-choice-badge">{{
                        t('admin.providers.gemini.oauthType.badges.highConcurrency')
                      }}</span>
                    </span>
                  </ProviderChoiceCard>
                </div>

                <!-- Custom OAuth client controls remain collapsed until advanced settings are opened. -->
                <button
                  type="button"
                  class="flex items-center gap-2 text-sm text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200"
                  :aria-expanded="showAdvancedOAuth"
                  @click="showAdvancedOAuth = !showAdvancedOAuth"
                >
                  <Icon
                    name="chevronRight"
                    size="sm"
                    :animate-on-hover="false"
                    :class="['transition-transform', showAdvancedOAuth ? 'rotate-90' : '']"
                  />
                  <span>{{
                    showAdvancedOAuth
                      ? t('admin.providers.gemini.oauthType.hideAdvanced')
                      : t('admin.providers.gemini.oauthType.showAdvanced')
                  }}</span>
                </button>
                <Collapse :open="showAdvancedOAuth" unmount-on-hide>
                  <div class="space-y-2">
                    <ProviderChoiceCard
                      :accent="form.platform"
                      :selected="geminiOAuthType === 'ai_studio'"
                      :disabled="!geminiAIStudioOAuthEnabled"
                      icon="sparkles"
                      :title="t('admin.providers.gemini.oauthType.customTitle')"
                      :description="t('admin.providers.gemini.oauthType.customDesc')"
                      @click="handleSelectGeminiOAuthType('ai_studio')"
                    >
                      <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
                        {{ t('admin.providers.gemini.oauthType.customRequirement') }}
                      </span>
                      <span class="mt-2 flex flex-wrap gap-1">
                        <span class="provider-choice-badge">{{
                          t('admin.providers.gemini.oauthType.badges.orgManaged')
                        }}</span>
                        <span class="provider-choice-badge">{{
                          t('admin.providers.gemini.oauthType.badges.adminRequired')
                        }}</span>
                      </span>
                    </ProviderChoiceCard>
                    <SettingsNotice v-if="!geminiAIStudioOAuthEnabled" tone="warning">
                      {{ t('admin.providers.oauth.gemini.aiStudioNotConfiguredTip') }}
                    </SettingsNotice>
                  </div>
                </Collapse>
              </div>

              <!-- Fallback quota tier used when automatic detection fails. -->
              <div v-if="providerCategory === 'oauth-based'" v-content-reveal>
                <label for="create-gemini-oauth-tier" class="input-label">{{
                  t('admin.providers.gemini.tier.label')
                }}</label>
                <Select
                  v-if="geminiOAuthType === 'google_one'"
                  id="create-gemini-oauth-tier"
                  v-model="geminiTierGoogleOne"
                  :options="geminiGoogleOneTierOptions"
                />
                <Select
                  v-else-if="geminiOAuthType === 'code_assist'"
                  id="create-gemini-oauth-tier"
                  v-model="geminiTierGcp"
                  :options="geminiGCPTierOptions"
                />
                <Select
                  v-else
                  id="create-gemini-oauth-tier"
                  v-model="geminiTierAIStudio"
                  :options="geminiAIStudioTierOptions"
                />
                <p class="input-hint">{{ t('admin.providers.gemini.tier.hint') }}</p>
              </div>
            </div>

            <!-- Qoder site is selected before authorization starts so the session uses the intended regional endpoints. -->
            <template v-if="form.platform === 'qoder'">
              <div v-content-reveal class="space-y-2">
                <span class="input-label">{{ t('admin.providers.qoder.site.label') }}</span>
                <SettingsSegmented
                  v-model="qoderSite"
                  block
                  :disabled="submitting || isQoderOAuthProviderCreating"
                  :ariaLabel="t('admin.providers.qoder.site.label')"
                  :options="qoderSiteOptions"
                />
              </div>
              <div v-content-reveal class="space-y-2">
                <span class="input-label">{{ t('admin.providers.providerType') }}</span>
                <div class="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup">
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="qoderProviderType === 'oauth'"
                    icon="link"
                    :title="t('admin.providers.qoder.providerType.oauthTitle')"
                    :description="t('admin.providers.qoder.providerType.oauthDesc')"
                    @click="qoderProviderType = 'oauth'"
                  />
                  <ProviderChoiceCard
                    :accent="form.platform"
                    :selected="qoderProviderType === 'manual'"
                    icon="key"
                    :title="t('admin.providers.qoder.providerType.manualTitle')"
                    :description="t('admin.providers.qoder.providerType.manualDesc')"
                    @click="qoderProviderType = 'manual'"
                  />
                </div>
              </div>
            </template>

            <!-- Anthropic OAuth or setup-token creation method. -->
            <div
              v-if="form.platform === 'anthropic' && isOAuthFlow"
              v-content-reveal
              class="space-y-2"
            >
              <span class="input-label">{{ t('admin.providers.addMethod') }}</span>
              <SettingsSegmented
                v-model="addMethod"
                :ariaLabel="t('admin.providers.addMethod')"
                :options="addMethodOptions"
              />
            </div>
          </SettingsSection>

          <SettingsSection
            v-if="showCredentialsSection"
            :title="t('admin.providers.sections.credentials')"
          >
            <!-- Antigravity OAuth project identity. -->
            <div v-if="form.platform === 'antigravity'" v-content-reveal>
              <label for="create-antigravity-project-id" class="input-label">{{
                t('admin.providers.antigravityProjectIdLabel')
              }}</label>
              <input
                id="create-antigravity-project-id"
                v-model="antigravityProjectId"
                data-testid="antigravity-project-id-input"
                type="text"
                class="input font-mono"
                :placeholder="t('admin.providers.antigravityProjectIdPlaceholder')"
              />
              <p class="input-hint">{{ t('admin.providers.antigravityProjectIdHint') }}</p>
            </div>

            <!-- Qoder manual credential entry. -->
            <div
              v-if="form.platform === 'qoder' && qoderProviderType === 'manual'"
              v-content-reveal
              class="space-y-4"
            >
              <div data-provider-field="qoder-pat">
                <label for="create-qoder-pat" class="input-label">{{
                  t('admin.providers.qoder.pat')
                }}</label>
                <input
                  id="create-qoder-pat"
                  v-model="qoderPAT"
                  type="password"
                  class="input font-mono"
                  autocomplete="off"
                  placeholder="pat-..."
                />
                <p class="input-hint">{{ t('admin.providers.qoder.patHint') }}</p>
              </div>
              <div class="flex items-center gap-3">
                <div class="h-px flex-1 bg-gray-200 dark:bg-dark-600"></div>
                <span class="text-xs uppercase tracking-wide text-gray-400">{{
                  t('common.or')
                }}</span>
                <div class="h-px flex-1 bg-gray-200 dark:bg-dark-600"></div>
              </div>
              <div data-provider-field="qoder-security-token">
                <label for="create-qoder-security-token" class="input-label">{{
                  t('admin.providers.qoder.securityOauthToken')
                }}</label>
                <input
                  id="create-qoder-security-token"
                  v-model="qoderSecurityOauthToken"
                  type="password"
                  class="input font-mono"
                  autocomplete="off"
                  placeholder="dt-..."
                />
                <p class="input-hint">{{ t('admin.providers.qoder.securityOauthTokenHint') }}</p>
              </div>
              <div class="grid gap-4 md:grid-cols-2">
                <div data-provider-field="qoder-machine-id">
                  <label for="create-qoder-machine-id" class="input-label">{{
                    t('admin.providers.qoder.machineId')
                  }}</label>
                  <input
                    id="create-qoder-machine-id"
                    v-model="qoderMachineId"
                    type="text"
                    class="input font-mono"
                    autocomplete="off"
                    placeholder="machine_id"
                  />
                  <p class="input-hint">{{ t('admin.providers.qoder.machineIdHint') }}</p>
                </div>
                <div data-provider-field="qoder-uid-aid">
                  <label for="create-qoder-uid-aid" class="input-label">{{
                    t('admin.providers.qoder.uidAid')
                  }}</label>
                  <input
                    id="create-qoder-uid-aid"
                    v-model="qoderUidAid"
                    type="text"
                    class="input font-mono"
                    autocomplete="off"
                    placeholder="uid or aid"
                  />
                  <p class="input-hint">{{ t('admin.providers.qoder.uidAidHint') }}</p>
                </div>
                <div>
                  <label for="create-qoder-refresh-token" class="input-label">{{
                    t('admin.providers.qoder.refreshToken')
                  }}</label>
                  <input
                    id="create-qoder-refresh-token"
                    v-model="qoderRefreshToken"
                    type="password"
                    class="input font-mono"
                    autocomplete="off"
                  />
                </div>
                <div>
                  <label for="create-qoder-user-type" class="input-label">{{
                    t('admin.providers.qoder.userType')
                  }}</label>
                  <input
                    id="create-qoder-user-type"
                    v-model="qoderUserType"
                    type="text"
                    class="input font-mono"
                    autocomplete="off"
                    placeholder="personal_standard"
                  />
                </div>
              </div>
            </div>

            <!-- Vertex Service Account -->
            <div v-if="isServiceAccountCategory" class="space-y-4">
              <div data-provider-field="vertex-sa-json">
                <span class="input-label">{{ t('admin.providers.vertexSaJsonLabel') }}</span>
                <input
                  ref="vertexServiceAccountFileInput"
                  type="file"
                  accept="application/json,.json"
                  class="hidden"
                  @change="handleVertexServiceAccountFile"
                />
                <div
                  :class="[
                    'rounded-control border-2 border-dashed px-4 py-4 transition-colors',
                    vertexServiceAccountDragActive
                      ? 'border-primary-500 bg-primary-50 dark:bg-primary-500/8'
                      : 'border-gray-300 bg-gray-50 hover:border-primary-400 dark:border-dark-500 dark:bg-dark-800/40 dark:hover:border-primary-500/60',
                  ]"
                  @dragenter.prevent="vertexServiceAccountDragActive = true"
                  @dragover.prevent="vertexServiceAccountDragActive = true"
                  @dragleave.prevent="vertexServiceAccountDragActive = false"
                  @drop.prevent="handleVertexServiceAccountDrop"
                >
                  <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div class="min-w-0">
                      <div
                        class="flex items-center gap-2 text-sm font-medium text-primary-900 dark:text-dark-50"
                      >
                        <Icon name="upload" size="sm" />
                        <span>{{
                          vertexClientEmail
                            ? t('admin.providers.vertexSaJsonLoaded')
                            : t('admin.providers.vertexSaJsonDrop')
                        }}</span>
                      </div>
                      <p class="input-hint">
                        {{
                          vertexClientEmail
                            ? t('admin.providers.vertexSaJsonKeyHidden')
                            : t('admin.providers.vertexSaJsonDropHint')
                        }}
                      </p>
                    </div>
                    <button
                      type="button"
                      class="btn btn-secondary shrink-0"
                      @click="vertexServiceAccountFileInput?.click()"
                    >
                      <Icon name="upload" size="sm" />
                      {{ t('admin.providers.vertexSaJsonSelectBtn') }}
                    </button>
                  </div>
                  <div
                    v-if="vertexClientEmail"
                    class="mt-3 rounded-control border border-gray-200 bg-white px-3 py-2 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-900 dark:text-gray-300"
                  >
                    <div class="truncate">
                      {{ t('admin.providers.vertexProjectId') }}:
                      <span class="font-mono">{{ vertexProjectId }}</span>
                    </div>
                    <div class="truncate">
                      {{ t('admin.providers.vertexClientEmail') }}:
                      <span class="font-mono">{{ vertexClientEmail }}</span>
                    </div>
                  </div>
                </div>
                <p class="input-hint">{{ t('admin.providers.vertexSaJsonUploadHint') }}</p>
              </div>

              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="create-vertex-project-id" class="input-label">{{
                    t('admin.providers.vertexProjectId')
                  }}</label>
                  <input
                    id="create-vertex-project-id"
                    v-model="vertexProjectId"
                    type="text"
                    class="input font-mono"
                    readonly
                    :placeholder="t('admin.providers.vertexProjectIdPlaceholder')"
                  />
                </div>
                <div data-provider-field="vertex-location">
                  <label for="create-vertex-location" class="input-label">{{
                    t('admin.providers.vertexLocation')
                  }}</label>
                  <Select
                    id="create-vertex-location"
                    v-model="vertexLocation"
                    :options="vertexLocationOptions"
                    class="font-mono"
                    searchable
                  />
                  <p class="input-hint">{{ t('admin.providers.vertexLocationHint') }}</p>
                </div>
              </div>
            </div>

            <!-- API Key credentials; Antigravity uses its upstream fields above. -->
            <template v-if="isApiKeyCredentials">
              <div v-if="form.platform === 'gemini'" v-content-reveal>
                <label for="create-gemini-provider-type" class="input-label">{{
                  t('admin.providers.gemini.connectionSource.label')
                }}</label>
                <Select
                  id="create-gemini-provider-type"
                  v-model="geminiProviderType"
                  :options="geminiProviderTypeOptions"
                  data-testid="create-gemini-provider-type"
                />
                <p class="input-hint">{{ geminiProviderTypeHint }}</p>
              </div>
              <div
                v-if="!isCNPlatform || apiProtocol !== 'adaptive'"
                data-provider-field="base-url"
              >
                <label for="create-provider-base-url" class="input-label">{{
                  t('admin.providers.baseUrl')
                }}</label>
                <input
                  id="create-provider-base-url"
                  v-model="apiKeyBaseUrl"
                  type="text"
                  class="input"
                  data-testid="create-provider-base-url"
                  :placeholder="apiKeyBaseUrlPlaceholder"
                />
                <p v-if="baseUrlHint" class="input-hint">{{ baseUrlHint }}</p>
                <GrokBaseUrlPresets
                  v-if="form.platform === 'grok'"
                  class="mt-2"
                  @select="apiKeyBaseUrl = $event"
                />
                <CnBaseUrlPresets
                  v-if="isCNPlatform"
                  class="mt-2"
                  :platform="cnPresetPlatform"
                  :mode="providerMode"
                  :protocol="apiProtocol"
                  :current-url="apiKeyBaseUrl"
                  @select="onCnPresetSelect"
                />
              </div>
              <div class="grid gap-4 md:grid-cols-2">
                <div
                  data-provider-field="api-key"
                  :class="{ 'md:col-span-2': !showGeminiApiKeyTier }"
                >
                  <label for="create-provider-api-key" class="input-label">{{
                    t('admin.providers.apiKeyRequired')
                  }}</label>
                  <input
                    id="create-provider-api-key"
                    v-model="apiKeyValue"
                    type="password"
                    required
                    class="input font-mono"
                    :placeholder="apiKeyPlaceholder"
                  />
                  <p v-if="apiKeyHint" class="input-hint">{{ apiKeyHint }}</p>
                </div>
                <div v-if="showGeminiApiKeyTier" v-content-reveal data-testid="create-gemini-tier">
                  <label for="create-gemini-tier" class="input-label">{{
                    t('admin.providers.gemini.tier.label')
                  }}</label>
                  <Select
                    id="create-gemini-tier"
                    v-model="geminiTierAIStudio"
                    :options="geminiAIStudioTierOptions"
                    data-testid="create-gemini-tier-select"
                  />
                  <p class="input-hint">{{ t('admin.providers.gemini.tier.aiStudioHint') }}</p>
                </div>
              </div>
            </template>

            <!-- Bedrock supports SigV4 and API Key authentication. -->
            <template v-if="isBedrockCategory">
              <div>
                <span class="input-label">{{ t('admin.providers.bedrockAuthMode') }}</span>
                <SettingsSegmented
                  v-model="bedrockAuthMode"
                  :ariaLabel="t('admin.providers.bedrockAuthMode')"
                  :options="bedrockAuthModeOptions"
                />
              </div>
              <div v-if="bedrockAuthMode === 'sigv4'" class="grid gap-4 md:grid-cols-2">
                <div class="md:col-span-2" data-provider-field="bedrock-access-key-id">
                  <label for="create-bedrock-access-key-id" class="input-label">{{
                    t('admin.providers.bedrockAccessKeyId')
                  }}</label>
                  <input
                    id="create-bedrock-access-key-id"
                    v-model="bedrockAccessKeyId"
                    type="text"
                    required
                    class="input font-mono"
                    placeholder="AKIA..."
                  />
                </div>
                <div data-provider-field="bedrock-secret">
                  <label for="create-bedrock-secret" class="input-label">{{
                    t('admin.providers.bedrockSecretAccessKey')
                  }}</label>
                  <input
                    id="create-bedrock-secret"
                    v-model="bedrockSecretAccessKey"
                    type="password"
                    required
                    class="input font-mono"
                  />
                </div>
                <div>
                  <label for="create-bedrock-session-token" class="input-label">{{
                    t('admin.providers.bedrockSessionToken')
                  }}</label>
                  <input
                    id="create-bedrock-session-token"
                    v-model="bedrockSessionToken"
                    type="password"
                    class="input font-mono"
                  />
                  <p class="input-hint">{{ t('admin.providers.bedrockSessionTokenHint') }}</p>
                </div>
              </div>
              <div v-else v-content-reveal data-provider-field="bedrock-api-key">
                <label for="create-bedrock-api-key" class="input-label">{{
                  t('admin.providers.bedrockApiKeyInput')
                }}</label>
                <input
                  id="create-bedrock-api-key"
                  v-model="bedrockApiKeyValue"
                  type="password"
                  required
                  class="input font-mono"
                />
              </div>
              <div>
                <label for="create-bedrock-region" class="input-label">{{
                  t('admin.providers.bedrockRegion')
                }}</label>
                <Select
                  id="create-bedrock-region"
                  v-model="bedrockRegion"
                  :options="bedrockRegionOptions"
                  searchable
                />
                <p class="input-hint">{{ t('admin.providers.bedrockRegionHint') }}</p>
              </div>
              <SettingToggleRow
                id="create-bedrock-force-global"
                v-model="bedrockForceGlobal"
                :label="t('admin.providers.bedrockForceGlobal')"
                :hint="t('admin.providers.bedrockForceGlobalHint')"
              />
            </template>
          </SettingsSection>

          <SettingsSection :title="t('admin.providers.sections.groupsAndNetwork')">
            <GroupSelector
              v-model="form.group_ids"
              :groups="groups"
              data-tour="provider-form-groups"
            />
            <SettingToggleRow
              v-if="form.platform === 'antigravity'"
              id="create-provider-allow-overages"
              v-model="allowOverages"
              :label="t('admin.providers.allowOverages')"
              :help="t('admin.providers.allowOveragesTooltip')"
            />
            <div>
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
            :platform="isBedrockCategory ? 'anthropic' : form.platform"
            :sync-credentials="form.platform === 'qoder' ? undefined : syncPreviewCredentials"
            :source-placeholder="isBedrockCategory ? t('admin.providers.fromModel') : undefined"
            :target-placeholder="isBedrockCategory ? t('admin.providers.toModel') : undefined"
            @update:allowed-models="setAllowedModels"
            @synced="storeModelProtocolSuggestions"
            @add="touchQoderModelRestriction"
            @remove="touchQoderModelRestriction"
          />

          <OpenCodeFields
            v-if="form.platform === 'opencode'"
            v-model="openCodeProtocols"
            :mode="providerMode"
            :protocols="upstreamProtocols"
          />

          <!-- Antigravity whitelist and mappings separately control final model admission and request rewriting. -->
          <SettingsSection
            v-if="form.platform === 'antigravity'"
            :title="t('admin.providers.modelRestriction')"
            :hint="t('admin.providers.selectAllowedModels')"
          >
            <ModelWhitelistSelector v-model="antigravityWhitelistModels" platform="antigravity" />
            <ProviderModelMappingEditor v-model="antigravityModelMappings" wildcard-validation />
          </SettingsSection>
        </template>

        <template #scheduling>
          <SettingsSection :title="t('admin.providers.sections.scheduling')">
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="create-provider-concurrency" class="input-label">{{
                  t('admin.providers.concurrency')
                }}</label>
                <input
                  id="create-provider-concurrency"
                  v-model.number="form.concurrency"
                  type="number"
                  min="1"
                  class="input"
                  @input="form.concurrency = Math.max(1, form.concurrency || 1)"
                />
              </div>
              <div>
                <label for="create-provider-load-factor" class="input-label">{{
                  t('admin.providers.loadFactor')
                }}</label>
                <input
                  id="create-provider-load-factor"
                  v-model.number="form.load_factor"
                  type="number"
                  min="1"
                  class="input"
                  :placeholder="String(form.concurrency || 1)"
                  @input="
                    form.load_factor =
                      form.load_factor && form.load_factor >= 1 ? form.load_factor : null
                  "
                />
                <p class="input-hint">{{ t('admin.providers.loadFactorHint') }}</p>
              </div>
              <div>
                <label for="create-provider-priority" class="input-label">{{
                  t('admin.providers.priority')
                }}</label>
                <input
                  id="create-provider-priority"
                  v-model.number="form.priority"
                  type="number"
                  min="1"
                  class="input"
                  data-tour="provider-form-priority"
                />
                <p class="input-hint">{{ t('admin.providers.priorityHint') }}</p>
              </div>
              <div>
                <label for="create-provider-rate-multiplier" class="input-label">{{
                  t('admin.providers.billingRateMultiplier')
                }}</label>
                <input
                  id="create-provider-rate-multiplier"
                  v-model.number="form.rate_multiplier"
                  type="number"
                  min="0"
                  step="0.001"
                  class="input"
                />
                <p class="input-hint">{{ t('admin.providers.billingRateMultiplierHint') }}</p>
              </div>
            </div>
          </SettingsSection>

          <TempUnschedFields
            v-model:enabled="tempUnschedEnabled"
            v-model:rules="tempUnschedRules"
          />

          <PoolModeFields
            v-if="isApiKeyCredentials || isBedrockCategory"
            v-model:enabled="poolModeEnabled"
            v-model:retry-count="poolModeRetryCount"
            v-model:retry-status-codes="poolModeRetryStatusCodesInput"
          />

          <CustomErrorCodesFields
            v-if="isApiKeyCredentials"
            v-model:enabled="customErrorCodesEnabled"
            v-model:codes="selectedErrorCodes"
          />

          <SettingsSection :title="t('admin.providers.sections.autoPause')">
            <SettingToggleRow
              v-if="form.platform === 'anthropic' || form.platform === 'antigravity'"
              id="create-provider-intercept-warmup"
              v-model="interceptWarmupRequests"
              :label="t('admin.providers.interceptWarmupRequests')"
              :hint="t('admin.providers.interceptWarmupRequestsDesc')"
            />
            <SettingToggleRow
              id="create-provider-auto-pause-expired"
              v-model="autoPauseOnExpired"
              :label="t('admin.providers.autoPauseOnExpired')"
              :hint="t('admin.providers.autoPauseOnExpiredDesc')"
            />
            <template v-if="isOpenAIOAuthCategory">
              <SettingToggleRow
                id="create-provider-auto-pause-5h-disabled"
                v-model="autoPause5hDisabled"
                :label="t('admin.providers.autoPause5hDisabled')"
                :hint="t('admin.providers.autoPauseDisabledHint')"
                testid="create-auto-pause-5h-disabled"
              />
              <SettingToggleRow
                id="create-provider-auto-pause-7d-disabled"
                v-model="autoPause7dDisabled"
                :label="t('admin.providers.autoPause7dDisabled')"
                :hint="t('admin.providers.autoPauseDisabledHint')"
                testid="create-auto-pause-7d-disabled"
              />
              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="create-provider-auto-pause-5h" class="input-label">{{
                    t('admin.providers.autoPause5hThreshold')
                  }}</label>
                  <input
                    id="create-provider-auto-pause-5h"
                    v-model.number="autoPause5hThreshold"
                    type="number"
                    min="0"
                    max="100"
                    step="0.1"
                    class="input"
                    :disabled="autoPause5hDisabled"
                    data-testid="create-auto-pause-5h-threshold"
                  />
                  <p class="input-hint">{{ t('admin.providers.autoPauseThresholdHint') }}</p>
                </div>
                <div>
                  <label for="create-provider-auto-pause-7d" class="input-label">{{
                    t('admin.providers.autoPause7dThreshold')
                  }}</label>
                  <input
                    id="create-provider-auto-pause-7d"
                    v-model.number="autoPause7dThreshold"
                    type="number"
                    min="0"
                    max="100"
                    step="0.1"
                    class="input"
                    :disabled="autoPause7dDisabled"
                    data-testid="create-auto-pause-7d-threshold"
                  />
                  <p class="input-hint">{{ t('admin.providers.autoPauseThresholdHint') }}</p>
                </div>
              </div>
            </template>
          </SettingsSection>
        </template>

        <template #quota>
          <QuotaLimitFields
            v-if="form.type === 'apikey' || form.type === 'bedrock'"
            :limits="quotaLimits"
            :notify="quotaNotifyState"
            :notify-global-enabled="quotaNotifyGlobalEnabled"
            :hint="
              form.platform === 'anthropic'
                ? t('admin.providers.quotaControl.hint')
                : t('admin.providers.quotaLimitHint')
            "
            @update:limit="setQuotaLimit"
            @update:notify="setQuotaNotifyField"
          />

          <AnthropicOAuthLimitFields
            v-if="isAnthropicOAuthCategory"
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
            v-if="form.type === 'apikey'"
            v-model="balanceAlert"
            :supported="balanceAlertSupported"
            :query-enabled="upstreamUsageEnabled"
          />
        </template>

        <template #request>
          <ProviderProtocolSelector
            v-model="upstreamProtocols"
            v-model:image-modes="imageModes"
            :platform="form.platform"
            :type="form.type"
            :auth-mode="
              oauthFlowRef?.inputMethod === 'codex_pat'
                ? 'personalAccessToken'
                : oauthFlowRef?.inputMethod === 'agent_identity'
                  ? 'agentIdentity'
                  : ''
            "
          />

          <SettingsSection
            v-if="isApiKeyCredentials && endpointSlots.length > 0"
            :title="t('admin.providers.cnProviders.apiProtocol.endpoints')"
          >
            <div v-for="item in endpointSlots" :key="item.slot">
              <label :for="`create-provider-endpoint-${item.slot}`" class="input-label">
                {{ item.label }}
              </label>
              <input
                :id="`create-provider-endpoint-${item.slot}`"
                v-model="adaptiveBaseUrls[item.slot]"
                type="text"
                class="input"
                :placeholder="item.placeholder"
                :data-testid="`provider-endpoint-${item.slot}`"
              />
            </div>
            <p v-if="isCNPlatform && !cnSupportsNativeResponses(form.platform)" class="input-hint">
              {{ t('admin.providers.cnProviders.apiProtocol.responsesFallbackDesc') }}
            </p>
          </SettingsSection>

          <SettingsSection :title="t('admin.providers.sections.requestHeaders')">
            <UpstreamRequestIdHeaderField
              v-model="upstreamRequestIdHeader"
              :platform="form.platform"
              :type="form.type"
            />
          </SettingsSection>

          <HeaderOverrideFields
            v-if="showHeaderOverride"
            v-model:enabled="headerOverrideEnabled"
            v-model:rows="headerOverrideRows"
            data-provider-field="header-override"
          />

          <!-- Grok custom URLs control request forwarding; authorization and refresh retain their own endpoints. -->
          <SettingsSection
            v-if="form.platform === 'grok' && isOAuthFlow"
            :title="t('admin.providers.sections.grok')"
          >
            <SettingToggleRow
              id="create-grok-custom-base-url"
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
          </SettingsSection>

          <template v-if="form.platform === 'openai'">
            <SettingsSection :title="t('admin.providers.sections.openaiCompatibility')">
              <SettingToggleRow
                id="create-openai-passthrough"
                v-model="openaiPassthroughEnabled"
                :label="t('admin.providers.openai.oauthPassthrough')"
                :hint="t('admin.providers.openai.oauthPassthroughDesc')"
              />
              <SettingToggleRow
                v-if="form.type === 'oauth'"
                id="create-openai-flatten-namespaces"
                v-model="openaiFlattenNamespacesEnabled"
                :label="t('admin.providers.openai.flattenNamespaces')"
                :hint="t('admin.providers.openai.flattenNamespacesDesc')"
                testid="create-openai-flatten-namespaces-toggle"
              />
              <SettingToggleRow
                v-if="isOpenAIOAuthCategory || providerCategory === 'apikey'"
                id="create-openai-images-gif"
                v-model="openAIImagesGIFEnabled"
                :label="t('admin.providers.openai.imagesGIFEnabled')"
                :hint="t('admin.providers.openai.imagesGIFEnabledDesc')"
              />
              <template v-if="providerCategory === 'apikey'">
                <SettingToggleRow
                  id="create-openai-continuation-supported"
                  v-model="openAIResponsesContinuationSupported"
                  :label="t('admin.providers.openai.responsesContinuationSupported')"
                  :hint="t('admin.providers.openai.responsesContinuationSupportedDesc')"
                  testid="create-openai-continuation-supported"
                />
                <SettingToggleRow
                  id="create-openai-images-url-to-b64-json"
                  v-model="openAIImagesURLToB64JSON"
                  :label="t('admin.providers.openai.imagesURLToB64JSON')"
                  :hint="t('admin.providers.openai.imagesURLToB64JSONDesc')"
                  testid="create-openai-images-url-to-b64-json"
                />
              </template>
            </SettingsSection>

            <SettingsSection
              v-if="isOpenAIOAuthCategory"
              :title="t('admin.providers.sections.openaiClient')"
            >
              <SettingRow
                id="create-openai-client-policy"
                label-for="create-openai-client-policy-select"
                :label="t('admin.providers.openai.clientPolicy')"
                :hint="t('admin.providers.openai.clientPolicyDesc')"
                field
              >
                <Select
                  id="create-openai-client-policy-select"
                  v-model="openAIOAuthClientPolicy"
                  :options="openAIOAuthClientPolicyOptions"
                />
              </SettingRow>
              <Collapse :open="openAIOAuthClientPolicy === 'codex_only'" unmount-on-hide>
                <SettingsSubpanel>
                  <SettingToggleRow
                    id="create-openai-codex-allow-claude-code"
                    v-model="codexCLIOnlyAllowClaudeCodeEnabled"
                    :label="t('admin.providers.openai.codexCLIOnlyAllowClaudeCode')"
                    :hint="t('admin.providers.openai.codexCLIOnlyAllowClaudeCodeDesc')"
                  />
                </SettingsSubpanel>
              </Collapse>
              <SettingRow
                id="create-codex-fingerprint-mode"
                label-for="create-codex-fingerprint-mode-select"
                :label="t('admin.providers.openai.codexFingerprintMode')"
                :hint="t('admin.providers.openai.codexFingerprintModeDesc')"
                field
              >
                <Select
                  id="create-codex-fingerprint-mode-select"
                  v-model="codexFingerprintMode"
                  data-testid="create-codex-fingerprint-mode-select"
                  :options="codexFingerprintModeOptions"
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection v-if="isOpenAIOAuthCategory">
              <SettingRow
                id="create-image-responses-model"
                label-for="create-image-responses-model-input"
                :label="t('admin.providers.openai.imageResponsesModel')"
                :hint="t('admin.providers.openai.imageResponsesModelDesc')"
                field
              >
                <input
                  id="create-image-responses-model-input"
                  v-model="codexImageResponsesModel"
                  data-testid="create-image-responses-model-input"
                  type="text"
                  class="input"
                  :placeholder="t('admin.providers.openai.imageResponsesModelDefault')"
                />
              </SettingRow>
            </SettingsSection>

            <ResponsesWSConnectionModeSelector
              v-if="upstreamProtocols?.includes('openai_responses_websocket')"
              v-model="responsesWSConnectionMode"
              test-id-prefix="create-openai-ws-connection"
            />

            <SettingsSection
              v-if="isOpenAIOAuthCategory || providerCategory === 'apikey'"
              :title="t('admin.providers.sections.compaction')"
            >
              <OpenAICompactionToggle
                v-model="openAINativeCompactionV2Mode"
                test-id="create-openai-native-compaction-v2-mode"
                :label="t('admin.providers.openai.nativeCompactV2Mode')"
                :hint="t('admin.providers.openai.nativeCompactV2ModeDesc')"
              />
              <OpenAICompactionToggle
                v-model="openAICompactMode"
                test-id="create-openai-compact-mode"
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
            v-if="form.platform === 'anthropic' && providerCategory === 'apikey'"
            :title="t('admin.providers.sections.anthropicCompatibility')"
          >
            <SettingToggleRow
              id="create-anthropic-passthrough"
              v-model="anthropicPassthroughEnabled"
              :label="t('admin.providers.anthropic.apiKeyPassthrough')"
              :hint="t('admin.providers.anthropic.apiKeyPassthroughDesc')"
            />
            <SettingRow
              id="create-anthropic-auth-scheme"
              label-for="create-anthropic-auth-scheme-select"
              :label="t('admin.providers.anthropic.apiKeyAuthScheme')"
              :hint="t('admin.providers.anthropic.apiKeyAuthSchemeDesc')"
              field
            >
              <Select
                id="create-anthropic-auth-scheme-select"
                v-model="anthropicAPIKeyAuthScheme"
                :options="anthropicAPIKeyAuthSchemeOptions"
              />
            </SettingRow>
            <!-- Provider overrides are hidden when web-search emulation is globally disabled. -->
            <SettingRow
              v-if="webSearchGlobalEnabled"
              id="create-anthropic-web-search"
              label-for="create-anthropic-web-search-select"
              :label="t('admin.providers.anthropic.webSearchEmulation')"
              :hint="t('admin.providers.anthropic.webSearchEmulationDesc')"
              field
            >
              <Select
                id="create-anthropic-web-search-select"
                v-model="webSearchEmulationMode"
                :options="webSearchEmulationOptions"
              />
            </SettingRow>
          </SettingsSection>

          <TLSFingerprintFields
            v-if="tlsFingerprintTestIdPrefix !== null"
            v-model:enabled="tlsFingerprintEnabled"
            v-model:profile-id="tlsFingerprintProfileId"
            v-model:router-id="tlsFingerprintRouterId"
            :profile-options="tlsFingerprintProfileOptions"
            :router-options="isOpenAIOAuthCategory ? tlsFingerprintRouterOptions : undefined"
            :test-id-prefix="tlsFingerprintTestIdPrefix || undefined"
          />

          <AnthropicOAuthRequestFields
            v-if="isAnthropicOAuthCategory"
            v-model:session-id-masking-enabled="sessionIdMaskingEnabled"
            v-model:cache-ttl-enabled="cacheTTLOverrideEnabled"
            v-model:cache-ttl-target="cacheTTLOverrideTarget"
            v-model:custom-base-url-enabled="customBaseUrlEnabled"
            v-model:custom-base-url="customBaseUrl"
          />
        </template>
      </SettingsTabs>
    </form>

    <!-- Step two completes OAuth authorization. -->
    <div v-else class="min-h-0 flex-1 space-y-5 overflow-y-auto overscroll-contain">
      <OAuthAuthorizationFlow
        ref="oauthFlowRef"
        :add-method="form.platform === 'anthropic' ? addMethod : 'oauth'"
        :auth-url="currentAuthUrl"
        :session-id="currentSessionId"
        :loading="currentOAuthLoading"
        :error="currentOAuthError"
        :show-help="form.platform === 'anthropic'"
        :show-proxy-warning="
          form.platform !== 'openai' && form.platform !== 'grok' && !!form.proxy_id
        "
        :allow-multiple="form.platform === 'anthropic'"
        :show-cookie-option="form.platform === 'anthropic'"
        :show-refresh-token-option="
          form.platform === 'openai' || form.platform === 'antigravity' || form.platform === 'grok'
        "
        :show-mobile-refresh-token-option="form.platform === 'openai'"
        :show-session-token-option="false"
        :show-access-token-option="false"
        :show-codex-session-import-option="form.platform === 'openai'"
        :show-agent-identity-option="form.platform === 'openai'"
        :show-codex-pat-option="form.platform === 'openai'"
        :show-sso-option="form.platform === 'grok'"
        :show-email-password-option="false"
        :show-manual-option="true"
        :initial-input-method="'manual'"
        :platform="form.platform"
        :show-project-id="geminiOAuthType === 'code_assist'"
        :auth-sessions="currentOpenAIAuthSessions"
        @generate-url="handleGenerateUrl"
        @remove-auth-session="handleRemoveOpenAIAuthSession"
        @cookie-auth="handleCookieAuth"
        @validate-refresh-token="handleValidateRefreshToken"
        @validate-mobile-refresh-token="handleOpenAIValidateMobileRT"
        @validate-session-token="handleValidateSessionToken"
        @import-codex-session="handleOpenAIImportCodexSession"
        @import-codex-pat="handleOpenAIImportCodexPAT"
        @import-sso="handleGrokImportSSO"
      />
    </div>

    <template #footer>
      <div v-if="step === 1" v-content-reveal class="flex justify-end gap-3">
        <button @click="handleClose" type="button" class="btn btn-secondary">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="create-provider-form"
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
          {{
            isOAuthFlow
              ? t('common.next')
              : submitting
                ? t('admin.providers.creating')
                : t('common.create')
          }}
        </button>
      </div>
      <div v-else class="flex justify-between gap-3">
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="submitting || isQoderOAuthProviderCreating"
          @click="goBackToBasicInfo"
        >
          {{ t('common.back') }}
        </button>
        <button
          v-if="isManualInputMethod"
          type="button"
          :disabled="!canExchangeCode"
          class="btn btn-primary"
          @click="handleExchangeCode"
        >
          <Icon
            name="loader"
            size="sm"
            :animate-on-hover="false"
            v-if="currentOAuthLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
          />
          {{
            currentOAuthLoading
              ? t('admin.providers.oauth.verifying')
              : t('admin.providers.oauth.completeAuth')
          }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <!-- Gemini Help Dialog -->
  <BaseDialog
    :show="showGeminiHelpDialog"
    :title="t('admin.providers.gemini.helpDialog.title')"
    width="wide"
    @close="showGeminiHelpDialog = false"
  >
    <div class="space-y-6">
      <!-- Setup Guide Section -->
      <div>
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.providers.gemini.setupGuide.title') }}
        </h3>
        <div class="space-y-4">
          <div>
            <p class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.providers.gemini.setupGuide.checklistTitle') }}
            </p>
            <ul class="list-inside list-disc space-y-1 text-sm text-gray-600 dark:text-gray-400">
              <li>{{ t('admin.providers.gemini.setupGuide.checklistItems.usIp') }}</li>
              <li>{{ t('admin.providers.gemini.setupGuide.checklistItems.age') }}</li>
            </ul>
          </div>
          <div>
            <p class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.providers.gemini.setupGuide.activationTitle') }}
            </p>
            <ul class="list-inside list-disc space-y-1 text-sm text-gray-600 dark:text-gray-400">
              <li>{{ t('admin.providers.gemini.setupGuide.activationItems.geminiWeb') }}</li>
              <li>{{ t('admin.providers.gemini.setupGuide.activationItems.gcpProject') }}</li>
            </ul>
            <div class="mt-2 flex flex-wrap gap-2">
              <a
                href="https://policies.google.com/terms"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-link"
              >
                {{ t('admin.providers.gemini.setupGuide.links.countryCheck') }}
              </a>
              <span class="text-gray-400">·</span>
              <a
                href="https://policies.google.com/country-association-form"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-link"
              >
                修改归属地
              </a>
              <span class="text-gray-400">·</span>
              <a
                href="https://gemini.google.com/gems/create?hl=en-US&pli=1"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-link"
              >
                {{ t('admin.providers.gemini.setupGuide.links.geminiWebActivation') }}
              </a>
              <span class="text-gray-400">·</span>
              <a
                href="https://console.cloud.google.com"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-link"
              >
                {{ t('admin.providers.gemini.setupGuide.links.gcpProject') }}
              </a>
            </div>
          </div>
        </div>
      </div>

      <!-- Quota Policy Section -->
      <div class="border-t border-gray-200 pt-6 dark:border-dark-600">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.providers.gemini.quotaPolicy.title') }}
        </h3>
        <p class="mb-4 text-xs text-amber-600 dark:text-amber-400">
          {{ t('admin.providers.gemini.quotaPolicy.note') }}
        </p>
        <div class="overflow-x-auto">
          <table class="w-full text-xs">
            <thead class="bg-gray-50 dark:bg-dark-600">
              <tr>
                <th class="px-3 py-2 text-left font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.providers.gemini.quotaPolicy.columns.channel') }}
                </th>
                <th class="px-3 py-2 text-left font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.providers.gemini.quotaPolicy.columns.provider') }}
                </th>
                <th class="px-3 py-2 text-left font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.providers.gemini.quotaPolicy.columns.limits') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 dark:divide-dark-600">
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.googleOne.channel') }}
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Free</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.googleOne.limitsFree') }}
                </td>
              </tr>
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white"></td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Pro</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.googleOne.limitsPro') }}
                </td>
              </tr>
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white"></td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Ultra</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.googleOne.limitsUltra') }}
                </td>
              </tr>
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.gcp.channel') }}
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Standard</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.gcp.limitsStandard') }}
                </td>
              </tr>
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white"></td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Enterprise</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.gcp.limitsEnterprise') }}
                </td>
              </tr>
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.aiStudio.channel') }}
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Free</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsFree') }}
                </td>
              </tr>
              <tr>
                <td class="px-3 py-2 text-gray-900 dark:text-white"></td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">Paid</td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                  {{ t('admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsPaid') }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="mt-4 flex flex-wrap gap-3">
          <a
            :href="geminiQuotaDocs.codeAssist"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-link"
          >
            {{ t('admin.providers.gemini.quotaPolicy.docs.codeAssist') }}
          </a>
          <a
            :href="geminiQuotaDocs.aiStudio"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-link"
          >
            {{ t('admin.providers.gemini.quotaPolicy.docs.aiStudio') }}
          </a>
          <a
            :href="geminiQuotaDocs.vertex"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-link"
          >
            {{ t('admin.providers.gemini.quotaPolicy.docs.vertex') }}
          </a>
        </div>
      </div>

      <!-- API Key Links Section -->
      <div class="border-t border-gray-200 pt-6 dark:border-dark-600">
        <h3 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('admin.providers.gemini.helpDialog.apiKeySection') }}
        </h3>
        <div class="flex flex-wrap gap-3">
          <a
            :href="geminiHelpLinks.apiKey"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-link"
          >
            {{ t('admin.providers.gemini.providerType.apiKeyLink') }}
          </a>
          <a
            :href="geminiHelpLinks.aiStudioPricing"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-link"
          >
            {{ t('admin.providers.gemini.providerType.quotaLink') }}
          </a>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button @click="showGeminiHelpDialog = false" type="button" class="btn btn-primary">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import Collapse from '@/components/common/Collapse.vue'

import {
  buildOpenAICreateExtra,
  buildQoderCreateExtra,
  buildAnthropicCreateExtra,
} from './providerCreatePayload'
import OpenCodeFields from '@/components/provider/form/OpenCodeFields.vue'
import { openCodeDraft, openCodeCredentials } from '@/components/provider/form/openCodeDraft'
import ProviderProtocolSelector from '../../../../components/provider/ProviderProtocolSelector.vue'
import { readImageStreamModes } from '@/components/provider/form/imageStreamModes'
import {
  loadProtocolCatalog,
  nativeProtocolOptions,
  protocolName,
} from '@/api/admin/protocolCapabilities'
import type { ProtocolID } from '@/api/protocols.types'
import OpenAICompactionToggle from '../../../../components/provider/OpenAICompactionToggle.vue'
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  buildModelMappingObject,
  buildPersistedModelRestriction,
  fetchAntigravityDefaultMappings,
} from '@/composables/useModelWhitelist'
import { adminAPI } from '@/api/admin'
import { useQuotaNotifyState } from '@/composables/useQuotaNotifyState'
import {
  useProviderOAuth,
  type AddMethod,
  type AuthInputMethod,
} from '@/composables/useProviderOAuth'
import { useOpenAICreateAuthorization } from './useOpenAICreateAuthorization'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'
import { useQoderCreateAuthorization } from './useQoderCreateAuthorization'
import type { QoderSite } from '@/api/admin/qoder'
import { useGrokOAuth } from '@/composables/useGrokOAuth'
import type { Proxy } from '@/api/admin/proxies.types'
import type { AdminGroup } from '@/api/groups.types'
import type {
  ProviderPlatform,
  ProviderType,
  CreateProviderRequest,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
  UpstreamUsageAdapter,
} from '@/api/admin/providers.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import UpstreamRequestIdHeaderField from '@/components/provider/UpstreamRequestIdHeaderField.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import ProviderModelMappingEditor from '@/components/provider/ProviderModelMappingEditor.vue'
import type { TempUnschedRuleForm } from '@/components/provider/TempUnschedRulesEditor.vue'
import type { ModelMappingRow } from '@/utils/modelMappingRules'
import ProxySelector from '@/components/admin/proxy/ProxySelector.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ModelWhitelistSelector from '@/components/provider/ModelWhitelistSelector.vue'
import ResponsesWSConnectionModeSelector from '@/components/provider/ResponsesWSConnectionModeSelector.vue'
import GrokBaseUrlPresets from '@/components/provider/GrokBaseUrlPresets.vue'
import CnBaseUrlPresets from '@/components/provider/CnBaseUrlPresets.vue'
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
import ProviderChoiceCard from '@/components/provider/form/ProviderChoiceCard.vue'
import QuotaLimitFields from '@/components/provider/form/QuotaLimitFields.vue'
import TempUnschedFields from '@/components/provider/form/TempUnschedFields.vue'
import TLSFingerprintFields from '@/components/provider/form/TLSFingerprintFields.vue'
import { bindQuotaLimits } from '@/components/provider/form/quotaLimit'
import {
  DEFAULT_POOL_MODE_RETRY_COUNT,
  normalizePoolModeRetryCount,
  parsePoolModeRetryStatusCodes,
} from '@/components/provider/form/poolMode'
import {
  useAnthropicAPIKeyAuthSchemeOptions,
  useCodexFingerprintModeOptions,
  useOpenAIOAuthClientPolicyOptions,
  useWebSearchEmulationOptions,
  type AnthropicAPIKeyAuthScheme,
  type CodexFingerprintMode,
  type RpmStrategy,
} from '@/components/provider/form/providerFormOptions'
import { vSegmented } from '@/directives/segmented'
import { platformIconClass, platformLabel, platformTextClass } from '@/utils/platformColors'
import { resolveUpstreamUsageConfig } from '@/utils/upstreamUsage'
import type { SyncUpstreamModelsResult } from '@/api/admin/providers'
import {
  applyAntigravityProjectID,
  applyInterceptWarmup,
} from '@/components/provider/credentialsBuilder'
import {
  applyHeaderOverride,
  isHeaderOverrideCapable,
  validateHeaderOverrideRows,
  type HeaderOverrideRow,
} from '@/components/provider/form/headerOverrides'
import {
  apiKeyPreset,
  cnSupportsNativeResponses,
  defaultCNAdaptiveBaseUrls,
  defaultCNBaseUrl,
  derivedEndpointOverrides,
  derivedEndpointSlots,
  ENDPOINT_SLOT_PROTOCOLS,
  providerModeChoices,
  type CnApiProtocol,
  type CnNativeApiProtocol,
  type CnProviderMode,
  type EndpointSlotAddresses,
  type ProviderMode,
} from '@/components/provider/form/providerEndpoints'
import { formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import {
  BEDROCK_REGION_OPTIONS,
  VERTEX_LOCATION_OPTIONS,
  groupedProviderSelectOptions,
} from '@/constants/provider'
import { RESPONSES_WS_POOLED, type ResponsesWSConnectionMode } from '@/utils/responsesWsConnection'
import {
  buildUpstreamBalanceAlert,
  defaultUpstreamBalanceAlert,
  supportsUpstreamBalanceAlert,
} from '@/utils/upstreamBalanceAlert'
import OAuthAuthorizationFlow from '../../../../components/provider/OAuthAuthorizationFlow.vue'

// CreateProviderModal.vue collects administrator input and creates providers with
// platform-specific credentials, protocols, and routing settings. API Key forms
// submit their configuration directly. OAuth forms collect authorization input
// through the embedded flow and pass resulting token data into the selected
// platform's creation path.
//
// Form drafts combine shared fields with platform-specific options before
// submission. The upstream request-ID field stores a response-header override in
// provider Extra. A blank override omits that key, allowing the backend default
// to apply across request diagnostics and usage recording. The dialog reports
// completed creation through its event callbacks.

// upstreamProtocols distinguishes an untouched selection from an explicitly
// empty native set; submission filters either against the loaded catalog.
const upstreamProtocols = ref<ProtocolID[] | undefined>(undefined)
// imageModes owns separate endpoint capabilities until the create dialog is reset.
const imageModes = ref(readImageStreamModes())
// OAuthFlowExposed represents public authorization input after Vue unwraps
// exposed refs.
interface OAuthFlowExposed {
  // Authorization draft fields are unwrapped by Vue and read from the mounted
  // child only; reset clears its editable input when the parent leaves authorization.
  authCode: string // pasted authorization input
  oauthState: string // pasted callback state
  projectId: string // selected Google project
  sessionKey: string // pasted session keys
  refreshToken: string // pasted refresh tokens
  sessionToken: string // retained session input
  codexSession: string // pasted session document
  codexPAT: string // personal access token
  ssoCookie: string // pasted Grok cookies
  inputMethod: AuthInputMethod // selected authorization method
  reset: () => void // clears child drafts
}

// Localization resolves visible labels at render time; option computations below
// follow the active language without storing translated form values.
const { t } = useI18n()

// Authorization headings and field hints follow the selected platform; these
// derived labels do not alter credential drafts when they recompute.
const oauthStepTitle = computed(() => {
  if (form.platform === 'openai') return t('admin.providers.oauth.openai.title')
  if (form.platform === 'gemini') return t('admin.providers.oauth.gemini.title')
  if (form.platform === 'antigravity') return t('admin.providers.oauth.antigravity.title')
  if (form.platform === 'qoder') return t('admin.providers.oauth.qoder.title')
  if (form.platform === 'grok') return t('admin.providers.oauth.grok.title')
  return t('admin.providers.oauth.title')
})

// upstreamRequestIdHeader selects the upstream response header captured in request
// diagnostics and usage records; a blank field uses the backend X-Request-ID default.
const upstreamRequestIdHeader = ref('')
// codexImageResponsesModel selects an OAuth image driver; blank uses the backend default.
const codexImageResponsesModel = ref('')
// withUpstreamRequestIdHeader applies the trimmed diagnostic header override to
// optional metadata. Blank input preserves the supplied base unchanged,
// allowing the backend default header to remain effective for the created
// provider.
const withUpstreamRequestIdHeader = <T extends Record<string, unknown> | undefined>(
  extra: T,
): T | Record<string, unknown> => {
  const name = upstreamRequestIdHeader.value.trim()
  if (!name) return extra
  return { ...(extra || {}), upstream_request_id_header: name }
}

// baseUrlHint describes the selected platform's address and Gemini connection source.
// apiKeyHint supplies credential guidance for Anthropic, OpenAI, and Gemini.
const baseUrlHint = computed(() => {
  if (form.platform === 'gemini' && geminiProviderType.value === 'third_party') {
    return t('admin.providers.gemini.connectionSource.thirdPartyBaseUrlHint')
  }
  const hintKey = platformApiKeyPreset.value.baseUrlHintKey
  return hintKey ? t(hintKey) : ''
})

const apiKeyHint = computed(() => {
  if (form.platform === 'openai') return t('admin.providers.openai.apiKeyHint')
  if (form.platform === 'gemini' && geminiProviderType.value === 'third_party') {
    return t('admin.providers.gemini.connectionSource.thirdPartyApiKeyHint')
  }
  if (form.platform === 'gemini') return t('admin.providers.gemini.apiKeyHint')
  if (form.platform === 'anthropic') return t('admin.providers.apiKeyHint')
  return ''
})

// Gemini tier and source options keep submitted identifiers stable while
// translating only their visible labels for the current language.
const geminiGoogleOneTierOptions = computed(() => [
  { value: 'google_one_free', label: t('admin.providers.gemini.tier.googleOne.free') },
  { value: 'google_ai_pro', label: t('admin.providers.gemini.tier.googleOne.pro') },
  { value: 'google_ai_ultra', label: t('admin.providers.gemini.tier.googleOne.ultra') },
])

const geminiGCPTierOptions = computed(() => [
  { value: 'gcp_standard', label: t('admin.providers.gemini.tier.gcp.standard') },
  { value: 'gcp_enterprise', label: t('admin.providers.gemini.tier.gcp.enterprise') },
])

const geminiAIStudioTierOptions = computed(() => [
  { value: 'aistudio_free', label: t('admin.providers.gemini.tier.aiStudio.free') },
  { value: 'aistudio_paid', label: t('admin.providers.gemini.tier.aiStudio.paid') },
])

const geminiProviderTypeOptions = computed(() => [
  { value: 'official', label: t('admin.providers.gemini.connectionSource.official') },
  { value: 'third_party', label: t('admin.providers.gemini.connectionSource.thirdParty') },
])

const geminiProviderTypeHint = computed(() =>
  geminiProviderType.value === 'third_party'
    ? t('admin.providers.gemini.connectionSource.thirdPartyHint')
    : t('admin.providers.gemini.connectionSource.officialHint'),
)
// isGeminiThirdPartyBaseUrl reports whether a nonempty URL names a nonofficial
// Gemini host. Invalid URLs return false so switching sources clears unsuitable
// drafts and submission can request a valid third-party endpoint.
const isGeminiThirdPartyBaseUrl = (value: string) => {
  const normalized = value.trim()
  if (!normalized) return false
  try {
    return new URL(normalized).hostname.toLowerCase() !== 'generativelanguage.googleapis.com'
  } catch {
    // Invalid URLs cannot establish a third-party endpoint, so source changes
    // clear them and submission keeps the endpoint field available for correction.
    return false
  }
}

// Cloud region choices come from shared provider constants so create and edit
// forms submit the same supported location identifiers.
const vertexLocationOptions = groupedProviderSelectOptions(VERTEX_LOCATION_OPTIONS)
const bedrockRegionOptions = groupedProviderSelectOptions(BEDROCK_REGION_OPTIONS)
// Props supplies dialog visibility and shared administrator choices for
// provider creation.
interface Props {
  show: boolean // parent-controlled dialog visibility
  // initialPlatform is applied when the dialog opens; omission selects Anthropic
  // through the same platform watcher used for explicit administrator selection.
  initialPlatform?: ProviderPlatform
  proxies: Proxy[] // available outbound proxies
  groups: AdminGroup[] // available routing groups
}

// Parent props control visibility and routing choices; completion events request
// list refresh and dismissal after the corresponding creation outcome.
const props = defineProps<Props>()
const emit = defineEmits<{
  close: [] // requests parent dismissal
  created: [] // requests provider-list refresh
}>()

// Application notifications display creation outcomes and field failures
// independently of the authorization panel's retained inline error text.
const appStore = useAppStore()

// Platform OAuth clients expose authorization progress and errors to this dialog and its child flow.
const oauth = useProviderOAuth() // Anthropic OAuth
const geminiOAuth = useGeminiOAuth() // Gemini OAuth
const antigravityOAuth = useAntigravityOAuth() // Antigravity OAuth
const grokOAuth = useGrokOAuth() // Grok OAuth
// Platform selection chooses the current authorization URL, session, progress, and error displayed by the shared OAuth child.
const currentAuthUrl = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.authUrl.value
  if (form.platform === 'gemini') return geminiOAuth.authUrl.value
  if (form.platform === 'antigravity') return antigravityOAuth.authUrl.value
  if (form.platform === 'qoder') return qoderOAuth.authUrl.value
  if (form.platform === 'grok') return grokOAuth.authUrl.value
  return oauth.authUrl.value
})

const currentSessionId = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.sessionId.value
  if (form.platform === 'gemini') return geminiOAuth.sessionId.value
  if (form.platform === 'antigravity') return antigravityOAuth.sessionId.value
  if (form.platform === 'qoder') return qoderOAuth.sessionId.value
  if (form.platform === 'grok') return grokOAuth.sessionId.value
  return oauth.sessionId.value
})

const currentOAuthLoading = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.loading.value
  if (form.platform === 'gemini') return geminiOAuth.loading.value
  if (form.platform === 'antigravity') return antigravityOAuth.loading.value
  if (form.platform === 'qoder') {
    return qoderOAuth.loading.value || submitting.value || isQoderOAuthProviderCreating.value
  }
  if (form.platform === 'grok') return grokOAuth.loading.value
  return oauth.loading.value
})

const currentOAuthError = computed(() => {
  if (form.platform === 'openai') return openaiOAuth.error.value
  if (form.platform === 'gemini') return geminiOAuth.error.value
  if (form.platform === 'antigravity') return antigravityOAuth.error.value
  if (form.platform === 'qoder') return qoderOAuth.error.value
  if (form.platform === 'grok') return grokOAuth.error.value
  return oauth.error.value
})

const currentOpenAIAuthSessions = computed(() =>
  form.platform === 'openai' ? openaiOAuth.authSessions.value : [],
)

// oauthFlowRef reads the exposed child draft after authorization mounts; null
// means the configuration step has no authorization child to inspect or reset.
const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

// Dialog progression and credential drafts belong to this mounted component;
// resetForm restores their defaults when the parent hides the creation dialog.
const step = ref(1)
const submitting = ref(false)
const providerCategory = ref<'oauth-based' | 'apikey' | 'bedrock' | 'service_account'>(
  'oauth-based',
) // UI selection for provider category
const addMethod = ref<AddMethod>('oauth') // For oauth-based: 'oauth' or 'setup-token'
const apiKeyBaseUrl = ref('https://api.anthropic.com')
const apiKeyValue = ref('')
const upstreamUsageEnabled = ref(true)
const balanceAlert = ref(defaultUpstreamBalanceAlert())
const upstreamUsageAdapter = ref<UpstreamUsageAdapter>('sub2api')
const upstreamUsageBaseUrl = ref('')
const upstreamUsageWalletAccessToken = ref('')
const upstreamUsageWalletUserId = ref('')

// OpenCode model protocol tables belong to this dialog and are saved with its credential payload.
const openCodeProtocols = ref(openCodeDraft())

// Provider mode selects the CN billing mode or OpenCode service; with the CN API protocol
// it also selects the default endpoints written into the credential draft.
const providerMode = ref<ProviderMode>('payg')
// modeChoices lists the provider modes offered by the selected platform; empty hides the selector.
const modeChoices = computed(() => providerModeChoices(form.platform))
// Zhipu organization and project drafts are submitted for Coding Plan providers when their trimmed values are nonempty.
const zhipuOrganization = ref('')
const zhipuProject = ref('')
// API protocol selects native forwarding endpoints independently of provider type.
// Adaptive mode retains per-protocol URLs; Responses is available for DeepSeek and Kimi.
// adaptiveBaseUrls also holds the optional address overrides of root-derived platforms.
const apiProtocol = ref<CnApiProtocol>('adaptive')
const adaptiveBaseUrls = ref<EndpointSlotAddresses>({
  chat_completions: '',
  anthropic: '',
  responses: '',
})
const isCNPlatform = computed(
  () => form.platform === 'kimi' || form.platform === 'zhipu' || form.platform === 'deepseek',
)
// The preset component accepts only CN platforms, so this computed value narrows the selected platform before template binding.
const cnPresetPlatform = computed<'kimi' | 'zhipu' | 'deepseek'>(() => {
  if (form.platform === 'kimi' || form.platform === 'zhipu' || form.platform === 'deepseek') {
    return form.platform
  }
  return 'kimi'
})
// CN protocol choices include native Responses only for platforms whose preset metadata declares that endpoint.
const cnAdaptiveProtocolOptions = computed<Array<{ value: CnNativeApiProtocol }>>(() => {
  const opts: Array<{ value: CnNativeApiProtocol }> = [
    { value: 'chat_completions' },
    { value: 'anthropic' },
  ]
  if (cnSupportsNativeResponses(form.platform)) opts.push({ value: 'responses' })
  return opts
})
// endpointSlots lists the per-protocol address inputs with catalog protocol names. CN
// adaptive mode edits every native slot prefilled from presets; root-derived platforms
// edit optional overrides whose placeholders follow the entered or official root.
const endpointSlots = computed(() => {
  const slots =
    isCNPlatform.value && apiProtocol.value === 'adaptive'
      ? cnAdaptiveProtocolOptions.value.map(({ value }) => ({ slot: value, placeholder: '' }))
      : derivedEndpointSlots(form.platform, apiKeyBaseUrl.value, providerMode.value)
  return slots.map((item) => ({
    ...item,
    label: protocolName(ENDPOINT_SLOT_PROTOCOLS[item.slot]),
  }))
})
// resetAdaptiveBaseUrls restores the CN endpoint family for the selected
// billing mode.
function resetAdaptiveBaseUrls(platform: 'kimi' | 'zhipu' | 'deepseek', mode: ProviderMode) {
  adaptiveBaseUrls.value = defaultCNAdaptiveBaseUrls(platform, mode)
}

// selectCNPlatform selects API Key authentication and resets CN endpoint drafts.
// The caller has already restricted the billing mode to the platform's choices, so
// adaptive defaults are restored for that mode.
function selectCNPlatform(platform: 'kimi' | 'zhipu' | 'deepseek') {
  form.platform = platform
  form.type = 'apikey'
  providerCategory.value = 'apikey'
  apiProtocol.value = 'adaptive'
  if (platform !== 'zhipu') {
    zhipuOrganization.value = ''
    zhipuProject.value = ''
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(platform, providerMode.value, apiProtocol.value)
  resetAdaptiveBaseUrls(platform, providerMode.value)
}

// Mode changes replace only untouched preset endpoints; administrator-edited
// native URLs survive. Protocol selection then chooses the corresponding URL
// without changing the resolved credential category.
watch(providerMode, (mode, previousMode) => {
  if (!isCNPlatform.value) return
  if (apiProtocol.value === 'adaptive') {
    // Compare against the previous mode's defaults before replacing endpoints;
    // custom URLs must survive billing-mode changes while untouched presets follow the new mode.
    const previousDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, previousMode)
    const nextDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, mode)
    for (const item of cnAdaptiveProtocolOptions.value) {
      if (
        !adaptiveBaseUrls.value[item.value] ||
        adaptiveBaseUrls.value[item.value] === previousDefaults[item.value]
      ) {
        adaptiveBaseUrls.value[item.value] = nextDefaults[item.value]
      }
    }
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
    return
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(form.platform, mode, apiProtocol.value)
})
watch(apiProtocol, (protocol) => {
  if (!isCNPlatform.value) return
  if (protocol === 'adaptive') {
    // Fill only absent adaptive endpoints when returning from one protocol;
    // populated URLs remain administrator choices and must not be overwritten with presets.
    const defaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, providerMode.value)
    for (const item of cnAdaptiveProtocolOptions.value) {
      if (!adaptiveBaseUrls.value[item.value])
        adaptiveBaseUrls.value[item.value] = defaults[item.value]
    }
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
    return
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(form.platform, providerMode.value, protocol)
})

// onCnPresetSelect selects the preset billing mode and adaptive editing.
// Native protocol presets replace their endpoint slot before the Chat preview updates.
// Queued mode and protocol watchers fill remaining defaults while retaining
// custom entries according to their previous-default and blank-value checks.
function onCnPresetSelect(preset: {
  mode: CnProviderMode // preset billing mode
  protocol: CnApiProtocol // target endpoint slot
  url: string // preset endpoint URL
}) {
  providerMode.value = preset.mode
  apiProtocol.value = 'adaptive'
  if (preset.protocol !== 'adaptive') adaptiveBaseUrls.value[preset.protocol] = preset.url
  apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
}

// syncPreviewCredentials supplies entered API Key credentials to model discovery;
// absent keys disable preview, and adaptive CN discovery uses the Chat endpoint.
// Platforms with provider modes also send the drafted mode.
const syncPreviewCredentials = computed(() => {
  if (!apiKeyValue.value) return undefined
  const baseUrl =
    isCNPlatform.value && apiProtocol.value === 'adaptive'
      ? adaptiveBaseUrls.value.chat_completions.trim() || apiKeyBaseUrl.value.trim()
      : apiKeyBaseUrl.value.trim()
  return {
    platform: form.platform,
    type: form.type,
    base_url: baseUrl || undefined,
    api_key: apiKeyValue.value,
    provider_mode: modeChoices.value.length > 0 ? providerMode.value : undefined,
  }
})

// storeModelProtocolSuggestions keeps per-model protocol suggestions returned by a model
// sync as the current mode's accepted catalog table. Responses without suggestions leave
// the table unchanged; manual overrides stay separate in either case.
function storeModelProtocolSuggestions(result: SyncUpstreamModelsResult) {
  if (result.model_protocols)
    openCodeProtocols.value.suggestions[providerMode.value] = { ...result.model_protocols }
}

// resolvedUpstreamUsageAdapter resolves the usage adapter for the drafted provider, or
// null when its type and mode support no usage query and the editor stays hidden.
const resolvedUpstreamUsageAdapter = computed(
  () =>
    resolveUpstreamUsageConfig({
      type: form.type,
      platform: form.platform,
      credentials: { provider_mode: providerMode.value },
      extra: { upstream_usage_query: { adapter: upstreamUsageAdapter.value } },
    }).adapter,
)

// Local quota limits use USD amounts and optional reset schedules. Null fields
// remain unconfigured until createProviderAndFinish selects the applicable
// serialized defaults.
const editQuotaLimit = ref<number | null>(null)
const editQuotaDailyLimit = ref<number | null>(null)
const editQuotaWeeklyLimit = ref<number | null>(null)
const editQuotaMonthlyLimit = ref<number | null>(null)
const editDailyResetMode = ref<'rolling' | 'fixed' | null>(null)
const editDailyResetHour = ref<number | null>(null)
const editWeeklyResetMode = ref<'rolling' | 'fixed' | null>(null)
// editMonthlyResetMode selects elapsed thirty-day windows or a local monthly date.
const editMonthlyResetMode = ref<'rolling' | 'fixed' | null>(null)
const editWeeklyResetDay = ref<number | null>(null)
// editMonthlyResetDay selects a calendar date from one through thirty-one.
const editMonthlyResetDay = ref<number | null>(null)
const editWeeklyResetHour = ref<number | null>(null)
// editMonthlyResetHour selects the hour in the shared quota timezone.
const editMonthlyResetHour = ref<number | null>(null)
const editResetTimezone = ref<string | null>(null)
// The quota field adapter shares these refs with the reusable editor, so child
// updates remain part of the dialog's draft until submission.
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
// Model mappings rewrite requests independently of the final whitelist. Qoder
// additionally tracks explicit interaction so generated editor defaults do not
// become persisted restrictions.
const modelMappings = ref<ModelMappingRow[]>([])
const openAICompactModelMappings = ref<ModelMappingRow[]>([])
const modelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const allowedModels = ref<string[]>([])
// qoderModelRestrictionTouched distinguishes generated defaults from explicit
// edits; untouched mapping and whitelist fields are removed from Qoder credentials.
const qoderModelRestrictionTouched = ref(false)
// qoderModelWhitelistTouched records direct whitelist edits separately, so
// mapping-only changes cannot persist model names populated by the editor itself.
const qoderModelWhitelistTouched = ref(false)
// Pool retry drafts select optional credential policy; disabled pooling omits
// retry settings and enabled pooling normalizes the configured retry count.
const poolModeEnabled = ref(false)
const poolModeRetryCount = ref(DEFAULT_POOL_MODE_RETRY_COUNT)
const poolModeRetryStatusCodesInput = ref('')

// Custom error codes and header overrides remain local drafts; submission
// includes their values only when the corresponding policy is enabled and
// supported.
const customErrorCodesEnabled = ref(false)
const selectedErrorCodes = ref<number[]>([])
const headerOverrideEnabled = ref(false)
const headerOverrideRows = ref<HeaderOverrideRow[]>([])

// Grok forwarding drafts apply only to provider requests; authorization and refresh continue using their dedicated OAuth endpoints.
const grokOAuthCustomBaseUrlEnabled = ref(false)
const grokOAuthBaseUrl = ref('')

// validateGrokOAuthUpstreamConfig validates forwarding configuration before credentials are consumed.
// Code exchange, refresh-token batches, and SSO imports share this check so invalid URLs or headers stop before upstream authorization.
const validateGrokOAuthUpstreamConfig = (): boolean => {
  if (grokOAuthCustomBaseUrlEnabled.value) {
    const trimmed = grokOAuthBaseUrl.value.trim()
    if (!trimmed) {
      failAt(t('admin.providers.grokCustomBaseUrl.required'), 'grok-base-url')
      return false
    }
    if (!/^https?:\/\//i.test(trimmed)) {
      failAt(t('admin.providers.grokCustomBaseUrl.invalid'), 'grok-base-url')
      return false
    }
  }
  if (headerOverrideEnabled.value) {
    const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
    if (headerError) {
      failAt(t(`admin.providers.headerOverride.${headerError}`), 'header-override')
      return false
    }
  }
  return true
}

// applyGrokOAuthUpstreamConfig writes validated forwarding and header drafts into credentials.
// Callers validate first; disabled custom URLs preserve existing credential fields while header policy is applied by its shared builder.
const applyGrokOAuthUpstreamConfig = (credentials: Record<string, unknown>) => {
  if (grokOAuthCustomBaseUrlEnabled.value) {
    credentials.base_url = grokOAuthBaseUrl.value.trim()
  }
  applyHeaderOverride(credentials, headerOverrideEnabled.value, headerOverrideRows.value, 'create')
}

// Request and quota controls are collected separately from tokens; platform
// builders include only policies applicable to the chosen authentication
// category.
const interceptWarmupRequests = ref(false)
const autoPauseOnExpired = ref(true)
const autoPause5hThreshold = ref<number | null>(null)
const autoPause7dThreshold = ref<number | null>(null)
const autoPause5hDisabled = ref(false)
const autoPause7dDisabled = ref(false)
const openaiPassthroughEnabled = ref(false)
// Namespace flattening is an OAuth compatibility option; its disabled default preserves incoming tool namespaces.
const openaiFlattenNamespacesEnabled = ref(false)
const openAICompactMode = ref<OpenAICompactMode>('force_on')
const openAINativeCompactionV2Mode = ref<OpenAICompactMode>('force_on')
// HTTP continuation starts disabled and is enabled only by the administrator for upstream previous_response_id support.
const openAIResponsesContinuationSupported = ref(false)
// Image URL backfill starts disabled and is serialized only for OpenAI API Key providers.
/** GIF defaults to enabled for each new OpenAI provider draft. */
const openAIImagesGIFEnabled = ref(true)
const openAIImagesURLToB64JSON = ref(false)
const responsesWSConnectionMode = ref<ResponsesWSConnectionMode>(RESPONSES_WS_POOLED)
const codexCLIOnlyAllowClaudeCodeEnabled = ref(false)
const openAIOAuthClientPolicy = ref<OpenAIOAuthClientPolicy>('any')
const codexFingerprintMode = ref<CodexFingerprintMode>('off')
const codexFingerprintModeOptions = useCodexFingerprintModeOptions()
const anthropicPassthroughEnabled = ref(false)
const anthropicAPIKeyAuthScheme = ref<AnthropicAPIKeyAuthScheme>('x_api_key')
const webSearchEmulationMode = ref('default')
const webSearchGlobalEnabled = ref(false)

// Quota notification drafts and global availability share the editor lifecycle;
// enabled thresholds are written only when provider metadata is assembled for submission.
const {
  globalEnabled: quotaNotifyGlobalEnabled,
  state: quotaNotifyState,
  loadGlobalState: loadQuotaNotifyGlobal,
  writeToExtra: writeQuotaNotifyToExtra,
  setField: setQuotaNotifyField,
} = useQuotaNotifyState()

// Load global feature states once
adminAPI.settings
  .getWebSearchEmulationConfig()
  .then((cfg) => {
    webSearchGlobalEnabled.value = cfg?.enabled === true && (cfg?.providers?.length ?? 0) > 0
  })
  .catch(() => {
    // Failed configuration loading hides unavailable emulation overrides so the
    // administrator cannot select a feature whose global availability is unknown.
    webSearchGlobalEnabled.value = false
  })

loadQuotaNotifyGlobal()
// Antigravity overages and manual Qoder credentials are draft-only until
// submission; changing authorization site resets sessions without clearing
// manual credential text.
const allowOverages = ref(false) // For antigravity providers: enable AI Credits overages
const qoderProviderType = ref<'oauth' | 'manual'>('oauth')
const qoderSite = ref<QoderSite>('global')
const qoderPAT = ref('')
const qoderSecurityOauthToken = ref('')
const qoderMachineId = ref('')
const qoderUidAid = ref('')
const qoderRefreshToken = ref('')
const qoderUserType = ref('personal_standard')
// Antigravity project identity, whitelist, and mappings are independent inputs;
// creation stores project selection and model restrictions alongside exchanged
// credentials.
const antigravityProjectId = ref('')
const antigravityModelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const antigravityWhitelistModels = ref<string[]>([])
const antigravityModelMappings = ref<ModelMappingRow[]>([])

// Bedrock credentials
// Bedrock credentials select SigV4 or API Key authentication; only the chosen
// authentication branch validates and serializes its required secret fields.
const bedrockAuthMode = ref<'sigv4' | 'apikey'>('sigv4')
const bedrockAccessKeyId = ref('')
const bedrockSecretAccessKey = ref('')
const bedrockSessionToken = ref('')
const bedrockRegion = ref('us-east-1')
const bedrockForceGlobal = ref(false)
const bedrockApiKeyValue = ref('')
// Service-account JSON can be pasted, selected, or dropped; accepted JSON
// populates project and email previews while location remains an independent
// provider setting.
const vertexServiceAccountFileInput = ref<HTMLInputElement | null>(null)
const vertexServiceAccountJson = ref('')
const vertexProjectId = ref('')
const vertexClientEmail = ref('')
const vertexLocation = ref('global')
const vertexServiceAccountDragActive = ref(false)
// Temporary-unschedulable drafts are validated before submission; Gemini
// authorization capability state controls which OAuth variants can be selected
// for the current provider.
const tempUnschedEnabled = ref(false)
const tempUnschedRules = ref<TempUnschedRuleForm[]>([])
const geminiOAuthType = ref<'code_assist' | 'google_one' | 'ai_studio'>('google_one')
const geminiAIStudioOAuthEnabled = ref(false)

// The shared option factory supplies translated client-policy labels while
// preserving the stable values consumed by OpenAI metadata construction.
const openAIOAuthClientPolicyOptions = useOpenAIOAuthClientPolicyOptions()
// buildAntigravityExtra serializes enabled AI Credits overages for Antigravity
// creation. Disabled overages return undefined, leaving the backend default
// policy intact without sending an explicit false override in Extra.
function buildAntigravityExtra(): Record<string, unknown> | undefined {
  const extra: Record<string, unknown> = {}
  if (allowOverages.value) extra.allow_overages = true
  return Object.keys(extra).length > 0 ? extra : undefined
}

// buildOpenAICompactModelMapping converts compact-context rows into persisted credentials.
// An absent mapping object lets callers omit the optional compact_model_mapping
// key, while populated rows remain separate from the ordinary model restriction.
const buildOpenAICompactModelMapping = () =>
  buildModelMappingObject('mapping', [], openAICompactModelMappings.value)
// Advanced OAuth and Gemini help visibility belong to the dialog; these controls
// reveal explanations and optional input without changing credential selection.
const showAdvancedOAuth = ref(false)
const showGeminiHelpDialog = ref(false)

// Quota control state (Anthropic OAuth/SetupToken only)
// Anthropic authorization drafts govern cost windows, session limits, RPM
// limits, and request queue mode; disabled policies are omitted when Extra is
// assembled.
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
// TLS fingerprint drafts keep profile and router selection separate. Discovery
// populates display choices, while platform-specific builders decide which
// selected identifiers may be persisted.
const tlsFingerprintEnabled = ref(false)
// tlsFingerprintProfileId uses null for the default profile and negative one for
// random selection; discovered profiles contribute their persisted positive identifiers.
const tlsFingerprintProfileId = ref<number | null>(null)
const tlsFingerprintProfiles = ref<
  {
    id: number // persisted selection identifier
    name: string // administrator display label
  }[]
>([])
// tlsFingerprintRouterId starts unset; metadata builders persist a selected router
// only for OpenAI OAuth creation, independently of the chosen fingerprint profile.
const tlsFingerprintRouterId = ref<number | null>(null)
const tlsFingerprintRouters = ref<
  {
    id: number // persisted selection identifier
    name: string // administrator display label
  }[]
>([])
// Fingerprint select options prepend the translated default choice to discovered
// administrator records; selection uses IDs rather than labels in creation
// requests.
const tlsFingerprintProfileOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.defaultProfile') },
  ...(tlsFingerprintProfiles.value.length > 0
    ? [{ value: -1, label: t('admin.providers.quotaControl.tlsFingerprint.randomProfile') }]
    : []),
  ...tlsFingerprintProfiles.value.map((profile) => ({ value: profile.id, label: profile.name })),
])
const tlsFingerprintRouterOptions = computed(() => [
  { value: null, label: t('admin.providers.quotaControl.tlsFingerprint.noRouter') },
  ...tlsFingerprintRouters.value.map((router) => ({ value: router.id, label: router.name })),
])
// Anthropic request options independently control session masking, cache
// lifetime, and custom forwarding address; disabled settings do not contribute
// metadata fields during creation.
const sessionIdMaskingEnabled = ref(false)
const cacheTTLOverrideEnabled = ref(false)
const cacheTTLOverrideTarget = ref<string>('5m')
const webSearchEmulationOptions = useWebSearchEmulationOptions()
const anthropicAPIKeyAuthSchemeOptions = useAnthropicAPIKeyAuthSchemeOptions()
const customBaseUrlEnabled = ref(false)
const customBaseUrl = ref('')

// Gemini tier selection (used as fallback when auto-detection is unavailable/fails)
// GeminiProviderType distinguishes official tier handling from third-party endpoint entry.
type GeminiProviderType = 'official' | 'third_party'
// Gemini source and quota-tier drafts determine official credential metadata;
// third-party API Key creation requires a custom endpoint and omits official
// tier information.
const geminiProviderType = ref<GeminiProviderType>('official')
const geminiTierGoogleOne = ref<'google_one_free' | 'google_ai_pro' | 'google_ai_ultra'>(
  'google_one_free',
)
const geminiTierGcp = ref<'gcp_standard' | 'gcp_enterprise'>('gcp_standard')
const geminiTierAIStudio = ref<'aistudio_free' | 'aistudio_paid'>('aistudio_free')

const geminiSelectedTier = computed(() => {
  if (form.platform !== 'gemini') return ''
  if (providerCategory.value === 'apikey') {
    return geminiProviderType.value === 'official' ? geminiTierAIStudio.value : ''
  }
  switch (geminiOAuthType.value) {
    case 'google_one':
      return geminiTierGoogleOne.value
    case 'code_assist':
      return geminiTierGcp.value
    default:
      return geminiTierAIStudio.value
  }
})

// Gemini help links point to provider-maintained quota references and account
// setup pages; they are display navigation and never credential or request
// endpoints.
const geminiQuotaDocs = {
  codeAssist: 'https://developers.google.com/gemini-code-assist/resources/quotas',
  aiStudio: 'https://ai.google.dev/pricing',
  vertex: 'https://cloud.google.com/vertex-ai/generative-ai/docs/quotas',
}

const geminiHelpLinks = {
  apiKey: 'https://aistudio.google.com/app/apikey',
  aiStudioPricing: 'https://ai.google.dev/pricing',
  gcpProject: 'https://console.cloud.google.com/welcome/new',
  geminiWebActivation: 'https://gemini.google.com/gems/create?hl=en-US&pli=1',
  countryCheck: 'https://policies.google.com/terms',
  countryChange: 'https://policies.google.com/country-association-form',
}

// Shared provider fields remain reactive across both configuration and
// authorization steps. Request builders preserve these values, applying
// endpoint-specific omission rules only at submission.
const form = reactive({
  name: '', // administrator display name
  notes: '', // administrator provider notes
  platform: 'anthropic' as ProviderPlatform, // selected upstream platform
  type: 'oauth' as ProviderType, // platform-derived credential type
  credentials: {} as Record<string, unknown>, // pending credential object
  proxy_id: null as number | null, // optional outbound proxy
  concurrency: 10, // concurrent request capacity
  load_factor: null as number | null, // optional load weighting
  priority: 1, // routing selection priority
  rate_multiplier: 1, // provider rate multiplier
  group_ids: [] as number[], // selected routing groups
  expires_at: null as number | null, // optional expiration Unix seconds
})
// OpenAI batches and imports own their progress and partial-success reporting;
// the dialog supplies the current draft policies at each persistence boundary.
const {
  openaiOAuth,
  handleOpenAIExchange,
  handleOpenAIImportCodexSession,
  handleOpenAIImportCodexPAT,
  handleOpenAIValidateRT,
  handleOpenAIValidateMobileRT,
} = useOpenAICreateAuthorization({
  form,
  autoPauseOnExpired,
  upstreamProtocols,
  inputMethod: () => oauthFlowRef.value?.inputMethod,
  buildCredentialExtras: () => buildOpenAICodexImportCredentialExtras(),
  applyCredentialPolicies: (credentials) => applyOpenAICreateCredentialPolicies(credentials),
  buildExtra: (base) => buildOpenAIExtra(base),
  withRequestIdHeader: (base) => withUpstreamRequestIdHeader(base),
  validateTemporaryRules: () => ensureOpenAITempUnschedConfigReady(),
  tokenTLSRouterId: () => (tlsFingerprintEnabled.value ? tlsFingerprintRouterId.value : null),
  createProvider: (payload) => createProtocolProvider(payload),
  created: () => emit('created'),
  close: () => handleClose(),
})

// The Qoder controller owns request identity, popup leases, polling, and the
// creation lock. Draft readers stay live so platform and site changes invalidate
// pending authorization results before those results can publish completion.
const {
  qoderOAuth,
  isQoderOAuthProviderCreating,
  stopQoderPolling,
  closeQoderAuthPopup,
  resetQoderOAuthCompletionState,
  handleGenerateUrl: generateQoderAuthUrl,
  handleQoderExchange,
} = useQoderCreateAuthorization({
  show: () => props.show,
  platform: () => form.platform,
  site: () => qoderSite.value,
  proxyId: () => form.proxy_id,
  submitting: () => submitting.value,
  setSubmitting: (value) => {
    submitting.value = value
  },
  oauthState: () => oauthFlowRef.value?.oauthState || '',
  create: (tokenInfo, current) => {
    const credentials = qoderOAuth.buildCredentials(tokenInfo)
    applyQoderModelRestriction(credentials)
    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
    return createProviderAndFinish('qoder', 'cosy', credentials, buildQoderExtra(), current)
  },
  popupBlocked: () => appStore.showWarning(t('admin.providers.oauth.qoder.popupBlocked')),
  authFailed: () => t('admin.providers.oauth.authFailed'),
  showError: (message) => appStore.showError(message),
})

// Balance reminders require an upstream adapter that exposes wallet balance;
// eligibility follows platform, provider type, and CN billing mode changes.
const balanceAlertSupported = computed(() =>
  supportsUpstreamBalanceAlert(form.type, form.platform, providerMode.value),
)

// isOAuthFlow selects the authorization step for OAuth-based categories; manual
// Qoder and Anthropic Bedrock credentials instead submit from the configuration form.
const isOAuthFlow = computed(() => {
  if (form.platform === 'qoder' && qoderProviderType.value === 'manual') {
    return false
  }

  // Bedrock submits its credentials directly without entering authorization.
  if (form.platform === 'anthropic' && providerCategory.value === 'bedrock') {
    return false
  }
  return providerCategory.value === 'oauth-based'
})

// isGrokSSOInputMethod relaxes required name validation for Grok SSO imports;
// handleGrokImportSSO omits the name field when the administrator leaves it blank.
const isGrokSSOInputMethod = computed(
  () => form.platform === 'grok' && oauthFlowRef.value?.inputMethod === 'sso_cookie',
)

// isManualInputMethod exposes the completion action for the child panel's manual
// authorization input; other methods dispatch their dedicated child events.
const isManualInputMethod = computed(() => {
  return oauthFlowRef.value?.inputMethod === 'manual'
})

// platformRows distributes the platform choices across rows of six and five;
// labels and brand colors come from the shared platform catalog.
const platformRows: Array<
  Array<{
    value: ProviderPlatform // selected upstream platform
    testid?: string // optional test selector
  }>
> = [
  [
    { value: 'anthropic' },
    { value: 'openai' },
    { value: 'gemini', testid: 'create-provider-platform-gemini' },
    { value: 'antigravity' },
    { value: 'qoder', testid: 'create-provider-platform-qoder' },
    { value: 'grok' },
  ],
  [
    { value: 'jev', testid: 'create-provider-platform-jev' },
    { value: 'opencode', testid: 'create-provider-platform-opencode' },
    { value: 'kimi' },
    { value: 'zhipu' },
    { value: 'deepseek' },
  ],
]
// selectPlatform selects a platform through the CN preset reset when
// applicable. International platforms update the platform field directly,
// letting the existing platform watcher restore their authentication and
// endpoint defaults.
//
// A mode chosen for the previous platform may not exist on the new one, so the
// mode falls back to the new platform's first choice before any endpoint
// defaults are computed. Address slots belong to one platform, so international
// platforms start with blank slots.
function selectPlatform(platform: ProviderPlatform) {
  const choices = providerModeChoices(platform)
  if (choices.length > 0 && !choices.some((choice) => choice.value === providerMode.value)) {
    providerMode.value = choices[0]!.value
  }
  if (platform === 'kimi' || platform === 'zhipu' || platform === 'deepseek') {
    selectCNPlatform(platform)
    return
  }
  adaptiveBaseUrls.value = { chat_completions: '', anthropic: '', responses: '' }
  form.platform = platform
}

// Authorization and authentication option labels are localized while their
// values retain the identifiers expected by the platform-specific creation
// handlers.
const qoderSiteOptions = computed(() => [
  {
    value: 'global' as QoderSite,
    label: t('admin.providers.qoder.site.global'),
    testid: 'create-qoder-site-global',
  },
  {
    value: 'cn' as QoderSite,
    label: t('admin.providers.qoder.site.cn'),
    testid: 'create-qoder-site-cn',
  },
])
const addMethodOptions = computed(() => [
  { value: 'oauth' as AddMethod, label: t('admin.providers.types.oauth') },
  { value: 'setup-token' as AddMethod, label: t('admin.providers.setupTokenLongLived') },
])
const bedrockAuthModeOptions = computed(() => [
  { value: 'sigv4' as const, label: t('admin.providers.bedrockAuthModeSigv4') },
  { value: 'apikey' as const, label: t('admin.providers.bedrockAuthModeApikey') },
])

// Category predicates determine which form sections exist and which policies
// apply; derived visibility leaves persisted behavior to the selected creation
// handler.
const isBedrockCategory = computed(
  () => form.platform === 'anthropic' && providerCategory.value === 'bedrock',
)
const isServiceAccountCategory = computed(
  () =>
    (form.platform === 'gemini' || form.platform === 'anthropic') &&
    providerCategory.value === 'service_account',
)
const isAnthropicOAuthCategory = computed(
  () => form.platform === 'anthropic' && providerCategory.value === 'oauth-based',
)
const isOpenAIOAuthCategory = computed(
  () => form.platform === 'openai' && providerCategory.value === 'oauth-based',
)
// API Key providers share credential controls; visibility follows the resolved provider type after category selection.
const isApiKeyCredentials = computed(() => form.type === 'apikey')
const showGeminiApiKeyTier = computed(
  () => form.platform === 'gemini' && geminiProviderType.value === 'official',
)
const showCredentialsSection = computed(
  () =>
    form.platform === 'antigravity' ||
    (form.platform === 'qoder' && qoderProviderType.value === 'manual') ||
    isServiceAccountCategory.value ||
    isApiKeyCredentials.value ||
    isBedrockCategory.value,
)
const showModelRestriction = computed(
  () =>
    form.platform === 'qoder' ||
    isApiKeyCredentials.value ||
    isBedrockCategory.value ||
    (['openai', 'grok', 'gemini'].includes(form.platform) && isOAuthFlow.value),
)
const showHeaderOverride = computed(
  () =>
    (isApiKeyCredentials.value && isHeaderOverrideCapable(form.platform, 'apikey')) ||
    (form.platform === 'grok' && isOAuthFlow.value),
)
// TLS fingerprint controls appear only for supported client-emulation flows; null hides the section for other provider categories.
const tlsFingerprintTestIdPrefix = computed<string | null>(() => {
  if (form.platform === 'qoder') return 'create-qoder-tls-fingerprint'
  if (isOpenAIOAuthCategory.value) return 'create-openai-tls-fingerprint'
  if (isAnthropicOAuthCategory.value) return ''
  return null
})
// platformApiKeyPreset resolves the selected platform's API-key defaults for the current
// provider mode; the placeholders below apply the Gemini relay source on top of it.
const platformApiKeyPreset = computed(() => apiKeyPreset(form.platform, providerMode.value))
// isGeminiRelaySource reports whether the Gemini draft targets an administrator relay.
const isGeminiRelaySource = computed(
  () => form.platform === 'gemini' && geminiProviderType.value === 'third_party',
)
// apiKeyBaseUrlPlaceholder shows the preset address, or a bare scheme for a Gemini relay.
const apiKeyBaseUrlPlaceholder = computed(() =>
  isGeminiRelaySource.value ? 'https://' : platformApiKeyPreset.value.baseUrlPlaceholder,
)
// apiKeyPlaceholder shows the preset key prefix, or a generic key for a Gemini relay.
const apiKeyPlaceholder = computed(() =>
  isGeminiRelaySource.value ? 'api-key-...' : platformApiKeyPreset.value.apiKeyPlaceholder,
)

// Tab definitions omit empty categories for the selected platform and provider type; SettingsTabs handles fallback when the active tab disappears.
const tabsRef = ref<InstanceType<typeof SettingsTabs> | null>(null)
const formTabs = computed(() => {
  const hasModels = showModelRestriction.value || form.platform === 'antigravity'
  const hasQuota =
    form.type === 'apikey' || form.type === 'bedrock' || isAnthropicOAuthCategory.value
  return [
    { key: 'basic', label: t('admin.providers.tabs.basic') },
    { key: 'models', label: t('admin.providers.tabs.models'), hidden: !hasModels },
    { key: 'scheduling', label: t('admin.providers.tabs.scheduling') },
    { key: 'quota', label: t('admin.providers.tabs.quota'), hidden: !hasQuota },
    { key: 'request', label: t('admin.providers.tabs.request') },
  ]
})

// failAt reports validation through the shared toast and field navigation.
// SettingsTabs reveals and focuses the field when it exists, allowing errors in hidden panels to be corrected directly.
function failAt(message: string, field: string) {
  appStore.showError(message)
  void tabsRef.value?.revealField(`[data-provider-field="${field}"]`)
}

// Expiration uses the shared local datetime formatter and parser, keeping the
// stored Unix seconds separate from the browser input's editable local-time text.
const expiresAtInput = computed({
  // get formats expiration Unix seconds as browser-local datetime input text through minutes.
  // Unset, zero, or invalid timestamps display an empty value for the date input.
  get: () => formatDateTimeLocal(form.expires_at),
  // set parses timezone-free local input into whole Unix seconds for provider API submission.
  // Empty or invalid date input writes null, clearing the draft expiration value.
  set: (value: string) => {
    form.expires_at = parseDateTimeLocal(value)
  },
})

// Authorization completion availability follows each input method's required
// draft fields and prevents duplicate Qoder submission while its creation lock
// is held.
const canExchangeCode = computed(() => {
  const authCode = oauthFlowRef.value?.authCode || ''
  if (form.platform === 'openai') {
    return (
      authCode.trim() && openaiOAuth.authSessions.value.length > 0 && !openaiOAuth.loading.value
    )
  }
  if (form.platform === 'gemini') {
    return authCode.trim() && geminiOAuth.sessionId.value && !geminiOAuth.loading.value
  }
  if (form.platform === 'antigravity') {
    return authCode.trim() && antigravityOAuth.sessionId.value && !antigravityOAuth.loading.value
  }
  if (form.platform === 'qoder') {
    return (
      qoderOAuth.sessionId.value &&
      !qoderOAuth.loading.value &&
      !submitting.value &&
      !isQoderOAuthProviderCreating.value
    )
  }
  if (form.platform === 'grok') {
    return authCode.trim() && grokOAuth.sessionId.value && !grokOAuth.loading.value
  }
  return authCode.trim() && oauth.sessionId.value && !oauth.loading.value
})

// Opening initializes platform-specific choices; closing restores all form
// drafts through resetForm while asynchronous capability reads keep their own results.
watch(
  () => props.show,
  (newVal) => {
    if (newVal) {
      form.platform = props.initialPlatform || 'anthropic'
      // Load TLS fingerprint profiles
      adminAPI.tlsFingerprintProfiles
        .list()
        .then((profiles) => {
          tlsFingerprintProfiles.value = profiles.map((p) => ({ id: p.id, name: p.name }))
        })
        .catch(() => {
          // Failed discovery hides unavailable profiles while preserving manual
          // provider editing; a later dialog opening retries the profile request.
          tlsFingerprintProfiles.value = []
        })
      adminAPI.tlsFingerprintRouters
        .list()
        .then((routers) => {
          tlsFingerprintRouters.value = routers.map((router) => ({
            id: router.id,
            name: router.name,
          }))
        })
        .catch(() => {
          // Failed discovery hides unavailable routers while retaining the form
          // draft; another dialog opening can refresh the available router list.
          tlsFingerprintRouters.value = []
        })
      // Modal opened - fill related models
      allowedModels.value = []
      // Antigravity starts in mapping mode and asynchronously fills the platform default mappings while leaving its whitelist unrestricted.
      if (form.platform === 'antigravity') {
        antigravityModelRestrictionMode.value = 'mapping'
        fetchAntigravityDefaultMappings().then((mappings) => {
          antigravityModelMappings.value = [...mappings]
        })
        antigravityWhitelistModels.value = []
      } else {
        antigravityWhitelistModels.value = []
        antigravityModelMappings.value = []
        antigravityModelRestrictionMode.value = 'mapping'
      }
    } else {
      resetForm()
    }
  },
)

// The resolved provider type follows platform and authorization category; Qoder, Antigravity, and Jev override the generic category mapping.
watch(
  [providerCategory, addMethod, qoderProviderType, () => form.platform],
  ([category, method]) => {
    if (platformApiKeyPreset.value.apiKeyOnly) {
      form.type = 'apikey'
      return
    }
    if (form.platform === 'qoder') {
      form.type = 'cosy'
      return
    }
    if (form.platform === 'antigravity') {
      form.type = 'oauth'
      return
    }

    // Bedrock is a distinct Anthropic credential type.
    if (form.platform === 'anthropic' && category === 'bedrock') {
      form.type = 'bedrock' as ProviderType
      return
    }
    if (
      (form.platform === 'gemini' || form.platform === 'anthropic') &&
      category === 'service_account'
    ) {
      form.type = 'service_account' as ProviderType
    } else if (category === 'oauth-based') {
      form.type = form.platform === 'anthropic' ? (method as ProviderType) : 'oauth'
    } else {
      form.type = 'apikey'
    }
  },
  { immediate: true },
)

// Reset platform-specific settings when platform changes
watch(
  () => form.platform,
  (newPlatform) => {
    // Reset base URL based on platform; a Gemini relay source starts blank for its relay address.
    apiKeyBaseUrl.value =
      newPlatform === 'gemini' && geminiProviderType.value === 'third_party'
        ? ''
        : apiKeyPreset(newPlatform).defaultBaseUrl
    // Model protocol tables describe one platform's models, so each platform starts empty.
    openCodeProtocols.value = openCodeDraft()
    // Clear restrictions when the platform changes so prior-platform model names cannot silently become restrictions on the newly selected provider.
    allowedModels.value = []
    modelMappings.value = []
    modelRestrictionMode.value =
      newPlatform === 'qoder' || newPlatform === 'grok' ? 'mapping' : 'whitelist'
    qoderModelRestrictionTouched.value = false
    qoderModelWhitelistTouched.value = false
    // Antigravity starts in mapping mode and asynchronously fills the platform default mappings while leaving its whitelist unrestricted.
    if (newPlatform === 'antigravity') {
      antigravityModelRestrictionMode.value = 'mapping'
      fetchAntigravityDefaultMappings().then((mappings) => {
        antigravityModelMappings.value = [...mappings]
      })
      antigravityWhitelistModels.value = []
      providerCategory.value = 'oauth-based'
    } else {
      allowOverages.value = false
      antigravityWhitelistModels.value = []
      antigravityModelMappings.value = []
      antigravityModelRestrictionMode.value = 'mapping'
    }
    if (newPlatform === 'qoder') {
      providerCategory.value = 'oauth-based'
      qoderProviderType.value = 'oauth'
      qoderSite.value = 'global'
    } else {
      qoderProviderType.value = 'oauth'
      qoderPAT.value = ''
      qoderSecurityOauthToken.value = ''
      qoderMachineId.value = ''
      qoderUidAid.value = ''
      qoderRefreshToken.value = ''
      qoderUserType.value = 'personal_standard'
    }
    if (newPlatform === 'grok') {
      providerCategory.value = 'oauth-based'
      addMethod.value = 'oauth'
      modelRestrictionMode.value = 'mapping'
      form.concurrency = 1
      form.load_factor = null
    }
    if (
      newPlatform !== 'gemini' &&
      newPlatform !== 'anthropic' &&
      providerCategory.value === 'service_account'
    ) {
      providerCategory.value = 'oauth-based'
    }
    if (newPlatform !== 'anthropic' && providerCategory.value === 'bedrock') {
      providerCategory.value = 'oauth-based'
    }
    if (apiKeyPreset(newPlatform).apiKeyOnly) providerCategory.value = 'apikey'
    upstreamUsageEnabled.value = newPlatform !== 'jev'
    // Reset Bedrock fields when switching platforms
    bedrockAccessKeyId.value = ''
    bedrockSecretAccessKey.value = ''
    bedrockSessionToken.value = ''
    bedrockRegion.value = 'us-east-1'
    bedrockForceGlobal.value = false
    bedrockAuthMode.value = 'sigv4'
    bedrockApiKeyValue.value = ''
    vertexServiceAccountJson.value = ''
    vertexProjectId.value = ''
    vertexClientEmail.value = ''
    vertexLocation.value = 'global'
    // Reset Anthropic/Antigravity-specific settings when switching to other platforms
    if (newPlatform !== 'anthropic' && newPlatform !== 'antigravity') {
      interceptWarmupRequests.value = false
    }
    if (newPlatform !== 'openai') {
      openaiPassthroughEnabled.value = false
      openaiFlattenNamespacesEnabled.value = false
      openAIResponsesContinuationSupported.value = false
      openAIImagesGIFEnabled.value = true
      openAIImagesURLToB64JSON.value = false
      responsesWSConnectionMode.value = RESPONSES_WS_POOLED
      codexCLIOnlyAllowClaudeCodeEnabled.value = false
      openAIOAuthClientPolicy.value = 'any'
    }
    if (newPlatform !== 'anthropic') {
      anthropicPassthroughEnabled.value = false
      anthropicAPIKeyAuthScheme.value = 'x_api_key'
      webSearchEmulationMode.value = 'default'
    }

    // Header overrides use platform-specific names; clear their rows during platform
    // changes so another platform cannot inherit the previous provider draft's request headers.
    headerOverrideEnabled.value = false
    headerOverrideRows.value = []
    grokOAuthCustomBaseUrlEnabled.value = false
    grokOAuthBaseUrl.value = ''
    // Reset OAuth states
    oauth.resetState()
    openaiOAuth.resetState()

    geminiOAuth.resetState()
    antigravityOAuth.resetState()
    qoderOAuth.resetState()
    grokOAuth.resetState()
  },
)

watch(geminiProviderType, (providerType) => {
  if (form.platform !== 'gemini' || providerType !== 'third_party') return
  // Clear the official endpoint when switching to third-party Gemini so an unchanged default cannot masquerade as the administrator's third-party URL.
  if (!isGeminiThirdPartyBaseUrl(apiKeyBaseUrl.value)) {
    apiKeyBaseUrl.value = ''
  }
})

watch(qoderSite, (newSite, oldSite) => {
  if (newSite === oldSite || form.platform !== 'qoder') return
  // Site changes invalidate the authorization session and its browser resources
  // because the old session captured another endpoint family; manual credential drafts remain available.
  stopQoderPolling()
  closeQoderAuthPopup()
  resetQoderOAuthCompletionState()
  qoderOAuth.resetState()
})

// Gemini AI Studio OAuth availability (requires operator-configured OAuth client)
watch([providerCategory, () => form.platform], ([category, platform]) => {
  if (platform === 'openai' && category !== 'oauth-based') {
    codexCLIOnlyAllowClaudeCodeEnabled.value = false
    openAIOAuthClientPolicy.value = 'any'
    tlsFingerprintRouterId.value = null
  }
  if (platform !== 'anthropic' || category !== 'apikey') {
    anthropicPassthroughEnabled.value = false
    anthropicAPIKeyAuthScheme.value = 'x_api_key'
    webSearchEmulationMode.value = 'default'
  }
})

watch(
  [() => props.show, () => form.platform, providerCategory],
  async ([show, platform, category]) => {
    if (!show || platform !== 'gemini' || category !== 'oauth-based') {
      geminiAIStudioOAuthEnabled.value = false
      return
    }
    const caps = await geminiOAuth.getCapabilities()
    geminiAIStudioOAuthEnabled.value = !!caps?.ai_studio_oauth_enabled
    if (!geminiAIStudioOAuthEnabled.value && geminiOAuthType.value === 'ai_studio') {
      geminiOAuthType.value = 'code_assist'
    }
  },
  { immediate: true },
)
// handleSelectGeminiOAuthType selects a configured Gemini authorization
// variant. AI Studio requires a positive capability response; unsupported
// selection reports an error and retains the current authorization type for
// this draft.
const handleSelectGeminiOAuthType = (oauthType: 'code_assist' | 'google_one' | 'ai_studio') => {
  if (oauthType === 'ai_studio' && !geminiAIStudioOAuthEnabled.value) {
    appStore.showError(t('admin.providers.oauth.gemini.aiStudioNotConfigured'))
    return
  }
  geminiOAuthType.value = oauthType
}

watch([antigravityModelRestrictionMode, () => form.platform], ([, platform]) => {
  if (platform !== 'antigravity') return
  // Antigravity keeps its whitelist empty unless explicitly edited, allowing
  // unrestricted final models while the independently edited mapping still controls request rewriting.
})
// touchQoderModelRestriction records explicit Qoder model-policy edits for
// subsequent serialization. Generated defaults remain untouched until this
// marker is set, preventing automatic editor population from becoming an
// administrator-configured restriction.
const touchQoderModelRestriction = () => {
  if (form.platform === 'qoder') {
    qoderModelRestrictionTouched.value = true
  }
}

// setAllowedModels replaces the shared whitelist and marks explicit Qoder
// whitelist edits. Qoder tracks this separately from mapping edits so an edited
// mapping alone does not persist generated whitelist choices.
const setAllowedModels = (models: string[]) => {
  touchQoderModelRestriction()
  if (form.platform === 'qoder') {
    qoderModelWhitelistTouched.value = true
  }
  allowedModels.value = models
}

// applyPersistedModelRestriction updates mutable credentials with independent
// mappings and whitelist. It removes absent mappings and always writes the
// whitelist, including an explicit empty array that prevents backend derivation
// from self-mappings.
const applyPersistedModelRestriction = (credentials: Record<string, unknown>) => {
  // Persist request mappings separately from the final whitelist. An explicit
  // empty array must survive serialization because absence lets the backend
  // derive a whitelist from self-mapping entries instead.
  const persisted = buildPersistedModelRestriction(allowedModels.value, modelMappings.value)
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  credentials.model_whitelist = persisted.modelWhitelist
}

// applyQoderModelRestriction preserves the distinction between generated editor
// defaults and explicit administrator policy. Untouched controls remove both
// fields so backend defaults remain authoritative. Once restrictions are
// touched, mapping edits are serialized independently, while whitelist values
// are included only after a direct whitelist edit. An explicitly cleared
// whitelist stays an empty array; mapping-only edits also write an empty
// whitelist to prevent accidental derivation from self-mapping entries.
const applyQoderModelRestriction = (credentials: Record<string, unknown>) => {
  if (!qoderModelRestrictionTouched.value) {
    delete credentials.model_mapping
    delete credentials.model_whitelist
    return
  }
  const persisted = buildPersistedModelRestriction(
    qoderModelWhitelistTouched.value ? allowedModels.value : [],
    modelMappings.value,
  )
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  credentials.model_whitelist = persisted.modelWhitelist
}

// buildTempUnschedRules converts valid form rows into temporary-unschedulable
// request rules. Invalid HTTP codes, nonpositive durations, and empty keyword
// lists are skipped; accepted numeric values are truncated and descriptions are
// trimmed before persistence.
const buildTempUnschedRules = (rules: TempUnschedRuleForm[]) => {
  const out: Array<{
    error_code: number // HTTP status code
    keywords: string[] // matched message fragments
    duration_minutes: number // suspension duration minutes
    description: string // administrator rule description
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

    // Valid numeric form values may contain fractions; truncate both fields
    // before persistence because HTTP codes and configured minutes are serialized as integers.
    out.push({
      error_code: Math.trunc(errorCode),
      keywords,
      duration_minutes: Math.trunc(duration),
      description: rule.description.trim(),
    })
  }

  return out
}

// applyTempUnschedConfig writes enabled temporary-unschedulable policy into
// mutable credentials. Disabled policy removes both fields; enabled policy with
// no valid rules reports the field error and returns false so submission stops.
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

// splitTempUnschedKeywords trims comma-separated or semicolon-separated
// keywords and removes empty entries.
const splitTempUnschedKeywords = (value: string) => {
  return value
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

// createProtocolProvider applies native capability selection before creating a provider.
// It waits for the protocol catalog, preserves an explicit empty selection, and
// filters unsupported protocols against platform, type, and credential auth mode.
// Antigravity and Gemini policies are applied before filtering; obsolete protocol
// fields are removed from credentials and Extra so they cannot compete with the
// new native set. Callers pass mutable payloads and receive the API creation result.
async function createProtocolProvider(payload: CreateProviderRequest) {
  if (payload.platform === 'antigravity') {
    payload.credentials = {
      ...payload.credentials,
      model_whitelist: [...antigravityWhitelistModels.value],
    }
  } else if (payload.platform === 'gemini') {
    applyPersistedModelRestriction(payload.credentials)
  }
  await loadProtocolCatalog()
  const options = nativeProtocolOptions(
    payload.platform,
    payload.type,
    String(payload.credentials?.auth_mode ?? ''),
  )
  payload.credentials = {
    ...payload.credentials,
    upstream_protocols: (upstreamProtocols.value ?? options).filter((id) => options.includes(id)),
  }
  if (payload.platform === 'openai' && payload.type === 'apikey') {
    Object.assign(payload.credentials, imageModes.value)
  }
  delete payload.credentials.api_protocol
  delete payload.credentials.openai_workload_capabilities
  if (payload.extra) delete payload.extra.openai_text_route_mode
  return adminAPI.providers.create(payload)
}

// ProviderCreateGuard admits completion only while its authorization identity remains current.
type ProviderCreateGuard = () => boolean
// submitCreateProvider persists one request and publishes completion only for
// its current flow. The optional guard defaults to allowing publication for
// ordinary creation paths. API success produces a notification, a list-refresh
// event, and dialog closure in that order; obsolete results return false
// without publishing. Failures display server detail when current, and finally
// releases shared submission state only if the guard still admits this flow.
const submitCreateProvider = async (
  payload: CreateProviderRequest,
  isCurrent: ProviderCreateGuard = () => true,
): Promise<boolean> => {
  submitting.value = true
  try {
    await createProtocolProvider(payload)
    if (!isCurrent()) return false
    appStore.showSuccess(t('admin.providers.providerCreated'))
    emit('created')
    finishClose()
    return true
  } catch (error: any) {
    if (!isCurrent()) return false
    appStore.showError(
      error.response?.data?.message ||
        error.response?.data?.detail ||
        t('admin.providers.failedToCreate'),
    )
    return false
  } finally {
    // Only the still-current flow releases shared submission state; an obsolete
    // response must not unlock a newer provider creation while it remains pending.
    if (isCurrent()) {
      submitting.value = false
    }
  }
}

// resetForm clears dialog drafts and selects the initial creation step.
// It also removes quota amounts and schedules so reopening the dialog starts
// with unconfigured local spending limits.
const resetForm = () => {
  stopQoderPolling()
  closeQoderAuthPopup()
  resetQoderOAuthCompletionState()
  step.value = 1
  form.name = ''
  form.notes = ''
  form.platform = 'anthropic'
  form.type = 'oauth'
  form.credentials = {}
  upstreamProtocols.value = undefined
  imageModes.value = readImageStreamModes()
  form.proxy_id = null
  form.concurrency = 10
  form.load_factor = null
  form.priority = 1
  form.rate_multiplier = 1
  form.group_ids = []
  form.expires_at = null
  providerCategory.value = 'oauth-based'
  addMethod.value = 'oauth'
  providerMode.value = 'payg'
  apiProtocol.value = 'adaptive'
  zhipuOrganization.value = ''
  zhipuProject.value = ''
  adaptiveBaseUrls.value = { chat_completions: '', anthropic: '', responses: '' }
  apiKeyBaseUrl.value = 'https://api.anthropic.com'
  apiKeyValue.value = ''
  upstreamUsageEnabled.value = true
  balanceAlert.value = defaultUpstreamBalanceAlert()
  upstreamUsageAdapter.value = 'sub2api'
  upstreamUsageBaseUrl.value = ''
  upstreamUsageWalletAccessToken.value = ''
  upstreamUsageWalletUserId.value = ''
  upstreamRequestIdHeader.value = ''
  codexImageResponsesModel.value = ''
  editQuotaLimit.value = null
  editQuotaDailyLimit.value = null
  editQuotaWeeklyLimit.value = null
  editQuotaMonthlyLimit.value = null
  editDailyResetMode.value = null
  editDailyResetHour.value = null
  editWeeklyResetMode.value = null
  editMonthlyResetMode.value = null
  editWeeklyResetDay.value = null
  editMonthlyResetDay.value = null
  editWeeklyResetHour.value = null
  editMonthlyResetHour.value = null
  editResetTimezone.value = null
  modelMappings.value = []
  openAICompactModelMappings.value = []
  modelRestrictionMode.value = 'whitelist'
  allowedModels.value = []
  qoderModelRestrictionTouched.value = false
  qoderModelWhitelistTouched.value = false

  antigravityModelRestrictionMode.value = 'mapping'
  antigravityWhitelistModels.value = []
  antigravityProjectId.value = ''
  fetchAntigravityDefaultMappings().then((mappings) => {
    antigravityModelMappings.value = [...mappings]
  })
  poolModeEnabled.value = false
  poolModeRetryCount.value = DEFAULT_POOL_MODE_RETRY_COUNT
  poolModeRetryStatusCodesInput.value = ''
  customErrorCodesEnabled.value = false
  selectedErrorCodes.value = []
  headerOverrideEnabled.value = false
  headerOverrideRows.value = []
  grokOAuthCustomBaseUrlEnabled.value = false
  grokOAuthBaseUrl.value = ''
  interceptWarmupRequests.value = false
  autoPauseOnExpired.value = true
  autoPause5hThreshold.value = null
  autoPause7dThreshold.value = null
  autoPause5hDisabled.value = false
  autoPause7dDisabled.value = false
  openaiPassthroughEnabled.value = false
  openaiFlattenNamespacesEnabled.value = false
  openAICompactMode.value = 'force_on'
  openAINativeCompactionV2Mode.value = 'force_on'
  openAIResponsesContinuationSupported.value = false
  openAIImagesGIFEnabled.value = true
  openAIImagesURLToB64JSON.value = false
  responsesWSConnectionMode.value = RESPONSES_WS_POOLED
  codexCLIOnlyAllowClaudeCodeEnabled.value = false
  openAIOAuthClientPolicy.value = 'any'
  codexFingerprintMode.value = 'off'
  anthropicPassthroughEnabled.value = false
  anthropicAPIKeyAuthScheme.value = 'x_api_key'
  webSearchEmulationMode.value = 'default'
  // Reset quota control state
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
  allowOverages.value = false
  qoderProviderType.value = 'oauth'
  qoderSite.value = 'global'
  qoderPAT.value = ''
  qoderSecurityOauthToken.value = ''
  qoderMachineId.value = ''
  qoderUidAid.value = ''
  qoderRefreshToken.value = ''
  qoderUserType.value = 'personal_standard'
  antigravityProjectId.value = ''
  vertexServiceAccountJson.value = ''
  vertexProjectId.value = ''
  vertexClientEmail.value = ''
  vertexLocation.value = 'global'
  tempUnschedEnabled.value = false
  tempUnschedRules.value = []
  geminiOAuthType.value = 'code_assist'
  geminiProviderType.value = 'official'
  geminiTierGoogleOne.value = 'google_one_free'
  geminiTierGcp.value = 'gcp_standard'
  geminiTierAIStudio.value = 'aistudio_free'
  oauth.resetState()
  openaiOAuth.resetState()
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  qoderOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
}

// finishClose invalidates Qoder authorization, releases browser resources, and
// emits dialog dismissal. It is the completion path after successful creation;
// callers needing the pending-creation guard use handleClose instead.
const finishClose = () => {
  stopQoderPolling()
  resetQoderOAuthCompletionState()
  closeQoderAuthPopup()
  emit('close')
}

// handleClose dismisses the dialog unless Qoder provider persistence is still
// pending. Waiting preserves the flow identity needed to publish its eventual
// server response and prevents a premature close event.
const handleClose = () => {
  // Keep the Qoder dialog open while persistence is pending so its server result
  // can publish creation and close the same authorization flow after settlement.
  if (form.platform === 'qoder' && submitting.value) return
  finishClose()
}

// buildOpenAIExtra snapshots the current OpenAI policy controls for the typed
// payload builder. Optional base metadata carries token information, and the
// builder applies platform-specific omission rules without owning reactive
// state.
const buildOpenAIExtra = (base?: Record<string, unknown>) =>
  buildOpenAICreateExtra(
    {
      platform: form.platform,
      type: form.type,
      responsesWSConnectionMode: responsesWSConnectionMode.value,
      openaiPassthroughEnabled: openaiPassthroughEnabled.value,
      openaiFlattenNamespacesEnabled: openaiFlattenNamespacesEnabled.value,
      providerCategory: providerCategory.value,
      openAIOAuthClientPolicy: openAIOAuthClientPolicy.value,
      codexCLIOnlyAllowClaudeCodeEnabled: codexCLIOnlyAllowClaudeCodeEnabled.value,
      autoPause5hThreshold: autoPause5hThreshold.value,
      autoPause7dThreshold: autoPause7dThreshold.value,
      autoPause5hDisabled: autoPause5hDisabled.value,
      autoPause7dDisabled: autoPause7dDisabled.value,
      tlsFingerprintEnabled: tlsFingerprintEnabled.value,
      tlsFingerprintProfileId: tlsFingerprintProfileId.value,
      tlsFingerprintRouterId: tlsFingerprintRouterId.value,
      codexFingerprintMode: codexFingerprintMode.value,
      codexImageResponsesModel: codexImageResponsesModel.value,
      openAICompactMode: openAICompactMode.value,
      openAINativeCompactionV2Mode: openAINativeCompactionV2Mode.value,
      openAIImagesGIFEnabled: openAIImagesGIFEnabled.value,
      openAIImagesURLToB64JSON: openAIImagesURLToB64JSON.value,
      openAIResponsesContinuationSupported: openAIResponsesContinuationSupported.value,
    },
    base,
  )
// buildQoderExtra snapshots Qoder TLS policy for the typed creation payload
// builder.
const buildQoderExtra = () =>
  buildQoderCreateExtra({
    tlsFingerprintEnabled: tlsFingerprintEnabled.value,
    tlsFingerprintProfileId: tlsFingerprintProfileId.value,
  })
// buildAnthropicExtra snapshots Anthropic request policies for the typed
// payload builder. The builder applies them only to API Key providers and
// preserves supplied base metadata for other platforms and categories.
const buildAnthropicExtra = (base?: Record<string, unknown>) =>
  buildAnthropicCreateExtra(
    {
      platform: form.platform,
      providerCategory: providerCategory.value,
      anthropicPassthroughEnabled: anthropicPassthroughEnabled.value,
      anthropicAPIKeyAuthScheme: anthropicAPIKeyAuthScheme.value,
      webSearchEmulationMode: webSearchEmulationMode.value,
    },
    base,
  )
// doCreateProvider checks flow identity before entering shared provider
// submission. An obsolete authorization returns false without a request;
// admitted requests retain the same guard for completion notification and state
// cleanup.
const doCreateProvider = async (
  payload: CreateProviderRequest,
  isCurrent: ProviderCreateGuard = () => true,
): Promise<boolean> => {
  if (!isCurrent()) return false
  return submitCreateProvider(payload, isCurrent)
}

// applyVertexServiceAccountJson validates service-account identity and stores
// normalized JSON. Empty input clears displayed identity fields; invalid JSON
// or missing project, email, or private key reports the relevant field error
// without replacing accepted identity.
const applyVertexServiceAccountJson = (value: string) => {
  const raw = value.trim()
  if (!raw) {
    vertexProjectId.value = ''
    vertexClientEmail.value = ''
    return false
  }
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const projectId = typeof parsed.project_id === 'string' ? parsed.project_id.trim() : ''
    const clientEmail = typeof parsed.client_email === 'string' ? parsed.client_email.trim() : ''
    const privateKey = typeof parsed.private_key === 'string' ? parsed.private_key.trim() : ''
    if (!projectId || !clientEmail || !privateKey) {
      failAt(t('admin.providers.vertexSaJsonMissingFields'), 'vertex-sa-json')
      return false
    }
    vertexProjectId.value = projectId
    vertexClientEmail.value = clientEmail
    vertexServiceAccountJson.value = JSON.stringify(parsed)
    return true
  } catch {
    failAt(t('admin.providers.vertexSaJsonInvalid'), 'vertex-sa-json')
    return false
  }
}

// parseVertexServiceAccountJson validates the currently edited service-account JSON.
// It returns the shared parser result, including false after field navigation
// reports invalid JSON or missing required identity and private-key values.
const parseVertexServiceAccountJson = () =>
  applyVertexServiceAccountJson(vertexServiceAccountJson.value)
// handleVertexServiceAccountFile reads the first selected file and validates
// its JSON. The file input is cleared after reading even when reading fails,
// allowing the same file to be selected again.
const handleVertexServiceAccountFile = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    applyVertexServiceAccountJson(await file.text())
  } finally {
    // Release the file selection after reading so selecting the same corrected
    // file again triggers the input event instead of retaining its previous value.
    input.value = ''
  }
}

// handleVertexServiceAccountDrop ends drag highlighting and reads the first
// dropped file. Missing files leave credentials unchanged, while supplied text
// uses the same service-account validation as pasted or selected-file input.
const handleVertexServiceAccountDrop = async (event: DragEvent) => {
  vertexServiceAccountDragActive.value = false
  const file = event.dataTransfer?.files?.[0]
  if (!file) return
  applyVertexServiceAccountJson(await file.text())
}

// handleSubmit validates the visible provider draft and chooses its creation
// path. OAuth providers enter authorization after temporary-rule validation,
// while manual Qoder, service-account, Bedrock, and API Key categories
// serialize their corresponding credentials immediately. CN endpoints and
// billing mode are preserved until the shared protocol boundary applies native
// capability selection. The request builder adds quota and usage policies
// before persistence, and invalid fields use tab navigation so hidden controls
// remain reachable.
const handleSubmit = async () => {
  // SettingsTabs locates invalid required fields across hidden panels because
  // the form disables native browser validation and must reveal the failing field.
  if (tabsRef.value && !(await tabsRef.value.validate())) return

  // OAuth categories enter authorization after the basic draft passes validation.
  if (isOAuthFlow.value) {
    if (!isGrokSSOInputMethod.value && !form.name.trim()) {
      failAt(t('admin.providers.pleaseEnterProviderName'), 'name')
      return
    }

    // Validate temporary rules before leaving configuration so the relevant tab
    // can be focused; authorization submission validates again before consuming credentials.
    if (form.platform === 'grok' && !validateGrokOAuthUpstreamConfig()) return
    if (tempUnschedEnabled.value && buildTempUnschedRules(tempUnschedRules.value).length === 0) {
      failAt(t('admin.providers.tempUnschedulable.rulesInvalid'), 'temp-unsched')
      return
    }
    step.value = 2
    return
  }

  // For Bedrock type, create directly
  if (form.platform === 'anthropic' && providerCategory.value === 'bedrock') {
    if (!form.name.trim()) {
      failAt(t('admin.providers.pleaseEnterProviderName'), 'name')
      return
    }

    const credentials: Record<string, unknown> = {
      auth_mode: bedrockAuthMode.value,
      aws_region: bedrockRegion.value.trim() || 'us-east-1',
    }

    if (bedrockAuthMode.value === 'sigv4') {
      if (!bedrockAccessKeyId.value.trim()) {
        failAt(t('admin.providers.bedrockAccessKeyIdRequired'), 'bedrock-access-key-id')
        return
      }
      if (!bedrockSecretAccessKey.value.trim()) {
        failAt(t('admin.providers.bedrockSecretAccessKeyRequired'), 'bedrock-secret')
        return
      }
      credentials.aws_access_key_id = bedrockAccessKeyId.value.trim()
      credentials.aws_secret_access_key = bedrockSecretAccessKey.value.trim()
      if (bedrockSessionToken.value.trim()) {
        credentials.aws_session_token = bedrockSessionToken.value.trim()
      }
    } else {
      if (!bedrockApiKeyValue.value.trim()) {
        failAt(t('admin.providers.bedrockApiKeyRequired'), 'bedrock-api-key')
        return
      }
      credentials.api_key = bedrockApiKeyValue.value.trim()
    }

    if (bedrockForceGlobal.value) {
      credentials.aws_force_global = 'true'
    }

    // Model restriction
    applyPersistedModelRestriction(credentials)

    // Pool mode
    if (poolModeEnabled.value) {
      credentials.pool_mode = true
      credentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
      const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(
        poolModeRetryStatusCodesInput.value,
      )
      if (parsedRetryStatusCodes.length > 0) {
        credentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
      }
    }

    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')

    await createProviderAndFinish('anthropic', 'bedrock' as ProviderType, credentials)
    return
  }

  if (form.platform === 'qoder' && qoderProviderType.value === 'manual') {
    if (!form.name.trim()) {
      failAt(t('admin.providers.pleaseEnterProviderName'), 'name')
      return
    }
    const credentials: Record<string, unknown> = {
      site: qoderSite.value,
      refresh_mode: 'cosy',
    }
    if (qoderPAT.value.trim()) {
      credentials.pat = qoderPAT.value.trim()
    } else {
      if (!qoderSecurityOauthToken.value.trim()) {
        failAt(t('admin.providers.qoder.pleaseEnterSecurityOauthToken'), 'qoder-security-token')
        return
      }
      if (!qoderMachineId.value.trim()) {
        failAt(t('admin.providers.qoder.pleaseEnterMachineId'), 'qoder-machine-id')
        return
      }
      if (!qoderUidAid.value.trim()) {
        failAt(t('admin.providers.qoder.pleaseEnterUidAid'), 'qoder-uid-aid')
        return
      }

      const uidAid = qoderUidAid.value.trim()
      credentials.security_oauth_token = qoderSecurityOauthToken.value.trim()
      credentials.machine_id = qoderMachineId.value.trim()
      credentials.uid = uidAid
      credentials.aid = uidAid
      credentials.user_type = qoderUserType.value.trim() || 'personal_standard'
      if (qoderRefreshToken.value.trim()) {
        credentials.refresh_token = qoderRefreshToken.value.trim()
      }
    }
    applyQoderModelRestriction(credentials)
    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
    await createProviderAndFinish('qoder', 'cosy', credentials, buildQoderExtra())
    return
  }

  if (
    (form.platform === 'gemini' || form.platform === 'anthropic') &&
    providerCategory.value === 'service_account'
  ) {
    if (!form.name.trim()) {
      failAt(t('admin.providers.pleaseEnterProviderName'), 'name')
      return
    }
    if (!parseVertexServiceAccountJson()) {
      return
    }
    if (!vertexLocation.value.trim()) {
      failAt(t('admin.providers.vertexLocationRequired'), 'vertex-location')
      return
    }
    const credentials: Record<string, unknown> = {
      service_account_json: vertexServiceAccountJson.value.trim(),
      project_id: vertexProjectId.value.trim(),
      client_email: vertexClientEmail.value.trim(),
      location: vertexLocation.value.trim(),
      tier_id: 'vertex',
    }
    await createProviderAndFinish(form.platform, 'service_account' as ProviderType, credentials)
    return
  }

  // For apikey type, create directly
  if (!apiKeyValue.value.trim()) {
    failAt(t('admin.providers.pleaseEnterApiKey'), 'api-key')
    return
  }

  const enteredBaseUrl = apiKeyBaseUrl.value.trim()
  if (
    form.platform === 'gemini' &&
    geminiProviderType.value === 'third_party' &&
    !isGeminiThirdPartyBaseUrl(enteredBaseUrl)
  ) {
    failAt(t('admin.providers.gemini.connectionSource.thirdPartyBaseUrlRequired'), 'base-url')
    return
  }

  // Build credentials with optional model mapping; a blank address stores the platform default.
  const credentials: Record<string, unknown> = {
    base_url: enteredBaseUrl || apiKeyPreset(form.platform).defaultBaseUrl,
    api_key: apiKeyValue.value.trim(),
  }
  if (modeChoices.value.length > 0) credentials.provider_mode = providerMode.value

  // New API wallet queries use a user access token; keep that secret in
  // Credentials alongside the wallet user ID rather than exposing it through Extra.
  if (upstreamUsageAdapter.value === 'new_api') {
    if (upstreamUsageWalletAccessToken.value.trim()) {
      credentials.new_api_user_access_token = upstreamUsageWalletAccessToken.value.trim()
    }
    if (upstreamUsageWalletUserId.value.trim()) {
      credentials.new_api_user_id = upstreamUsageWalletUserId.value.trim()
    }
  }
  if (form.platform === 'gemini') {
    credentials.provider_type = geminiProviderType.value
    if (geminiProviderType.value === 'official') {
      credentials.tier_id = geminiTierAIStudio.value
    }
  }
  // Root-derived address slots store only administrator overrides, and OpenCode keeps its
  // per-mode model protocol tables beside them.
  const endpointOverrides = derivedEndpointOverrides(form.platform, adaptiveBaseUrls.value)
  if (endpointOverrides) credentials.api_base_urls = endpointOverrides
  if (form.platform === 'opencode') {
    Object.assign(credentials, openCodeCredentials(openCodeProtocols.value))
  }

  // CN credentials carry the protocol selector and per-protocol endpoint settings. The
  // shared creation boundary subsequently replaces the legacy protocol selector
  // with the supported native protocol set before the provider API receives it.
  if (form.platform === 'kimi' || form.platform === 'zhipu' || form.platform === 'deepseek') {
    credentials.api_protocol = apiProtocol.value
    if (apiProtocol.value === 'adaptive') {
      const defaults = defaultCNAdaptiveBaseUrls(form.platform, providerMode.value)
      const protocolBaseUrls: Record<string, string> = {}
      for (const item of cnAdaptiveProtocolOptions.value) {
        protocolBaseUrls[item.value] = (
          adaptiveBaseUrls.value[item.value] || defaults[item.value]
        ).trim()
      }
      credentials.api_base_urls = protocolBaseUrls
      credentials.base_url = protocolBaseUrls.chat_completions
    }
    const resolvedCNBase = (
      apiKeyBaseUrl.value.trim() ||
      defaultCNBaseUrl(form.platform, providerMode.value, apiProtocol.value)
    ).trim()
    if (apiProtocol.value !== 'adaptive' && resolvedCNBase) {
      credentials.base_url = resolvedCNBase
    }
    if (form.platform === 'zhipu' && providerMode.value === 'coding') {
      const organization = zhipuOrganization.value.trim()
      const project = zhipuProject.value.trim()
      if (organization) {
        credentials.zhipu_organization = organization
        if (project) credentials.zhipu_project = project
      }
    }
  }

  // Persist mappings and final whitelist independently so administrator clearing
  // remains explicit rather than being reconstructed from request-side mapping rows.
  applyPersistedModelRestriction(credentials)
  if (form.platform === 'openai') {
    const compactModelMapping = buildOpenAICompactModelMapping()
    if (compactModelMapping) {
      credentials.compact_model_mapping = compactModelMapping
    }
  }

  // Add pool mode if enabled
  if (poolModeEnabled.value) {
    credentials.pool_mode = true
    credentials.pool_mode_retry_count = normalizePoolModeRetryCount(poolModeRetryCount.value)
    const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(
      poolModeRetryStatusCodesInput.value,
    )
    if (parsedRetryStatusCodes.length > 0) {
      credentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
    }
  }

  // Add custom error codes if enabled
  if (customErrorCodesEnabled.value) {
    credentials.custom_error_codes_enabled = true
    credentials.custom_error_codes = [...selectedErrorCodes.value]
  }

  // Apply header overrides only for supported API Key platforms, after validation
  // has accepted their rows and before the shared provider submission boundary.
  if (isHeaderOverrideCapable(form.platform, 'apikey')) {
    if (headerOverrideEnabled.value) {
      const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
      if (headerError) {
        failAt(t(`admin.providers.headerOverride.${headerError}`), 'header-override')
        return
      }
    }
    applyHeaderOverride(
      credentials,
      headerOverrideEnabled.value,
      headerOverrideRows.value,
      'create',
    )
  }

  applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')

  form.credentials = credentials
  const extra = buildAnthropicExtra(buildOpenAIExtra())

  // Route API Key submission through the shared builder so local quota schedules
  // and enabled upstream usage settings accompany credentials in the same request.
  await createProviderAndFinish(form.platform, 'apikey', credentials, extra)
}

// goBackToBasicInfo abandons authorization and returns to configuration while
// preserving provider drafts. It invalidates Qoder work, releases its popup,
// resets every OAuth client, and clears exposed authorization input before the
// next attempt.
const goBackToBasicInfo = () => {
  stopQoderPolling()
  closeQoderAuthPopup()
  resetQoderOAuthCompletionState()
  step.value = 1
  oauth.resetState()
  openaiOAuth.resetState()
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  qoderOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
}

// handleGenerateUrl dispatches authorization generation to the selected
// platform client. OpenAI appends a session for batching, Gemini includes its
// project and tier, and Qoder delegates popup and polling ownership to its
// controller.
const handleGenerateUrl = async () => {
  if (form.platform === 'openai') {
    await openaiOAuth.appendAuthUrl(form.proxy_id)
  } else if (form.platform === 'gemini') {
    await geminiOAuth.generateAuthUrl(
      form.proxy_id,
      oauthFlowRef.value?.projectId,
      geminiOAuthType.value,
      geminiSelectedTier.value,
    )
  } else if (form.platform === 'antigravity') {
    await antigravityOAuth.generateAuthUrl(form.proxy_id)
  } else if (form.platform === 'qoder') {
    await generateQoderAuthUrl()
  } else if (form.platform === 'grok') {
    await grokOAuth.generateAuthUrl(form.proxy_id)
  } else {
    await oauth.generateAuthUrl(addMethod.value, form.proxy_id)
  }
}

// handleRemoveOpenAIAuthSession removes the selected pending session from the
// OpenAI authorization queue.
const handleRemoveOpenAIAuthSession = (sessionId: string) => {
  openaiOAuth.removeAuthSession(sessionId)
}

// handleValidateRefreshToken dispatches manual refresh-token input to supported
// platform handlers. OpenAI, Antigravity, and Grok own their batch validation
// and error reporting; other platforms have no refresh-token action here.
const handleValidateRefreshToken = (rt: string) => {
  if (form.platform === 'openai') {
    handleOpenAIValidateRT(rt)
  } else if (form.platform === 'antigravity') {
    handleAntigravityValidateRT(rt)
  } else if (form.platform === 'grok') {
    handleGrokValidateRT(rt)
  }
}

// handleValidateSessionToken retains the child event handler without issuing a
// validation request.
const handleValidateSessionToken = (_sessionToken: string) => {
  // The child still emits this event, but creation no longer starts a
  // session-token exchange; preserving the handler keeps that event intentionally inert.
}

// Datetime aliases keep template bindings tied to shared formatting and parsing,
// so creation uses the same local-time interpretation as other provider forms.
const formatDateTimeLocal = formatDateTimeLocalInput
const parseDateTimeLocal = parseDateTimeLocalInput

// createProviderAndFinish builds and submits one provider creation payload.
// API Key and Bedrock inputs merge local spending limits and reset schedules
// into Extra, while enabled notification drafts contribute their thresholds.
// Fixed schedules share a timezone and default missing clock fields before
// serialization. Other platform configuration is added before the guarded call.
// The supplied creation guard is checked before submission so an obsolete
// authorization flow cannot complete a dialog that has moved on.
const createProviderAndFinish = async (
  platform: ProviderPlatform,
  type: ProviderType,
  credentials: Record<string, unknown>,
  extra?: Record<string, unknown>,
  isCurrent: ProviderCreateGuard = () => true,
): Promise<boolean> => {
  if (!applyTempUnschedConfig(credentials)) {
    return false
  }

  // Inject quota limits for apikey/bedrock providers
  let finalExtra = withUpstreamRequestIdHeader(extra)
  if (type === 'apikey' || type === 'bedrock') {
    const quotaExtra: Record<string, unknown> = { ...(finalExtra || {}) }
    if (editQuotaLimit.value != null && editQuotaLimit.value > 0) {
      quotaExtra.quota_limit = editQuotaLimit.value
    }
    if (editQuotaDailyLimit.value != null && editQuotaDailyLimit.value > 0) {
      quotaExtra.quota_daily_limit = editQuotaDailyLimit.value
    }
    if (editQuotaWeeklyLimit.value != null && editQuotaWeeklyLimit.value > 0) {
      quotaExtra.quota_weekly_limit = editQuotaWeeklyLimit.value
    }
    if (editQuotaMonthlyLimit.value != null && editQuotaMonthlyLimit.value > 0) {
      quotaExtra.quota_monthly_limit = editQuotaMonthlyLimit.value
    }

    // Quota reset mode config
    if (editDailyResetMode.value === 'fixed') {
      quotaExtra.quota_daily_reset_mode = 'fixed'
      quotaExtra.quota_daily_reset_hour = editDailyResetHour.value ?? 0
    }
    if (editWeeklyResetMode.value === 'fixed') {
      quotaExtra.quota_weekly_reset_mode = 'fixed'
      quotaExtra.quota_weekly_reset_day = editWeeklyResetDay.value ?? 1
      quotaExtra.quota_weekly_reset_hour = editWeeklyResetHour.value ?? 0
    }
    if (editMonthlyResetMode.value === 'fixed') {
      quotaExtra.quota_monthly_reset_mode = 'fixed'
      quotaExtra.quota_monthly_reset_day = editMonthlyResetDay.value ?? 1
      quotaExtra.quota_monthly_reset_hour = editMonthlyResetHour.value ?? 0
    }
    if (
      editDailyResetMode.value === 'fixed' ||
      editWeeklyResetMode.value === 'fixed' ||
      editMonthlyResetMode.value === 'fixed'
    ) {
      quotaExtra.quota_reset_timezone = editResetTimezone.value || 'UTC'
    }

    // Quota notify config
    writeQuotaNotifyToExtra(quotaExtra, 'create')
    if (type === 'apikey') {
      const upstreamConfig: Record<string, unknown> = {
        enabled: upstreamUsageEnabled.value,
        adapter: upstreamUsageAdapter.value,
      }
      if (upstreamUsageBaseUrl.value.trim()) {
        upstreamConfig.base_url = upstreamUsageBaseUrl.value.trim()
      }
      quotaExtra.upstream_usage_query = upstreamConfig
      // A missing policy reads as disabled on the backend, so only enabled reminders are sent.
      const alert = buildUpstreamBalanceAlert(balanceAlert.value, balanceAlertSupported.value)
      if (alert.enabled) {
        quotaExtra.upstream_balance_alert = alert
      }
    }
    if (Object.keys(quotaExtra).length > 0) {
      finalExtra = quotaExtra
    }
  }
  if (platform === 'openai') {
    const compactModelMapping = buildOpenAICompactModelMapping()
    if (compactModelMapping) {
      credentials.compact_model_mapping = compactModelMapping
    } else {
      delete credentials.compact_model_mapping
    }
  }
  if (platform === 'grok') {
    if (!credentials.base_url) {
      credentials.base_url = apiKeyBaseUrl.value.trim() || 'https://api.x.ai/v1'
    }
    applyPersistedModelRestriction(credentials)
  }
  if (!isCurrent()) return false
  return doCreateProvider(
    {
      name: form.name,
      notes: form.notes,
      platform,
      type,
      credentials,
      extra: finalExtra,
      proxy_id: form.proxy_id,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      group_ids: form.group_ids,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value,
    },
    isCurrent,
  )
}

// ensureOpenAITempUnschedConfigReady validates enabled temporary rules before
// OpenAI batches consume tokens. Disabled policy or any valid rule permits
// progress; otherwise the OAuth panel and toast show the validation failure.
const ensureOpenAITempUnschedConfigReady = () => {
  if (!tempUnschedEnabled.value) {
    return true
  }
  if (buildTempUnschedRules(tempUnschedRules.value).length > 0) {
    return true
  }
  const message = t('admin.providers.tempUnschedulable.rulesInvalid')
  openaiOAuth.error.value = message
  appStore.showError(message)
  return false
}

// applyOpenAICreateCredentialPolicies writes the currently selected model and
// temporary-unschedulable policies into mutable credentials. It returns false
// after validation reports invalid enabled rules, so callers stop before sending.
const applyOpenAICreateCredentialPolicies = (credentials: Record<string, unknown>): boolean => {
  applyPersistedModelRestriction(credentials)
  const compactModelMapping = buildOpenAICompactModelMapping()
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  }
  return applyTempUnschedConfig(credentials)
}

// buildOpenAICodexImportCredentialExtras creates policy fields for import endpoints.
// A null result means enabled temporary-unschedulable rules failed validation;
// callers retain the dialog and skip the import request in that case.
const buildOpenAICodexImportCredentialExtras = (): Record<string, unknown> | null => {
  const credentials: Record<string, unknown> = {}
  return applyOpenAICreateCredentialPolicies(credentials) ? credentials : null
}

// handleGrokValidateRT validates newline-separated refresh tokens and creates
// Grok providers sequentially. Forwarding configuration is checked before token
// validation; accepted credentials receive mapping, whitelist, header overrides,
// and temporary-unschedulable rules, while Extra includes the request-ID header.
// Failures are indexed without stopping later entries. Complete success closes
// the dialog, while partial success refreshes the list and retains diagnostics.
// Loading state is released after the batch, including failure paths.
const handleGrokValidateRT = async (refreshTokenInput: string) => {
  if (!refreshTokenInput.trim()) return

  const refreshTokens = refreshTokenInput
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt)

  if (refreshTokens.length === 0) {
    grokOAuth.error.value = t('admin.providers.oauth.grok.pleaseEnterRefreshToken')
    return
  }
  if (!validateGrokOAuthUpstreamConfig()) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []

  try {
    for (let i = 0; i < refreshTokens.length; i++) {
      try {
        const tokenInfo = await grokOAuth.validateRefreshToken(refreshTokens[i], form.proxy_id)
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${grokOAuth.error.value || 'Validation failed'}`)
          grokOAuth.error.value = ''
          continue
        }

        const credentials = grokOAuth.buildCredentials(tokenInfo)
        applyGrokOAuthUpstreamConfig(credentials)
        const extra = grokOAuth.buildExtraInfo(tokenInfo)
        const providerName =
          refreshTokens.length > 1
            ? `${form.name || tokenInfo.email || 'Grok OAuth Provider'} #${i + 1}`
            : form.name || tokenInfo.email || 'Grok OAuth Provider'

        applyPersistedModelRestriction(credentials)
        if (!applyTempUnschedConfig(credentials)) {
          failedCount++
          errors.push(`#${i + 1}: ${t('admin.providers.tempUnschedulable.rulesInvalid')}`)
          continue
        }

        await createProtocolProvider({
          name: providerName,
          notes: form.notes,
          platform: 'grok',
          type: 'oauth',
          credentials,
          extra: withUpstreamRequestIdHeader(extra),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value,
        })
        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
        errors.push(`#${i + 1}: ${errMsg}`)
      }
    }

    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        refreshTokens.length > 1
          ? t('admin.providers.oauth.batchSuccess', { count: successCount })
          : t('admin.providers.providerCreated'),
      )
      emit('created')
      handleClose()
    } else if (successCount > 0) {
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', {
          success: successCount,
          failed: failedCount,
        }),
      )
      grokOAuth.error.value = errors.join('\n')
      emit('created')
    } else {
      grokOAuth.error.value = errors.join('\n')
      appStore.showError(t('admin.providers.oauth.batchFailed'))
    }
  } finally {
    grokOAuth.loading.value = false
  }
}

// handleGrokImportSSO imports one SSO credential per nonempty input line
// through the Grok endpoint. It validates forwarding and temporary-rule policy
// before submission and sends current shared creation fields with credential
// supplements. Server counts decide whether to close after complete success or
// retain diagnostics after partial completion. A partial success still
// refreshes the provider list, while a fully failed import keeps the dialog
// available for correction.
const handleGrokImportSSO = async (ssoInput: string) => {
  // Treat every nonempty line as an import entry, including duplicates, so
  // the returned counts and indexed errors correspond to the administrator's input order.
  const ssoTokens = ssoInput
    .split('\n')
    .map((token) => token.trim())
    .filter((token) => token)
  if (ssoTokens.length === 0) return
  if (!validateGrokOAuthUpstreamConfig()) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  const credentials: Record<string, unknown> = {}
  applyGrokOAuthUpstreamConfig(credentials)
  applyPersistedModelRestriction(credentials)
  if (!applyTempUnschedConfig(credentials)) {
    grokOAuth.loading.value = false
    return
  }

  try {
    const result = await adminAPI.grok.createFromSSO({
      sso_tokens: ssoTokens,
      name: form.name || undefined,
      notes: form.notes || undefined,
      proxy_id: form.proxy_id,
      group_ids: form.group_ids,
      credentials,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value,
    })

    const successCount = result.created?.length || 0
    const failedCount = result.failed?.length || 0
    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        ssoTokens.length > 1
          ? t('admin.providers.oauth.batchSuccess', { count: successCount })
          : t('admin.providers.providerCreated'),
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      // Partial success refreshes the provider list while preserving input and
      // failed-entry diagnostics, allowing the administrator to inspect and retry remaining entries.
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', {
          success: successCount,
          failed: failedCount,
        }),
      )
      grokOAuth.error.value = (result.failed || [])
        .map((item) => `#${item.index}: ${item.error || 'Unknown error'}`)
        .join('\n')
      emit('created')
    } else {
      grokOAuth.error.value =
        (result.failed || [])
          .map((item) => `#${item.index}: ${item.error || 'Unknown error'}`)
          .join('\n') || t('admin.providers.oauth.grok.failedToConvertSSO')
      appStore.showError(t('admin.providers.oauth.batchFailed'))
    }
  } catch (error: any) {
    grokOAuth.error.value =
      error.response?.data?.detail ||
      error.message ||
      t('admin.providers.oauth.grok.failedToConvertSSO')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

// handleAntigravityValidateRT validates newline-separated refresh tokens and
// persists each successful Antigravity account. Accepted credentials receive the
// project identity override, and createProtocolProvider adds the current model
// whitelist and native protocols. Extra carries the optional request-ID header.
// Individual failures are retained while later inputs continue, and generated
// batch names preserve their input positions. Complete success closes the dialog;
// partial success refreshes the provider list while leaving input and errors visible.
const handleAntigravityValidateRT = async (refreshTokenInput: string) => {
  if (!refreshTokenInput.trim()) return

  // Parse multiple refresh tokens (one per line)
  const refreshTokens = refreshTokenInput
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt)

  if (refreshTokens.length === 0) {
    antigravityOAuth.error.value = t('admin.providers.oauth.antigravity.pleaseEnterRefreshToken')
    return
  }

  antigravityOAuth.loading.value = true
  antigravityOAuth.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []

  try {
    for (let i = 0; i < refreshTokens.length; i++) {
      try {
        const tokenInfo = await antigravityOAuth.validateRefreshToken(
          refreshTokens[i],
          form.proxy_id,
        )
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${antigravityOAuth.error.value || 'Validation failed'}`)
          antigravityOAuth.error.value = ''
          continue
        }

        const credentials = antigravityOAuth.buildCredentials(tokenInfo, refreshTokens[i])
        applyAntigravityProjectID(credentials, antigravityProjectId.value, 'create')

        // Generate provider name with index for batch
        const providerName = refreshTokens.length > 1 ? `${form.name} #${i + 1}` : form.name

        // Note: Antigravity doesn't have buildExtraInfo, so we pass empty extra or rely on credentials
        const createPayload: CreateProviderRequest = {
          name: providerName,
          notes: form.notes,
          platform: 'antigravity',
          type: 'oauth',
          credentials,
          extra: withUpstreamRequestIdHeader({}),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value,
        }
        await createProtocolProvider(createPayload)
        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || 'Unknown error'
        errors.push(`#${i + 1}: ${errMsg}`)
      }
    }

    // Show results
    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        refreshTokens.length > 1
          ? t('admin.providers.oauth.batchSuccess', { count: successCount })
          : t('admin.providers.providerCreated'),
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', {
          success: successCount,
          failed: failedCount,
        }),
      )
      antigravityOAuth.error.value = errors.join('\n')
      emit('created')
    } else {
      antigravityOAuth.error.value = errors.join('\n')
      appStore.showError(t('admin.providers.oauth.batchFailed'))
    }
  } finally {
    antigravityOAuth.loading.value = false
  }
}

// handleGeminiExchange exchanges the current code with the selected Gemini
// session and callback state. Token credentials and account metadata pass through
// shared creation, which adds model restrictions, temporary-unschedulable rules,
// and native protocols. Failures remain in the OAuth error panel and loading clears.
const handleGeminiExchange = async (authCode: string) => {
  if (!authCode.trim() || !geminiOAuth.sessionId.value) return

  geminiOAuth.loading.value = true
  geminiOAuth.error.value = ''

  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || geminiOAuth.state.value
    if (!stateToUse) {
      geminiOAuth.error.value = t('admin.providers.oauth.authFailed')
      appStore.showError(geminiOAuth.error.value)
      return
    }

    const tokenInfo = await geminiOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId: geminiOAuth.sessionId.value,
      state: stateToUse,
      proxyId: form.proxy_id,
      oauthType: geminiOAuthType.value,
      tierId: geminiSelectedTier.value,
    })
    if (!tokenInfo) return

    const credentials = geminiOAuth.buildCredentials(tokenInfo)
    const extra = geminiOAuth.buildExtraInfo(tokenInfo)
    await createProviderAndFinish('gemini', 'oauth', credentials, extra)
  } catch (error: any) {
    geminiOAuth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(geminiOAuth.error.value)
  } finally {
    geminiOAuth.loading.value = false
  }
}

// handleAntigravityExchange exchanges the current Antigravity code and submits
// its provider. It applies project identity, mapping mode, independent
// whitelist, warmup interception, and enabled overages before creation,
// retaining reported failures in the authorization panel.
const handleAntigravityExchange = async (authCode: string) => {
  if (!authCode.trim() || !antigravityOAuth.sessionId.value) return

  antigravityOAuth.loading.value = true
  antigravityOAuth.error.value = ''

  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || antigravityOAuth.state.value
    if (!stateToUse) {
      antigravityOAuth.error.value = t('admin.providers.oauth.authFailed')
      appStore.showError(antigravityOAuth.error.value)
      return
    }

    const tokenInfo = await antigravityOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId: antigravityOAuth.sessionId.value,
      state: stateToUse,
      proxyId: form.proxy_id,
    })
    if (!tokenInfo) return

    const credentials = antigravityOAuth.buildCredentials(tokenInfo)
    applyAntigravityProjectID(credentials, antigravityProjectId.value, 'create')
    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
    // Antigravity mapping serialization uses mapping mode regardless of the UI
    // selection, while its separate whitelist continues to control final model admission.
    const antigravityModelMapping = buildModelMappingObject(
      'mapping',
      [],
      antigravityModelMappings.value,
    )
    if (antigravityModelMapping) {
      credentials.model_mapping = antigravityModelMapping
    }
    credentials.model_whitelist = [...antigravityWhitelistModels.value]
    const extra = buildAntigravityExtra()
    await createProviderAndFinish('antigravity', 'oauth', credentials, extra)
  } catch (error: any) {
    antigravityOAuth.error.value =
      error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(antigravityOAuth.error.value)
  } finally {
    antigravityOAuth.loading.value = false
  }
}

// handleGrokExchange validates forwarding settings before exchanging the
// current Grok code. Successful credentials receive endpoint and header overrides,
// model mapping and whitelist, and temporary-unschedulable rules through shared
// creation. Invalid local policy stops persistence while retaining the dialog.
const handleGrokExchange = async (authCode: string) => {
  if (!authCode.trim() || !grokOAuth.sessionId.value) return
  if (!validateGrokOAuthUpstreamConfig()) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''

  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || grokOAuth.state.value
    if (!stateToUse) {
      grokOAuth.error.value = t('admin.providers.oauth.authFailed')
      appStore.showError(grokOAuth.error.value)
      return
    }

    const tokenInfo = await grokOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId: grokOAuth.sessionId.value,
      state: stateToUse,
      proxyId: form.proxy_id,
    })
    if (!tokenInfo) return

    const credentials = grokOAuth.buildCredentials(tokenInfo)
    applyGrokOAuthUpstreamConfig(credentials)
    const extra = grokOAuth.buildExtraInfo(tokenInfo)
    await createProviderAndFinish('grok', 'oauth', credentials, extra)
  } catch (error: any) {
    grokOAuth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

// handleAnthropicExchange exchanges the selected OAuth or setup-token
// authorization code. It merges token metadata with enabled Anthropic limits and
// request options, then applies warmup interception to copied token credentials.
// Shared creation validates temporary-unschedulable rules, adds the request-ID
// header override, and applies the current native protocol selection before
// persistence. Failures remain in the authorization panel, and the OAuth loading
// flag is released when exchange or persistence settles.
const handleAnthropicExchange = async (authCode: string) => {
  if (!authCode.trim() || !oauth.sessionId.value) return

  oauth.loading.value = true
  oauth.error.value = ''

  try {
    const proxyConfig = form.proxy_id ? { proxy_id: form.proxy_id } : {}
    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/providers/exchange-code'
        : '/admin/providers/exchange-setup-token-code'

    const tokenInfo = await adminAPI.providers.exchangeCode(endpoint, {
      session_id: oauth.sessionId.value,
      code: authCode.trim(),
      ...proxyConfig,
    })

    // Build extra with quota control settings
    const baseExtra = oauth.buildExtraInfo(tokenInfo) || {}
    const extra: Record<string, unknown> = { ...baseExtra }

    // Add window cost limit settings
    if (windowCostEnabled.value && windowCostLimit.value != null && windowCostLimit.value > 0) {
      extra.window_cost_limit = windowCostLimit.value
      extra.window_cost_sticky_reserve = windowCostStickyReserve.value ?? 10
    }

    // Add session limit settings
    if (sessionLimitEnabled.value && maxSessions.value != null && maxSessions.value > 0) {
      extra.max_sessions = maxSessions.value
      extra.session_idle_timeout_minutes = sessionIdleTimeout.value ?? 5
    }

    // Add RPM limit settings
    if (rpmLimitEnabled.value) {
      // Missing or nonpositive RPM input uses fifteen requests per minute.
      const DEFAULT_BASE_RPM = 15
      extra.base_rpm = baseRpm.value != null && baseRpm.value > 0 ? baseRpm.value : DEFAULT_BASE_RPM
      extra.rpm_strategy = rpmStrategy.value
      if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
        extra.rpm_sticky_buffer = rpmStickyBuffer.value
      }
    }

    // User-message queue mode is configured independently of the RPM limiter.
    if (userMsgQueueMode.value) {
      extra.user_msg_queue_mode = userMsgQueueMode.value
    }

    // Add TLS fingerprint settings
    if (tlsFingerprintEnabled.value) {
      extra.enable_tls_fingerprint = true
      if (tlsFingerprintProfileId.value) {
        extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
      }
      if (
        form.platform === 'openai' &&
        providerCategory.value === 'oauth-based' &&
        tlsFingerprintRouterId.value
      ) {
        extra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
      }
    }

    // Add session ID masking settings
    if (sessionIdMaskingEnabled.value) {
      extra.session_id_masking_enabled = true
    }

    // Add cache TTL override settings
    if (cacheTTLOverrideEnabled.value) {
      extra.cache_ttl_override_enabled = true
      extra.cache_ttl_override_target = cacheTTLOverrideTarget.value
    }

    // Add custom base URL settings
    if (customBaseUrlEnabled.value && customBaseUrl.value.trim()) {
      extra.custom_base_url_enabled = true
      extra.custom_base_url = customBaseUrl.value.trim()
    }

    const credentials: Record<string, unknown> = { ...tokenInfo }
    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
    await createProviderAndFinish(
      form.platform,
      addMethod.value as ProviderType,
      credentials,
      extra,
    )
  } catch (error: any) {
    oauth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(oauth.error.value)
  } finally {
    oauth.loading.value = false
  }
}

// handleExchangeCode dispatches the child's authorization code to the selected
// platform handler, which validates its required input. Grok code exchange uses
// handleGrokExchange; the child's separate import-sso event invokes handleGrokImportSSO.
const handleExchangeCode = async () => {
  const authCode = oauthFlowRef.value?.authCode || ''

  switch (form.platform) {
    case 'openai':
      return handleOpenAIExchange(authCode)
    case 'gemini':
      return handleGeminiExchange(authCode)
    case 'antigravity':
      return handleAntigravityExchange(authCode)
    case 'qoder':
      return handleQoderExchange(authCode)
    case 'grok':
      return handleGrokExchange(authCode)
    default:
      return handleAnthropicExchange(authCode)
  }
}

// handleCookieAuth processes nonempty session-key lines sequentially for
// Anthropic authorization. Each successful key receives the currently selected
// request policies and provider metadata before persistence; later entries
// still run after individual failures. Batch names preserve source order,
// successful entries trigger a list refresh, and the dialog closes only when
// every input succeeds. Failed entries remain indexed in the OAuth panel so
// administrators can inspect partial results without losing the form.
const handleCookieAuth = async (sessionKey: string) => {
  oauth.loading.value = true
  oauth.error.value = ''

  try {
    const proxyConfig = form.proxy_id ? { proxy_id: form.proxy_id } : {}
    const keys = oauth.parseSessionKeys(sessionKey)

    if (keys.length === 0) {
      oauth.error.value = t('admin.providers.oauth.pleaseEnterSessionKey')
      return
    }

    const tempUnschedPayload = tempUnschedEnabled.value
      ? buildTempUnschedRules(tempUnschedRules.value)
      : []
    if (tempUnschedEnabled.value && tempUnschedPayload.length === 0) {
      failAt(t('admin.providers.tempUnschedulable.rulesInvalid'), 'temp-unsched')
      return
    }

    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/providers/cookie-auth'
        : '/admin/providers/setup-token-cookie-auth'

    let successCount = 0
    let failedCount = 0
    const errors: string[] = []

    for (let i = 0; i < keys.length; i++) {
      try {
        const tokenInfo = await adminAPI.providers.exchangeCode(endpoint, {
          session_id: '',
          code: keys[i],
          ...proxyConfig,
        })

        // Build extra with quota control settings
        const baseExtra = oauth.buildExtraInfo(tokenInfo) || {}
        const extra: Record<string, unknown> = { ...baseExtra }

        // Add window cost limit settings
        if (windowCostEnabled.value && windowCostLimit.value != null && windowCostLimit.value > 0) {
          extra.window_cost_limit = windowCostLimit.value
          extra.window_cost_sticky_reserve = windowCostStickyReserve.value ?? 10
        }

        // Add session limit settings
        if (sessionLimitEnabled.value && maxSessions.value != null && maxSessions.value > 0) {
          extra.max_sessions = maxSessions.value
          extra.session_idle_timeout_minutes = sessionIdleTimeout.value ?? 5
        }

        // Add RPM limit settings
        if (rpmLimitEnabled.value) {
          // Missing or nonpositive RPM input uses fifteen requests per minute.
          const DEFAULT_BASE_RPM = 15
          extra.base_rpm =
            baseRpm.value != null && baseRpm.value > 0 ? baseRpm.value : DEFAULT_BASE_RPM
          extra.rpm_strategy = rpmStrategy.value
          if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
            extra.rpm_sticky_buffer = rpmStickyBuffer.value
          }
        }

        // User-message queue mode is configured independently of the RPM limiter.
        if (userMsgQueueMode.value) {
          extra.user_msg_queue_mode = userMsgQueueMode.value
        }

        // Add TLS fingerprint settings
        if (tlsFingerprintEnabled.value) {
          extra.enable_tls_fingerprint = true
          if (tlsFingerprintProfileId.value) {
            extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
          }
          if (
            form.platform === 'openai' &&
            providerCategory.value === 'oauth-based' &&
            tlsFingerprintRouterId.value
          ) {
            extra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
          }
        }

        // Add session ID masking settings
        if (sessionIdMaskingEnabled.value) {
          extra.session_id_masking_enabled = true
        }

        // Add cache TTL override settings
        if (cacheTTLOverrideEnabled.value) {
          extra.cache_ttl_override_enabled = true
          extra.cache_ttl_override_target = cacheTTLOverrideTarget.value
        }

        // Add custom base URL settings
        if (customBaseUrlEnabled.value && customBaseUrl.value.trim()) {
          extra.custom_base_url_enabled = true
          extra.custom_base_url = customBaseUrl.value.trim()
        }

        const providerName = keys.length > 1 ? `${form.name} #${i + 1}` : form.name

        const credentials: Record<string, unknown> = { ...tokenInfo }
        applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
        if (tempUnschedEnabled.value) {
          credentials.temp_unschedulable_enabled = true
          credentials.temp_unschedulable_rules = tempUnschedPayload
        }

        await createProtocolProvider({
          name: providerName,
          notes: form.notes,
          platform: form.platform,
          type: addMethod.value, // Use addMethod as type: 'oauth' or 'setup-token'
          credentials,
          extra: withUpstreamRequestIdHeader(extra),
          proxy_id: form.proxy_id,
          concurrency: form.concurrency,
          load_factor: form.load_factor ?? undefined,
          priority: form.priority,
          rate_multiplier: form.rate_multiplier,
          group_ids: form.group_ids,
          expires_at: form.expires_at,
          auto_pause_on_expired: autoPauseOnExpired.value,
        })

        successCount++
      } catch (error: any) {
        failedCount++
        errors.push(
          t('admin.providers.oauth.keyAuthFailed', {
            index: i + 1,
            error: error.response?.data?.detail || t('admin.providers.oauth.authFailed'),
          }),
        )
      }
    }

    if (successCount > 0) {
      appStore.showSuccess(t('admin.providers.oauth.successCreated', { count: successCount }))
      if (failedCount === 0) {
        emit('created')
        handleClose()
      } else {
        emit('created')
      }
    }

    if (failedCount > 0) {
      oauth.error.value = errors.join('\n')
    }
  } catch (error: any) {
    oauth.error.value = error.response?.data?.detail || t('admin.providers.oauth.cookieAuthFailed')
  } finally {
    oauth.loading.value = false
  }
}
</script>

<style scoped>
.provider-choice-badge {
  @apply rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300;
}
</style>
