<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-2">
          <div class="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between">
            <div class="flex min-w-0 flex-1 items-center gap-2">
              <SearchInput
                v-model="filterSearch"
                :placeholder="t('keys.searchPlaceholder')"
                class="min-w-0 flex-1 sm:w-56 sm:flex-none lg:w-48 xl:w-64"
                @search="onFilterChange"
              />
              <FilterDropdown
                :active-count="activeFilterCount"
                :columns="2"
                keep-mounted
                @reset="resetKeyFilters"
              >
                <FilterField :label="t('keys.group')">
                  <Select
                    :model-value="filterGroupId"
                    :options="groupFilterOptions"
                    @update:model-value="onGroupFilterChange"
                  />
                </FilterField>
                <FilterField :label="t('common.status')">
                  <Select
                    :model-value="filterStatus"
                    :options="statusFilterOptions"
                    @update:model-value="onStatusFilterChange"
                  />
                </FilterField>
              </FilterDropdown>
            </div>
            <div class="flex shrink-0 justify-end gap-2">
              <button
                @click="loadApiKeys"
                :disabled="loading"
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('common.refresh')"
              >
                <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
              </button>
              <div class="relative" ref="columnDropdownRef">
                <button
                  @click.stop="showColumnDropdown = !showColumnDropdown"
                  class="btn btn-secondary shrink-0 btn-icon"
                  :title="t('keys.columnSettings')"
                >
                  <Icon name="grid" size="sm" />
                </button>
                <MotionTransition name="dropdown-fade">
                  <div
                    v-if="showColumnDropdown"
                    :inert="!showColumnDropdown || undefined"
                    class="dropdown right-0 top-full z-50 mt-2 max-h-menu w-52 overflow-y-auto p-2"
                  >
                    <button
                      v-for="column in toggleableColumns"
                      :key="column.key"
                      @click="toggleColumn(column.key)"
                      class="dropdown-item-sm justify-between rounded-control"
                    >
                      <span>{{ column.label }}</span>
                      <Icon
                        v-if="isColumnVisible(column.key)"
                        name="check"
                        size="sm"
                        class="text-primary-500"
                        :stroke-width="2"
                        :animate-on-hover="false"
                      />
                    </button>
                  </div>
                </MotionTransition>
              </div>
              <ScopeDropdown v-if="teamFeatureEnabled" v-model="scope" @change="onScopeChange" />
              <button @click="openCreateModal" class="btn btn-primary" data-tour="keys-create-btn">
                <Icon name="plus" size="sm" class="mr-2" />
                {{ t('keys.createKey') }}
              </button>
            </div>
          </div>
          <!-- The endpoint strip belongs to the toolbar; its negative margin reduces the default table gap to match the strip's other spacing. -->
          <EndpointPopover
            v-if="
              publicSettings?.api_base_url || (publicSettings?.custom_endpoints?.length ?? 0) > 0
            "
            class="-mb-2"
            :api-base-url="publicSettings?.api_base_url || ''"
            :custom-endpoints="publicSettings?.custom_endpoints || []"
          />
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="user-keys-column-order"
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
                class="rounded-control p-1 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
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
                  :animate-on-hover="false"
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
              <!-- Show current concurrency and its cap beside the key name while active requests are present. -->
              <span
                v-if="(row.current_concurrency ?? 0) > 0"
                data-test="key-concurrency"
                :class="[
                  'ml-1 inline-flex items-center gap-1 text-xs tabular-nums',
                  concurrencyTone(row).text,
                ]"
                :title="concurrencyTitle(row)"
                :aria-label="concurrencyTitle(row)"
              >
                <span
                  :class="['h-1.5 w-1.5 rounded-full', concurrencyTone(row).dot]"
                  aria-hidden="true"
                />
                <span>
                  <span>{{ row.current_concurrency }}</span>
                  <span
                    v-if="(row.concurrency_limit ?? 0) > 0"
                    class="text-gray-400 dark:text-dark-500"
                    >/{{ row.concurrency_limit }}</span
                  >
                </span>
              </span>
            </div>
          </template>

          <template #cell-group="{ row }">
            <button
              v-if="row.is_composite"
              type="button"
              data-test="composite-group-summary"
              class="-mx-1 -my-1 flex max-w-[22rem] flex-wrap items-center gap-1.5 rounded-control p-1 text-left transition duration-normal hover:bg-gray-100 dark:hover:bg-dark-700"
              :title="t('keys.composite.editMappings')"
              @click="editKey(row)"
            >
              <!-- Composite mappings show prefix and group in branded chips, with excess mappings represented by the remaining count. -->
              <span
                v-for="binding in visibleCompositeGroups(row)"
                :key="`${row.id}-${binding.group_id}`"
                :class="[
                  'inline-flex min-w-0 items-center gap-1.5 rounded-compact px-2 py-0.5 text-xs leading-5',
                  compositeGroupChipClass(binding.group?.display_brand),
                ]"
              >
                <span class="max-w-24 truncate font-mono font-medium">{{ binding.prefix }}</span>
                <span class="opacity-40">/</span>
                <span class="max-w-28 truncate opacity-75">{{
                  binding.group?.display_name || binding.group?.name || `#${binding.group_id}`
                }}</span>
              </span>
              <span
                v-if="hiddenCompositeGroupCount(row) > 0"
                class="inline-flex items-center rounded-compact bg-gray-100 px-2 py-0.5 text-xs leading-5 text-gray-500 dark:bg-dark-800 dark:text-dark-400"
              >
                {{ t('keys.composite.moreMappings', { count: hiddenCompositeGroupCount(row) }) }}
              </span>
            </button>
            <div v-else class="group/dropdown relative">
              <button
                :ref="(el) => setGroupButtonRef(row.id, el)"
                @click="openGroupSelector(row)"
                class="-mx-2 -my-1 flex cursor-pointer items-center gap-2 rounded-control px-2 py-1 transition duration-normal hover:bg-gray-100 dark:hover:bg-dark-700"
                :title="t('keys.clickToChangeGroup')"
              >
                <GroupBadge
                  v-if="row.group"
                  :name="row.group.display_name || row.group.name"
                  :display-brand="row.group.display_brand"
                  :rate-multiplier="row.group.rate_multiplier"
                  :user-rate-multiplier="userGroupRates[row.group.id]"
                />
                <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{
                  t('keys.noGroup')
                }}</span>
                <Icon
                  name="sort"
                  size="xs"
                  :animate-on-hover="false"
                  class="h-3.5 w-3.5 text-gray-400 opacity-60 transition-opacity group-hover/dropdown:opacity-100"
                />
              </button>
            </div>
          </template>

          <template #cell-usage="{ row }">
            <!-- Align usage to the trailing edge in narrow cards while keeping ordinary table cells left-aligned. -->
            <div class="text-sm">
              <div
                v-if="usageLoading && !usageStats[row.id]"
                class="flex h-10 flex-col items-end justify-center gap-2 lg:items-start"
                role="status"
                :aria-label="t('common.loading')"
                aria-busy="true"
                data-loading-skeleton
              >
                <Skeleton :width="96" :height="12" />
                <Skeleton :width="112" :height="12" />
              </div>
              <div v-else class="space-y-0.5">
                <div class="flex items-center justify-end gap-1.5 lg:justify-start">
                  <span class="text-gray-500 dark:text-gray-400">{{ t('keys.today') }}:</span>
                  <span class="font-medium text-gray-900 dark:text-white">
                    {{
                      formatBalanceAmount(usageStats[row.id]?.today_actual_cost ?? 0, {
                        fractionDigits: 4,
                      })
                    }}
                  </span>
                </div>
                <!-- The batch endpoint reports total_actual_cost for the recent thirty days, so the label names that interval. -->
                <div class="flex items-center justify-end gap-1.5 lg:justify-start">
                  <span class="text-gray-500 dark:text-gray-400">{{ t('keys.total') }}:</span>
                  <span class="font-medium text-gray-900 dark:text-white">
                    {{
                      formatBalanceAmount(usageStats[row.id]?.total_actual_cost ?? 0, {
                        fractionDigits: 4,
                      })
                    }}
                  </span>
                </div>
              </div>
              <!-- Quota progress (if quota is set) -->
              <div v-if="row.quota > 0" class="mt-1.5">
                <div class="flex items-center justify-end gap-1.5 lg:justify-start">
                  <span class="text-gray-500 dark:text-gray-400">{{ t('keys.quota') }}:</span>
                  <span
                    :class="[
                      'font-medium',
                      row.quota_used >= row.quota
                        ? 'text-red-500'
                        : row.quota_used >= row.quota * 0.8
                          ? 'text-yellow-500'
                          : 'text-gray-900 dark:text-white',
                    ]"
                  >
                    {{ formatBalancePair(row.quota_used, row.quota, 2, 2) }}
                  </span>
                </div>
                <div
                  class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600"
                >
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      row.quota_used >= row.quota
                        ? 'bg-red-500'
                        : row.quota_used >= row.quota * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-primary-500',
                    ]"
                    :style="{ width: Math.min((row.quota_used / row.quota) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </div>
          </template>

          <template #cell-rate_limit="{ row }">
            <div
              v-if="row.rate_limit_5h > 0 || row.rate_limit_1d > 0 || row.rate_limit_7d > 0"
              class="space-y-1.5 min-w-[140px]"
            >
              <!-- 5h window -->
              <div v-if="row.rate_limit_5h > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">5h</span>
                  <span
                    :class="[
                      'font-medium tabular-nums',
                      row.usage_5h >= row.rate_limit_5h
                        ? 'text-red-500'
                        : row.usage_5h >= row.rate_limit_5h * 0.8
                          ? 'text-yellow-500'
                          : 'text-gray-700 dark:text-gray-300',
                    ]"
                  >
                    {{ formatBalancePair(row.usage_5h, row.rate_limit_5h, 2, 2, false) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      row.usage_5h >= row.rate_limit_5h
                        ? 'bg-red-500'
                        : row.usage_5h >= row.rate_limit_5h * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-emerald-500',
                    ]"
                    :style="{
                      width: Math.min((row.usage_5h / row.rate_limit_5h) * 100, 100) + '%',
                    }"
                  />
                </div>
                <div
                  v-if="row.reset_5h_at && formatResetTime(row.reset_5h_at)"
                  class="text-xs text-gray-400 dark:text-gray-500 tabular-nums"
                >
                  ⟳ {{ formatResetTime(row.reset_5h_at) }}
                </div>
              </div>
              <!-- 1d window -->
              <div v-if="row.rate_limit_1d > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">1d</span>
                  <span
                    :class="[
                      'font-medium tabular-nums',
                      row.usage_1d >= row.rate_limit_1d
                        ? 'text-red-500'
                        : row.usage_1d >= row.rate_limit_1d * 0.8
                          ? 'text-yellow-500'
                          : 'text-gray-700 dark:text-gray-300',
                    ]"
                  >
                    {{ formatBalancePair(row.usage_1d, row.rate_limit_1d, 2, 2, false) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      row.usage_1d >= row.rate_limit_1d
                        ? 'bg-red-500'
                        : row.usage_1d >= row.rate_limit_1d * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-emerald-500',
                    ]"
                    :style="{
                      width: Math.min((row.usage_1d / row.rate_limit_1d) * 100, 100) + '%',
                    }"
                  />
                </div>
                <div
                  v-if="row.reset_1d_at && formatResetTime(row.reset_1d_at)"
                  class="text-xs text-gray-400 dark:text-gray-500 tabular-nums"
                >
                  ⟳ {{ formatResetTime(row.reset_1d_at) }}
                </div>
              </div>
              <!-- 7d window -->
              <div v-if="row.rate_limit_7d > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">7d</span>
                  <span
                    :class="[
                      'font-medium tabular-nums',
                      row.usage_7d >= row.rate_limit_7d
                        ? 'text-red-500'
                        : row.usage_7d >= row.rate_limit_7d * 0.8
                          ? 'text-yellow-500'
                          : 'text-gray-700 dark:text-gray-300',
                    ]"
                  >
                    {{ formatBalancePair(row.usage_7d, row.rate_limit_7d, 2, 2, false) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      row.usage_7d >= row.rate_limit_7d
                        ? 'bg-red-500'
                        : row.usage_7d >= row.rate_limit_7d * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-emerald-500',
                    ]"
                    :style="{
                      width: Math.min((row.usage_7d / row.rate_limit_7d) * 100, 100) + '%',
                    }"
                  />
                </div>
                <div
                  v-if="row.reset_7d_at && formatResetTime(row.reset_7d_at)"
                  class="text-xs text-gray-400 dark:text-gray-500 tabular-nums"
                >
                  ⟳ {{ formatResetTime(row.reset_7d_at) }}
                </div>
              </div>
              <!-- Reset button -->
              <button
                v-if="row.usage_5h > 0 || row.usage_1d > 0 || row.usage_7d > 0"
                @click.stop="confirmResetRateLimitFromTable(row)"
                class="mt-0.5 inline-flex items-center gap-1 rounded-compact px-1.5 py-0.5 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                :title="t('keys.resetRateLimitUsage')"
              >
                <Icon name="refresh" size="xs" />
                {{ t('keys.resetUsage') }}
              </button>
            </div>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-expires_at="{ value }">
            <span
              v-if="value"
              :class="[
                'text-sm',
                new Date(value) < new Date()
                  ? 'text-red-500 dark:text-red-400'
                  : 'text-gray-500 dark:text-dark-400',
              ]"
            >
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{
              t('keys.noExpiration')
            }}</span>
          </template>

          <template #cell-status="{ value, row }">
            <span
              :class="[
                'badge gap-1',
                row.team_owner_disabled
                  ? 'badge-danger'
                  : value === 'active'
                    ? 'badge-success'
                    : value === 'quota_exhausted'
                      ? 'badge-warning'
                      : value === 'expired'
                        ? 'badge-danger'
                        : 'badge-gray',
              ]"
            >
              <Icon v-if="row.team_owner_disabled" name="lock" size="xs" />
              <!-- Baseline alignment keeps status text level with the mobile field label when a lock icon precedes it. -->
              <span class="self-baseline">{{
                row.team_owner_disabled
                  ? t('keys.status.team_owner_disabled')
                  : t('keys.status.' + value)
              }}</span>
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
            <span class="text-sm text-gray-500 dark:text-dark-400">{{
              formatDateTime(value)
            }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <!-- Keep frequent actions visible and collect less frequent or destructive actions in the overflow menu. -->
              <button
                @click="editKey(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <!-- A team administrator must release an owner-imposed lock. -->
              <span
                v-if="row.team_owner_disabled"
                class="flex cursor-not-allowed flex-col items-center gap-0.5 rounded-control p-1.5 text-amber-600 dark:text-amber-400"
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
                  'flex flex-col items-center gap-0.5 rounded-control p-1.5 transition-colors',
                  row.status === 'active'
                    ? 'text-gray-500 hover:bg-yellow-50 hover:text-yellow-600 dark:hover:bg-yellow-900/20 dark:hover:text-yellow-400'
                    : 'text-gray-500 hover:bg-green-50 hover:text-green-600 dark:hover:bg-green-900/20 dark:hover:text-green-400',
                ]"
              >
                <Icon v-if="row.status === 'active'" name="ban" size="sm" />
                <Icon v-else name="checkCircle" size="sm" :animate-on-hover="false" />
                <span class="text-xs">{{
                  row.status === 'active' ? t('keys.disable') : t('keys.enable')
                }}</span>
              </button>
              <button
                class="key-action-menu-trigger flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
                :class="{
                  'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white':
                    actionMenuKey?.id === row.id,
                }"
                aria-haspopup="menu"
                :aria-expanded="actionMenuKey?.id === row.id"
                :aria-controls="
                  actionMenuKey?.id === row.id ? `key-action-menu-${row.id}` : undefined
                "
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

    <!-- Creation and editing share a dialog whose controls shrink with narrow viewports. -->
    <BaseDialog
      :show="showCreateModal || showEditModal"
      :title="showEditModal ? t('keys.editKey') : t('keys.createKey')"
      width="normal"
      @close="closeModals"
    >
      <form
        id="key-form"
        @submit.prevent="handleSubmit"
        class="key-form-controls min-w-0 max-w-full space-y-5"
      >
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

        <!-- The shared toggle switches composite routing into the full mapping editor. -->
        <div class="flex items-center justify-between gap-4">
          <div>
            <label class="input-label mb-0">{{ t('keys.composite.label') }}</label>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              {{ t('keys.composite.hint') }}
            </p>
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
                :display-brand="(option as unknown as GroupOption).displayBrand"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
              />
              <span v-else class="text-gray-400">{{ t('keys.selectGroup') }}</span>
            </template>
            <template #option="{ option, selected }">
              <GroupOptionItem
                :name="(option as unknown as GroupOption).label"
                :display-brand="(option as unknown as GroupOption).displayBrand"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                :description="(option as unknown as GroupOption).description"
                :selected="selected"
              />
            </template>
          </Select>
        </div>

        <RuleListEditor
          v-else
          :items="formData.composite_groups"
          :item-key="(binding) => binding.local_id"
          :add-label="t('keys.composite.addMapping')"
          add-placement="footer"
          :min="1"
          :max="20"
          reorderable
          data-test="composite-group-editor"
          @add="addCompositeBinding"
          @remove="removeCompositeBinding"
          @move="moveCompositeBinding"
        >
          <template #row="{ item: binding, index }">
            <div
              class="grid min-w-0 grid-cols-1 items-start gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(7rem,0.65fr)]"
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
                  :class="{ 'input-error': compositeBindingError(index) }"
                  :placeholder="t('keys.composite.prefixPlaceholder')"
                />
                <p v-if="compositeBindingError(index)" class="input-error-text">
                  {{ compositeBindingError(index) }}
                </p>
              </div>
            </div>
          </template>
        </RuleListEditor>

        <!-- The shared selector sets this key's Fast policy; server policy still decides the effective request behavior. -->
        <div>
          <label class="input-label">{{ t('keys.fastModePolicyLabel') }}</label>
          <Select
            v-model="formData.fast_mode_policy"
            :options="fastModePolicyOptions"
            :placeholder="t('keys.fastModePolicyLabel')"
            data-test="fast-mode-policy-select"
          />
        </div>

        <!-- This switch allows request routing to fall back when the chosen group is unavailable. -->
        <div class="flex items-center justify-between">
          <label class="input-label mb-0">{{ t('keys.fallbackWhenGroupUnavailable') }}</label>
          <Toggle v-model="formData.fallback_when_group_unavailable" size="sm" />
        </div>

        <!-- Model redirects are edited as rows; removing every row submits an empty mapping object during update. -->
        <ModelMappingEditor
          v-model="formData.model_mapping_rows"
          :title="t('keys.modelRedirect.label')"
          :hint="t('keys.modelRedirect.hint')"
          :add-label="t('keys.modelRedirect.addRule')"
          :empty-text="t('keys.modelRedirect.empty')"
          :max="MODEL_REDIRECT_MAX_RULES"
          :source-label="t('keys.modelRedirect.source')"
          :target-label="t('keys.modelRedirect.target')"
          :source-placeholder="t('keys.modelRedirect.sourcePlaceholder')"
          :target-placeholder="t('keys.modelRedirect.targetPlaceholder')"
          :field-errors="modelMappingFieldErrors"
          test-id="model-mapping"
          data-test="model-mapping-editor"
        >
          <template #title-suffix>
            <ModelRedirectHelp />
          </template>
        </ModelMappingEditor>

        <!-- Custom Key Section (only for create) -->
        <div v-if="!showEditModal" class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.customKeyLabel') }}</label>
            <Toggle v-model="formData.use_custom_key" size="sm" off-tone="soft" />
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
              :aria-describedby="
                selectedKey?.team_owner_disabled ? 'team-owner-disabled-hint' : undefined
              "
              data-test="key-status-toggle"
              @update:model-value="onStatusToggle"
            />
          </div>
          <p
            v-if="selectedKey?.team_owner_disabled"
            id="team-owner-disabled-hint"
            class="mt-2 flex items-start gap-2 rounded-control border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-900/20 dark:text-amber-300"
          >
            <Icon name="lock" size="sm" class="mt-0.5 shrink-0" />
            <span>{{ t('keys.teamOwnerDisabledHint') }}</span>
          </p>
        </div>

        <!-- IP Restriction Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.ipRestriction') }}</label>
            <Toggle v-model="formData.enable_ip_restriction" size="sm" off-tone="soft" />
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
            <Toggle v-model="formData.enable_quota" size="sm" off-tone="soft" />
          </div>
          -->

          <div class="space-y-4">
            <div>
              <div class="relative">
                <span class="input-icon text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.quota"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input input-has-icon input-icon-text"
                  :placeholder="t('keys.quotaAmountPlaceholder')"
                />
              </div>
              <p class="input-hint">{{ t('keys.quotaAmountHint') }}</p>
            </div>

            <!-- Quota used display (only in edit mode) -->
            <div v-if="showEditModal && selectedKey && selectedKey.quota > 0">
              <label class="input-label">{{ t('keys.quotaUsed') }}</label>
              <div class="flex items-center gap-2">
                <div class="flex-1 h-9 rounded-control bg-gray-100 px-3 py-1.5 dark:bg-dark-700">
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

        <!-- Request-count limits and spending limits have separate controls and units. -->
        <div class="space-y-3">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label for="key-concurrency-limit" class="input-label">{{
                t('keys.concurrencyLimit')
              }}</label>
              <input
                id="key-concurrency-limit"
                v-model.number="formData.concurrency_limit"
                type="number"
                min="0"
                max="2147483647"
                step="1"
                class="input"
                placeholder="0"
              />
            </div>
            <div>
              <label for="key-rpm-limit" class="input-label">{{ t('keys.rpmLimit') }}</label>
              <input
                id="key-rpm-limit"
                v-model.number="formData.rpm_limit"
                type="number"
                min="0"
                max="2147483647"
                step="1"
                class="input"
                placeholder="0"
              />
            </div>
          </div>
          <p class="input-hint">{{ t('keys.requestLimitsHint') }}</p>
        </div>

        <!-- Rate Limit Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between">
            <label class="input-label mb-0">{{ t('keys.rateLimitSection') }}</label>
            <Toggle v-model="formData.enable_rate_limit" size="sm" off-tone="soft" />
          </div>

          <div v-if="formData.enable_rate_limit" class="space-y-4 pt-2">
            <p class="input-hint -mt-2">{{ t('keys.rateLimitHint') }}</p>
            <!-- 5-Hour Limit -->
            <div>
              <label class="input-label">{{ t('keys.rateLimit5h') }}</label>
              <div class="relative">
                <span class="input-icon text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.rate_limit_5h"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input input-has-icon input-icon-text"
                  :placeholder="'0'"
                />
              </div>
              <!-- Usage info (edit mode only) -->
              <div
                v-if="showEditModal && selectedKey && selectedKey.rate_limit_5h > 0"
                class="mt-2"
              >
                <div class="flex items-center gap-2">
                  <div
                    class="flex-1 h-9 rounded-control bg-gray-100 px-3 py-1.5 text-sm dark:bg-dark-700"
                  >
                    <span
                      :class="[
                        'font-medium',
                        selectedKey.usage_5h >= selectedKey.rate_limit_5h
                          ? 'text-red-500'
                          : selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8
                            ? 'text-yellow-500'
                            : 'text-gray-900 dark:text-white',
                      ]"
                    >
                      {{ formatBalanceAmount(selectedKey.usage_5h, { fractionDigits: 4 }) }}
                    </span>
                    <span class="mx-2 text-gray-400">/</span>
                    <span class="text-gray-500 dark:text-gray-400">
                      {{ formatBalanceAmount(selectedKey.rate_limit_5h, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                </div>
                <div
                  class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600"
                >
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h
                        ? 'bg-red-500'
                        : selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-green-500',
                    ]"
                    :style="{
                      width:
                        Math.min((selectedKey.usage_5h / selectedKey.rate_limit_5h) * 100, 100) +
                        '%',
                    }"
                  />
                </div>
              </div>
            </div>

            <!-- Daily Limit -->
            <div>
              <label class="input-label">{{ t('keys.rateLimit1d') }}</label>
              <div class="relative">
                <span class="input-icon text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.rate_limit_1d"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input input-has-icon input-icon-text"
                  :placeholder="'0'"
                />
              </div>
              <!-- Usage info (edit mode only) -->
              <div
                v-if="showEditModal && selectedKey && selectedKey.rate_limit_1d > 0"
                class="mt-2"
              >
                <div class="flex items-center gap-2">
                  <div
                    class="flex-1 h-9 rounded-control bg-gray-100 px-3 py-1.5 text-sm dark:bg-dark-700"
                  >
                    <span
                      :class="[
                        'font-medium',
                        selectedKey.usage_1d >= selectedKey.rate_limit_1d
                          ? 'text-red-500'
                          : selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8
                            ? 'text-yellow-500'
                            : 'text-gray-900 dark:text-white',
                      ]"
                    >
                      {{ formatBalanceAmount(selectedKey.usage_1d, { fractionDigits: 4 }) }}
                    </span>
                    <span class="mx-2 text-gray-400">/</span>
                    <span class="text-gray-500 dark:text-gray-400">
                      {{ formatBalanceAmount(selectedKey.rate_limit_1d, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                </div>
                <div
                  class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600"
                >
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d
                        ? 'bg-red-500'
                        : selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-green-500',
                    ]"
                    :style="{
                      width:
                        Math.min((selectedKey.usage_1d / selectedKey.rate_limit_1d) * 100, 100) +
                        '%',
                    }"
                  />
                </div>
              </div>
            </div>

            <!-- 7-Day Limit -->
            <div>
              <label class="input-label">{{ t('keys.rateLimit7d') }}</label>
              <div class="relative">
                <span class="input-icon text-gray-500">{{ balanceUnitSymbol }}</span>
                <input
                  v-model.number="formData.rate_limit_7d"
                  type="number"
                  step="0.01"
                  min="0"
                  class="input input-has-icon input-icon-text"
                  :placeholder="'0'"
                />
              </div>
              <!-- Usage info (edit mode only) -->
              <div
                v-if="showEditModal && selectedKey && selectedKey.rate_limit_7d > 0"
                class="mt-2"
              >
                <div class="flex items-center gap-2">
                  <div
                    class="flex-1 h-9 rounded-control bg-gray-100 px-3 py-1.5 text-sm dark:bg-dark-700"
                  >
                    <span
                      :class="[
                        'font-medium',
                        selectedKey.usage_7d >= selectedKey.rate_limit_7d
                          ? 'text-red-500'
                          : selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8
                            ? 'text-yellow-500'
                            : 'text-gray-900 dark:text-white',
                      ]"
                    >
                      {{ formatBalanceAmount(selectedKey.usage_7d, { fractionDigits: 4 }) }}
                    </span>
                    <span class="mx-2 text-gray-400">/</span>
                    <span class="text-gray-500 dark:text-gray-400">
                      {{ formatBalanceAmount(selectedKey.rate_limit_7d, { fractionDigits: 2 }) }}
                    </span>
                  </div>
                </div>
                <div
                  class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600"
                >
                  <div
                    :class="[
                      'h-full rounded-full transition-[width,background-color]',
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d
                        ? 'bg-red-500'
                        : selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8
                          ? 'bg-yellow-500'
                          : 'bg-green-500',
                    ]"
                    :style="{
                      width:
                        Math.min((selectedKey.usage_7d / selectedKey.rate_limit_7d) * 100, 100) +
                        '%',
                    }"
                  />
                </div>
              </div>
            </div>

            <!-- Reset Rate Limit button (edit mode only) -->
            <div
              v-if="
                showEditModal &&
                selectedKey &&
                (selectedKey.rate_limit_5h > 0 ||
                  selectedKey.rate_limit_1d > 0 ||
                  selectedKey.rate_limit_7d > 0)
              "
            >
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
            <Toggle v-model="formData.enable_expiration" size="sm" off-tone="soft" />
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
                  'rounded-control px-3 py-1.5 text-sm transition-colors',
                  formData.expiration_preset === days
                    ? 'bg-primary-100 text-primary-700 dark:bg-primary-500/8 dark:text-primary-500'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600',
                ]"
              >
                {{
                  showEditModal ? t('keys.extendDays', { days }) : t('keys.expiresInDays', { days })
                }}
              </button>
              <button
                type="button"
                @click="formData.expiration_preset = 'custom'"
                :class="[
                  'rounded-control px-3 py-1.5 text-sm transition-colors',
                  formData.expiration_preset === 'custom'
                    ? 'bg-primary-100 text-primary-700 dark:bg-primary-500/8 dark:text-primary-500'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-400 dark:hover:bg-dark-600',
                ]"
              >
                {{ t('keys.customDate') }}
              </button>
            </div>

            <!-- Date picker (always show for precise adjustment) -->
            <div>
              <label class="input-label">{{ t('keys.expirationDate') }}</label>
              <input v-model="formData.expiration_date" type="datetime-local" class="input" />
              <p class="input-hint">{{ t('keys.expirationDateHint') }}</p>
            </div>

            <!-- Current expiration display (only in edit mode) -->
            <div v-if="showEditModal && selectedKey?.expires_at" class="text-sm">
              <span class="text-gray-500 dark:text-gray-400"
                >{{ t('keys.currentExpiration') }}:
              </span>
              <span class="font-medium text-gray-900 dark:text-white">
                {{ formatDateTime(selectedKey.expires_at) }}
              </span>
            </div>
          </div>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeModals" type="button" class="btn btn-secondary py-1.5">
            {{ t('common.cancel') }}
          </button>
          <button
            form="key-form"
            type="submit"
            :disabled="submitting"
            class="btn btn-primary py-1.5"
            data-tour="key-form-submit"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
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

    <!-- Rotation confirmation explains credential invalidation and stays open while submission prevents repeated operations. -->
    <ConfirmDialog
      :show="rotationKey !== null"
      :title="t('keys.rotateKey')"
      :message="t('keys.rotateConfirmMessage', { name: rotationKey?.name })"
      :confirm-text="t('keys.confirmRotate')"
      :danger="true"
      :loading="rotatingKey"
      @confirm="handleRotate"
      @cancel="cancelRotate"
    />

    <!-- Show the returned secret after rotation so the user can copy it into clients immediately. -->
    <BaseDialog
      :show="rotatedKey !== null"
      :title="t('keys.keyRotatedSuccess')"
      width="narrow"
      @close="rotatedKey = null"
    >
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('keys.rotatedKeyHint') }}</p>
        <code
          class="block break-all rounded-surface bg-gray-50 p-4 text-sm text-gray-900 dark:bg-dark-800 dark:text-gray-100"
        >
          {{ rotatedKey?.key }}
        </code>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="rotatedKey = null">
            {{ t('common.close') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            @click="rotatedKey && copyToClipboard(rotatedKey.key, rotatedKey.id)"
          >
            {{ t('common.copy') }}
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
      :message="
        t('keys.resetQuotaConfirmMessage', {
          name: selectedKey?.name,
          used: formatBalanceAmount(selectedKey?.quota_used, { fractionDigits: 4 }),
        })
      "
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
      :group="selectedKeyGroup"
      :composite-groups="selectedCompositeGroups"
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
      @rotate="confirmRotate"
      @delete="confirmDelete"
    />

    <!-- Client and model choices follow the available protocols. -->
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
        <Select v-model="ccClient" :options="ccClientOptions" />
        <Select
          v-model="ccModel"
          :options="ccModelOptions"
          searchable
          :placeholder="t('keys.useKeyModal.selectModel')"
        />
        <p v-if="!ccModelOptions.length" class="input-hint">{{ t('keys.useKeyModal.noModels') }}</p>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="!ccModel"
          @click="handleCcsClientSelect(ccClient)"
        >
          {{ t('common.confirm') }}
        </button>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button @click="closeCcsClientSelect" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Group Selector Dropdown (Teleported to body to avoid overflow clipping) -->
    <Teleport to="body">
      <MotionTransition name="dropdown-fade">
        <div
          v-if="groupSelectorKeyId !== null && dropdownPosition"
          :inert="!(groupSelectorKeyId !== null && dropdownPosition) || undefined"
          ref="dropdownRef"
          class="dropdown animate-in fade-in slide-in-from-top-2 fixed z-teleport-dropdown w-max max-w-[calc(100vw-16px)] overflow-hidden duration-normal sm:min-w-[380px] py-0"
          style="pointer-events: auto !important"
          :style="{
            top: dropdownPosition.top !== undefined ? dropdownPosition.top + 'px' : undefined,
            bottom:
              dropdownPosition.bottom !== undefined ? dropdownPosition.bottom + 'px' : undefined,
            left: dropdownPosition.left + 'px',
          }"
        >
          <!-- Group search uses the shared input so its surface and focus treatment follow the current global theme. -->
          <div class="border-b border-gray-100 p-2 dark:border-dark-700">
            <SearchInput
              v-model="groupSearchQuery"
              :placeholder="t('keys.searchGroup')"
              @click.stop
            />
          </div>
          <!-- Group list -->
          <div class="max-h-80 overflow-y-auto p-1.5">
            <button
              v-for="option in filteredGroupOptions"
              :key="option.value ?? 'null'"
              @click="changeGroup(selectedKeyForGroup!, option.value)"
              :class="[
                'flex w-full items-center justify-between rounded-control px-3 py-2.5 text-sm transition-colors',
                'border-b border-gray-100 last:border-0 dark:border-dark-700',
                selectedKeyForGroup?.group_id === option.value ||
                (!selectedKeyForGroup?.group_id && option.value === null)
                  ? 'bg-primary-50 dark:bg-primary-500/8 dark:text-primary-500'
                  : 'hover:bg-gray-100 dark:hover:bg-dark-700',
              ]"
              :title="option.description || undefined"
            >
              <GroupOptionItem
                :name="option.label"
                :display-brand="option.displayBrand"
                :rate-multiplier="option.rate"
                :user-rate-multiplier="option.userRate"
                :description="option.description"
                :selected="
                  selectedKeyForGroup?.group_id === option.value ||
                  (!selectedKeyForGroup?.group_id && option.value === null)
                "
              />
            </button>
            <!-- Empty state when search has no results -->
            <div
              v-if="filteredGroupOptions.length === 0"
              class="py-4 text-center text-sm text-gray-400 dark:text-gray-500"
            >
              {{ t('keys.noGroupFound') }}
            </div>
          </div>
        </div>
      </MotionTransition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import Skeleton from '@/components/common/Skeleton.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import {
  watch,
  ref,
  reactive,
  computed,
  onMounted,
  onUnmounted,
  type ComponentPublicInstance,
} from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { useOnboardingStore } from '@/stores/onboarding'
import { useClipboard } from '@/composables/useClipboard'
import { COPY_FEEDBACK_MS } from '@/constants/ui'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { keysAPI, authAPI, usageAPI, userGroupsAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import Toggle from '@/components/common/Toggle.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import ModelMappingEditor from '@/components/common/ModelMappingEditor.vue'
import Icon from '@/components/icons/Icon.vue'
import KeyActionMenu from '@/components/keys/KeyActionMenu.vue'
import { buildKeyFormPayload } from '@/components/keys/keyForm'
import UseKeyModal from '@/components/keys/UseKeyModal.vue'
import ModelRedirectHelp from '@/components/keys/ModelRedirectHelp.vue'
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
  UpdateApiKeyRequest,
} from '@/api/keys.types'
import type { Group } from '@/api/groups.types'
import type { PublicSettings } from '@/api/settings.types'
import type { Column } from '@/components/common/table/types'
import type { BatchApiKeyUsageStats } from '@/api/usage'
import { formatDateTime } from '@/utils/format'
import {
  KEY_REDIRECT_RULES,
  firstModelMappingIssue,
  validateModelMappingRows,
  type ModelMappingRow,
} from '@/utils/modelMappingRules'
import { resolveProviderBrand } from '@/utils/providerBrand'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import {
  buildCcSwitchImportDeeplink,
  buildCcSwitchUsageScript,
  type CcSwitchClientType,
} from '@/utils/ccswitchImport'
import {
  availableClients,
  CLIENT_LABELS,
  clientProtocol,
  modelsForProtocol,
  groupForKeyConfig,
} from '@/utils/clientConfig'

// KeysView.vue manages API-key listings and the forms used to create, update,
// rotate, and remove keys for the selected ownership scope. The page coordinates
// group choices, billing options, usage observations, and per-key dialogs while
// keeping table column preferences in browser storage. Restriction conversion
// belongs to keyForm.ts, where creation and update expiry values remain distinct.
//
// List queries and usage reads complete independently, so the page maintains
// request and selection state around refreshes. Credential rotation replaces the
// displayed key before starting another list refresh, allowing subsequent copy
// actions to use the returned credential. Dialog visibility and selected records
// remain page-owned so user actions retain their intended target during updates.

// Translation setup supplies localized labels and validation messages for the
// page's subsequent computed values, handlers, and template rendering.
const { t } = useI18n()

// formatDateTimeLocal converts a parseable API timestamp to browser-local date
// and time with minute precision. Callers provide valid date text; invalid input
// produces NaN components because this formatter performs no date validation.
const formatDateTimeLocal = (isoDate: string): string => {
  const date = new Date(isoDate)
  // pad formats one local date component with at least two digits.
  const pad = (n: number) => n.toString().padStart(2, '0')
  // Omit seconds and milliseconds to match the datetime-local control's precision.
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// GroupOption combines a routing-group identity with labels and rates used by the selector.
interface GroupOption {
  search_terms?: string[] // Additional group search aliases.
  value: number // Routing-group numeric identity.
  label: string // Group display name.
  description: string | null // Optional group description text.
  displayBrand: string | null // Optional badge brand identifier.
  rate: number // Default group billing multiplier.
  userRate: number | null // Account-specific multiplier when present.
}

// Route scope, account presentation, clipboard support, and onboarding state connect key-
// list actions to their shared page dependencies.
const appStore = useAppStore()
const route = useRoute()
const router = useRouter()
const scope = ref<DataScope>(route?.query?.scope === 'team' ? 'team' : 'personal')
const onboardingStore = useOnboardingStore()
const { copyToClipboard: clipboardCopy } = useClipboard()
const { balanceUnitName, balanceUnitSymbol, formatBalanceAmount } = useBalanceDisplay()

// formatBalancePair formats used and limit amounts with independently selected precision.
// The caller can omit spaces around the separator for compact table cells while retaining
// the configured balance formatter.
const formatBalancePair = (
  used: number | null | undefined,
  limit: number | null | undefined,
  usedDigits: number,
  limitDigits: number,
  spaced: boolean = true,
) => {
  const separator = spaced ? ' / ' : '/'
  return `${formatBalanceAmount(used, { fractionDigits: usedDigits })}${separator}${formatBalanceAmount(limit, { fractionDigits: limitDigits })}`
}

// COMPOSITE_GROUP_PREVIEW_LIMIT bounds ordinary inline mapping previews, while a single
// extra mapping is shown directly to avoid a one-item remainder.
const COMPOSITE_GROUP_PREVIEW_LIMIT = 4

// visibleCompositeGroups selects the inline composite mapping preview from a key row. At
// most one mapping above the nominal limit remains fully visible; larger sets show the
// leading slice.
const visibleCompositeGroups = (row: ApiKey) => {
  const groups = row.composite_groups ?? []
  if (groups.length <= COMPOSITE_GROUP_PREVIEW_LIMIT + 1) {
    return groups
  }
  return groups.slice(0, COMPOSITE_GROUP_PREVIEW_LIMIT)
}

// compositeGroupChipClass chooses the same provider-brand palette used by ordinary group
// badges. An absent or whitespace-only brand uses the neutral badge surface and inset border
// for the composite mapping chip.
const compositeGroupChipClass = (displayBrand?: string | null) => {
  const brand = displayBrand?.trim()
  if (!brand) {
    return 'ring-1 ring-inset bg-gray-100 text-gray-900 ring-gray-200 dark:bg-dark-800 dark:text-dark-50 dark:ring-dark-600'
  }
  return `ring-1 ring-inset ${resolveProviderBrand(brand).badgeClass}`
}

// concurrencyTone colors key occupancy using the configured concurrency cap. Full occupancy
// is red, eighty percent is amber, and unlimited or lower occupancy uses the ordinary green
// indicator and muted text.
const concurrencyTone = (row: ApiKey) => {
  const current = row.current_concurrency ?? 0
  const limit = row.concurrency_limit ?? 0
  if (limit > 0 && current >= limit) {
    return { dot: 'bg-red-500', text: 'font-medium text-red-600 dark:text-red-400' }
  }
  if (limit > 0 && current >= limit * 0.8) {
    return { dot: 'bg-amber-500', text: 'font-medium text-amber-600 dark:text-amber-400' }
  }
  return { dot: 'bg-emerald-500', text: 'text-gray-500 dark:text-dark-300' }
}

// concurrencyTitle formats the active request count against a finite cap or unlimited label.
const concurrencyTitle = (row: ApiKey) => {
  const current = row.current_concurrency ?? 0
  const limit = row.concurrency_limit ?? 0
  return limit > 0
    ? t('keys.concurrencyInUse', { current, limit })
    : t('keys.concurrencyInUseUnlimited', { current })
}

// hiddenCompositeGroupCount reports how many mappings remain outside the inline row preview.
const hiddenCompositeGroupCount = (row: ApiKey) => {
  return (row.composite_groups?.length ?? 0) - visibleCompositeGroups(row).length
}

// allColumns supplies localized table definitions; the visibility configuration below
// retains fixed action and name columns while allowing other columns to be hidden.
const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('common.name'), sortable: true },
  { key: 'id', label: t('keys.id'), sortable: true },
  { key: 'key', label: t('keys.apiKey'), sortable: false },
  { key: 'group', label: t('keys.group'), sortable: false },
  { key: 'usage', label: t('keys.usage'), sortable: true },
  { key: 'rate_limit', label: t('keys.rateLimitColumn'), sortable: false },
  { key: 'expires_at', label: t('keys.expiresAt'), sortable: true },
  { key: 'status', label: t('common.status'), sortable: true },
  { key: 'last_used_at', label: t('keys.lastUsedAt'), sortable: true },
  { key: 'last_used_ip', label: t('keys.lastUsedIP'), sortable: false },
  { key: 'created_at', label: t('keys.created'), sortable: true },
  { key: 'actions', label: t('common.actions'), sortable: false },
])

// Column preference constants define fixed columns, first-visit defaults, storage keys, and
// versioned additions to the default-hidden set for returning users.
const ALWAYS_VISIBLE_COLUMNS = new Set(['name', 'actions'])
const DEFAULT_HIDDEN_COLUMNS = ['id', 'rate_limit', 'last_used_at', 'last_used_ip']
const HIDDEN_COLUMNS_KEY = 'api-key-hidden-columns'
const COLUMN_SETTINGS_VERSION_KEY = 'api-key-column-settings-version'
const COLUMN_SETTINGS_VERSION = 3
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['last_used_ip'],
  3: ['id'],
}

// toggleableColumns excludes mandatory identity and action columns; hiddenColumns holds the
// current in-memory preference set consumed by the table.
const toggleableColumns = computed(() =>
  allColumns.value.filter((column) => !ALWAYS_VISIBLE_COLUMNS.has(column.key)),
)
const hiddenColumns = reactive<Set<string>>(new Set())

// loadSavedColumns restores valid hidden-column preferences and applies versioned defaults
// for newly introduced low-frequency fields. Mandatory columns stay visible and unknown
// saved keys are removed from the in-memory set. A missing preference starts with default
// hidden columns; older versions add only their documented migrations before persistence.
// Invalid storage or parsing reports an error and adds default hidden fields, keeping the
// table usable when preferences cannot be restored.
const loadSavedColumns = () => {
  hiddenColumns.clear()
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    const validColumnKeys = new Set(allColumns.value.map((column) => column.key))
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed
        .filter(
          (key) =>
            typeof key === 'string' && validColumnKeys.has(key) && !ALWAYS_VISIBLE_COLUMNS.has(key),
        )
        .forEach((key) => hiddenColumns.add(key))
      const rawStoredVersion = Number(localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? '1')
      // Treat invalid stored versions as version one so the defined hidden-column migrations still apply to returning users with unusable preference metadata.
      const storedVersion =
        Number.isInteger(rawStoredVersion) && rawStoredVersion >= 1 ? rawStoredVersion : 1
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
    // Restore default hidden columns after storage or parsing failure so table
    // presentation remains usable even when saved preferences cannot be read.
    console.error('Failed to load API key table columns:', error)
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

// saveColumnsToStorage persists the current hidden set and settings version. Storage failure
// is logged while the reactive preference remains active for the current page, so column
// toggles still affect the visible table.
const saveColumnsToStorage = () => {
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
  } catch (error) {
    // Keep the current reactive column choices after persistence failure;
    // logging the error lets the user continue working with this page's layout.
    console.error('Failed to save API key table columns:', error)
  }
}

// toggleColumn changes visibility for a nonmandatory column and saves the resulting
// preference. Name and action columns are ignored because the table requires them for record
// identity and operations.
const toggleColumn = (key: string) => {
  if (ALWAYS_VISIBLE_COLUMNS.has(key)) return
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
}

// isColumnVisible reports whether the current hidden-column set excludes this key.
const isColumnVisible = (key: string) => !hiddenColumns.has(key)

// columns filters localized definitions using the current preferences while preserving
// mandatory columns and their existing order in the table.
const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (column) => ALWAYS_VISIBLE_COLUMNS.has(column.key) || !hiddenColumns.has(column.key),
  ),
)

// apiKeys and groups retain the current scope's fetched records; formGroups and billing
// subscriptions separately constrain editing choices without narrowing list filters.
const apiKeys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
// formGroups is separate from list-filter groups so a selected subscription narrows form
// bindings while the table retains all visible routing groups.
const formGroups = ref<Group[]>([])
const billingSubscriptions = ref<ApiKeyBillingSubscriptionOption[]>([])
const billingOptionsLoading = ref(false)
const formGroupsLoading = ref(false)
// loading begins before public settings resolve and ends after the first key list response;
// usageLoading tracks its separately fetched usage cells.
const loading = ref(true)
const usageLoading = ref(false)
const submitting = ref(false)
// now and resetTimer update reset countdown labels each minute, while usageStats and
// userGroupRates retain usage observations and account-specific group multipliers.
const now = ref(new Date())
let resetTimer: ReturnType<typeof setInterval> | null = null
const usageStats = ref<Record<string, BatchApiKeyUsageStats>>({})
const userGroupRates = ref<Record<number, number>>({})

// pagination stores the selected page and persisted page size alongside server totals;
// sortState supplies the request ordering for that list.
const pagination = ref({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0,
})
const sortState = ref({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc',
})

// Search, status, and group filters belong to the list request; an empty group value means
// all groups while numeric zero requests ungrouped keys.
const filterSearch = ref('')
const filterStatus = ref('')
const filterGroupId = ref<string | number>('')

// Dialog flags and selected records retain the target for each page operation, separating
// rotation, local CLI import, client import, and general key editing.
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const rotationKey = ref<ApiKey | null>(null)
const rotatedKey = ref<ApiKey | null>(null)
const rotatingKey = ref(false)
const showResetQuotaDialog = ref(false)
const showResetRateLimitDialog = ref(false)
const showUseKeyModal = ref(false)
const showTfCliImportDialog = ref(false)
const showCcsClientSelect = ref(false)
const showColumnDropdown = ref(false)
const pendingCcsRow = ref<ApiKey | null>(null)
const tfImportKey = ref<ApiKey | null>(null)
const selectedKey = ref<ApiKey | null>(null)
// Action-menu and group-selector state retains the row target and viewport position, while
// copiedKeyId marks the latest successful row copy.
const actionMenuKey = ref<ApiKey | null>(null)
const actionMenuPosition = ref<{
  top: number // Viewport menu top pixels.
  left: number // Viewport menu left pixels.
} | null>(null)
const copiedKeyId = ref<number | null>(null)
const groupSelectorKeyId = ref<number | null>(null)
// Public settings control team scope and import visibility; element refs support teleported
// panels and per-key group-button positioning.
const publicSettings = ref<PublicSettings | null>(null)
const teamFeatureEnabled = computed(() => publicSettings.value?.team_enabled !== false)
const dropdownRef = ref<HTMLElement | null>(null)
const columnDropdownRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<{
  // top measures pixels from the viewport top; upward placement omits this
  // anchor and uses bottom to position the panel.
  top?: number
  // bottom measures pixels from the viewport bottom when the positioning helper
  // selects available space above the panel's trigger.
  bottom?: number
  left: number // Viewport panel left pixels.
} | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())
// abortController owns the latest list and usage batch; compositeBindingSequence supplies
// stable draft identities, and formGroupsRequestID orders subscription-specific group
// lookups.
let abortController: AbortController | null = null
let compositeBindingSequence = 0
let formGroupsRequestID = 0

// newCompositeBinding creates a draft mapping with a monotonically increasing local
// identity. Reordering keeps that identity so the editor retains its input elements; prefix
// defaults empty for user confirmation.
const newCompositeBinding = (groupId: number | null = null, prefix = '') => ({
  local_id: ++compositeBindingSequence,
  group_id: groupId,
  prefix,
})

// selectedKeyForGroup resolves the open inline group selector against the current key list,
// returning null when its target row is absent.
const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

// setGroupButtonRef registers a row's actual button element for floating-panel positioning.
// Component instances and null remove the entry so stale elements are not used after table
// rows unmount.
const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

// formData holds the shared create/edit draft; request conversion uses keyForm.ts for
// restrictions while this page owns identity, billing, composite bindings, and status rules.
const formData = ref({
  name: '', // Editable key display name.
  // group_id and composite_groups retain the single-group and prefix-mapping
  // drafts. is_composite selects which submitKeyForm sends and which it omits.
  // Inactive mappings remain available when switching modes. Enabling composite
  // mode adds its first row when the list is empty; disabling an originally
  // composite key clears group_id for a fresh single-group choice.
  group_id: null as number | null, // Single routing-group identity.
  is_composite: false, // Prefix-routing mode enabled flag.
  composite_groups: [] as Array<ReturnType<typeof newCompositeBinding>>, // Stable prefix-to-group draft rows.
  status: 'active' as 'active' | 'inactive', // Editable active/inactive selection.
  fast_mode_policy: 'follow_request' as ApiKeyFastModePolicy, // Request Fast-mode override policy.
  // billing_mode selects how preferred_subscription_id is submitted: subscription
  // mode sends the chosen ID, and other modes send null. Editing omits both
  // fields when their draft values match the selected key's stored billing values.
  billing_mode: 'auto' as ApiKeyBillingMode, // Selected funding strategy.
  preferred_subscription_id: null as number | null, // Chosen subscription identity or null.
  model_mapping_rows: [] as ModelMappingRow[], // Editable source-to-target model rows.
  // use_custom_key controls whether creation submits custom_key. Disabling the
  // switch retains the draft text, while submitKeyForm omits it; updates do not
  // submit custom secrets through these fields.
  use_custom_key: false, // Custom creation secret enabled.
  custom_key: '', // Optional user-chosen creation secret.
  // enable_ip_restriction controls how buildKeyFormPayload consumes ip_whitelist
  // and ip_blacklist. Disabled inputs keep their draft text but submit empty
  // arrays, clearing previously stored address restrictions when editing.
  enable_ip_restriction: false, // Address-filter controls enabled flag.
  ip_whitelist: '', // Newline-separated allowed addresses.
  ip_blacklist: '', // Newline-separated denied addresses.
  // quota alone controls the lifetime cap in buildKeyFormPayload; enable_quota
  // is retained draft state and does not gate conversion. Null and nonpositive
  // quota values submit zero for unlimited usage.
  enable_quota: false, // Retained unused quota switch.
  quota: null as number | null, // Balance-unit lifetime quota input.
  // enable_rate_limit gates rate_limit_5h, rate_limit_1d, and rate_limit_7d during
  // buildKeyFormPayload conversion. Disabled controls retain draft amounts but
  // submit zero for all windows; nonpositive individual amounts also submit zero.
  enable_rate_limit: false, // Spending-window controls enabled flag.
  // concurrency_limit and rpm_limit submit independently of enable_rate_limit.
  // Their inputs remain visible when spending-window controls are disabled;
  // each uses zero for an unlimited request count in its corresponding limit.
  concurrency_limit: 0, // Concurrent request count cap.
  rpm_limit: 0, // Requests per minute cap.
  rate_limit_5h: null as number | null, // Five-hour spending amount cap.
  rate_limit_1d: null as number | null, // One-day spending amount cap.
  rate_limit_7d: null as number | null, // Seven-day spending amount cap.
  // enable_expiration gates expiration_date in buildKeyFormPayload, while
  // expiration_preset identifies the shortcut that fills that date or custom
  // input. Disabling retains the draft date; disabled or empty dates omit
  // creation expiry and submit an empty string to clear expiry when editing.
  enable_expiration: false, // Expiration input enabled flag.
  expiration_preset: '30' as '7' | '30' | '90' | 'custom', // Selected day shortcut or custom.
  expiration_date: '', // Browser-local expiration datetime text.
  fallback_when_group_unavailable: true, // Group-unavailable routing fallback flag.
})

// MODEL_REDIRECT_MAX_RULES bounds the redirect editor before submission so the page rejects
// oversized mapping lists alongside per-row validation errors.
const MODEL_REDIRECT_MAX_RULES = 100

// modelMappingIssues applies the shared case-sensitive redirect rules, including trailing-
// wildcard and length constraints, to the current editable mapping rows.
const modelMappingIssues = computed(() =>
  validateModelMappingRows(formData.value.model_mapping_rows, KEY_REDIRECT_RULES),
)

// modelMappingFieldErrors localizes each row issue for its source and destination inputs
// while modelMappingFormError selects the first submission-blocking error.
const modelMappingFieldErrors = computed(() =>
  modelMappingIssues.value.map((issue) => ({
    from: issue.from ? t(`keys.modelRedirect.${issue.from}`) : undefined,
    to: issue.to ? t(`keys.modelRedirect.${issue.to}`) : undefined,
  })),
)

const modelMappingFormError = computed(() => {
  if (formData.value.model_mapping_rows.length > MODEL_REDIRECT_MAX_RULES) {
    return t('keys.modelRedirect.tooManyRules')
  }
  const issue = firstModelMappingIssue(modelMappingIssues.value)
  return issue ? t(`keys.modelRedirect.${issue}`) : ''
})

// customKeyError validates an enabled nonempty custom secret for minimum length and allowed
// characters; required-input handling remains in handleSubmit.
const customKeyError = computed(() => {
  if (!formData.value.use_custom_key || !formData.value.custom_key) {
    return ''
  }
  const key = formData.value.custom_key
  if (key.length < 16) {
    return t('keys.customKeyTooShort')
  }
  // Custom secrets allow ASCII letters, digits, underscores, and hyphens.
  if (!/^[a-zA-Z0-9_-]+$/.test(key)) {
    return t('keys.customKeyInvalidChars')
  }
  return ''
})

// fastModePolicyOptions offers request-following, forced-on, and forced-off policies while
// the shared Select component owns keyboard and popup interaction.
const fastModePolicyOptions = computed(() => [
  { value: 'follow_request', label: t('keys.fastModePolicy.followRequest') },
  { value: 'force_on', label: t('keys.fastModePolicy.forceOn') },
  { value: 'force_off', label: t('keys.fastModePolicy.forceOff') },
])

// billingModeOptions retains auto alongside subscription and balance choices so existing
// keys can display and restore automatic billing before server validation.
const billingModeOptions = computed(() => [
  { value: 'auto', label: t('keys.billing.modes.auto') },
  { value: 'subscription', label: t('keys.billing.modes.subscription') },
  { value: 'balance', label: t('keys.billing.modes.balance') },
])

// billingSubscriptionOptions presents eligible subscription records and retains an
// unavailable selected ID as a disabled entry so editing does not silently replace its
// identity.
const billingSubscriptionOptions = computed<
  Array<{
    value: number /* Subscription numeric identity. */
    label: string /* Plan name and subscription ID. */
    description?: string /* Optional group restriction summary. */
    disabled?: boolean /* Unavailable selection disabled flag. */
  }>
>(() => {
  const options: Array<{
    value: number /* Subscription numeric identity. */
    label: string /* Plan name and subscription ID. */
    description?: string /* Optional group restriction summary. */
    disabled?: boolean /* Unavailable selection disabled flag. */
  }> = billingSubscriptions.value.map((subscription) => ({
    value: subscription.id,
    label: `${subscription.plan_name} #${subscription.id}`,
    description: subscription.groups_restricted
      ? t('keys.billing.groupsRestricted')
      : t('keys.billing.allGroups'),
  }))
  const selectedID = formData.value.preferred_subscription_id
  if (selectedID && !options.some((option) => option.value === selectedID)) {
    options.unshift({
      value: selectedID,
      label: t('keys.billing.unavailableSubscription', { id: selectedID }),
      disabled: true,
    })
  }
  return options
})

// shouldSubmitEditStatus decides whether an ordinary update may include the editable active
// or inactive value. A team-owner lock prohibits every status write even though unrelated
// fields remain editable. Quota-exhausted and expired server states collapse to inactive in
// the form, so leaving that display untouched must not overwrite their terminal reason. Only
// an explicit active choice submits status for those records, allowing the server to
// evaluate reactivation.
const shouldSubmitEditStatus = (key: ApiKey, status: 'active' | 'inactive') => {
  // An owner lock still permits unrelated edits, but status must be omitted so an ordinary update cannot bypass the owner's disable decision.
  if (key.team_owner_disabled) return false
  if (key.status === 'quota_exhausted' || key.status === 'expired') {
    return status === 'active'
  }
  return true
}

// List filter options include unrestricted choices, numeric-zero ungrouped selection,
// visible groups, and server key statuses used by the list endpoint.
const groupFilterOptions = computed(() => [
  { value: '', label: t('keys.allGroups') },
  { value: 0, label: t('keys.noGroup') },
  ...groups.value.map((g) => ({ value: g.id, label: g.name })),
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('keys.allStatus') },
  { value: 'active', label: t('keys.status.active') },
  { value: 'inactive', label: t('keys.status.inactive') },
  { value: 'disabled', label: t('keys.status.disabled') },
  { value: 'quota_exhausted', label: t('keys.status.quota_exhausted') },
  { value: 'expired', label: t('keys.status.expired') },
])

// activeFilterCount counts selected group and status filters for the toolbar badge,
// excluding the separate text-search field from that count.
const activeFilterCount = computed(
  () => [filterGroupId.value !== '', filterStatus.value !== ''].filter(Boolean).length,
)

// resetKeyFilters clears status and group conditions and reloads the first list page.
const resetKeyFilters = () => {
  filterGroupId.value = ''
  filterStatus.value = ''
  pagination.value.page = 1
  loadApiKeys()
}

// onFilterChange resets pagination before reloading keys for the current filter values.
const onFilterChange = () => {
  pagination.value.page = 1
  loadApiKeys()
}

// onGroupFilterChange stores the group choice and reloads the first filtered page.
const onGroupFilterChange = (value: string | number | boolean | null) => {
  filterGroupId.value = value as string | number
  onFilterChange()
}

// onStatusFilterChange stores the status choice and reloads the first filtered page.
const onStatusFilterChange = (value: string | number | boolean | null) => {
  filterStatus.value = value as string
  onFilterChange()
}

// buildGroupOptions adds display branding and account-specific rates to selectable routing
// groups.
const buildGroupOptions = (source: Group[]) =>
  source.map((group) => ({
    value: group.id,
    label: group.display_name || group.name,
    search_terms: group.search_terms,
    description: group.description,
    displayBrand: group.display_brand?.trim() || null,
    rate: group.rate_multiplier,
    userRate: userGroupRates.value[group.id] ?? null,
  }))

// formGroupOptions uses server-approved form groups while allGroupOptions retains the
// broader list for filtering and ordinary inline group selection.
const formGroupOptions = computed(() => buildGroupOptions(formGroups.value))
const allGroupOptions = computed(() => buildGroupOptions(groups.value))

// onCompositeModeChange switches between single-group and prefix-mapped form
// representations. Enabling creates an initial mapping from the current group when needed,
// keeping its prefix empty for the user. Disabling an originally composite key clears the
// single group so it must be selected; other drafts can recover their first mapping's group
// when none is already selected. Existing mapping objects remain available for later mode
// switches.
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

// addCompositeBinding adds an empty mapping while the draft contains fewer than twenty rows.
// Its stable local ID is assigned by newCompositeBinding for later editor movement and field
// retention.
const addCompositeBinding = () => {
  if (formData.value.composite_groups.length >= 20) return
  formData.value.composite_groups.push(newCompositeBinding())
}

// removeCompositeBinding removes the requested row while retaining at least one mapping. The
// caller supplies a current editor index so removal targets the row displayed by that
// action.
const removeCompositeBinding = (index: number) => {
  if (formData.value.composite_groups.length <= 1) return
  formData.value.composite_groups.splice(index, 1)
}

// moveCompositeBinding relocates a mapping while preserving its object identity and local
// ID. Out-of-range target positions are ignored; the editor supplies the current source
// index for the requested move.
const moveCompositeBinding = (index: number, target: number) => {
  if (target < 0 || target >= formData.value.composite_groups.length) return
  const [binding] = formData.value.composite_groups.splice(index, 1)
  if (binding) formData.value.composite_groups.splice(target, 0, binding)
}

// compositeBindingError returns the first validation issue for a draft mapping. It requires
// a selected group and a trimmed valid prefix, then rejects case-insensitive duplicate
// prefixes and duplicate group identities across other rows.
const compositeBindingError = (index: number) => {
  const binding = formData.value.composite_groups[index]
  if (!binding?.group_id) return t('keys.composite.groupRequired')
  const prefix = binding.prefix.trim()
  if (!prefix) return t('keys.composite.prefixRequired')
  if (!/^[A-Za-z0-9_-]{1,32}$/.test(prefix)) return t('keys.composite.prefixInvalid')
  const duplicatePrefix = formData.value.composite_groups.some(
    (item, itemIndex) =>
      itemIndex !== index && item.prefix.trim().toLowerCase() === prefix.toLowerCase(),
  )
  if (duplicatePrefix) return t('keys.composite.prefixDuplicate')
  const duplicateGroup = formData.value.composite_groups.some(
    (item, itemIndex) => itemIndex !== index && item.group_id === binding.group_id,
  )
  if (duplicateGroup) return t('keys.composite.groupDuplicate')
  return ''
}

// compositeFormError validates mapping count and row rules only in composite mode, allowing
// the inactive mapping draft to remain while editing a single-group key.
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

// groupSearchQuery filters the inline selector across search terms, display labels,
// branding, and descriptions while retaining all options for an empty query.
const groupSearchQuery = ref('')
const filteredGroupOptions = computed(() => {
  const query = groupSearchQuery.value.trim().toLowerCase()
  if (!query) return allGroupOptions.value
  return allGroupOptions.value.filter((opt) => {
    return (
      opt.search_terms?.some((text) => text.toLowerCase().includes(query)) ||
      opt.label.toLowerCase().includes(query) ||
      (opt.displayBrand && opt.displayBrand.toLowerCase().includes(query)) ||
      (opt.description && opt.description.toLowerCase().includes(query))
    )
  })
})

// pruneFormGroupBindings clears a disallowed single group and removes composite bindings
// outside the supplied allowed set. Subscription changes use it to prevent submitting
// mappings no longer covered by the selected plan.
const pruneFormGroupBindings = (allowedGroupIDs: Set<number>) => {
  if (formData.value.group_id !== null && !allowedGroupIDs.has(formData.value.group_id)) {
    formData.value.group_id = null
  }
  formData.value.composite_groups = formData.value.composite_groups.filter(
    (binding) => binding.group_id !== null && allowedGroupIDs.has(binding.group_id),
  )
}

// onBillingModeChange normalizes the selected billing mode and clears a preferred
// subscription. Automatic or balance mode reloads ordinary form groups; subscription mode
// starts empty until a subscription is chosen.
const onBillingModeChange = (value: string | number | boolean | null) => {
  const mode: ApiKeyBillingMode = value === 'subscription' || value === 'balance' ? value : 'auto'
  formData.value.billing_mode = mode
  if (mode !== 'subscription') {
    formData.value.preferred_subscription_id = null
    void loadFormGroups()
    return
  }

  // Wait for a selected subscription before offering its groups; compatible existing mappings are pruned when that selection supplies its allowed intersection.
  formData.value.preferred_subscription_id = null
  formGroups.value = []
}

// onPreferredSubscriptionChange records a positive subscription ID and prunes known
// restricted groups immediately. It then requests the server-approved form groups, which can
// further restrict bindings using the current payer permissions.
const onPreferredSubscriptionChange = (value: string | number | boolean | null) => {
  const subscriptionID = typeof value === 'number' && value > 0 ? value : null
  formData.value.preferred_subscription_id = subscriptionID
  const subscription = billingSubscriptions.value.find((item) => item.id === subscriptionID)
  if (subscription?.groups_restricted) {
    pruneFormGroupBindings(new Set(subscription.applicable_groups))
  }
  void loadFormGroups()
}

// maskKey abbreviates long secrets to their leading eight and trailing four characters.
// Secrets of twelve characters or fewer remain visible because no middle segment can be
// hidden by that format.
const maskKey = (key: string): string => {
  if (key.length <= 12) return key
  return `${key.slice(0, 8)}...${key.slice(-4)}`
}

// copyToClipboard copies a secret and marks its row after browser success. Each successful
// call schedules a reset independently, so an earlier timeout can clear feedback from a
// later copy.
const copyToClipboard = async (text: string, keyId: number) => {
  const success = await clipboardCopy(text, t('keys.copied'))
  if (success) {
    copiedKeyId.value = keyId
    setTimeout(() => {
      copiedKeyId.value = null
    }, COPY_FEEDBACK_MS)
  }
}

// isAbortError recognizes browser aborts and Axios cancellation codes among request errors.
const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const { name, code } = error as {
    name?: string // Browser exception category name.
    code?: string // HTTP-client cancellation category code.
  }
  return name === 'AbortError' || code === 'ERR_CANCELED'
}

// loadApiKeys replaces the current list request and clears usage cells from the prior page.
// The new AbortController is shared with its later usage query, so another list load cancels
// both phases together. A successful list publishes rows and pagination before usage
// finishes, allowing the table to become interactive while cells populate. Cancellation
// remains silent, and only the owning controller clears loading during completion.
const loadApiKeys = async () => {
  // Cancel the prior list and its usage batch before assigning a new owner,
  // preventing those requests from publishing after the filter or page changes.
  abortController?.abort()
  const controller = new AbortController()
  abortController = controller
  const { signal } = controller
  loading.value = true
  // Clear prior-page usage immediately when replacing a list request so a cancellation race cannot attach old amounts to newly displayed keys.
  usageLoading.value = false
  usageStats.value = {}
  try {
    // Build the request from selected list filters and ordering.
    const filters: {
      search?: string /* Optional name-or-secret search text. */
      status?: string /* Selected server status filter. */
      group_id?:
        | number
        | string /* Group identity; numeric zero selects keys without a group, while omission leaves the group unrestricted. */
      sort_by?: string /* Selected sortable field identity. */
      sort_order?: 'asc' | 'desc' /* Requested ascending/descending order. */
      scope?: DataScope /* Personal or team ownership. */
    } = {}
    if (filterSearch.value) filters.search = filterSearch.value
    if (filterStatus.value) filters.status = filterStatus.value
    if (filterGroupId.value !== '') filters.group_id = filterGroupId.value
    filters.sort_by = sortState.value.sort_by
    filters.sort_order = sortState.value.sort_order
    filters.scope = scope.value

    const response = await keysAPI.list(
      pagination.value.page,
      pagination.value.page_size,
      filters,
      {
        signal,
      },
    )
    // An aborted list can still settle after replacement; reject its rows before
    // publishing pagination or starting a usage request for that obsolete page.
    if (signal.aborted) return
    apiKeys.value = response.items
    pagination.value.total = response.total
    pagination.value.pages = response.pages
    // Publish the key list before usage arrives so table actions are usable while independently loaded usage cells retain their own loading indicators.
    loading.value = false
    void loadUsageStats(response.items, controller)
  } catch (error) {
    // Replacing a request cancels its transport intentionally, so keep that
    // completion quiet instead of presenting it as a failed key-list load.
    if (isAbortError(error)) {
      return
    }
    appStore.showError(t('keys.failedToLoad'))
  } finally {
    // Only this controller's completion can clear the shared list spinner;
    // a superseding load still needs its own visible loading state.
    if (abortController === controller) {
      loading.value = false
    }
  }
}

// loadUsageStats fetches usage for the supplied key rows under their list controller. Empty
// lists finish immediately. Abortion or a replacement controller prevents response
// publication, and completion clears the usage spinner only while that batch still owns the
// page. Noncancellation failure logs the cause and leaves the cleared usage map, so stale
// usage from another page cannot remain attached to newly listed keys.
const loadUsageStats = async (items: ApiKey[], controller: AbortController) => {
  usageStats.value = {}
  if (items.length === 0) {
    usageLoading.value = false
    return
  }
  usageLoading.value = true
  try {
    const keyIds = items.map((key) => key.id)
    const usageResponse = await usageAPI.getDashboardApiKeysUsage(keyIds, {
      signal: controller.signal,
    })
    // Usage belongs to the list controller that selected these IDs; reject
    // cancelled or superseded batches before attaching amounts to current rows.
    if (controller.signal.aborted || abortController !== controller) return
    usageStats.value = usageResponse.stats
  } catch (error) {
    // Cancellation is expected during list replacement. Other failures remain
    // logged while the cleared usage map prevents old amounts from reappearing.
    if (!isAbortError(error)) {
      console.error('Failed to load usage stats:', error)
    }
  } finally {
    // A replacement controller owns its usage spinner independently, so an
    // older batch finishing cannot hide that replacement's pending usage request.
    if (abortController === controller) {
      usageLoading.value = false
    }
  }
}

// loadGroups fetches groups visible to the selected ownership scope. Automatic and balance
// forms reuse them, while subscription forms retain their separately filtered list; failures
// log the error and keep previous groups.
const loadGroups = async () => {
  try {
    const available = await userGroupsAPI.getAvailable(scope.value)
    groups.value = available
    // Automatic and balance modes reuse the visible scope groups; subscription mode keeps its separately restricted choices when the general list reloads.
    if (formData.value.billing_mode !== 'subscription') {
      formGroups.value = available
    }
  } catch (error) {
    // Retain the last successful group choices when retrieval fails, allowing
    // existing list labels and automatic or balance drafts to remain populated.
    console.error('Failed to load groups:', error)
  }
}

// loadFormGroups updates edit choices for the current billing selection using an independent
// request sequence. Automatic and balance modes reuse the general group list; subscription
// mode requires a chosen subscription before requesting its allowed intersection. Only the
// latest lookup publishes or clears loading. A successful response prunes incompatible
// bindings, while a latest failure clears choices and logs the error, preventing unavailable
// subscription groups from being offered.
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
    // Subscription changes and dialog closure invalidate this sequence; discard
    // the obsolete result before pruning bindings in a later form draft.
    if (requestID !== formGroupsRequestID) return
    formGroups.value = available
    pruneFormGroupBindings(new Set(available.map((group) => group.id)))
  } catch (error) {
    // A current failed lookup clears its choices so unavailable subscription
    // groups cannot remain selectable; stale failures leave the newer form intact.
    if (requestID === formGroupsRequestID) {
      formGroups.value = []
    }
    console.error('Failed to load API key form groups:', error)
  } finally {
    // Keep a newer lookup's loading state until that lookup finishes;
    // completion of an obsolete sequence cannot release its spinner.
    if (requestID === formGroupsRequestID) {
      formGroupsLoading.value = false
    }
  }
}

// loadBillingOptions loads subscription choices for the current ownership scope. Failure
// clears the candidate list and logs the cause, while the computed options retain an
// unavailable currently selected ID for display.
const loadBillingOptions = async () => {
  billingOptionsLoading.value = true
  try {
    billingSubscriptions.value = await keysAPI.getBillingOptions(scope.value)
  } catch (error) {
    // Remove stale candidates after retrieval failure; the options computation
    // still shows an unavailable selected ID so its stored identity remains visible.
    billingSubscriptions.value = []
    console.error('Failed to load API key billing options:', error)
  } finally {
    billingOptionsLoading.value = false
  }
}

// loadUserGroupRates loads account-specific group multipliers for the selected scope.
// Request failure logs the cause and retains previously loaded rates until another
// successful response replaces them.
const loadUserGroupRates = async () => {
  try {
    userGroupRates.value = await userGroupsAPI.getUserGroupRates(scope.value)
  } catch (error) {
    // Retain the last loaded account rates when refresh fails, so displayed
    // multipliers continue using that snapshot until a later successful request.
    console.error('Failed to load user group rates:', error)
  }
}

// loadPublicSettings retrieves feature and endpoint settings used by key actions. A disabled
// team feature switches an active team scope to personal; failed settings retrieval leaves
// current defaults in place.
const loadPublicSettings = async () => {
  try {
    publicSettings.value = await authAPI.getPublicSettings()
    if (!teamFeatureEnabled.value && scope.value === 'team') scope.value = 'personal'
  } catch (error) {
    // Leave settings and derived visibility defaults in place when retrieval
    // fails, allowing key actions to continue using their current endpoint configuration.
    console.error('Failed to load public settings:', error)
  }
}

// openUseKeyModal selects the key and opens its generated client setup instructions.
const openUseKeyModal = (key: ApiKey) => {
  selectedKey.value = key
  showUseKeyModal.value = true
}

// closeUseKeyModal hides client instructions and releases their selected key record.
const closeUseKeyModal = () => {
  showUseKeyModal.value = false
  selectedKey.value = null
}

// openTfCliImportDialog selects the key for local TokenFlux CLI discovery and import.
const openTfCliImportDialog = (key: ApiKey) => {
  tfImportKey.value = key
  showTfCliImportDialog.value = true
}

// closeTfCliImportDialog closes CLI import and releases its selected key record.
const closeTfCliImportDialog = () => {
  showTfCliImportDialog.value = false
  tfImportKey.value = null
}

// handlePageChange selects the requested list page and reloads its key records.
const handlePageChange = (page: number) => {
  pagination.value.page = page
  loadApiKeys()
}

// handlePageSizeChange stores the page size and reloads the first key page.
const handlePageSizeChange = (pageSize: number) => {
  pagination.value.page_size = pageSize
  pagination.value.page = 1
  loadApiKeys()
}

// handleSort selects list ordering and reloads keys from the first page.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.value.sort_by = key
  sortState.value.sort_order = order
  pagination.value.page = 1
  loadApiKeys()
}

// openCreateModal opens the create draft and starts billing and group-choice loading. Those
// loaders handle their own errors, leaving the dialog available while candidate lists
// resolve.
const openCreateModal = () => {
  showCreateModal.value = true
  void Promise.all([loadBillingOptions(), loadFormGroups()])
}

// openCreateFromRoute opens the create dialog when the URL carries create=1, as the global
// search "Create API key" action does, then removes the parameter so reloading the page
// or navigating back does not open the dialog again.
const openCreateFromRoute = () => {
  if (route?.query?.create !== '1') return
  openCreateModal()
  const { create: _create, ...query } = route.query
  void router.replace({ query })
}

// The action can run while this page is already open, which changes only the query on
// the mounted instance; mount handles the first visit after the list loads.
watch(() => route?.query?.create, openCreateFromRoute)

// editKey converts a persisted key into the shared edit draft and loads its billing choices.
// Composite bindings receive stable local IDs, model mappings become sorted rows, and expiry
// uses browser-local datetime text. Server terminal statuses display as inactive, with
// shouldSubmitEditStatus later deciding whether a status write is permitted. Missing
// billing, Fast, and restriction values receive the defaults expected by the form controls
// before it opens.
const editKey = (key: ApiKey) => {
  selectedKey.value = key
  const hasIPRestriction = key.ip_whitelist?.length > 0 || key.ip_blacklist?.length > 0
  const hasExpiration = !!key.expires_at
  formData.value = {
    name: key.name,
    group_id: key.group_id,
    is_composite: key.is_composite ?? false,
    composite_groups: (key.composite_groups || []).map((binding) =>
      newCompositeBinding(binding.group_id, binding.prefix),
    ),
    // Terminal server states display as inactive in this two-state editor; submission separately decides whether a status update is allowed for the persisted state.
    status: key.status === 'active' ? 'active' : 'inactive',
    fast_mode_policy: key.fast_mode_policy ?? 'follow_request',
    billing_mode: key.billing_mode ?? 'auto',
    preferred_subscription_id: key.preferred_subscription_id ?? null,
    model_mapping_rows: Object.entries(key.model_mapping ?? {})
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([from, to]) => ({ from, to })),
    use_custom_key: false,
    custom_key: '',
    enable_ip_restriction: hasIPRestriction,
    ip_whitelist: (key.ip_whitelist || []).join('\n'),
    ip_blacklist: (key.ip_blacklist || []).join('\n'),
    enable_quota: key.quota > 0,
    quota: key.quota > 0 ? key.quota : null,
    enable_rate_limit: key.rate_limit_5h > 0 || key.rate_limit_1d > 0 || key.rate_limit_7d > 0,
    concurrency_limit: key.concurrency_limit ?? 0,
    rpm_limit: key.rpm_limit ?? 0,
    rate_limit_5h: key.rate_limit_5h || null,
    rate_limit_1d: key.rate_limit_1d || null,
    rate_limit_7d: key.rate_limit_7d || null,
    enable_expiration: hasExpiration,
    expiration_preset: 'custom',
    expiration_date: key.expires_at ? formatDateTimeLocal(key.expires_at) : '',
    fallback_when_group_unavailable: key.fallback_when_group_unavailable ?? false,
  }
  formGroups.value = []
  showEditModal.value = true
  void Promise.all([loadBillingOptions(), loadFormGroups()])
}

// onStatusToggle updates the editable active or inactive value unless a team-owner lock
// prohibits status changes. The final request still checks shouldSubmitEditStatus before
// including that field.
const onStatusToggle = (enabled: boolean) => {
  if (selectedKey.value?.team_owner_disabled) return
  formData.value.status = enabled ? 'active' : 'inactive'
}

// toggleKeyStatus switches an ordinary row between active and inactive and refreshes the
// list after success. Team-owner-disabled rows reject the action locally, including calls
// outside the hidden template button.
const toggleKeyStatus = async (key: ApiKey) => {
  // Enforce the owner lock in the handler as well as the template so another invocation path cannot accidentally submit a reactivation request.
  if (key.team_owner_disabled) {
    appStore.showError(t('keys.teamOwnerDisabledHint'))
    return
  }
  const newStatus = key.status === 'active' ? 'inactive' : 'active'
  try {
    await keysAPI.toggleStatus(key.id, newStatus)
    appStore.showSuccess(
      newStatus === 'active' ? t('keys.keyEnabledSuccess') : t('keys.keyDisabledSuccess'),
    )
    loadApiKeys()
  } catch {
    appStore.showError(t('keys.failedToUpdateStatus'))
  }
}

// openKeyActionMenu positions the selected row menu in viewport coordinates for teleporting
// outside clipped table cells. Repeated activation of the same row closes it, while menu
// height includes the optional client-import action.
const openKeyActionMenu = (key: ApiKey, event: MouseEvent) => {
  if (actionMenuKey.value?.id === key.id) {
    closeKeyActionMenu()
    return
  }
  const target = event.currentTarget as HTMLElement | null
  if (!target) return
  const rect = target.getBoundingClientRect()
  // The menu's fixed height includes optional import actions. Positioning can flip the complete panel upward while keeping its trailing edge aligned on narrow screens.
  const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, {
    maxWidth: 192,
    fixedHeight: publicSettings.value?.hide_ccs_import_button ? 178 : 218,
    viewportPadding: 8,
    gap: 4,
    pinLeftOnMobile: false,
  })
  // Fixed-height positioning supplies a top coordinate for the teleported menu.
  actionMenuPosition.value = { top: position.top ?? 8, left: position.left }
  actionMenuKey.value = key
}

// closeKeyActionMenu releases the action target and its viewport positioning state.
const closeKeyActionMenu = () => {
  actionMenuKey.value = null
  actionMenuPosition.value = null
}

// openGroupSelector opens an inline selector for ordinary non-subscription keys and routes
// other keys into the full editor. It positions the panel from the registered row button and
// clears search on a newly selected row.
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
      // Estimate a comfortable group-panel height before choosing its side, and allow the positioning helper to use near-full viewport width on narrow screens.
      const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, {
        align: 'left',
        maxWidth: 380,
        viewportPadding: 8,
        gap: 4,
        maxHeightRatio: 1,
        minComfortableHeight: 400,
      })
      if (position.bottom !== null) {
        // Anchor upward when the positioning helper selects space above the trigger.
        dropdownPosition.value = { bottom: position.bottom, left: position.left }
      } else {
        // Otherwise use the helper's downward top coordinate.
        dropdownPosition.value = { top: position.top ?? undefined, left: position.left }
      }
    }
    groupSelectorKeyId.value = key.id
    groupSearchQuery.value = ''
  }
}

// submitGroupChange saves a key's single-group assignment and starts a list refresh after
// success. Request failures propagate to changeGroup, which owns the user-facing error
// notification for this action.
const submitGroupChange = async (key: ApiKey, newGroupId: number | null) => {
  await keysAPI.update(key.id, { group_id: newGroupId })
  appStore.showSuccess(t('keys.groupChangedSuccess'))
  loadApiKeys()
}

// changeGroup closes the inline selector before saving a different group assignment.
// Selecting the existing group skips the request, while failed updates report a localized
// error and leave server state authoritative.
const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
  if (key.group_id === newGroupId) return

  try {
    await submitGroupChange(key, newGroupId)
  } catch {
    appStore.showError(t('keys.failedToChangeGroup'))
  }
}

// closeGroupSelector dismisses group and column menus when a document click falls outside
// their controls. Group trigger ancestors count as inside so their click handlers can manage
// their own open state.
const closeGroupSelector = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  // Treat group triggers and panel contents as internal clicks while independently dismissing column settings when the click leaves its own root.
  if (!target.closest('.group\\/dropdown') && !dropdownRef.value?.contains(target)) {
    groupSelectorKeyId.value = null
    dropdownPosition.value = null
  }
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
}

// confirmDelete selects the key and opens its deletion confirmation dialog.
const confirmDelete = (key: ApiKey) => {
  selectedKey.value = key
  showDeleteDialog.value = true
}

// confirmRotate selects the key whose secret will require rotation confirmation.
const confirmRotate = (key: ApiKey) => {
  rotationKey.value = key
}

// cancelRotate releases the rotation target when no rotation request is active. The current
// target remains while busy so request completion can publish the returned secret
// consistently.
const cancelRotate = () => {
  if (!rotatingKey.value) rotationKey.value = null
}

// handleRotate replaces the confirmed key secret and publishes it before refreshing the
// list. It updates matching row and selected-key records, clears old copy feedback, and
// opens the returned-secret result. Publishing first keeps copy operations on the newly
// issued credential even if a later list refresh fails. Missing targets or an active
// rotation skip submission, and the rotation guard resets after the mutation settles.
const handleRotate = async () => {
  if (!rotationKey.value || rotatingKey.value) return
  rotatingKey.value = true
  try {
    const updated = await keysAPI.rotate(rotationKey.value.id)
    // Publish the returned credential before the asynchronous list refresh so copying uses the new secret even if that later refresh fails to replace rows.
    apiKeys.value = apiKeys.value.map((key) =>
      key.id === updated.id ? { ...key, key: updated.key, updated_at: updated.updated_at } : key,
    )
    if (selectedKey.value?.id === updated.id) selectedKey.value = updated
    if (copiedKeyId.value === updated.id) copiedKeyId.value = null
    rotationKey.value = null
    rotatedKey.value = updated
    void loadApiKeys()
  } catch (error: unknown) {
    const message =
      error instanceof Error
        ? error.message
        : (error as { message?: string /* Optional rotation failure explanation. */ } | null)
            ?.message
    appStore.showError(message || t('keys.failedToRotate'))
  } finally {
    rotatingKey.value = false
  }
}

// submitKeyForm converts validated restriction inputs and submits either a create or update
// payload. Editing sends billing fields only when the mode or preferred subscription
// changed, and includes status only when server-state rules permit it. Composite mappings
// submit trimmed prefixes and suppress the single-group field. Creation carries ownership
// scope and optional custom secret, then advances onboarding only after success at the
// matching submission step. Successful requests close and reset the draft before refreshing
// the list.
const submitKeyForm = async () => {
  const { ipWhitelist, ipBlacklist, quota, expiresInDays, expiresAt, rateLimitData, modelMapping } =
    buildKeyFormPayload(formData.value, showEditModal.value)
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
              prefix: binding.prefix.trim(),
            }))
          : undefined,
        fast_mode_policy: formData.value.fast_mode_policy,
        model_mapping: modelMapping,
        ip_whitelist: ipWhitelist,
        ip_blacklist: ipBlacklist,
        quota: quota,
        expires_at: expiresAt,
        concurrency_limit: Number(formData.value.concurrency_limit || 0),
        rpm_limit: Number(formData.value.rpm_limit || 0),
        rate_limit_5h: rateLimitData.rate_limit_5h,
        rate_limit_1d: rateLimitData.rate_limit_1d,
        rate_limit_7d: rateLimitData.rate_limit_7d,
        fallback_when_group_unavailable: formData.value.fallback_when_group_unavailable,
      }
      const originalBillingMode = selectedKey.value.billing_mode ?? 'auto'
      const originalPreferredSubscriptionID = selectedKey.value.preferred_subscription_id ?? null
      if (
        formData.value.billing_mode !== originalBillingMode ||
        formData.value.preferred_subscription_id !== originalPreferredSubscriptionID
      ) {
        updates.billing_mode = formData.value.billing_mode
        updates.preferred_subscription_id =
          formData.value.billing_mode === 'subscription'
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
              prefix: binding.prefix.trim(),
            }))
          : undefined,
        fast_mode_policy: formData.value.fast_mode_policy,
        billing_mode: formData.value.billing_mode,
        preferred_subscription_id:
          formData.value.billing_mode === 'subscription'
            ? formData.value.preferred_subscription_id
            : null,
        model_mapping: modelMapping,
        custom_key: customKey,
        ip_whitelist: ipWhitelist,
        ip_blacklist: ipBlacklist,
        quota,
        expires_in_days: expiresInDays,
        concurrency_limit: Number(formData.value.concurrency_limit || 0),
        rpm_limit: Number(formData.value.rpm_limit || 0),
        rate_limit_5h: rateLimitData.rate_limit_5h,
        rate_limit_1d: rateLimitData.rate_limit_1d,
        rate_limit_7d: rateLimitData.rate_limit_7d,
        fallback_when_group_unavailable: formData.value.fallback_when_group_unavailable,
      }
      await keysAPI.createWithPayload(payload)
      appStore.showSuccess(t('keys.keyCreatedSuccess'))
      // Advance onboarding only after a successful create response and while its active step still points to this submit control.
      if (onboardingStore.isCurrentStep('[data-tour="key-form-submit"]')) {
        onboardingStore.nextStep(500)
      }
    }
    closeModals()
    loadApiKeys()
  } catch (error: any) {
    const errorMsg =
      error?.reason === 'API_KEY_LIMIT_REACHED'
        ? t('keys.apiKeyLimitReached', {
            current: error?.metadata?.current ?? '?',
            limit: error?.metadata?.limit ?? '?',
          })
        : error?.message || t('keys.failedToSave')
    appStore.showError(errorMsg)
    // A rejected create leaves onboarding at the submission step for correction.
  } finally {
    submitting.value = false
  }
}

// onScopeChange returns to the first page and clears the group filter before reloading
// scope-dependent keys, groups, rates, and billing options. Other filter values remain
// available across the ownership switch.
const onScopeChange = () => {
  pagination.value.page = 1
  filterGroupId.value = ''
  void Promise.all([
    loadApiKeys(),
    loadGroups(),
    loadUserGroupRates(),
    loadBillingOptions(),
    loadFormGroups(),
  ])
}

// handleSubmit validates integer request limits, model redirects, composite mappings,
// billing selection, and required group identity before submission. Custom-secret checks
// apply only to creation; accepted drafts delegate payload conversion and persistence to
// submitKeyForm.
const handleSubmit = async () => {
  // Empty numeric controls submit zero; other request caps must fit the nonnegative signed 32-bit integer range accepted by persistence.
  const requestLimits = [formData.value.concurrency_limit, formData.value.rpm_limit]
  if (
    requestLimits.some(
      (value) =>
        !Number.isInteger(Number(value || 0)) ||
        Number(value || 0) < 0 ||
        Number(value || 0) > 2147483647,
    )
  ) {
    appStore.showError(t('keys.requestLimitsInvalid'))
    return
  }

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

  // Single-group keys require a selected group, including drafts converted from composite mode, so an implicit stale binding cannot enter the submitted payload.
  if (!formData.value.is_composite && formData.value.group_id === null) {
    appStore.showError(t('keys.groupRequired'))
    return
  }

  // Creation validates the custom secret when its optional control is enabled.
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

  await submitKeyForm()
}

// handleDelete removes the selected key and closes its confirmation after success. Missing
// selection skips the request, and failures prefer the API message while retaining the
// dialog for another attempt.
const handleDelete = async () => {
  if (!selectedKey.value) return

  try {
    await keysAPI.delete(selectedKey.value.id)
    appStore.showSuccess(t('keys.keyDeletedSuccess'))
    showDeleteDialog.value = false
    loadApiKeys()
  } catch (error: any) {
    // Use the server's actionable failure message when available, otherwise use the localized deletion failure label so the dialog still explains the failed request.
    const errorMsg = error?.message || t('keys.failedToDelete')
    appStore.showError(errorMsg)
  }
}

// closeModals hides create and edit dialogs and restores their shared draft
// defaults. It advances the group-request sequence before clearing choices, so
// a subscription lookup already in flight loses permission to publish or prune
// bindings when it finishes. Reopening the editor can then start a new lookup
// against the reset draft. The request itself keeps running, but its completion
// checks the captured sequence before updating choices or releasing loading.
const closeModals = () => {
  showCreateModal.value = false
  showEditModal.value = false
  selectedKey.value = null
  // Invalidate lookup ownership before clearing groups and resetting the draft,
  // so pending results cannot repopulate a closed or subsequently reopened editor.
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
    concurrency_limit: 0,
    rpm_limit: 0,
    rate_limit_5h: null,
    rate_limit_1d: null,
    rate_limit_7d: null,
    enable_expiration: false,
    expiration_preset: '30',
    expiration_date: '',
    fallback_when_group_unavailable: true,
  }
}

// confirmResetQuota opens the quota-reset confirmation for the currently selected edit key.
const confirmResetQuota = () => {
  showResetQuotaDialog.value = true
}

// setExpirationDays stores the shortcut and derives a local datetime expiration from today.
// Calendar-day arithmetic follows the browser date before formatting the result for the
// datetime-local input.
const setExpirationDays = (days: number) => {
  formData.value.expiration_preset = days.toString() as '7' | '30' | '90'
  const expDate = new Date()
  expDate.setDate(expDate.getDate() + days)
  formData.value.expiration_date = formatDateTimeLocal(expDate.toISOString())
}

// resetQuotaUsed clears used quota for the selected key and closes its confirmation before
// requesting. Success updates the local selected record to zero, while API failure reports
// the server detail or default message.
const resetQuotaUsed = async () => {
  if (!selectedKey.value) return
  showResetQuotaDialog.value = false
  try {
    await keysAPI.update(selectedKey.value.id, { reset_quota: true })
    appStore.showSuccess(t('keys.quotaResetSuccess'))
    // Update selected quota usage after persistence succeeds so the edit dialog reflects the reset immediately, before another list refresh is required.
    if (selectedKey.value) {
      selectedKey.value.quota_used = 0
    }
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToResetQuota')
    appStore.showError(errorMsg)
  }
}

// confirmResetRateLimit opens spending-window reset confirmation for the current edit
// selection.
const confirmResetRateLimit = () => {
  showResetRateLimitDialog.value = true
}

// confirmResetRateLimitFromTable selects the table key before opening spending-window reset
// confirmation.
const confirmResetRateLimitFromTable = (row: ApiKey) => {
  selectedKey.value = row
  showResetRateLimitDialog.value = true
}

// resetRateLimitUsage resets the selected key's spending-window usage and reloads the key
// list. After refresh it replaces the selected edit object with the matching returned row,
// while failures report API detail or a localized message.
const resetRateLimitUsage = async () => {
  if (!selectedKey.value) return
  showResetRateLimitDialog.value = false
  try {
    await keysAPI.update(selectedKey.value.id, { reset_rate_limit_usage: true })
    appStore.showSuccess(t('keys.rateLimitResetSuccess'))
    // Reload list data after the spending-window reset because the server determines the resulting window values and timestamps used by the table.
    await loadApiKeys()
    // Replace the current edit record with its refreshed counterpart so its displayed window usage comes from the same server response as the list.
    const refreshedKey = apiKeys.value.find((k) => k.id === selectedKey.value!.id)
    if (refreshedKey) {
      selectedKey.value = refreshedKey
    }
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToResetRateLimit')
    appStore.showError(errorMsg)
  }
}

// groupWithModels resolves the group catalog used by client configuration and applies key
// redirects through groupForKeyConfig. Available group data overrides embedded key metadata;
// ungrouped or missing keys return undefined.
const groupWithModels = (key: ApiKey | null): Group | undefined => {
  if (!key?.group_id) return undefined
  const available = groups.value.find((group) => group.id === key.group_id)
  return groupForKeyConfig(
    available ? { ...key.group, ...available } : (key.group ?? undefined),
    key.model_mapping,
  )
}
// Selected single and composite group views combine loaded catalog metadata with key
// redirects before passing capabilities to client configuration instructions.
const selectedKeyGroup = computed(() => groupWithModels(selectedKey.value))
const selectedCompositeGroups = computed(() =>
  (selectedKey.value?.composite_groups ?? []).map((binding) => ({
    ...binding,
    group: groupForKeyConfig(
      groups.value.find((group) => group.id === binding.group_id) ?? binding.group,
      selectedKey.value?.model_mapping,
    ),
  })),
)
// Client-import state retains its chosen client and model, deriving options from the pending
// key's effective group capabilities before opening an external protocol link.
const ccClient = ref<CcSwitchClientType>('claude')
const ccModel = ref('')
const ccGroup = computed(() => groupWithModels(pendingCcsRow.value))
const ccClientOptions = computed(() =>
  availableClients(ccGroup.value?.allowed_protocols ?? [])
    .filter((client) => client !== 'opencode')
    .map((client) => ({ value: client, label: CLIENT_LABELS[client] })),
)
const ccModelOptions = computed(() => {
  const protocol = clientProtocol(ccClient.value, ccGroup.value?.allowed_protocols ?? [])
  return protocol
    ? modelsForProtocol(ccGroup.value, protocol).map((model) => ({ value: model, label: model }))
    : []
})
watch(ccClientOptions, (options) => {
  if (!options.some((option) => option.value === ccClient.value))
    ccClient.value = (options[0]?.value as CcSwitchClientType) ?? 'claude'
})
watch(ccModelOptions, (options) => {
  if (!options.some((option) => option.value === ccModel.value))
    ccModel.value = options[0]?.value ?? ''
})
// importToCcswitch retains the key and opens the client-import choice dialog.
const importToCcswitch = (row: ApiKey) => {
  pendingCcsRow.value = row
  showCcsClientSelect.value = true
}

// executeCcsImport builds the external client link using the selected model, key, endpoint,
// and balance usage script. It opens that custom protocol in the current window and
// schedules a focus-based installation hint. The focus probe is a heuristic: remaining focus
// after the delay reports the client-not-installed message, while synchronous open failures
// use the same feedback. The timer is not owned by the component cleanup.
const executeCcsImport = (row: ApiKey, clientType: CcSwitchClientType) => {
  const baseUrl = publicSettings.value?.api_base_url || window.location.origin

  const usageScript = buildCcSwitchUsageScript(baseUrl, balanceUnitName.value)
  const providerName = (publicSettings.value?.site_name || 'tokenrouter').trim() || 'tokenrouter'
  const deeplink = buildCcSwitchImportDeeplink({
    baseUrl,
    model: ccModel.value,
    clientType,
    providerName,
    apiKey: row.key,
    usageScript,
  })

  try {
    window.open(deeplink, '_self')

    // Probe focus after opening the custom protocol as an installation hint; browser focus behavior is only an approximation of whether a handler launched.
    setTimeout(() => {
      if (document.hasFocus()) {
        // Remaining page focus triggers the installation hint, although it is not definitive proof that the external application failed to open.
        appStore.showError(t('keys.ccSwitchNotInstalled'))
      }
    }, 100)
  } catch {
    appStore.showError(t('keys.ccSwitchNotInstalled'))
  }
}

// handleCcsClientSelect launches import only when a pending key and currently offered model
// remain selected. It closes the choice dialog and clears the pending record whether import
// runs or is skipped.
const handleCcsClientSelect = (clientType: CcSwitchClientType) => {
  if (
    pendingCcsRow.value &&
    ccModelOptions.value.some((option) => option.value === ccModel.value)
  ) {
    executeCcsImport(pendingCcsRow.value, clientType)
  }
  showCcsClientSelect.value = false
  pendingCcsRow.value = null
}

// closeCcsClientSelect closes the client chooser and releases its pending key record.
const closeCcsClientSelect = () => {
  showCcsClientSelect.value = false
  pendingCcsRow.value = null
}

// formatResetTime formats a reset timestamp against the page's minute-updated clock. Missing
// timestamps return empty text, elapsed windows report reset-now, and future durations
// display days, hours, or remaining minutes.
function formatResetTime(resetAt: string | null): string {
  if (!resetAt) return ''
  const diff = new Date(resetAt).getTime() - now.value.getTime()
  if (diff <= 0) return t('keys.resetNow')
  // Truncate elapsed milliseconds to complete units for the compact countdown.
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
  // Refresh the display clock each minute because reset labels show whole
  // minutes; the mounted page releases this recurring timer during unmount.
  resetTimer = setInterval(() => {
    now.value = new Date()
  }, 60000)
  await loadPublicSettings()
  await Promise.all([
    loadApiKeys(),
    loadGroups(),
    loadUserGroupRates(),
    loadBillingOptions(),
    loadFormGroups(),
  ])
  openCreateFromRoute()
})

onUnmounted(() => {
  // Release the page's outside-click listener, current list/usage controller,
  // and recurring clock timer so these existing resources stop reacting after navigation.
  document.removeEventListener('click', closeGroupSelector)
  abortController?.abort()
  if (resetTimer) clearInterval(resetTimer)
})
useLocaleRefresh(() =>
  Promise.all([
    loadApiKeys(),
    loadGroups(),
    loadFormGroups(),
    loadBillingOptions(),
    loadPublicSettings(),
  ]),
)
</script>

<style scoped>
/* Single-line controls share a 36-pixel height while textareas retain natural height. Select triggers already receive that size from the common input rules. */
.key-form-controls :deep(input.input),
.key-form-controls :deep(.btn) {
  height: 2.25rem;
  min-height: 0;
}

.key-form-controls :deep(input.input),
.key-form-controls :deep(.btn) {
  padding-top: 0.375rem;
  padding-bottom: 0.375rem;
}
</style>
