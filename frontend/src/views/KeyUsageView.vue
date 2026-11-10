<template>
  <div class="ba-theme-shell relative flex min-h-screen flex-col">
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

    <!-- Header (same pattern as HomeView) -->
    <header class="relative z-20 px-6 py-4">
      <nav class="mx-auto flex max-w-6xl items-center justify-between">
        <router-link to="/home" class="flex items-center gap-3">
          <div class="h-10 w-10 overflow-hidden rounded-surface shadow-md">
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <span class="text-lg font-semibold tracking-tight text-gray-900 dark:text-white">{{
            siteName
          }}</span>
        </router-link>
        <div class="flex items-center gap-3">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="rounded-control p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="sm" />
          </a>
          <button
            @click="toggleTheme"
            class="rounded-control p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main
      v-content-reveal="motionRoute?.path"
      class="relative z-10 flex-1 w-full max-w-5xl mx-auto px-6 py-12"
    >
      <!-- Hero -->
      <div class="text-center mb-12">
        <h1
          class="text-3xl sm:text-4xl font-bold tracking-tight mb-3 text-gray-900 dark:text-white"
        >
          {{ t('keyUsage.title') }}
        </h1>
        <p class="text-gray-500 dark:text-dark-400 text-base max-w-md mx-auto">
          {{ t('keyUsage.subtitle') }}
        </p>
      </div>

      <!-- Input Section -->
      <div class="max-w-xl mx-auto mb-14">
        <div class="flex gap-2">
          <div class="flex-1 relative">
            <div class="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-500">
              <Icon name="lock" size="md" class="w-5 h-5" />
            </div>
            <input
              v-model="apiKey"
              :type="keyVisible ? 'text' : 'password'"
              :placeholder="t('keyUsage.placeholder')"
              class="input pl-12 pr-12"
              @keydown.enter="queryKey"
            />
            <button
              @click="keyVisible = !keyVisible"
              class="absolute right-4 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-700 dark:text-dark-500 dark:hover:text-white transition-colors"
            >
              <Icon name="eyeOff" size="sm" v-if="!keyVisible" />
              <Icon name="eye" size="sm" v-else />
            </button>
          </div>
          <button
            @click="queryKey"
            :disabled="isQuerying"
            class="btn btn-primary whitespace-nowrap px-7"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="isQuerying"
              class="animate-spin"
            />
            <Icon name="search" size="sm" v-else />
            {{ isQuerying ? t('keyUsage.querying') : t('keyUsage.query') }}
          </button>
        </div>
        <p class="text-xs text-gray-400 dark:text-dark-500 mt-3 text-center">
          {{ t('keyUsage.privacyNote') }}
        </p>

        <!-- Date Range Picker -->
        <div v-if="showDatePicker" class="mt-4">
          <div class="flex flex-wrap items-center gap-2 justify-center">
            <span class="text-xs text-gray-500 dark:text-dark-400">{{
              t('keyUsage.dateRange')
            }}</span>
            <button
              v-for="range in dateRanges"
              :key="range.key"
              @click="setDateRange(range.key)"
              class="text-xs px-3 py-1.5 rounded-control border transition"
              :class="
                currentRange === range.key
                  ? 'border-primary-500/60 bg-primary-50 text-primary-700 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                  : 'border-gray-200 bg-white text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200 hover:border-black/20 dark:hover:border-dark-600'
              "
            >
              {{ range.label }}
            </button>
            <div v-if="currentRange === 'custom'" class="flex items-center gap-2 ml-1">
              <input v-model="customStartDate" type="date" class="input w-auto px-2 text-xs" />
              <span class="text-xs text-gray-400">-</span>
              <input v-model="customEndDate" type="date" class="input w-auto px-2 text-xs" />
              <button @click="queryKey" class="btn btn-primary btn-sm h-9">
                {{ t('keyUsage.apply') }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Results Container -->
      <div v-if="showResults">
        <!-- Loading Skeleton -->
        <div v-if="showLoading" class="space-y-4">
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div
              class="rounded-surface border border-gray-200 bg-white p-8 dark:border-dark-700 dark:bg-dark-900"
            >
              <div class="skeleton h-5 w-24 mb-6"></div>
              <div class="flex justify-center">
                <div class="skeleton w-44 h-44 rounded-full"></div>
              </div>
            </div>
            <div
              class="rounded-surface border border-gray-200 bg-white p-8 dark:border-dark-700 dark:bg-dark-900"
            >
              <div class="skeleton h-5 w-24 mb-6"></div>
              <div class="flex justify-center">
                <div class="skeleton w-44 h-44 rounded-full"></div>
              </div>
            </div>
          </div>
          <div
            class="rounded-surface border border-gray-200 bg-white p-8 dark:border-dark-700 dark:bg-dark-900"
          >
            <div class="skeleton h-5 w-32 mb-6"></div>
            <div class="space-y-4">
              <div class="skeleton h-4 w-full"></div>
              <div class="skeleton h-4 w-3/4"></div>
              <div class="skeleton h-4 w-5/6"></div>
              <div class="skeleton h-4 w-2/3"></div>
            </div>
          </div>
        </div>

        <!-- Result Content -->
        <div v-else-if="resultData" class="space-y-4">
          <!-- Status Badge -->
          <div v-if="statusInfo" class="fade-up flex items-center justify-center mb-2">
            <div
              class="inline-flex items-center gap-2 px-5 py-2.5 rounded-full border border-gray-200 bg-white/90 shadow-sm backdrop-blur-sm dark:border-dark-700 dark:bg-dark-900/90"
            >
              <span
                class="w-2.5 h-2.5 rounded-full pulse-dot"
                :class="statusInfo.isActive ? 'bg-emerald-500' : 'bg-rose-500'"
              ></span>
              <span class="text-sm font-medium text-gray-900 dark:text-white">{{
                statusInfo.label
              }}</span>
              <span class="text-xs text-gray-400 dark:text-dark-500">|</span>
              <span class="text-xs text-gray-500 dark:text-dark-400">{{
                statusInfo.statusText
              }}</span>
            </div>
          </div>

          <!-- Ring Cards Grid -->
          <div v-if="ringItems.length > 0" :class="ringGridClass">
            <div
              v-for="(ring, i) in ringItems"
              :key="i"
              class="fade-up rounded-surface border border-gray-200 bg-white/90 p-8 backdrop-blur-sm transition duration-layout hover:shadow-lg dark:border-dark-700 dark:bg-dark-900/90"
              :class="`fade-up-delay-${Math.min(i + 1, 4)}`"
            >
              <div class="flex items-center justify-between mb-6">
                <h3
                  class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400"
                >
                  {{ ring.title }}
                </h3>
                <!-- Clock icon -->
                <Icon
                  name="clock"
                  size="md"
                  v-if="ring.iconType === 'clock'"
                  class="w-5 h-5 text-gray-400 dark:text-dark-500"
                />
                <!-- Calendar icon -->
                <Icon
                  name="calendar"
                  size="md"
                  v-else-if="ring.iconType === 'calendar'"
                  class="w-5 h-5 text-gray-400 dark:text-dark-500"
                />
                <BalanceIcon v-else size="md" class="text-gray-400 dark:text-dark-500" />
              </div>
              <div class="flex justify-center">
                <div class="relative">
                  <svg class="w-44 h-44" viewBox="0 0 160 160">
                    <circle
                      cx="80"
                      cy="80"
                      r="68"
                      fill="none"
                      :stroke="ringTrackColor"
                      stroke-width="10"
                    />
                    <circle
                      class="progress-ring"
                      cx="80"
                      cy="80"
                      r="68"
                      fill="none"
                      :stroke="`url(#ring-grad-${i})`"
                      stroke-width="10"
                      stroke-linecap="round"
                      :stroke-dasharray="CIRCUMFERENCE.toFixed(2)"
                      :stroke-dashoffset="getRingOffset(ring)"
                      :style="{ transitionDuration: `${RING_ANIMATION_MS}ms` }"
                    />
                    <defs>
                      <linearGradient :id="`ring-grad-${i}`" x1="0%" y1="0%" x2="100%" y2="100%">
                        <stop offset="0%" :stop-color="RING_GRADIENTS[i % 4].from" />
                        <stop offset="100%" :stop-color="RING_GRADIENTS[i % 4].to" />
                      </linearGradient>
                    </defs>
                  </svg>
                  <div class="absolute inset-0 flex flex-col items-center justify-center">
                    <template v-if="ring.isBalance">
                      <span
                        class="text-2xl font-bold tabular-nums"
                        :style="{ color: RING_GRADIENTS[i % 4].from }"
                      >
                        {{ ring.amount }}
                      </span>
                    </template>
                    <template v-else>
                      <span class="text-3xl font-bold tabular-nums text-gray-900 dark:text-white">
                        {{ displayPcts[i] ?? 0 }}%
                      </span>
                      <span class="text-xs text-gray-500 dark:text-dark-400 mt-0.5">{{
                        t('keyUsage.used')
                      }}</span>
                      <span
                        class="text-sm font-semibold mt-1 tabular-nums"
                        :style="{ color: RING_GRADIENTS[i % 4].from }"
                        >{{ ring.amount }}</span
                      >
                      <p
                        v-if="ring.resetAt && formatResetTime(ring.resetAt)"
                        class="text-xs text-gray-400 dark:text-gray-500 mt-0.5 tabular-nums"
                      >
                        ⟳ {{ formatResetTime(ring.resetAt) }}
                      </p>
                    </template>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Detail Card -->
          <div
            v-if="detailRows.length > 0"
            class="fade-up fade-up-delay-3 rounded-surface border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="px-8 py-5 border-b border-gray-200 dark:border-dark-700">
              <h3
                class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400"
              >
                {{ t('keyUsage.detailInfo') }}
              </h3>
            </div>
            <div class="divide-y divide-gray-100 dark:divide-dark-800">
              <div
                v-for="(row, i) in detailRows"
                :key="i"
                class="px-8 py-4 flex items-center justify-between"
              >
                <div class="flex items-center gap-3">
                  <div
                    class="w-8 h-8 rounded-control flex items-center justify-center"
                    :class="row.iconBg"
                  >
                    <BalanceIcon v-if="row.useBalanceIcon" size="sm" :class="row.iconColor" />
                    <svg
                      v-else
                      class="w-4 h-4"
                      :class="row.iconColor"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      v-html="row.iconSvg"
                    ></svg>
                  </div>
                  <span class="text-sm text-gray-700 dark:text-dark-200">{{ row.label }}</span>
                </div>
                <span
                  class="text-sm font-semibold tabular-nums"
                  :class="row.valueClass || 'text-gray-900 dark:text-white'"
                >
                  {{ row.value }}
                </span>
              </div>
            </div>
          </div>

          <!-- Usage Stats Card -->
          <div
            v-if="usageStatCells.length > 0"
            class="fade-up fade-up-delay-3 rounded-surface border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="px-8 py-5 border-b border-gray-200 dark:border-dark-700">
              <h3
                class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400"
              >
                {{ t('keyUsage.tokenStats') }}
              </h3>
            </div>
            <div class="grid grid-cols-2 md:grid-cols-4 gap-px bg-gray-100 dark:bg-dark-950">
              <div
                v-for="(cell, i) in usageStatCells"
                :key="i"
                class="bg-white px-6 py-4 dark:bg-dark-900"
              >
                <div class="text-xs text-gray-500 dark:text-dark-400 mb-1">{{ cell.label }}</div>
                <div class="text-sm font-semibold tabular-nums text-gray-900 dark:text-white">
                  {{ cell.value }}
                </div>
              </div>
            </div>
          </div>

          <!-- Daily usage history -->
          <div
            v-if="showDailyUsage"
            class="fade-up fade-up-delay-4 rounded-surface border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div
              class="flex flex-col gap-3 px-8 py-5 border-b border-gray-200 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between"
            >
              <h3
                class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400"
              >
                {{ t('keyUsage.dailyDetail') }}
              </h3>
              <div v-segmented class="segmented">
                <button
                  v-for="option in dailyUsageOptions"
                  :key="option.value"
                  type="button"
                  @click="setDailyUsageDays(option.value)"
                  class="segmented-item min-w-12 px-3 py-1.5 text-xs"
                  :class="{ 'segmented-item-active': dailyUsageDays === option.value }"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
            <div v-if="dailyUsageRows.length > 0" class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr
                    class="border-b border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950"
                  >
                    <th
                      class="px-4 py-3 text-left text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.date') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.requests') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.inputTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.outputTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.cacheReadTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.cacheWriteTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.cost') }}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="row in dailyUsageRows"
                    :key="row.date"
                    class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
                  >
                    <td
                      class="px-4 py-3 text-sm font-medium whitespace-nowrap text-gray-900 dark:text-white"
                    >
                      {{ row.date }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(row.requests) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(row.input_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(row.output_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(row.cache_read_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(row.cache_write_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right font-medium text-gray-900 dark:text-white"
                    >
                      {{ formatUsageBalance(row.actual_cost != null ? row.actual_cost : row.cost) }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div v-else class="px-8 py-8 text-center text-sm text-gray-500 dark:text-dark-400">
              {{ t('keyUsage.noDailyUsage') }}
            </div>
          </div>

          <!-- Model Stats Table -->
          <div
            v-if="modelStats.length > 0"
            class="fade-up fade-up-delay-4 rounded-surface border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="px-8 py-5 border-b border-gray-200 dark:border-dark-700">
              <h3
                class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400"
              >
                {{ t('keyUsage.modelStats') }}
              </h3>
            </div>
            <div class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr
                    class="border-b border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950"
                  >
                    <th
                      class="px-4 py-3 text-left text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.model') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.requests') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.inputTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.outputTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.cacheCreationTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.cacheReadTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.totalTokens') }}
                    </th>
                    <th
                      class="px-4 py-3 text-right text-xs font-semibold tracking-wider text-gray-500 dark:text-dark-400"
                    >
                      {{ t('keyUsage.cost') }}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="(m, i) in modelStats"
                    :key="i"
                    class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
                  >
                    <td
                      class="px-4 py-3 text-sm font-medium whitespace-nowrap text-gray-900 dark:text-white"
                    >
                      {{ m.model || '-' }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(m.requests) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(m.input_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(m.output_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(m.cache_creation_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(m.cache_read_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200"
                    >
                      {{ fmtNum(m.total_tokens) }}
                    </td>
                    <td
                      class="px-4 py-3 text-sm tabular-nums text-right font-medium text-gray-900 dark:text-white"
                    >
                      {{ formatUsageBalance(m.actual_cost != null ? m.actual_cost : m.cost) }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Footer (same pattern as HomeView) -->
    <footer class="relative z-10 border-t border-gray-200/50 px-6 py-8 dark:border-dark-800/50">
      <div
        class="mx-auto flex max-w-6xl flex-col items-center justify-center gap-4 text-center sm:flex-row sm:text-left"
      >
        <p class="text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-link"
            >{{ t('home.docs') }}</a
          >
          <a :href="githubUrl" target="_blank" rel="noopener noreferrer" class="text-sm text-link"
            >GitHub</a
          >
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { getLocale } from '@/i18n'
import { localizedErrorMessage } from '@/i18n/errors'
import { vContentReveal } from '@/directives/contentReveal'
import { vSegmented } from '@/directives/segmented'
import { useRoute as useMotionRoute } from 'vue-router'
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import Icon from '@/components/icons/Icon.vue'
import { useTheme } from '@/composables/useTheme'
import { buildGatewayUrl } from '@/api/client'
import { formatDateLocalInput } from '@/utils/format'
import { sanitizeUrl } from '@/utils/url'

// KeyUsageView.vue queries API-key usage and displays quota, billing availability, reset
// times, and usage summaries for a selected range. The key is sent as a bearer credential
// to the gateway usage endpoint. Relative minute ranges use exact timestamps, while
// calendar ranges carry local day strings and a browser timezone.
//
// Billing presentation distinguishes quota-limited keys, subscription limits, and wallet
// balances, including legacy responses whose subscription object supplies the billing
// source. Ring percentages and SVG strokes share an animation duration, while reset labels
// follow a component-owned minute timer. Responses requested under an outdated locale are
// retried before publication so errors and data use the active language.

// motionRoute supplies the current route path used by the page reveal directive to identify
// navigation changes.
const motionRoute = useMotionRoute()

// Translation, application settings, and balance formatting supply localized labels and
// configured monetary presentation for the public key lookup.
const { t, locale } = useI18n()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

// Site presentation derives sanitized branding and documentation links from cached public
// settings, with application defaults for missing values.
const siteName = computed(() => appStore.siteName || 'TokenRouter')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const docUrl = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''),
)
// githubUrl supplies the project repository destination displayed by the public usage page
// footer alongside documentation.
const githubUrl = 'https://github.com/TokenFlux/TokenRouter'

// Theme state controls the public page switch and reactive ring track colors for light and
// dark backgrounds.
const { isDark, toggleTheme } = useTheme()

// currentYear supplies the calendar year displayed beside the configured site name in the
// public footer.
const currentYear = computed(() => new Date().getFullYear())

// Key lookup state separates credential input visibility, request progress, result
// presence, and date-control availability for successful lookups.
const apiKey = ref('')
const keyVisible = ref(false)
const isQuerying = ref(false)
const showResults = ref(false)
const showLoading = ref(false)
const showDatePicker = ref(false)
const resultData = ref<any>(null)
// now supplies the current instant for reset countdowns, and resetTimer owns the interval
// that refreshes it each minute.
const now = ref(new Date())
let resetTimer: ReturnType<typeof setInterval> | null = null

// DateRangeKey names supported relative windows and the custom calendar range.
type DateRangeKey = '15m' | '30m' | 'today' | '7d' | '30d' | 'custom'
// Range state owns the selected preset, optional custom endpoints, and the independently
// requested daily-history length.
const currentRange = ref<DateRangeKey>('today')
const customStartDate = ref('')
const customEndDate = ref('')
const dailyUsageDays = ref<7 | 30 | 90>(30)

// dateRanges supplies translated choices for minute windows, calendar ranges, and custom
// dates in the key usage controls.
const dateRanges = computed(() => [
  { key: '15m' as const, label: t('keyUsage.dateRange15m') },
  { key: '30m' as const, label: t('keyUsage.dateRange30m') },
  { key: 'today' as const, label: t('keyUsage.dateRangeToday') },
  { key: '7d' as const, label: t('keyUsage.dateRange7d') },
  { key: '30d' as const, label: t('keyUsage.dateRange30d') },
  { key: 'custom' as const, label: t('keyUsage.dateRangeCustom') },
])

// dailyUsageOptions supplies seven, thirty, and ninety-day history lengths independently of
// the main query range selector.
const dailyUsageOptions = computed(() => [
  { value: 7 as const, label: t('keyUsage.dateRange7d') },
  { value: 30 as const, label: t('keyUsage.dateRange30d') },
  { value: 90 as const, label: t('keyUsage.dateRange90d') },
])

// setDateRange changes the active preset and immediately queries noncustom ranges. Custom
// selection waits for the user to supply both endpoints and submit through the apply
// control.
function setDateRange(key: DateRangeKey) {
  currentRange.value = key
  if (key !== 'custom') {
    queryKey()
  }
}

// getDateParams serializes the selected range, daily-history length, and browser timezone.
// Custom dates contribute bounds only when both endpoints exist; minute presets use exact
// UTC timestamps instead of day strings.
//
// Today and longer calendar presets use local date formatting, with the longer ranges
// subtracting fixed elapsed days before formatting. This keeps their wire representation
// distinct from precise minute windows. Every query also includes days for the history
// table and timezone for the server's calendar interpretation.
function getDateParams(): string {
  const now = new Date()
  // fmtDateTime serializes an instant as an ISO UTC timestamp.
  const fmtDateTime = (d: Date) => d.toISOString()
  const params = new URLSearchParams()

  if (currentRange.value === 'custom') {
    if (customStartDate.value && customEndDate.value) {
      params.set('start_date', customStartDate.value)
      params.set('end_date', customEndDate.value)
    }
  } else {
    const end = formatDateLocalInput(now)
    let exactEnd = end
    let start: string
    switch (currentRange.value) {
      case '15m':
        // Minute windows use exact instants instead of calendar day endpoints.
        start = fmtDateTime(new Date(now.getTime() - 15 * 60000))
        exactEnd = fmtDateTime(now)
        break
      case '30m':
        start = fmtDateTime(new Date(now.getTime() - 30 * 60000))
        exactEnd = fmtDateTime(now)
        break
      case 'today':
        start = end
        break
      case '7d':
        // Subtract elapsed days before formatting the local calendar endpoint.
        start = formatDateLocalInput(new Date(now.getTime() - 7 * 86400000))
        break
      case '30d':
        // The thirty-day preset also uses fixed elapsed-day subtraction.
        start = formatDateLocalInput(new Date(now.getTime() - 30 * 86400000))
        break
      default:
        start = formatDateLocalInput(new Date(now.getTime() - 30 * 86400000))
    }
    params.set('start_date', start)
    params.set('end_date', exactEnd)
  }
  params.set('days', String(dailyUsageDays.value))
  params.set('timezone', getBrowserTimezone())
  return params.toString()
}

// setDailyUsageDays changes the independent daily-history length when it differs from the
// current choice. It reruns a query only when results already exist and the key input is
// nonempty.
function setDailyUsageDays(days: 7 | 30 | 90) {
  if (dailyUsageDays.value === days) return
  dailyUsageDays.value = days
  if (resultData.value && apiKey.value.trim()) {
    queryKey()
  }
}

// CIRCUMFERENCE matches the SVG ring's sixty-eight-unit radius and converts progress ratios
// into stroke offsets used by each card.
const CIRCUMFERENCE = 2 * Math.PI * 68
// RING_ANIMATION_MS supplies the shared duration for numeric percentage interpolation and
// the template's SVG stroke transition.
const RING_ANIMATION_MS = 1200
// RING_GRADIENTS cycles four accent pairs across quota cards and supplies matching colors
// for their displayed amounts.
const RING_GRADIENTS = [
  { from: '#00D2FF', to: '#8BDDF8' },
  { from: '#6366F1', to: '#A5B4FC' },
  { from: '#10B981', to: '#6EE7B7' },
  { from: '#F59E0B', to: '#FCD34D' },
]

// Ring animation state distinguishes the initial empty stroke from interpolated
// percentages; ringTrackColor follows the current theme.
const ringAnimated = ref(false)
const displayPcts = ref<number[]>([])

const ringTrackColor = computed(() => (isDark.value ? '#27272A' : '#F0F0EE'))

// RingItem describes one quota or balance card and its ring presentation.
interface RingItem {
  title: string // translated ring card title
  pct: number // displayed quota usage percentage
  amount: string // formatted quota or balance amount
  // isBalance selects a fully filled ring and amount-only center; quota rings show animated
  // percentages when this flag is absent.
  isBalance?: boolean
  iconType: 'clock' | 'calendar' | 'dollar' // ring heading icon category
  resetAt?: string | null // optional reset timestamp string
}

// getRingOffset converts the current ring state into an SVG stroke offset. Unstarted
// animations remain empty, balance rings become complete, and quota percentages are capped
// at a full circle.
function getRingOffset(ring: RingItem): number {
  if (!ringAnimated.value) return CIRCUMFERENCE
  if (ring.isBalance) return 0
  // Cap percentage fill so excess usage cannot wrap beyond a complete ring.
  return CIRCUMFERENCE - (Math.min(ring.pct, 100) / 100) * CIRCUMFERENCE
}

// triggerRingAnimation resets displayed percentages and schedules interpolation after
// rendering and a short paint delay. The supplied item list fixes the target percentages
// for that animation, with balance cards receiving zero.
//
// Each frame computes elapsed progress against RING_ANIMATION_MS and applies cubic easing
// before rounding the displayed numbers. Frames continue until the normalized duration
// reaches one. The function retains no cancellation handle, so repeated calls and unmount
// do not invalidate an already scheduled animation loop.
function triggerRingAnimation(items: RingItem[]) {
  ringAnimated.value = false
  displayPcts.value = items.map(() => 0)

  nextTick(() => {
    requestAnimationFrame(() => {
      setTimeout(() => {
        ringAnimated.value = true

        // Animate percentage numbers
        // Capture the animation start after the paint delay so numeric interpolation
        // begins alongside the enabled ring stroke transition.
        const startTime = performance.now()
        const targets = items.map((item) => (item.isBalance ? 0 : item.pct))

        // tick updates animated percentages using elapsed time and schedules another frame
        // until completion. Targets remain those captured when the animation began, even if
        // result data changes during the animation.
        function tick() {
          const elapsed = performance.now() - startTime
          // Cap normalized progress and ease toward whole percentages for the
          // animated labels.
          const p = Math.min(elapsed / RING_ANIMATION_MS, 1)
          const ease = 1 - Math.pow(1 - p, 3)
          displayPcts.value = targets.map((target) => Math.round(ease * target))
          if (p < 1) requestAnimationFrame(tick)
        }
        requestAnimationFrame(tick)
      }, 50)
    })
  })
}

// statusInfo selects quota status or billing availability for the status badge, keeping
// missing results separate from valid active data.
const statusInfo = computed(() => {
  const data = resultData.value
  if (!data) return null
  const billingUnavailable = data.billing?.available === false

  if (data.mode === 'quota_limited') {
    const isValid = data.isValid !== false
    const statusMap: Record<string, string> = {
      active: 'Active',
      quota_exhausted: 'Quota Exhausted',
      expired: 'Expired',
    }
    return {
      label: t('keyUsage.quotaMode'),
      statusText: statusMap[data.status] || data.status || 'Unknown',
      isActive: isValid && data.status === 'active',
    }
  }

  return {
    label: data.billing?.plan_name || data.planName || t('keyUsage.walletBalance'),
    statusText: billingUnavailable ? t('keyUsage.billingUnavailable') : 'Active',
    isActive: data.isValid !== false && !billingUnavailable,
  }
})

// billingModeLabel translates subscription and balance preferences, using automatic mode
// for other values. This supplies a display label even when older responses omit the
// configured billing mode.
function billingModeLabel(mode: unknown): string {
  switch (mode) {
    case 'subscription':
      return t('keyUsage.billingModes.subscription')
    case 'balance':
      return t('keyUsage.billingModes.balance')
    default:
      return t('keyUsage.billingModes.auto')
  }
}

// billingSourceLabel names subscription funding when explicitly selected and otherwise
// names wallet balance. Callers use this label when the response has no more specific
// billing plan name.
function billingSourceLabel(source: unknown): string {
  return source === 'subscription'
    ? t('keyUsage.billingSources.subscription')
    : t('keyUsage.billingSources.balance')
}

// usesSubscriptionBilling recognizes an explicit subscription source or a legacy
// subscription object. The legacy object counts only when billing metadata is absent, so
// current billing source remains authoritative.
function usesSubscriptionBilling(
  data:
    | {
        billing?: {
          source?: unknown // selected billing funding source
        } // optional current billing metadata
        subscription?: unknown // optional subscription limit metadata
      }
    | null
    | undefined,
): boolean {
  return data?.billing?.source === 'subscription' || (!data?.billing && !!data?.subscription)
}

// ringItems builds quota, recurring-limit, subscription, or wallet cards from the available
// response. Missing responses produce no cards, and subscription funding follows
// usesSubscriptionBilling rather than the mere presence of a subscription object.
//
// Quota percentages and positive subscription limits produce capped usage rings, while
// wallet balances use a filled balance ring. Rate-limit records keep their reset timestamp
// for countdown labels. Absent or nonpositive subscription limits omit their cards,
// retaining the distinction between a usable limit and an unavailable limit value.
const ringItems = computed<RingItem[]>(() => {
  const data = resultData.value
  if (!data) return []

  const items: RingItem[] = []
  const subscriptionSource = usesSubscriptionBilling(data)

  if (data.mode === 'quota_limited') {
    if (data.quota) {
      // Cap quota progress at one full ring for exhausted limits.
      const pct =
        data.quota.limit > 0
          ? Math.min(Math.round((data.quota.used / data.quota.limit) * 100), 100)
          : 0
      items.push({
        title: t('keyUsage.totalQuota'),
        pct,
        amount: `${formatUsageBalance(data.quota.used)} / ${formatUsageBalance(data.quota.limit)}`,
        iconType: 'dollar',
      })
    }
    if (data.rate_limits) {
      const windowLabels: Record<string, string> = {
        '5h': t('keyUsage.limit5h'),
        '1d': t('keyUsage.limitDaily'),
        '7d': t('keyUsage.limit7d'),
      }
      const windowIcons: Record<string, 'clock' | 'calendar'> = {
        '5h': 'clock',
        '1d': 'calendar',
        '7d': 'calendar',
      }
      for (const rl of data.rate_limits) {
        // A nonpositive rate limit yields zero progress instead of division.
        const pct = rl.limit > 0 ? Math.min(Math.round((rl.used / rl.limit) * 100), 100) : 0
        items.push({
          title: windowLabels[rl.window] || rl.window,
          pct,
          amount: `${formatUsageBalance(rl.used)} / ${formatUsageBalance(rl.limit)}`,
          iconType: windowIcons[rl.window] || 'clock',
          resetAt: rl.reset_at,
        })
      }
    }
  } else {
    if (subscriptionSource && data.subscription) {
      const sub = data.subscription
      const limits = [
        { label: t('keyUsage.limitDaily'), usage: sub.daily_usage_usd, limit: sub.daily_limit_usd },
        {
          label: t('keyUsage.limitWeekly'),
          usage: sub.weekly_usage_usd,
          limit: sub.weekly_limit_usd,
        },
        {
          label: t('keyUsage.limitMonthly'),
          usage: sub.monthly_usage_usd,
          limit: sub.monthly_limit_usd,
        },
      ]
      for (const l of limits) {
        if (l.limit != null && l.limit > 0) {
          // Positive subscription limits produce whole percentages capped at one circle.
          const pct = Math.min(Math.round((l.usage / l.limit) * 100), 100)
          items.push({
            title: l.label,
            pct,
            amount: `${formatUsageBalance(l.usage)} / ${formatUsageBalance(l.limit)}`,
            iconType: 'calendar',
          })
        }
      }
    }
    if (!subscriptionSource && data.balance != null) {
      items.push({
        title: t('keyUsage.walletBalance'),
        pct: 0,
        amount: formatUsageBalance(data.balance),
        isBalance: true,
        iconType: 'dollar',
      })
    }
  }

  return items
})

// ringGridClass chooses the card grid from the number of rendered rings. A single ring is
// centered, two use a desktop pair, and larger sets add a third wide-screen column.
const ringGridClass = computed(() => {
  const len = ringItems.value.length
  if (len === 1) return 'grid grid-cols-1 max-w-md mx-auto gap-6'
  if (len === 2) return 'grid grid-cols-1 md:grid-cols-2 gap-6'
  return 'grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6'
})

// DetailRow describes one billing or quota fact with its icon and text styling.
interface DetailRow {
  iconBg: string // detail icon background classes
  iconColor: string // detail icon foreground classes
  iconSvg: string // trusted inline SVG fragment
  label: string // translated measurement heading
  value: string // formatted measurement display text
  valueClass: string // measurement text color classes
  // useBalanceIcon selects the shared balance icon instead of rendering the row's trusted
  // inline SVG fragment.
  useBalanceIcon?: boolean
}

// getUsageColor assigns warning colors above seventy and ninety percent usage. Exact
// threshold values stay in the lower severity class, while smaller observations use the
// normal green color.
function getUsageColor(pct: number): string {
  // Escalate usage colors after each band, keeping exact thresholds in the
  // lower band.
  if (pct > 90) return 'text-rose-500'
  if (pct > 70) return 'text-amber-500'
  return 'text-emerald-500'
}

// detailRows builds billing source, quota, expiry, and remaining-allowance facts from the
// response. Quota-limited keys show their own limits; other keys use subscription metadata
// only when that funding source applies.
//
// An absent billing object permits legacy plan labels. Negative remaining allowance becomes
// unlimited only for subscription-backed responses with a subscription record; other
// negative values pass through the amount formatter as unavailable. Reset timestamps and
// local language units accompany recurring limits, while warning colors distinguish
// exhausted, low, and healthy balances.
const detailRows = computed<DetailRow[]>(() => {
  const data = resultData.value
  if (!data) return []

  const rows: DetailRow[] = []
  const ICON_SHIELD = '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>'
  const ICON_CALENDAR =
    '<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>'
  const ICON_CHECK = '<polyline points="20 6 9 17 4 12"/>'
  const subscriptionSource = usesSubscriptionBilling(data)

  if (data.billing) {
    const sourceName = data.billing.plan_name || billingSourceLabel(data.billing.source)
    const availability =
      data.billing.available === false ? ` (${t('keyUsage.billingUnavailable')})` : ''
    rows.push({
      iconBg: 'bg-primary-500/10',
      iconColor: 'text-primary-500',
      iconSvg: ICON_CHECK,
      label: t('keyUsage.billingMode'),
      value: `${billingModeLabel(data.billing.mode)} · ${sourceName}${availability}`,
      valueClass: data.billing.available === false ? 'text-rose-500' : '',
    })
  }

  if (data.mode === 'quota_limited') {
    if (data.quota) {
      // Mark exhausted quota red and less than ten percent remaining amber.
      const remainColor =
        data.quota.remaining <= 0
          ? 'text-rose-500'
          : data.quota.remaining < data.quota.limit * 0.1
            ? 'text-amber-500'
            : 'text-emerald-500'
      rows.push({
        iconBg: 'bg-emerald-500/10',
        iconColor: 'text-emerald-500',
        iconSvg: ICON_SHIELD,
        label: t('keyUsage.remainingQuota'),
        value: formatUsageBalance(data.quota.remaining),
        valueClass: remainColor,
        useBalanceIcon: true,
      })
    }
    if (data.expires_at) {
      const daysLeft = data.days_until_expiry
      let expiryStr = formatDate(data.expires_at)
      if (daysLeft != null) {
        expiryStr +=
          daysLeft > 0
            ? ` ${t('keyUsage.daysLeft', { days: daysLeft })}`
            : daysLeft === 0
              ? ` ${t('keyUsage.todayExpires')}`
              : ''
      }
      rows.push({
        iconBg: 'bg-amber-500/10',
        iconColor: 'text-amber-500',
        iconSvg: ICON_CALENDAR,
        label: t('keyUsage.expiresAt'),
        value: expiryStr,
        valueClass: '',
      })
    }
    if (data.rate_limits) {
      const windowMap: Record<string, string> = {
        '5h': '5H',
        '1d': locale.value === 'zh-Hans' ? '日' : 'D',
        '7d': '7D',
      }
      for (const rl of data.rate_limits) {
        // Convert window usage into the percentage consumed by warning
        // colors.
        const pct = rl.limit > 0 ? (rl.used / rl.limit) * 100 : 0
        let valueStr = `${formatUsageBalance(rl.used)} / ${formatUsageBalance(rl.limit)}`
        const resetStr = formatResetTime(rl.reset_at)
        if (resetStr) {
          valueStr += ` (⟳ ${resetStr})`
        }
        rows.push({
          iconBg: 'bg-primary-500/10',
          iconColor: 'text-primary-500',
          iconSvg: '',
          label: `${t('keyUsage.usedQuota')} (${windowMap[rl.window] || rl.window})`,
          value: valueStr,
          valueClass: getUsageColor(pct),
          useBalanceIcon: true,
        })
      }
    }
  } else {
    if (!data.billing) {
      rows.push({
        iconBg: 'bg-emerald-500/10',
        iconColor: 'text-emerald-500',
        iconSvg: ICON_CHECK,
        label: t('keyUsage.subscriptionType'),
        value: data.planName || t('keyUsage.walletBalance'),
        valueClass: '',
      })
    }

    if (subscriptionSource && data.subscription) {
      const sub = data.subscription
      if (sub.daily_limit_usd > 0) {
        // Convert daily usage into the percentage consumed by warning colors.
        const pct = (sub.daily_usage_usd / sub.daily_limit_usd) * 100
        rows.push({
          iconBg: 'bg-primary-500/10',
          iconColor: 'text-primary-500',
          iconSvg: '',
          label: `${t('keyUsage.usedQuota')} (${locale.value === 'zh-Hans' ? '日' : 'D'})`,
          value: `${formatUsageBalance(sub.daily_usage_usd)} / ${formatUsageBalance(sub.daily_limit_usd)}`,
          valueClass: getUsageColor(pct),
          useBalanceIcon: true,
        })
      }
      if (sub.weekly_limit_usd > 0) {
        // Convert weekly usage into the percentage consumed by warning
        // colors.
        const pct = (sub.weekly_usage_usd / sub.weekly_limit_usd) * 100
        rows.push({
          iconBg: 'bg-indigo-500/10',
          iconColor: 'text-indigo-500',
          iconSvg: '',
          label: `${t('keyUsage.usedQuota')} (${locale.value === 'zh-Hans' ? '周' : 'W'})`,
          value: `${formatUsageBalance(sub.weekly_usage_usd)} / ${formatUsageBalance(sub.weekly_limit_usd)}`,
          valueClass: getUsageColor(pct),
          useBalanceIcon: true,
        })
      }
      if (sub.monthly_limit_usd > 0) {
        // Convert monthly usage into the percentage consumed by warning
        // colors.
        const pct = (sub.monthly_usage_usd / sub.monthly_limit_usd) * 100
        rows.push({
          iconBg: 'bg-emerald-500/10',
          iconColor: 'text-emerald-500',
          iconSvg: '',
          label: `${t('keyUsage.usedQuota')} (${locale.value === 'zh-Hans' ? '月' : 'M'})`,
          value: `${formatUsageBalance(sub.monthly_usage_usd)} / ${formatUsageBalance(sub.monthly_limit_usd)}`,
          valueClass: getUsageColor(pct),
          useBalanceIcon: true,
        })
      }
      if (sub.expires_at) {
        rows.push({
          iconBg: 'bg-amber-500/10',
          iconColor: 'text-amber-500',
          iconSvg: ICON_CALENDAR,
          label: t('keyUsage.subscriptionExpires'),
          value: formatDate(sub.expires_at),
          valueClass: '',
        })
      }
    }

    const hasUnlimitedSubscriptionRemaining =
      subscriptionSource && !!data.subscription && data.remaining != null && data.remaining < 0
    // Unlimited subscription allowance stays green; finite allowance uses
    // exhausted and low-balance bands, while missing allowance receives no
    // color.
    const remainColor = hasUnlimitedSubscriptionRemaining
      ? 'text-emerald-500'
      : data.remaining != null
        ? data.remaining <= 0
          ? 'text-rose-500'
          : data.remaining < 10
            ? 'text-amber-500'
            : 'text-emerald-500'
        : ''
    rows.push({
      iconBg: 'bg-emerald-500/10',
      iconColor: 'text-emerald-500',
      iconSvg: ICON_SHIELD,
      label: t('keyUsage.remainingQuota'),
      value: hasUnlimitedSubscriptionRemaining
        ? t('payment.planCard.unlimited')
        : data.remaining != null
          ? formatUsageBalance(data.remaining)
          : '-',
      valueClass: remainColor,
      useBalanceIcon: true,
    })
  }

  return rows
})

// StatCell pairs a translated usage heading with its formatted measurement.
interface StatCell {
  label: string // translated measurement heading
  value: string // formatted measurement display text
}

// usageStatCells maps daily and total request, token, cache, and cost observations into
// labeled cells, retaining unavailable values through shared formatters.
const usageStatCells = computed<StatCell[]>(() => {
  const usage = resultData.value?.usage
  if (!usage) return []

  const today = usage.today || {}
  const total = usage.total || {}

  return [
    { label: t('keyUsage.todayRequests'), value: fmtNum(today.requests) },
    { label: t('keyUsage.todayInputTokens'), value: fmtNum(today.input_tokens) },
    { label: t('keyUsage.todayOutputTokens'), value: fmtNum(today.output_tokens) },
    { label: t('keyUsage.todayTokens'), value: fmtNum(today.total_tokens) },
    { label: t('keyUsage.todayCacheCreation'), value: fmtNum(today.cache_creation_tokens) },
    { label: t('keyUsage.todayCacheRead'), value: fmtNum(today.cache_read_tokens) },
    { label: t('keyUsage.todayCost'), value: formatUsageBalance(today.actual_cost) },
    { label: t('keyUsage.rpmTpm'), value: `${usage.rpm || 0} / ${usage.tpm || 0}` },
    { label: t('keyUsage.totalRequests'), value: fmtNum(total.requests) },
    { label: t('keyUsage.totalInputTokens'), value: fmtNum(total.input_tokens) },
    { label: t('keyUsage.totalOutputTokens'), value: fmtNum(total.output_tokens) },
    { label: t('keyUsage.totalTokensLabel'), value: fmtNum(total.total_tokens) },
    { label: t('keyUsage.totalCacheCreation'), value: fmtNum(total.cache_creation_tokens) },
    { label: t('keyUsage.totalCacheRead'), value: fmtNum(total.cache_read_tokens) },
    { label: t('keyUsage.totalCost'), value: formatUsageBalance(total.actual_cost) },
    {
      label: t('keyUsage.avgDuration'),
      // Round the displayed average to whole milliseconds.
      value: usage.average_duration_ms ? `${Math.round(usage.average_duration_ms)} ms` : '-',
    },
  ]
})

// modelStats exposes model-specific usage rows from the response and supplies an empty
// array while no breakdown exists.
const modelStats = computed<any[]>(() => resultData.value?.model_stats || [])

// DailyUsageRow carries one calendar day of request, token, and spending observations.
interface DailyUsageRow {
  date: string // usage calendar date string
  requests: number // daily request record count
  input_tokens: number // daily input token count
  output_tokens: number // daily output token count
  cache_read_tokens: number // daily cache read count
  cache_write_tokens: number // daily cache write count
  cost: number // daily standard usage cost
  // actual_cost supplies actual daily charges when present; the table otherwise displays
  // the recorded standard cost.
  actual_cost?: number
}

// dailyUsageRows accepts array-shaped daily history and supplies an empty list for missing
// or incompatible response values.
const dailyUsageRows = computed<DailyUsageRow[]>(() => {
  const rows = resultData.value?.daily_usage
  return Array.isArray(rows) ? rows : []
})

// showDailyUsage distinguishes a present daily-history array, including an empty array,
// from responses that do not support the section.
const showDailyUsage = computed(() =>
  Boolean(resultData.value && Array.isArray(resultData.value.daily_usage)),
)

// formatUsageBalance renders nonnegative amounts with two decimals in configured balance
// units. Nullish or negative values use a dash so sentinel amounts are not shown as
// spendable money.
function formatUsageBalance(value: number | null | undefined): string {
  if (value == null || value < 0) return '-'
  return formatBalanceAmount(Number(value), { fractionDigits: 2 })
}

// fmtNum renders a numeric usage count with the active locale. Nullish values use a dash,
// retaining the difference between a missing observation and a measured zero count.
function fmtNum(val: number | null | undefined): string {
  if (val == null) return '-'
  return val.toLocaleString(getLocale())
}

// formatDate renders a supplied timestamp as a localized long calendar date. Missing values
// use a dash, while Date and the active locale determine the display of supplied
// timestamps.
function formatDate(iso: string | null | undefined): string {
  if (!iso) return '-'
  const d = new Date(iso)
  const loc = locale.value
  return d.toLocaleDateString(loc, { year: 'numeric', month: 'long', day: 'numeric' })
}

// getBrowserTimezone reads the browser's IANA timezone with UTC as its default. Empty
// timezone results and Intl failures both use UTC so every key query carries a timezone
// value.
function getBrowserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    // Use UTC if timezone discovery fails so the date query still supplies an
    // interpretation for calendar-based usage buckets.
    return 'UTC'
  }
}

// fetchUsage requests key usage with bearer authorization, selected date parameters, and
// the active locale. JSON decoding failure yields null so HTTP failure handling can still
// report status information.
//
// If the locale changes while the request runs, it repeats the entire query before
// interpreting status or returning data. Current HTTP failures choose a server message or
// status-based default and pass it through localizedErrorMessage. Successful responses
// return parsed data, including null when a successful response has an unreadable body.
async function fetchUsage(key: string): Promise<any> {
  const requestedLocale = getLocale()
  const dateParams = getDateParams()
  const url = buildGatewayUrl('/v1/usage') + (dateParams ? '?' + dateParams : '')
  const res = await fetch(url, {
    headers: { Authorization: 'Bearer ' + key, 'Accept-Language': requestedLocale },
  })
  // Unreadable JSON becomes null so HTTP errors can still use their status-based message,
  // while successful unreadable responses retain the null result.
  const data = await res.json().catch(() => null)
  // Retry before interpreting response status when language changes during retrieval,
  // preventing messages from the earlier locale from becoming the published result.
  if (requestedLocale !== getLocale()) return fetchUsage(key)
  if (!res.ok) {
    const message =
      data?.error?.message || data?.message || `${t('keyUsage.queryFailed')} (${res.status})`
    throw new Error(localizedErrorMessage(data?.error?.code || data?.reason, res.status, message))
  }
  return data
}

useLocaleRefresh(async () => {
  if (!resultData.value || isQuerying.value) return
  const data = await fetchUsage(apiKey.value.trim())
  resultData.value = data
})

// queryKey validates a nonempty credential and starts a lookup unless another lookup is
// busy. It clears earlier results, shows loading, and reveals date controls after a
// successful response.
//
// Success publishes data before scheduling ring animation for the resulting cards. Failure
// hides the result container and reports the error message; settlement releases the request
// guard. The guard prevents overlapping direct queries, while locale refresh uses its own
// callback and skips refresh during an active direct query.
async function queryKey() {
  if (isQuerying.value) return
  const key = apiKey.value.trim()
  if (!key) {
    appStore.showInfo(t('keyUsage.enterApiKey'))
    return
  }

  isQuerying.value = true
  showResults.value = true
  showLoading.value = true
  resultData.value = null

  try {
    const data = await fetchUsage(key)
    resultData.value = data
    showLoading.value = false
    showDatePicker.value = true

    // Trigger ring animations after DOM update
    nextTick(() => {
      triggerRingAnimation(ringItems.value)
    })

    appStore.showSuccess(t('keyUsage.querySuccess'))
  } catch (err) {
    // Hide the failed result area and report the localized message; successful date-
    // control availability from an earlier lookup remains unchanged.
    showResults.value = false
    showLoading.value = false
    appStore.showError((err as Error).message || t('keyUsage.queryFailedRetry'))
  } finally {
    isQuerying.value = false
  }
}

// formatResetTime formats the interval from the ticking current instant to a reset
// timestamp. Missing timestamps return empty text, expired times use reset-now text, and
// future values show their two largest available units.
function formatResetTime(resetAt: string | null | undefined): string {
  if (!resetAt) return ''
  const diff = new Date(resetAt).getTime() - now.value.getTime()
  if (diff <= 0) return t('keyUsage.resetNow')
  // Convert elapsed milliseconds into whole countdown units for compact reset labels.
  const days = Math.floor(diff / 86400000)
  const hours = Math.floor((diff % 86400000) / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  // unit formats one countdown quantity with the active locale's narrow unit label.
  const unit = (value: number, name: string) =>
    new Intl.NumberFormat(getLocale(), { style: 'unit', unit: name, unitDisplay: 'narrow' }).format(
      value,
    )
  if (days > 0) return `${unit(days, 'day')} ${unit(hours, 'hour')}`
  if (hours > 0) return `${unit(hours, 'hour')} ${unit(mins, 'minute')}`
  return unit(mins, 'minute')
}

onMounted(() => {
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
  // Refresh the countdown clock once per minute so rendered reset labels age even while
  // the user leaves the results unchanged.
  resetTimer = setInterval(() => {
    now.value = new Date()
  }, 60000)
})

onUnmounted(() => {
  // Release the countdown interval when the page unmounts; separately scheduled ring
  // animation callbacks do not share this timer handle.
  if (resetTimer) clearInterval(resetTimer)
})
</script>

<style scoped>
/* The template supplies RING_ANIMATION_MS as transitionDuration so stroke movement and numeric interpolation share the same duration. */
.progress-ring {
  transition-property: stroke-dashoffset;
  transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1);
  transform: rotate(-90deg);
  transform-origin: 50% 50%;
}

/* Skeleton loading */
@keyframes shimmer-kv {
  0% {
    background-position: -200% 0;
  }
  100% {
    background-position: 200% 0;
  }
}
.skeleton {
  background: linear-gradient(90deg, #ddf4fc 25%, #f6fcff 50%, #ddf4fc 75%);
  background-size: 200% 100%;
  animation: shimmer-kv 1.8s ease-in-out infinite;
  border-radius: var(--radius-surface);
}
.dark .skeleton {
  background: linear-gradient(90deg, #17171a 25%, #0f0f10 50%, #17171a 75%);
  background-size: 200% 100%;
}

/* Fade up animation */
@keyframes fade-up-kv {
  from {
    opacity: 0;
    transform: translateY(16px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
.fade-up {
  animation: fade-up-kv 0.5s cubic-bezier(0.4, 0, 0.2, 1) forwards;
}
.fade-up-delay-1 {
  animation-delay: 0.1s;
  opacity: 0;
}
.fade-up-delay-2 {
  animation-delay: 0.2s;
  opacity: 0;
}
.fade-up-delay-3 {
  animation-delay: 0.3s;
  opacity: 0;
}
.fade-up-delay-4 {
  animation-delay: 0.4s;
  opacity: 0;
}

/* Pulse dot */
@keyframes pulse-dot-kv {
  0%,
  100% {
    opacity: 1;
    box-shadow: 0 0 0 0 currentColor;
  }
  50% {
    opacity: 0.6;
    box-shadow: 0 0 8px 2px currentColor;
  }
}
.pulse-dot {
  animation: pulse-dot-kv 2s ease-in-out infinite;
}

/* Tabular nums */
.tabular-nums {
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
}
</style>
