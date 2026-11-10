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
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'
import Icon from '@/components/icons/Icon.vue'
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
const canUseMatchMedia = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
/**
 * getDesktopViewportMatches reads the shared desktop breakpoint when matchMedia is available.
 * Environments without that browser API default to desktop behavior, allowing usage loading
 * without a viewport observer dependency.
 */
const getDesktopViewportMatches = () =>
  canUseMatchMedia ? window.matchMedia(TABLE_DESKTOP_MEDIA_QUERY).matches : true

/**
 * unmounted suppresses ordinary queued-load publication after teardown; request activity,
 * observations, and viewport-entry state remain local to the mounted cell.
 */
const unmounted = ref(false)
onBeforeUnmount(() => {
  unmounted.value = true
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
const isDesktopViewport = ref(getDesktopViewportMatches())
const hasEnteredViewport = ref(false)
const pendingAutoLoad = ref(false)
const pendingAutoLoadSource = ref<'passive' | 'active' | undefined>(undefined)

/**
 * Viewport handles belong to this row and are released on unmount; visibility observation
 * defers eligible mobile loads until first entry.
 */
let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null
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
 * AntigravityUsageResult summarizes utilization and earliest reset for one model family.
 */
interface AntigravityUsageResult {
  utilization: number // observed utilization percentage
  resetTime: string | null // earliest reported reset timestamp
}

/**
 * Antigravity quota availability requires a nonempty returned quota map; family summaries then
 * select matching model observations from that map.
 */
const hasAntigravityQuotaFromAPI = computed(() => {
  return (
    usageInfo.value?.antigravity_quota && Object.keys(usageInfo.value.antigravity_quota).length > 0
  )
})

/**
 * getAntigravityUsageFromAPI summarizes recognized model observations for one display family.
 * It chooses the highest observed utilization and the lexically earliest nonempty reset
 * timestamp among matching models. A missing quota map or no matching entries returns null.
 *
 * A matching entry with zero utilization still produces a result even when no reset is present,
 * keeping observed zero separate from an unknown family. The reset and utilization can
 * originate from different model entries, so the summary represents conservative family-level
 * display values rather than one model's complete observation.
 */
const getAntigravityUsageFromAPI = (modelNames: string[]): AntigravityUsageResult | null => {
  const quota = usageInfo.value?.antigravity_quota
  if (!quota) return null

  let maxUtilization = 0
  let earliestReset: string | null = null

  for (const model of modelNames) {
    const modelQuota = quota[model]
    if (!modelQuota) continue

    if (modelQuota.utilization > maxUtilization) {
      maxUtilization = modelQuota.utilization
    }
    if (modelQuota.reset_time) {
      if (!earliestReset || modelQuota.reset_time < earliestReset) {
        earliestReset = modelQuota.reset_time
      }
    }
  }

  // A matching zero-use observation still represents known quota; return null only when none of
  // the requested model entries exists.
  if (maxUtilization === 0 && earliestReset === null) {
    const hasAnyData = modelNames.some((m) => quota[m])
    if (!hasAnyData) return null
  }

  return {
    utilization: maxUtilization,
    resetTime: earliestReset,
  }
}

/**
 * Antigravity family summaries group Pro, Flash, image, and Claude model identifiers into
 * separate utilization and earliest-reset bars for the table.
 */
const antigravity3ProUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-3-pro-low', 'gemini-3-pro-high', 'gemini-3-pro-preview']),
)

// Gemini 3 Flash from API
const antigravity3FlashUsageFromAPI = computed(() => getAntigravityUsageFromAPI(['gemini-3-flash']))

// Gemini Image from API
const antigravity3ImageUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI([
    'gemini-2.5-flash-image',
    'gemini-3.1-flash-image',
    'gemini-3-pro-image',
  ]),
)

// Claude from API (all Claude model variants)
const antigravityClaudeUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI([
    'claude-fable-5-1',
    'claude-fable-5',
    'claude-sonnet-4-5',
    'claude-opus-4-5-thinking',
    'claude-sonnet-4-6',
    'claude-opus-4-6',
    'claude-opus-4-6-thinking',
    'claude-opus-4-7',
    'claude-opus-4-8',
  ]),
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
  // Credit badges show whole amounts to fit the compact row.
  return total.toFixed(0)
})

/**
 * Antigravity tier presentation prefers a string paidTier identifier, then currentTier, keeping
 * missing entitlement metadata distinct from a recognized free tier.
 */
const antigravityTier = computed(() => {
  const extra = props.provider.extra as Record<string, unknown> | undefined
  if (!extra) return null

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return null

  // Prefer the paid entitlement identifier when present so a concurrent currentTier marker does
  // not hide the paid subscription label.
  const paidTier = loadCodeAssist.paidTier as Record<string, unknown> | undefined
  if (paidTier && typeof paidTier.id === 'string') {
    return paidTier.id
  }

  const currentTier = loadCodeAssist.currentTier as Record<string, unknown> | undefined
  if (currentTier && typeof currentTier.id === 'string') {
    return currentTier.id
  }

  return null
})

/**
 * Gemini tier and OAuth markers identify the saved channel, including the legacy project_id
 * rule used when an explicit oauth_type is absent.
 */
const geminiTier = computed(() => {
  if (props.provider.platform !== 'gemini') return null
  const creds = props.provider.credentials as GeminiCredentials | undefined
  return creds?.tier_id || null
})

const geminiOAuthType = computed(() => {
  if (props.provider.platform !== 'gemini') return null
  const creds = props.provider.credentials as GeminiCredentials | undefined
  return (creds?.oauth_type || '').trim() || null
})

const isGeminiCodeAssist = computed(() => {
  if (props.provider.platform !== 'gemini') return false
  const creds = props.provider.credentials as GeminiCredentials | undefined
  return creds?.oauth_type === 'code_assist' || (!creds?.oauth_type && !!creds?.project_id)
})

/**
 * Gemini channel and level labels normalize saved current or legacy credential markers into
 * concise channel-specific subscription descriptions for the table.
 */
const geminiChannelShort = computed((): 'ai studio' | 'gcp' | 'google one' | 'client' | null => {
  if (props.provider.platform !== 'gemini') return null

  // API Key providers are AI Studio.
  if (props.provider.type === 'apikey') return 'ai studio'

  if (geminiOAuthType.value === 'google_one') return 'google one'
  if (isGeminiCodeAssist.value) return 'gcp'
  if (geminiOAuthType.value === 'ai_studio') return 'client'

  // Credentials with an unrecognized legacy OAuth marker use the AI Studio channel label until
  // a recognized channel is supplied.
  return 'ai studio'
})

const geminiUserLevel = computed((): string | null => {
  if (props.provider.platform !== 'gemini') return null

  const tier = (geminiTier.value || '').toString().trim()
  const tierLower = tier.toLowerCase()
  const tierUpper = tier.toUpperCase()

  // Google One: free / pro / ultra
  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_one_free') return 'free'
    if (tierLower === 'google_ai_pro') return 'pro'
    if (tierLower === 'google_ai_ultra') return 'ultra'

    // Interpret older tier spellings within this channel so saved credentials retain their
    // subscription label until refreshed metadata replaces them.
    if (tierUpper === 'AI_PREMIUM' || tierUpper === 'GOOGLE_ONE_STANDARD') return 'pro'
    if (tierUpper === 'GOOGLE_ONE_UNLIMITED') return 'ultra'
    if (
      tierUpper === 'FREE' ||
      tierUpper === 'GOOGLE_ONE_BASIC' ||
      tierUpper === 'GOOGLE_ONE_UNKNOWN' ||
      tierUpper === ''
    )
      return 'free'

    return null
  }

  // GCP Code Assist: standard / enterprise
  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise') return 'enterprise'
    if (tierLower === 'gcp_standard') return 'standard'

    // Map legacy credential markers within the selected channel so existing records continue to
    // show their saved subscription level.
    if (tierUpper.includes('ULTRA') || tierUpper.includes('ENTERPRISE')) return 'enterprise'
    return 'standard'
  }

  // AI Studio (API Key) and Client OAuth: free / paid
  if (props.provider.type === 'apikey' || geminiOAuthType.value === 'ai_studio') {
    if (tierLower === 'aistudio_paid') return 'paid'
    if (tierLower === 'aistudio_free') return 'free'

    // Map legacy credential markers within the selected channel so existing records continue to
    // show their saved subscription level.
    if (tierUpper.includes('PAID') || tierUpper.includes('PAYG') || tierUpper.includes('PAY'))
      return 'paid'
    if (tierUpper.includes('FREE')) return 'free'
    if (props.provider.type === 'apikey') return 'free'
    return null
  }

  return null
})

/**
 * Gemini label and palette combine normalized channel and level; quota-policy text and links
 * follow the same saved channel interpretation.
 */
const geminiAuthTypeLabel = computed(() => {
  if (props.provider.platform !== 'gemini') return null
  if (!geminiChannelShort.value) return null
  return geminiUserLevel.value
    ? `${geminiChannelShort.value} ${geminiUserLevel.value}`
    : geminiChannelShort.value
})

const geminiTierClass = computed(() => {
  // Use channel+level to choose a stable color without depending on raw tier_id variants.
  const channel = geminiChannelShort.value
  const level = geminiUserLevel.value

  if (channel === 'client' || channel === 'ai studio') {
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  if (channel === 'google one') {
    if (level === 'ultra')
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    if (level === 'pro') return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200'
  }

  if (channel === 'gcp') {
    if (level === 'enterprise')
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  return ''
})

/**
 * Gemini quota-policy labels describe the selected saved channel and level, while the
 * documentation link follows official Code Assist or AI Studio ownership.
 */
const geminiQuotaPolicyChannel = computed(() => {
  if (geminiOAuthType.value === 'google_one') {
    return t('admin.providers.gemini.quotaPolicy.rows.googleOne.channel')
  }
  if (isGeminiCodeAssist.value) {
    return t('admin.providers.gemini.quotaPolicy.rows.gcp.channel')
  }
  return t('admin.providers.gemini.quotaPolicy.rows.aiStudio.channel')
})

const geminiQuotaPolicyLimits = computed(() => {
  const tierLower = (geminiTier.value || '').toString().trim().toLowerCase()

  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_ai_ultra' || geminiUserLevel.value === 'ultra') {
      return t('admin.providers.gemini.quotaPolicy.rows.googleOne.limitsUltra')
    }
    if (tierLower === 'google_ai_pro' || geminiUserLevel.value === 'pro') {
      return t('admin.providers.gemini.quotaPolicy.rows.googleOne.limitsPro')
    }
    return t('admin.providers.gemini.quotaPolicy.rows.googleOne.limitsFree')
  }

  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise' || geminiUserLevel.value === 'enterprise') {
      return t('admin.providers.gemini.quotaPolicy.rows.gcp.limitsEnterprise')
    }
    return t('admin.providers.gemini.quotaPolicy.rows.gcp.limitsStandard')
  }

  // AI Studio (API Key / custom OAuth)
  if (tierLower === 'aistudio_paid' || geminiUserLevel.value === 'paid') {
    return t('admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsPaid')
  }
  return t('admin.providers.gemini.quotaPolicy.rows.aiStudio.limitsFree')
})

const geminiQuotaPolicyDocsUrl = computed(() => {
  if (geminiOAuthType.value === 'google_one' || isGeminiCodeAssist.value) {
    return 'https://developers.google.com/gemini-code-assist/resources/quotas'
  }
  return 'https://ai.google.dev/pricing'
})

/**
 * Shared Gemini pool observations or Google One and Code Assist channels select the single
 * daily bar; other records expose available Pro and Flash daily bars.
 */
const geminiUsesSharedDaily = computed(() => {
  if (props.provider.platform !== 'gemini') return false
  // Google One and Code Assist select the shared pool presentation even when only minute
  // metadata is currently available from the response.
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    geminiOAuthType.value === 'google_one' ||
    isGeminiCodeAssist.value
  )
})

/**
 * geminiUsageBars uses returned daily observations for either the shared pool or model
 * families, carrying each window reset and optional local statistics into its bar.
 */
const geminiUsageBars = computed(() => {
  if (props.provider.platform !== 'gemini') return []
  if (!usageInfo.value) return []

  const bars: Array<{
    key: string // stable usage bar identifier
    label: string // compact usage window label
    utilization: number // observed utilization percentage
    resetsAt: string | null // quota reset timestamp or null
    windowStats?: WindowStats | null // optional local window observations
    color: 'indigo' | 'emerald' // usage window label palette
  }> = []

  if (geminiUsesSharedDaily.value) {
    const sharedDaily = usageInfo.value.gemini_shared_daily
    if (sharedDaily) {
      bars.push({
        key: 'shared_daily',
        label: '1d',
        utilization: sharedDaily.utilization,
        resetsAt: sharedDaily.resets_at,
        windowStats: sharedDaily.window_stats,
        color: 'indigo',
      })
    }
    return bars
  }

  const pro = usageInfo.value.gemini_pro_daily
  if (pro) {
    bars.push({
      key: 'pro_daily',
      label: 'pro',
      utilization: pro.utilization,
      resetsAt: pro.resets_at,
      windowStats: pro.window_stats,
      color: 'indigo',
    })
  }

  const flash = usageInfo.value.gemini_flash_daily
  if (flash) {
    bars.push({
      key: 'flash_daily',
      label: 'flash',
      utilization: flash.utilization,
      resetsAt: flash.resets_at,
      windowStats: flash.window_stats,
      color: 'emerald',
    })
  }

  return bars
})

/**
 * GrokQuotaBarInfo pairs billing utilization with its reset and optional local usage
 * observations.
 */
interface GrokQuotaBarInfo {
  utilization: number // observed utilization percentage
  resetsAt: string | null // quota reset timestamp or null
  windowStats?: WindowStats | null // optional local window observations
}

/**
 * QoderQuotaProgressLike identifies the shared shape of Qoder quota pool observations.
 */
type QoderQuotaProgressLike = NonNullable<
  NonNullable<ProviderUsageInfo['qoder_quota']>['user_quota']
>

/**
 * qoderQuotaPoolCapacity derives one pool capacity from its available observations. A positive
 * total has precedence, followed by a positive cap; otherwise used and remaining amounts are
 * added with missing components treated as zero.
 *
 * An explicit zero total can therefore fall through to the derived amount, and an entirely
 * absent pool amount produces zero as well. Callers pair this numeric result with
 * qoderQuotaPoolHasCapacity so the display can distinguish a measured zero from unknown
 * capacity.
 */
const qoderQuotaPoolCapacity = (pool: QoderQuotaProgressLike) => {
  if (pool.total && pool.total > 0) return pool.total
  if (pool.cap && pool.cap > 0) return pool.cap
  return (pool.used ?? 0) + (pool.remaining ?? 0)
}

/**
 * qoderQuotaPoolHasCapacity reports whether any capacity observation is present on a Qoder
 * pool. Explicit zero counts as known data, allowing the label to distinguish zero capacity
 * from missing observations.
 */
const qoderQuotaPoolHasCapacity = (pool: QoderQuotaProgressLike) => {
  return pool.total != null || pool.cap != null || pool.used != null || pool.remaining != null
}

/**
 * Qoder summaries combine user, add-on, and organization pools, keeping a separate
 * known-capacity flag so a present zero differs from missing capacity.
 */
const qoderQuotaPools = computed<QoderQuotaProgressLike[]>(() => {
  const quota = usageInfo.value?.qoder_quota
  return [quota?.user_quota, quota?.add_on_quota, quota?.org_resource_package].filter(
    (pool): pool is QoderQuotaProgressLike => !!pool,
  )
})

/**
 * qoderQuotaAggregate sums available pool observations, prefers positive derived total for
 * utilization, and otherwise uses the supplied overall or first-pool percentage.
 */
const qoderQuotaAggregate = computed(() => {
  const pools = qoderQuotaPools.value
  if (pools.length === 0) return null

  const used = pools.reduce((sum, pool) => sum + (pool.used ?? 0), 0)
  const remaining = pools.reduce((sum, pool) => sum + (pool.remaining ?? 0), 0)
  const total = pools.reduce((sum, pool) => sum + qoderQuotaPoolCapacity(pool), 0)
  const totalKnown = pools.some(qoderQuotaPoolHasCapacity)
  const unit = pools.find((pool) => pool.unit)?.unit || 'credits'
  const utilization =
    total > 0
      ? (used / total) * 100
      : (usageInfo.value?.qoder_quota?.total_usage_percentage ?? pools[0]?.percentage ?? 0)

  return { used, remaining, total, totalKnown, unit, utilization }
})

/**
 * Qoder bar geometry clamps utilization while its amount label distinguishes an observed total
 * from unknown capacity and retains the reported pool unit.
 */
const qoderQuotaUsageBar = computed(() => {
  const quota = usageInfo.value?.qoder_quota
  const utilization = qoderQuotaAggregate.value?.utilization ?? quota?.total_usage_percentage ?? 0
  return {
    utilization: Math.max(0, Math.min(100, utilization)),
    resetsAt: quota?.expires_at || null,
  }
})
const qoderQuotaCreditsLabel = computed(() => {
  const quota = qoderQuotaAggregate.value
  if (!quota) return '-'
  const used = formatCompactNumber(quota.used)
  const total = quota.totalKnown ? formatCompactNumber(quota.total) : 'unknown'
  const unit = quota.unit
  return `${used}/${total} ${unit}`
})
/**
 * Grok billing observations and local seven-day or monthly usage statistics feed separate
 * upstream utilization bars and local accounting chips.
 */
const grokBilling = computed(() => usageInfo.value?.grok_billing || null)
const grokLocalUsage7d = computed(
  () => usageInfo.value?.grok_local_usage_7d || usageInfo.value?.seven_day?.window_stats || null,
)
const grokLocalUsageMonthly = computed(
  () =>
    usageInfo.value?.grok_local_usage_monthly || usageInfo.value?.thirty_day?.window_stats || null,
)
/**
 * The Grok weekly bar requires a weekly period and measured usage percentage; the monthly bar
 * prefers used_percent before deriving a ratio from cents.
 */
const grokWeeklyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (billing?.period_type?.toLowerCase() !== 'weekly' || billing.usage_percent == null) {
    return null
  }
  return {
    // Keep the weekly bar within its available percentage range.
    utilization: Math.min(100, Math.max(0, billing.usage_percent)),
    resetsAt: billing.period_end || null,
    windowStats: grokLocalUsage7d.value,
  }
})

/**
 * grokMonthlyBillingBar suppresses duplicate weekly-only data and pairs a usable monthly
 * percentage with the billing or general period-end timestamp.
 */
const grokMonthlyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (!billing) return null
  let utilization: number | null = null
  if (billing.used_percent != null && Number.isFinite(billing.used_percent)) {
    utilization = billing.used_percent
  } else if (
    billing.monthly_limit_cents != null &&
    billing.monthly_limit_cents > 0 &&
    billing.used_cents != null
  ) {
    utilization = (billing.used_cents / billing.monthly_limit_cents) * 100
  }
  if (utilization == null) return null
  // Weekly observations without a monthly limit already have a weekly bar, so they cannot
  // establish a separate monthly allowance display.
  if (billing.period_type?.toLowerCase() === 'weekly' && billing.monthly_limit_cents == null) {
    return null
  }
  return {
    // Keep the monthly bar within its available percentage range.
    utilization: Math.min(100, Math.max(0, utilization)),
    resetsAt: billing.billing_period_end || billing.period_end || null,
    windowStats: grokLocalUsageMonthly.value,
  }
})
/**
 * formatGrokMoney formats Grok money values with magnitude-dependent decimal precision and
 * compact suffixes. Missing or NaN values display zero; smaller values keep more fractional
 * digits in the concise billing line.
 */
const formatGrokMoney = (value?: number | null) => {
  if (value == null || Number.isNaN(value)) return '0'
  // Larger money amounts use fewer decimals in the compact billing line.
  if (value >= 1000) return formatCompactNumber(value)
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}

/**
 * Grok money lines require positive finite prepaid or limit values, prefer major-unit amounts,
 * and convert cent observations when major-unit fields are absent.
 */
const grokPrepaidMoneyLine = computed(() => {
  const billing = grokBilling.value
  if (!billing) return null
  const prepaid = billing.prepaid_balance
  const showPrepaid = prepaid != null && Number.isFinite(prepaid) && prepaid > 0
  const limitRaw =
    billing.monthly_limit != null
      ? billing.monthly_limit
      : billing.monthly_limit_cents != null
        ? // Use major currency units when the response supplies cents.
          billing.monthly_limit_cents / 100
        : null
  const showUsedLimit = limitRaw != null && Number.isFinite(limitRaw) && limitRaw > 0
  if (!showPrepaid && !showUsedLimit) return null
  const used =
    billing.monthly_used != null
      ? billing.monthly_used
      : billing.used_cents != null
        ? // Match the limit display unit before formatting used money.
          billing.used_cents / 100
        : 0
  return {
    showPrepaid,
    showUsedLimit,
    prepaid: showPrepaid ? formatGrokMoney(prepaid) : null,
    used: showUsedLimit ? formatGrokMoney(used) : null,
    limit: showUsedLimit ? formatGrokMoney(limitRaw) : null,
  }
})
/**
 * grokPlanLabelIsFree recognizes labels containing the free or basic substrings. Callers
 * supply lowercase labels because matching is case-sensitive; the helper accepts surrounding
 * whitespace without trimming it.
 */
const grokPlanLabelIsFree = (value: string) => value.includes('free') || value.includes('basic')
/**
 * grokPlanLabelIsPaid recognizes nonempty normalized labels that are neither free nor unknown.
 * Callers trim and lowercase labels before invoking it, so matching follows the shared plan
 * classification rules.
 */
const grokPlanLabelIsPaid = (value: string) => {
  return value !== '' && !grokPlanLabelIsFree(value) && !value.includes('unknown')
}
/**
 * Grok free-plan classification gives a recognized subscription tier precedence, then billing
 * observations and plan labels, before falling back to the presence of billing data.
 */
const grokIsFree = computed(() => {
  if (props.provider.platform !== 'grok' || props.provider.type !== 'oauth') return false
  const billing = grokBilling.value
  const plan = (billing?.plan || '').trim().toLowerCase()
  const tier = (usageInfo.value?.subscription_tier || '').trim().toLowerCase()
  const entitlement = (usageInfo.value?.grok_entitlement_status || '').toLowerCase()
  if (grokPlanLabelIsFree(tier)) return true
  if (grokPlanLabelIsPaid(tier)) return false
  if (
    billing?.usage_percent != null ||
    billing?.used_percent != null ||
    (billing?.monthly_limit_cents != null && billing.monthly_limit_cents > 0)
  )
    return false
  if (grokPlanLabelIsPaid(plan)) return false
  if (grokPlanLabelIsFree(plan) || grokPlanLabelIsFree(entitlement)) return true
  return billing != null
})
/**
 * Free Grok usage uses the local twenty-four-hour token observation and a positive configured
 * limit; unknown quota labels remain separate from measured bar values.
 */
const grokFreeQuotaUsage = computed(() => usageInfo.value?.grok_local_usage_24h || null)
const grokFreeTokenBar = computed(() => {
  if (!grokIsFree.value || !grokFreeQuotaUsage.value) return null
  const limit = usageInfo.value?.grok_free_token_limit
  if (typeof limit !== 'number' || limit <= 0) return null
  const used = Math.max(0, grokFreeQuotaUsage.value.tokens || 0)
  // Convert observed tokens to bounded allowance utilization for bar geometry.
  return { utilization: Math.min(100, (used / limit) * 100), limit }
})
/**
 * Grok quota availability treats a usable free-token bar or paid billing observation as known,
 * then consults the saved quota snapshot state.
 */
const grokQuotaUnknown = computed(() => {
  if (props.provider.platform !== 'grok') return false
  if (grokIsFree.value) {
    return !grokFreeTokenBar.value
  }
  if (grokWeeklyBillingBar.value || grokMonthlyBillingBar.value || grokPrepaidMoneyLine.value) {
    return false
  }
  return usageInfo.value?.grok_quota_snapshot_state !== 'observed'
})
const grokQuotaUnknownLabel = computed(() => {
  return usageInfo.value?.grok_quota_snapshot_state === 'no_headers'
    ? t('admin.providers.usageWindow.grokNoHeaders')
    : t('admin.providers.usageWindow.grokUnknown')
})
/**
 * grokRetryAfterLabel displays positive retry delays in seconds below one minute and rounds
 * longer delays upward to whole minutes.
 */
const grokRetryAfterLabel = computed(() => {
  const seconds = usageInfo.value?.grok_retry_after_seconds
  if (seconds == null || seconds <= 0) return null
  if (seconds < 60) return `${seconds}s`
  // Round retry delays upward so a partial minute remains visible.
  const minutes = Math.ceil(seconds / 60)
  return `${minutes}m`
})

/**
 * Antigravity recognized tier labels and palettes distinguish free, Pro, and Ultra entitlement
 * markers; unrecognized markers omit the tier badge.
 */
const antigravityTierLabel = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return t('admin.providers.tier.free')
    case 'g1-pro-tier':
      return t('admin.providers.tier.pro')
    case 'g1-ultra-tier':
      return t('admin.providers.tier.ultra')
    default:
      return null
  }
})

const antigravityTierClass = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-200'
    case 'g1-pro-tier':
      return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    case 'g1-ultra-tier':
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    default:
      return ''
  }
})

/**
 * hasIneligibleTiers detects a nonempty ineligibleTiers array in saved Code Assist metadata so
 * the template can explain unavailable entitlement.
 */
const hasIneligibleTiers = computed(() => {
  const extra = props.provider.extra as Record<string, unknown> | undefined
  if (!extra) return false

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return false

  const ineligibleTiers = loadCodeAssist.ineligibleTiers as unknown[] | undefined
  return Array.isArray(ineligibleTiers) && ineligibleTiers.length > 0
})

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
 * copyValidationURL copies a present verification URL and briefly shows success feedback.
 * Clipboard failures are ignored, leaving the visible link available for direct opening or
 * manual copying by the administrator.
 */
const copyValidationURL = async () => {
  if (!validationURL.value) return
  try {
    await navigator.clipboard.writeText(validationURL.value)
    linkCopied.value = true
    // Reset copy feedback after its display interval; the callback changes presentation state
    // but does not own the verification URL resource.
    setTimeout(() => {
      linkCopied.value = false
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
 * eligible providers. Parent-managed calls forward the force intent and return after
 * delegation. Local calls reuse a fresh cache entry unless bypassCache is requested, then
 * publish a successful queued response and its cache timestamp.
 *
 * Queued completion checks the unmounted flag before writing display or cache state. That check
 * protects component teardown but does not distinguish provider changes or competing requests
 * within the same mounted instance. Failures set a generic display error and retain previous
 * usage, while finally clears loading when the component is still mounted.
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

  // Reuse a fresh instance-local observation before entering the shared request queue so
  // rerenders need no additional upstream request.
  if (!options?.bypassCache) {
    const cached = _usageCache.get(props.provider.id)
    if (cached && Date.now() - cached.ts < USAGE_CACHE_TTL) {
      usageInfo.value = cached.data
      loading.value = false
      return
    }
  }

  loading.value = true
  error.value = null

  try {
    /**
     * fetchFn requests provider usage using the optional active or passive source selector.
     */
    const fetchFn = () =>
      options?.source
        ? adminAPI.providers.getUsage(props.provider.id, options.source)
        : adminAPI.providers.getUsage(props.provider.id)
    // The shared queue bounds concurrent usage work; the unmount check below suppresses
    // publication after this cell loses ownership of its display.
    const result = await enqueueUsageRequest(props.provider, fetchFn)
    if (!unmounted.value) {
      usageInfo.value = result
      _usageCache.set(props.provider.id, { data: result, ts: Date.now() })
    }
  } catch (e: any) {
    if (!unmounted.value) {
      error.value = t('common.error')
      console.error('Failed to load usage:', e)
    }
  } finally {
    if (!unmounted.value) loading.value = false
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
 * loadActiveUsage explicitly queries fresh active usage and publishes the returned observation
 * to local state and cache. Failures are logged while existing data remains visible; this path
 * has no unmount or request-generation guard.
 */
const loadActiveUsage = async () => {
  activeQueryLoading.value = true
  error.value = null
  try {
    const result = await adminAPI.providers.getUsage(props.provider.id, 'active', true)
    usageInfo.value = result
    _usageCache.set(props.provider.id, { data: result, ts: Date.now() })
  } catch (e: any) {
    console.error('Failed to load active usage:', e)
  } finally {
    activeQueryLoading.value = false
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
 * QuotaBarInfo describes local spending utilization and its optional reset timestamp.
 */
interface QuotaBarInfo {
  utilization: number // observed utilization percentage
  resetsAt: string | null // quota reset timestamp or null
}

/**
 * makeQuotaBar builds utilization and an optional reset timestamp for one local spending limit.
 * Positive limits divide effective usage into a percentage; nonpositive limits yield zero. A
 * missing startKey leaves the quota without a periodic deadline.
 *
 * Fixed periods use the server's reset_at metadata. Rolling periods add one, seven, or thirty
 * elapsed days to the saved start, matching daily, weekly, or monthly display windows. Missing
 * starts keep the timestamp null until accounting initializes a period. The calculation uses
 * supplied effective usage and does not reset consumption when the derived deadline passes.
 */
const makeQuotaBar = (used: number, limit: number, startKey?: string): QuotaBarInfo => {
  const utilization = limit > 0 ? (used / limit) * 100 : 0
  let resetsAt: string | null = null
  if (startKey) {
    const extra = props.provider.extra as Record<string, unknown> | undefined
    const prefix = startKey.replace(/_start$/, '')
    const mode = (extra?.[`${prefix}_reset_mode`] as string) || 'rolling'

    if (mode === 'fixed') {
      // Fixed schedules publish their next reset from the server, keeping this display
      // consistent with calendar-based accounting decisions and configured timezone.
      const resetAtKey = `${prefix}_reset_at`
      resetsAt = (extra?.[resetAtKey] as string) || null
    } else {
      // Rolling periods begin at the saved accounting start, so an uninitialized period keeps
      // its reset time absent until usage starts.
      const startStr = extra?.[startKey] as string | undefined
      if (startStr) {
        const startDate = new Date(startStr)
        // Convert rolling quota days to elapsed milliseconds for the deadline.
        const days = prefix === 'quota_daily' ? 1 : prefix === 'quota_weekly' ? 7 : 30
        const periodMs = days * 24 * 60 * 60 * 1000
        resetsAt = new Date(startDate.getTime() + periodMs).toISOString()
      }
    }
  }
  return { utilization, resetsAt }
}

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
  // Keep viewport mode synchronized with the shared breakpoint; teardown removes the matching
  // modern or legacy media-query listener for this cell.
  if (canUseMatchMedia) {
    desktopViewportMediaQuery = window.matchMedia(TABLE_DESKTOP_MEDIA_QUERY)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }

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
  { immediate: true, deep: true },
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
    if (!managed || !shouldFetchUsage.value) return
    syncManagedUsageState()
    requestParentBatchUsage()
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
  // Remove the listener registered by this row before clearing its handles so later viewport
  // changes cannot update the unmounted cell.
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
  }
  desktopViewportListener = null
  desktopViewportMediaQuery = null
})
</script>
