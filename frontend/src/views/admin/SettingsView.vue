<template>
  <AppLayout>
    <div class="w-full min-w-0 space-y-4">
      <!-- Mount editable panels after bulk loading completes; every tab then keeps its controls mounted while hidden. -->
      <SettingsSkeleton v-if="loading" />

      <form v-else @submit.prevent="saveAllSettings" class="space-y-4" novalidate>
        <div class="settings-tabs-sticky">
          <div
            :class="[
              'settings-tabs-shell',
              activeTab === 'gateway' && 'settings-tabs-shell-stacked',
            ]"
          >
            <nav
              :ref="navigation.settingsTabsScrollRef"
              class="settings-tabs-scroll"
              role="tablist"
              :aria-label="t('admin.settings.title')"
            >
              <div class="settings-tabs">
                <button
                  v-for="tab in settingsTabs"
                  :key="tab.key"
                  :id="`settings-tab-${tab.key}`"
                  type="button"
                  role="tab"
                  :aria-selected="activeTab === tab.key"
                  :tabindex="activeTab === tab.key ? 0 : -1"
                  :class="['settings-tab', activeTab === tab.key && 'settings-tab-active']"
                  @click="selectSettingsTab(tab.key)"
                  @keydown="handleSettingsTabKeydown($event, tab.key)"
                >
                  <span class="settings-tab-icon">
                    <Icon :name="tab.icon" size="sm" />
                  </span>
                  <span class="settings-tab-label">
                    {{ t(`admin.settings.tabs.${tab.key}`) }}
                  </span>
                </button>
              </div>
            </nav>

            <nav
              v-if="activeTab === 'gateway'"
              :ref="navigation.gatewaySectionsScrollRef"
              class="gateway-section-tabs-scroll"
              role="tablist"
              :aria-label="t('admin.settings.gatewaySections.label')"
            >
              <div class="gateway-section-tabs">
                <button
                  v-for="section in gatewaySections"
                  :id="`gateway-section-tab-${section.key}`"
                  :key="section.key"
                  type="button"
                  role="tab"
                  :aria-selected="activeGatewaySection === section.key"
                  :tabindex="activeGatewaySection === section.key ? 0 : -1"
                  :data-testid="`gateway-section-tab-${section.key}`"
                  :class="[
                    'gateway-section-tab',
                    activeGatewaySection === section.key && 'gateway-section-tab-active',
                  ]"
                  @click="selectGatewaySection(section.key)"
                  @keydown="handleGatewaySectionKeydown($event, section.key)"
                >
                  <span class="gateway-section-tab-icon">
                    <ProviderIcon
                      v-if="section.providerBrand"
                      :brand="section.providerBrand"
                      size="18px"
                      color="currentColor"
                    />
                    <Icon v-else name="cog" size="sm" />
                  </span>
                  <span class="gateway-section-tab-label">
                    {{ t(`admin.settings.gatewaySections.${section.key}`) }}
                  </span>
                </button>
              </div>
            </nav>
          </div>
        </div>

        <div
          v-show="activeTab === 'security'"
          v-content-reveal="activeTab === 'security'"
          class="space-y-4"
        >
          <AdminApiKeySettings :controller="adminApiKey" />
        </div>

        <div
          v-show="activeTab === 'gateway'"
          v-content-reveal="activeTab === 'gateway'"
          :ref="navigation.gatewayContentStartRef"
          class="gateway-settings-content space-y-4"
        >
          <GatewayPolicySettings
            :controller="gateway"
            :active-gateway-section="activeGatewaySection"
          />
        </div>

        <div
          v-show="activeTab === 'security'"
          v-content-reveal="activeTab === 'security'"
          class="space-y-4"
        >
          <SecuritySettings :form="form" :controller="security" />

          <IdentitySettings :form="form" :controller="identity" />
        </div>

        <div
          v-show="activeTab === 'users'"
          v-content-reveal="activeTab === 'users'"
          class="space-y-4"
        >
          <UserDefaultsSettings :form="form" :controller="users" />
        </div>

        <div
          v-show="activeTab === 'gateway'"
          v-content-reveal="activeTab === 'gateway'"
          class="space-y-4"
        >
          <GatewayForwardingSettings
            :form="form"
            :controller="gateway"
            :web-search="webSearch"
            :active-gateway-section="activeGatewaySection"
          />
        </div>

        <div
          v-show="activeTab === 'general'"
          v-content-reveal="activeTab === 'general'"
          class="space-y-4"
        >
          <GeneralSettings :form="form" :controller="general" :images-disabled="discarding" />
        </div>

        <div
          v-show="activeTab === 'agreement'"
          v-content-reveal="activeTab === 'agreement'"
          class="space-y-4"
        >
          <AgreementSettings :form="form" :controller="agreement" />
        </div>

        <div
          v-show="activeTab === 'features'"
          v-content-reveal="activeTab === 'features'"
          class="space-y-4"
        >
          <FeatureSettings
            :form="form"
            :controller="features"
            :preview-balance-unit-name="general.previewBalanceUnitName.value"
          />
        </div>

        <div
          v-show="activeTab === 'payment'"
          v-content-reveal="activeTab === 'payment'"
          class="space-y-4"
        >
          <PaymentSettings
            :form="form"
            :controller="payment"
            :preview-balance-unit-name="general.previewBalanceUnitName.value"
          />
        </div>

        <div
          v-show="activeTab === 'email'"
          v-content-reveal="activeTab === 'email'"
          class="space-y-4"
        >
          <EmailSettings
            :form="form"
            :controller="email"
            :load-failed="loadFailed"
            :preview-balance-unit-symbol="general.previewBalanceUnitSymbol.value"
          />
        </div>

        <div v-show="activeTab === 'backup'" v-content-reveal="activeTab === 'backup'">
          <BackupSettings />
        </div>

        <SettingsSaveBar
          :visible="settingsSaveRegistry.dirty.value && !loadFailed"
          :message="t('admin.settings.unsavedChanges')"
        >
          <button
            type="button"
            :disabled="saving || discarding"
            class="btn bg-transparent text-white/70 hover:bg-white/10 hover:text-white focus:ring-offset-gray-900 dark:text-dark-200 dark:hover:bg-dark-800 dark:focus:ring-offset-dark-900"
            data-testid="settings-save-bar-discard"
            @click="discardAllSettings"
          >
            {{ t('admin.settings.discardChanges') }}
          </button>
          <button
            type="submit"
            :disabled="saving || discarding"
            class="btn btn-primary focus:ring-offset-gray-900 dark:focus:ring-offset-dark-900"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="saving"
              class="h-4 w-4 animate-spin"
            />
            {{ saving ? t('admin.settings.saving') : t('admin.settings.saveSettings') }}
          </button>
        </SettingsSaveBar>
      </form>

      <!-- Provider dialogs own nested forms, so they remain siblings of the settings form and cannot submit its drafts. -->
      <PaymentProviderDialog
        :ref="payment.providerDialogRef"
        :show="showProviderDialog"
        :saving="providerSaving"
        :testing="providerTesting"
        :editing="editingProvider"
        :all-key-options="providerKeyOptions"
        :enabled-key-options="enabledProviderKeyOptions"
        :all-payment-types="allPaymentTypes"
        :redirect-label="t('admin.settings.payment.easypayRedirect')"
        @close="showProviderDialog = false"
        @save="handleSaveProvider"
        @test="handleTestProviderDraft"
      />
      <ConfirmDialog
        :show="showDeleteProviderDialog"
        :title="t('admin.settings.payment.deleteProvider')"
        :message="t('admin.settings.payment.deleteProviderConfirm')"
        :confirm-text="t('common.delete')"
        danger
        @confirm="handleDeleteProvider"
        @cancel="showDeleteProviderDialog = false"
      />
      <TotpStepUpDialog :controller="settingsStepUp" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import { ref, computed, onMounted, nextTick } from 'vue'
import SettingsSkeleton from './settings/SettingsSkeleton.vue'
import SettingsSaveBar from '@/components/common/settings/SettingsSaveBar.vue'
import { useDirtyTracker } from '@/composables/useDirtyTracker'
import {
  provideSettingsSaveRegistry,
  type SettingsSaveTarget,
} from '@/composables/useSettingsSaveRegistry'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import PaymentProviderDialog from '@/components/admin/payment/PaymentProviderDialog.vue'
import BackupSettings from './settings/BackupSettings.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useAppStore } from '@/stores'
import { createSettingsForm } from './settings/settingsState'
import { useNavigationSettings } from './settings/useNavigationSettings'
import { useGatewaySettings } from './settings/useGatewaySettings'
import { useEmailSettings } from './settings/useEmailSettings'
import { useSecuritySettings } from './settings/useSecuritySettings'
import { useGeneralSettings } from './settings/useGeneralSettings'
import { usePaymentSettings } from './settings/usePaymentSettings'
import { useAdminApiKeySettings } from './settings/useAdminApiKeySettings'
import { useUsersSettings } from './settings/useUsersSettings'
import { useAgreementSettings } from './settings/useAgreementSettings'
import { useFeaturesSettings } from './settings/useFeaturesSettings'
import { useIdentitySettings } from './settings/useIdentitySettings'
import { useWebSearchSettings } from './settings/useWebSearchSettings'
import AdminApiKeySettings from './settings/AdminApiKeySettings.vue'
import GatewayPolicySettings from './settings/GatewayPolicySettings.vue'
import SecuritySettings from './settings/SecuritySettings.vue'
import IdentitySettings from './settings/IdentitySettings.vue'
import UserDefaultsSettings from './settings/UserDefaultsSettings.vue'
import GatewayForwardingSettings from './settings/GatewayForwardingSettings.vue'
import GeneralSettings from './settings/GeneralSettings.vue'
import AgreementSettings from './settings/AgreementSettings.vue'
import FeatureSettings from './settings/FeatureSettings.vue'
import PaymentSettings from './settings/PaymentSettings.vue'
import EmailSettings from './settings/EmailSettings.vue'
import { createSettingsPayload } from './settings/settingsPayload'
import { createSettingsHydration } from './settings/settingsHydration'
import { useSettingsPersistence } from './settings/settingsPersistence'

/**
 * SettingsView.vue composes the administrator settings tabs and registers their
 * page-level save targets in a fixed order. One reactive bulk draft supplies all
 * domain controllers, while dedicated endpoint drafts retain independent dirty
 * snapshots. Controllers start during root setup, including feature polling for
 * an initial Features tab; editable panels mount after the bulk loader finishes.
 *
 * All tab containers remain mounted while hidden, preserving editor drafts and
 * child save registrations. The bulk target includes search and registers before
 * the eight dedicated targets and mounted child targets. Payment dialogs stay
 * outside the root form because their submissions belong to immediate provider
 * operations. The page delegates bulk persistence ordering to settingsPersistence.ts.
 */
/**
 * Page services supply translated feedback and the one reactive draft shared by all settings domains.
 */
const { t } = useI18n()

const appStore = useAppStore()

const form = createSettingsForm()

/**
 * Navigation exposes the selected tabs and DOM references used to focus and scroll their buttons.
 */
const navigation = useNavigationSettings()

const {
  settingsTabs,
  activeTab,
  gatewaySections,
  activeGatewaySection,
  selectSettingsTab,
  handleSettingsTabKeydown,
  selectGatewaySection,
  handleGatewaySectionKeydown,
  scrollActiveSettingsTabIntoView,
  scrollActiveGatewaySectionIntoView,
} = navigation

/**
 * Domain controllers share selected fields of form and retain their own transient editor and request state.
 */
const general = useGeneralSettings(form)
const { tablePageSizeOptionsInput, loadHomeFeaturedModelOptions } = general

const agreement = useAgreementSettings(form)

const security = useSecuritySettings(form, (...keys) => markSettingsClean(...keys))
const {
  registrationEmailSuffixWhitelistTags,
  panelRateLimitForm,
  loadPanelRateLimitSettings,
  savePanelRateLimitSettings,
} = security

const identity = useIdentitySettings(form)

const users = useUsersSettings(form)
const { authSourceDefaults, loadSubscriptionPlans } = users

const features = useFeaturesSettings(form, activeTab)
const { loadCreativeModelCandidates } = features

const gateway = useGatewaySettings(form, (...keys) => markSettingsClean(...keys))
const {
  ollamaCloudUsageForm,
  overloadCooldownForm,
  openAI403CooldownForm,
  rateLimit429CooldownForm,
  streamTimeoutForm,
  rectifierForm,
  betaPolicyForm,
  openaiFastPolicyForm,
  openaiFastPolicyLoaded,
  loadOllamaCloudUsageSettings,
  saveOllamaCloudUsageSettings,
  loadOverloadCooldownSettings,
  saveOverloadCooldownSettings,
  loadOpenAI403CooldownSettings,
  saveOpenAI403CooldownSettings,
  loadRateLimit429CooldownSettings,
  saveRateLimit429CooldownSettings,
  loadStreamTimeoutSettings,
  saveStreamTimeoutSettings,
  loadRectifierSettings,
  saveRectifierSettings,
  loadBetaPolicySettings,
  saveBetaPolicySettings,
} = gateway

const email = useEmailSettings(form)

const webSearch = useWebSearchSettings()

const { webSearchConfig, loadWebSearchConfig, saveWebSearchConfig } = webSearch

const adminApiKey = useAdminApiKeySettings()

const { loadAdminApiKey } = adminApiKey

/**
 * Bulk conversion and persistence use concrete domain actions, keeping validation separate from ordered endpoint writes.
 */
const buildPayload = createSettingsPayload(form, {
  general,
  agreement,
  security,
  identity,
  users,
  features,
  gateway,
})
const hydration = createSettingsHydration(form, { general, security, users, gateway, email })
const { loading, loadFailed, settingsStepUp, loadSettings, saveSettings } = useSettingsPersistence({
  buildPayload,
  ...hydration,
  loadWebSearchConfig,
  saveWebSearchConfig,
  loadCreativeModelCandidates,
  markSettingsClean: (...keys) => markSettingsClean(...keys),
  afterLoad: () => {
    scrollActiveSettingsTabIntoView()
    scrollActiveGatewaySectionIntoView()
  },
})
/**
 * Payment provider operations can request the completed bulk save action after their immediate provider write succeeds.
 */
const payment = usePaymentSettings(form, saveSettings)

const {
  allPaymentTypes,
  providerSaving,
  providerTesting,
  showProviderDialog,
  showDeleteProviderDialog,
  editingProvider,
  providerKeyOptions,
  enabledProviderKeyOptions,
  loadProviders,
  handleSaveProvider,
  handleTestProviderDraft,
  handleDeleteProvider,
} = payment

/**
 * Save and discard activity disable the floating controls until their corresponding registry operation finishes.
 */
const saving = ref(false)

const discarding = ref(false)

// Bulk and search drafts have separate snapshots so a successful bulk write can
// become clean when search fails. Usage resets write immediately, so quota_used
// is excluded from the search configuration snapshot.
const { isDirty: isSettingsDirty, markClean: markSettingsClean } = useDirtyTracker({
  settings: () => ({
    form,
    authSourceDefaults,
    registrationEmailSuffixWhitelist: registrationEmailSuffixWhitelistTags.value,
    tablePageSizeOptions: tablePageSizeOptionsInput.value,
    openaiFastPolicyRules: openaiFastPolicyLoaded.value ? openaiFastPolicyForm.rules : null,
  }),
  webSearch: () => ({
    enabled: webSearchConfig.enabled,
    providers: webSearchConfig.providers.map(({ quota_used: _quotaUsed, ...provider }) => provider),
  }),
  overloadCooldown: () => overloadCooldownForm,
  openAI403Cooldown: () => openAI403CooldownForm,
  rateLimit429Cooldown: () => rateLimit429CooldownForm,
  streamTimeout: () => streamTimeoutForm,
  rectifier: () => rectifierForm,
  betaPolicy: () => betaPolicyForm,
  ollamaCloudUsage: () => ollamaCloudUsageForm,
  panelRateLimit: () => panelRateLimitForm,
})

// The registry is provided during root setup, before any child settings panel
// mounts and registers its own targets. Insertion order determines save order.
const settingsSaveRegistry = provideSettingsSaveRegistry()

/**
 * Root targets register bulk with search first, followed by dedicated endpoint drafts in their established save order.
 */
const settingsSaveTargets: Array<[string, SettingsSaveTarget]> = [
  [
    'settings',
    {
      dirty: computed(() => isSettingsDirty('settings') || isSettingsDirty('webSearch')),
      save: saveSettings,
      discard: () => loadSettings({ silent: true }),
    },
  ],
  [
    'overloadCooldown',
    {
      dirty: computed(() => isSettingsDirty('overloadCooldown')),
      save: saveOverloadCooldownSettings,
      discard: loadOverloadCooldownSettings,
    },
  ],
  [
    'openAI403Cooldown',
    {
      dirty: computed(() => isSettingsDirty('openAI403Cooldown')),
      save: saveOpenAI403CooldownSettings,
      discard: loadOpenAI403CooldownSettings,
    },
  ],
  [
    'rateLimit429Cooldown',
    {
      dirty: computed(() => isSettingsDirty('rateLimit429Cooldown')),
      save: saveRateLimit429CooldownSettings,
      discard: loadRateLimit429CooldownSettings,
    },
  ],
  [
    'streamTimeout',
    {
      dirty: computed(() => isSettingsDirty('streamTimeout')),
      save: saveStreamTimeoutSettings,
      discard: loadStreamTimeoutSettings,
    },
  ],
  [
    'rectifier',
    {
      dirty: computed(() => isSettingsDirty('rectifier')),
      save: saveRectifierSettings,
      discard: loadRectifierSettings,
    },
  ],
  [
    'betaPolicy',
    {
      dirty: computed(() => isSettingsDirty('betaPolicy')),
      save: saveBetaPolicySettings,
      discard: loadBetaPolicySettings,
    },
  ],
  [
    'ollamaCloudUsage',
    {
      dirty: computed(() => isSettingsDirty('ollamaCloudUsage')),
      save: saveOllamaCloudUsageSettings,
      discard: loadOllamaCloudUsageSettings,
    },
  ],
  [
    'panelRateLimit',
    {
      dirty: computed(() => isSettingsDirty('panelRateLimit')),
      save: savePanelRateLimitSettings,
      discard: loadPanelRateLimitSettings,
    },
  ],
]

/**
 * Register these targets before rendering children so their own registration lifecycle appends them after every root target.
 */
for (const [key, target] of settingsSaveTargets) {
  settingsSaveRegistry.registry.register(key, target)
}

/**
 * discardAllSettings reloads dirty targets through the shared save registry.
 * The activity flag disables general-panel image editors before reload starts,
 * invalidating their pending reads even when the saved image has not changed.
 * The flag remains set until the registry's aggregate promise settles.
 * Failed reloads also wait for the remaining targets before controls are enabled,
 * so no outstanding restoration can race with a newly selected image.
 */
async function discardAllSettings() {
  discarding.value = true
  try {
    // Propagate disabled state before selecting dirty targets or restoring values;
    // pending image callbacks must not overwrite the snapshot returned by this reload.
    await nextTick()
    await settingsSaveRegistry.discardDirty()
  } finally {
    discarding.value = false
  }
}

/**
 * saveAllSettings submits dirty registry targets and shows one success message
 * when every selected target succeeds. A clean form submission, including Enter
 * inside a control, still invokes the bulk and search save action.
 */
async function saveAllSettings() {
  saving.value = true
  try {
    const ok = settingsSaveRegistry.dirty.value
      ? await settingsSaveRegistry.saveDirty()
      : await saveSettings()
    if (ok) {
      appStore.showSuccess(t('admin.settings.settingsSaved'))
    }
  } finally {
    saving.value = false
  }
}

/**
 * Start settings, catalog, key, and provider reads when the page mounts. The
 * bulk/search path records clean snapshots after normalization; other loaders
 * independently populate catalogs, API-key status, provider configuration, or
 * their own editable targets.
 */
onMounted(() => {
  loadSettings()
  loadHomeFeaturedModelOptions()
  loadSubscriptionPlans()
  loadAdminApiKey()
  loadOllamaCloudUsageSettings()
  loadOverloadCooldownSettings()
  loadOpenAI403CooldownSettings()
  loadRateLimit429CooldownSettings()
  loadPanelRateLimitSettings()
  loadStreamTimeoutSettings()
  loadRectifierSettings()
  loadBetaPolicySettings()
  loadProviders()
})
</script>

<style scoped>
/* The sticky shell fills the gap above its tabs with page background. Its shadow
   extends beyond both rounded edges so scrolled cards stay covered underneath. */
.settings-tabs-sticky {
  @apply sticky z-20 -mx-2 px-2;
  top: calc(var(--header-h) + 1rem);
  background: var(--page-bg);
  box-shadow: 0 -1rem 0 var(--page-bg);
}

.settings-tabs-shell {
  @apply -mx-1 rounded-full border border-gray-200 bg-white p-1.5 dark:border-dark-600/70 dark:bg-dark-900;
  /* The inset edge and lower shadow separate the floating tab shell from scrolling cards. */
  box-shadow:
    0 1px 0 rgb(255 255 255 / 0.9) inset,
    0 10px 24px -16px rgb(15 23 42 / 0.28);
}

/* Gateway navigation stacks two rows inside the dialog-radius shell. */
.settings-tabs-shell-stacked {
  @apply rounded-dialog;
}

.settings-tabs-scroll {
  @apply overflow-x-auto rounded-full;
  -ms-overflow-style: none;
  scrollbar-width: none;
  scroll-padding-inline: 0.5rem;
}

.settings-tabs-scroll::-webkit-scrollbar {
  display: none;
}

.settings-tabs {
  @apply flex min-w-max items-center gap-1;
}

.settings-tab {
  @apply relative isolate flex h-9 min-w-[6.75rem] shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-full border border-transparent px-3 text-sm font-medium text-gray-600 outline-none transition-colors duration-fast ease-standard dark:text-gray-300;
}

@media (min-width: 768px) {
  .settings-tabs {
    @apply min-w-full;
  }

  .settings-tab {
    @apply min-w-0 flex-1 basis-0 overflow-hidden px-2 text-[13px];
  }

  .settings-tab-icon {
    @apply h-6 w-6;
  }
}

.settings-tab::before {
  @apply absolute inset-0 -z-10 rounded-full opacity-0 transition-opacity duration-normal;
  content: '';
  background: linear-gradient(135deg, rgb(248 250 252 / 0.95), rgb(241 245 249 / 0.8));
}

.settings-tab:hover::before,
.settings-tab:focus-visible::before {
  opacity: 1;
}

.settings-tab:focus-visible {
  @apply ring-2 ring-primary-500/40 ring-offset-2 ring-offset-white dark:ring-offset-dark-900;
}

.settings-tab-active {
  @apply border-primary-200/80 bg-white text-primary-700 shadow-sm dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500;
  box-shadow: 0 1px 0 rgb(255 255 255 / 0.92) inset;
}

.settings-tab-active::before {
  opacity: 0;
}

.settings-tab-icon {
  @apply flex h-7 w-7 shrink-0 items-center justify-center text-gray-500 transition-colors duration-normal dark:text-gray-400;
}

.settings-tab:hover .settings-tab-icon,
.settings-tab:focus-visible .settings-tab-icon {
  @apply text-gray-700 dark:text-gray-200;
}

.settings-tab-active .settings-tab-icon {
  @apply text-primary-600 dark:text-primary-500;
}

.settings-tab-label {
  @apply min-w-0 overflow-hidden text-ellipsis whitespace-nowrap leading-none;
}

/* Gateway section buttons share the sticky container with the top-level navigation row. */
.gateway-section-tabs-scroll {
  @apply mt-1.5 overflow-x-auto border-t border-gray-100 pt-1.5 dark:border-dark-700;
  -ms-overflow-style: none;
  scrollbar-width: none;
  scroll-padding-inline: 0.5rem;
}

.gateway-section-tabs-scroll::-webkit-scrollbar {
  display: none;
}

.gateway-section-tabs {
  @apply flex min-w-max items-center gap-1;
}

.gateway-section-tab {
  @apply flex h-9 min-w-[7.75rem] shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-full border border-transparent px-3 text-sm font-medium text-gray-600 outline-none transition-colors duration-normal dark:text-gray-300;
}

.gateway-section-tab:hover,
.gateway-section-tab:focus-visible {
  @apply bg-gray-50 text-gray-900 dark:bg-dark-700 dark:text-white;
}

.gateway-section-tab:focus-visible {
  @apply ring-2 ring-primary-500/40 ring-offset-1 ring-offset-white dark:ring-offset-dark-900;
}

.gateway-section-tab-active {
  @apply border-primary-200 bg-primary-50 text-primary-700 dark:border-primary-400/30 dark:bg-primary-400/10 dark:text-primary-200;
}

.gateway-section-tab-icon {
  @apply flex h-5 w-5 shrink-0 items-center justify-center;
}

.gateway-section-tab-label {
  @apply min-w-0 whitespace-nowrap leading-none;
}

.gateway-settings-content {
  /* The extra 9.25rem accounts for the sticky offset, both tab rows, and the
     clearance below the navigation when its first gateway card scrolls into view. */
  scroll-margin-top: calc(var(--header-h) + 9.25rem);
}

@media (min-width: 768px) {
  .gateway-section-tabs {
    @apply min-w-full;
  }

  .gateway-section-tab {
    @apply min-w-0 flex-1 basis-0;
  }
}
</style>

<style>
/* Global dark selectors reach the theme ancestor above this component in production builds. */
.dark .settings-tabs-shell {
  /* A brighter border separates the dark sticky shell from the cards beneath its shadow. */
  border-color: theme('borderColor.dark.500');
  background: theme('colors.dark.900');
  box-shadow:
    0 1px 0 rgb(255 255 255 / 0.06) inset,
    0 12px 28px -14px rgb(0 0 0 / 0.8);
}

.dark .settings-tab::before {
  background: linear-gradient(135deg, rgb(39 39 42 / 0.9), rgb(63 63 70 / 0.62));
}

.dark .settings-tab-active {
  box-shadow: 0 1px 0 rgb(255 255 255 / 0.08) inset;
}
</style>
