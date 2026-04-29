<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="homeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- 默认首页 -->
  <div v-else class="ba-theme-shell relative flex min-h-screen flex-col overflow-hidden text-gray-950 dark:text-white">
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

    <header class="relative z-20 border-b border-gray-200/70 bg-white/90 px-4 backdrop-blur dark:border-dark-800 dark:bg-dark-950/90 sm:px-6">
      <nav class="mx-auto flex h-16 max-w-7xl items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-2.5">
          <span class="h-8 w-8 shrink-0 overflow-hidden rounded-lg shadow-sm">
            <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
          </span>
          <span class="truncate text-sm font-semibold text-gray-950 dark:text-white">{{ siteName }}</span>
        </router-link>

        <router-link
          to="/models"
          class="hidden min-w-[220px] max-w-xs flex-1 items-center gap-2 rounded-lg bg-gray-100 px-3 py-2 text-sm text-gray-500 transition hover:bg-gray-200 hover:text-gray-900 dark:bg-dark-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white md:flex"
        >
          <Icon name="search" size="sm" />
          <span class="truncate">{{ t('home.nav.searchModels') }}</span>
        </router-link>

        <div class="flex items-center gap-2 sm:gap-3">
          <div class="hidden items-center gap-5 text-sm font-medium text-gray-600 dark:text-dark-300 lg:flex">
            <router-link to="/models" class="transition hover:text-gray-950 dark:hover:text-white">
              {{ t('home.nav.models') }}
            </router-link>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="transition hover:text-gray-950 dark:hover:text-white"
            >
              {{ t('home.docs') }}
            </a>
            <a
              :href="githubUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="transition hover:text-gray-950 dark:hover:text-white"
            >
              GitHub
            </a>
          </div>

          <LocaleSwitcher />

          <button
            @click="toggleTheme"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-900 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>

          <router-link
            v-if="isAuthenticated"
            :to="dashboardPath"
            class="inline-flex items-center gap-1.5 rounded-full bg-primary-600 py-1.5 pl-1.5 pr-3 text-xs font-semibold text-white shadow-sm shadow-primary-500/20 transition hover:bg-primary-700 dark:bg-primary-600 dark:text-white dark:hover:bg-primary-700"
          >
            <span class="flex h-5 w-5 items-center justify-center rounded-full bg-primary-500 text-[10px] text-white">
              {{ userInitial }}
            </span>
            {{ t('home.dashboard') }}
          </router-link>
          <router-link
            v-else
            to="/login"
            class="inline-flex items-center rounded-full bg-gray-950 px-4 py-2 text-xs font-semibold text-white transition hover:bg-gray-800 dark:bg-white dark:text-dark-950 dark:hover:bg-dark-200"
          >
            {{ t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="relative z-10 flex-1 px-4 pb-20 pt-16 sm:px-6 lg:px-8">
      <section class="mx-auto max-w-5xl text-center">
        <h1 class="mx-auto max-w-4xl text-4xl font-bold leading-[1.05] tracking-tight text-gray-950 dark:text-white sm:text-5xl md:text-6xl lg:text-7xl">
          {{ homeHeroTitle }}
        </h1>
        <p class="mx-auto mt-6 max-w-2xl text-lg leading-8 text-gray-600 dark:text-dark-300">
          {{ homeHeroSubtitle }}
        </p>

        <div class="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-lg bg-primary-600 px-6 py-3 text-sm font-semibold text-white shadow-sm shadow-primary-500/20 transition hover:bg-primary-700"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            <Icon name="arrowRight" size="sm" :stroke-width="2" />
          </router-link>
          <router-link
            to="/models"
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-lg border border-gray-200 bg-white px-6 py-3 text-sm font-semibold text-gray-900 shadow-sm transition hover:border-primary-300 hover:text-primary-600 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 dark:hover:border-primary-500"
          >
            {{ t('home.exploreMarketplace') }}
            <Icon name="sparkles" size="sm" class="text-primary-500" />
          </router-link>
        </div>
      </section>

      <section class="mx-auto mt-16 grid max-w-4xl grid-cols-2 gap-x-6 gap-y-8 md:grid-cols-4">
        <div v-for="card in homeStatsCards" :key="card.key" class="text-center">
          <p class="text-3xl font-bold tracking-tight text-gray-950 dark:text-white md:text-4xl">
            {{ card.value }}
          </p>
          <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ card.label }}</p>
        </div>
      </section>
      <p v-if="homeStatsError" class="mt-4 text-center text-xs text-gray-500 dark:text-dark-400">
        {{ t('home.stats.unavailable') }}
      </p>

      <section class="mx-auto mt-20 grid max-w-7xl gap-6 lg:grid-cols-4">
        <article class="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm transition hover:-translate-y-0.5 hover:shadow-md dark:border-dark-800 dark:bg-dark-900">
          <div class="relative h-48 overflow-hidden border-b border-gray-200 bg-white dark:border-dark-800 dark:bg-dark-950">
            <span class="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_50%_30%,rgba(15,23,42,0.05),transparent_52%)] dark:bg-[radial-gradient(circle_at_50%_30%,rgba(255,255,255,0.08),transparent_52%)]"></span>
            <span
              v-for="(icon, index) in homeProviderCloudIcons"
              :key="`${icon.brand}-${index}`"
              class="absolute flex h-7 w-7 items-center justify-center rounded-full border border-gray-100 bg-white/95 text-gray-700 shadow-[0_5px_16px_rgba(15,23,42,0.13)] ring-1 ring-black/[0.02] dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 dark:ring-white/[0.04]"
              :style="{
                left: icon.left,
                top: icon.top,
                opacity: icon.opacity,
                transform: `translate(-50%, -50%) scale(${icon.scale})`,
              }"
            >
              <ProviderIcon :brand="icon.brand" size="14px" />
            </span>
            <span
              class="pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t from-white via-white/35 to-transparent dark:from-dark-950 dark:via-dark-950/35"
            ></span>
          </div>
          <div class="p-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.unifiedGateway') }}
            </h2>
            <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.unifiedGatewayDesc') }}
            </p>
            <router-link to="/models" class="mt-5 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300">
              {{ t('home.features.browseAll') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article class="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm transition hover:-translate-y-0.5 hover:shadow-md dark:border-dark-800 dark:bg-dark-900">
          <div class="relative flex h-48 items-center justify-center overflow-hidden border-b border-gray-200 bg-white dark:border-dark-800 dark:bg-dark-950">
            <div class="absolute top-7 z-10 max-w-[82%] truncate rounded-lg bg-gray-100 px-3.5 py-1.5 text-xs font-medium text-gray-800 shadow-sm dark:bg-dark-900 dark:text-dark-100">
              {{ homeRouteLabel }}
            </div>
            <svg
              class="absolute left-1/2 top-12 h-28 w-[260px] -translate-x-1/2 text-gray-300 dark:text-dark-700"
              viewBox="0 0 260 120"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M130 0V30"
                stroke="currentColor"
                stroke-width="1.35"
                stroke-linecap="round"
              />
              <path
                d="M130 30C130 63 35 55 35 92M130 30C130 58 130 68 130 92M130 30C130 63 225 55 225 92"
                stroke="currentColor"
                stroke-width="1.35"
                stroke-linecap="round"
              />
            </svg>
            <div class="absolute bottom-6 left-1/2 flex w-[226px] -translate-x-1/2 justify-between">
              <span
                v-for="brand in homeRouteProviderBrands"
                :key="brand"
                class="flex h-9 w-9 items-center justify-center rounded-lg border border-gray-100 bg-white text-gray-700 shadow-[0_5px_16px_rgba(15,23,42,0.13)] dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100"
              >
                <ProviderIcon :brand="brand" size="17px" />
              </span>
            </div>
          </div>
          <div class="p-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.multiAccount') }}
            </h2>
            <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.multiAccountDesc') }}
            </p>
            <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="mt-5 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300">
              {{ t('home.features.learnMore') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article class="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm transition hover:-translate-y-0.5 hover:shadow-md dark:border-dark-800 dark:bg-dark-900">
          <div class="flex h-48 items-center justify-center border-b border-gray-200 bg-gray-50 p-6 dark:border-dark-800 dark:bg-dark-950">
            <div class="w-full max-w-[220px] rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-900">
              <div class="mb-4 flex items-center justify-between text-xs text-gray-500 dark:text-dark-400">
                <span>{{ t('home.features.usageChart') }}</span>
                <Icon name="chart" size="sm" />
              </div>
              <div class="space-y-3">
                <div class="h-2 w-11/12 rounded-full bg-sky-300"></div>
                <div class="h-2 w-2/3 rounded-full bg-amber-300"></div>
                <div class="h-2 w-5/6 rounded-full bg-emerald-300"></div>
                <div class="h-2 w-1/2 rounded-full bg-violet-300"></div>
              </div>
            </div>
          </div>
          <div class="p-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.balanceQuota') }}
            </h2>
            <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.balanceQuotaDesc') }}
            </p>
            <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="mt-5 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300">
              {{ t('home.features.viewUsage') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article class="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm transition hover:-translate-y-0.5 hover:shadow-md dark:border-dark-800 dark:bg-dark-900">
          <div class="flex h-48 items-center justify-center border-b border-gray-200 bg-gray-50 dark:border-dark-800 dark:bg-dark-950">
            <div class="relative flex h-28 w-28 items-center justify-center rounded-full border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900">
              <Icon name="shield" size="xl" class="text-gray-400 dark:text-dark-300" />
              <span class="absolute -right-1 -top-1 flex h-9 w-9 items-center justify-center rounded-full bg-emerald-100 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-300">
                <Icon name="check" size="md" :stroke-width="2" />
              </span>
            </div>
          </div>
          <div class="p-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.dataPolicies') }}
            </h2>
            <p class="mt-3 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.dataPoliciesDesc') }}
            </p>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="mt-5 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300"
            >
              {{ t('home.docs') }}
              <Icon name="externalLink" size="xs" />
            </a>
          </div>
        </article>
      </section>

      <section class="mx-auto mt-20 max-w-7xl">
        <div class="mb-6 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <router-link to="/models" class="inline-flex items-center gap-2 text-2xl font-bold text-gray-950 hover:text-primary-600 dark:text-white dark:hover:text-primary-300">
              {{ t('home.providers.title') }}
              <Icon name="chevronRight" size="md" />
            </router-link>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
              {{ formatMarketplaceStat(totalModelCount) }} {{ t('marketplace.modelsStat') }}
              ·
              {{ formatMarketplaceStat(supportedProviders.length) }} {{ t('home.stats.providerTypes') }}
            </p>
          </div>
          <router-link to="/models" class="text-sm font-medium text-gray-500 transition hover:text-primary-600 dark:text-dark-400 dark:hover:text-primary-300">
            {{ t('home.viewAll') }}
            <Icon name="arrowRight" size="xs" class="inline-block" />
          </router-link>
        </div>

        <div class="grid gap-6 lg:grid-cols-3">
          <div
            v-if="homeMarketplaceLoading"
            class="rounded-xl border border-gray-200 bg-white px-5 py-4 text-center text-sm text-gray-500 dark:border-dark-800 dark:bg-dark-900 dark:text-dark-400 lg:col-span-3"
          >
            {{ t('common.loading') }}
          </div>

          <div
            v-else-if="supportedProviders.length === 0"
            class="rounded-xl border border-gray-200 bg-white px-5 py-4 text-center text-sm text-gray-500 dark:border-dark-800 dark:bg-dark-900 dark:text-dark-400 lg:col-span-3"
          >
            {{ homeMarketplaceError ? t('home.providers.unavailable') : t('home.providers.empty') }}
          </div>

          <template v-else>
            <article
              v-for="provider in supportedProviders.slice(0, 6)"
              :key="provider.key"
              class="rounded-xl border border-gray-200 bg-white p-6 shadow-sm transition hover:-translate-y-0.5 hover:shadow-md dark:border-dark-800 dark:bg-dark-900"
            >
              <div class="flex items-start gap-4">
                <span
                  class="flex h-12 w-12 shrink-0 items-center justify-center rounded-full border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950"
                  :class="providerIconWrapClass(provider)"
                >
                  <ProviderIcon :brand="provider.iconBrand" size="22px" />
                </span>
                <div class="min-w-0 flex-1">
                  <h3 class="truncate text-lg font-semibold text-gray-950 dark:text-white">
                    {{ provider.label }}
                  </h3>
                  <p class="text-sm text-gray-500 dark:text-dark-400">
                    {{ provider.groupCount }} {{ t('home.providers.groups') }}
                  </p>
                </div>
              </div>
              <div class="mt-5 border-t border-gray-200 pt-5 dark:border-dark-800">
                <div class="flex items-end justify-between gap-4">
                  <div>
                    <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('home.providers.modelCount') }}</p>
                    <p class="mt-1 text-lg font-semibold text-gray-950 dark:text-white">
                      {{ provider.modelCount }}
                    </p>
                  </div>
                  <p
                    v-if="provider.officialPriceRatio"
                    class="max-w-[180px] text-right text-sm font-semibold text-emerald-600 dark:text-emerald-300"
                  >
                    {{ formatOfficialPriceRatio(provider.officialPriceRatio) }}
                  </p>
                  <p v-else class="text-sm font-medium text-primary-600 dark:text-primary-300">
                    {{ t('home.providers.supported') }}
                  </p>
                </div>
              </div>
            </article>
          </template>
        </div>
      </section>

      <section class="mx-auto mt-16 max-w-7xl px-2 sm:px-0">
        <div class="grid gap-8 md:grid-cols-3">
          <article
            v-for="step in homeSteps"
            :key="step.key"
            class="flex min-h-[190px] flex-col"
          >
            <div class="flex items-center gap-3">
              <span class="flex h-8 w-8 items-center justify-center rounded-full bg-primary-50 text-base font-semibold text-primary-600 dark:bg-primary-500/10 dark:text-primary-300">
                {{ step.index }}
              </span>
              <h2 class="text-lg font-semibold tracking-tight text-gray-950 dark:text-white">{{ step.title }}</h2>
            </div>
            <p class="mt-4 max-w-sm text-sm leading-6 text-gray-600 dark:text-dark-300">{{ step.description }}</p>

            <div v-if="step.key === 'signup'" class="mt-auto pt-6">
              <div class="flex items-center gap-3 text-primary-500">
                <Icon name="user" size="md" :stroke-width="1.8" />
                <div class="space-y-1.5">
                  <div class="h-1.5 w-7 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                  <div class="h-1.5 w-20 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                </div>
              </div>
              <div class="mt-4 grid max-w-[210px] grid-cols-4 gap-3">
                <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-white/90 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800">
                  <ProviderIcon brand="Google" size="20px" />
                </span>
                <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-white/90 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800">
                  <ProviderIcon brand="OpenAI" size="19px" />
                </span>
                <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-white/90 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800">
                  <ProviderIcon brand="Claude" size="19px" />
                </span>
                <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-white/90 text-primary-500 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800">
                  <Icon name="mail" size="md" :stroke-width="1.8" />
                </span>
              </div>
            </div>

            <div v-else-if="step.key === 'browse'" class="mt-auto max-w-[270px] pt-6">
              <div class="flex items-center gap-3 text-primary-500">
                <Icon name="grid" size="md" :stroke-width="1.8" />
                <div class="grid flex-1 grid-cols-4 gap-2">
                  <div class="h-1 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                  <div class="h-1 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                  <div class="h-1 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                  <div class="h-1 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                </div>
              </div>
              <div class="mt-4 space-y-2">
                <div class="flex items-center gap-2 rounded-md bg-white/90 px-3 py-2 text-gray-700 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-dark-200 dark:ring-dark-800">
                  <span class="w-14 text-xs font-medium">Claude</span>
                  <span class="h-2 flex-1 rounded-full bg-primary-100 dark:bg-primary-400/20"></span>
                  <span class="h-2 w-12 rounded-full bg-primary-100 dark:bg-primary-400/20"></span>
                </div>
                <div class="flex items-center gap-2 rounded-md bg-white/90 px-3 py-2 text-gray-700 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-dark-200 dark:ring-dark-800">
                  <span class="w-14 text-xs font-medium">GPT</span>
                  <span class="h-2 flex-1 rounded-full bg-primary-100 dark:bg-primary-400/20"></span>
                  <span class="h-2 w-12 rounded-full bg-primary-100 dark:bg-primary-400/20"></span>
                </div>
              </div>
            </div>

            <div v-else class="mt-auto max-w-[270px] pt-6">
              <div class="flex items-center gap-3 text-primary-500">
                <Icon name="key" size="md" :stroke-width="1.8" />
                <div class="flex-1 rounded-md bg-white/90 px-3 py-2 font-mono text-xs text-gray-600 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-dark-300 dark:ring-dark-800">
                  TOKENFLUX_API_KEY
                </div>
              </div>
              <div class="mt-3 rounded-md bg-white/90 px-3 py-2 font-mono text-sm tracking-[0.2em] text-gray-950 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-white dark:ring-dark-800">
                ••••••••••••••••
              </div>
            </div>
          </article>
        </div>
      </section>
    </main>

    <footer class="relative z-10 border-t border-gray-200 bg-white/90 px-6 py-8 backdrop-blur dark:border-dark-800 dark:bg-dark-950/90">
      <div class="mx-auto flex max-w-7xl flex-col items-center justify-between gap-4 text-center sm:flex-row sm:text-left">
        <p class="text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-900 dark:text-dark-400 dark:hover:text-white"
          >
            {{ t('home.docs') }}
          </a>
          <a
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-900 dark:text-dark-400 dark:hover:text-white"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useTheme } from '@/composables/useTheme'
import { getMarketplaceModels, getMarketplaceStats } from '@/api/marketplace'
import type { MarketplaceGroup, MarketplaceStats } from '@/types'
import {
  providerBrandDisplayName,
  providerBrandFilterKey,
  resolveProviderBrand,
  resolveProviderBrandKey,
} from '@/utils/providerBrand'

type HomeStatsIcon = 'bolt' | 'database' | 'users' | 'grid'
type HomeStepIcon = 'userPlus' | 'grid' | 'key'

interface HomeProviderCategory {
  key: string
  label: string
  iconBrand: string
}

interface HomeProviderSummary extends HomeProviderCategory {
  modelCount: number
  groupCount: number
  officialPriceRatio?: number
  sortOrder: number
  firstIndex: number
}

interface HomeStatsCard {
  key: string
  label: string
  value: string
  icon: HomeStatsIcon
  iconWrapClass: string
  iconClass: string
}

interface HomeStep {
  key: string
  index: number
  title: string
  description: string
  icon: HomeStepIcon
}

interface HomeProviderCloudIcon {
  brand: string
  left: string
  top: string
  opacity: number
  scale: number
}

const { t, locale } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

// 站点设置直接读取已注入或已缓存的公开配置。
const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const currentLanguage = computed(() => String(locale.value).toLowerCase().startsWith('zh') ? 'zh' : 'en')
const numberLocale = computed(() => currentLanguage.value === 'zh' ? 'zh-CN' : 'en-US')
const homeHeroTitle = computed(() => {
  const settings = appStore.cachedPublicSettings
  return localizedHomeCopy(
    settings?.site_title_zh,
    settings?.site_title_en,
    t('home.heroTitle')
  )
})
const homeHeroSubtitle = computed(() => {
  const settings = appStore.cachedPublicSettings
  return localizedHomeCopy(
    settings?.site_subtitle_zh,
    settings?.site_subtitle_en,
    t('home.heroDescription')
  )
})

// 自定义首页支持 URL iframe 和 HTML 两种模式。
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

const { isDark, toggleTheme } = useTheme()

const githubUrl = 'https://github.com/TokenFlux/TokenRouter'

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

const currentYear = computed(() => new Date().getFullYear())

const marketplaceGroups = ref<MarketplaceGroup[]>([])
const homeStats = ref<MarketplaceStats | null>(null)
const homeMarketplaceLoading = ref(true)
const homeStatsLoading = ref(true)
const homeMarketplaceError = ref(false)
const homeStatsError = ref(false)

const providerVisualFallbacks = [
  'Google',
  'Meta',
  'Gemini',
  'OpenAI',
  'Qwen',
  'DeepSeek',
  'Mistral',
  'Moonshot',
  'Claude',
  'xAI',
  'Antigravity',
  'Zhipu',
  'Cohere',
  'Perplexity',
  'Minimax',
  'Doubao',
  'Baidu',
  'Tencent',
  'Cloudflare',
  'OpenRouter',
]

const providerCloudLayout = [
  { left: '7%', top: '13%', opacity: 0.72, scale: 0.94 },
  { left: '25%', top: '13%', opacity: 0.8, scale: 0.94 },
  { left: '43%', top: '13%', opacity: 0.9, scale: 1 },
  { left: '62%', top: '13%', opacity: 0.8, scale: 0.94 },
  { left: '81%', top: '13%', opacity: 0.72, scale: 0.94 },
  { left: '17%', top: '35%', opacity: 0.86, scale: 0.98 },
  { left: '35%', top: '35%', opacity: 0.92, scale: 1 },
  { left: '53%', top: '35%', opacity: 0.96, scale: 1.04 },
  { left: '72%', top: '35%', opacity: 0.9, scale: 0.98 },
  { left: '91%', top: '35%', opacity: 0.76, scale: 0.94 },
  { left: '7%', top: '57%', opacity: 0.82, scale: 0.94 },
  { left: '25%', top: '57%', opacity: 0.9, scale: 0.98 },
  { left: '43%', top: '57%', opacity: 1, scale: 1.06 },
  { left: '62%', top: '57%', opacity: 0.9, scale: 0.98 },
  { left: '81%', top: '57%', opacity: 0.82, scale: 0.94 },
  { left: '17%', top: '79%', opacity: 0.72, scale: 0.94 },
  { left: '35%', top: '79%', opacity: 0.78, scale: 0.96 },
  { left: '53%', top: '79%', opacity: 0.82, scale: 0.98 },
  { left: '72%', top: '79%', opacity: 0.78, scale: 0.96 },
  { left: '91%', top: '79%', opacity: 0.66, scale: 0.92 },
  { left: '7%', top: '98%', opacity: 0.6, scale: 0.9 },
  { left: '25%', top: '98%', opacity: 0.66, scale: 0.92 },
  { left: '43%', top: '98%', opacity: 0.7, scale: 0.94 },
  { left: '62%', top: '98%', opacity: 0.66, scale: 0.92 },
  { left: '81%', top: '98%', opacity: 0.6, scale: 0.9 },
  { left: '99%', top: '98%', opacity: 0.48, scale: 0.86 },
] as const

const totalModelCount = computed(() =>
  marketplaceGroups.value.reduce((total, group) => total + group.models.length, 0)
)

const supportedProviders = computed<HomeProviderSummary[]>(() => {
  const summaries = new Map<string, HomeProviderSummary>()
  const sortedGroups = [...marketplaceGroups.value].sort((left, right) => {
    const sortDiff = (left.sort_order ?? 0) - (right.sort_order ?? 0)
    if (sortDiff !== 0) {
      return sortDiff
    }
    return left.id - right.id
  })

  sortedGroups.forEach((group, index) => {
    const modelCount = group.models.length
    if (modelCount === 0) {
      return
    }

    const category = homeProviderCategory(group)
    const existing = summaries.get(category.key)
    const ratio = validOfficialPriceRatio(group.official_price_ratio)
    if (!existing) {
      summaries.set(category.key, {
        ...category,
        modelCount,
        groupCount: 1,
        officialPriceRatio: ratio ?? undefined,
        sortOrder: group.sort_order ?? 0,
        firstIndex: index,
      })
      return
    }

    existing.modelCount += modelCount
    existing.groupCount += 1
    existing.sortOrder = Math.min(existing.sortOrder, group.sort_order ?? 0)
    existing.firstIndex = Math.min(existing.firstIndex, index)
    if (ratio && (!existing.officialPriceRatio || ratio < existing.officialPriceRatio)) {
      existing.officialPriceRatio = ratio
    }
  })

  return [...summaries.values()].sort((left, right) => {
    const priorityDiff = homeProviderPriority(left.key) - homeProviderPriority(right.key)
    if (priorityDiff !== 0) {
      return priorityDiff
    }
    const sortDiff = left.sortOrder - right.sortOrder
    if (sortDiff !== 0) {
      return sortDiff
    }
    return left.firstIndex - right.firstIndex
  })
})

const homeStatsCards = computed<HomeStatsCard[]>(() => [
  {
    key: 'today-tokens',
    label: t('home.stats.todayTokens'),
    value: formatHomeStat(homeStats.value?.today_tokens),
    icon: 'bolt',
    iconWrapClass: 'bg-sky-100 dark:bg-sky-500/15',
    iconClass: 'text-sky-600 dark:text-sky-300',
  },
  {
    key: 'total-tokens',
    label: t('home.stats.totalTokens'),
    value: formatHomeStat(homeStats.value?.total_tokens),
    icon: 'database',
    iconWrapClass: 'bg-emerald-100 dark:bg-emerald-500/15',
    iconClass: 'text-emerald-600 dark:text-emerald-300',
  },
  {
    key: 'total-users',
    label: t('home.stats.totalUsers'),
    value: formatHomeStat(homeStats.value?.total_users),
    icon: 'users',
    iconWrapClass: 'bg-violet-100 dark:bg-violet-500/15',
    iconClass: 'text-violet-600 dark:text-violet-300',
  },
  {
    key: 'supported-models',
    label: t('home.stats.supportedModels'),
    value: formatMarketplaceStat(totalModelCount.value),
    icon: 'grid',
    iconWrapClass: 'bg-primary-100 dark:bg-primary-500/15',
    iconClass: 'text-primary-600 dark:text-primary-300',
  },
])

const homeProviderVisuals = computed(() => {
  const brands = supportedProviders.value.map(provider => provider.iconBrand)
  return mergeProviderVisualBrands(brands)
})

const homeProviderCloudIcons = computed<HomeProviderCloudIcon[]>(() => {
  const brands = homeProviderVisuals.value
  return providerCloudLayout.map((layout, index) => ({
    brand: brands[index % brands.length],
    ...layout,
  }))
})

const homeRouteProviderBrands = computed(() => homeProviderVisuals.value.slice(0, 3))

const homeRouteLabel = computed(() => {
  return 'OpenAI/GPT-5.4'
})

const homeSteps = computed<HomeStep[]>(() => [
  {
    key: 'signup',
    index: 1,
    title: t('home.steps.signup.title'),
    description: t('home.steps.signup.description'),
    icon: 'userPlus',
  },
  {
    key: 'browse',
    index: 2,
    title: t('home.steps.browse.title'),
    description: t('home.steps.browse.description'),
    icon: 'grid',
  },
  {
    key: 'api-key',
    index: 3,
    title: t('home.steps.apiKey.title'),
    description: t('home.steps.apiKey.description'),
    icon: 'key',
  },
])

function homeProviderCategory(group: MarketplaceGroup): HomeProviderCategory {
  const brandSource = group.display_brand?.trim() || group.name.trim()
  const brandKey = resolveProviderBrandKey(brandSource)
  if (brandKey && brandKey !== 'unknown') {
    return homeProviderCategoryFromBrand(brandKey, brandSource)
  }

  switch (group.platform) {
    case 'anthropic':
      return { key: 'claude', label: t('home.providers.claude'), iconBrand: 'Claude' }
    case 'openai':
      return { key: 'gpt', label: t('home.providers.gpt'), iconBrand: 'OpenAI' }
    case 'gemini':
      return { key: 'gemini', label: t('home.providers.gemini'), iconBrand: 'Gemini' }
    case 'antigravity':
      return { key: 'antigravity', label: t('home.providers.antigravity'), iconBrand: 'Antigravity' }
  }

  const fallbackLabel = brandSource || group.platform
  return {
    key: providerBrandFilterKey(fallbackLabel),
    label: fallbackLabel,
    iconBrand: fallbackLabel,
  }
}

function homeProviderCategoryFromBrand(brandKey: string, source: string): HomeProviderCategory {
  switch (brandKey) {
    case 'anthropic':
      return { key: 'claude', label: t('home.providers.claude'), iconBrand: 'Claude' }
    case 'openai':
      return { key: 'gpt', label: t('home.providers.gpt'), iconBrand: 'OpenAI' }
    case 'google':
      return { key: 'gemini', label: t('home.providers.gemini'), iconBrand: 'Gemini' }
    default: {
      const label = providerBrandDisplayName(source)
      return { key: brandKey || providerBrandFilterKey(source), label, iconBrand: label }
    }
  }
}

function homeProviderPriority(key: string): number {
  const priorities = ['claude', 'gpt', 'deepseek', 'gemini', 'antigravity']
  const index = priorities.indexOf(key)
  return index === -1 ? priorities.length : index
}

function validOfficialPriceRatio(value?: number): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null
}

function formatOfficialPriceRatio(ratio: number): string {
  const discount = new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(ratio * 10)

  return t('marketplace.officialPriceDiscount', { discount })
}

function formatHomeStat(value?: number): string {
  if (homeStatsLoading.value) {
    return '...'
  }
  return typeof value === 'number' ? formatCompactNumber(value) : '-'
}

function formatMarketplaceStat(value: number): string {
  if (homeMarketplaceLoading.value) {
    return '...'
  }
  return new Intl.NumberFormat(numberLocale.value).format(value)
}

function formatCompactNumber(value: number): string {
  return new Intl.NumberFormat(numberLocale.value, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
}

function localizedHomeCopy(zhText: string | undefined, enText: string | undefined, fallback: string): string {
  const primary = currentLanguage.value === 'zh' ? zhText : enText
  const secondary = currentLanguage.value === 'zh' ? enText : zhText
  return firstConfiguredText(primary, secondary, fallback)
}

function firstConfiguredText(...values: Array<string | undefined>): string {
  for (const value of values) {
    const normalized = value?.trim()
    if (normalized) {
      return normalized
    }
  }
  return ''
}

function mergeProviderVisualBrands(brands: string[]): string[] {
  const seen = new Set<string>()
  const merged: string[] = []

  ;[...brands, ...providerVisualFallbacks].forEach((brand) => {
    const normalizedBrand = brand.trim()
    if (!normalizedBrand || seen.has(normalizedBrand)) {
      return
    }
    seen.add(normalizedBrand)
    merged.push(normalizedBrand)
  })

  return merged
}

function providerIconWrapClass(provider: HomeProviderSummary): string {
  if (provider.key === 'antigravity') {
    return 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-500/15 dark:text-rose-200 dark:ring-rose-400/30'
  }
  return resolveProviderBrand(provider.iconBrand).iconWrapClass
}

async function fetchHomeMarketplace() {
  homeMarketplaceLoading.value = true
  homeMarketplaceError.value = false

  try {
    marketplaceGroups.value = await getMarketplaceModels()
  } catch (error) {
    console.error('Failed to load home marketplace models:', error)
    marketplaceGroups.value = []
    homeMarketplaceError.value = true
  } finally {
    homeMarketplaceLoading.value = false
  }
}

async function fetchHomeStats() {
  homeStatsLoading.value = true
  homeStatsError.value = false

  try {
    homeStats.value = await getMarketplaceStats()
  } catch (error) {
    console.error('Failed to load home marketplace stats:', error)
    homeStats.value = null
    homeStatsError.value = true
  } finally {
    homeStatsLoading.value = false
  }
}

onMounted(async () => {
  authStore.checkAuth()

  if (!appStore.publicSettingsLoaded) {
    try {
      await appStore.fetchPublicSettings()
    } catch (error) {
      console.error('Failed to load public settings:', error)
    }
  }

  if (!homeContent.value) {
    await Promise.all([fetchHomeMarketplace(), fetchHomeStats()])
  }
})
</script>
