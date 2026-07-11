<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">数据概览</h2>
        <div class="flex flex-wrap items-center gap-2 self-start sm:self-auto">
          <div ref="statsAutoRefreshDropdownRef" class="relative">
            <button
              ref="statsAutoRefreshButtonRef"
              class="btn btn-secondary btn-sm justify-center sm:min-w-[7.5rem]"
              :title="statsAutoRefreshTitle"
              @click="toggleStatsAutoRefreshDropdown"
            >
              <Icon name="refresh" size="sm" :class="statsAutoRefreshEnabled ? 'animate-spin' : ''" />
              <span class="ml-1">自动刷新</span>
            </button>
            <div
              v-if="statsAutoRefreshDropdownOpen"
              class="fixed z-50 w-56 origin-top-left overflow-hidden rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800"
              :style="statsAutoRefreshDropdownStyle"
            >
              <div class="p-2">
                <button
                  class="flex w-full items-center justify-between rounded-md px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700"
                  @click="setStatsAutoRefreshEnabled(!statsAutoRefreshEnabled)"
                >
                  <span>启用自动刷新</span>
                  <Icon v-if="statsAutoRefreshEnabled" name="check" size="sm" class="text-primary-500" />
                </button>
                <div class="my-1 border-t border-gray-100 dark:border-gray-700"></div>
                <button
                  v-for="seconds in statsAutoRefreshIntervals"
                  :key="seconds"
                  class="flex w-full items-center justify-between rounded-md px-3 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700"
                  @click="setStatsAutoRefreshInterval(seconds)"
                >
                  <span>{{ seconds }} 秒</span>
                  <Icon v-if="statsAutoRefreshIntervalSeconds === seconds" name="check" size="sm" class="text-primary-500" />
                </button>
              </div>
            </div>
          </div>
          <button class="btn btn-secondary btn-sm" :disabled="statsLoading || storageLimitLoading" title="刷新统计图表" @click="refreshStats">
            <Icon name="refresh" size="sm" :class="statsLoading ? 'animate-spin' : ''" />
            <span class="ml-1">刷新统计</span>
          </button>
        </div>
      </div>

      <div class="grid gap-4 md:grid-cols-4 xl:grid-cols-7">
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">Session 总数</p>
          <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatNumber(stats?.session_count) }}</p>
        </div>
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">完整</p>
          <p class="mt-2 text-2xl font-semibold text-emerald-600 dark:text-emerald-400">{{ formatNumber(stats?.complete_count) }}</p>
        </div>
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">部分完整</p>
          <p class="mt-2 text-2xl font-semibold text-amber-600 dark:text-amber-400">{{ formatNumber(stats?.partial_count) }}</p>
        </div>
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">无效</p>
          <p class="mt-2 text-2xl font-semibold text-red-600 dark:text-red-400">{{ formatNumber(stats?.invalid_count) }}</p>
        </div>
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">占用空间</p>
          <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatBytes(stats?.total_storage_bytes) }}</p>
        </div>
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">单 session 平均 token</p>
          <p class="mt-2 text-2xl font-semibold text-purple-600 dark:text-purple-400">
            {{ formatNumber(Math.round(stats?.avg_tokens_per_session || 0)) }}
          </p>
        </div>
        <div class="card p-4">
          <p class="text-xs text-gray-500 dark:text-gray-400">单 session 平均{{ balanceUnitName }}</p>
          <p class="mt-2 text-2xl font-semibold text-sky-600 dark:text-sky-400">
            {{ formatBalanceAmount(stats?.avg_actual_cost_per_session || 0, { fractionDigits: 4 }) }}
          </p>
        </div>
      </div>

      <div class="card overflow-hidden">
        <div class="border-b border-gray-200 p-4 dark:border-gray-700">
          <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
            <div>
              <div class="flex items-center gap-2">
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">采集 Worker 运行状态</h2>
                <span :class="['badge', captureWorkerBadgeClass]">{{ captureWorkerStatusText }}</span>
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">异步数据共享采集任务的队列与 Worker 池状态，配置保存后对新任务在线生效。</p>
            </div>
            <button class="btn btn-primary btn-sm" :disabled="savingCaptureRuntimeSettings" @click="saveCaptureRuntimeSettings">
              <Icon name="check" size="sm" class="mr-1" />
              保存运行配置
            </button>
          </div>
        </div>
        <div class="grid gap-4 p-4 xl:grid-cols-[minmax(0,0.9fr)_minmax(360px,1.1fr)]">
          <div class="space-y-4">
            <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
              <div class="space-y-4">
                <div>
                  <div class="mb-2 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
                    <span>采集队列占用</span>
                    <span>{{ captureWorkerQueueRatioText }}</span>
                  </div>
                  <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                    <div class="h-full rounded-full bg-sky-500 transition-all" :style="{ width: `${captureWorkerQueueProgress}%` }"></div>
                  </div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ captureWorkerQueueText }}
                  </p>
                </div>
                <div>
                  <div class="mb-2 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
                    <span>Flush 队列占用</span>
                    <span>{{ captureWorkerFlushQueueRatioText }}</span>
                  </div>
                  <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                    <div class="h-full rounded-full bg-cyan-500 transition-all" :style="{ width: `${captureWorkerFlushQueueProgress}%` }"></div>
                  </div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ captureWorkerFlushQueueText }}
                  </p>
                </div>
              </div>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">处理中</p>
                <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatNumber(captureWorkerRunningWorkers) }}</p>
              </div>
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">已完成</p>
                <p class="mt-2 text-2xl font-semibold text-emerald-600 dark:text-emerald-400">{{ formatNumber(captureWorkerCompletedTotal) }}</p>
              </div>
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">累计失败/超时</p>
                <p class="mt-2 text-2xl font-semibold text-amber-600 dark:text-amber-400">
                  {{ formatNumber(captureWorkerFailedTotal) }}/{{ formatNumber(captureWorkerTimeoutTotal) }}
                </p>
              </div>
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">丢弃</p>
                <p class="mt-2 text-2xl font-semibold text-red-600 dark:text-red-400">{{ formatNumber(captureWorkerDroppedTotal) }}</p>
              </div>
            </div>
            <p
              v-if="captureWorkerHistoryNoticeText"
              :class="['truncate text-xs', captureWorkerLastErrorActive ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400']"
              :title="captureWorkerLastErrorTitle"
            >
              {{ captureWorkerLastErrorPrefix }}：{{ captureWorkerHistoryNoticeText }}
            </p>
          </div>

          <div class="space-y-4">
            <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
              <div>
                <label class="input-label">Worker 数量</label>
                <input v-model="captureWorkerCountInput" type="number" min="1" :max="captureWorkerCountMax" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">队列大小</label>
                <input v-model="captureQueueSizeInput" type="number" min="1" :max="captureQueueSizeMax" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">Flush 队列大小</label>
                <input v-model="captureFlushQueueSizeInput" type="number" min="1" :max="captureFlushQueueSizeMax" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">任务超时（秒）</label>
                <input v-model="captureTimeoutInput" type="number" min="1" :max="captureTimeoutSecondsMax" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">压缩等级</label>
                <Select v-model="captureCompressionLevelInput" :options="captureCompressionLevelOptions" />
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">Worker 池</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    运行 {{ formatNumber(captureWorkerRunningWorkers) }} · 空闲 {{ formatNumber(captureWorkerAvailableWorkers) }} · 总计 {{ formatNumber(captureWorkerCount) }}
                  </p>
                </div>
                <span v-if="captureWorkerHiddenCount > 0" class="badge badge-gray">+{{ formatNumber(captureWorkerHiddenCount) }}</span>
              </div>
              <div class="mb-3 flex flex-wrap gap-x-4 gap-y-2 text-xs text-gray-500 dark:text-gray-400">
                <span class="inline-flex items-center gap-1.5">
                  <span class="h-2.5 w-2.5 rounded-full bg-sky-500"></span>
                  采集任务
                </span>
                <span class="inline-flex items-center gap-1.5">
                  <span class="h-2.5 w-2.5 rounded-full bg-violet-500"></span>
                  Flush 落库
                </span>
                <span class="inline-flex items-center gap-1.5">
                  <span class="h-2.5 w-2.5 rounded-full bg-emerald-500"></span>
                  空闲
                </span>
              </div>
              <div class="grid grid-cols-2 gap-2 sm:grid-cols-4 md:grid-cols-6 xl:grid-cols-8">
                <div
                  v-for="worker in captureWorkerSlots"
                  :key="worker.id"
                  class="flex h-11 items-center justify-between rounded-lg border px-3 transition-colors"
                  :class="captureWorkerSlotClass(worker.state, worker.jobKind)"
                  :title="worker.label"
                >
                  <span class="text-sm font-semibold">#{{ worker.id }}</span>
                  <span class="h-2.5 w-2.5 rounded-full" :class="captureWorkerDotClass(worker.state, worker.jobKind)"></span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="card overflow-hidden">
        <div class="border-b border-gray-200 p-4 dark:border-gray-700">
          <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
            <div>
              <div class="flex items-center gap-2">
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">采集缓冲池状态</h2>
                <span :class="['badge', captureBufferBadgeClass]">{{ captureBufferStatusText }}</span>
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">热点 session 会先在内存中聚合，空闲后再合并压缩落库。</p>
            </div>
            <button class="btn btn-primary btn-sm" :disabled="savingCaptureRuntimeSettings" @click="saveCaptureRuntimeSettings">
              <Icon name="check" size="sm" class="mr-1" />
              保存运行配置
            </button>
          </div>
        </div>
        <div class="grid gap-4 p-4 xl:grid-cols-[minmax(0,0.95fr)_minmax(360px,1.05fr)]">
          <div class="space-y-4">
            <div class="rounded-lg border border-gray-200 p-4 dark:border-gray-700">
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium text-gray-900 dark:text-white">缓冲池容量</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    已提交 {{ formatNumber(captureBufferSubmittedTotal) }} · 丢弃 {{ formatNumber(captureBufferDroppedTotal) }} · 空闲阈值 {{ formatNumber(captureBufferIdleFlushSeconds) }} 秒
                  </p>
                </div>
                <span :class="['badge', captureBufferEnabled ? 'badge-success' : 'badge-gray']">{{ captureBufferEnabled ? '已启用' : '已关闭' }}</span>
              </div>
              <div class="space-y-4">
                <div>
                  <div class="mb-2 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
                    <span>Session 容量</span>
                    <span>{{ captureBufferSessionRatioText }}</span>
                  </div>
                  <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                    <div class="h-full rounded-full bg-sky-500 transition-all" :style="{ width: `${captureBufferSessionProgress}%` }"></div>
                  </div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ formatNumber(captureBufferBufferedSessions) }}/{{ formatNumber(captureBufferMaxSessions) }} 个 session
                  </p>
                </div>
                <div>
                  <div class="mb-2 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
                    <span>待落库增量</span>
                    <span>{{ captureBufferPendingRatioText }}</span>
                  </div>
                  <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                    <div class="h-full rounded-full bg-emerald-500 transition-all" :style="{ width: `${captureBufferPendingProgress}%` }"></div>
                  </div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ formatNumber(captureBufferPendingEvents) }}/{{ formatNumber(captureBufferMaxPendingEvents) }} 个增量
                  </p>
                </div>
              </div>
            </div>
            <p
              v-if="captureBufferHistoryNoticeText"
              :class="['truncate text-xs', captureBufferLastErrorActive ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400']"
              :title="captureBufferLastErrorTitle"
            >
              {{ captureBufferLastErrorPrefix }}：{{ captureBufferHistoryNoticeText }}
            </p>
          </div>

          <div class="space-y-4">
            <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              <div>
                <label class="input-label">空闲 Flush（秒）</label>
                <input v-model="captureBufferIdleFlushInput" type="number" min="1" :max="captureBufferIdleFlushMax" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">最大 Session</label>
                <input v-model="captureBufferMaxSessionsInput" type="number" min="1" :max="captureBufferMaxSessionsLimit" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">最大增量</label>
                <input v-model="captureBufferMaxPendingEventsInput" type="number" min="1" :max="captureBufferMaxPendingEventsLimit" step="1" class="input" />
              </div>
              <div>
                <label class="input-label">耗时窗口样本数</label>
                <input v-model="captureDurationWindowInput" type="number" :min="captureDurationWindowMin" :max="captureDurationWindowMax" step="1" class="input" />
              </div>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">缓冲 Session</p>
                <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatNumber(captureBufferBufferedSessions) }}</p>
              </div>
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">Flush 中</p>
                <p class="mt-2 text-2xl font-semibold text-sky-600 dark:text-sky-400">{{ formatNumber(captureBufferFlushingSessions) }}</p>
              </div>
              <div class="rounded-lg bg-gray-50 p-4 dark:bg-gray-800/60">
                <p class="text-xs text-gray-500 dark:text-gray-400">累计成功/失败</p>
                <p class="mt-2 text-2xl font-semibold text-emerald-600 dark:text-emerald-400">
                  {{ formatNumber(captureBufferFlushSuccessTotal) }}/{{ formatNumber(captureBufferFlushFailedTotal) }}
                </p>
              </div>
              <button
                type="button"
                class="rounded-lg bg-gray-50 p-4 text-left transition hover:bg-gray-100 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:bg-gray-800/60 dark:hover:bg-gray-800"
                title="查看采集链路耗时分布"
                @click="durationDetailOpen = true"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">最近耗时</p>
                <p class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(captureBufferRecentDurationMillis) }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">窗口 {{ formatNumber(captureDurationWindowSize) }} · 样本 {{ formatNumber(captureDurationSampleCount) }}</p>
              </button>
            </div>
          </div>
        </div>
      </div>

      <div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(360px,0.7fr)_minmax(320px,0.65fr)]">
        <div class="card p-4">
          <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">空间增长趋势</h2>
          <div class="h-64">
            <div v-if="statsLoading" class="flex h-full items-center justify-center">
              <LoadingSpinner />
            </div>
            <Line v-else-if="storageTrendChartData" :data="storageTrendChartData" :options="lineChartOptions" />
            <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">暂无趋势数据</div>
          </div>
        </div>

        <div class="card p-4">
          <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">分组空间占用</h2>
          <div class="h-64">
            <div v-if="statsLoading" class="flex h-full items-center justify-center">
              <LoadingSpinner />
            </div>
            <Bar v-else-if="groupStorageChartData" :data="groupStorageChartData" :options="barChartOptions" />
            <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">暂无分组数据</div>
          </div>
        </div>

        <div class="card p-4">
          <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">请求路径分布</h2>
          <div class="h-64">
            <div v-if="statsLoading" class="flex h-full items-center justify-center">
              <LoadingSpinner />
            </div>
            <Doughnut v-else-if="requestPathChartData" :data="requestPathChartData" :options="doughnutChartOptions" />
            <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">暂无路径数据</div>
          </div>
        </div>
      </div>

      <div class="grid gap-6 lg:grid-cols-2">
        <div class="card p-4">
          <div class="mb-4 flex items-center justify-between gap-3">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">无效会话用户排行</h2>
            <span v-if="stats?.invalid_user_breakdown?.length" class="badge badge-danger">Top {{ stats.invalid_user_breakdown.length }}</span>
          </div>
          <div class="h-96">
            <div v-if="statsLoading" class="flex h-full items-center justify-center">
              <LoadingSpinner />
            </div>
            <Bar v-else-if="invalidUserChartData" :data="invalidUserChartData" :options="invalidUserBarChartOptions" />
            <div v-else class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400">暂无无效用户数据</div>
          </div>
        </div>

        <div class="card p-4">
          <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">模型分布</h2>
          <div v-if="statsLoading" class="flex h-64 items-center justify-center">
            <LoadingSpinner />
          </div>
          <div v-else-if="modelChartData" class="space-y-3">
            <div class="h-52">
              <Doughnut :data="modelChartData" :options="modelDoughnutChartOptions" />
            </div>
            <div class="grid max-h-40 gap-2 overflow-y-auto rounded-lg border border-gray-100 p-2 sm:grid-cols-2 dark:border-gray-700">
              <div
                v-for="item in modelLegendItems"
                :key="item.key"
                class="flex min-w-0 items-start justify-between gap-3 text-xs"
                :title="item.fullLabel"
              >
                <span class="flex min-w-0 items-start gap-2">
                  <span class="mt-1 h-2.5 w-2.5 flex-shrink-0 rounded-full" :style="{ backgroundColor: item.color }"></span>
                  <span class="min-w-0 truncate text-gray-700 dark:text-gray-300">{{ item.label }}</span>
                </span>
                <span class="flex-shrink-0 font-medium text-gray-500 dark:text-gray-400">
                  {{ formatNumber(item.sessionCount) }}
                </span>
              </div>
            </div>
          </div>
          <div v-else class="flex h-64 items-center justify-center text-sm text-gray-500 dark:text-gray-400">暂无模型数据</div>
        </div>

        <div class="card p-4">
          <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">User Agent 分布</h2>
          <div v-if="statsLoading" class="flex h-64 items-center justify-center">
            <LoadingSpinner />
          </div>
          <div v-else-if="userAgentChartData" class="space-y-3">
            <div class="h-52">
              <Doughnut :data="userAgentChartData" :options="userAgentDoughnutChartOptions" />
            </div>
            <div class="max-h-32 space-y-2 overflow-y-auto rounded-lg border border-gray-100 p-2 dark:border-gray-700">
              <div
                v-for="item in userAgentLegendItems"
                :key="item.key"
                class="flex items-start justify-between gap-3 text-xs"
                :title="item.fullLabel"
              >
                <span class="flex min-w-0 items-start gap-2">
                  <span class="mt-1 h-2.5 w-2.5 flex-shrink-0 rounded-full" :style="{ backgroundColor: item.color }"></span>
                  <span class="min-w-0 truncate text-gray-700 dark:text-gray-300">{{ item.label }}</span>
                </span>
                <span class="flex-shrink-0 font-medium text-gray-500 dark:text-gray-400">
                  {{ formatNumber(item.sessionCount) }}
                </span>
              </div>
            </div>
          </div>
          <div v-else class="flex h-64 items-center justify-center text-sm text-gray-500 dark:text-gray-400">暂无 User Agent 数据</div>
        </div>

        <div class="card p-4">
          <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.dataSharing.qualityErrorDistribution') }}</h2>
          <div v-if="statsLoading" class="flex h-64 items-center justify-center">
            <LoadingSpinner />
          </div>
          <div v-else-if="qualityErrorChartData" class="space-y-3">
            <div class="h-52">
              <Doughnut :data="qualityErrorChartData" :options="qualityErrorDoughnutChartOptions" />
            </div>
            <div class="grid max-h-40 gap-2 overflow-y-auto rounded-lg border border-gray-100 p-2 sm:grid-cols-2 dark:border-gray-700">
              <div
                v-for="item in qualityErrorLegendItems"
                :key="item.key"
                class="flex min-w-0 items-start justify-between gap-3 text-xs"
                :title="item.fullLabel"
              >
                <span class="flex min-w-0 items-start gap-2">
                  <span class="mt-1 h-2.5 w-2.5 flex-shrink-0 rounded-full" :style="{ backgroundColor: item.color }"></span>
                  <span class="min-w-0 truncate text-gray-700 dark:text-gray-300">{{ item.label }}</span>
                </span>
                <span class="flex-shrink-0 font-medium text-gray-500 dark:text-gray-400">
                  {{ formatNumber(item.sessionCount) }}
                </span>
              </div>
            </div>
          </div>
          <div v-else class="flex h-64 items-center justify-center text-sm text-gray-500 dark:text-gray-400">{{ t('admin.dataSharing.noQualityErrorData') }}</div>
        </div>
      </div>

      <div class="card p-4">
        <div class="mb-4 flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">采集空间保护</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">超过阈值后停止记录新的数据共享采集，已有记录仍可查看和导出。</p>
          </div>
          <button class="btn btn-primary btn-sm" :disabled="savingStorageLimit || storageLimitLoading" @click="saveStorageLimit">
            <Icon name="check" size="sm" class="mr-1" />
            保存阈值
          </button>
        </div>
        <div v-if="storageLimitLoading" class="flex h-24 items-center justify-center">
          <LoadingSpinner />
        </div>
        <div v-else class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(260px,0.7fr)]">
          <div>
            <div class="mb-2 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
              <span>当前占用 {{ formatBytes(storageLimit?.current_storage_bytes) }}</span>
              <span>{{ storageLimitStatusText }}</span>
            </div>
            <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
              <div
                class="h-full rounded-full transition-all"
                :class="storageLimit?.exceeded ? 'bg-red-500' : 'bg-primary-500'"
                :style="{ width: `${storageLimitProgress}%` }"
              ></div>
            </div>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
              阈值按压缩后的 session payload 统计；设置为 0 表示不限制。
            </p>
          </div>
          <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_120px]">
            <div>
              <label class="input-label">空间阈值</label>
              <input v-model="storageLimitInput" type="number" min="0" step="0.01" class="input" placeholder="0" />
            </div>
            <div>
              <label class="input-label">单位</label>
              <Select v-model="storageLimitUnit" :options="storageLimitUnitOptions" />
            </div>
          </div>
        </div>
      </div>

      <div class="card p-4">
        <div class="mb-3 flex items-center justify-between gap-3">
          <div>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">数据共享须知</h2>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">当前版本：{{ notice?.version || '-' }}</p>
          </div>
          <button class="btn btn-primary" :disabled="savingNotice" @click="saveNotice">
            <Icon name="check" size="md" class="mr-2" />
            保存须知
          </button>
        </div>
        <textarea
          v-model="noticeContent"
          rows="5"
          class="input"
          placeholder="请输入用户切换到数据共享分组前需要确认的须知内容"
        ></textarea>
      </div>

      <div class="card p-4">
        <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <button
              type="button"
              class="inline-flex items-center gap-2 text-left text-sm font-semibold text-gray-900 dark:text-white"
              :aria-expanded="skipRulesExpanded"
              aria-controls="data-sharing-skip-rules"
              @click="toggleSkipRulesExpanded"
            >
              <span>采集跳过规则</span>
              <span class="badge badge-gray">{{ skipRulesSummary }}</span>
            </button>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">命中启用规则的辅助请求不会进入数据共享 session。</p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-secondary btn-sm" @click="toggleSkipRulesExpanded">
              <Icon :name="skipRulesExpanded ? 'chevronUp' : 'chevronDown'" size="sm" class="mr-1" />
              {{ skipRulesExpanded ? '收起' : '展开' }}
            </button>
            <button class="btn btn-secondary btn-sm" :disabled="skipRulesLoading || savingSkipRules" @click="restoreDefaultSkipRules">
              恢复默认规则
            </button>
            <button class="btn btn-secondary btn-sm" :disabled="skipRulesLoading || savingSkipRules" @click="addSkipRule">
              <Icon name="plus" size="sm" class="mr-1" />
              新增规则
            </button>
            <button class="btn btn-primary btn-sm" :disabled="skipRulesLoading || savingSkipRules" @click="saveSkipRules">
              <Icon name="check" size="sm" class="mr-1" />
              保存规则
            </button>
          </div>
        </div>

        <div v-if="skipRulesExpanded" id="data-sharing-skip-rules" class="mt-4">
          <div v-if="skipRulesLoading" class="flex h-32 items-center justify-center">
            <LoadingSpinner />
          </div>
          <div v-else-if="skipRules.length === 0" class="rounded-lg border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-gray-700 dark:text-gray-400">
            暂无跳过规则
          </div>
          <div v-else class="space-y-3">
            <div
              v-for="(rule, index) in skipRules"
              :key="rule.id || index"
              class="rounded-lg border border-gray-200 p-4 dark:border-gray-700"
            >
              <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                <label class="inline-flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  <input v-model="rule.enabled" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                  启用规则
                </label>
                <button class="btn btn-ghost btn-sm text-red-600 hover:text-red-700" @click="removeSkipRule(index)">
                  <Icon name="trash" size="sm" class="mr-1" />
                  删除
                </button>
              </div>

              <div class="mt-3 grid gap-3 md:grid-cols-2 xl:grid-cols-5">
                <div>
                  <label class="input-label">规则 ID</label>
                  <input v-model="rule.id" type="text" class="input font-mono text-sm" placeholder="custom_rule" />
                </div>
                <div>
                  <label class="input-label">规则名称</label>
                  <input v-model="rule.name" type="text" class="input" placeholder="辅助请求跳过规则" />
                </div>
                <div>
                  <label class="input-label">客户端</label>
                  <input
                    :value="joinList(rule.client_families)"
                    type="text"
                    class="input"
                    placeholder="opencode, claude-cli"
                    @input="setSkipRuleList(rule, 'client_families', eventValue($event))"
                  />
                </div>
                <div>
                  <label class="input-label">模型</label>
                  <input
                    :value="joinList(rule.models)"
                    type="text"
                    class="input font-mono text-sm"
                    placeholder="gpt-5.4-mini, codex-auto-review"
                    @input="setSkipRuleList(rule, 'models', eventValue($event))"
                  />
                </div>
                <div>
                  <label class="input-label">请求路径</label>
                  <div class="relative" :ref="el => setSkipRulePathMenuRef(rule.id || String(index), el)">
                    <button type="button" class="input flex items-center justify-between gap-2 text-left" @click="toggleSkipRulePathMenu(rule.id || String(index))">
                      <span class="truncate">{{ formatSkipRulePaths(rule.request_paths) }}</span>
                      <Icon name="chevronDown" size="sm" class="text-gray-400" />
                    </button>
                    <div
                      v-if="openSkipRulePathMenu === (rule.id || String(index))"
                      class="absolute z-30 mt-1 w-full rounded-lg border border-gray-200 bg-white p-2 shadow-lg dark:border-gray-700 dark:bg-gray-900"
                    >
                      <label
                        v-for="option in skipRuleRequestPathOptions"
                        :key="option.value"
                        class="flex cursor-pointer items-center gap-2 rounded px-2 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-800"
                      >
                        <input
                          :checked="rule.request_paths.includes(option.value)"
                          type="checkbox"
                          class="rounded border-gray-300 text-primary-600"
                          @change="toggleSkipRulePath(rule, option.value, $event)"
                        />
                        <span>{{ option.label }}</span>
                      </label>
                    </div>
                  </div>
                </div>
              </div>

              <div class="mt-3 grid gap-3 lg:grid-cols-[minmax(0,1fr)_180px_180px]">
                <div>
                  <label class="input-label">匹配字段</label>
                  <div class="flex flex-wrap gap-2">
                    <label
                      v-for="scope in skipRuleScopeOptions"
                      :key="scope.value"
                      class="inline-flex items-center gap-2 rounded border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-gray-700 dark:text-gray-300"
                    >
                      <input
                        :checked="rule.field_scopes.includes(scope.value)"
                        type="checkbox"
                        class="rounded border-gray-300 text-primary-600"
                        @change="toggleSkipRuleScope(rule, scope.value, $event)"
                      />
                      {{ scope.label }}
                    </label>
                  </div>
                </div>
                <div>
                  <label class="input-label">匹配方式</label>
                  <Select v-model="rule.match_mode" :options="skipRuleMatchModeOptions" />
                </div>
                <div>
                  <label class="input-label">大小写</label>
                  <label class="flex h-10 items-center gap-2 rounded border border-gray-200 px-3 text-sm text-gray-700 dark:border-gray-700 dark:text-gray-300">
                    <input v-model="rule.case_sensitive" type="checkbox" class="rounded border-gray-300 text-primary-600" />
                    区分大小写
                  </label>
                </div>
              </div>

              <div class="mt-3">
                <label class="input-label">关键词（一行一条）</label>
                <textarea
                  :value="joinLines(rule.patterns)"
                  rows="3"
                  class="input font-mono text-sm"
                  placeholder="Generate a title for this conversation:"
                  @input="setSkipRulePatterns(rule, eventValue($event))"
                ></textarea>
              </div>
            </div>
          </div>
        </div>
      </div>

      <TablePageLayout>
        <template #filters>
          <div class="flex flex-col gap-4">
            <div class="flex flex-col justify-between gap-4 lg:flex-row lg:items-center">
              <div class="flex flex-1 flex-wrap items-center gap-3">
                <div class="relative w-full sm:w-64">
                  <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                  <input v-model="filters.search" type="text" class="input pl-10" placeholder="搜索 session、轨迹、模型或 UA" @input="handleFilterChange" />
                </div>
                <input v-model="filters.user_name" type="text" class="input w-36" placeholder="用户名称" @input="handleFilterChange" />
                <input v-model="filters.api_key_name" type="text" class="input w-36" placeholder="Key 名称" @input="handleFilterChange" />
                <input v-model="filters.group_name" type="text" class="input w-36" placeholder="分组名称" @input="handleFilterChange" />
                <Select v-model="filters.model" :options="modelOptions" class="w-56" searchable @change="handleFilterChange" />
                <Select v-model="filters.request_path" :options="requestPathOptions" class="w-52" @change="handleFilterChange" />
                <Select v-model="filters.user_agent" :options="userAgentOptions" class="w-56" searchable @change="handleFilterChange" />
                <Select v-model="filters.quality_status" :options="qualityOptions" class="w-40" @change="handleFilterChange" />
                <div
                  v-if="filters.user_id > 0"
                  class="inline-flex min-h-10 max-w-full items-center gap-2 rounded border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-900/20 dark:text-red-200"
                >
                  <span class="truncate">用户：{{ invalidUserFilterLabel }}</span>
                  <button type="button" class="rounded p-0.5 hover:bg-red-100 dark:hover:bg-red-900/40" title="清除用户筛选" @click="clearInvalidUserFilter">
                    <Icon name="x" size="sm" />
                  </button>
                </div>
                <input v-model="filters.start_date" type="date" class="input w-40" @change="handleFilterChange" />
                <input v-model="filters.end_date" type="date" class="input w-40" @change="handleFilterChange" />
              </div>
              <div class="flex flex-wrap justify-end gap-3">
                <button class="btn btn-secondary" :disabled="loading" @click="refreshAll">
                  <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
                </button>
                <button
                  class="btn btn-secondary"
                  :disabled="pagination.total === 0"
                  :title="selectAllMatching ? '清空当前选中 session' : '选中当前筛选条件下的所有 session'"
                  @click="toggleSelectAllFilteredSessions"
                >
                  <Icon :name="selectAllMatching ? 'xCircle' : 'checkCircle'" size="md" class="mr-2" />
                  {{ selectAllMatching ? '取消全选' : '全选结果' }}
                </button>
                <button class="btn btn-secondary" :disabled="selectedCount === 0" @click="batchDelete">
                  <Icon name="trash" size="md" class="mr-2" />
                  删除已选
                </button>
                <button class="btn btn-primary" :disabled="exporting || selectedCount === 0" @click="downloadSelected">
                  <Icon name="download" size="md" class="mr-2" />
                  生成导出文件
                </button>
              </div>
            </div>
          </div>
        </template>

        <template #table>
          <DataTable
            :columns="columns"
            :data="sessions"
            :loading="loading"
            :server-side-sort="true"
            default-sort-key="created_at"
            default-sort-order="desc"
            @sort="handleSort"
          >
            <template #header-select>
              <div class="flex min-w-[3.5rem] flex-col items-start gap-1 normal-case tracking-normal">
                <span v-if="selectedCount > 0" class="whitespace-nowrap text-[11px] font-medium leading-none text-primary-600 dark:text-primary-400" :title="selectionSummary">
                  已选 {{ formatNumber(selectedCount) }}
                </span>
                <input
                  :checked="currentPageAllSelected"
                  :disabled="sessions.length === 0"
                  :indeterminate="currentPageSelectionIndeterminate"
                  type="checkbox"
                  class="rounded border-gray-300 text-primary-600"
                  title="选择当前页 session"
                  @change="toggleSelectCurrentPage"
                />
              </div>
            </template>
            <template #cell-select="{ row }">
              <input :checked="isSelected(row.id)" type="checkbox" class="rounded border-gray-300 text-primary-600" @change="toggleSelect(row.id)" />
            </template>
            <template #cell-session_id="{ value, row }">
              <div class="max-w-xs">
                <p class="truncate font-medium text-gray-900 dark:text-white">{{ value }}</p>
                <p class="truncate text-xs text-gray-500 dark:text-gray-400">
                  {{ displayUser(row) }} / {{ displayAPIKey(row) }} / {{ displayGroup(row) }}
                </p>
              </div>
            </template>
            <template #cell-model="{ value }">
              <span class="badge badge-gray">{{ value || '-' }}</span>
            </template>
            <template #cell-request_path="{ value }">
              <span class="badge badge-gray">{{ value || '-' }}</span>
            </template>
            <template #cell-user_agent="{ value }">
              <span v-if="value" class="block max-w-[260px] truncate text-sm text-gray-600 dark:text-gray-400" :title="value">{{ formatUserAgent(value) }}</span>
              <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
            </template>
            <template #cell-quality_status="{ value, row }">
              <span :class="['badge', qualityBadgeClass(value)]">
                {{ qualityLabel(value) }}<span v-if="value === 'invalid' && row.quality_errors?.length"> {{ row.quality_errors.length }}</span>
              </span>
            </template>
            <template #cell-storage_bytes="{ value }">{{ formatBytes(value) }}</template>
            <template #cell-total_tokens="{ value }">{{ formatNumber(value) }}</template>
            <template #cell-created_at="{ value }">{{ formatDate(value) }}</template>
            <template #cell-actions="{ row }">
              <div class="flex items-center gap-1">
                <button class="btn btn-ghost btn-sm" @click="openDetail(row)">
                  <Icon name="eye" size="sm" class="mr-1" />
                  查看
                </button>
                <button class="btn btn-ghost btn-sm" @click="downloadOne(row)">
                  <Icon name="download" size="sm" class="mr-1" />
                  生成
                </button>
                <button class="btn btn-ghost btn-sm text-red-600 hover:text-red-700" @click="deleteOne(row)">
                  <Icon name="trash" size="sm" class="mr-1" />
                  删除
                </button>
              </div>
            </template>
            <template #empty>
              <EmptyState title="暂无数据共享记录" description="数据共享分组产生的对话 session 会显示在这里。" />
            </template>
          </DataTable>
        </template>

        <template #pagination>
          <Pagination
            v-if="pagination.total > 0"
            :page="pagination.page"
            :total="pagination.total"
            :page-size="pagination.page_size"
            @update:page="handlePageChange"
            @update:pageSize="handlePageSizeChange"
          />
        </template>
      </TablePageLayout>

      <div class="card overflow-hidden">
        <div class="border-b border-gray-200 p-4 dark:border-gray-700">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">导出文件</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">已预处理完成的文件可下载，也可上传到数据共享专用 S3/R2 存储桶。</p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button class="btn btn-secondary btn-sm" type="button" :disabled="testingExportRemoteConfig" @click="testExportRemoteConfig">
                {{ testingExportRemoteConfig ? '测试中' : '测试连接' }}
              </button>
              <button class="btn btn-primary btn-sm" type="button" :disabled="savingExportSettings" @click="saveExportSettings">
                {{ savingExportSettings ? '保存中' : '保存配置' }}
              </button>
              <button class="btn btn-secondary btn-sm" :disabled="exportArtifactsLoading || statsLoading" @click="refreshExportArtifacts">
                <Icon name="refresh" size="sm" :class="exportArtifactsLoading || statsLoading ? 'animate-spin' : ''" />
                <span class="ml-1">刷新</span>
              </button>
            </div>
          </div>
          <div class="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-4 md:grid-cols-2">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-batch-size">导出批次大小</label>
              <input id="data-share-export-batch-size" v-model="exportBatchSizeInput" type="number" :min="exportBatchSizeMin" :max="exportBatchSizeMax" step="1" class="input w-full text-sm" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-worker-count">导出并发数</label>
              <input id="data-share-export-worker-count" v-model="exportWorkerCountInput" type="number" :min="exportWorkerCountMin" :max="exportWorkerCountMax" step="1" class="input w-full text-sm" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-remote-endpoint">端点地址</label>
              <input id="data-share-export-remote-endpoint" v-model="exportRemoteForm.endpoint" class="input w-full text-sm" placeholder="https://<账号ID>.r2.cloudflarestorage.com" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-remote-region">区域</label>
              <input id="data-share-export-remote-region" v-model="exportRemoteForm.region" class="input w-full text-sm" placeholder="auto" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-remote-bucket">存储桶</label>
              <input id="data-share-export-remote-bucket" v-model="exportRemoteForm.bucket" class="input w-full text-sm" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-remote-prefix">对象前缀</label>
              <input id="data-share-export-remote-prefix" v-model="exportRemoteForm.prefix" class="input w-full text-sm" placeholder="data-sharing-exports" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-upload-concurrency">上传并发数</label>
              <input id="data-share-export-upload-concurrency" v-model.number="exportRemoteForm.upload_concurrency" type="number" :min="exportUploadConcurrencyMin" :max="exportUploadConcurrencyMax" step="1" class="input w-full text-sm" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-upload-part-size">分片大小(MB)</label>
              <input id="data-share-export-upload-part-size" v-model.number="exportRemoteForm.upload_part_size_mb" type="number" :min="exportUploadPartSizeMBMin" :max="exportUploadPartSizeMBMax" step="1" class="input w-full text-sm" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-remote-access-key">访问密钥 ID</label>
              <input id="data-share-export-remote-access-key" v-model="exportRemoteForm.access_key_id" class="input w-full text-sm" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400" for="data-share-export-remote-secret-key">访问密钥 Secret</label>
              <input id="data-share-export-remote-secret-key" v-model="exportRemoteForm.secret_access_key" type="password" class="input w-full text-sm" :placeholder="exportRemoteSecretConfigured ? '已配置，留空则保留' : ''" />
            </div>
            <div class="flex items-end">
              <label class="flex min-h-10 items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
                <input v-model="exportRemoteForm.force_path_style" type="checkbox" />
                <span>使用路径样式 URL</span>
              </label>
            </div>
          </div>
          <div class="mt-4 border-t border-gray-100 pt-4 dark:border-gray-800">
            <div class="mb-3 flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
              <div>
                <p class="text-sm font-medium text-gray-900 dark:text-white">导出生成耗时</p>
                <p class="text-xs text-gray-500 dark:text-gray-400">窗口 {{ formatNumber(exportDurationWindowSize) }} · 样本 {{ formatNumber(exportDurationSampleCount) }}</p>
              </div>
              <span v-if="exportGenerateTotalDurationPart?.sample_count" class="badge badge-gray">最近总耗时 {{ formatDurationMillis(exportGenerateTotalDurationPart.last_millis) }}</span>
            </div>
            <div v-if="exportDurationSampleCount" class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.75fr)]">
              <div class="h-56">
                <Bar v-if="exportDurationChartParts.length" :data="exportDurationChartData" :options="exportDurationChartOptions" />
                <div v-else class="flex h-full items-center justify-center rounded-lg border border-dashed border-gray-300 text-xs text-gray-500 dark:border-gray-700 dark:text-gray-400">
                  暂无阶段耗时样本
                </div>
              </div>
              <div class="overflow-x-auto rounded-lg border border-gray-100 dark:border-gray-800">
                <table class="min-w-full divide-y divide-gray-100 text-xs dark:divide-gray-800">
                  <thead class="bg-gray-50 text-gray-500 dark:bg-gray-800/60 dark:text-gray-400">
                    <tr>
                      <th class="px-3 py-2 text-left font-medium">阶段</th>
                      <th class="px-3 py-2 text-right font-medium">最近</th>
                      <th class="px-3 py-2 text-right font-medium">平均</th>
                      <th class="px-3 py-2 text-right font-medium">P95</th>
                      <th class="px-3 py-2 text-right font-medium">样本</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
                    <tr v-for="part in exportDurationActiveParts" :key="part.key">
                      <td class="px-3 py-2 text-gray-700 dark:text-gray-300">{{ part.label }}</td>
                      <td class="px-3 py-2 text-right font-medium text-gray-900 dark:text-white">{{ formatDurationMillis(part.last_millis) }}</td>
                      <td class="px-3 py-2 text-right text-gray-600 dark:text-gray-400">{{ formatDurationMillis(part.avg_millis) }}</td>
                      <td class="px-3 py-2 text-right text-gray-600 dark:text-gray-400">{{ formatDurationMillis(part.p95_millis) }}</td>
                      <td class="px-3 py-2 text-right text-gray-500 dark:text-gray-400">{{ formatNumber(part.sample_count) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
            <div v-else class="rounded-lg border border-dashed border-gray-300 p-4 text-center text-xs text-gray-500 dark:border-gray-700 dark:text-gray-400">
              暂无导出耗时样本
            </div>
          </div>
        </div>
        <DataTable :columns="exportArtifactColumns" :data="exportArtifacts" :loading="exportArtifactsLoading">
          <template #cell-status="{ row }">
            <div class="min-w-36">
              <span :class="['badge', exportArtifactStatusBadgeClass(row.status)]">{{ exportArtifactStatusLabel(row.status) }}</span>
              <div v-if="row.status === 'running'" class="mt-2 w-36">
                <div class="h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
                  <div class="h-full rounded-full bg-primary-600 transition-all" :style="{ width: formatExportGenerateProgress(row) }"></div>
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ formatExportGenerateProgress(row) }} · {{ formatExportGenerateCount(row) }}
                </p>
              </div>
            </div>
          </template>
          <template #cell-filename="{ row }">
            <div class="max-w-sm">
              <p class="truncate font-medium text-gray-900 dark:text-white" :title="row.filename">{{ row.filename }}</p>
              <p v-if="row.error_message" class="truncate text-xs text-red-600 dark:text-red-400" :title="row.error_message">{{ row.error_message }}</p>
              <p v-else class="truncate text-xs text-gray-500 dark:text-gray-400">{{ row.sha256 ? `SHA256 ${row.sha256.slice(0, 12)}` : row.encoding }}</p>
            </div>
          </template>
          <template #cell-session_count="{ value }">{{ formatNumber(value) }}</template>
          <template #cell-file_size="{ value }">{{ formatBytes(value) }}</template>
          <template #cell-remote_status="{ row }">
            <div class="max-w-xs">
              <span :class="['badge', exportArtifactRemoteStatusBadgeClass(row.remote_status)]">{{ exportArtifactRemoteStatusLabel(row.remote_status) }}</span>
              <div v-if="row.remote_status === 'uploading'" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ formatExportUploadProgress(row) }} · {{ formatExportUploadSpeed(row.remote_upload_speed) }}
              </div>
              <div v-if="row.remote_key" class="mt-1 flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                <span class="truncate" :title="`${row.remote_bucket}/${row.remote_key}`">{{ row.remote_bucket }}/{{ row.remote_key }}</span>
                <button
                  class="btn btn-ghost btn-xs shrink-0"
                  type="button"
                  title="复制远端对象 key"
                  @click="copyRemoteKey(row)"
                >
                  <Icon name="copy" size="xs" />
                </button>
              </div>
              <p v-if="row.remote_error_message" class="mt-1 truncate text-xs text-red-600 dark:text-red-400" :title="row.remote_error_message">
                {{ row.remote_error_message }}
              </p>
            </div>
          </template>
          <template #cell-created_at="{ value }">{{ formatDate(value) }}</template>
          <template #cell-completed_at="{ value }">{{ formatDate(value) }}</template>
          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button class="btn btn-ghost btn-sm" :disabled="row.status !== 'completed'" @click="downloadExportArtifact(row)">
                <Icon name="download" size="sm" class="mr-1" />
                下载
              </button>
              <button
                class="btn btn-ghost btn-sm"
                :class="row.remote_status === 'uploading' ? 'text-amber-600 hover:text-amber-700 dark:text-amber-400 dark:hover:text-amber-300' : ''"
                :disabled="row.status !== 'completed' || isCancelingExportUpload(row.id)"
                @click="handleExportArtifactUploadAction(row)"
              >
                <Icon :name="row.remote_status === 'uploading' ? 'xCircle' : 'upload'" size="sm" class="mr-1" />
                {{ exportArtifactUploadActionLabel(row) }}
              </button>
              <button
                class="btn btn-ghost btn-sm"
                :disabled="!row.remote_key"
                @click="downloadRemoteExportArtifact(row)"
              >
                <Icon name="externalLink" size="sm" class="mr-1" />
                远端下载
              </button>
              <button
                class="btn btn-ghost btn-sm text-red-600 hover:text-red-700"
                :disabled="row.status === 'deleted' || row.status === 'pending' || row.status === 'running' || row.remote_status === 'uploading'"
                @click="deleteExportArtifact(row)"
              >
                <Icon name="trash" size="sm" class="mr-1" />
                删除
              </button>
            </div>
          </template>
          <template #empty>
            <EmptyState title="暂无导出文件" description="选择数据并生成导出文件后会显示在这里。" />
          </template>
        </DataTable>
        <!-- 导出文件分页直接使用通用分页器的条形样式，保持和上方 session 表一致。 -->
        <div v-if="exportArtifactPagination.total > 0">
          <Pagination
            :page="exportArtifactPagination.page"
            :total="exportArtifactPagination.total"
            :page-size="exportArtifactPagination.page_size"
            @update:page="handleExportArtifactPageChange"
            @update:pageSize="handleExportArtifactPageSizeChange"
          />
        </div>
      </div>
    </div>

    <BaseDialog :show="detailOpen" title="数据共享详情" width="extra-wide" @close="detailOpen = false">
      <div v-if="detailLoading" class="flex h-48 items-center justify-center">
        <LoadingSpinner />
      </div>
      <div v-else-if="selectedSession" class="space-y-4">
        <div class="flex flex-wrap gap-2">
          <span :class="['badge', qualityBadgeClass(selectedSession.quality_status)]">
            {{ qualityLabel(selectedSession.quality_status) }}
          </span>
          <span v-if="!selectedSession.is_final_snapshot" class="badge badge-warning">非最终快照</span>
        </div>
        <div class="grid gap-3 md:grid-cols-3 xl:grid-cols-6">
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">用户</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="displayUser(selectedSession)">{{ displayUser(selectedSession) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">Key</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="displayAPIKey(selectedSession)">{{ displayAPIKey(selectedSession) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">分组</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="displayGroup(selectedSession)">{{ displayGroup(selectedSession) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">Session</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="selectedSession.session_id">{{ selectedSession.session_id }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">模型</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="selectedSession.model">{{ selectedSession.model || '-' }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">请求路径</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="selectedSession.request_path">{{ selectedSession.request_path || '-' }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">User Agent</p>
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="selectedSession.user_agent">{{ formatUserAgent(selectedSession.user_agent) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">Token</p>
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ formatNumber(selectedSession.total_tokens) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500">空间</p>
            <p class="text-sm font-medium text-gray-900 dark:text-white">{{ formatBytes(selectedSession.storage_bytes) }}</p>
          </div>
        </div>
        <div
          v-if="selectedSession.quality_errors?.length"
          class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200"
        >
          <p class="mb-2 text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-300">错误类型</p>
          <div class="flex flex-wrap gap-2">
            <span
              v-for="code in selectedSession.quality_errors"
              :key="code"
              class="rounded-md bg-amber-100 px-2 py-1 text-xs font-medium text-amber-900 dark:bg-amber-950/50 dark:text-amber-100"
            >
              {{ qualityErrorLabel(code) }}
            </span>
          </div>
        </div>
        <pre class="max-h-[60vh] overflow-auto rounded-lg bg-gray-950 p-4 text-xs leading-relaxed text-gray-100">{{ prettySession }}</pre>
      </div>
    </BaseDialog>

    <BaseDialog :show="durationDetailOpen" title="采集耗时分布" width="extra-wide" @close="durationDetailOpen = false">
      <div class="space-y-4">
        <div class="grid gap-3 md:grid-cols-3">
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">窗口样本数</p>
            <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ formatNumber(captureDurationWindowSize) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">当前样本</p>
            <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ formatNumber(captureDurationSampleCount) }}</p>
          </div>
          <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">最近 Flush</p>
            <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(captureBufferRecentDurationMillis) }}</p>
          </div>
        </div>

        <div v-if="!captureDurationSampleCount" class="rounded-lg border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-gray-700 dark:text-gray-400">
          当前窗口还没有新采集耗时样本；产生新的数据共享请求并等待 Flush 后会出现柱状分布。
        </div>
        <div v-if="captureDurationParts.length" class="space-y-3">
          <div
            v-for="part in captureDurationParts"
            :key="part.key"
            class="rounded-lg border border-gray-200 p-4 dark:border-gray-700"
          >
            <div class="mb-3 flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
              <div>
                <div class="flex items-center gap-2">
                  <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ part.label }}</h3>
                  <span class="badge badge-gray">{{ part.category }}</span>
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">样本 {{ formatNumber(part.sample_count) }}</p>
              </div>
              <div class="grid grid-cols-2 gap-2 text-right text-xs sm:grid-cols-5">
                <div>
                  <p class="text-gray-500 dark:text-gray-400">最近</p>
                  <p class="font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(part.last_millis) }}</p>
                </div>
                <div>
                  <p class="text-gray-500 dark:text-gray-400">平均</p>
                  <p class="font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(part.avg_millis) }}</p>
                </div>
                <div>
                  <p class="text-gray-500 dark:text-gray-400">P50</p>
                  <p class="font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(part.p50_millis) }}</p>
                </div>
                <div>
                  <p class="text-gray-500 dark:text-gray-400">P95</p>
                  <p class="font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(part.p95_millis) }}</p>
                </div>
                <div>
                  <p class="text-gray-500 dark:text-gray-400">最大</p>
                  <p class="font-semibold text-gray-900 dark:text-white">{{ formatDurationMillis(part.max_millis) }}</p>
                </div>
              </div>
            </div>
            <div class="h-48">
              <Bar :data="durationBucketChartData(part)" :options="durationBucketChartOptions" />
            </div>
          </div>
        </div>
      </div>
    </BaseDialog>

    <ConfirmDialog
      :show="cancelUploadDialogOpen"
      title="取消上传"
      :message="cancelUploadDialogMessage"
      confirm-text="取消上传"
      cancel-text="继续上传"
      danger
      @confirm="confirmCancelExportArtifactUpload"
      @cancel="closeCancelExportArtifactUploadDialog"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, type ComponentPublicInstance } from 'vue'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  BarElement,
  ArcElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Bar, Doughnut, Line } from 'vue-chartjs'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useClipboard } from '@/composables/useClipboard'
import {
  adminDataSharingAPI,
  type AdminDataShareSessionFilters,
  type DataShareCaptureDurationPart,
  type DataShareInvalidUserPoint,
  type DataShareCaptureSkipRule,
  type DataShareCaptureSkipRuleFieldScope,
  type DataShareExportArtifact,
  type DataShareExportRemoteConfig,
  type DataShareStorageLimit,
  type DataShareStats
} from '@/api/admin/dataSharing'
import { dataSharingAPI, type DataShareNotice, type DataShareQualityFilterStatus, type DataShareSession, type DataShareSessionFilterOptions } from '@/api/dataSharing'
import { useAppStore } from '@/stores/app'
import type { Column } from '@/components/common/types'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, BarElement, ArcElement, Tooltip, Legend, Filler)

const appStore = useAppStore()
const { t, te } = useI18n()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()
const { copyToClipboard } = useClipboard()

const notice = ref<DataShareNotice | null>(null)
const noticeContent = ref('')
const skipRules = ref<DataShareCaptureSkipRule[]>([])
const storageLimit = ref<DataShareStorageLimit | null>(null)
const storageLimitInput = ref('')
const storageLimitUnit = ref<'MB' | 'GB' | 'TB'>('GB')
const captureWorkerCountInput = ref('')
const captureQueueSizeInput = ref('')
const captureFlushQueueSizeInput = ref('')
const captureTimeoutInput = ref('')
const captureCompressionLevelInput = ref('')
const captureBufferEnabledInput = ref(true)
const captureBufferIdleFlushInput = ref('')
const captureBufferMaxSessionsInput = ref('')
const captureBufferMaxPendingEventsInput = ref('')
const captureDurationWindowInput = ref('')
const exportBatchSizeInput = ref('')
const exportWorkerCountInput = ref('')
const captureWorkerCountMax = 1024
const captureQueueSizeMax = 100000
const captureFlushQueueSizeMax = 100000
// 采集 flush 可能处理大 session，前端允许配置到 30 分钟。
const captureTimeoutSecondsMax = 1800
// 缓冲池空闲 Flush 上限与后端一致，最长 30 分钟。
const captureBufferIdleFlushMax = 1800
const captureBufferMaxSessionsLimit = 100000
const captureBufferMaxPendingEventsLimit = 1000000
const captureDurationWindowMin = 32
const captureDurationWindowMax = 10000
const captureDurationWindowDefault = 512
const exportBatchSizeMin = 50
const exportBatchSizeMax = 2000
const exportBatchSizeDefault = 500
const exportWorkerCountMin = 1
const exportWorkerCountMax = 8
const exportWorkerCountDefault = 4
const exportUploadConcurrencyMin = 1
const exportUploadConcurrencyMax = 8
const exportUploadConcurrencyDefault = 4
const exportUploadPartSizeMBMin = 5
const exportUploadPartSizeMBMax = 128
const exportUploadPartSizeMBDefault = 64
const statsAutoRefreshDefaultSeconds = 5
const statsAutoRefreshIntervals = [5, 10, 15, 30] as const
const captureCompressionLevelOptions = [
  { value: 'fastest', label: '最快' },
  { value: 'default', label: '默认' },
  { value: 'better', label: '更高压缩' },
  { value: 'best', label: '最高压缩' }
]

const storageLimitUnitOptions = [
  { value: 'MB', label: 'MB' },
  { value: 'GB', label: 'GB' },
  { value: 'TB', label: 'TB' }
]
const stats = ref<DataShareStats | null>(null)
const sessions = ref<DataShareSession[]>([])
const selectedSession = ref<DataShareSession | null>(null)
const filterOptions = ref<DataShareSessionFilterOptions>({ models: [], request_paths: [], user_agents: [] })
const openSkipRulePathMenu = ref<string | null>(null)
const skipRulesExpanded = ref(false)
const skipRulePathMenuRefs = new Map<string, HTMLElement>()
// 选中状态支持两种模式：显式 ID 列表，以及“当前筛选条件全集 + 排除列表”。
const selectedIds = ref<Set<number>>(new Set())
const excludedIds = ref<Set<number>>(new Set())
const selectAllMatching = ref(false)

const loading = ref(false)
const exportArtifactsLoading = ref(false)
const statsLoading = ref(false)
const statsAutoRefreshEnabled = ref(false)
const statsAutoRefreshIntervalSeconds = ref<(typeof statsAutoRefreshIntervals)[number]>(statsAutoRefreshDefaultSeconds)
const statsAutoRefreshDropdownOpen = ref(false)
const statsAutoRefreshDropdownRef = ref<HTMLElement | null>(null)
const statsAutoRefreshButtonRef = ref<HTMLElement | null>(null)
const statsAutoRefreshDropdownStyle = ref<Record<string, string>>({})
const savingNotice = ref(false)
const skipRulesLoading = ref(false)
const savingSkipRules = ref(false)
const storageLimitLoading = ref(false)
const savingStorageLimit = ref(false)
const savingCaptureRuntimeSettings = ref(false)
const savingExportRemoteConfig = ref(false)
const savingExportSettings = ref(false)
const testingExportRemoteConfig = ref(false)
const exporting = ref(false)
const detailOpen = ref(false)
const detailLoading = ref(false)
const durationDetailOpen = ref(false)

const pagination = reactive({ page: 1, page_size: 20, total: 0, pages: 1 })
const exportArtifactPagination = reactive({ page: 1, page_size: 10, total: 0, pages: 1 })
const sortState = reactive({ sort_by: 'created_at', sort_order: 'desc' as 'asc' | 'desc' })
const exportArtifacts = ref<DataShareExportArtifact[]>([])
const exportRemoteSecretConfigured = ref(false)
const cancelUploadDialogOpen = ref(false)
const cancelUploadTarget = ref<DataShareExportArtifact | null>(null)
const cancelingExportUploadIds = ref<Set<number>>(new Set())
const exportRemoteForm = ref<DataShareExportRemoteConfig>({
  endpoint: '',
  region: 'auto',
  bucket: '',
  access_key_id: '',
  secret_access_key: '',
  prefix: 'data-sharing-exports',
  force_path_style: false,
  upload_concurrency: exportUploadConcurrencyDefault,
  upload_part_size_mb: exportUploadPartSizeMBDefault
})
const filters = reactive({
  search: '',
  user_name: '',
  api_key_name: '',
  group_name: '',
  request_path: 'all',
  user_agent: 'all',
  model: 'all',
  quality_status: 'all' as DataShareQualityFilterStatus,
  user_id: 0,
  user_filter_label: '',
  start_date: '',
  end_date: ''
})

const qualityOptions = [
  { value: 'all', label: '全部质量' },
  { value: 'non_invalid', label: '非无效' },
  { value: 'complete', label: '完整' },
  { value: 'partial', label: '部分完整' },
  { value: 'invalid', label: '无效' }
]

const skipRuleScopeOptions: Array<{ value: DataShareCaptureSkipRuleFieldScope; label: string }> = [
  { value: 'system', label: 'System' },
  { value: 'messages', label: 'Messages' },
  { value: 'input', label: 'Input' },
  { value: 'instructions', label: 'Instructions' }
]

const skipRuleRequestPathOptions = [
  { value: '/v1/messages', label: '/v1/messages' },
  { value: '/v1/chat/completions', label: '/v1/chat/completions' },
  { value: '/v1/responses', label: '/v1/responses' }
]

const skipRuleMatchModeOptions = [
  { value: 'contains', label: '包含' },
  { value: 'equals', label: '等于' }
]

const defaultSkipRules: DataShareCaptureSkipRule[] = [
  {
    id: 'claude_code_title',
    name: 'Claude Code 标题生成',
    enabled: true,
    client_families: ['claude-cli'],
    request_paths: ['/v1/messages'],
    models: [],
    field_scopes: ['system'],
    patterns: ['Generate a concise, sentence-case title'],
    case_sensitive: false,
    match_mode: 'contains'
  },
  {
    id: 'opencode_title_system',
    name: 'opencode 标题生成系统提示',
    enabled: true,
    client_families: ['opencode'],
    request_paths: ['/v1/messages', '/v1/chat/completions', '/v1/responses'],
    models: [],
    field_scopes: ['system'],
    patterns: [
      'You are a title generator. You output ONLY a thread title. Nothing else.',
      'Generate a brief title that would help the user find this conversation later.',
      'NEVER respond to questions, just generate a title for the conversation'
    ],
    case_sensitive: false,
    match_mode: 'contains'
  },
  {
    id: 'opencode_title_user_prompt',
    name: 'opencode 标题生成用户提示',
    enabled: true,
    client_families: ['opencode'],
    request_paths: ['/v1/messages', '/v1/chat/completions', '/v1/responses'],
    models: [],
    field_scopes: ['messages', 'input'],
    patterns: ['Generate a title for this conversation:'],
    case_sensitive: false,
    match_mode: 'contains'
  },
  {
    id: 'agent_title_from_messages',
    name: 'Agent 会话标题生成',
    enabled: true,
    client_families: [],
    request_paths: ['/v1/messages', '/v1/chat/completions', '/v1/responses'],
    models: [],
    field_scopes: ['messages', 'input'],
    patterns: ['Please write a 5-10 word title for the following conversation:'],
    case_sensitive: false,
    match_mode: 'contains'
  },
  {
    id: 'agent_topic_title',
    name: 'Agent 主题标题提取',
    enabled: true,
    client_families: [],
    request_paths: ['/v1/messages', '/v1/chat/completions', '/v1/responses'],
    models: [],
    field_scopes: ['system', 'instructions'],
    patterns: ['extract a 2-3 word title'],
    case_sensitive: false,
    match_mode: 'contains'
  },
  {
    id: 'agent_warmup',
    name: 'Agent 预热请求',
    enabled: true,
    client_families: [],
    request_paths: ['/v1/messages', '/v1/chat/completions', '/v1/responses'],
    models: [],
    field_scopes: ['messages', 'input'],
    patterns: ['Warmup'],
    case_sensitive: false,
    match_mode: 'equals'
  },
  {
    id: 'excluded_models',
    name: '默认排除模型',
    enabled: true,
    client_families: [],
    request_paths: [],
    models: ['gpt-5.4-mini', 'codex-auto-review'],
    field_scopes: [],
    patterns: [],
    case_sensitive: false,
    match_mode: 'equals'
  }
]

const columns: Column[] = [
  { key: 'select', label: '' },
  { key: 'session_id', label: 'Session', sortable: true },
  { key: 'provider', label: 'Provider', sortable: true },
  { key: 'request_path', label: '请求路径', sortable: true },
  { key: 'model', label: '模型', sortable: true },
  { key: 'user_agent', label: 'User Agent', sortable: true },
  { key: 'quality_status', label: '质量', sortable: true },
  { key: 'storage_bytes', label: '空间', sortable: true },
  { key: 'total_tokens', label: 'Token', sortable: true },
  { key: 'created_at', label: '创建时间', sortable: true },
  { key: 'actions', label: '操作' }
]

const exportArtifactColumns: Column[] = [
  { key: 'status', label: '状态' },
  { key: 'filename', label: '文件' },
  { key: 'session_count', label: 'Session' },
  { key: 'file_size', label: '大小' },
  { key: 'remote_status', label: 'S3/R2' },
  { key: 'created_at', label: '创建时间' },
  { key: 'completed_at', label: '完成时间' },
  { key: 'actions', label: '操作' }
]

const isDarkMode = computed(() => document.documentElement.classList.contains('dark'))
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#E4E4E7' : '#3F3F46',
  grid: isDarkMode.value ? '#3F3F46' : '#E4E4E7',
  storage: '#2563eb',
  sessions: '#10b981',
  group: '#7c3aed'
}))

const doughnutPalette = ['#2563eb', '#10b981', '#f59e0b', '#ef4444', '#7c3aed', '#0891b2', '#db2777', '#65a30d']

type DoughnutLegendPoint = { session_count: number }
type DoughnutLegendItem = {
  key: string
  fullLabel: string
  label: string
  sessionCount: number
  color: string
}

const storageTrendChartData = computed(() => {
  const points = stats.value?.storage_trend || []
  if (!points.length) return null
  return {
    labels: points.map(point => point.date),
    datasets: [
      {
        label: '空间',
        data: points.map(point => point.storage_bytes),
        borderColor: chartColors.value.storage,
        backgroundColor: `${chartColors.value.storage}22`,
        fill: true,
        tension: 0.3
      },
      {
        label: 'Session',
        data: points.map(point => point.session_count),
        borderColor: chartColors.value.sessions,
        backgroundColor: `${chartColors.value.sessions}22`,
        yAxisID: 'y1',
        tension: 0.3
      }
    ]
  }
})

const groupStorageChartData = computed(() => {
  const points = stats.value?.group_storage_breakdown || []
  if (!points.length) return null
  return {
    labels: points.map(point => point.group_name || `#${point.group_id}`),
    datasets: [
      {
        label: '空间',
        data: points.map(point => point.storage_bytes),
        backgroundColor: `${chartColors.value.group}88`,
        borderColor: chartColors.value.group,
        borderWidth: 1
      }
    ]
  }
})

const requestPathChartData = computed(() => {
  const points = stats.value?.request_path_breakdown || []
  if (!points.length) return null
  return {
    labels: points.map(point => point.request_path || '(unknown)'),
    datasets: [
      {
        label: 'Session',
        data: points.map(point => point.session_count),
        backgroundColor: points.map((_, index) => doughnutPalette[index % doughnutPalette.length]),
        borderWidth: 0
      }
    ]
  }
})

const modelChartData = computed(() => buildBreakdownChartData(stats.value?.model_breakdown || [], point => point.model || '(unknown)'))

const userAgentChartData = computed(() => buildBreakdownChartData(
  stats.value?.user_agent_breakdown || [],
  point => formatUserAgent(point.user_agent || '(unknown)')
))

const modelLegendItems = computed(() => buildDoughnutLegendItems(
  stats.value?.model_breakdown || [],
  point => point.model || '(unknown)'
))
const userAgentLegendItems = computed(() => buildDoughnutLegendItems(
  stats.value?.user_agent_breakdown || [],
  point => point.user_agent || '(unknown)',
  fullLabel => formatUserAgent(fullLabel)
))

const qualityErrorChartData = computed(() => buildBreakdownChartData(
  stats.value?.quality_error_breakdown || [],
  point => qualityErrorLabel(point.error_code)
))
const qualityErrorLegendItems = computed(() => buildDoughnutLegendItems(
  stats.value?.quality_error_breakdown || [],
  point => qualityErrorLabel(point.error_code)
))

const invalidUserChartData = computed(() => {
  const points = stats.value?.invalid_user_breakdown || []
  if (!points.length) return null
  return {
    labels: points.map(point => invalidUserLabel(point)),
    datasets: [
      {
        label: '无效 Session',
        data: points.map(point => point.invalid_count),
        backgroundColor: '#ef444488',
        borderColor: '#ef4444',
        borderWidth: 1,
        maxBarThickness: 28
      }
    ]
  }
})

const lineChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' as const },
  scales: {
    x: { ticks: { color: chartColors.value.text }, grid: { color: chartColors.value.grid } },
    y: {
      ticks: { color: chartColors.value.text, callback: (value: string | number) => formatBytes(Number(value)) },
      grid: { color: chartColors.value.grid }
    },
    y1: {
      position: 'right' as const,
      ticks: { color: chartColors.value.text },
      grid: { drawOnChartArea: false }
    }
  },
  plugins: {
    legend: { labels: { color: chartColors.value.text } },
    tooltip: {
      callbacks: {
        label: (ctx: any) => ctx.dataset.yAxisID === 'y1'
          ? `${ctx.dataset.label}: ${formatNumber(ctx.raw)}`
          : `${ctx.dataset.label}: ${formatBytes(ctx.raw)}`
      }
    }
  }
}))

const barChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  indexAxis: 'y' as const,
  scales: {
    x: {
      ticks: { color: chartColors.value.text, callback: (value: string | number) => formatBytes(Number(value)) },
      grid: { color: chartColors.value.grid }
    },
    y: { ticks: { color: chartColors.value.text }, grid: { color: chartColors.value.grid } }
  },
  plugins: {
    legend: { labels: { color: chartColors.value.text } },
    tooltip: {
      callbacks: {
        label: (ctx: any) => `${ctx.dataset.label}: ${formatBytes(ctx.raw)}`
      }
    }
  }
}))

const doughnutChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { position: 'bottom' as const, labels: { color: chartColors.value.text } },
    tooltip: {
      callbacks: {
        label: (ctx: any) => {
          const point = stats.value?.request_path_breakdown?.[ctx.dataIndex]
          if (!point) return `${ctx.label}: ${formatNumber(ctx.raw)}`
          return `${ctx.label}: ${formatNumber(point.session_count)} · ${formatBytes(point.storage_bytes)} · ${formatNumber(point.total_tokens)} tokens`
        }
      }
    }
  }
}))

const modelDoughnutChartOptions = computed(() =>
  buildDoughnutChartOptions(stats.value?.model_breakdown || [], { legend: false })
)
const userAgentDoughnutChartOptions = computed(() =>
  buildDoughnutChartOptions(stats.value?.user_agent_breakdown || [], { legend: false })
)
const qualityErrorDoughnutChartOptions = computed(() =>
  buildSessionCountDoughnutChartOptions(stats.value?.quality_error_breakdown || [], { legend: false })
)
const invalidUserBarChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  indexAxis: 'y' as const,
  onClick: (_event: unknown, elements: Array<{ index: number }>) => {
    const index = elements?.[0]?.index
    if (typeof index === 'number') {
      applyInvalidUserFilter(stats.value?.invalid_user_breakdown?.[index])
    }
  },
  onHover: (event: any, elements: Array<{ index: number }>) => {
    if (event?.native?.target?.style) {
      event.native.target.style.cursor = elements?.length ? 'pointer' : 'default'
    }
  },
  scales: {
    x: {
      beginAtZero: true,
      ticks: { color: chartColors.value.text, precision: 0 },
      grid: { color: chartColors.value.grid }
    },
    y: {
      ticks: {
        color: chartColors.value.text,
        callback: (_value: string | number, index: number) => truncateChartLabel(invalidUserLabel(stats.value?.invalid_user_breakdown?.[index]), 22)
      },
      grid: { color: chartColors.value.grid }
    }
  },
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (ctx: any) => {
          const point = stats.value?.invalid_user_breakdown?.[ctx.dataIndex]
          if (!point) return `无效 Session: ${formatNumber(ctx.raw)}`
          return [
            `无效 Session: ${formatNumber(point.invalid_count)}`,
            `总 Session: ${formatNumber(point.session_count)}`,
            `无效率: ${formatPercent(point.invalid_ratio)}`,
            `空间: ${formatBytes(point.storage_bytes)}`,
            `Token: ${formatNumber(point.total_tokens)}`
          ]
        }
      }
    }
  }
}))
const durationBucketChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  indexAxis: 'y' as const,
  scales: {
    x: {
      beginAtZero: true,
      ticks: { color: chartColors.value.text, precision: 0 },
      grid: { color: chartColors.value.grid }
    },
    y: { ticks: { color: chartColors.value.text }, grid: { color: chartColors.value.grid } }
  },
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (ctx: any) => `样本: ${formatNumber(Number(ctx.raw || 0))}`
      }
    }
  }
}))
const exportDurationChartOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  indexAxis: 'y' as const,
  scales: {
    x: {
      beginAtZero: true,
      ticks: {
        color: chartColors.value.text,
        callback: (value: string | number) => formatDurationMillis(Number(value))
      },
      grid: { color: chartColors.value.grid }
    },
    y: {
      ticks: { color: chartColors.value.text },
      grid: { color: chartColors.value.grid }
    }
  },
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (ctx: any) => {
          const part = exportDurationChartParts.value[ctx.dataIndex]
          if (!part) return `P95: ${formatDurationMillis(Number(ctx.raw || 0))}`
          return [
            `最近: ${formatDurationMillis(part.last_millis)}`,
            `P95: ${formatDurationMillis(part.p95_millis)}`,
            `平均: ${formatDurationMillis(part.avg_millis)}`,
            `最大: ${formatDurationMillis(part.max_millis)}`,
            `样本: ${formatNumber(part.sample_count)}`
          ]
        }
      }
    }
  }
}))
const captureWorkerQueueText = computed(() => {
  const worker = stats.value?.capture_worker
  if (!worker) return '-'
  return `${formatNumber(worker.queue_depth)}/${formatNumber(worker.queue_capacity)}`
})
const captureWorkerQueueProgress = computed(() => {
  const worker = stats.value?.capture_worker
  if (!worker?.queue_capacity) return 0
  return Math.min(Math.max((worker.queue_depth / worker.queue_capacity) * 100, 0), 100)
})
const captureWorkerQueueRatioText = computed(() => `${captureWorkerQueueProgress.value.toFixed(1)}%`)
const captureWorkerFlushQueueText = computed(() => {
  const worker = stats.value?.capture_worker
  if (!worker) return '-'
  return `${formatNumber(worker.flush_queue_depth || 0)}/${formatNumber(worker.flush_queue_capacity || worker.queue_capacity || 0)}`
})
const captureWorkerFlushQueueProgress = computed(() => {
  const worker = stats.value?.capture_worker
  const capacity = worker?.flush_queue_capacity || worker?.queue_capacity || 0
  if (!capacity) return 0
  return Math.min(Math.max(((worker?.flush_queue_depth || 0) / capacity) * 100, 0), 100)
})
const captureWorkerFlushQueueRatioText = computed(() => `${captureWorkerFlushQueueProgress.value.toFixed(1)}%`)
const captureWorkerRunningWorkers = computed(() => stats.value?.capture_worker?.running_workers || 0)
const captureWorkerAvailableWorkers = computed(() => stats.value?.capture_worker?.available_workers || 0)
const captureWorkerCompletedTotal = computed(() => stats.value?.capture_worker?.completed_total || 0)
const captureWorkerFailedTotal = computed(() => stats.value?.capture_worker?.failed_total || 0)
const captureWorkerTimeoutTotal = computed(() => stats.value?.capture_worker?.timeout_total || 0)
const captureWorkerDroppedTotal = computed(() => stats.value?.capture_worker?.dropped_total || 0)
const captureWorkerLastErrorText = computed(() => stats.value?.capture_worker?.last_error?.trim() || '')
const captureWorkerHistoryNoticeText = computed(() => {
  if (captureWorkerLastErrorText.value) return captureWorkerLastErrorText.value
  if (captureWorkerFailedTotal.value > 0 || captureWorkerTimeoutTotal.value > 0) return '暂无最近错误详情，可能来自旧版本清空的历史错误'
  return ''
})
const captureWorkerLastErrorActive = computed(() => statsRecordHasActiveError(
  stats.value?.capture_worker?.last_error_at,
  stats.value?.capture_worker?.last_success_at
))
const captureWorkerLastErrorRecovered = computed(() => statsRecordHasRecoveredError(
  stats.value?.capture_worker?.last_error_at,
  stats.value?.capture_worker?.last_success_at
))
const captureWorkerLastErrorPrefix = computed(() => captureLastErrorPrefix(
  captureWorkerLastErrorActive.value,
  stats.value?.capture_worker?.last_error_at
))
const captureWorkerLastErrorTitle = computed(() => captureLastErrorTitle(
  captureWorkerHistoryNoticeText.value,
  stats.value?.capture_worker?.last_error_at,
  stats.value?.capture_worker?.last_success_at
))
const captureWorkerStatusText = computed(() => {
  const worker = stats.value?.capture_worker
  if (!worker) return '未启用'
  if (captureWorkerLastErrorActive.value) return '有失败'
  if (captureWorkerLastErrorRecovered.value) return '已恢复'
  if (worker.failed_total > 0 || worker.timeout_total > 0) return '历史失败'
  if (worker.dropped_total > 0) return '曾丢弃'
  return '正常'
})
const captureWorkerTaskTimeoutSeconds = computed(() => stats.value?.capture_worker?.task_timeout_seconds || 0)
const captureWorkerCompressionLevel = computed(() => stats.value?.capture_worker?.compression_level || '')
const captureWorkerCount = computed(() => stats.value?.capture_worker?.worker_count || 0)
const captureWorkerQueueCapacity = computed(() => stats.value?.capture_worker?.queue_capacity || 0)
const captureWorkerFlushQueueCapacity = computed(() => stats.value?.capture_worker?.flush_queue_capacity || 0)
const captureWorkerSlotLimit = 64
const captureWorkerHiddenCount = computed(() => Math.max(captureWorkerCount.value - captureWorkerSlotLimit, 0))
type CaptureWorkerJobKind = 'capture' | 'flush' | ''
type CaptureWorkerSlotState = 'active' | 'idle' | 'disabled'
const captureWorkerSlots = computed(() => {
  const total = Math.min(Math.max(captureWorkerCount.value, 0), captureWorkerSlotLimit)
  const active = Math.max(captureWorkerRunningWorkers.value, 0)
  const enabled = Boolean(stats.value?.capture_worker)
  const workerStates = stats.value?.capture_worker?.worker_states || []
  return Array.from({ length: total }, (_, index) => ({
    id: index + 1,
    jobKind: normalizeCaptureWorkerJobKind(workerStates[index]?.job_kind),
    state: (!enabled ? 'disabled' : workerStates.length > index ? (workerStates[index]?.job_kind ? 'active' : 'idle') : index < active ? 'active' : 'idle') as CaptureWorkerSlotState,
    label: captureWorkerSlotLabel(enabled, normalizeCaptureWorkerJobKind(workerStates[index]?.job_kind), index < active)
  }))
})
const captureWorkerBadgeClass = computed(() => {
  const worker = stats.value?.capture_worker
  if (!worker) return 'badge-gray'
  if (captureWorkerLastErrorActive.value) return 'badge-danger'
  if (worker.dropped_total > 0 || worker.timeout_total > 0 || worker.failed_total > 0) return 'badge-warning'
  return 'badge-success'
})
const captureBufferEnabled = computed(() => stats.value?.capture_buffer?.enabled ?? false)
const captureBufferIdleFlushSeconds = computed(() => stats.value?.capture_buffer?.idle_flush_seconds || 0)
const captureBufferMaxSessions = computed(() => stats.value?.capture_buffer?.max_sessions || 0)
const captureBufferMaxPendingEvents = computed(() => stats.value?.capture_buffer?.max_pending_events || 0)
const captureBufferBufferedSessions = computed(() => stats.value?.capture_buffer?.buffered_sessions || 0)
const captureBufferPendingEvents = computed(() => stats.value?.capture_buffer?.pending_events || 0)
const captureBufferFlushingSessions = computed(() => stats.value?.capture_buffer?.flushing_sessions || 0)
const captureBufferSubmittedTotal = computed(() => stats.value?.capture_buffer?.submitted_total || 0)
const captureBufferFlushSuccessTotal = computed(() => stats.value?.capture_buffer?.flush_success_total || 0)
const captureBufferFlushFailedTotal = computed(() => stats.value?.capture_buffer?.flush_failed_total || 0)
const captureBufferDroppedTotal = computed(() => stats.value?.capture_buffer?.dropped_total || 0)
const captureBufferLastFlushDurationMillis = computed(() => stats.value?.capture_buffer?.last_flush_duration_millis || 0)
const captureBufferLastErrorText = computed(() => stats.value?.capture_buffer?.last_error?.trim() || '')
const captureBufferHistoryNoticeText = computed(() => {
  if (captureBufferLastErrorText.value) return captureBufferLastErrorText.value
  if (captureBufferFlushFailedTotal.value > 0) return '暂无最近错误详情，可能来自旧版本清空的历史错误'
  return ''
})
const captureBufferLastErrorActive = computed(() => statsRecordHasActiveError(
  stats.value?.capture_buffer?.last_error_at,
  stats.value?.capture_buffer?.last_success_at
))
const captureBufferLastErrorRecovered = computed(() => statsRecordHasRecoveredError(
  stats.value?.capture_buffer?.last_error_at,
  stats.value?.capture_buffer?.last_success_at
))
const captureBufferLastErrorPrefix = computed(() => captureLastErrorPrefix(
  captureBufferLastErrorActive.value,
  stats.value?.capture_buffer?.last_error_at
))
const captureBufferLastErrorTitle = computed(() => captureLastErrorTitle(
  captureBufferHistoryNoticeText.value,
  stats.value?.capture_buffer?.last_error_at,
  stats.value?.capture_buffer?.last_success_at
))
const captureDurationWindowSize = computed(() => stats.value?.capture_durations?.window_size || captureDurationWindowDefault)
const captureDurationSampleCount = computed(() => stats.value?.capture_durations?.sample_count || 0)
const captureDurationParts = computed(() => stats.value?.capture_durations?.parts || [])
const captureFlushTotalDurationPart = computed(() => captureDurationParts.value.find(part => part.key === 'flush_total'))
const exportDurationWindowSize = computed(() => stats.value?.export_durations?.window_size || captureDurationWindowDefault)
const exportDurationSampleCount = computed(() => stats.value?.export_durations?.sample_count || 0)
const exportDurationParts = computed(() => stats.value?.export_durations?.parts || [])
const exportDurationActiveParts = computed(() => exportDurationParts.value.filter(part => part.sample_count > 0))
const exportGenerateTotalDurationPart = computed(() => exportDurationParts.value.find(part => part.key === 'export_generate_total'))
// 图表排除总耗时，避免总耗时数量级过大导致分页、解码等子阶段不可读。
const exportDurationChartParts = computed(() => exportDurationActiveParts.value.filter(part => part.key !== 'export_generate_total'))
const exportDurationChartData = computed(() => ({
  labels: exportDurationChartParts.value.map(part => part.label),
  datasets: [
    {
      label: 'P95',
      data: exportDurationChartParts.value.map(part => part.p95_millis),
      backgroundColor: '#0ea5e988',
      borderColor: '#0ea5e9',
      borderWidth: 1
    }
  ]
}))
const captureBufferRecentDurationMillis = computed(() => {
  const flushTotal = captureFlushTotalDurationPart.value
  return flushTotal && flushTotal.sample_count > 0 ? flushTotal.last_millis : captureBufferLastFlushDurationMillis.value
})
const captureBufferSessionProgress = computed(() => ratioPercent(captureBufferBufferedSessions.value, captureBufferMaxSessions.value))
const captureBufferPendingProgress = computed(() => ratioPercent(captureBufferPendingEvents.value, captureBufferMaxPendingEvents.value))
const captureBufferSessionRatioText = computed(() => `${captureBufferSessionProgress.value.toFixed(1)}%`)
const captureBufferPendingRatioText = computed(() => `${captureBufferPendingProgress.value.toFixed(1)}%`)
const captureBufferStatusText = computed(() => {
  const buffer = stats.value?.capture_buffer
  if (!buffer) return '未启用'
  if (!buffer.enabled) return '已关闭'
  if (captureBufferLastErrorActive.value) return '有失败'
  if (buffer.flushing_sessions > 0) return 'Flush 中'
  if (captureBufferLastErrorRecovered.value) return '已恢复'
  if (buffer.flush_failed_total > 0) return '历史失败'
  if (buffer.dropped_total > 0) return '曾丢弃'
  return '正常'
})
const captureBufferBadgeClass = computed(() => {
  const buffer = stats.value?.capture_buffer
  if (!buffer || !buffer.enabled) return 'badge-gray'
  if (captureBufferLastErrorActive.value) return 'badge-danger'
  if (buffer.dropped_total > 0 || buffer.flush_failed_total > 0) return 'badge-warning'
  return 'badge-success'
})
const skipRulesSummary = computed(() => {
  const total = skipRules.value.length
  const enabled = skipRules.value.filter(rule => rule.enabled).length
  return `${enabled}/${total} 启用`
})

const storageLimitProgress = computed(() => {
  if (!storageLimit.value?.enabled) return 0
  return Math.min(Math.max(storageLimit.value.usage_ratio * 100, 0), 100)
})

const storageLimitStatusText = computed(() => {
  if (!storageLimit.value?.enabled) return '未设置阈值'
  const limit = formatBytes(storageLimit.value.limit_bytes)
  if (storageLimit.value.exceeded) return `已超过 ${limit}`
  return `阈值 ${limit} · ${storageLimitProgress.value.toFixed(1)}%`
})

const requestPathOptions = computed(() => {
  return [
    { value: 'all', label: '全部路径' },
    ...filterOptions.value.request_paths.map(value => ({ value, label: value }))
  ]
})

const modelOptions = computed(() => {
  return [
    { value: 'all', label: '全部模型' },
    ...filterOptions.value.models.map(value => ({ value, label: value }))
  ]
})

const userAgentOptions = computed(() => {
  return [
    { value: 'all', label: '全部 User Agent' },
    ...filterOptions.value.user_agents.map(value => ({ value, label: formatUserAgent(value) }))
  ]
})

const selectedCount = computed(() => {
  if (selectAllMatching.value) {
    return Math.max(pagination.total - excludedIds.value.size, 0)
  }
  return selectedIds.value.size
})
const currentPageSelectedCount = computed(() => sessions.value.filter(row => isSelected(row.id)).length)
const currentPageAllSelected = computed(() => sessions.value.length > 0 && currentPageSelectedCount.value === sessions.value.length)
const currentPageSelectionIndeterminate = computed(() => currentPageSelectedCount.value > 0 && !currentPageAllSelected.value)
const selectionSummary = computed(() => {
  if (selectAllMatching.value) {
    return `已选择当前筛选条件下 ${formatNumber(selectedCount.value)} 条数据`
  }
  return `已选择 ${formatNumber(selectedCount.value)} 条数据`
})
const prettySession = computed(() => JSON.stringify(selectedSession.value?.session_json || selectedSession.value, null, 2))
const invalidUserFilterLabel = computed(() => filters.user_filter_label || (filters.user_id > 0 ? `#${filters.user_id}` : ''))
const statsAutoRefreshTitle = computed(() => {
  if (!statsAutoRefreshEnabled.value) return '自动刷新已关闭'
  return `每 ${statsAutoRefreshIntervalSeconds.value} 秒自动刷新统计`
})
const cancelUploadDialogMessage = computed(() => {
  const target = cancelUploadTarget.value
  if (!target) return '确定取消当前上传任务吗？'
  return `确定取消上传 ${target.filename} 吗？已上传到远端的临时分片会由对象存储清理，本地导出文件会保留。`
})

let filterTimer: number | null = null
let statsAutoRefreshTimer: number | null = null

function buildFilters(): AdminDataShareSessionFilters {
  const out: AdminDataShareSessionFilters = {
    sort_by: sortState.sort_by,
    sort_order: sortState.sort_order
  }
  if (filters.search.trim()) out.search = filters.search.trim()
  if (filters.user_name.trim()) out.user_name = filters.user_name.trim()
  if (filters.user_id > 0) out.user_id = filters.user_id
  if (filters.api_key_name.trim()) out.api_key_name = filters.api_key_name.trim()
  if (filters.group_name.trim()) out.group_name = filters.group_name.trim()
  if (filters.model !== 'all') out.model = filters.model
  if (filters.request_path !== 'all') out.request_path = filters.request_path
  if (filters.user_agent !== 'all') out.user_agent = filters.user_agent
  if (filters.quality_status !== 'all') out.quality_status = filters.quality_status
  if (filters.start_date) out.start_date = filters.start_date
  if (filters.end_date) out.end_date = filters.end_date
  return out
}

async function loadNotice() {
  try {
    notice.value = await adminDataSharingAPI.getNotice()
    noticeContent.value = notice.value.content
  } catch (error) {
    appStore.showError('加载数据共享须知失败')
  }
}

async function saveNotice() {
  savingNotice.value = true
  try {
    notice.value = await adminDataSharingAPI.updateNotice(noticeContent.value)
    noticeContent.value = notice.value.content
    appStore.showSuccess('数据共享须知已保存')
  } catch (error) {
    appStore.showError('保存数据共享须知失败')
  } finally {
    savingNotice.value = false
  }
}

async function loadSkipRules() {
  skipRulesLoading.value = true
  try {
    skipRules.value = cloneSkipRules(await adminDataSharingAPI.getSkipRules())
  } catch (error) {
    appStore.showError('加载采集跳过规则失败')
  } finally {
    skipRulesLoading.value = false
  }
}

async function loadStorageLimit() {
  storageLimitLoading.value = true
  try {
    storageLimit.value = await adminDataSharingAPI.getStorageLimit()
    applyStorageLimitToForm(storageLimit.value.limit_bytes)
  } catch (error) {
    appStore.showError('加载采集空间阈值失败')
  } finally {
    storageLimitLoading.value = false
  }
}

async function saveStorageLimit() {
  savingStorageLimit.value = true
  try {
    storageLimit.value = await adminDataSharingAPI.updateStorageLimit(storageLimitBytesFromForm())
    applyStorageLimitToForm(storageLimit.value.limit_bytes)
    await loadStats()
    appStore.showSuccess('采集空间阈值已保存')
  } catch (error) {
    appStore.showError('保存采集空间阈值失败')
  } finally {
    savingStorageLimit.value = false
  }
}

async function saveCaptureRuntimeSettings() {
  savingCaptureRuntimeSettings.value = true
  try {
    const settings = await adminDataSharingAPI.updateRuntimeSettings(captureRuntimeSettingsFromForm())
    applyCaptureRuntimeSettingsToForm(settings)
    await loadStats()
    appStore.showSuccess('采集 Worker 配置已更新')
  } catch (error) {
    appStore.showError('保存采集 Worker 配置失败')
  } finally {
    savingCaptureRuntimeSettings.value = false
  }
}

async function loadCaptureRuntimeSettings() {
  try {
    applyCaptureRuntimeSettingsToForm(await adminDataSharingAPI.getRuntimeSettings())
  } catch (error) {
    appStore.showError('加载采集 Worker 配置失败')
  }
}

async function loadExportRemoteConfig() {
  try {
    const cfg = await adminDataSharingAPI.getExportRemoteConfig()
    exportRemoteForm.value = normalizeExportRemoteConfig(cfg)
    exportRemoteSecretConfigured.value = Boolean(cfg.access_key_id)
  } catch (error) {
    appStore.showError('加载远端上传配置失败')
  }
}

async function saveExportSettings() {
  savingExportSettings.value = true
  try {
    await saveExportRuntimeSettingsOnly()
    await saveExportRemoteConfigOnly()
    await loadStats()
    appStore.showSuccess('导出配置已保存')
  } catch (error) {
    appStore.showError('保存导出配置失败')
  } finally {
    savingExportSettings.value = false
  }
}

async function saveExportRuntimeSettingsOnly() {
  const exportBatchSize = boundedIntegerFromInput(exportBatchSizeInput.value, exportBatchSizeMin, exportBatchSizeMax)
  if (!exportBatchSize) {
    throw new Error('invalid export batch size')
  }
  const exportWorkerCount = boundedIntegerFromInput(exportWorkerCountInput.value, exportWorkerCountMin, exportWorkerCountMax)
  if (!exportWorkerCount) {
    throw new Error('invalid export worker count')
  }
  const current = await adminDataSharingAPI.getRuntimeSettings()
  const settings = await adminDataSharingAPI.updateRuntimeSettings({
    ...current,
    export_batch_size: exportBatchSize,
    export_worker_count: exportWorkerCount
  })
  exportBatchSizeInput.value = String(settings.export_batch_size || exportBatchSizeDefault)
  exportWorkerCountInput.value = String(settings.export_worker_count || exportWorkerCountDefault)
}

async function saveExportRemoteConfigOnly() {
  savingExportRemoteConfig.value = true
  try {
    const cfg = await adminDataSharingAPI.updateExportRemoteConfig(buildExportRemoteConfigPayload())
    exportRemoteForm.value = normalizeExportRemoteConfig(cfg)
    exportRemoteSecretConfigured.value = Boolean(cfg.access_key_id)
  } finally {
    savingExportRemoteConfig.value = false
  }
}

async function testExportRemoteConfig() {
  testingExportRemoteConfig.value = true
  try {
    const result = await adminDataSharingAPI.testExportRemoteConfig(buildExportRemoteConfigPayload())
    if (result.ok) {
      appStore.showSuccess(result.message || '连接成功')
    } else {
      appStore.showError(result.message || '连接失败')
    }
  } catch (error) {
    appStore.showError('测试远端上传配置失败')
  } finally {
    testingExportRemoteConfig.value = false
  }
}

function normalizeExportRemoteConfig(cfg?: Partial<DataShareExportRemoteConfig>): DataShareExportRemoteConfig {
  return {
    endpoint: cfg?.endpoint || '',
    region: cfg?.region || 'auto',
    bucket: cfg?.bucket || '',
    access_key_id: cfg?.access_key_id || '',
    secret_access_key: '',
    prefix: cfg?.prefix || 'data-sharing-exports',
    force_path_style: Boolean(cfg?.force_path_style),
    upload_concurrency: boundedIntegerFromInput(String(cfg?.upload_concurrency || exportUploadConcurrencyDefault), exportUploadConcurrencyMin, exportUploadConcurrencyMax) || exportUploadConcurrencyDefault,
    upload_part_size_mb: boundedIntegerFromInput(String(cfg?.upload_part_size_mb || exportUploadPartSizeMBDefault), exportUploadPartSizeMBMin, exportUploadPartSizeMBMax) || exportUploadPartSizeMBDefault
  }
}

function buildExportRemoteConfigPayload(): DataShareExportRemoteConfig {
  const cfg = normalizeExportRemoteConfig(exportRemoteForm.value)
  cfg.secret_access_key = exportRemoteForm.value.secret_access_key || ''
  cfg.upload_concurrency = boundedIntegerFromInput(String(exportRemoteForm.value.upload_concurrency), exportUploadConcurrencyMin, exportUploadConcurrencyMax) || exportUploadConcurrencyDefault
  cfg.upload_part_size_mb = boundedIntegerFromInput(String(exportRemoteForm.value.upload_part_size_mb), exportUploadPartSizeMBMin, exportUploadPartSizeMBMax) || exportUploadPartSizeMBDefault
  return cfg
}

function captureRuntimeSettingsFromForm() {
  const workerCount = boundedPositiveIntegerFromInput(captureWorkerCountInput.value, captureWorkerCountMax)
  const queueSize = boundedPositiveIntegerFromInput(captureQueueSizeInput.value, captureQueueSizeMax)
  const flushQueueSize = boundedPositiveIntegerFromInput(captureFlushQueueSizeInput.value, captureFlushQueueSizeMax)
  const timeoutSeconds = boundedPositiveIntegerFromInput(captureTimeoutInput.value, captureTimeoutSecondsMax)
  const bufferIdleFlushSeconds = boundedPositiveIntegerFromInput(captureBufferIdleFlushInput.value, captureBufferIdleFlushMax)
  const bufferMaxSessions = boundedPositiveIntegerFromInput(captureBufferMaxSessionsInput.value, captureBufferMaxSessionsLimit)
  const bufferMaxPendingEvents = boundedPositiveIntegerFromInput(captureBufferMaxPendingEventsInput.value, captureBufferMaxPendingEventsLimit)
  const durationWindowSize = boundedIntegerFromInput(captureDurationWindowInput.value, captureDurationWindowMin, captureDurationWindowMax)
  const exportBatchSize = boundedIntegerFromInput(exportBatchSizeInput.value, exportBatchSizeMin, exportBatchSizeMax)
  const exportWorkerCount = boundedIntegerFromInput(exportWorkerCountInput.value, exportWorkerCountMin, exportWorkerCountMax)
  if (!workerCount || !queueSize || !flushQueueSize || !timeoutSeconds || !bufferIdleFlushSeconds || !bufferMaxSessions || !bufferMaxPendingEvents || !durationWindowSize || !exportBatchSize || !exportWorkerCount) {
    throw new Error('invalid capture runtime settings')
  }
  return {
    worker_count: workerCount,
    queue_size: queueSize,
    flush_queue_size: flushQueueSize,
    task_timeout_seconds: timeoutSeconds,
    compression_level: normalizeCaptureCompressionLevel(captureCompressionLevelInput.value),
    buffer_enabled: true,
    buffer_idle_flush_seconds: bufferIdleFlushSeconds,
    buffer_max_sessions: bufferMaxSessions,
    buffer_max_pending_events: bufferMaxPendingEvents,
    duration_window_size: durationWindowSize,
    export_batch_size: exportBatchSize,
    export_worker_count: exportWorkerCount
  }
}

function normalizeCaptureCompressionLevel(level: string) {
  return captureCompressionLevelOptions.some(option => option.value === level) ? level : 'fastest'
}

function boundedPositiveIntegerFromInput(value: string, max: number) {
  const raw = Number(value)
  if (!Number.isFinite(raw) || raw <= 0) return 0
  return Math.min(Math.round(raw), max)
}

function boundedIntegerFromInput(value: string, min: number, max: number) {
  const raw = Number(value)
  if (!Number.isFinite(raw) || raw <= 0) return 0
  return Math.min(Math.max(Math.round(raw), min), max)
}

function ratioPercent(value: number, total: number) {
  if (!total) return 0
  return Math.min(Math.max((value / total) * 100, 0), 100)
}

function applyCaptureRuntimeSettingsToForm(settings: {
  worker_count: number
  queue_size: number
  flush_queue_size?: number
  task_timeout_seconds: number
  compression_level?: string
  buffer_enabled?: boolean
  buffer_idle_flush_seconds?: number
  buffer_max_sessions?: number
  buffer_max_pending_events?: number
  duration_window_size?: number
  export_batch_size?: number
  export_worker_count?: number
}) {
  captureWorkerCountInput.value = String(settings.worker_count)
  captureQueueSizeInput.value = String(settings.queue_size)
  captureFlushQueueSizeInput.value = String(settings.flush_queue_size || settings.queue_size)
  captureTimeoutInput.value = String(settings.task_timeout_seconds)
  captureCompressionLevelInput.value = normalizeCaptureCompressionLevel(settings.compression_level || 'fastest')
  captureBufferEnabledInput.value = true
  captureBufferIdleFlushInput.value = String(settings.buffer_idle_flush_seconds || 30)
  captureBufferMaxSessionsInput.value = String(settings.buffer_max_sessions || 4096)
  captureBufferMaxPendingEventsInput.value = String(settings.buffer_max_pending_events || 65536)
  captureDurationWindowInput.value = String(settings.duration_window_size || captureDurationWindowDefault)
  exportBatchSizeInput.value = String(settings.export_batch_size || exportBatchSizeDefault)
  exportWorkerCountInput.value = String(settings.export_worker_count || exportWorkerCountDefault)
}

function applyStorageLimitToForm(limitBytes: number) {
  if (!limitBytes || limitBytes <= 0) {
    storageLimitInput.value = ''
    storageLimitUnit.value = 'GB'
    return
  }
  const tb = 1024 ** 4
  const gb = 1024 ** 3
  const mb = 1024 ** 2
  if (limitBytes % tb === 0) {
    storageLimitUnit.value = 'TB'
    storageLimitInput.value = String(limitBytes / tb)
  } else if (limitBytes % gb === 0) {
    storageLimitUnit.value = 'GB'
    storageLimitInput.value = String(limitBytes / gb)
  } else {
    storageLimitUnit.value = 'MB'
    storageLimitInput.value = trimDecimal(limitBytes / mb)
  }
}

function storageLimitBytesFromForm() {
  const raw = Number(storageLimitInput.value)
  if (!Number.isFinite(raw) || raw <= 0) return 0
  const multiplier = storageLimitUnit.value === 'TB'
    ? 1024 ** 4
    : storageLimitUnit.value === 'GB'
      ? 1024 ** 3
      : 1024 ** 2
  return Math.round(raw * multiplier)
}

function trimDecimal(value: number) {
  return value.toFixed(2).replace(/\.?0+$/, '')
}

async function saveSkipRules() {
  savingSkipRules.value = true
  try {
    skipRules.value = cloneSkipRules(await adminDataSharingAPI.updateSkipRules(normalizeSkipRulesForSave()))
    appStore.showSuccess('采集跳过规则已保存')
  } catch (error) {
    appStore.showError('保存采集跳过规则失败')
  } finally {
    savingSkipRules.value = false
  }
}

function toggleSkipRulesExpanded() {
  skipRulesExpanded.value = !skipRulesExpanded.value
  if (!skipRulesExpanded.value) {
    openSkipRulePathMenu.value = null
  }
}

function restoreDefaultSkipRules() {
  skipRulesExpanded.value = true
  skipRules.value = cloneSkipRules(defaultSkipRules)
}

function addSkipRule() {
  skipRulesExpanded.value = true
  skipRules.value = [
    ...skipRules.value,
    {
      id: `custom_${Date.now()}`,
      name: '自定义跳过规则',
      enabled: true,
      client_families: [],
      request_paths: [],
      models: [],
      field_scopes: ['messages'],
      patterns: [],
      case_sensitive: false,
      match_mode: 'contains'
    }
  ]
}

function removeSkipRule(index: number) {
  skipRules.value = skipRules.value.filter((_, i) => i !== index)
}

function toggleSkipRuleScope(rule: DataShareCaptureSkipRule, scope: DataShareCaptureSkipRuleFieldScope, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const values = new Set(rule.field_scopes)
  if (checked) {
    values.add(scope)
  } else {
    values.delete(scope)
  }
  rule.field_scopes = Array.from(values)
}

function toggleSkipRulePathMenu(key: string) {
  openSkipRulePathMenu.value = openSkipRulePathMenu.value === key ? null : key
}

function setSkipRulePathMenuRef(key: string, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLElement) {
    skipRulePathMenuRefs.set(key, el)
  } else {
    skipRulePathMenuRefs.delete(key)
  }
}

function closeSkipRulePathMenuOnOutsideClick(event: MouseEvent) {
  const key = openSkipRulePathMenu.value
  if (!key) return
  const container = skipRulePathMenuRefs.get(key)
  if (container && event.target instanceof Node && container.contains(event.target)) {
    return
  }
  openSkipRulePathMenu.value = null
}

function toggleSkipRulePath(rule: DataShareCaptureSkipRule, path: string, event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const values = new Set(rule.request_paths)
  if (checked) {
    values.add(path)
  } else {
    values.delete(path)
  }
  rule.request_paths = Array.from(values)
}

function formatSkipRulePaths(paths: string[]) {
  if (!paths.length) return '不限路径'
  return paths.join(', ')
}

function eventValue(event: Event) {
  return (event.target as HTMLInputElement | HTMLTextAreaElement).value
}

function joinList(values: string[]) {
  return values.join(', ')
}

function joinLines(values: string[]) {
  return values.join('\n')
}

function splitList(value: string) {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

function splitLines(value: string) {
  return value.split(/\r?\n/).map(item => item.trim()).filter(Boolean)
}

function setSkipRuleList(rule: DataShareCaptureSkipRule, key: 'client_families' | 'request_paths' | 'models', value: string) {
  rule[key] = splitList(value)
}

function setSkipRulePatterns(rule: DataShareCaptureSkipRule, value: string) {
  rule.patterns = splitLines(value)
}

function cloneSkipRules(rules: DataShareCaptureSkipRule[]) {
  return rules.map(rule => ({
    ...rule,
    client_families: [...(rule.client_families || [])],
    request_paths: [...(rule.request_paths || [])],
    models: [...(rule.models || [])],
    field_scopes: [...(rule.field_scopes || [])],
    patterns: [...(rule.patterns || [])]
  }))
}

function normalizeSkipRulesForSave() {
  return cloneSkipRules(skipRules.value).map(rule => ({
    ...rule,
    id: rule.id.trim(),
    name: rule.name.trim(),
    client_families: rule.client_families.map(item => item.trim()).filter(Boolean),
    request_paths: rule.request_paths.map(item => item.trim()).filter(Boolean),
    models: rule.models.map(item => item.trim()).filter(Boolean),
    field_scopes: rule.field_scopes.filter(scope => skipRuleScopeOptions.some(option => option.value === scope)),
    patterns: rule.patterns.map(item => item.trim()).filter(Boolean)
  }))
}

async function loadStats() {
  statsLoading.value = true
  try {
    stats.value = await adminDataSharingAPI.getStats(buildFilters())
    if (!captureWorkerCountInput.value && captureWorkerCount.value > 0) {
      captureWorkerCountInput.value = String(captureWorkerCount.value)
    }
    if (!captureQueueSizeInput.value && captureWorkerQueueCapacity.value > 0) {
      captureQueueSizeInput.value = String(captureWorkerQueueCapacity.value)
    }
    if (!captureFlushQueueSizeInput.value && captureWorkerFlushQueueCapacity.value > 0) {
      captureFlushQueueSizeInput.value = String(captureWorkerFlushQueueCapacity.value)
    }
    if (!captureTimeoutInput.value && captureWorkerTaskTimeoutSeconds.value > 0) {
      captureTimeoutInput.value = String(captureWorkerTaskTimeoutSeconds.value)
    }
    if (!captureCompressionLevelInput.value && captureWorkerCompressionLevel.value) {
      captureCompressionLevelInput.value = normalizeCaptureCompressionLevel(captureWorkerCompressionLevel.value)
    }
    if (!captureBufferIdleFlushInput.value && captureBufferIdleFlushSeconds.value > 0) {
      captureBufferIdleFlushInput.value = String(captureBufferIdleFlushSeconds.value)
    }
    if (!captureBufferMaxSessionsInput.value && captureBufferMaxSessions.value > 0) {
      captureBufferMaxSessionsInput.value = String(captureBufferMaxSessions.value)
    }
    if (!captureBufferMaxPendingEventsInput.value && captureBufferMaxPendingEvents.value > 0) {
      captureBufferMaxPendingEventsInput.value = String(captureBufferMaxPendingEvents.value)
    }
    if (!captureDurationWindowInput.value) {
      captureDurationWindowInput.value = String(captureDurationWindowSize.value)
    }
    if (!exportBatchSizeInput.value) {
      exportBatchSizeInput.value = String(exportBatchSizeDefault)
    }
    if (!exportWorkerCountInput.value) {
      exportWorkerCountInput.value = String(exportWorkerCountDefault)
    }
  } catch (error) {
    appStore.showError('加载数据共享统计失败')
  } finally {
    statsLoading.value = false
  }
}

async function refreshStats() {
  await Promise.all([loadStats(), loadStorageLimit()])
}

function restartStatsAutoRefresh() {
  stopStatsAutoRefresh()
  statsAutoRefreshTimer = window.setInterval(runStatsAutoRefresh, statsAutoRefreshIntervalSeconds.value * 1000)
}

function stopStatsAutoRefresh() {
  if (!statsAutoRefreshTimer) return
  window.clearInterval(statsAutoRefreshTimer)
  statsAutoRefreshTimer = null
}

async function runStatsAutoRefresh() {
  // 自动刷新只更新顶部统计与采集状态，避免影响下方列表筛选和分页。
  if (!statsAutoRefreshEnabled.value || document.hidden || statsLoading.value || storageLimitLoading.value) return
  await refreshStats()
}

function setStatsAutoRefreshEnabled(enabled: boolean) {
  statsAutoRefreshEnabled.value = enabled
  if (enabled) {
    restartStatsAutoRefresh()
  } else {
    stopStatsAutoRefresh()
  }
}

function setStatsAutoRefreshInterval(seconds: (typeof statsAutoRefreshIntervals)[number]) {
  statsAutoRefreshIntervalSeconds.value = seconds
  if (statsAutoRefreshEnabled.value) restartStatsAutoRefresh()
}

function clampDropdownLeft(left: number, width: number) {
  const margin = 16
  return Math.min(
    Math.max(left, margin),
    Math.max(margin, window.innerWidth - width - margin)
  )
}

function updateStatsAutoRefreshDropdownPosition() {
  if (!statsAutoRefreshDropdownOpen.value || !statsAutoRefreshButtonRef.value) return
  const width = 224
  const rect = statsAutoRefreshButtonRef.value.getBoundingClientRect()
  // 下拉菜单跟随按钮左边展开，并限制在视口内避免被边缘裁切。
  statsAutoRefreshDropdownStyle.value = {
    top: `${rect.bottom + 8}px`,
    left: `${clampDropdownLeft(rect.left, width)}px`,
    maxHeight: `${Math.max(240, window.innerHeight - rect.bottom - 24)}px`
  }
}

function toggleStatsAutoRefreshDropdown() {
  statsAutoRefreshDropdownOpen.value = !statsAutoRefreshDropdownOpen.value
  updateStatsAutoRefreshDropdownPosition()
}

function closeStatsAutoRefreshDropdownOnOutsideClick(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof Node)) return
  if (statsAutoRefreshDropdownRef.value?.contains(target)) return
  statsAutoRefreshDropdownOpen.value = false
}

function handleStatsAutoRefreshViewportChange() {
  updateStatsAutoRefreshDropdownPosition()
}

async function loadFilterOptions() {
  try {
    filterOptions.value = await adminDataSharingAPI.getFilterOptions()
  } catch (error) {
    appStore.showError('加载数据共享筛选项失败')
  }
}

async function loadSessions() {
  loading.value = true
  try {
    const res = await adminDataSharingAPI.listSessions(pagination.page, pagination.page_size, buildFilters())
    sessions.value = res.items
    pagination.total = res.total
    pagination.pages = res.pages
  } catch (error) {
    appStore.showError('加载数据共享记录失败')
  } finally {
    loading.value = false
  }
}

async function loadExportArtifacts() {
  // 导出文件列表和 session 列表分开分页，避免刷新任务状态时影响当前筛选结果。
  exportArtifactsLoading.value = true
  try {
    const res = await adminDataSharingAPI.listExportArtifacts(exportArtifactPagination.page, exportArtifactPagination.page_size)
    exportArtifacts.value = res.items
    exportArtifactPagination.total = res.total
    exportArtifactPagination.pages = res.pages
    // 导出文件状态只在页面加载、手动刷新和任务操作后刷新，避免后台自动轮询。
  } catch (error) {
    appStore.showError('加载导出文件失败')
  } finally {
    exportArtifactsLoading.value = false
  }
}

async function refreshExportArtifacts() {
  await Promise.all([loadExportArtifacts(), loadStats()])
}

function refreshAll() {
  loadSessions()
  loadExportArtifacts()
  loadStats()
  loadStorageLimit()
}

function handleFilterChange() {
  pagination.page = 1
  clearSelection()
  if (filterTimer) window.clearTimeout(filterTimer)
  filterTimer = window.setTimeout(refreshAll, 250)
}

// 用户排行固定统计无效贡献，点击条形后再把列表切到该用户的无效样本。
function applyInvalidUserFilter(point?: DataShareInvalidUserPoint) {
  if (!point?.user_id) return
  filters.user_id = point.user_id
  filters.user_filter_label = invalidUserLabel(point)
  filters.quality_status = 'invalid'
  handleFilterChange()
}

function clearInvalidUserFilter() {
  filters.user_id = 0
  filters.user_filter_label = ''
  handleFilterChange()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadSessions()
}

function handlePageChange(page: number) {
  pagination.page = page
  loadSessions()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadSessions()
}

function handleExportArtifactPageChange(page: number) {
  exportArtifactPagination.page = page
  loadExportArtifacts()
}

function handleExportArtifactPageSizeChange(pageSize: number) {
  exportArtifactPagination.page_size = pageSize
  exportArtifactPagination.page = 1
  loadExportArtifacts()
}

function clearSelection() {
  selectedIds.value = new Set()
  excludedIds.value = new Set()
  selectAllMatching.value = false
}

function isSelected(id: number) {
  return selectAllMatching.value ? !excludedIds.value.has(id) : selectedIds.value.has(id)
}

function toggleSelect(id: number) {
  if (selectAllMatching.value) {
    const next = new Set(excludedIds.value)
    if (next.has(id)) {
      next.delete(id)
    } else {
      next.add(id)
    }
    excludedIds.value = next
    return
  }
  const next = new Set(selectedIds.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  selectedIds.value = next
}

function toggleSelectCurrentPage(event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const pageIds = sessions.value.map(row => row.id)
  if (selectAllMatching.value) {
    // 表头复选框只影响当前页，通过排除列表保留“筛选结果全选”的语义。
    const nextExcluded = new Set(excludedIds.value)
    pageIds.forEach(id => {
      if (checked) {
        nextExcluded.delete(id)
      } else {
        nextExcluded.add(id)
      }
    })
    excludedIds.value = nextExcluded
    return
  }

  const nextSelected = new Set(selectedIds.value)
  pageIds.forEach(id => {
    if (checked) {
      nextSelected.add(id)
    } else {
      nextSelected.delete(id)
    }
  })
  selectedIds.value = nextSelected
}

function toggleSelectAllFilteredSessions() {
  if (selectAllMatching.value) {
    clearSelection()
    return
  }
  // 工具栏全选按钮显式进入“当前筛选条件全集”模式，供批量删除和导出复用。
  selectedIds.value = new Set()
  excludedIds.value = new Set()
  selectAllMatching.value = true
}

function buildSelectionFilters(): AdminDataShareSessionFilters {
  const out = buildFilters()
  if (selectAllMatching.value) {
    out.select_all = true
    const excluded = Array.from(excludedIds.value)
    if (excluded.length) out.exclude_ids = excluded.join(',')
    return out
  }
  const ids = Array.from(selectedIds.value)
  if (ids.length) out.ids = ids.join(',')
  return out
}

async function openDetail(row: DataShareSession) {
  detailOpen.value = true
  detailLoading.value = true
  selectedSession.value = row
  try {
    selectedSession.value = { ...row, ...(await adminDataSharingAPI.getSession(row.id)) }
  } catch (error) {
    appStore.showError('加载详情失败')
  } finally {
    detailLoading.value = false
  }
}

async function deleteOne(row: DataShareSession) {
  if (!window.confirm(`确定删除 session ${row.session_id} 吗？`)) return
  try {
    await adminDataSharingAPI.deleteSession(row.id)
    if (!selectAllMatching.value) {
      selectedIds.value.delete(row.id)
    }
    appStore.showSuccess('数据已删除')
    refreshAll()
  } catch (error) {
    appStore.showError('删除失败')
  }
}

async function batchDelete() {
  if (selectedCount.value === 0) return
  const count = selectedCount.value
  const ids = selectAllMatching.value ? [] : Array.from(selectedIds.value)
  const params = selectAllMatching.value ? buildSelectionFilters() : buildFilters()
  if (!window.confirm(`确定删除已选 ${formatNumber(count)} 条数据吗？`)) return
  try {
    await adminDataSharingAPI.batchDeleteSessions(ids, params)
    clearSelection()
    appStore.showSuccess('已删除选中数据')
    refreshAll()
  } catch (error) {
    appStore.showError('批量删除失败')
  }
}

async function downloadSelected() {
  if (selectedCount.value === 0) return
  exporting.value = true
  try {
    // 批量导出只创建后台生成任务，实际下载从“导出文件”板块读取已生成文件。
    await adminDataSharingAPI.createExportArtifact(buildSelectionFilters())
    exportArtifactPagination.page = 1
    await loadExportArtifacts()
    appStore.showSuccess('导出文件生成任务已创建')
  } catch (error) {
    appStore.showError('创建导出任务失败')
  } finally {
    exporting.value = false
  }
}

async function downloadOne(row: DataShareSession) {
  try {
    // 单条 session 也走预生成任务，保持管理端下载链路一致。
    await adminDataSharingAPI.createSessionExportArtifact(row.id)
    exportArtifactPagination.page = 1
    await loadExportArtifacts()
    appStore.showSuccess('单条导出文件生成任务已创建')
  } catch (error) {
    appStore.showError('创建导出任务失败')
  }
}

async function downloadExportArtifact(row: DataShareExportArtifact) {
  try {
    // 下载票据只绑定已生成文件，不再触发实时查询和压缩。
    const ticket = await adminDataSharingAPI.createExportArtifactDownloadTicket(row.id)
    dataSharingAPI.startTicketDownload(ticket)
    appStore.showSuccess('下载已开始')
  } catch (error) {
    appStore.showError('下载失败')
  }
}

async function uploadExportArtifact(row: DataShareExportArtifact) {
  try {
    const artifact = await adminDataSharingAPI.uploadExportArtifact(row.id)
    replaceExportArtifact(artifact)
    appStore.showSuccess('上传任务已开始')
  } catch (error) {
    appStore.showError('启动上传到 S3/R2 失败')
    await loadExportArtifacts()
  }
}

function handleExportArtifactUploadAction(row: DataShareExportArtifact) {
  if (row.remote_status === 'uploading') {
    openCancelExportArtifactUploadDialog(row)
    return
  }
  uploadExportArtifact(row)
}

function openCancelExportArtifactUploadDialog(row: DataShareExportArtifact) {
  cancelUploadTarget.value = row
  cancelUploadDialogOpen.value = true
}

function closeCancelExportArtifactUploadDialog() {
  cancelUploadDialogOpen.value = false
  cancelUploadTarget.value = null
}

function isCancelingExportUpload(id: number) {
  return cancelingExportUploadIds.value.has(id)
}

function setCancelingExportUpload(id: number, canceling: boolean) {
  const next = new Set(cancelingExportUploadIds.value)
  if (canceling) {
    next.add(id)
  } else {
    next.delete(id)
  }
  cancelingExportUploadIds.value = next
}

async function confirmCancelExportArtifactUpload() {
  const target = cancelUploadTarget.value
  if (!target) {
    closeCancelExportArtifactUploadDialog()
    return
  }
  setCancelingExportUpload(target.id, true)
  cancelUploadDialogOpen.value = false
  try {
    // 取消只中断远端上传任务，本地生成好的导出文件仍保留，可稍后重新上传。
    const artifact = await adminDataSharingAPI.cancelExportArtifactUpload(target.id)
    replaceExportArtifact(artifact)
    appStore.showSuccess('上传任务已取消')
  } catch (error) {
    appStore.showError('取消上传失败')
    await loadExportArtifacts()
  } finally {
    setCancelingExportUpload(target.id, false)
    cancelUploadTarget.value = null
  }
}

async function downloadRemoteExportArtifact(row: DataShareExportArtifact) {
  try {
    const result = await adminDataSharingAPI.getExportArtifactRemoteDownloadURL(row.id)
    if (result.url) {
      window.open(result.url, '_blank', 'noopener')
    }
  } catch (error) {
    appStore.showError('远端下载链接生成失败')
  }
}

function replaceExportArtifact(artifact: DataShareExportArtifact) {
  const idx = exportArtifacts.value.findIndex(item => item.id === artifact.id)
  if (idx >= 0) {
    exportArtifacts.value.splice(idx, 1, artifact)
  } else {
    exportArtifacts.value.unshift(artifact)
  }
}

function copyRemoteKey(row: DataShareExportArtifact) {
  if (!row.remote_key) return
  copyToClipboard(row.remote_key, '远端对象 key 已复制')
}

function exportArtifactUploadActionLabel(row: DataShareExportArtifact) {
  if (isCancelingExportUpload(row.id)) return '取消中'
  if (row.remote_status === 'uploading') return '取消上传'
  return row.remote_status === 'uploaded' ? '重新上传' : '上传'
}

async function deleteExportArtifact(row: DataShareExportArtifact) {
  if (!window.confirm(`确定删除导出文件 ${row.filename} 吗？`)) return
  try {
    await adminDataSharingAPI.deleteExportArtifact(row.id)
    appStore.showSuccess('导出文件已删除')
    loadExportArtifacts()
  } catch (error) {
    appStore.showError('删除导出文件失败')
  }
}

function displayUser(row: DataShareSession) {
  return row.user_name || row.user_email || `#${row.user_id}`
}

function invalidUserLabel(point?: DataShareInvalidUserPoint | null) {
  if (!point) return '-'
  return point.user_name || point.user_email || `#${point.user_id}`
}

function displayAPIKey(row: DataShareSession) {
  return row.api_key_name || `#${row.api_key_id}`
}

function displayGroup(row: DataShareSession) {
  return row.group_name || `#${row.group_id}`
}

function formatDate(value?: string | null) {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}

function parseDateMillis(value?: string | null) {
  if (!value) return 0
  const millis = new Date(value).getTime()
  return Number.isFinite(millis) ? millis : 0
}

function statsRecordHasActiveError(errorAt?: string | null, successAt?: string | null) {
  const errorMillis = parseDateMillis(errorAt)
  if (!errorMillis) return false
  const successMillis = parseDateMillis(successAt)
  return !successMillis || errorMillis > successMillis
}

function statsRecordHasRecoveredError(errorAt?: string | null, successAt?: string | null) {
  const errorMillis = parseDateMillis(errorAt)
  const successMillis = parseDateMillis(successAt)
  return Boolean(errorMillis && successMillis && successMillis >= errorMillis)
}

function formatOptionalDateSuffix(value?: string | null) {
  return value ? `（${formatDate(value)}）` : ''
}

function captureLastErrorPrefix(active: boolean, errorAt?: string | null) {
  if (!errorAt) return '历史失败'
  return active
    ? `最近失败${formatOptionalDateSuffix(errorAt)}`
    : `最近失败已恢复${formatOptionalDateSuffix(errorAt)}`
}

function captureLastErrorTitle(errorText: string, errorAt?: string | null, successAt?: string | null) {
  const parts = [errorText]
  if (errorAt) parts.push(`最近失败：${formatDate(errorAt)}`)
  if (successAt) parts.push(`最近成功：${formatDate(successAt)}`)
  return parts.filter(Boolean).join('\n')
}

function formatNumber(value?: number | null) {
  return new Intl.NumberFormat().format(value || 0)
}

function formatPercent(value?: number | null) {
  return `${((value || 0) * 100).toFixed(1)}%`
}

function formatDurationMillis(value?: number | null) {
  const millis = Math.max(Math.round(value || 0), 0)
  if (millis < 1000) return `${formatNumber(millis)} ms`
  return `${(millis / 1000).toFixed(2)} s`
}

function formatUserAgent(value?: string | null) {
  const userAgent = (value || '').trim()
  if (!userAgent || userAgent === '(unknown)') return userAgent || '-'
  return userAgent.length > 56 ? `${userAgent.slice(0, 56)}...` : userAgent
}

function truncateChartLabel(value: string, maxLength: number) {
  const label = (value || '').trim()
  if (label.length <= maxLength) return label
  return `${label.slice(0, Math.max(maxLength - 3, 1))}...`
}

function buildBreakdownChartData<T extends { session_count: number }>(points: T[], labelOf: (point: T) => string) {
  if (!points.length) return null
  return {
    labels: points.map(labelOf),
    datasets: [
      {
        label: 'Session',
        data: points.map(point => point.session_count),
        backgroundColor: points.map((_, index) => doughnutPalette[index % doughnutPalette.length]),
        borderWidth: 0
      }
    ]
  }
}

function buildDoughnutLegendItems<T extends DoughnutLegendPoint>(
  points: T[],
  fullLabelOf: (point: T) => string,
  labelOf: (fullLabel: string, point: T) => string = fullLabel => fullLabel
): DoughnutLegendItem[] {
  // 自定义图例使用独立滚动容器，避免 Chart.js 内置图例挤占饼图高度后被裁切。
  return points.map((point, index) => {
    const fullLabel = fullLabelOf(point)
    return {
      key: `${index}:${fullLabel}`,
      fullLabel,
      label: labelOf(fullLabel, point),
      sessionCount: point.session_count,
      color: doughnutPalette[index % doughnutPalette.length]
    }
  })
}

function buildDoughnutChartOptions(
  points: Array<{ storage_bytes: number; session_count: number; total_tokens: number }>,
  options: { legend?: boolean } = {}
) {
  return {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: options.legend === false
        ? { display: false }
        : { position: 'bottom' as const, labels: { color: chartColors.value.text } },
      tooltip: {
        callbacks: {
          label: (ctx: any) => {
            const point = points[ctx.dataIndex]
            if (!point) return `${ctx.label}: ${formatNumber(ctx.raw)}`
            return `${ctx.label}: ${formatNumber(point.session_count)} · ${formatBytes(point.storage_bytes)} · ${formatNumber(point.total_tokens)} tokens`
          }
        }
      }
    }
  }
}

function buildSessionCountDoughnutChartOptions(
  points: Array<{ session_count: number }>,
  options: { legend?: boolean } = {}
) {
  return {
    responsive: true,
    maintainAspectRatio: false,
    plugins: {
      legend: options.legend === false
        ? { display: false }
        : { position: 'bottom' as const, labels: { color: chartColors.value.text } },
      tooltip: {
        callbacks: {
          label: (ctx: any) => {
            const point = points[ctx.dataIndex]
            const count = point?.session_count ?? Number(ctx.raw || 0)
            return `${ctx.label}: ${formatNumber(count)} ${t('admin.dataSharing.sessions')}`
          }
        }
      }
    }
  }
}

function durationBucketChartData(part: DataShareCaptureDurationPart) {
  return {
    labels: part.buckets.map(bucket => bucket.label),
    datasets: [
      {
        label: part.label,
        data: part.buckets.map(bucket => bucket.count),
        backgroundColor: '#0891b288',
        borderColor: '#0891b2',
        borderWidth: 1
      }
    ]
  }
}

function normalizeCaptureWorkerJobKind(value?: string | null): CaptureWorkerJobKind {
  return value === 'flush' || value === 'capture' ? value : ''
}

function captureWorkerSlotLabel(enabled: boolean, jobKind: CaptureWorkerJobKind, fallbackActive: boolean) {
  if (!enabled) return 'Worker 未启用'
  if (jobKind === 'flush') return 'Worker 正在执行 Flush 落库'
  if (jobKind === 'capture') return 'Worker 正在执行采集任务'
  if (fallbackActive) return 'Worker 处理中'
  return 'Worker 空闲'
}

function captureWorkerSlotClass(state: CaptureWorkerSlotState, jobKind: CaptureWorkerJobKind) {
  if (state === 'active' && jobKind === 'flush') return 'border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900/60 dark:bg-violet-900/20 dark:text-violet-200'
  if (state === 'active') return 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900/60 dark:bg-sky-900/20 dark:text-sky-200'
  if (state === 'idle') return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-900/20 dark:text-emerald-200'
  return 'border-gray-200 bg-gray-50 text-gray-400 dark:border-gray-700 dark:bg-gray-800/60 dark:text-gray-500'
}

function captureWorkerDotClass(state: CaptureWorkerSlotState, jobKind: CaptureWorkerJobKind) {
  if (state === 'active' && jobKind === 'flush') return 'bg-violet-500'
  if (state === 'active') return 'bg-sky-500'
  if (state === 'idle') return 'bg-emerald-500'
  return 'bg-gray-400'
}

function qualityErrorLabel(code?: string | null) {
  const raw = (code || '').trim()
  const normalized = !raw || raw === '(unknown)' ? 'unknown' : raw
  const key = `admin.dataSharing.qualityErrors.${normalized}`
  return te(key) ? t(key) : normalized
}

function qualityLabel(value?: string) {
  if (value === 'complete') return '完整'
  if (value === 'partial') return '部分完整'
  return '无效'
}

function qualityBadgeClass(value?: string) {
  if (value === 'complete') return 'badge-success'
  if (value === 'partial') return 'badge-warning'
  return 'badge-danger'
}

function exportArtifactStatusLabel(value?: string) {
  if (value === 'pending') return '等待中'
  if (value === 'running') return '生成中'
  if (value === 'completed') return '已完成'
  if (value === 'failed') return '失败'
  if (value === 'deleted') return '已删除'
  return value || '-'
}

function exportArtifactStatusBadgeClass(value?: string) {
  if (value === 'completed') return 'badge-success'
  if (value === 'running') return 'badge-gray'
  if (value === 'pending') return 'badge-warning'
  if (value === 'failed') return 'badge-danger'
  return 'badge-gray'
}

function exportArtifactRemoteStatusLabel(value?: string) {
  if (value === 'uploading') return '上传中'
  if (value === 'uploaded') return '已上传'
  if (value === 'failed') return '上传失败'
  return '未上传'
}

function exportArtifactRemoteStatusBadgeClass(value?: string) {
  if (value === 'uploaded') return 'badge-success'
  if (value === 'uploading') return 'badge-warning'
  if (value === 'failed') return 'badge-danger'
  return 'badge-gray'
}

function formatBytes(value?: number | null) {
  const bytes = value || 0
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = bytes / 1024
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit++
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unit]}`
}

function formatExportUploadProgress(row: DataShareExportArtifact) {
  if (!row.file_size || row.file_size <= 0) return '0%'
  const percent = Math.min(100, Math.max(0, ((row.remote_upload_bytes || 0) / row.file_size) * 100))
  return `${percent.toFixed(percent >= 10 ? 1 : 2)}%`
}

function formatExportGenerateProgress(row: DataShareExportArtifact) {
  if (!row.generate_progress_total || row.generate_progress_total <= 0) return '0%'
  const percent = Math.min(100, Math.max(0, row.generate_progress_percent || 0))
  return `${percent.toFixed(percent >= 10 ? 1 : 2)}%`
}

function formatExportGenerateCount(row: DataShareExportArtifact) {
  if (!row.generate_progress_total || row.generate_progress_total <= 0) return '计算中'
  return `${formatNumber(row.generate_progress_done || 0)} / ${formatNumber(row.generate_progress_total)}`
}

function formatExportUploadSpeed(bytesPerSecond?: number | null) {
  const mbPerSecond = Math.max(0, bytesPerSecond || 0) / 1_000_000
  return `${mbPerSecond.toFixed(mbPerSecond >= 10 ? 1 : 2)} MB/s`
}

onMounted(() => {
  document.addEventListener('click', closeSkipRulePathMenuOnOutsideClick)
  document.addEventListener('click', closeStatsAutoRefreshDropdownOnOutsideClick)
  window.addEventListener('resize', handleStatsAutoRefreshViewportChange)
  window.addEventListener('scroll', handleStatsAutoRefreshViewportChange, true)
  loadFilterOptions()
  loadNotice()
  loadSkipRules()
  loadCaptureRuntimeSettings()
  loadExportRemoteConfig()
  refreshAll()
  if (statsAutoRefreshEnabled.value) restartStatsAutoRefresh()
})

onUnmounted(() => {
  document.removeEventListener('click', closeSkipRulePathMenuOnOutsideClick)
  document.removeEventListener('click', closeStatsAutoRefreshDropdownOnOutsideClick)
  window.removeEventListener('resize', handleStatsAutoRefreshViewportChange)
  window.removeEventListener('scroll', handleStatsAutoRefreshViewportChange, true)
  stopStatsAutoRefresh()
})
</script>
