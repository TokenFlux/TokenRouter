<template>
  <AppLayout>
    <div class="space-y-4">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <h1 class="page-title">{{ t('admin.riskControl.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
            t('admin.riskControl.description')
          }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary shrink-0 btn-icon"
            :disabled="loading || statusLoading"
            :title="t('admin.riskControl.refreshStatus')"
            @click="loadStatus(false)"
          >
            <Icon name="refresh" size="sm" :class="statusLoading ? 'animate-spin' : ''" />
          </button>
          <button
            type="button"
            class="btn btn-primary inline-flex items-center gap-2"
            :disabled="loading"
            @click="openSettings"
          >
            <Icon name="cog" size="sm" />
            {{ t('admin.riskControl.openSettings') }}
          </button>
        </div>
      </div>

      <div
        v-if="loading"
        class="space-y-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4" aria-hidden="true">
          <div
            v-for="card in 4"
            :key="card"
            class="flex min-w-0 items-center gap-3 rounded-control border border-gray-100 bg-white px-4 py-3 shadow-sm dark:border-dark-700 dark:bg-dark-800"
          >
            <Skeleton :width="36" :height="36" class="shrink-0 rounded-control" />
            <div class="min-w-0 flex-1 space-y-2">
              <Skeleton :width="96" :height="12" class="max-w-full" />
              <Skeleton :width="72" :height="20" class="max-w-full" />
            </div>
          </div>
        </div>
        <div class="card overflow-hidden" aria-hidden="true">
          <div class="space-y-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <div class="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
              <div class="min-w-0 flex-1 space-y-2">
                <Skeleton :width="144" :height="20" />
                <Skeleton :width="320" :height="14" class="max-w-full" />
              </div>
              <Skeleton :width="88" :height="36" class="shrink-0" />
            </div>
            <div class="flex gap-2">
              <Skeleton v-for="tab in 3" :key="tab" :width="86" :height="32" />
            </div>
            <Skeleton :height="36" />
          </div>
          <div
            v-for="row in 4"
            :key="row"
            class="flex justify-between gap-4 border-b border-gray-100 px-6 py-4 last:border-0 dark:border-dark-700"
          >
            <Skeleton width="28%" :height="14" />
            <Skeleton width="18%" :height="14" />
            <Skeleton width="25%" :height="14" />
          </div>
        </div>
      </div>

      <template v-else>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
          <div
            v-for="item in overviewItems"
            :key="item.key"
            class="rounded-control border border-gray-100 bg-white px-4 py-3 shadow-sm dark:border-dark-700 dark:bg-dark-800"
          >
            <div class="flex min-w-0 items-center gap-3">
              <div
                class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-control"
                :class="item.iconClass"
              >
                <Icon :name="item.icon" size="sm" />
              </div>
              <div class="min-w-0 flex-1">
                <div class="flex min-w-0 items-center justify-between gap-2">
                  <p class="truncate text-xs font-medium text-gray-500 dark:text-gray-400">{{
                    item.label
                  }}</p>
                  <span
                    v-if="item.badge"
                    class="inline-flex flex-shrink-0 items-center rounded-full px-2 py-0.5 text-xs font-medium"
                    :class="item.badgeClass"
                  >
                    {{ item.badge }}
                  </span>
                </div>
                <div class="mt-1 flex min-w-0 items-baseline gap-2">
                  <Skeleton
                    v-if="
                      item.key === 'logs' &&
                      activeRecordTab === 'cyber' &&
                      logsLoading &&
                      !cyberSummary
                    "
                    :width="48"
                    :height="20"
                    class="my-1"
                  />
                  <p
                    v-else
                    class="truncate text-xl font-semibold leading-7 text-gray-900 dark:text-white"
                    >{{ item.value }}</p
                  >
                  <p v-if="item.meta" class="truncate text-xs text-gray-500 dark:text-gray-400">{{
                    item.meta
                  }}</p>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div
          v-if="showPreBlockRuntimeCard"
          data-test="pre-block-runtime-cards"
          class="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]"
        >
          <div data-test="pre-block-sync-card" class="card">
            <div
              class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between"
            >
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.preBlockSyncStatus')
                }}</h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.preBlockSyncHint')
                }}</p>
              </div>
              <span
                class="inline-flex w-fit items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
              >
                {{ modeLabel(status?.mode ?? configForm.mode) }}
              </span>
            </div>

            <div class="p-6">
              <div data-test="pre-block-metric-grid" class="grid grid-cols-2 gap-3 md:grid-cols-3">
                <div
                  v-for="item in preBlockMetricItems"
                  :key="item.key"
                  class="rounded-control p-4"
                  :class="item.class"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{ item.label }}</p>
                  <p
                    class="mt-2 truncate text-2xl font-semibold leading-8"
                    :class="item.valueClass"
                    >{{ item.value }}</p
                  >
                  <p
                    v-if="item.meta"
                    class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400"
                    >{{ item.meta }}</p
                  >
                </div>
              </div>
            </div>
          </div>

          <div data-test="pre-block-api-key-load-card" class="card">
            <div
              class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between"
            >
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.preBlockAPIKeyLoad')
                }}</h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.riskControl.preBlockAPIKeyLoadHint') }}
                </p>
              </div>
              <span
                class="inline-flex w-fit items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
              >
                {{ preBlockAPIKeyLoadSummaryText }}
              </span>
            </div>

            <div class="p-6">
              <div
                v-if="preBlockAPIKeyLoads.length > 0"
                data-test="pre-block-api-key-load-list"
                class="max-h-[280px] space-y-3 overflow-y-auto pr-1"
                ><!-- check-ui-allow: Runtime credential list uses a bounded local scrolling area. -->
                <div
                  v-for="item in preBlockAPIKeyLoads"
                  :key="item.key_hash || item.index"
                  class="rounded-control bg-gray-50 p-3 dark:bg-dark-700/50"
                >
                  <div
                    class="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div class="min-w-0">
                      <div class="flex min-w-0 items-center gap-2">
                        <span class="font-mono text-sm font-semibold text-gray-900 dark:text-white"
                          >#{{ item.index + 1 }}</span
                        >
                        <span class="truncate font-mono text-sm text-gray-700 dark:text-gray-200">{{
                          item.masked || '-'
                        }}</span>
                        <span
                          class="h-2 w-2 flex-shrink-0 rounded-full"
                          :class="apiKeyStatusDotClass(item.status)"
                        ></span>
                      </div>
                      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                        {{
                          t('admin.riskControl.preBlockAPIKeyTotals', {
                            total: formatNumber(item.total),
                            success: formatNumber(item.success),
                            errors: formatNumber(item.errors),
                          })
                        }}
                      </p>
                      <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
                        {{
                          t('admin.riskControl.apiKeyPriorityValue', {
                            priority: item.priority || 100,
                          })
                        }}
                        <span v-if="item.note"> / {{ item.note }}</span>
                      </p>
                    </div>
                    <div
                      class="grid grid-cols-4 gap-2 text-right text-xs text-gray-500 dark:text-gray-400 sm:min-w-[280px]"
                    >
                      <div>
                        <p>{{ t('admin.riskControl.preBlockKeyActiveShort') }}</p>
                        <p class="mt-1 text-sm font-semibold text-sky-700 dark:text-sky-300">{{
                          formatNumber(item.active)
                        }}</p>
                      </div>
                      <div>
                        <p>{{ t('admin.riskControl.preBlockKeyTotalShort') }}</p>
                        <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white">{{
                          formatNumber(item.total)
                        }}</p>
                      </div>
                      <div>
                        <p>{{ t('admin.riskControl.preBlockKeyAvgShort') }}</p>
                        <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white"
                          >{{ formatNumber(item.avg_latency_ms) }} ms</p
                        >
                      </div>
                      <div>
                        <p>{{ t('admin.riskControl.preBlockKeyLastShort') }}</p>
                        <p class="mt-1 text-sm font-semibold text-gray-900 dark:text-white"
                          >{{ formatNumber(item.last_latency_ms) }} ms</p
                        >
                      </div>
                    </div>
                  </div>
                  <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-white dark:bg-dark-900">
                    <div
                      class="h-full rounded-full bg-sky-500"
                      :style="{ width: preBlockAPIKeyLoadWidth(item.total) }"
                    ></div>
                  </div>
                </div>
              </div>
              <p
                v-else
                class="rounded-control bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-700/50 dark:text-gray-400"
              >
                {{ t('admin.riskControl.preBlockAPIKeyLoadEmpty') }}
              </p>
            </div>
          </div>
        </div>

        <div v-if="showWorkerRuntimeCard" class="card">
          <div
            class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700 lg:flex-row lg:items-center lg:justify-between"
          >
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{
                t('admin.riskControl.workerStatus')
              }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.workerStatusHint')
              }}</p>
            </div>
            <div class="flex flex-wrap items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
              <span>{{ t('admin.riskControl.autoRefresh') }}</span>
              <span v-if="status?.last_cleanup_at">
                {{
                  t('admin.riskControl.lastCleanup', {
                    time: formatDateTime(status.last_cleanup_at),
                  })
                }}
              </span>
            </div>
          </div>

          <div class="grid grid-cols-1 gap-6 p-6 xl:grid-cols-[minmax(0,360px)_1fr]">
            <div class="space-y-4">
              <div class="rounded-control border border-gray-100 p-4 dark:border-dark-700">
                <div class="flex items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                      t('admin.riskControl.queueUsage')
                    }}</p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      {{ formatNumber(status?.queue_length ?? 0) }} /
                      {{ formatNumber(status?.queue_size ?? configForm.queue_size) }}
                    </p>
                  </div>
                  <span class="text-sm font-semibold text-gray-900 dark:text-white">{{
                    queueUsagePercent
                  }}</span>
                </div>
                <div class="mt-4 h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                  <div
                    class="h-full rounded-full bg-primary-500 transition-[width,background-color] duration-layout"
                    :style="queueUsageStyle"
                  ></div>
                </div>
              </div>

              <div class="grid grid-cols-2 gap-3">
                <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-700/50">
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.activeWorkers')
                  }}</p>
                  <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{
                    status?.active_workers ?? 0
                  }}</p>
                </div>
                <div class="rounded-control bg-emerald-50 p-4 dark:bg-emerald-900/10">
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.idleWorkers')
                  }}</p>
                  <p class="mt-2 text-2xl font-semibold text-emerald-700 dark:text-emerald-300">{{
                    status?.idle_workers ?? configForm.worker_count
                  }}</p>
                </div>
                <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-700/50">
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.processed')
                  }}</p>
                  <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{
                    formatNumber(status?.processed ?? 0)
                  }}</p>
                </div>
                <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-700/50">
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.droppedErrors')
                  }}</p>
                  <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{
                    formatNumber((status?.dropped ?? 0) + (status?.errors ?? 0))
                  }}</p>
                </div>
              </div>
            </div>

            <div>
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                    t('admin.riskControl.workerPool')
                  }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t('admin.riskControl.workerPoolMeta', {
                        active: status?.active_workers ?? 0,
                        idle: status?.idle_workers ?? configForm.worker_count,
                        total: status?.worker_count ?? configForm.worker_count,
                      })
                    }}
                  </p>
                </div>
                <span
                  class="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
                >
                  {{ modeLabel(status?.mode ?? configForm.mode) }}
                </span>
              </div>
              <div
                class="grid grid-cols-2 gap-2 sm:grid-cols-4 md:grid-cols-6 xl:grid-cols-8 2xl:grid-cols-10"
              >
                <div
                  v-for="worker in workerSlots"
                  :key="worker.id"
                  class="flex h-12 items-center justify-between rounded-control border px-3 transition-colors"
                  :class="workerSlotClass(worker.state)"
                  :title="worker.label"
                >
                  <span class="text-sm font-semibold">#{{ worker.id }}</span>
                  <span
                    class="h-2.5 w-2.5 rounded-full"
                    :class="workerDotClass(worker.state)"
                  ></span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="card overflow-hidden">
          <div class="flex flex-col gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <div class="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.records')
                }}</h2>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.recordsHint')
                }}</p>
              </div>
              <button
                type="button"
                class="btn btn-secondary inline-flex items-center gap-2"
                :disabled="logsLoading"
                @click="loadRecords"
              >
                <Icon name="refresh" size="sm" :class="logsLoading ? 'animate-spin' : ''" />
                {{ t('admin.riskControl.refresh') }}
              </button>
            </div>

            <div v-segmented class="segmented">
              <button
                v-for="tab in recordTabs"
                :key="tab.id"
                type="button"
                class="segmented-item px-3 py-1.5 text-sm"
                :class="{ 'segmented-item-active': activeRecordTab === tab.id }"
                @click="switchRecordTab(tab.id)"
              >
                {{ tab.label }}
              </button>
            </div>

            <div
              class="flex flex-col gap-2 rounded-control border border-gray-100 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/30 sm:flex-row sm:items-center sm:justify-between"
            >
              <div class="flex min-w-0 items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
                <Icon name="filter" size="sm" class="flex-shrink-0 text-gray-400" />
                <span class="font-medium">{{ t('admin.riskControl.modelFilter') }}</span>
                <span class="truncate text-gray-500 dark:text-gray-400">{{
                  modelFilterSummary
                }}</span>
              </div>
              <div v-if="modelFilterPreviewModels.length > 0" class="flex flex-wrap gap-1.5">
                <span
                  v-for="model in modelFilterPreviewModels"
                  :key="model"
                  class="inline-flex max-w-[180px] items-center truncate rounded-compact bg-white px-2 py-1 font-mono text-xs text-gray-600 shadow-sm dark:bg-dark-800 dark:text-gray-300"
                >
                  {{ model }}
                </span>
                <span
                  v-if="hiddenModelFilterModelCount > 0"
                  class="inline-flex rounded-compact bg-white px-2 py-1 text-xs text-gray-500 shadow-sm dark:bg-dark-800 dark:text-gray-400"
                >
                  +{{ hiddenModelFilterModelCount }}
                </span>
              </div>
            </div>

            <div class="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-7">
              <Select
                v-if="activeRecordTab === 'moderation'"
                v-model="filters.result"
                :options="resultOptions"
                @change="reloadLogsFromFirstPage"
              />
              <Select
                v-if="activeRecordTab === 'moderation'"
                v-model="filters.group_id"
                :options="groupFilterOptions"
                @change="reloadLogsFromFirstPage"
              />
              <Select
                v-if="activeRecordTab === 'moderation'"
                v-model="filters.endpoint"
                :options="endpointOptions"
                @change="reloadLogsFromFirstPage"
              />
              <input
                v-model.trim="filters.request_id"
                type="search"
                class="input font-mono"
                spellcheck="false"
                maxlength="255"
                :aria-label="t('requests.id')"
                :placeholder="t('requests.id')"
                @keyup.enter="reloadLogsFromFirstPage"
              />
              <input
                v-model.trim="filters.search"
                type="search"
                class="input"
                :placeholder="t('admin.riskControl.filters.search')"
                @keyup.enter="reloadLogsFromFirstPage"
              />
              <input
                v-model="filters.from"
                type="datetime-local"
                class="input"
                :title="t('admin.riskControl.filters.from')"
                @change="reloadLogsFromFirstPage"
              />
              <input
                v-model="filters.to"
                type="datetime-local"
                class="input"
                :title="t('admin.riskControl.filters.to')"
                @change="reloadLogsFromFirstPage"
              />
            </div>
            <div v-if="activeRecordTab === 'cyber'" class="grid grid-cols-2 gap-3 md:grid-cols-4">
              <div
                class="rounded-control border border-gray-100 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/30"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.cyberEvents')
                }}</p>
                <Skeleton
                  v-if="logsLoading && !cyberSummary"
                  :width="48"
                  :height="20"
                  class="my-1"
                />
                <p v-else class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
                  {{ cyberSummary ? formatNumber(cyberSummary.events) : '—' }}
                </p>
              </div>
              <div
                class="rounded-control border border-gray-100 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/30"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.cyberRequests')
                }}</p>
                <Skeleton
                  v-if="logsLoading && !cyberSummary"
                  :width="48"
                  :height="20"
                  class="my-1"
                />
                <p v-else class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
                  {{ cyberSummary ? formatNumber(cyberSummary.requests) : '—' }}
                </p>
              </div>
              <div
                class="rounded-control border border-gray-100 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/30"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.cyberUsers')
                }}</p>
                <Skeleton
                  v-if="logsLoading && !cyberSummary"
                  :width="48"
                  :height="20"
                  class="my-1"
                />
                <p v-else class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
                  {{ cyberSummary ? formatNumber(cyberSummary.users) : '—' }}
                </p>
              </div>
              <div
                class="rounded-control border border-gray-100 bg-gray-50 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/30"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.cyberProviders')
                }}</p>
                <Skeleton
                  v-if="logsLoading && !cyberSummary"
                  :width="48"
                  :height="20"
                  class="my-1"
                />
                <p v-else class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
                  {{ cyberSummary ? formatNumber(cyberSummary.providers) : '—' }}
                </p>
              </div>
            </div>
          </div>

          <div
            v-if="recordsError"
            class="flex flex-wrap items-center gap-3 px-6 py-3 text-sm"
            role="alert"
          >
            <p class="flex-1 text-gray-600 dark:text-dark-300">{{ recordsError }}</p>
            <button type="button" class="btn btn-secondary" @click="loadRecords">{{
              t('common.retry')
            }}</button>
          </div>

          <div v-if="activeRecordTab === 'moderation'" class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-800">
                <tr>
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.time') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.group') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.user') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.apiKey') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.endpoint') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.result') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.highest') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.actionMeta') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.latency') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.input') }}</th
                  >
                </tr>
              </thead>
              <TableSkeletonBody
                v-if="logsLoading && !logs.length"
                :columns="10"
                cell-class="px-5 py-3"
              />
              <tbody
                v-else
                class="divide-y divide-gray-100 bg-white dark:divide-dark-800 dark:bg-dark-800"
              >
                <tr v-if="logs.length === 0 && !recordsError">
                  <td
                    colspan="10"
                    class="px-5 py-12 text-center text-sm text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.emptyLogs') }}</td
                  >
                </tr>
                <template v-else>
                  <tr
                    v-for="row in logs"
                    :key="row.id"
                    class="hover:bg-gray-50 dark:hover:bg-dark-700/60"
                  >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                      >{{ formatDateTime(row.created_at) }}</td
                    >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                      >{{ row.group_name || '-' }}</td
                    >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ row.user_email || '-' }}</div>
                      <div v-if="row.user_id" class="text-xs text-gray-400"
                        >UID {{ row.user_id }}</div
                      >
                      <div
                        v-if="row.team_id"
                        class="text-xs text-gray-400"
                        data-test="moderation-team-attribution"
                      >
                        {{
                          t('admin.riskControl.teamAttribution', {
                            teamId: row.team_id,
                            billingUserId: row.billing_user_id ?? '-',
                          })
                        }}
                      </div>
                    </td>
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                      >{{ row.api_key_name || '-' }}</td
                    >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ row.endpoint || '-' }}</div>
                      <div class="text-xs text-gray-400"
                        >{{ row.provider || '-' }} / {{ row.model || '-' }}</div
                      >
                    </td>
                    <td class="whitespace-nowrap px-5 py-4">
                      <span
                        class="inline-flex rounded-compact px-2 py-1 text-xs font-medium"
                        :class="resultBadgeClass(row)"
                      >
                        {{ resultLabel(row) }}
                      </span>
                    </td>
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ row.highest_category || '-' }}</div>
                      <div class="text-xs text-gray-400">{{ percent(row.highest_score) }}</div>
                      <div
                        v-if="row.matched_keyword"
                        class="mt-0.5 text-xs font-medium text-red-600 dark:text-red-300"
                        :title="t('admin.riskControl.matchedKeyword') + ': ' + row.matched_keyword"
                      >
                        {{ t('admin.riskControl.matchedKeyword') }}: {{ row.matched_keyword }}
                      </div>
                    </td>
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ violationCountText(row) }}</div>
                      <div class="text-xs text-gray-400">
                        {{
                          row.email_sent
                            ? t('admin.riskControl.emailSent')
                            : t('admin.riskControl.emailNotSent')
                        }}
                        <span v-if="row.auto_banned">
                          / {{ t('admin.riskControl.autoBanned') }}</span
                        >
                      </div>
                      <button
                        v-if="canUnbanRow(row)"
                        type="button"
                        class="mt-2 inline-flex items-center gap-1 rounded-compact border border-emerald-200 bg-emerald-50 px-2 py-1 text-xs font-medium text-emerald-700 transition-colors hover:bg-emerald-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-300 dark:hover:bg-emerald-900/30"
                        :disabled="unbanningUserID === row.user_id"
                        @click="unbanUser(row)"
                      >
                        <Icon
                          name="checkCircle"
                          size="xs"
                          :class="unbanningUserID === row.user_id ? 'animate-spin' : ''"
                          :animate-on-hover="false"
                        />
                        {{
                          unbanningUserID === row.user_id
                            ? t('common.processing')
                            : t('admin.riskControl.unbanUser')
                        }}
                      </button>
                    </td>
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ latencyText(row.upstream_latency_ms) }}</div>
                      <div
                        v-if="row.queue_delay_ms !== null && row.queue_delay_ms !== undefined"
                        class="text-xs text-gray-400"
                      >
                        {{ t('admin.riskControl.queueDelay', { ms: row.queue_delay_ms }) }}
                      </div>
                    </td>
                    <td
                      class="w-[320px] max-w-sm px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <button
                        type="button"
                        data-test="moderation-detail-button"
                        class="group flex w-full min-w-0 items-center gap-2 rounded-control px-2 py-1.5 text-left transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                        :title="inputSummaryText(row)"
                        @click="openInputDetail(row)"
                      >
                        <span class="min-w-0 flex-1 truncate">{{ inputSummaryText(row) }}</span>
                        <Icon
                          name="eye"
                          size="xs"
                          class="flex-shrink-0 text-gray-300 transition-colors group-hover:text-primary-500 dark:text-gray-500"
                        />
                      </button>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>

          <div v-else class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-800">
                <tr>
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.time') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.user') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.apiKey') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.cyberProvider') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.endpoint') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.cyberStatus') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.table.actionMeta') }}</th
                  >
                  <th
                    class="px-5 py-3 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.cyberWarning') }}</th
                  >
                </tr>
              </thead>
              <TableSkeletonBody
                v-if="logsLoading && !cyberWarnings.length"
                :columns="8"
                cell-class="px-5 py-3"
              />
              <tbody
                v-else
                class="divide-y divide-gray-100 bg-white dark:divide-dark-800 dark:bg-dark-800"
              >
                <tr v-if="cyberWarnings.length === 0 && !recordsError">
                  <td
                    colspan="8"
                    class="px-5 py-12 text-center text-sm text-gray-500 dark:text-gray-400"
                    >{{ t('admin.riskControl.emptyCyberWarnings') }}</td
                  >
                </tr>
                <template v-else>
                  <tr
                    v-for="row in cyberWarnings"
                    :key="row.id"
                    class="hover:bg-gray-50 dark:hover:bg-dark-700/60"
                  >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                      >{{ formatDateTime(row.created_at) }}</td
                    >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ row.user_email || '-' }}</div>
                      <div v-if="row.user_id" class="text-xs text-gray-400"
                        >UID {{ row.user_id }}</div
                      >
                      <div
                        v-if="row.team_id"
                        class="text-xs text-gray-400"
                        data-test="cyber-team-attribution"
                      >
                        {{
                          t('admin.riskControl.teamAttribution', {
                            teamId: row.team_id,
                            billingUserId: row.billing_user_id ?? '-',
                          })
                        }}
                      </div>
                    </td>
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                      >{{ row.api_key_name || '-' }}</td
                    >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ row.provider_name || '-' }}</div>
                      <div v-if="row.provider_id" class="text-xs text-gray-400"
                        >AID {{ row.provider_id }}</div
                      >
                    </td>
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{ row.endpoint || '-' }}</div>
                      <div class="text-xs text-gray-400">openai / {{ row.model || '-' }}</div>
                    </td>
                    <td class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                      >HTTP {{ row.upstream_status || '-' }}</td
                    >
                    <td
                      class="whitespace-nowrap px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <div>{{
                        t('admin.riskControl.violationCount', { count: row.violation_count || 1 })
                      }}</div>
                      <div class="text-xs text-gray-400">
                        {{
                          row.email_sent
                            ? t('admin.riskControl.emailSent')
                            : t('admin.riskControl.emailNotSent')
                        }}
                        <span v-if="row.auto_banned">
                          / {{ t('admin.riskControl.autoBanned') }}</span
                        >
                      </div>
                      <button
                        v-if="canUnbanCyberRow(row)"
                        type="button"
                        class="mt-2 inline-flex items-center gap-1 rounded-compact border border-emerald-200 bg-emerald-50 px-2 py-1 text-xs font-medium text-emerald-700 transition-colors hover:bg-emerald-100 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-300 dark:hover:bg-emerald-900/30"
                        :disabled="unbanningUserID === row.user_id"
                        @click="unbanCyberUser(row)"
                      >
                        <Icon
                          name="checkCircle"
                          size="xs"
                          :class="unbanningUserID === row.user_id ? 'animate-spin' : ''"
                          :animate-on-hover="false"
                        />
                        {{
                          unbanningUserID === row.user_id
                            ? t('common.processing')
                            : t('admin.riskControl.unbanUser')
                        }}
                      </button>
                    </td>
                    <td
                      class="w-[360px] max-w-sm px-5 py-4 text-sm text-gray-700 dark:text-gray-300"
                    >
                      <button
                        type="button"
                        data-test="cyber-detail-button"
                        class="group flex w-full min-w-0 items-center gap-2 rounded-control px-2 py-1.5 text-left transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                        :title="cyberSummaryText(row)"
                        @click="openCyberDetail(row)"
                      >
                        <span class="min-w-0 flex-1 truncate">{{ cyberSummaryText(row) }}</span>
                        <Icon
                          name="eye"
                          size="xs"
                          class="flex-shrink-0 text-gray-300 transition-colors group-hover:text-primary-500 dark:text-gray-500"
                        />
                      </button>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>

          <Pagination
            v-if="pagination.total > 0"
            :page="pagination.page"
            :total="pagination.total"
            :page-size="pagination.page_size"
            @update:page="onPageChange"
            @update:pageSize="onPageSizeChange"
          />
        </div>
      </template>

      <BaseDialog
        :show="settingsOpen"
        :title="t('admin.riskControl.settingsTitle')"
        width="extra-wide"
        @close="settingsOpen = false"
      >
        <div class="space-y-6">
          <div
            class="flex gap-2 overflow-x-auto border-b border-gray-100 pb-3 dark:border-dark-700"
          >
            <button
              v-for="tab in settingsTabs"
              :key="tab.id"
              type="button"
              class="inline-flex h-9 items-center whitespace-nowrap rounded-control px-3 py-1.5 text-sm font-medium transition-colors"
              :class="
                activeSettingsTab === tab.id
                  ? 'bg-primary-50 text-primary-700 dark:bg-primary-500/8 dark:text-primary-500'
                  : 'text-gray-500 hover:bg-gray-50 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white'
              "
              @click="activeSettingsTab = tab.id"
            >
              {{ tab.label }}
            </button>
          </div>

          <div v-if="activeSettingsTab === 'basic'" class="space-y-5">
            <div class="grid grid-cols-1 gap-5 lg:grid-cols-2">
              <div
                class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
              >
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                    t('admin.riskControl.enabled')
                  }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.enabledHint')
                  }}</p>
                </div>
                <Toggle v-model="configForm.enabled" />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.mode') }}</label>
                <Select v-model="configForm.mode" :options="modeOptions" />
                <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                  modeDescription(configForm.mode)
                }}</p>
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.baseUrl') }}</label>
                <input
                  v-model.trim="configForm.base_url"
                  type="url"
                  class="input"
                  placeholder="https://api.openai.com"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.model') }}</label>
                <input
                  v-model.trim="configForm.model"
                  type="text"
                  class="input"
                  placeholder="omni-moderation-latest"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.timeoutMs') }}</label>
                <input
                  v-model.number="configForm.timeout_ms"
                  type="number"
                  min="500"
                  max="30000"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.retryCount') }}</label>
                <input
                  v-model.number="configForm.retry_count"
                  type="number"
                  min="0"
                  max="5"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.sampleRate') }}</label>
                <div class="relative">
                  <input
                    v-model.number="configForm.sample_rate"
                    type="number"
                    min="0"
                    max="100"
                    step="1"
                    class="input pr-8"
                  />
                  <span
                    class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-gray-400"
                    >%</span
                  >
                </div>
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.proxy') }}</label>
                <ProxySelector v-model="configForm.proxy_id" :proxies="proxies" />
                <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ t('admin.riskControl.proxyHint') }}
                </p>
              </div>
            </div>

            <div
              class="overflow-hidden rounded-surface border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800"
            >
              <div
                class="flex flex-col gap-4 border-b border-gray-100 bg-gray-50 px-4 py-4 dark:border-dark-700 dark:bg-dark-800/60 lg:flex-row lg:items-center lg:justify-between"
              >
                <div class="flex items-start gap-3">
                  <span
                    class="mt-0.5 flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"
                  >
                    <Icon name="key" size="md" />
                  </span>
                  <div>
                    <label class="text-sm font-semibold text-gray-900 dark:text-white">{{
                      t('admin.riskControl.apiKeys')
                    }}</label>
                    <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-500 dark:text-gray-400">
                      {{ t('admin.riskControl.apiKeysHint', { count: configForm.api_key_count }) }}
                    </p>
                  </div>
                </div>
                <div class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    class="btn btn-secondary inline-flex items-center gap-2"
                    :disabled="apiKeyTesting || inputApiKeyCount === 0 || configForm.clear_api_key"
                    @click="testApiKeys(true)"
                  >
                    <Icon name="beaker" size="sm" :class="apiKeyTesting ? 'animate-pulse' : ''" />
                    {{
                      apiKeyTesting
                        ? t('admin.riskControl.testingApiKeys')
                        : t('admin.riskControl.testInputApiKeys')
                    }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-secondary inline-flex items-center gap-2"
                    :disabled="
                      apiKeyTesting ||
                      effectiveStoredApiKeyCount === 0 ||
                      pendingDeletedApiKeyCount > 0 ||
                      configForm.clear_api_key ||
                      configForm.api_keys_mode === 'replace'
                    "
                    @click="testApiKeys(false)"
                  >
                    <Icon name="shield" size="sm" />
                    {{ storedApiKeyTestButtonText }}
                  </button>
                  <button
                    v-if="configForm.api_key_configured"
                    type="button"
                    class="btn btn-secondary inline-flex items-center gap-2"
                    @click="toggleClearApiKey"
                  >
                    <Icon :name="configForm.clear_api_key ? 'x' : 'trash'" size="sm" />
                    {{
                      configForm.clear_api_key
                        ? t('admin.riskControl.keepApiKey')
                        : t('admin.riskControl.clearApiKey')
                    }}
                  </button>
                </div>
              </div>

              <div
                class="grid grid-cols-1 items-start gap-4 p-4 xl:grid-cols-[minmax(0,1fr)_minmax(360px,440px)]"
              >
                <div class="space-y-3">
                  <div
                    class="flex flex-col gap-2 rounded-control border border-gray-100 bg-gray-50 p-2 dark:border-dark-700 dark:bg-dark-900/30 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div class="text-xs leading-5 text-gray-500 dark:text-gray-400">
                      <span class="font-medium text-gray-700 dark:text-gray-200">{{
                        t('admin.riskControl.apiKeysWriteMode')
                      }}</span>
                      <span class="ml-2">{{ apiKeysModeHint }}</span>
                    </div>
                    <div v-segmented class="segmented">
                      <button
                        type="button"
                        class="segmented-item px-3 py-1.5 text-xs"
                        :class="{ 'segmented-item-active': configForm.api_keys_mode === 'append' }"
                        :disabled="configForm.clear_api_key"
                        @click="setAPIKeysMode('append')"
                      >
                        {{ t('admin.riskControl.apiKeysModeAppend') }}
                      </button>
                      <button
                        type="button"
                        class="segmented-item px-3 py-1.5 text-xs"
                        :class="{ 'segmented-item-active': configForm.api_keys_mode === 'replace' }"
                        :disabled="configForm.clear_api_key"
                        @click="setAPIKeysMode('replace')"
                      >
                        {{ t('admin.riskControl.apiKeysModeReplace') }}
                      </button>
                    </div>
                  </div>
                  <textarea
                    v-model="configForm.api_keys_text"
                    class="input min-h-44 resize-y font-mono text-sm"
                    :placeholder="apiKeysPlaceholder"
                    autocomplete="new-password"
                    :disabled="configForm.clear_api_key"
                  ></textarea>
                  <div
                    class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400"
                  >
                    <span
                      class="inline-flex rounded-compact bg-gray-100 px-2 py-1 dark:bg-dark-700"
                    >
                      {{ t('admin.riskControl.inputApiKeyCount', { count: inputApiKeyCount }) }}
                    </span>
                    <span
                      v-if="configForm.api_key_configured"
                      class="inline-flex rounded-compact bg-gray-100 px-2 py-1 dark:bg-dark-700"
                    >
                      {{
                        t('admin.riskControl.storedApiKeyCount', {
                          count: configForm.api_key_count,
                        })
                      }}
                    </span>
                    <span
                      v-if="configForm.clear_api_key"
                      class="inline-flex rounded-compact bg-red-50 px-2 py-1 text-red-700 dark:bg-red-900/20 dark:text-red-300"
                    >
                      {{ t('admin.riskControl.apiKeyWillClear') }}
                    </span>
                    <span
                      v-else-if="pendingDeletedApiKeyCount > 0"
                      class="inline-flex rounded-compact bg-amber-50 px-2 py-1 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                    >
                      {{
                        t('admin.riskControl.apiKeyPendingDeleteCount', {
                          count: pendingDeletedApiKeyCount,
                        })
                      }}
                    </span>
                    <span
                      v-if="configForm.api_keys_mode === 'replace'"
                      class="inline-flex rounded-compact bg-amber-50 px-2 py-1 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                    >
                      {{ t('admin.riskControl.apiKeysReplaceWarning') }}
                    </span>
                  </div>

                  <div
                    class="rounded-surface border border-gray-100 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900/30"
                    @paste="handleModerationImagePaste"
                  >
                    <div class="mb-3 flex items-center justify-between gap-3">
                      <div>
                        <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                          t('admin.riskControl.auditTestInput')
                        }}</p>
                        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                          t('admin.riskControl.auditTestInputHint')
                        }}</p>
                      </div>
                      <button
                        v-if="
                          moderationTestPrompt ||
                          moderationTestImages.length > 0 ||
                          moderationTestResult
                        "
                        type="button"
                        class="inline-flex items-center gap-1 rounded-compact px-2 py-1 text-xs font-medium text-gray-500 hover:bg-white hover:text-gray-900 dark:text-gray-400 dark:hover:bg-dark-800 dark:hover:text-white"
                        @click="clearModerationTestInput"
                      >
                        <Icon name="x" size="xs" />
                        {{ t('admin.riskControl.clearAuditTest') }}
                      </button>
                    </div>
                    <textarea
                      v-model="moderationTestPrompt"
                      class="input min-h-24 resize-y text-sm"
                      :placeholder="t('admin.riskControl.auditTestPromptPlaceholder')"
                    ></textarea>
                    <div
                      class="mt-3 rounded-surface border border-dashed border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800"
                      @dragover.prevent
                      @drop.prevent="handleModerationImageDrop"
                    >
                      <div
                        class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"
                      >
                        <div class="flex items-start gap-2">
                          <Icon name="upload" size="md" class="mt-0.5 text-gray-400" />
                          <div>
                            <p class="text-sm font-medium text-gray-800 dark:text-gray-100">{{
                              t('admin.riskControl.auditTestImages')
                            }}</p>
                            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                              t('admin.riskControl.auditTestImagesHint')
                            }}</p>
                          </div>
                        </div>
                        <label
                          class="btn btn-secondary inline-flex cursor-pointer items-center gap-2"
                        >
                          <Icon name="plus" size="sm" />
                          {{ t('admin.riskControl.addAuditTestImage') }}
                          <input
                            type="file"
                            accept="image/*"
                            multiple
                            class="sr-only"
                            @change="handleModerationImageUpload"
                          />
                        </label>
                      </div>
                      <div
                        v-if="moderationTestImages.length > 0"
                        class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4"
                      >
                        <div
                          v-for="(image, index) in moderationTestImages"
                          :key="image.slice(0, 64) + index"
                          class="group relative aspect-square overflow-hidden rounded-control border border-gray-100 bg-gray-100 dark:border-dark-700 dark:bg-dark-700"
                        >
                          <img :src="image" alt="" class="h-full w-full object-cover" />
                          <button
                            type="button"
                            class="absolute right-1.5 top-1.5 flex h-7 w-7 items-center justify-center rounded-full bg-black/60 text-white opacity-0 transition-opacity group-hover:opacity-100"
                            @click="removeModerationTestImage(index)"
                          >
                            <Icon name="x" size="xs" :stroke-width="2" />
                          </button>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>

                <div
                  class="rounded-surface border border-gray-100 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900/30"
                >
                  <div class="mb-3 flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                        t('admin.riskControl.apiKeyHealth')
                      }}</p>
                      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                        t('admin.riskControl.apiKeyFreezeRule')
                      }}</p>
                    </div>
                    <span
                      class="inline-flex shrink-0 items-center whitespace-nowrap rounded-full bg-white px-2 py-0.5 text-xs font-medium leading-5 text-gray-600 shadow-sm dark:bg-dark-800 dark:text-gray-300"
                    >
                      {{ t('admin.riskControl.apiKeyRows', { count: apiKeyRows.length }) }}
                    </span>
                  </div>

                  <div
                    v-if="apiKeyRows.length === 0"
                    class="flex min-h-32 flex-col items-center justify-center rounded-control border border-dashed border-gray-200 bg-white px-4 py-6 text-center dark:border-dark-700 dark:bg-dark-800"
                  >
                    <Icon name="infoCircle" size="lg" class="text-gray-300 dark:text-dark-500" />
                    <p class="mt-2 text-sm font-medium text-gray-700 dark:text-gray-200">{{
                      t('admin.riskControl.apiKeyHealthEmpty')
                    }}</p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.riskControl.apiKeyHealthEmptyHint')
                    }}</p>
                  </div>
                  <div v-else class="space-y-2">
                    <div class="space-y-2" :class="apiKeyRowsExpanded ? 'overflow-visible' : ''">
                      <div
                        v-for="(row, index) in visibleApiKeyRows"
                        :key="apiKeyRowKey(row, index)"
                        class="rounded-control border bg-white p-2.5 shadow-sm dark:bg-dark-800"
                        :class="
                          isStoredApiKeyPendingDelete(row)
                            ? 'border-amber-200 opacity-70 dark:border-amber-800/60'
                            : 'border-gray-100 dark:border-dark-700'
                        "
                      >
                        <div class="flex items-start justify-between gap-2">
                          <div class="min-w-0">
                            <div class="flex min-w-0 flex-wrap items-center gap-2">
                              <span
                                class="truncate font-mono text-sm font-semibold text-gray-900 dark:text-white"
                                >{{ row.masked || '-' }}</span
                              >
                              <span
                                class="inline-flex rounded-compact px-1.5 py-0.5 text-xs font-medium"
                                :class="
                                  row.configured
                                    ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                                    : 'bg-purple-50 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300'
                                "
                              >
                                {{
                                  isStoredApiKeyPendingDelete(row)
                                    ? t('admin.riskControl.apiKeyPendingDelete')
                                    : row.configured
                                      ? t('admin.riskControl.apiKeyConfigured')
                                      : t('admin.riskControl.apiKeyTemporary')
                                }}
                              </span>
                            </div>
                            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                              apiKeyStatusMeta(row)
                            }}</p>
                          </div>
                          <div class="flex flex-shrink-0 items-center gap-1.5">
                            <span
                              class="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-medium"
                              :class="apiKeyStatusBadgeClass(row.status)"
                            >
                              <span
                                class="h-1.5 w-1.5 rounded-full"
                                :class="apiKeyStatusDotClass(row.status)"
                              ></span>
                              {{ apiKeyStatusLabel(row.status) }}
                            </span>
                            <button
                              v-if="row.configured && !configForm.clear_api_key"
                              type="button"
                              class="inline-flex h-7 w-7 items-center justify-center rounded-control text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                              :title="
                                isStoredApiKeyPendingDelete(row)
                                  ? t('admin.riskControl.undoDeleteApiKey')
                                  : t('admin.riskControl.deleteApiKey')
                              "
                              @click="toggleDeleteStoredApiKey(row)"
                            >
                              <Icon
                                :name="isStoredApiKeyPendingDelete(row) ? 'refresh' : 'trash'"
                                size="xs"
                              />
                            </button>
                          </div>
                        </div>
                        <p
                          v-if="row.last_error"
                          class="mt-1.5 rounded-control bg-amber-50 px-2 py-1.5 text-xs leading-5 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                        >
                          {{ row.last_error }}
                        </p>
                        <div
                          v-if="row.configured && row.key_hash && !configForm.clear_api_key"
                          class="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-[120px_minmax(0,1fr)]"
                        >
                          <div>
                            <label
                              class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400"
                              >{{ t('admin.riskControl.apiKeyPriority') }}</label
                            >
                            <input
                              :value="apiKeyDraftPriority(row)"
                              type="number"
                              data-test="api-key-priority"
                              min="1"
                              max="1000"
                              class="input text-sm"
                              :disabled="isStoredApiKeyPendingDelete(row)"
                              @input="setApiKeyDraftPriority(row, $event)"
                            />
                          </div>
                          <div>
                            <label
                              class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400"
                              >{{ t('admin.riskControl.apiKeyNote') }}</label
                            >
                            <input
                              :value="apiKeyDraftNote(row)"
                              type="text"
                              data-test="api-key-note"
                              maxlength="200"
                              class="input text-sm"
                              :placeholder="t('admin.riskControl.apiKeyNotePlaceholder')"
                              :disabled="isStoredApiKeyPendingDelete(row)"
                              @input="setApiKeyDraftNote(row, $event)"
                            />
                          </div>
                        </div>
                      </div>
                    </div>

                    <div
                      v-if="canToggleApiKeyRows"
                      class="flex items-center justify-between gap-3 rounded-control border border-dashed border-gray-200 bg-white px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-400"
                    >
                      <span class="min-w-0 truncate">
                        {{
                          apiKeyRowsExpanded
                            ? t('admin.riskControl.apiKeyRowsExpanded', {
                                count: apiKeyRows.length,
                              })
                            : t('admin.riskControl.apiKeyRowsCollapsed', {
                                count: hiddenApiKeyRowCount,
                              })
                        }}
                      </span>
                      <button
                        type="button"
                        class="inline-flex shrink-0 items-center gap-1 rounded-compact px-2 py-1 font-medium text-primary-600 transition-colors hover:bg-primary-50 hover:text-primary-700 dark:text-primary-300 dark:hover:bg-primary-900/20"
                        @click="apiKeyRowsExpanded = !apiKeyRowsExpanded"
                      >
                        <Icon
                          name="chevronDown"
                          class="transition-transform duration-normal"
                          :class="{ 'rotate-180': apiKeyRowsExpanded }"
                          size="xs"
                          :animate-on-hover="false"
                        />
                        {{
                          apiKeyRowsExpanded
                            ? t('admin.riskControl.collapseApiKeyRows')
                            : t('admin.riskControl.expandApiKeyRows')
                        }}
                      </button>
                    </div>
                  </div>

                  <div
                    v-if="moderationTestResult"
                    class="mt-4 rounded-surface border border-gray-100 bg-white p-3 dark:border-dark-700 dark:bg-dark-800"
                  >
                    <div class="flex items-start justify-between gap-3">
                      <div>
                        <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                          t('admin.riskControl.auditTestResult')
                        }}</p>
                        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                          {{
                            t('admin.riskControl.auditTestHighest', {
                              category: moderationTestResult.highest_category || '-',
                              score: percent(moderationTestResult.highest_score),
                            })
                          }}
                        </p>
                      </div>
                      <span
                        class="inline-flex rounded-full px-2 py-1 text-xs font-medium"
                        :class="
                          moderationTestResult.flagged
                            ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'
                            : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
                        "
                      >
                        {{
                          moderationTestResult.flagged
                            ? t('admin.riskControl.auditTestFlagged')
                            : t('admin.riskControl.auditTestPassed')
                        }}
                      </span>
                    </div>
                    <div class="mt-3">
                      <div
                        class="mb-2 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400"
                      >
                        <span>{{ t('admin.riskControl.auditTestComposite') }}</span>
                        <span class="font-semibold text-gray-900 dark:text-white">{{
                          percent(moderationTestResult.composite_score)
                        }}</span>
                      </div>
                      <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                        <div
                          class="h-full rounded-full"
                          :class="moderationTestResult.flagged ? 'bg-red-500' : 'bg-emerald-500'"
                          :style="{ width: percentWidth(moderationTestResult.composite_score) }"
                        ></div>
                      </div>
                    </div>
                    <div class="mt-3 max-h-52 space-y-2 overflow-y-auto pr-1">
                      <div v-for="score in moderationScoreRows" :key="score.category">
                        <div class="mb-1 flex items-center justify-between gap-3 text-xs">
                          <span class="truncate text-gray-600 dark:text-gray-300">{{
                            score.category
                          }}</span>
                          <span class="font-mono text-gray-500 dark:text-gray-400"
                            >{{ percent(score.score) }} / {{ percent(score.threshold) }}</span
                          >
                        </div>
                        <div
                          class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"
                        >
                          <div
                            class="h-full rounded-full"
                            :class="score.hit ? 'bg-red-500' : 'bg-primary-500'"
                            :style="{ width: percentWidth(score.score) }"
                          ></div>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div v-else-if="activeSettingsTab === 'scope'" class="space-y-5">
            <div class="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
              <div>
                <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.groupScope')
                }}</h3>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.groupScopeHint')
                }}</p>
              </div>
              <div v-segmented class="segmented">
                <button
                  type="button"
                  class="segmented-item px-3 py-1.5 text-sm"
                  :class="{ 'segmented-item-active': configForm.all_groups }"
                  @click="configForm.all_groups = true"
                >
                  {{ t('admin.riskControl.allGroups') }}
                </button>
                <button
                  type="button"
                  class="segmented-item px-3 py-1.5 text-sm"
                  :class="{ 'segmented-item-active': !configForm.all_groups }"
                  @click="configForm.all_groups = false"
                >
                  {{ t('admin.riskControl.selectedGroups') }}
                </button>
              </div>
            </div>

            <div v-if="!configForm.all_groups" class="space-y-4">
              <div class="relative">
                <Icon name="search" size="sm" class="input-icon text-gray-400" />
                <input
                  v-model.trim="groupSearch"
                  type="search"
                  class="input input-has-icon [--input-icon-slot:1.5rem]"
                  :placeholder="t('admin.riskControl.searchGroups')"
                />
              </div>
              <div
                class="grid max-h-panel grid-cols-1 gap-3 overflow-y-auto pr-1 md:grid-cols-2 xl:grid-cols-3"
              >
                <button
                  v-for="group in filteredGroups"
                  :key="group.id"
                  type="button"
                  class="flex min-h-20 items-center justify-between rounded-control border p-4 text-left transition-colors"
                  :class="
                    isGroupSelected(group.id)
                      ? 'border-primary-300 bg-primary-50 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                      : 'border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60'
                  "
                  @click="toggleGroup(group.id)"
                >
                  <span class="min-w-0">
                    <span
                      class="block truncate text-sm font-semibold text-gray-900 dark:text-white"
                      >{{ group.name }}</span
                    >
                  </span>
                  <span
                    class="flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-full border"
                    :class="
                      isGroupSelected(group.id)
                        ? 'border-primary-700 bg-primary-700 text-white dark:border-primary-600 dark:bg-primary-600'
                        : 'border-gray-300 text-transparent dark:border-dark-500'
                    "
                  >
                    <Icon name="check" size="xs" :stroke-width="2" :animate-on-hover="false" />
                  </span>
                </button>
                <p
                  v-if="filteredGroups.length === 0"
                  class="text-sm text-gray-500 dark:text-gray-400"
                  >{{ t('admin.riskControl.noGroups') }}</p
                >
              </div>
            </div>

            <div class="space-y-4 rounded-control border border-gray-100 p-4 dark:border-dark-700">
              <div class="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
                <div>
                  <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{
                    t('admin.riskControl.modelFilter')
                  }}</h3>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.modelFilterHint')
                  }}</p>
                </div>
                <span
                  class="inline-flex w-fit rounded-compact bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
                >
                  {{ modelFilterSummary }}
                </span>
              </div>

              <div class="grid grid-cols-1 gap-2 md:grid-cols-3">
                <button
                  v-for="option in modelFilterOptions"
                  :key="option.value"
                  type="button"
                  class="rounded-control border p-3 text-left transition-colors"
                  :class="
                    configForm.model_filter_type === option.value
                      ? 'border-primary-300 bg-primary-50 text-primary-900 shadow-sm dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                      : 'border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60'
                  "
                  @click="setModelFilterType(option.value)"
                >
                  <div class="flex items-center justify-between gap-2">
                    <span class="text-sm font-semibold">{{ option.label }}</span>
                    <span
                      class="flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full border"
                      :class="
                        configForm.model_filter_type === option.value
                          ? 'border-primary-700 bg-primary-700 text-white dark:border-primary-600 dark:bg-primary-600'
                          : 'border-gray-300 text-transparent dark:border-dark-500'
                      "
                    >
                      <Icon name="check" size="xs" :stroke-width="2" :animate-on-hover="false" />
                    </span>
                  </div>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                    option.description
                  }}</p>
                </button>
              </div>

              <div v-if="configForm.model_filter_type !== 'all'" class="space-y-2">
                <label class="input-label">{{ t('admin.riskControl.modelFilterModels') }}</label>
                <ModelWhitelistSelector v-model="configForm.model_filter_models" />
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{
                    t('admin.riskControl.modelFilterModelCount', { count: modelFilterModelCount })
                  }}
                </p>
              </div>
            </div>

            <div
              class="space-y-4 rounded-control border border-gray-100 p-4 dark:border-dark-700"
              data-test="audit-content-scope"
            >
              <div>
                <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.auditContentScope')
                }}</h3>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.auditContentScopeHint')
                }}</p>
              </div>
              <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                <div>
                  <label class="input-label">{{
                    t('admin.riskControl.auditUserTextMaxChars')
                  }}</label>
                  <input
                    v-model.number="configForm.audit_user_text_max_chars"
                    data-test="audit-user-text-max-chars"
                    type="number"
                    min="1"
                    max="1000000"
                    class="input"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.auditUserTextMaxCharsHint')
                  }}</p>
                </div>
                <div>
                  <label class="input-label">{{
                    t('admin.riskControl.auditToolOutputMaxChars')
                  }}</label>
                  <input
                    v-model.number="configForm.audit_tool_output_max_chars"
                    data-test="audit-tool-output-max-chars"
                    type="number"
                    min="1"
                    max="1000000"
                    class="input"
                    :disabled="!configForm.audit_tool_outputs"
                  />
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.auditToolOutputMaxCharsHint')
                  }}</p>
                </div>
                <div
                  class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
                >
                  <div class="pr-4">
                    <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                      t('admin.riskControl.auditImages')
                    }}</p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.riskControl.auditImagesHint')
                    }}</p>
                  </div>
                  <Toggle v-model="configForm.audit_images" />
                </div>
                <div
                  class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
                >
                  <div class="pr-4">
                    <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                      t('admin.riskControl.auditToolOutputs')
                    }}</p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.riskControl.auditToolOutputsHint')
                    }}</p>
                  </div>
                  <Toggle v-model="configForm.audit_tool_outputs" />
                </div>
              </div>
            </div>
          </div>

          <div
            v-else-if="activeSettingsTab === 'runtime'"
            class="grid grid-cols-1 gap-5 lg:grid-cols-2"
          >
            <div>
              <label class="input-label">{{ t('admin.riskControl.workerCount') }}</label>
              <input
                v-model.number="configForm.worker_count"
                type="number"
                min="1"
                max="32"
                class="input"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.riskControl.queueSize') }}</label>
              <input
                v-model.number="configForm.queue_size"
                type="number"
                min="100"
                max="100000"
                class="input"
              />
            </div>
            <div
              class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700 lg:col-span-2"
            >
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                  t('admin.riskControl.recordNonHits')
                }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.recordNonHitsHint')
                }}</p>
              </div>
              <Toggle v-model="configForm.record_non_hits" />
            </div>
            <div
              class="space-y-4 rounded-control border border-gray-100 p-4 dark:border-dark-700 lg:col-span-2"
            >
              <div class="flex items-center justify-between gap-4">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                    t('admin.riskControl.preHashCheck')
                  }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.preHashCheckHint')
                  }}</p>
                </div>
                <Toggle v-model="configForm.pre_hash_check_enabled" />
              </div>
              <div class="rounded-control bg-gray-50 p-3 dark:bg-dark-900/30">
                <div class="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
                  <div>
                    <p class="text-sm font-medium text-gray-900 dark:text-white">
                      {{
                        t('admin.riskControl.flaggedHashCount', {
                          count: formatNumber(status?.flagged_hash_count ?? 0),
                        })
                      }}
                    </p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.riskControl.flaggedHashHint')
                    }}</p>
                  </div>
                  <button
                    type="button"
                    class="btn btn-secondary inline-flex items-center justify-center gap-2 text-red-600 hover:text-red-700 dark:text-red-300"
                    :disabled="hashActionLoading || (status?.flagged_hash_count ?? 0) === 0"
                    @click="clearFlaggedHashes"
                  >
                    <Icon
                      name="trash"
                      size="sm"
                      :class="hashActionLoading ? 'animate-pulse' : ''"
                    />
                    {{ t('admin.riskControl.clearFlaggedHashes') }}
                  </button>
                </div>
                <div class="mt-3 flex flex-col gap-2 sm:flex-row">
                  <input
                    v-model.trim="flaggedHashInput"
                    type="text"
                    class="input min-w-0 flex-1 font-mono text-sm"
                    :placeholder="t('admin.riskControl.flaggedHashPlaceholder')"
                  />
                  <button
                    type="button"
                    class="btn btn-secondary shrink-0 whitespace-nowrap px-3 sm:min-w-[9rem]"
                    :disabled="hashActionLoading || !isFlaggedHashInputValid"
                    @click="deleteFlaggedHash"
                  >
                    <Icon name="trash" size="sm" />
                    {{ t('admin.riskControl.deleteFlaggedHash') }}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div v-else-if="activeSettingsTab === 'response'" class="space-y-5">
            <div class="grid grid-cols-1 gap-5 lg:grid-cols-2">
              <div>
                <label class="input-label">{{ t('admin.riskControl.blockStatus') }}</label>
                <input
                  v-model.number="configForm.block_status"
                  type="number"
                  min="400"
                  max="599"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.blockMessage') }}</label>
                <input v-model.trim="configForm.block_message" type="text" class="input" />
              </div>
              <div
                class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
              >
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                    t('admin.riskControl.emailOnHit')
                  }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.emailOnHitHint')
                  }}</p>
                </div>
                <Toggle v-model="configForm.email_on_hit" />
              </div>
              <div
                class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
              >
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                    t('admin.riskControl.autoBan')
                  }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                    t('admin.riskControl.autoBanHint')
                  }}</p>
                </div>
                <Toggle v-model="configForm.auto_ban_enabled" />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.banThreshold') }}</label>
                <input
                  v-model.number="configForm.ban_threshold"
                  type="number"
                  min="1"
                  max="1000"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.riskControl.violationWindowHours') }}</label>
                <input
                  v-model.number="configForm.violation_window_hours"
                  type="number"
                  min="1"
                  max="8760"
                  class="input"
                />
              </div>
            </div>
            <div class="rounded-control border border-gray-100 p-4 dark:border-dark-700">
              <div class="mb-4">
                <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.cyberSettings')
                }}</h3>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.cyberSettingsHint')
                }}</p>
              </div>
              <div class="grid grid-cols-1 gap-5 lg:grid-cols-2">
                <div
                  class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
                >
                  <div>
                    <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                      t('admin.riskControl.cyberWarningEnabled')
                    }}</p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.riskControl.cyberWarningEnabledHint')
                    }}</p>
                  </div>
                  <Toggle v-model="configForm.cyber_warning_enabled" />
                </div>
                <div
                  class="flex items-center justify-between rounded-control border border-gray-100 p-4 dark:border-dark-700"
                >
                  <div>
                    <p class="text-sm font-medium text-gray-900 dark:text-white">{{
                      t('admin.riskControl.cyberAutoBan')
                    }}</p>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.riskControl.cyberAutoBanHint')
                    }}</p>
                  </div>
                  <Toggle v-model="configForm.cyber_auto_ban_enabled" />
                </div>
                <div>
                  <label class="input-label">{{ t('admin.riskControl.cyberBanThreshold') }}</label>
                  <input
                    v-model.number="configForm.cyber_ban_threshold"
                    type="number"
                    min="1"
                    max="1000"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{
                    t('admin.riskControl.cyberViolationWindowHours')
                  }}</label>
                  <input
                    v-model.number="configForm.cyber_violation_window_hours"
                    type="number"
                    min="1"
                    max="8760"
                    class="input"
                  />
                </div>
              </div>
            </div>
          </div>

          <div v-else-if="activeSettingsTab === 'riskThresholds'" class="space-y-5">
            <div class="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
              <div>
                <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.riskThresholds')
                }}</h3>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{
                  t('admin.riskControl.riskThresholdsHint')
                }}</p>
              </div>
              <button
                type="button"
                class="btn btn-secondary inline-flex items-center justify-center gap-2"
                @click="resetRiskThresholds"
              >
                <Icon name="refresh" size="sm" />
                {{ t('admin.riskControl.riskThresholdReset') }}
              </button>
            </div>

            <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
              <div
                v-for="row in riskThresholdRows"
                :key="row.category"
                class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900/30"
              >
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0">
                    <label
                      class="block truncate text-sm font-semibold text-gray-900 dark:text-white"
                      :for="`risk-threshold-${row.category}`"
                    >
                      {{ row.category }}
                    </label>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      {{
                        t('admin.riskControl.riskThresholdDefault', {
                          value: formatThresholdPercent(row.defaultValue),
                        })
                      }}
                    </p>
                  </div>
                  <span
                    class="inline-flex shrink-0 rounded-compact bg-white px-2 py-1 font-mono text-xs font-medium text-gray-600 shadow-sm dark:bg-dark-800 dark:text-gray-300"
                  >
                    {{ formatThresholdPercent(row.value) }}
                  </span>
                </div>
                <div class="mt-3">
                  <label class="sr-only" :for="`risk-threshold-${row.category}`">
                    {{ t('admin.riskControl.riskThresholdPercent') }}
                  </label>
                  <div class="relative">
                    <input
                      :id="`risk-threshold-${row.category}`"
                      v-model.number="configForm.thresholds[row.category]"
                      :data-test="`risk-threshold-${row.category}`"
                      type="number"
                      min="0"
                      max="100"
                      step="0.1"
                      class="input pr-8 font-mono"
                    />
                    <span
                      class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-gray-400"
                      >%</span
                    >
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div v-else-if="activeSettingsTab === 'keywords'" class="space-y-5">
            <div
              class="flex items-start gap-3 rounded-control border p-4"
              :class="keywordNotice.toneClass"
            >
              <Icon :name="keywordNotice.icon" size="md" :class="keywordNotice.iconClass" />
              <div class="text-sm leading-6">
                <p class="font-medium" :class="keywordNotice.titleClass">{{
                  keywordNotice.title
                }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{
                  keywordNotice.description
                }}</p>
              </div>
            </div>

            <div class="space-y-2">
              <label class="input-label">{{ t('admin.riskControl.keywordBlockingMode') }}</label>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-3">
                <button
                  v-for="option in keywordBlockingModeOptions"
                  :key="option.value"
                  type="button"
                  class="rounded-control border p-3 text-left transition-colors"
                  :class="
                    configForm.keyword_blocking_mode === option.value
                      ? 'border-primary-300 bg-primary-50 text-primary-900 shadow-sm dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                      : 'border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60'
                  "
                  @click="configForm.keyword_blocking_mode = option.value"
                >
                  <div class="flex items-center justify-between gap-2">
                    <span class="text-sm font-semibold">{{ option.label }}</span>
                    <span
                      class="flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full border"
                      :class="
                        configForm.keyword_blocking_mode === option.value
                          ? 'border-primary-700 bg-primary-700 text-white dark:border-primary-600 dark:bg-primary-600'
                          : 'border-gray-300 text-transparent dark:border-dark-500'
                      "
                    >
                      <Icon name="check" size="xs" :stroke-width="2" :animate-on-hover="false" />
                    </span>
                  </div>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                    option.description
                  }}</p>
                </button>
              </div>
            </div>

            <div>
              <div class="mb-2 flex items-center justify-between">
                <label class="input-label mb-0">{{ t('admin.riskControl.blockedKeywords') }}</label>
                <span
                  class="inline-flex rounded-compact bg-gray-100 px-2 py-1 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-300"
                >
                  {{ t('admin.riskControl.blockedKeywordCount', { count: blockedKeywordCount }) }}
                </span>
              </div>
              <textarea
                v-model="configForm.blocked_keywords_text"
                class="input min-h-52 resize-y font-mono text-sm"
                :placeholder="t('admin.riskControl.blockedKeywordsPlaceholder')"
                :disabled="configForm.keyword_blocking_mode === 'api_only'"
              ></textarea>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.riskControl.blockedKeywordsLimit', { max: blockedKeywordMax }) }}
              </p>
            </div>
          </div>

          <div v-else class="grid grid-cols-1 gap-5 lg:grid-cols-2">
            <div>
              <label class="input-label">{{ t('admin.riskControl.hitRetentionDays') }}</label>
              <input
                v-model.number="configForm.hit_retention_days"
                type="number"
                min="1"
                max="3650"
                class="input"
              />
            </div>
            <div>
              <label class="input-label">{{ t('admin.riskControl.nonHitRetentionDays') }}</label>
              <input
                v-model.number="configForm.non_hit_retention_days"
                type="number"
                min="1"
                max="3"
                class="input"
              />
            </div>
            <div
              class="rounded-control border border-gray-100 p-4 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400 lg:col-span-2"
            >
              <div class="flex flex-wrap items-center gap-3">
                <Icon name="database" size="md" class="text-gray-400" />
                <span>{{
                  t('admin.riskControl.cleanupStats', {
                    hit: status?.last_cleanup_deleted_hit ?? 0,
                    nonHit: status?.last_cleanup_deleted_non_hit ?? 0,
                  })
                }}</span>
              </div>
            </div>
          </div>
        </div>

        <template #footer>
          <div class="flex justify-end gap-2">
            <button type="button" class="btn btn-secondary" @click="settingsOpen = false">{{
              t('common.cancel')
            }}</button>
            <button
              type="button"
              class="btn btn-primary inline-flex items-center gap-2"
              :disabled="saving"
              @click="saveConfig"
            >
              <Icon
                v-if="saving"
                name="refresh"
                size="sm"
                class="animate-spin"
                :animate-on-hover="false"
              />
              <Icon v-else name="check" size="sm" :animate-on-hover="false" />
              {{ saving ? t('common.saving') : t('admin.riskControl.saveConfig') }}
            </button>
          </div>
        </template>
      </BaseDialog>

      <BaseDialog
        :show="inputDetailRow !== null"
        :title="t('admin.riskControl.inputDetailTitle')"
        width="wide"
        @close="closeInputDetail"
      >
        <div v-if="inputDetailRow" class="space-y-5">
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.table.time')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
                formatDateTime(inputDetailRow.created_at)
              }}</p>
            </div>
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.table.user')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
                inputDetailRow.user_email || '-'
              }}</p>
              <p
                v-if="inputDetailRow.team_id"
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                data-test="moderation-detail-team-attribution"
              >
                {{
                  t('admin.riskControl.teamAttribution', {
                    teamId: inputDetailRow.team_id,
                    billingUserId: inputDetailRow.billing_user_id ?? '-',
                  })
                }}
              </p>
            </div>
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.table.result')
              }}</p>
              <span
                class="mt-1 inline-flex rounded-compact px-2 py-1 text-xs font-medium"
                :class="resultBadgeClass(inputDetailRow)"
              >
                {{ resultLabel(inputDetailRow) }}
              </span>
            </div>
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.table.highest')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">
                {{ inputDetailRow.highest_category || '-' }} /
                {{ percent(inputDetailRow.highest_score) }}
              </p>
            </div>
            <div
              v-if="inputDetailRow.matched_keyword"
              class="rounded-control border border-red-100 bg-red-50 p-4 dark:border-red-900/60 dark:bg-red-900/20"
            >
              <p class="text-xs font-medium text-red-500 dark:text-red-300">{{
                t('admin.riskControl.matchedKeyword')
              }}</p>
              <p
                class="mt-1 truncate text-sm font-semibold text-red-700 dark:text-red-200"
                :title="inputDetailRow.matched_keyword"
                >{{ inputDetailRow.matched_keyword }}</p
              >
            </div>
          </div>

          <ContentSkeleton v-if="inputDetailLoading" variant="article" :rows="2" class="py-4" />

          <div v-else class="space-y-4">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.inputDetailContent')
                }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ inputDetailRow.endpoint || '-' }} · {{ inputDetailRow.provider || '-' }} /
                  {{ inputDetailRow.model || '-' }}
                </p>
                <RequestIdLink
                  v-if="inputDetailRow.request_id"
                  :value="inputDetailRow.request_id"
                  class="mt-1.5"
                />
              </div>
              <span
                v-if="inputDetailRow.group_name"
                class="inline-flex rounded-compact bg-sky-50 px-2.5 py-1 text-xs font-medium text-sky-700 dark:bg-sky-900/20 dark:text-sky-300"
              >
                {{ inputDetailRow.group_name }}
              </span>
            </div>

            <div
              v-if="!inputDetailRow.content_complete"
              class="border-l-4 border-amber-400 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
            >
              {{ t('admin.riskControl.historicalContentTruncated') }}
            </div>
            <div
              v-if="inputDetailRow.audit_complete === false"
              class="border-l-4 border-red-400 bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-900/20 dark:text-red-200"
            >
              {{
                t('admin.riskControl.auditIncomplete', {
                  count: inputDetailRow.failed_unit_count || 0,
                })
              }}
            </div>

            <div
              v-if="inputDetailRow.input_items?.length"
              class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700"
            >
              <section v-for="item in inputDetailRow.input_items" :key="item.index" class="py-4">
                <div
                  class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase text-gray-500 dark:text-gray-400"
                >
                  <span>{{ detailSourceLabel(item.source) }}</span>
                  <span>·</span>
                  <span>{{ detailTypeLabel(item.type) }}</span>
                </div>
                <pre
                  class="max-h-panel overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 text-sm leading-6 text-gray-100"
                  >{{ detailItemContent(item) }}</pre>
              </section>
            </div>
            <pre
              v-else
              class="max-h-panel overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 text-sm leading-6 text-gray-100"
              >{{ inputDetailText }}</pre>

            <div v-if="inputDetailRow.media?.length" class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <figure
                v-for="media in inputDetailRow.media"
                :key="media.id"
                class="overflow-hidden border border-gray-200 dark:border-dark-700"
              >
                <img
                  v-if="mediaObjectURLs[media.id]"
                  :src="mediaObjectURLs[media.id]"
                  :alt="t('admin.riskControl.reviewImage')"
                  class="aspect-video w-full bg-gray-100 object-contain dark:bg-dark-900"
                />
                <div
                  v-else
                  class="flex aspect-video items-center justify-center bg-gray-100 px-4 text-center text-xs text-gray-500 dark:bg-dark-900 dark:text-gray-400"
                >
                  {{ media.snapshot_error || t('admin.riskControl.mediaUnavailable') }}
                </div>
                <figcaption
                  class="border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400"
                >
                  {{ detailSourceLabel(media.source) }} · {{ formatByteSize(media.byte_size) }}
                </figcaption>
              </figure>
            </div>

            <div v-if="inputDetailRow.failed_units?.length" class="space-y-2">
              <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                t('admin.riskControl.failedUnits')
              }}</p>
              <div
                v-for="unit in inputDetailRow.failed_units"
                :key="`${unit.type}-${unit.index}`"
                class="border-l-2 border-red-400 pl-3 text-sm text-gray-700 dark:text-gray-300"
              >
                {{ unit.type }}[{{ unit.index }}]: {{ unit.error }}
              </div>
            </div>
          </div>
        </div>

        <template #footer>
          <div class="flex justify-end">
            <button type="button" class="btn btn-secondary" @click="closeInputDetail">{{
              t('common.close')
            }}</button>
          </div>
        </template>
      </BaseDialog>

      <BaseDialog
        :show="cyberDetailRow !== null"
        :title="t('admin.riskControl.cyberDetailTitle')"
        width="wide"
        @close="closeCyberDetail"
      >
        <div v-if="cyberDetailRow" class="space-y-5">
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.table.time')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
                formatDateTime(cyberDetailRow.created_at)
              }}</p>
            </div>
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.table.user')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
                cyberDetailRow.user_email || '-'
              }}</p>
              <p
                v-if="cyberDetailRow.team_id"
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                data-test="cyber-detail-team-attribution"
              >
                {{
                  t('admin.riskControl.teamAttribution', {
                    teamId: cyberDetailRow.team_id,
                    billingUserId: cyberDetailRow.billing_user_id ?? '-',
                  })
                }}
              </p>
            </div>
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.cyberProvider')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white">{{
                cyberDetailRow.provider_name || '-'
              }}</p>
            </div>
            <div
              class="rounded-surface border border-gray-100 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/70"
            >
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{
                t('admin.riskControl.cyberStatus')
              }}</p>
              <p class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-white"
                >HTTP {{ cyberDetailRow.upstream_status || '-' }}</p
              >
            </div>
          </div>

          <ContentSkeleton v-if="cyberDetailLoading" variant="article" :rows="2" class="py-4" />

          <div v-else class="space-y-4">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.cyberPrompt')
                }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ cyberDetailRow.endpoint || '-' }} · openai / {{ cyberDetailRow.model || '-' }}
                </p>
                <RequestIdLink
                  v-if="cyberDetailRow.request_id"
                  :value="cyberDetailRow.request_id"
                  class="mt-1.5"
                />
              </div>
              <span
                v-if="cyberDetailRow.group_name"
                class="inline-flex rounded-compact bg-sky-50 px-2.5 py-1 text-xs font-medium text-sky-700 dark:bg-sky-900/20 dark:text-sky-300"
              >
                {{ cyberDetailRow.group_name }}
              </span>
            </div>
            <div
              v-if="!cyberDetailRow.content_complete"
              class="border-l-4 border-amber-400 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
            >
              {{ t('admin.riskControl.historicalContentTruncated') }}
            </div>
            <div
              v-if="cyberDetailRow.input_items?.length"
              class="divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700"
            >
              <section v-for="item in cyberDetailRow.input_items" :key="item.index" class="py-4">
                <div
                  class="mb-2 flex items-center gap-2 text-xs font-semibold uppercase text-gray-500 dark:text-gray-400"
                >
                  <span>{{ detailSourceLabel(item.source) }}</span>
                  <span>·</span>
                  <span>{{ detailTypeLabel(item.type) }}</span>
                </div>
                <pre
                  class="max-h-panel overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 text-sm leading-6 text-gray-100"
                  >{{ detailItemContent(item) }}</pre>
              </section>
            </div>
            <pre
              v-else
              class="max-h-menu overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-4 text-sm leading-6 text-gray-100"
              >{{ cyberPromptText }}</pre>

            <div v-if="cyberDetailRow.media?.length" class="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <figure
                v-for="media in cyberDetailRow.media"
                :key="media.id"
                class="overflow-hidden border border-gray-200 dark:border-dark-700"
              >
                <img
                  v-if="mediaObjectURLs[media.id]"
                  :src="mediaObjectURLs[media.id]"
                  :alt="t('admin.riskControl.reviewImage')"
                  class="aspect-video w-full bg-gray-100 object-contain dark:bg-dark-900"
                />
                <div
                  v-else
                  class="flex aspect-video items-center justify-center bg-gray-100 px-4 text-center text-xs text-gray-500 dark:bg-dark-900 dark:text-gray-400"
                >
                  {{ media.snapshot_error || t('admin.riskControl.mediaUnavailable') }}
                </div>
                <figcaption
                  class="border-t border-gray-200 px-3 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400"
                >
                  {{ detailSourceLabel(media.source) }} · {{ formatByteSize(media.byte_size) }}
                </figcaption>
              </figure>
            </div>
          </div>

          <div
            v-if="!cyberDetailLoading"
            class="border-t border-gray-200 pt-4 dark:border-dark-700"
          >
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p class="text-sm font-semibold text-gray-900 dark:text-white">{{
                  t('admin.riskControl.cyberWarning')
                }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ cyberDetailRow.endpoint || '-' }} · openai / {{ cyberDetailRow.model || '-' }}
                </p>
              </div>
              <span
                v-if="cyberDetailRow.group_name"
                class="inline-flex rounded-compact bg-sky-50 px-2.5 py-1 text-xs font-medium text-sky-700 dark:bg-sky-900/20 dark:text-sky-300"
              >
                {{ cyberDetailRow.group_name }}
              </span>
            </div>
            <pre
              class="mt-4 max-h-panel overflow-auto whitespace-pre-wrap break-words rounded-control bg-gray-950 p-4 text-sm leading-6 text-gray-100 shadow-inner dark:bg-black/50"
              >{{ cyberDetailText }}</pre>
          </div>
        </div>

        <template #footer>
          <div class="flex justify-end">
            <button type="button" class="btn btn-secondary" @click="closeCyberDetail">{{
              t('common.close')
            }}</button>
          </div>
        </template>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import {
  createRiskControlDraft,
  buildRiskControlConfigRequest,
  normalizeAPIKeyPriority,
  parseApiKeyEntries,
  parseApiKeys,
  normalizeKeywordBlockingMode,
  normalizeModelFilter,
  blockedKeywordMax,
  riskThresholdsFromConfig,
  riskThresholdDefaults,
  riskThresholdCategories,
  clampPercent,
  parseBlockedKeywords,
} from './risk-control/configDraft'
import type { APIKeysWriteMode, APIKeyMetadataDraft } from './risk-control/configDraft'
import { useRiskControlDetails } from './risk-control/useRiskControlDetails'
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import { vSegmented } from '@/directives/segmented'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Pagination from '@/components/common/Pagination.vue'
import ModelWhitelistSelector from '@/components/provider/ModelWhitelistSelector.vue'
import ProxySelector from '@/components/admin/proxy/ProxySelector.vue'
import { adminAPI } from '@/api/admin'
import type {
  ContentModerationAPIKeyLoad,
  ContentModerationAPIKeyStatus,
  ContentModerationConfig,
  ContentModerationCyberWarning,
  ContentModerationInputItem,
  ContentModerationMedia,
  CyberSummary,
  ContentModerationLog,
  ContentModerationModelFilterType,
  ContentModerationRuntimeStatus,
  ContentModerationTestAuditResult,
  ModerationMode,
  KeywordBlockingMode,
} from '@/api/admin/riskControl'
import type { AdminGroup } from '@/api/groups.types'
import type { Proxy } from '@/api/admin/proxies.types'
import type { SelectOption } from '@/types/ui'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

/**
 * RiskControlView.vue presents moderation configuration, audit records, and
 * runtime health to administrators. Settings stay in a local draft until the
 * server accepts a save; status polling updates health rows while a separate
 * hash-indexed metadata draft keeps pending priority and note edits available.
 * The page applies a saved configuration before closing settings and refreshing
 * runtime status together with the active record list.
 *
 * Configuration conversion belongs to configDraft, which resolves clearing,
 * replacement, individual deletion, and metadata precedence before persistence.
 * useRiskControlDetails owns the shared detail generation and authenticated
 * media previews, including stale results and URL disposal. The page keeps its
 * two record lists, filters, pagination, and presentation helpers together so
 * tab changes and user actions continue through one visible orchestration flow.
 */

/** SettingsTab identifies the active panel inside the risk configuration dialog. */
type SettingsTab =
  'basic' | 'scope' | 'runtime' | 'response' | 'riskThresholds' | 'keywords' | 'retention'
/** RecordTab identifies which audit record list currently owns the shared pagination controls. */
type RecordTab = 'moderation' | 'cyber'
/** WorkerSlotState describes whether a displayed runtime worker is active, idle, or disabled. */
type WorkerSlotState = 'active' | 'idle' | 'disabled'
/** OverviewIcon restricts summary card icons to the supported risk overview symbols. */
type OverviewIcon = 'shield' | 'key' | 'users' | 'document'
/** OverviewItem contains labels, values, and styling for one risk summary card. */
type OverviewItem = {
  key: string // Stable card identity.
  label: string // Localized visible label.
  value: string // Formatted displayed value.
  meta: string // Supplementary card detail.
  icon: OverviewIcon // Supported overview icon.
  iconClass: string // Icon container colors.
  // badge and badgeClass describe the optional runtime badge together. The template
  // renders the badge only when its label is nonempty.
  badge?: string // Optional runtime state label.
  badgeClass?: string // Runtime state badge colors.
}
/** ModerationScoreRow pairs a returned category score with its effective test threshold. */
type ModerationScoreRow = {
  category: string // Moderation category identifier.
  score: number // Returned audit score ratio.
  threshold: number // Effective category threshold ratio.
  hit: boolean // Score meets category threshold.
}
/** KeywordNoticeView contains the explanatory notice selected by the keyword and moderation modes. */
type KeywordNoticeView = {
  icon: 'infoCircle' | 'exclamationTriangle' // Selected notice severity icon.
  toneClass: string // Notice container color classes.
  iconClass: string // Icon container colors.
  title: string // Localized notice title.
  description: string // Localized notice explanation.
  titleClass: string // Notice title color classes.
}
/** RiskThresholdRow pairs each editable category percentage with its configured default percentage. */
type RiskThresholdRow = {
  category: string // Moderation category identifier.
  value: number // Editable category threshold percentage.
  defaultValue: number // Default category percentage value.
}

// Audit image count and byte limits bound local test inputs; credential rows start
// collapsed to three entries.
const maxModerationTestImages = 1
const maxModerationTestImageSize = 8 * 1024 * 1024
const maxVisibleApiKeyRows: number = 3

// Translation and notification services supply labels and action feedback throughout
// this administrator page.
const { t } = useI18n()
const appStore = useAppStore()

// Request flags disable their corresponding page actions while each asynchronous
// operation is pending.
const loading = ref(true)
const saving = ref(false)
const logsLoading = ref(false)
const recordsError = ref('')

// recordsRequest identifies the latest record query across the moderation and cyber tabs.
let recordsRequest = 0
const statusLoading = ref(false)
const apiKeyTesting = ref(false)
const hashActionLoading = ref(false)
// unbanningUserID serializes user restoration across both record kinds and
// identifies which row shows progress.
const unbanningUserID = ref<number | null>(null)
// Dialog visibility, selected tabs, and text filters belong to this mounted page and
// drive its displayed controls.
const settingsOpen = ref(false)
const activeSettingsTab = ref<SettingsTab>('basic')
const activeRecordTab = ref<RecordTab>('moderation')
const groupSearch = ref('')
const flaggedHashInput = ref('')
// Loaded catalog choices, audit rows, summaries, and runtime status populate the
// current page instance.
const groups = ref<AdminGroup[]>([])
const proxies = ref<Proxy[]>([])
const logs = ref<ContentModerationLog[]>([])
const cyberWarnings = ref<ContentModerationCyberWarning[]>([])
const cyberSummary = ref<CyberSummary | null>(null)
const status = ref<ContentModerationRuntimeStatus | null>(null)
// Temporary credential test rows and queued deletion hashes describe unsaved
// credential actions in the settings dialog.
const testedApiKeyStatuses = ref<ContentModerationAPIKeyStatus[]>([])
const pendingDeleteApiKeyHashes = ref<string[]>([])
// apiKeyMetadataDrafts keeps edits by credential hash; runtime polling replaces
// health rows while these unsaved priority and note values remain available.
const apiKeyMetadataDrafts = reactive<Record<string, APIKeyMetadataDraft>>({})
// Credential expansion and local audit inputs control the settings test panel until
// it is reset or saved.
const apiKeyRowsExpanded = ref<boolean>(false)
const moderationTestPrompt = ref('')
const moderationTestImages = ref<string[]>([])
const moderationTestResult = ref<ContentModerationTestAuditResult | null>(null)
// The detail controller owns both dialogs and their shared media previews; page
// unmount disposes pending work and URLs.
const {
  inputDetailRow,
  cyberDetailRow,
  inputDetailLoading,
  cyberDetailLoading,
  mediaObjectURLs,
  openInputDetail,
  closeInputDetail,
  openCyberDetail,
  closeCyberDetail,
  dispose: disposeDetails,
} = useRiskControlDetails({
  getLog: (id) => adminAPI.riskControl.getLog(id),
  getCyberWarning: (id) => adminAPI.riskControl.getCyberWarning(id),
  getMediaContent: (id) => adminAPI.riskControl.getMediaContent(id),
  onError: (err) =>
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.detailLoadFailed'))),
})
// statusTimer belongs to this mounted page and is cleared when navigation unmounts the page.
let statusTimer: number | null = null

// The settings draft remains editable across tab changes and is replaced only after
// configuration load or save.
const configForm = reactive(createRiskControlDraft())

// Both record tabs use these pagination values; switching tabs resets the page while
// retaining its selected size.
const pagination = reactive({
  page: 1, // Current one-based page number.
  page_size: 20, // Requested rows per page.
  total: 0, // Matching record total count.
  pages: 1, // Server-reported page count.
})

// Record filters are converted to optional request fields, with cyber queries using
// the shared text and date subset.
const filters = reactive({
  request_id: '', // Exact request identifier filter.
  result: '', // Moderation result action filter.
  group_id: 0, // Selected moderation group identifier.
  endpoint: '', // Selected moderation endpoint path.
  search: '', // Free-text record search input.
  from: '', // Local start date input.
  to: '', // Local end date input.
})

// Settings navigation and mode choices recompute translated labels while retaining
// stable values used by the editor.
const settingsTabs = computed<
  Array<{
    id: SettingsTab // Stable settings panel identifier.
    label: string // Localized settings panel label.
  }>
>(() => [
  { id: 'basic', label: t('admin.riskControl.tabs.basic') },
  { id: 'scope', label: t('admin.riskControl.tabs.scope') },
  { id: 'runtime', label: t('admin.riskControl.tabs.runtime') },
  { id: 'response', label: t('admin.riskControl.tabs.response') },
  { id: 'riskThresholds', label: t('admin.riskControl.tabs.riskThresholds') },
  { id: 'keywords', label: t('admin.riskControl.tabs.keywords') },
  { id: 'retention', label: t('admin.riskControl.tabs.retention') },
])

const modeOptions = computed<SelectOption[]>(() => [
  { value: 'pre_block', label: t('admin.riskControl.modePreBlock') },
  { value: 'observe', label: t('admin.riskControl.modeObserve') },
  { value: 'off', label: t('admin.riskControl.modeOff') },
])

const keywordBlockingModeOptions = computed<
  Array<{
    value: KeywordBlockingMode // Selected keyword checking strategy.
    label: string // Localized keyword strategy label.
    description: string // Localized keyword strategy explanation.
  }>
>(() => [
  {
    value: 'keyword_and_api',
    label: t('admin.riskControl.keywordModeKeywordAndApi'),
    description: t('admin.riskControl.keywordModeKeywordAndApiDesc'),
  },
  {
    value: 'keyword_only',
    label: t('admin.riskControl.keywordModeKeywordOnly'),
    description: t('admin.riskControl.keywordModeKeywordOnlyDesc'),
  },
  {
    value: 'api_only',
    label: t('admin.riskControl.keywordModeApiOnly'),
    description: t('admin.riskControl.keywordModeApiOnlyDesc'),
  },
])

const modelFilterOptions = computed<
  Array<{
    value: ContentModerationModelFilterType // Selected model filtering mode.
    label: string // Localized model filter label.
    description: string // Localized model filter explanation.
  }>
>(() => [
  {
    value: 'all',
    label: t('admin.riskControl.modelFilterAll'),
    description: t('admin.riskControl.modelFilterAllDesc'),
  },
  {
    value: 'include',
    label: t('admin.riskControl.modelFilterInclude'),
    description: t('admin.riskControl.modelFilterIncludeDesc'),
  },
  {
    value: 'exclude',
    label: t('admin.riskControl.modelFilterExclude'),
    description: t('admin.riskControl.modelFilterExcludeDesc'),
  },
])

// Notice tones share visual classes across strategy descriptions, and the selected
// notice follows strategy and moderation mode.
const keywordNoticeTones = {
  info: {
    icon: 'infoCircle' as const,
    toneClass:
      'border-primary-100 bg-primary-50/60 dark:border-primary-900/40 dark:bg-primary-900/10',
    iconClass: 'mt-0.5 flex-shrink-0 text-primary-500 dark:text-primary-300',
    titleClass: 'text-primary-700 dark:text-primary-200',
  },
  warning: {
    icon: 'exclamationTriangle' as const,
    toneClass: 'border-amber-200 bg-amber-50/80 dark:border-amber-900/50 dark:bg-amber-900/10',
    iconClass: 'mt-0.5 flex-shrink-0 text-amber-500 dark:text-amber-300',
    titleClass: 'text-amber-700 dark:text-amber-200',
  },
}

const keywordNotice = computed<KeywordNoticeView>(() => {
  const strategy = configForm.keyword_blocking_mode
  if (strategy === 'api_only') {
    return {
      ...keywordNoticeTones.info,
      title: t('admin.riskControl.keywordModeApiOnlyNotice'),
      description: t('admin.riskControl.keywordModeApiOnlyDesc'),
    }
  }
  if (configForm.mode !== 'pre_block') {
    return {
      ...keywordNoticeTones.warning,
      title: t('admin.riskControl.blockedKeywordsModeWarning', {
        mode: modeLabel(configForm.mode),
      }),
      description: t('admin.riskControl.blockedKeywordsDescription'),
    }
  }
  if (strategy === 'keyword_only') {
    return {
      ...keywordNoticeTones.info,
      title: t('admin.riskControl.keywordModeKeywordOnlyNotice'),
      description: t('admin.riskControl.keywordModeKeywordOnlyDesc'),
    }
  }
  return {
    ...keywordNoticeTones.info,
    title: t('admin.riskControl.blockedKeywordsPreBlockHint'),
    description: t('admin.riskControl.blockedKeywordsDescription'),
  }
})

// Record filter choices retain distinct action values so keyword, hash, and standard
// blocks remain independently queryable.
const resultOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.riskControl.result.all') },
  { value: 'hit', label: t('admin.riskControl.result.hitNotBlocked') },
  // Each blocked action retains its own request filter value, allowing
  // administrators to inspect standard, keyword, and hash decisions separately.
  { value: 'block', label: t('admin.riskControl.result.blocked') },
  { value: 'keyword_block', label: t('admin.riskControl.result.keywordBlocked') },
  { value: 'hash_block', label: t('admin.riskControl.result.hashBlocked') },
  { value: 'pass', label: t('admin.riskControl.result.pass') },
  { value: 'error', label: t('admin.riskControl.result.error') },
])

const endpointOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.riskControl.filters.allEndpoints') },
  { value: '/v1/messages', label: '/v1/messages' },
  { value: '/v1/responses', label: '/v1/responses' },
  { value: '/v1/chat/completions', label: '/v1/chat/completions' },
  { value: '/v1beta/models', label: '/v1beta/models' },
  { value: '/v1/images/generations', label: '/v1/images/generations' },
  { value: '/v1/images/edits', label: '/v1/images/edits' },
])

const recordTabs = computed<
  Array<{
    id: RecordTab // Selected record list identifier.
    label: string // Localized record list label.
  }>
>(() => [
  { id: 'moderation', label: t('admin.riskControl.recordTabs.moderation') },
  { id: 'cyber', label: t('admin.riskControl.recordTabs.cyber') },
])

const groupFilterOptions = computed<SelectOption[]>(() => [
  { value: 0, label: t('admin.riskControl.filters.allGroups') },
  ...groups.value.map((group) => ({
    value: group.id,
    label: group.name,
  })),
])

// Scope summaries describe the draft group and model selections, with a short
// preview for selected model names.
const selectedGroupCount = computed(() => String(configForm.group_ids.length))

const modelFilterModelCount = computed(() => configForm.model_filter_models.length)

const modelFilterSummary = computed(() => {
  if (configForm.model_filter_type === 'include') {
    return t('admin.riskControl.modelFilterIncludeSummary', { count: modelFilterModelCount.value })
  }
  if (configForm.model_filter_type === 'exclude') {
    return t('admin.riskControl.modelFilterExcludeSummary', { count: modelFilterModelCount.value })
  }
  return t('admin.riskControl.modelFilterAllSummary')
})

// Keep the scope preview compact by showing six model names.
const modelFilterPreviewModels = computed(() => configForm.model_filter_models.slice(0, 6))

const hiddenModelFilterModelCount = computed(() =>
  Math.max(0, configForm.model_filter_models.length - modelFilterPreviewModels.value.length),
)

const filteredGroups = computed(() => {
  const keyword = groupSearch.value.trim().toLowerCase()
  if (!keyword) return groups.value
  return groups.value.filter((group) => {
    return group.name.toLowerCase().includes(keyword)
  })
})

// Credential and keyword summaries parse editor text using the same rules used to
// construct persistence requests.
const inputApiKeyCount = computed(() => parseApiKeys(configForm.api_keys_text).length)

const blockedKeywordList = computed(() => parseBlockedKeywords(configForm.blocked_keywords_text))

const blockedKeywordCount = computed(() => blockedKeywordList.value.length)

const pendingDeletedApiKeyCount = computed(() => pendingDeleteApiKeyHashes.value.length)

const effectiveStoredApiKeyCount = computed(() =>
  Math.max(0, configForm.api_key_count - pendingDeletedApiKeyCount.value),
)

const apiKeysPlaceholder = computed(() =>
  configForm.api_keys_mode === 'replace'
    ? t('admin.riskControl.apiKeysPlaceholderReplace')
    : t('admin.riskControl.apiKeysPlaceholder'),
)

const apiKeysModeHint = computed(() =>
  configForm.api_keys_mode === 'replace'
    ? t('admin.riskControl.apiKeysModeReplaceHint')
    : t('admin.riskControl.apiKeysModeAppendHint'),
)

// Audit test controls distinguish content checks from credential checks and validate
// the entered blocked-hash identifier.
const hasModerationAuditInput = computed(() => {
  return moderationTestPrompt.value.trim() !== '' || moderationTestImages.value.length > 0
})

const isFlaggedHashInputValid = computed(() =>
  /^[a-fA-F0-9]{64}$/.test(flaggedHashInput.value.trim()),
)

const storedApiKeyTestButtonText = computed(() => {
  if (apiKeyTesting.value) return t('admin.riskControl.testingApiKeys')
  if (hasModerationAuditInput.value) return t('admin.riskControl.testContentWithStoredApiKey')
  return t('admin.riskControl.testStoredApiKeys')
})

// Credential rows prefer nonempty runtime health results, append unsaved test
// results, and apply the local expansion state.
const savedApiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => {
  // An empty runtime list uses the last configuration rows so configured credentials
  // remain visible while runtime health is unavailable.
  const rows = status.value?.api_key_statuses?.length
    ? status.value.api_key_statuses
    : configForm.api_key_statuses
  return Array.isArray(rows) ? rows : []
})

const apiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => [
  ...savedApiKeyRows.value,
  ...testedApiKeyStatuses.value,
])

const visibleApiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => {
  if (apiKeyRowsExpanded.value) return apiKeyRows.value
  return apiKeyRows.value.slice(0, maxVisibleApiKeyRows)
})

const hiddenApiKeyRowCount = computed<number>(() =>
  Math.max(0, apiKeyRows.value.length - visibleApiKeyRows.value.length),
)

const canToggleApiKeyRows = computed<boolean>(() => apiKeyRows.value.length > maxVisibleApiKeyRows)

// Health summaries exclude queued removals and represent configured credentials with
// unavailable health rows as unknown.
const activeSavedApiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() =>
  savedApiKeyRows.value.filter((row) => !isStoredApiKeyPendingDelete(row)),
)

const apiKeyHealthBadges = computed<
  Array<{
    status: ContentModerationAPIKeyStatus['status'] // Credential health status category.
    count: number // Effective credentials in category.
  }>
>(() => {
  const counts: Record<ContentModerationAPIKeyStatus['status'], number> = {
    ok: 0,
    error: 0,
    frozen: 0,
    unknown: 0,
  }
  for (const row of activeSavedApiKeyRows.value) {
    counts[row.status] = (counts[row.status] ?? 0) + 1
  }
  if (activeSavedApiKeyRows.value.length === 0 && effectiveStoredApiKeyCount.value > 0) {
    // Configuration can report credentials before health rows are available; show
    // their effective count as unknown after accounting for pending deletions.
    counts.unknown = effectiveStoredApiKeyCount.value
  }
  return (['ok', 'frozen', 'error', 'unknown'] as Array<ContentModerationAPIKeyStatus['status']>)
    .map((item) => ({ status: item, count: counts[item] }))
    .filter((item) => item.count > 0)
})

const apiKeyHealthSummary = computed(() => {
  if (!configForm.api_key_configured) return ''
  if (apiKeyHealthBadges.value.length === 0) return t('admin.riskControl.apiKeyStatusUnknown')
  return apiKeyHealthBadges.value
    .map((badge) => `${apiKeyStatusLabel(badge.status)} ${badge.count}`)
    .join(' · ')
})

// Overview cards combine draft configuration, runtime health, and the selected
// record count for the page header.
const overviewItems = computed<OverviewItem[]>(() => [
  {
    key: 'status',
    label: t('admin.riskControl.overview.status'),
    value: configForm.enabled
      ? t('admin.riskControl.overview.enabled')
      : t('admin.riskControl.overview.disabled'),
    meta: modeLabel(configForm.mode),
    icon: 'shield',
    iconClass: configForm.enabled
      ? 'bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-300'
      : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400',
    badge: runtimeBadgeText.value,
    badgeClass: runtimeBadgeClass.value,
  },
  {
    key: 'api-key',
    label: t('admin.riskControl.overview.apiKey'),
    value: configForm.api_key_configured
      ? t('admin.riskControl.apiKeyCount', { count: configForm.api_key_count })
      : t('admin.riskControl.notConfigured'),
    meta: configForm.api_key_configured
      ? apiKeyHealthSummary.value || configForm.model || '-'
      : configForm.model || '-',
    icon: 'key',
    iconClass: 'bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-300',
  },
  {
    key: 'scope',
    label: t('admin.riskControl.overview.groupScope'),
    value: configForm.all_groups ? t('admin.riskControl.allGroups') : selectedGroupCount.value,
    meta: modelFilterSummary.value,
    icon: 'users',
    iconClass: 'bg-violet-50 text-violet-600 dark:bg-violet-900/20 dark:text-violet-300',
  },
  {
    key: 'logs',
    label: t('admin.riskControl.overview.logs'),
    value:
      activeRecordTab.value === 'cyber'
        ? cyberSummary.value
          ? formatNumber(cyberSummary.value.events)
          : '—'
        : formatNumber(pagination.total),
    meta:
      activeRecordTab.value === 'cyber'
        ? cyberSummary.value
          ? t('admin.riskControl.cyberSummaryMeta', {
              requests: formatNumber(cyberSummary.value.requests),
              users: formatNumber(cyberSummary.value.users),
              providers: formatNumber(cyberSummary.value.providers),
            })
          : ''
        : t('admin.riskControl.overview.currentFilter'),
    icon: 'document',
    iconClass: 'bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-300',
  },
])

// Test scores sort by descending ratio and compare against returned thresholds,
// using one for an absent category threshold.
const moderationScoreRows = computed<ModerationScoreRow[]>(() => {
  const result = moderationTestResult.value
  if (!result) return []
  return Object.entries(result.category_scores || {})
    .map(([category, score]) => {
      const threshold = result.thresholds?.[category] ?? 1
      return {
        category,
        score,
        threshold,
        hit: score >= threshold,
      }
    })
    .sort((a, b) => b.score - a.score)
})

// Threshold controls pair each known category with its edited percentage and the
// reset value shown beside it.
const riskThresholdRows = computed<RiskThresholdRow[]>(() =>
  riskThresholdCategories.map((category) => ({
    category,
    value: configForm.thresholds[category] ?? riskThresholdDefaults[category],
    defaultValue: riskThresholdDefaults[category],
  })),
)

// Detail summary text keeps moderation excerpts, upstream warnings, and submitted
// cyber prompts in separate visible sections.
const inputDetailText = computed(() => {
  if (!inputDetailRow.value) return '-'
  return inputDetailRow.value.input_excerpt || inputDetailRow.value.error || '-'
})

const cyberDetailText = computed(() => {
  if (!cyberDetailRow.value) return '-'
  return cyberDetailRow.value.warning_text || '-'
})

// Cyber prompts and upstream warning text are separate fields because the submitted
// input and refusal explain different parts of the record.
const cyberPromptText = computed(() => {
  if (!cyberDetailRow.value) return '-'
  return cyberDetailRow.value.prompt_excerpt || '-'
})

// Runtime cards derive bounded queue widths, mode visibility, worker counts, and
// per-credential call load from current status.
const queueUsagePercent = computed(
  () => `${Math.min(100, Math.max(0, status.value?.queue_usage_percent ?? 0)).toFixed(1)}%`,
)

const queueUsageStyle = computed(() => ({
  width: queueUsagePercent.value,
}))

const runtimeMode = computed<ModerationMode>(() => status.value?.mode ?? configForm.mode)

const showPreBlockRuntimeCard = computed(() => runtimeMode.value === 'pre_block')

const showWorkerRuntimeCard = computed(() => runtimeMode.value === 'observe')

const preBlockMetricItems = computed(() => [
  {
    key: 'active',
    label: t('admin.riskControl.preBlockActive'),
    value: formatNumber(status.value?.pre_block_active ?? 0),
    meta: t('admin.riskControl.preBlockActiveHint'),
    class: 'bg-sky-50 dark:bg-sky-900/10',
    valueClass: 'text-sky-700 dark:text-sky-300',
  },
  {
    key: 'checked',
    label: t('admin.riskControl.preBlockChecked'),
    value: formatNumber(status.value?.pre_block_checked ?? 0),
    meta: t('admin.riskControl.preBlockCheckedHint'),
    class: 'bg-gray-50 dark:bg-dark-700/50',
    valueClass: 'text-gray-900 dark:text-white',
  },
  {
    key: 'allowed',
    label: t('admin.riskControl.preBlockAllowed'),
    value: formatNumber(status.value?.pre_block_allowed ?? 0),
    meta: t('admin.riskControl.preBlockAllowedHint'),
    class: 'bg-emerald-50 dark:bg-emerald-900/10',
    valueClass: 'text-emerald-700 dark:text-emerald-300',
  },
  {
    key: 'blocked',
    label: t('admin.riskControl.preBlockBlocked'),
    value: formatNumber(status.value?.pre_block_blocked ?? 0),
    meta: t('admin.riskControl.preBlockBlockedHint'),
    class: 'bg-rose-50 dark:bg-rose-900/10',
    valueClass: 'text-rose-700 dark:text-rose-300',
  },
  {
    key: 'errors',
    label: t('admin.riskControl.preBlockErrors'),
    value: formatNumber(status.value?.pre_block_errors ?? 0),
    meta: t('admin.riskControl.preBlockErrorsHint'),
    class: 'bg-amber-50 dark:bg-amber-900/10',
    valueClass: 'text-amber-700 dark:text-amber-300',
  },
  {
    key: 'latency',
    label: t('admin.riskControl.preBlockAvgLatency'),
    value: `${formatNumber(status.value?.pre_block_avg_latency_ms ?? 0)} ms`,
    meta: t('admin.riskControl.preBlockAvgLatencyHint'),
    class: 'bg-violet-50 dark:bg-violet-900/10',
    valueClass: 'text-violet-700 dark:text-violet-300',
  },
])

const preBlockAPIKeyLoads = computed<ContentModerationAPIKeyLoad[]>(() =>
  [...(status.value?.pre_block_api_key_loads ?? [])].sort((a, b) => a.index - b.index),
)

const preBlockAPIKeyMaxTotal = computed(() =>
  Math.max(1, ...preBlockAPIKeyLoads.value.map((item) => item.total || 0)),
)

const preBlockAPIKeyLoadSummaryText = computed(() =>
  t('admin.riskControl.preBlockAPIKeyLoadSummary', {
    active: formatNumber(status.value?.pre_block_api_key_active ?? 0),
    available: formatNumber(status.value?.pre_block_api_key_available_count ?? 0),
    total: formatNumber(status.value?.pre_block_api_key_total_calls ?? 0),
    workerActive: formatNumber(status.value?.active_workers ?? 0),
    workerTotal: formatNumber(status.value?.worker_count ?? configForm.worker_count),
  }),
)

/**
 * preBlockAPIKeyLoadWidth formats one credential's share of the largest call count.
 * The result is bounded to a percentage width, and the denominator remains at
 * least one even when all credentials have zero calls.
 */
function preBlockAPIKeyLoadWidth(total: number): string {
  return `${Math.min(100, Math.max(0, (total / preBlockAPIKeyMaxTotal.value) * 100)).toFixed(1)}%`
}

// Worker slots combine runtime enablement and active counts, while status badges
// also reflect the editable configuration switch.
const workerSlots = computed(() => {
  const total = Math.max(0, status.value?.worker_count ?? configForm.worker_count)
  const active = Math.max(0, status.value?.active_workers ?? 0)
  const enabled = Boolean(
    status.value?.risk_control_enabled && status.value?.enabled && status.value?.mode !== 'off',
  )
  return Array.from({ length: total }, (_, index) => ({
    id: index + 1,
    state: (!enabled ? 'disabled' : index < active ? 'active' : 'idle') as WorkerSlotState,
    label: !enabled
      ? t('admin.riskControl.workerDisabled')
      : index < active
        ? t('admin.riskControl.workerActive')
        : t('admin.riskControl.workerIdle'),
  }))
})

const runtimeBadgeText = computed(() => {
  if (!status.value?.risk_control_enabled) return t('admin.riskControl.riskSwitchOff')
  if (!configForm.enabled || configForm.mode === 'off')
    return t('admin.riskControl.overview.disabled')
  return t('admin.riskControl.overview.enabled')
})

const runtimeBadgeClass = computed(() => {
  if (!status.value?.risk_control_enabled || !configForm.enabled || configForm.mode === 'off') {
    return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
  }
  return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300'
})

/**
 * applyConfig replaces the editable settings with a saved configuration and
 * resets credential operations that belong to the preceding draft. The caller
 * uses it after initial loading or a successful save, so unsaved secret text,
 * pending deletions, test results, and metadata edits are intentionally reset.
 *
 * Response defaults distinguish nullish numeric values from values treated as
 * unset by individual controls. Threshold ratios become percentages, model scope
 * normalization supplies supported modes, and stored credential arrays are copied
 * before runtime polling may replace their health status.
 */
function applyConfig(config: ContentModerationConfig) {
  configForm.enabled = config.enabled
  configForm.mode = config.mode
  configForm.base_url = config.base_url || 'https://api.openai.com'
  configForm.model = config.model || 'omni-moderation-latest'
  configForm.proxy_id = config.proxy_id || null
  configForm.api_keys_text = ''
  configForm.api_key_configured = config.api_key_configured
  configForm.api_key_masked = config.api_key_masked || ''
  configForm.api_key_count = config.api_key_count || 0
  configForm.api_key_masks = Array.isArray(config.api_key_masks) ? [...config.api_key_masks] : []
  configForm.api_key_statuses = Array.isArray(config.api_key_statuses)
    ? [...config.api_key_statuses]
    : []
  // Applying saved configuration replaces the editing session; reset metadata here,
  // while runtime-only polling leaves these drafts untouched.
  resetAPIKeyMetadataDrafts(configForm.api_key_statuses)
  configForm.api_keys_mode = 'append'
  configForm.clear_api_key = false
  pendingDeleteApiKeyHashes.value = []
  testedApiKeyStatuses.value = []
  apiKeyRowsExpanded.value = false
  configForm.timeout_ms = config.timeout_ms || 3000
  configForm.retry_count = config.retry_count ?? 2
  configForm.sample_rate = config.sample_rate ?? 100
  configForm.all_groups = config.all_groups
  configForm.group_ids = Array.isArray(config.group_ids) ? [...config.group_ids] : []
  configForm.record_non_hits = config.record_non_hits
  configForm.worker_count = config.worker_count || 4
  configForm.queue_size = config.queue_size || 32768
  configForm.block_status = config.block_status || 403
  configForm.block_message =
    config.block_message ||
    "This request was blocked by the site's content policy. Revise your input and try again."
  configForm.email_on_hit = config.email_on_hit ?? true
  configForm.auto_ban_enabled = config.auto_ban_enabled ?? true
  configForm.ban_threshold = config.ban_threshold || 10
  configForm.violation_window_hours = config.violation_window_hours || 720
  configForm.cyber_warning_enabled = config.cyber_warning_enabled ?? true
  configForm.cyber_auto_ban_enabled = config.cyber_auto_ban_enabled ?? false
  configForm.cyber_ban_threshold = config.cyber_ban_threshold || 10
  configForm.cyber_violation_window_hours = config.cyber_violation_window_hours || 720
  configForm.hit_retention_days = config.hit_retention_days || 180
  // Non-hit retention is restricted to one through three days.
  configForm.non_hit_retention_days = Math.min(Math.max(config.non_hit_retention_days || 3, 1), 3)
  configForm.pre_hash_check_enabled = config.pre_hash_check_enabled ?? false
  configForm.thresholds = riskThresholdsFromConfig(config.thresholds)
  configForm.blocked_keywords_text = Array.isArray(config.blocked_keywords)
    ? config.blocked_keywords.join('\n')
    : ''
  configForm.keyword_blocking_mode = normalizeKeywordBlockingMode(config.keyword_blocking_mode)
  const modelFilter = normalizeModelFilter(config.model_filter)
  configForm.model_filter_type = modelFilter.type
  configForm.model_filter_models = modelFilter.models
  configForm.audit_user_text_max_chars = config.audit_user_text_max_chars || 1000000
  configForm.audit_images = config.audit_images ?? true
  configForm.audit_tool_outputs = config.audit_tool_outputs ?? true
  configForm.audit_tool_output_max_chars = config.audit_tool_output_max_chars || 1000000
}

/**
 * loadAll initializes settings, group choices, runtime status, proxies, and records.
 * Proxy lookup failures leave an empty selector while other required requests
 * continue; failed required requests show the page's load error.
 */
async function loadAll() {
  loading.value = true
  try {
    const [config, groupItems, runtimeStatus, proxyItems] = await Promise.all([
      adminAPI.riskControl.getConfig(),
      adminAPI.groups.getAll(),
      adminAPI.riskControl.getStatus(),
      // Proxy choices are optional during initialization; keep loading moderation
      // settings and records when this catalog request fails independently.
      adminAPI.proxies.getAll().catch(() => [] as Proxy[]),
    ])
    applyConfig(config)
    groups.value = groupItems
    status.value = runtimeStatus
    proxies.value = proxyItems
    if (Array.isArray(runtimeStatus.api_key_statuses)) {
      configForm.api_key_statuses = [...runtimeStatus.api_key_statuses]
      prunePendingDeleteAPIKeyHashes()
    }
    await loadRecords()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.loadFailed')))
  } finally {
    loading.value = false
  }
}

/**
 * loadStatus refreshes runtime metrics and stored credential health rows.
 * Silent polling suppresses error notifications, while manual refresh reports
 * failures; existing status remains visible until a successful response replaces it.
 */
async function loadStatus(silent = true) {
  statusLoading.value = true
  try {
    const runtimeStatus = await adminAPI.riskControl.getStatus()
    status.value = runtimeStatus
    if (Array.isArray(runtimeStatus.api_key_statuses)) {
      configForm.api_key_statuses = [...runtimeStatus.api_key_statuses]
      prunePendingDeleteAPIKeyHashes()
    }
  } catch (err: unknown) {
    // Automatic polling retains the last successful status silently; manual refresh
    // reports failures because the administrator requested immediate feedback.
    if (!silent) {
      appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.statusFailed')))
    }
  } finally {
    statusLoading.value = false
  }
}

/**
 * saveConfig validates the current editor and persists its request through the
 * risk-control API. Validation errors leave the dialog open and send no request.
 * After success, the returned configuration replaces the draft before the dialog
 * closes and a success notification appears.
 *
 * Runtime status and the active records then refresh together using their own
 * error reporting. The saving flag spans validation, persistence, and these
 * refreshes, and clears in finally even when validation returns early or a request
 * fails. The conversion module determines field normalization and credential
 * operation precedence before persistence starts.
 */
async function saveConfig() {
  saving.value = true
  try {
    const result = buildRiskControlConfigRequest(
      configForm,
      pendingDeleteApiKeyHashes.value,
      apiKeyMetadataDrafts,
    )
    if ('error' in result) {
      appStore.showError(t(result.error))
      return
    }
    const payload = result.payload

    // Persist before replacing drafts and closing settings; failed saves keep the
    // editor values available for correction or another attempt.
    const updated = await adminAPI.riskControl.updateConfig(payload)
    applyConfig(updated)
    settingsOpen.value = false
    appStore.showSuccess(t('admin.riskControl.saved'))
    await Promise.all([loadStatus(true), loadRecords()])
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.saveFailed')))
  } finally {
    saving.value = false
  }
}

/**
 * loadRecords requests the selected tab's records and publishes their pagination.
 * A later request takes ownership of the table, its error, and loading indicator,
 * even when the two requests belong to different tabs. Cyber warnings and their
 * summary publish together so the counters describe the same query as the rows.
 * Failed reads do not publish a successful empty result.
 */
async function loadRecords() {
  // Claim the shared table state before dispatching so a completion from
  // another tab cannot clear the indicator or replace the selected page's totals.
  const request = ++recordsRequest
  const tab = activeRecordTab.value
  logsLoading.value = true
  recordsError.value = ''
  const params = {
    page: pagination.page,
    page_size: pagination.page_size,
    request_id: filters.request_id || undefined,
    search: filters.search || undefined,
    from: normalizeDateTimeLocal(filters.from),
    to: normalizeDateTimeLocal(filters.to),
  }
  try {
    if (tab === 'cyber') {
      const [result, summary] = await Promise.all([
        adminAPI.riskControl.listCyberWarnings(params),
        adminAPI.riskControl.getCyberSummary(params),
      ])
      if (request !== recordsRequest) return
      cyberWarnings.value = result.items
      cyberSummary.value = summary
      Object.assign(pagination, {
        total: result.total,
        page: result.page,
        page_size: result.page_size,
        pages: result.pages,
      })
      return
    }
    const result = await adminAPI.riskControl.listLogs({
      ...params,
      result: filters.result || undefined,
      group_id: filters.group_id || undefined,
      endpoint: filters.endpoint || undefined,
    })
    if (request !== recordsRequest) return
    logs.value = result.items
    Object.assign(pagination, {
      total: result.total,
      page: result.page,
      page_size: result.page_size,
      pages: result.pages,
    })
  } catch (err: unknown) {
    if (request !== recordsRequest) return
    recordsError.value = extractApiErrorMessage(
      err,
      t(tab === 'cyber' ? 'admin.riskControl.cyberFailed' : 'admin.riskControl.logsFailed'),
    )
  } finally {
    if (request === recordsRequest) logsLoading.value = false
  }
}

/**
 * canUnbanRow reports whether an automatically banned moderation user remains disabled.
 */
function canUnbanRow(row: ContentModerationLog): boolean {
  return Boolean(row.auto_banned && row.user_id && row.user_status === 'disabled')
}

/**
 * canUnbanCyberRow reports whether an automatically banned cyber user remains disabled.
 */
function canUnbanCyberRow(row: ContentModerationCyberWarning): boolean {
  return Boolean(row.auto_banned && row.user_id && row.user_status === 'disabled')
}

/**
 * inputSummaryText returns the moderation excerpt, then its error, then a dash.
 * The same priority keeps failed audit rows readable when no input excerpt was
 * stored with the record.
 */
function inputSummaryText(row: ContentModerationLog): string {
  return row.input_excerpt || row.error || '-'
}

/**
 * cyberSummaryText returns the prompt excerpt, warning text, or a dash.
 * The list favors the submitted prompt, while the detail dialog presents the
 * prompt and upstream warning in separate sections.
 */
function cyberSummaryText(row: ContentModerationCyberWarning): string {
  return row.prompt_excerpt || row.warning_text || '-'
}

/**
 * detailSourceLabel returns the localized user or tool audit source label.
 */
function detailSourceLabel(
  source: ContentModerationInputItem['source'] | ContentModerationMedia['source'],
): string {
  return source === 'tool' ? t('admin.riskControl.sourceTool') : t('admin.riskControl.sourceUser')
}

/**
 * detailTypeLabel returns the localized image or text input label.
 */
function detailTypeLabel(type: ContentModerationInputItem['type']): string {
  return type === 'image' ? t('admin.riskControl.typeImage') : t('admin.riskControl.typeText')
}

/**
 * detailItemContent selects the image reference or text for one input item.
 * An empty selected value displays a dash, allowing the detail list to retain the
 * item's source and type metadata.
 */
function detailItemContent(item: ContentModerationInputItem): string {
  return item.type === 'image' ? item.image_ref || '-' : item.text || '-'
}

/**
 * formatByteSize formats stored media size using bytes, kilobytes, or megabytes.
 * Nonpositive and nonfinite values display zero bytes, while larger units use one
 * decimal place with base-1024 conversion.
 */
function formatByteSize(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  // Media sizes use binary multiples for the displayed units.
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * unbanUser restores the selected moderation user's status through the admin API.
 * A shared pending user ID serializes unban actions across both record tabs;
 * success updates every displayed moderation row for that user.
 */
async function unbanUser(row: ContentModerationLog) {
  if (!row.user_id || unbanningUserID.value !== null) return
  unbanningUserID.value = row.user_id
  try {
    const result = await adminAPI.riskControl.unbanUser(row.user_id)
    // Publish the restored status only after the API succeeds, updating all visible
    // records attributed to the same affected user.
    logs.value = logs.value.map((item) => {
      if (item.user_id !== row.user_id) return item
      return { ...item, user_status: result.status }
    })
    appStore.showSuccess(t('admin.riskControl.unbanSuccess'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.unbanFailed')))
  } finally {
    unbanningUserID.value = null
  }
}

/**
 * unbanCyberUser restores the selected cyber user's status through the admin API.
 * A shared pending user ID serializes unban actions across both record tabs;
 * success updates every displayed cyber row for that user.
 */
async function unbanCyberUser(row: ContentModerationCyberWarning) {
  if (!row.user_id || unbanningUserID.value !== null) return
  unbanningUserID.value = row.user_id
  try {
    const result = await adminAPI.riskControl.unbanUser(row.user_id)
    // Publish the restored status only after the API succeeds, updating all visible
    // warnings attributed to the same affected user.
    cyberWarnings.value = cyberWarnings.value.map((item) => {
      if (item.user_id !== row.user_id) return item
      return { ...item, user_status: result.status }
    })
    appStore.showSuccess(t('admin.riskControl.unbanSuccess'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.unbanFailed')))
  } finally {
    unbanningUserID.value = null
  }
}

/**
 * deleteFlaggedHash removes the entered valid hash from the blocked set.
 * The pending flag serializes hash actions, and successful deletion clears the
 * input before refreshing runtime counts and displaying the result.
 */
async function deleteFlaggedHash() {
  if (!isFlaggedHashInputValid.value || hashActionLoading.value) return
  hashActionLoading.value = true
  try {
    const result = await adminAPI.riskControl.deleteFlaggedHash(flaggedHashInput.value)
    flaggedHashInput.value = ''
    await loadStatus(true)
    appStore.showSuccess(
      result.deleted
        ? t('admin.riskControl.flaggedHashDeleted')
        : t('admin.riskControl.flaggedHashNotFound'),
    )
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.flaggedHashDeleteFailed')))
  } finally {
    hashActionLoading.value = false
  }
}

/**
 * clearFlaggedHashes empties the stored blocked-hash set through the admin API.
 * The pending flag serializes hash actions, and successful clearing refreshes
 * runtime counts before displaying the number of deleted hashes.
 */
async function clearFlaggedHashes() {
  if (hashActionLoading.value) return
  const confirmed = window.confirm(t('admin.riskControl.clearFlaggedHashesConfirm'))
  if (!confirmed) return
  hashActionLoading.value = true
  try {
    const result = await adminAPI.riskControl.clearFlaggedHashes()
    await loadStatus(true)
    appStore.showSuccess(t('admin.riskControl.flaggedHashesCleared', { count: result.deleted }))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.flaggedHashesClearFailed')))
  } finally {
    hashActionLoading.value = false
  }
}

/**
 * openSettings opens the settings dialog on its basic configuration tab.
 */
function openSettings() {
  activeSettingsTab.value = 'basic'
  settingsOpen.value = true
}

/**
 * reloadLogsFromFirstPage resets pagination and refreshes the selected record tab.
 */
function reloadLogsFromFirstPage() {
  pagination.page = 1
  void loadRecords()
}

/**
 * onPageChange selects the requested page and refreshes the active records.
 */
function onPageChange(page: number) {
  pagination.page = page
  void loadRecords()
}

/**
 * onPageSizeChange selects a page size and refreshes records from page one.
 */
function onPageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  void loadRecords()
}

/**
 * switchRecordTab changes the visible record kind and loads its first page.
 * Selecting the already active tab leaves pagination and records untouched, so
 * repeated clicks do not issue another request.
 */
function switchRecordTab(tab: RecordTab) {
  if (activeRecordTab.value === tab) return
  activeRecordTab.value = tab
  // The outbound tab's pagination and summary cannot describe the incoming tab's first request.
  pagination.total = 0
  pagination.pages = 0
  pagination.page = 1
  void loadRecords()
}

/**
 * toggleClearApiKey toggles the pending removal of all stored credentials.
 * Enabling clear resets entered secrets, replacement mode, test rows, and individual
 * deletions so those visible operations agree with the selected action.
 */
function toggleClearApiKey() {
  configForm.clear_api_key = !configForm.clear_api_key
  if (configForm.clear_api_key) {
    configForm.api_keys_text = ''
    configForm.api_keys_mode = 'append'
    testedApiKeyStatuses.value = []
    pendingDeleteApiKeyHashes.value = []
  }
}

/**
 * setAPIKeysMode updates how entered credentials will be saved by the server.
 * Selecting replacement clears pending individual removals because a replacement
 * request defines the complete next collection of credentials.
 */
function setAPIKeysMode(mode: APIKeysWriteMode) {
  configForm.api_keys_mode = mode
  if (mode === 'replace') {
    pendingDeleteApiKeyHashes.value = []
  }
}

/**
 * setModelFilterType selects the model matching mode for the settings draft.
 * Choosing all models clears the selected names, so the editor preview and eventual
 * request describe the same unrestricted scope.
 */
function setModelFilterType(type: ContentModerationModelFilterType) {
  configForm.model_filter_type = type
  if (type === 'all') {
    configForm.model_filter_models = []
  }
}

/**
 * testApiKeys tests entered credentials or the stored credential pool with the
 * current provider, proxy, and optional audit input. Entered-key testing requires
 * at least one parsed key and associates returned rows with metadata from the
 * same input positions, marking those rows as unsaved.
 *
 * Stored-key testing merges health results into configured rows, clears temporary
 * test rows, and refreshes runtime status before notifying success. A content
 * audit can test only part of the pool, so mergeConfiguredAPIKeyStatuses keeps
 * untested stored rows when audit input is present. Failures retain the existing
 * rows and produce a localized error notification.
 */
async function testApiKeys(useInputKeys: boolean) {
  const inputEntries = useInputKeys ? parseApiKeyEntries(configForm.api_keys_text) : []
  const keys = inputEntries.map((item) => item.api_key)
  if (useInputKeys && keys.length === 0) {
    appStore.showError(t('admin.riskControl.apiKeyTestNoInput'))
    return
  }
  apiKeyTesting.value = true
  try {
    const result = await adminAPI.riskControl.testAPIKeys({
      api_keys: keys,
      base_url: configForm.base_url,
      model: configForm.model,
      timeout_ms: Number(configForm.timeout_ms) || 3000,
      // Test requests use the current proxy choice, including zero for direct
      // access, so testing follows the connection selected for saved audits.
      proxy_id: configForm.proxy_id ?? 0,
      prompt: moderationTestPrompt.value,
      images: moderationTestImages.value,
    })
    moderationTestResult.value = result.audit_result ?? null
    if (useInputKeys) {
      // Returned test rows follow submitted key order, so attach each entry's
      // priority and note before displaying it as unsaved.
      testedApiKeyStatuses.value = result.items.map((item, index) => ({
        ...item,
        configured: false,
        priority: inputEntries[index]?.priority ?? 100,
        note: inputEntries[index]?.note ?? '',
      }))
    } else {
      mergeConfiguredAPIKeyStatuses(result.items)
      testedApiKeyStatuses.value = []
      await loadStatus(true)
    }
    appStore.showSuccess(t('admin.riskControl.apiKeyTestDone', { count: result.items.length }))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.apiKeyTestFailed')))
  } finally {
    apiKeyTesting.value = false
  }
}

/**
 * mergeConfiguredAPIKeyStatuses applies stored-credential test results to the
 * configuration's health rows. A test with prompt or image input may return only
 * the keys involved in that audit, so existing rows are matched by key hash and
 * rows absent from the result remain available.
 *
 * When no audit input is present, the returned list replaces the configured list.
 * An empty configured list also accepts the result directly. This distinction
 * keeps subset audit results from removing unrelated credentials from the
 * settings display.
 */
function mergeConfiguredAPIKeyStatuses(items: ContentModerationAPIKeyStatus[]) {
  if (!hasModerationAuditInput.value || configForm.api_key_statuses.length === 0) {
    configForm.api_key_statuses = items
    return
  }
  // Content audits can return a subset of the stored pool; retain untested rows by
  // applying returned updates through credential hashes.
  const updates = new Map(items.map((item) => [item.key_hash, item]))
  configForm.api_key_statuses = configForm.api_key_statuses.map(
    (item) => updates.get(item.key_hash) ?? item,
  )
}

/**
 * toggleDeleteStoredApiKey toggles a configured credential's pending removal hash.
 * Unconfigured test rows and rows with empty hashes are ignored because the
 * persistence API identifies stored credentials by their hash.
 */
function toggleDeleteStoredApiKey(row: ContentModerationAPIKeyStatus) {
  if (!row.configured || !row.key_hash) return
  const index = pendingDeleteApiKeyHashes.value.indexOf(row.key_hash)
  if (index >= 0) {
    pendingDeleteApiKeyHashes.value.splice(index, 1)
    return
  }
  pendingDeleteApiKeyHashes.value.push(row.key_hash)
}

/**
 * isStoredApiKeyPendingDelete reports whether a configured row's hash is queued for removal.
 */
function isStoredApiKeyPendingDelete(row: ContentModerationAPIKeyStatus): boolean {
  return (
    row.configured && row.key_hash !== '' && pendingDeleteApiKeyHashes.value.includes(row.key_hash)
  )
}

/**
 * prunePendingDeleteAPIKeyHashes removes queued deletions absent from current saved rows.
 * Call it after replacing runtime credential status so obsolete pending hashes
 * do not remain counted in the editor's effective credential total.
 */
function prunePendingDeleteAPIKeyHashes() {
  const currentHashes = new Set(savedApiKeyRows.value.map((row) => row.key_hash).filter(Boolean))
  pendingDeleteApiKeyHashes.value = pendingDeleteApiKeyHashes.value.filter((hash) =>
    currentHashes.has(hash),
  )
}

/**
 * clearModerationTestInput clears the prompt, images, and displayed test result.
 */
function clearModerationTestInput() {
  moderationTestPrompt.value = ''
  moderationTestImages.value = []
  moderationTestResult.value = null
}

/**
 * removeModerationTestImage removes the indexed image from the audit test input.
 */
function removeModerationTestImage(index: number) {
  moderationTestImages.value.splice(index, 1)
}

/**
 * handleModerationImageUpload reads the selected image files into audit test input.
 * The file control clears after processing so selecting the same file again can
 * trigger another change event in the browser.
 */
async function handleModerationImageUpload(event: Event) {
  const input = event.target as HTMLInputElement
  await addModerationTestFiles(input.files)
  input.value = ''
}

/**
 * handleModerationImageDrop passes dropped files to the audit image input validator.
 */
async function handleModerationImageDrop(event: DragEvent) {
  await addModerationTestFiles(event.dataTransfer?.files ?? null)
}

/**
 * handleModerationImagePaste accepts clipboard image files for the audit test.
 * When images are present it suppresses the browser's paste action; clipboard
 * content with no image files follows the normal control behavior.
 */
async function handleModerationImagePaste(event: ClipboardEvent) {
  const files = Array.from(event.clipboardData?.files ?? []).filter((file) =>
    file.type.startsWith('image/'),
  )
  if (files.length === 0) return
  event.preventDefault()
  await addModerationTestFiles(files)
}

/**
 * addModerationTestFiles reads supported image files into the audit input list.
 * The count limit stops the batch, while oversized or unreadable files show an
 * error and let subsequent files continue through validation.
 */
async function addModerationTestFiles(files: FileList | File[] | null) {
  if (!files) return
  const items = Array.from(files).filter((file) => file.type.startsWith('image/'))
  for (const file of items) {
    // The audit input supports at most one selected image.
    if (moderationTestImages.value.length >= maxModerationTestImages) {
      appStore.showError(
        t('admin.riskControl.auditTestImageLimit', { count: maxModerationTestImages }),
      )
      return
    }
    // Reject individual images larger than eight binary megabytes.
    if (file.size > maxModerationTestImageSize) {
      appStore.showError(t('admin.riskControl.auditTestImageTooLarge'))
      continue
    }
    try {
      moderationTestImages.value.push(await fileToDataURL(file))
    } catch {
      // A failed local read leaves accepted images intact and permits later files in
      // this batch to pass their own validation.
      appStore.showError(t('admin.riskControl.auditTestImageReadFailed'))
    }
  }
}

/**
 * fileToDataURL reads a local file as an embedded data URL for audit input.
 * The promise resolves with the FileReader result and rejects with its error when
 * reading fails in the browser.
 */
function fileToDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

/**
 * toggleGroup toggles one group identifier in the editable moderation scope.
 */
function toggleGroup(groupID: number) {
  const index = configForm.group_ids.indexOf(groupID)
  if (index >= 0) {
    configForm.group_ids.splice(index, 1)
  } else {
    configForm.group_ids.push(groupID)
  }
}

/**
 * isGroupSelected reports whether the draft includes the supplied group identifier.
 */
function isGroupSelected(groupID: number): boolean {
  return configForm.group_ids.includes(groupID)
}

/**
 * modeLabel returns the localized label for a known moderation mode.
 * An unmatched value displays its raw mode string, keeping unexpected server
 * values visible in runtime cards and configuration summaries.
 */
function modeLabel(mode: ModerationMode): string {
  const found = modeOptions.value.find((option) => option.value === mode)
  return found?.label ?? mode
}

/**
 * modeDescription returns the localized explanation for a known moderation mode.
 * An unmatched value produces empty text because there is no matching explanation
 * in the page's configured mode descriptions.
 */
function modeDescription(mode: ModerationMode): string {
  const descriptions: Record<ModerationMode, string> = {
    pre_block: t('admin.riskControl.modePreBlockDesc'),
    observe: t('admin.riskControl.modeObserveDesc'),
    off: t('admin.riskControl.modeOffDesc'),
  }
  return descriptions[mode] ?? ''
}

/**
 * resultLabel describes a moderation row using action precedence before flagged state.
 * Keyword, hash, and standard blocks remain distinguishable; errors precede ordinary
 * hits, and unflagged rows use the pass label.
 */
function resultLabel(row: ContentModerationLog): string {
  if (row.action === 'keyword_block') return t('admin.riskControl.action.keywordBlock')
  if (row.action === 'hash_block') return t('admin.riskControl.action.hashBlock')
  if (row.action === 'block') return t('admin.riskControl.action.block')
  if (row.action === 'error' || row.error) return t('admin.riskControl.action.error')
  if (row.flagged) return t('admin.riskControl.result.hit')
  return t('admin.riskControl.result.pass')
}

/**
 * resultBadgeClass selects colors for the moderation row's effective action.
 * Blocked actions share red styling, errors use amber, ordinary hits use pink,
 * and remaining rows use the green pass styling.
 */
function resultBadgeClass(row: ContentModerationLog): string {
  if (row.action === 'keyword_block')
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (row.action === 'hash_block')
    return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (row.action === 'block') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (row.action === 'error' || row.error)
    return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  if (row.flagged) return 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-300'
  return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
}

/**
 * workerSlotClass returns the card colors for one displayed worker state.
 */
function workerSlotClass(state: WorkerSlotState): string {
  if (state === 'active') {
    return 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/60 dark:bg-sky-900/20 dark:text-sky-300'
  }
  if (state === 'idle') {
    return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-300'
  }
  return 'border-gray-100 bg-white text-gray-400 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-500'
}

/**
 * workerDotClass returns the indicator color for one displayed worker state.
 */
function workerDotClass(state: WorkerSlotState): string {
  if (state === 'active') return 'bg-sky-500'
  if (state === 'idle') return 'bg-emerald-500'
  return 'bg-gray-300 dark:bg-dark-500'
}

/**
 * percent formats an audit score ratio as a one-decimal percentage label.
 * Nonfinite scores display a dash, while finite values remain unclamped so the
 * label reflects the value returned by the audit.
 */
function percent(value: number): string {
  if (!Number.isFinite(value)) return '-'
  // Audit score ratios become one-decimal percentage labels.
  return `${(value * 100).toFixed(1)}%`
}

/**
 * percentWidth formats an audit score ratio as a bounded bar width.
 * Nonfinite scores use zero width, and finite ratios are constrained to the
 * zero-to-one-hundred percentage range supported by the bar.
 */
function percentWidth(value: number): string {
  if (!Number.isFinite(value)) return '0%'
  // Score ratios are clamped before becoming percentage bar widths.
  return `${Math.min(100, Math.max(0, value * 100)).toFixed(1)}%`
}

/**
 * latencyText formats a measured moderation latency with its millisecond unit.
 * Null and undefined measurements display a dash, while zero remains a measured
 * value that can be shown as zero milliseconds.
 */
function latencyText(value: number | null): string {
  if (value === null || value === undefined) return '-'
  return `${value} ms`
}

/**
 * apiKeyRowKey distinguishes saved and test rows using hash or row index.
 * When a row has no hash, its position supplies the suffix within that row kind
 * so the displayed collections can coexist.
 */
function apiKeyRowKey(row: ContentModerationAPIKeyStatus, index: number): string {
  return `${row.configured ? 'saved' : 'test'}-${row.key_hash || index}`
}

/**
 * apiKeyStatusLabel returns the localized label for a credential health status.
 * Unrecognized status values use the unknown label so the table can still render
 * a row with unexpected runtime data.
 */
function apiKeyStatusLabel(statusValue: ContentModerationAPIKeyStatus['status']): string {
  const labels: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: t('admin.riskControl.apiKeyStatusOk'),
    error: t('admin.riskControl.apiKeyStatusError'),
    frozen: t('admin.riskControl.apiKeyStatusFrozen'),
    unknown: t('admin.riskControl.apiKeyStatusUnknown'),
  }
  return labels[statusValue] ?? labels.unknown
}

/**
 * apiKeyStatusBadgeClass returns badge colors for a credential health status.
 * Unrecognized statuses use the unknown styling, matching the label used when
 * runtime data does not contain one of the supported statuses.
 */
function apiKeyStatusBadgeClass(statusValue: ContentModerationAPIKeyStatus['status']): string {
  const classes: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
    error: 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300',
    frozen: 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300',
    unknown: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
  }
  return classes[statusValue] ?? classes.unknown
}

/**
 * apiKeyStatusDotClass returns the dot color for a credential health status.
 * Unrecognized statuses use the gray unknown color, matching the status badge's
 * handling of values outside the supported status set.
 */
function apiKeyStatusDotClass(statusValue: ContentModerationAPIKeyStatus['status']): string {
  const classes: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: 'bg-emerald-500',
    error: 'bg-amber-500',
    frozen: 'bg-red-500',
    unknown: 'bg-gray-400',
  }
  return classes[statusValue] ?? classes.unknown
}

/**
 * apiKeyStatusMeta builds the credential health detail line for the settings table.
 * Positive latency and HTTP status appear when available; freeze expiry takes
 * precedence over last-check time, with untested text otherwise.
 */
function apiKeyStatusMeta(row: ContentModerationAPIKeyStatus): string {
  const parts: string[] = []
  parts.push(t('admin.riskControl.apiKeyFailureCount', { count: row.failure_count || 0 }))
  if (row.last_latency_ms > 0) {
    parts.push(t('admin.riskControl.apiKeyLatency', { ms: row.last_latency_ms }))
  }
  if (row.last_http_status > 0) {
    parts.push(t('admin.riskControl.apiKeyHTTPStatus', { status: row.last_http_status }))
  }
  if (row.frozen_until) {
    parts.push(t('admin.riskControl.apiKeyFrozenUntil', { time: formatDateTime(row.frozen_until) }))
  } else if (row.last_checked_at) {
    parts.push(
      t('admin.riskControl.apiKeyLastChecked', { time: formatDateTime(row.last_checked_at) }),
    )
  } else {
    parts.push(t('admin.riskControl.apiKeyNotTested'))
  }
  return parts.join(' / ')
}

/**
 * resetAPIKeyMetadataDrafts replaces editable metadata from configured credential rows.
 * Call it after applying saved configuration; runtime status polling intentionally
 * avoids this reset so pending priority and note edits remain available.
 */
function resetAPIKeyMetadataDrafts(rows: ContentModerationAPIKeyStatus[]) {
  for (const key of Object.keys(apiKeyMetadataDrafts)) {
    delete apiKeyMetadataDrafts[key]
  }
  for (const row of rows) {
    if (!row.configured || !row.key_hash) continue
    apiKeyMetadataDrafts[row.key_hash] = {
      priority: normalizeAPIKeyPriority(row.priority),
      note: row.note || '',
    }
  }
}

/**
 * ensureAPIKeyMetadataDraft returns a mutable metadata draft for the row's hash.
 * The first access initializes priority and note from the row, while later accesses
 * reuse the administrator's unsaved edits for that hash.
 */
function ensureAPIKeyMetadataDraft(row: ContentModerationAPIKeyStatus): APIKeyMetadataDraft {
  if (!apiKeyMetadataDrafts[row.key_hash]) {
    apiKeyMetadataDrafts[row.key_hash] = {
      priority: normalizeAPIKeyPriority(row.priority),
      note: row.note || '',
    }
  }
  return apiKeyMetadataDrafts[row.key_hash]
}

/**
 * apiKeyDraftPriority returns the stored row's editable priority by credential hash.
 * Access initializes a missing draft from the row, while later reads retain
 * the administrator's unsaved priority edits.
 */
function apiKeyDraftPriority(row: ContentModerationAPIKeyStatus): number {
  return ensureAPIKeyMetadataDraft(row).priority
}

/**
 * apiKeyDraftNote returns the stored row's editable note by credential hash.
 * Access initializes a missing draft from the row, while later reads retain
 * the administrator's unsaved annotation text.
 */
function apiKeyDraftNote(row: ContentModerationAPIKeyStatus): string {
  return ensureAPIKeyMetadataDraft(row).note
}

/**
 * setApiKeyDraftPriority normalizes the input value into the row's metadata draft.
 * The numeric conversion uses the shared priority defaults and limits, and the
 * resulting value remains unsaved until configuration submission.
 */
function setApiKeyDraftPriority(row: ContentModerationAPIKeyStatus, event: Event) {
  ensureAPIKeyMetadataDraft(row).priority = normalizeAPIKeyPriority(
    Number((event.target as HTMLInputElement).value),
  )
}

/**
 * setApiKeyDraftNote copies the note input into its credential metadata draft.
 * The draft is limited to two hundred characters immediately, and request
 * construction trims surrounding whitespace before saving the annotation.
 */
function setApiKeyDraftNote(row: ContentModerationAPIKeyStatus, event: Event) {
  // The metadata field stores at most two hundred characters.
  ensureAPIKeyMetadataDraft(row).note = (event.target as HTMLInputElement).value.slice(0, 200)
}

/**
 * resetRiskThresholds replaces edited category percentages with a fresh default map.
 */
function resetRiskThresholds() {
  configForm.thresholds = { ...riskThresholdDefaults }
}

/**
 * formatThresholdPercent formats a bounded threshold percentage with one decimal
 * place. Nonfinite input becomes zero through the shared clamp, so the threshold
 * label remains valid while its editor value is incomplete.
 */
function formatThresholdPercent(value: number): string {
  return `${clampPercent(value).toFixed(1)}%`
}

/**
 * violationCountText returns the localized count for a flagged moderation record.
 * An unflagged record displays a dash; flagged rows with an absent or zero count
 * use one as the displayed count.
 */
function violationCountText(row: ContentModerationLog): string {
  if (!row.flagged) return '-'
  return t('admin.riskControl.violationCount', { count: row.violation_count || 1 })
}

/**
 * normalizeDateTimeLocal converts a browser date input into an ISO timestamp.
 * Empty or invalid values return undefined so list requests omit those filters;
 * valid values use the Date parser's local-time interpretation.
 */
function normalizeDateTimeLocal(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return undefined
  return date.toISOString()
}

/**
 * formatDateTime formats stored timestamps with the shared date presentation helper.
 * An empty formatted result displays a dash so optional timestamps keep a visible
 * placeholder in audit rows and credential metadata.
 */
function formatDateTime(value: string): string {
  return formatDateTimeValue(value) || '-'
}

/**
 * formatNumber renders a numeric count using the browser's default number locale.
 */
function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value)
}

// Start initialization and periodic health refresh for this page instance; polling
// continues while settings or detail dialogs are open.
onMounted(() => {
  void loadAll()
  statusTimer = window.setInterval(() => {
    void loadStatus(true)
  }, 15000)
})

// Navigation invalidates pending detail generations and releases media before
// clearing the status timer, preventing previews from outliving this page.
onUnmounted(() => {
  recordsRequest += 1
  disposeDetails()
  if (statusTimer !== null) {
    window.clearInterval(statusTimer)
    statusTimer = null
  }
})
</script>
