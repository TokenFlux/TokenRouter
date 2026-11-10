<template>
  <div class="space-y-6">
    <!-- Platform identity and instructions share the header with the authorization method; narrow screens stack those groups vertically. -->
    <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div class="flex min-w-0 items-center gap-3">
        <span
          class="flex h-10 w-10 shrink-0 items-center justify-center rounded-control"
          :class="platformBadgeLightClass(platform)"
        >
          <PlatformIcon :platform="platform" size="md" />
        </span>
        <div class="min-w-0">
          <h4 class="text-sm font-semibold text-primary-900 dark:text-dark-50">{{ oauthTitle }}</h4>
          <p v-if="inputMethod === 'manual'" class="input-hint">{{ oauthFollowSteps }}</p>
        </div>
      </div>

      <!-- Up to four methods use segmented controls, while larger method sets use a dropdown beside the label. -->
      <div v-if="showMethodSelection" class="flex shrink-0 items-center gap-3">
        <label :for="`${uid}-method`" class="shrink-0 text-sm text-gray-500 dark:text-gray-400">
          {{ methodLabel || t('admin.providers.oauth.authMethod') }}
        </label>
        <SettingsSegmented
          v-if="methodOptions.length <= 4"
          v-model="inputMethod"
          :ariaLabel="methodLabel || t('admin.providers.oauth.authMethod')"
          :options="methodOptions"
        />
        <Select
          v-else
          :id="`${uid}-method`"
          v-model="inputMethod"
          class="min-w-0 flex-1 sm:w-56 sm:flex-none"
          data-testid="oauth-method-select"
          :options="methodOptions"
        />
      </div>
    </div>

    <!-- Refresh Token（OpenAI / Antigravity / Grok / Mobile RT） -->
    <OAuthCredentialImport
      v-if="inputMethod === 'refresh_token' || inputMethod === 'mobile_refresh_token'"
      v-model="refreshTokenInput"
      :description="t(getOAuthKey('refreshTokenDesc'))"
      label="Refresh Token"
      :placeholder="t(getOAuthKey('refreshTokenPlaceholder'))"
      :count="parsedRefreshTokenCount"
      :count-hint="
        t('admin.providers.oauth.batchCreateProviders', { count: parsedRefreshTokenCount })
      "
      :loading="loading"
      :error="error"
      :submit-label="t(getOAuthKey('validateAndCreate'))"
      :loading-label="t(getOAuthKey('validating'))"
      @submit="handleValidateRefreshToken"
    />

    <!-- Grok Web SSO input for Grok Build conversion. -->
    <OAuthCredentialImport
      v-if="inputMethod === 'sso_cookie'"
      v-model="ssoCookieInput"
      :description="t(getOAuthKey('ssoCookieDesc'))"
      :label="t(getOAuthKey('ssoCookieLabel'))"
      :placeholder="t(getOAuthKey('ssoCookiePlaceholder'))"
      :hint="t(getOAuthKey('ssoCookieHint'))"
      :count="parsedSSOCount"
      :rows="5"
      :loading="loading"
      :error="error"
      :submit-label="t(getOAuthKey('convertSSOAndCreate'))"
      :loading-label="t(getOAuthKey('convertingSSO'))"
      @submit="handleImportSSO"
    />

    <!-- Codex auth.json, Agent Identity, and session credentials share the batch import form. -->
    <OAuthCredentialImport
      v-if="inputMethod === 'codex_session' || inputMethod === 'agent_identity'"
      v-model="codexSessionInput"
      :description="
        t(
          isAgentIdentityInput
            ? 'admin.providers.oauth.openai.agentIdentityDesc'
            : 'admin.providers.oauth.openai.codexSessionDesc',
        )
      "
      :label="
        t(
          isAgentIdentityInput
            ? 'admin.providers.oauth.openai.agentIdentityInputLabel'
            : 'admin.providers.oauth.openai.codexSessionInputLabel',
        )
      "
      :placeholder="
        t(
          isAgentIdentityInput
            ? 'admin.providers.oauth.openai.agentIdentityPlaceholder'
            : 'admin.providers.oauth.openai.codexSessionPlaceholder',
        )
      "
      :hint="
        t(
          isAgentIdentityInput
            ? 'admin.providers.oauth.openai.agentIdentityHint'
            : 'admin.providers.oauth.openai.codexSessionHint',
        )
      "
      :count="parsedCodexSessionCount"
      :rows="8"
      :loading="loading"
      :error="error"
      :submit-label="t('admin.providers.oauth.openai.codexSessionImportAndCreate')"
      :loading-label="t('admin.providers.oauth.openai.validating')"
      @submit="handleImportCodexSession"
    />

    <!-- Codex PAT -->
    <OAuthCredentialImport
      v-if="inputMethod === 'codex_pat'"
      v-model="codexPATInput"
      password
      :description="t('admin.providers.oauth.openai.codexPatDesc')"
      :label="t('admin.providers.oauth.openai.codexPatInputLabel')"
      :placeholder="t('admin.providers.oauth.openai.codexPatPlaceholder')"
      :hint="t('admin.providers.oauth.openai.codexPatHint')"
      :loading="loading"
      :error="error"
      :submit-label="t('admin.providers.oauth.openai.codexPatImportAndCreate')"
      :loading-label="t('admin.providers.oauth.openai.validating')"
      @submit="handleImportCodexPAT"
    />

    <!-- Cookie authorization input. -->
    <OAuthCredentialImport
      v-if="inputMethod === 'cookie'"
      v-model="sessionKeyInput"
      :description="t('admin.providers.oauth.cookieAutoAuthDesc')"
      :label="t('admin.providers.oauth.sessionKey')"
      :placeholder="
        allowMultiple
          ? t('admin.providers.oauth.sessionKeyPlaceholder')
          : t('admin.providers.oauth.sessionKeyPlaceholderSingle')
      "
      :count="allowMultiple ? parsedKeyCount : 0"
      :count-hint="t('admin.providers.oauth.batchCreateProviders', { count: parsedKeyCount })"
      :loading="loading"
      :error="error"
      :submit-label="t('admin.providers.oauth.startAutoAuth')"
      :loading-label="t('admin.providers.oauth.authorizing')"
      @submit="handleCookieAuth"
    >
      <template v-if="showHelp" #label-extra>
        <button
          type="button"
          class="inline-flex rounded-compact text-gray-400 hover:text-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
          :aria-label="t('admin.providers.oauth.howToGetSessionKey')"
          :aria-expanded="showHelpDialog"
          @click="showHelpDialog = !showHelpDialog"
        >
          <Icon name="questionCircle" size="sm" />
        </button>
      </template>
      <Collapse :open="showHelpDialog && showHelp" unmount-on-hide>
        <SettingsNotice>
          <p class="font-medium">{{ t('admin.providers.oauth.howToGetSessionKey') }}</p>
          <ol class="list-inside list-decimal space-y-1">
            <li>{{ t('admin.providers.oauth.step1') }}</li>
            <li>{{ t('admin.providers.oauth.step2') }}</li>
            <li>{{ t('admin.providers.oauth.step3') }}</li>
            <li>{{ t('admin.providers.oauth.step4') }}</li>
            <li>{{ t('admin.providers.oauth.step5') }}</li>
            <li>{{ t('admin.providers.oauth.step6') }}</li>
          </ol>
          <p v-text="t('admin.providers.oauth.sessionKeyFormat')"></p>
        </SettingsNotice>
      </Collapse>
    </OAuthCredentialImport>

    <!-- Manual authorization proceeds through URL generation, browser approval, and pasted callback input. -->
    <ol v-if="inputMethod === 'manual'">
      <OAuthStep :index="1" :title="oauthStep1GenerateUrl" :done="hasGeneratedUrl">
        <div v-if="showProjectId && platform === 'gemini'">
          <div class="mb-1.5 flex items-center gap-2">
            <label :for="`${uid}-project-id`" class="input-label mb-0">{{
              t('admin.providers.oauth.gemini.projectIdLabel')
            }}</label>
            <a
              href="https://console.cloud.google.com/"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-1 text-xs text-link"
            >
              <Icon name="questionCircle" size="xs" />
              {{ t('admin.providers.oauth.gemini.howToGetProjectId') }}
            </a>
          </div>
          <input
            :id="`${uid}-project-id`"
            v-model="projectId"
            type="text"
            class="input font-mono text-sm"
            :placeholder="t('admin.providers.oauth.gemini.projectIdPlaceholder')"
          />
          <p class="input-hint">{{ t('admin.providers.oauth.gemini.projectIdHint') }}</p>
        </div>

        <!-- OpenAI can append authorization sessions so the parent can create providers from multiple callbacks. -->
        <template v-if="isOpenAI">
          <div v-if="openAIAuthSessions.length > 0" class="space-y-2">
            <div
              v-for="(session, index) in openAIAuthSessions"
              :key="session.sessionId"
              class="flex items-center gap-2"
            >
              <span
                class="w-8 shrink-0 text-center text-xs font-medium tabular-nums text-gray-500 dark:text-gray-400"
              >
                #{{ index + 1 }}
              </span>
              <input
                :value="session.authUrl"
                readonly
                type="text"
                class="input min-w-0 flex-1 font-mono text-xs"
                :aria-label="t('admin.providers.oauth.openAuthUrl')"
              />
              <button
                type="button"
                class="btn btn-secondary btn-icon shrink-0 px-0"
                :title="t('admin.providers.oauth.copyAuthUrl')"
                :aria-label="t('admin.providers.oauth.copyAuthUrl')"
                @click="handleCopySessionUrl(session.authUrl)"
              >
                <Icon name="copy" size="sm" />
              </button>
              <a
                :href="session.authUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="btn btn-primary btn-icon shrink-0 px-0"
                :title="t('admin.providers.oauth.openAuthUrl')"
                :aria-label="t('admin.providers.oauth.openAuthUrl')"
              >
                <Icon name="externalLink" size="sm" />
              </a>
              <button
                v-if="isOpenAIBatchAuth"
                type="button"
                class="btn btn-secondary btn-icon shrink-0 px-0 text-red-600 hover:text-red-700 dark:text-red-400"
                :title="t('common.delete')"
                :aria-label="t('common.delete')"
                @click="handleRemoveAuthSession(session.sessionId)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
            <p v-if="isOpenAIBatchAuth" class="input-hint">
              {{
                t('admin.providers.oauth.openai.multiAuthUrlHint', {
                  count: openAIAuthSessions.length,
                })
              }}
            </p>
          </div>
          <button
            type="button"
            :disabled="loading"
            :class="openAIAuthSessions.length > 0 ? 'btn btn-secondary' : 'btn btn-primary'"
            @click="handleGenerateUrl"
          >
            <Icon
              v-if="loading"
              name="loader"
              size="sm"
              :animate-on-hover="false"
              class="animate-spin"
            />
            <Icon v-else name="plus" size="sm" />
            {{
              loading
                ? t('admin.providers.oauth.generating')
                : isOpenAIBatchAuth
                  ? t('admin.providers.oauth.openai.appendAuthUrl')
                  : oauthGenerateAuthUrl
            }}
          </button>
        </template>

        <button
          v-else-if="!authUrl"
          type="button"
          :disabled="loading"
          class="btn btn-primary"
          @click="handleGenerateUrl"
        >
          <Icon
            v-if="loading"
            name="loader"
            size="sm"
            :animate-on-hover="false"
            class="animate-spin"
          />
          <Icon v-else name="link" size="sm" />
          {{ loading ? t('admin.providers.oauth.generating') : oauthGenerateAuthUrl }}
        </button>

        <div v-else class="space-y-2">
          <div class="flex items-center gap-2">
            <input
              :value="authUrl"
              readonly
              type="text"
              class="input min-w-0 flex-1 font-mono text-xs"
              :aria-label="t('admin.providers.oauth.openAuthUrl')"
            />
            <button
              type="button"
              class="btn btn-secondary btn-icon shrink-0 px-0"
              :title="t('admin.providers.oauth.copyAuthUrl')"
              :aria-label="t('admin.providers.oauth.copyAuthUrl')"
              @click="handleCopyUrl"
            >
              <Icon v-if="!copied" name="clipboard" size="sm" />
              <Icon
                v-else
                name="check"
                size="sm"
                class="text-green-500"
                :stroke-width="2"
                :animate-on-hover="false"
              />
            </button>
            <a
              :href="authUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="btn btn-primary btn-icon shrink-0 px-0"
              :title="t('admin.providers.oauth.openAuthUrl')"
              :aria-label="t('admin.providers.oauth.openAuthUrl')"
            >
              <Icon name="externalLink" size="sm" />
            </a>
          </div>
          <button
            type="button"
            :disabled="loading"
            class="inline-flex items-center gap-1 text-xs text-primary-600 hover:text-primary-700 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-500"
            @click="handleRegenerate"
          >
            <Icon name="refresh" size="xs" />
            {{ t('admin.providers.oauth.regenerate') }}
          </button>
        </div>
      </OAuthStep>

      <OAuthStep
        :index="2"
        :title="oauthStep2OpenUrl"
        :description="oauthOpenUrlDesc"
        :done="hasAuthCode"
      >
        <!-- Platform callback instructions and proxy guidance. -->
        <SettingsNotice v-if="showLocalCallbackNotice || oauthImportantNotice" tone="warning">
          <p v-text="oauthImportantNotice"></p>
        </SettingsNotice>
        <SettingsNotice v-if="showProxyWarning" tone="warning">
          <p v-text="t('admin.providers.oauth.proxyWarning')"></p>
        </SettingsNotice>
      </OAuthStep>

      <OAuthStep
        :index="3"
        :title="oauthStep3EnterCode"
        :description="oauthAuthCodeDesc"
        :done="hasAuthCode"
        last
      >
        <div>
          <div class="mb-1.5 flex items-center gap-2">
            <label :for="`${uid}-auth-code`" class="input-label mb-0">{{ oauthAuthCode }}</label>
            <span
              v-if="isOpenAI && parsedAuthCodeLineCount > 1"
              class="rounded-full bg-primary-50 px-2 py-0.5 text-xs font-medium text-primary-700 dark:bg-primary-500/10 dark:text-primary-300"
            >
              {{ t('admin.providers.oauth.keysCount', { count: parsedAuthCodeLineCount }) }}
            </span>
          </div>
          <textarea
            :id="`${uid}-auth-code`"
            v-model="authCodeInput"
            :rows="isOpenAI ? 5 : 3"
            :class="['input font-mono text-sm', isOpenAI ? 'resize-y' : 'resize-none']"
            :placeholder="oauthAuthCodePlaceholder"
          ></textarea>
          <p v-if="isOpenAI && parsedAuthCodeLineCount > 1" class="input-hint">
            {{
              t('admin.providers.oauth.batchCreateProviders', { count: parsedAuthCodeLineCount })
            }}
          </p>
          <p class="input-hint">{{ oauthAuthCodeHint }}</p>
        </div>

        <!-- Gemini callback input retains the state parameter for the parent code-exchange request. -->
        <SettingsNotice v-if="platform === 'gemini'" tone="warning">
          <p class="font-medium">{{ t('admin.providers.oauth.gemini.stateWarningTitle') }}</p>
          <p>{{ t('admin.providers.oauth.gemini.stateWarningDesc') }}</p>
        </SettingsNotice>

        <SettingsNotice v-if="error" tone="error">
          <p class="whitespace-pre-line">{{ error }}</p>
        </SettingsNotice>
      </OAuthStep>
    </ol>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import Icon from '@/components/icons/Icon.vue'
import Collapse from '@/components/common/Collapse.vue'
import Select from '@/components/common/Select.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import OAuthCredentialImport from '@/components/provider/form/OAuthCredentialImport.vue'
import OAuthStep from '@/components/provider/form/OAuthStep.vue'
import { platformBadgeLightClass } from '@/utils/platformColors'
import type { AddMethod, AuthInputMethod } from '@/composables/useProviderOAuth'
import type { OpenAIOAuthSession } from '@/composables/useOpenAIOAuth'
import type { ProviderPlatform } from '@/api/admin/providers.types'

// OAuthAuthorizationFlow.vue presents manual authorization and credential import methods
// selected by its caller. Local drafts contain pasted codes, tokens, session records, and
// callback state, while emitted events delegate exchange or import to the parent. The
// exposed refs let dialog controllers read those drafts and reset them when their own
// lifecycle requires it.
//
// Single callback input extracts the code and retains state separately for exchange.
// OpenAI batch sessions keep the raw multiline callbacks so the parent can match each
// state to its authorization session. Method visibility follows the supplied flags;
// platform selection supplies labels and callback guidance.

// Props describes caller-owned authorization settings and display state for this
// component.
interface Props {
  addMethod: AddMethod // OAuth or setup-token mode.
  authUrl?: string // Current single-session authorization URL.
  sessionId?: string // Current single-session authorization identifier.
  loading?: boolean // Caller-owned pending operation flag.
  error?: string // Caller-owned error display text.
  showHelp?: boolean // Shows authorization help access.
  showProxyWarning?: boolean // Shows proxy configuration guidance.
  allowMultiple?: boolean // Allows multiline credential guidance.
  methodLabel?: string // Caller-supplied method label text.
  showCookieOption?: boolean // Enables the cookie method choice.
  showRefreshTokenOption?: boolean // Enables refresh-token method choice.
  showMobileRefreshTokenOption?: boolean // Enables mobile refresh-token choice.
  showSessionTokenOption?: boolean // Enables session-token method choice.
  showAccessTokenOption?: boolean // Enables access-token method choice.
  showCodexSessionImportOption?: boolean // Enables Codex session import choice.
  showAgentIdentityOption?: boolean // Enables Agent Identity import choice.
  showCodexPatOption?: boolean // Enables personal access-token choice.
  showSsoOption?: boolean // Enables SSO cookie import choice.
  showManualOption?: boolean // Enables manual callback method choice.
  // Initial method and later reset target; prop changes replace the local selection even
  // if unavailable.
  initialInputMethod?: AuthInputMethod
  platform?: ProviderPlatform // Provider platform identifier.
  showProjectId?: boolean // Shows the project input.
  // OpenAI batch sessions take precedence over the single authUrl/sessionId pair and
  // preserve callback state matching.
  authSessions?: OpenAIOAuthSession[]
}

// props exposes caller-owned inputs used by the template and derived state in this
// component.
const props = withDefaults(defineProps<Props>(), {
  authUrl: '',
  sessionId: '',
  loading: false,
  error: '',
  showHelp: true,
  showProxyWarning: true,
  allowMultiple: false,
  methodLabel: '',
  showCookieOption: true,
  showRefreshTokenOption: false,
  showMobileRefreshTokenOption: false,
  showSessionTokenOption: false,
  showAccessTokenOption: false,
  showCodexSessionImportOption: false,
  showAgentIdentityOption: false,
  showCodexPatOption: false,
  showSsoOption: false,
  showManualOption: true,
  initialInputMethod: 'manual',
  platform: 'anthropic',
  showProjectId: true,
  authSessions: () => [],
})

// emit publishes typed draft changes and actions to the parent that owns this component.
const emit = defineEmits<{
  'generate-url': [] // Authorization URL generation request.
  'remove-auth-session': [sessionId: string] // Session removal identifier payload.
  'exchange-code': [code: string] // Manual authorization code payload.
  'cookie-auth': [sessionKey: string] // Untrimmed cookie input payload.
  'validate-refresh-token': [refreshToken: string] // Trimmed refresh-token input payload.
  'validate-mobile-refresh-token': [refreshToken: string] // Trimmed mobile refresh-token payload.
  'validate-session-token': [sessionToken: string] // Session-token validation request payload.
  'import-access-token': [accessToken: string] // Access-token import request payload.
  'import-codex-session': [content: string] // Trimmed session content payload.
  'import-codex-pat': [accessToken: string] // Trimmed personal access-token payload.
  'import-sso': [content: string] // Trimmed SSO input payload.
  'update:inputMethod': [method: AuthInputMethod] // Replacement input method draft value.
}>()

// t resolves interface labels from the active component locale while reactive consumers
// update their displayed text.
const { t } = useI18n()

// isOpenAI selects OpenAI authorization controls and hints from the platform provided by
// the owning dialog.
const isOpenAI = computed(() => props.platform === 'openai')
// isOpenAIBatchAuth reports supplied session entries so raw callbacks can retain state
// for parent-side batch association.
const isOpenAIBatchAuth = computed(() => props.authSessions.length > 0)
// openAIAuthSessions prefers supplied batch entries; the single URL and session ID
// synthesize one display entry when both exist.
const openAIAuthSessions = computed(() => {
  if (props.authSessions.length > 0) {
    return props.authSessions
  }
  if (!props.authUrl || !props.sessionId) {
    return []
  }
  return [
    {
      authUrl: props.authUrl,
      sessionId: props.sessionId,
      state: '',
    },
  ]
})
// showLocalCallbackNotice enables callback guidance for OpenAI and Grok, whose flow
// accepts pasted local callback addresses.
const showLocalCallbackNotice = computed(
  () => props.platform === 'openai' || props.platform === 'grok',
)

// getOAuthKey selects the platform translation namespace for a shared authorization
// label. Anthropic and unrecognized platforms use the base OAuth namespace, while
// supported alternatives supply their platform-specific prefix.
const getOAuthKey = (key: string) => {
  if (props.platform === 'openai') return `admin.providers.oauth.openai.${key}`
  if (props.platform === 'gemini') return `admin.providers.oauth.gemini.${key}`
  if (props.platform === 'antigravity') return `admin.providers.oauth.antigravity.${key}`
  if (props.platform === 'qoder') return `admin.providers.oauth.qoder.${key}`
  if (props.platform === 'grok') return `admin.providers.oauth.grok.${key}`
  return `admin.providers.oauth.${key}`
}

// The OAuth title, instructions, step labels, and code hints share the platform
// namespace returned by getOAuthKey. Computed translations keep those labels
// synchronized when the caller changes the platform or locale.
const oauthTitle = computed(() => t(getOAuthKey('title')))
const oauthFollowSteps = computed(() => t(getOAuthKey('followSteps')))
const oauthStep1GenerateUrl = computed(() => t(getOAuthKey('step1GenerateUrl')))
const oauthGenerateAuthUrl = computed(() => t(getOAuthKey('generateAuthUrl')))
const oauthStep2OpenUrl = computed(() => t(getOAuthKey('step2OpenUrl')))
const oauthOpenUrlDesc = computed(() => t(getOAuthKey('openUrlDesc')))
const oauthStep3EnterCode = computed(() => t(getOAuthKey('step3EnterCode')))
const oauthAuthCodeDesc = computed(() => t(getOAuthKey('authCodeDesc')))
const oauthAuthCode = computed(() => t(getOAuthKey('authCode')))
const oauthAuthCodePlaceholder = computed(() => t(getOAuthKey('authCodePlaceholder')))
const oauthAuthCodeHint = computed(() => t(getOAuthKey('authCodeHint')))
// oauthImportantNotice selects callback warnings for OpenAI, Antigravity, Qoder, and
// Grok. Other platforms receive an empty notice so the template can omit that block.
const oauthImportantNotice = computed(() => {
  if (props.platform === 'openai') return t('admin.providers.oauth.openai.importantNotice')
  if (props.platform === 'antigravity')
    return t('admin.providers.oauth.antigravity.importantNotice')
  if (props.platform === 'qoder') return t('admin.providers.oauth.qoder.importantNotice')
  if (props.platform === 'grok') return t('admin.providers.oauth.grok.importantNotice')
  return ''
})

// inputMethod starts from initialInputMethod and reports later changes to the parent that
// performs the chosen authorization action.
const inputMethod = ref<AuthInputMethod>(props.initialInputMethod)
// isAgentIdentityInput selects Agent Identity labels for the shared session import
// controls when that method is active.
const isAgentIdentityInput = computed(() => inputMethod.value === 'agent_identity')
// authCodeInput holds a manual code or raw batch callbacks; the single-session watcher
// extracts code while retaining state separately.
const authCodeInput = ref('')
// sessionKeyInput retains cookie authorization text, including internal newlines, until
// the parent resets the exposed input flow.
const sessionKeyInput = ref('')
// refreshTokenInput holds regular or mobile refresh-token drafts and retains internal
// line breaks for parent-side batch parsing.
const refreshTokenInput = ref('')
// sessionTokenInput holds the session-token draft read by the owning dialog through the
// exposed component reference.
const sessionTokenInput = ref('')
// codexSessionInput retains JSON session records or multiline import text for the parent
// authorization parser to interpret.
const codexSessionInput = ref('')
// codexPATInput stores the personal access token draft until the owning dialog resets the
// exposed input flow.
const codexPATInput = ref('')
// ssoCookieInput retains multiline SSO entries and is trimmed at the outer edges when
// import is requested.
const ssoCookieInput = ref('')
// showHelpDialog controls the local authorization help dialog and closes when the parent
// invokes the exposed reset operation.
const showHelpDialog = ref(false)
// oauthState retains callback state separately from a normalized code so the parent can
// supply it during exchange.
const oauthState = ref('')
// projectId retains the optional project draft that platform-specific parent controllers
// read before generating an authorization URL.
const projectId = ref('')

// methodOptionCount counts enabled method flags so the template can choose between
// hidden, segmented, and dropdown method controls.
const methodOptionCount = computed(
  () =>
    [
      props.showManualOption,
      props.showCookieOption,
      props.showRefreshTokenOption,
      props.showMobileRefreshTokenOption,
      props.showSessionTokenOption,
      props.showAccessTokenOption,
      props.showCodexSessionImportOption,
      props.showAgentIdentityOption,
      props.showCodexPatOption,
      props.showSsoOption,
    ].filter(Boolean).length,
)
// showMethodSelection hides method choice when at most one method flag is enabled for the
// owning dialog.
const showMethodSelection = computed(() => methodOptionCount.value > 1)

// methodOptions filters caller-enabled methods in display order and assigns stable test
// identifiers to each resulting selector option.
const methodOptions = computed(() => {
  const options: Array<{
    value: AuthInputMethod // Selector option wire value.
    label: string // Displayed input label.
    show: boolean // Caller-enabled method visibility.
  }> = [
    { value: 'manual', label: t('admin.providers.oauth.manualAuth'), show: props.showManualOption },
    {
      value: 'cookie',
      label: t('admin.providers.oauth.cookieAutoAuth'),
      show: props.showCookieOption,
    },
    {
      value: 'refresh_token',
      label: t(getOAuthKey('refreshTokenAuth')),
      show: props.showRefreshTokenOption,
    },
    { value: 'sso_cookie', label: t(getOAuthKey('ssoCookieAuth')), show: props.showSsoOption },
    {
      value: 'mobile_refresh_token',
      label: t('admin.providers.oauth.openai.mobileRefreshTokenAuth', '手动输入 Mobile RT'),
      show: props.showMobileRefreshTokenOption,
    },
    {
      value: 'session_token',
      label: t(getOAuthKey('sessionTokenAuth')),
      show: props.showSessionTokenOption,
    },
    {
      value: 'access_token',
      label: t('admin.providers.oauth.openai.accessTokenAuth', '手动输入 AT'),
      show: props.showAccessTokenOption,
    },
    {
      value: 'codex_session',
      label: t('admin.providers.oauth.openai.codexSessionAuth'),
      show: props.showCodexSessionImportOption,
    },
    {
      value: 'agent_identity',
      label: t('admin.providers.oauth.openai.agentIdentityAuth'),
      show: props.showAgentIdentityOption,
    },
    {
      value: 'codex_pat',
      label: t('admin.providers.oauth.openai.codexPatAuth'),
      show: props.showCodexPatOption,
    },
  ]
  return options
    .filter((option) => option.show)
    .map((option) => ({
      value: option.value,
      label: option.label,
      testid: `oauth-method-${option.value}`,
    }))
})

// uid supplies one stable instance ID so labels target their own input when several
// editors are mounted.
const uid = useId()

// hasGeneratedUrl marks the generation step complete when a single authorization URL or
// batch session entry is available.
const hasGeneratedUrl = computed(() => !!props.authUrl || props.authSessions.length > 0)
// hasAuthCode marks the input step complete when the current code draft contains any
// non-whitespace text.
const hasAuthCode = computed(() => authCodeInput.value.trim() !== '')

// copied and copyToClipboard expose shared clipboard feedback for authorization URLs
// displayed by this flow.
const { copied, copyToClipboard } = useClipboard()

// parsedKeyCount counts nonblank cookie input lines for batch guidance while leaving
// parsing and validation to the parent.
const parsedKeyCount = computed(() => {
  return sessionKeyInput.value
    .split('\n')
    .map((k) => k.trim())
    .filter((k) => k).length
})

// parsedRefreshTokenCount counts nonblank refresh-token lines for display while
// preserving their complete text in the local draft.
const parsedRefreshTokenCount = computed(() => {
  return refreshTokenInput.value
    .split('\n')
    .map((rt) => rt.trim())
    .filter((rt) => rt).length
})

// parsedAuthCodeLineCount counts nonblank callback lines for batch guidance without
// extracting or matching the individual authorization sessions.
const parsedAuthCodeLineCount = computed(() => {
  return authCodeInput.value
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line).length
})

// parsedCodexSessionCount treats JSON-looking content as one displayed entry and
// otherwise counts nonblank lines for import guidance.
const parsedCodexSessionCount = computed(() => {
  const trimmed = codexSessionInput.value.trim()
  if (!trimmed) return 0
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) return 1
  return trimmed
    .split('\n')
    .map((item) => item.trim())
    .filter((item) => item).length
})

// extractCallbackParam extracts a trimmed code or state from a callback URL or pasted
// fragment. A valid URL prefers its search parameter and then checks the fragment;
// malformed URLs use a parameter-pattern match.
//
// The malformed-URL path decodes plus signs as spaces before percent decoding. If decoding
// fails, it returns the captured text so the pasted credential remains available to the
// caller. Missing parameters return an empty string.
const extractCallbackParam = (value: string, param: 'code' | 'state') => {
  try {
    const url = new URL(value)
    const fromSearch = url.searchParams.get(param)
    // Search parameters take precedence over fragment parameters for valid URLs, matching
    // the code and state chosen from a complete callback address.
    if (fromSearch) return fromSearch.trim()
    if (url.hash) {
      return new URLSearchParams(url.hash.replace(/^#/, '')).get(param)?.trim() || ''
    }
  } catch {
    // Malformed URL input may still contain pasted callback parameters, so the pattern path
    // extracts those values before attempting percent decoding.
    const match = value.match(new RegExp(`(?:^|[?#&])${param}=([^&#\\s]+)`))
    if (!match?.[1]) return ''
    try {
      return decodeURIComponent(match[1].replace(/\+/g, ' ')).trim()
    } catch {
      // Invalid percent escapes keep their captured text so the parent receives the pasted
      // value and can report any exchange failure.
      return match[1].trim()
    }
  }
  return ''
}

// shouldNormalizeSingleCallbackInput keeps raw OpenAI batch callbacks available while
// allowing single-session code and state extraction in the input watcher.
const shouldNormalizeSingleCallbackInput = computed(() => {
  return props.platform !== 'openai' || !isOpenAIBatchAuth.value
})

// parsedSSOCount counts nonblank SSO lines for display while the parent remains
// responsible for parsing and validating entries.
const parsedSSOCount = computed(() => {
  return ssoCookieInput.value
    .split('\n')
    .map((item) => item.trim())
    .filter((item) => item).length
})

watch(
  () => props.initialInputMethod,
  (newVal) => {
    inputMethod.value = newVal
  },
)

watch(inputMethod, (newVal) => {
  emit('update:inputMethod', newVal)
})

// Single callbacks store state before replacing the draft with the extracted code; OpenAI
// batch callbacks retain their original text for session matching.
watch(authCodeInput, (newVal) => {
  if (!shouldNormalizeSingleCallbackInput.value) return

  const trimmed = newVal.trim()
  if (!trimmed || !/(?:^|[?#&])(?:code|state)=/.test(trimmed)) return

  const stateParam = extractCallbackParam(trimmed, 'state')
  if (stateParam) {
    oauthState.value = stateParam
  }
  const code = extractCallbackParam(trimmed, 'code')
  if (code && code !== trimmed) {
    authCodeInput.value = code
  }
})

// handleGenerateUrl asks the owning dialog to generate an authorization URL.
const handleGenerateUrl = () => {
  emit('generate-url')
}

// handleCopyUrl copies the current authorization URL when a nonempty URL is available.
// Clipboard success feedback comes from the shared helper, and this handler leaves
// authorization state and input drafts unchanged.
const handleCopyUrl = () => {
  if (props.authUrl) {
    copyToClipboard(props.authUrl, 'URL copied to clipboard')
  }
}

// handleCopySessionUrl copies the supplied session URL with the shared clipboard helper.
// The caller provides the URL belonging to the displayed session so batch entries retain
// their own authorization destinations.
const handleCopySessionUrl = (urlValue: string) => {
  copyToClipboard(urlValue, 'URL copied to clipboard')
}

// handleRemoveAuthSession asks the parent to remove the authorization session identified
// by its ID. Session storage and pending-request handling belong to the parent rather
// than this list action.
const handleRemoveAuthSession = (targetSessionId: string) => {
  emit('remove-auth-session', targetSessionId)
}

// handleRegenerate clears the entered code and extracted state before requesting a new
// URL. Other credential drafts remain available, and the parent replaces its
// authorization session when generation completes.
const handleRegenerate = () => {
  authCodeInput.value = ''
  oauthState.value = ''
  emit('generate-url')
}

// handleCookieAuth emits the cookie draft when it contains non-whitespace characters. The
// original text is forwarded so the parent can split multiple session keys and apply its
// own trimming rules.
const handleCookieAuth = () => {
  if (sessionKeyInput.value.trim()) {
    emit('cookie-auth', sessionKeyInput.value)
  }
}

// handleValidateRefreshToken emits trimmed refresh-token input through the selected
// validation event. Mobile refresh mode uses its dedicated event, while other modes use
// the regular refresh-token event and preserve internal newlines.
const handleValidateRefreshToken = () => {
  if (refreshTokenInput.value.trim()) {
    if (inputMethod.value === 'mobile_refresh_token') {
      emit('validate-mobile-refresh-token', refreshTokenInput.value.trim())
    } else {
      emit('validate-refresh-token', refreshTokenInput.value.trim())
    }
  }
}

// handleImportCodexSession emits trimmed Codex session content when the draft contains
// text. JSON objects and multiline imports remain intact for the parent parser to
// interpret and associate with accounts.
const handleImportCodexSession = () => {
  if (codexSessionInput.value.trim()) {
    emit('import-codex-session', codexSessionInput.value.trim())
  }
}

// handleImportCodexPAT emits a trimmed personal access token when its draft contains
// text. Validation and persistence belong to the parent handling import-codex-pat, so the
// draft remains visible until reset.
const handleImportCodexPAT = () => {
  if (codexPATInput.value.trim()) {
    emit('import-codex-pat', codexPATInput.value.trim())
  }
}

// handleImportSSO emits trimmed SSO cookie content when the draft contains text. Internal
// line breaks remain available to the parent for batch parsing, and the component retains
// its local draft.
const handleImportSSO = () => {
  if (ssoCookieInput.value.trim()) {
    emit('import-sso', ssoCookieInput.value.trim())
  }
}

// The parent reads unwrapped draft refs and calls reset when its dialog lifecycle ends, so
// credential clearing is coordinated by the owner.
defineExpose({
  authCode: authCodeInput,
  oauthState,
  projectId,
  sessionKey: sessionKeyInput,
  refreshToken: refreshTokenInput,
  sessionToken: sessionTokenInput,
  codexSession: codexSessionInput,
  codexPAT: codexPATInput,
  ssoCookie: ssoCookieInput,
  inputMethod,
  // reset clears every exposed credential draft and callback state, restores the initial
  // input method, and closes help. The owning dialog invokes it when its authorization
  // lifecycle should discard the entered secrets.
  reset: () => {
    authCodeInput.value = ''
    oauthState.value = ''
    projectId.value = ''
    sessionKeyInput.value = ''
    refreshTokenInput.value = ''
    sessionTokenInput.value = ''
    codexSessionInput.value = ''
    codexPATInput.value = ''
    ssoCookieInput.value = ''
    inputMethod.value = props.initialInputMethod
    showHelpDialog.value = false
  },
})
</script>
