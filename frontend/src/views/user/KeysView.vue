<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-4">
          <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
            <div class="flex min-w-0 flex-1 items-center gap-2">
              <SearchInput
                v-model="filterSearch"
                :placeholder="t('keys.searchPlaceholder')"
                class="min-w-0 flex-1 sm:w-56 sm:flex-none lg:w-48 xl:w-64 [&>input]:h-9 [&>input]:min-h-0"
                @search="onFilterChange"
              />
              <div ref="filterDropdownRef" class="relative shrink-0">
                <button
                  type="button"
                  class="btn btn-secondary relative h-9 w-9 p-0"
                  :aria-expanded="showFilterDropdown"
                  :aria-label="t('common.filter')"
                  :title="t('common.filter')"
                  @click="showFilterDropdown = !showFilterDropdown"
                >
                  <Icon name="filter" size="sm" />
                  <span v-if="activeFilterCount > 0" class="absolute -right-1 -top-1 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary-100 px-1.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">
                    {{ activeFilterCount }}
                  </span>
                </button>
                <div v-show="showFilterDropdown" class="absolute left-0 right-auto top-full z-[60] mt-2 w-[min(32rem,calc(100vw-2rem))] max-w-[calc(100vw-2rem)] rounded-xl border border-gray-200 bg-white p-4 shadow-xl dark:border-dark-600 dark:bg-dark-900 max-[639px]:left-auto max-[639px]:right-0" @click.stop>
                  <div class="mb-3 flex items-center justify-between">
                    <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('common.filter') }}</div>
                    <button v-if="activeFilterCount > 0" type="button" class="text-xs font-medium text-primary-600 dark:text-primary-400" @click="resetKeyFilters">
                      {{ t('common.reset') }}
                    </button>
                  </div>
                  <div class="space-y-3">
                    <div>
                      <label class="input-label">{{ t('keys.allGroups') }}</label>
                      <Select :model-value="filterGroupId" :options="groupFilterOptions" @update:model-value="onGroupFilterChange" />
                    </div>
                    <div>
                      <label class="input-label">{{ t('keys.allStatus') }}</label>
                      <Select :model-value="filterStatus" :options="statusFilterOptions" @update:model-value="onStatusFilterChange" />
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <div class="flex shrink-0 justify-end gap-3">
              <button
                @click="loadApiKeys"
                :disabled="loading"
                class="btn btn-secondary h-9 w-9 shrink-0 p-0"
                :title="t('common.refresh')"
              >
                <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
              </button>
              <div class="relative" ref="columnDropdownRef">
                <button
                  @click.stop="showColumnDropdown = !showColumnDropdown"
                  class="btn btn-secondary h-9 w-9 shrink-0 p-0"
                  :title="t('keys.columnSettings')"
                >
                  <Icon name="grid" size="md" />
                </button>
                <div
                  v-if="showColumnDropdown"
                  class="absolute right-0 top-full z-50 mt-2 max-h-80 w-52 overflow-y-auto rounded-lg border border-gray-200 bg-white p-2 shadow-xl dark:border-gray-700 dark:bg-gray-800"
                >
                  <button
                    v-for="column in toggleableColumns"
                    :key="column.key"
                    @click="toggleColumn(column.key)"
                    class="flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700"
                  >
                    <span>{{ column.label }}</span>
                    <Icon
                      v-if="isColumnVisible(column.key)"
                      name="check"
                      size="sm"
                      class="text-primary-500"
                      :stroke-width="2"
                    />
                  </button>
                </div>
              </div>
              <ScopeDropdown v-if="teamFeatureEnabled" v-model="scope" @change="onScopeChange" />
              <button @click="openCreateModal" class="btn btn-primary h-9" data-tour="keys-create-btn">
                <Icon name="plus" size="md" class="mr-2" />
                {{ t('keys.createKey') }}
              </button>
            </div>
          </div>
          <EndpointPopover
            v-if="publicSettings?.api_base_url || (publicSettings?.custom_endpoints?.length ?? 0) > 0"
            :api-base-url="publicSettings?.api_base_url || ''"
            :custom-endpoints="publicSettings?.custom_endpoints || []"
          />
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="apiKeys"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ value }}</span>
          </template>

          <template #cell-key="{ value, row }">
            <div class="flex items-center gap-2">
              <code class="code text-xs">
                {{ maskKey(value) }}
              </code>
              <button
                @click="copyToClipboard(value, row.id)"
                class="rounded-lg p-1 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                :class="
                  copiedKeyId === row.id
                    ? 'text-green-500'
                    : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-300'
                "
                :title="copiedKeyId === row.id ? t('keys.copied') : t('keys.copyToClipboard')"
              >
                <Icon
                  v-if="copiedKeyId === row.id"
                  name="check"
                  size="sm"
                  :stroke-width="2"
                />
                <Icon v-else name="clipboard" size="sm" />
              </button>
            </div>
          </template>

          <template #cell-name="{ value, row }">
            <div class="flex items-center gap-1.5">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
              <Icon
                v-if="row.ip_whitelist?.length > 0 || row.ip_blacklist?.length > 0"
                name="shield"
                size="sm"
                class="text-blue-500"
                :title="t('keys.ipRestrictionEnabled')"
              />
            </div>
          </template>

          <template #cell-group="{ row }">
            <button
              v-if="row.is_composite"
              type="button"
              data-test="composite-group-summary"
              class="flex max-w-[22rem] flex-wrap items-center gap-1.5 rounded-md px-1 py-1 text-left hover:bg-gray-100 dark:hover:bg-dark-700"
              :title="t('keys.composite.editMappings')"
              @click="editKey(row)"
            >
              <span
                v-for="binding in row.composite_groups"
                :key="`${row.id}-${binding.group_id}`"
                class="inline-flex min-w-0 items-center gap-1 rounded border border-gray-200 bg-gray-50 px-1.5 py-1 dark:border-dark-600 dark:bg-dark-800"
              >
                <span class="max-w-24 truncate font-mono text-xs font-semibold text-primary-700 dark:text-primary-300">{{ binding.prefix }}</span>
                <span class="text-gray-300 dark:text-dark-500">/</span>
                <span class="max-w-28 truncate text-xs text-gray-600 dark:text-dark-300">{{ binding.group?.name || `#${binding.group_id}` }}</span>
              </span>
            </button>
            <div v-else class="group/dropdown relative">
              <button
                :ref="(el) => setGroupButtonRef(row.id, el)"
                @click="openGroupSelector(row)"
                class="-mx-2 -my-1 flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1 transition-all duration-200 hover:bg-gray-100 dark:hover:bg-dark-700"
                :title="t('keys.clickToChangeGroup')"
              >
                <GroupBadge
                  v-if="row.group"
                  :name="row.group.name"
                  :platform="row.group.platform"
                  :display-brand="row.group.display_brand"
                  :rate-multiplier="row.group.rate_multiplier"
                  :user-rate-multiplier="userGroupRates[row.group.id]"
                  :peak-rate-enabled="row.group.peak_rate_enabled"
                  :peak-start="row.group.peak_start"
                  :peak-end="row.group.peak_end"
                  :peak-rate-multiplier="row.group.peak_rate_multiplier"
                />
                <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{
                  t('keys.noGroup')
                }}</span>
                <svg
                  class="h-3.5 w-3.5 text-gray-400 opacity-60 transition-opacity group-hover/dropdown:opacity-100"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                  stroke-width="2"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9"
                  />
                </svg>
              </button>
            </div>
          </template>

          <template #cell-current_concurrency="{ value }">
            <span
              :class="[
                'inline-flex min-w-8 items-center justify-center rounded px-2 py-1 text-sm font-semibold tabular-nums',
                (value ?? 0) > 0
                  ? 'bg-emerald-50 text-emerald-700 ring-1 ring-emerald-200 dark:bg-emerald-900/25 dark:text-emerald-300 dark:ring-emerald-800'
                  : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-400'
              ]"
            >
              {{ value ?? 0 }}
            </span>
          </template>

          <template #cell-usage="{ row }">
            <div class="text-sm">
              <div v-if="usageLoading && !usageStats[row.id]" class="flex h-10 items-center text-gray-400">
                <Icon name="refresh" size="sm" class="animate-spin" />
              </div>
              <div v-else class="space-y-0.5">
                <div class="flex items-center gap-1.5">
                  <span class="text-gray-500 dark:text-gray-400">{{ t('keys.today') }}:</span>
                  <span class="font-medium text-gray-900 dark:text-white">
                    {{ formatBalanceAmount(usageStats[row.id]?.today_actual_cost ?? 0, { fractionDigits: 4 }) }}
                  </span>
                </div>
              </div>
              <!-- Quota progress (if quota is set) -->
              <div v-if="row.quota > 0" class="mt-1.5">
                <div class="flex items-center gap-1.5">
                  <span class="text-gray-500 dark:text-gray-400">{{ t('keys.quota') }}:</span>
                  <span :class="[
                    'font-medium',
                    row.quota_used >= row.quota ? 'text-red-500' :
                    row.quota_used >= row.quota * 0.8 ? 'text-yellow-500' :
                    'text-gray-900 dark:text-white'
                  ]">
                    {{ formatBalancePair(row.quota_used, row.quota, 2, 2) }}
                  </span>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.quota_used >= row.quota ? 'bg-red-500' :
                      row.quota_used >= row.quota * 0.8 ? 'bg-yellow-500' :
                      'bg-primary-500'
                    ]"
                    :style="{ width: Math.min((row.quota_used / row.quota) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </div>
          </template>

          <template #cell-rate_limit="{ row }">
            <div v-if="row.rate_limit_5h > 0 || row.rate_limit_1d > 0 || row.rate_limit_7d > 0" class="space-y-1.5 min-w-[140px]">
              <!-- 5h window -->
              <div v-if="row.rate_limit_5h > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">5h</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.usage_5h >= row.rate_limit_5h ? 'text-red-500' :
                    row.usage_5h >= row.rate_limit_5h * 0.8 ? 'text-yellow-500' :
                    'text-gray-700 dark:text-gray-300'
                  ]">
                    {{ formatBalancePair(row.usage_5h, row.rate_limit_5h, 2, 2, false) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.usage_5h >= row.rate_limit_5h ? 'bg-red-500' :
                      row.usage_5h >= row.rate_limit_5h * 0.8 ? 'bg-yellow-500' :
                      'bg-emerald-500'
                    ]"
                    :style="{ width: Math.min((row.usage_5h / row.rate_limit_5h) * 100, 100) + '%' }"
                  />
                </div>
                <div v-if="row.reset_5h_at && formatResetTime(row.reset_5h_at)" class="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  ⟳ {{ formatResetTime(row.reset_5h_at) }}
                </div>
              </div>
              <!-- 1d window -->
              <div v-if="row.rate_limit_1d > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">1d</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.usage_1d >= row.rate_limit_1d ? 'text-red-500' :
                    row.usage_1d >= row.rate_limit_1d * 0.8 ? 'text-yellow-500' :
                    'text-gray-700 dark:text-gray-300'
                  ]">
                    {{ formatBalancePair(row.usage_1d, row.rate_limit_1d, 2, 2, false) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.usage_1d >= row.rate_limit_1d ? 'bg-red-500' :
                      row.usage_1d >= row.rate_limit_1d * 0.8 ? 'bg-yellow-500' :
                      'bg-emerald-500'
                    ]"
                    :style="{ width: Math.min((row.usage_1d / row.rate_limit_1d) * 100, 100) + '%' }"
                  />
                </div>
                <div v-if="row.reset_1d_at && formatResetTime(row.reset_1d_at)" class="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  ⟳ {{ formatResetTime(row.reset_1d_at) }}
                </div>
              </div>
              <!-- 7d window -->
              <div v-if="row.rate_limit_7d > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">7d</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.usage_7d >= row.rate_limit_7d ? 'text-red-500' :
                    row.usage_7d >= row.rate_limit_7d * 0.8 ? 'text-yellow-500' :
                    'text-gray-700 dark:text-gray-300'
                  ]">
                    {{ formatBalancePair(row.usage_7d, row.rate_limit_7d, 2, 2, false) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.usage_7d >= row.rate_limit_7d ? 'bg-red-500' :
                      row.usage_7d >= row.rate_limit_7d * 0.8 ? 'bg-yellow-500' :
                      'bg-emerald-500'
                    ]"
                    :style="{ width: Math.min((row.usage_7d / row.rate_limit_7d) * 100, 100) + '%' }"
                  />
                </div>
                <div v-if="row.reset_7d_at && formatResetTime(row.reset_7d_at)" class="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  ⟳ {{ formatResetTime(row.reset_7d_at) }}
                </div>
              </div>
              <!-- Reset button -->
              <button
                v-if="row.usage_5h > 0 || row.usage_1d > 0 || row.usage_7d > 0"
                @click.stop="confirmResetRateLimitFromTable(row)"
                class="mt-0.5 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                :title="t('keys.resetRateLimitUsage')"
              >
                <Icon name="refresh" size="xs" />
                {{ t('keys.resetUsage') }}
              </button>
            </div>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-expires_at="{ value }">
            <span v-if="value" :class="[
              'text-sm',
              new Date(value) < new Date() ? 'text-red-500 dark:text-red-400' : 'text-gray-500 dark:text-dark-400'
            ]">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{ t('keys.noExpiration') }}</span>
          </template>

          <template #cell-status="{ value, row }">
            <span :class="[
              'badge gap-1',
              row.team_owner_disabled ? 'badge-danger' :
              value === 'active' ? 'badge-success' :
              value === 'quota_exhausted' ? 'badge-warning' :
              value === 'expired' ? 'badge-danger' :
              'badge-gray'
            ]">
              <Icon v-if="row.team_owner_disabled" name="lock" size="xs" />
              {{ row.team_owner_disabled ? t('keys.status.team_owner_disabled') : t('keys.status.' + value) }}
            </span>
          </template>

          <template #cell-last_used_at="{ value }">
            <span v-if="value" class="text-sm text-gray-500 dark:text-dark-400">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-last_used_ip="{ value }">
            <span v-if="value" class="break-all font-mono text-xs text-gray-500 dark:text-dark-400">
              {{ value }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <!-- 高频操作固定展示，低频和危险操作收进更多菜单。 -->
              <button
                @click="editKey(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <!-- Owner 锁定由团队管理员控制，成员侧不再提供无效的恢复入口。 -->
              <span
                v-if="row.team_owner_disabled"
                class="flex cursor-not-allowed flex-col items-center gap-0.5 rounded-lg p-1.5 text-amber-600 dark:text-amber-400"
                :title="t('keys.teamOwnerDisabledHint')"
              >
                <Icon name="lock" size="sm" />
                <span class="text-xs">{{ t('keys.teamOwnerLocked') }}</span>
              </span>
              <!-- Toggle Status Button -->
              <button
                v-else
                @click="toggleKeyStatus(row)"
                :class="[
                  'flex flex-col items-center gap-0.5 rounded-lg p-1.5 transition-colors',
                  row.status === 'active'
                    ? 'text-gray-500 hover:bg-yellow-50 hover:text-yellow-600 dark:hover:bg-yellow-900/20 dark:hover:text-yellow-400'
                    : 'text-gray-500 hover:bg-green-50 hover:text-green-600 dark:hover:bg-green-900/20 dark:hover:text-green-400'
                ]"
              >
                <Icon v-if="row.status === 'active'" name="ban" size="sm" />
                <Icon v-else name="checkCircle" size="sm" />
                <span class="text-xs">{{ row.status === 'active' ? t('keys.disable') : t('keys.enable') }}</span>
              </button>
              <button
                class="key-action-menu-trigger flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
                :class="{ 'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white': actionMenuKey?.id === row.id }"
                aria-haspopup="menu"
                :aria-expanded="actionMenuKey?.id === row.id"
                :aria-controls="actionMenuKey?.id === row.id ? `key-action-menu-${row.id}` : undefined"
                @click="openKeyActionMenu(row, $event)"
              >
                <Icon name="more" size="sm" />
                <span class="text-xs">{{ t('common.more') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('keys.noKeysYet')"
              :description="t('keys.createFirstKey')"
              :action-text="t('keys.createKey')"
              @action="openCreateModal"
            />
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

    <!-- 创建/编辑密钥共用弹窗；表单允许在窄屏内收缩，避免撑开页面。 -->
    <BaseDialog
      :show="showCreateModal || showEditModal"
      :title="showEditModal ? t('keys.editKey') : t('keys.createKey')"
      width="normal"
      @close="closeModals"
    >
      <form id="key-form" @submit.prevent="handleSubmit" class="key-form-controls min-w-0 max-w-full space-y-5">
        <div>
          <label class="input-label">{{ t('keys.nameLabel') }}</label>
          <input
            v-model="formData.name"
            type="text"
            required
            class="input"
            :placeholder="t('keys.namePlaceholder')"
            data-tour="key-form-name"
          />
        </div>

        <!-- 复合模式使用项目 Toggle，切换后改为完整映射编辑。 -->
        <div class="flex items-center justify-between gap-4">
          <div>
            <label class="input-label mb-0">{{ t('keys.composite.label') }}</label>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('keys.composite.hint') }}</p>
          </div>
          <Toggle
            :model-value="formData.is_composite"
            size="sm"
            data-test="composite-key-toggle"
            @update:model-value="onCompositeModeChange"
          />
        </div>

        <div class="space-y-3">
          <label class="input-label">{{ t('keys.billing.modeLabel') }}</label>
          <Select
            v-model="formData.billing_mode"
            :options="billingModeOptions"
            :placeholder="t('keys.billing.modeLabel')"
            data-test="api-key-billing-mode"
            @change="onBillingModeChange"
          />

          <div v-if="formData.billing_mode === 'subscription'">
            <label class="input-label">{{ t('keys.billing.subscriptionLabel') }}</label>
            <Select
              v-model="formData.preferred_subscription_id"
              :options="billingSubscriptionOptions"
              :placeholder="t('keys.billing.selectSubscription')"
              :searchable="true"
              :disabled="billingOptionsLoading"
              data-test="api-key-preferred-subscription"
              @change="onPreferredSubscriptionChange"
            />
          </div>
        </div>

        <div v-if="!formData.is_composite">
          <label class="input-label">{{ t('keys.groupLabel') }}</label>
          <Select
            v-model="formData.group_id"
            :options="formGroupOptions"
            :placeholder="t('keys.selectGroup')"
            :searchable="true"
            :search-placeholder="t('keys.searchGroup')"
            :disabled="formGroupsLoading"
            data-tour="key-form-group"
          >
            <template #selected="{ option }">
              <GroupBadge
                v-if="option"
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :display-brand="(option as unknown as GroupOption).displayBrand"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                :peak-start="(option as unknown as GroupOption).peakStart"
                :peak-end="(option as unknown as GroupOption).peakEnd"
                :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
              />
              <span v-else class="text-gray-400">{{ t('keys.selectGroup') }}</span>
            </template>
            <template #option="{ option, selected }">
              <GroupOptionItem
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :display-brand="(option as unknown as GroupOption).displayBrand"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                :peak-start="(option as unknown as GroupOption).peakStart"
                :peak-end="(option as unknown as GroupOption).peakEnd"
                :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
                :description="(option as unknown as GroupOption).description"
                :selected="selected"
              />
            </template>
          </Select>
        </div>

        <div v-else class="space-y-3" data-test="composite-group-editor">
          <div
            v-for="(binding, index) in formData.composite_groups"
            :key="binding.local_id"
            class="grid min-w-0 grid-cols-1 items-start gap-2 rounded-md border border-gray-200 p-3 sm:grid-cols-[minmax(0,1fr)_minmax(7rem,0.65fr)_auto] dark:border-dark-600"
          >
            <Select
              v-model="binding.group_id"
              :options="formGroupOptions"
              :placeholder="t('keys.selectGroup')"
              :searchable="true"
              :disabled="formGroupsLoading"
              class="min-w-0"
            />
            <div class="min-w-0">
              <input
                v-model="binding.prefix"
                type="text"
                maxlength="32"
                class="input min-w-0 font-mono"
                :class="{ 'border-red-500 dark:border-red-500': compositeBindingError(index) }"
                :placeholder="t('keys.composite.prefixPlaceholder')"
              />
              <p v-if="compositeBindingError(index)" class="mt-1 text-xs text-red-500">
                {{ compositeBindingError(index) }}
              </p>
            </div>
            <div class="flex items-center justify-end gap-1 sm:justify-start">
              <button type="button" class="rounded p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-700" :disabled="index === 0" :title="t('keys.composite.moveUp')" @click="moveCompositeBinding(index, -1)">
                <Icon name="arrowUp" size="sm" />
              </button>
              <button type="button" class="rounded p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-dark-700" :disabled="index === formData.composite_groups.length - 1" :title="t('keys.composite.moveDown')" @click="moveCompositeBinding(index, 1)">
                <Icon name="arrowDown" size="sm" />
              </button>
              <button type="button" class="rounded p-1.5 text-red-500 hover:bg-red-50 disabled:opacity-30 dark:hover:bg-red-900/20" :disabled="formData.composite_groups.length <= 1" :title="t('common.delete')" @click="removeCompositeBinding(index)">
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </div>
          <button
            type="button"
            class="btn btn-secondary w-full"
            :disabled="formData.composite_groups.length >= 20"
            @click="addCompositeBinding"
          >
            <Icon name="plus" size="sm" class="mr-1.5" />
            {{ t('keys.composite.addMapping') }}
          </button>
        </div>

        <!-- 单 Key Fast 策略使用项目统一选择框，系统策略仍在服务端最终裁决。 -->
        <div>
          <label class="input-label">{{ t('keys.fastModePolicyLabel') }}</label>
          <Select
            v-model="formData.fast_mode_policy"
            :options="fastModePolicyOptions"
            :placeholder="t('keys.fastModePolicyLabel')"
            data-test="fast-mode-policy-select"
          />
        </div>

        <!-- 分组停用时的请求级自动降级开关。 -->
        <div class="flex items-center justify-between">
          <label class="input-label mb-0">{{ t('keys.fallbackToDefaultGroupWhenUnavailable') }}</label>
          <Toggle v-model="formData.fallback_to_default_group_when_unavailable" size="sm" />
        </div>

        <!-- 模型重定向按行编辑，删除全部行会在更新时提交空对象。 -->
        <div class="space-y-3" data-test="model-mapping-editor">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <label class="input-label mb-0">{{ t('keys.modelRedirect.label') }}</label>
              <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                {{ t('keys.modelRedirect.hint') }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-secondary shrink-0"
              :disabled="formData.model_mapping_rows.length >= 100"
              :title="t('keys.modelRedirect.addRule')"
              data-test="model-mapping-add"
              @click="addModelMappingRow"
            >
              <Icon name="plus" size="sm" class="mr-1.5" />
              {{ t('keys.modelRedirect.addRule') }}
            </button>
          </div>

          <p
            v-if="formData.model_mapping_rows.length === 0"
            class="rounded-md border border-dashed border-gray-200 px-3 py-4 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400"
          >
            {{ t('keys.modelRedirect.empty') }}
          </p>

          <div
            v-for="(row, index) in formData.model_mapping_rows"
            :key="row.local_id"
            class="grid min-w-0 grid-cols-1 items-start gap-2 border-b border-gray-200 pb-3 last:border-b-0 last:pb-0 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto] dark:border-dark-600"
            data-test="model-mapping-row"
          >
            <div class="min-w-0">
              <input
                v-model="row.source"
                type="text"
                class="input min-w-0 font-mono"
                :class="{ 'border-red-500 dark:border-red-500': modelMappingRowErrors[row.local_id]?.source }"
                :placeholder="t('keys.modelRedirect.sourcePlaceholder')"
                :aria-label="t('keys.modelRedirect.source')"
                :data-test="`model-mapping-source-${index}`"
              />
              <p v-if="modelMappingRowErrors[row.local_id]?.source" class="mt-1 text-xs text-red-500" role="alert">
                {{ modelMappingRowErrors[row.local_id]?.source }}
              </p>
            </div>
            <Icon name="arrowRight" size="sm" class="hidden text-gray-400 sm:mt-3 sm:block" />
            <div class="min-w-0">
              <input
                v-model="row.target"
                type="text"
                class="input min-w-0 font-mono"
                :class="{ 'border-red-500 dark:border-red-500': modelMappingRowErrors[row.local_id]?.target }"
                :placeholder="t('keys.modelRedirect.targetPlaceholder')"
                :aria-label="t('keys.modelRedirect.target')"
                :data-test="`model-mapping-target-${index}`"
              />
              <p v-if="modelMappingRowErrors[row.local_id]?.target" class="mt-1 text-xs text-red-500" role="alert">
                {{ modelMappingRowErrors[row.local_id]?.target }}
              </p>
            </div>
            <button
              type="button"
              class="flex h-9 w-9 items-center justify-center rounded text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20"
              :title="t('common.delete')"
              :aria-label="t('common.delete')"
              :data-test="`model-mapping-remove-${index}`"
              @click="removeModelMappingRow(index)"
            >
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>

        <!-- Custom Key Section (only for create) -->
        <div v-if="!showEditModal" class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.customKeyLabel') }}</label>
            <button
              type="button"
              @click="formData.use_custom_key = !formData.use_custom_key"
              :class="[
                'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none',
                formData.use_custom_key ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  formData.use_custom_key ? 'translate-x-4' : 'translate-x-0'
                ]"
              />
            </button>
          </div>
          <div v-if="formData.use_custom_key">
            <input
              v-model="formData.custom_key"
              type="text"
              class="input font-mono"
              :placeholder="t('keys.customKeyPlaceholder')"
              :class="{ 'border-red-500 dark:border-red-500': customKeyError }"
            />
            <p v-if="customKeyError" class="mt-1 text-sm text-red-500">{{ customKeyError }}</p>
            <p v-else class="input-hint">{{ t('keys.customKeyHint') }}</p>
          </div>
        </div>

        <div v-if="showEditModal" class="space-y-3">
          <div class="flex items-center justify-between gap-4">
            <label class="input-label mb-0">{{ t('keys.statusLabel') }}</label>
            <Toggle
              :model-value="formData.status === 'active'"
              :disabled="Boolean(selectedKey?.team_owner_disabled)"
              :aria-label="t('keys.statusLabel')"
              :aria-describedby="selectedKey?.team_owner_disabled ? 'team-owner-disabled-hint' : undefined"
              data-test="key-status-toggle"
              @update:model-value="onStatusToggle"
            />
          </div>
          <p
            v-if="selectedKey?.team_owner_disabled"
            id="team-owner-disabled-hint"
            class="mt-2 flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-300"
          >
            <Icon name="lock" size="sm" class="mt-0.5 shrink-0" />
            <span>{{ t('keys.teamOwnerDisabledHint') }}</span>
          </p>
        </div>

        <!-- IP Restriction Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.ipRestriction') }}</label>
            <button
              type="button"
              @click="formData.enable_ip_restriction = !formData.enable_ip_restriction"
              :class="[
                'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none',
                formData.enable_ip_restriction ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  formData.enable_ip_restriction ? 'translate-x-4' : 'translate-x-0'
                ]"
              />
            </button>
          </div>

          <div v-if="formData.enable_ip_restriction" class="space-y-4 pt-2">
            <div>
              <label class="input-label">{{ t('keys.ipWhitelist') }}</label>
              <textarea
                v-model="formData.ip_whitelist"
                rows="3"
                class="input font-mono text-sm"
                :placeholder="t('keys.ipWhitelistPlaceholder')"
              />
              <p class="input-hint">{{ t('keys.ipWhitelistHint') }}</p>
            </div>

            <div>
              <label class="input-label">{{ t('keys.ipBlacklist') }}</label>
              <textarea
                v-model="formData.ip_blacklist"
                rows="3"
                class="input font-mono text-sm"
                :placeholder="t('keys.ipBlacklistPlaceholder')"
              />
              <p class="input-hint">{{ t('keys.ipBlacklistHint') }}</p>
            </div>
          </div>
        </div>

        <!-- Quota Limit Section -->
        <div class="space-y-3">
          <label class="input-label">{{ t('keys.quotaLimit') }}</label>
          <!-- Switch commented out - always show input, 0 = unlimited
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.quotaLimit') }}</label>
            <button
              type="button"
              @click="formData.enable_quota = !formData.enable_quota"
              :class="[
                'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none',
                formData.enable_quota ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  formData.enable_quota ? 'translate-x-4' : 'translate-x-0'
                ]"
              />
            </button>
          </div>
          -->

          <div class="space-y-4">
            <div>
              <div class="relative">
                <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.quota"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input pl-7"
                  :placeholder="t('keys.quotaAmountPlaceholder')"
                />
              </div>
              <p class="input-hint">{{ t('keys.quotaAmountHint') }}</p>
            </div>

            <!-- Quota used display (only in edit mode) -->
            <div v-if="showEditModal && selectedKey && selectedKey.quota > 0">
              <label class="input-label">{{ t('keys.quotaUsed') }}</label>
              <div class="flex items-center gap-2">
                <div class="flex-1 h-9 rounded-lg bg-gray-100 px-3 py-1.5 dark:bg-dark-700">
                  <span class="font-medium text-gray-900 dark:text-white">
                    {{ formatBalanceAmount(selectedKey.quota_used, { fractionDigits: 4 }) }}
                  </span>
                  <span class="mx-2 text-gray-400">/</span>
                  <span class="text-gray-500 dark:text-gray-400">
                    {{ formatBalanceAmount(selectedKey.quota, { fractionDigits: 2 }) }}
                  </span>
                </div>
                <button
                  type="button"
                  @click="confirmResetQuota"
                  class="btn btn-secondary text-sm"
                  :title="t('keys.resetQuotaUsed')"
                >
                  {{ t('keys.reset') }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Rate Limit Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.rateLimitSection') }}</label>
            <button
              type="button"
              @click="formData.enable_rate_limit = !formData.enable_rate_limit"
              :class="[
                'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none',
                formData.enable_rate_limit ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  formData.enable_rate_limit ? 'translate-x-4' : 'translate-x-0'
                ]"
              />
            </button>
          </div>

          <div v-if="formData.enable_rate_limit" class="space-y-4 pt-2">
            <p class="input-hint -mt-2">{{ t('keys.rateLimitHint') }}</p>
            <!-- 5-Hour Limit -->
            <div>
              <label class="input-label">{{ t('keys.rateLimit5h') }}</label>
              <div class="relative">
                <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.rate_limit_5h"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input pl-7"
                  :placeholder="'0'"
                />
              </div>
              <!-- Usage info (edit mode only) -->
              <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_5h > 0" class="mt-2">
                <div class="flex items-center gap-2">
                  <div class="flex-1 h-9 rounded-lg bg-gray-100 px-3 py-1.5 text-sm dark:bg-dark-700">
                    <span :class="[
                      'font-medium',
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h ? 'text-red-500' :
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8 ? 'text-yellow-500' :
                      'text-gray-900 dark:text-white'
                    ]">
                      {{ formatBalanceAmount(selectedKey.usage_5h, { fractionDigits: 4 }) }}
                    </span>
                    <span class="mx-2 text-gray-400">/</span>
                    <span class="text-gray-500 dark:text-gray-400">
                      {{ formatBalanceAmount(selectedKey.rate_limit_5h, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h ? 'bg-red-500' :
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8 ? 'bg-yellow-500' :
                      'bg-green-500'
                    ]"
                    :style="{ width: Math.min((selectedKey.usage_5h / selectedKey.rate_limit_5h) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </div>

            <!-- Daily Limit -->
            <div>
              <label class="input-label">{{ t('keys.rateLimit1d') }}</label>
              <div class="relative">
                <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.rate_limit_1d"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input pl-7"
                  :placeholder="'0'"
                />
              </div>
              <!-- Usage info (edit mode only) -->
              <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_1d > 0" class="mt-2">
                <div class="flex items-center gap-2">
                  <div class="flex-1 h-9 rounded-lg bg-gray-100 px-3 py-1.5 text-sm dark:bg-dark-700">
                    <span :class="[
                      'font-medium',
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d ? 'text-red-500' :
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8 ? 'text-yellow-500' :
                      'text-gray-900 dark:text-white'
                    ]">
                      {{ formatBalanceAmount(selectedKey.usage_1d, { fractionDigits: 4 }) }}
                    </span>
                    <span class="mx-2 text-gray-400">/</span>
                    <span class="text-gray-500 dark:text-gray-400">
                      {{ formatBalanceAmount(selectedKey.rate_limit_1d, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d ? 'bg-red-500' :
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8 ? 'bg-yellow-500' :
                      'bg-green-500'
                    ]"
                    :style="{ width: Math.min((selectedKey.usage_1d / selectedKey.rate_limit_1d) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </div>

            <!-- 7-Day Limit -->
            <div>
              <label class="input-label">{{ t('keys.rateLimit7d') }}</label>
              <div class="relative">
                <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.rate_limit_7d"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input pl-7"
                  :placeholder="'0'"
                />
              </div>
              <!-- Usage info (edit mode only) -->
              <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_7d > 0" class="mt-2">
                <div class="flex items-center gap-2">
                  <div class="flex-1 h-9 rounded-lg bg-gray-100 px-3 py-1.5 text-sm dark:bg-dark-700">
                    <span :class="[
                      'font-medium',
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d ? 'text-red-500' :
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8 ? 'text-yellow-500' :
                      'text-gray-900 dark:text-white'
                    ]">
                      {{ formatBalanceAmount(selectedKey.usage_7d, { fractionDigits: 4 }) }}
                    </span>
                    <span class="mx-2 text-gray-400">/</span>
                    <span class="text-gray-500 dark:text-gray-400">
                      {{ formatBalanceAmount(selectedKey.rate_limit_7d, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d ? 'bg-red-500' :
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8 ? 'bg-yellow-500' :
                      'bg-green-500'
                    ]"
                    :style="{ width: Math.min((selectedKey.usage_7d / selectedKey.rate_limit_7d) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </div>

            <!-- Reset Rate Limit button (edit mode only) -->
            <div v-if="showEditModal && selectedKey && (selectedKey.rate_limit_5h > 0 || selectedKey.rate_limit_1d > 0 || selectedKey.rate_limit_7d > 0)">
              <button
                type="button"
                @click="confirmResetRateLimit"
                class="btn btn-secondary text-sm"
              >
                {{ t('keys.resetRateLimitUsage') }}
              </button>
            </div>
          </div>
        </div>

        <!-- Expiration Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.expiration') }}</label>
            <button
              type="button"
              @click="formData.enable_expiration = !formData.enable_expiration"
              :class="[
                'relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none',
                formData.enable_expiration ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
              ]"
            >
              <span
                :class="[
                  'pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                  formData.enable_expiration ? 'translate-x-4' : 'translate-x-0'
                ]"
              />
            </button>
          </div>

          <div v-if="formData.enable_expiration" class="space-y-4 pt-2">
            <!-- Quick select buttons (for both create and edit mode) -->
            <div class="flex flex-wrap gap-2">
              <button
                v-for="days in ['7', '30', '90']"
                :key="days"
                type="button"
                @click="setExpirationDays(parseInt(days))"
                :class="[
                  'rounded-lg px-3 py-1.5 text-sm transition-colors',
                  formData.expiration_preset === days
                    ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600'
                ]"
              >
                {{ showEditModal ? t('keys.extendDays', { days }) : t('keys.expiresInDays', { days }) }}
              </button>
              <button
                type="button"
                @click="formData.expiration_preset = 'custom'"
                :class="[
                  'rounded-lg px-3 py-1.5 text-sm transition-colors',
                  formData.expiration_preset === 'custom'
                    ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600'
                ]"
              >
                {{ t('keys.customDate') }}
              </button>
            </div>

            <!-- Date picker (always show for precise adjustment) -->
            <div>
              <label class="input-label">{{ t('keys.expirationDate') }}</label>
              <input
                v-model="formData.expiration_date"
                type="datetime-local"
                class="input"
              />
              <p class="input-hint">{{ t('keys.expirationDateHint') }}</p>
            </div>

            <!-- Current expiration display (only in edit mode) -->
            <div v-if="showEditModal && selectedKey?.expires_at" class="text-sm">
              <span class="text-gray-500 dark:text-gray-400">{{ t('keys.currentExpiration') }}: </span>
              <span class="font-medium text-gray-900 dark:text-white">
                {{ formatDateTime(selectedKey.expires_at) }}
              </span>
            </div>
          </div>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeModals" type="button" class="btn btn-secondary h-9 py-1.5">
            {{ t('common.cancel') }}
          </button>
          <button
            form="key-form"
            type="submit"
            :disabled="submitting"
            class="btn btn-primary h-9 py-1.5"
            data-tour="key-form-submit"
          >
            <svg
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
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
              submitting
                ? t('keys.saving')
                : showEditModal
                  ? t('common.update')
                  : t('common.create')
            }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('keys.deleteKey')"
      :message="t('keys.deleteConfirmMessage', { name: selectedKey?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Reset Quota Confirmation Dialog -->
    <ConfirmDialog
      :show="showResetQuotaDialog"
      :title="t('keys.resetQuotaTitle')"
      :message="t('keys.resetQuotaConfirmMessage', { name: selectedKey?.name, used: selectedKey?.quota_used?.toFixed(4) })"
      :confirm-text="t('keys.reset')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="resetQuotaUsed"
      @cancel="showResetQuotaDialog = false"
    />

    <!-- Reset Rate Limit Confirmation Dialog -->
    <ConfirmDialog
      :show="showResetRateLimitDialog"
      :title="t('keys.resetRateLimitTitle')"
      :message="t('keys.resetRateLimitConfirmMessage', { name: selectedKey?.name })"
      :confirm-text="t('keys.reset')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="resetRateLimitUsage"
      @cancel="showResetRateLimitDialog = false"
    />

    <!-- Use Key Modal -->
    <UseKeyModal
      :show="showUseKeyModal"
      :api-key="selectedKey?.key || ''"
      :base-url="publicSettings?.api_base_url || ''"
      :platform="selectedKey?.group?.platform || null"
      :allowed-client-protocols="selectedKey?.group?.allowed_client_protocols"
      :composite-groups="selectedKey?.composite_groups || []"
      @close="closeUseKeyModal"
    />

    <TfCliImportDialog
      :show="showTfCliImportDialog"
      :api-key="tfImportKey"
      @close="closeTfCliImportDialog"
    />

    <KeyActionMenu
      :show="Boolean(actionMenuKey)"
      :api-key="actionMenuKey"
      :position="actionMenuPosition"
      :allow-import="!publicSettings?.hide_ccs_import_button"
      @close="closeKeyActionMenu"
      @use="openUseKeyModal"
      @import-tf="openTfCliImportDialog"
      @import="importToCcswitch"
      @delete="confirmDelete"
    />

    <!-- CCS Client Selection Dialog for Antigravity -->
    <BaseDialog
      :show="showCcsClientSelect"
      :title="t('keys.ccsClientSelect.title')"
      width="narrow"
      @close="closeCcsClientSelect"
    >
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ t('keys.ccsClientSelect.description') }}
	        </p>
	        <div class="grid grid-cols-2 gap-3">
	          <button
	            @click="handleCcsClientSelect('claude')"
	            class="flex flex-col items-center gap-2 p-4 rounded-xl border-2 border-gray-200 dark:border-dark-600 hover:border-primary-500 dark:hover:border-primary-500 hover:bg-primary-50 dark:hover:bg-primary-900/20 transition-all"
	          >
	            <Icon name="terminal" size="xl" class="text-gray-600 dark:text-gray-400" />
	            <span class="font-medium text-gray-900 dark:text-white">{{
	              t('keys.ccsClientSelect.claudeCode')
	            }}</span>
	            <span class="text-xs text-gray-500 dark:text-gray-400">{{
	              t('keys.ccsClientSelect.claudeCodeDesc')
	            }}</span>
	          </button>
	          <button
	            @click="handleCcsClientSelect('gemini')"
	            class="flex flex-col items-center gap-2 p-4 rounded-xl border-2 border-gray-200 dark:border-dark-600 hover:border-primary-500 dark:hover:border-primary-500 hover:bg-primary-50 dark:hover:bg-primary-900/20 transition-all"
	          >
	            <Icon name="sparkles" size="xl" class="text-gray-600 dark:text-gray-400" />
	            <span class="font-medium text-gray-900 dark:text-white">{{
	              t('keys.ccsClientSelect.geminiCli')
	            }}</span>
	            <span class="text-xs text-gray-500 dark:text-gray-400">{{
	              t('keys.ccsClientSelect.geminiCliDesc')
	            }}</span>
	          </button>
	        </div>
	      </div>
      <template #footer>
        <div class="flex justify-end">
          <button @click="closeCcsClientSelect" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="dataSharingNoticeDialog.show"
      title="数据共享须知"
      width="normal"
      :close-on-click-outside="false"
      @close="closeDataSharingNotice"
    >
      <div class="space-y-4">
        <div class="max-h-72 overflow-y-auto whitespace-pre-wrap rounded-lg border border-gray-200 bg-gray-50 p-4 text-sm leading-6 text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200">
          {{ dataSharingNoticeDialog.notice?.content || '正在加载数据共享须知...' }}
        </div>
        <div class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
          只有点击确认后，API Key 才会切换到该数据共享分组。
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeDataSharingNotice">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="dataSharingNoticeDialog.loading || dataSharingNoticeDialog.countdown > 0"
            @click="confirmDataSharingNotice"
          >
            <Icon v-if="dataSharingNoticeDialog.countdown <= 0" name="check" size="md" class="mr-2" />
            {{
              dataSharingNoticeDialog.countdown > 0
                ? `请等待 ${dataSharingNoticeDialog.countdown}s`
                : '我已阅读并确认'
            }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Group Selector Dropdown (Teleported to body to avoid overflow clipping) -->
    <Teleport to="body">
      <div
        v-if="groupSelectorKeyId !== null && dropdownPosition"
        ref="dropdownRef"
        class="animate-in fade-in slide-in-from-top-2 fixed z-[100000020] w-max max-w-[calc(100vw-16px)] overflow-hidden rounded-control bg-white shadow-lg ring-1 ring-black/5 duration-200 sm:min-w-[380px] dark:bg-dark-800 dark:ring-white/10"
        style="pointer-events: auto !important;"
        :style="{
          top: dropdownPosition.top !== undefined ? dropdownPosition.top + 'px' : undefined,
          bottom: dropdownPosition.bottom !== undefined ? dropdownPosition.bottom + 'px' : undefined,
          left: dropdownPosition.left + 'px'
        }"
      >
        <!-- Search box -->
        <div class="border-b border-gray-100 p-2 dark:border-dark-700">
          <div class="relative">
            <svg class="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <input
              v-model="groupSearchQuery"
              type="text"
              class="w-full rounded-lg border border-primary-900/10 bg-gray-50 py-1.5 pl-8 pr-3 text-sm text-gray-900 placeholder-gray-400 outline-none focus:border-primary-900/10 focus:ring-2 focus:ring-black/10 dark:border-dark-600 dark:bg-dark-700 dark:text-white dark:placeholder-gray-500 dark:focus:border-primary-600 dark:focus:ring-primary-600"
              :placeholder="t('keys.searchGroup')"
              @click.stop
            />
          </div>
        </div>
        <!-- Group list -->
        <div class="max-h-80 overflow-y-auto p-1.5">
          <button
            v-for="option in filteredGroupOptions"
            :key="option.value ?? 'null'"
            @click="changeGroup(selectedKeyForGroup!, option.value)"
            :class="[
              'flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-sm transition-colors',
              'border-b border-gray-100 last:border-0 dark:border-dark-700',
              selectedKeyForGroup?.group_id === option.value ||
              (!selectedKeyForGroup?.group_id && option.value === null)
                ? 'bg-primary-50 dark:bg-primary-900/20'
                : 'hover:bg-gray-100 dark:hover:bg-dark-700'
            ]"
            :title="option.description || undefined"
          >
            <GroupOptionItem
              :name="option.label"
              :platform="option.platform"
              :display-brand="option.displayBrand"
              :rate-multiplier="option.rate"
              :user-rate-multiplier="option.userRate"
              :peak-rate-enabled="option.peakRateEnabled"
              :peak-start="option.peakStart"
              :peak-end="option.peakEnd"
              :peak-rate-multiplier="option.peakRateMultiplier"
              :description="option.description"
              :selected="
                selectedKeyForGroup?.group_id === option.value ||
                (!selectedKeyForGroup?.group_id && option.value === null)
              "
            />
          </button>
          <!-- Empty state when search has no results -->
          <div v-if="filteredGroupOptions.length === 0" class="py-4 text-center text-sm text-gray-400 dark:text-gray-500">
            {{ t('keys.noGroupFound') }}
          </div>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
	import { ref, reactive, computed, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
	import { useI18n } from 'vue-i18n'
	import { useRoute } from 'vue-router'
	import { useAppStore } from '@/stores/app'
import { useOnboardingStore } from '@/stores/onboarding'
import { useClipboard } from '@/composables/useClipboard'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'

const { t } = useI18n()
import { keysAPI, authAPI, usageAPI, userGroupsAPI, dataSharingAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
	import DataTable from '@/components/common/DataTable.vue'
	import Pagination from '@/components/common/Pagination.vue'
	import BaseDialog from '@/components/common/BaseDialog.vue'
	import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
	import EmptyState from '@/components/common/EmptyState.vue'
	import Select from '@/components/common/Select.vue'
	import Toggle from '@/components/common/Toggle.vue'
	import SearchInput from '@/components/common/SearchInput.vue'
	import Icon from '@/components/icons/Icon.vue'
	import KeyActionMenu from '@/components/keys/KeyActionMenu.vue'
	import UseKeyModal from '@/components/keys/UseKeyModal.vue'
	import TfCliImportDialog from '@/components/keys/TfCliImportDialog.vue'
	import EndpointPopover from '@/components/keys/EndpointPopover.vue'
	import GroupBadge from '@/components/common/GroupBadge.vue'
	import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
	import ScopeDropdown, { type DataScope } from '@/components/team/ScopeDropdown.vue'
	import type {
	  ApiKey,
	  ApiKeyBillingMode,
	  ApiKeyBillingSubscriptionOption,
	  ApiKeyFastModePolicy,
	  CreateApiKeyRequest,
	  Group,
	  PublicSettings,
	  GroupPlatform,
	  UpdateApiKeyRequest
	} from '@/types'
import type { Column } from '@/components/common/types'
import type { BatchApiKeyUsageStats } from '@/api/usage'
import type { DataShareNotice } from '@/api/dataSharing'
import { formatDateTime } from '@/utils/format'
import {
  buildCcSwitchImportDeeplink,
  buildCcSwitchUsageScript,
  type CcSwitchClientType
} from '@/utils/ccswitchImport'

// Helper to format date for datetime-local input
const formatDateTimeLocal = (isoDate: string): string => {
  const date = new Date(isoDate)
  const pad = (n: number) => n.toString().padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

interface GroupOption {
  value: number
  label: string
  description: string | null
  displayBrand: string | null
  rate: number
  userRate: number | null
  peakRateEnabled: boolean
  peakStart: string
  peakEnd: string
  peakRateMultiplier: number
  platform: GroupPlatform
  dataSharingEnabled?: boolean
}

const appStore = useAppStore()
const route = useRoute()
const scope = ref<DataScope>(route?.query?.scope === 'team' ? 'team' : 'personal')
const onboardingStore = useOnboardingStore()
const { copyToClipboard: clipboardCopy } = useClipboard()
const { balanceUnitName, balanceUnitSymbol, formatBalanceAmount } = useBalanceDisplay()

const formatBalancePair = (
  used: number | null | undefined,
  limit: number | null | undefined,
  usedDigits: number,
  limitDigits: number,
  spaced: boolean = true
) => {
  const separator = spaced ? ' / ' : '/'
  return `${formatBalanceAmount(used, { fractionDigits: usedDigits })}${separator}${formatBalanceAmount(limit, { fractionDigits: limitDigits })}`
}

const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('common.name'), sortable: true },
  { key: 'id', label: t('keys.id'), sortable: true },
  { key: 'key', label: t('keys.apiKey'), sortable: false },
  { key: 'group', label: t('keys.group'), sortable: false },
  { key: 'current_concurrency', label: t('keys.currentConcurrency'), sortable: true },
  { key: 'usage', label: t('keys.usage'), sortable: true },
  { key: 'rate_limit', label: t('keys.rateLimitColumn'), sortable: false },
  { key: 'expires_at', label: t('keys.expiresAt'), sortable: true },
  { key: 'status', label: t('common.status'), sortable: true },
  { key: 'last_used_at', label: t('keys.lastUsedAt'), sortable: true },
  { key: 'last_used_ip', label: t('keys.lastUsedIP'), sortable: false },
  { key: 'created_at', label: t('keys.created'), sortable: true },
  { key: 'actions', label: t('common.actions'), sortable: false }
])

const ALWAYS_VISIBLE_COLUMNS = new Set(['name', 'actions'])
const DEFAULT_HIDDEN_COLUMNS = ['id', 'rate_limit', 'last_used_at', 'last_used_ip']
const HIDDEN_COLUMNS_KEY = 'api-key-hidden-columns'
const COLUMN_SETTINGS_VERSION_KEY = 'api-key-column-settings-version'
const COLUMN_SETTINGS_VERSION = 3
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['last_used_ip'],
  3: ['id']
}

const toggleableColumns = computed(() =>
  allColumns.value.filter((column) => !ALWAYS_VISIBLE_COLUMNS.has(column.key))
)
const hiddenColumns = reactive<Set<string>>(new Set())

// 加载 API Key 表格列设置；新列默认展示，低频列首次加载默认隐藏。
const loadSavedColumns = () => {
  hiddenColumns.clear()
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    const validColumnKeys = new Set(allColumns.value.map((column) => column.key))
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed
        .filter((key) =>
          typeof key === 'string' &&
          validColumnKeys.has(key) &&
          !ALWAYS_VISIBLE_COLUMNS.has(key)
        )
        .forEach((key) => hiddenColumns.add(key))
      const rawStoredVersion = Number(localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? '1')
      // 版本值被损坏时按最旧版处理，避免新的低频列意外显示。
      const storedVersion = Number.isInteger(rawStoredVersion) && rawStoredVersion >= 1
        ? rawStoredVersion
        : 1
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        for (let version = storedVersion + 1; version <= COLUMN_SETTINGS_VERSION; version++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[version] ?? []) {
            if (validColumnKeys.has(key) && !ALWAYS_VISIBLE_COLUMNS.has(key)) {
              hiddenColumns.add(key)
            }
          }
        }
        saveColumnsToStorage()
      } else {
        localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
      localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
    }
  } catch (error) {
    console.error('Failed to load API key table columns:', error)
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

const saveColumnsToStorage = () => {
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
  } catch (error) {
    console.error('Failed to save API key table columns:', error)
  }
}

const toggleColumn = (key: string) => {
  if (ALWAYS_VISIBLE_COLUMNS.has(key)) return
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
}

const isColumnVisible = (key: string) => !hiddenColumns.has(key)

const columns = computed<Column[]>(() =>
  allColumns.value.filter((column) => ALWAYS_VISIBLE_COLUMNS.has(column.key) || !hiddenColumns.has(column.key))
)

const apiKeys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
// 表单分组独立于列表筛选分组，指定订阅时只收窄表单选择范围。
const formGroups = ref<Group[]>([])
const billingSubscriptions = ref<ApiKeyBillingSubscriptionOption[]>([])
const billingOptionsLoading = ref(false)
const formGroupsLoading = ref(false)
// 首次 Key 请求开始前也保持加载态，避免公共设置请求期间误显示空列表。
const loading = ref(true)
const usageLoading = ref(false)
const submitting = ref(false)
const now = ref(new Date())
let resetTimer: ReturnType<typeof setInterval> | null = null
const usageStats = ref<Record<string, BatchApiKeyUsageStats>>({})
const userGroupRates = ref<Record<number, number>>({})

const pagination = ref({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = ref({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

// Filter state
const filterSearch = ref('')
const filterStatus = ref('')
const filterGroupId = ref<string | number>('')
const showFilterDropdown = ref(false)
const filterDropdownRef = ref<HTMLElement | null>(null)

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const showResetQuotaDialog = ref(false)
const showResetRateLimitDialog = ref(false)
const showUseKeyModal = ref(false)
const showTfCliImportDialog = ref(false)
const showCcsClientSelect = ref(false)
const showColumnDropdown = ref(false)
const pendingCcsRow = ref<ApiKey | null>(null)
const tfImportKey = ref<ApiKey | null>(null)
const selectedKey = ref<ApiKey | null>(null)
const actionMenuKey = ref<ApiKey | null>(null)
const actionMenuPosition = ref<{ top: number; left: number } | null>(null)
const copiedKeyId = ref<number | null>(null)
const groupSelectorKeyId = ref<number | null>(null)
const publicSettings = ref<PublicSettings | null>(null)
const teamFeatureEnabled = computed(() => publicSettings.value?.team_enabled !== false)
const dropdownRef = ref<HTMLElement | null>(null)
const columnDropdownRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<{ top?: number; bottom?: number; left: number } | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())
let abortController: AbortController | null = null
let dataSharingCountdownTimer: number | null = null
let compositeBindingSequence = 0
let modelMappingSequence = 0
let formGroupsRequestID = 0

// 新建本地映射行时使用稳定 ID，排序不会导致输入框重建。
const newCompositeBinding = (groupId: number | null = null, prefix = '') => ({
  local_id: ++compositeBindingSequence,
  group_id: groupId,
  prefix
})

// 模型重定向行使用稳定 ID，输入校验更新时不会重建相邻输入框。
const newModelMappingRow = (source = '', target = '') => ({
  local_id: ++modelMappingSequence,
  source,
  target
})

const dataSharingNoticeDialog = ref<{
  show: boolean
  loading: boolean
  countdown: number
  notice: DataShareNotice | null
  key: ApiKey | null
  targetGroupId: number | null
  mode: 'row' | 'form' | null
}>({
  show: false,
  loading: false,
  countdown: 0,
  notice: null,
  key: null,
  targetGroupId: null,
  mode: null
})

// 获取当前正在切换分组的 API Key。
const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

const formData = ref({
  name: '',
  group_id: null as number | null,
  is_composite: false,
  composite_groups: [] as Array<ReturnType<typeof newCompositeBinding>>,
  status: 'active' as 'active' | 'inactive',
  fast_mode_policy: 'follow_request' as ApiKeyFastModePolicy,
  billing_mode: 'auto' as ApiKeyBillingMode,
  preferred_subscription_id: null as number | null,
  model_mapping_rows: [] as Array<ReturnType<typeof newModelMappingRow>>,
  use_custom_key: false,
  custom_key: '',
  enable_ip_restriction: false,
  ip_whitelist: '',
  ip_blacklist: '',
  // Quota settings (empty = unlimited)
  enable_quota: false,
  quota: null as number | null,
  // Rate limit settings
  enable_rate_limit: false,
  rate_limit_5h: null as number | null,
  rate_limit_1d: null as number | null,
  rate_limit_7d: null as number | null,
  enable_expiration: false,
  expiration_preset: '30' as '7' | '30' | '90' | 'custom',
  expiration_date: '',
  fallback_to_default_group_when_unavailable: true
})

type ModelMappingRowError = { source?: string; target?: string }

// 前端与后端共享相同的大小写敏感、单尾通配符和长度约束。
const modelMappingRowErrors = computed<Record<number, ModelMappingRowError>>(() => {
  const errors: Record<number, ModelMappingRowError> = {}
  const sourceCounts = new Map<string, number>()
  for (const row of formData.value.model_mapping_rows) {
    const source = row.source.trim()
    if (source) sourceCounts.set(source, (sourceCounts.get(source) ?? 0) + 1)
  }

  for (const row of formData.value.model_mapping_rows) {
    const source = row.source.trim()
    const target = row.target.trim()
    const rowError: ModelMappingRowError = {}
    if (!source) {
      rowError.source = t('keys.modelRedirect.sourceRequired')
    } else if ([...source].length > 100) {
      rowError.source = t('keys.modelRedirect.nameTooLong')
    } else {
      const wildcardCount = (source.match(/\*/g) ?? []).length
      if (wildcardCount > 1 || (wildcardCount === 1 && !source.endsWith('*'))) {
        rowError.source = t('keys.modelRedirect.sourceWildcardInvalid')
      } else if ((sourceCounts.get(source) ?? 0) > 1) {
        rowError.source = t('keys.modelRedirect.duplicateSource')
      }
    }

    if (!target) {
      rowError.target = t('keys.modelRedirect.targetRequired')
    } else if ([...target].length > 100) {
      rowError.target = t('keys.modelRedirect.nameTooLong')
    } else if (target.includes('*')) {
      rowError.target = t('keys.modelRedirect.targetWildcardInvalid')
    } else if (source && source === target) {
      rowError.target = t('keys.modelRedirect.selfMapping')
    }
    if (rowError.source || rowError.target) errors[row.local_id] = rowError
  }
  return errors
})

const modelMappingFormError = computed(() => {
  if (formData.value.model_mapping_rows.length > 100) {
    return t('keys.modelRedirect.tooManyRules')
  }
  for (const row of formData.value.model_mapping_rows) {
    const error = modelMappingRowErrors.value[row.local_id]
    if (error?.source) return error.source
    if (error?.target) return error.target
  }
  return ''
})

const addModelMappingRow = () => {
  if (formData.value.model_mapping_rows.length >= 100) return
  formData.value.model_mapping_rows.push(newModelMappingRow())
}

const removeModelMappingRow = (index: number) => {
  formData.value.model_mapping_rows.splice(index, 1)
}

const buildModelMappingPayload = (): Record<string, string> =>
  Object.fromEntries(
    formData.value.model_mapping_rows.map((row) => [row.source.trim(), row.target.trim()])
  )

// 自定义Key验证
const customKeyError = computed(() => {
  if (!formData.value.use_custom_key || !formData.value.custom_key) {
    return ''
  }
  const key = formData.value.custom_key
  if (key.length < 16) {
    return t('keys.customKeyTooShort')
  }
  // 检查字符：只允许字母、数字、下划线、连字符
  if (!/^[a-zA-Z0-9_-]+$/.test(key)) {
    return t('keys.customKeyInvalidChars')
  }
  return ''
})

// 单 Key Fast 策略选项；Select 组件负责键盘与弹层交互。
const fastModePolicyOptions = computed(() => [
  { value: 'follow_request', label: t('keys.fastModePolicy.followRequest') },
  { value: 'force_on', label: t('keys.fastModePolicy.forceOn') },
  { value: 'force_off', label: t('keys.fastModePolicy.forceOff') }
])

// 结算模式由服务端最终校验；这里保持存量 Key 的自动模式可见且可恢复。
const billingModeOptions = computed(() => [
  { value: 'auto', label: t('keys.billing.modes.auto') },
  { value: 'subscription', label: t('keys.billing.modes.subscription') },
  { value: 'balance', label: t('keys.billing.modes.balance') }
])

const billingSubscriptionOptions = computed<Array<{
  value: number
  label: string
  description?: string
  disabled?: boolean
}>>(() => {
  const options: Array<{
    value: number
    label: string
    description?: string
    disabled?: boolean
  }> = billingSubscriptions.value.map((subscription) => ({
    value: subscription.id,
    label: `${subscription.plan_name} #${subscription.id}`,
    description: subscription.groups_restricted
      ? t('keys.billing.groupsRestricted')
      : t('keys.billing.allGroups')
  }))
  const selectedID = formData.value.preferred_subscription_id
  if (selectedID && !options.some((option) => option.value === selectedID)) {
    options.unshift({
      value: selectedID,
      label: t('keys.billing.unavailableSubscription', { id: selectedID }),
      disabled: true
    })
  }
  return options
})

const shouldSubmitEditStatus = (key: ApiKey, status: 'active' | 'inactive') => {
  // Owner 锁定期间仍允许编辑其它字段，但不能通过普通更新接口触碰状态。
  if (key.team_owner_disabled) return false
  if (key.status === 'quota_exhausted' || key.status === 'expired') {
    return status === 'active'
  }
  return true
}

// 筛选下拉选项。
const groupFilterOptions = computed(() => [
  { value: '', label: t('keys.allGroups') },
  { value: 0, label: t('keys.noGroup') },
  ...groups.value.map((g) => ({ value: g.id, label: g.name }))
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('keys.allStatus') },
  { value: 'active', label: t('keys.status.active') },
  { value: 'inactive', label: t('keys.status.inactive') },
  { value: 'disabled', label: t('keys.status.disabled') },
  { value: 'quota_exhausted', label: t('keys.status.quota_exhausted') },
  { value: 'expired', label: t('keys.status.expired') }
])

const activeFilterCount = computed(() => [filterGroupId.value !== '', filterStatus.value !== ''].filter(Boolean).length)

const resetKeyFilters = () => {
  filterGroupId.value = ''
  filterStatus.value = ''
  pagination.value.page = 1
  showFilterDropdown.value = false
  loadApiKeys()
}

const handleFilterClickOutside = (event: MouseEvent) => {
  const target = event.target
  if (target instanceof Node && filterDropdownRef.value?.contains(target)) return
  if (target instanceof Element && target.closest('.select-dropdown-portal')) return
  showFilterDropdown.value = false
}

const onFilterChange = () => {
  pagination.value.page = 1
  loadApiKeys()
}

const onGroupFilterChange = (value: string | number | boolean | null) => {
  filterGroupId.value = value as string | number
  onFilterChange()
}

const onStatusFilterChange = (value: string | number | boolean | null) => {
  filterStatus.value = value as string
  onFilterChange()
}

// 用户侧分组选项只投影选择所需信息，不传递管理员使用的容量数据。
const buildGroupOptions = (source: Group[]) =>
  source.map((group) => ({
    value: group.id,
    label: group.name,
    description: group.description,
    displayBrand: group.display_brand?.trim() || null,
    rate: group.rate_multiplier,
    userRate: userGroupRates.value[group.id] ?? null,
    peakRateEnabled: group.peak_rate_enabled,
    peakStart: group.peak_start,
    peakEnd: group.peak_end,
    peakRateMultiplier: group.peak_rate_multiplier,
    platform: group.platform,
    dataSharingEnabled: group.data_sharing_enabled
  }))

// 指定订阅时仅使用服务端返回的权限与套餐分组交集。
const formGroupOptions = computed(() => buildGroupOptions(formGroups.value))
const allGroupOptions = computed(() => buildGroupOptions(groups.value))

// 切换复合模式时保留普通 Key 的原分组，前缀仍要求用户明确填写。
const onCompositeModeChange = (enabled: boolean) => {
  if (enabled) {
    if (formData.value.composite_groups.length === 0) {
      formData.value.composite_groups = [newCompositeBinding(formData.value.group_id)]
    }
    formData.value.is_composite = true
    return
  }
  formData.value.is_composite = false
  if (selectedKey.value?.is_composite) {
    formData.value.group_id = null
  } else if (formData.value.group_id === null) {
    formData.value.group_id = formData.value.composite_groups[0]?.group_id ?? null
  }
}

const addCompositeBinding = () => {
  if (formData.value.composite_groups.length >= 20) return
  formData.value.composite_groups.push(newCompositeBinding())
}

const removeCompositeBinding = (index: number) => {
  if (formData.value.composite_groups.length <= 1) return
  formData.value.composite_groups.splice(index, 1)
}

const moveCompositeBinding = (index: number, offset: -1 | 1) => {
  const target = index + offset
  if (target < 0 || target >= formData.value.composite_groups.length) return
  const [binding] = formData.value.composite_groups.splice(index, 1)
  if (binding) formData.value.composite_groups.splice(target, 0, binding)
}

// compositeBindingError 即时检查前缀和分组的行内错误。
const compositeBindingError = (index: number) => {
  const binding = formData.value.composite_groups[index]
  if (!binding?.group_id) return t('keys.composite.groupRequired')
  const prefix = binding.prefix.trim()
  if (!prefix) return t('keys.composite.prefixRequired')
  if (!/^[A-Za-z0-9_-]{1,32}$/.test(prefix)) return t('keys.composite.prefixInvalid')
  const duplicatePrefix = formData.value.composite_groups.some(
    (item, itemIndex) => itemIndex !== index && item.prefix.trim().toLowerCase() === prefix.toLowerCase()
  )
  if (duplicatePrefix) return t('keys.composite.prefixDuplicate')
  const duplicateGroup = formData.value.composite_groups.some(
    (item, itemIndex) => itemIndex !== index && item.group_id === binding.group_id
  )
  if (duplicateGroup) return t('keys.composite.groupDuplicate')
  return ''
}

const compositeFormError = computed(() => {
  if (!formData.value.is_composite) return ''
  if (formData.value.composite_groups.length === 0) return t('keys.composite.mappingRequired')
  if (formData.value.composite_groups.length > 20) return t('keys.composite.tooManyMappings')
  for (let index = 0; index < formData.value.composite_groups.length; index++) {
    const error = compositeBindingError(index)
    if (error) return error
  }
  return ''
})

// 分组下拉搜索。
const groupSearchQuery = ref('')
const filteredGroupOptions = computed(() => {
  const query = groupSearchQuery.value.trim().toLowerCase()
  if (!query) return allGroupOptions.value
  return allGroupOptions.value.filter((opt) => {
    return opt.label.toLowerCase().includes(query) ||
      (opt.displayBrand && opt.displayBrand.toLowerCase().includes(query)) ||
      (opt.description && opt.description.toLowerCase().includes(query))
  })
})

// 指定订阅切换后，普通 Key 与复合 Key 都不能继续保留套餐未覆盖的分组。
const pruneFormGroupBindings = (allowedGroupIDs: Set<number>) => {
  if (formData.value.group_id !== null && !allowedGroupIDs.has(formData.value.group_id)) {
    formData.value.group_id = null
  }
  formData.value.composite_groups = formData.value.composite_groups.filter((binding) =>
    binding.group_id !== null && allowedGroupIDs.has(binding.group_id)
  )
}

const onBillingModeChange = (value: string | number | boolean | null) => {
  const mode: ApiKeyBillingMode = value === 'subscription' || value === 'balance' ? value : 'auto'
  formData.value.billing_mode = mode
  if (mode !== 'subscription') {
    formData.value.preferred_subscription_id = null
    void loadFormGroups()
    return
  }

  // 选定订阅后再裁剪不兼容分组，避免丢失仍可用的现有映射。
  formData.value.preferred_subscription_id = null
  formGroups.value = []
}

const onPreferredSubscriptionChange = (value: string | number | boolean | null) => {
  const subscriptionID = typeof value === 'number' && value > 0 ? value : null
  formData.value.preferred_subscription_id = subscriptionID
  const subscription = billingSubscriptions.value.find((item) => item.id === subscriptionID)
  if (subscription?.groups_restricted) {
    pruneFormGroupBindings(new Set(subscription.applicable_groups))
  }
  void loadFormGroups()
}

const maskKey = (key: string): string => {
  if (key.length <= 12) return key
  return `${key.slice(0, 8)}...${key.slice(-4)}`
}

const copyToClipboard = async (text: string, keyId: number) => {
  const success = await clipboardCopy(text, t('keys.copied'))
  if (success) {
    copiedKeyId.value = keyId
    setTimeout(() => {
      copiedKeyId.value = null
    }, 800)
  }
}

const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const { name, code } = error as { name?: string; code?: string }
  return name === 'AbortError' || code === 'ERR_CANCELED'
}

const loadApiKeys = async () => {
  abortController?.abort()
  const controller = new AbortController()
  abortController = controller
  const { signal } = controller
  loading.value = true
  // 新一轮列表请求立即清理上一页用量状态，取消竞态不会留下过期单元格。
  usageLoading.value = false
  usageStats.value = {}
  try {
    // 构建筛选条件。
    const filters: {
      search?: string
      status?: string
      group_id?: number | string
      sort_by?: string
      sort_order?: 'asc' | 'desc'
      scope?: DataScope
    } = {}
    if (filterSearch.value) filters.search = filterSearch.value
    if (filterStatus.value) filters.status = filterStatus.value
    if (filterGroupId.value !== '') filters.group_id = filterGroupId.value
    filters.sort_by = sortState.value.sort_by
    filters.sort_order = sortState.value.sort_order
    filters.scope = scope.value

    const response = await keysAPI.list(pagination.value.page, pagination.value.page_size, filters, {
      signal
    })
    if (signal.aborted) return
    apiKeys.value = response.items
    pagination.value.total = response.total
    pagination.value.pages = response.pages
    // Key 列表先解除加载态，用量单元格在后台独立填充。
    loading.value = false
    void loadUsageStats(response.items, controller)
  } catch (error) {
    if (isAbortError(error)) {
      return
    }
    appStore.showError(t('keys.failedToLoad'))
  } finally {
    if (abortController === controller) {
      loading.value = false
    }
  }
}

const loadUsageStats = async (items: ApiKey[], controller: AbortController) => {
  usageStats.value = {}
  if (items.length === 0) {
    usageLoading.value = false
    return
  }
  usageLoading.value = true
  try {
    const keyIds = items.map((key) => key.id)
    const usageResponse = await usageAPI.getDashboardApiKeysUsage(keyIds, { signal: controller.signal })
    if (controller.signal.aborted || abortController !== controller) return
    usageStats.value = usageResponse.stats
  } catch (error) {
    if (!isAbortError(error)) {
      console.error('Failed to load usage stats:', error)
    }
  } finally {
    if (abortController === controller) {
      usageLoading.value = false
    }
  }
}

const loadGroups = async () => {
  try {
    const available = await userGroupsAPI.getAvailable(scope.value)
    groups.value = available
    // 自动与余额模式沿用用户原有分组，避免表单打开时的重复请求清空已有选择。
    if (formData.value.billing_mode !== 'subscription') {
      formGroups.value = available
    }
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

// 指定订阅时由服务端返回付款主体权限与套餐分组的交集，表格筛选不受影响。
const loadFormGroups = async () => {
  const requestID = ++formGroupsRequestID
  if (formData.value.billing_mode !== 'subscription') {
    formGroups.value = groups.value
    formGroupsLoading.value = false
    return
  }

  const subscriptionID = formData.value.preferred_subscription_id
  if (!subscriptionID) {
    formGroups.value = []
    formGroupsLoading.value = false
    return
  }

  formGroupsLoading.value = true
  try {
    const available = await userGroupsAPI.getAvailable(scope.value, subscriptionID)
    if (requestID !== formGroupsRequestID) return
    formGroups.value = available
    pruneFormGroupBindings(new Set(available.map((group) => group.id)))
  } catch (error) {
    if (requestID === formGroupsRequestID) {
      formGroups.value = []
    }
    console.error('Failed to load API key form groups:', error)
  } finally {
    if (requestID === formGroupsRequestID) {
      formGroupsLoading.value = false
    }
  }
}

const loadBillingOptions = async () => {
  billingOptionsLoading.value = true
  try {
    billingSubscriptions.value = await keysAPI.getBillingOptions(scope.value)
  } catch (error) {
    billingSubscriptions.value = []
    console.error('Failed to load API key billing options:', error)
  } finally {
    billingOptionsLoading.value = false
  }
}

const loadUserGroupRates = async () => {
  try {
    userGroupRates.value = await userGroupsAPI.getUserGroupRates(scope.value)
  } catch (error) {
    console.error('Failed to load user group rates:', error)
  }
}

const loadPublicSettings = async () => {
  try {
    publicSettings.value = await authAPI.getPublicSettings()
    if (!teamFeatureEnabled.value && scope.value === 'team') scope.value = 'personal'
  } catch (error) {
    console.error('Failed to load public settings:', error)
  }
}

const openUseKeyModal = (key: ApiKey) => {
  selectedKey.value = key
  showUseKeyModal.value = true
}

const closeUseKeyModal = () => {
  showUseKeyModal.value = false
  selectedKey.value = null
}

const openTfCliImportDialog = (key: ApiKey) => {
  tfImportKey.value = key
  showTfCliImportDialog.value = true
}

const closeTfCliImportDialog = () => {
  showTfCliImportDialog.value = false
  tfImportKey.value = null
}

const handlePageChange = (page: number) => {
  pagination.value.page = page
  loadApiKeys()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.value.page_size = pageSize
  pagination.value.page = 1
  loadApiKeys()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.value.sort_by = key
  sortState.value.sort_order = order
  pagination.value.page = 1
  loadApiKeys()
}

const openCreateModal = () => {
  showCreateModal.value = true
  void Promise.all([loadBillingOptions(), loadFormGroups()])
}

const editKey = (key: ApiKey) => {
  selectedKey.value = key
  const hasIPRestriction = (key.ip_whitelist?.length > 0) || (key.ip_blacklist?.length > 0)
  const hasExpiration = !!key.expires_at
  formData.value = {
    name: key.name,
    group_id: key.group_id,
    is_composite: key.is_composite ?? false,
    composite_groups: (key.composite_groups || []).map((binding) =>
      newCompositeBinding(binding.group_id, binding.prefix)
    ),
    // 后端的终态统一映射为不可用，编辑表单只提交 active/inactive。
    status: key.status === 'active' ? 'active' : 'inactive',
    fast_mode_policy: key.fast_mode_policy ?? 'follow_request',
    billing_mode: key.billing_mode ?? 'auto',
    preferred_subscription_id: key.preferred_subscription_id ?? null,
    model_mapping_rows: Object.entries(key.model_mapping ?? {})
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([source, target]) => newModelMappingRow(source, target)),
    use_custom_key: false,
    custom_key: '',
    enable_ip_restriction: hasIPRestriction,
    ip_whitelist: (key.ip_whitelist || []).join('\n'),
    ip_blacklist: (key.ip_blacklist || []).join('\n'),
    enable_quota: key.quota > 0,
    quota: key.quota > 0 ? key.quota : null,
    enable_rate_limit: (key.rate_limit_5h > 0) || (key.rate_limit_1d > 0) || (key.rate_limit_7d > 0),
    rate_limit_5h: key.rate_limit_5h || null,
    rate_limit_1d: key.rate_limit_1d || null,
    rate_limit_7d: key.rate_limit_7d || null,
    enable_expiration: hasExpiration,
    expiration_preset: 'custom',
    expiration_date: key.expires_at ? formatDateTimeLocal(key.expires_at) : '',
    fallback_to_default_group_when_unavailable: key.fallback_to_default_group_when_unavailable ?? false
  }
  formGroups.value = []
  showEditModal.value = true
  void Promise.all([loadBillingOptions(), loadFormGroups()])
}

// 状态开关只维护编辑表单的 active/inactive 两种可提交值。
const onStatusToggle = (enabled: boolean) => {
  if (selectedKey.value?.team_owner_disabled) return
  formData.value.status = enabled ? 'active' : 'inactive'
}

const toggleKeyStatus = async (key: ApiKey) => {
  // 模板已经隐藏恢复按钮，这里保留防御检查避免其它调用路径误触发请求。
  if (key.team_owner_disabled) {
    appStore.showError(t('keys.teamOwnerDisabledHint'))
    return
  }
  const newStatus = key.status === 'active' ? 'inactive' : 'active'
  try {
    await keysAPI.toggleStatus(key.id, newStatus)
    appStore.showSuccess(
      newStatus === 'active' ? t('keys.keyEnabledSuccess') : t('keys.keyDisabledSuccess')
    )
    loadApiKeys()
  } catch (error) {
    appStore.showError(t('keys.failedToUpdateStatus'))
  }
}

// 更多菜单使用视口坐标并传送到 body，避免被固定操作列的 overflow 裁切。
const openKeyActionMenu = (key: ApiKey, event: MouseEvent) => {
  if (actionMenuKey.value?.id === key.id) {
    closeKeyActionMenu()
    return
  }
  const target = event.currentTarget as HTMLElement | null
  if (!target) return
  const rect = target.getBoundingClientRect()
  const width = 192
  const height = publicSettings.value?.hide_ccs_import_button ? 138 : 178
  const padding = 8
  const left = Math.max(padding, Math.min(rect.right - width, window.innerWidth - width - padding))
  let top = rect.bottom + 4
  if (top + height > window.innerHeight - padding) top = Math.max(padding, rect.top - height - 4)
  actionMenuKey.value = key
  actionMenuPosition.value = { top, left }
}

const closeKeyActionMenu = () => {
  actionMenuKey.value = null
  actionMenuPosition.value = null
}

const openGroupSelector = (key: ApiKey) => {
	if (key.is_composite || key.billing_mode === 'subscription') {
		editKey(key)
		return
	}
  if (groupSelectorKeyId.value === key.id) {
    groupSelectorKeyId.value = null
    dropdownPosition.value = null
  } else {
    const buttonEl = groupButtonRefs.value.get(key.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()
      const dropdownEstHeight = 400 // 预估下拉框最大高度
      const dropdownEstWidth = Math.min(380, window.innerWidth - 16)
      const spaceBelow = window.innerHeight - rect.bottom
      const spaceAbove = rect.top
      // 夹取 left，避免窄屏下浮层超出视口右缘
      const left = Math.max(8, Math.min(rect.left, window.innerWidth - dropdownEstWidth - 8))

      if (spaceBelow < dropdownEstHeight && spaceAbove > spaceBelow) {
        // 下方空间不足时向上弹出。
        dropdownPosition.value = {
          bottom: window.innerHeight - rect.top + 4,
          left
        }
      } else {
        // 默认向下弹出。
        dropdownPosition.value = {
          top: rect.bottom + 4,
          left
        }
      }
    }
    groupSelectorKeyId.value = key.id
    groupSearchQuery.value = ''
  }
}

const getGroupById = (groupId: number | null) => {
  if (!groupId) return null
  return groups.value.find(group => group.id === groupId) || null
}

const groupRequiresDataSharingNotice = (groupId: number | null) => {
  return getGroupById(groupId)?.data_sharing_enabled === true
}

const clearDataSharingCountdown = () => {
  if (dataSharingCountdownTimer) {
    window.clearInterval(dataSharingCountdownTimer)
    dataSharingCountdownTimer = null
  }
}

const startDataSharingCountdown = () => {
  clearDataSharingCountdown()
  dataSharingNoticeDialog.value.countdown = 10
  dataSharingCountdownTimer = window.setInterval(() => {
    dataSharingNoticeDialog.value.countdown = Math.max(0, dataSharingNoticeDialog.value.countdown - 1)
    if (dataSharingNoticeDialog.value.countdown <= 0) {
      clearDataSharingCountdown()
    }
  }, 1000)
}

const closeDataSharingNotice = () => {
  clearDataSharingCountdown()
  dataSharingNoticeDialog.value = {
    show: false,
    loading: false,
    countdown: 0,
    notice: null,
    key: null,
    targetGroupId: null,
    mode: null
  }
}

const openDataSharingNotice = async (
  targetGroupId: number,
  mode: 'row' | 'form',
  key: ApiKey | null = null
) => {
  dataSharingNoticeDialog.value = {
    show: true,
    loading: true,
    countdown: 10,
    notice: null,
    key,
    targetGroupId,
    mode
  }
  startDataSharingCountdown()
  try {
    const notice = await dataSharingAPI.getNotice(targetGroupId)
    dataSharingNoticeDialog.value.notice = notice
  } catch (error) {
    closeDataSharingNotice()
    appStore.showError('加载数据共享须知失败')
  } finally {
    dataSharingNoticeDialog.value.loading = false
  }
}

const confirmDataSharingNotice = async () => {
  const state = dataSharingNoticeDialog.value
  if (!state.notice || !state.targetGroupId || state.countdown > 0) return

  state.loading = true
  try {
    await dataSharingAPI.confirmNotice(state.targetGroupId, state.notice.version)
    if (state.mode === 'row' && state.key) {
      await submitGroupChange(state.key, state.targetGroupId, {
        data_sharing_confirmed: true,
        data_sharing_notice_version: state.notice.version
      })
    } else if (state.mode === 'form') {
      await submitKeyForm({
        data_sharing_confirmed: true,
        data_sharing_notice_version: state.notice.version
      })
    }
    closeDataSharingNotice()
  } catch (error: any) {
    const errorMsg = error?.message || '确认数据共享须知失败'
    appStore.showError(errorMsg)
  } finally {
    state.loading = false
  }
}

const submitGroupChange = async (
  key: ApiKey,
  newGroupId: number | null,
  consent?: { data_sharing_confirmed: boolean; data_sharing_notice_version: number }
) => {
  await keysAPI.update(key.id, { group_id: newGroupId, ...consent })
  appStore.showSuccess(t('keys.groupChangedSuccess'))
  loadApiKeys()
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
  if (key.group_id === newGroupId) return
  if (groupRequiresDataSharingNotice(newGroupId)) {
    await openDataSharingNotice(newGroupId!, 'row', key)
    return
  }

  try {
    await submitGroupChange(key, newGroupId)
  } catch (error) {
    appStore.showError(t('keys.failedToChangeGroup'))
  }
}

const closeGroupSelector = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  // 判断点击是否发生在下拉框或触发按钮内，同时关闭列设置菜单。
  if (!target.closest('.group\\/dropdown') && !dropdownRef.value?.contains(target)) {
    groupSelectorKeyId.value = null
    dropdownPosition.value = null
  }
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
}

const confirmDelete = (key: ApiKey) => {
  selectedKey.value = key
  showDeleteDialog.value = true
}

const buildKeyFormPayload = () => {
  // 仅在启用 IP 限制时解析名单。
  const parseIPList = (text: string): string[] =>
    text.split('\n').map(ip => ip.trim()).filter(ip => ip.length > 0)
  const ipWhitelist = formData.value.enable_ip_restriction ? parseIPList(formData.value.ip_whitelist) : []
  const ipBlacklist = formData.value.enable_ip_restriction ? parseIPList(formData.value.ip_blacklist) : []

  // 计算额度值，空值和 0 都按不限额处理。
  const quota = formData.value.quota && formData.value.quota > 0 ? formData.value.quota : 0

  // 计算过期时间。
  let expiresInDays: number | undefined
  let expiresAt: string | null | undefined
  if (formData.value.enable_expiration && formData.value.expiration_date) {
    if (!showEditModal.value) {
      // 创建模式：按选择日期换算剩余天数。
      const expDate = new Date(formData.value.expiration_date)
      const now = new Date()
      const diffDays = Math.ceil((expDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24))
      expiresInDays = diffDays > 0 ? diffDays : 1
    } else {
      // 编辑模式：直接提交自定义日期。
      expiresAt = new Date(formData.value.expiration_date).toISOString()
    }
  } else if (showEditModal.value) {
    // 编辑模式：关闭过期或清空日期时发送空字符串以清除。
    expiresAt = ''
  }

  // 计算限速值，关闭开关时提交 0。
  const rateLimitData = formData.value.enable_rate_limit ? {
    rate_limit_5h: formData.value.rate_limit_5h && formData.value.rate_limit_5h > 0 ? formData.value.rate_limit_5h : 0,
    rate_limit_1d: formData.value.rate_limit_1d && formData.value.rate_limit_1d > 0 ? formData.value.rate_limit_1d : 0,
    rate_limit_7d: formData.value.rate_limit_7d && formData.value.rate_limit_7d > 0 ? formData.value.rate_limit_7d : 0,
  } : { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }

  return {
    ipWhitelist,
    ipBlacklist,
    quota,
    expiresInDays,
    expiresAt,
    rateLimitData,
    modelMapping: buildModelMappingPayload()
  }
}

const submitKeyForm = async (
  consent?: { data_sharing_confirmed: boolean; data_sharing_notice_version: number }
) => {
  const { ipWhitelist, ipBlacklist, quota, expiresInDays, expiresAt, rateLimitData, modelMapping } = buildKeyFormPayload()
  submitting.value = true
  try {
    if (showEditModal.value && selectedKey.value) {
      const updates: UpdateApiKeyRequest = {
        name: formData.value.name,
        group_id: formData.value.is_composite ? undefined : formData.value.group_id,
        is_composite: formData.value.is_composite,
        composite_groups: formData.value.is_composite
          ? formData.value.composite_groups.map((binding) => ({
              group_id: binding.group_id!,
              prefix: binding.prefix.trim()
            }))
          : undefined,
        fast_mode_policy: formData.value.fast_mode_policy,
        model_mapping: modelMapping,
        ip_whitelist: ipWhitelist,
        ip_blacklist: ipBlacklist,
        quota: quota,
        expires_at: expiresAt,
        rate_limit_5h: rateLimitData.rate_limit_5h,
        rate_limit_1d: rateLimitData.rate_limit_1d,
        rate_limit_7d: rateLimitData.rate_limit_7d,
        fallback_to_default_group_when_unavailable: formData.value.fallback_to_default_group_when_unavailable,
        ...consent
      }
      const originalBillingMode = selectedKey.value.billing_mode ?? 'auto'
      const originalPreferredSubscriptionID = selectedKey.value.preferred_subscription_id ?? null
      if (
        formData.value.billing_mode !== originalBillingMode ||
        formData.value.preferred_subscription_id !== originalPreferredSubscriptionID
      ) {
        updates.billing_mode = formData.value.billing_mode
        updates.preferred_subscription_id = formData.value.billing_mode === 'subscription'
          ? formData.value.preferred_subscription_id
          : null
      }
      if (shouldSubmitEditStatus(selectedKey.value, formData.value.status)) {
        updates.status = formData.value.status
      }
      await keysAPI.update(selectedKey.value.id, updates)
      appStore.showSuccess(t('keys.keyUpdatedSuccess'))
    } else {
      const customKey = formData.value.use_custom_key ? formData.value.custom_key : undefined
      const payload: CreateApiKeyRequest = {
        name: formData.value.name,
        scope: scope.value,
        group_id: formData.value.is_composite ? undefined : formData.value.group_id,
        is_composite: formData.value.is_composite,
        composite_groups: formData.value.is_composite
          ? formData.value.composite_groups.map((binding) => ({
              group_id: binding.group_id!,
              prefix: binding.prefix.trim()
            }))
          : undefined,
        fast_mode_policy: formData.value.fast_mode_policy,
        billing_mode: formData.value.billing_mode,
        preferred_subscription_id: formData.value.billing_mode === 'subscription'
          ? formData.value.preferred_subscription_id
          : null,
        model_mapping: modelMapping,
        custom_key: customKey,
        ip_whitelist: ipWhitelist,
        ip_blacklist: ipBlacklist,
        quota,
        expires_in_days: expiresInDays,
        rate_limit_5h: rateLimitData.rate_limit_5h,
        rate_limit_1d: rateLimitData.rate_limit_1d,
        rate_limit_7d: rateLimitData.rate_limit_7d,
        fallback_to_default_group_when_unavailable: formData.value.fallback_to_default_group_when_unavailable,
        ...consent
      }
      await keysAPI.createWithPayload(payload)
      appStore.showSuccess(t('keys.keyCreatedSuccess'))
      // 仅在引导进行到提交步骤且创建成功后推进引导。
      if (onboardingStore.isCurrentStep('[data-tour="key-form-submit"]')) {
        onboardingStore.nextStep(500)
      }
    }
    closeModals()
    loadApiKeys()
  } catch (error: any) {
    const errorMsg = error?.reason === 'API_KEY_LIMIT_REACHED'
      ? t('keys.apiKeyLimitReached', {
          current: error?.metadata?.current ?? '?',
          limit: error?.metadata?.limit ?? '?'
        })
      : error?.message || t('keys.failedToSave')
    appStore.showError(errorMsg)
    // 创建失败时不推进引导。
  } finally {
    submitting.value = false
  }
}

// 切换作用域后清空分页与筛选缓存，并只重新加载当前内容区域的数据。
const onScopeChange = () => {
  pagination.value.page = 1
  filterGroupId.value = ''
  void Promise.all([loadApiKeys(), loadGroups(), loadUserGroupRates(), loadBillingOptions(), loadFormGroups()])
}

const handleSubmit = async () => {
	if (modelMappingFormError.value) {
		appStore.showError(modelMappingFormError.value)
		return
	}

	if (formData.value.is_composite && compositeFormError.value) {
		appStore.showError(compositeFormError.value)
		return
	}

  if (formData.value.billing_mode === 'subscription' && !formData.value.preferred_subscription_id) {
    appStore.showError(t('keys.billing.subscriptionRequired'))
    return
  }

  // 普通模式必须显式选择单个分组，复合转普通时同样不沿用隐式值。
  if (!formData.value.is_composite && formData.value.group_id === null) {
    appStore.showError(t('keys.groupRequired'))
    return
  }

  // 启用自定义 Key 时校验输入。
  if (!showEditModal.value && formData.value.use_custom_key) {
    if (!formData.value.custom_key) {
      appStore.showError(t('keys.customKeyRequired'))
      return
    }
    if (customKeyError.value) {
      appStore.showError(customKeyError.value)
      return
    }
  }

	const existingCompositeGroups = new Set(
		selectedKey.value?.is_composite
			? (selectedKey.value.composite_groups || []).map((binding) => binding.group_id)
			: []
	)
	const newDataSharingGroup = formData.value.is_composite
		? formData.value.composite_groups.find(
			(binding) => !existingCompositeGroups.has(binding.group_id!) && groupRequiresDataSharingNotice(binding.group_id)
		)
		: null
	const changingGroup = !showEditModal.value || selectedKey.value?.group_id !== formData.value.group_id
  if (newDataSharingGroup?.group_id) {
		await openDataSharingNotice(newDataSharingGroup.group_id, 'form', selectedKey.value)
		return
	}
	if (!formData.value.is_composite && changingGroup && groupRequiresDataSharingNotice(formData.value.group_id)) {
		await openDataSharingNotice(formData.value.group_id!, 'form', selectedKey.value)
    return
  }

  await submitKeyForm()
}

/**
 * 处理删除 API Key 的操作
 * 优化：错误处理改进，优先显示后端返回的具体错误消息（如权限不足等），
 * 若后端未返回消息则显示默认的国际化文本
 */
const handleDelete = async () => {
  if (!selectedKey.value) return

  try {
    await keysAPI.delete(selectedKey.value.id)
    appStore.showSuccess(t('keys.keyDeletedSuccess'))
    showDeleteDialog.value = false
    loadApiKeys()
  } catch (error: any) {
    // 优先使用后端返回的错误消息，提供更具体的错误信息给用户
    const errorMsg = error?.message || t('keys.failedToDelete')
    appStore.showError(errorMsg)
  }
}

const closeModals = () => {
  showCreateModal.value = false
  showEditModal.value = false
  selectedKey.value = null
  formGroupsRequestID++
  formGroups.value = []
  formData.value = {
    name: '',
    group_id: null,
    is_composite: false,
    composite_groups: [],
    status: 'active',
    fast_mode_policy: 'follow_request',
    billing_mode: 'auto',
    preferred_subscription_id: null,
    model_mapping_rows: [],
    use_custom_key: false,
    custom_key: '',
    enable_ip_restriction: false,
    ip_whitelist: '',
    ip_blacklist: '',
    enable_quota: false,
    quota: null,
    enable_rate_limit: false,
    rate_limit_5h: null,
    rate_limit_1d: null,
    rate_limit_7d: null,
    enable_expiration: false,
    expiration_preset: '30',
    expiration_date: '',
    fallback_to_default_group_when_unavailable: true
  }
}

// 展示重置额度确认弹窗。
const confirmResetQuota = () => {
  showResetQuotaDialog.value = true
}

// 根据快捷天数设置过期日期。
const setExpirationDays = (days: number) => {
  formData.value.expiration_preset = days.toString() as '7' | '30' | '90'
  const expDate = new Date()
  expDate.setDate(expDate.getDate() + days)
  formData.value.expiration_date = formatDateTimeLocal(expDate.toISOString())
}

// 重置 API Key 已用额度。
const resetQuotaUsed = async () => {
  if (!selectedKey.value) return
  showResetQuotaDialog.value = false
  try {
    await keysAPI.update(selectedKey.value.id, { reset_quota: true })
    appStore.showSuccess(t('keys.quotaResetSuccess'))
    // 同步更新本地状态。
    if (selectedKey.value) {
      selectedKey.value.quota_used = 0
    }
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToResetQuota')
    appStore.showError(errorMsg)
  }
}

// 从编辑弹窗展示重置限速确认弹窗。
const confirmResetRateLimit = () => {
  showResetRateLimitDialog.value = true
}

// 从表格行展示重置限速确认弹窗。
const confirmResetRateLimitFromTable = (row: ApiKey) => {
  selectedKey.value = row
  showResetRateLimitDialog.value = true
}

// 重置 API Key 限速用量。
const resetRateLimitUsage = async () => {
  if (!selectedKey.value) return
  showResetRateLimitDialog.value = false
  try {
    await keysAPI.update(selectedKey.value.id, { reset_rate_limit_usage: true })
    appStore.showSuccess(t('keys.rateLimitResetSuccess'))
    // 刷新 Key 数据。
    await loadApiKeys()
    // 用刷新后的数据更新当前编辑对象。
    const refreshedKey = apiKeys.value.find(k => k.id === selectedKey.value!.id)
    if (refreshedKey) {
      selectedKey.value = refreshedKey
    }
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToResetRateLimit')
    appStore.showError(errorMsg)
  }
}

const importToCcswitch = (row: ApiKey) => {
  const platform = row.group?.platform || 'anthropic'

  // Antigravity 平台需要先选择客户端。
  if (platform === 'antigravity') {
    pendingCcsRow.value = row
    showCcsClientSelect.value = true
    return
  }

  // 其他平台直接执行导入。
  executeCcsImport(row, platform === 'gemini' ? 'gemini' : 'claude')
}

const executeCcsImport = (row: ApiKey, clientType: CcSwitchClientType) => {
  const baseUrl = publicSettings.value?.api_base_url || window.location.origin
  const platform = row.group?.platform || 'anthropic'

  const usageScript = buildCcSwitchUsageScript(baseUrl, balanceUnitName.value)
  const providerName = (publicSettings.value?.site_name || 'sub2api').trim() || 'sub2api'
  const deeplink = buildCcSwitchImportDeeplink({
    baseUrl,
    platform,
    clientType,
    providerName,
    apiKey: row.key,
    usageScript
  })

  try {
    window.open(deeplink, '_self')

    // 通过窗口焦点粗略判断协议处理器是否拉起成功。
    setTimeout(() => {
      if (document.hasFocus()) {
        // 仍然聚焦通常说明协议处理器未成功拉起。
        appStore.showError(t('keys.ccSwitchNotInstalled'))
      }
    }, 100)
  } catch (error) {
    appStore.showError(t('keys.ccSwitchNotInstalled'))
  }
}

const handleCcsClientSelect = (clientType: CcSwitchClientType) => {
  if (pendingCcsRow.value) {
    executeCcsImport(pendingCcsRow.value, clientType)
  }
  showCcsClientSelect.value = false
  pendingCcsRow.value = null
}

const closeCcsClientSelect = () => {
  showCcsClientSelect.value = false
  pendingCcsRow.value = null
}

function formatResetTime(resetAt: string | null): string {
  if (!resetAt) return ''
  const diff = new Date(resetAt).getTime() - now.value.getTime()
  if (diff <= 0) return t('keys.resetNow')
  const days = Math.floor(diff / 86400000)
  const hours = Math.floor((diff % 86400000) / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

onMounted(async () => {
  loadSavedColumns()
  document.addEventListener('click', closeGroupSelector)
  document.addEventListener('click', handleFilterClickOutside)
  resetTimer = setInterval(() => { now.value = new Date() }, 60000)
  await loadPublicSettings()
  await Promise.all([loadApiKeys(), loadGroups(), loadUserGroupRates(), loadBillingOptions(), loadFormGroups()])
})

onUnmounted(() => {
  document.removeEventListener('click', closeGroupSelector)
  document.removeEventListener('click', handleFilterClickOutside)
  abortController?.abort()
  clearDataSharingCountdown()
  if (resetTimer) clearInterval(resetTimer)
})
</script>

<style scoped>
/* 创建密钥弹窗的单行控件统一为 36px，多行文本域保留自然高度。 */
.key-form-controls :deep(input.input),
.key-form-controls :deep(.select-trigger),
.key-form-controls :deep(.btn) {
  height: 2.25rem;
  min-height: 0;
}

.key-form-controls :deep(input.input),
.key-form-controls :deep(.btn) {
  padding-top: 0.375rem;
  padding-bottom: 0.375rem;
}

.key-form-controls :deep(.select-trigger) {
  padding-top: 0.375rem;
  padding-bottom: 0.375rem;
}
</style>
