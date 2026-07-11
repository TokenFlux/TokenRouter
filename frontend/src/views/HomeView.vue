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
            class="inline-flex items-center gap-1.5 rounded-full bg-primary-600 py-1.5 pl-1.5 pr-3 text-xs font-semibold text-white shadow-none transition hover:bg-primary-700 dark:bg-primary-600 dark:text-white dark:hover:bg-primary-700"
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
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-lg bg-primary-600 px-6 py-3 text-sm font-semibold text-white shadow-none transition hover:bg-primary-700"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            <Icon name="arrowRight" size="sm" :stroke-width="2" />
          </router-link>
          <router-link
            to="/models"
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-lg border border-gray-200 bg-white px-6 py-3 text-sm font-semibold text-gray-900 shadow-sm transition hover:border-primary-300 hover:text-primary-600 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 dark:hover:border-primary-500"
          >
            {{ t('home.exploreMarketplace') }}
            <span class="relative flex h-5 w-5 items-center justify-center overflow-hidden">
              <Transition name="home-marketplace-icon" mode="out-in">
                <ProviderIcon
                  v-if="homeMarketplaceButtonBrand"
                  :key="homeMarketplaceButtonBrand"
                  :brand="homeMarketplaceButtonBrand"
                  size="18px"
                />
                <Icon v-else key="marketplace-fallback" name="sparkles" size="sm" class="text-primary-500" />
              </Transition>
            </span>
          </router-link>
        </div>
      </section>

      <section class="mx-auto mt-16 grid max-w-4xl grid-cols-2 gap-x-6 gap-y-8 md:grid-cols-4">
        <div v-for="card in homeStatsCards" :key="card.key" class="text-center">
          <p class="min-h-[1.1em] text-3xl font-bold tabular-nums tracking-tight text-gray-950 dark:text-white md:text-4xl">
            {{ card.value }}
          </p>
          <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ card.label }}</p>
        </div>
      </section>
      <p v-if="homeStatsError" class="mt-4 text-center text-xs text-gray-500 dark:text-dark-400">
        {{ t('home.stats.unavailable') }}
      </p>

      <!-- Provider icon marquee -->
      <section class="mx-auto mt-14 max-w-5xl" aria-hidden="true">
        <div class="home-marquee relative overflow-hidden">
          <div class="home-marquee-track flex w-max items-center gap-8">
            <span
              v-for="(brand, index) in homeMarqueeBrands"
              :key="`${brand}-${index}`"
              class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full border border-gray-200/80 bg-white text-gray-700 shadow-sm dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100"
            >
              <ProviderIcon :brand="brand" size="17px" />
            </span>
          </div>
        </div>
      </section>

      <section class="mx-auto mt-20 grid max-w-7xl gap-5 sm:grid-cols-2 xl:grid-cols-4">
        <article class="group overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm ring-1 ring-transparent transition duration-300 hover:-translate-y-1 hover:border-primary-300 hover:shadow-[0_12px_32px_rgba(13,42,63,0.1)] focus-within:border-primary-300 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600 dark:hover:shadow-[0_12px_32px_rgba(0,0,0,0.4)]">
          <div class="relative h-44 overflow-hidden border-b border-gray-200 bg-white dark:border-dark-800 dark:bg-dark-950">
            <div class="absolute inset-0 transition-transform duration-500 ease-out group-hover:scale-110">
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
            </div>
            <span
              class="pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t from-white via-white/35 to-transparent dark:from-dark-950 dark:via-dark-950/35"
            ></span>
          </div>
          <div class="p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.unifiedGateway') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.unifiedGatewayDesc') }}
            </p>
            <router-link to="/models" class="mt-4 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300">
              {{ t('home.features.browseAll') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article class="group overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm ring-1 ring-transparent transition duration-300 hover:-translate-y-1 hover:border-primary-300 hover:shadow-[0_12px_32px_rgba(13,42,63,0.1)] focus-within:border-primary-300 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600 dark:hover:shadow-[0_12px_32px_rgba(0,0,0,0.4)]">
          <div class="relative flex h-44 items-center justify-center overflow-hidden border-b border-gray-200 bg-white dark:border-dark-800 dark:bg-dark-950">
            <div class="relative h-full w-full transition-transform duration-500 ease-out group-hover:scale-110">
              <div class="absolute left-1/2 top-7 z-10 max-w-[82%] -translate-x-1/2 truncate rounded-lg bg-gray-100 px-3.5 py-1.5 text-xs font-medium text-gray-800 shadow-sm dark:bg-dark-900 dark:text-dark-100">
                {{ homeRouteLabel }}
              </div>
              <svg
                class="absolute left-1/2 top-12 h-24 w-[220px] -translate-x-1/2 text-gray-300 dark:text-dark-700"
                viewBox="0 0 220 110"
                fill="none"
                aria-hidden="true"
              >
                <path
                  d="M110 0V30"
                  stroke="currentColor"
                  stroke-width="1.35"
                  stroke-linecap="round"
                />
                <path
                  d="M110 30C110 60 28 52 28 84M110 30C110 55 110 64 110 84M110 30C110 60 192 52 192 84"
                  stroke="currentColor"
                  stroke-width="1.35"
                  stroke-linecap="round"
                />
              </svg>
              <div class="absolute bottom-6 left-1/2 flex w-[190px] -translate-x-1/2 justify-between">
                <span
                  v-for="brand in homeRouteProviderBrands"
                  :key="brand"
                  class="flex h-9 w-9 items-center justify-center rounded-lg border border-gray-100 bg-white text-gray-700 shadow-[0_5px_16px_rgba(15,23,42,0.13)] dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100"
                >
                  <ProviderIcon :brand="brand" size="17px" />
                </span>
              </div>
            </div>
          </div>
          <div class="p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.multiAccount') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.multiAccountDesc') }}
            </p>
            <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="mt-4 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300">
              {{ t('home.features.learnMore') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article class="group overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm ring-1 ring-transparent transition duration-300 hover:-translate-y-1 hover:border-primary-300 hover:shadow-[0_12px_32px_rgba(13,42,63,0.1)] focus-within:border-primary-300 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600 dark:hover:shadow-[0_12px_32px_rgba(0,0,0,0.4)]">
          <div class="flex h-44 items-center justify-center border-b border-gray-200 bg-gray-50 p-6 dark:border-dark-800 dark:bg-dark-950">
            <div class="w-full max-w-[200px] rounded-lg border border-gray-200 bg-white p-4 shadow-sm transition-transform duration-500 ease-out group-hover:scale-110 dark:border-dark-700 dark:bg-dark-900">
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
          <div class="p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.balanceQuota') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.balanceQuotaDesc') }}
            </p>
            <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="mt-4 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300">
              {{ t('home.features.viewUsage') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article class="group overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm ring-1 ring-transparent transition duration-300 hover:-translate-y-1 hover:border-primary-300 hover:shadow-[0_12px_32px_rgba(13,42,63,0.1)] focus-within:border-primary-300 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600 dark:hover:shadow-[0_12px_32px_rgba(0,0,0,0.4)]">
          <div class="flex h-44 items-center justify-center border-b border-gray-200 bg-gray-50 dark:border-dark-800 dark:bg-dark-950">
            <div class="relative flex h-24 w-24 items-center justify-center rounded-full border border-gray-200 bg-white shadow-sm transition-transform duration-500 ease-out group-hover:scale-110 dark:border-dark-700 dark:bg-dark-900">
              <Icon name="shield" size="xl" class="text-gray-400 dark:text-dark-300" />
              <span class="absolute -right-1 -top-1 flex h-9 w-9 items-center justify-center rounded-full bg-emerald-100 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-300">
                <Icon name="check" size="md" :stroke-width="2" />
              </span>
            </div>
          </div>
          <div class="p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.dataPolicies') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.dataPoliciesDesc') }}
            </p>
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="mt-4 inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-300"
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
              class="rounded-xl border border-gray-200 bg-white p-6 shadow-sm ring-1 ring-transparent transition duration-300 hover:-translate-y-1 hover:border-primary-300 hover:shadow-[0_12px_32px_rgba(13,42,63,0.1)] focus-within:border-primary-300 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600 dark:hover:shadow-[0_12px_32px_rgba(0,0,0,0.4)]"
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

            <div v-if="step.key === 'signup'" class="mt-8">
              <div class="flex items-center gap-3 text-primary-500">
                <Icon name="user" size="md" :stroke-width="1.8" />
                <div class="space-y-1.5">
                  <div class="h-1.5 w-7 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                  <div class="h-1.5 w-20 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                </div>
              </div>
              <div class="mt-4 grid max-w-[156px] grid-cols-3 gap-3">
                <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-white/90 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800">
                  <ProviderIcon brand="Google" size="20px" />
                </span>
                <span class="flex h-10 w-10 items-center justify-center rounded-lg bg-white/90 text-gray-800 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-gray-100 dark:ring-dark-800">
                  <GitHubMark class="h-5 w-5" />
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

            <div v-else class="mt-8 max-w-[270px]">
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

      <!-- CTA -->
      <section class="mx-auto mt-24 max-w-3xl text-center">
        <h2 class="text-3xl font-bold tracking-tight text-gray-950 dark:text-white sm:text-4xl">
          {{ t('home.cta.title') }}
        </h2>
        <p class="mx-auto mt-4 max-w-xl text-base leading-7 text-gray-600 dark:text-dark-300">
          {{ t('home.cta.description') }}
        </p>
        <div class="mt-8">
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-lg bg-primary-600 px-8 py-3 text-sm font-semibold text-white transition hover:bg-primary-700"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.cta.button') }}
            <Icon name="arrowRight" size="sm" :stroke-width="2" />
          </router-link>
        </div>
      </section>
    </main>

    <footer class="relative z-10 border-t border-gray-200 bg-white/90 px-6 py-12 backdrop-blur dark:border-dark-800 dark:bg-dark-950/90">
      <div class="mx-auto max-w-7xl">
        <div class="grid grid-cols-2 gap-x-8 gap-y-10 sm:grid-cols-3 lg:flex lg:justify-between lg:gap-8">
          <!-- Brand -->
          <div class="col-span-2 sm:col-span-3 lg:col-auto lg:max-w-[240px] lg:shrink-0">
            <div class="flex items-center gap-2.5">
              <span class="h-8 w-8 shrink-0 overflow-hidden rounded-lg shadow-sm">
                <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
              </span>
              <span class="text-sm font-semibold text-gray-950 dark:text-white">{{ siteName }}</span>
            </div>
            <p class="mt-4 text-sm text-gray-500 dark:text-dark-400">
              &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
            </p>
            <p
              v-for="(line, index) in footerTextLines"
              :key="index"
              class="mt-1 text-xs text-gray-400 dark:text-dark-500"
            >
              {{ line }}
            </p>
          </div>

          <!-- Link columns -->
          <div v-for="column in footerColumns" :key="column.title" class="lg:min-w-[140px]">
            <h3 class="text-sm font-semibold text-gray-950 dark:text-white">{{ column.title }}</h3>
            <ul class="mt-4 space-y-2.5">
              <li v-for="link in column.links" :key="link.label">
                <router-link
                  v-if="link.url.startsWith('/')"
                  :to="link.url"
                  class="text-sm text-gray-500 transition hover:text-gray-950 dark:text-dark-400 dark:hover:text-white"
                >
                  {{ link.label }}
                </router-link>
                <a
                  v-else
                  :href="link.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="text-sm text-gray-500 transition hover:text-gray-950 dark:text-dark-400 dark:hover:text-white"
                >
                  {{ link.label }}
                </a>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import GitHubMark from '@/components/auth/GitHubMark.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { useTheme } from '@/composables/useTheme'
import { getMarketplaceModels, getMarketplaceStats } from '@/api/marketplace'
import type { MarketplaceGroup, MarketplaceStats } from '@/types'
import { sanitizeUrl } from '@/utils/url'
import {
  providerBrandDisplayName,
  providerBrandFilterKey,
  resolveProviderBrand,
  resolveProviderBrandKey,
} from '@/utils/providerBrand'

type HomeStatsKey = 'today-tokens' | 'total-tokens' | 'total-users' | 'supported-models'
type HomeStatsIcon = 'bolt' | 'database' | 'users' | 'grid'
type HomeStepIcon = 'userPlus' | 'grid' | 'key'
type HomeStatFormat = 'compact' | 'number'

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
  key: HomeStatsKey
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
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
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

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

const currentYear = computed(() => new Date().getFullYear())

// 底栏:管理员配置的链接分组 + 内置"快速链接"列;附加文本按行渲染
const footerTextLines = computed<string[]>(() => {
  const raw = appStore.cachedPublicSettings?.footer_text || ''
  return raw.split('\n').map(line => line.trim()).filter(Boolean)
})

const footerColumns = computed(() => {
  const configured = (appStore.cachedPublicSettings?.footer_links || [])
    .filter(group => group.title && Array.isArray(group.links) && group.links.length > 0)
    .map(group => ({
      title: group.title,
      links: group.links.filter(link => link.label && link.url),
    }))
    .filter(group => group.links.length > 0)

  // 管理员已配置分组时以配置为准;未配置时回退到内置"快速链接"列
  if (configured.length > 0) {
    return configured
  }

  const quickLinks: Array<{ label: string; url: string }> = [
    { label: t('home.nav.models'), url: '/models' },
    { label: t('keyUsage.title'), url: '/key-usage' },
  ]
  if (docUrl.value) {
    quickLinks.push({ label: t('home.docs'), url: docUrl.value })
  }

  return [{ title: t('home.footer.quickLinks'), links: quickLinks }]
})

// 服务商图标滚动条:图标列表复制一份实现无缝循环
const homeMarqueeBrands = computed(() => {
  const brands = homeProviderVisuals.value.slice(0, 20)
  return [...brands, ...brands]
})

const marketplaceGroups = ref<MarketplaceGroup[]>([])
const homeStats = ref<MarketplaceStats | null>(null)
const homeMarketplaceLoading = ref(true)
const homeStatsLoading = ref(true)
const homeMarketplaceError = ref(false)
const homeStatsError = ref(false)
const homeMarketplaceButtonIconIndex = ref(0)
let homeMarketplaceButtonIconTimer: number | null = null
const homeAnimatedStats = ref<Record<HomeStatsKey, number>>({
  'today-tokens': 0,
  'total-tokens': 0,
  'total-users': 0,
  'supported-models': 0,
})
const homeAnimatedStatKeys = new Set<HomeStatsKey>()
const homeStatAnimationFrames = new Map<HomeStatsKey, number>()
const homeStatAnimationDurationMs = 3200

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

const homeStatAnimationTargets = computed<Record<HomeStatsKey, number | null>>(() => ({
  'today-tokens': homeStatsLoading.value ? null : normalizedHomeStatTarget(homeStats.value?.today_tokens),
  'total-tokens': homeStatsLoading.value ? null : normalizedHomeStatTarget(homeStats.value?.total_tokens),
  'total-users': homeStatsLoading.value ? null : normalizedHomeStatTarget(homeStats.value?.total_users),
  'supported-models': homeMarketplaceLoading.value ? null : normalizedHomeStatTarget(totalModelCount.value),
}))

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
    value: formatAnimatedHomeStat('today-tokens', homeStatAnimationTargets.value['today-tokens'], homeStatsLoading.value),
    icon: 'bolt',
    iconWrapClass: 'bg-sky-100 dark:bg-sky-500/15',
    iconClass: 'text-sky-600 dark:text-sky-300',
  },
  {
    key: 'total-tokens',
    label: t('home.stats.totalTokens'),
    value: formatAnimatedHomeStat('total-tokens', homeStatAnimationTargets.value['total-tokens'], homeStatsLoading.value),
    icon: 'database',
    iconWrapClass: 'bg-emerald-100 dark:bg-emerald-500/15',
    iconClass: 'text-emerald-600 dark:text-emerald-300',
  },
  {
    key: 'total-users',
    label: t('home.stats.totalUsers'),
    value: formatAnimatedHomeStat('total-users', homeStatAnimationTargets.value['total-users'], homeStatsLoading.value, 'number'),
    icon: 'users',
    iconWrapClass: 'bg-violet-100 dark:bg-violet-500/15',
    iconClass: 'text-violet-600 dark:text-violet-300',
  },
  {
    key: 'supported-models',
    label: t('home.stats.supportedModels'),
    value: formatAnimatedHomeStat('supported-models', homeStatAnimationTargets.value['supported-models'], homeMarketplaceLoading.value, 'number'),
    icon: 'grid',
    iconWrapClass: 'bg-primary-100 dark:bg-primary-500/15',
    iconClass: 'text-primary-600 dark:text-primary-300',
  },
])

const homeProviderVisuals = computed(() => {
  const brands = supportedProviders.value.map(provider => provider.iconBrand)
  return mergeProviderVisualBrands(brands)
})

const homeMarketplaceButtonBrands = computed(() => supportedProviders.value.map(provider => provider.iconBrand))

const homeMarketplaceButtonBrand = computed(() => {
  const brands = homeMarketplaceButtonBrands.value
  if (brands.length === 0) {
    return ''
  }
  return brands[homeMarketplaceButtonIconIndex.value % brands.length]
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

function formatAnimatedHomeStat(
  key: HomeStatsKey,
  target: number | null,
  loading: boolean,
  format: HomeStatFormat = 'compact'
): string {
  if (loading) {
    return '...'
  }
  if (target === null) {
    return '-'
  }

  const value = homeAnimatedStats.value[key]
  const formatted = format === 'compact'
    ? formatAnimatedCompactNumber(value, target)
    : formatWholeNumber(value)
  return `${formatted}+`
}

function formatMarketplaceStat(value: number): string {
  if (homeMarketplaceLoading.value) {
    return '...'
  }
  return new Intl.NumberFormat(numberLocale.value).format(value)
}

function formatAnimatedCompactNumber(value: number, target: number): string {
  const targetParts = new Intl.NumberFormat(numberLocale.value, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).formatToParts(target)
  const compactPart = targetParts.find(part => part.type === 'compact')?.value ?? ''
  const scaledValue = compactPart ? scaleCompactValue(value, target) : value
  const decimalDigits = compactPart && targetParts.some(part => part.type === 'fraction') ? 1 : 0
  const numberText = new Intl.NumberFormat(numberLocale.value, {
    minimumFractionDigits: decimalDigits,
    maximumFractionDigits: decimalDigits,
    useGrouping: false,
  }).format(scaledValue)

  return `${numberText}${compactPart}`
}

function scaleCompactValue(value: number, target: number): number {
  const compactTargetNumber = Number(new Intl.NumberFormat(numberLocale.value, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).formatToParts(target)
    .filter(part => part.type === 'integer' || part.type === 'decimal' || part.type === 'fraction')
    .map(part => part.value)
    .join(''))
  if (!Number.isFinite(compactTargetNumber) || compactTargetNumber <= 0) {
    return value
  }

  return value / (target / compactTargetNumber)
}

function formatWholeNumber(value: number): string {
  return new Intl.NumberFormat(numberLocale.value, {
    maximumFractionDigits: 0,
    useGrouping: false,
  }).format(Math.round(value))
}

function normalizedHomeStatTarget(value?: number): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, value) : null
}

function startHomeStatAnimation(key: HomeStatsKey, target: number) {
  homeAnimatedStatKeys.add(key)
  if (homeStatAnimationFrames.has(key)) {
    cancelAnimationFrame(homeStatAnimationFrames.get(key)!)
    homeStatAnimationFrames.delete(key)
  }

  const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  if (reduceMotion || target === 0) {
    homeAnimatedStats.value = { ...homeAnimatedStats.value, [key]: target }
    return
  }

  const startTime = performance.now()
  const tick = (now: number) => {
    const progress = Math.min((now - startTime) / homeStatAnimationDurationMs, 1)
    // 四次缓出配合更长时长，让计数器前段启动快，尾段明显慢下来。
    const easedProgress = 1 - Math.pow(1 - progress, 4)
    homeAnimatedStats.value = {
      ...homeAnimatedStats.value,
      [key]: target * easedProgress,
    }

    if (progress < 1) {
      homeStatAnimationFrames.set(key, requestAnimationFrame(tick))
      return
    }

    homeAnimatedStats.value = { ...homeAnimatedStats.value, [key]: target }
    homeStatAnimationFrames.delete(key)
  }

  homeStatAnimationFrames.set(key, requestAnimationFrame(tick))
}

watch(
  homeStatAnimationTargets,
  (targets) => {
    const statEntries = Object.entries(targets) as Array<[HomeStatsKey, number | null]>
    statEntries.forEach(([key, target]) => {
      if (target === null || homeAnimatedStatKeys.has(key)) {
        return
      }
      startHomeStatAnimation(key, target)
    })
  },
  { immediate: true }
)

watch(
  homeMarketplaceButtonBrands,
  (brands) => {
    if (brands.length === 0) {
      homeMarketplaceButtonIconIndex.value = 0
      return
    }
    homeMarketplaceButtonIconIndex.value %= brands.length
  },
  { immediate: true }
)

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
  homeMarketplaceButtonIconTimer = window.setInterval(() => {
    if (homeMarketplaceButtonBrands.value.length <= 1) {
      return
    }
    homeMarketplaceButtonIconIndex.value += 1
  }, 1800)

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

onUnmounted(() => {
  homeStatAnimationFrames.forEach(frameId => cancelAnimationFrame(frameId))
  homeStatAnimationFrames.clear()
  if (homeMarketplaceButtonIconTimer) {
    window.clearInterval(homeMarketplaceButtonIconTimer)
    homeMarketplaceButtonIconTimer = null
  }
})
</script>

<style scoped>
.home-marketplace-icon-enter-active,
.home-marketplace-icon-leave-active {
  transition: opacity 220ms ease, transform 220ms ease;
}

.home-marketplace-icon-enter-from {
  opacity: 0;
  transform: translateY(-70%);
}

.home-marketplace-icon-leave-to {
  opacity: 0;
  transform: translateY(70%);
}

/* 服务商图标无缝滚动条,两端用渐隐遮罩 */
.home-marquee {
  -webkit-mask-image: linear-gradient(to right, transparent, black 12%, black 88%, transparent);
  mask-image: linear-gradient(to right, transparent, black 12%, black 88%, transparent);
}

.home-marquee-track {
  animation: home-marquee-scroll 48s linear infinite;
}

.home-marquee:hover .home-marquee-track {
  animation-play-state: paused;
}

@keyframes home-marquee-scroll {
  0% {
    transform: translateX(0);
  }
  100% {
    transform: translateX(-50%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .home-marquee-track {
    animation: none;
  }
}
</style>
