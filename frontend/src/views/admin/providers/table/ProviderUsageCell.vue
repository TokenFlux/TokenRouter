<template>
  <div ref="rootRef" v-if="showUsageWindows">
    <!-- Anthropic OAuth and setup-token rows show observed provider usage. -->
    <template
      v-if="
        provider.platform === 'anthropic' &&
        (provider.type === 'oauth' || provider.type === 'setup-token')
      "
    >
      <!-- Loading state -->
      <div v-if="loading" class="space-y-1.5">
        <!-- OAuth: 3 rows, Setup Token: 1 row -->
        <div class="flex items-center gap-1">
          <div class="skeleton h-3 w-[32px]"></div>
          <div class="skeleton h-1.5 w-8 rounded-full"></div>
          <div class="skeleton h-3 w-[32px]"></div>
        </div>
        <template v-if="provider.type === 'oauth'">
          <div class="flex items-center gap-1">
            <div class="skeleton h-3 w-[32px]"></div>
            <div class="skeleton h-1.5 w-8 rounded-full"></div>
            <div class="skeleton h-3 w-[32px]"></div>
          </div>
          <div class="flex items-center gap-1">
            <div class="skeleton h-3 w-[32px]"></div>
            <div class="skeleton h-1.5 w-8 rounded-full"></div>
            <div class="skeleton h-3 w-[32px]"></div>
          </div>
        </template>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>

      <!-- Usage data -->
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- API error (degraded response) -->
        <div
          v-if="usageInfo.error"
          class="text-xs text-amber-600 dark:text-amber-400 truncate max-w-[200px]"
          :title="usageInfo.error"
        >
          {{ usageInfo.error }}
        </div>
        <!-- 5h Window -->
        <UsageProgressBar
          v-if="usageInfo.five_hour"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          color="indigo"
        />

        <!-- 7d Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          color="emerald"
        />

        <!-- 7d Sonnet Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_sonnet"
          label="7d S"
          :utilization="usageInfo.seven_day_sonnet.utilization"
          :resets-at="usageInfo.seven_day_sonnet.resets_at"
          color="purple"
        />

        <!-- Fable-specific seven-day usage window. -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_fable"
          label="7d F"
          :utilization="usageInfo.seven_day_fable.utilization"
          :resets-at="usageInfo.seven_day_fable.resets_at"
          color="amber"
        />

        <!-- Passive sampling label + active query button -->
        <div class="mt-0.5 flex items-center justify-end gap-1.5 lg:justify-start">
          <span
            v-if="usageInfo.source === 'passive'"
            class="text-xs text-gray-400 dark:text-gray-500 italic"
          >
            {{ t('admin.providers.usageWindow.passiveSampled') }}
          </span>
          <button
            type="button"
            class="inline-flex items-center gap-0.5 rounded-compact px-1.5 py-0.5 text-xs font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-500 dark:hover:bg-primary-500/8 transition-colors"
            :disabled="activeQueryLoading"
            @click="loadActiveUsage"
          >
            <Icon
              name="refresh"
              size="xs"
              :class="{ 'animate-spin': activeQueryLoading }"
              :stroke-width="2"
            />
            {{ t('admin.providers.usageWindow.activeQuery') }}
          </button>
        </div>
      </div>

      <!-- No data yet -->
      <div v-else class="space-y-1">
        <div class="text-xs text-gray-400">-</div>
        <!-- Render the probe component in the empty observation branch; its own platform predicate decides whether an explicit quota probe applies. -->
        <GrokQuotaProbeCell :provider="provider" />
      </div>
    </template>

    <!-- OpenAI OAuth rows consume the provider usage response. -->
    <template v-else-if="provider.platform === 'openai' && provider.type === 'oauth'">
      <div v-if="hasOpenAIUsageFallback" class="space-y-1">
        <UsageProgressBar
          v-if="usageInfo?.five_hour"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          :show-now-when-idle="true"
          color="indigo"
        />
        <UsageProgressBar
          v-if="usageInfo?.seven_day"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          :window-stats="usageInfo.seven_day.window_stats"
          :show-now-when-idle="true"
          color="emerald"
        />
        <OpenAIQuotaResetCell :provider="provider">
          <template #pre-actions>
            <button
              type="button"
              class="inline-flex items-center gap-0.5 rounded-compact px-1.5 py-0.5 text-xs font-medium text-primary-600 transition-colors hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-500 dark:hover:bg-primary-500/8"
              :disabled="activeQueryLoading"
              @click="loadActiveUsage"
            >
              <Icon
                name="refresh"
                size="xs"
                :class="{ 'animate-spin': activeQueryLoading }"
                :stroke-width="2"
              />
              {{ t('admin.providers.usageWindow.activeQuery') }}
            </button>
          </template>
          <span
            v-if="openAIQuotaAutoPaused"
            class="inline-flex items-center rounded-compact bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
          >
            {{ t('admin.providers.usageWindow.quotaAutoPaused') }}
          </span>
        </OpenAIQuotaResetCell>
      </div>
      <div v-else-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="skeleton h-3 w-[32px]"></div>
          <div class="skeleton h-1.5 w-8 rounded-full"></div>
          <div class="skeleton h-3 w-[32px]"></div>
        </div>
        <div class="flex items-center gap-1">
          <div class="skeleton h-3 w-[32px]"></div>
          <div class="skeleton h-1.5 w-8 rounded-full"></div>
          <div class="skeleton h-3 w-[32px]"></div>
        </div>
      </div>
      <div v-else>
        <div class="text-xs text-gray-400">-</div>
        <OpenAIQuotaResetCell :provider="provider" class="mt-1" />
      </div>
    </template>

    <!-- Antigravity OAuth providers: fetch usage from API -->
    <template v-else-if="provider.platform === 'antigravity' && provider.type === 'oauth'">
      <!-- Provider entitlement tier badge. -->
      <div v-if="antigravityTierLabel" class="mb-1 flex items-center gap-1">
        <span
          :class="[
            'inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium',
            antigravityTierClass,
          ]"
        >
          {{ antigravityTierLabel }}
        </span>
        <!-- Ineligible entitlement warning. -->
        <span v-if="hasIneligibleTiers" class="group relative cursor-help">
          <Icon name="exclamationCircle" size="xs" class="h-3.5 w-3.5 text-red-500" />
          <span
            class="pointer-events-none absolute left-0 top-full z-50 mt-1 w-80 whitespace-normal break-words tooltip-panel rounded-compact px-3 py-2 text-xs leading-relaxed opacity-0 shadow-lg transition-opacity group-hover:opacity-100"
          >
            {{ t('admin.providers.ineligibleWarning') }}
          </span>
        </span>
      </div>

      <!-- Forbidden state (403) -->
      <div v-if="isForbidden" class="space-y-1">
        <span
          :class="[
            'inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium',
            forbiddenBadgeClass,
          ]"
        >
          {{ forbiddenLabel }}
        </span>
        <div v-if="validationURL" class="flex items-center gap-1">
          <a
            :href="validationURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-xs text-link"
            :title="t('admin.providers.openVerification')"
          >
            {{ t('admin.providers.openVerification') }}
          </a>
          <button
            type="button"
            class="text-xs text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
            :title="t('admin.providers.copyLink')"
            @click="copyValidationURL"
          >
            {{ linkCopied ? t('admin.providers.linkCopied') : t('admin.providers.copyLink') }}
          </button>
        </div>
      </div>

      <!-- Needs reauth (401) -->
      <div v-else-if="needsReauth" class="space-y-1">
        <span
          class="inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300"
        >
          {{ t('admin.providers.needsReauth') }}
        </span>
      </div>

      <!-- Degraded error (non-403, non-401) -->
      <div v-else-if="usageInfo?.error" class="space-y-1">
        <span
          class="inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
        >
          {{ usageErrorLabel }}
        </span>
      </div>

      <!-- Loading state -->
      <div v-else-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="skeleton h-3 w-[32px]"></div>
          <div class="skeleton h-1.5 w-8 rounded-full"></div>
          <div class="skeleton h-3 w-[32px]"></div>
        </div>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>

      <!-- Usage data from API -->
      <div v-else-if="hasAntigravityQuotaFromAPI" class="space-y-1">
        <!-- Gemini 3 Pro -->
        <UsageProgressBar
          v-if="antigravity3ProUsageFromAPI !== null"
          :label="t('admin.providers.usageWindow.gemini3Pro')"
          :utilization="antigravity3ProUsageFromAPI.utilization"
          :resets-at="antigravity3ProUsageFromAPI.resetTime"
          color="indigo"
        />

        <!-- Gemini 3 Flash -->
        <UsageProgressBar
          v-if="antigravity3FlashUsageFromAPI !== null"
          :label="t('admin.providers.usageWindow.gemini3Flash')"
          :utilization="antigravity3FlashUsageFromAPI.utilization"
          :resets-at="antigravity3FlashUsageFromAPI.resetTime"
          color="emerald"
        />

        <!-- Gemini 3 Image -->
        <UsageProgressBar
          v-if="antigravity3ImageUsageFromAPI !== null"
          :label="t('admin.providers.usageWindow.gemini3Image')"
          :utilization="antigravity3ImageUsageFromAPI.utilization"
          :resets-at="antigravity3ImageUsageFromAPI.resetTime"
          color="purple"
        />

        <!-- Claude -->
        <UsageProgressBar
          v-if="antigravityClaudeUsageFromAPI !== null"
          :label="t('admin.providers.usageWindow.claude')"
          :utilization="antigravityClaudeUsageFromAPI.utilization"
          :resets-at="antigravityClaudeUsageFromAPI.resetTime"
          color="amber"
        />

        <div
          v-if="aiCreditsDisplay"
          class="mt-1 flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400"
        >
          <Icon name="creditCard" size="xs" />
          {{ t('admin.providers.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
        </div>
      </div>
      <div
        v-else-if="aiCreditsDisplay"
        class="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400"
      >
        <Icon name="creditCard" size="xs" />
        {{ t('admin.providers.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
      </div>
      <div v-else class="text-xs text-gray-400">-</div>
    </template>

    <!-- Grok OAuth rows combine xAI quota observations with local request accounting. -->
    <template v-else-if="provider.platform === 'grok' && provider.type === 'oauth'">
      <div v-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="skeleton h-3 w-[32px]"></div>
          <div class="skeleton h-1.5 w-8 rounded-full"></div>
          <div class="skeleton h-3 w-[32px]"></div>
        </div>
      </div>
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>
      <div v-else-if="needsReauth" class="space-y-1">
        <span
          class="inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300"
        >
          {{ t('admin.providers.needsReauth') }}
        </span>
      </div>
      <div v-else-if="isForbidden" class="space-y-1">
        <span
          class="inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300"
        >
          {{ usageInfo?.grok_entitlement_status || t('admin.providers.forbidden') }}
        </span>
      </div>
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- Free-plan rows use the local rolling-day token allowance; paid-plan rows expose available weekly, monthly, and prepaid observations. -->
        <template v-if="grokIsFree">
          <UsageProgressBar
            v-if="grokFreeTokenBar"
            label="24h"
            :title="
              t('admin.providers.usageWindow.grokFreeQuota24hHint', {
                limit: formatCompactNumber(grokFreeTokenBar.limit),
              })
            "
            :utilization="grokFreeTokenBar.utilization"
            :window-stats="grokFreeQuotaUsage"
            :show-now-when-idle="true"
            color="emerald"
          />
          <div v-else-if="grokQuotaUnknown" class="text-xs text-gray-500 dark:text-gray-400">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <template v-else>
          <UsageProgressBar
            v-if="grokWeeklyBillingBar"
            label="7d"
            :utilization="grokWeeklyBillingBar.utilization"
            :resets-at="grokWeeklyBillingBar.resetsAt"
            :window-stats="grokWeeklyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <UsageProgressBar
            v-if="grokMonthlyBillingBar"
            label="30d"
            :utilization="grokMonthlyBillingBar.utilization"
            :resets-at="grokMonthlyBillingBar.resetsAt"
            :window-stats="grokMonthlyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <div
            v-if="grokPrepaidMoneyLine"
            class="flex flex-wrap items-center gap-1 text-xs text-gray-500 dark:text-gray-400"
          >
            <span
              v-if="grokPrepaidMoneyLine.showPrepaid"
              class="rounded-compact bg-emerald-50 px-1 py-0.5 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
              :title="t('admin.providers.usageWindow.grokPrepaid')"
            >
              {{ t('admin.providers.usageWindow.grokPrepaid') }} ${{ grokPrepaidMoneyLine.prepaid }}
            </span>
            <span
              v-if="grokPrepaidMoneyLine.showUsedLimit"
              :title="t('admin.providers.usageWindow.grokMonthlyLimit')"
            >
              {{ t('admin.providers.usageWindow.grokUsed') }}
              {{ grokPrepaidMoneyLine.used }}/{{ grokPrepaidMoneyLine.limit }}
            </span>
          </div>
          <div v-if="grokQuotaUnknown" class="text-xs text-gray-500 dark:text-gray-400">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <div
          v-if="usageInfo.error"
          class="truncate text-xs text-amber-600 dark:text-amber-400 max-w-[200px]"
          :title="usageInfo.error"
        >
          {{ usageErrorLabel }}
        </div>
        <div v-if="grokRetryAfterLabel" class="text-xs text-amber-600 dark:text-amber-400">
          {{ t('admin.providers.usageWindow.grokRetryAfter', { time: grokRetryAfterLabel }) }}
        </div>
        <GrokQuotaProbeCell :provider="provider" compact @probed="handleGrokProbed" />
      </div>
      <div v-else class="space-y-1">
        <div class="text-xs text-gray-400">-</div>
        <GrokQuotaProbeCell :provider="provider" compact @probed="handleGrokProbed" />
      </div>
    </template>

    <!-- Qoder Cosy rows display upstream credit-pool observations. -->
    <template v-else-if="provider.platform === 'qoder'">
      <div v-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="skeleton h-3 w-[48px]"></div>
          <div class="skeleton h-1.5 w-8 rounded-full"></div>
          <div class="skeleton h-3 w-[40px]"></div>
        </div>
      </div>
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>
      <div
        v-else-if="usageInfo?.qoder_quota?.display_mode === 'enterprise'"
        class="space-y-1 text-xs"
      >
        <a
          v-if="qoderEnterpriseURL"
          :href="qoderEnterpriseURL"
          target="_blank"
          rel="noopener noreferrer"
          class="text-link"
        >
          {{ t('admin.providers.qoder.viewUpstreamUsage') }}
        </a>
        <span v-else>{{ t('admin.providers.qoder.enterpriseUsage') }}</span>
      </div>
      <div v-else-if="usageInfo?.qoder_quota" class="space-y-1">
        <div
          v-if="usageInfo.error"
          class="truncate text-xs text-amber-600 dark:text-amber-400 max-w-[200px]"
          :title="usageInfo.error"
        >
          {{ usageErrorLabel }}
        </div>
        <UsageProgressBar
          label="Credits"
          :utilization="qoderQuotaUsageBar.utilization"
          :resets-at="qoderQuotaUsageBar.resetsAt"
          wide-label
          color="indigo"
        />
        <div
          v-for="pack in usageInfo.qoder_quota.dedicated_packages"
          :key="pack.id"
          class="text-xs text-gray-500 dark:text-gray-400"
          :title="pack.description"
        >
          {{ pack.name }}: {{ formatCompactNumber(pack.remaining) }}/{{
            formatCompactNumber(pack.total)
          }}
          <span v-for="label in pack.labels" :key="label.dimension + label.value">
            {{ label.value }}</span
          >
        </div>
        <div
          class="flex flex-wrap items-center justify-end gap-1.5 text-xs text-gray-500 dark:text-gray-400 lg:justify-start"
        >
          <span
            class="whitespace-nowrap rounded-compact bg-gray-100 px-1.5 py-0.5 dark:bg-dark-700"
          >
            {{ qoderQuotaCreditsLabel }}
          </span>
          <span
            v-if="usageInfo.qoder_quota.is_quota_exceeded"
            class="rounded-compact bg-amber-100 px-1.5 py-0.5 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300"
          >
            exceeded
          </span>
          <span
            v-if="usageInfo.qoder_quota.snapshot_from_provider"
            class="whitespace-nowrap rounded-compact bg-gray-100 px-1.5 py-0.5 dark:bg-dark-700"
          >
            cached
          </span>
        </div>
        <button
          type="button"
          class="inline-flex items-center gap-0.5 rounded-compact px-1.5 py-0.5 text-xs font-medium text-primary-600 hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-500 dark:hover:bg-primary-500/8 transition-colors"
          :disabled="activeQueryLoading"
          @click="loadActiveUsage"
        >
          <Icon
            name="refresh"
            size="xs"
            :class="{ 'animate-spin': activeQueryLoading }"
            :stroke-width="2"
          />
          {{ t('admin.providers.usageWindow.activeQuery') }}
        </button>
      </div>
      <div v-else-if="usageInfo?.error" class="space-y-1">
        <div
          class="truncate text-xs text-amber-600 dark:text-amber-400 max-w-[200px]"
          :title="usageInfo.error"
        >
          {{ usageErrorLabel }}
        </div>
        <button
          type="button"
          class="inline-flex items-center gap-0.5 rounded-compact px-1.5 py-0.5 text-xs font-medium text-primary-600 hover:bg-primary-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-primary-500 dark:hover:bg-primary-500/8 transition-colors"
          :disabled="activeQueryLoading"
          @click="loadActiveUsage"
        >
          <Icon
            name="refresh"
            size="xs"
            :class="{ 'animate-spin': activeQueryLoading }"
            :stroke-width="2"
          />
          {{ t('admin.providers.usageWindow.activeQuery') }}
        </button>
      </div>
      <div v-else class="text-xs text-gray-400">-</div>
    </template>

    <!-- Gemini platform: show quota + local usage window -->
    <template v-else-if="provider.platform === 'gemini'">
      <ProviderUpstreamUsageCell
        v-if="provider.type === 'apikey'"
        :provider="provider"
        :result="upstreamUsage"
        :error="upstreamUsageError"
        :loading="upstreamUsageLoading"
        :show-query-button="false"
        :request="requestUpstreamUsage"
      />
      <!-- Auth Type + Tier Badge (first line) -->
      <div v-if="geminiAuthTypeLabel" class="mb-1 flex items-center gap-1">
        <span
          :class="[
            'inline-block rounded-compact px-1.5 py-0.5 text-xs font-medium',
            geminiTierClass,
          ]"
        >
          {{ geminiAuthTypeLabel }}
        </span>
        <!-- Help icon -->
        <span class="group relative cursor-help">
          <Icon
            name="questionCircle"
            size="xs"
            class="h-3.5 w-3.5 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
          />
          <span
            class="pointer-events-none absolute left-0 top-full z-50 mt-1 w-80 whitespace-normal break-words tooltip-panel rounded-compact px-3 py-2 text-xs leading-relaxed opacity-0 shadow-lg transition-opacity group-hover:opacity-100"
          >
            <div class="font-semibold mb-1">{{
              t('admin.providers.gemini.quotaPolicy.title')
            }}</div>
            <div class="mb-2 text-gray-300">{{ t('admin.providers.gemini.quotaPolicy.note') }}</div>
            <div class="space-y-1">
              <div
                ><strong>{{ geminiQuotaPolicyChannel }}:</strong></div
              >
              <div class="pl-2">• {{ geminiQuotaPolicyLimits }}</div>
              <div class="mt-2">
                <a
                  :href="geminiQuotaPolicyDocsUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-link"
                >
                  {{ t('admin.providers.gemini.quotaPolicy.columns.docs') }} →
                </a>
              </div>
            </div>
          </span>
        </span>
      </div>

      <!-- Usage data or unlimited flow -->
      <div class="space-y-1">
        <ProviderUsageStatsChips
          v-if="showGeminiTodayStats && todayStats"
          :stats="todayStats"
          scope="today"
        />
        <div
          v-else-if="showGeminiTodayStats && todayStatsLoading"
          class="mb-0.5 flex items-center justify-end gap-1 lg:justify-start"
        >
          <div class="skeleton h-3 w-10"></div>
          <div class="skeleton h-3 w-8"></div>
          <div class="skeleton h-3 w-12"></div>
        </div>
        <div v-if="loading" class="space-y-1">
          <div class="flex items-center gap-1">
            <div class="skeleton h-3 w-[32px]"></div>
            <div class="skeleton h-1.5 w-8 rounded-full"></div>
            <div class="skeleton h-3 w-[32px]"></div>
          </div>
        </div>
        <div v-else-if="error" class="text-xs text-red-500">
          {{ error }}
        </div>
        <!-- Gemini: show daily usage bars when available -->
        <div v-else-if="geminiUsageAvailable" class="space-y-1">
          <UsageProgressBar
            v-for="bar in geminiUsageBars"
            :key="bar.key"
            :label="bar.label"
            :utilization="bar.utilization"
            :resets-at="bar.resetsAt"
            :window-stats="bar.windowStats"
            :color="bar.color"
          />
          <p class="mt-1 text-xs leading-tight text-gray-400 dark:text-gray-500 italic">
            * {{ t('admin.providers.gemini.quotaPolicy.simulatedNote') || 'Simulated quota' }}
          </p>
        </div>
        <!-- A Gemini row with no available usage windows uses the unrestricted display label. -->
        <div v-else class="text-xs text-gray-400">
          {{ t('admin.providers.gemini.rateLimit.unlimited') }}
        </div>
      </div>
    </template>

    <!-- Other providers: no usage window -->
    <template v-else>
      <div
        v-if="
          provider.type !== 'apikey' ||
          (!upstreamUsage && !upstreamUsageError && !upstreamUsageLoading && !upstreamUsageDisabled)
        "
        class="text-xs text-gray-400"
        >-</div
      >
    </template>
  </div>

  <!-- Non-OAuth/Setup-Token providers -->
  <div ref="rootRef" v-else>
    <div v-if="isGeminiThirdPartyProvider" class="space-y-1">
      <ProviderUpstreamUsageCell
        v-if="provider.type === 'apikey'"
        :provider="provider"
        :result="upstreamUsage"
        :error="upstreamUsageError"
        :loading="upstreamUsageLoading"
        :show-query-button="false"
        :request="requestUpstreamUsage"
      />
      <ProviderUsageStatsChips
        v-if="showGeminiTodayStats && todayStats"
        :stats="todayStats"
        scope="today"
      />
      <div
        v-else-if="showGeminiTodayStats && todayStatsLoading"
        class="mb-0.5 flex items-center justify-end gap-1 lg:justify-start"
      >
        <div class="skeleton h-3 w-10"></div>
        <div class="skeleton h-3 w-8"></div>
        <div class="skeleton h-3 w-12"></div>
      </div>
      <div v-else-if="provider.type !== 'apikey'" class="text-xs text-gray-400">-</div>
    </div>
    <!-- Eligible non-API-key Gemini rows display their quota-window observations. -->
    <ProviderQuotaInfo
      v-else-if="provider.platform === 'gemini' && provider.type !== 'apikey'"
      :provider="provider"
    />
    <!-- API-key rows combine explicit upstream observations, local statistics, and configured local spending quotas. -->
    <div v-else class="space-y-1">
      <ProviderUpstreamUsageCell
        v-if="provider.type === 'apikey'"
        :provider="provider"
        :result="upstreamUsage"
        :error="upstreamUsageError"
        :loading="upstreamUsageLoading"
        :show-query-button="false"
        :request="requestUpstreamUsage"
      />
      <ProviderQuotaInfo
        v-if="provider.platform === 'gemini' && provider.type === 'apikey'"
        :provider="provider"
      />
      <OllamaCloudUsageCell
        v-if="provider.ollama_cloud_usage?.eligible"
        :provider="provider"
        @updated="handleOllamaCloudUsageUpdated"
      />
      <!-- Daily accounting includes requests, tokens, provider USD cost, and optional user charges. -->
      <ProviderUsageStatsChips v-if="todayStats" :stats="todayStats" scope="today" />
      <!-- Loading skeleton for today stats -->
      <div
        v-else-if="todayStatsLoading"
        class="mb-0.5 flex items-center justify-end gap-1 lg:justify-start"
      >
        <div class="skeleton h-3 w-10"></div>
        <div class="skeleton h-3 w-8"></div>
        <div class="skeleton h-3 w-12"></div>
      </div>

      <!-- Eligible API-key and Bedrock records display each positive local spending limit. -->
      <UsageProgressBar
        v-if="quotaDailyBar"
        label="1d"
        :utilization="quotaDailyBar.utilization"
        :resets-at="quotaDailyBar.resetsAt"
        color="indigo"
      />
      <UsageProgressBar
        v-if="quotaWeeklyBar"
        label="7d"
        :utilization="quotaWeeklyBar.utilization"
        :resets-at="quotaWeeklyBar.resetsAt"
        color="emerald"
      />
      <UsageProgressBar
        v-if="quotaMonthlyBar"
        :label="provider.quota_monthly_reset_mode === 'fixed' ? 'M' : '30d'"
        :utilization="quotaMonthlyBar.utilization"
        :resets-at="quotaMonthlyBar.resetsAt"
        color="emerald"
      />
      <UsageProgressBar
        v-if="quotaTotalBar"
        label="total"
        :utilization="quotaTotalBar.utilization"
        color="purple"
      />

      <!-- No data at all -->
      <div
        v-if="
          !todayStats &&
          !todayStatsLoading &&
          !hasApiKeyQuota &&
          !provider.ollama_cloud_usage?.eligible &&
          provider.type !== 'apikey'
        "
        class="text-xs text-gray-400"
        >-</div
      >
    </div>
  </div>
  <!-- The explicit upstream-query action follows local statistics and quotas in the owning cell. -->
  <div
    v-if="isUpstreamUsageQueryEnabled(provider)"
    class="mt-0.5 flex items-center justify-end gap-1.5 lg:justify-start"
  >
    <ProviderUpstreamUsageQueryButton
      :provider="provider"
      :loading="upstreamUsageLoading"
      :request="requestUpstreamUsage"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { COPY_FEEDBACK_MS } from '@/constants/ui'
import type {
  Provider,
  ProviderUsageInfo,
  GeminiCredentials,
  WindowStats,
  UpstreamUsageQueryError,
  UpstreamUsageQueryResult,
} from '@/api/admin/providers.types'
import { buildOpenAIUsageRefreshKey } from '@/utils/providerUsageRefresh'
import { isUpstreamUsageQueryEnabled } from '@/utils/upstreamUsage'
import { enqueueUsageRequest } from '@/utils/usageLoadQueue'
import { formatCompactNumber } from '@/utils/format'
import { useDesktopTableViewport } from '@/composables/useDesktopTableViewport'
import Icon from '@/components/icons/Icon.vue'
import {
  ANTIGRAVITY_CLAUDE_MODELS,
  ANTIGRAVITY_FLASH_MODELS,
  ANTIGRAVITY_IMAGE_MODELS,
  ANTIGRAVITY_PRO_MODELS,
  buildGeminiUsageBars,
  buildLocalQuotaBar,
  getAntigravityTierBadgeClass,
  getAntigravityTierId,
  getAntigravityTierLabelKey,
  hasAntigravityIneligibleTiers,
  resolveQoderEnterpriseUrl,
  summarizeAntigravityFamilyUsage,
  summarizeGeminiChannel,
  summarizeGrokUsage,
  summarizeQoderQuota,
  type QuotaBarInfo,
} from './providerUsagePresentation'
import UsageProgressBar from './UsageProgressBar.vue'
import ProviderQuotaInfo from './ProviderQuotaInfo.vue'
import OpenAIQuotaResetCell from './OpenAIQuotaResetCell.vue'
import GrokQuotaProbeCell from './GrokQuotaProbeCell.vue'
import OllamaCloudUsageCell from './OllamaCloudUsageCell.vue'
import ProviderUpstreamUsageCell from './ProviderUpstreamUsageCell.vue'
import ProviderUsageStatsChips from './ProviderUsageStatsChips.vue'
import ProviderUpstreamUsageQueryButton from './ProviderUpstreamUsageQueryButton.vue'

/**
 * ProviderUsageCell.vue presents upstream usage observations, provider-specific entitlement
 * details, and local spending progress in provider table rows. Platform and authentication type
 * determine which observations apply. API-key upstream queries remain explicit parent-owned
 * actions, while eligible OAuth or Cosy records can use the page's batched loader or this
 * component's local request queue.
 *
 * Local loading reuses an instance-owned five-minute cache and defers mobile requests until the
 * row approaches the viewport. Parent-managed usage bypasses that local publication path. Local
 * quota bars combine effective server usage with fixed reset metadata or elapsed-day deadlines,
 * while Grok and Qoder presentations distinguish observed zero values from missing capacity.
 * Ordinary queued loads suppress writes after unmount; manual active queries have a separate
 * completion path.
 */

/**
 * _usageCache and USAGE_CACHE_TTL retain successful local observations for this setup instance;
 * Vue creates a separate cache for each mounted cell.
 */
const _usageCache = new Map<
  number,
  {
    data: ProviderUsageInfo // cached provider usage observation
    ts: number // cache publication epoch milliseconds
  }
>()
const USAGE_CACHE_TTL = 5 * 60 * 1000 // five minutes in milliseconds

/**
 * props supplies the provider record and optional parent-managed usage channels, separating
 * daily statistics, batched OAuth usage, and explicit upstream queries.
 */
const props = withDefaults(
  defineProps<{
    provider: Provider // current provider row record
    todayStats?: WindowStats | null // optional local daily observations
    todayStatsLoading?: boolean // parent daily statistics request activity
    /**
     * Changing this token requests a fresh eligible usage observation, delegating force to the
     * parent when batch loading is configured.
     */
    manualRefreshToken?: number
    batchedUsage?: ProviderUsageInfo | null // parent-managed usage observation
    batchedUsageError?: string | null // parent-managed usage request failure
    batchedUsageLoading?: boolean // parent-managed usage request activity
    /**
     * Presence enables parent-managed loading; the parent publishes result, error, and loading
     * props for this cell.
     */
    requestBatchedUsage?:
      | ((
          provider: Provider,
          options?: {
            force?: boolean // bypass owning loader cache
          },
        ) => void)
      | null
    upstreamUsage?: UpstreamUsageQueryResult | null // parent manual upstream query result
    upstreamUsageError?: UpstreamUsageQueryError | null // parent manual upstream query error
    upstreamUsageLoading?: boolean // parent manual upstream query activity
    /**
     * Delegates explicit API-key upstream queries to the parent, which owns their result and
     * request state.
     */
    requestUpstreamUsage?:
      | ((
          provider: Provider,
          options?: {
            force?: boolean // bypass owning loader cache
          },
        ) => void)
      | null
  }>(),
  {
    todayStats: null,
    todayStatsLoading: false,
    manualRefreshToken: 0,
    batchedUsage: null,
    batchedUsageError: null,
    batchedUsageLoading: false,
    requestBatchedUsage: null,
    upstreamUsage: null,
    upstreamUsageError: null,
    upstreamUsageLoading: false,
    requestUpstreamUsage: null,
  },
)

/**
 * emit returns an updated provider record after the Ollama Cloud child refreshes its saved
 * usage observation for this table row.
 */
const emit = defineEmits<{
  /**
   * Emits a copied provider with refreshed Ollama Cloud usage so the parent can replace the
   * displayed row.
   */
  'provider-updated': [provider: Provider]
}>()

/**
 * Translation and browser capability detection initialize labels and viewport behavior before
 * the mounted row begins automatic usage loading.
 */
const { t } = useI18n()
const isDesktopViewport = useDesktopTableViewport()

/**
 * unmounted, localUsageRequestSeq, and copyFeedbackTimer guard asynchronous
 * usage responses and clipboard timers against component teardown and virtualized
 * row-identity switches.
 */
const unmounted = ref(false)
let localUsageRequestSeq = 0
let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null

/**
 * clearCopyFeedbackTimer cancels any pending verification-URL copy badge reset.
 */
const clearCopyFeedbackTimer = () => {
  if (copyFeedbackTimer !== null) {
    clearTimeout(copyFeedbackTimer)
    copyFeedbackTimer = null
  }
}

onBeforeUnmount(() => {
  unmounted.value = true
  localUsageRequestSeq++
  clearCopyFeedbackTimer()
})

/**
 * Local request feedback and usage observations support autonomous loading, while
 * parent-managed mode copies its supplied state into these same display refs.
 */
const loading = ref(false)
const activeQueryLoading = ref(false)
const error = ref<string | null>(null)
const usageInfo = ref<ProviderUsageInfo | null>(null)
const rootRef = ref<HTMLElement | null>(null)
const hasEnteredViewport = ref(false)
const pendingAutoLoad = ref(false)
const pendingAutoLoadSource = ref<'passive' | 'active' | undefined>(undefined)

/**
 * visibilityObserver defers eligible mobile loads until first viewport entry and
 * is disconnected on teardown or desktop transitions.
 */
let visibilityObserver: IntersectionObserver | null = null

/**
 * Platform and credential predicates distinguish official Gemini usage from third-party API-key
 * upstream balances, which follow a separate explicit query path.
 */
const isGeminiThirdPartyProvider = computed(() => {
  if (props.provider.platform !== 'gemini' || props.provider.type !== 'apikey') return false
  const credentials = props.provider.credentials as GeminiCredentials | undefined
  return credentials?.provider_type === 'third_party'
})

/**
 * upstreamUsageDisabled reads an explicit API-key query disable flag, allowing the template to
 * explain why only local observations are displayed.
 */
const upstreamUsageDisabled = computed(() => {
  if (props.provider.type !== 'apikey') return false
  const config = props.provider.extra?.upstream_usage_query as Record<string, unknown> | undefined
  return config?.enabled === false
})

/**
 * showUsageWindows chooses OAuth, setup-token, and Cosy usage presentations while leaving
 * API-key upstream querying to the separate explicit-query components.
 */
const showUsageWindows = computed(() => {
  // API-key balances use the explicit parent query path, so mounting or entering the viewport
  // does not trigger the OAuth usage loader.
  if (props.provider.type === 'apikey') return false
  // Third-party Gemini credentials select upstream querying instead of official Gemini channel
  // and quota presentation for the provider row.
  if (props.provider.platform === 'gemini') return !isGeminiThirdPartyProvider.value
  if (props.provider.platform === 'qoder') return props.provider.type === 'cosy'
  return props.provider.type === 'oauth' || props.provider.type === 'setup-token'
})

/**
 * shouldFetchUsage restricts automatic usage requests to supported platform and authentication
 * combinations, with parent batching selected by callback presence.
 */
const shouldFetchUsage = computed(() => {
  if (props.provider.platform === 'anthropic') {
    return props.provider.type === 'oauth' || props.provider.type === 'setup-token'
  }
  if (props.provider.platform === 'gemini') {
    return props.provider.type !== 'apikey' && !isGeminiThirdPartyProvider.value
  }
  if (props.provider.platform === 'antigravity') {
    return props.provider.type === 'oauth'
  }
  if (props.provider.platform === 'grok') {
    return props.provider.type === 'oauth'
  }
  if (props.provider.platform === 'qoder') {
    return props.provider.type === 'cosy'
  }
  if (props.provider.platform === 'openai') {
    return props.provider.type === 'oauth'
  }
  return false
})

const isBatchManaged = computed(() => typeof props.requestBatchedUsage === 'function')

/**
 * Gemini display predicates distinguish local daily statistics, available quota observations,
 * and OpenAI fallback windows before choosing the template branches.
 */
const showGeminiTodayStats = computed(() => {
  return (
    props.provider.platform === 'gemini' &&
    (props.provider.type === 'service_account' || props.provider.type === 'apikey')
  )
})

const geminiUsageAvailable = computed(() => {
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_pro_daily ||
    !!usageInfo.value?.gemini_flash_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    !!usageInfo.value?.gemini_pro_minute ||
    !!usageInfo.value?.gemini_flash_minute
  )
})

/**
 * OpenAI window and pause indicators prefer available usage observations, then provider-level
 * pause metadata when the usage response omits that state.
 */
const hasOpenAIUsageFallback = computed(() => {
  if (props.provider.platform !== 'openai' || props.provider.type !== 'oauth') return false
  return !!usageInfo.value?.five_hour || !!usageInfo.value?.seven_day
})

const openAIQuotaAutoPaused = computed(() => {
  if (props.provider.platform !== 'openai' || props.provider.type !== 'oauth') return false
  return usageInfo.value?.quota_auto_paused ?? props.provider.quota_auto_paused ?? false
})

/**
 * The OpenAI refresh key and viewport predicates decide when stored provider metadata
 * invalidates cached usage and when mobile loading waits for row visibility.
 */
const openAIUsageRefreshKey = computed(() => buildOpenAIUsageRefreshKey(props.provider))

const shouldAutoLoadUsageOnMount = computed(() => {
  return shouldFetchUsage.value
})

const shouldLazyLoadOnMobile = computed(() => {
  return shouldFetchUsage.value && !isDesktopViewport.value
})

/**
 * Antigravity quota availability requires a nonempty returned quota map; family summaries then
 * select matching model observations through providerUsagePresentation.
 */
const hasAntigravityQuotaFromAPI = computed(() => {
  return (
    usageInfo.value?.antigravity_quota && Object.keys(usageInfo.value.antigravity_quota).length > 0
  )
})

const antigravity3ProUsageFromAPI = computed(() =>
  summarizeAntigravityFamilyUsage(usageInfo.value?.antigravity_quota, ANTIGRAVITY_PRO_MODELS),
)
const antigravity3FlashUsageFromAPI = computed(() =>
  summarizeAntigravityFamilyUsage(usageInfo.value?.antigravity_quota, ANTIGRAVITY_FLASH_MODELS),
)
const antigravity3ImageUsageFromAPI = computed(() =>
  summarizeAntigravityFamilyUsage(usageInfo.value?.antigravity_quota, ANTIGRAVITY_IMAGE_MODELS),
)
const antigravityClaudeUsageFromAPI = computed(() =>
  summarizeAntigravityFamilyUsage(usageInfo.value?.antigravity_quota, ANTIGRAVITY_CLAUDE_MODELS),
)

/**
 * aiCreditsDisplay totals observed credit amounts and shows a rounded integer only when the
 * resulting available amount is positive.
 */
const aiCreditsDisplay = computed(() => {
  const credits = usageInfo.value?.ai_credits
  if (!credits || credits.length === 0) return null
  const total = credits.reduce((sum, credit) => sum + (credit.amount ?? 0), 0)
  if (total <= 0) return null
  return total.toFixed(0)
})

/**
 * Gemini, Antigravity, Qoder, and Grok presentation projections derive badge
 * labels, quota-policy keys, and progress bars from providerUsagePresentation.
 */
const antigravityTier = computed(() => getAntigravityTierId(props.provider))
const geminiChannelSummary = computed(() => summarizeGeminiChannel(props.provider))
const geminiAuthTypeLabel = computed(() => geminiChannelSummary.value.authTypeLabel)
const geminiTierClass = computed(() => geminiChannelSummary.value.tierClass)
const geminiQuotaPolicyChannel = computed(() => t(geminiChannelSummary.value.quotaPolicyChannelKey))
const geminiQuotaPolicyLimits = computed(() => t(geminiChannelSummary.value.quotaPolicyLimitsKey))
const geminiQuotaPolicyDocsUrl = computed(() => geminiChannelSummary.value.quotaPolicyDocsUrl)
const geminiUsageBars = computed(() =>
  buildGeminiUsageBars(props.provider.platform, geminiChannelSummary.value, usageInfo.value),
)

const qoderEnterpriseURL = computed(() => resolveQoderEnterpriseUrl(usageInfo.value))
const qoderSummary = computed(() => summarizeQoderQuota(usageInfo.value))
const qoderQuotaUsageBar = computed(() => qoderSummary.value.usageBar)
const qoderQuotaCreditsLabel = computed(() => qoderSummary.value.creditsLabel)

const grokSummary = computed(() => summarizeGrokUsage(props.provider, usageInfo.value))
const grokWeeklyBillingBar = computed(() => grokSummary.value.weeklyBillingBar)
const grokMonthlyBillingBar = computed(() => grokSummary.value.monthlyBillingBar)
const grokPrepaidMoneyLine = computed(() => grokSummary.value.prepaidMoneyLine)
const grokIsFree = computed(() => grokSummary.value.isFree)
const grokFreeQuotaUsage = computed(() => grokSummary.value.freeQuotaUsage)
const grokFreeTokenBar = computed(() => grokSummary.value.freeTokenBar)
const grokQuotaUnknown = computed(() => grokSummary.value.quotaUnknown)
const grokQuotaUnknownLabel = computed(() => {
  return usageInfo.value?.grok_quota_snapshot_state === 'no_headers'
    ? t('admin.providers.usageWindow.grokNoHeaders')
    : t('admin.providers.usageWindow.grokUnknown')
})
const grokRetryAfterLabel = computed(() => grokSummary.value.retryAfterLabel)

const antigravityTierLabel = computed(() => {
  const key = getAntigravityTierLabelKey(antigravityTier.value)
  return key ? t(key) : null
})
const antigravityTierClass = computed(() => getAntigravityTierBadgeClass(antigravityTier.value))
const hasIneligibleTiers = computed(() =>
  hasAntigravityIneligibleTiers(props.provider.extra as Record<string, unknown> | undefined),
)

/**
 * Authorization feedback distinguishes forbidden verification or violation states, missing
 * authorization, and other degraded usage errors while retaining any verification URL.
 */
const isForbidden = computed(() => !!usageInfo.value?.is_forbidden)
const forbiddenType = computed(() => usageInfo.value?.forbidden_type || 'forbidden')
const validationURL = computed(() => usageInfo.value?.validation_url || '')

const needsReauth = computed(() => !!usageInfo.value?.needs_reauth)

/**
 * Usage error and forbidden labels classify server response flags into localized badges, with
 * verification failures using the warning palette.
 */
const usageErrorLabel = computed(() => {
  const code = usageInfo.value?.error_code
  if (code === 'rate_limited') return t('admin.providers.rateLimited')
  return t('admin.providers.usageError')
})

const forbiddenLabel = computed(() => {
  switch (forbiddenType.value) {
    case 'validation':
      return t('admin.providers.forbiddenValidation')
    case 'violation':
      return t('admin.providers.forbiddenViolation')
    default:
      return t('admin.providers.forbidden')
  }
})

const forbiddenBadgeClass = computed(() => {
  if (forbiddenType.value === 'validation') {
    return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  }
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
})

/**
 * linkCopied controls brief copy feedback for the verification URL while the visible link
 * remains available regardless of clipboard support.
 */
const linkCopied = ref(false)
/**
 * copyValidationURL copies a present verification URL and briefly shows success feedback,
 * replacing any prior timer so repeated clicks or row teardown cannot fire stale callbacks.
 */
const copyValidationURL = async () => {
  if (!validationURL.value) return
  try {
    await navigator.clipboard.writeText(validationURL.value)
    linkCopied.value = true
    clearCopyFeedbackTimer()
    copyFeedbackTimer = setTimeout(() => {
      copyFeedbackTimer = null
      if (!unmounted.value) {
        linkCopied.value = false
      }
    }, COPY_FEEDBACK_MS)
  } catch {
    // Keep the verification link usable after clipboard rejection; copy feedback stays
    // unchanged because no successful write was observed.
  }
}

/**
 * Anthropic OAuth and setup-token rows request passive observations during automatic loading;
 * the explicit active action uses its separate fresh-query path.
 */
const isAnthropicOAuthOrSetupToken = computed(() => {
  return (
    props.provider.platform === 'anthropic' &&
    (props.provider.type === 'oauth' || props.provider.type === 'setup-token')
  )
})

/**
 * requestParentBatchUsage delegates eligible usage loading to the parent batch callback. It
 * does nothing when batching is absent or this provider type is excluded from automatic usage
 * fetching.
 */
const requestParentBatchUsage = (options?: {
  force?: boolean // bypass owning loader cache
}) => {
  if (!isBatchManaged.value || !shouldFetchUsage.value) return
  props.requestBatchedUsage?.(props.provider, options)
}

/**
 * syncManagedUsageState copies parent-supplied usage, error, and loading values into local
 * display refs. It runs only for parent-managed loading, using null for missing observations
 * and false for absent activity.
 */
const syncManagedUsageState = () => {
  if (!isBatchManaged.value) return
  usageInfo.value = props.batchedUsage ?? null
  error.value = props.batchedUsageError ?? null
  loading.value = props.batchedUsageLoading === true
}

/**
 * loadUsage obtains usage through the parent batch loader or the local request queue for
 * eligible providers. Local requests capture both localUsageRequestSeq and provider.id so a
 * virtualized row switch or a newer manual refresh rejects stale responses while still
 * populating _usageCache under the queried provider's own ID.
 */
const loadUsage = async (options?: {
  source?: 'passive' | 'active' // optional active or passive query
  bypassCache?: boolean // request a fresh usage observation
}) => {
  if (!shouldFetchUsage.value) return
  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: options?.bypassCache === true })
    return
  }

  const requestProvider = props.provider
  const requestProviderId = requestProvider.id

  // Reuse a fresh instance-local observation before entering the shared request queue and
  // advance localUsageRequestSeq so any earlier in-flight request cannot overwrite it.
  if (!options?.bypassCache) {
    const cached = _usageCache.get(requestProviderId)
    if (cached && Date.now() - cached.ts < USAGE_CACHE_TTL) {
      localUsageRequestSeq++
      usageInfo.value = cached.data
      error.value = null
      loading.value = false
      return
    }
  }

  const requestSeq = ++localUsageRequestSeq
  loading.value = true
  error.value = null

  try {
    /**
     * fetchFn requests provider usage using the optional active or passive source selector.
     */
    const fetchFn = () =>
      options?.source
        ? adminAPI.providers.getUsage(requestProviderId, options.source)
        : adminAPI.providers.getUsage(requestProviderId)
    const result = await enqueueUsageRequest(requestProvider, fetchFn)
    if (!unmounted.value) {
      _usageCache.set(requestProviderId, { data: result, ts: Date.now() })
      if (requestSeq === localUsageRequestSeq && props.provider.id === requestProviderId) {
        usageInfo.value = result
      }
    }
  } catch (e: any) {
    if (
      !unmounted.value &&
      requestSeq === localUsageRequestSeq &&
      props.provider.id === requestProviderId
    ) {
      error.value = t('common.error')
      console.error('Failed to load usage:', e)
    }
  } finally {
    if (
      !unmounted.value &&
      requestSeq === localUsageRequestSeq &&
      props.provider.id === requestProviderId
    ) {
      loading.value = false
    }
  }
}

/**
 * flushPendingAutoLoad consumes a deferred mobile request and clears its pending source before
 * dispatch. Loading failures are logged, and subsequent visibility changes do not replay that
 * consumed pending marker.
 */
const flushPendingAutoLoad = () => {
  if (!pendingAutoLoad.value) return
  const source = pendingAutoLoadSource.value
  pendingAutoLoad.value = false
  pendingAutoLoadSource.value = undefined
  loadUsage({ source }).catch((e) => {
    console.error('Failed to load deferred usage:', e)
  })
}

/**
 * requestAutoLoad requests eligible usage immediately or records a pending request for an
 * unseen mobile row. Repeated deferred calls replace the pending source, and viewport entry
 * later dispatches the retained request.
 */
const requestAutoLoad = (source?: 'passive' | 'active') => {
  if (!shouldFetchUsage.value) return
  if (shouldLazyLoadOnMobile.value && !hasEnteredViewport.value) {
    pendingAutoLoad.value = true
    pendingAutoLoadSource.value = source
    return
  }
  loadUsage({ source }).catch((e) => {
    console.error('Failed to auto load usage:', e)
  })
}

/**
 * detachVisibilityObserver disconnects and clears the row visibility observer. Callers use it
 * before replacement, after first entry, on desktop transition, and during component teardown
 * to release observation ownership.
 */
const detachVisibilityObserver = () => {
  // Disconnect before clearing the handle so the observer releases the row even when teardown
  // is repeated from multiple lifecycle paths.
  visibilityObserver?.disconnect()
  visibilityObserver = null
}

/**
 * attachVisibilityObserver installs visibility observation for an unseen mobile row after
 * releasing any previous observer. Rows already seen or using desktop loading need no observer.
 * When the browser lacks observation support, the row is marked entered and pending work is
 * released immediately.
 *
 * The observer watches with a two-hundred-pixel vertical margin and a small intersection
 * threshold so loading can begin before the row reaches the visible viewport. Its first
 * intersecting callback marks entry, disconnects observation, and then consumes deferred work.
 * The caller invokes teardown when the row unmounts or changes viewport mode.
 */
const attachVisibilityObserver = () => {
  detachVisibilityObserver()
  if (!shouldLazyLoadOnMobile.value || hasEnteredViewport.value) return
  if (typeof window === 'undefined' || typeof IntersectionObserver === 'undefined') {
    // Without observation support, release pending work immediately so mobile rows still obtain
    // usage instead of waiting for an unavailable visibility signal.
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  if (!rootRef.value) return

  visibilityObserver = new IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return
      hasEnteredViewport.value = true
      detachVisibilityObserver()
      flushPendingAutoLoad()
    },
    {
      root: null,
      // Begin mobile loading before the row reaches the viewport.
      rootMargin: '200px 0px',
      threshold: 0.01,
    },
  )
  visibilityObserver.observe(rootRef.value)
}

/**
 * loadActiveUsage explicitly queries fresh active usage and guards response publication
 * with localUsageRequestSeq and provider.id so virtualized row switches or unmount drop
 * stale active-query results.
 */
const loadActiveUsage = async () => {
  const requestProviderId = props.provider.id
  const requestSeq = ++localUsageRequestSeq
  activeQueryLoading.value = true
  error.value = null
  try {
    const result = await adminAPI.providers.getUsage(requestProviderId, 'active', true)
    if (!unmounted.value) {
      _usageCache.set(requestProviderId, { data: result, ts: Date.now() })
      if (requestSeq === localUsageRequestSeq && props.provider.id === requestProviderId) {
        usageInfo.value = result
      }
    }
  } catch (e: any) {
    console.error('Failed to load active usage:', e)
  } finally {
    if (
      !unmounted.value &&
      requestSeq === localUsageRequestSeq &&
      props.provider.id === requestProviderId
    ) {
      activeQueryLoading.value = false
    }
  }
}

/**
 * handleGrokProbed reloads active usage after a quota probe updates the server snapshot. Cache
 * bypass is requested so the usage bar and entitlement display can reflect the newly observed
 * quota state.
 */
const handleGrokProbed = async () => {
  await loadUsage({ source: 'active', bypassCache: true })
}

/**
 * makeQuotaBar delegates local spending percentage and periodic deadline derivation to
 * providerUsagePresentation.
 */
const makeQuotaBar = (used: number, limit: number, startKey?: string): QuotaBarInfo =>
  buildLocalQuotaBar(
    props.provider.extra as Record<string, unknown> | undefined,
    used,
    limit,
    startKey,
  )

/**
 * Local quota visibility requires an API-key or Bedrock provider with at least one positive
 * total, daily, weekly, or monthly spending limit.
 */
const hasApiKeyQuota = computed(() => {
  if (props.provider.type !== 'apikey' && props.provider.type !== 'bedrock') return false
  return (
    (props.provider.quota_daily_limit ?? 0) > 0 ||
    (props.provider.quota_weekly_limit ?? 0) > 0 ||
    (props.provider.quota_monthly_limit ?? 0) > 0 ||
    (props.provider.quota_limit ?? 0) > 0
  )
})

/**
 * Each periodic local quota bar is created independently from its positive limit and effective
 * usage, with total quota retaining no reset deadline.
 */
const quotaDailyBar = computed((): QuotaBarInfo | null => {
  const limit = props.provider.quota_daily_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.provider.quota_daily_used ?? 0, limit, 'quota_daily_start')
})

const quotaWeeklyBar = computed((): QuotaBarInfo | null => {
  const limit = props.provider.quota_weekly_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.provider.quota_weekly_used ?? 0, limit, 'quota_weekly_start')
})

/**
 * quotaMonthlyBar displays effective monthly spending against its positive USD limit and
 * derives its deadline through the shared local-quota calculation.
 */
const quotaMonthlyBar = computed((): QuotaBarInfo | null => {
  const limit = props.provider.quota_monthly_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.provider.quota_monthly_used ?? 0, limit, 'quota_monthly_start')
})

const quotaTotalBar = computed((): QuotaBarInfo | null => {
  const limit = props.provider.quota_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.provider.quota_used ?? 0, limit)
})

/**
 * handleOllamaCloudUsageUpdated emits a copied provider record with the refreshed cloud
 * snapshot.
 */
const handleOllamaCloudUsageUpdated = (state: NonNullable<Provider['ollama_cloud_usage']>) => {
  emit('provider-updated', { ...props.provider, ollama_cloud_usage: state })
}

onMounted(() => {
  if (isBatchManaged.value) {
    syncManagedUsageState()
    requestParentBatchUsage()
    return
  }

  if (!shouldAutoLoadUsageOnMount.value) return
  const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
  requestAutoLoad(source)
})

watch(
  () =>
    [
      props.batchedUsage,
      props.batchedUsageError,
      props.batchedUsageLoading,
      isBatchManaged.value,
    ] as const,
  () => {
    syncManagedUsageState()
  },
  { immediate: true },
)

watch(isBatchManaged, (managed, wasManaged) => {
  if (managed && !wasManaged) {
    syncManagedUsageState()
    requestParentBatchUsage()
  }
})

watch(
  () =>
    [
      props.provider.id,
      props.provider.platform,
      props.provider.type,
      isBatchManaged.value,
    ] as const,
  (
    [providerID, platform, providerType, managed],
    [previousProviderID, previousPlatform, previousProviderType],
  ) => {
    if (
      providerID === previousProviderID &&
      platform === previousPlatform &&
      providerType === previousProviderType
    ) {
      return
    }
    // Invalidate in-flight local requests and clear previous-row state when virtual
    // scrolling rebinds this cell to a different provider record.
    localUsageRequestSeq++
    activeQueryLoading.value = false
    if (managed) {
      syncManagedUsageState()
      if (shouldFetchUsage.value) {
        requestParentBatchUsage()
      }
      return
    }
    usageInfo.value = null
    error.value = null
    loading.value = false
    if (!shouldFetchUsage.value) return
    const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
    requestAutoLoad(source)
  },
  { flush: 'post' },
)

watch(openAIUsageRefreshKey, (nextKey, prevKey) => {
  if (!prevKey || nextKey === prevKey) return
  if (props.provider.platform !== 'openai' || props.provider.type !== 'oauth') return

  // Changed OpenAI quota metadata invalidates the prior local observation before requesting
  // usage for the updated saved provider state.
  _usageCache.delete(props.provider.id)
  usageInfo.value = null
  requestAutoLoad()
})

watch(
  () => props.manualRefreshToken,
  (nextToken, prevToken) => {
    if (nextToken === prevToken) return
    if (!shouldFetchUsage.value) return

    if (isBatchManaged.value) {
      requestParentBatchUsage({ force: true })
      return
    }

    const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
    // Manual refresh invalidates the local observation before loading so the row cannot
    // immediately reuse the same cached response.
    _usageCache.delete(props.provider.id)
    loadUsage({ source, bypassCache: true }).catch((e) => {
      console.error('Failed to refresh usage after manual refresh:', e)
    })
  },
)

watch(
  [rootRef, shouldLazyLoadOnMobile],
  () => {
    if (shouldLazyLoadOnMobile.value) {
      attachVisibilityObserver()
      return
    }
    detachVisibilityObserver()
  },
  { immediate: true, flush: 'post' },
)

watch(isDesktopViewport, (isDesktop) => {
  if (isDesktop) {
    detachVisibilityObserver()
    // Desktop rows load immediately, so release any request deferred while this same mounted
    // cell was waiting for mobile viewport entry.
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  hasEnteredViewport.value = false
  attachVisibilityObserver()
})

onUnmounted(() => {
  detachVisibilityObserver()
  clearCopyFeedbackTimer()
})
</script>
