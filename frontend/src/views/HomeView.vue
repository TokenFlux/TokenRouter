<template>
  <GoogleOneTap :enabled="googleOneTapEligible" :client-id="googleOneTapClientID" />

  <!-- Configured home content replaces the default landing page while retaining the independent One Tap control. -->
  <div v-if="homeContent" class="min-h-screen">
    <!-- HTTP(S) content is embedded directly as the configured full-page destination. -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- Administrator-configured HTML is inserted directly; this rendering path performs no additional HTML sanitization. -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- The built-in page supplies marketplace summaries when no custom home content is configured. -->
  <div
    v-else
    class="ba-theme-shell relative flex min-h-screen flex-col overflow-hidden pt-[var(--header-h)] text-gray-950 dark:text-white"
  >
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

    <!-- Share the application header while reserving its fixed height in the public page shell. -->
    <AppHeader public-page />

    <main
      v-content-reveal="motionRoute?.path"
      class="relative z-10 flex-1 px-4 pb-20 pt-16 sm:px-6 lg:px-8"
    >
      <section class="mx-auto max-w-5xl text-center">
        <h1
          class="mx-auto max-w-4xl text-4xl font-bold leading-[1.05] tracking-tight text-gray-950 dark:text-white sm:text-5xl md:text-6xl lg:text-7xl"
        >
          {{ homeHeroTitle }}
        </h1>
        <p class="mx-auto mt-6 max-w-2xl text-lg leading-8 text-gray-600 dark:text-dark-300">
          {{ homeHeroSubtitle }}
        </p>

        <div class="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-control bg-primary-600 px-6 py-3 text-sm font-semibold text-white shadow-none transition hover:bg-primary-700"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            <Icon name="arrowRight" size="sm" :stroke-width="2" />
          </router-link>
          <router-link
            to="/models"
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-control border border-gray-200 bg-white px-6 py-3 text-sm font-semibold text-gray-900 shadow-sm transition hover:border-black/20 hover:text-primary-600 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 dark:hover:border-primary-500"
          >
            {{ t('home.exploreMarketplace') }}
            <span class="relative flex h-5 w-5 items-center justify-center overflow-hidden">
              <MotionTransition name="home-marketplace-icon" mode="out-in">
                <ProviderIcon
                  v-if="homeMarketplaceButtonBrand"
                  :key="homeMarketplaceButtonBrand"
                  :brand="homeMarketplaceButtonBrand"
                  size="18px"
                />
                <Icon
                  v-else
                  key="marketplace-fallback"
                  name="sparkles"
                  size="sm"
                  class="text-primary-500"
                />
              </MotionTransition>
            </span>
          </router-link>
        </div>
      </section>

      <section class="mx-auto mt-16 grid max-w-4xl grid-cols-2 gap-x-6 gap-y-8 md:grid-cols-4">
        <div v-for="card in homeStatsCards" :key="card.key" class="text-center">
          <p
            class="min-h-[1.1em] text-3xl font-bold tabular-nums tracking-tight text-gray-950 dark:text-white md:text-4xl"
          >
            {{ card.value }}
          </p>
          <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ card.label }}</p>
        </div>
      </section>
      <p v-if="homeStatsError" class="mt-4 text-center text-xs text-gray-500 dark:text-dark-400">
        {{ t('home.stats.unavailable') }}
      </p>

      <!-- Each link covers its card, and flexible text panels align the links along the bottom when descriptions wrap. -->
      <section class="mx-auto mt-20 grid max-w-7xl gap-5 sm:grid-cols-2 xl:grid-cols-4">
        <article
          class="group relative isolate flex flex-col overflow-hidden rounded-surface border border-gray-200 bg-white transition-colors duration-fast hover:border-black/20 focus-within:border-black/20 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600"
        >
          <div
            class="relative h-44 shrink-0 overflow-hidden border-b border-gray-200 bg-[var(--page-bg)] dark:border-dark-800"
          >
            <div
              class="absolute inset-0 transition-transform duration-normal motion-safe:group-hover:scale-105"
            >
              <span
                v-for="icon in homeProviderCloudIcons"
                :key="icon.brand"
                class="absolute flex h-7 w-7 items-center justify-center rounded-full border border-gray-100 bg-white/95 text-gray-700 shadow-[0_5px_16px_rgba(0,0,0,0.13)] ring-1 ring-black/[0.02] dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 dark:ring-white/[0.04]"
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
          </div>
          <div class="flex flex-1 flex-col p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.unifiedGateway') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.unifiedGatewayDesc') }}
            </p>
            <router-link
              to="/models"
              class="mt-auto inline-flex items-center gap-1 self-start pt-4 text-sm font-medium home-feature-link text-link"
            >
              {{ t('home.features.browseAll') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article
          class="group relative isolate flex flex-col overflow-hidden rounded-surface border border-gray-200 bg-white transition-colors duration-fast hover:border-black/20 focus-within:border-black/20 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600"
        >
          <div
            class="relative flex h-44 shrink-0 items-center justify-center overflow-hidden border-b border-gray-200 bg-[var(--page-bg)] dark:border-dark-800"
          >
            <div
              class="relative h-full w-full transition-transform duration-normal motion-safe:group-hover:scale-105"
            >
              <div
                class="absolute left-1/2 top-7 z-10 max-w-[82%] -translate-x-1/2 truncate rounded-control bg-gray-100 px-3.5 py-1.5 text-xs font-medium text-gray-800 shadow-sm dark:bg-dark-900 dark:text-dark-100"
              >
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
              <div
                class="absolute bottom-6 left-1/2 flex w-[190px] -translate-x-1/2 justify-between"
              >
                <span
                  v-for="brand in homeRouteProviderBrands"
                  :key="brand"
                  class="flex h-9 w-9 items-center justify-center rounded-control border border-gray-100 bg-white text-gray-700 shadow-[0_5px_16px_rgba(0,0,0,0.13)] dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100"
                >
                  <ProviderIcon :brand="brand" size="20px" />
                </span>
              </div>
            </div>
          </div>
          <div class="flex flex-1 flex-col p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.multiProvider') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.multiProviderDesc') }}
            </p>
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="mt-auto inline-flex items-center gap-1 self-start pt-4 text-sm font-medium home-feature-link text-link"
            >
              {{ t('home.features.learnMore') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article
          class="group relative isolate flex flex-col overflow-hidden rounded-surface border border-gray-200 bg-white transition-colors duration-fast hover:border-black/20 focus-within:border-black/20 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600"
        >
          <div
            class="flex h-44 shrink-0 items-center justify-center overflow-hidden border-b border-gray-200 bg-[var(--page-bg)] p-6 dark:border-dark-800"
          >
            <div
              class="w-full max-w-[200px] rounded-surface border border-gray-200 bg-white p-4 shadow-sm transition-transform duration-normal motion-safe:group-hover:scale-105 dark:border-dark-700 dark:bg-dark-900"
            >
              <div
                class="mb-4 flex items-center justify-between text-xs text-gray-500 dark:text-dark-400"
              >
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
          <div class="flex flex-1 flex-col p-5">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.balanceQuota') }}
            </h2>
            <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ t('home.features.balanceQuotaDesc') }}
            </p>
            <router-link
              :to="isAuthenticated ? dashboardPath : '/login'"
              class="mt-auto inline-flex items-center gap-1 self-start pt-4 text-sm font-medium home-feature-link text-link"
            >
              {{ t('home.features.viewUsage') }}
              <Icon name="arrowRight" size="xs" />
            </router-link>
          </div>
        </article>

        <article
          class="group relative isolate flex flex-col overflow-hidden rounded-surface border border-gray-200 bg-white transition-colors duration-fast hover:border-black/20 focus-within:border-black/20 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600"
        >
          <div
            class="flex h-44 shrink-0 items-center justify-center overflow-hidden border-b border-gray-200 bg-[var(--page-bg)] dark:border-dark-800"
          >
            <div
              class="relative flex h-24 w-24 items-center justify-center rounded-full border border-gray-200 bg-white shadow-sm transition-transform duration-normal motion-safe:group-hover:scale-105 dark:border-dark-700 dark:bg-dark-900"
            >
              <Icon name="shield" size="xl" class="text-gray-400 dark:text-dark-300" />
              <span
                class="absolute -right-1 -top-1 flex h-9 w-9 items-center justify-center rounded-full bg-emerald-100 text-emerald-600 dark:bg-emerald-500/15 dark:text-emerald-300"
              >
                <Icon name="check" size="md" :stroke-width="2" :animate-on-hover="false" />
              </span>
            </div>
          </div>
          <div class="flex flex-1 flex-col p-5">
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
              class="mt-auto inline-flex items-center gap-1 self-start pt-4 text-sm font-medium home-feature-link text-link"
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
            <router-link
              to="/models"
              class="inline-flex items-center gap-2 text-2xl font-bold text-link"
            >
              {{ t('home.providers.title') }}
              <Icon name="chevronRight" size="md" :animate-on-hover="false" />
            </router-link>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">
              {{ formatMarketplaceStat(totalModelCount) }} {{ t('marketplace.modelsStat') }}
              ·
              {{ formatMarketplaceStat(supportedProviders.length) }}
              {{ t('home.stats.providerTypes') }}
            </p>
          </div>
          <router-link to="/models" class="text-sm font-medium text-link">
            {{ t('home.viewAll') }}
            <Icon name="arrowRight" size="xs" class="inline-block" />
          </router-link>
        </div>

        <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          <div
            v-if="homeMarketplaceLoading"
            class="rounded-surface border border-gray-200 bg-white px-5 py-4 text-center text-sm text-gray-500 dark:border-dark-800 dark:bg-dark-900 dark:text-dark-400 sm:col-span-2 lg:col-span-3"
          >
            {{ t('common.loading') }}
          </div>

          <div
            v-else-if="supportedProviders.length === 0"
            class="rounded-surface border border-gray-200 bg-white px-5 py-4 text-center text-sm text-gray-500 dark:border-dark-800 dark:bg-dark-900 dark:text-dark-400 sm:col-span-2 lg:col-span-3"
          >
            {{ homeMarketplaceError ? t('home.providers.unavailable') : t('home.providers.empty') }}
          </div>

          <!-- Resolved featured-model configuration replaces the fallback provider-category cards in its configured order. -->
          <template v-else-if="featuredModels.length > 0">
            <article
              v-for="featured in featuredModels"
              :key="featured.model.id"
              class="rounded-surface border border-gray-200 bg-white p-6 transition-colors duration-fast hover:border-black/20 focus-within:border-black/20 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600"
            >
              <div class="flex items-start gap-4">
                <!-- Use the same model artwork and framed icon treatment as the marketplace cards. -->
                <span
                  class="flex h-12 w-12 shrink-0 items-center justify-center rounded-surface border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-950"
                >
                  <ModelIcon :model="featured.model.id" size="28px" />
                </span>
                <div class="min-w-0 flex-1">
                  <h3 class="truncate text-lg font-semibold text-gray-950 dark:text-white">
                    {{ featured.model.display_name || featured.model.id }}
                  </h3>
                  <p class="truncate text-sm text-gray-500 dark:text-dark-400">
                    {{
                      t('home.featured.byProvider', {
                        provider: homeProviderCategory(featured.group).label,
                      })
                    }}
                  </p>
                </div>
              </div>
              <!-- Omit the entire discount footer when the group has no valid saving against its official reference price. -->
              <div
                v-if="featured.discountOff"
                class="mt-5 border-t border-gray-200 pt-5 dark:border-dark-800"
              >
                <p class="text-sm font-semibold tabular-nums text-gray-950 dark:text-white">
                  {{ featured.discountOff }}
                </p>
              </div>
            </article>
          </template>

          <template v-else>
            <article
              v-for="provider in supportedProviders.slice(0, 6)"
              :key="provider.key"
              class="rounded-surface border border-gray-200 bg-white p-6 transition-colors duration-fast hover:border-black/20 focus-within:border-black/20 dark:border-dark-800 dark:bg-dark-900 dark:hover:border-dark-600"
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
                    <p class="text-sm text-gray-500 dark:text-dark-400">
                      {{ t('home.providers.modelCount') }}
                    </p>
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
          <article v-for="step in homeSteps" :key="step.key" class="flex min-h-[190px] flex-col">
            <div class="flex items-center gap-3">
              <span
                class="flex h-8 w-8 items-center justify-center rounded-full bg-primary-50 text-base font-semibold text-primary-600 dark:bg-primary-500/10 dark:text-primary-300"
              >
                {{ step.index }}
              </span>
              <h2 class="text-lg font-semibold tracking-tight text-gray-950 dark:text-white">
                {{ step.title }}
              </h2>
            </div>
            <p class="mt-4 max-w-sm text-sm leading-6 text-gray-600 dark:text-dark-300">
              {{ step.description }}
            </p>

            <div v-if="step.key === 'signup'" class="mt-8">
              <div class="flex items-center gap-3 text-primary-500">
                <Icon name="user" size="md" :stroke-width="1.8" />
                <div class="space-y-1.5">
                  <div class="h-1.5 w-7 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                  <div class="h-1.5 w-20 rounded-full bg-primary-100 dark:bg-primary-400/20"></div>
                </div>
              </div>
              <div class="mt-4 grid max-w-[156px] grid-cols-3 gap-3">
                <span
                  class="flex h-10 w-10 items-center justify-center rounded-control bg-white/90 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800"
                >
                  <ProviderIcon brand="Google" size="20px" />
                </span>
                <span
                  class="flex h-10 w-10 items-center justify-center rounded-control bg-white/90 text-gray-800 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-gray-100 dark:ring-dark-800"
                >
                  <GitHubMark class="h-5 w-5" />
                </span>
                <span
                  class="flex h-10 w-10 items-center justify-center rounded-control bg-white/90 text-primary-500 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:ring-dark-800"
                >
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
                <div
                  class="flex items-center gap-2 rounded-control bg-white/90 px-3 py-2 text-gray-700 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-dark-200 dark:ring-dark-800"
                >
                  <span class="w-14 text-xs font-medium">Claude</span>
                  <span
                    class="h-2 flex-1 rounded-full bg-primary-100 dark:bg-primary-400/20"
                  ></span>
                  <span class="h-2 w-12 rounded-full bg-primary-100 dark:bg-primary-400/20"></span>
                </div>
                <div
                  class="flex items-center gap-2 rounded-control bg-white/90 px-3 py-2 text-gray-700 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-dark-200 dark:ring-dark-800"
                >
                  <span class="w-14 text-xs font-medium">GPT</span>
                  <span
                    class="h-2 flex-1 rounded-full bg-primary-100 dark:bg-primary-400/20"
                  ></span>
                  <span class="h-2 w-12 rounded-full bg-primary-100 dark:bg-primary-400/20"></span>
                </div>
              </div>
            </div>

            <div v-else class="mt-8 max-w-[270px]">
              <div class="flex items-center gap-3 text-primary-500">
                <Icon name="key" size="md" :stroke-width="1.8" />
                <div
                  class="flex-1 rounded-control bg-white/90 px-3 py-2 font-mono text-xs text-gray-600 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-dark-300 dark:ring-dark-800"
                >
                  TOKENFLUX_API_KEY
                </div>
              </div>
              <div
                class="mt-3 rounded-control bg-white/90 px-3 py-2 font-mono text-sm tracking-[0.2em] text-gray-950 shadow-sm ring-1 ring-gray-100 dark:bg-dark-950 dark:text-white dark:ring-dark-800"
              >
                ••••••••••••••••
              </div>
            </div>
          </article>
        </div>
      </section>

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
            class="inline-flex min-h-[44px] min-w-[180px] items-center justify-center gap-2 rounded-control bg-primary-600 px-8 py-3 text-sm font-semibold text-white transition hover:bg-primary-700"
          >
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.cta.button') }}
            <Icon name="arrowRight" size="sm" :stroke-width="2" />
          </router-link>
        </div>
      </section>
    </main>

    <!-- The footer shares the page backdrop in both themes, with a border separating it from the main content. -->
    <footer class="relative z-10 border-t border-gray-200 px-6 py-12 dark:border-dark-800">
      <div class="mx-auto max-w-7xl">
        <div
          class="grid grid-cols-2 gap-x-8 gap-y-10 sm:grid-cols-3 lg:flex lg:justify-between lg:gap-8"
        >
          <div class="col-span-2 sm:col-span-3 lg:col-auto lg:max-w-[240px] lg:shrink-0">
            <div class="flex items-center gap-2.5">
              <span class="h-8 w-8 shrink-0 overflow-hidden rounded-control shadow-sm">
                <img
                  :src="siteLogo || '/logo.svg'"
                  alt="Logo"
                  class="h-full w-full object-contain"
                />
              </span>
              <span class="text-sm font-semibold text-gray-950 dark:text-white">{{
                siteName
              }}</span>
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
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { vContentReveal } from '@/directives/contentReveal'
import { useRoute as useMotionRoute } from 'vue-router'

import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import GitHubMark from '@/components/auth/GitHubMark.vue'
import GoogleOneTap from '@/components/auth/GoogleOneTap.vue'
import AppHeader from '@/components/layout/AppHeader.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { getMarketplaceModels, getMarketplaceStats } from '@/api/marketplace'
import type { MarketplaceGroup, MarketplaceModel, MarketplaceStats } from '@/api/marketplace.types'
import { sanitizeUrl } from '@/utils/url'
import { hasAcceptedLoginAgreement } from '@/utils/loginAgreement'
import { isGoogleOneTapEligible, isGoogleOneTapOriginSupported } from '@/utils/googleIdentity'
import {
  providerBrandDisplayName,
  providerBrandFilterKey,
  resolveProviderBrand,
  resolveProviderBrandKey,
} from '@/utils/providerBrand'

// HomeView.vue combines public branding, optional custom home content, and the
// built-in marketplace landing page. Configured HTTP(S) content uses an iframe,
// while configured HTML is inserted directly. The default page groups public
// models by provider brand, resolves configured featured aliases, and displays
// independently loaded usage totals with one initial animation per statistic.
//
// Page setup retains shared account and public-settings initialization. The
// initial marketplace and statistics reads run together only for the default
// page; locale changes separately refresh marketplace records. A tracked interval
// rotates the marketplace action's brand icon, and tracked animation frames update
// statistics. Teardown clears those handles while in-flight API requests retain
// their existing independent completion paths without cancellation or identity guards.

// motionRoute supplies the path used to replay the default page's reveal animation.
const motionRoute = useMotionRoute()

// HomeStatsKey identifies one independently animated statistic in the public landing page.
type HomeStatsKey = 'today-tokens' | 'total-tokens' | 'total-users' | 'supported-models'
// HomeStatsIcon restricts configured statistic icon metadata to the supported icon names.
type HomeStatsIcon = 'bolt' | 'database' | 'users' | 'grid'
// HomeStepIcon identifies the configured icon for a public onboarding step.
type HomeStepIcon = 'userPlus' | 'grid' | 'key'
// HomeStatFormat chooses compact notation or whole-number formatting for animated totals.
type HomeStatFormat = 'compact' | 'number'

// HomeProviderCategory groups marketplace records under one displayed provider identity.
interface HomeProviderCategory {
  key: string // Normalized category identity.
  label: string // Displayed provider category.
  iconBrand: string // Shared artwork brand identifier.
}

// HomeProviderSummary aggregates model entries and display ordering for a provider category.
interface HomeProviderSummary extends HomeProviderCategory {
  modelCount: number // Model entries across contributing groups.
  groupCount: number // Contributing group record count.
  // officialPriceRatio is the smallest valid positive group ratio; omission means
  // no usable official-price comparison was reported for this provider category.
  officialPriceRatio?: number
  sortOrder: number // Minimum configured group order.
  firstIndex: number // First sorted group position.
}

// HomeStatsCard carries the configured label, formatted value, and statistic icon metadata.
interface HomeStatsCard {
  key: HomeStatsKey // Animated statistic identity.
  label: string // Translated statistic caption.
  // value contains formatted statistic text, with loading or missing placeholders
  // displayed until the corresponding animated numeric value becomes available.
  value: string
  icon: HomeStatsIcon // Configured statistic icon identifier.
  iconWrapClass: string // Configured icon wrapper classes.
  iconClass: string // Configured icon foreground classes.
}

// HomeStep describes one numbered instruction in the landing page's introductory sequence.
interface HomeStep {
  key: string // Stable instruction identity.
  index: number // Displayed one-based step number.
  title: string // Translated instruction heading.
  description: string // Translated instruction description.
  icon: HomeStepIcon // Configured instruction icon name.
}

// HomeProviderCloudIcon positions one decorative brand mark in the gateway illustration.
interface HomeProviderCloudIcon {
  brand: string // Shared artwork brand identifier.
  left: string // Horizontal CSS percentage position.
  top: string // Vertical CSS percentage position.
  opacity: number // Icon opacity fraction.
  scale: number // Relative icon scale.
}

// HomeFeaturedModel pairs a public model with its first resolved group and discount.
interface HomeFeaturedModel {
  model: MarketplaceModel // Selected public model record.
  group: MarketplaceGroup // Owning brand and pricing group.
  // Translated savings against the official price; null hides the footer when the ratio is unusable or undiscounted.
  discountOff: string | null
}

// Shared services provide language, account role, and cached public settings for
// the page's branding, navigation destinations, and optional authentication prompt.
const { t, locale } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

// Branding reads injected or cached public settings, filtering logo and documentation
// URLs while retaining explicitly configured title and subtitle overrides, including empty text.
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
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const numberLocale = computed(() => locale.value)
const homeHeroTitle = computed(() =>
  appStore.cachedPublicSettings?.site_text_overrides?.includes('site_title')
    ? appStore.cachedPublicSettings.site_title || ''
    : t('home.heroTitle'),
)
const homeHeroSubtitle = computed(() =>
  appStore.cachedPublicSettings?.site_text_overrides?.includes('site_subtitle')
    ? appStore.cachedPublicSettings.site_subtitle || ''
    : t('home.heroDescription'),
)

// Custom content uses the existing HTTP(S)-prefix decision to choose an iframe;
// every other nonempty value follows the administrator-configured HTML rendering path.
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Authentication and role select the primary navigation destination without
// changing the independently evaluated eligibility for the Google One Tap prompt.
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))

// One Tap eligibility combines loaded public flags, account state, consent for
// the current agreement revision, captcha policy, client identity, and origin support.
const googleOneTapClientID = computed(
  () => appStore.cachedPublicSettings?.google_oauth_client_id || '',
)
const googleOneTapEligible = computed(() => {
  const settings = appStore.cachedPublicSettings
  if (!settings) return false
  const agreementEnabled = settings.login_agreement_enabled === true
  return isGoogleOneTapEligible({
    publicSettingsLoaded: appStore.publicSettingsLoaded,
    isAuthenticated: isAuthenticated.value,
    oneTapEnabled: settings.google_one_tap_enabled === true,
    clientID: googleOneTapClientID.value,
    backendModeEnabled: settings.backend_mode_enabled,
    tencentCaptchaEnabled: settings.tencent_captcha_enabled === true,
    aliyunCaptchaEnabled: settings.aliyun_captcha_enabled === true,
    loginAgreementEnabled: agreementEnabled,
    loginAgreementAccepted:
      !agreementEnabled || hasAcceptedLoginAgreement(settings.login_agreement_revision || ''),
    originSupported: isGoogleOneTapOriginSupported(),
  })
})

// Copyright text uses a lazily evaluated year snapshot; no clock dependency
// forces it to recompute while this page remains mounted across a year boundary.
const currentYear = computed(() => new Date().getFullYear())

// Footer text discards empty trimmed lines, and link columns prefer nonempty
// configured groups over the built-in quick links. Configured URLs remain unchanged.
const footerTextLines = computed<string[]>(() => {
  const raw = appStore.cachedPublicSettings?.footer_text || ''
  return raw
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
})

const footerColumns = computed(() => {
  const configured = (appStore.cachedPublicSettings?.footer_links || [])
    .filter((group) => group.title && Array.isArray(group.links) && group.links.length > 0)
    .map((group) => ({
      title: group.title,
      links: group.links.filter((link) => link.label && link.url),
    }))
    .filter((group) => group.links.length > 0)

  // A usable configured group replaces the built-in quick-link column; incomplete
  // groups and links are filtered before this decision uses their remaining count.
  if (configured.length > 0) {
    return configured
  }

  const quickLinks: Array<{
    label: string // Translated quick-link label.
    url: string // Internal or filtered documentation URL.
  }> = [
    { label: t('home.nav.models'), url: '/models' },
    { label: t('keyUsage.title'), url: '/key-usage' },
  ]
  if (docUrl.value) {
    quickLinks.push({ label: t('home.docs'), url: docUrl.value })
  }

  return [{ title: t('home.footer.quickLinks'), links: quickLinks }]
})

// Marketplace and aggregate statistics keep independent loading/error states so
// one failed read can clear its content without discarding the other's result.
const marketplaceGroups = ref<MarketplaceGroup[]>([])
const homeStats = ref<MarketplaceStats | null>(null)
const homeMarketplaceLoading = ref(true)
const homeStatsLoading = ref(true)
const homeMarketplaceError = ref(false)
const homeStatsError = ref(false)
// The marketplace action cycles through loaded provider brands using one tracked
// interval and a bounded display index; empty lists use the template fallback icon.
const homeMarketplaceButtonIconIndex = ref(0)
let homeMarketplaceButtonIconTimer: number | null = null
// Statistic values, started keys, and active frame handles coordinate one initial
// count-up per key; teardown cancels frames still present in the handle map.
const homeAnimatedStats = ref<Record<HomeStatsKey, number>>({
  'today-tokens': 0,
  'total-tokens': 0,
  'total-users': 0,
  'supported-models': 0,
})
const homeAnimatedStatKeys = new Set<HomeStatsKey>()
const homeStatAnimationFrames = new Map<HomeStatsKey, number>()
const homeStatAnimationDurationMs = 3200

// homeProviderCloudIcons places a different brand in each slot and distributes warm,
// cool, and neutral marks across rows. Fixed positions and brands keep the illustration
// consistent when the marketplace loads or its group order changes.
const homeProviderCloudIcons: readonly HomeProviderCloudIcon[] = [
  { brand: 'Mistral', left: '7%', top: '13%', opacity: 0.72, scale: 0.94 },
  { brand: 'Meta', left: '25%', top: '13%', opacity: 0.8, scale: 0.94 },
  { brand: 'MiniMax', left: '43%', top: '13%', opacity: 0.9, scale: 1 },
  { brand: 'Moonshot', left: '62%', top: '13%', opacity: 0.8, scale: 0.94 },
  { brand: 'Cohere', left: '81%', top: '13%', opacity: 0.72, scale: 0.94 },
  { brand: 'DeepSeek', left: '17%', top: '35%', opacity: 0.86, scale: 0.98 },
  { brand: 'Anthropic', left: '39%', top: '35%', opacity: 0.92, scale: 1 },
  { brand: 'Perplexity', left: '61%', top: '35%', opacity: 0.96, scale: 1.04 },
  { brand: 'xAI', left: '83%', top: '35%', opacity: 0.9, scale: 0.98 },
  { brand: 'Cloudflare', left: '7%', top: '57%', opacity: 0.82, scale: 0.94 },
  { brand: 'Google', left: '25%', top: '57%', opacity: 0.9, scale: 0.98 },
  { brand: 'OpenAI', left: '43%', top: '57%', opacity: 1, scale: 1.06 },
  { brand: 'Alibaba', left: '62%', top: '57%', opacity: 0.9, scale: 0.98 },
  { brand: 'OpenCode', left: '81%', top: '57%', opacity: 0.82, scale: 0.94 },
  { brand: 'Doubao', left: '17%', top: '79%', opacity: 0.72, scale: 0.94 },
  { brand: 'Xiaomi', left: '39%', top: '79%', opacity: 0.78, scale: 0.96 },
  { brand: 'TypeSafe', left: '61%', top: '79%', opacity: 0.82, scale: 0.98 },
  { brand: 'OpenRouter', left: '83%', top: '79%', opacity: 0.78, scale: 0.96 },
  { brand: 'Tencent', left: '7%', top: '98%', opacity: 0.6, scale: 0.9 },
  { brand: 'Suno', left: '25%', top: '98%', opacity: 0.66, scale: 0.92 },
  { brand: 'Zhipu', left: '43%', top: '98%', opacity: 0.7, scale: 0.94 },
  { brand: 'ZeroOne', left: '62%', top: '98%', opacity: 0.66, scale: 0.92 },
  { brand: 'Baidu', left: '81%', top: '98%', opacity: 0.6, scale: 0.9 },
  { brand: 'iFlytek', left: '99%', top: '98%', opacity: 0.48, scale: 0.86 },
]

// homeRouteLabel and homeRouteProviderBrands supply the example route illustration.
// The three destinations use violet, neutral, and orange marks in a fixed order.
const homeRouteLabel = 'OpenAI/GPT-5.4'
const homeRouteProviderBrands = ['OpenRouter', 'OpenAI', 'Cloudflare']

// totalModelCount counts model entries across groups, including aliases repeated
// in separate groups; it supplies both the summary and supported-model animation target.
const totalModelCount = computed(() =>
  marketplaceGroups.value.reduce((total, group) => total + group.models.length, 0),
)

// homeFeaturedModelIds reads the configured display order from public settings;
// an absent or non-array value leaves the page using provider-category cards.
const homeFeaturedModelIds = computed<string[]>(() => {
  const configured = appStore.cachedPublicSettings?.home_featured_models
  return Array.isArray(configured) ? configured : []
})

// featuredModels resolves configured aliases in order and takes the first matching
// marketplace group for each alias. Unresolved identifiers contribute no featured card.
const featuredModels = computed<HomeFeaturedModel[]>(() => {
  const resolved: HomeFeaturedModel[] = []
  for (const modelId of homeFeaturedModelIds.value) {
    for (const group of marketplaceGroups.value) {
      const model = group.models.find((item) => item.id === modelId)
      if (model) {
        resolved.push({
          model,
          group,
          discountOff: formatFeaturedDiscountOff(group.official_price_ratio),
        })
        break
      }
    }
  }
  return resolved
})

// Animation targets remain null during their owning load and normalize invalid
// values afterward; the watcher starts each valid key only once per mounted page.
const homeStatAnimationTargets = computed<Record<HomeStatsKey, number | null>>(() => ({
  'today-tokens': homeStatsLoading.value
    ? null
    : normalizedHomeStatTarget(homeStats.value?.today_tokens),
  'total-tokens': homeStatsLoading.value
    ? null
    : normalizedHomeStatTarget(homeStats.value?.total_tokens),
  'total-users': homeStatsLoading.value
    ? null
    : normalizedHomeStatTarget(homeStats.value?.total_users),
  'supported-models': homeMarketplaceLoading.value
    ? null
    : normalizedHomeStatTarget(totalModelCount.value),
}))

// supportedProviders groups nonempty marketplace records by resolved brand category.
// It preserves the smallest positive official ratio and earliest configured position,
// then applies category priority before group ordering for the resulting public cards.
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

// Statistic cards combine translated captions and animated values with their
// existing icon metadata, using whole-number formatting for users and supported model entries.
const homeStatsCards = computed<HomeStatsCard[]>(() => [
  {
    key: 'today-tokens',
    label: t('home.stats.todayTokens'),
    value: formatAnimatedHomeStat(
      'today-tokens',
      homeStatAnimationTargets.value['today-tokens'],
      homeStatsLoading.value,
    ),
    icon: 'bolt',
    iconWrapClass: 'bg-sky-100 dark:bg-sky-500/15',
    iconClass: 'text-sky-600 dark:text-sky-300',
  },
  {
    key: 'total-tokens',
    label: t('home.stats.totalTokens'),
    value: formatAnimatedHomeStat(
      'total-tokens',
      homeStatAnimationTargets.value['total-tokens'],
      homeStatsLoading.value,
    ),
    icon: 'database',
    iconWrapClass: 'bg-emerald-100 dark:bg-emerald-500/15',
    iconClass: 'text-emerald-600 dark:text-emerald-300',
  },
  {
    key: 'total-users',
    label: t('home.stats.totalUsers'),
    value: formatAnimatedHomeStat(
      'total-users',
      homeStatAnimationTargets.value['total-users'],
      homeStatsLoading.value,
      'number',
    ),
    icon: 'users',
    iconWrapClass: 'bg-violet-100 dark:bg-violet-500/15',
    iconClass: 'text-violet-600 dark:text-violet-300',
  },
  {
    key: 'supported-models',
    label: t('home.stats.supportedModels'),
    value: formatAnimatedHomeStat(
      'supported-models',
      homeStatAnimationTargets.value['supported-models'],
      homeMarketplaceLoading.value,
      'number',
    ),
    icon: 'grid',
    iconWrapClass: 'bg-primary-100 dark:bg-primary-500/15',
    iconClass: 'text-primary-600 dark:text-primary-300',
  },
])

// The marketplace action rotates through the provider categories present in the catalog.
const homeMarketplaceButtonBrands = computed(() =>
  supportedProviders.value.map((provider) => provider.iconBrand),
)

const homeMarketplaceButtonBrand = computed(() => {
  const brands = homeMarketplaceButtonBrands.value
  if (brands.length === 0) {
    return ''
  }
  return brands[homeMarketplaceButtonIconIndex.value % brands.length]
})

// Introductory steps keep their fixed signup, browsing, and API-key sequence while
// translating each heading and description for the active interface language.
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

// homeProviderCategory chooses an explicit display brand before the trimmed group name.
// Known brands use the shared category mapping. Unknown brands retain their selected
// label and derive a filter key without discarding the group's visible identity.
function homeProviderCategory(group: MarketplaceGroup): HomeProviderCategory {
  const brandSource = group.display_brand?.trim() || group.name.trim()
  const brandKey = resolveProviderBrandKey(brandSource)
  if (brandKey && brandKey !== 'unknown') {
    return homeProviderCategoryFromBrand(brandKey, brandSource)
  }

  const fallbackLabel = brandSource
  return {
    key: providerBrandFilterKey(fallbackLabel),
    label: fallbackLabel,
    iconBrand: fallbackLabel,
  }
}

// homeProviderCategoryFromBrand maps core provider brands to localized home categories.
// Other recognized brands retain the shared display name and brand key, allowing
// the same aggregation path to cover providers outside the three core categories.
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

// homeProviderPriority ranks preferred categories before all remaining provider identities.
function homeProviderPriority(key: string): number {
  const priorities = ['claude', 'gpt', 'deepseek', 'gemini', 'antigravity']
  const index = priorities.indexOf(key)
  return index === -1 ? priorities.length : index
}

// validOfficialPriceRatio returns a finite positive ratio for official-price comparisons.
// Missing, nonnumeric, nonfinite, and nonpositive values return null, allowing
// provider summaries and featured cards to omit unusable comparisons.
function validOfficialPriceRatio(value?: number): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null
}

// formatOfficialPriceRatio translates the ratio expressed on the existing ten-point scale.
function formatOfficialPriceRatio(ratio: number): string {
  const discount = new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(ratio * 10)

  return t('marketplace.officialPriceDiscount', { discount })
}

// formatFeaturedDiscountOff formats savings only for a positive ratio below one.
// Invalid or undiscounted comparisons return null so the feature card omits its
// discount footer. Percentage formatting uses the browser's default number locale.
function formatFeaturedDiscountOff(ratio?: number): string | null {
  const valid = validOfficialPriceRatio(ratio)
  if (valid === null || valid >= 1) {
    return null
  }
  const percent = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
  }).format((1 - valid) * 100)
  return t('home.featured.discountOff', { percent })
}

// formatAnimatedHomeStat formats the current animated value for a prepared target.
// Loading and invalid targets produce distinct placeholders. Available targets use
// compact or whole-number output and retain the existing trailing plus marker.
function formatAnimatedHomeStat(
  key: HomeStatsKey,
  target: number | null,
  loading: boolean,
  format: HomeStatFormat = 'compact',
): string {
  if (loading) {
    return '...'
  }
  if (target === null) {
    return '-'
  }

  const value = homeAnimatedStats.value[key]
  const formatted =
    format === 'compact' ? formatAnimatedCompactNumber(value, target) : formatWholeNumber(value)
  return `${formatted}+`
}

// formatMarketplaceStat formats a marketplace count using the active number locale.
// While the marketplace request is loading, it returns the three-dot placeholder
// regardless of the supplied count.
function formatMarketplaceStat(value: number): string {
  if (homeMarketplaceLoading.value) {
    return '...'
  }
  return new Intl.NumberFormat(numberLocale.value).format(value)
}

// formatAnimatedCompactNumber formats an animation value using its target's compact
// suffix and precision. The caller supplies the final target alongside the current
// frame value so crossing a compact-number threshold does not change the suffix
// during the count-up. A target containing a compact suffix delegates scaling to
// scaleCompactValue; otherwise the frame value stays unscaled. Fractional digits
// appear only when that compact target has a fractional part, keeping the displayed
// precision consistent throughout the animation.
function formatAnimatedCompactNumber(value: number, target: number): string {
  const targetParts = new Intl.NumberFormat(numberLocale.value, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).formatToParts(target)
  const compactPart = targetParts.find((part) => part.type === 'compact')?.value ?? ''
  const scaledValue = compactPart ? scaleCompactValue(value, target) : value
  const decimalDigits = compactPart && targetParts.some((part) => part.type === 'fraction') ? 1 : 0
  const numberText = new Intl.NumberFormat(numberLocale.value, {
    minimumFractionDigits: decimalDigits,
    maximumFractionDigits: decimalDigits,
    useGrouping: false,
  }).format(scaledValue)

  return `${numberText}${compactPart}`
}

// scaleCompactValue derives a scale from the target's compact numeric parts.
// The existing conversion uses Number on concatenated locale parts. Nonfinite or
// nonpositive parsed targets return the original value instead of applying a scale.
function scaleCompactValue(value: number, target: number): number {
  const compactTargetNumber = Number(
    new Intl.NumberFormat(numberLocale.value, {
      notation: 'compact',
      maximumFractionDigits: 1,
    })
      .formatToParts(target)
      .filter(
        (part) => part.type === 'integer' || part.type === 'decimal' || part.type === 'fraction',
      )
      .map((part) => part.value)
      .join(''),
  )
  if (!Number.isFinite(compactTargetNumber) || compactTargetNumber <= 0) {
    return value
  }

  return value / (target / compactTargetNumber)
}

// formatWholeNumber rounds the animated value and formats it without grouping separators.
function formatWholeNumber(value: number): string {
  return new Intl.NumberFormat(numberLocale.value, {
    maximumFractionDigits: 0,
    useGrouping: false,
  }).format(Math.round(value))
}

// normalizedHomeStatTarget converts finite values into nonnegative animation targets.
// Negative numbers become zero; missing, nonnumeric, and nonfinite values return
// null so their counters retain the existing unavailable-value display.
function normalizedHomeStatTarget(value?: number): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, value) : null
}

// startHomeStatAnimation starts or replaces the tracked count-up for one statistic.
// The key is marked started before any frame is scheduled, and a previous frame
// for that key is cancelled. Reduced-motion preferences and a zero target publish
// the final value immediately without creating another animation-frame handle.
//
// Otherwise each frame applies the configured easing from zero toward the captured
// target and replaces its own map entry while more frames are needed. Completion
// publishes the exact target and removes that entry. The target watcher admits
// only keys that have not started, and component teardown cancels the remaining
// registered handles without resetting the started-key set.
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
  // tick updates one statistic against the captured start time and target.
  // It records the next frame handle until progress reaches one, then publishes
  // the exact target and removes its handle from teardown ownership.
  const tick = (now: number) => {
    const progress = Math.min((now - startTime) / homeStatAnimationDurationMs, 1)
    // Quartic easing advances quickly at first and slows near the target,
    // while the shared duration keeps all admitted statistics on the same timing scale.
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

// Admit each statistic once when its first valid target arrives; later target
// changes retain the existing started-key behavior instead of restarting its count-up.
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
  { immediate: true },
)

// Keep the rotating action index valid as available brands change, resetting it
// when no catalog brands remain and otherwise reducing it to the current list length.
watch(
  homeMarketplaceButtonBrands,
  (brands) => {
    if (brands.length === 0) {
      homeMarketplaceButtonIconIndex.value = 0
      return
    }
    homeMarketplaceButtonIconIndex.value %= brands.length
  },
  { immediate: true },
)

// providerIconWrapClass applies the home-specific Antigravity palette before shared brand styling.
function providerIconWrapClass(provider: Pick<HomeProviderSummary, 'key' | 'iconBrand'>): string {
  if (provider.key === 'antigravity') {
    return 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-500/15 dark:text-rose-200 dark:ring-rose-400/30'
  }
  return resolveProviderBrand(provider.iconBrand).iconWrapClass
}

// fetchHomeMarketplace replaces public groups and reports its own loading outcome.
// Failure clears the previous groups and exposes the unavailable state. Concurrent
// calls retain completion-order publication without a generation guard or cancellation signal.
async function fetchHomeMarketplace() {
  homeMarketplaceLoading.value = true
  homeMarketplaceError.value = false

  try {
    marketplaceGroups.value = await getMarketplaceModels()
  } catch (error) {
    // Clear stale groups and mark this source unavailable so provider cards and
    // model totals reflect the failed refresh while independently loaded statistics remain.
    console.error('Failed to load home marketplace models:', error)
    marketplaceGroups.value = []
    homeMarketplaceError.value = true
  } finally {
    homeMarketplaceLoading.value = false
  }
}

// fetchHomeStats loads aggregate public counters independently from marketplace groups.
// Failure clears the prior statistics and marks their unavailable state. The
// loading flag is released by each request's completion without coordinating overlapping calls.
async function fetchHomeStats() {
  homeStatsLoading.value = true
  homeStatsError.value = false

  try {
    homeStats.value = await getMarketplaceStats()
  } catch (error) {
    // Clear stale totals and mark statistics unavailable so the cards show their
    // error state while a successful marketplace response can still populate provider data.
    console.error('Failed to load home marketplace stats:', error)
    homeStats.value = null
    homeStatsError.value = true
  } finally {
    homeStatsLoading.value = false
  }
}

// Start the tracked icon rotation before awaiting public settings, then request
// both default-page data sources together only when custom home content is absent.
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
      // Continue with the settings already available after reporting the failed
      // refresh, allowing the default page's independent data requests to proceed.
      console.error('Failed to load public settings:', error)
    }
  }

  if (!homeContent.value) {
    await Promise.all([fetchHomeMarketplace(), fetchHomeStats()])
  }
})

// Cancel every tracked count-up frame and the icon interval using their current
// handles; API requests retain their independent completion behavior after teardown.
onUnmounted(() => {
  homeStatAnimationFrames.forEach((frameId) => cancelAnimationFrame(frameId))
  homeStatAnimationFrames.clear()
  if (homeMarketplaceButtonIconTimer) {
    window.clearInterval(homeMarketplaceButtonIconTimer)
    homeMarketplaceButtonIconTimer = null
  }
})
// Locale refresh reloads public model descriptions through the shared subscription;
// this callback retains its existing independence from the custom-home-content decision.
useLocaleRefresh(fetchHomeMarketplace)
</script>

<style scoped>
/* Stretch the native link over the illustration and draw its focus ring inside
   the clipped card. The text remains the link's accessible label. */
.home-feature-link::after {
  @apply absolute inset-0 z-20 content-[''];
}

.home-feature-link:focus-visible::after {
  @apply ring-2 ring-inset ring-primary-600 dark:ring-primary-500;
}

.home-marketplace-icon-enter-active,
.home-marketplace-icon-leave-active {
  transition:
    opacity var(--motion-layout) var(--motion-ease),
    transform var(--motion-layout) var(--motion-ease);
}

.home-marketplace-icon-enter-from {
  opacity: 0;
  transform: translateY(-70%);
}

.home-marketplace-icon-leave-to {
  opacity: 0;
  transform: translateY(70%);
}
</style>
