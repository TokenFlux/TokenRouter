<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <div
          class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"
        ></div>
      </div>

      <!-- Settings Form -->
      <form v-else @submit.prevent="saveSettings" class="space-y-6" novalidate>
        <!-- Tab Navigation -->
        <div class="settings-tabs-shell">
          <nav
            ref="settingsTabsScrollRef"
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
                :class="[
                  'settings-tab',
                  activeTab === tab.key && 'settings-tab-active',
                ]"
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
        </div>

        <!-- Tab: Security — Admin API Key -->
        <div v-show="activeTab === 'security'" class="space-y-6">
          <!-- Admin API Key Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.adminApiKey.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.adminApiKey.description") }}
              </p>
            </div>
            <div class="space-y-4 p-6">
              <!-- Security Warning -->
              <div
                class="rounded-lg border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-900/20"
              >
                <div class="flex items-start">
                  <Icon
                    name="exclamationTriangle"
                    size="md"
                    class="mt-0.5 flex-shrink-0 text-amber-500"
                  />
                  <p class="ml-3 text-sm text-amber-700 dark:text-amber-300">
                    {{ t("admin.settings.adminApiKey.securityWarning") }}
                  </p>
                </div>
              </div>

              <!-- Loading State -->
              <div
                v-if="adminApiKeyLoading"
                class="flex items-center gap-2 text-gray-500"
              >
                <div
                  class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"
                ></div>
                {{ t("common.loading") }}
              </div>

              <!-- No Key Configured -->
              <div
                v-else-if="!adminApiKeyExists"
                class="flex items-center justify-between"
              >
                <span class="text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.adminApiKey.notConfigured") }}
                </span>
                <button
                  type="button"
                  @click="createAdminApiKey"
                  :disabled="adminApiKeyOperating"
                  class="btn btn-primary btn-sm"
                >
                  <svg
                    v-if="adminApiKeyOperating"
                    class="mr-1 h-4 w-4 animate-spin"
                    fill="none"
                    viewBox="0 0 24 24"
                  >
                    <circle
                      class="opacity-25"
                      cx="12"
                      cy="12"
                      r="10"
                      stroke="currentColor"
                      stroke-width="4"
                    ></circle>
                    <path
                      class="opacity-75"
                      fill="currentColor"
                      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                    ></path>
                  </svg>
                  {{
                    adminApiKeyOperating
                      ? t("admin.settings.adminApiKey.creating")
                      : t("admin.settings.adminApiKey.create")
                  }}
                </button>
              </div>

              <!-- Key Exists -->
              <div v-else class="space-y-4">
                <div class="flex items-center justify-between">
                  <div>
                    <label
                      class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.adminApiKey.currentKey") }}
                    </label>
                    <code
                      class="rounded bg-gray-100 px-2 py-1 font-mono text-sm text-gray-900 dark:bg-dark-700 dark:text-gray-100"
                    >
                      {{ adminApiKeyMasked }}
                    </code>
                  </div>
                  <div class="flex gap-2">
                    <button
                      type="button"
                      @click="regenerateAdminApiKey"
                      :disabled="adminApiKeyOperating"
                      class="btn btn-secondary btn-sm"
                    >
                      {{
                        adminApiKeyOperating
                          ? t("admin.settings.adminApiKey.regenerating")
                          : t("admin.settings.adminApiKey.regenerate")
                      }}
                    </button>
                    <button
                      type="button"
                      @click="deleteAdminApiKey"
                      :disabled="adminApiKeyOperating"
                      class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
                    >
                      {{ t("admin.settings.adminApiKey.delete") }}
                    </button>
                  </div>
                </div>

                <!-- Newly Generated Key Display -->
                <div
                  v-if="newAdminApiKey"
                  class="space-y-3 rounded-lg border border-green-200 bg-green-50 p-4 dark:border-green-800 dark:bg-green-900/20"
                >
                  <p
                    class="text-sm font-medium text-green-700 dark:text-green-300"
                  >
                    {{ t("admin.settings.adminApiKey.keyWarning") }}
                  </p>
                  <div class="flex items-center gap-2">
                    <code
                      class="flex-1 select-all break-all rounded border border-green-300 bg-white px-3 py-2 font-mono text-sm dark:border-green-700 dark:bg-dark-800"
                    >
                      {{ newAdminApiKey }}
                    </code>
                    <button
                      type="button"
                      @click="copyNewKey"
                      class="btn btn-primary btn-sm flex-shrink-0"
                    >
                      {{ t("admin.settings.adminApiKey.copyKey") }}
                    </button>
                  </div>
                  <p class="text-xs text-green-600 dark:text-green-400">
                    {{ t("admin.settings.adminApiKey.usage") }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
        <!-- /Tab: Security — Admin API Key -->

        <!-- Tab: Gateway -->
        <div v-show="activeTab === 'gateway'" class="space-y-6">
          <!-- Overload Cooldown (529) Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.overloadCooldown.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.overloadCooldown.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div
                v-if="overloadCooldownLoading"
                class="flex items-center gap-2 text-gray-500"
              >
                <div
                  class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"
                ></div>
                {{ t("common.loading") }}
              </div>

              <template v-else>
                <div class="flex items-center justify-between">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">{{
                      t("admin.settings.overloadCooldown.enabled")
                    }}</label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.overloadCooldown.enabledHint") }}
                    </p>
                  </div>
                  <Toggle v-model="overloadCooldownForm.enabled" />
                </div>

                <div
                  v-if="overloadCooldownForm.enabled"
                  class="space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.overloadCooldown.cooldownMinutes") }}
                    </label>
                    <input
                      v-model.number="overloadCooldownForm.cooldown_minutes"
                      type="number"
                      min="1"
                      max="120"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        t("admin.settings.overloadCooldown.cooldownMinutesHint")
                      }}
                    </p>
                  </div>
                </div>

                <div
                  class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <button
                    type="button"
                    @click="saveOverloadCooldownSettings"
                    :disabled="overloadCooldownSaving"
                    class="btn btn-primary btn-sm"
                  >
                    <svg
                      v-if="overloadCooldownSaving"
                      class="mr-1 h-4 w-4 animate-spin"
                      fill="none"
                      viewBox="0 0 24 24"
                    >
                      <circle
                        class="opacity-25"
                        cx="12"
                        cy="12"
                        r="10"
                        stroke="currentColor"
                        stroke-width="4"
                      ></circle>
                      <path
                        class="opacity-75"
                        fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                      ></path>
                    </svg>
                    {{
                      overloadCooldownSaving
                        ? t("common.saving")
                        : t("common.save")
                    }}
                  </button>
                </div>
              </template>
            </div>
          </div>

          <!-- OpenAI OAuth 403 Cooldown Settings -->
        <div class="card">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('admin.settings.openAI403Cooldown.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.openAI403Cooldown.description') }}
            </p>
          </div>
          <div class="space-y-5 p-6">
            <div v-if="openAI403CooldownLoading" class="flex items-center gap-2 text-gray-500">
              <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
              {{ t('common.loading') }}
            </div>

            <template v-else>
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t('admin.settings.openAI403Cooldown.enabled')
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.openAI403Cooldown.enabledHint') }}
                  </p>
                </div>
                <Toggle v-model="openAI403CooldownForm.enabled" />
              </div>

              <div
                v-if="openAI403CooldownForm.enabled"
                class="space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.settings.openAI403Cooldown.cooldownMinutes') }}
                  </label>
                  <input
                    v-model.number="openAI403CooldownForm.cooldown_minutes"
                    type="number"
                    min="1"
                    max="120"
                    class="input w-32"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.openAI403Cooldown.cooldownMinutesHint') }}
                  </p>
                </div>

                <div class="flex items-center justify-between">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">
                      {{ t('admin.settings.openAI403Cooldown.errorOnThresholdEnabled') }}
                    </label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t('admin.settings.openAI403Cooldown.errorOnThresholdEnabledHint') }}
                    </p>
                  </div>
                  <Toggle v-model="openAI403CooldownForm.error_on_threshold_enabled" />
                </div>

                <div
                  v-if="openAI403CooldownForm.error_on_threshold_enabled"
                  class="grid gap-4 sm:grid-cols-2"
                >
                  <div>
                    <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                      {{ t('admin.settings.openAI403Cooldown.thresholdCount') }}
                    </label>
                    <input
                      v-model.number="openAI403CooldownForm.threshold_count"
                      type="number"
                      min="1"
                      max="20"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t('admin.settings.openAI403Cooldown.thresholdCountHint') }}
                    </p>
                  </div>

                  <div>
                    <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                      {{ t('admin.settings.openAI403Cooldown.thresholdWindowMinutes') }}
                    </label>
                    <input
                      v-model.number="openAI403CooldownForm.threshold_window_minutes"
                      type="number"
                      min="1"
                      max="1440"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t('admin.settings.openAI403Cooldown.thresholdWindowMinutesHint') }}
                    </p>
                  </div>
                </div>
              </div>

              <div class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700">
                <button
                  type="button"
                  @click="saveOpenAI403CooldownSettings"
                  :disabled="openAI403CooldownSaving"
                  class="btn btn-primary btn-sm"
                >
                  <svg
                    v-if="openAI403CooldownSaving"
                    class="mr-1 h-4 w-4 animate-spin"
                    fill="none"
                    viewBox="0 0 24 24"
                  >
                    <circle
                      class="opacity-25"
                      cx="12"
                      cy="12"
                      r="10"
                      stroke="currentColor"
                      stroke-width="4"
                    ></circle>
                    <path
                      class="opacity-75"
                      fill="currentColor"
                      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                    ></path>
                  </svg>
                  {{ openAI403CooldownSaving ? t('common.saving') : t('common.save') }}
                </button>
              </div>
            </template>
          </div>
        </div>

        <OpenAIOAuthImportDefaultsSettings />

        <!-- Rate Limit Cooldown (429) Settings -->
        <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.rateLimit429Cooldown.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.rateLimit429Cooldown.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div
                v-if="rateLimit429CooldownLoading"
                class="flex items-center gap-2 text-gray-500"
              >
                <div
                  class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"
                ></div>
                {{ t("common.loading") }}
              </div>

              <template v-else>
                <div class="flex items-center justify-between">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">{{
                      t("admin.settings.rateLimit429Cooldown.enabled")
                    }}</label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.rateLimit429Cooldown.enabledHint") }}
                    </p>
                  </div>
                  <Toggle v-model="rateLimit429CooldownForm.enabled" />
                </div>

                <div
                  v-if="rateLimit429CooldownForm.enabled"
                  class="space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{
                        t(
                          "admin.settings.rateLimit429Cooldown.cooldownSeconds",
                        )
                      }}
                    </label>
                    <input
                      v-model.number="rateLimit429CooldownForm.cooldown_seconds"
                      type="number"
                      min="1"
                      max="7200"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        t(
                          "admin.settings.rateLimit429Cooldown.cooldownSecondsHint",
                        )
                      }}
                    </p>
                  </div>
                </div>

                <div
                  class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <button
                    type="button"
                    @click="saveRateLimit429CooldownSettings"
                    :disabled="rateLimit429CooldownSaving"
                    class="btn btn-primary btn-sm"
                  >
                    <svg
                      v-if="rateLimit429CooldownSaving"
                      class="mr-1 h-4 w-4 animate-spin"
                      fill="none"
                      viewBox="0 0 24 24"
                    >
                      <circle
                        class="opacity-25"
                        cx="12"
                        cy="12"
                        r="10"
                        stroke="currentColor"
                        stroke-width="4"
                      ></circle>
                      <path
                        class="opacity-75"
                        fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                      ></path>
                    </svg>
                    {{
                      rateLimit429CooldownSaving
                        ? t("common.saving")
                        : t("common.save")
                    }}
                  </button>
                </div>
              </template>
            </div>
        </div>

        <!-- Stream Timeout Settings -->
        <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.streamTimeout.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.streamTimeout.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Loading State -->
              <div
                v-if="streamTimeoutLoading"
                class="flex items-center gap-2 text-gray-500"
              >
                <div
                  class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"
                ></div>
                {{ t("common.loading") }}
              </div>

              <template v-else>
                <!-- Enable Stream Timeout -->
                <div class="flex items-center justify-between">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">{{
                      t("admin.settings.streamTimeout.enabled")
                    }}</label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.streamTimeout.enabledHint") }}
                    </p>
                  </div>
                  <Toggle v-model="streamTimeoutForm.enabled" />
                </div>

                <!-- Settings - Only show when enabled -->
                <div
                  v-if="streamTimeoutForm.enabled"
                  class="space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <!-- Action -->
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.streamTimeout.action") }}
                    </label>
                    <Select
                      v-model="streamTimeoutForm.action"
                      :options="streamTimeoutActionOptions"
                      class="w-64"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.streamTimeout.actionHint") }}
                    </p>
                  </div>

                  <!-- Temp Unsched Minutes (only show when action is temp_unsched) -->
                  <div v-if="streamTimeoutForm.action === 'temp_unsched'">
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.streamTimeout.tempUnschedMinutes") }}
                    </label>
                    <input
                      v-model.number="streamTimeoutForm.temp_unsched_minutes"
                      type="number"
                      min="1"
                      max="60"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        t("admin.settings.streamTimeout.tempUnschedMinutesHint")
                      }}
                    </p>
                  </div>

                  <!-- Threshold Count -->
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.streamTimeout.thresholdCount") }}
                    </label>
                    <input
                      v-model.number="streamTimeoutForm.threshold_count"
                      type="number"
                      min="1"
                      max="10"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.streamTimeout.thresholdCountHint") }}
                    </p>
                  </div>

                  <!-- Threshold Window Minutes -->
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{
                        t("admin.settings.streamTimeout.thresholdWindowMinutes")
                      }}
                    </label>
                    <input
                      v-model.number="
                        streamTimeoutForm.threshold_window_minutes
                      "
                      type="number"
                      min="1"
                      max="60"
                      class="input w-32"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        t(
                          "admin.settings.streamTimeout.thresholdWindowMinutesHint",
                        )
                      }}
                    </p>
                  </div>
                </div>

                <!-- Save Button -->
                <div
                  class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <button
                    type="button"
                    @click="saveStreamTimeoutSettings"
                    :disabled="streamTimeoutSaving"
                    class="btn btn-primary btn-sm"
                  >
                    <svg
                      v-if="streamTimeoutSaving"
                      class="mr-1 h-4 w-4 animate-spin"
                      fill="none"
                      viewBox="0 0 24 24"
                    >
                      <circle
                        class="opacity-25"
                        cx="12"
                        cy="12"
                        r="10"
                        stroke="currentColor"
                        stroke-width="4"
                      ></circle>
                      <path
                        class="opacity-75"
                        fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                      ></path>
                    </svg>
                    {{
                      streamTimeoutSaving
                        ? t("common.saving")
                        : t("common.save")
                    }}
                  </button>
                </div>
              </template>
            </div>
          </div>

          <!-- Request Rectifier Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.rectifier.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.rectifier.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Loading State -->
              <div
                v-if="rectifierLoading"
                class="flex items-center gap-2 text-gray-500"
              >
                <div
                  class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"
                ></div>
                {{ t("common.loading") }}
              </div>

              <template v-else>
                <!-- Master Toggle -->
                <div class="flex items-center justify-between">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">{{
                      t("admin.settings.rectifier.enabled")
                    }}</label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.rectifier.enabledHint") }}
                    </p>
                  </div>
                  <Toggle v-model="rectifierForm.enabled" />
                </div>

                <!-- Sub-toggles (only show when master is enabled) -->
                <div
                  v-if="rectifierForm.enabled"
                  class="space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <!-- Thinking Signature Rectifier -->
                  <div class="flex items-center justify-between">
                    <div>
                      <label
                        class="text-sm font-medium text-gray-700 dark:text-gray-300"
                        >{{
                          t("admin.settings.rectifier.thinkingSignature")
                        }}</label
                      >
                      <p class="text-xs text-gray-500 dark:text-gray-400">
                        {{
                          t("admin.settings.rectifier.thinkingSignatureHint")
                        }}
                      </p>
                    </div>
                    <Toggle
                      v-model="rectifierForm.thinking_signature_enabled"
                    />
                  </div>

                  <!-- Thinking Budget Rectifier -->
                  <div class="flex items-center justify-between">
                    <div>
                      <label
                        class="text-sm font-medium text-gray-700 dark:text-gray-300"
                        >{{
                          t("admin.settings.rectifier.thinkingBudget")
                        }}</label
                      >
                      <p class="text-xs text-gray-500 dark:text-gray-400">
                        {{ t("admin.settings.rectifier.thinkingBudgetHint") }}
                      </p>
                    </div>
                    <Toggle v-model="rectifierForm.thinking_budget_enabled" />
                  </div>

                  <!-- API Key Signature Rectifier -->
                  <div class="flex items-center justify-between">
                    <div>
                      <label
                        class="text-sm font-medium text-gray-700 dark:text-gray-300"
                        >{{
                          t("admin.settings.rectifier.apikeySignature")
                        }}</label
                      >
                      <p class="text-xs text-gray-500 dark:text-gray-400">
                        {{ t("admin.settings.rectifier.apikeySignatureHint") }}
                      </p>
                    </div>
                    <Toggle v-model="rectifierForm.apikey_signature_enabled" />
                  </div>

                  <!-- Custom Patterns (only when apikey_signature_enabled) -->
                  <div
                    v-if="rectifierForm.apikey_signature_enabled"
                    class="ml-4 space-y-3 border-l-2 border-gray-200 pl-4 dark:border-dark-600"
                  >
                    <div>
                      <label
                        class="text-sm font-medium text-gray-700 dark:text-gray-300"
                        >{{
                          t("admin.settings.rectifier.apikeyPatterns")
                        }}</label
                      >
                      <p class="text-xs text-gray-500 dark:text-gray-400">
                        {{ t("admin.settings.rectifier.apikeyPatternsHint") }}
                      </p>
                    </div>
                    <div
                      v-for="(
                        _, index
                      ) in rectifierForm.apikey_signature_patterns"
                      :key="index"
                      class="flex items-center gap-2"
                    >
                      <input
                        v-model="rectifierForm.apikey_signature_patterns[index]"
                        type="text"
                        class="input input-sm flex-1"
                        :placeholder="
                          t('admin.settings.rectifier.apikeyPatternPlaceholder')
                        "
                      />
                      <button
                        type="button"
                        @click="
                          rectifierForm.apikey_signature_patterns.splice(
                            index,
                            1,
                          )
                        "
                        class="btn btn-ghost btn-xs text-red-500 hover:text-red-700"
                      >
                        <svg
                          class="h-4 w-4"
                          fill="none"
                          stroke="currentColor"
                          viewBox="0 0 24 24"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            stroke-width="2"
                            d="M6 18L18 6M6 6l12 12"
                          />
                        </svg>
                      </button>
                    </div>
                    <button
                      type="button"
                      @click="rectifierForm.apikey_signature_patterns.push('')"
                      class="btn btn-ghost btn-xs text-primary-600 dark:text-primary-400"
                    >
                      + {{ t("admin.settings.rectifier.addPattern") }}
                    </button>
                  </div>
                </div>

                <!-- Save Button -->
                <div
                  class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <button
                    type="button"
                    @click="saveRectifierSettings"
                    :disabled="rectifierSaving"
                    class="btn btn-primary btn-sm"
                  >
                    <svg
                      v-if="rectifierSaving"
                      class="mr-1 h-4 w-4 animate-spin"
                      fill="none"
                      viewBox="0 0 24 24"
                    >
                      <circle
                        class="opacity-25"
                        cx="12"
                        cy="12"
                        r="10"
                        stroke="currentColor"
                        stroke-width="4"
                      ></circle>
                      <path
                        class="opacity-75"
                        fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                      ></path>
                    </svg>
                    {{
                      rectifierSaving ? t("common.saving") : t("common.save")
                    }}
                  </button>
                </div>
              </template>
            </div>
          </div>
          <!-- Beta Policy Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.betaPolicy.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.betaPolicy.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Loading State -->
              <div
                v-if="betaPolicyLoading"
                class="flex items-center gap-2 text-gray-500"
              >
                <div
                  class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"
                ></div>
                {{ t("common.loading") }}
              </div>

              <template v-else>
                <!-- Rule Cards -->
                <div
                  v-for="rule in betaPolicyForm.rules"
                  :key="rule.beta_token"
                  class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
                >
                  <div class="mb-3 flex items-center gap-2">
                    <span
                      class="text-sm font-medium text-gray-900 dark:text-white"
                    >
                      {{ getBetaDisplayName(rule.beta_token) }}
                    </span>
                    <span
                      class="rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400"
                    >
                      {{ rule.beta_token }}
                    </span>
                  </div>

                  <div class="grid grid-cols-2 gap-4">
                    <!-- Action -->
                    <div>
                      <label
                        class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                      >
                        {{ t("admin.settings.betaPolicy.action") }}
                      </label>
                      <Select
                        :modelValue="rule.action"
                        @update:modelValue="rule.action = $event as any"
                        :options="betaPolicyActionOptions"
                      />
                    </div>

                    <!-- Scope -->
                    <div>
                      <label
                        class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                      >
                        {{ t("admin.settings.betaPolicy.scope") }}
                      </label>
                      <Select
                        :modelValue="rule.scope"
                        @update:modelValue="rule.scope = $event as any"
                        :options="betaPolicyScopeOptions"
                      />
                    </div>
                  </div>

                  <!-- Error Message (only when action=block) -->
                  <div v-if="rule.action === 'block'" class="mt-3">
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.betaPolicy.errorMessage") }}
                    </label>
                    <input
                      v-model="rule.error_message"
                      type="text"
                      class="input"
                      :placeholder="
                        t('admin.settings.betaPolicy.errorMessagePlaceholder')
                      "
                    />
                    <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                      {{ t("admin.settings.betaPolicy.errorMessageHint") }}
                    </p>
                  </div>

                  <!-- Quick Presets (only for tokens with presets) -->
                  <div v-if="betaPresets[rule.beta_token]?.length" class="mt-3">
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.betaPolicy.quickPresets") }}
                    </label>
                    <div class="flex flex-wrap gap-2">
                      <button
                        v-for="preset in betaPresets[rule.beta_token]"
                        :key="preset.label"
                        type="button"
                        class="inline-flex items-center gap-1 rounded-md border border-primary-200 bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 transition-colors hover:bg-primary-100 dark:border-primary-800 dark:bg-primary-900/30 dark:text-primary-300 dark:hover:bg-primary-900/50"
                        @click="applyBetaPreset(rule, preset)"
                        :title="preset.description"
                      >
                        {{ preset.label }}
                      </button>
                    </div>
                  </div>

                  <!-- Model Whitelist -->
                  <div class="mt-3">
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.betaPolicy.modelWhitelist") }}
                    </label>
                    <p class="mb-2 text-xs text-gray-400 dark:text-gray-500">
                      {{ t("admin.settings.betaPolicy.modelWhitelistHint") }}
                    </p>
                    <!-- Existing patterns -->
                    <div
                      v-for="(_, index) in rule.model_whitelist || []"
                      :key="index"
                      class="mb-1.5 flex items-center gap-2"
                    >
                      <input
                        v-model="rule.model_whitelist![index]"
                        type="text"
                        class="input input-sm flex-1"
                        :placeholder="
                          t('admin.settings.betaPolicy.modelPatternPlaceholder')
                        "
                      />
                      <button
                        type="button"
                        @click="rule.model_whitelist!.splice(index, 1)"
                        class="shrink-0 rounded p-1 text-red-400 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                      >
                        <svg
                          class="h-4 w-4"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                          stroke-width="2"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            d="M6 18L18 6M6 6l12 12"
                          />
                        </svg>
                      </button>
                    </div>
                    <!-- Add pattern button -->
                    <button
                      type="button"
                      @click="
                        if (!rule.model_whitelist) rule.model_whitelist = [];
                        rule.model_whitelist.push('');
                      "
                      class="mb-2 inline-flex items-center gap-1 text-xs text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                    >
                      <svg
                        class="h-3.5 w-3.5"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        stroke-width="2"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M12 4v16m8-8H4"
                        />
                      </svg>
                      {{ t("admin.settings.betaPolicy.addModelPattern") }}
                    </button>
                    <!-- Common pattern chips -->
                    <div class="flex flex-wrap items-center gap-1.5">
                      <span class="text-xs text-gray-400 dark:text-gray-500"
                        >{{
                          t("admin.settings.betaPolicy.commonPatterns")
                        }}:</span
                      >
                      <button
                        v-for="pattern in commonModelPatterns"
                        :key="pattern"
                        type="button"
                        class="rounded border border-gray-200 px-2 py-0.5 text-xs text-gray-600 transition-colors hover:border-primary-300 hover:bg-primary-50 hover:text-primary-700 dark:border-dark-600 dark:text-gray-400 dark:hover:border-primary-700 dark:hover:bg-primary-900/30 dark:hover:text-primary-300"
                        @click="addQuickPattern(rule, pattern)"
                      >
                        {{ pattern }}
                      </button>
                    </div>
                  </div>

                  <!-- Fallback Action (only when model_whitelist is non-empty) -->
                  <div
                    v-if="
                      rule.model_whitelist && rule.model_whitelist.length > 0
                    "
                    class="mt-3"
                  >
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.betaPolicy.fallbackAction") }}
                    </label>
                    <Select
                      :modelValue="rule.fallback_action || 'pass'"
                      @update:modelValue="rule.fallback_action = $event as any"
                      :options="betaPolicyActionOptions"
                    />
                    <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                      {{ t("admin.settings.betaPolicy.fallbackActionHint") }}
                    </p>
                    <!-- Fallback Error Message (only when fallback_action=block) -->
                    <div v-if="rule.fallback_action === 'block'" class="mt-2">
                      <input
                        v-model="rule.fallback_error_message"
                        type="text"
                        class="input"
                        :placeholder="
                          t(
                            'admin.settings.betaPolicy.fallbackErrorMessagePlaceholder',
                          )
                        "
                      />
                      <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                        {{ t("admin.settings.betaPolicy.errorMessageHint") }}
                      </p>
                    </div>
                  </div>
                </div>

                <!-- Save Button -->
                <div
                  class="flex justify-end border-t border-gray-100 pt-4 dark:border-dark-700"
                >
                  <button
                    type="button"
                    @click="saveBetaPolicySettings"
                    :disabled="betaPolicySaving"
                    class="btn btn-primary btn-sm"
                  >
                    <svg
                      v-if="betaPolicySaving"
                      class="mr-1 h-4 w-4 animate-spin"
                      fill="none"
                      viewBox="0 0 24 24"
                    >
                      <circle
                        class="opacity-25"
                        cx="12"
                        cy="12"
                        r="10"
                        stroke="currentColor"
                        stroke-width="4"
                      ></circle>
                      <path
                        class="opacity-75"
                        fill="currentColor"
                        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                      ></path>
                    </svg>
                    {{
                      betaPolicySaving ? t("common.saving") : t("common.save")
                    }}
                  </button>
                </div>
              </template>
            </div>
          </div>
          <!-- OpenAI Fast/Flex Policy Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.openaiFastPolicy.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.openaiFastPolicy.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Empty state -->
              <div
                v-if="openaiFastPolicyForm.rules.length === 0"
                class="rounded-lg border border-dashed border-gray-200 p-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
              >
                {{ t("admin.settings.openaiFastPolicy.empty") }}
              </div>

              <!-- Rule Cards -->
              <div
                v-for="(rule, ruleIndex) in openaiFastPolicyForm.rules"
                :key="ruleIndex"
                class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="mb-3 flex items-center justify-between">
                  <span
                    class="text-sm font-medium text-gray-900 dark:text-white"
                  >
                    {{
                      t("admin.settings.openaiFastPolicy.ruleHeader", {
                        index: ruleIndex + 1,
                      })
                    }}
                  </span>
                  <button
                    type="button"
                    @click="removeOpenAIFastPolicyRule(ruleIndex)"
                    class="rounded p-1 text-red-400 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                    :title="t('admin.settings.openaiFastPolicy.removeRule')"
                  >
                    <svg
                      class="h-4 w-4"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                      stroke-width="2"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        d="M6 18L18 6M6 6l12 12"
                      />
                    </svg>
                  </button>
                </div>

                <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
                  <!-- Service Tier -->
                  <div>
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.openaiFastPolicy.serviceTier") }}
                    </label>
                    <Select
                      :modelValue="rule.service_tier"
                      @update:modelValue="
                        rule.service_tier = $event as
                          | 'all'
                          | 'priority'
                          | 'flex'
                      "
                      :options="openaiFastPolicyTierOptions"
                    />
                  </div>

                  <!-- Action -->
                  <div>
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.openaiFastPolicy.action") }}
                    </label>
                    <Select
                      :modelValue="rule.action"
                      @update:modelValue="
                        rule.action = $event as
                          | 'pass'
                          | 'filter'
                          | 'block'
                          | 'force_priority'
                      "
                      :options="openaiFastPolicyActionOptions"
                    />
                  </div>

                  <!-- Scope -->
                  <div>
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.openaiFastPolicy.scope") }}
                    </label>
                    <Select
                      :modelValue="rule.scope"
                      @update:modelValue="
                        rule.scope = $event as
                          | 'all'
                          | 'oauth'
                          | 'apikey'
                          | 'bedrock'
                      "
                      :options="openaiFastPolicyScopeOptions"
                    />
                  </div>
                </div>

                <!-- 用户范围 -->
                <div class="mt-3">
                  <label
                    class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                  >
                    {{ t("admin.settings.openaiFastPolicy.userIds") }}
                  </label>
                  <p class="mb-2 text-xs text-gray-400 dark:text-gray-500">
                    {{ t("admin.settings.openaiFastPolicy.userIdsHint") }}
                  </p>
                  <div
                    v-for="(_, userIDIndex) in rule.user_ids || []"
                    :key="userIDIndex"
                    class="mb-1.5 flex items-center gap-2"
                  >
                    <input
                      v-model.number="rule.user_ids![userIDIndex]"
                      type="number"
                      min="1"
                      step="1"
                      class="input input-sm flex-1"
                      :placeholder="t('admin.settings.openaiFastPolicy.userIdPlaceholder')"
                    />
                    <button
                      type="button"
                      @click="removeOpenAIFastPolicyUserID(rule, userIDIndex)"
                      class="shrink-0 rounded p-1 text-red-400 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                      :title="t('admin.settings.openaiFastPolicy.removeUserId')"
                    >
                      <Icon
                        name="x"
                        size="sm"
                        class="h-4 w-4"
                        :stroke-width="2"
                      />
                    </button>
                  </div>
                  <button
                    type="button"
                    @click="addOpenAIFastPolicyUserID(rule)"
                    class="mb-2 inline-flex items-center gap-1 text-xs text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                  >
                    <Icon
                      name="plus"
                      size="xs"
                      class="h-3.5 w-3.5"
                      :stroke-width="2"
                    />
                    {{ t("admin.settings.openaiFastPolicy.addUserId") }}
                  </button>
                </div>

                <!-- Error Message (only when action=block) -->
                <div v-if="rule.action === 'block'" class="mt-3">
                  <label
                    class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                  >
                    {{ t("admin.settings.openaiFastPolicy.errorMessage") }}
                  </label>
                  <input
                    v-model="rule.error_message"
                    type="text"
                    class="input"
                    :placeholder="
                      t(
                        'admin.settings.openaiFastPolicy.errorMessagePlaceholder',
                      )
                    "
                  />
                  <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                    {{ t("admin.settings.openaiFastPolicy.errorMessageHint") }}
                  </p>
                </div>

                <!-- Model Whitelist -->
                <div class="mt-3">
                  <label
                    class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                  >
                    {{ t("admin.settings.openaiFastPolicy.modelWhitelist") }}
                  </label>
                  <p class="mb-2 text-xs text-gray-400 dark:text-gray-500">
                    {{
                      t("admin.settings.openaiFastPolicy.modelWhitelistHint")
                    }}
                  </p>
                  <div
                    v-for="(_, patternIdx) in rule.model_whitelist || []"
                    :key="patternIdx"
                    class="mb-1.5 flex items-center gap-2"
                  >
                    <input
                      v-model="rule.model_whitelist![patternIdx]"
                      type="text"
                      class="input input-sm flex-1"
                      :placeholder="
                        t(
                          'admin.settings.openaiFastPolicy.modelPatternPlaceholder',
                        )
                      "
                    />
                    <button
                      type="button"
                      @click="
                        removeOpenAIFastPolicyModelPattern(rule, patternIdx)
                      "
                      class="shrink-0 rounded p-1 text-red-400 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                    >
                      <svg
                        class="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        stroke-width="2"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M6 18L18 6M6 6l12 12"
                        />
                      </svg>
                    </button>
                  </div>
                  <button
                    type="button"
                    @click="addOpenAIFastPolicyModelPattern(rule)"
                    class="mb-2 inline-flex items-center gap-1 text-xs text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                  >
                    <svg
                      class="h-3.5 w-3.5"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                      stroke-width="2"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        d="M12 4v16m8-8H4"
                      />
                    </svg>
                    {{ t("admin.settings.openaiFastPolicy.addModelPattern") }}
                  </button>
                </div>

                <!-- Fallback Action (only when model_whitelist is non-empty) -->
                <div
                  v-if="
                    rule.model_whitelist && rule.model_whitelist.length > 0
                  "
                  class="mt-3"
                >
                  <label
                    class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                  >
                    {{ t("admin.settings.openaiFastPolicy.fallbackAction") }}
                  </label>
                  <Select
                    :modelValue="rule.fallback_action || 'pass'"
                    @update:modelValue="
                      rule.fallback_action = $event as
                        | 'pass'
                        | 'filter'
                        | 'block'
                        | 'force_priority'
                    "
                    :options="openaiFastPolicyActionOptions"
                  />
                  <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                    {{
                      t("admin.settings.openaiFastPolicy.fallbackActionHint")
                    }}
                  </p>
                  <div v-if="rule.fallback_action === 'block'" class="mt-2">
                    <input
                      v-model="rule.fallback_error_message"
                      type="text"
                      class="input"
                      :placeholder="
                        t(
                          'admin.settings.openaiFastPolicy.fallbackErrorMessagePlaceholder',
                        )
                      "
                    />
                  </div>
                </div>
              </div>

              <!-- Add Rule Button -->
              <div>
                <button
                  type="button"
                  @click="addOpenAIFastPolicyRule"
                  class="btn btn-secondary btn-sm inline-flex items-center gap-1"
                >
                  <svg
                    class="h-4 w-4"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    stroke-width="2"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      d="M12 4v16m8-8H4"
                    />
                  </svg>
                  {{ t("admin.settings.openaiFastPolicy.addRule") }}
                </button>
                <p class="mt-2 text-xs text-gray-400 dark:text-gray-500">
                  {{ t("admin.settings.openaiFastPolicy.saveHint") }}
                </p>
              </div>
            </div>
          </div>
        </div>
        <!-- /Tab: Gateway -->

        <!-- Tab: Security — Registration, Turnstile, LinuxDo -->
        <div v-show="activeTab === 'security'" class="space-y-6">
          <!-- Registration Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.registration.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.registration.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Enable Registration -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.enableRegistration")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.registration.enableRegistrationHint")
                    }}
                  </p>
                </div>
                <Toggle v-model="form.registration_enabled" />
              </div>

              <!-- Email Verification -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.emailVerification")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.registration.emailVerificationHint") }}
                  </p>
                </div>
                <Toggle v-model="form.email_verify_enabled" />
              </div>

              <!-- Email Suffix Whitelist -->
              <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                <label class="font-medium text-gray-900 dark:text-white">{{
                  t("admin.settings.registration.emailSuffixWhitelist")
                }}</label>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  {{
                    t("admin.settings.registration.emailSuffixWhitelistHint")
                  }}
                </p>
                <div
                  class="mt-3 rounded-lg border border-gray-300 bg-white p-2 dark:border-dark-500 dark:bg-dark-700"
                >
                  <div class="flex flex-wrap items-center gap-2">
                    <span
                      v-for="suffix in registrationEmailSuffixWhitelistTags"
                      :key="suffix"
                      class="inline-flex items-center gap-1 rounded bg-gray-100 px-2 py-1 text-xs font-mono text-gray-700 dark:bg-dark-600 dark:text-gray-200"
                    >
                      <span>{{ suffix }}</span>
                      <button
                        type="button"
                        class="rounded-full text-gray-500 hover:bg-gray-200 hover:text-gray-700 dark:text-gray-300 dark:hover:bg-dark-500 dark:hover:text-white"
                        @click="
                          removeRegistrationEmailSuffixWhitelistTag(suffix)
                        "
                      >
                        <Icon
                          name="x"
                          size="xs"
                          class="h-3.5 w-3.5"
                          :stroke-width="2"
                        />
                      </button>
                    </span>

                    <div
                      class="flex min-w-[220px] flex-1 items-center gap-1 rounded border border-transparent px-2 py-1 focus-within:border-primary-300 dark:focus-within:border-primary-700"
                    >
                      <input
                        v-model="registrationEmailSuffixWhitelistDraft"
                        type="text"
                        class="w-full bg-transparent text-sm font-mono text-gray-900 outline-none placeholder:text-gray-400 dark:text-white dark:placeholder:text-gray-500"
                        :placeholder="
                          t(
                            'admin.settings.registration.emailSuffixWhitelistPlaceholder',
                          )
                        "
                        @input="
                          handleRegistrationEmailSuffixWhitelistDraftInput
                        "
                        @keydown="
                          handleRegistrationEmailSuffixWhitelistDraftKeydown
                        "
                        @blur="commitRegistrationEmailSuffixWhitelistDraft"
                        @paste="handleRegistrationEmailSuffixWhitelistPaste"
                      />
                    </div>
                  </div>
                </div>
                <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                  {{
                    t(
                      "admin.settings.registration.emailSuffixWhitelistInputHint",
                    )
                  }}
                </p>
              </div>

              <!-- 邮箱地址归一化 -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.emailNormalization")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.registration.emailNormalizationHint")
                    }}
                  </p>
                </div>
                <Toggle v-model="form.registration_email_normalization" />
              </div>

              <!-- Promo Code -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.promoCode")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.registration.promoCodeHint") }}
                  </p>
                </div>
                <Toggle v-model="form.promo_code_enabled" />
              </div>

              <!-- Invitation Code -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.invitationCode")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.registration.invitationCodeHint") }}
                  </p>
                </div>
                <Toggle v-model="form.invitation_code_enabled" />
              </div>
              <!-- Password Reset - Only show when email verification is enabled -->
              <div
                v-if="form.email_verify_enabled"
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.passwordReset")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.registration.passwordResetHint") }}
                  </p>
                </div>
                <Toggle v-model="form.password_reset_enabled" />
              </div>
              <!-- Frontend URL - Only show when password reset is enabled -->
              <div
                v-if="form.email_verify_enabled && form.password_reset_enabled"
                class="border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.registration.frontendUrl") }}
                </label>
                <input
                  v-model="form.frontend_url"
                  type="url"
                  class="input"
                  :placeholder="
                    t('admin.settings.registration.frontendUrlPlaceholder')
                  "
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.registration.frontendUrlHint") }}
                </p>
              </div>

              <!-- TOTP 2FA -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.registration.totp")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.registration.totpHint") }}
                  </p>
                  <!-- 未配置密钥时只阻止启用，保留关闭已有开关的能力 -->
                  <p
                    v-if="!form.totp_encryption_key_configured"
                    class="mt-2 text-sm text-amber-600 dark:text-amber-400"
                  >
                    {{ t("admin.settings.registration.totpKeyNotConfigured") }}
                  </p>
                </div>
                <Toggle
                  v-model="form.totp_enabled"
                  :disabled="
                    !form.totp_encryption_key_configured && !form.totp_enabled
                  "
                />
              </div>
            </div>
          </div>

          <!-- API Key IP ACL Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.apiKeyAcl.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.apiKeyAcl.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.apiKeyAcl.trustForwardedIp") }}
                  </label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.apiKeyAcl.trustForwardedIpHint") }}
                  </p>
                </div>
                <Toggle v-model="form.api_key_acl_trust_forwarded_ip" />
              </div>
            </div>
          </div>

          <!-- Cloudflare Turnstile Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.turnstile.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.turnstile.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Enable Turnstile -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.turnstile.enableTurnstile")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.turnstile.enableTurnstileHint") }}
                  </p>
                </div>
                <Toggle v-model="form.turnstile_enabled" />
              </div>

              <!-- Turnstile Keys - Only show when enabled -->
              <div
                v-if="form.turnstile_enabled"
                class="border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div class="grid grid-cols-1 gap-6">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.turnstile.siteKey") }}
                    </label>
                    <input
                      v-model="form.turnstile_site_key"
                      type="text"
                      class="input font-mono text-sm"
                      placeholder="0x4AAAAAAA..."
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.turnstile.siteKeyHint") }}
                      <a
                        href="https://dash.cloudflare.com/"
                        target="_blank"
                        class="text-primary-600 hover:text-primary-500"
                        >{{
                          t("admin.settings.turnstile.cloudflareDashboard")
                        }}</a
                      >
                    </p>
                  </div>
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.turnstile.secretKey") }}
                    </label>
                    <input
                      v-model="form.turnstile_secret_key"
                      type="password"
                      class="input font-mono text-sm"
                      placeholder="0x4AAAAAAA..."
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        form.turnstile_secret_key_configured
                          ? t(
                              "admin.settings.turnstile.secretKeyConfiguredHint",
                            )
                          : t("admin.settings.turnstile.secretKeyHint")
                      }}
                    </p>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- LinuxDo Connect OAuth 登录 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.linuxdo.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.linuxdo.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.linuxdo.enable")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.linuxdo.enableHint") }}
                  </p>
                </div>
                <Toggle v-model="form.linuxdo_connect_enabled" />
              </div>

              <div
                v-if="form.linuxdo_connect_enabled"
                class="border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div class="grid grid-cols-1 gap-6">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.linuxdo.clientId") }}
                    </label>
                    <input
                      v-model="form.linuxdo_connect_client_id"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.linuxdo.clientIdPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.linuxdo.clientIdHint") }}
                    </p>
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.linuxdo.clientSecret") }}
                    </label>
                    <input
                      v-model="form.linuxdo_connect_client_secret"
                      type="password"
                      class="input font-mono text-sm"
                      :placeholder="
                        form.linuxdo_connect_client_secret_configured
                          ? t(
                              'admin.settings.linuxdo.clientSecretConfiguredPlaceholder',
                            )
                          : t('admin.settings.linuxdo.clientSecretPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        form.linuxdo_connect_client_secret_configured
                          ? t(
                              "admin.settings.linuxdo.clientSecretConfiguredHint",
                            )
                          : t("admin.settings.linuxdo.clientSecretHint")
                      }}
                    </p>
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.linuxdo.redirectUrl") }}
                    </label>
                    <input
                      v-model="form.linuxdo_connect_redirect_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.linuxdo.redirectUrlPlaceholder')
                      "
                    />
                    <div
                      class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3"
                    >
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm w-fit"
                        @click="setAndCopyLinuxdoRedirectUrl"
                      >
                        {{ t("admin.settings.linuxdo.quickSetCopy") }}
                      </button>
                      <code
                        v-if="linuxdoRedirectUrlSuggestion"
                        class="select-all break-all rounded bg-gray-50 px-2 py-1 font-mono text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
                      >
                        {{ linuxdoRedirectUrlSuggestion }}
                      </code>
                    </div>
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.linuxdo.redirectUrlHint") }}
                    </p>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- GitHub / Google 邮箱快捷登录 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ localText("邮箱快捷登录", "Email OAuth Sign-in") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{
                  localText(
                    "开启 GitHub 或 Google 邮箱授权登录后，系统会读取已验证邮箱，存在则直接登录，不存在则进入本地注册补全流程。",
                    "After GitHub or Google email OAuth is enabled, the system reads a verified email, signs in matching users, and sends missing users through local registration completion.",
                  )
                }}
              </p>
            </div>
            <div class="space-y-6 p-6">
              <div class="grid grid-cols-1 gap-6 xl:grid-cols-2">
                <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
                  <div class="flex items-start justify-between gap-4">
                    <div>
                      <h3 class="font-medium text-gray-900 dark:text-white">
                        GitHub
                      </h3>
                      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                        {{
                          localText(
                            "GitHub OAuth App 需要 read:user user:email 权限，回调地址填写下方后端地址。",
                            "GitHub OAuth App needs read:user user:email scopes. Use the backend callback URL below.",
                          )
                        }}
                      </p>
                    </div>
                    <Toggle v-model="form.github_oauth_enabled" />
                  </div>

                  <div v-if="form.github_oauth_enabled" class="mt-4 space-y-4">
                    <div class="rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                      <template v-if="isZhLocale">
                        开通引导：GitHub Settings → Developer settings →
                        <a
                          data-testid="github-oauth-apps-guide-link"
                          href="https://github.com/settings/developers"
                          target="_blank"
                          rel="noopener noreferrer"
                          class="font-medium text-primary-600 hover:underline dark:text-primary-400"
                        >OAuth Apps</a>
                        → New OAuth App；Homepage URL 填站点域名，Authorization callback URL 填下面的后端回调地址。
                      </template>
                      <template v-else>
                        Setup guide: GitHub Settings → Developer settings →
                        <a
                          data-testid="github-oauth-apps-guide-link"
                          href="https://github.com/settings/developers"
                          target="_blank"
                          rel="noopener noreferrer"
                          class="font-medium text-primary-600 hover:underline dark:text-primary-400"
                        >OAuth Apps</a>
                        → New OAuth App. Use your site origin as Homepage URL and the backend callback URL below as Authorization callback URL.
                      </template>
                    </div>

                    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                      <div>
                        <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">Client ID</label>
                        <input
                          v-model="form.github_oauth_client_id"
                          type="text"
                          class="input font-mono text-sm"
                          placeholder="GitHub OAuth Client ID"
                        />
                      </div>
                      <div>
                        <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">Client Secret</label>
                        <input
                          v-model="form.github_oauth_client_secret"
                          type="password"
                          class="input font-mono text-sm"
                          :placeholder="
                            form.github_oauth_client_secret_configured
                              ? localText('密钥已配置，留空以保留当前值。', 'Secret configured. Leave empty to keep the current value.')
                              : 'GitHub OAuth Client Secret'
                          "
                        />
                      </div>
                    </div>

                    <div>
                      <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                        {{ localText("后端回调地址", "Backend Callback URL") }}
                      </label>
                      <input
                        v-model="form.github_oauth_redirect_url"
                        type="url"
                        class="input font-mono text-sm"
                        placeholder="https://your-domain.com/api/v1/auth/oauth/github/callback"
                      />
                      <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                        <button
                          type="button"
                          class="btn btn-secondary btn-sm w-fit"
                          @click="setAndCopyEmailOAuthRedirectUrl('github')"
                        >
                          {{ localText("生成并复制", "Generate and copy") }}
                        </button>
                        <code
                          v-if="githubOAuthRedirectUrlSuggestion"
                          class="select-all break-all rounded bg-gray-50 px-2 py-1 font-mono text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
                        >
                          {{ githubOAuthRedirectUrlSuggestion }}
                        </code>
                      </div>
                    </div>

                    <div>
                      <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                        {{ localText("前端回跳地址", "Frontend Callback URL") }}
                      </label>
                      <input
                        v-model="form.github_oauth_frontend_redirect_url"
                        type="text"
                        class="input font-mono text-sm"
                        placeholder="/auth/oauth/callback"
                      />
                    </div>
                  </div>
                </div>

                <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
                  <div class="flex items-start justify-between gap-4">
                    <div>
                      <h3 class="font-medium text-gray-900 dark:text-white">
                        Google
                      </h3>
                      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                        {{
                          localText(
                            "Google OAuth 客户端需要 openid email profile 范围，并在凭据里登记后端回调地址。",
                            "Google OAuth client needs openid email profile scopes and the backend callback URL registered in credentials.",
                          )
                        }}
                      </p>
                    </div>
                    <Toggle v-model="form.google_oauth_enabled" />
                  </div>

                  <div v-if="form.google_oauth_enabled" class="mt-4 space-y-4">
                    <div class="rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                      {{
                        localText(
                          "开通引导：Google Cloud Console → APIs & Services → OAuth consent screen 完成同意屏幕；Credentials → Create Credentials → OAuth client ID，类型选择 Web application，并把下面地址加入 Authorized redirect URIs。",
                          "Setup guide: Google Cloud Console → APIs & Services → OAuth consent screen, then Credentials → Create Credentials → OAuth client ID, choose Web application, and add the URL below to Authorized redirect URIs.",
                        )
                      }}
                    </div>

                    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                      <div>
                        <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">Client ID</label>
                        <input
                          v-model="form.google_oauth_client_id"
                          type="text"
                          class="input font-mono text-sm"
                          placeholder="Google OAuth Client ID"
                        />
                      </div>
                      <div>
                        <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">Client Secret</label>
                        <input
                          v-model="form.google_oauth_client_secret"
                          type="password"
                          class="input font-mono text-sm"
                          :placeholder="
                            form.google_oauth_client_secret_configured
                              ? localText('密钥已配置，留空以保留当前值。', 'Secret configured. Leave empty to keep the current value.')
                              : 'Google OAuth Client Secret'
                          "
                        />
                      </div>
                    </div>

                    <div>
                      <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                        {{ localText("后端回调地址", "Backend Callback URL") }}
                      </label>
                      <input
                        v-model="form.google_oauth_redirect_url"
                        type="url"
                        class="input font-mono text-sm"
                        placeholder="https://your-domain.com/api/v1/auth/oauth/google/callback"
                      />
                      <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
                        <button
                          type="button"
                          class="btn btn-secondary btn-sm w-fit"
                          @click="setAndCopyEmailOAuthRedirectUrl('google')"
                        >
                          {{ localText("生成并复制", "Generate and copy") }}
                        </button>
                        <code
                          v-if="googleOAuthRedirectUrlSuggestion"
                          class="select-all break-all rounded bg-gray-50 px-2 py-1 font-mono text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
                        >
                          {{ googleOAuthRedirectUrlSuggestion }}
                        </code>
                      </div>
                    </div>

                    <div>
                      <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                        {{ localText("前端回跳地址", "Frontend Callback URL") }}
                      </label>
                      <input
                        v-model="form.google_oauth_frontend_redirect_url"
                        type="text"
                        class="input font-mono text-sm"
                        placeholder="/auth/oauth/callback"
                      />
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- WeChat Connect OAuth 登录 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.wechatConnect.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.wechatConnect.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.wechatConnect.enabledLabel")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.wechatConnect.enabledHint") }}
                  </p>
                </div>
                <Toggle
                  v-model="form.wechat_connect_enabled"
                  data-testid="wechat-connect-enabled"
                />
              </div>

              <div
                v-if="form.wechat_connect_enabled"
                class="space-y-6 border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div class="space-y-4">
                  <div
                    class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"
                  >
                    <div class="flex items-start justify-between gap-4">
                      <div>
                        <h3 class="font-medium text-gray-900 dark:text-white">
                          {{ localText("PC 应用", "PC App") }}
                        </h3>
                        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                          {{
                            localText(
                              "桌面浏览器通过微信开放平台扫码登录。可与公众号或移动应用同时存在。",
                              "Desktop browsers sign in through WeChat Open Platform QR login. This can coexist with Official Account or Mobile App.",
                            )
                          }}
                        </p>
                      </div>
                      <Toggle
                        :model-value="form.wechat_connect_open_enabled"
                        data-testid="wechat-connect-open-enabled"
                        @update:model-value="handleWeChatOpenEnabledChange"
                      />
                    </div>
                    <div
                      v-if="form.wechat_connect_open_enabled"
                      class="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2"
                    >
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ localText("PC AppID", "PC App ID") }}
                        </label>
                        <input
                          v-model="form.wechat_connect_open_app_id"
                          data-testid="wechat-connect-open-app-id"
                          type="text"
                          class="input font-mono text-sm"
                          :placeholder="
                            localText(
                              '微信开放平台 PC 应用 AppID',
                              'WeChat Open Platform PC App ID',
                            )
                          "
                        />
                      </div>
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ localText("PC AppSecret", "PC App Secret") }}
                        </label>
                        <input
                          v-model="form.wechat_connect_open_app_secret"
                          data-testid="wechat-connect-open-app-secret"
                          type="password"
                          class="input font-mono text-sm"
                          :placeholder="
                            form.wechat_connect_open_app_secret_configured
                              ? localText(
                                  '密钥已配置，留空以保留当前值。',
                                  'Secret configured. Leave empty to keep the current value.',
                                )
                              : localText(
                                  '微信开放平台 PC 应用 AppSecret',
                                  'WeChat Open Platform PC App Secret',
                                )
                          "
                        />
                      </div>
                    </div>
                  </div>

                  <div
                    class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"
                  >
                    <div class="flex items-start justify-between gap-4">
                      <div>
                        <h3 class="font-medium text-gray-900 dark:text-white">
                          {{ localText("公众号", "Official Account") }}
                        </h3>
                        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                          {{
                            localText(
                              "仅在微信内浏览器可用；非微信环境下会显示不可用。",
                              "Only available inside the WeChat browser. It is shown as unavailable outside WeChat.",
                            )
                          }}
                        </p>
                      </div>
                      <Toggle
                        :model-value="form.wechat_connect_mp_enabled"
                        data-testid="wechat-connect-mp-enabled"
                        @update:model-value="handleWeChatMPEnabledChange"
                      />
                    </div>
                    <div
                      v-if="form.wechat_connect_mp_enabled"
                      class="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2"
                    >
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ localText("公众号 AppID", "Official Account App ID") }}
                        </label>
                        <input
                          v-model="form.wechat_connect_mp_app_id"
                          data-testid="wechat-connect-mp-app-id"
                          type="text"
                          class="input font-mono text-sm"
                          :placeholder="
                            localText(
                              '公众号 AppID',
                              'Official Account App ID',
                            )
                          "
                        />
                      </div>
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{
                            localText(
                              "公众号 AppSecret",
                              "Official Account App Secret",
                            )
                          }}
                        </label>
                        <input
                          v-model="form.wechat_connect_mp_app_secret"
                          data-testid="wechat-connect-mp-app-secret"
                          type="password"
                          class="input font-mono text-sm"
                          :placeholder="
                            form.wechat_connect_mp_app_secret_configured
                              ? localText(
                                  '密钥已配置，留空以保留当前值。',
                                  'Secret configured. Leave empty to keep the current value.',
                                )
                              : localText(
                                  '公众号 AppSecret',
                                  'Official Account App Secret',
                                )
                          "
                        />
                      </div>
                    </div>
                  </div>

                  <div
                    class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"
                  >
                    <div class="flex items-start justify-between gap-4">
                      <div>
                        <h3 class="font-medium text-gray-900 dark:text-white">
                          {{ localText("移动应用", "Mobile App") }}
                        </h3>
                        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                          {{
                            localText(
                              "原生移动端通过微信 SDK 唤起授权，网页端不会直接发起该流程。",
                              "Native mobile clients start authorization through the WeChat SDK. The web UI does not launch this flow directly.",
                            )
                          }}
                        </p>
                      </div>
                      <Toggle
                        :model-value="form.wechat_connect_mobile_enabled"
                        data-testid="wechat-connect-mobile-enabled"
                        @update:model-value="handleWeChatMobileEnabledChange"
                      />
                    </div>
                    <div
                      v-if="form.wechat_connect_mobile_enabled"
                      class="mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2"
                    >
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ localText("移动应用 AppID", "Mobile App ID") }}
                        </label>
                        <input
                          v-model="form.wechat_connect_mobile_app_id"
                          data-testid="wechat-connect-mobile-app-id"
                          type="text"
                          class="input font-mono text-sm"
                          :placeholder="
                            localText(
                              '移动应用 AppID',
                              'Mobile App ID',
                            )
                          "
                        />
                      </div>
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ localText("移动应用 AppSecret", "Mobile App Secret") }}
                        </label>
                        <input
                          v-model="form.wechat_connect_mobile_app_secret"
                          data-testid="wechat-connect-mobile-app-secret"
                          type="password"
                          class="input font-mono text-sm"
                          :placeholder="
                            form.wechat_connect_mobile_app_secret_configured
                              ? localText(
                                  '密钥已配置，留空以保留当前值。',
                                  'Secret configured. Leave empty to keep the current value.',
                                )
                              : localText(
                                  '移动应用 AppSecret',
                                  'Mobile App Secret',
                                )
                          "
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <div
                  v-if="
                    form.wechat_connect_open_enabled &&
                    (form.wechat_connect_mp_enabled ||
                      form.wechat_connect_mobile_enabled)
                  "
                  class="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/40 dark:bg-amber-900/10 dark:text-amber-300"
                >
                  {{
                    localText(
                      "如果同时启用 PC 应用和公众号/移动应用，这些应用需要挂在同一个微信开放平台主体下，否则 UnionID 无法稳定归并账号。",
                      "When PC App is enabled together with Official Account or Mobile App, they should belong to the same WeChat Open Platform account so UnionID can merge identities reliably.",
                    )
                  }}
                </div>

                <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{
                        localText(
                          "浏览器回调地址",
                          "Browser Redirect URL",
                        )
                      }}
                    </label>
                    <input
                      data-testid="wechat-connect-redirect-url"
                      v-model="form.wechat_connect_redirect_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="t('admin.settings.wechatConnect.redirectUrlPlaceholder')"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        localText(
                          "用于 PC 应用和公众号的网页回调。移动应用走原生 SDK 时不直接使用这个浏览器回调。",
                          "Used by PC App and Official Account browser callbacks. Native mobile SDK flows do not start from this browser callback directly.",
                        )
                      }}
                    </p>
                    <div
                      class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3"
                    >
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm w-fit"
                        @click="setAndCopyWeChatRedirectUrl"
                      >
                        {{ t("admin.settings.wechatConnect.generateAndCopy") }}
                      </button>
                      <code
                        v-if="wechatRedirectUrlSuggestion"
                        class="select-all break-all rounded bg-gray-50 px-2 py-1 font-mono text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
                      >
                        {{ wechatRedirectUrlSuggestion }}
                      </code>
                    </div>
                  </div>
                </div>

                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.wechatConnect.frontendRedirectUrlLabel") }}
                  </label>
                  <input
                    data-testid="wechat-connect-frontend-redirect-url"
                    v-model="form.wechat_connect_frontend_redirect_url"
                    type="text"
                    class="input font-mono text-sm"
                    :placeholder="t('admin.settings.wechatConnect.frontendRedirectUrlPlaceholder')"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.wechatConnect.frontendRedirectUrlHint") }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <!-- 钉钉 Connect OAuth 登录 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.dingtalk.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.dingtalk.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.dingtalk.enable")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.dingtalk.enableHint") }}
                  </p>
                </div>
                <Toggle v-model="form.dingtalk_connect_enabled" />
              </div>

              <div
                v-if="form.dingtalk_connect_enabled"
                class="border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div class="grid grid-cols-1 gap-6">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.dingtalk.clientId") }}
                    </label>
                    <input
                      v-model="form.dingtalk_connect_client_id"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.dingtalk.clientIdPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.dingtalk.clientIdHint") }}
                    </p>
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.dingtalk.clientSecret") }}
                    </label>
                    <input
                      v-model="form.dingtalk_connect_client_secret"
                      type="password"
                      class="input font-mono text-sm"
                      :placeholder="
                        form.dingtalk_connect_client_secret_configured
                          ? t(
                              'admin.settings.dingtalk.clientSecretConfiguredPlaceholder',
                            )
                          : t('admin.settings.dingtalk.clientSecretPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        form.dingtalk_connect_client_secret_configured
                          ? t(
                              "admin.settings.dingtalk.clientSecretConfiguredHint",
                            )
                          : t("admin.settings.dingtalk.clientSecretHint")
                      }}
                    </p>
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.dingtalk.redirectUrl") }}
                    </label>
                    <input
                      v-model="form.dingtalk_connect_redirect_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.dingtalk.redirectUrlPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.dingtalk.redirectUrlHint") }}
                    </p>
                  </div>

                  <!-- 企业限制策略 -->
                  <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                    <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                      {{ t("admin.settings.dingtalk.corpPolicy.label") }}
                    </label>
                    <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.dingtalk.corpPolicy.hint") }}
                    </p>
                    <div class="space-y-2">
                      <label class="flex cursor-pointer items-center gap-3">
                        <input
                          v-model="form.dingtalk_connect_corp_restriction_policy"
                          type="radio"
                          value="none"
                          class="h-4 w-4 text-primary-600"
                        />
                        <span class="text-sm text-gray-700 dark:text-gray-300">
                          {{ t("admin.settings.dingtalk.corpPolicy.none") }}
                        </span>
                      </label>
                      <label class="flex cursor-pointer items-center gap-3">
                        <input
                          v-model="form.dingtalk_connect_corp_restriction_policy"
                          type="radio"
                          value="internal_only"
                          class="h-4 w-4 text-primary-600"
                        />
                        <span class="text-sm text-gray-700 dark:text-gray-300">
                          {{ t("admin.settings.dingtalk.corpPolicy.internalOnly") }}
                        </span>
                      </label>
                    </div>
                  </div>

                  <!-- bypass_registration 开关（仅 internal_only 模式下可见可用） -->
                  <div
                    v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                    class="flex items-center justify-between pt-4 border-t border-gray-100 dark:border-dark-700"
                  >
                    <div>
                      <label class="font-medium text-gray-900 dark:text-white">{{
                        t("admin.settings.dingtalk.bypassRegistration")
                      }}</label>
                      <p class="text-sm text-gray-500 dark:text-gray-400">
                        {{ t("admin.settings.dingtalk.bypassRegistrationHint") }}
                      </p>
                    </div>
                    <Toggle v-model="form.dingtalk_connect_bypass_registration" />
                  </div>

                  <!-- 身份同步开关（仅 internal_only 模式下可见） -->
                  <div
                    v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                    class="pt-4 border-t border-gray-100 dark:border-dark-700 space-y-2"
                  >
                    <div class="flex items-center justify-between">
                      <div>
                        <label class="font-medium text-gray-900 dark:text-white">{{
                          t("admin.settings.dingtalk.syncDisplayName")
                        }}</label>
                        <p class="text-sm text-gray-500 dark:text-gray-400">
                          {{ t("admin.settings.dingtalk.syncDisplayNameHint") }}
                        </p>
                      </div>
                      <Toggle v-model="form.dingtalk_connect_sync_display_name" />
                    </div>
                    <div v-if="form.dingtalk_connect_sync_display_name" class="space-y-2">
                      <div class="flex items-center gap-2">
                        <label class="text-sm text-gray-600 dark:text-gray-400 whitespace-nowrap min-w-[5rem]">
                          {{ t("admin.settings.dingtalk.syncDisplayNameTarget") }}
                        </label>
                        <input
                          v-model="form.dingtalk_connect_sync_display_name_attr_key"
                          type="text"
                          placeholder="dingtalk_name"
                          class="input text-sm flex-1 max-w-xs"
                        />
                      </div>
                      <div class="flex items-center gap-2">
                        <label class="text-sm text-gray-600 dark:text-gray-400 whitespace-nowrap min-w-[5rem]">
                          {{ t("admin.settings.dingtalk.syncAttrDisplayName") }}
                        </label>
                        <input
                          v-model="form.dingtalk_connect_sync_display_name_attr_name"
                          type="text"
                          placeholder="钉钉姓名"
                          class="input text-sm flex-1 max-w-xs"
                        />
                      </div>
                    </div>
                    <p v-if="form.dingtalk_connect_sync_display_name" class="text-xs text-gray-400 dark:text-gray-500">
                      {{ t("admin.settings.dingtalk.syncDisplayNameTargetHint") }}
                    </p>
                  </div>
                  <div
                    v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                    class="pt-4 border-t border-gray-100 dark:border-dark-700 space-y-2"
                  >
                    <div class="flex items-center justify-between">
                      <div>
                        <label class="font-medium text-gray-900 dark:text-white">{{
                          t("admin.settings.dingtalk.syncCorpEmail")
                        }}</label>
                        <p class="text-sm text-gray-500 dark:text-gray-400">
                          {{ t("admin.settings.dingtalk.syncCorpEmailHint") }}
                        </p>
                        <p class="text-xs text-amber-600 dark:text-amber-400 mt-1">
                          {{ t("admin.settings.dingtalk.syncCorpEmailPermissionHint") }}
                        </p>
                      </div>
                      <Toggle v-model="form.dingtalk_connect_sync_corp_email" />
                    </div>
                    <div v-if="form.dingtalk_connect_sync_corp_email" class="space-y-2">
                      <div class="flex items-center gap-2">
                        <label class="text-sm text-gray-600 dark:text-gray-400 whitespace-nowrap min-w-[5rem]">
                          {{ t("admin.settings.dingtalk.syncCorpEmailTarget") }}
                        </label>
                        <input
                          v-model="form.dingtalk_connect_sync_corp_email_attr_key"
                          type="text"
                          placeholder="dingtalk_email"
                          class="input text-sm flex-1 max-w-xs"
                        />
                      </div>
                      <div class="flex items-center gap-2">
                        <label class="text-sm text-gray-600 dark:text-gray-400 whitespace-nowrap min-w-[5rem]">
                          {{ t("admin.settings.dingtalk.syncAttrDisplayName") }}
                        </label>
                        <input
                          v-model="form.dingtalk_connect_sync_corp_email_attr_name"
                          type="text"
                          placeholder="钉钉企业邮箱"
                          class="input text-sm flex-1 max-w-xs"
                        />
                      </div>
                    </div>
                    <p v-if="form.dingtalk_connect_sync_corp_email" class="text-xs text-gray-400 dark:text-gray-500">
                      {{ t("admin.settings.dingtalk.syncCorpEmailTargetHint") }}
                    </p>
                  </div>
                  <div
                    v-if="form.dingtalk_connect_corp_restriction_policy === 'internal_only'"
                    class="pt-4 border-t border-gray-100 dark:border-dark-700 space-y-2"
                  >
                    <div class="flex items-center justify-between">
                      <div>
                        <label class="font-medium text-gray-900 dark:text-white">{{
                          t("admin.settings.dingtalk.syncDept")
                        }}</label>
                        <p class="text-sm text-gray-500 dark:text-gray-400">
                          {{ t("admin.settings.dingtalk.syncDeptHint") }}
                        </p>
                        <p class="text-xs text-amber-600 dark:text-amber-400 mt-1">
                          {{ t("admin.settings.dingtalk.syncDeptPermissionHint") }}
                        </p>
                      </div>
                      <Toggle v-model="form.dingtalk_connect_sync_dept" />
                    </div>
                    <div v-if="form.dingtalk_connect_sync_dept" class="space-y-2">
                      <div class="flex items-center gap-2">
                        <label class="text-sm text-gray-600 dark:text-gray-400 whitespace-nowrap min-w-[5rem]">
                          {{ t("admin.settings.dingtalk.syncDeptTarget") }}
                        </label>
                        <input
                          v-model="form.dingtalk_connect_sync_dept_attr_key"
                          type="text"
                          placeholder="dingtalk_department"
                          class="input text-sm flex-1 max-w-xs"
                        />
                      </div>
                      <div class="flex items-center gap-2">
                        <label class="text-sm text-gray-600 dark:text-gray-400 whitespace-nowrap min-w-[5rem]">
                          {{ t("admin.settings.dingtalk.syncAttrDisplayName") }}
                        </label>
                        <input
                          v-model="form.dingtalk_connect_sync_dept_attr_name"
                          type="text"
                          placeholder="钉钉部门"
                          class="input text-sm flex-1 max-w-xs"
                        />
                      </div>
                    </div>
                    <p v-if="form.dingtalk_connect_sync_dept" class="text-xs text-gray-400 dark:text-gray-500">
                      {{ t("admin.settings.dingtalk.syncDeptTargetHint") }}
                    </p>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Generic OIDC OAuth 登录 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.oidc.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.oidc.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.oidc.enable")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.oidc.enableHint") }}
                  </p>
                </div>
                <Toggle v-model="form.oidc_connect_enabled" />
              </div>

              <div
                v-if="form.oidc_connect_enabled"
                class="space-y-6 border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.providerName") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_provider_name"
                      type="text"
                      class="input"
                      :placeholder="
                        t('admin.settings.oidc.providerNamePlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.clientId") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_client_id"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.clientIdPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.clientSecret") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_client_secret"
                      type="password"
                      class="input font-mono text-sm"
                      :placeholder="
                        form.oidc_connect_client_secret_configured
                          ? t(
                              'admin.settings.oidc.clientSecretConfiguredPlaceholder',
                            )
                          : t('admin.settings.oidc.clientSecretPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        form.oidc_connect_client_secret_configured
                          ? t("admin.settings.oidc.clientSecretConfiguredHint")
                          : t("admin.settings.oidc.clientSecretHint")
                      }}
                    </p>
                  </div>
                </div>

                <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.issuerUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_issuer_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.issuerUrlPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.discoveryUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_discovery_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.discoveryUrlPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.authorizeUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_authorize_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.authorizeUrlPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.tokenUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_token_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.tokenUrlPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.userinfoUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_userinfo_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.userinfoUrlPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.jwksUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_jwks_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="t('admin.settings.oidc.jwksUrlPlaceholder')"
                    />
                  </div>
                </div>

                <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.scopes") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_scopes"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="t('admin.settings.oidc.scopesPlaceholder')"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.oidc.scopesHint") }}
                    </p>
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.redirectUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_redirect_url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.redirectUrlPlaceholder')
                      "
                    />
                    <div
                      class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3"
                    >
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm w-fit"
                        @click="setAndCopyOIDCRedirectUrl"
                      >
                        {{ t("admin.settings.oidc.quickSetCopy") }}
                      </button>
                      <code
                        v-if="oidcRedirectUrlSuggestion"
                        class="select-all break-all rounded bg-gray-50 px-2 py-1 font-mono text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
                      >
                        {{ oidcRedirectUrlSuggestion }}
                      </code>
                    </div>
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.oidc.redirectUrlHint") }}
                    </p>
                  </div>

                  <div class="lg:col-span-2">
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.frontendRedirectUrl") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_frontend_redirect_url"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.frontendRedirectUrlPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.oidc.frontendRedirectUrlHint") }}
                    </p>
                  </div>
                </div>

                <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.tokenAuthMethod") }}
                    </label>
                    <Select
                      v-model="form.oidc_connect_token_auth_method"
                      :options="oidcTokenAuthMethodOptions"
                      class="font-mono text-sm"
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.clockSkewSeconds") }}
                    </label>
                    <input
                      v-model.number="form.oidc_connect_clock_skew_seconds"
                      type="number"
                      min="0"
                      max="600"
                      class="input"
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.allowedSigningAlgs") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_allowed_signing_algs"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.allowedSigningAlgsPlaceholder')
                      "
                    />
                  </div>
                </div>

                <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                  <div
                    class="flex items-center justify-between rounded border border-gray-200 px-4 py-3 dark:border-dark-700"
                  >
                    <div>
                      <label class="font-medium text-gray-900 dark:text-white">
                        {{ t("admin.settings.oidc.usePkce") }}
                      </label>
                    </div>
                    <Toggle
                      v-model="form.oidc_connect_use_pkce"
                      data-testid="oidc-connect-use-pkce"
                    />
                  </div>

                  <div
                    class="flex items-center justify-between rounded border border-gray-200 px-4 py-3 dark:border-dark-700"
                  >
                    <div>
                      <label class="font-medium text-gray-900 dark:text-white">
                        {{ t("admin.settings.oidc.validateIdToken") }}
                      </label>
                    </div>
                    <Toggle
                      v-model="form.oidc_connect_validate_id_token"
                      data-testid="oidc-connect-validate-id-token"
                    />
                  </div>

                  <div
                    class="flex items-center justify-between rounded border border-gray-200 px-4 py-3 dark:border-dark-700"
                  >
                    <div>
                      <label class="font-medium text-gray-900 dark:text-white">
                        {{ t("admin.settings.oidc.requireEmailVerified") }}
                      </label>
                    </div>
                    <Toggle
                      v-model="form.oidc_connect_require_email_verified"
                    />
                  </div>
                </div>

                <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.userinfoEmailPath") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_userinfo_email_path"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.userinfoEmailPathPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.userinfoIdPath") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_userinfo_id_path"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.userinfoIdPathPlaceholder')
                      "
                    />
                  </div>

                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.oidc.userinfoUsernamePath") }}
                    </label>
                    <input
                      v-model="form.oidc_connect_userinfo_username_path"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.oidc.userinfoUsernamePathPlaceholder')
                      "
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <!-- /Tab: Security — Registration, Turnstile, LinuxDo, OIDC -->

        <!-- Tab: Users -->
        <div v-show="activeTab === 'users'" class="space-y-6">
          <!-- Default Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.defaults.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.defaults.description") }}
              </p>
            </div>
            <div class="space-y-6 p-6">
              <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.defaults.defaultBalance") }}
                  </label>
                  <input
                    v-model.number="form.default_balance"
                    type="number"
                    step="0.01"
                    min="0"
                    class="input"
                    placeholder="0.00"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.defaults.defaultBalanceHint") }}
                  </p>
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.defaults.defaultConcurrency") }}
                  </label>
                  <input
                    v-model.number="form.default_concurrency"
                    type="number"
                    min="1"
                    class="input"
                    placeholder="1"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.defaults.defaultConcurrencyHint") }}
                  </p>
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.defaults.defaultUserRpmLimit") }}
                  </label>
                  <input
                    v-model.number="form.default_user_rpm_limit"
                    type="number"
                    min="0"
                    step="1"
                    class="input"
                    placeholder="0"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.defaults.defaultUserRpmLimitHint") }}
                  </p>
                </div>
              </div>

              <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                <div class="mb-3 flex items-center justify-between">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">
                      {{ t("admin.settings.defaults.defaultSubscriptions") }}
                    </label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{
                        t("admin.settings.defaults.defaultSubscriptionsHint")
                      }}
                    </p>
                  </div>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    @click="addDefaultSubscription"
                    :disabled="subscriptionPlans.length === 0"
                  >
                    {{ t("admin.settings.defaults.addDefaultSubscription") }}
                  </button>
                </div>

                <div
                  v-if="form.default_subscriptions.length === 0"
                  class="rounded border border-dashed border-gray-300 px-4 py-3 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
                >
                  {{ t("admin.settings.defaults.defaultSubscriptionsEmpty") }}
                </div>

                <div v-else class="space-y-3">
                  <div
                    v-for="(item, index) in form.default_subscriptions"
                    :key="`default-sub-${index}`"
                    class="grid grid-cols-1 gap-3 rounded border border-gray-200 p-3 md:grid-cols-[1fr_auto] dark:border-dark-600"
                  >
                    <div>
                      <label
                        class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                      >
                        {{ t("admin.settings.defaults.subscriptionGroup") }}
                      </label>
                      <Select
                        v-model="item.plan_id"
                        class="default-sub-group-select"
                        :options="defaultSubscriptionPlanOptions"
                        :placeholder="
                          t('admin.settings.defaults.subscriptionGroup')
                        "
                      />
                    </div>
                    <div class="flex items-end">
                      <button
                        type="button"
                        class="btn btn-secondary default-sub-delete-btn w-full text-red-600 hover:text-red-700 dark:text-red-400"
                        @click="removeDefaultSubscription(index)"
                      >
                        {{ t("common.delete") }}
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              <!-- ★ 新增：系统全局默认平台限额矩阵 -->
              <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                <div class="mb-3">
                  <label class="font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.defaults.defaultPlatformQuotas") }}
                  </label>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.defaults.defaultPlatformQuotasHint") }}
                  </p>
                  <p class="mt-0.5 text-xs text-amber-600 dark:text-amber-400">
                    {{ t("admin.settings.defaults.platformQuotaNotice") }}
                  </p>
                </div>
                <div class="overflow-x-auto">
                  <table class="min-w-full text-sm">
                    <thead>
                      <tr class="text-left text-xs text-gray-500 dark:text-gray-400">
                        <th class="pb-2 pr-4 font-medium">{{ t("admin.settings.platformQuota.platform") }}</th>
                        <th class="pb-2 pr-4 font-medium">{{ t("admin.settings.platformQuota.daily") }}</th>
                        <th class="pb-2 pr-4 font-medium">{{ t("admin.settings.platformQuota.weekly") }}</th>
                        <th class="pb-2 font-medium">{{ t("admin.settings.platformQuota.monthly") }}</th>
                      </tr>
                    </thead>
                    <tbody class="space-y-2">
                      <tr v-for="p in (['anthropic', 'openai', 'gemini', 'antigravity', 'qoder', 'grok'] as const)" :key="p" class="align-top">
                        <td class="pr-4 py-1">
                          <span class="font-mono text-xs text-gray-700 dark:text-gray-300">{{ p }}</span>
                        </td>
                        <td class="pr-4 py-1">
                          <input
                            v-model.number="form.default_platform_quotas[p]!.daily"
                            type="number"
                            step="0.01"
                            min="0"
                            class="input h-8 w-28 text-sm"
                            :placeholder="t('admin.settings.platformQuota.placeholder')"
                          />
                        </td>
                        <td class="pr-4 py-1">
                          <input
                            v-model.number="form.default_platform_quotas[p]!.weekly"
                            type="number"
                            step="0.01"
                            min="0"
                            class="input h-8 w-28 text-sm"
                            :placeholder="t('admin.settings.platformQuota.placeholder')"
                          />
                        </td>
                        <td class="py-1">
                          <input
                            v-model.number="form.default_platform_quotas[p]!.monthly"
                            type="number"
                            step="0.01"
                            min="0"
                            class="input h-8 w-28 text-sm"
                            :placeholder="t('admin.settings.platformQuota.placeholder')"
                          />
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </div>
              <!-- /全局平台限额矩阵 -->
            </div>
          </div>

          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.authSourceDefaults.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.authSourceDefaults.description") }}
              </p>
            </div>
            <div class="space-y-6 p-6">
              <div
                class="flex items-center justify-between rounded border border-gray-200 px-4 py-3 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.authSourceDefaults.requireEmailLabel") }}
                  </label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.authSourceDefaults.requireEmailHint") }}
                  </p>
                </div>
                <Toggle v-model="form.force_email_on_third_party_signup" />
              </div>

              <div class="space-y-4">
                <div
                  v-for="authSource in authSourceDefaultsMeta"
                  :key="authSource.source"
                  class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"
                >
                  <div class="flex items-center justify-between gap-4">
                    <div>
                      <div class="font-medium text-gray-900 dark:text-white">
                        {{ authSource.title }}
                      </div>
                      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                        {{ authSource.description }}
                      </p>
                    </div>
                    <Toggle
                      v-model="
                        authSourceDefaults[authSource.source].grant_on_signup
                      "
                      :data-testid="`auth-source-${authSource.source}-enabled`"
                    />
                  </div>

                  <div
                    v-if="authSourceDefaults[authSource.source].grant_on_signup"
                    :data-testid="`auth-source-${authSource.source}-panel`"
                    class="mt-4 space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700"
                  >
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.authSourceDefaults.enabledHint") }}
                    </p>

                    <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ t("admin.settings.defaults.defaultBalance") }}
                        </label>
                        <input
                          v-model.number="
                            authSourceDefaults[authSource.source].balance
                          "
                          type="number"
                          step="0.01"
                          min="0"
                          class="input"
                          placeholder="0.00"
                        />
                      </div>
                      <div>
                        <label
                          class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                        >
                          {{ t("admin.settings.defaults.defaultConcurrency") }}
                        </label>
                        <input
                          v-model.number="
                            authSourceDefaults[authSource.source].concurrency
                          "
                          type="number"
                          min="1"
                          class="input"
                          placeholder="5"
                        />
                      </div>
                    </div>

                    <div
                      class="flex items-center justify-between rounded border border-gray-200 px-4 py-3 dark:border-dark-700"
                    >
                      <div>
                        <label
                          class="font-medium text-gray-900 dark:text-white"
                        >
                          {{ t("admin.settings.authSourceDefaults.grantOnFirstBindLabel") }}
                        </label>
                        <p
                          class="mt-0.5 text-xs text-gray-500 dark:text-gray-400"
                        >
                          {{ t("admin.settings.authSourceDefaults.grantOnFirstBindHint") }}
                        </p>
                      </div>
                      <Toggle
                        v-model="
                          authSourceDefaults[authSource.source]
                            .grant_on_first_bind
                        "
                      />
                    </div>

                    <div class="mb-3 flex items-center justify-between">
                      <div>
                        <label
                          class="font-medium text-gray-900 dark:text-white"
                        >
                          {{ t("admin.settings.authSourceDefaults.defaultSubscriptionsLabel") }}
                        </label>
                        <p class="text-sm text-gray-500 dark:text-gray-400">
                          {{ t("admin.settings.authSourceDefaults.defaultSubscriptionsHint") }}
                        </p>
                      </div>
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        @click="
                          addAuthSourceDefaultSubscription(authSource.source)
                        "
                        :disabled="subscriptionPlans.length === 0"
                      >
                        {{
                          t("admin.settings.defaults.addDefaultSubscription")
                        }}
                      </button>
                    </div>

                    <div
                      v-if="
                        authSourceDefaults[authSource.source].subscriptions
                          .length === 0
                      "
                      class="rounded border border-dashed border-gray-300 px-4 py-3 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.authSourceDefaults.noSourceSubscriptions") }}
                    </div>

                    <div v-else class="space-y-3">
                      <div
                        v-for="(item, index) in authSourceDefaults[
                          authSource.source
                        ].subscriptions"
                        :key="`${authSource.source}-sub-${index}`"
                        class="grid grid-cols-1 gap-3 rounded border border-gray-200 p-3 md:grid-cols-[1fr_auto] dark:border-dark-600"
                      >
                        <div>
                          <label
                            class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                          >
                            {{ t("admin.settings.defaults.subscriptionGroup") }}
                          </label>
                          <Select
                            v-model="item.plan_id"
                            class="default-sub-group-select"
                            :options="defaultSubscriptionPlanOptions"
                            :placeholder="
                              t('admin.settings.defaults.subscriptionGroup')
                            "
                          />
                        </div>
                        <div class="flex items-end">
                          <button
                            type="button"
                            class="btn btn-secondary w-full text-red-600 hover:text-red-700 dark:text-red-400"
                            @click="
                              removeAuthSourceDefaultSubscription(
                                authSource.source,
                                index,
                              )
                            "
                          >
                            {{ t("common.delete") }}
                          </button>
                        </div>
                      </div>
                    </div>

                    <!-- ★ 新增：auth source 平台限额覆盖区块 -->
                    <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                      <div class="mb-3">
                        <label class="font-medium text-gray-900 dark:text-white">
                          {{ t("admin.settings.authSourceDefaults.platformQuotasOverride") }}
                        </label>
                        <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                          {{ t("admin.settings.authSourceDefaults.platformQuotasOverrideHint") }}
                        </p>
                      </div>
                      <div class="overflow-x-auto">
                        <table class="min-w-full text-sm">
                          <thead>
                            <tr class="text-left text-xs text-gray-500 dark:text-gray-400">
                              <th class="pb-2 pr-4 font-medium">{{ t("admin.settings.platformQuota.platform") }}</th>
                              <th class="pb-2 pr-4 font-medium">{{ t("admin.settings.platformQuota.daily") }}</th>
                              <th class="pb-2 pr-4 font-medium">{{ t("admin.settings.platformQuota.weekly") }}</th>
                              <th class="pb-2 font-medium">{{ t("admin.settings.platformQuota.monthly") }}</th>
                            </tr>
                          </thead>
                          <tbody>
                            <tr v-for="p in (['anthropic', 'openai', 'gemini', 'antigravity', 'qoder', 'grok'] as const)" :key="`${authSource.source}-pq-${p}`" class="align-top">
                              <td class="pr-4 py-1">
                                <span class="font-mono text-xs text-gray-700 dark:text-gray-300">{{ p }}</span>
                              </td>
                              <td class="pr-4 py-1">
                                <input
                                  v-model.number="authSourceDefaults[authSource.source].platform_quotas[p]!.daily"
                                  type="number"
                                  step="0.01"
                                  min="0"
                                  class="input h-8 w-28 text-sm"
                                  :placeholder="t('admin.settings.platformQuota.placeholder')"
                                />
                              </td>
                              <td class="pr-4 py-1">
                                <input
                                  v-model.number="authSourceDefaults[authSource.source].platform_quotas[p]!.weekly"
                                  type="number"
                                  step="0.01"
                                  min="0"
                                  class="input h-8 w-28 text-sm"
                                  :placeholder="t('admin.settings.platformQuota.placeholder')"
                                />
                              </td>
                              <td class="py-1">
                                <input
                                  v-model.number="authSourceDefaults[authSource.source].platform_quotas[p]!.monthly"
                                  type="number"
                                  step="0.01"
                                  min="0"
                                  class="input h-8 w-28 text-sm"
                                  :placeholder="t('admin.settings.platformQuota.placeholder')"
                                />
                              </td>
                            </tr>
                          </tbody>
                        </table>
                      </div>
                    </div>
                    <!-- /auth source 平台限额覆盖区块 -->
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <!-- /Tab: Users -->

        <!-- Tab: Gateway — Claude Code, Scheduling -->
        <div v-show="activeTab === 'gateway'" class="space-y-6">
          <!-- Claude Code Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.claudeCode.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.claudeCode.description") }}
              </p>
            </div>
            <div class="p-6">
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.claudeCode.minVersion") }}
                </label>
                <input
                  v-model="form.min_claude_code_version"
                  type="text"
                  class="input max-w-xs font-mono text-sm"
                  :placeholder="
                    t('admin.settings.claudeCode.minVersionPlaceholder')
                  "
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.claudeCode.minVersionHint") }}
                </p>
              </div>
              <div class="mt-4">
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.claudeCode.maxVersion") }}
                </label>
                <input
                  v-model="form.max_claude_code_version"
                  type="text"
                  class="input max-w-xs font-mono text-sm"
                  :placeholder="
                    t('admin.settings.claudeCode.maxVersionPlaceholder')
                  "
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.claudeCode.maxVersionHint") }}
                </p>
              </div>
            </div>
          </div>

          <!-- Gateway Scheduling Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.scheduling.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.scheduling.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.scheduling.allowUngroupedKey") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.scheduling.allowUngroupedKeyHint") }}
                  </p>
                </div>
                <Toggle v-model="form.allow_ungrouped_key_scheduling" />
              </div>

              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.openaiExperimentalScheduler.title") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.openaiExperimentalScheduler.description")
                    }}
                  </p>
                </div>
                <Toggle v-model="form.openai_advanced_scheduler_enabled" />
              </div>

              <div
                v-if="form.openai_advanced_scheduler_enabled"
                class="flex items-center justify-between border-t border-gray-100 pt-5 dark:border-dark-700"
              >
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.openaiExperimentalScheduler.stickyWeightedTitle") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.openaiExperimentalScheduler.stickyWeightedDescription")
                    }}
                  </p>
                </div>
                <Toggle v-model="form.openai_advanced_scheduler_sticky_weighted_enabled" />
              </div>

              <div
                v-if="form.openai_advanced_scheduler_enabled"
                class="flex items-center justify-between border-t border-gray-100 pt-5 dark:border-dark-700"
              >
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.openaiExperimentalScheduler.subscriptionPriorityTitle") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.openaiExperimentalScheduler.subscriptionPriorityDescription")
                    }}
                  </p>
                </div>
                <Toggle v-model="form.openai_advanced_scheduler_subscription_priority_enabled" />
              </div>

              <div
                v-if="form.openai_advanced_scheduler_enabled"
                class="border-t border-gray-100 pt-5 dark:border-dark-700"
              >
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.openaiExperimentalScheduler.weightsTitle") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.openaiExperimentalScheduler.weightsDescription")
                    }}
                  </p>
                </div>

                <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
                  <label
                    v-for="field in openAIAdvancedSchedulerWeightFields"
                    :key="field.key"
                    class="block"
                  >
                    <span class="text-xs font-medium text-gray-600 dark:text-gray-400">
                      {{ field.label }}
                    </span>
                    <input
                      v-model="form[field.key]"
                      class="input mt-1"
                      inputmode="decimal"
                      :placeholder="field.placeholder"
                      type="text"
                    />
                  </label>
                </div>
              </div>

              <div class="border-t border-gray-100 pt-5 dark:border-dark-700">
                <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
                  {{ t("admin.settings.openaiQuotaAutoPause.title") }}
                </h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.openaiQuotaAutoPause.description") }}
                </p>
                <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2">
                  <div>
                    <label class="input-label">
                      {{ t("admin.settings.openaiQuotaAutoPause.default5h") }}
                    </label>
                    <input
                      v-model.number="openAIQuotaAutoPause5hPercent"
                      type="number"
                      min="0"
                      max="100"
                      step="0.1"
                      class="input"
                      data-testid="settings-openai-quota-auto-pause-5h"
                    />
                  </div>
                  <div>
                    <label class="input-label">
                      {{ t("admin.settings.openaiQuotaAutoPause.default7d") }}
                    </label>
                    <input
                      v-model.number="openAIQuotaAutoPause7dPercent"
                      type="number"
                      min="0"
                      max="100"
                      step="0.1"
                      class="input"
                      data-testid="settings-openai-quota-auto-pause-7d"
                    />
                  </div>
                </div>
                <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.openaiQuotaAutoPause.thresholdHint") }}
                </p>
              </div>
            </div>
          </div>

          <!-- Gateway Forwarding Behavior -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.gatewayForwarding.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.gatewayForwarding.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Fingerprint Unification -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.fingerprintUnification",
                      )
                    }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.fingerprintUnificationHint",
                      )
                    }}
                  </p>
                </div>
                <Toggle v-model="form.enable_fingerprint_unification" />
              </div>

              <!-- Metadata Passthrough -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t("admin.settings.gatewayForwarding.metadataPassthrough")
                    }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.metadataPassthroughHint",
                      )
                    }}
                  </p>
                </div>
                <Toggle v-model="form.enable_metadata_passthrough" />
              </div>

              <!-- CCH Signing -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.gatewayForwarding.cchSigning") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.gatewayForwarding.cchSigningHint") }}
                  </p>
                </div>
                <Toggle v-model="form.enable_cch_signing" />
              </div>

              <!-- Claude OAuth System 注入 -->
              <div class="flex items-center justify-between gap-4">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.claudeOAuthSystemPromptInjection",
                      )
                    }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.claudeOAuthSystemPromptInjectionHint",
                      )
                    }}
                  </p>
                </div>
                <Toggle
                  v-model="form.enable_claude_oauth_system_prompt_injection"
                />
              </div>

              <div class="grid gap-5 md:grid-cols-2">
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    for="claude-oauth-system-prompt"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.claudeOAuthSystemPrompt",
                      )
                    }}
                  </label>
                  <textarea
                    id="claude-oauth-system-prompt"
                    v-model="form.claude_oauth_system_prompt"
                    rows="6"
                    class="input min-h-32 font-mono text-sm"
                    :placeholder="
                      t(
                        'admin.settings.gatewayForwarding.claudeOAuthSystemPromptPlaceholder',
                      )
                    "
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.claudeOAuthSystemPromptHint",
                      )
                    }}
                  </p>
                </div>

                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    for="claude-oauth-system-prompt-blocks"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.claudeOAuthSystemPromptBlocks",
                      )
                    }}
                  </label>
                  <textarea
                    id="claude-oauth-system-prompt-blocks"
                    v-model="form.claude_oauth_system_prompt_blocks"
                    rows="6"
                    class="input min-h-32 font-mono text-sm"
                    :placeholder="
                      t(
                        'admin.settings.gatewayForwarding.claudeOAuthSystemPromptBlocksPlaceholder',
                      )
                    "
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.claudeOAuthSystemPromptBlocksHint",
                      )
                    }}
                  </p>
                </div>
              </div>

              <!-- Anthropic Cache TTL 1h Injection -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.anthropicCacheTTL1hInjection",
                      )
                    }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.anthropicCacheTTL1hInjectionHint",
                      )
                    }}
                  </p>
                </div>
                <Toggle
                  v-model="form.enable_anthropic_cache_ttl_1h_injection"
                />
              </div>

              <!-- messages cache_control 改写 -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.rewriteMessageCacheControl",
                      )
                    }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.rewriteMessageCacheControlHint",
                      )
                    }}
                  </p>
                </div>
                <Toggle v-model="form.rewrite_message_cache_control" />
              </div>

              <!-- 客户端 dateline 归一化（仅 Anthropic OAuth/SetupToken） -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t(
                        "admin.settings.gatewayForwarding.clientDatelineNormalization",
                      )
                    }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t(
                        "admin.settings.gatewayForwarding.clientDatelineNormalizationHint",
                      )
                    }}
                  </p>
                </div>
                <Toggle
                  v-model="form.enable_client_dateline_normalization"
                />
              </div>

              <!-- Antigravity UA 版本 -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{
                    t(
                      "admin.settings.gatewayForwarding.antigravityUserAgentVersion",
                    )
                  }}
                </label>
                <input
                  v-model="form.antigravity_user_agent_version"
                  type="text"
                  class="input max-w-xs font-mono text-sm"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.antigravityUserAgentVersionPlaceholder',
                    )
                  "
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{
                    t(
                      "admin.settings.gatewayForwarding.antigravityUserAgentVersionHint",
                    )
                  }}
                </p>
              </div>

              <!-- OpenAI Codex UA 设置 -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{
                    t(
                      "admin.settings.gatewayForwarding.openaiCodexUserAgent",
                    )
                  }}
                </label>
                <input
                  v-model="form.openai_codex_user_agent"
                  type="text"
                  class="input w-full font-mono text-sm"
                  :placeholder="
                    t(
                      'admin.settings.gatewayForwarding.openaiCodexUserAgentPlaceholder',
                    )
                  "
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{
                    t(
                      "admin.settings.gatewayForwarding.openaiCodexUserAgentHint",
                    )
                  }}
                </p>
              </div>

              <!-- 是否允许在 Claude Code 中使用 Codex 插件（全局开关） -->
              <div class="flex items-center justify-between">
                <div class="pr-4">
                  <label class="block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t("admin.settings.gatewayForwarding.openaiAllowClaudeCodeCodexPlugin") }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.gatewayForwarding.openaiAllowClaudeCodeCodexPluginDesc") }}
                  </p>
                </div>
                <Toggle v-model="form.openai_allow_claude_code_codex_plugin" />
              </div>
            </div>
          </div>
          <!-- 用户提示词替换 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <div>
                  <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                    {{ t("admin.settings.userPromptReplacement.title") }}
                  </h2>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.userPromptReplacement.description") }}
                  </p>
                </div>
                <Toggle v-model="form.user_prompt_replacement_config.enabled" />
              </div>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  @click="addUserPromptReplacementRule"
                >
                  {{ t("admin.settings.userPromptReplacement.addRule") }}
                </button>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  @click="resetUserPromptReplacementRules"
                >
                  {{ t("admin.settings.userPromptReplacement.resetDefault") }}
                </button>
              </div>

              <div
                v-if="form.user_prompt_replacement_config.rules.length === 0"
                class="rounded-lg border border-dashed border-gray-300 p-4 text-center text-sm text-gray-400 dark:border-dark-600"
              >
                {{ t("admin.settings.userPromptReplacement.empty") }}
              </div>

              <div
                v-for="(rule, index) in form.user_prompt_replacement_config.rules"
                :key="rule.id || index"
                class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div class="flex min-w-0 flex-1 items-center gap-3">
                    <Toggle v-model="rule.enabled" />
                    <input
                      v-model="rule.name"
                      type="text"
                      class="input min-w-0 flex-1 text-sm"
                      :placeholder="
                        t('admin.settings.userPromptReplacement.namePlaceholder')
                      "
                    />
                  </div>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm text-red-600 hover:text-red-700 dark:text-red-400"
                    @click="removeUserPromptReplacementRule(index)"
                  >
                    {{ t("common.delete") }}
                  </button>
                </div>

                <div class="grid gap-4 lg:grid-cols-6">
                  <div class="lg:col-span-4">
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.userPromptReplacement.pattern") }}
                    </label>
                    <textarea
                      v-model="rule.pattern"
                      rows="3"
                      class="input font-mono text-xs"
                      :placeholder="
                        t('admin.settings.userPromptReplacement.patternPlaceholder')
                      "
                    />
                  </div>
                  <div>
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.userPromptReplacement.targetGroup") }}
                    </label>
                    <input
                      v-model.number="rule.target_group"
                      type="number"
                      min="0"
                      step="1"
                      class="input text-sm"
                    />
                  </div>
                  <div>
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.userPromptReplacement.replacementType") }}
                    </label>
                    <Select
                      v-model="rule.replacement_type"
                      :options="userPromptReplacementTypeOptions"
                      class="text-sm"
                    />
                  </div>
                </div>

                <div class="mt-4 grid gap-4 md:grid-cols-3">
                  <div v-if="rule.replacement_type === 'static'">
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.userPromptReplacement.staticText") }}
                    </label>
                    <input
                      v-model="rule.static_text"
                      type="text"
                      class="input text-sm"
                      :placeholder="
                        t('admin.settings.userPromptReplacement.staticTextPlaceholder')
                      "
                    />
                  </div>
                  <div v-if="rule.replacement_type !== 'static'">
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.userPromptReplacement.timezone") }}
                    </label>
                    <Select
                      v-model="rule.timezone"
                      :options="userPromptReplacementTimezoneOptions"
                      searchable
                      creatable
                      :creatable-prefix="
                        t('admin.settings.userPromptReplacement.useTimezone')
                      "
                      class="text-sm"
                    />
                  </div>
                  <div v-if="rule.replacement_type === 'current_time'">
                    <label class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.userPromptReplacement.timeFormat") }}
                    </label>
                    <input
                      v-model="rule.time_format"
                      type="text"
                      class="input font-mono text-sm"
                      placeholder="2006-01-02"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
          <!-- Web Search Emulation -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.webSearchEmulation.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.webSearchEmulation.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <!-- Global Toggle -->
              <div class="flex items-center justify-between">
                <div>
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.webSearchEmulation.enabled") }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.webSearchEmulation.enabledHint") }}
                  </p>
                </div>
                <Toggle v-model="webSearchConfig.enabled" />
              </div>

              <!-- Providers -->
              <div v-if="webSearchConfig.enabled" class="space-y-4">
                <div class="flex items-center justify-between">
                  <label
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.webSearchEmulation.providers") }}
                  </label>
                  <button
                    type="button"
                    class="btn btn-secondary btn-sm"
                    @click="addWebSearchProvider"
                  >
                    {{ t("admin.settings.webSearchEmulation.addProvider") }}
                  </button>
                </div>

                <div
                  v-if="webSearchConfig.providers.length === 0"
                  class="rounded-lg border border-dashed border-gray-300 p-4 text-center text-sm text-gray-400 dark:border-dark-600"
                >
                  {{ t("admin.settings.webSearchEmulation.noProviders") }}
                </div>

                <div
                  v-for="(provider, pIdx) in webSearchConfig.providers"
                  :key="pIdx"
                  class="rounded-lg border border-gray-200 dark:border-dark-600"
                >
                  <!-- Collapsible header -->
                  <div
                    class="flex cursor-pointer items-center justify-between px-4 py-3"
                    @click="toggleProviderExpand(pIdx)"
                  >
                    <div class="flex items-center gap-3">
                      <svg
                        class="h-4 w-4 text-gray-400 transition-transform"
                        :class="{ 'rotate-90': expandedProviders[pIdx] }"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          stroke-width="2"
                          d="M9 5l7 7-7 7"
                        />
                      </svg>
                      <Select
                        v-model="provider.type"
                        :options="[
                          { value: 'brave', label: 'Brave Search' },
                          { value: 'tavily', label: 'Tavily' },
                        ]"
                        class="w-36"
                        @click.stop
                      />
                      <!-- Quota summary (always visible) -->
                      <span class="text-xs text-gray-400">
                        {{ provider.quota_used ?? 0 }} /
                        {{
                          provider.quota_limit != null &&
                          provider.quota_limit > 0
                            ? provider.quota_limit
                            : "∞"
                        }}
                      </span>
                      <span
                        v-if="
                          !expandedProviders[pIdx] &&
                          provider.api_key_configured
                        "
                        class="text-xs text-green-500"
                      >
                        {{
                          t(
                            "admin.settings.webSearchEmulation.apiKeyConfigured",
                          )
                        }}
                      </span>
                    </div>
                    <button
                      type="button"
                      class="text-red-500 hover:text-red-700 text-xs"
                      @click.stop="removeWebSearchProvider(pIdx)"
                    >
                      {{
                        t("admin.settings.webSearchEmulation.removeProvider")
                      }}
                    </button>
                  </div>

                  <!-- Expanded content -->
                  <div
                    v-if="expandedProviders[pIdx]"
                    class="space-y-3 border-t border-gray-100 px-4 pb-4 pt-3 dark:border-dark-700"
                  >
                    <!-- API Key with inline show/copy -->
                    <div>
                      <label class="text-xs text-gray-500">{{
                        t("admin.settings.webSearchEmulation.apiKey")
                      }}</label>
                      <div class="relative">
                        <input
                          v-model="provider.api_key"
                          :type="apiKeyVisible[pIdx] ? 'text' : 'password'"
                          class="input w-full text-sm"
                          :class="
                            provider.api_key || provider.api_key_configured
                              ? 'pr-16'
                              : ''
                          "
                          :placeholder="
                            provider.api_key_configured
                              ? '••••••••'
                              : t(
                                  'admin.settings.webSearchEmulation.apiKeyPlaceholder',
                                )
                          "
                        />
                        <div
                          v-if="provider.api_key || provider.api_key_configured"
                          class="absolute inset-y-0 right-0 flex items-center pr-1.5"
                        >
                          <button
                            type="button"
                            class="rounded p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                            :title="
                              apiKeyVisible[pIdx]
                                ? t(
                                    'admin.settings.webSearchEmulation.hideApiKey',
                                  )
                                : t(
                                    'admin.settings.webSearchEmulation.showApiKey',
                                  )
                            "
                            @click="apiKeyVisible[pIdx] = !apiKeyVisible[pIdx]"
                          >
                            <svg
                              v-if="!apiKeyVisible[pIdx]"
                              class="h-4 w-4"
                              fill="none"
                              viewBox="0 0 24 24"
                              stroke="currentColor"
                            >
                              <path
                                stroke-linecap="round"
                                stroke-linejoin="round"
                                stroke-width="2"
                                d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
                              />
                              <path
                                stroke-linecap="round"
                                stroke-linejoin="round"
                                stroke-width="2"
                                d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"
                              />
                            </svg>
                            <svg
                              v-else
                              class="h-4 w-4"
                              fill="none"
                              viewBox="0 0 24 24"
                              stroke="currentColor"
                            >
                              <path
                                stroke-linecap="round"
                                stroke-linejoin="round"
                                stroke-width="2"
                                d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.878 9.878L3 3m6.878 6.878L21 21"
                              />
                            </svg>
                          </button>
                          <button
                            type="button"
                            class="rounded p-1 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                            :class="{
                              'opacity-30 cursor-not-allowed':
                                !provider.api_key,
                            }"
                            :title="
                              t('admin.settings.webSearchEmulation.copyApiKey')
                            "
                            :disabled="!provider.api_key"
                            @click="copyApiKey(pIdx)"
                          >
                            <svg
                              class="h-4 w-4"
                              fill="none"
                              viewBox="0 0 24 24"
                              stroke="currentColor"
                            >
                              <path
                                stroke-linecap="round"
                                stroke-linejoin="round"
                                stroke-width="2"
                                d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"
                              />
                            </svg>
                          </button>
                        </div>
                      </div>
                    </div>

                    <!-- Quota + Subscription in compact row -->
                    <div class="grid grid-cols-2 gap-3">
                      <div>
                        <label class="text-xs text-gray-500">{{
                          t("admin.settings.webSearchEmulation.quotaLimit")
                        }}</label>
                        <input
                          v-model="provider.quota_limit"
                          type="number"
                          min="1"
                          class="input text-sm"
                          :placeholder="'∞'"
                        />
                        <p class="mt-0.5 text-xs text-gray-400">
                          {{
                            t(
                              "admin.settings.webSearchEmulation.quotaLimitHint",
                            )
                          }}
                        </p>
                      </div>
                      <div>
                        <label class="text-xs text-gray-500">{{
                          t("admin.settings.webSearchEmulation.subscribedAt")
                        }}</label>
                        <input
                          :value="formatSubscribedAt(provider.subscribed_at)"
                          type="date"
                          class="input text-sm"
                          @input="
                            provider.subscribed_at = parseSubscribedAt(
                              ($event.target as HTMLInputElement).value,
                            )
                          "
                        />
                        <p class="mt-0.5 text-xs text-gray-400">
                          {{
                            t(
                              "admin.settings.webSearchEmulation.subscribedAtHint",
                            )
                          }}
                        </p>
                      </div>
                    </div>

                    <!-- Usage display -->
                    <div class="flex items-center gap-2">
                      <span class="text-xs text-gray-500"
                        >{{
                          t("admin.settings.webSearchEmulation.quotaUsage")
                        }}:</span
                      >
                      <div
                        v-if="
                          provider.quota_limit != null &&
                          provider.quota_limit > 0
                        "
                        class="flex-1 rounded-full bg-gray-200 dark:bg-dark-600"
                        style="height: 6px"
                      >
                        <div
                          class="h-full rounded-full transition-all"
                          :class="
                            quotaPercentage(provider) > 90
                              ? 'bg-red-500'
                              : quotaPercentage(provider) > 70
                                ? 'bg-yellow-500'
                                : 'bg-green-500'
                          "
                          :style="{
                            width:
                              Math.min(quotaPercentage(provider), 100) + '%',
                          }"
                        />
                      </div>
                      <div v-else class="flex-1" />
                      <span class="text-xs text-gray-500"
                        >{{ provider.quota_used ?? 0 }} /
                        {{
                          provider.quota_limit != null &&
                          provider.quota_limit > 0
                            ? provider.quota_limit
                            : "∞"
                        }}</span
                      >
                      <button
                        v-if="(provider.quota_used ?? 0) > 0"
                        type="button"
                        class="text-xs text-primary-600 hover:text-primary-700"
                        @click="resetWebSearchUsage(pIdx)"
                      >
                        {{ t("admin.settings.webSearchEmulation.resetUsage") }}
                      </button>
                    </div>

                    <!-- Proxy + Test on same row -->
                    <div class="flex items-end gap-3">
                      <div class="flex-1">
                        <label class="text-xs text-gray-500">{{
                          t("admin.settings.webSearchEmulation.proxy")
                        }}</label>
                        <ProxySelector
                          v-model="provider.proxy_id"
                          :proxies="webSearchProxies"
                        />
                      </div>
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm whitespace-nowrap"
                        @click="openTestDialog()"
                      >
                        {{ t("admin.settings.webSearchEmulation.test") }}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Web Search Test Dialog -->
          <div
            v-if="wsTestDialogOpen"
            class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
            @click.self="wsTestDialogOpen = false"
          >
            <div
              class="mx-4 w-full max-w-lg rounded-xl bg-white p-6 shadow-xl dark:bg-dark-800"
            >
              <h3
                class="mb-4 text-lg font-semibold text-gray-900 dark:text-white"
              >
                {{ t("admin.settings.webSearchEmulation.testResultTitle") }}
              </h3>
              <div class="flex items-center gap-2">
                <input
                  v-model="wsTestQuery"
                  type="text"
                  class="input flex-1 text-sm"
                  :placeholder="
                    t('admin.settings.webSearchEmulation.testDefaultQuery')
                  "
                  @keyup.enter="testWebSearchProvider()"
                />
                <button
                  type="button"
                  class="btn btn-primary btn-sm"
                  :disabled="wsTestLoading"
                  @click="testWebSearchProvider()"
                >
                  {{
                    wsTestLoading
                      ? t("admin.settings.webSearchEmulation.testing")
                      : t("admin.settings.webSearchEmulation.test")
                  }}
                </button>
              </div>
              <!-- Test results -->
              <div
                v-if="wsTestResult"
                class="mt-4 max-h-80 overflow-y-auto rounded-lg bg-gray-50 p-4 dark:bg-dark-700"
              >
                <p
                  class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{
                    t("admin.settings.webSearchEmulation.testResultProvider")
                  }}: {{ wsTestResult.provider }}
                </p>
                <div
                  v-if="wsTestResult.results.length === 0"
                  class="text-sm text-gray-400"
                >
                  {{ t("admin.settings.webSearchEmulation.testNoResults") }}
                </div>
                <div
                  v-for="(r, rIdx) in wsTestResult.results"
                  :key="rIdx"
                  class="mt-2 border-t border-gray-200 pt-2 first:mt-0 first:border-0 first:pt-0 dark:border-dark-600"
                >
                  <a
                    :href="r.url"
                    target="_blank"
                    class="text-sm font-medium text-blue-600 hover:underline dark:text-blue-400"
                    >{{ r.title }}</a
                  >
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ r.snippet }}
                  </p>
                </div>
              </div>
              <div class="mt-4 flex justify-end">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  @click="wsTestDialogOpen = false"
                >
                  {{ t("common.close") }}
                </button>
              </div>
            </div>
          </div>

        <!-- 用量记录设置 -->
        <div class="card">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('admin.settings.usageRecords.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.usageRecords.description') }}
            </p>
          </div>
          <div class="space-y-4 p-6">
            <!-- 用户错误请求可见性 -->
            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.settings.user_error_view.label') }}
                </label>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.user_error_view.description') }}
                </p>
              </div>
              <label class="toggle">
                <input v-model="form.allow_user_view_error_requests" type="checkbox" />
                <span class="toggle-slider"></span>
              </label>
            </div>
          </div>
        </div>
        </div>
        <!-- /Tab: Gateway — Claude Code, Scheduling -->

        <!-- Tab: General -->
        <div v-show="activeTab === 'general'" class="space-y-6">
          <!-- Balance Display Settings -->
          <div class="card">
            <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t('admin.settings.balanceDisplay.title') }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.balanceDisplay.description') }}
              </p>
            </div>
            <div class="grid grid-cols-1 gap-6 p-6 lg:grid-cols-[minmax(0,1fr)_280px]">
              <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.settings.balanceDisplay.unitName') }}
                  </label>
                  <input
                    v-model="form.balance_unit_name"
                    type="text"
                    class="input"
                    :placeholder="t('admin.settings.balanceDisplay.unitNamePlaceholder')"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.balanceDisplay.unitNameHint') }}
                  </p>
                </div>
                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.settings.balanceDisplay.unitSymbol') }}
                  </label>
                  <input
                    v-model="form.balance_unit_symbol"
                    type="text"
                    class="input"
                    :placeholder="t('admin.settings.balanceDisplay.unitSymbolPlaceholder')"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.balanceDisplay.unitSymbolHint') }}
                  </p>
                </div>
                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.settings.balanceDisplay.reasoningPointRmbUnitPrice') }}
                  </label>
                  <input
                    v-model.number="form.reasoning_point_rmb_unit_price"
                    type="number"
                    min="0"
                    step="0.0001"
                    class="input"
                    :placeholder="t('admin.settings.balanceDisplay.reasoningPointRmbUnitPricePlaceholder')"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.balanceDisplay.reasoningPointRmbUnitPriceHint') }}
                  </p>
                </div>
                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.settings.balanceDisplay.usdExchangeRate') }}
                  </label>
                  <input
                    v-model.number="form.usd_exchange_rate"
                    type="number"
                    min="0"
                    step="0.0001"
                    class="input"
                    :placeholder="t('admin.settings.balanceDisplay.usdExchangeRatePlaceholder')"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.balanceDisplay.usdExchangeRateHint') }}
                  </p>
                </div>
                <div class="md:col-span-2">
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.settings.balanceDisplay.iconSvg') }}
                  </label>
                  <ImageUpload
                    v-model="form.balance_icon_svg"
                    mode="svg"
                    :upload-label="t('admin.settings.balanceDisplay.uploadSvg')"
                    :remove-label="t('admin.settings.balanceDisplay.removeSvg')"
                    :hint="t('admin.settings.balanceDisplay.iconHint')"
                  />
                </div>
              </div>

              <div class="rounded-2xl border border-primary-100 bg-primary-50/60 p-5 dark:border-primary-900/40 dark:bg-primary-900/10">
                <p class="text-xs font-medium uppercase tracking-[0.18em] text-primary-600 dark:text-primary-300">
                  {{ t('admin.settings.balanceDisplay.previewLabel') }}
                </p>
                <div class="mt-4 rounded-2xl bg-white p-4 shadow-sm dark:bg-dark-800">
                  <div class="flex items-center gap-3">
                    <div class="flex h-11 w-11 items-center justify-center rounded-2xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
                      <BalanceIcon
                        :svg="form.balance_icon_svg"
                        :use-global-fallback="false"
                        class="h-5 w-5"
                      />
                    </div>
                    <div>
                      <p class="text-xs text-gray-500 dark:text-gray-400">
                        {{ t('admin.settings.balanceDisplay.previewUnit', { unitName: previewBalanceUnitName }) }}
                      </p>
                      <p class="text-xl font-semibold text-gray-900 dark:text-white">
                        {{ previewBalanceAmount }}
                      </p>
                    </div>
                  </div>
                  <p class="mt-4 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.settings.balanceDisplay.previewHint') }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div class="card">
            <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.marketplaceAvailability.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.marketplaceAvailability.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                <div>
                  <label class="input-label">
                    {{ t("admin.settings.marketplaceAvailability.windowDays") }}
                  </label>
                  <input
                    v-model.number="form.marketplace_availability_window_days"
                    type="number"
                    :min="marketplaceAvailabilityWindowDaysMin"
                    :max="marketplaceAvailabilityWindowDaysMax"
                    step="1"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.marketplaceAvailability.windowDaysHint", {
                        min: marketplaceAvailabilityWindowDaysMin,
                        max: marketplaceAvailabilityWindowDaysMax,
                      })
                    }}
                  </p>
                </div>

                <div>
                  <label class="input-label">
                    {{ t("admin.settings.marketplaceAvailability.bucketMinutes") }}
                  </label>
                  <input
                    v-model.number="form.marketplace_availability_bucket_minutes"
                    type="number"
                    :min="marketplaceAvailabilityBucketMinutesMin"
                    :max="marketplaceAvailabilityBucketMinutesMax"
                    step="1"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.settings.marketplaceAvailability.bucketMinutesHint", {
                        min: marketplaceAvailabilityBucketMinutesMin,
                        max: marketplaceAvailabilityBucketMinutesMax,
                      })
                    }}
                  </p>
                </div>
              </div>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.marketplaceAvailability.bucketLimitHint") }}
              </p>
            </div>
          </div>

          <!-- Site Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.site.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.site.description") }}
              </p>
            </div>
            <div class="space-y-6 p-6">
              <!-- Backend Mode -->
              <div
                class="flex items-center justify-between rounded-lg border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-900/20"
              >
                <div>
                  <h3 class="text-sm font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.site.backendMode") }}
                  </h3>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.site.backendModeDescription") }}
                  </p>
                </div>
                <Toggle v-model="form.backend_mode_enabled" />
              </div>

              <!-- 站点文案 -->
              <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                <h3 class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t("admin.settings.site.siteCopyTitle") }}
                </h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.siteCopyDescription") }}
                </p>
                <div class="mt-4 space-y-6">
                  <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                    <div>
                      <label
                        class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{ t("admin.settings.site.siteNameZh") }}
                      </label>
                      <input
                        v-model="form.site_name_zh"
                        type="text"
                        class="input"
                        :placeholder="t('admin.settings.site.siteNameZhPlaceholder')"
                      />
                    </div>
                    <div>
                      <label
                        class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{ t("admin.settings.site.siteNameEn") }}
                      </label>
                      <input
                        v-model="form.site_name_en"
                        type="text"
                        class="input"
                        :placeholder="t('admin.settings.site.siteNameEnPlaceholder')"
                      />
                    </div>
                  </div>
                  <div>
                    <p class="text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.site.siteNameHint") }}
                    </p>
                  </div>

                  <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                    <div>
                      <label
                        class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{ t("admin.settings.site.siteTitleZh") }}
                      </label>
                      <input
                        v-model="form.site_title_zh"
                        type="text"
                        class="input"
                        :placeholder="t('admin.settings.site.siteTitleZhPlaceholder')"
                      />
                    </div>
                    <div>
                      <label
                        class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{ t("admin.settings.site.siteTitleEn") }}
                      </label>
                      <input
                        v-model="form.site_title_en"
                        type="text"
                        class="input"
                        :placeholder="t('admin.settings.site.siteTitleEnPlaceholder')"
                      />
                    </div>
                  </div>
                  <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                    <div>
                      <label
                        class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{ t("admin.settings.site.siteSubtitleZh") }}
                      </label>
                      <textarea
                        v-model="form.site_subtitle_zh"
                        rows="2"
                        class="input min-h-[72px] resize-y"
                        :placeholder="t('admin.settings.site.siteSubtitleZhPlaceholder')"
                      ></textarea>
                    </div>
                    <div>
                      <label
                        class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{ t("admin.settings.site.siteSubtitleEn") }}
                      </label>
                      <textarea
                        v-model="form.site_subtitle_en"
                        rows="2"
                        class="input min-h-[72px] resize-y"
                        :placeholder="t('admin.settings.site.siteSubtitleEnPlaceholder')"
                      ></textarea>
                    </div>
                  </div>
                </div>
              </div>

              <!-- API Base URL -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.site.apiBaseUrl") }}
                </label>
                <input
                  v-model="form.api_base_url"
                  type="text"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.site.apiBaseUrlPlaceholder')"
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.apiBaseUrlHint") }}
                </p>
              </div>

              <!-- Global Table Preferences -->
              <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                <h3 class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ t("admin.settings.site.tablePreferencesTitle") }}
                </h3>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.tablePreferencesDescription") }}
                </p>
                <div class="mt-4 grid grid-cols-1 gap-6 md:grid-cols-3">
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.site.tableDefaultPageSize") }}
                    </label>
                    <input
                      v-model.number="form.table_default_page_size"
                      type="number"
                      min="5"
                      max="1000"
                      step="1"
                      class="input w-40"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.site.tableDefaultPageSizeHint") }}
                    </p>
                  </div>
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.site.tablePageSizeOptions") }}
                    </label>
                    <input
                      v-model="tablePageSizeOptionsInput"
                      type="text"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.site.tablePageSizeOptionsPlaceholder')
                      "
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.site.tablePageSizeOptionsHint") }}
                    </p>
                  </div>
                  <div>
                    <label
                      class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                    >
                      {{ t("admin.settings.site.usageRankingLimit") }}
                    </label>
                    <input
                      v-model.number="form.usage_ranking_limit"
                      type="number"
                      min="1"
                      max="100"
                      step="1"
                      class="input w-40"
                    />
                    <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.site.usageRankingLimitHint") }}
                    </p>
                  </div>
                </div>
              </div>

              <!-- Custom Endpoints -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.site.customEndpoints.title") }}
                </label>
                <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.customEndpoints.description") }}
                </p>

                <div class="space-y-3">
                  <div
                    v-for="(ep, index) in form.custom_endpoints"
                    :key="index"
                    class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
                  >
                    <div class="mb-3 flex items-center justify-between">
                      <span
                        class="text-sm font-medium text-gray-700 dark:text-gray-300"
                      >
                        {{
                          t("admin.settings.site.customEndpoints.itemLabel", {
                            n: index + 1,
                          })
                        }}
                      </span>
                      <button
                        type="button"
                        class="rounded p-1 text-red-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                        @click="removeEndpoint(index)"
                      >
                        <svg
                          class="h-4 w-4"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                          stroke-width="2"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
                          />
                        </svg>
                      </button>
                    </div>
                    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <div>
                        <label
                          class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                        >
                          {{ t("admin.settings.site.customEndpoints.name") }}
                        </label>
                        <input
                          v-model="ep.name"
                          type="text"
                          class="input text-sm"
                          :placeholder="
                            t(
                              'admin.settings.site.customEndpoints.namePlaceholder',
                            )
                          "
                        />
                      </div>
                      <div>
                        <label
                          class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                        >
                          {{
                            t("admin.settings.site.customEndpoints.endpointUrl")
                          }}
                        </label>
                        <input
                          v-model="ep.endpoint"
                          type="url"
                          class="input font-mono text-sm"
                          :placeholder="
                            t(
                              'admin.settings.site.customEndpoints.endpointUrlPlaceholder',
                            )
                          "
                        />
                      </div>
                      <div class="sm:col-span-2">
                        <label
                          class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                        >
                          {{
                            t(
                              "admin.settings.site.customEndpoints.descriptionLabel",
                            )
                          }}
                        </label>
                        <input
                          v-model="ep.description"
                          type="text"
                          class="input text-sm"
                          :placeholder="
                            t(
                              'admin.settings.site.customEndpoints.descriptionPlaceholder',
                            )
                          "
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <button
                  type="button"
                  class="mt-3 flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed border-gray-300 px-4 py-2.5 text-sm text-gray-500 transition-colors hover:border-primary-400 hover:text-primary-600 dark:border-dark-600 dark:text-gray-400 dark:hover:border-primary-500 dark:hover:text-primary-400"
                  @click="addEndpoint"
                >
                  <svg
                    class="h-4 w-4"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    stroke-width="2"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      d="M12 4v16m8-8H4"
                    />
                  </svg>
                  {{ t("admin.settings.site.customEndpoints.add") }}
                </button>
              </div>

              <!-- Contact Info -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.site.contactInfo") }}
                </label>
                <input
                  v-model="form.contact_info"
                  type="text"
                  class="input"
                  :placeholder="t('admin.settings.site.contactInfoPlaceholder')"
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.contactInfoHint") }}
                </p>
              </div>

              <!-- Doc URL -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.site.docUrl") }}
                </label>
                <input
                  v-model="form.doc_url"
                  type="url"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.site.docUrlPlaceholder')"
                />
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.docUrlHint") }}
                </p>
              </div>

              <!-- Site Logo Upload -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.site.siteLogo") }}
                </label>
                <ImageUpload
                  v-model="form.site_logo"
                  mode="image"
                  :upload-label="t('admin.settings.site.uploadImage')"
                  :remove-label="t('admin.settings.site.remove')"
                  :hint="t('admin.settings.site.logoHint')"
                  :max-size="300 * 1024"
                />
              </div>

              <!-- Home Content -->
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  {{ t("admin.settings.site.homeContent") }}
                </label>
                <textarea
                  v-model="form.home_content"
                  rows="6"
                  class="input font-mono text-sm"
                  :placeholder="t('admin.settings.site.homeContentPlaceholder')"
                ></textarea>
                <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.site.homeContentHint") }}
                </p>
                <!-- iframe CSP Warning -->
                <p class="mt-2 text-xs text-amber-600 dark:text-amber-400">
                  {{ t("admin.settings.site.homeContentIframeWarning") }}
                </p>
              </div>

              <!-- Hide CCS Import Button -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.site.hideCcsImportButton")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.site.hideCcsImportButtonHint") }}
                  </p>
                </div>
                <Toggle v-model="form.hide_ccs_import_button" />
              </div>
            </div>
          </div>

          <!-- Custom Menu Items -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.customMenu.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.customMenu.description") }}
              </p>
            </div>
            <div class="space-y-4 p-6">
              <!-- Existing menu items -->
              <div
                v-for="(item, index) in form.custom_menu_items"
                :key="item.id || index"
                class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="mb-3 flex items-center justify-between">
                  <span
                    class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{
                      t("admin.settings.customMenu.itemLabel", { n: index + 1 })
                    }}
                  </span>
                  <div class="flex items-center gap-2">
                    <!-- Move up -->
                    <button
                      v-if="index > 0"
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.customMenu.moveUp')"
                      @click="moveMenuItem(index, -1)"
                    >
                      <svg
                        class="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        stroke-width="2"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M5 15l7-7 7 7"
                        />
                      </svg>
                    </button>
                    <!-- Move down -->
                    <button
                      v-if="index < form.custom_menu_items.length - 1"
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.customMenu.moveDown')"
                      @click="moveMenuItem(index, 1)"
                    >
                      <svg
                        class="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        stroke-width="2"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M19 9l-7 7-7-7"
                        />
                      </svg>
                    </button>
                    <!-- Delete -->
                    <button
                      type="button"
                      class="rounded p-1 text-red-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                      :title="t('admin.settings.customMenu.remove')"
                      @click="removeMenuItem(index)"
                    >
                      <svg
                        class="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        stroke-width="2"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
                        />
                      </svg>
                    </button>
                  </div>
                </div>

                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <!-- Label -->
                  <div>
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.customMenu.name") }}
                    </label>
                    <input
                      v-model="item.label"
                      type="text"
                      class="input text-sm"
                      :placeholder="
                        t('admin.settings.customMenu.namePlaceholder')
                      "
                    />
                  </div>

                  <!-- Visibility -->
                  <div>
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.customMenu.visibility") }}
                    </label>
                    <Select v-model="item.visibility" :options="customMenuVisibilityOptions" class="text-sm" />
                  </div>

                  <!-- URL (full width) -->
                  <div class="sm:col-span-2">
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.customMenu.url") }}
                    </label>
                    <input
                      v-model="item.url"
                      type="url"
                      class="input font-mono text-sm"
                      :placeholder="
                        t('admin.settings.customMenu.urlPlaceholder')
                      "
                    />
                  </div>

                  <!-- SVG Icon (full width) -->
                  <div class="sm:col-span-2">
                    <label
                      class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
                    >
                      {{ t("admin.settings.customMenu.iconSvg") }}
                    </label>
                    <ImageUpload
                      :model-value="item.icon_svg"
                      mode="svg"
                      size="sm"
                      :upload-label="t('admin.settings.customMenu.uploadSvg')"
                      :remove-label="t('admin.settings.customMenu.removeSvg')"
                      @update:model-value="(v: string) => (item.icon_svg = v)"
                    />
                  </div>
                </div>
              </div>

              <!-- Add button -->
              <button
                type="button"
                class="flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed border-gray-300 py-3 text-sm text-gray-500 transition-colors hover:border-primary-400 hover:text-primary-600 dark:border-dark-600 dark:text-gray-400 dark:hover:border-primary-500 dark:hover:text-primary-400"
                @click="addMenuItem"
              >
                <svg
                  class="h-4 w-4"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  stroke-width="2"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M12 4v16m8-8H4"
                  />
                </svg>
                {{ t("admin.settings.customMenu.add") }}
              </button>
            </div>
          </div>

          <!-- Footer Settings -->
          <div class="card">
            <div
              class="flex items-start justify-between gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                  {{ localText("底栏设置", "Footer settings") }}
                </h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  {{
                    localText(
                      "配置首页底栏的链接分组与附加文本（如备案号）。留空时底栏仅显示版权信息。",
                      "Configure homepage footer link groups and extra text (e.g. ICP number). When empty, the footer only shows the copyright line.",
                    )
                  }}
                </p>
              </div>
              <button
                type="button"
                class="btn btn-secondary btn-sm shrink-0"
                @click="applyDefaultFooterLinks"
              >
                {{ localText("使用默认模板", "Use default template") }}
              </button>
            </div>
            <div class="space-y-4 p-6">
              <!-- Link groups -->
              <div
                v-for="(group, gIndex) in form.footer_links"
                :key="gIndex"
                class="rounded-lg border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="mb-3 flex items-center justify-between gap-3">
                  <input
                    v-model="group.title"
                    type="text"
                    class="input max-w-xs text-sm font-medium"
                    :placeholder="localText('分组标题，如：产品', 'Group title, e.g. Product')"
                  />
                  <div class="flex items-center gap-2">
                    <button
                      v-if="gIndex > 0"
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.customMenu.moveUp')"
                      @click="moveFooterGroup(gIndex, -1)"
                    >
                      <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M5 15l7-7 7 7" />
                      </svg>
                    </button>
                    <button
                      v-if="gIndex < form.footer_links.length - 1"
                      type="button"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700"
                      :title="t('admin.settings.customMenu.moveDown')"
                      @click="moveFooterGroup(gIndex, 1)"
                    >
                      <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
                      </svg>
                    </button>
                    <button
                      type="button"
                      class="rounded p-1 text-red-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                      :title="localText('删除分组', 'Remove group')"
                      @click="removeFooterGroup(gIndex)"
                    >
                      <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                      </svg>
                    </button>
                  </div>
                </div>

                <div class="space-y-2">
                  <div
                    v-for="(link, lIndex) in group.links"
                    :key="lIndex"
                    class="flex items-center gap-2"
                  >
                    <input
                      v-model="link.label"
                      type="text"
                      class="input w-40 text-sm"
                      :placeholder="localText('名称', 'Label')"
                    />
                    <input
                      v-model="link.url"
                      type="text"
                      class="input flex-1 font-mono text-sm"
                      :placeholder="localText('https://... 或 /models', 'https://... or /models')"
                    />
                    <button
                      type="button"
                      class="rounded p-1 text-red-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                      :title="localText('删除链接', 'Remove link')"
                      @click="group.links.splice(lIndex, 1)"
                    >
                      <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                  <button
                    type="button"
                    class="inline-flex items-center gap-1.5 text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400"
                    @click="group.links.push({ label: '', url: '' })"
                  >
                    <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                    </svg>
                    {{ localText("添加链接", "Add link") }}
                  </button>
                </div>
              </div>

              <!-- Add group button -->
              <button
                type="button"
                class="flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed border-gray-300 py-3 text-sm text-gray-500 transition-colors hover:border-primary-400 hover:text-primary-600 dark:border-dark-600 dark:text-gray-400 dark:hover:border-primary-500 dark:hover:text-primary-400"
                @click="addFooterGroup"
              >
                <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                </svg>
                {{ localText("添加分组", "Add group") }}
              </button>

              <!-- Footer text -->
              <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
                <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ localText("底栏附加文本", "Footer text") }}
                </label>
                <textarea
                  v-model="form.footer_text"
                  rows="2"
                  class="input min-h-[64px] resize-y text-sm"
                  :placeholder="localText('例如备案号、自定义版权说明，支持多行', 'e.g. ICP number or custom copyright, multiline supported')"
                ></textarea>
              </div>
            </div>
          </div>
        </div>
        <!-- /分页：通用设置 -->

        <!-- 分页：登录条款 -->
        <div v-show="activeTab === 'agreement'" class="space-y-6">
          <div class="card">
            <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
              <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                <div>
                  <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                    {{ localText("登录条款确认", "Login agreement") }}
                  </h2>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    {{
                      localText(
                        "控制登录页是否要求用户先阅读并同意服务条款、隐私政策或其他 Markdown 文档。",
                        "Control whether the login page requires users to accept Markdown policy documents first.",
                      )
                    }}
                  </p>
                </div>
                <div class="flex items-center gap-3">
                  <span class="text-sm text-gray-600 dark:text-gray-300">
                    {{ form.login_agreement_enabled ? localText("已启用", "Enabled") : localText("未启用", "Disabled") }}
                  </span>
                  <Toggle v-model="form.login_agreement_enabled" />
                </div>
              </div>
            </div>

            <div class="space-y-6 p-6">
              <div class="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,1fr)_220px]">
                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ localText("展示形式", "Display mode") }}
                  </label>
                  <div class="grid grid-cols-2 gap-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
                    <button
                      type="button"
                      class="inline-flex items-center justify-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition"
                      :class="
                        form.login_agreement_mode === 'modal'
                          ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                          : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
                      "
                      @click="form.login_agreement_mode = 'modal'"
                    >
                      <Icon name="shield" size="sm" />
                      {{ localText("弹窗", "Modal") }}
                    </button>
                    <button
                      type="button"
                      class="inline-flex items-center justify-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition"
                      :class="
                        form.login_agreement_mode === 'checkbox'
                          ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                          : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
                      "
                      @click="form.login_agreement_mode = 'checkbox'"
                    >
                      <Icon name="checkCircle" size="sm" />
                      {{ localText("复选框", "Checkbox") }}
                    </button>
                  </div>
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      form.login_agreement_mode === "checkbox"
                        ? localText("复选框会显示在登录按钮下方，未勾选前所有登录入口禁用。", "The checkbox appears below the login button and gates all login actions.")
                        : localText("弹窗会在登录页打开，用户拒绝后所有登录入口保持禁用。", "The modal opens on the login page and gates all login actions until accepted.")
                    }}
                  </p>
                </div>

                <div>
                  <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ localText("条款更新日期", "Updated date") }}
                  </label>
                  <input
                    v-model="form.login_agreement_updated_at"
                    type="date"
                    class="input"
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ localText("日期或文档内容变化后，用户需要重新同意。", "Changing the date or content requires fresh consent.") }}
                  </p>
                </div>
              </div>

              <div>
                <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <h3 class="text-sm font-medium text-gray-900 dark:text-white">
                      {{ localText("协议文档", "Agreement documents") }}
                    </h3>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        localText(
                          "文档名称可自定义，内容按 Markdown 保存。可参考：服务条款、使用政策、支持的国家和地区、服务特定条款。",
                          "Document titles are customizable and content is saved as Markdown.",
                        )
                      }}
                    </p>
                  </div>
                  <button
                    type="button"
                    class="btn btn-primary btn-sm inline-flex items-center gap-1.5"
                    @click="addLoginAgreementDocument"
                  >
                    <Icon name="plus" size="sm" />
                    {{ localText("添加文档", "Add document") }}
                  </button>
                </div>

                <div class="mt-4 space-y-3">
                  <div
                    v-for="(doc, index) in form.login_agreement_documents"
                    :key="doc.id || index"
                    class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800/60"
                  >
                    <div class="mb-3 flex items-center justify-between gap-3">
                      <div class="flex min-w-0 items-center gap-3">
                        <span class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-dark-200">
                          <Icon
                            :name="
                              index === 1
                                ? 'shield'
                                : index === 2
                                  ? 'globe'
                                  : index === 3
                                    ? 'cog'
                                    : 'document'
                            "
                            size="sm"
                          />
                        </span>
                        <div class="min-w-0">
                          <p class="truncate text-sm font-semibold text-gray-900 dark:text-white">
                            {{ doc.title || localText("未命名文档", "Untitled document") }}
                          </p>
                          <p class="truncate text-xs text-gray-500 dark:text-gray-400">
                            {{ loginAgreementRoutePath(doc, index) }}
                          </p>
                        </div>
                      </div>
                      <button
                        type="button"
                        class="rounded-md p-2 text-red-400 transition hover:bg-red-50 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-red-900/20"
                        :disabled="
                          form.login_agreement_enabled &&
                          form.login_agreement_documents.length <= 1
                        "
                        @click="removeLoginAgreementDocument(index)"
                      >
                        <Icon name="trash" size="sm" />
                      </button>
                    </div>

                    <div class="grid grid-cols-1 gap-3 lg:grid-cols-2">
                      <div>
                        <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                          {{ localText("文档名称", "Document title") }}
                        </label>
                        <input
                          v-model="doc.title"
                          type="text"
                          class="input text-sm"
                          :placeholder="localText('例如：服务条款', 'Example: Terms of Service')"
                        />
                      </div>
                      <div>
                        <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                          {{ localText("路由标识", "Route slug") }}
                        </label>
                        <div class="flex overflow-hidden rounded-lg border border-gray-300 bg-white focus-within:border-primary-500 focus-within:ring-1 focus-within:ring-primary-500 dark:border-dark-600 dark:bg-dark-900">
                          <span class="inline-flex flex-shrink-0 items-center border-r border-gray-200 bg-gray-50 px-3 text-sm text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-400">
                            /legal/
                          </span>
                          <input
                            v-model="doc.id"
                            type="text"
                            class="min-w-0 flex-1 border-0 bg-transparent px-3 py-2 text-sm text-gray-900 outline-none placeholder:text-gray-400 focus:ring-0 dark:text-white dark:placeholder:text-dark-500"
                            placeholder="usage-policy"
                          />
                        </div>
                      </div>
                    </div>
                    <div class="mt-3">
                      <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                        {{ localText("Markdown 内容", "Markdown content") }}
                      </label>
                        <textarea
                          v-model="doc.content_md"
                          rows="8"
                          class="input font-mono text-sm"
                          :placeholder="localText('在这里填写正式 Markdown 内容。', 'Write the final Markdown content here.')"
                        ></textarea>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
        <!-- /分页：登录条款 -->

        <!-- 分页：功能特性 -->
        <div v-show="activeTab === 'features'" class="space-y-6">
          <div class="card">
            <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.features.affiliate.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.features.affiliate.description") }}
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.features.affiliate.enabled") }}
                  </label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.affiliate.enabledHint") }}
                  </p>
                </div>
                <Toggle v-model="form.affiliate_enabled" />
              </div>

              <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                <div>
                  <label class="input-label">
                    {{ t("admin.settings.features.affiliate.rebateRate") }}
                  </label>
                  <div class="relative">
                    <input
                      v-model.number="form.affiliate_rebate_rate"
                      type="number"
                      min="0"
                      max="100"
                      step="0.01"
                      class="input pr-8"
                      placeholder="20"
                    />
                    <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-gray-400">%</span>
                  </div>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.affiliate.rebateRateHint") }}
                  </p>
                </div>

                <div>
                  <label class="input-label">
                    {{ t("admin.settings.features.affiliate.freezeHours") }}
                  </label>
                  <input
                    v-model.number="form.affiliate_rebate_freeze_hours"
                    type="number"
                    min="0"
                    max="720"
                    step="1"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.affiliate.freezeHoursDesc") }}
                  </p>
                </div>

                <div>
                  <label class="input-label">
                    {{ t("admin.settings.features.affiliate.durationDays") }}
                  </label>
                  <input
                    v-model.number="form.affiliate_rebate_duration_days"
                    type="number"
                    min="0"
                    max="3650"
                    step="1"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.affiliate.durationDaysDesc") }}
                  </p>
                </div>

                <div>
                  <label class="input-label">
                    {{ t("admin.settings.features.affiliate.perInviteeCap") }}
                  </label>
                  <input
                    v-model.number="form.affiliate_rebate_per_invitee_cap"
                    type="number"
                    min="0"
                    step="0.01"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.affiliate.perInviteeCapDesc") }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <div class="card">
            <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.features.riskControl.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.features.riskControl.description") }}
              </p>
              <p class="mt-1.5 text-xs">
                <router-link
                  to="/admin/risk-control"
                  class="inline-flex items-center gap-1 text-primary-600 hover:underline dark:text-primary-400"
                >
                  {{ t("admin.settings.features.riskControl.configureLink") }}
                  <span aria-hidden="true">-&gt;</span>
                </router-link>
              </p>
            </div>
            <div class="space-y-5 p-6">
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.features.riskControl.enabled") }}
                  </label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.riskControl.enabledHint") }}
                  </p>
                </div>
                <Toggle v-model="form.risk_control_enabled" />
              </div>

              <div class="border-t border-gray-100 pt-5 dark:border-dark-700">
                <div class="flex items-center justify-between gap-4">
                  <div>
                    <label class="font-medium text-gray-900 dark:text-white">
                      {{ t("admin.settings.features.riskControl.cyberSessionBlockEnabled") }}
                    </label>
                    <p class="text-sm text-gray-500 dark:text-gray-400">
                      {{ t("admin.settings.features.riskControl.cyberSessionBlockEnabledHint") }}
                    </p>
                  </div>
                  <Toggle v-model="form.cyber_session_block_enabled" />
                </div>

                <div
                  v-if="form.cyber_session_block_enabled"
                  class="mt-4 max-w-xs"
                >
                  <label class="input-label">
                    {{ t("admin.settings.features.riskControl.cyberSessionBlockTTLSeconds") }}
                  </label>
                  <input
                    v-model.number="form.cyber_session_block_ttl_seconds"
                    type="number"
                    min="1"
                    step="1"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.features.riskControl.cyberSessionBlockTTLSecondsHint") }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
        <!-- /分页：功能特性 -->

        <!-- Tab: Email -->
        <!-- Tab: Payment -->
        <div v-show="activeTab === 'payment'" class="space-y-6">
          <!-- Payment System Settings -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.payment.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.payment.description") }}
                <a
                  :href="paymentGuideHref"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="ml-2 inline-flex items-center text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                >
                  <svg
                    class="mr-0.5 h-3.5 w-3.5"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      stroke-width="2"
                      d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
                    />
                  </svg>
                  {{ t("admin.settings.payment.configGuide") }}
                </a>
              </p>
            </div>
            <div class="space-y-4 p-6">
              <!-- Enable toggle -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.payment.enabled")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.payment.enabledHint") }}
                  </p>
                </div>
                <Toggle v-model="form.payment_enabled" />
              </div>
              <template v-if="form.payment_enabled">
                <!-- Row 1: Product name -->
                <div class="grid grid-cols-3 gap-3">
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.productNamePrefix")
                    }}</label
                    ><input
                      v-model="form.payment_product_name_prefix"
                      type="text"
                      class="input"
                      placeholder="Sub2API"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.productNameSuffix")
                    }}</label
                    ><input
                      v-model="form.payment_product_name_suffix"
                      type="text"
                      class="input"
                      placeholder="CNY"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.preview")
                    }}</label>
                    <div
                      class="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300"
                    >
                      {{
                        (form.payment_product_name_prefix || "Sub2API") +
                        " 100 " +
                        (form.payment_product_name_suffix || "CNY")
                      }}
                    </div>
                  </div>
                </div>
                <!-- Row 2: Balance toggle + amounts -->
                <div class="grid grid-cols-2 gap-3 sm:grid-cols-5">
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.minAmount")
                    }}</label
                    ><input
                      :value="form.payment_min_amount || ''"
                      @input="
                        form.payment_min_amount =
                          parseFloat(
                            ($event.target as HTMLInputElement).value,
                          ) || 0
                      "
                      type="number"
                      step="0.01"
                      min="0"
                      class="input"
                      :placeholder="t('admin.settings.payment.noLimit')"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.maxAmount")
                    }}</label
                    ><input
                      :value="form.payment_max_amount || ''"
                      @input="
                        form.payment_max_amount =
                          parseFloat(
                            ($event.target as HTMLInputElement).value,
                          ) || 0
                      "
                      type="number"
                      step="0.01"
                      min="0"
                      class="input"
                      :placeholder="t('admin.settings.payment.noLimit')"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.dailyLimit")
                    }}</label
                    ><input
                      :value="form.payment_daily_limit || ''"
                      @input="
                        form.payment_daily_limit =
                          parseFloat(
                            ($event.target as HTMLInputElement).value,
                          ) || 0
                      "
                      type="number"
                      step="0.01"
                      min="0"
                      class="input"
                      :placeholder="t('admin.settings.payment.noLimit')"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.balanceRechargeMultiplier")
                    }}</label>
                    <input
                      :value="form.payment_balance_recharge_multiplier || ''"
                      @input="
                        form.payment_balance_recharge_multiplier =
                          parseFloat(
                            ($event.target as HTMLInputElement).value,
                          ) || 1
                      "
                      type="number"
                      step="0.01"
                      min="0.01"
                      class="input"
                    />
                    <p class="mt-0.5 text-xs text-gray-400">
                      {{
                        t("admin.settings.payment.balanceRechargeMultiplierHint", { unitName: previewBalanceUnitName })
                      }}
                    </p>
                    <p
                      class="mt-1 text-xs font-medium text-primary-600 dark:text-primary-400"
                    >
                      {{
                        t("admin.settings.payment.balanceRechargePreview", { amount: (Number(form.payment_balance_recharge_multiplier) || 1).toFixed(2), unitName: previewBalanceUnitName })
                      }}
                    </p>
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.subscriptionUsdToCnyRate")
                    }}</label>
                    <input
                      :value="form.payment_subscription_usd_to_cny_rate || ''"
                      @input="
                        form.payment_subscription_usd_to_cny_rate =
                          parseFloat(
                            ($event.target as HTMLInputElement).value,
                          ) || 0
                      "
                      type="number"
                      step="0.01"
                      min="0"
                      class="input"
                      :placeholder="
                        t(
                          'admin.settings.payment.subscriptionUsdToCnyRateDisabled',
                        )
                      "
                    />
                    <p class="mt-0.5 text-xs text-gray-400">
                      {{
                        t("admin.settings.payment.subscriptionUsdToCnyRateHint")
                      }}
                    </p>
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.rechargeFeeRate")
                    }}</label>
                    <div class="relative">
                      <input
                        :value="form.payment_recharge_fee_rate ?? ''"
                        @input="
                          form.payment_recharge_fee_rate = Math.min(
                            100,
                            Math.max(
                              0,
                              Math.round(
                                parseFloat(
                                  ($event.target as HTMLInputElement).value ||
                                    '0',
                                ) * 100,
                              ) / 100,
                            ),
                          )
                        "
                        type="number"
                        step="0.01"
                        min="0"
                        max="100"
                        class="input pr-8"
                      />
                      <span
                        class="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-3 text-gray-400"
                        >%</span
                      >
                    </div>
                    <p class="mt-0.5 text-xs text-gray-400">
                      {{ t("admin.settings.payment.rechargeFeeRateHint") }}
                    </p>
                    <p
                      v-if="(Number(form.payment_recharge_fee_rate) || 0) > 0"
                      class="mt-1 text-xs font-medium text-primary-600 dark:text-primary-400"
                    >
                      {{
                        t("admin.settings.payment.rechargeFeePreview", {
                          fee: (
                            Number(form.payment_recharge_fee_rate) || 0
                          ).toFixed(2),
                        })
                      }}
                    </p>
                  </div>
                  <div class="col-span-2 sm:col-span-5">
                    <div class="rounded-lg border border-gray-200 dark:border-dark-700">
                      <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700">
                        <p
                          class="text-sm font-semibold text-gray-900 dark:text-white"
                        >
                          {{ t("admin.settings.payment.methodFeesTitle") }}
                        </p>
                        <p class="mt-0.5 text-xs text-gray-400">
                          {{ t("admin.settings.payment.methodFeesHint") }}
                        </p>
                      </div>
                      <div
                        class="hidden grid-cols-[minmax(120px,1fr)_minmax(140px,180px)_minmax(140px,180px)_minmax(180px,1.2fr)] gap-4 bg-gray-50 px-4 py-2 text-xs font-medium text-gray-500 dark:bg-dark-800 dark:text-gray-400 md:grid"
                      >
                        <span>{{
                          t("admin.settings.payment.paymentMethod")
                        }}</span>
                        <span>{{ t("admin.settings.payment.fixedFee") }}</span>
                        <span>{{ t("admin.settings.payment.feeRate") }}</span>
                        <span>{{
                          t("admin.settings.payment.methodFeeResult")
                        }}</span>
                      </div>
                      <div class="divide-y divide-gray-100 dark:divide-dark-700">
                        <div
                          v-for="method in paymentMethodFeeOptions"
                          :key="method.value"
                          class="grid gap-3 px-4 py-3 md:grid-cols-[minmax(120px,1fr)_minmax(140px,180px)_minmax(140px,180px)_minmax(180px,1.2fr)] md:items-center md:gap-4"
                        >
                          <label
                            class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300"
                          >
                            <input
                              type="checkbox"
                              class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                              :checked="methodFeeEnabled(method.value)"
                              @change="setMethodFeeEnabled(method.value, ($event.target as HTMLInputElement).checked)"
                            />
                            {{ method.label }}
                          </label>
                          <div class="min-w-0">
                            <label
                              class="mb-1 block text-xs text-gray-500 dark:text-gray-400 md:hidden"
                              >{{ t("admin.settings.payment.fixedFee") }}</label
                            >
                            <div class="relative">
                              <span
                                class="pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3 text-gray-400"
                                >¥</span
                              >
                              <input
                                type="number"
                                step="0.01"
                                min="0"
                                class="input h-10 w-full pl-8"
                                :disabled="!methodFeeEnabled(method.value)"
                                :value="methodFeeValue(method.value, 'fixed_fee')"
                                @input="setMethodFeeValue(method.value, 'fixed_fee', ($event.target as HTMLInputElement).value)"
                              />
                            </div>
                          </div>
                          <div class="min-w-0">
                            <label
                              class="mb-1 block text-xs text-gray-500 dark:text-gray-400 md:hidden"
                              >{{ t("admin.settings.payment.feeRate") }}</label
                            >
                            <div class="relative">
                              <input
                                type="number"
                                step="0.01"
                                min="0"
                                max="100"
                                class="input h-10 w-full pr-8"
                                :disabled="!methodFeeEnabled(method.value)"
                                :value="methodFeeValue(method.value, 'fee_rate')"
                                @input="setMethodFeeValue(method.value, 'fee_rate', ($event.target as HTMLInputElement).value)"
                              />
                              <span
                                class="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-3 text-gray-400"
                                >%</span
                              >
                            </div>
                          </div>
                          <div class="min-w-0 text-xs">
                            <p
                              class="mb-1 text-gray-500 dark:text-gray-400 md:hidden"
                            >
                              {{ t("admin.settings.payment.methodFeeResult") }}
                            </p>
                            <p
                              :class="methodFeeEnabled(method.value)
                                ? 'font-medium text-primary-600 dark:text-primary-400'
                                : 'text-gray-400 dark:text-gray-500'"
                            >
                              {{
                                methodFeeEnabled(method.value)
                                  ? methodFeePreview(method.value)
                                  : t("admin.settings.payment.methodFeeFallback")
                              }}
                            </p>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div>
                    <label class="input-label"
                      >{{ t("admin.settings.payment.orderTimeout") }}
                      <span class="text-red-500">*</span></label
                    ><input
                      v-model.number="form.payment_order_timeout_minutes"
                      type="number"
                      min="1"
                      class="input"
                      required
                    />
                    <p class="mt-0.5 text-xs text-gray-400">
                      {{ t("admin.settings.payment.orderTimeoutHint") }}
                    </p>
                  </div>
                </div>
                <!-- Row 3: Pending orders + load balance + cancel rate limit (all in one row) -->
                <div class="flex flex-wrap items-end gap-4">
                  <div class="w-28">
                    <label class="input-label">{{
                      t("admin.settings.payment.maxPendingOrders")
                    }}</label
                    ><input
                      v-model.number="form.payment_max_pending_orders"
                      type="number"
                      min="1"
                      class="input"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.loadBalanceStrategy")
                    }}</label>
                    <Select
                      v-model="form.payment_load_balance_strategy"
                      :options="loadBalanceOptions"
                      class="w-40"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.cancelRateLimit")
                    }}</label>
                    <div class="flex items-center gap-2">
                      <button
                        type="button"
                        :class="[
                          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                          form.payment_cancel_rate_limit_enabled
                            ? 'bg-primary-500'
                            : 'bg-gray-300 dark:bg-dark-600',
                        ]"
                        @click="
                          form.payment_cancel_rate_limit_enabled =
                            !form.payment_cancel_rate_limit_enabled
                        "
                      >
                        <span
                          :class="[
                            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                            form.payment_cancel_rate_limit_enabled
                              ? 'translate-x-5'
                              : 'translate-x-0',
                          ]"
                        />
                      </button>
                      <Select
                        v-model="form.payment_cancel_rate_limit_window_mode"
                        :options="cancelRateLimitModeOptions"
                        class="w-24"
                        :disabled="!form.payment_cancel_rate_limit_enabled"
                      />
                      <span
                        :class="[
                          'text-sm whitespace-nowrap',
                          form.payment_cancel_rate_limit_enabled
                            ? 'text-gray-700 dark:text-gray-300'
                            : 'text-gray-400 dark:text-gray-600',
                        ]"
                        >{{
                          t("admin.settings.payment.cancelRateLimitEvery")
                        }}</span
                      >
                      <input
                        v-model.number="form.payment_cancel_rate_limit_window"
                        type="number"
                        min="1"
                        required
                        class="input w-14 text-center"
                        :disabled="!form.payment_cancel_rate_limit_enabled"
                      />
                      <Select
                        v-model="form.payment_cancel_rate_limit_unit"
                        :options="cancelRateLimitUnitOptions"
                        class="w-28"
                        :disabled="!form.payment_cancel_rate_limit_enabled"
                      />
                      <span
                        :class="[
                          'text-sm whitespace-nowrap',
                          form.payment_cancel_rate_limit_enabled
                            ? 'text-gray-700 dark:text-gray-300'
                            : 'text-gray-400 dark:text-gray-600',
                        ]"
                        >{{
                          t("admin.settings.payment.cancelRateLimitAllowMax")
                        }}</span
                      >
                      <input
                        v-model.number="form.payment_cancel_rate_limit_max"
                        type="number"
                        min="1"
                        required
                        class="input w-14 text-center"
                        :disabled="!form.payment_cancel_rate_limit_enabled"
                      />
                      <span
                        :class="[
                          'text-sm whitespace-nowrap',
                          form.payment_cancel_rate_limit_enabled
                            ? 'text-gray-700 dark:text-gray-300'
                            : 'text-gray-400 dark:text-gray-600',
                        ]"
                        >{{
                          t("admin.settings.payment.cancelRateLimitTimes")
                        }}</span
                      >
                    </div>
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.alipayForceQRCode")
                    }}</label>
                    <div class="flex items-center gap-2">
                      <button
                        type="button"
                        :class="[
                          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
                          form.payment_alipay_force_qrcode
                            ? 'bg-primary-500'
                            : 'bg-gray-300 dark:bg-dark-600',
                        ]"
                        @click="
                          form.payment_alipay_force_qrcode =
                            !form.payment_alipay_force_qrcode
                        "
                      >
                        <span
                          :class="[
                            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                            form.payment_alipay_force_qrcode
                              ? 'translate-x-5'
                              : 'translate-x-0',
                          ]"
                        />
                      </button>
                      <span class="text-sm text-gray-500 dark:text-gray-400">{{
                        t("admin.settings.payment.alipayForceQRCodeHint")
                      }}</span>
                    </div>
                  </div>
                </div>
                <!-- Row 4: Enabled payment types (provider badges like sub2apipay) -->
                <div>
                  <label class="input-label">{{
                    t("admin.settings.payment.enabledPaymentTypes")
                  }}</label>
                  <div class="mt-1.5 flex flex-wrap gap-2">
                    <button
                      v-for="pt in allPaymentTypes"
                      :key="pt.value"
                      type="button"
                      @click="togglePaymentType(pt.value)"
                      :class="[
                        'rounded-lg border px-3 py-1.5 text-sm font-medium transition-all',
                        isPaymentTypeEnabled(pt.value)
                          ? 'border-primary-500 bg-primary-500 text-white shadow-sm'
                          : 'border-gray-300 bg-white text-gray-600 hover:border-gray-400 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-dark-500',
                      ]"
                    >
                      {{ pt.label }}
                    </button>
                  </div>
                  <p class="mt-2 text-xs text-gray-400 dark:text-gray-500">
                    {{ t("admin.settings.payment.enabledPaymentTypesHint") }}
                    <a
                      :href="paymentMethodsHref"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="ml-1 text-primary-500 hover:text-primary-600 dark:text-primary-400 dark:hover:text-primary-300"
                    >
                      {{ t("admin.settings.payment.findProvider") }}
                      <svg
                        class="mb-0.5 ml-0.5 inline h-3 w-3"
                        fill="none"
                        stroke="currentColor"
                        viewBox="0 0 24 24"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          stroke-width="2"
                          d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
                        />
                      </svg>
                    </a>
                  </p>
                </div>
                <!-- Row 5: Help image + text -->
                <div class="grid grid-cols-2 gap-3">
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.helpImage")
                    }}</label>
                    <ImageUpload
                      v-model="form.payment_help_image_url"
                      :upload-label="t('admin.settings.site.uploadImage')"
                      :remove-label="t('admin.settings.site.remove')"
                      :placeholder="
                        t('admin.settings.payment.helpImagePlaceholder')
                      "
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.settings.payment.helpText")
                    }}</label>
                    <p class="mb-1 text-xs text-gray-400">
                      {{ t("admin.settings.payment.helpTextHint") }}
                    </p>
                    <textarea
                      v-model="form.payment_help_text"
                      rows="3"
                      class="input"
                      :placeholder="
                        t('admin.settings.payment.helpTextPlaceholder')
                      "
                    ></textarea>
                  </div>
                </div>
              </template>
            </div>
          </div>

          <!-- Provider Management -->
          <PaymentProviderList
            v-if="form.payment_enabled"
            :providers="providers"
            :loading="providersLoading"
            :can-create="hasAnyPaymentTypeEnabled"
            :enabled-payment-types="form.payment_enabled_types"
            :all-payment-types="allPaymentTypes"
            :redirect-label="t('admin.settings.payment.easypayRedirect')"
            :updating-provider-ids="updatingProviderIds"
            @refresh="loadProviders"
            @create="openCreateProvider"
            @edit="openEditProvider"
            @delete="confirmDeleteProvider"
            @toggle-field="handleToggleField"
            @toggle-type="handleToggleType"
            @reorder="handleReorderProviders"
          />
        </div>

        <div v-show="activeTab === 'email'" class="space-y-6">
          <!-- Email disabled hint - show when email_verify_enabled is off -->
          <div v-if="!form.email_verify_enabled" class="card">
            <div class="p-6">
              <div class="flex items-start gap-3">
                <Icon
                  name="mail"
                  size="md"
                  class="mt-0.5 flex-shrink-0 text-gray-400 dark:text-gray-500"
                />
                <div>
                  <h3 class="font-medium text-gray-900 dark:text-white">
                    {{ t("admin.settings.emailTabDisabledTitle") }}
                  </h3>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.emailTabDisabledHint") }}
                  </p>
                </div>
              </div>
            </div>
          </div>

          <!-- SMTP Settings - Only show when email verification is enabled -->
          <div v-if="form.email_verify_enabled" class="card">
            <div
              class="flex items-center justify-between border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                  {{ t("admin.settings.smtp.title") }}
                </h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.smtp.description") }}
                </p>
              </div>
              <button
                type="button"
                @click="testSmtpConnection"
                :disabled="testingSmtp || loadFailed"
                class="btn btn-secondary btn-sm"
              >
                <svg
                  v-if="testingSmtp"
                  class="h-4 w-4 animate-spin"
                  fill="none"
                  viewBox="0 0 24 24"
                >
                  <circle
                    class="opacity-25"
                    cx="12"
                    cy="12"
                    r="10"
                    stroke="currentColor"
                    stroke-width="4"
                  ></circle>
                  <path
                    class="opacity-75"
                    fill="currentColor"
                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                  ></path>
                </svg>
                {{
                  testingSmtp
                    ? t("admin.settings.smtp.testing")
                    : t("admin.settings.smtp.testConnection")
                }}
              </button>
            </div>
            <div class="space-y-6 p-6">
              <div class="grid grid-cols-1 gap-6 md:grid-cols-2">
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.smtp.host") }}
                  </label>
                  <input
                    v-model="form.smtp_host"
                    type="text"
                    class="input"
                    :placeholder="t('admin.settings.smtp.hostPlaceholder')"
                  />
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.smtp.port") }}
                  </label>
                  <input
                    v-model.number="form.smtp_port"
                    type="number"
                    min="1"
                    max="65535"
                    class="input"
                    :placeholder="t('admin.settings.smtp.portPlaceholder')"
                  />
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.smtp.username") }}
                  </label>
                  <input
                    v-model="form.smtp_username"
                    type="text"
                    class="input"
                    :placeholder="t('admin.settings.smtp.usernamePlaceholder')"
                  />
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.smtp.password") }}
                  </label>
                  <input
                    v-model="form.smtp_password"
                    type="password"
                    class="input"
                    autocomplete="new-password"
                    autocapitalize="off"
                    spellcheck="false"
                    @keydown="smtpPasswordManuallyEdited = true"
                    @paste="smtpPasswordManuallyEdited = true"
                    :placeholder="
                      form.smtp_password_configured
                        ? t('admin.settings.smtp.passwordConfiguredPlaceholder')
                        : t('admin.settings.smtp.passwordPlaceholder')
                    "
                  />
                  <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      form.smtp_password_configured
                        ? t("admin.settings.smtp.passwordConfiguredHint")
                        : t("admin.settings.smtp.passwordHint")
                    }}
                  </p>
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.smtp.fromEmail") }}
                  </label>
                  <input
                    v-model="form.smtp_from_email"
                    type="email"
                    class="input"
                    :placeholder="t('admin.settings.smtp.fromEmailPlaceholder')"
                  />
                </div>
                <div>
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.smtp.fromName") }}
                  </label>
                  <input
                    v-model="form.smtp_from_name"
                    type="text"
                    class="input"
                    :placeholder="t('admin.settings.smtp.fromNamePlaceholder')"
                  />
                </div>
              </div>

              <!-- Use TLS Toggle -->
              <div
                class="flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700"
              >
                <div>
                  <label class="font-medium text-gray-900 dark:text-white">{{
                    t("admin.settings.smtp.useTls")
                  }}</label>
                  <p class="text-sm text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.smtp.useTlsHint") }}
                  </p>
                </div>
                <Toggle v-model="form.smtp_use_tls" />
              </div>
            </div>
          </div>

          <!-- Send Test Email - Only show when email verification is enabled -->
          <div v-if="form.email_verify_enabled" class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.testEmail.title") }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.testEmail.description") }}
              </p>
            </div>
            <div class="p-6">
              <div class="flex items-end gap-4">
                <div class="flex-1">
                  <label
                    class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.testEmail.recipientEmail") }}
                  </label>
                  <input
                    v-model="testEmailAddress"
                    type="email"
                    class="input"
                    :placeholder="
                      t('admin.settings.testEmail.recipientEmailPlaceholder')
                    "
                  />
                </div>
                <button
                  type="button"
                  @click="sendTestEmail"
                  :disabled="
                    sendingTestEmail || !testEmailAddress || loadFailed
                  "
                  class="btn btn-secondary"
                >
                  <svg
                    v-if="sendingTestEmail"
                    class="h-4 w-4 animate-spin"
                    fill="none"
                    viewBox="0 0 24 24"
                  >
                    <circle
                      class="opacity-25"
                      cx="12"
                      cy="12"
                      r="10"
                      stroke="currentColor"
                      stroke-width="4"
                    ></circle>
                    <path
                      class="opacity-75"
                      fill="currentColor"
                      d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                    ></path>
                  </svg>
                  {{
                    sendingTestEmail
                      ? t("admin.settings.testEmail.sending")
                      : t("admin.settings.testEmail.sendTestEmail")
                  }}
                </button>
              </div>
            </div>
          </div>

          <!-- 订阅到期提醒 -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h3 class="text-base font-medium text-gray-900 dark:text-white">
                {{ t("admin.settings.subscriptionExpiryNotify.title") }}
              </h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.subscriptionExpiryNotify.description") }}
              </p>
            </div>
            <div class="px-6 py-6">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <label
                    class="mb-0 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >
                    {{ t("admin.settings.subscriptionExpiryNotify.enabled") }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.settings.subscriptionExpiryNotify.enabledHint") }}
                  </p>
                </div>
                <Toggle v-model="form.subscription_expiry_notify_enabled" />
              </div>
            </div>
          </div>

          <EmailTemplateEditor />

          <!-- Balance Low Notification -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h3 class="text-base font-medium text-gray-900 dark:text-white">
                {{ t("admin.settings.balanceNotify.title") }}
              </h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.balanceNotify.description") }}
              </p>
            </div>
            <div class="px-6 py-6 space-y-4">
              <div class="flex items-center justify-between">
                <label
                  class="mb-0 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t("admin.settings.balanceNotify.enabled") }}</label
                >
                <Toggle v-model="form.balance_low_notify_enabled" />
              </div>
              <div v-if="form.balance_low_notify_enabled">
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t("admin.settings.balanceNotify.threshold") }}</label
                >
                <div class="relative">
                  <span
                    class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
                    >{{ previewBalanceUnitSymbol }}</span>
                  <input
                    v-model.number="form.balance_low_notify_threshold"
                    type="number"
                    min="0"
                    step="0.01"
                    class="input pl-7"
                  />
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.balanceNotify.thresholdHint") }}
                </p>
              </div>
              <div>
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t("admin.settings.balanceNotify.rechargeUrl") }}</label
                >
                <input
                  v-model="form.balance_low_notify_recharge_url"
                  type="url"
                  class="input"
                  :placeholder="currentOrigin"
                />
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.balanceNotify.rechargeUrlHint") }}
                </p>
              </div>
            </div>
          </div>

          <!-- Account Quota Notification -->
          <div class="card">
            <div
              class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"
            >
              <h3 class="text-base font-medium text-gray-900 dark:text-white">
                {{ t("admin.settings.quotaNotify.title") }}
              </h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t("admin.settings.quotaNotify.description") }}
              </p>
            </div>
            <div class="px-6 py-6 space-y-4">
              <div class="flex items-center justify-between">
                <label
                  class="mb-0 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t("admin.settings.quotaNotify.enabled") }}</label
                >
                <Toggle v-model="form.account_quota_notify_enabled" />
              </div>
              <div v-if="form.account_quota_notify_enabled">
                <label
                  class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t("admin.settings.quotaNotify.emails") }}</label
                >
                <div class="space-y-2">
                  <div
                    v-for="(entry, index) in form.account_quota_notify_emails ||
                    []"
                    :key="index"
                    class="flex items-center gap-2"
                  >
                    <label
                      class="relative inline-flex items-center cursor-pointer shrink-0"
                    >
                      <input
                        type="checkbox"
                        :checked="!entry.disabled"
                        @change="entry.disabled = !entry.disabled"
                        class="sr-only peer"
                      />
                      <div
                        class="w-9 h-5 bg-gray-200 peer-focus:outline-none rounded-full peer dark:bg-gray-600 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all dark:after:border-gray-500 peer-checked:bg-primary-600"
                      ></div>
                    </label>
                    <input
                      v-model="entry.email"
                      type="email"
                      class="input flex-1"
                      :placeholder="
                        t('admin.settings.quotaNotify.emailPlaceholder')
                      "
                    />
                    <button
                      @click="form.account_quota_notify_emails.splice(index, 1)"
                      class="btn btn-secondary px-2"
                      type="button"
                    >
                      <Icon name="x" size="xs" class="h-4 w-4" />
                    </button>
                  </div>
                  <button
                    @click="addQuotaNotifyEmail"
                    class="btn btn-secondary btn-sm"
                    type="button"
                  >
                    + {{ t("admin.settings.quotaNotify.addEmail") }}
                  </button>
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.settings.quotaNotify.emailsHint") }}
                </p>
              </div>
            </div>
          </div>
        </div>
        <!-- /Tab: Email -->

        <!-- Tab: Backup -->
        <div v-show="activeTab === 'backup'">
          <BackupSettings />
        </div>

        <!-- Save Button -->
        <div v-show="activeTab !== 'backup'" class="flex justify-end">
          <button
            type="submit"
            :disabled="saving || loadFailed"
            class="btn btn-primary"
          >
            <svg
              v-if="saving"
              class="h-4 w-4 animate-spin"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              ></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            {{
              saving
                ? t("admin.settings.saving")
                : t("admin.settings.saveSettings")
            }}
          </button>
        </div>
      </form>

      <!-- Provider dialogs placed outside the settings form to prevent form submission bubbling -->
      <PaymentProviderDialog
        ref="providerDialogRef"
        :show="showProviderDialog"
        :saving="providerSaving"
        :editing="editingProvider"
        :all-key-options="providerKeyOptions"
        :enabled-key-options="enabledProviderKeyOptions"
        :all-payment-types="allPaymentTypes"
        :redirect-label="t('admin.settings.payment.easypayRedirect')"
        @close="showProviderDialog = false"
        @save="handleSaveProvider"
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
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, nextTick, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute } from "vue-router";
import { adminAPI } from "@/api";
import {
  appendAuthSourceDefaultsToUpdateRequest,
  buildAuthSourceDefaultsState,
  normalizePlatformQuotasMap,
  sanitizePlatformQuotasMap,
  defaultWeChatConnectScopesForMode,
  deriveWeChatConnectStoredMode,
  normalizeDefaultSubscriptionSettings,
  resolveWeChatConnectModeCapabilities,
} from "@/api/admin/settings";
import type {
  AuthSourceDefaultsState,
  AuthSourceType,
  SystemSettings,
  UpdateSettingsRequest,
  DefaultSubscriptionSetting,
  DefaultPlatformQuotasMap,
  OpenAIFastPolicyRule,
  WeChatConnectMode,
  WebSearchEmulationConfig,
  WebSearchProviderConfig,
  WebSearchTestResult,
  PaymentMethodFeeConfig,
  OpenAIQuotaAutoPauseSettings,
  UserPromptReplacementConfig,
  UserPromptReplacementRule,
  UserPromptReplacementType,
} from "@/api/admin/settings";
import type { LoginAgreementDocument, NotifyEmailEntry, Proxy } from "@/types";
import type { ProviderInstance, SubscriptionPlan } from "@/types/payment";
import AppLayout from "@/components/layout/AppLayout.vue";
import Icon from "@/components/icons/Icon.vue";
import Select from "@/components/common/Select.vue";
import ConfirmDialog from "@/components/common/ConfirmDialog.vue";
import PaymentProviderList from "@/components/payment/PaymentProviderList.vue";
import PaymentProviderDialog from "@/components/payment/PaymentProviderDialog.vue";
import Toggle from "@/components/common/Toggle.vue";
import ProxySelector from "@/components/common/ProxySelector.vue";
import ImageUpload from "@/components/common/ImageUpload.vue";
import BalanceIcon from "@/components/common/BalanceIcon.vue";
import OpenAIOAuthImportDefaultsSettings from "@/components/admin/account/OpenAIOAuthImportDefaultsSettings.vue";
import BackupSettings from "@/views/admin/BackupView.vue";
import { useBalanceDisplay } from "@/composables/useBalanceDisplay";
import EmailTemplateEditor from "@/views/admin/settings/EmailTemplateEditor.vue";
import { useClipboard } from "@/composables/useClipboard";
import { extractApiErrorMessage, extractI18nErrorMessage } from "@/utils/apiError";
import { useAppStore } from "@/stores";
import { useAdminSettingsStore } from "@/stores/adminSettings";
import { normalizeVisibleMethod } from "@/components/payment/paymentFlow";
import {
  isRegistrationEmailSuffixDomainValid,
  normalizeRegistrationEmailSuffixDomain,
  normalizeRegistrationEmailSuffixDomains,
  parseRegistrationEmailSuffixWhitelistInput,
} from "@/utils/registrationEmailPolicy";

const { t, locale } = useI18n();
const route = useRoute();
const appStore = useAppStore();
const adminSettingsStore = useAdminSettingsStore();

function localText(zh: string, en: string): string {
  return locale.value.startsWith("zh") ? zh : en;
}

const isZhLocale = computed(() => locale.value.startsWith("zh"));

const paymentGuideHref = computed(() =>
  locale.value.startsWith("zh")
    ? "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT_CN.md"
    : "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT.md",
);

const paymentMethodsHref = computed(() =>
  locale.value.startsWith("zh")
    ? "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT_CN.md#支持的支付方式"
    : "https://github.com/Wei-Shaw/sub2api/blob/main/docs/PAYMENT.md#supported-payment-methods",
);

type SettingsTab =
  | "general"
  | "agreement"
  | "features"
  | "security"
  | "users"
  | "gateway"
  | "payment"
  | "email"
  | "backup";
const settingsTabs = [
  { key: "general" as SettingsTab, icon: "home" as const },
  { key: "agreement" as SettingsTab, icon: "document" as const },
  { key: "features" as SettingsTab, icon: "bolt" as const },
  { key: "security" as SettingsTab, icon: "shield" as const },
  { key: "users" as SettingsTab, icon: "user" as const },
  { key: "gateway" as SettingsTab, icon: "server" as const },
  { key: "payment" as SettingsTab, icon: "creditCard" as const },
  { key: "email" as SettingsTab, icon: "mail" as const },
  { key: "backup" as SettingsTab, icon: "database" as const },
];
// 允许通过路由参数直接定位到指定标签。
const settingsTabKeys = new Set<SettingsTab>(settingsTabs.map((tab) => tab.key));
const initialSettingsTab = settingsTabKeys.has(route.query.tab as SettingsTab)
  ? (route.query.tab as SettingsTab)
  : "general";
const activeTab = ref<SettingsTab>(initialSettingsTab);
const settingsTabsScrollRef = ref<HTMLElement | null>(null);
// 支持方向键和 Home / End 在标签之间快速切换。
const settingsTabKeyboardActions = {
  ArrowLeft: -1,
  ArrowUp: -1,
  ArrowRight: 1,
  ArrowDown: 1,
  Home: "first",
  End: "last",
} as const;

function selectSettingsTab(tab: SettingsTab): void {
  activeTab.value = tab;
}

function focusSettingsTab(tab: SettingsTab): void {
  window.requestAnimationFrame(() => {
    document.getElementById(`settings-tab-${tab}`)?.focus();
  });
}

function handleSettingsTabKeydown(event: KeyboardEvent, tab: SettingsTab): void {
  const action =
    settingsTabKeyboardActions[
      event.key as keyof typeof settingsTabKeyboardActions
    ];
  if (action === undefined) {
    return;
  }

  event.preventDefault();
  const currentIndex = settingsTabs.findIndex((item) => item.key === tab);
  let nextIndex = currentIndex < 0 ? 0 : currentIndex;

  if (action === "first") {
    nextIndex = 0;
  } else if (action === "last") {
    nextIndex = settingsTabs.length - 1;
  } else {
    nextIndex =
      (nextIndex + action + settingsTabs.length) % settingsTabs.length;
  }

  const nextTab = settingsTabs[nextIndex]?.key;
  if (!nextTab) {
    return;
  }

  selectSettingsTab(nextTab);
  focusSettingsTab(nextTab);
}

const { copyToClipboard } = useClipboard();

// 切换标签后自动把选中项滚动到视口中央，避免横向滚动时贴边裁切。
function scrollActiveSettingsTabIntoView() {
  void nextTick(() => {
    const tabElement =
      settingsTabsScrollRef.value?.querySelector<HTMLElement>(
        ".settings-tab-active",
      );
    tabElement?.scrollIntoView?.({
      behavior: "smooth",
      block: "nearest",
      inline: "center",
    });
  });
}

watch(activeTab, scrollActiveSettingsTabIntoView, {
  immediate: true,
  flush: "post",
});

const loading = ref(true);
const loadFailed = ref(false);
const saving = ref(false);
const testingSmtp = ref(false);
const sendingTestEmail = ref(false);
const smtpPasswordManuallyEdited = ref(false);
const testEmailAddress = ref("");
const registrationEmailSuffixWhitelistTags = ref<string[]>([]);
const registrationEmailSuffixWhitelistDraft = ref("");
const tablePageSizeOptionsInput = ref("10, 20, 50, 100");
const paymentMethodFeeOptions = [
  { value: "stripe", label: "Stripe" },
  { value: "alipay", label: "支付宝" },
  { value: "wxpay", label: "微信支付" },
] as const;

// Admin API Key 状态
const adminApiKeyLoading = ref(true);
const adminApiKeyExists = ref(false);
const adminApiKeyMasked = ref("");
const adminApiKeyOperating = ref(false);
const newAdminApiKey = ref("");
const subscriptionPlans = ref<SubscriptionPlan[]>([]);

// Overload Cooldown (529) 状态
const overloadCooldownLoading = ref(true);
const overloadCooldownSaving = ref(false);
const overloadCooldownForm = reactive({
  enabled: true,
  cooldown_minutes: 10,
});

// OpenAI OAuth 403 Cooldown 状态
const openAI403CooldownLoading = ref(true);
const openAI403CooldownSaving = ref(false);
const openAI403CooldownForm = reactive({
  enabled: true,
  cooldown_minutes: 10,
  error_on_threshold_enabled: true,
  threshold_count: 3,
  threshold_window_minutes: 180,
});

// Rate Limit Cooldown (429) 状态
const rateLimit429CooldownLoading = ref(true);
const rateLimit429CooldownSaving = ref(false);
const rateLimit429CooldownForm = reactive({
  enabled: true,
  cooldown_seconds: 5,
});

// Stream Timeout 状态
const streamTimeoutLoading = ref(true);
const streamTimeoutSaving = ref(false);
const streamTimeoutForm = reactive({
  enabled: true,
  action: "temp_unsched" as "temp_unsched" | "error" | "none",
  temp_unsched_minutes: 5,
  threshold_count: 3,
  threshold_window_minutes: 10,
});

const streamTimeoutActionOptions = computed(() => [
  {
    value: "temp_unsched",
    label: t("admin.settings.streamTimeout.actionTempUnsched"),
  },
  { value: "error", label: t("admin.settings.streamTimeout.actionError") },
  { value: "none", label: t("admin.settings.streamTimeout.actionNone") },
]);

// Rectifier 状态
const rectifierLoading = ref(true);
const rectifierSaving = ref(false);
const rectifierForm = reactive({
  enabled: true,
  thinking_signature_enabled: true,
  thinking_budget_enabled: true,
  apikey_signature_enabled: false,
  apikey_signature_patterns: [] as string[],
});

// Beta Policy 状态
const betaPolicyLoading = ref(true);
const betaPolicySaving = ref(false);
const betaPolicyForm = reactive({
  rules: [] as Array<{
    beta_token: string;
    action: "pass" | "filter" | "block";
    scope: "all" | "oauth" | "apikey" | "bedrock";
    error_message?: string;
    model_whitelist?: string[];
    fallback_action?: "pass" | "filter" | "block";
    fallback_error_message?: string;
  }>,
});

// OpenAI Fast/Flex Policy 状态
const openaiFastPolicyForm = reactive({
  rules: [] as OpenAIFastPolicyRule[],
});
// 标记 openai_fast_policy_settings 是否已成功从后端加载，
// 避免后端 GET 出错或字段缺失时，保存把默认规则覆盖成空数组。
const openaiFastPolicyLoaded = ref(false);

const tablePageSizeMin = 5;
const tablePageSizeMax = 1000;
const tablePageSizeDefault = 20;
const usageRankingLimitMin = 1;
const usageRankingLimitMax = 100;
const usageRankingLimitDefault = 20;
const marketplaceAvailabilityWindowDaysMin = 1;
const marketplaceAvailabilityWindowDaysMax = 90;
const marketplaceAvailabilityWindowDaysDefault = 7;
const marketplaceAvailabilityBucketMinutesMin = 5;
const marketplaceAvailabilityBucketMinutesMax = 1440;
const marketplaceAvailabilityBucketMinutesDefault = 120;

function defaultLoginAgreementDocuments(): LoginAgreementDocument[] {
  return [
    {
      id: "terms",
      title: "服务条款",
      content_md: "",
    },
    {
      id: "usage-policy",
      title: "使用政策",
      content_md: "",
    },
    {
      id: "supported-regions",
      title: "支持的国家和地区",
      content_md: "",
    },
    {
      id: "service-specific-terms",
      title: "服务特定条款",
      content_md: "",
    },
  ];
}

// 默认规则与后端内置配置保持一致，便于新环境或旧后端缺字段时回显。
function defaultUserPromptReplacementConfig(): UserPromptReplacementConfig {
  return {
    enabled: true,
    rules: [
      {
        id: "environment-context-timezone-japan",
        name: "environment_context timezone -> Asia/Tokyo",
        enabled: true,
        pattern:
          "(?s)(<environment_context\\b[^>]*>.*?<timezone>)([^<]*)(</timezone>.*?</environment_context>)",
        target_group: 2,
        replacement_type: "timezone_name",
        scope: "environment_context",
        timezone: "Asia/Tokyo",
      },
      {
        id: "environment-context-current-date-japan",
        name: "environment_context current_date -> Asia/Tokyo today",
        enabled: true,
        pattern:
          "(?s)(<environment_context\\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?</environment_context>)",
        target_group: 2,
        replacement_type: "current_time",
        scope: "environment_context",
        timezone: "Asia/Tokyo",
        time_format: "2006-01-02",
      },
    ],
  };
}

// 后端可能返回旧格式或空值，这里统一整理为表单可直接绑定的结构。
function normalizeUserPromptReplacementConfig(
  raw: UserPromptReplacementConfig | null | undefined,
): UserPromptReplacementConfig {
  const source = raw ?? defaultUserPromptReplacementConfig();
  return {
    enabled: source.enabled !== false,
    rules: Array.isArray(source.rules)
      ? source.rules.map((rule, index) => ({
          id: String(rule.id || `rule-${index + 1}`).trim(),
          name: String(rule.name || rule.id || `Rule ${index + 1}`).trim(),
          enabled: rule.enabled !== false,
          pattern: String(rule.pattern || "").trim(),
          target_group: Number.isFinite(Number(rule.target_group))
            ? Math.max(0, Math.floor(Number(rule.target_group)))
            : 0,
          replacement_type: normalizeUserPromptReplacementType(
            rule.replacement_type,
          ),
          scope: String(rule.scope || "").trim(),
          static_text: String(rule.static_text ?? ""),
          timezone: String(rule.timezone || "Asia/Tokyo").trim(),
          time_format: String(rule.time_format || "2006-01-02").trim(),
        }))
      : [],
  };
}

// 替换类型只允许后端支持的三种内置值，未知值降级为固定文本。
function normalizeUserPromptReplacementType(
  value: unknown,
): UserPromptReplacementType {
  return value === "static" ||
    value === "timezone_name" ||
    value === "current_time"
    ? value
    : "static";
}

// 新增规则默认使用固定文本，避免管理员未配置动态参数时保存失败。
function createUserPromptReplacementRule(): UserPromptReplacementRule {
  return {
    id: `rule-${Date.now()}`,
    name: "",
    enabled: true,
    pattern: "",
    target_group: 0,
    replacement_type: "static",
    scope: "",
    static_text: "",
    timezone: "Asia/Tokyo",
    time_format: "2006-01-02",
  };
}

function normalizeLoginAgreementDocumentId(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/[-_]{2,}/g, "-")
    .replace(/^[-_]+|[-_]+$/g, "");
}

function loginAgreementRoutePath(
  doc: LoginAgreementDocument,
  index: number,
): string {
  const id =
    normalizeLoginAgreementDocumentId(doc.id || doc.title) || `doc-${index + 1}`;
  return `/legal/${id}`;
}

interface DefaultSubscriptionPlanOption {
  value: number;
  label: string;
  description: string | null;
  [key: string]: unknown;
}

type SettingsForm = Omit<
  SystemSettings,
  | "wechat_connect_open_enabled"
  | "wechat_connect_mp_enabled"
  | "wechat_connect_mobile_enabled"
> & {
  smtp_password: string;
  turnstile_secret_key: string;
  linuxdo_connect_client_secret: string;
  dingtalk_connect_client_secret: string;
  wechat_connect_app_secret: string;
  wechat_connect_open_app_secret: string;
  wechat_connect_mp_app_secret: string;
  wechat_connect_mobile_app_secret: string;
  wechat_connect_open_enabled: boolean;
  wechat_connect_mp_enabled: boolean;
  wechat_connect_mobile_enabled: boolean;
  oidc_connect_client_secret: string;
  github_oauth_client_secret: string;
  google_oauth_client_secret: string;
  force_email_on_third_party_signup: boolean;
  openai_advanced_scheduler_enabled: boolean;
  openai_advanced_scheduler_sticky_weighted_enabled: boolean;
  openai_advanced_scheduler_subscription_priority_enabled: boolean;
  openai_advanced_scheduler_lb_top_k: string;
  openai_advanced_scheduler_weight_priority: string;
  openai_advanced_scheduler_weight_load: string;
  openai_advanced_scheduler_weight_queue: string;
  openai_advanced_scheduler_weight_error_rate: string;
  openai_advanced_scheduler_weight_ttft: string;
  openai_advanced_scheduler_weight_reset: string;
  openai_advanced_scheduler_weight_quota_headroom: string;
  openai_advanced_scheduler_weight_previous_response: string;
  openai_advanced_scheduler_weight_session_sticky: string;
  openai_account_quota_auto_pause: OpenAIQuotaAutoPauseSettings;
  // 系统全局平台限额 map；form 内始终归一化为全平台对象（模板非空绑定依赖此不变量）
  default_platform_quotas: DefaultPlatformQuotasMap;
};

const form = reactive<SettingsForm>({
  registration_enabled: true,
  email_verify_enabled: false,
  registration_email_suffix_whitelist: [],
  registration_email_normalization: false,
  promo_code_enabled: true,
  invitation_code_enabled: false,
  password_reset_enabled: false,
  totp_enabled: false,
  totp_encryption_key_configured: false,
  login_agreement_enabled: false,
  login_agreement_mode: "modal",
  login_agreement_updated_at: "2026-03-31",
  login_agreement_documents: defaultLoginAgreementDocuments(),
  default_balance: 0,
  affiliate_enabled: false,
  default_platform_quotas: normalizePlatformQuotasMap() as DefaultPlatformQuotasMap,
  affiliate_rebate_rate: 20,
  affiliate_rebate_freeze_hours: 0,
  affiliate_rebate_duration_days: 0,
  affiliate_rebate_per_invitee_cap: 0,
  default_concurrency: 1,
  default_subscriptions: [],
  balance_unit_name: "USD",
  balance_unit_symbol: "$",
  balance_icon_svg: "",
  reasoning_point_rmb_unit_price: 0,
  usd_exchange_rate: 0,
  marketplace_availability_window_days:
    marketplaceAvailabilityWindowDaysDefault,
  marketplace_availability_bucket_minutes:
    marketplaceAvailabilityBucketMinutesDefault,
  force_email_on_third_party_signup: false,
  default_user_rpm_limit: 0,
  site_name: "Sub2API",
  site_logo: "",
  site_subtitle: "Subscription to API Conversion Platform",
  site_name_zh: "",
  site_name_en: "",
  site_title_zh: "",
  site_title_en: "",
  site_subtitle_zh: "",
  site_subtitle_en: "",
  api_base_url: "",
  contact_info: "",
  doc_url: "",
  home_content: "",
  backend_mode_enabled: false,
  hide_ccs_import_button: false,
  payment_enabled: false,
  payment_min_amount: 1,
  payment_max_amount: 10000,
  payment_daily_limit: 50000,
  payment_max_pending_orders: 3,
  payment_order_timeout_minutes: 30,
  payment_balance_disabled: false,
  payment_balance_recharge_multiplier: 1,
  payment_subscription_usd_to_cny_rate: 0,
  payment_recharge_fee_rate: 0,
  payment_method_fees: {},
  payment_enabled_types: [],
  payment_help_image_url: "",
  payment_help_text: "",
  payment_product_name_prefix: "",
  payment_product_name_suffix: "",
  payment_load_balance_strategy: "round-robin",
  payment_cancel_rate_limit_enabled: false,
  payment_cancel_rate_limit_max: 10,
  payment_cancel_rate_limit_window: 1,
  payment_cancel_rate_limit_unit: "day",
  payment_cancel_rate_limit_window_mode: "rolling",
  payment_alipay_force_qrcode: false,
  table_default_page_size: tablePageSizeDefault,
  table_page_size_options: [10, 20, 50, 100],
  usage_ranking_limit: usageRankingLimitDefault,
  custom_menu_items: [] as Array<{
    id: string;
    label: string;
    icon_svg: string;
    url: string;
    visibility: "user" | "admin";
    sort_order: number;
  }>,
  custom_endpoints: [] as Array<{
    name: string;
    endpoint: string;
    description: string;
  }>,
  footer_links: [] as Array<{
    title: string;
    links: Array<{ label: string; url: string }>;
  }>,
  footer_text: "",
  frontend_url: "",
  smtp_host: "",
  smtp_port: 587,
  smtp_username: "",
  smtp_password: "",
  smtp_password_configured: false,
  smtp_from_email: "",
  smtp_from_name: "",
  smtp_use_tls: true,
  // Cloudflare Turnstile
  turnstile_enabled: false,
  turnstile_site_key: "",
  turnstile_secret_key: "",
  turnstile_secret_key_configured: false,
  api_key_acl_trust_forwarded_ip: false,
  // LinuxDo Connect OAuth 登录
  linuxdo_connect_enabled: false,
  linuxdo_connect_client_id: "",
  linuxdo_connect_client_secret: "",
  linuxdo_connect_client_secret_configured: false,
  linuxdo_connect_redirect_url: "",
  // 钉钉 Connect OAuth 登录
  dingtalk_connect_enabled: false,
  dingtalk_connect_client_id: "",
  dingtalk_connect_client_secret: "",
  dingtalk_connect_client_secret_configured: false,
  dingtalk_connect_redirect_url: "",
  dingtalk_connect_corp_restriction_policy: "none",
  dingtalk_connect_internal_corp_id: "",
  dingtalk_connect_bypass_registration: false,
  dingtalk_connect_sync_corp_email: false,
  dingtalk_connect_sync_display_name: false,
  dingtalk_connect_sync_dept: false,
  dingtalk_connect_sync_corp_email_attr_key: "dingtalk_email",
  dingtalk_connect_sync_display_name_attr_key: "dingtalk_name",
  dingtalk_connect_sync_dept_attr_key: "dingtalk_department",
  dingtalk_connect_sync_corp_email_attr_name: "钉钉企业邮箱",
  dingtalk_connect_sync_display_name_attr_name: "钉钉姓名",
  dingtalk_connect_sync_dept_attr_name: "钉钉部门",
  wechat_connect_enabled: false,
  wechat_connect_app_id: "",
  wechat_connect_app_secret: "",
  wechat_connect_app_secret_configured: false,
  wechat_connect_open_app_id: "",
  wechat_connect_open_app_secret: "",
  wechat_connect_open_app_secret_configured: false,
  wechat_connect_mp_app_id: "",
  wechat_connect_mp_app_secret: "",
  wechat_connect_mp_app_secret_configured: false,
  wechat_connect_mobile_app_id: "",
  wechat_connect_mobile_app_secret: "",
  wechat_connect_mobile_app_secret_configured: false,
  wechat_connect_open_enabled: false,
  wechat_connect_mp_enabled: false,
  wechat_connect_mobile_enabled: false,
  wechat_connect_mode: "open",
  wechat_connect_scopes: "snsapi_login",
  wechat_connect_redirect_url: "",
  wechat_connect_frontend_redirect_url: "/auth/wechat/callback",
  // Generic OIDC OAuth 登录
  oidc_connect_enabled: false,
  oidc_connect_provider_name: "OIDC",
  oidc_connect_client_id: "",
  oidc_connect_client_secret: "",
  oidc_connect_client_secret_configured: false,
  oidc_connect_issuer_url: "",
  oidc_connect_discovery_url: "",
  oidc_connect_authorize_url: "",
  oidc_connect_token_url: "",
  oidc_connect_userinfo_url: "",
  oidc_connect_jwks_url: "",
  oidc_connect_scopes: "openid email profile",
  oidc_connect_redirect_url: "",
  oidc_connect_frontend_redirect_url: "/auth/oidc/callback",
  oidc_connect_token_auth_method: "client_secret_post",
  oidc_connect_use_pkce: false,
  oidc_connect_validate_id_token: false,
  oidc_connect_allowed_signing_algs: "RS256,ES256,PS256",
  oidc_connect_clock_skew_seconds: 120,
  oidc_connect_require_email_verified: false,
  oidc_connect_userinfo_email_path: "",
  oidc_connect_userinfo_id_path: "",
  oidc_connect_userinfo_username_path: "",
  // GitHub / Google 邮箱快捷登录
  github_oauth_enabled: false,
  github_oauth_client_id: "",
  github_oauth_client_secret: "",
  github_oauth_client_secret_configured: false,
  github_oauth_redirect_url: "",
  github_oauth_frontend_redirect_url: "/auth/oauth/callback",
  google_oauth_enabled: false,
  google_oauth_client_id: "",
  google_oauth_client_secret: "",
  google_oauth_client_secret_configured: false,
  google_oauth_redirect_url: "",
  google_oauth_frontend_redirect_url: "/auth/oauth/callback",
  // Model fallback
  enable_model_fallback: false,
  fallback_model_anthropic: "claude-3-5-sonnet-20241022",
  fallback_model_openai: "gpt-4o",
  fallback_model_gemini: "gemini-2.5-pro",
  fallback_model_antigravity: "gemini-2.5-pro",
  // Identity patch (Claude -> Gemini)
  enable_identity_patch: true,
  identity_patch_prompt: "",
  // Ops monitoring (vNext)
  ops_monitoring_enabled: true,
  ops_realtime_monitoring_enabled: true,
  ops_query_mode_default: "auto",
  ops_metrics_interval_seconds: 60,
  // Claude Code version check
  min_claude_code_version: "",
  max_claude_code_version: "",
  // 分组隔离
  allow_ungrouped_key_scheduling: false,
  openai_advanced_scheduler_enabled: false,
  openai_advanced_scheduler_sticky_weighted_enabled: false,
  openai_advanced_scheduler_subscription_priority_enabled: false,
  openai_advanced_scheduler_lb_top_k: "",
  openai_advanced_scheduler_weight_priority: "",
  openai_advanced_scheduler_weight_load: "",
  openai_advanced_scheduler_weight_queue: "",
  openai_advanced_scheduler_weight_error_rate: "",
  openai_advanced_scheduler_weight_ttft: "",
  openai_advanced_scheduler_weight_reset: "",
  openai_advanced_scheduler_weight_quota_headroom: "",
  openai_advanced_scheduler_weight_previous_response: "",
  openai_advanced_scheduler_weight_session_sticky: "",
  openai_account_quota_auto_pause: {
    default_threshold_5h: 0,
    default_threshold_7d: 0,
  },
  // Gateway forwarding behavior
  enable_fingerprint_unification: true,
  enable_metadata_passthrough: false,
  enable_cch_signing: false,
  enable_claude_oauth_system_prompt_injection: true,
  claude_oauth_system_prompt: "",
  claude_oauth_system_prompt_blocks: "",
  enable_anthropic_cache_ttl_1h_injection: false,
  rewrite_message_cache_control: false,
  enable_client_dateline_normalization: true,
  risk_control_enabled: false,
  cyber_session_block_enabled: false,
  cyber_session_block_ttl_seconds: 3600,
  antigravity_user_agent_version: "",
  openai_codex_user_agent: "",
  openai_allow_claude_code_codex_plugin: false,
  user_prompt_replacement_config: defaultUserPromptReplacementConfig(),
  // 余额、订阅到期与账号限额通知
  balance_low_notify_enabled: false,
  balance_low_notify_threshold: 0,
  balance_low_notify_recharge_url: "",
  subscription_expiry_notify_enabled: true,
  account_quota_notify_enabled: false,
  account_quota_notify_emails: [] as NotifyEmailEntry[],
  allow_user_view_error_requests: false,
});

const {
  balanceUnitName: previewBalanceUnitName,
  balanceUnitSymbol: previewBalanceUnitSymbol,
  formatBalanceAmount: formatPreviewBalanceAmount,
} = useBalanceDisplay({
  unitName: computed(() => form.balance_unit_name),
  unitSymbol: computed(() => form.balance_unit_symbol),
  iconSvg: computed(() => form.balance_icon_svg),
});
const previewBalanceAmount = computed(() => formatPreviewBalanceAmount(123.45));

function quotaThresholdToPercent(value: number | undefined): number | null {
  if (!value || value <= 0) return null;
  return Math.round(value * 1000) / 10;
}

function percentToQuotaThreshold(value: number | null): number {
  return value != null && value > 0 ? value / 100 : 0;
}

// OpenAI 配额自动暂停在后端以 0~1 存储，系统设置页按百分比展示。
const openAIQuotaAutoPause5hPercent = computed<number | null>({
  get() {
    return quotaThresholdToPercent(form.openai_account_quota_auto_pause?.default_threshold_5h);
  },
  set(value) {
    form.openai_account_quota_auto_pause.default_threshold_5h = percentToQuotaThreshold(value);
  },
});

const openAIQuotaAutoPause7dPercent = computed<number | null>({
  get() {
    return quotaThresholdToPercent(form.openai_account_quota_auto_pause?.default_threshold_7d);
  },
  set(value) {
    form.openai_account_quota_auto_pause.default_threshold_7d = percentToQuotaThreshold(value);
  },
});

const oidcTokenAuthMethodOptions = [
  { value: "client_secret_post", label: "client_secret_post" },
  { value: "client_secret_basic", label: "client_secret_basic" },
  { value: "none", label: "none" },
];

const customMenuVisibilityOptions = computed(() => [
  { value: "user", label: t("admin.settings.customMenu.visibilityUser") },
  { value: "admin", label: t("admin.settings.customMenu.visibilityAdmin") },
]);

const userPromptReplacementTypeOptions = computed(() => [
  {
    value: "static",
    label: t("admin.settings.userPromptReplacement.typeStatic"),
  },
  {
    value: "timezone_name",
    label: t("admin.settings.userPromptReplacement.typeTimezoneName"),
  },
  {
    value: "current_time",
    label: t("admin.settings.userPromptReplacement.typeCurrentTime"),
  },
]);

const userPromptReplacementTimezoneOptions = [
  { value: "Asia/Tokyo", label: "Asia/Tokyo" },
  { value: "Asia/Shanghai", label: "Asia/Shanghai" },
  { value: "UTC", label: "UTC" },
  { value: "America/Los_Angeles", label: "America/Los_Angeles" },
  { value: "America/New_York", label: "America/New_York" },
  { value: "Europe/London", label: "Europe/London" },
];

function addUserPromptReplacementRule(): void {
  form.user_prompt_replacement_config.rules.push(
    createUserPromptReplacementRule(),
  );
}

function removeUserPromptReplacementRule(index: number): void {
  form.user_prompt_replacement_config.rules.splice(index, 1);
}

function resetUserPromptReplacementRules(): void {
  form.user_prompt_replacement_config = defaultUserPromptReplacementConfig();
}

function defaultPaymentMethodFee(enabled = false): PaymentMethodFeeConfig {
  return { enabled, fixed_fee: 0, fee_rate: 0 };
}

function ensurePaymentMethodFee(method: string): PaymentMethodFeeConfig {
  if (!form.payment_method_fees) form.payment_method_fees = {};
  if (!form.payment_method_fees[method]) {
    form.payment_method_fees[method] = defaultPaymentMethodFee(false);
  }
  return form.payment_method_fees[method];
}

function methodFeeEnabled(method: string): boolean {
  return ensurePaymentMethodFee(method).enabled;
}

function setMethodFeeEnabled(method: string, enabled: boolean) {
  ensurePaymentMethodFee(method).enabled = enabled;
}

function normalizeFeeInput(value: string, max = Number.POSITIVE_INFINITY): number {
  const parsed = Number.parseFloat(value || "0");
  if (!Number.isFinite(parsed)) return 0;
  return Math.min(max, Math.max(0, Math.round(parsed * 100) / 100));
}

function methodFeeValue(method: string, field: "fixed_fee" | "fee_rate"): number {
  return ensurePaymentMethodFee(method)[field] || 0;
}

function setMethodFeeValue(method: string, field: "fixed_fee" | "fee_rate", value: string) {
  ensurePaymentMethodFee(method)[field] = normalizeFeeInput(value, field === "fee_rate" ? 100 : Number.POSITIVE_INFINITY);
}

function methodFeePreview(method: string): string {
  const cfg = ensurePaymentMethodFee(method);
  const base = 100;
  const rateFee = Math.ceil(((base * (Number(cfg.fee_rate) || 0)) / 100) * 100 - 1e-9) / 100;
  const totalFee = Math.round(((Number(cfg.fixed_fee) || 0) + rateFee + Number.EPSILON) * 100) / 100;
  return t("admin.settings.payment.methodFeePreview", {
    fee: totalFee.toFixed(2),
    amount: (base + totalFee).toFixed(2),
  });
}


type OpenAIAdvancedSchedulerOverrideKey =
  | "openai_advanced_scheduler_lb_top_k"
  | "openai_advanced_scheduler_weight_priority"
  | "openai_advanced_scheduler_weight_load"
  | "openai_advanced_scheduler_weight_queue"
  | "openai_advanced_scheduler_weight_error_rate"
  | "openai_advanced_scheduler_weight_ttft"
  | "openai_advanced_scheduler_weight_reset"
  | "openai_advanced_scheduler_weight_quota_headroom"
  | "openai_advanced_scheduler_weight_previous_response"
  | "openai_advanced_scheduler_weight_session_sticky";

type OpenAIAdvancedSchedulerEffectiveKey =
  | "openai_advanced_scheduler_effective_lb_top_k"
  | "openai_advanced_scheduler_effective_weight_priority"
  | "openai_advanced_scheduler_effective_weight_load"
  | "openai_advanced_scheduler_effective_weight_queue"
  | "openai_advanced_scheduler_effective_weight_error_rate"
  | "openai_advanced_scheduler_effective_weight_ttft"
  | "openai_advanced_scheduler_effective_weight_reset"
  | "openai_advanced_scheduler_effective_weight_quota_headroom"
  | "openai_advanced_scheduler_effective_weight_previous_response"
  | "openai_advanced_scheduler_effective_weight_session_sticky";

const openAIAdvancedSchedulerWeightFields = computed<
  Array<{
    key: OpenAIAdvancedSchedulerOverrideKey;
    label: string;
    placeholder: string;
  }>
>(() => {
  const placeholder = (
    effectiveKey: OpenAIAdvancedSchedulerEffectiveKey,
    fallbackValue: string,
  ) => {
    const effectiveValue = String(
      (form as Record<string, unknown>)[effectiveKey] ?? "",
    ).trim();
    return t("admin.settings.openaiExperimentalScheduler.defaultPlaceholder", {
      value: effectiveValue || fallbackValue,
    });
  };

  return [
    {
      key: "openai_advanced_scheduler_lb_top_k",
      label: t("admin.settings.openaiExperimentalScheduler.topKLabel"),
      placeholder: placeholder("openai_advanced_scheduler_effective_lb_top_k", "7"),
    },
    {
      key: "openai_advanced_scheduler_weight_priority",
      label: t("admin.settings.openaiExperimentalScheduler.priorityWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_priority", "1"),
    },
    {
      key: "openai_advanced_scheduler_weight_load",
      label: t("admin.settings.openaiExperimentalScheduler.loadWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_load", "1"),
    },
    {
      key: "openai_advanced_scheduler_weight_queue",
      label: t("admin.settings.openaiExperimentalScheduler.queueWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_queue", "0.7"),
    },
    {
      key: "openai_advanced_scheduler_weight_error_rate",
      label: t("admin.settings.openaiExperimentalScheduler.errorRateWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_error_rate", "0.8"),
    },
    {
      key: "openai_advanced_scheduler_weight_ttft",
      label: t("admin.settings.openaiExperimentalScheduler.ttftWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_ttft", "0.5"),
    },
    {
      key: "openai_advanced_scheduler_weight_reset",
      label: t("admin.settings.openaiExperimentalScheduler.resetWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_reset", "0"),
    },
    {
      key: "openai_advanced_scheduler_weight_quota_headroom",
      label: t("admin.settings.openaiExperimentalScheduler.quotaHeadroomWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_quota_headroom", "0"),
    },
    {
      key: "openai_advanced_scheduler_weight_previous_response",
      label: t("admin.settings.openaiExperimentalScheduler.previousResponseWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_previous_response", "5"),
    },
    {
      key: "openai_advanced_scheduler_weight_session_sticky",
      label: t("admin.settings.openaiExperimentalScheduler.sessionStickyWeight"),
      placeholder: placeholder("openai_advanced_scheduler_effective_weight_session_sticky", "3"),
    },
  ];
});

const authSourceDefaults = reactive<AuthSourceDefaultsState>(
  buildAuthSourceDefaultsState({}),
);

const authSourceDefaultsMeta = computed(() => [
  {
    source: "email" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.email.title"),
    description: t("admin.settings.authSourceDefaults.sources.email.description"),
  },
  {
    source: "linuxdo" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.linuxdo.title"),
    description: t("admin.settings.authSourceDefaults.sources.linuxdo.description"),
  },
  {
    source: "oidc" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.oidc.title"),
    description: t("admin.settings.authSourceDefaults.sources.oidc.description"),
  },
  {
    source: "wechat" as AuthSourceType,
    title: t("admin.settings.authSourceDefaults.sources.wechat.title"),
    description: t("admin.settings.authSourceDefaults.sources.wechat.description"),
  },
  {
    source: "github" as AuthSourceType,
    title: "GitHub",
    description: localText("GitHub 邮箱快捷登录注册或首次绑定。", "GitHub email OAuth signup or first bind."),
  },
  {
    source: "google" as AuthSourceType,
    title: "Google",
    description: localText("Google 邮箱快捷登录注册或首次绑定。", "Google email OAuth signup or first bind."),
  },
  {
    source: "dingtalk" as AuthSourceType,
    title: "钉钉",
    description: localText(
      "通过钉钉首次注册或首次绑定时应用。",
      "Applied on first signup or first bind through DingTalk.",
    ),
  },
]);

// Proxies for web search emulation ProxySelector
const webSearchProxies = ref<Proxy[]>([]);

// Web Search Emulation config (loaded/saved separately)
const DEFAULT_WEB_SEARCH_QUOTA_LIMIT = 1000;

const webSearchConfig = reactive<WebSearchEmulationConfig>({
  enabled: false,
  providers: [],
});

const expandedProviders = reactive<Record<number, boolean>>({});
const apiKeyVisible = reactive<Record<number, boolean>>({});
const wsTestQuery = ref("");
const wsTestLoading = ref(false);
const wsTestResult = ref<WebSearchTestResult | null>(null);
const wsTestDialogOpen = ref(false);

function openTestDialog() {
  wsTestResult.value = null;
  wsTestDialogOpen.value = true;
}

function toggleProviderExpand(idx: number) {
  expandedProviders[idx] = !expandedProviders[idx];
}

function removeWebSearchProvider(idx: number) {
  webSearchConfig.providers.splice(idx, 1);
  // Re-index expandedProviders and apiKeyVisible after removal
  const newExpanded: Record<number, boolean> = {};
  const newVisible: Record<number, boolean> = {};
  for (let i = 0; i < webSearchConfig.providers.length; i++) {
    const oldIdx = i >= idx ? i + 1 : i;
    newExpanded[i] = expandedProviders[oldIdx] ?? false;
    newVisible[i] = apiKeyVisible[oldIdx] ?? false;
  }
  Object.keys(expandedProviders).forEach(
    (k) => delete expandedProviders[Number(k)],
  );
  Object.keys(apiKeyVisible).forEach((k) => delete apiKeyVisible[Number(k)]);
  Object.assign(expandedProviders, newExpanded);
  Object.assign(apiKeyVisible, newVisible);
}

function addWebSearchProvider() {
  const idx = webSearchConfig.providers.length;
  webSearchConfig.providers.push({
    type: "brave",
    api_key: "",
    api_key_configured: false,
    quota_limit: DEFAULT_WEB_SEARCH_QUOTA_LIMIT,
    subscribed_at: null,
    proxy_id: null,
    expires_at: null,
  } as WebSearchProviderConfig);
  expandedProviders[idx] = true;
}

function formatSubscribedAt(ts: number | null): string {
  if (!ts) return "";
  // Use UTC to avoid timezone drift on repeated edits
  const d = new Date(ts * 1000);
  const y = d.getUTCFullYear();
  const m = String(d.getUTCMonth() + 1).padStart(2, "0");
  const day = String(d.getUTCDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function parseSubscribedAt(dateStr: string): number | null {
  if (!dateStr) return null;
  // Parse as UTC to match formatSubscribedAt
  return Math.floor(new Date(dateStr + "T00:00:00Z").getTime() / 1000);
}

function quotaPercentage(provider: WebSearchProviderConfig): number {
  if (!provider.quota_limit || provider.quota_limit <= 0) return 0;
  return ((provider.quota_used ?? 0) / provider.quota_limit) * 100;
}

async function resetWebSearchUsage(idx: number) {
  const provider = webSearchConfig.providers[idx];
  if (!provider) return;
  if (!confirm(t("admin.settings.webSearchEmulation.resetUsageConfirm")))
    return;
  try {
    await adminAPI.settings.resetWebSearchUsage({
      provider_type: provider.type,
    });
    provider.quota_used = 0;
    appStore.showSuccess(
      t("admin.settings.webSearchEmulation.resetUsageSuccess"),
    );
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  }
}

async function copyApiKey(idx: number) {
  const key = webSearchConfig.providers[idx]?.api_key;
  if (!key) {
    appStore.showError(
      t("admin.settings.webSearchEmulation.apiKeyPlaceholder"),
    );
    return;
  }
  try {
    await navigator.clipboard.writeText(key);
    appStore.showSuccess(t("admin.settings.webSearchEmulation.copied"));
  } catch {
    appStore.showError(t("common.error"));
  }
}

async function testWebSearchProvider() {
  wsTestLoading.value = true;
  wsTestResult.value = null;
  try {
    const query =
      wsTestQuery.value.trim() ||
      t("admin.settings.webSearchEmulation.testDefaultQuery");
    wsTestResult.value = await adminAPI.settings.testWebSearchEmulation(query);
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
  } finally {
    wsTestLoading.value = false;
  }
}

async function loadWebSearchConfig() {
  try {
    const [resp, proxiesResp] = await Promise.all([
      adminAPI.settings.getWebSearchEmulationConfig(),
      adminAPI.proxies.list().catch(() => ({ items: [] as Proxy[] })),
    ]);
    if (resp) {
      webSearchConfig.enabled = resp.enabled || false;
      webSearchConfig.providers = resp.providers || [];
    }
    webSearchProxies.value = proxiesResp.items || [];
  } catch (err: unknown) {
    // 404 is expected when config hasn't been created yet; show error for other failures
    const status = (err as { status?: number })?.status;
    if (status !== 404 && status !== undefined) {
      appStore.showError(extractApiErrorMessage(err, t("common.error")));
    }
  }
}

async function saveWebSearchConfig(): Promise<boolean> {
  try {
    for (const p of webSearchConfig.providers) {
      const raw = p.quota_limit;
      if (raw != null && Number(raw) !== 0 && Number(raw) < 1) {
        appStore.showError(
          t("admin.settings.webSearchEmulation.quotaLimitMustBePositive"),
        );
        return false;
      }
    }
    const providers = webSearchConfig.providers.map(
      (p: WebSearchProviderConfig) => ({
        ...p,
        quota_limit: Number(p.quota_limit) > 0 ? Number(p.quota_limit) : null,
      }),
    );
    await adminAPI.settings.updateWebSearchEmulationConfig({
      enabled: webSearchConfig.enabled,
      providers,
    });
    return true;
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t("common.error")));
    return false;
  }
}

const defaultSubscriptionPlanOptions = computed<
  DefaultSubscriptionPlanOption[]
>(() =>
  subscriptionPlans.value.map((plan) => ({
    value: plan.id,
    label: plan.name,
    description: plan.description,
  })),
);

const registrationEmailSuffixWhitelistSeparatorKeys = new Set([
  " ",
  ",",
  "，",
  "Enter",
  "Tab",
]);

function removeRegistrationEmailSuffixWhitelistTag(suffix: string) {
  registrationEmailSuffixWhitelistTags.value =
    registrationEmailSuffixWhitelistTags.value.filter(
      (item) => item !== suffix,
    );
}

function addRegistrationEmailSuffixWhitelistTag(raw: string) {
  const suffix = normalizeRegistrationEmailSuffixDomain(raw);
  if (
    !isRegistrationEmailSuffixDomainValid(suffix) ||
    registrationEmailSuffixWhitelistTags.value.includes(suffix)
  ) {
    return;
  }
  registrationEmailSuffixWhitelistTags.value = [
    ...registrationEmailSuffixWhitelistTags.value,
    suffix,
  ];
}

function commitRegistrationEmailSuffixWhitelistDraft() {
  if (!registrationEmailSuffixWhitelistDraft.value) {
    return;
  }
  addRegistrationEmailSuffixWhitelistTag(
    registrationEmailSuffixWhitelistDraft.value,
  );
  registrationEmailSuffixWhitelistDraft.value = "";
}

function handleRegistrationEmailSuffixWhitelistDraftInput() {
  registrationEmailSuffixWhitelistDraft.value =
    normalizeRegistrationEmailSuffixDomain(
      registrationEmailSuffixWhitelistDraft.value,
    );
}

function handleRegistrationEmailSuffixWhitelistDraftKeydown(
  event: KeyboardEvent,
) {
  if (event.isComposing) {
    return;
  }

  if (registrationEmailSuffixWhitelistSeparatorKeys.has(event.key)) {
    event.preventDefault();
    commitRegistrationEmailSuffixWhitelistDraft();
    return;
  }

  if (
    event.key === "Backspace" &&
    !registrationEmailSuffixWhitelistDraft.value &&
    registrationEmailSuffixWhitelistTags.value.length > 0
  ) {
    registrationEmailSuffixWhitelistTags.value.pop();
  }
}

function handleRegistrationEmailSuffixWhitelistPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData("text") || "";
  if (!text.trim()) {
    return;
  }
  event.preventDefault();
  const tokens = parseRegistrationEmailSuffixWhitelistInput(text);
  for (const token of tokens) {
    addRegistrationEmailSuffixWhitelistTag(token);
  }
}

// Quota notify email helpers
const addQuotaNotifyEmail = () => {
  if (!form.account_quota_notify_emails) {
    form.account_quota_notify_emails = [];
  }
  form.account_quota_notify_emails.push({
    email: "",
    disabled: false,
    verified: true,
  });
};

const currentOrigin =
  typeof window !== "undefined" ? window.location.origin : "";

function buildApiCallbackUrl(path: string): string {
  const base = (form.api_base_url || currentOrigin).replace(/\/+$/, "");
  const apiRoot = base.endsWith("/api/v1") ? base : `${base}/api/v1`;
  return `${apiRoot}${path.startsWith("/") ? path : `/${path}`}`;
}

// LinuxDo OAuth redirect URL suggestion
const linuxdoRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/linuxdo/callback");
});

async function setAndCopyLinuxdoRedirectUrl() {
  const url = linuxdoRedirectUrlSuggestion.value;
  if (!url) return;

  form.linuxdo_connect_redirect_url = url;
  await copyToClipboard(
    url,
    t("admin.settings.linuxdo.redirectUrlSetAndCopied"),
  );
}

type EmailOAuthProvider = "github" | "google";

const githubOAuthRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/github/callback");
});

const googleOAuthRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/google/callback");
});

async function setAndCopyEmailOAuthRedirectUrl(provider: EmailOAuthProvider) {
  const url =
    provider === "github"
      ? githubOAuthRedirectUrlSuggestion.value
      : googleOAuthRedirectUrlSuggestion.value;
  if (!url) return;

  if (provider === "github") {
    form.github_oauth_redirect_url = url;
  } else {
    form.google_oauth_redirect_url = url;
  }
  await copyToClipboard(url, localText("回调地址已生成并复制。", "Callback URL generated and copied."));
}

const wechatRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/wechat/callback");
});

function syncWeChatConnectMode(preferredMode?: WeChatConnectMode) {
  if (form.wechat_connect_mp_enabled && form.wechat_connect_mobile_enabled) {
    if (preferredMode === "mobile") {
      form.wechat_connect_mp_enabled = false;
    } else {
      form.wechat_connect_mobile_enabled = false;
    }
  }

  const capabilities = resolveWeChatConnectModeCapabilities(
    form.wechat_connect_open_enabled,
    form.wechat_connect_mp_enabled,
    form.wechat_connect_mobile_enabled,
    form.wechat_connect_mode,
  );
  form.wechat_connect_open_enabled = capabilities.openEnabled;
  form.wechat_connect_mp_enabled = capabilities.mpEnabled;
  form.wechat_connect_mobile_enabled = capabilities.mobileEnabled;
  form.wechat_connect_mode = deriveWeChatConnectStoredMode(
    capabilities.openEnabled,
    capabilities.mpEnabled,
    capabilities.mobileEnabled,
    form.wechat_connect_mode,
  );
  form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(
    form.wechat_connect_mode,
  );
}

function handleWeChatOpenEnabledChange(value: boolean) {
  form.wechat_connect_open_enabled = value;
  syncWeChatConnectMode(value ? "open" : undefined);
}

function handleWeChatMPEnabledChange(value: boolean) {
  form.wechat_connect_mp_enabled = value;
  if (value) {
    form.wechat_connect_mobile_enabled = false;
  }
  syncWeChatConnectMode(value ? "mp" : undefined);
}

function handleWeChatMobileEnabledChange(value: boolean) {
  form.wechat_connect_mobile_enabled = value;
  if (value) {
    form.wechat_connect_mp_enabled = false;
  }
  syncWeChatConnectMode(value ? "mobile" : undefined);
}

async function setAndCopyWeChatRedirectUrl() {
  const url = wechatRedirectUrlSuggestion.value;
  if (!url) return;

  form.wechat_connect_redirect_url = url;
  await copyToClipboard(
    url,
    t("admin.settings.wechatConnect.redirectUrlSetAndCopied"),
  );
}

const oidcRedirectUrlSuggestion = computed(() => {
  return buildApiCallbackUrl("/auth/oauth/oidc/callback");
});

async function setAndCopyOIDCRedirectUrl() {
  const url = oidcRedirectUrlSuggestion.value;
  if (!url) return;

  form.oidc_connect_redirect_url = url;
  await copyToClipboard(url, t("admin.settings.oidc.redirectUrlSetAndCopied"));
}

// Custom menu item management
function addMenuItem() {
  form.custom_menu_items.push({
    id: "",
    label: "",
    icon_svg: "",
    url: "",
    visibility: "user",
    sort_order: form.custom_menu_items.length,
  });
}

function removeMenuItem(index: number) {
  form.custom_menu_items.splice(index, 1);
  // Re-index sort_order
  form.custom_menu_items.forEach((item, i) => {
    item.sort_order = i;
  });
}

function moveMenuItem(index: number, direction: -1 | 1) {
  const targetIndex = index + direction;
  if (targetIndex < 0 || targetIndex >= form.custom_menu_items.length) return;
  const items = form.custom_menu_items;
  const temp = items[index];
  items[index] = items[targetIndex];
  items[targetIndex] = temp;
  // Re-index sort_order
  items.forEach((item, i) => {
    item.sort_order = i;
  });
}

// Custom endpoint management
function addEndpoint() {
  form.custom_endpoints.push({ name: "", endpoint: "", description: "" });
}

function removeEndpoint(index: number) {
  form.custom_endpoints.splice(index, 1);
}

// Footer link group management
function addFooterGroup() {
  form.footer_links.push({ title: "", links: [{ label: "", url: "" }] });
}

// 默认底栏模板:基于站内已有页面 + 已配置的文档/联系方式,可直接用或小改
function applyDefaultFooterLinks() {
  const zh = locale.value.toLowerCase().startsWith("zh");
  const groups: Array<{ title: string; links: Array<{ label: string; url: string }> }> = [
    {
      title: zh ? "产品" : "Product",
      links: [
        { label: zh ? "模型广场" : "Models", url: "/models" },
        { label: zh ? "用量查询" : "Key usage", url: "/key-usage" },
        { label: zh ? "控制台" : "Dashboard", url: "/dashboard" },
      ],
    },
    {
      title: zh ? "开发者" : "Developer",
      links: [
        ...(form.doc_url ? [{ label: zh ? "文档" : "Documentation", url: form.doc_url }] : []),
        ...(form.api_base_url
          ? [{ label: "API Base URL", url: form.api_base_url }]
          : []),
      ],
    },
    {
      title: zh ? "支持" : "Support",
      links: [
        { label: zh ? "登录 / 注册" : "Sign in / Sign up", url: "/login" },
      ],
    },
  ];
  form.footer_links = groups.filter((g) => g.links.length > 0);
}

function removeFooterGroup(index: number) {
  form.footer_links.splice(index, 1);
}

function moveFooterGroup(index: number, direction: -1 | 1) {
  const targetIndex = index + direction;
  if (targetIndex < 0 || targetIndex >= form.footer_links.length) return;
  const groups = form.footer_links;
  const temp = groups[index];
  groups[index] = groups[targetIndex];
  groups[targetIndex] = temp;
}

// 保存前清理:去掉空链接行和无标题且无链接的分组
function normalizeFooterLinksForSave() {
  return form.footer_links
    .map((group) => ({
      title: group.title.trim(),
      links: group.links
        .map((link) => ({ label: link.label.trim(), url: link.url.trim() }))
        .filter((link) => link.label && link.url),
    }))
    .filter((group) => group.title && group.links.length > 0);
}

function addLoginAgreementDocument() {
  form.login_agreement_documents.push({
    id: `custom-${Date.now().toString(36)}`,
    title: "",
    content_md: "",
  });
}

function removeLoginAgreementDocument(index: number) {
  form.login_agreement_documents.splice(index, 1);
}

function normalizeLoginAgreementDocumentsForSave(): LoginAgreementDocument[] {
  return form.login_agreement_documents
    .map((doc, index) => ({
      id:
        normalizeLoginAgreementDocumentId(doc.id || doc.title) ||
        `doc-${index + 1}`,
      title: doc.title.trim(),
      content_md: doc.content_md.trim(),
    }))
    .filter((doc) => doc.title || doc.content_md);
}

// 保存前清洗空白与数值字段，让后端只负责正则和时区等强校验。
function normalizeUserPromptReplacementConfigForSave(): UserPromptReplacementConfig {
  const config = normalizeUserPromptReplacementConfig(
    form.user_prompt_replacement_config,
  );
  return {
    enabled: config.enabled,
    rules: config.rules.map((rule, index) => ({
      id: rule.id || `rule-${index + 1}`,
      name: rule.name || rule.id || `Rule ${index + 1}`,
      enabled: rule.enabled !== false,
      pattern: rule.pattern.trim(),
      target_group: Math.max(0, Math.floor(Number(rule.target_group) || 0)),
      replacement_type: normalizeUserPromptReplacementType(
        rule.replacement_type,
      ),
      scope: rule.scope || "",
      static_text: rule.static_text || "",
      timezone: rule.timezone || "Asia/Tokyo",
      time_format: rule.time_format || "2006-01-02",
    })),
  };
}

function findDuplicateLoginAgreementDocumentId(
  documents: LoginAgreementDocument[],
): string | null {
  const seen = new Set<string>();
  for (const doc of documents) {
    if (seen.has(doc.id)) {
      return doc.id;
    }
    seen.add(doc.id);
  }
  return null;
}

function formatTablePageSizeOptions(options: number[]): string {
  return options.join(", ");
}

function parseTablePageSizeOptionsInput(raw: string): number[] | null {
  const tokens = raw
    .split(",")
    .map((token) => token.trim())
    .filter((token) => token.length > 0);

  if (tokens.length === 0) {
    return null;
  }

  const parsed = tokens.map((token) => Number(token));
  if (parsed.some((value) => !Number.isInteger(value))) {
    return null;
  }

  const deduped = Array.from(new Set(parsed)).sort((a, b) => a - b);
  if (
    deduped.some(
      (value) => value < tablePageSizeMin || value > tablePageSizeMax,
    )
  ) {
    return null;
  }

  return deduped;
}

async function loadSettings() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const settings = await adminAPI.settings.getSettings();
    settings.payment_load_balance_strategy =
      settings.payment_load_balance_strategy || "round-robin";
    // Only assign non-null values from backend (null means unconfigured, keep defaults)
    for (const [key, value] of Object.entries(settings)) {
      if (value !== null && value !== undefined) {
        (form as Record<string, unknown>)[key] = value;
      }
    }
    form.site_name_zh = form.site_name_zh || settings.site_name || "Sub2API";
    form.site_name_en = form.site_name_en || "";
    form.site_subtitle_zh =
      form.site_subtitle_zh || settings.site_subtitle || "";
    form.site_subtitle_en = form.site_subtitle_en || "";
    form.login_agreement_mode =
      settings.login_agreement_mode === "checkbox" ? "checkbox" : "modal";
    form.login_agreement_updated_at =
      settings.login_agreement_updated_at || "2026-03-31";
    form.login_agreement_documents =
      Array.isArray(settings.login_agreement_documents) &&
      settings.login_agreement_documents.length > 0
        ? settings.login_agreement_documents.map((doc) => ({
            id: doc.id || "",
            title: doc.title || "",
            content_md: doc.content_md || "",
          }))
        : defaultLoginAgreementDocuments();
    Object.assign(authSourceDefaults, buildAuthSourceDefaultsState(settings));
    form.default_platform_quotas = normalizePlatformQuotasMap(settings.default_platform_quotas);
    form.user_prompt_replacement_config =
      normalizeUserPromptReplacementConfig(
        settings.user_prompt_replacement_config,
      );
    form.openai_account_quota_auto_pause = {
      default_threshold_5h:
        settings.openai_account_quota_auto_pause?.default_threshold_5h ?? 0,
      default_threshold_7d:
        settings.openai_account_quota_auto_pause?.default_threshold_7d ?? 0,
    };
    form.backend_mode_enabled = settings.backend_mode_enabled;
    form.default_subscriptions = normalizeDefaultSubscriptionSettings(
      settings.default_subscriptions,
    );
    registrationEmailSuffixWhitelistTags.value =
      normalizeRegistrationEmailSuffixDomains(
        settings.registration_email_suffix_whitelist,
      );
    tablePageSizeOptionsInput.value = formatTablePageSizeOptions(
      Array.isArray(settings.table_page_size_options)
        ? settings.table_page_size_options
        : [10, 20, 50, 100],
    );
    registrationEmailSuffixWhitelistDraft.value = "";
    form.smtp_password = "";
    smtpPasswordManuallyEdited.value = false;
    form.turnstile_secret_key = "";
    form.linuxdo_connect_client_secret = "";
    form.dingtalk_connect_client_secret = "";
    form.wechat_connect_app_secret = "";
    form.wechat_connect_open_app_secret = "";
    form.wechat_connect_mp_app_secret = "";
    form.wechat_connect_mobile_app_secret = "";
    form.github_oauth_client_secret = "";
    form.google_oauth_client_secret = "";
    const wechatCapabilities = resolveWeChatConnectModeCapabilities(
      settings.wechat_connect_open_enabled,
      settings.wechat_connect_mp_enabled,
      settings.wechat_connect_mobile_enabled,
      settings.wechat_connect_mode,
    );
    form.wechat_connect_open_enabled = wechatCapabilities.openEnabled;
    form.wechat_connect_mp_enabled = wechatCapabilities.mpEnabled;
    form.wechat_connect_mobile_enabled = wechatCapabilities.mobileEnabled;
    form.wechat_connect_mode = deriveWeChatConnectStoredMode(
      wechatCapabilities.openEnabled,
      wechatCapabilities.mpEnabled,
      wechatCapabilities.mobileEnabled,
      settings.wechat_connect_mode,
    );
    const legacyWeChatAppID = String(settings.wechat_connect_app_id || "").trim();
    const legacyWeChatSecretConfigured = Boolean(
      settings.wechat_connect_app_secret_configured,
    );
    if (!form.wechat_connect_open_app_id && wechatCapabilities.openEnabled) {
      form.wechat_connect_open_app_id = legacyWeChatAppID;
    }
    if (!form.wechat_connect_mp_app_id && wechatCapabilities.mpEnabled) {
      form.wechat_connect_mp_app_id = legacyWeChatAppID;
    }
    if (!form.wechat_connect_mobile_app_id && wechatCapabilities.mobileEnabled) {
      form.wechat_connect_mobile_app_id = legacyWeChatAppID;
    }
    if (
      !form.wechat_connect_open_app_secret_configured &&
      wechatCapabilities.openEnabled
    ) {
      form.wechat_connect_open_app_secret_configured =
        legacyWeChatSecretConfigured;
    }
    if (
      !form.wechat_connect_mp_app_secret_configured &&
      wechatCapabilities.mpEnabled
    ) {
      form.wechat_connect_mp_app_secret_configured = legacyWeChatSecretConfigured;
    }
    if (
      !form.wechat_connect_mobile_app_secret_configured &&
      wechatCapabilities.mobileEnabled
    ) {
      form.wechat_connect_mobile_app_secret_configured =
        legacyWeChatSecretConfigured;
    }
    form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(
      form.wechat_connect_mode,
    );
    form.oidc_connect_client_secret = "";

    // Load OpenAI fast/flex policy rules from bulk settings.
    // 仅当 payload 真的包含该字段时填充并标记为已加载；否则保持表单空值，
    // 让 saveSettings 在未加载时跳过该字段，防止覆盖后端默认规则。
    if (
      settings.openai_fast_policy_settings &&
      Array.isArray(settings.openai_fast_policy_settings.rules)
    ) {
      openaiFastPolicyForm.rules =
        settings.openai_fast_policy_settings.rules.map((rule) => ({
          ...rule,
          user_ids: rule.user_ids ? [...rule.user_ids] : [],
          model_whitelist: rule.model_whitelist
            ? [...rule.model_whitelist]
            : [],
        }));
      openaiFastPolicyLoaded.value = true;
    }

    // Load web search emulation config separately
    await loadWebSearchConfig();
  } catch (error: unknown) {
    loadFailed.value = true;
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToLoad")),
    );
  } finally {
    loading.value = false;
  }
}

async function loadSubscriptionPlans() {
  try {
    const response = await adminAPI.payment.getPlans();
    subscriptionPlans.value = response.data || [];
  } catch (_error: unknown) {
    subscriptionPlans.value = [];
  }
}

function findNextAvailableSubscriptionPlan(
  existingPlanIDs: number[],
): SubscriptionPlan | undefined {
  const existing = new Set(existingPlanIDs);
  return subscriptionPlans.value.find((plan) => !existing.has(plan.id));
}

function addDefaultSubscription() {
  if (subscriptionPlans.value.length === 0) return;
  const candidate = findNextAvailableSubscriptionPlan(
    form.default_subscriptions.map((item) => item.plan_id),
  );
  if (!candidate) return;
  form.default_subscriptions.push({
    plan_id: candidate.id,
  });
}

function removeDefaultSubscription(index: number) {
  form.default_subscriptions.splice(index, 1);
}

function addAuthSourceDefaultSubscription(source: AuthSourceType) {
  if (subscriptionPlans.value.length === 0) return;
  const candidate = findNextAvailableSubscriptionPlan(
    authSourceDefaults[source].subscriptions.map((item) => item.plan_id),
  );
  if (!candidate) return;
  authSourceDefaults[source].subscriptions.push({
    plan_id: candidate.id,
  });
}

function removeAuthSourceDefaultSubscription(
  source: AuthSourceType,
  index: number,
) {
  authSourceDefaults[source].subscriptions.splice(index, 1);
}

function findDuplicateDefaultSubscription(
  subscriptions: DefaultSubscriptionSetting[],
): DefaultSubscriptionSetting | undefined {
  const seenPlanIDs = new Set<number>();

  return subscriptions.find((item) => {
    if (seenPlanIDs.has(item.plan_id)) {
      return true;
    }
    seenPlanIDs.add(item.plan_id);
    return false;
  });
}

async function saveSettings() {
  saving.value = true;
  try {
    const normalizedTableDefaultPageSize = Math.floor(
      Number(form.table_default_page_size),
    );
    if (
      !Number.isInteger(normalizedTableDefaultPageSize) ||
      normalizedTableDefaultPageSize < tablePageSizeMin ||
      normalizedTableDefaultPageSize > tablePageSizeMax
    ) {
      appStore.showError(
        t("admin.settings.site.tableDefaultPageSizeRangeError", {
          min: tablePageSizeMin,
          max: tablePageSizeMax,
        }),
      );
      return;
    }

    const normalizedTablePageSizeOptions = parseTablePageSizeOptionsInput(
      tablePageSizeOptionsInput.value,
    );
    if (!normalizedTablePageSizeOptions) {
      appStore.showError(
        t("admin.settings.site.tablePageSizeOptionsFormatError", {
          min: tablePageSizeMin,
          max: tablePageSizeMax,
        }),
      );
      return;
    }

    const normalizedUsageRankingLimit = Math.floor(
      Number(form.usage_ranking_limit),
    );
    if (
      !Number.isInteger(normalizedUsageRankingLimit) ||
      normalizedUsageRankingLimit < usageRankingLimitMin ||
      normalizedUsageRankingLimit > usageRankingLimitMax
    ) {
      appStore.showError(
        t("admin.settings.site.usageRankingLimitRangeError", {
          min: usageRankingLimitMin,
          max: usageRankingLimitMax,
        }),
      );
      return;
    }

    form.table_default_page_size = normalizedTableDefaultPageSize;
    form.table_page_size_options = normalizedTablePageSizeOptions;
    form.usage_ranking_limit = normalizedUsageRankingLimit;
    form.balance_unit_name = form.balance_unit_name.trim() || "USD";
    form.balance_unit_symbol = form.balance_unit_symbol.trim() || "$";
    form.balance_icon_svg = form.balance_icon_svg.trim();
    form.reasoning_point_rmb_unit_price = Math.max(
      0,
      Number(form.reasoning_point_rmb_unit_price) || 0,
    );
    form.usd_exchange_rate = Math.max(0, Number(form.usd_exchange_rate) || 0);
    form.marketplace_availability_window_days = Math.min(
      marketplaceAvailabilityWindowDaysMax,
      Math.max(
        marketplaceAvailabilityWindowDaysMin,
        Math.floor(
          Number(form.marketplace_availability_window_days) ||
            marketplaceAvailabilityWindowDaysDefault,
        ),
      ),
    );
    form.marketplace_availability_bucket_minutes = Math.min(
      marketplaceAvailabilityBucketMinutesMax,
      Math.max(
        marketplaceAvailabilityBucketMinutesMin,
        Math.floor(
          Number(form.marketplace_availability_bucket_minutes) ||
            marketplaceAvailabilityBucketMinutesDefault,
        ),
      ),
    );
    if (
      form.openai_account_quota_auto_pause.default_threshold_5h < 0 ||
      form.openai_account_quota_auto_pause.default_threshold_5h > 1 ||
      form.openai_account_quota_auto_pause.default_threshold_7d < 0 ||
      form.openai_account_quota_auto_pause.default_threshold_7d > 1
    ) {
      appStore.showError(t("admin.settings.openaiQuotaAutoPause.rangeError"));
      return;
    }

    const normalizedLoginAgreementDocuments =
      normalizeLoginAgreementDocumentsForSave();
    if (form.login_agreement_enabled && normalizedLoginAgreementDocuments.length === 0) {
      appStore.showError(
        localText(
          "启用登录条款确认时，至少需要保留一份文档。",
          "At least one document is required when login agreement is enabled.",
        ),
      );
      return;
    }
    const emptyTitleDocument = normalizedLoginAgreementDocuments.find(
      (doc) => !doc.title,
    );
    if (emptyTitleDocument) {
      appStore.showError(
        localText(
          "登录条款文档名称不能为空。",
          "Login agreement document title cannot be empty.",
        ),
      );
      return;
    }
    const duplicateLoginAgreementDocumentId =
      findDuplicateLoginAgreementDocumentId(normalizedLoginAgreementDocuments);
    if (duplicateLoginAgreementDocumentId) {
      appStore.showError(
        localText(
          `登录条款文档路由不能重复：/legal/${duplicateLoginAgreementDocumentId}`,
          `Login agreement document routes cannot be duplicated: /legal/${duplicateLoginAgreementDocumentId}`,
        ),
      );
      return;
    }
    form.login_agreement_mode =
      form.login_agreement_mode === "checkbox" ? "checkbox" : "modal";
    form.login_agreement_documents = normalizedLoginAgreementDocuments;

    const normalizedDefaultSubscriptions = normalizeDefaultSubscriptionSettings(
      form.default_subscriptions,
    );
    const duplicateDefaultSubscription = findDuplicateDefaultSubscription(
      normalizedDefaultSubscriptions,
    );
    if (duplicateDefaultSubscription) {
      appStore.showError(
        t("admin.settings.defaults.defaultSubscriptionsDuplicate", {
          planId: duplicateDefaultSubscription.plan_id,
        }),
      );
      return;
    }

    for (const authSource of authSourceDefaultsMeta.value) {
      authSourceDefaults[authSource.source].subscriptions =
        normalizeDefaultSubscriptionSettings(
          authSourceDefaults[authSource.source].subscriptions,
        );
      const duplicate = findDuplicateDefaultSubscription(
        authSourceDefaults[authSource.source].subscriptions,
      );
      if (duplicate) {
        appStore.showError(
          `${authSource.title}: ${t(
            "admin.settings.defaults.defaultSubscriptionsDuplicate",
            {
              planId: duplicate.plan_id,
            },
          )}`,
        );
        return;
      }
    }

    if (form.wechat_connect_mp_enabled && form.wechat_connect_mobile_enabled) {
      appStore.showError(
        localText(
          "公众号和移动应用不能同时启用。",
          "Official Account and Mobile App cannot be enabled at the same time.",
        ),
      );
      return;
    }
    // Validate URL fields — novalidate disables browser-native checks, so we validate here
    const isValidHttpUrl = (url: string): boolean => {
      if (!url) return true;
      try {
        const u = new URL(url);
        return u.protocol === "http:" || u.protocol === "https:";
      } catch {
        return false;
      }
    };
    // Optional URL fields: auto-clear invalid values so they don't cause backend 400 errors
    if (!isValidHttpUrl(form.frontend_url)) form.frontend_url = "";
    if (!isValidHttpUrl(form.doc_url)) form.doc_url = "";
    syncWeChatConnectMode();
    const wechatStoredMode = deriveWeChatConnectStoredMode(
      form.wechat_connect_open_enabled,
      form.wechat_connect_mp_enabled,
      form.wechat_connect_mobile_enabled,
      form.wechat_connect_mode,
    );

    const payload: UpdateSettingsRequest = {
      registration_enabled: form.registration_enabled,
      email_verify_enabled: form.email_verify_enabled,
      registration_email_suffix_whitelist:
        registrationEmailSuffixWhitelistTags.value.map((suffix) =>
          suffix.startsWith('*.') ? suffix : `@${suffix}`,
        ),
      registration_email_normalization:
        form.registration_email_normalization,
      promo_code_enabled: form.promo_code_enabled,
      invitation_code_enabled: form.invitation_code_enabled,
      password_reset_enabled: form.password_reset_enabled,
      totp_enabled: form.totp_enabled,
      login_agreement_enabled: form.login_agreement_enabled,
      login_agreement_mode: form.login_agreement_mode,
      login_agreement_updated_at: form.login_agreement_updated_at,
      login_agreement_documents: form.login_agreement_documents,
      default_balance: form.default_balance,
      affiliate_enabled: form.affiliate_enabled,
      affiliate_rebate_rate: Math.min(
        100,
        Math.max(0, Number(form.affiliate_rebate_rate) || 0),
      ),
      affiliate_rebate_freeze_hours: Math.max(
        0,
        Math.min(720, Math.floor(Number(form.affiliate_rebate_freeze_hours) || 0)),
      ),
      affiliate_rebate_duration_days: Math.max(
        0,
        Math.min(3650, Math.floor(Number(form.affiliate_rebate_duration_days) || 0)),
      ),
      affiliate_rebate_per_invitee_cap: Math.max(
        0,
        Number(form.affiliate_rebate_per_invitee_cap) || 0,
      ),
      default_concurrency: form.default_concurrency,
      default_subscriptions: normalizedDefaultSubscriptions,
      balance_unit_name: form.balance_unit_name,
      balance_unit_symbol: form.balance_unit_symbol,
      balance_icon_svg: form.balance_icon_svg,
      reasoning_point_rmb_unit_price: form.reasoning_point_rmb_unit_price,
      usd_exchange_rate: form.usd_exchange_rate,
      marketplace_availability_window_days:
        form.marketplace_availability_window_days,
      marketplace_availability_bucket_minutes:
        form.marketplace_availability_bucket_minutes,
      force_email_on_third_party_signup: form.force_email_on_third_party_signup,
      default_user_rpm_limit: form.default_user_rpm_limit,
      site_name: form.site_name_zh || form.site_name_en || form.site_name,
      site_logo: form.site_logo,
      site_subtitle:
        form.site_subtitle_zh || form.site_subtitle_en || form.site_subtitle,
      site_name_zh: form.site_name_zh,
      site_name_en: form.site_name_en,
      site_title_zh: form.site_title_zh,
      site_title_en: form.site_title_en,
      site_subtitle_zh: form.site_subtitle_zh,
      site_subtitle_en: form.site_subtitle_en,
      api_base_url: form.api_base_url,
      contact_info: form.contact_info,
      doc_url: form.doc_url,
      home_content: form.home_content,
      backend_mode_enabled: form.backend_mode_enabled,
      hide_ccs_import_button: form.hide_ccs_import_button,
      table_default_page_size: form.table_default_page_size,
      table_page_size_options: form.table_page_size_options,
      usage_ranking_limit: form.usage_ranking_limit,
      custom_menu_items: form.custom_menu_items,
      custom_endpoints: form.custom_endpoints,
      footer_links: normalizeFooterLinksForSave(),
      footer_text: form.footer_text,
      frontend_url: form.frontend_url,
      smtp_host: form.smtp_host,
      smtp_port: form.smtp_port,
      smtp_username: form.smtp_username,
      smtp_password: form.smtp_password || undefined,
      smtp_from_email: form.smtp_from_email,
      smtp_from_name: form.smtp_from_name,
      smtp_use_tls: form.smtp_use_tls,
      turnstile_enabled: form.turnstile_enabled,
      turnstile_site_key: form.turnstile_site_key,
      turnstile_secret_key: form.turnstile_secret_key || undefined,
      api_key_acl_trust_forwarded_ip: form.api_key_acl_trust_forwarded_ip,
      linuxdo_connect_enabled: form.linuxdo_connect_enabled,
      linuxdo_connect_client_id: form.linuxdo_connect_client_id,
      linuxdo_connect_client_secret:
        form.linuxdo_connect_client_secret || undefined,
      linuxdo_connect_redirect_url: form.linuxdo_connect_redirect_url,
      dingtalk_connect_enabled: form.dingtalk_connect_enabled,
      dingtalk_connect_client_id: form.dingtalk_connect_client_id,
      dingtalk_connect_client_secret:
        form.dingtalk_connect_client_secret || undefined,
      dingtalk_connect_redirect_url: form.dingtalk_connect_redirect_url,
      dingtalk_connect_corp_restriction_policy:
        form.dingtalk_connect_corp_restriction_policy,
      dingtalk_connect_internal_corp_id: form.dingtalk_connect_internal_corp_id,
      dingtalk_connect_bypass_registration: form.dingtalk_connect_bypass_registration,
      dingtalk_connect_sync_corp_email: form.dingtalk_connect_sync_corp_email,
      dingtalk_connect_sync_display_name: form.dingtalk_connect_sync_display_name,
      dingtalk_connect_sync_dept: form.dingtalk_connect_sync_dept,
      dingtalk_connect_sync_corp_email_attr_key: form.dingtalk_connect_sync_corp_email_attr_key,
      dingtalk_connect_sync_display_name_attr_key: form.dingtalk_connect_sync_display_name_attr_key,
      dingtalk_connect_sync_dept_attr_key: form.dingtalk_connect_sync_dept_attr_key,
      dingtalk_connect_sync_corp_email_attr_name: form.dingtalk_connect_sync_corp_email_attr_name,
      dingtalk_connect_sync_display_name_attr_name: form.dingtalk_connect_sync_display_name_attr_name,
      dingtalk_connect_sync_dept_attr_name: form.dingtalk_connect_sync_dept_attr_name,
      wechat_connect_enabled: form.wechat_connect_enabled,
      wechat_connect_app_id:
        form.wechat_connect_open_app_id ||
        form.wechat_connect_mp_app_id ||
        form.wechat_connect_mobile_app_id ||
        form.wechat_connect_app_id,
      wechat_connect_app_secret: form.wechat_connect_app_secret || undefined,
      wechat_connect_open_app_id: form.wechat_connect_open_app_id,
      wechat_connect_open_app_secret:
        form.wechat_connect_open_app_secret || undefined,
      wechat_connect_mp_app_id: form.wechat_connect_mp_app_id,
      wechat_connect_mp_app_secret:
        form.wechat_connect_mp_app_secret || undefined,
      wechat_connect_mobile_app_id: form.wechat_connect_mobile_app_id,
      wechat_connect_mobile_app_secret:
        form.wechat_connect_mobile_app_secret || undefined,
      wechat_connect_open_enabled: form.wechat_connect_open_enabled,
      wechat_connect_mp_enabled: form.wechat_connect_mp_enabled,
      wechat_connect_mobile_enabled: form.wechat_connect_mobile_enabled,
      wechat_connect_mode: wechatStoredMode,
      wechat_connect_scopes:
        defaultWeChatConnectScopesForMode(wechatStoredMode),
      wechat_connect_redirect_url: form.wechat_connect_redirect_url,
      wechat_connect_frontend_redirect_url:
        form.wechat_connect_frontend_redirect_url,
      oidc_connect_enabled: form.oidc_connect_enabled,
      oidc_connect_provider_name: form.oidc_connect_provider_name,
      oidc_connect_client_id: form.oidc_connect_client_id,
      oidc_connect_client_secret: form.oidc_connect_client_secret || undefined,
      oidc_connect_issuer_url: form.oidc_connect_issuer_url,
      oidc_connect_discovery_url: form.oidc_connect_discovery_url,
      oidc_connect_authorize_url: form.oidc_connect_authorize_url,
      oidc_connect_token_url: form.oidc_connect_token_url,
      oidc_connect_userinfo_url: form.oidc_connect_userinfo_url,
      oidc_connect_jwks_url: form.oidc_connect_jwks_url,
      oidc_connect_scopes: form.oidc_connect_scopes,
      oidc_connect_redirect_url: form.oidc_connect_redirect_url,
      oidc_connect_frontend_redirect_url:
        form.oidc_connect_frontend_redirect_url,
      oidc_connect_token_auth_method: form.oidc_connect_token_auth_method,
      oidc_connect_use_pkce: form.oidc_connect_use_pkce,
      oidc_connect_validate_id_token: form.oidc_connect_validate_id_token,
      oidc_connect_allowed_signing_algs: form.oidc_connect_allowed_signing_algs,
      oidc_connect_clock_skew_seconds: form.oidc_connect_clock_skew_seconds,
      oidc_connect_require_email_verified:
        form.oidc_connect_require_email_verified,
      oidc_connect_userinfo_email_path: form.oidc_connect_userinfo_email_path,
      oidc_connect_userinfo_id_path: form.oidc_connect_userinfo_id_path,
      oidc_connect_userinfo_username_path:
        form.oidc_connect_userinfo_username_path,
      github_oauth_enabled: form.github_oauth_enabled,
      github_oauth_client_id: form.github_oauth_client_id,
      github_oauth_client_secret:
        form.github_oauth_client_secret || undefined,
      github_oauth_redirect_url: form.github_oauth_redirect_url,
      github_oauth_frontend_redirect_url:
        form.github_oauth_frontend_redirect_url,
      google_oauth_enabled: form.google_oauth_enabled,
      google_oauth_client_id: form.google_oauth_client_id,
      google_oauth_client_secret:
        form.google_oauth_client_secret || undefined,
      google_oauth_redirect_url: form.google_oauth_redirect_url,
      google_oauth_frontend_redirect_url:
        form.google_oauth_frontend_redirect_url,
      enable_model_fallback: form.enable_model_fallback,
      fallback_model_anthropic: form.fallback_model_anthropic,
      fallback_model_openai: form.fallback_model_openai,
      fallback_model_gemini: form.fallback_model_gemini,
      fallback_model_antigravity: form.fallback_model_antigravity,
      enable_identity_patch: form.enable_identity_patch,
      identity_patch_prompt: form.identity_patch_prompt,
      min_claude_code_version: form.min_claude_code_version,
      max_claude_code_version: form.max_claude_code_version,
      allow_ungrouped_key_scheduling: form.allow_ungrouped_key_scheduling,
      enable_fingerprint_unification: form.enable_fingerprint_unification,
      enable_metadata_passthrough: form.enable_metadata_passthrough,
      enable_cch_signing: form.enable_cch_signing,
      enable_claude_oauth_system_prompt_injection:
        form.enable_claude_oauth_system_prompt_injection,
      claude_oauth_system_prompt:
        form.claude_oauth_system_prompt?.trim() || "",
      claude_oauth_system_prompt_blocks:
        form.claude_oauth_system_prompt_blocks?.trim() || "",
      enable_anthropic_cache_ttl_1h_injection:
        form.enable_anthropic_cache_ttl_1h_injection,
      rewrite_message_cache_control: form.rewrite_message_cache_control,
      enable_client_dateline_normalization:
        form.enable_client_dateline_normalization,
      antigravity_user_agent_version:
        form.antigravity_user_agent_version?.trim() || "",
      openai_codex_user_agent:
        form.openai_codex_user_agent?.trim() || "",
      openai_allow_claude_code_codex_plugin: form.openai_allow_claude_code_codex_plugin,
      user_prompt_replacement_config:
        normalizeUserPromptReplacementConfigForSave(),
      // Payment configuration
      payment_enabled: form.payment_enabled,
      risk_control_enabled: form.risk_control_enabled,
      cyber_session_block_enabled: form.cyber_session_block_enabled,
      cyber_session_block_ttl_seconds: Math.max(
        1,
        Math.floor(Number(form.cyber_session_block_ttl_seconds) || 3600),
      ),
      payment_min_amount: Number(form.payment_min_amount) || 0,
      payment_max_amount: Number(form.payment_max_amount) || 0,
      payment_daily_limit: Number(form.payment_daily_limit) || 0,
      payment_max_pending_orders: Number(form.payment_max_pending_orders) || 0,
      payment_order_timeout_minutes:
        Number(form.payment_order_timeout_minutes) || 0,
      payment_balance_disabled: form.payment_balance_disabled,
      payment_balance_recharge_multiplier:
        Number(form.payment_balance_recharge_multiplier) || 1,
      payment_subscription_usd_to_cny_rate:
        Number(form.payment_subscription_usd_to_cny_rate) || 0,
      payment_recharge_fee_rate: Number(form.payment_recharge_fee_rate) || 0,
      payment_method_fees: form.payment_method_fees,
      payment_enabled_types: form.payment_enabled_types,
      payment_load_balance_strategy: form.payment_load_balance_strategy,
      payment_product_name_prefix: form.payment_product_name_prefix,
      payment_product_name_suffix: form.payment_product_name_suffix,
      payment_help_image_url: form.payment_help_image_url,
      payment_help_text: form.payment_help_text,
      payment_cancel_rate_limit_enabled: form.payment_cancel_rate_limit_enabled,
      payment_cancel_rate_limit_max:
        Number(form.payment_cancel_rate_limit_max) || 10,
      payment_cancel_rate_limit_window:
        Number(form.payment_cancel_rate_limit_window) || 1,
      payment_cancel_rate_limit_unit: form.payment_cancel_rate_limit_unit,
      payment_cancel_rate_limit_window_mode:
        form.payment_cancel_rate_limit_window_mode,
      payment_alipay_force_qrcode: form.payment_alipay_force_qrcode,
      openai_advanced_scheduler_enabled: form.openai_advanced_scheduler_enabled,
      openai_advanced_scheduler_sticky_weighted_enabled:
        form.openai_advanced_scheduler_sticky_weighted_enabled,
      openai_advanced_scheduler_subscription_priority_enabled:
        form.openai_advanced_scheduler_subscription_priority_enabled,
      openai_advanced_scheduler_lb_top_k:
        form.openai_advanced_scheduler_lb_top_k.trim(),
      openai_advanced_scheduler_weight_priority:
        form.openai_advanced_scheduler_weight_priority.trim(),
      openai_advanced_scheduler_weight_load:
        form.openai_advanced_scheduler_weight_load.trim(),
      openai_advanced_scheduler_weight_queue:
        form.openai_advanced_scheduler_weight_queue.trim(),
      openai_advanced_scheduler_weight_error_rate:
        form.openai_advanced_scheduler_weight_error_rate.trim(),
      openai_advanced_scheduler_weight_ttft:
        form.openai_advanced_scheduler_weight_ttft.trim(),
      openai_advanced_scheduler_weight_reset:
        form.openai_advanced_scheduler_weight_reset.trim(),
      openai_advanced_scheduler_weight_quota_headroom:
        form.openai_advanced_scheduler_weight_quota_headroom.trim(),
      openai_advanced_scheduler_weight_previous_response:
        form.openai_advanced_scheduler_weight_previous_response.trim(),
      openai_advanced_scheduler_weight_session_sticky:
        form.openai_advanced_scheduler_weight_session_sticky.trim(),
      openai_account_quota_auto_pause: {
        default_threshold_5h:
          form.openai_account_quota_auto_pause.default_threshold_5h,
        default_threshold_7d:
          form.openai_account_quota_auto_pause.default_threshold_7d,
      },
      // 余额、订阅到期与账号限额通知
      balance_low_notify_enabled: form.balance_low_notify_enabled,
      balance_low_notify_threshold:
        Number(form.balance_low_notify_threshold) || 0,
      balance_low_notify_recharge_url: (form.balance_low_notify_recharge_url =
        form.balance_low_notify_recharge_url || currentOrigin),
      subscription_expiry_notify_enabled:
        form.subscription_expiry_notify_enabled,
      account_quota_notify_enabled: form.account_quota_notify_enabled,
      account_quota_notify_emails: (
        form.account_quota_notify_emails || []
      ).filter((e) => e.email.trim() !== ""),
      allow_user_view_error_requests: form.allow_user_view_error_requests,
    };

    // 仅当 openai_fast_policy_settings 已成功从后端加载时才回写，
    // 否则省略整个字段，让后端保留既有规则（含默认值）。
    if (openaiFastPolicyLoaded.value) {
      payload.openai_fast_policy_settings = {
        rules: openaiFastPolicyForm.rules.map((rule) => {
          const whitelist = (rule.model_whitelist || [])
            .map((p) => p.trim())
            .filter((p) => p !== "");
          const hasWhitelist = whitelist.length > 0;
          return {
            service_tier: rule.service_tier,
            action: rule.action,
            scope: rule.scope,
            user_ids:
              rule.user_ids && rule.user_ids.length > 0
                ? [...rule.user_ids]
                : undefined,
            error_message:
              rule.action === "block" ? rule.error_message : undefined,
            model_whitelist: hasWhitelist ? whitelist : undefined,
            fallback_action: hasWhitelist
              ? rule.fallback_action || "pass"
              : undefined,
            fallback_error_message:
              hasWhitelist && rule.fallback_action === "block"
                ? rule.fallback_error_message
                : undefined,
          };
        }),
      };
    }

    payload.default_platform_quotas = sanitizePlatformQuotasMap(form.default_platform_quotas);
    appendAuthSourceDefaultsToUpdateRequest(payload, authSourceDefaults);

    const updated = await adminAPI.settings.updateSettings(payload);
    for (const [key, value] of Object.entries(updated)) {
      if (key === "openai_fast_policy_settings") continue;
      if (value !== null && value !== undefined) {
        (form as Record<string, unknown>)[key] = value;
      }
    }
    Object.assign(authSourceDefaults, buildAuthSourceDefaultsState(updated));
    form.default_platform_quotas = normalizePlatformQuotasMap(updated.default_platform_quotas);
    form.openai_account_quota_auto_pause = {
      default_threshold_5h:
        updated.openai_account_quota_auto_pause?.default_threshold_5h ?? 0,
      default_threshold_7d:
        updated.openai_account_quota_auto_pause?.default_threshold_7d ?? 0,
    };
    registrationEmailSuffixWhitelistTags.value =
      normalizeRegistrationEmailSuffixDomains(
        updated.registration_email_suffix_whitelist,
      );
    tablePageSizeOptionsInput.value = formatTablePageSizeOptions(
      Array.isArray(updated.table_page_size_options)
        ? updated.table_page_size_options
        : [10, 20, 50, 100],
    );
    registrationEmailSuffixWhitelistDraft.value = "";
    form.smtp_password = "";
    smtpPasswordManuallyEdited.value = false;
    form.turnstile_secret_key = "";
    form.linuxdo_connect_client_secret = "";
    form.dingtalk_connect_client_secret = "";
    form.wechat_connect_app_secret = "";
    form.wechat_connect_open_app_secret = "";
    form.wechat_connect_mp_app_secret = "";
    form.wechat_connect_mobile_app_secret = "";
    const updatedWechatCapabilities = resolveWeChatConnectModeCapabilities(
      updated.wechat_connect_open_enabled,
      updated.wechat_connect_mp_enabled,
      updated.wechat_connect_mobile_enabled,
      updated.wechat_connect_mode,
    );
    form.wechat_connect_open_enabled = updatedWechatCapabilities.openEnabled;
    form.wechat_connect_mp_enabled = updatedWechatCapabilities.mpEnabled;
    form.wechat_connect_mobile_enabled =
      updatedWechatCapabilities.mobileEnabled;
    form.wechat_connect_mode = deriveWeChatConnectStoredMode(
      updatedWechatCapabilities.openEnabled,
      updatedWechatCapabilities.mpEnabled,
      updatedWechatCapabilities.mobileEnabled,
      updated.wechat_connect_mode,
    );
    form.wechat_connect_scopes = defaultWeChatConnectScopesForMode(
      form.wechat_connect_mode,
    );
    form.oidc_connect_client_secret = "";
    form.github_oauth_client_secret = "";
    form.google_oauth_client_secret = "";
    // Refresh OpenAI fast/flex policy from server response
    if (
      updated.openai_fast_policy_settings &&
      Array.isArray(updated.openai_fast_policy_settings.rules)
    ) {
      openaiFastPolicyForm.rules =
        updated.openai_fast_policy_settings.rules.map((rule) => ({
          ...rule,
          user_ids: rule.user_ids ? [...rule.user_ids] : [],
          model_whitelist: rule.model_whitelist
            ? [...rule.model_whitelist]
            : [],
        }));
      openaiFastPolicyLoaded.value = true;
    }
    // Save web search emulation config separately (errors handled internally)
    const wsOk = await saveWebSearchConfig();
    // Refresh cached settings so sidebar/header update immediately
    await appStore.fetchPublicSettings(true);
    await adminSettingsStore.fetch(true);
    if (wsOk) {
      appStore.showSuccess(t("admin.settings.settingsSaved"));
    }
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToSave")),
    );
  } finally {
    saving.value = false;
  }
}

async function testSmtpConnection() {
  testingSmtp.value = true;
  try {
    const smtpPasswordForTest = smtpPasswordManuallyEdited.value
      ? form.smtp_password
      : "";
    const result = await adminAPI.settings.testSmtpConnection({
      smtp_host: form.smtp_host,
      smtp_port: form.smtp_port,
      smtp_username: form.smtp_username,
      smtp_password: smtpPasswordForTest,
      smtp_use_tls: form.smtp_use_tls,
    });
    // API returns { message: "..." } on success, errors are thrown as exceptions
    appStore.showSuccess(
      result.message || t("admin.settings.smtpConnectionSuccess"),
    );
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToTestSmtp")),
    );
  } finally {
    testingSmtp.value = false;
  }
}

async function sendTestEmail() {
  if (!testEmailAddress.value) {
    appStore.showError(t("admin.settings.testEmail.enterRecipientHint"));
    return;
  }

  sendingTestEmail.value = true;
  try {
    const smtpPasswordForSend = smtpPasswordManuallyEdited.value
      ? form.smtp_password
      : "";
    const result = await adminAPI.settings.sendTestEmail({
      email: testEmailAddress.value,
      smtp_host: form.smtp_host,
      smtp_port: form.smtp_port,
      smtp_username: form.smtp_username,
      smtp_password: smtpPasswordForSend,
      smtp_from_email: form.smtp_from_email,
      smtp_from_name: form.smtp_from_name,
      smtp_use_tls: form.smtp_use_tls,
    });
    // API returns { message: "..." } on success, errors are thrown as exceptions
    appStore.showSuccess(result.message || t("admin.settings.testEmailSent"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.failedToSendTestEmail")),
    );
  } finally {
    sendingTestEmail.value = false;
  }
}

// Admin API Key 方法
async function loadAdminApiKey() {
  adminApiKeyLoading.value = true;
  try {
    const status = await adminAPI.settings.getAdminApiKey();
    adminApiKeyExists.value = status.exists;
    adminApiKeyMasked.value = status.masked_key;
  } catch (_error: unknown) {
    // Silent fail - admin API key status is non-critical
  } finally {
    adminApiKeyLoading.value = false;
  }
}

async function createAdminApiKey() {
  adminApiKeyOperating.value = true;
  try {
    const result = await adminAPI.settings.regenerateAdminApiKey();
    newAdminApiKey.value = result.key;
    adminApiKeyExists.value = true;
    adminApiKeyMasked.value =
      result.key.substring(0, 10) + "..." + result.key.slice(-4);
    appStore.showSuccess(t("admin.settings.adminApiKey.keyGenerated"));
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("common.error")));
  } finally {
    adminApiKeyOperating.value = false;
  }
}

async function regenerateAdminApiKey() {
  if (!confirm(t("admin.settings.adminApiKey.regenerateConfirm"))) return;
  await createAdminApiKey();
}

async function deleteAdminApiKey() {
  if (!confirm(t("admin.settings.adminApiKey.deleteConfirm"))) return;
  adminApiKeyOperating.value = true;
  try {
    await adminAPI.settings.deleteAdminApiKey();
    adminApiKeyExists.value = false;
    adminApiKeyMasked.value = "";
    newAdminApiKey.value = "";
    appStore.showSuccess(t("admin.settings.adminApiKey.keyDeleted"));
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("common.error")));
  } finally {
    adminApiKeyOperating.value = false;
  }
}

function copyNewKey() {
  navigator.clipboard
    .writeText(newAdminApiKey.value)
    .then(() => {
      appStore.showSuccess(t("admin.settings.adminApiKey.keyCopied"));
    })
    .catch(() => {
      appStore.showError(t("common.copyFailed"));
    });
}

// Overload Cooldown 方法
async function loadOverloadCooldownSettings() {
  overloadCooldownLoading.value = true;
  try {
    const settings = await adminAPI.settings.getOverloadCooldownSettings();
    Object.assign(overloadCooldownForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    overloadCooldownLoading.value = false;
  }
}

async function saveOverloadCooldownSettings() {
  overloadCooldownSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateOverloadCooldownSettings({
      enabled: overloadCooldownForm.enabled,
      cooldown_minutes: overloadCooldownForm.cooldown_minutes,
    });
    Object.assign(overloadCooldownForm, updated);
    appStore.showSuccess(t("admin.settings.overloadCooldown.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.overloadCooldown.saveFailed"),
      ),
    );
  } finally {
    overloadCooldownSaving.value = false;
  }
}

async function loadOpenAI403CooldownSettings() {
  openAI403CooldownLoading.value = true;
  try {
    const settings = await adminAPI.settings.getOpenAI403CooldownSettings();
    Object.assign(openAI403CooldownForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    openAI403CooldownLoading.value = false;
  }
}

async function saveOpenAI403CooldownSettings() {
  openAI403CooldownSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateOpenAI403CooldownSettings({
      enabled: openAI403CooldownForm.enabled,
      cooldown_minutes: openAI403CooldownForm.cooldown_minutes,
      error_on_threshold_enabled: openAI403CooldownForm.error_on_threshold_enabled,
      threshold_count: openAI403CooldownForm.threshold_count,
      threshold_window_minutes: openAI403CooldownForm.threshold_window_minutes,
    });
    Object.assign(openAI403CooldownForm, updated);
    appStore.showSuccess(t("admin.settings.openAI403Cooldown.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.openAI403Cooldown.saveFailed"),
      ),
    );
  } finally {
    openAI403CooldownSaving.value = false;
  }
}

// Rate Limit Cooldown (429) 方法
async function loadRateLimit429CooldownSettings() {
  rateLimit429CooldownLoading.value = true;
  try {
    const settings = await adminAPI.settings.getRateLimit429CooldownSettings();
    Object.assign(rateLimit429CooldownForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    rateLimit429CooldownLoading.value = false;
  }
}

async function saveRateLimit429CooldownSettings() {
  rateLimit429CooldownSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateRateLimit429CooldownSettings({
      enabled: rateLimit429CooldownForm.enabled,
      cooldown_seconds: rateLimit429CooldownForm.cooldown_seconds,
    });
    Object.assign(rateLimit429CooldownForm, updated);
    appStore.showSuccess(t("admin.settings.rateLimit429Cooldown.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.rateLimit429Cooldown.saveFailed"),
      ),
    );
  } finally {
    rateLimit429CooldownSaving.value = false;
  }
}

// Stream Timeout 方法
async function loadStreamTimeoutSettings() {
  streamTimeoutLoading.value = true;
  try {
    const settings = await adminAPI.settings.getStreamTimeoutSettings();
    Object.assign(streamTimeoutForm, settings);
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    streamTimeoutLoading.value = false;
  }
}

async function saveStreamTimeoutSettings() {
  streamTimeoutSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateStreamTimeoutSettings({
      enabled: streamTimeoutForm.enabled,
      action: streamTimeoutForm.action,
      temp_unsched_minutes: streamTimeoutForm.temp_unsched_minutes,
      threshold_count: streamTimeoutForm.threshold_count,
      threshold_window_minutes: streamTimeoutForm.threshold_window_minutes,
    });
    Object.assign(streamTimeoutForm, updated);
    appStore.showSuccess(t("admin.settings.streamTimeout.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t("admin.settings.streamTimeout.saveFailed"),
      ),
    );
  } finally {
    streamTimeoutSaving.value = false;
  }
}

// Rectifier 方法
async function loadRectifierSettings() {
  rectifierLoading.value = true;
  try {
    const settings = await adminAPI.settings.getRectifierSettings();
    Object.assign(rectifierForm, settings);
    // 确保 patterns 是数组（旧数据可能为 null）
    if (!Array.isArray(rectifierForm.apikey_signature_patterns)) {
      rectifierForm.apikey_signature_patterns = [];
    }
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    rectifierLoading.value = false;
  }
}

async function saveRectifierSettings() {
  rectifierSaving.value = true;
  try {
    const updated = await adminAPI.settings.updateRectifierSettings({
      enabled: rectifierForm.enabled,
      thinking_signature_enabled: rectifierForm.thinking_signature_enabled,
      thinking_budget_enabled: rectifierForm.thinking_budget_enabled,
      apikey_signature_enabled: rectifierForm.apikey_signature_enabled,
      apikey_signature_patterns: rectifierForm.apikey_signature_patterns.filter(
        (p) => p.trim() !== "",
      ),
    });
    Object.assign(rectifierForm, updated);
    if (!Array.isArray(rectifierForm.apikey_signature_patterns)) {
      rectifierForm.apikey_signature_patterns = [];
    }
    appStore.showSuccess(t("admin.settings.rectifier.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.rectifier.saveFailed")),
    );
  } finally {
    rectifierSaving.value = false;
  }
}

const betaPolicyActionOptions = computed(() => [
  { value: "pass", label: t("admin.settings.betaPolicy.actionPass") },
  { value: "filter", label: t("admin.settings.betaPolicy.actionFilter") },
  { value: "block", label: t("admin.settings.betaPolicy.actionBlock") },
]);

const betaPolicyScopeOptions = computed(() => [
  { value: "all", label: t("admin.settings.betaPolicy.scopeAll") },
  { value: "oauth", label: t("admin.settings.betaPolicy.scopeOAuth") },
  { value: "apikey", label: t("admin.settings.betaPolicy.scopeAPIKey") },
  { value: "bedrock", label: t("admin.settings.betaPolicy.scopeBedrock") },
]);

// Beta Policy 方法
const betaDisplayNames: Record<string, string> = {
  "fast-mode-2026-02-01": "Fast Mode",
  "context-1m-2025-08-07": "Context 1M",
};

// 快捷预设：按 beta_token 定义预设方案
const betaPresets: Record<
  string,
  Array<{
    label: string;
    description: string;
    action: "pass" | "filter" | "block";
    model_whitelist: string[];
    fallback_action: "pass" | "filter" | "block";
  }>
> = {
  "context-1m-2025-08-07": [
    {
      label: t("admin.settings.betaPolicy.presetOpusOnly"),
      description: t("admin.settings.betaPolicy.presetOpusOnlyDesc"),
      action: "pass",
      model_whitelist: ["claude-opus-4-6"],
      fallback_action: "filter",
    },
  ],
};

// 常用模型模式（具体 ID + 通配符示例）
const commonModelPatterns = [
  "claude-opus-4-6",
  "claude-sonnet-4-6",
  "claude-opus-*",
  "claude-sonnet-*",
];

function getBetaDisplayName(token: string): string {
  return betaDisplayNames[token] || token;
}

function applyBetaPreset(
  rule: (typeof betaPolicyForm.rules)[number],
  preset: {
    action: "pass" | "filter" | "block";
    model_whitelist: string[];
    fallback_action: "pass" | "filter" | "block";
  },
) {
  rule.action = preset.action;
  rule.model_whitelist = [...preset.model_whitelist];
  rule.fallback_action = preset.fallback_action;
}

function addQuickPattern(
  rule: (typeof betaPolicyForm.rules)[number],
  pattern: string,
) {
  if (!rule.model_whitelist) rule.model_whitelist = [];
  if (!rule.model_whitelist.includes(pattern)) {
    rule.model_whitelist.push(pattern);
  }
}

async function loadBetaPolicySettings() {
  betaPolicyLoading.value = true;
  try {
    const settings = await adminAPI.settings.getBetaPolicySettings();
    betaPolicyForm.rules = settings.rules;
  } catch (_error: unknown) {
    // Silent fail - settings will use defaults
  } finally {
    betaPolicyLoading.value = false;
  }
}

// ==================== OpenAI Fast/Flex Policy ====================

const openaiFastPolicyTierOptions = computed(() => [
  { value: "all", label: t("admin.settings.openaiFastPolicy.tierAll") },
  {
    value: "priority",
    label: t("admin.settings.openaiFastPolicy.tierPriority"),
  },
  { value: "flex", label: t("admin.settings.openaiFastPolicy.tierFlex") },
]);

const openaiFastPolicyActionOptions = computed(() => [
  { value: "pass", label: t("admin.settings.openaiFastPolicy.actionPass") },
  { value: "filter", label: t("admin.settings.openaiFastPolicy.actionFilter") },
  {
    value: "force_priority",
    label: t("admin.settings.openaiFastPolicy.actionForcePriority"),
  },
  { value: "block", label: t("admin.settings.openaiFastPolicy.actionBlock") },
]);

const openaiFastPolicyScopeOptions = computed(() => [
  { value: "all", label: t("admin.settings.openaiFastPolicy.scopeAll") },
  { value: "oauth", label: t("admin.settings.openaiFastPolicy.scopeOAuth") },
  { value: "apikey", label: t("admin.settings.openaiFastPolicy.scopeAPIKey") },
  {
    value: "bedrock",
    label: t("admin.settings.openaiFastPolicy.scopeBedrock"),
  },
]);

function addOpenAIFastPolicyRule() {
  openaiFastPolicyForm.rules.push({
    service_tier: "priority",
    action: "filter",
    scope: "all",
    user_ids: [],
    error_message: "",
    model_whitelist: [],
    fallback_action: "pass",
    fallback_error_message: "",
  });
}

function removeOpenAIFastPolicyRule(index: number) {
  openaiFastPolicyForm.rules.splice(index, 1);
}

// addOpenAIFastPolicyUserID 为规则追加一个待填写的用户 ID。
function addOpenAIFastPolicyUserID(rule: OpenAIFastPolicyRule) {
  if (!rule.user_ids) rule.user_ids = [];
  rule.user_ids.push(0);
}

// removeOpenAIFastPolicyUserID 删除指定位置的用户 ID。
function removeOpenAIFastPolicyUserID(
  rule: OpenAIFastPolicyRule,
  idx: number,
) {
  rule.user_ids?.splice(idx, 1);
}

function addOpenAIFastPolicyModelPattern(rule: OpenAIFastPolicyRule) {
  if (!rule.model_whitelist) rule.model_whitelist = [];
  rule.model_whitelist.push("");
}

function removeOpenAIFastPolicyModelPattern(
  rule: OpenAIFastPolicyRule,
  idx: number,
) {
  rule.model_whitelist?.splice(idx, 1);
}

async function saveBetaPolicySettings() {
  betaPolicySaving.value = true;
  try {
    // Clean up empty patterns before saving
    const cleanedRules = betaPolicyForm.rules.map((rule) => {
      const whitelist = rule.model_whitelist?.filter((p) => p.trim() !== "");
      const hasWhitelist = whitelist && whitelist.length > 0;
      return {
        beta_token: rule.beta_token,
        action: rule.action,
        scope: rule.scope,
        error_message: rule.error_message,
        model_whitelist: hasWhitelist ? whitelist : undefined,
        fallback_action: hasWhitelist
          ? rule.fallback_action || "pass"
          : undefined,
        fallback_error_message:
          hasWhitelist && rule.fallback_action === "block"
            ? rule.fallback_error_message
            : undefined,
      };
    });
    const updated = await adminAPI.settings.updateBetaPolicySettings({
      rules: cleanedRules,
    });
    betaPolicyForm.rules = updated.rules;
    appStore.showSuccess(t("admin.settings.betaPolicy.saved"));
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.settings.betaPolicy.saveFailed")),
    );
  } finally {
    betaPolicySaving.value = false;
  }
}

// ==================== Provider Management ====================

const allPaymentTypes = computed(() => [
  { value: "easypay", label: t("payment.methods.easypay") },
  { value: "alipay", label: t("payment.methods.alipay") },
  { value: "wxpay", label: t("payment.methods.wxpay") },
  { value: "stripe", label: t("payment.methods.stripe") },
  { value: "airwallex", label: t("payment.methods.airwallex") },
]);

function isPaymentTypeEnabled(type: string): boolean {
  return form.payment_enabled_types.includes(type);
}

const hasAnyPaymentTypeEnabled = computed(
  () => form.payment_enabled_types.length > 0,
);

function togglePaymentType(type: string) {
  if (form.payment_enabled_types.includes(type)) {
    form.payment_enabled_types = form.payment_enabled_types.filter(
      (t) => t !== type,
    );
    // Disable all provider instances matching this type
    disableProvidersByType(type);
  } else {
    form.payment_enabled_types = [...form.payment_enabled_types, type];
  }
}

async function disableProvidersByType(type: string) {
  const matching = providers.value.filter(
    (p) => p.provider_key === type && p.enabled,
  );
  for (const p of matching) {
    try {
      await adminAPI.payment.updateProvider(p.id, { enabled: false });
      p.enabled = false;
    } catch (err: unknown) {
      slog("disable provider failed", p.id, err);
    }
  }
}

function slog(...args: unknown[]) {
  console.warn("[payment]", ...args);
}

const providersLoading = ref(false);
const providerSaving = ref(false);
const providers = ref<ProviderInstance[]>([]);
const providerUpdatingIds = ref<Set<number>>(new Set());
const showProviderDialog = ref(false);
const showDeleteProviderDialog = ref(false);
const editingProvider = ref<ProviderInstance | null>(null);
const deletingProviderId = ref<number | null>(null);
const providerDialogRef = ref<InstanceType<
  typeof PaymentProviderDialog
> | null>(null);
let providersLoadSeq = 0;

function normalizePaymentProvider(provider: ProviderInstance): ProviderInstance {
  return {
    ...provider,
    // 兼容旧后端把空 supported_types 序列化成 null 的情况，避免卡片和弹窗读取 includes 时抛错。
    supported_types: Array.isArray(provider.supported_types)
      ? provider.supported_types
      : [],
  };
}

const updatingProviderIds = computed(() => Array.from(providerUpdatingIds.value));

function isProviderUpdating(providerId: number): boolean {
  return providerUpdatingIds.value.has(providerId);
}

function setProviderUpdating(providerId: number, updating: boolean) {
  // 同一个服务商的更新必须串行化，避免快速连点产生乱序 PATCH 和列表回写。
  const next = new Set(providerUpdatingIds.value);
  if (updating) {
    next.add(providerId);
  } else {
    next.delete(providerId);
  }
  providerUpdatingIds.value = next;
}

const providerKeyOptions = computed(() => [
  { value: "easypay", label: t("admin.settings.payment.providerEasypay") },
  { value: "alipay", label: t("admin.settings.payment.providerAlipay") },
  { value: "wxpay", label: t("admin.settings.payment.providerWxpay") },
  { value: "stripe", label: t("admin.settings.payment.providerStripe") },
  { value: "airwallex", label: t("admin.settings.payment.providerAirwallex") },
]);

const enabledProviderKeyOptions = computed(() => {
  const enabled = form.payment_enabled_types;
  return providerKeyOptions.value.filter((opt) => enabled.includes(opt.value));
});

const loadBalanceOptions = computed(() => [
  {
    value: "round-robin",
    label: t("admin.settings.payment.strategyRoundRobin"),
  },
  {
    value: "least-amount",
    label: t("admin.settings.payment.strategyLeastAmount"),
  },
]);

const cancelRateLimitUnitOptions = computed(() => [
  {
    value: "minute",
    label: t("admin.settings.payment.cancelRateLimitUnitMinute"),
  },
  { value: "hour", label: t("admin.settings.payment.cancelRateLimitUnitHour") },
  { value: "day", label: t("admin.settings.payment.cancelRateLimitUnitDay") },
]);

const cancelRateLimitModeOptions = computed(() => [
  {
    value: "rolling",
    label: t("admin.settings.payment.cancelRateLimitWindowModeRolling"),
  },
  {
    value: "fixed",
    label: t("admin.settings.payment.cancelRateLimitWindowModeFixed"),
  },
]);

type ProviderEnablementCandidate = Pick<
  ProviderInstance,
  "id" | "provider_key" | "supported_types" | "enabled" | "name"
>;

function getProviderVisibleMethods(
  provider: ProviderEnablementCandidate,
): Array<"alipay" | "wxpay"> {
  if (!provider.enabled) {
    return [];
  }

  const supportedTypes = Array.isArray(provider.supported_types)
    ? provider.supported_types
    : [];
  const methods = new Set<"alipay" | "wxpay">();
  const addMethod = (type: string) => {
    const method = normalizeVisibleMethod(type);
    if (method === "alipay" || method === "wxpay") {
      methods.add(method);
    }
  };

  if (provider.provider_key === "alipay") {
    if (supportedTypes.length === 0) {
      methods.add("alipay");
    } else {
      supportedTypes.forEach((type) => {
        if (normalizeVisibleMethod(type) === "alipay") {
          methods.add("alipay");
        }
      });
    }
  } else if (provider.provider_key === "wxpay") {
    if (supportedTypes.length === 0) {
      methods.add("wxpay");
    } else {
      supportedTypes.forEach((type) => {
        if (normalizeVisibleMethod(type) === "wxpay") {
          methods.add("wxpay");
        }
      });
    }
  } else if (provider.provider_key === "easypay") {
    supportedTypes.forEach(addMethod);
  }

  return Array.from(methods);
}

function findProviderEnablementConflict(
  candidate: ProviderEnablementCandidate,
): { method: "alipay" | "wxpay"; conflicting: ProviderInstance } | null {
  const claimedMethods = getProviderVisibleMethods(candidate);
  if (claimedMethods.length === 0) {
    return null;
  }

  for (const other of providers.value) {
    if (other.id === candidate.id || !other.enabled) {
      continue;
    }

    const otherMethods = getProviderVisibleMethods(other);
    const matchedMethod = claimedMethods.find((method) =>
      otherMethods.includes(method),
    );
    if (matchedMethod) {
      return {
        method: matchedMethod,
        conflicting: other,
      };
    }
  }

  return null;
}

function showProviderEnablementConflict(
  conflict: { method: "alipay" | "wxpay"; conflicting: ProviderInstance },
) {
  appStore.showError(
    t("admin.settings.payment.enableConflict", {
      method: t(`payment.methods.${conflict.method}`),
      provider: conflict.conflicting.name,
    }),
  );
}

async function loadProviders() {
  const seq = ++providersLoadSeq;
  providersLoading.value = true;
  try {
    const res = await adminAPI.payment.getProviders();
    // 只接受最后一次加载结果，避免较慢的旧请求覆盖较新的服务商列表。
    if (seq === providersLoadSeq) {
      providers.value = (res.data || []).map(normalizePaymentProvider);
    }
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  } finally {
    if (seq === providersLoadSeq) {
      providersLoading.value = false;
    }
  }
}

function openCreateProvider() {
  editingProvider.value = null;
  providerDialogRef.value?.reset(
    enabledProviderKeyOptions.value[0]?.value || "easypay",
  );
  showProviderDialog.value = true;
}

function openEditProvider(provider: ProviderInstance) {
  editingProvider.value = provider;
  providerDialogRef.value?.loadProvider(provider);
  showProviderDialog.value = true;
}

async function handleSaveProvider(payload: Partial<ProviderInstance>) {
  providerSaving.value = true;
  try {
    const candidate: ProviderEnablementCandidate = {
      id: editingProvider.value?.id ?? 0,
      provider_key:
        payload.provider_key ?? editingProvider.value?.provider_key ?? "",
      supported_types:
        payload.supported_types ?? editingProvider.value?.supported_types ?? [],
      enabled: payload.enabled ?? editingProvider.value?.enabled ?? false,
      name: payload.name ?? editingProvider.value?.name ?? "",
    };
    const conflict = findProviderEnablementConflict(candidate);
    if (conflict) {
      showProviderEnablementConflict(conflict);
      return;
    }

    if (editingProvider.value) {
      await adminAPI.payment.updateProvider(editingProvider.value.id, payload);
    } else {
      await adminAPI.payment.createProvider(payload);
    }
    showProviderDialog.value = false;
    // Reload full list (API returns decrypted/formatted data with correct sort order)
    await loadProviders();
    // Auto-save settings so provider changes take effect immediately
    await saveSettings();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  } finally {
    providerSaving.value = false;
  }
}

async function handleToggleField(
  provider: ProviderInstance,
  field: "enabled" | "refund_enabled" | "allow_user_refund",
) {
  if (isProviderUpdating(provider.id)) {
    return;
  }
  setProviderUpdating(provider.id, true);
  try {
    const currentProvider =
      providers.value.find((item) => item.id === provider.id) ?? provider;
    let newValue: boolean;
    if (field === "enabled") newValue = !currentProvider.enabled;
    else if (field === "refund_enabled") newValue = !currentProvider.refund_enabled;
    else newValue = !currentProvider.allow_user_refund;

    if (field === "enabled" && newValue) {
      const conflict = findProviderEnablementConflict({
        id: currentProvider.id,
        provider_key: currentProvider.provider_key,
        supported_types: currentProvider.supported_types,
        enabled: true,
        name: currentProvider.name,
      });
      if (conflict) {
        showProviderEnablementConflict(conflict);
        return;
      }
    }

    const payload: Record<string, boolean> = { [field]: newValue };
    // 关闭退款能力时同步关闭用户自助退款，保持前后端状态一致。
    if (field === "refund_enabled" && !newValue) {
      payload.allow_user_refund = false;
    }
    await adminAPI.payment.updateProvider(currentProvider.id, payload);
    await loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  } finally {
    setProviderUpdating(provider.id, false);
  }
}

async function handleToggleType(provider: ProviderInstance, type: string) {
  if (isProviderUpdating(provider.id)) {
    return;
  }
  setProviderUpdating(provider.id, true);
  try {
    const currentProvider =
      providers.value.find((item) => item.id === provider.id) ?? provider;
    if (currentProvider.provider_key === "stripe") {
      // Stripe Checkout 的支付方式由 Stripe Dashboard 控制，管理端不再写入无效子方式。
      return;
    }
    const supportedTypes = Array.isArray(currentProvider.supported_types)
      ? currentProvider.supported_types
      : [];
    const updated = supportedTypes.includes(type)
      ? supportedTypes.filter((t) => t !== type)
      : [...supportedTypes, type];
    const conflict = findProviderEnablementConflict({
      id: currentProvider.id,
      provider_key: currentProvider.provider_key,
      supported_types: updated,
      enabled: currentProvider.enabled,
      name: currentProvider.name,
    });
    if (conflict) {
      showProviderEnablementConflict(conflict);
      return;
    }
    await adminAPI.payment.updateProvider(currentProvider.id, {
      supported_types: updated,
    } as Partial<ProviderInstance>);
    await loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  } finally {
    setProviderUpdating(provider.id, false);
  }
}

function confirmDeleteProvider(provider: ProviderInstance) {
  deletingProviderId.value = provider.id;
  showDeleteProviderDialog.value = true;
}

async function handleReorderProviders(
  updates: { id: number; sort_order: number }[],
) {
  try {
    await Promise.all(
      updates.map((u) =>
        adminAPI.payment.updateProvider(u.id, {
          sort_order: u.sort_order,
        } as Partial<ProviderInstance>),
      ),
    );
    await loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
    loadProviders();
  }
}

async function handleDeleteProvider() {
  if (!deletingProviderId.value) return;
  try {
    await adminAPI.payment.deleteProvider(deletingProviderId.value);
    appStore.showSuccess(t("common.deleted"));
    showDeleteProviderDialog.value = false;
    loadProviders();
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, "payment.errors", t("common.error")));
  }
}

onMounted(() => {
  loadSettings();
  loadSubscriptionPlans();
  loadAdminApiKey();
  loadOverloadCooldownSettings();
  loadOpenAI403CooldownSettings();
  loadRateLimit429CooldownSettings();
  loadStreamTimeoutSettings();
  loadRectifierSettings();
  loadBetaPolicySettings();
  loadProviders();
});

// bypass_registration 与身份同步三开关仅在 internal_only 模式下生效。切换 policy 到其它值时，
// 立即把相关字段重置为 false，避免保存请求里残留旧值。后端 admin handler 与
// 配置加载层都有 coerce 兜底，这里是交互层的同步而非安全防线。
watch(
  () => form.dingtalk_connect_corp_restriction_policy,
  (policy) => {
    if (policy !== "internal_only") {
      if (form.dingtalk_connect_bypass_registration) form.dingtalk_connect_bypass_registration = false;
      if (form.dingtalk_connect_sync_corp_email) form.dingtalk_connect_sync_corp_email = false;
      if (form.dingtalk_connect_sync_display_name) form.dingtalk_connect_sync_display_name = false;
      if (form.dingtalk_connect_sync_dept) form.dingtalk_connect_sync_dept = false;
    }
  },
);
</script>

<style scoped>
.default-sub-group-select :deep(.select-trigger) {
  @apply h-[42px];
}

.default-sub-delete-btn {
  @apply h-[42px];
}

/* ============ 系统设置 Tab 导航 ============ */
.settings-tabs-shell {
  @apply sticky z-20 -mx-1 rounded-2xl border border-white/80 bg-white/90 p-1.5 backdrop-blur-xl dark:border-dark-600/70 dark:bg-dark-900/90;
  top: 4.75rem;
  box-shadow:
    0 12px 28px rgb(15 23 42 / 0.07),
    0 1px 0 rgb(255 255 255 / 0.9) inset;
}

.settings-tabs-scroll {
  @apply overflow-x-auto;
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
  @apply relative isolate flex h-10 min-w-[6.75rem] shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-xl border border-transparent px-3 text-sm font-medium text-gray-600 outline-none transition-colors duration-200 ease-out dark:text-gray-300;
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
  @apply absolute inset-0 -z-10 rounded-xl opacity-0 transition-opacity duration-200;
  content: "";
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
  @apply border-primary-200/80 bg-white text-primary-700 shadow-sm dark:border-primary-400/30 dark:bg-dark-700/95 dark:text-primary-200;
  box-shadow:
    0 8px 18px rgb(15 23 42 / 0.08),
    0 1px 0 rgb(255 255 255 / 0.92) inset;
}

.settings-tab-active::before {
  opacity: 0;
}

.settings-tab-active::after {
  position: absolute;
  right: 0.75rem;
  bottom: 0.25rem;
  left: 0.75rem;
  height: 2px;
  border-radius: 9999px;
  content: "";
  background: linear-gradient(90deg, #14b8a6, #0ea5e9);
}

.settings-tab-icon {
  @apply flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-gray-500 transition-colors duration-200 dark:text-gray-400;
}

.settings-tab:hover .settings-tab-icon,
.settings-tab:focus-visible .settings-tab-icon {
  @apply text-gray-700 dark:text-gray-200;
}

.settings-tab-active .settings-tab-icon {
  @apply bg-primary-50 text-primary-600 dark:bg-primary-400/10 dark:text-primary-300;
}

.settings-tab-label {
  @apply min-w-0 overflow-hidden text-ellipsis whitespace-nowrap leading-none;
}
</style>

<style>
/* 暗色模式 Tab 覆盖必须放在非 scoped 样式块，避免生产构建丢弃后导致未激活 Tab 不可读。 */
.dark .settings-tabs-shell {
  border-color: rgb(63 63 70 / 0.65);
  background: rgb(24 24 27 / 0.86);
  box-shadow:
    0 16px 36px rgb(0 0 0 / 0.28),
    0 1px 0 rgb(255 255 255 / 0.06) inset;
}

.dark .settings-tab::before {
  background: linear-gradient(135deg, rgb(39 39 42 / 0.9), rgb(63 63 70 / 0.62));
}

.dark .settings-tab-active {
  box-shadow:
    0 12px 26px rgb(0 0 0 / 0.22),
    0 1px 0 rgb(255 255 255 / 0.08) inset;
}
</style>
