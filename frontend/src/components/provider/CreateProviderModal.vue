<template>
  <BaseDialog
    :show="show"
    :title="t('admin.providers.createProvider')"
    width="wide"
    :body-scroll="false"
    @close="handleClose"
  >
    <!-- OAuth 类账号的两步流程指示 -->
    <div v-if="isOAuthFlow" class="mb-4 flex shrink-0 items-center justify-center">
      <div class="flex items-center gap-4">
        <div class="flex items-center gap-2">
          <span
            :class="[
              'flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold',
              step >= 1 ? 'bg-primary-600 text-white' : 'bg-gray-200 text-gray-500 dark:bg-dark-600'
            ]"
          >1</span>
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.providers.oauth.authMethod') }}</span>
        </div>
        <div class="h-0.5 w-8 bg-gray-300 dark:bg-dark-600" />
        <div class="flex items-center gap-2">
          <span
            :class="[
              'flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold',
              step >= 2 ? 'bg-primary-600 text-white' : 'bg-gray-200 text-gray-500 dark:bg-dark-600'
            ]"
          >2</span>
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ oauthStepTitle }}</span>
        </div>
      </div>
    </div>

    <!-- 第 1 步：账号设置 -->
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
                <label for="create-provider-name" class="input-label">{{ t('admin.providers.providerName') }}</label>
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
                <label for="create-provider-expires-at" class="input-label">{{ t('admin.providers.expiresAt') }}</label>
                <input id="create-provider-expires-at" v-model="expiresAtInput" type="datetime-local" class="input" />
                <p class="input-hint">{{ t('admin.providers.expiresAtHint') }}</p>
              </div>
              <div class="md:col-span-2">
                <label for="create-provider-notes" class="input-label">{{ t('admin.providers.notes') }}</label>
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
            <!-- 平台选择：国际平台与国产供应商分两行 -->
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
                  <!-- 图标始终使用平台品牌色；选中项文字同色，颜色放在 span 上以免被分段控件的悬停色覆盖。 -->
                  <PlatformIcon :platform="item.value" size="sm" :class="platformIconClass(item.value)" />
                  <span :class="form.platform === item.value && platformTextClass(item.value)">{{ item.label }}</span>
                </button>
              </div>
            </div>

            <SettingsNotice v-if="form.platform === 'jev'" data-testid="jev-provider-type-api-key">
              {{ t('admin.providers.jev.description') }}
            </SettingsNotice>

            <!-- 账号类型（Anthropic） -->
            <div v-if="form.platform === 'anthropic'" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.providerType') }}</span>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup" data-tour="provider-form-type">
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

            <!-- 账号类型（OpenAI） -->
            <div v-if="form.platform === 'openai'" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.providerType') }}</span>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup" data-tour="provider-form-type">
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

            <!-- 账号类型（Grok） -->
            <div v-if="form.platform === 'grok'" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.providerType') }}</span>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup" data-tour="provider-form-type">
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

            <!-- 国产供应商接入模式 -->
            <div v-if="isCNPlatform" class="space-y-2">
              <span class="input-label">{{ t('admin.providers.cnProviders.providerMode.title') }}</span>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2" role="radiogroup" data-tour="provider-form-mode">
                <ProviderChoiceCard
                  :accent="form.platform"
                  :selected="providerMode === 'payg'"
                  icon="creditCard"
                  :title="t('admin.providers.cnProviders.providerMode.payg')"
                  :description="t('admin.providers.cnProviders.providerMode.paygDesc')"
                  @click="providerMode = 'payg'"
                />
                <!-- Coding Plan 支持 Kimi 和智谱。 -->
                <ProviderChoiceCard
                  :accent="form.platform"
                  v-if="form.platform !== 'deepseek'"
                  :selected="providerMode === 'coding'"
                  icon="bolt"
                  :title="t('admin.providers.cnProviders.providerMode.coding')"
                  :description="t('admin.providers.cnProviders.providerMode.codingDesc')"
                  @click="providerMode = 'coding'"
                />
              </div>
            </div>

            <!-- 智谱团队版 Coding Plan：组织/项目 ID 可选，填写组织 ID 后切换团队额度端点。 -->
            <div v-if="form.platform === 'zhipu' && providerMode === 'coding'" v-content-reveal class="space-y-2">
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
                  <label for="create-zhipu-organization" class="input-label">{{ t('admin.providers.cnProviders.zhipuTeam.organization') }}</label>
                  <input
                    id="create-zhipu-organization"
                    v-model="zhipuOrganization"
                    type="text"
                    class="input"
                    :placeholder="t('admin.providers.cnProviders.zhipuTeam.organizationPlaceholder')"
                  />
                </div>
                <div>
                  <label for="create-zhipu-project" class="input-label">{{ t('admin.providers.cnProviders.zhipuTeam.project') }}</label>
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

            <!-- 账号类型（Gemini） -->
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
                <div class="grid grid-cols-1 gap-2 sm:grid-cols-3" role="radiogroup" data-tour="provider-form-type">
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
                <SettingsNotice v-if="providerCategory === 'apikey' && geminiProviderType === 'official'">
                  <p>{{ t('admin.providers.gemini.providerType.apiKeyNote') }}</p>
                  <a
                    :href="geminiHelpLinks.apiKey"
                    class="font-medium text-primary-600 hover:underline dark:text-primary-500"
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

              <!-- Gemini OAuth 授权类型 -->
              <div v-if="providerCategory === 'oauth-based'" v-content-reveal class="space-y-2">
                <span class="input-label">{{ t('admin.providers.oauth.gemini.oauthTypeLabel') }}</span>
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
                      <span class="provider-choice-badge">{{ t('admin.providers.gemini.oauthType.badges.personal') }}</span>
                      <span class="provider-choice-badge">{{ t('admin.providers.gemini.oauthType.badges.noGcp') }}</span>
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
                        class="ml-1 text-primary-600 hover:underline dark:text-primary-500"
                        target="_blank"
                        rel="noreferrer"
                        @click.stop
                      >
                        {{ t('admin.providers.gemini.oauthType.gcpProjectLink') }}
                      </a>
                    </span>
                    <span class="mt-2 flex flex-wrap gap-1">
                      <span class="provider-choice-badge">{{ t('admin.providers.gemini.oauthType.badges.enterprise') }}</span>
                      <span class="provider-choice-badge">{{ t('admin.providers.gemini.oauthType.badges.highConcurrency') }}</span>
                    </span>
                  </ProviderChoiceCard>
                </div>

                <!-- 自建 OAuth Client 属于高级选项，默认收起。 -->
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
                  <span>{{ showAdvancedOAuth ? t('admin.providers.gemini.oauthType.hideAdvanced') : t('admin.providers.gemini.oauthType.showAdvanced') }}</span>
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
                        <span class="provider-choice-badge">{{ t('admin.providers.gemini.oauthType.badges.orgManaged') }}</span>
                        <span class="provider-choice-badge">{{ t('admin.providers.gemini.oauthType.badges.adminRequired') }}</span>
                      </span>
                    </ProviderChoiceCard>
                    <SettingsNotice v-if="!geminiAIStudioOAuthEnabled" tone="warning">
                      {{ t('admin.providers.oauth.gemini.aiStudioNotConfiguredTip') }}
                    </SettingsNotice>
                  </div>
                </Collapse>
              </div>

              <!-- 自动识别失败时使用的配额档位 -->
              <div v-if="providerCategory === 'oauth-based'" v-content-reveal>
                <label for="create-gemini-oauth-tier" class="input-label">{{ t('admin.providers.gemini.tier.label') }}</label>
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

            <!-- Qoder 站点必须在登录方式之前冻结。 -->
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

            <!-- Anthropic OAuth 的添加方式 -->
            <div v-if="form.platform === 'anthropic' && isOAuthFlow" v-content-reveal class="space-y-2">
              <span class="input-label">{{ t('admin.providers.addMethod') }}</span>
              <SettingsSegmented
                v-model="addMethod"
                :ariaLabel="t('admin.providers.addMethod')"
                :options="addMethodOptions"
              />
            </div>
          </SettingsSection>

          <SettingsSection v-if="showCredentialsSection" :title="t('admin.providers.sections.credentials')">
            <!-- Antigravity OAuth 项目 ID -->
            <div v-if="form.platform === 'antigravity'" v-content-reveal>
              <label for="create-antigravity-project-id" class="input-label">{{ t('admin.providers.antigravityProjectIdLabel') }}</label>
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

            <!-- Qoder 手动凭据 -->
            <div v-if="form.platform === 'qoder' && qoderProviderType === 'manual'" v-content-reveal class="space-y-4">
              <div data-provider-field="qoder-pat">
                <label for="create-qoder-pat" class="input-label">{{ t('admin.providers.qoder.pat') }}</label>
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
                <span class="text-xs uppercase tracking-wide text-gray-400">{{ t('common.or') }}</span>
                <div class="h-px flex-1 bg-gray-200 dark:bg-dark-600"></div>
              </div>
              <div data-provider-field="qoder-security-token">
                <label for="create-qoder-security-token" class="input-label">{{ t('admin.providers.qoder.securityOauthToken') }}</label>
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
                  <label for="create-qoder-machine-id" class="input-label">{{ t('admin.providers.qoder.machineId') }}</label>
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
                  <label for="create-qoder-uid-aid" class="input-label">{{ t('admin.providers.qoder.uidAid') }}</label>
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
                  <label for="create-qoder-refresh-token" class="input-label">{{ t('admin.providers.qoder.refreshToken') }}</label>
                  <input
                    id="create-qoder-refresh-token"
                    v-model="qoderRefreshToken"
                    type="password"
                    class="input font-mono"
                    autocomplete="off"
                  />
                </div>
                <div>
                  <label for="create-qoder-user-type" class="input-label">{{ t('admin.providers.qoder.userType') }}</label>
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
                      : 'border-gray-300 bg-gray-50 hover:border-primary-400 dark:border-dark-500 dark:bg-dark-800/40 dark:hover:border-primary-500/60'
                  ]"
                  @dragenter.prevent="vertexServiceAccountDragActive = true"
                  @dragover.prevent="vertexServiceAccountDragActive = true"
                  @dragleave.prevent="vertexServiceAccountDragActive = false"
                  @drop.prevent="handleVertexServiceAccountDrop"
                >
                  <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div class="min-w-0">
                      <div class="flex items-center gap-2 text-sm font-medium text-primary-900 dark:text-dark-50">
                        <Icon name="upload" size="sm" />
                        <span>{{ vertexClientEmail ? t('admin.providers.vertexSaJsonLoaded') : t('admin.providers.vertexSaJsonDrop') }}</span>
                      </div>
                      <p class="input-hint">
                        {{ vertexClientEmail ? t('admin.providers.vertexSaJsonKeyHidden') : t('admin.providers.vertexSaJsonDropHint') }}
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
                    <div class="truncate">{{ t('admin.providers.vertexProjectId') }}: <span class="font-mono">{{ vertexProjectId }}</span></div>
                    <div class="truncate">{{ t('admin.providers.vertexClientEmail') }}: <span class="font-mono">{{ vertexClientEmail }}</span></div>
                  </div>
                </div>
                <p class="input-hint">{{ t('admin.providers.vertexSaJsonUploadHint') }}</p>
              </div>

              <div class="grid gap-4 md:grid-cols-2">
                <div>
                  <label for="create-vertex-project-id" class="input-label">{{ t('admin.providers.vertexProjectId') }}</label>
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
                  <label for="create-vertex-location" class="input-label">{{ t('admin.providers.vertexLocation') }}</label>
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

            <!-- API Key 账号（Antigravity 使用上方的上游字段） -->
            <template v-if="isApiKeyCredentials">
              <div v-if="form.platform === 'gemini'" v-content-reveal>
                <label for="create-gemini-provider-type" class="input-label">{{ t('admin.providers.gemini.connectionSource.label') }}</label>
                <Select
                  id="create-gemini-provider-type"
                  v-model="geminiProviderType"
                  :options="geminiProviderTypeOptions"
                  data-testid="create-gemini-provider-type"
                />
                <p class="input-hint">{{ geminiProviderTypeHint }}</p>
              </div>
              <div v-if="!isCNPlatform || apiProtocol !== 'adaptive'" data-provider-field="base-url">
                <label for="create-provider-base-url" class="input-label">{{ t('admin.providers.baseUrl') }}</label>
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
              <div v-else class="space-y-4">
                <p class="input-label mb-0">{{ t('admin.providers.cnProviders.apiProtocol.endpoints') }}</p>
                <div v-for="item in cnAdaptiveProtocolOptions" :key="item.value">
                  <label :for="`create-provider-endpoint-${item.value}`" class="input-label">
                    {{ t(`admin.providers.cnProviders.apiProtocol.${item.labelKey}`) }}
                  </label>
                  <input
                    :id="`create-provider-endpoint-${item.value}`"
                    v-model="adaptiveBaseUrls[item.value]"
                    type="text"
                    class="input"
                    :data-testid="`cn-adaptive-base-url-${item.value}`"
                  />
                </div>
                <p v-if="!cnSupportsNativeResponses(form.platform)" class="input-hint">
                  {{ t('admin.providers.cnProviders.apiProtocol.responsesFallbackDesc') }}
                </p>
              </div>
              <div class="grid gap-4 md:grid-cols-2">
                <div data-provider-field="api-key" :class="{ 'md:col-span-2': !showGeminiApiKeyTier }">
                  <label for="create-provider-api-key" class="input-label">{{ t('admin.providers.apiKeyRequired') }}</label>
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
                  <label for="create-gemini-tier" class="input-label">{{ t('admin.providers.gemini.tier.label') }}</label>
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

            <!-- Bedrock：SigV4 与 API Key 两种鉴权 -->
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
                  <label for="create-bedrock-access-key-id" class="input-label">{{ t('admin.providers.bedrockAccessKeyId') }}</label>
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
                  <label for="create-bedrock-secret" class="input-label">{{ t('admin.providers.bedrockSecretAccessKey') }}</label>
                  <input
                    id="create-bedrock-secret"
                    v-model="bedrockSecretAccessKey"
                    type="password"
                    required
                    class="input font-mono"
                  />
                </div>
                <div>
                  <label for="create-bedrock-session-token" class="input-label">{{ t('admin.providers.bedrockSessionToken') }}</label>
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
                <label for="create-bedrock-api-key" class="input-label">{{ t('admin.providers.bedrockApiKeyInput') }}</label>
                <input
                  id="create-bedrock-api-key"
                  v-model="bedrockApiKeyValue"
                  type="password"
                  required
                  class="input font-mono"
                />
              </div>
              <div>
                <label for="create-bedrock-region" class="input-label">{{ t('admin.providers.bedrockRegion') }}</label>
                <Select id="create-bedrock-region" v-model="bedrockRegion" :options="bedrockRegionOptions" searchable />
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
            @add="touchQoderModelRestriction"
            @remove="touchQoderModelRestriction"
          />

          <!-- Antigravity 白名单与映射分别控制最终范围和请求改写。 -->
          <SettingsSection
            v-if="form.platform === 'antigravity'"
            :title="t('admin.providers.modelRestriction')"
            :hint="t('admin.providers.selectAllowedModels')"
          >
            <ModelWhitelistSelector v-model="antigravityWhitelistModels" platform="antigravity" />
            <ProviderModelMappingEditor
              v-model="antigravityModelMappings"
              wildcard-validation
            />
          </SettingsSection>
        </template>

        <template #scheduling>
          <SettingsSection :title="t('admin.providers.sections.scheduling')">
            <div class="grid gap-4 md:grid-cols-2">
              <div>
                <label for="create-provider-concurrency" class="input-label">{{ t('admin.providers.concurrency') }}</label>
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
                <label for="create-provider-load-factor" class="input-label">{{ t('admin.providers.loadFactor') }}</label>
                <input
                  id="create-provider-load-factor"
                  v-model.number="form.load_factor"
                  type="number"
                  min="1"
                  class="input"
                  :placeholder="String(form.concurrency || 1)"
                  @input="form.load_factor = (form.load_factor && form.load_factor >= 1) ? form.load_factor : null"
                />
                <p class="input-hint">{{ t('admin.providers.loadFactorHint') }}</p>
              </div>
              <div>
                <label for="create-provider-priority" class="input-label">{{ t('admin.providers.priority') }}</label>
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
                <label for="create-provider-rate-multiplier" class="input-label">{{ t('admin.providers.billingRateMultiplier') }}</label>
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

          <TempUnschedFields v-model:enabled="tempUnschedEnabled" v-model:rules="tempUnschedRules" />

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
                  <label for="create-provider-auto-pause-5h" class="input-label">{{ t('admin.providers.autoPause5hThreshold') }}</label>
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
                  <label for="create-provider-auto-pause-7d" class="input-label">{{ t('admin.providers.autoPause7dThreshold') }}</label>
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
            :hint="form.platform === 'anthropic' ? t('admin.providers.quotaControl.hint') : t('admin.providers.quotaLimitHint')"
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
            v-if="form.type === 'apikey'"
            :enabled="upstreamUsageEnabled"
            :adapter="upstreamUsageAdapter"
            :base-url="upstreamUsageBaseUrl"
            :wallet-access-token="upstreamUsageWalletAccessToken"
            :wallet-user-id="upstreamUsageWalletUserId"
            :automatic-adapter="isCNPlatform"
            @update:enabled="upstreamUsageEnabled = $event"
            @update:adapter="upstreamUsageAdapter = $event"
            @update:base-url="upstreamUsageBaseUrl = $event"
            @update:wallet-access-token="upstreamUsageWalletAccessToken = $event"
            @update:wallet-user-id="upstreamUsageWalletUserId = $event"
          />
        </template>

        <template #request>
          <ProviderProtocolSelector
            v-model="upstreamProtocols"
            :platform="form.platform"
            :type="form.type"
            :auth-mode="oauthFlowRef?.inputMethod === 'codex_pat' ? 'personalAccessToken' : oauthFlowRef?.inputMethod === 'agent_identity' ? 'agentIdentity' : ''"
          />

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

          <!-- Grok OAuth 的自定义地址用于请求转发，授权与刷新使用各自的端点。 -->
          <SettingsSection v-if="form.platform === 'grok' && isOAuthFlow" :title="t('admin.providers.sections.grok')">
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

            <SettingsSection v-if="isOpenAIOAuthCategory" :title="t('admin.providers.sections.openaiClient')">
              <SettingRow
                id="create-openai-client-policy"
                label-for="create-openai-client-policy-select"
                :label="t('admin.providers.openai.clientPolicy')"
                :hint="t('admin.providers.openai.clientPolicyDesc')"
                field
              >
                <Select id="create-openai-client-policy-select" v-model="openAIOAuthClientPolicy" :options="openAIOAuthClientPolicyOptions" />
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
              <Select id="create-anthropic-auth-scheme-select" v-model="anthropicAPIKeyAuthScheme" :options="anthropicAPIKeyAuthSchemeOptions" />
            </SettingRow>
            <!-- 全局关闭网页搜索模拟时隐藏提供商级覆盖。 -->
            <SettingRow
              v-if="webSearchGlobalEnabled"
              id="create-anthropic-web-search"
              label-for="create-anthropic-web-search-select"
              :label="t('admin.providers.anthropic.webSearchEmulation')"
              :hint="t('admin.providers.anthropic.webSearchEmulationDesc')"
              field
            >
              <Select id="create-anthropic-web-search-select" v-model="webSearchEmulationMode" :options="webSearchEmulationOptions" />
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

    <!-- 第 2 步：OAuth 授权 -->
    <div v-else class="min-h-0 flex-1 space-y-5 overflow-y-auto overscroll-contain">
      <OAuthAuthorizationFlow
        ref="oauthFlowRef"
        :add-method="form.platform === 'anthropic' ? addMethod : 'oauth'"
        :auth-url="currentAuthUrl"
        :session-id="currentSessionId"
        :loading="currentOAuthLoading"
        :error="currentOAuthError"
        :show-help="form.platform === 'anthropic'"
        :show-proxy-warning="form.platform !== 'openai' && form.platform !== 'grok' && !!form.proxy_id"
        :allow-multiple="form.platform === 'anthropic'"
        :show-cookie-option="form.platform === 'anthropic'"
        :show-refresh-token-option="form.platform === 'openai' || form.platform === 'antigravity' || form.platform === 'grok'"
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
                class="text-sm text-primary-600 hover:underline dark:text-primary-500"
              >
                {{ t('admin.providers.gemini.setupGuide.links.countryCheck') }}
              </a>
              <span class="text-gray-400">·</span>
              <a
                href="https://policies.google.com/country-association-form"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary-600 hover:underline dark:text-primary-500"
              >
                修改归属地
              </a>
              <span class="text-gray-400">·</span>
              <a
                href="https://gemini.google.com/gems/create?hl=en-US&pli=1"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary-600 hover:underline dark:text-primary-500"
              >
                {{ t('admin.providers.gemini.setupGuide.links.geminiWebActivation') }}
              </a>
              <span class="text-gray-400">·</span>
              <a
                href="https://console.cloud.google.com"
                target="_blank"
                rel="noreferrer"
                class="text-sm text-primary-600 hover:underline dark:text-primary-500"
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
            class="text-sm text-primary-600 hover:underline dark:text-primary-500"
          >
            {{ t('admin.providers.gemini.quotaPolicy.docs.codeAssist') }}
          </a>
          <a
            :href="geminiQuotaDocs.aiStudio"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary-600 hover:underline dark:text-primary-500"
          >
            {{ t('admin.providers.gemini.quotaPolicy.docs.aiStudio') }}
          </a>
          <a
            :href="geminiQuotaDocs.vertex"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary-600 hover:underline dark:text-primary-500"
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
            class="text-sm text-primary-600 hover:underline dark:text-primary-500"
          >
            {{ t('admin.providers.gemini.providerType.apiKeyLink') }}
          </a>
          <a
            :href="geminiHelpLinks.aiStudioPricing"
            target="_blank"
            rel="noreferrer"
            class="text-sm text-primary-600 hover:underline dark:text-primary-500"
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

// 协议选择保存提供商支持的协议集合。
const upstreamProtocols = ref<ProtocolID[] | undefined>(undefined)

import { normalizeLegacyOpenAIExtra } from '@/utils/openaiLegacyConfiguration'
import ProviderProtocolSelector from './ProviderProtocolSelector.vue'
import { loadProtocolCatalog, nativeProtocolOptions } from '@/api/admin/protocolCapabilities'
import type { ProtocolID } from '@/types'
import OpenAICompactionToggle from './OpenAICompactionToggle.vue'
import { ref, reactive, computed, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import {
  buildModelMappingObject,
  buildPersistedModelRestriction,
  fetchAntigravityDefaultMappings
} from '@/composables/useModelWhitelist'
import { adminAPI } from '@/api/admin'
import { useQuotaNotifyState } from '@/composables/useQuotaNotifyState'
import {
  useProviderOAuth,
  type AddMethod,
  type AuthInputMethod
} from '@/composables/useProviderOAuth'
import {
  useOpenAIOAuth,
  type OpenAIOAuthSession,
  type OpenAITokenInfo
} from '@/composables/useOpenAIOAuth'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'
import { useQoderOAuth } from '@/composables/useQoderOAuth'
import type { QoderSite, QoderTokenInfo } from '@/api/admin/qoder'
import { useGrokOAuth } from '@/composables/useGrokOAuth'
import type {
  Proxy,
  AdminGroup,
  ProviderPlatform,
  ProviderType,
  CreateProviderRequest,
  CodexSessionImportMessage,
  OpenAICompactMode,
  OpenAIOAuthClientPolicy,
  UpstreamUsageAdapter
} from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import UpstreamRequestIdHeaderField from '@/components/provider/UpstreamRequestIdHeaderField.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import ProviderModelMappingEditor from '@/components/provider/ProviderModelMappingEditor.vue'
import type { TempUnschedRuleForm } from '@/components/provider/TempUnschedRulesEditor.vue'
import type { ModelMappingRow } from '@/utils/modelMappingRules'
import ProxySelector from '@/components/common/ProxySelector.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ModelWhitelistSelector from '@/components/provider/ModelWhitelistSelector.vue'
import ResponsesWSConnectionModeSelector from '@/components/provider/ResponsesWSConnectionModeSelector.vue'
import GrokBaseUrlPresets from '@/components/provider/GrokBaseUrlPresets.vue'
import CnBaseUrlPresets from '@/components/provider/CnBaseUrlPresets.vue'
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
import ProviderChoiceCard from '@/components/provider/form/ProviderChoiceCard.vue'
import QuotaLimitFields from '@/components/provider/form/QuotaLimitFields.vue'
import TempUnschedFields from '@/components/provider/form/TempUnschedFields.vue'
import TLSFingerprintFields from '@/components/provider/form/TLSFingerprintFields.vue'
import { bindQuotaLimits } from '@/components/provider/form/quotaLimit'
import {
  DEFAULT_POOL_MODE_RETRY_COUNT,
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
import { vSegmented } from '@/directives/segmented'
import { platformIconClass, platformTextClass } from '@/utils/platformColors'
import {
  applyAntigravityProjectID,
  applyHeaderOverride,
  applyInterceptWarmup,
  cnSupportsNativeResponses,
  defaultCNAdaptiveBaseUrls,
  defaultCNBaseUrl,
  isHeaderOverrideCapable,
  validateHeaderOverrideRows,
  type CnProviderMode,
  type CnApiProtocol,
  type CnNativeApiProtocol,
  type HeaderOverrideRow
} from '@/components/provider/credentialsBuilder'
import { formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import {
  BEDROCK_REGION_OPTIONS,
  VERTEX_LOCATION_OPTIONS,
  groupedProviderSelectOptions
} from '@/constants/provider'
import {
  clearLegacyResponsesWSSettings,
  RESPONSES_WS_POOLED,
  type ResponsesWSConnectionMode
} from '@/utils/responsesWsConnection'
import OAuthAuthorizationFlow from './OAuthAuthorizationFlow.vue'

// Type for exposed OAuthAuthorizationFlow component
// Note: defineExpose automatically unwraps refs, so we use the unwrapped types
interface OAuthFlowExposed {
  authCode: string
  oauthState: string
  projectId: string
  sessionKey: string
  refreshToken: string
  sessionToken: string
  codexSession: string
  codexPAT: string
  ssoCookie: string
  inputMethod: AuthInputMethod
  reset: () => void
}

const { t } = useI18n()

const oauthStepTitle = computed(() => {
  if (form.platform === 'openai') return t('admin.providers.oauth.openai.title')
  if (form.platform === 'gemini') return t('admin.providers.oauth.gemini.title')
  if (form.platform === 'antigravity') return t('admin.providers.oauth.antigravity.title')
  if (form.platform === 'qoder') return t('admin.providers.oauth.qoder.title')
  if (form.platform === 'grok') return t('admin.providers.oauth.grok.title')
  return t('admin.providers.oauth.title')
})

// Platform-specific hints for API Key type
// 上游ID：直接上游声明请求标识的响应头名，留空不记录。
const upstreamRequestIdHeader = ref('')
const withUpstreamRequestIdHeader = <T extends Record<string, unknown> | undefined>(extra: T): T | Record<string, unknown> => {
  const name = upstreamRequestIdHeader.value.trim()
  if (!name) return extra
  return { ...(extra || {}), upstream_request_id_header: name }
}

const baseUrlHint = computed(() => {
  if (form.platform === 'openai') return t('admin.providers.openai.baseUrlHint')
  if (form.platform === 'gemini' && geminiProviderType.value === 'third_party') {
    return t('admin.providers.gemini.connectionSource.thirdPartyBaseUrlHint')
  }
  if (form.platform === 'gemini') return t('admin.providers.gemini.baseUrlHint')
  if (form.platform === 'grok') return ''
  return t('admin.providers.baseUrlHint')
})

const apiKeyHint = computed(() => {
  if (form.platform === 'openai') return t('admin.providers.openai.apiKeyHint')
  if (form.platform === 'gemini' && geminiProviderType.value === 'third_party') {
    return t('admin.providers.gemini.connectionSource.thirdPartyApiKeyHint')
  }
  if (form.platform === 'gemini') return t('admin.providers.gemini.apiKeyHint')
  if (form.platform === 'grok') return ''
  return t('admin.providers.apiKeyHint')
})

const geminiGoogleOneTierOptions = computed(() => [
  { value: 'google_one_free', label: t('admin.providers.gemini.tier.googleOne.free') },
  { value: 'google_ai_pro', label: t('admin.providers.gemini.tier.googleOne.pro') },
  { value: 'google_ai_ultra', label: t('admin.providers.gemini.tier.googleOne.ultra') }
])

const geminiGCPTierOptions = computed(() => [
  { value: 'gcp_standard', label: t('admin.providers.gemini.tier.gcp.standard') },
  { value: 'gcp_enterprise', label: t('admin.providers.gemini.tier.gcp.enterprise') }
])

const geminiAIStudioTierOptions = computed(() => [
  { value: 'aistudio_free', label: t('admin.providers.gemini.tier.aiStudio.free') },
  { value: 'aistudio_paid', label: t('admin.providers.gemini.tier.aiStudio.paid') }
])

const geminiProviderTypeOptions = computed(() => [
  { value: 'official', label: t('admin.providers.gemini.connectionSource.official') },
  { value: 'third_party', label: t('admin.providers.gemini.connectionSource.thirdParty') }
])

const geminiProviderTypeHint = computed(() =>
  geminiProviderType.value === 'third_party'
    ? t('admin.providers.gemini.connectionSource.thirdPartyHint')
    : t('admin.providers.gemini.connectionSource.officialHint')
)

const isGeminiThirdPartyBaseUrl = (value: string) => {
  const normalized = value.trim()
  if (!normalized) return false
  try {
    return new URL(normalized).hostname.toLowerCase() !== 'generativelanguage.googleapis.com'
  } catch {
    return false
  }
}

const vertexLocationOptions = groupedProviderSelectOptions(VERTEX_LOCATION_OPTIONS)
const bedrockRegionOptions = groupedProviderSelectOptions(BEDROCK_REGION_OPTIONS)

interface Props {
  show: boolean
  initialPlatform?: ProviderPlatform
  proxies: Proxy[]
  groups: AdminGroup[]
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
  created: []
}>()

const appStore = useAppStore()

// OAuth 组合式状态
const oauth = useProviderOAuth() // Anthropic OAuth
const openaiOAuth = useOpenAIOAuth() // OpenAI OAuth
const geminiOAuth = useGeminiOAuth() // Gemini OAuth
const antigravityOAuth = useAntigravityOAuth() // Antigravity OAuth
const qoderOAuth = useQoderOAuth() // Qoder 设备授权
const grokOAuth = useGrokOAuth() // Grok OAuth
let qoderPollTimer: number | null = null
interface QoderAuthPopupLease {
  generation: number
  popup: Window | null
}

let qoderAuthPopupLease: QoderAuthPopupLease | null = null
let qoderAuthPopupGeneration = 0
let qoderPollInFlight = false
let qoderPollGeneration = 0
const qoderFlowGeneration = ref(0)
const qoderProviderCreateGeneration = ref<number | null>(null)
let qoderOAuthCompleted = false

const isQoderOAuthProviderCreating = computed(
  () => qoderProviderCreateGeneration.value !== null
)

// 当前 OAuth 状态用于模板绑定。
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
  form.platform === 'openai' ? openaiOAuth.authSessions.value : []
)

// Refs
const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

// Model mapping type
// State
const step = ref(1)
const submitting = ref(false)
const providerCategory = ref<'oauth-based' | 'apikey' | 'bedrock' | 'service_account'>('oauth-based') // UI selection for provider category
const addMethod = ref<AddMethod>('oauth') // For oauth-based: 'oauth' or 'setup-token'
const apiKeyBaseUrl = ref('https://api.anthropic.com')
const apiKeyValue = ref('')
const upstreamUsageEnabled = ref(true)
const upstreamUsageAdapter = ref<UpstreamUsageAdapter>('sub2api')
const upstreamUsageBaseUrl = ref('')
const upstreamUsageWalletAccessToken = ref('')
const upstreamUsageWalletUserId = ref('')

// 国产供应商提供商的计费模式、协议与默认端点彼此联动。
const providerMode = ref<CnProviderMode>('payg')
// 智谱团队版 Coding Plan 的组织/项目 ID，仅在创建团队提供商时写入凭据。
const zhipuOrganization = ref('')
const zhipuProject = ref('')
// API 协议决定转发端点与格式：cc=现有转换链，anthropic=原生直通（Claude Code），
// responses=deepseek / kimi 原生 Responses 端点（Codex）。与提供商类型正交。
const apiProtocol = ref<CnApiProtocol>('adaptive')
const adaptiveBaseUrls = ref<Record<CnNativeApiProtocol, string>>({
  chat_completions: '',
  anthropic: '',
  responses: ''
})
const isCNPlatform = computed(
  () => form.platform === 'kimi' || form.platform === 'zhipu' || form.platform === 'deepseek'
)
// 模板不支持联合类型断言，因此在脚本中收窄预设组件的平台类型。
const cnPresetPlatform = computed<'kimi' | 'zhipu' | 'deepseek'>(() => {
  if (form.platform === 'kimi' || form.platform === 'zhipu' || form.platform === 'deepseek') {
    return form.platform
  }
  return 'kimi'
})
// 当前平台可选的协议档（responses 仅 deepseek / kimi）。
const cnAdaptiveProtocolOptions = computed<Array<{ value: CnNativeApiProtocol; labelKey: string }>>(() => {
  const opts: Array<{ value: CnNativeApiProtocol; labelKey: string }> = [
    { value: 'chat_completions', labelKey: 'chatCompletions' },
    { value: 'anthropic', labelKey: 'anthropic' }
  ]
  if (cnSupportsNativeResponses(form.platform)) opts.push({ value: 'responses', labelKey: 'responses' })
  return opts
})

function resetAdaptiveBaseUrls(platform: 'kimi' | 'zhipu' | 'deepseek', mode: CnProviderMode) {
  adaptiveBaseUrls.value = defaultCNAdaptiveBaseUrls(platform, mode)
}
// 切换国产供应商平台：强制 apikey 类型，deepseek 无 coding 套餐故锁定 payg，
// 协议回落 adaptive，并把 base url 重置为该平台默认端点。
function selectCNPlatform(platform: 'kimi' | 'zhipu' | 'deepseek') {
  form.platform = platform
  form.type = 'apikey'
  providerCategory.value = 'apikey'
  apiProtocol.value = 'adaptive'
  if (platform === 'deepseek') {
    providerMode.value = 'payg'
  }
  if (platform !== 'zhipu') {
    zhipuOrganization.value = ''
    zhipuProject.value = ''
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(platform, providerMode.value, apiProtocol.value)
  resetAdaptiveBaseUrls(platform, providerMode.value)
}
// 提供商类型 / 协议变更时同步默认 base url。
watch(providerMode, (mode, previousMode) => {
  if (!isCNPlatform.value) return
  if (apiProtocol.value === 'adaptive') {
    const previousDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, previousMode)
    const nextDefaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, mode)
    for (const item of cnAdaptiveProtocolOptions.value) {
      if (!adaptiveBaseUrls.value[item.value] || adaptiveBaseUrls.value[item.value] === previousDefaults[item.value]) {
        adaptiveBaseUrls.value[item.value] = nextDefaults[item.value]
      }
    }
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
    return
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(form.platform, mode, apiProtocol.value)
})
watch(apiProtocol, protocol => {
  if (!isCNPlatform.value) return
  if (protocol === 'adaptive') {
    const defaults = defaultCNAdaptiveBaseUrls(cnPresetPlatform.value, providerMode.value)
    for (const item of cnAdaptiveProtocolOptions.value) {
      if (!adaptiveBaseUrls.value[item.value]) adaptiveBaseUrls.value[item.value] = defaults[item.value]
    }
    apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
    return
  }
  apiKeyBaseUrl.value = defaultCNBaseUrl(form.platform, providerMode.value, protocol)
})

// 端点预设同时更新模式和协议，保持表单字段一致。
function onCnPresetSelect(preset: { mode: CnProviderMode; protocol: CnApiProtocol; url: string }) {
  providerMode.value = preset.mode
  apiProtocol.value = 'adaptive'
  if (preset.protocol !== 'adaptive') adaptiveBaseUrls.value[preset.protocol] = preset.url
  apiKeyBaseUrl.value = adaptiveBaseUrls.value.chat_completions
}

const syncPreviewCredentials = computed(() => {
  if (!apiKeyValue.value) return undefined
  const baseUrl = isCNPlatform.value && apiProtocol.value === 'adaptive'
    ? adaptiveBaseUrls.value.chat_completions.trim() || apiKeyBaseUrl.value.trim()
    : apiKeyBaseUrl.value.trim()
  return {
    platform: form.platform,
    type: form.type,
    base_url: baseUrl || undefined,
    api_key: apiKeyValue.value
  }
})

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
const modelMappings = ref<ModelMappingRow[]>([])
const openAICompactModelMappings = ref<ModelMappingRow[]>([])
const modelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const allowedModels = ref<string[]>([])
const qoderModelRestrictionTouched = ref(false)
const qoderModelWhitelistTouched = ref(false)
const poolModeEnabled = ref(false)
const poolModeRetryCount = ref(DEFAULT_POOL_MODE_RETRY_COUNT)
const poolModeRetryStatusCodesInput = ref('')

const customErrorCodesEnabled = ref(false)
const selectedErrorCodes = ref<number[]>([])
const headerOverrideEnabled = ref(false)
const headerOverrideRows = ref<HeaderOverrideRow[]>([])

// Grok OAuth 自定义转发地址，授权与刷新使用各自的端点。
const grokOAuthCustomBaseUrlEnabled = ref(false)
const grokOAuthBaseUrl = ref('')

// Grok OAuth 三条创建路径（授权码/RT 批量/SSO 批量）共用的前置校验。
// 兑换 code 前校验上游配置，配置错误时授权码仍可继续使用。
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

// 把已通过校验的自定义上游地址与请求头覆写写入 credentials
const applyGrokOAuthUpstreamConfig = (credentials: Record<string, unknown>) => {
  if (grokOAuthCustomBaseUrlEnabled.value) {
    credentials.base_url = grokOAuthBaseUrl.value.trim()
  }
  applyHeaderOverride(credentials, headerOverrideEnabled.value, headerOverrideRows.value, 'create')
}
const interceptWarmupRequests = ref(false)
const autoPauseOnExpired = ref(true)
const autoPause5hThreshold = ref<number | null>(null)
const autoPause7dThreshold = ref<number | null>(null)
const autoPause5hDisabled = ref(false)
const autoPause7dDisabled = ref(false)
const openaiPassthroughEnabled = ref(false)
// OpenAI OAuth namespace 工具摊平兼容开关，缺省关闭即原样保留。
const openaiFlattenNamespacesEnabled = ref(false)
const openAICompactMode = ref<OpenAICompactMode>('force_on')
const openAINativeCompactionV2Mode = ref<OpenAICompactMode>('force_on')
// HTTP continuation 默认关闭，确认支持 previous_response_id 后启用。
const openAIResponsesContinuationSupported = ref(false)
// 图片回填默认关闭，只对 OpenAI API Key 提供商生效。
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

const {
  globalEnabled: quotaNotifyGlobalEnabled,
  state: quotaNotifyState,
  loadGlobalState: loadQuotaNotifyGlobal,
  writeToExtra: writeQuotaNotifyToExtra,
  setField: setQuotaNotifyField,
} = useQuotaNotifyState()

// Load global feature states once
adminAPI.settings.getWebSearchEmulationConfig().then(cfg => {
  webSearchGlobalEnabled.value = cfg?.enabled === true && (cfg?.providers?.length ?? 0) > 0
}).catch(() => { webSearchGlobalEnabled.value = false })

loadQuotaNotifyGlobal()
const allowOverages = ref(false) // For antigravity providers: enable AI Credits overages
const qoderProviderType = ref<'oauth' | 'manual'>('oauth')
const qoderSite = ref<QoderSite>('global')
const qoderPAT = ref('')
const qoderSecurityOauthToken = ref('')
const qoderMachineId = ref('')
const qoderUidAid = ref('')
const qoderRefreshToken = ref('')
const qoderUserType = ref('personal_standard')
const antigravityProjectId = ref('')
const antigravityModelRestrictionMode = ref<'whitelist' | 'mapping'>('whitelist')
const antigravityWhitelistModels = ref<string[]>([])
const antigravityModelMappings = ref<ModelMappingRow[]>([])

// Bedrock credentials
const bedrockAuthMode = ref<'sigv4' | 'apikey'>('sigv4')
const bedrockAccessKeyId = ref('')
const bedrockSecretAccessKey = ref('')
const bedrockSessionToken = ref('')
const bedrockRegion = ref('us-east-1')
const bedrockForceGlobal = ref(false)
const bedrockApiKeyValue = ref('')
const vertexServiceAccountFileInput = ref<HTMLInputElement | null>(null)
const vertexServiceAccountJson = ref('')
const vertexProjectId = ref('')
const vertexClientEmail = ref('')
const vertexLocation = ref('global')
const vertexServiceAccountDragActive = ref(false)
const tempUnschedEnabled = ref(false)
const tempUnschedRules = ref<TempUnschedRuleForm[]>([])
const geminiOAuthType = ref<'code_assist' | 'google_one' | 'ai_studio'>('google_one')
const geminiAIStudioOAuthEnabled = ref(false)

const openAIOAuthClientPolicyOptions = useOpenAIOAuthClientPolicyOptions()

function buildAntigravityExtra(): Record<string, unknown> | undefined {
  const extra: Record<string, unknown> = {}
  if (allowOverages.value) extra.allow_overages = true
  return Object.keys(extra).length > 0 ? extra : undefined
}

const buildOpenAICompactModelMapping = () =>
  buildModelMappingObject('mapping', [], openAICompactModelMappings.value)
const showAdvancedOAuth = ref(false)
const showGeminiHelpDialog = ref(false)

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
const webSearchEmulationOptions = useWebSearchEmulationOptions()
const anthropicAPIKeyAuthSchemeOptions = useAnthropicAPIKeyAuthSchemeOptions()
const customBaseUrlEnabled = ref(false)
const customBaseUrl = ref('')

// Gemini tier selection (used as fallback when auto-detection is unavailable/fails)
type GeminiProviderType = 'official' | 'third_party'
const geminiProviderType = ref<GeminiProviderType>('official')
const geminiTierGoogleOne = ref<'google_one_free' | 'google_ai_pro' | 'google_ai_ultra'>('google_one_free')
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

const geminiQuotaDocs = {
  codeAssist: 'https://developers.google.com/gemini-code-assist/resources/quotas',
  aiStudio: 'https://ai.google.dev/pricing',
  vertex: 'https://cloud.google.com/vertex-ai/generative-ai/docs/quotas'
}

const geminiHelpLinks = {
  apiKey: 'https://aistudio.google.com/app/apikey',
  aiStudioPricing: 'https://ai.google.dev/pricing',
  gcpProject: 'https://console.cloud.google.com/welcome/new',
  geminiWebActivation: 'https://gemini.google.com/gems/create?hl=en-US&pli=1',
  countryCheck: 'https://policies.google.com/terms',
  countryChange: 'https://policies.google.com/country-association-form'
}

const form = reactive({
  name: '',
  notes: '',
  platform: 'anthropic' as ProviderPlatform,
  type: 'oauth' as ProviderType, // Will be 'oauth', 'setup-token', or 'apikey'
  credentials: {} as Record<string, unknown>,
  proxy_id: null as number | null,
  concurrency: 10,
  load_factor: null as number | null,
  priority: 1,
  rate_multiplier: 1,
  group_ids: [] as number[],
  expires_at: null as number | null
})

// Helper to check if current type needs OAuth flow
const isOAuthFlow = computed(() => {
  if (form.platform === 'qoder' && qoderProviderType.value === 'manual') {
    return false
  }
  // Bedrock 类型不需要 OAuth 流程
  if (form.platform === 'anthropic' && providerCategory.value === 'bedrock') {
    return false
  }
  return providerCategory.value === 'oauth-based'
})

const isGrokSSOInputMethod = computed(() => form.platform === 'grok' && oauthFlowRef.value?.inputMethod === 'sso_cookie')

const isManualInputMethod = computed(() => {
  return oauthFlowRef.value?.inputMethod === 'manual'
})

// 平台按国际平台与国产供应商分两行展示，国产供应商切换时同步重置类型与端点。
const platformRows: Array<Array<{ value: ProviderPlatform; label: string; testid?: string }>> = [
  [
    { value: 'anthropic', label: 'Anthropic' },
    { value: 'openai', label: 'OpenAI' },
    { value: 'gemini', label: 'Gemini', testid: 'create-provider-platform-gemini' },
    { value: 'antigravity', label: 'Antigravity' },
    { value: 'qoder', label: 'Qoder', testid: 'create-provider-platform-qoder' },
    { value: 'grok', label: 'Grok' },
    { value: 'jev', label: 'Jev', testid: 'create-provider-platform-jev' }
  ],
  [
    { value: 'kimi', label: 'Kimi' },
    { value: 'zhipu', label: 'Zhipu GLM' },
    { value: 'deepseek', label: 'DeepSeek' }
  ]
]
function selectPlatform(platform: ProviderPlatform) {
  if (platform === 'kimi' || platform === 'zhipu' || platform === 'deepseek') {
    selectCNPlatform(platform)
    return
  }
  form.platform = platform
}

const qoderSiteOptions = computed(() => [
  { value: 'global' as QoderSite, label: t('admin.providers.qoder.site.global'), testid: 'create-qoder-site-global' },
  { value: 'cn' as QoderSite, label: t('admin.providers.qoder.site.cn'), testid: 'create-qoder-site-cn' }
])
const addMethodOptions = computed(() => [
  { value: 'oauth' as AddMethod, label: t('admin.providers.types.oauth') },
  { value: 'setup-token' as AddMethod, label: t('admin.providers.setupTokenLongLived') }
])
const bedrockAuthModeOptions = computed(() => [
  { value: 'sigv4' as const, label: t('admin.providers.bedrockAuthModeSigv4') },
  { value: 'apikey' as const, label: t('admin.providers.bedrockAuthModeApikey') }
])

const isBedrockCategory = computed(() => form.platform === 'anthropic' && providerCategory.value === 'bedrock')
const isServiceAccountCategory = computed(() =>
  (form.platform === 'gemini' || form.platform === 'anthropic') && providerCategory.value === 'service_account'
)
const isAnthropicOAuthCategory = computed(() => form.platform === 'anthropic' && providerCategory.value === 'oauth-based')
const isOpenAIOAuthCategory = computed(() => form.platform === 'openai' && providerCategory.value === 'oauth-based')
// API Key 提供商使用通用凭据表单。
const isApiKeyCredentials = computed(() => form.type === 'apikey')
const showGeminiApiKeyTier = computed(() => form.platform === 'gemini' && geminiProviderType.value === 'official')
const showCredentialsSection = computed(() =>
  form.platform === 'antigravity' ||
  (form.platform === 'qoder' && qoderProviderType.value === 'manual') ||
  isServiceAccountCategory.value ||
  isApiKeyCredentials.value ||
  isBedrockCategory.value
)
const showModelRestriction = computed(() =>
  form.platform === 'qoder' ||
  isApiKeyCredentials.value ||
  isBedrockCategory.value ||
  (['openai', 'grok', 'gemini'].includes(form.platform) && isOAuthFlow.value)
)
const showHeaderOverride = computed(() =>
  (isApiKeyCredentials.value && isHeaderOverrideCapable(form.platform, 'apikey')) ||
  (form.platform === 'grok' && isOAuthFlow.value)
)
// TLS 指纹只对模拟官方客户端的链路开放；null 表示当前账号类型不显示。
const tlsFingerprintTestIdPrefix = computed<string | null>(() => {
  if (form.platform === 'qoder') return 'create-qoder-tls-fingerprint'
  if (isOpenAIOAuthCategory.value) return 'create-openai-tls-fingerprint'
  if (isAnthropicOAuthCategory.value) return ''
  return null
})
const apiKeyBaseUrlPlaceholder = computed(() => {
  switch (form.platform) {
    case 'jev': return 'https://api.typesafe.ai'
    case 'openai': return 'https://api.openai.com'
    case 'gemini': return geminiProviderType.value === 'third_party' ? 'https://' : 'https://generativelanguage.googleapis.com'
    case 'grok': return 'https://api.x.ai/v1'
    default: return 'https://api.anthropic.com'
  }
})
const apiKeyPlaceholder = computed(() => {
  switch (form.platform) {
    case 'jev': return 'api-key-...'
    case 'openai': return 'sk-proj-...'
    case 'gemini': return geminiProviderType.value === 'third_party' ? 'api-key-...' : 'AIza...'
    case 'grok': return 'xai-...'
    default: return 'sk-ant-...'
  }
})

// 页签按平台和账号类型隐藏没有内容的分类，切换后当前页签失效时回到基本信息。
const tabsRef = ref<InstanceType<typeof SettingsTabs> | null>(null)
const formTabs = computed(() => {
  const hasModels = showModelRestriction.value || form.platform === 'antigravity'
  const hasQuota = form.type === 'apikey' || form.type === 'bedrock' || isAnthropicOAuthCategory.value
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

const expiresAtInput = computed({
  get: () => formatDateTimeLocal(form.expires_at),
  set: (value: string) => {
    form.expires_at = parseDateTimeLocal(value)
  }
})

const canExchangeCode = computed(() => {
  const authCode = oauthFlowRef.value?.authCode || ''
  if (form.platform === 'openai') {
    return authCode.trim() && openaiOAuth.authSessions.value.length > 0 && !openaiOAuth.loading.value
  }
  if (form.platform === 'gemini') {
    return authCode.trim() && geminiOAuth.sessionId.value && !geminiOAuth.loading.value
  }
  if (form.platform === 'antigravity') {
    return authCode.trim() && antigravityOAuth.sessionId.value && !antigravityOAuth.loading.value
  }
  if (form.platform === 'qoder') {
    return qoderOAuth.sessionId.value &&
      !qoderOAuth.loading.value &&
      !submitting.value &&
      !isQoderOAuthProviderCreating.value
  }
  if (form.platform === 'grok') {
    return authCode.trim() && grokOAuth.sessionId.value && !grokOAuth.loading.value
  }
  return authCode.trim() && oauth.sessionId.value && !oauth.loading.value
})

// Watchers
watch(
  () => props.show,
  (newVal) => {
    if (newVal) {
      form.platform = props.initialPlatform || 'anthropic'
      // Load TLS fingerprint profiles
      adminAPI.tlsFingerprintProfiles.list()
        .then(profiles => { tlsFingerprintProfiles.value = profiles.map(p => ({ id: p.id, name: p.name })) })
        .catch(() => { tlsFingerprintProfiles.value = [] })
      adminAPI.tlsFingerprintRouters.list()
        .then(routers => { tlsFingerprintRouters.value = routers.map(router => ({ id: router.id, name: router.name })) })
        .catch(() => { tlsFingerprintRouters.value = [] })
      // Modal opened - fill related models
      allowedModels.value = []
      // Antigravity: 默认使用映射模式并填充默认映射
      if (form.platform === 'antigravity') {
        antigravityModelRestrictionMode.value = 'mapping'
        fetchAntigravityDefaultMappings().then(mappings => {
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
  }
)

// 提供商类型由平台和授权方式确定。
watch(
  [providerCategory, addMethod, qoderProviderType, () => form.platform],
  ([category, method]) => {
    if (form.platform === 'jev') {
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
    // Bedrock 类型
    if (form.platform === 'anthropic' && category === 'bedrock') {
      form.type = 'bedrock' as ProviderType
      return
    }
    if ((form.platform === 'gemini' || form.platform === 'anthropic') && category === 'service_account') {
      form.type = 'service_account' as ProviderType
    } else if (category === 'oauth-based') {
      form.type = form.platform === 'anthropic' ? method as ProviderType : 'oauth'
    } else {
      form.type = 'apikey'
    }
  },
  { immediate: true }
)

// Reset platform-specific settings when platform changes
watch(
  () => form.platform,
  (newPlatform) => {
    // Reset base URL based on platform
    apiKeyBaseUrl.value =
      (newPlatform === 'openai')
        ? 'https://api.openai.com'
        : newPlatform === 'gemini'
          ? geminiProviderType.value === 'third_party'
            ? ''
            : 'https://generativelanguage.googleapis.com'
          : newPlatform === 'grok'
            ? 'https://api.x.ai/v1'
            : 'https://api.anthropic.com'
    // 切换平台后清空表单中的模型限制，由管理员选择或同步上游型号。
    allowedModels.value = []
    modelMappings.value = []
    modelRestrictionMode.value = (newPlatform === 'qoder' || newPlatform === 'grok') ? 'mapping' : 'whitelist'
    qoderModelRestrictionTouched.value = false
    qoderModelWhitelistTouched.value = false
    // Antigravity: 默认使用映射模式并填充默认映射
    if (newPlatform === 'antigravity') {
      antigravityModelRestrictionMode.value = 'mapping'
      fetchAntigravityDefaultMappings().then(mappings => {
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
    if (newPlatform !== 'gemini' && newPlatform !== 'anthropic' && providerCategory.value === 'service_account') {
      providerCategory.value = 'oauth-based'
    }
    if (newPlatform !== 'anthropic' && providerCategory.value === 'bedrock') {
      providerCategory.value = 'oauth-based'
    }
    if (newPlatform === 'jev') {
      providerCategory.value = 'apikey'
      apiKeyBaseUrl.value = 'https://api.typesafe.ai'
    }
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
    // 请求头覆写为平台相关配置（常用头集合不同），切换平台时清空，
    // 避免上一平台的配置行被提交到新平台提供商
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
  }
)

watch(geminiProviderType, (providerType) => {
  if (form.platform !== 'gemini' || providerType !== 'third_party') return
  // 切换到第三方来源时，不能把官方默认端点误当成第三方地址提交。
  if (!isGeminiThirdPartyBaseUrl(apiKeyBaseUrl.value)) {
    apiKeyBaseUrl.value = ''
  }
})

watch(qoderSite, (newSite, oldSite) => {
  if (newSite === oldSite || form.platform !== 'qoder') return
  // OAuth 会话冻结站点和代理；切站后必须销毁旧会话，手动输入保持不变。
  stopQoderPolling()
  closeQoderAuthPopup()
  resetQoderOAuthCompletionState()
  qoderOAuth.resetState()
})

// Gemini AI Studio OAuth availability (requires operator-configured OAuth client)
watch(
  [providerCategory, () => form.platform],
  ([category, platform]) => {
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
  }
)

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
  { immediate: true }
)

const handleSelectGeminiOAuthType = (oauthType: 'code_assist' | 'google_one' | 'ai_studio') => {
  if (oauthType === 'ai_studio' && !geminiAIStudioOAuthEnabled.value) {
    appStore.showError(t('admin.providers.oauth.gemini.aiStudioNotConfigured'))
    return
  }
  geminiOAuthType.value = oauthType
}

watch(
  [antigravityModelRestrictionMode, () => form.platform],
  ([, platform]) => {
    if (platform !== 'antigravity') return
    // Antigravity 默认不做限制：白名单留空表示允许所有（包含未来新增模型）。
    // 如果需要快速填充常用模型，可在组件内点“填充相关模型”。
  }
)

// Model mapping helpers
const touchQoderModelRestriction = () => {
  if (form.platform === 'qoder') {
    qoderModelRestrictionTouched.value = true
  }
}

const setAllowedModels = (models: string[]) => {
  touchQoderModelRestriction()
  if (form.platform === 'qoder') {
    qoderModelWhitelistTouched.value = true
  }
  allowedModels.value = models
}

const applyPersistedModelRestriction = (credentials: Record<string, unknown>) => {
  // 普通提供商将请求侧映射与最终白名单拆开持久化。
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
  if (!qoderModelRestrictionTouched.value) {
    delete credentials.model_mapping
    delete credentials.model_whitelist
    return
  }
  const persisted = buildPersistedModelRestriction(
    qoderModelWhitelistTouched.value ? allowedModels.value : [],
    modelMappings.value
  )
  if (persisted.modelMapping) {
    credentials.model_mapping = persisted.modelMapping
  } else {
    delete credentials.model_mapping
  }
  credentials.model_whitelist = persisted.modelWhitelist
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

const splitTempUnschedKeywords = (value: string) => {
  return value
    .split(/[,;]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

// 普通提交、批量授权与导入入口共用此边界，不能遗漏原生集合。
async function createProtocolProvider(payload: CreateProviderRequest) {
  if (payload.platform === 'antigravity') {
    payload.credentials = { ...payload.credentials, model_whitelist: [...antigravityWhitelistModels.value] }
  } else if (payload.platform === 'gemini') {
    applyPersistedModelRestriction(payload.credentials)
  }
  await loadProtocolCatalog()
  const options = nativeProtocolOptions(payload.platform, payload.type, String(payload.credentials?.auth_mode ?? ''))
  payload.credentials = { ...payload.credentials, upstream_protocols: (upstreamProtocols.value ?? options).filter(id => options.includes(id)) }
  delete payload.credentials.api_protocol
  delete payload.credentials.openai_workload_capabilities
  if (payload.extra) delete payload.extra.openai_text_route_mode
  return adminAPI.providers.create(payload)
}

type ProviderCreateGuard = () => boolean

const submitCreateProvider = async (
  payload: CreateProviderRequest,
  isCurrent: ProviderCreateGuard = () => true
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
    appStore.showError(error.response?.data?.message || error.response?.data?.detail || t('admin.providers.failedToCreate'))
    return false
  } finally {
    // 旧流程结束时不能解除新流程的提交锁。
    if (isCurrent()) {
      submitting.value = false
    }
  }
}

// Methods
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
  upstreamUsageAdapter.value = 'sub2api'
  upstreamUsageBaseUrl.value = ''
  upstreamUsageWalletAccessToken.value = ''
  upstreamUsageWalletUserId.value = ''
  upstreamRequestIdHeader.value = ''
  editQuotaLimit.value = null
  editQuotaDailyLimit.value = null
  editQuotaWeeklyLimit.value = null
  editDailyResetMode.value = null
  editDailyResetHour.value = null
  editWeeklyResetMode.value = null
  editWeeklyResetDay.value = null
  editWeeklyResetHour.value = null
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
  fetchAntigravityDefaultMappings().then(mappings => {
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

const finishClose = () => {
  stopQoderPolling()
  resetQoderOAuthCompletionState()
  closeQoderAuthPopup()
  emit('close')
}

const handleClose = () => {
  // Qoder 创建期间等待服务端响应，再处理创建成功事件和关闭弹窗。
  if (form.platform === 'qoder' && submitting.value) return
  finishClose()
}

const buildOpenAIExtra = (base?: Record<string, unknown>): Record<string, unknown> | undefined => {
  if (form.platform !== 'openai') {
    return base
  }

  const extra: Record<string, unknown> = { ...base }
  extra.responses_ws_connection_mode = responsesWSConnectionMode.value
  // 保存统一的连接方式。
  clearLegacyResponsesWSSettings(extra)
  delete extra.openai_ws_enabled
  delete extra.openai_long_context_billing_enabled
  if (openaiPassthroughEnabled.value) {
    extra.openai_passthrough = true
  } else {
    delete extra.openai_passthrough
    delete extra.openai_oauth_passthrough
  }
  // 关闭时删除 extra 中的默认项。
  if (form.type === 'oauth' && openaiFlattenNamespacesEnabled.value) {
    extra.openai_responses_flatten_namespaces = true
  } else {
    delete extra.openai_responses_flatten_namespaces
  }
  if (providerCategory.value === 'oauth-based') {
    extra.openai_oauth_client_policy = openAIOAuthClientPolicy.value
    if (openAIOAuthClientPolicy.value === 'codex_only') {
      extra.codex_cli_only = true
    } else {
      delete extra.codex_cli_only
    }
    if (openAIOAuthClientPolicy.value === 'codex_only' && codexCLIOnlyAllowClaudeCodeEnabled.value) {
      extra.codex_cli_only_allowed_clients = ['claude_code']
    } else {
      delete extra.codex_cli_only_allowed_clients
    }
  } else {
    delete extra.openai_oauth_client_policy
    delete extra.codex_cli_only
    delete extra.codex_cli_only_allowed_clients
  }

  if (providerCategory.value === 'oauth-based') {
    if (autoPause5hThreshold.value != null && autoPause5hThreshold.value > 0) {
      extra.auto_pause_5h_threshold = autoPause5hThreshold.value / 100
    } else {
      delete extra.auto_pause_5h_threshold
    }
    if (autoPause7dThreshold.value != null && autoPause7dThreshold.value > 0) {
      extra.auto_pause_7d_threshold = autoPause7dThreshold.value / 100
    } else {
      delete extra.auto_pause_7d_threshold
    }
    if (autoPause5hDisabled.value) {
      extra.auto_pause_5h_disabled = true
    } else {
      delete extra.auto_pause_5h_disabled
    }
    if (autoPause7dDisabled.value) {
      extra.auto_pause_7d_disabled = true
    } else {
      delete extra.auto_pause_7d_disabled
    }
  } else {
    delete extra.auto_pause_5h_threshold
    delete extra.auto_pause_7d_threshold
    delete extra.auto_pause_5h_disabled
    delete extra.auto_pause_7d_disabled
  }
  if (providerCategory.value === 'oauth-based' && tlsFingerprintEnabled.value) {
    extra.enable_tls_fingerprint = true
    if (tlsFingerprintProfileId.value) {
      extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
    } else {
      delete extra.tls_fingerprint_profile_id
    }
    if (form.platform === 'openai' && providerCategory.value === 'oauth-based' && tlsFingerprintRouterId.value) {
      extra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
    } else {
      delete extra.tls_fingerprint_router_id
    }
  } else {
    delete extra.enable_tls_fingerprint
    delete extra.tls_fingerprint_profile_id
    delete extra.tls_fingerprint_router_id
  }

  // 收敛是显式 opt-in；off 为默认值，不写入 extra。
  if (providerCategory.value === 'oauth-based' && codexFingerprintMode.value !== 'off') {
    extra.codex_fingerprint_mode = codexFingerprintMode.value
  } else {
    delete extra.codex_fingerprint_mode
  }
  extra.openai_compact_mode = openAICompactMode.value
  extra.openai_native_compaction_v2_mode = openAINativeCompactionV2Mode.value

  if (providerCategory.value === 'apikey' && openAIImagesURLToB64JSON.value) {
    extra.images_url_to_b64_json = true
  } else {
    delete extra.images_url_to_b64_json
  }

  if (providerCategory.value === 'apikey') {
    delete extra.openai_responses_mode
    extra.openai_responses_continuation_supported = openAIResponsesContinuationSupported.value
  }

  return Object.keys(extra).length > 0 ? normalizeLegacyOpenAIExtra(extra) : undefined
}

const buildQoderExtra = (): Record<string, unknown> | undefined => {
  const extra: Record<string, unknown> = {}
  if (tlsFingerprintEnabled.value) {
    extra.enable_tls_fingerprint = true
    if (tlsFingerprintProfileId.value) {
      extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
    }
  }
  return Object.keys(extra).length > 0 ? extra : undefined
}

const buildAnthropicExtra = (base?: Record<string, unknown>): Record<string, unknown> | undefined => {
  if (form.platform !== 'anthropic' || providerCategory.value !== 'apikey') {
    return base
  }

  const extra: Record<string, unknown> = { ...(base || {}) }
  if (anthropicPassthroughEnabled.value) {
    extra.anthropic_passthrough = true
  } else {
    delete extra.anthropic_passthrough
  }
  if (anthropicAPIKeyAuthScheme.value === 'authorization_bearer') {
    extra.anthropic_apikey_auth_scheme = 'authorization_bearer'
  } else {
    delete extra.anthropic_apikey_auth_scheme
  }
  if (webSearchEmulationMode.value === 'default') {
    delete extra.web_search_emulation
  } else {
    extra.web_search_emulation = webSearchEmulationMode.value
  }

  return Object.keys(extra).length > 0 ? extra : undefined
}

const doCreateProvider = async (payload: CreateProviderRequest, isCurrent: ProviderCreateGuard = () => true): Promise<boolean> => {
  if (!isCurrent()) return false
  return submitCreateProvider(payload, isCurrent)
}


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

const parseVertexServiceAccountJson = () => applyVertexServiceAccountJson(vertexServiceAccountJson.value)

const handleVertexServiceAccountFile = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    applyVertexServiceAccountJson(await file.text())
  } finally {
    input.value = ''
  }
}

const handleVertexServiceAccountDrop = async (event: DragEvent) => {
  vertexServiceAccountDragActive.value = false
  const file = event.dataTransfer?.files?.[0]
  if (!file) return
  applyVertexServiceAccountJson(await file.text())
}

const handleSubmit = async () => {
  // 表单关闭了浏览器自带校验，隐藏页签中的必填项由页签组件定位后报告。
  if (tabsRef.value && !(await tabsRef.value.validate())) return

  // OAuth 类账号进入第 2 步授权
  if (isOAuthFlow.value) {
    if (!isGrokSSOInputMethod.value && !form.name.trim()) {
      failAt(t('admin.providers.pleaseEnterProviderName'), 'name')
      return
    }
    // 第 1 步的配置在进入授权前校验，失败时定位到所在页签。授权阶段再次校验。
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
      const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(poolModeRetryStatusCodesInput.value)
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
      refresh_mode: 'cosy'
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

  if ((form.platform === 'gemini' || form.platform === 'anthropic') && providerCategory.value === 'service_account') {
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
      tier_id: 'vertex'
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

  // Determine default base URL based on platform
  const defaultBaseUrl =
    form.platform === 'jev'
      ? 'https://api.typesafe.ai'
      : form.platform === 'openai'
      ? 'https://api.openai.com'
      : form.platform === 'gemini'
        ? 'https://generativelanguage.googleapis.com'
        : form.platform === 'grok'
          ? 'https://api.x.ai/v1'
          : 'https://api.anthropic.com'

  // Build credentials with optional model mapping
  const credentials: Record<string, unknown> = {
    base_url: enteredBaseUrl || defaultBaseUrl,
    api_key: apiKeyValue.value.trim()
  }
  // New API 钱包是用户级余额，访问令牌写入 Credentials。
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

  // 国产供应商：提供商模式 + 协议 + 对应端点写入凭据；后端按 provider_mode 路由
  // 额度/余额探测，按 api_protocol 路由转发端点与格式。注意 CN apikey 走本函数
  // 的通用路径（直接 doCreateProvider），不经过 createProviderAndFinish。
  if (form.platform === 'kimi' || form.platform === 'zhipu' || form.platform === 'deepseek') {
    credentials.provider_mode = providerMode.value
    credentials.api_protocol = apiProtocol.value
    if (apiProtocol.value === 'adaptive') {
      const defaults = defaultCNAdaptiveBaseUrls(form.platform, providerMode.value)
      const protocolBaseUrls: Record<string, string> = {}
      for (const item of cnAdaptiveProtocolOptions.value) {
        protocolBaseUrls[item.value] = (adaptiveBaseUrls.value[item.value] || defaults[item.value]).trim()
      }
      credentials.api_base_urls = protocolBaseUrls
      credentials.base_url = protocolBaseUrls.chat_completions
    }
    const resolvedCNBase = (
      apiKeyBaseUrl.value.trim() || defaultCNBaseUrl(form.platform, providerMode.value, apiProtocol.value)
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

  // 保存模型映射和最终白名单。
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
    const parsedRetryStatusCodes = parsePoolModeRetryStatusCodes(poolModeRetryStatusCodesInput.value)
    if (parsedRetryStatusCodes.length > 0) {
      credentials.pool_mode_retry_status_codes = parsedRetryStatusCodes
    }
  }

  // Add custom error codes if enabled
  if (customErrorCodesEnabled.value) {
    credentials.custom_error_codes_enabled = true
    credentials.custom_error_codes = [...selectedErrorCodes.value]
  }

  // 为支持该功能的平台 API Key 提供商写入请求头覆写。
  if (isHeaderOverrideCapable(form.platform, 'apikey')) {
    if (headerOverrideEnabled.value) {
      const headerError = validateHeaderOverrideRows(headerOverrideRows.value)
      if (headerError) {
        failAt(t(`admin.providers.headerOverride.${headerError}`), 'header-override')
        return
      }
    }
    applyHeaderOverride(credentials, headerOverrideEnabled.value, headerOverrideRows.value, 'create')
  }

  applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')

  form.credentials = credentials
  const extra = buildAnthropicExtra(buildOpenAIExtra())

  // API Key 统一经过构造器，确保配额和上游用量查询配置一起写入请求。
  await createProviderAndFinish(form.platform, 'apikey', credentials, extra)
}

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

const getQoderPopupFeatures = () => {
  const width = Math.min(1100, (window.screen?.availWidth || 1100) - 40)
  const height = Math.min(820, (window.screen?.availHeight || 820) - 40)
  const left = Math.max(0, Math.floor(((window.screen?.availWidth || width) - width) / 2))
  const top = Math.max(0, Math.floor(((window.screen?.availHeight || height) - height) / 2))
  return `width=${width},height=${height},left=${left},top=${top},scrollbars=yes,resizable=yes`
}

const stopQoderPolling = () => {
  qoderPollGeneration += 1
  qoderOAuth.invalidatePendingRequests()
  if (qoderPollTimer) {
    window.clearInterval(qoderPollTimer)
    qoderPollTimer = null
  }
  qoderPollInFlight = false
}

const closeQoderAuthPopup = (lease?: QoderAuthPopupLease) => {
  const currentLease = qoderAuthPopupLease
  if (!currentLease || (lease && currentLease.generation !== lease.generation)) return

  // 先解绑再关闭，旧请求恢复执行时只能处理自己仍持有的弹窗。
  qoderAuthPopupLease = null
  currentLease.popup?.close()
}

const resetQoderOAuthCompletionState = () => {
  // 流程代数与轮询代数分离，停止一次轮询不会误伤同一授权流程的兑换或创建。
  qoderFlowGeneration.value += 1
  qoderOAuthCompleted = false
}

interface QoderFlowContext {
  generation: number
  site: QoderSite
}

const captureQoderFlowContext = (): QoderFlowContext => ({
  generation: qoderFlowGeneration.value,
  site: qoderSite.value
})

const isCurrentQoderFlow = (context: QoderFlowContext) =>
  context.generation === qoderFlowGeneration.value &&
  context.site === qoderSite.value &&
  form.platform === 'qoder' &&
  props.show

const createQoderOAuthProvider = async (
  tokenInfo: QoderTokenInfo | undefined,
  context: QoderFlowContext
): Promise<boolean> => {
  if (
    !tokenInfo ||
    !isCurrentQoderFlow(context) ||
    qoderProviderCreateGeneration.value !== null ||
    qoderOAuthCompleted
  ) return false

  qoderProviderCreateGeneration.value = context.generation
  try {
    if (!isCurrentQoderFlow(context)) return false
    const credentials = qoderOAuth.buildCredentials(tokenInfo)
    applyQoderModelRestriction(credentials)
    applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
    const created = await createProviderAndFinish(
      'qoder',
      'cosy',
      credentials,
      buildQoderExtra(),
      () => isCurrentQoderFlow(context)
    )
    if (created && isCurrentQoderFlow(context)) {
      qoderOAuthCompleted = true
    }
    return created
  } finally {
    // finally 按流程代次释放本次持有的创建锁。
    if (qoderProviderCreateGeneration.value === context.generation) {
      qoderProviderCreateGeneration.value = null
      submitting.value = false
    }
  }
}

const pollQoderAuthorizationOnce = async () => {
  if (qoderPollInFlight || qoderOAuthCompleted || !qoderOAuth.sessionId.value || !qoderOAuth.state.value)
    return
  const generation = qoderPollGeneration
  const flowContext = captureQoderFlowContext()
  const sessionId = qoderOAuth.sessionId.value
  const state = qoderOAuth.state.value
  if (!isCurrentQoderFlow(flowContext)) return
  qoderPollInFlight = true

  try {
    const result = await qoderOAuth.pollAuthorization({
      sessionId,
      state
    })
    if (generation !== qoderPollGeneration || !isCurrentQoderFlow(flowContext)) return
    if (!result && qoderOAuth.error.value) {
      stopQoderPolling()
      return
    }
    if (result?.status !== 'completed' || !result.token_info) return

    stopQoderPolling()
    closeQoderAuthPopup()
    await createQoderOAuthProvider(result.token_info, flowContext)
  } finally {
    if (generation === qoderPollGeneration) {
      qoderPollInFlight = false
    }
  }
}

const startQoderPolling = (intervalSeconds = 2) => {
  stopQoderPolling()
  const generation = qoderPollGeneration
  void pollQoderAuthorizationOnce()
  const intervalMs = Math.max(1, intervalSeconds) * 1000
  qoderPollTimer = window.setInterval(() => {
    if (generation !== qoderPollGeneration) return
    void pollQoderAuthorizationOnce()
  }, intervalMs)
}

const handleGenerateUrl = async () => {
  if (form.platform === 'openai') {
    await openaiOAuth.appendAuthUrl(form.proxy_id)
  } else if (form.platform === 'gemini') {
    await geminiOAuth.generateAuthUrl(
      form.proxy_id,
      oauthFlowRef.value?.projectId,
      geminiOAuthType.value,
      geminiSelectedTier.value
    )
  } else if (form.platform === 'antigravity') {
    await antigravityOAuth.generateAuthUrl(form.proxy_id)
  } else if (form.platform === 'qoder') {
    // 创建请求不可取消；完成前禁止重置授权世代，否则成功响应会被当作旧流程丢弃。
    if (submitting.value || isQoderOAuthProviderCreating.value) return
    stopQoderPolling()
    closeQoderAuthPopup()
    resetQoderOAuthCompletionState()
    const flowContext = captureQoderFlowContext()
    const authPopup = window.open('about:blank', 'qoderAuthPopup', getQoderPopupFeatures())
    const popupLease: QoderAuthPopupLease = {
      generation: ++qoderAuthPopupGeneration,
      popup: authPopup
    }
    qoderAuthPopupLease = popupLease
    const ok = await qoderOAuth.generateAuthUrl(form.proxy_id, flowContext.site)
    if (!isCurrentQoderFlow(flowContext)) {
      closeQoderAuthPopup(popupLease)
      return
    }
    if (!ok) {
      closeQoderAuthPopup(popupLease)
      return
    }
    if (authPopup) {
      authPopup.location.href = qoderOAuth.authUrl.value
      authPopup.focus()
    } else {
      appStore.showWarning(t('admin.providers.oauth.qoder.popupBlocked'))
    }
    startQoderPolling(qoderOAuth.pollInterval.value)
  } else if (form.platform === 'grok') {
    await grokOAuth.generateAuthUrl(form.proxy_id)
  } else {
    await oauth.generateAuthUrl(addMethod.value, form.proxy_id)
  }
}

const handleRemoveOpenAIAuthSession = (sessionId: string) => {
  openaiOAuth.removeAuthSession(sessionId)
}

const handleValidateRefreshToken = (rt: string) => {
  if (form.platform === 'openai') {
    handleOpenAIValidateRT(rt)
  } else if (form.platform === 'antigravity') {
    handleAntigravityValidateRT(rt)
  } else if (form.platform === 'grok') {
    handleGrokValidateRT(rt)
  }
}

const handleValidateSessionToken = (_sessionToken: string) => {
  // Session token validation removed
}

const formatDateTimeLocal = formatDateTimeLocalInput
const parseDateTimeLocal = parseDateTimeLocalInput

// Create provider and handle success/failure
const createProviderAndFinish = async (
  platform: ProviderPlatform,
  type: ProviderType,
  credentials: Record<string, unknown>,
  extra?: Record<string, unknown>,
  isCurrent: ProviderCreateGuard = () => true
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
    if (editDailyResetMode.value === 'fixed' || editWeeklyResetMode.value === 'fixed') {
      quotaExtra.quota_reset_timezone = editResetTimezone.value || 'UTC'
    }
    // Quota notify config
    writeQuotaNotifyToExtra(quotaExtra, 'create')
    if (type === 'apikey') {
      const upstreamConfig: Record<string, unknown> = {
        enabled: upstreamUsageEnabled.value,
        adapter: upstreamUsageAdapter.value
      }
      if (upstreamUsageBaseUrl.value.trim()) {
        upstreamConfig.base_url = upstreamUsageBaseUrl.value.trim()
      }
      quotaExtra.upstream_usage_query = upstreamConfig
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
  return doCreateProvider({
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
    auto_pause_on_expired: autoPauseOnExpired.value
  }, isCurrent)
}

interface OpenAIAuthCodeEntry {
  lineNumber: number
  code: string
  state: string
}

const extractOpenAIAuthParam = (value: string, param: 'code' | 'state') => {
  try {
    const parsed = new URL(value)
    return (parsed.searchParams.get(param) || '').trim()
  } catch {
    const match = value.match(new RegExp(`(?:^|[?&])${param}=([^&#\\s]+)`))
    if (!match?.[1]) {
      return ''
    }
    try {
      return decodeURIComponent(match[1].replace(/\+/g, ' ')).trim()
    } catch {
      return match[1].trim()
    }
  }
}

const parseOpenAIAuthCodeEntries = (input: string): OpenAIAuthCodeEntry[] => {
  return input
    .split(/\r?\n/)
    .map((line, index) => {
      const trimmed = line.trim()
      const code = extractOpenAIAuthParam(trimmed, 'code')
      const state = extractOpenAIAuthParam(trimmed, 'state')
      const hasOAuthParam = /(?:^|[?&])(?:code|state)=/.test(trimmed)
      const looksLikeURL = /^[a-z][a-z\d+.-]*:\/\//i.test(trimmed)
      const plainCode = !hasOAuthParam && !looksLikeURL ? trimmed : ''
      return {
        lineNumber: index + 1,
        code: code || plainCode,
        state
      }
    })
    .filter((entry) => entry.code || entry.state)
}

const getOpenAIAuthSessions = (): OpenAIOAuthSession[] => {
  if (openaiOAuth.authSessions.value.length > 0) {
    return [...openaiOAuth.authSessions.value]
  }
  if (!openaiOAuth.sessionId.value) {
    return []
  }
  return [{
    authUrl: openaiOAuth.authUrl.value,
    sessionId: openaiOAuth.sessionId.value,
    state: openaiOAuth.oauthState.value
  }]
}

const findOpenAIAuthSession = (
  entry: OpenAIAuthCodeEntry,
  sessions: OpenAIOAuthSession[],
  usedSessionIds: Set<string>
) => {
  if (entry.state) {
    return sessions.find((session) =>
      session.state === entry.state && !usedSessionIds.has(session.sessionId)
    ) || null
  }
  return sessions.find((session) => !usedSessionIds.has(session.sessionId)) || null
}

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

const buildOpenAIOAuthProviderRequest = (
  tokenInfo: OpenAITokenInfo,
  providerName: string,
  clientId?: string
): CreateProviderRequest | null => {
  const credentials = openaiOAuth.buildCredentials(tokenInfo)
  if (clientId) {
    credentials.client_id = clientId
  }
  const oauthExtra = openaiOAuth.buildExtraInfo(tokenInfo) as Record<string, unknown> | undefined
  const extra = buildOpenAIExtra(oauthExtra)

  // 将创建表单中的模型限制写入凭据。
  applyPersistedModelRestriction(credentials)
  const compactModelMapping = buildOpenAICompactModelMapping()
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  }
  if (!applyTempUnschedConfig(credentials)) {
    openaiOAuth.error.value = t('admin.providers.tempUnschedulable.rulesInvalid')
    return null
  }

  return {
    name: providerName,
    notes: form.notes,
    platform: 'openai',
    type: 'oauth',
    credentials,
    extra,
    proxy_id: form.proxy_id,
    concurrency: form.concurrency,
    load_factor: form.load_factor ?? undefined,
    priority: form.priority,
    rate_multiplier: form.rate_multiplier,
    group_ids: form.group_ids,
    expires_at: form.expires_at,
    auto_pause_on_expired: autoPauseOnExpired.value
  }
}

const createOpenAIOAuthProviderFromToken = async (
  tokenInfo: OpenAITokenInfo,
  providerName: string,
  clientId?: string
) => {
  const payload = buildOpenAIOAuthProviderRequest(tokenInfo, providerName, clientId)
  if (!payload) {
    return false
  }
  await createProtocolProvider(payload)
  return true
}

const buildOpenAIOAuthProviderName = (
  tokenInfo: OpenAITokenInfo,
  index: number,
  total: number
) => {
  const baseName = form.name || tokenInfo.email || 'OpenAI OAuth Provider'
  return total > 1 ? `${baseName} #${index + 1}` : baseName
}

// OpenAI OAuth token 请求只在启用 TLS 指纹路由器时携带路由器 ID。
const selectedOpenAITokenTLSRouterId = () => {
  return tlsFingerprintEnabled.value ? tlsFingerprintRouterId.value : null
}

// Grok 手动 RT 批量验证和创建
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
        const providerName = refreshTokens.length > 1 ? `${form.name || tokenInfo.email || 'Grok OAuth Provider'} #${i + 1}` : (form.name || tokenInfo.email || 'Grok OAuth Provider')

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
          auto_pause_on_expired: autoPauseOnExpired.value
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
          : t('admin.providers.providerCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0) {
      appStore.showWarning(t('admin.providers.oauth.batchPartialSuccess', { success: successCount, failed: failedCount }))
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

const handleGrokImportSSO = async (ssoInput: string) => {
  // 与 OpenAI/Grok RT 批量导入保持一致：每行一个令牌，前端不去重。
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
      auto_pause_on_expired: autoPauseOnExpired.value
    })

    const successCount = result.created?.length || 0
    const failedCount = result.failed?.length || 0
    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        ssoTokens.length > 1
          ? t('admin.providers.oauth.batchSuccess', { count: successCount })
          : t('admin.providers.providerCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      // 与 OpenAI/Grok RT 一致：保留输入、显示失败项并刷新列表。
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
      )
      grokOAuth.error.value = (result.failed || [])
        .map((item) => `#${item.index}: ${item.error || 'Unknown error'}`)
        .join('\n')
      emit('created')
    } else {
      grokOAuth.error.value = (result.failed || [])
        .map((item) => `#${item.index}: ${item.error || 'Unknown error'}`)
        .join('\n') || t('admin.providers.oauth.grok.failedToConvertSSO')
      appStore.showError(t('admin.providers.oauth.batchFailed'))
    }
  } catch (error: any) {
    grokOAuth.error.value = error.response?.data?.detail || error.message || t('admin.providers.oauth.grok.failedToConvertSSO')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

// OpenAI OAuth 授权码批量兑换和创建
const handleOpenAIExchange = async (authCodeInput: string) => {
  const oauthClient = openaiOAuth
  if (!authCodeInput.trim()) return

  const entries = parseOpenAIAuthCodeEntries(authCodeInput)
  if (entries.length === 0) {
    oauthClient.error.value = t('admin.providers.oauth.openai.pleaseEnterAuthCode')
    return
  }

  const sessions = getOpenAIAuthSessions()
  if (sessions.length === 0) {
    oauthClient.error.value = t('admin.providers.oauth.openai.pleaseGenerateAuthUrl')
    appStore.showError(oauthClient.error.value)
    return
  }
  if (!ensureOpenAITempUnschedConfigReady()) {
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []
  const usedSessionIds = new Set<string>()

  try {
    for (let i = 0; i < entries.length; i++) {
      const entry = entries[i]
      const session = findOpenAIAuthSession(entry, sessions, usedSessionIds)
      if (!entry.code) {
        failedCount++
        errors.push(`#${entry.lineNumber}: ${t('admin.providers.oauth.openai.pleaseEnterAuthCode')}`)
        continue
      }
      if (!session) {
        failedCount++
        errors.push(`#${entry.lineNumber}: ${t('admin.providers.oauth.openai.noMatchingAuthUrl')}`)
        continue
      }

      usedSessionIds.add(session.sessionId)
      const stateToUse = entry.state || session.state
      if (!stateToUse) {
        failedCount++
        errors.push(`#${entry.lineNumber}: ${t('admin.providers.oauth.openai.missingState')}`)
        continue
      }

      try {
        const tokenInfo = await oauthClient.exchangeAuthCode(
          entry.code,
          session.sessionId,
          stateToUse,
          form.proxy_id,
          selectedOpenAITokenTLSRouterId()
        )
        oauthClient.loading.value = true
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${entry.lineNumber}: ${oauthClient.error.value || t('admin.providers.oauth.openai.failedToExchangeCode')}`)
          oauthClient.error.value = ''
          continue
        }

        oauthClient.removeAuthSession(session.sessionId)
        const providerName = buildOpenAIOAuthProviderName(tokenInfo, i, entries.length)
        const created = await createOpenAIOAuthProviderFromToken(tokenInfo, providerName)
        if (!created) {
          failedCount++
          errors.push(`#${entry.lineNumber}: ${oauthClient.error.value || t('admin.providers.failedToCreate')}`)
          oauthClient.error.value = ''
          continue
        }

        successCount++
      } catch (error: any) {
        failedCount++
        const errMsg = error.response?.data?.detail || error.message || t('admin.providers.oauth.authFailed')
        errors.push(`#${entry.lineNumber}: ${errMsg}`)
      }
    }

    if (successCount > 0 && failedCount === 0) {
      appStore.showSuccess(
        entries.length > 1
          ? t('admin.providers.oauth.batchSuccess', { count: successCount })
          : t('admin.providers.providerCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
      )
      oauthClient.error.value = errors.join('\n')
      emit('created')
    } else {
      oauthClient.error.value = errors.join('\n')
      appStore.showError(t('admin.providers.oauth.batchFailed'))
    }
  } finally {
    oauthClient.loading.value = false
  }
}

// OpenAI 手动 RT 批量验证和创建
// OpenAI Mobile RT client_id
const OPENAI_MOBILE_RT_CLIENT_ID = 'app_LlGpXReQgckcGGUo2JrYvtJK'

const buildOpenAICodexImportCredentialExtras = (): Record<string, unknown> | null => {
  const credentials: Record<string, unknown> = {}
  // 模型映射和最终白名单分别保存。
  applyPersistedModelRestriction(credentials)

  const compactModelMapping = buildOpenAICompactModelMapping()
  if (compactModelMapping) {
    credentials.compact_model_mapping = compactModelMapping
  }

  if (!applyTempUnschedConfig(credentials)) {
    return null
  }
  return credentials
}

const formatCodexImportMessages = (messages?: CodexSessionImportMessage[]) => {
  return (messages || [])
    .map((item) => {
      const name = item.name ? ` ${item.name}` : ''
      return `#${item.index}${name}: ${item.message}`
    })
    .join('\n')
}

const isAgentIdentityImportContent = (content: string) => {
  const isAgentIdentityValue = (value: unknown): boolean => {
    if (Array.isArray(value)) return value.length > 0 && value.every(isAgentIdentityValue)
    if (!value || typeof value !== 'object') return false
    const record = value as Record<string, unknown>
    const authMode = record.auth_mode ?? record.authMode
    const agentIdentity = record.agent_identity ?? record.agentIdentity
    return (typeof authMode === 'string' && authMode.toLowerCase() === 'agentidentity')
      || (!!agentIdentity && typeof agentIdentity === 'object')
  }

  try {
    return isAgentIdentityValue(JSON.parse(content))
  } catch {
    const lines = content.split('\n').map((line) => line.trim()).filter(Boolean)
    if (lines.length === 0) return false
    try {
      return lines.every((line) => isAgentIdentityValue(JSON.parse(line)))
    } catch {
      return false
    }
  }
}

const handleOpenAIImportCodexSession = async (content: string) => {
  const oauthClient = openaiOAuth
  const trimmed = content.trim()
  if (!trimmed) {
    oauthClient.error.value = t('admin.providers.oauth.openai.codexSessionEmpty')
    return
  }
  if (oauthFlowRef.value?.inputMethod === 'agent_identity' && !isAgentIdentityImportContent(trimmed)) {
    oauthClient.error.value = t('admin.providers.oauth.openai.agentIdentityInvalid')
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  try {
    await loadProtocolCatalog()
    // Session 导入使用表单中的模型限制和协议配置。
    const credentialExtras = buildOpenAICodexImportCredentialExtras()
    if (credentialExtras === null) {
      return
    }
    const extra = buildOpenAIExtra()
    const result = await adminAPI.providers.importCodexSession({
      content: trimmed,
      name: form.name,
      notes: form.notes || null,
      proxy_id: form.proxy_id,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      group_ids: form.group_ids,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value,
      credential_extras: { ...credentialExtras, upstream_protocols: (upstreamProtocols.value ?? nativeProtocolOptions('openai', 'oauth', 'personalAccessToken')).filter(id => nativeProtocolOptions('openai', 'oauth', 'personalAccessToken').includes(id)) },
      extra: withUpstreamRequestIdHeader(extra),
      update_existing: true
    })

    const successCount = result.created + result.updated
    const params = {
      created: result.created,
      updated: result.updated,
      skipped: result.skipped,
      failed: result.failed
    }

    if (successCount > 0 && result.failed === 0) {
      appStore.showSuccess(t('admin.providers.oauth.openai.codexSessionImportSuccess', params))
      emit('created')
      handleClose()
      return
    }

    const errorText = formatCodexImportMessages(result.errors)
    const warningText = formatCodexImportMessages(result.warnings)
    oauthClient.error.value = [errorText, warningText].filter(Boolean).join('\n')

    if (result.failed === 0) {
      appStore.showWarning(t('admin.providers.oauth.openai.codexSessionImportSuccess', params))
      return
    }

    if (successCount > 0) {
      appStore.showWarning(t('admin.providers.oauth.openai.codexSessionImportPartial', params))
      emit('created')
      return
    }

    appStore.showError(t('admin.providers.oauth.openai.codexSessionImportFailed'))
  } catch (error: any) {
    oauthClient.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.providers.oauth.openai.codexSessionImportFailed')
    appStore.showError(oauthClient.error.value)
  } finally {
    oauthClient.loading.value = false
  }
}

const handleOpenAIImportCodexPAT = async (accessToken: string) => {
  const oauthClient = openaiOAuth
  const trimmed = accessToken.trim()
  if (!trimmed) {
    oauthClient.error.value = t('admin.providers.oauth.openai.codexPatEmpty')
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  try {
    // PAT 导入使用表单中的模型限制和协议配置。
    const credentialExtras = buildOpenAICodexImportCredentialExtras()
    if (credentialExtras === null) {
      return
    }
    const extra = buildOpenAIExtra()
    await adminAPI.providers.createOpenAICodexPAT({
      access_token: trimmed,
      name: form.name,
      notes: form.notes || null,
      proxy_id: form.proxy_id,
      concurrency: form.concurrency,
      load_factor: form.load_factor ?? undefined,
      priority: form.priority,
      rate_multiplier: form.rate_multiplier,
      group_ids: form.group_ids,
      expires_at: form.expires_at,
      auto_pause_on_expired: autoPauseOnExpired.value,
      credential_extras: Object.keys(credentialExtras).length > 0 ? credentialExtras : undefined,
      extra: withUpstreamRequestIdHeader(extra)
    })
    appStore.showSuccess(t('admin.providers.providerCreated'))
    emit('created')
    handleClose()
  } catch (error: any) {
    oauthClient.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.providers.oauth.openai.codexPatImportFailed')
    appStore.showError(oauthClient.error.value)
  } finally {
    oauthClient.loading.value = false
  }
}

// OpenAI RT 批量验证和创建（共享逻辑）
const handleOpenAIBatchRT = async (refreshTokenInput: string, clientId?: string) => {
  const oauthClient = openaiOAuth
  if (!refreshTokenInput.trim()) return

  const refreshTokens = refreshTokenInput
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt)

  if (refreshTokens.length === 0) {
    oauthClient.error.value = t('admin.providers.oauth.openai.pleaseEnterRefreshToken')
    return
  }
  if (!ensureOpenAITempUnschedConfigReady()) {
    return
  }

  oauthClient.loading.value = true
  oauthClient.error.value = ''

  let successCount = 0
  let failedCount = 0
  const errors: string[] = []

  try {
    for (let i = 0; i < refreshTokens.length; i++) {
      try {
        const tokenInfo = await oauthClient.validateRefreshToken(
          refreshTokens[i],
          form.proxy_id,
          clientId,
          selectedOpenAITokenTLSRouterId()
        )
        oauthClient.loading.value = true
        if (!tokenInfo) {
          failedCount++
          errors.push(`#${i + 1}: ${oauthClient.error.value || 'Validation failed'}`)
          oauthClient.error.value = ''
          continue
        }

        const providerName = buildOpenAIOAuthProviderName(tokenInfo, i, refreshTokens.length)
        const created = await createOpenAIOAuthProviderFromToken(tokenInfo, providerName, clientId)
        if (!created) {
          failedCount++
          errors.push(`#${i + 1}: ${oauthClient.error.value || t('admin.providers.failedToCreate')}`)
          oauthClient.error.value = ''
          continue
        }

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
          : t('admin.providers.providerCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
      )
      oauthClient.error.value = errors.join('\n')
      emit('created')
    } else {
      oauthClient.error.value = errors.join('\n')
      appStore.showError(t('admin.providers.oauth.batchFailed'))
    }
  } finally {
    oauthClient.loading.value = false
  }
}

// 手动输入 RT（Codex CLI client_id，默认）
const handleOpenAIValidateRT = (rt: string) => handleOpenAIBatchRT(rt)

// 手动输入 Mobile RT
const handleOpenAIValidateMobileRT = (rt: string) => handleOpenAIBatchRT(rt, OPENAI_MOBILE_RT_CLIENT_ID)

// Antigravity 手动 RT 批量验证和创建
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
          form.proxy_id
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
          auto_pause_on_expired: autoPauseOnExpired.value
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
          : t('admin.providers.providerCreated')
      )
      emit('created')
      handleClose()
    } else if (successCount > 0 && failedCount > 0) {
      appStore.showWarning(
        t('admin.providers.oauth.batchPartialSuccess', { success: successCount, failed: failedCount })
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

// Gemini OAuth 授权码兑换
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
      tierId: geminiSelectedTier.value
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

// Antigravity OAuth 授权码兑换
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
      proxyId: form.proxy_id
    })
		if (!tokenInfo) return

		const credentials = antigravityOAuth.buildCredentials(tokenInfo)
		applyAntigravityProjectID(credentials, antigravityProjectId.value, 'create')
		applyInterceptWarmup(credentials, interceptWarmupRequests.value, 'create')
		// Antigravity 只使用映射模式
		const antigravityModelMapping = buildModelMappingObject(
			'mapping',
			[],
			antigravityModelMappings.value
		)
		if (antigravityModelMapping) {
			credentials.model_mapping = antigravityModelMapping
		}
    credentials.model_whitelist = [...antigravityWhitelistModels.value]
		const extra = buildAntigravityExtra()
		await createProviderAndFinish('antigravity', 'oauth', credentials, extra)
  } catch (error: any) {
    antigravityOAuth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(antigravityOAuth.error.value)
  } finally {
    antigravityOAuth.loading.value = false
  }
}

const handleQoderExchange = async (authCode: string) => {
  const flowContext = captureQoderFlowContext()
  if (
    !qoderOAuth.sessionId.value ||
    !isCurrentQoderFlow(flowContext) ||
    qoderOAuthCompleted ||
    qoderProviderCreateGeneration.value !== null
  ) return

  const shouldResumePolling = qoderPollTimer !== null
  const sessionId = qoderOAuth.sessionId.value
  stopQoderPolling()
  qoderOAuth.loading.value = true
  qoderOAuth.error.value = ''
  const resumePollingIfNeeded = () => {
    if (
      isCurrentQoderFlow(flowContext) &&
      shouldResumePolling &&
      !qoderPollTimer &&
      qoderOAuth.sessionId.value &&
      qoderOAuth.state.value
    ) {
      startQoderPolling(qoderOAuth.pollInterval.value)
    }
  }

  let exchanged = false
  try {
    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || qoderOAuth.state.value
    if (!stateToUse) {
      qoderOAuth.error.value = t('admin.providers.oauth.authFailed')
      appStore.showError(qoderOAuth.error.value)
      resumePollingIfNeeded()
      return
    }

    const rawInput = authCode.trim()
    const tokenInfo = await qoderOAuth.exchangeAuthCode({
      code: rawInput,
      callbackUrl: rawInput,
      sessionId,
      state: stateToUse
    })
    if (!isCurrentQoderFlow(flowContext)) return
    if (!tokenInfo) {
      resumePollingIfNeeded()
      return
    }

    exchanged = true
    await createQoderOAuthProvider(tokenInfo, flowContext)
  } catch (error: any) {
    if (!isCurrentQoderFlow(flowContext)) return
    qoderOAuth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(qoderOAuth.error.value)
    if (!exchanged) {
      resumePollingIfNeeded()
    }
  } finally {
    if (isCurrentQoderFlow(flowContext)) {
      qoderOAuth.loading.value = false
    }
  }
}

// Grok OAuth 授权码兑换
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
      proxyId: form.proxy_id
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

// Anthropic OAuth 授权码兑换
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
      ...proxyConfig
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
      const DEFAULT_BASE_RPM = 15
      extra.base_rpm = (baseRpm.value != null && baseRpm.value > 0)
        ? baseRpm.value
        : DEFAULT_BASE_RPM
      extra.rpm_strategy = rpmStrategy.value
      if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
        extra.rpm_sticky_buffer = rpmStickyBuffer.value
      }
    }

    // UMQ mode（独立于 RPM）
    if (userMsgQueueMode.value) {
      extra.user_msg_queue_mode = userMsgQueueMode.value
    }

    // Add TLS fingerprint settings
    if (tlsFingerprintEnabled.value) {
      extra.enable_tls_fingerprint = true
      if (tlsFingerprintProfileId.value) {
        extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
      }
      if (form.platform === 'openai' && providerCategory.value === 'oauth-based' && tlsFingerprintRouterId.value) {
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
    await createProviderAndFinish(form.platform, addMethod.value as ProviderType, credentials, extra)
  } catch (error: any) {
    oauth.error.value = error.response?.data?.detail || t('admin.providers.oauth.authFailed')
    appStore.showError(oauth.error.value)
  } finally {
    oauth.loading.value = false
  }
}

// 主入口：根据平台路由到对应处理函数
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
          ...proxyConfig
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
          const DEFAULT_BASE_RPM = 15
          extra.base_rpm = (baseRpm.value != null && baseRpm.value > 0)
            ? baseRpm.value
            : DEFAULT_BASE_RPM
          extra.rpm_strategy = rpmStrategy.value
          if (rpmStickyBuffer.value != null && rpmStickyBuffer.value > 0) {
            extra.rpm_sticky_buffer = rpmStickyBuffer.value
          }
        }

        // UMQ mode（独立于 RPM）
        if (userMsgQueueMode.value) {
          extra.user_msg_queue_mode = userMsgQueueMode.value
        }

        // Add TLS fingerprint settings
        if (tlsFingerprintEnabled.value) {
          extra.enable_tls_fingerprint = true
          if (tlsFingerprintProfileId.value) {
            extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
          }
          if (form.platform === 'openai' && providerCategory.value === 'oauth-based' && tlsFingerprintRouterId.value) {
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
          auto_pause_on_expired: autoPauseOnExpired.value
        })

        successCount++
      } catch (error: any) {
        failedCount++
        errors.push(
          t('admin.providers.oauth.keyAuthFailed', {
            index: i + 1,
            error: error.response?.data?.detail || t('admin.providers.oauth.authFailed')
          })
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

onBeforeUnmount(() => {
  stopQoderPolling()
  closeQoderAuthPopup()
})
</script>

<style scoped>
.provider-choice-badge {
  @apply rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300;
}
</style>
