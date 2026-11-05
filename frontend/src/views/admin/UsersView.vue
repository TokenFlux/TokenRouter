<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
          <div class="flex min-w-0 w-full flex-1 flex-wrap items-center gap-2 sm:w-auto">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon name="search" size="md" class="input-icon text-gray-400" />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.users.searchUsers')"
                class="input input-has-icon"
                @input="handleSearch"
              />
            </div>

            <div class="relative shrink-0" ref="filterDropdownRef">
              <button
                type="button"
                @click="showFilterDropdown = !showFilterDropdown"
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('admin.users.filterSettings')"
                :aria-label="t('admin.users.filterSettings')"
              >
                <Icon name="filter" size="sm" />
              </button>
              <MotionTransition name="dropdown-fade">
                <div
                  v-if="showFilterDropdown"
                  :inert="!showFilterDropdown || undefined"
                  class="dropdown left-auto right-0 top-full z-50 mt-1 w-48 sm:left-0 sm:right-auto"
                  @click.stop
                >
                  <button
                    v-for="filter in builtInFilters"
                    :key="filter.key"
                    @click="toggleBuiltInFilter(filter.key)"
                    class="dropdown-item justify-between"
                  >
                    <span>{{ filter.name }}</span>
                    <Icon
                      v-if="visibleFilters.has(filter.key)"
                      name="check"
                      size="sm"
                      class="text-primary-500"
                      :stroke-width="2"
                      :animate-on-hover="false"
                    />
                  </button>
                  <div
                    v-if="filterableAttributes.length > 0"
                    class="my-1 border-t border-gray-100 dark:border-dark-700"
                  ></div>
                  <button
                    v-for="attr in filterableAttributes"
                    :key="attr.id"
                    @click="toggleAttributeFilter(attr)"
                    class="dropdown-item justify-between"
                  >
                    <span>{{ attr.name }}</span>
                    <Icon
                      v-if="visibleFilters.has(`attr_${attr.id}`)"
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

            <div v-if="visibleFilters.has('role')" class="w-full sm:w-32">
              <Select
                v-model="filters.role"
                :options="[
                  { value: '', label: t('admin.users.allRoles') },
                  { value: 'admin', label: t('admin.users.admin') },
                  { value: 'user', label: t('admin.users.user') },
                ]"
                @change="applyFilter"
              />
            </div>

            <div v-if="visibleFilters.has('status')" class="w-full sm:w-32">
              <Select
                v-model="filters.status"
                :options="[
                  { value: '', label: t('admin.users.allStatus') },
                  { value: 'active', label: t('common.active') },
                  { value: 'disabled', label: t('admin.users.disabled') },
                ]"
                @change="applyFilter"
              />
            </div>

            <div v-if="visibleFilters.has('group')" class="w-full sm:w-44">
              <Select
                v-model="filters.group"
                :options="groupFilterOptions"
                searchable
                creatable
                :creatable-prefix="t('admin.users.fuzzySearch')"
                :search-placeholder="t('admin.users.searchAuthorizedGroups')"
                @change="applyFilter"
              />
            </div>

            <div v-if="visibleFilters.has('apiKeyGroup')" class="w-full sm:w-44">
              <Select
                v-model="filters.apiKeyGroup"
                :options="apiKeyGroupFilterOptions"
                searchable
                :search-placeholder="t('admin.users.searchApiKeyGroups')"
                @change="applyFilter"
              />
            </div>

            <template v-for="(value, attrId) in activeAttributeFilters" :key="attrId">
              <div v-if="visibleFilters.has(`attr_${attrId}`)" class="relative w-full sm:w-36">
                <input
                  v-if="
                    ['text', 'textarea', 'email', 'url', 'date'].includes(
                      getAttributeDefinition(Number(attrId))?.type || 'text',
                    )
                  "
                  :value="value"
                  @input="
                    (e) =>
                      updateAttributeFilter(Number(attrId), (e.target as HTMLInputElement).value)
                  "
                  @keyup.enter="applyFilter"
                  :placeholder="getAttributeDefinitionName(Number(attrId))"
                  class="input w-full"
                />

                <input
                  v-else-if="getAttributeDefinition(Number(attrId))?.type === 'number'"
                  :value="value"
                  type="number"
                  @input="
                    (e) =>
                      updateAttributeFilter(Number(attrId), (e.target as HTMLInputElement).value)
                  "
                  @keyup.enter="applyFilter"
                  :placeholder="getAttributeDefinitionName(Number(attrId))"
                  class="input w-full"
                />

                <template
                  v-else-if="
                    ['select', 'multi_select'].includes(
                      getAttributeDefinition(Number(attrId))?.type || '',
                    )
                  "
                >
                  <div class="w-full">
                    <Select
                      :model-value="value"
                      :options="[
                        { value: '', label: getAttributeDefinitionName(Number(attrId)) },
                        ...(getAttributeDefinition(Number(attrId))?.options || []),
                      ]"
                      @update:model-value="
                        (val) => {
                          updateAttributeFilter(Number(attrId), String(val ?? ''))
                          applyFilter()
                        }
                      "
                    />
                  </div>
                </template>

                <input
                  v-else
                  :value="value"
                  @input="
                    (e) =>
                      updateAttributeFilter(Number(attrId), (e.target as HTMLInputElement).value)
                  "
                  @keyup.enter="applyFilter"
                  :placeholder="getAttributeDefinitionName(Number(attrId))"
                  class="input w-full"
                />
              </div>
            </template>
          </div>

          <div class="flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
            <div class="flex items-center gap-2 md:contents">
              <button
                @click="loadUsers"
                :disabled="loading"
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('common.refresh')"
              >
                <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
              </button>

              <div class="relative" ref="columnDropdownRef">
                <button
                  @click="showColumnDropdown = !showColumnDropdown"
                  class="btn btn-secondary shrink-0 btn-icon"
                  :title="t('admin.users.columnSettings')"
                >
                  <Icon name="columns" size="sm" class="h-4 w-4" />
                  <span class="hidden">{{ t('admin.users.columnSettings') }}</span>
                </button>

                <MotionTransition name="dropdown-fade">
                  <div
                    v-if="showColumnDropdown"
                    :inert="!showColumnDropdown || undefined"
                    class="dropdown right-0 top-full z-50 mt-1 max-h-menu w-48 overflow-y-auto"
                  >
                    <button
                      v-for="col in toggleableColumns"
                      :key="col.key"
                      :disabled="isForcedVisibleColumn(col.key)"
                      @click="toggleColumn(col.key)"
                      :class="[
                        'dropdown-item justify-between',
                        isForcedVisibleColumn(col.key)
                          ? 'cursor-not-allowed text-gray-400 hover:bg-transparent dark:text-gray-500 dark:hover:bg-transparent'
                          : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700',
                      ]"
                      :title="
                        isForcedVisibleColumn(col.key) ? t('admin.users.columnAlwaysVisible') : ''
                      "
                    >
                      <span>{{ col.label }}</span>
                      <Icon
                        v-if="isColumnVisible(col.key)"
                        name="check"
                        size="sm"
                        :class="
                          isForcedVisibleColumn(col.key)
                            ? 'text-gray-400 dark:text-gray-500'
                            : 'text-primary-500'
                        "
                        :stroke-width="2"
                        :animate-on-hover="false"
                      />
                    </button>
                  </div>
                </MotionTransition>
              </div>

              <button
                @click="showAttributesModal = true"
                class="btn btn-secondary shrink-0 btn-icon"
                :title="t('admin.users.attributes.configButton')"
              >
                <Icon name="cog" size="sm" />
                <span class="hidden">{{ t('admin.users.attributes.configButton') }}</span>
              </button>
            </div>

            <button
              v-if="selectedCount > 0"
              class="btn btn-secondary flex-none whitespace-nowrap px-3 md:flex-initial"
              data-test="bulk-edit-limits"
              @click="showBulkEditModal = true"
            >
              <Icon name="users" size="sm" class="mr-2" />
              {{ t('admin.users.bulkLimits.action', { count: selectedCount }) }}
            </button>

            <button
              @click="showCreateModal = true"
              class="btn btn-primary flex-none whitespace-nowrap px-3 md:flex-initial"
            >
              <Icon name="plus" size="sm" class="mr-2" />
              {{ t('admin.users.createUser') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-users-column-order"
          :columns="columns"
          :data="sortedUsers"
          :loading="loading"
          row-key="id"
          selectable
          :selected-keys="selectedIds"
          :selection-label="getUserSelectionLabel"
          :actions-count="7"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          :sort-storage-key="USER_SORT_STORAGE_KEY"
          @sort="handleSort"
          @update:selected-keys="handleSelectedKeysUpdate"
        >
          <template #cell-email="{ value, row }">
            <div class="flex items-center gap-2">
              <UserAvatar
                :avatar-url="row.avatar_url || ''"
                :user-id="row.id"
                :alt="value"
                size-class="h-8 w-8"
              />

              <!-- The 32-pixel line matches the avatar height while the email baseline remains available for mobile-card label alignment. -->
              <span class="self-baseline font-medium leading-8 text-gray-900 dark:text-white">{{
                value
              }}</span>
            </div>
          </template>

          <template #cell-username="{ value }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ value || '-' }}</span>
          </template>

          <!-- Notes longer than 30 UTF-16 code units show their first 25 units plus an ellipsis; the title retains the full note. -->
          <template #cell-notes="{ value }">
            <div class="max-w-xs">
              <span
                v-if="value"
                :title="value.length > 30 ? value : undefined"
                class="block truncate text-sm text-gray-600 dark:text-gray-400"
              >
                {{ value.length > 30 ? value.substring(0, 25) + '...' : value }}
              </span>
              <span v-else class="text-sm text-gray-400">-</span>
            </div>
          </template>

          <template
            v-for="def in attributeDefinitions.filter((d) => d.enabled)"
            :key="def.id"
            #[`cell-attr_${def.id}`]="{ row }"
          >
            <div class="max-w-xs">
              <span
                class="block truncate text-sm text-gray-700 dark:text-gray-300"
                :title="getAttributeValue(row.id, def.id)"
              >
                {{ getAttributeValue(row.id, def.id) }}
              </span>
            </div>
          </template>

          <template #cell-role="{ value }">
            <span :class="['badge', value === 'admin' ? 'badge-purple' : 'badge-gray']">
              {{ t('admin.users.roles.' + value) }}
            </span>
          </template>

          <template #cell-groups="{ row }">
            <div v-if="allGroups.length > 0" class="flex flex-col gap-1">
              <span
                data-icon-trigger
                v-if="getUserGroups(row).exclusive.length > 0"
                class="group/ex relative inline-flex cursor-pointer items-center gap-1 whitespace-nowrap text-xs"
                @click.stop="toggleExpandedGroup(row.id)"
              >
                <Icon
                  name="shield"
                  size="xs"
                  class="h-3.5 w-3.5 text-purple-500 dark:text-purple-400"
                />

                <!-- Align group count and label text with the mobile-card label baseline inside the combined icon and text row. -->
                <span class="self-baseline font-medium text-purple-600 dark:text-purple-400">{{
                  getUserGroups(row).exclusive.length
                }}</span>
                <span class="self-baseline text-gray-500 dark:text-dark-400">{{
                  t('admin.users.exclusiveLabel')
                }}</span>

                <div
                  v-if="expandedGroupUserId !== row.id"
                  class="pointer-events-none absolute left-0 top-full z-50 mt-1.5 tooltip-panel rounded-compact px-2.5 py-1.5 text-xs opacity-0 shadow-lg transition-opacity duration-fast group-hover/ex:opacity-100"
                >
                  <div class="tooltip-caret -top-1 left-4 border-l border-t"></div>
                  <div class="flex flex-col gap-0.5 whitespace-nowrap">
                    <span v-for="g in getUserGroups(row).exclusive" :key="g.id">{{ g.name }}</span>
                  </div>
                </div>

                <div
                  v-if="expandedGroupUserId === row.id"
                  class="dropdown left-0 top-full z-50 mt-1.5 min-w-[160px] overflow-hidden text-xs"
                >
                  <div
                    class="border-b border-gray-100 px-3 py-1.5 text-xs font-medium uppercase tracking-wider text-gray-400 dark:border-dark-600 dark:text-dark-400"
                  >
                    {{ t('admin.users.clickToReplace') }}
                  </div>
                  <div
                    data-icon-trigger
                    v-for="g in getUserGroups(row).exclusive"
                    :key="g.id"
                    class="dropdown-item-sm hover:bg-primary-50 hover:text-primary-600 dark:text-dark-200 dark:hover:bg-primary-900/30 dark:hover:text-primary-400"
                    @click.stop="openGroupReplace(row, g)"
                  >
                    <Icon name="swap" size="xs" class="h-3.5 w-3.5 flex-shrink-0 opacity-50" />
                    <span class="flex-1">{{ g.name }}</span>
                  </div>
                </div>
              </span>

              <span
                v-if="getUserGroups(row).publicGroups.length > 0"
                class="group/pub relative inline-flex cursor-default items-center gap-1 whitespace-nowrap text-xs"
              >
                <Icon name="globe" size="xs" class="h-3.5 w-3.5 text-gray-400 dark:text-dark-500" />
                <span class="self-baseline font-medium text-gray-600 dark:text-dark-300">{{
                  getUserGroups(row).publicGroups.length
                }}</span>
                <span class="self-baseline text-gray-400 dark:text-dark-500">{{
                  t('admin.users.publicLabel')
                }}</span>

                <div
                  class="pointer-events-none absolute left-0 top-full z-50 mt-1.5 tooltip-panel rounded-compact px-2.5 py-1.5 text-xs opacity-0 shadow-lg transition-opacity duration-fast group-hover/pub:opacity-100"
                >
                  <div class="tooltip-caret -top-1 left-4 border-l border-t"></div>
                  <div class="flex flex-col gap-0.5 whitespace-nowrap">
                    <span v-for="g in getUserGroups(row).publicGroups" :key="g.id">{{
                      g.name
                    }}</span>
                  </div>
                </div>
              </span>

              <span
                v-if="
                  getUserGroups(row).exclusive.length === 0 &&
                  getUserGroups(row).publicGroups.length === 0
                "
                class="text-xs text-gray-400 dark:text-dark-500"
                >-</span
              >
            </div>
            <span v-else class="text-xs text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-subscriptions="{ row }">
            <div
              v-if="row.subscriptions && row.subscriptions.length > 0"
              class="flex flex-wrap gap-1.5"
            >
              <GroupBadge
                v-for="sub in row.subscriptions"
                :key="sub.id"
                :name="sub.plan?.name || `Plan #${sub.plan_id}`"
                :days-remaining="sub.expires_at ? getDaysRemaining(sub.expires_at) : null"
                :title="sub.expires_at ? formatDateTime(sub.expires_at) : ''"
              />
            </div>
            <span
              v-else
              class="inline-flex items-center gap-1.5 rounded-compact bg-gray-50 px-2 py-1 text-xs text-gray-400 dark:bg-dark-700/50 dark:text-dark-500"
            >
              <Icon name="ban" size="xs" class="h-3.5 w-3.5" />
              <span>{{ t('admin.users.noSubscription') }}</span>
            </span>
          </template>

          <template #cell-balance="{ value, row }">
            <div class="flex items-center gap-2">
              <div class="group relative">
                <button
                  class="font-medium text-gray-900 underline decoration-dashed decoration-gray-300 underline-offset-4 transition-colors hover:text-primary-600 dark:text-white dark:decoration-dark-500 dark:hover:text-primary-400"
                  @click="handleBalanceHistory(row)"
                >
                  {{ formatBalanceAmount(value) }}
                </button>

                <div
                  class="pointer-events-none absolute bottom-full left-1/2 z-50 mb-1.5 -translate-x-1/2 whitespace-nowrap tooltip-panel rounded-compact px-2 py-1 text-xs opacity-0 shadow-lg transition-opacity duration-fast group-hover:opacity-100"
                >
                  {{ t('admin.users.balanceHistoryTip') }}
                  <div
                    class="tooltip-caret -bottom-1 left-1/2 -translate-x-1/2 border-b border-r"
                  ></div>
                </div>
              </div>
              <button
                @click.stop="handleDeposit(row)"
                class="rounded-compact px-2 py-0.5 text-xs font-medium text-emerald-600 transition-colors hover:bg-emerald-50 dark:text-emerald-400 dark:hover:bg-emerald-900/20"
                :title="t('admin.users.deposit')"
              >
                {{ t('admin.users.deposit') }}
              </button>
            </div>
          </template>

          <template #cell-balance_recharge_multiplier="{ value }">
            <span v-if="value != null" class="tabular-nums">{{ value.toFixed(2) }}</span>
          </template>

          <!-- Usage headers open a metric menu because these cells sort the current page locally; their columns keep DataTable server sorting disabled. -->
          <template
            v-for="usageKey in USAGE_COLUMN_KEYS"
            :key="usageKey"
            #[`header-${usageKey}`]="{ column }"
          >
            <div class="flex items-center gap-1.5">
              <span>{{ column.label }}</span>
              <div class="usage-sort-trigger relative">
                <button
                  type="button"
                  class="flex items-center gap-1 rounded-compact px-1 py-0.5 transition-colors hover:bg-gray-200 dark:hover:bg-dark-700"
                  :class="
                    usageSort && usageSort.key === usageKey
                      ? 'text-primary-600 dark:text-primary-400'
                      : 'text-gray-400 dark:text-dark-500'
                  "
                  :title="t('admin.users.sortBy')"
                  :data-test="`usage-sort-trigger-${usageKey}`"
                  @click.stop="toggleUsageSortMenu(usageKey)"
                >
                  <span
                    v-if="usageSort && usageSort.key === usageKey"
                    class="text-xs normal-case font-medium tracking-normal"
                    >{{
                      usageSort.metric === 'today' ? t('admin.users.today') : t('admin.users.total')
                    }}</span
                  >
                  <Icon
                    v-if="usageSort && usageSort.key === usageKey"
                    name="chevronUp"
                    size="xs"
                    :class="{ 'rotate-180': usageSort.order === 'desc' }"
                    :stroke-width="2"
                    :animate-on-hover="false"
                  />
                  <Icon v-else name="sort" size="xs" :stroke-width="2" :animate-on-hover="false" />
                </button>

                <MotionTransition name="dropdown-fade">
                  <div
                    v-if="openUsageSortMenu === usageKey"
                    :inert="!(openUsageSortMenu === usageKey) || undefined"
                    class="dropdown right-0 top-full z-50 mt-1 min-w-[120px]"
                  >
                    <button
                      v-for="metric in ['today', 'total'] as const"
                      :key="metric"
                      type="button"
                      class="flex w-full items-center justify-between gap-3 px-3 py-1.5 text-left text-xs normal-case tracking-normal hover:bg-gray-100 dark:hover:bg-dark-700"
                      :class="
                        isUsageSortActive(usageKey, metric)
                          ? 'font-medium text-primary-600 dark:text-primary-400'
                          : 'text-gray-700 dark:text-gray-300'
                      "
                      :data-test="`usage-sort-${usageKey}-${metric}`"
                      @click.stop="toggleUsageSort(usageKey, metric)"
                    >
                      <span>{{
                        metric === 'today' ? t('admin.users.today') : t('admin.users.total')
                      }}</span>
                      <Icon
                        v-if="getUsageSortOrder(usageKey, metric)"
                        name="chevronUp"
                        size="xs"
                        :class="{ 'rotate-180': getUsageSortOrder(usageKey, metric) === 'desc' }"
                        :stroke-width="2"
                        :animate-on-hover="false"
                      />
                    </button>
                    <div
                      class="mt-1 border-t border-gray-100 px-3 py-1 text-xs normal-case tracking-normal text-gray-400 dark:border-dark-700 dark:text-dark-500"
                    >
                      {{ t('admin.users.sortCurrentPageOnly') }}
                    </div>
                  </div>
                </MotionTransition>
              </div>
            </div>
          </template>

          <template #cell-usage="{ row }">
            <PlatformUsageBreakdown
              :today="usageStats[row.id]?.today_actual_cost ?? 0"
              :total="usageStats[row.id]?.total_actual_cost ?? 0"
              :by-platform="usageStats[row.id]?.by_platform"
            />
          </template>

          <template #cell-usage_anthropic="{ row }">
            <PlatformCostCell :usage="getPlatformUsage(row.id, 'anthropic')" />
          </template>

          <template #cell-usage_openai="{ row }">
            <PlatformCostCell :usage="getPlatformUsage(row.id, 'openai')" />
          </template>

          <template #cell-usage_gemini="{ row }">
            <PlatformCostCell :usage="getPlatformUsage(row.id, 'gemini')" />
          </template>

          <template #cell-usage_antigravity="{ row }">
            <PlatformCostCell :usage="getPlatformUsage(row.id, 'antigravity')" />
          </template>

          <template #cell-usage_qoder="{ row }">
            <PlatformCostCell :usage="getPlatformUsage(row.id, 'qoder')" />
          </template>

          <template #cell-concurrency="{ row }">
            <UserConcurrencyCell :current="row.current_concurrency ?? 0" :max="row.concurrency" />
          </template>

          <template #cell-status="{ value }">
            <div class="flex items-center gap-1.5">
              <span
                :class="[
                  'inline-block h-2 w-2 rounded-full',
                  value === 'active' ? 'bg-green-500' : 'bg-red-500',
                ]"
              ></span>

              <!-- Align the mobile-card label with the status text baseline inside its indicator row. -->
              <span class="self-baseline text-sm text-gray-700 dark:text-gray-300">
                {{ value === 'active' ? t('common.active') : t('admin.users.disabled') }}
              </span>
            </div>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{
              formatDateTime(value)
            }}</span>
          </template>

          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ value ? formatDateTime(value) : '-' }}
            </span>
          </template>

          <template #cell-last_active_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ value ? formatDateTime(value) : '-' }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>

              <button
                v-if="row.role !== 'admin'"
                @click="handleToggleStatus(row)"
                :class="[
                  'flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors',
                  row.status === 'active'
                    ? 'hover:bg-orange-50 hover:text-orange-600 dark:hover:bg-orange-900/20 dark:hover:text-orange-400'
                    : 'hover:bg-green-50 hover:text-green-600 dark:hover:bg-green-900/20 dark:hover:text-green-400',
                ]"
              >
                <Icon v-if="row.status === 'active'" name="ban" size="sm" />
                <Icon v-else name="checkCircle" size="sm" :animate-on-hover="false" />
                <span class="text-xs">{{
                  row.status === 'active' ? t('admin.users.disable') : t('admin.users.enable')
                }}</span>
              </button>

              <button
                @click="openActionMenu(row, $event)"
                class="action-menu-trigger flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
                :class="{
                  'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white':
                    activeMenuId === row.id,
                }"
              >
                <Icon name="more" size="sm" />
                <span class="text-xs">{{ t('common.more') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.users.noUsersYet')"
              :description="t('admin.users.createFirstUser')"
              :action-text="t('admin.users.createUser')"
              @action="showCreateModal = true"
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

    <Teleport to="body">
      <MotionTransition name="dropdown-fade">
        <div
          v-if="activeMenuId !== null && menuPosition"
          class="action-menu action-menu-content w-48 overflow-hidden"
          :style="{ top: menuPosition.top + 'px', left: menuPosition.left + 'px' }"
        >
          <div class="py-1">
            <template v-for="user in users" :key="user.id">
              <template v-if="user.id === activeMenuId">
                <button
                  @click="
                    handleViewApiKeys(user);
                    closeActionMenu();
                  "
                  class="dropdown-item"
                >
                  <Icon name="key" size="sm" class="text-gray-400" :stroke-width="2" />
                  {{ t('admin.users.apiKeys') }}
                </button>

                <button
                  @click="
                    handleAllowedGroups(user);
                    closeActionMenu();
                  "
                  class="dropdown-item"
                >
                  <Icon name="users" size="sm" class="text-gray-400" :stroke-width="2" />
                  {{ t('admin.users.groups') }}
                </button>

                <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>

                <button
                  @click="
                    handleDeposit(user);
                    closeActionMenu();
                  "
                  class="dropdown-item"
                >
                  <Icon name="plus" size="sm" class="text-emerald-500" :stroke-width="2" />
                  {{ t('admin.users.deposit') }}
                </button>

                <button
                  @click="
                    handleWithdraw(user);
                    closeActionMenu();
                  "
                  class="dropdown-item"
                >
                  <Icon name="minus" size="sm" class="h-4 w-4 text-amber-500" />
                  {{ t('admin.users.withdraw') }}
                </button>

                <button
                  @click="
                    handleBalanceHistory(user);
                    closeActionMenu();
                  "
                  class="dropdown-item"
                >
                  <BalanceIcon size="sm" class="text-gray-400" />
                  {{ t('admin.users.balanceHistory') }}
                </button>

                <div class="my-1 border-t border-gray-100 dark:border-dark-700"></div>

                <button
                  v-if="user.role !== 'admin'"
                  @click="
                    handleDelete(user);
                    closeActionMenu();
                  "
                  class="dropdown-item text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
                >
                  <Icon name="trash" size="sm" :stroke-width="2" />
                  {{ t('common.delete') }}
                </button>
              </template>
            </template>
          </div>
        </div>
      </MotionTransition>
    </Teleport>

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.users.deleteUser')"
      :message="t('admin.users.deleteConfirm', { email: deletingUser?.email })"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
    <UserCreateModal
      :show="showCreateModal"
      @close="showCreateModal = false"
      @success="loadUsers"
    />
    <UserEditModal
      :show="showEditModal"
      :user="editingUser"
      @close="closeEditModal"
      @success="loadUsers"
    />
    <BulkEditUserModal
      :show="showBulkEditModal"
      :selected-ids="selectedIds"
      @close="showBulkEditModal = false"
      @success="handleBulkLimitsSuccess"
    />

    <UserApiKeysModal :show="showApiKeysModal" :user="viewingUser" @close="closeApiKeysModal" />
    <UserAllowedGroupsModal
      :show="showAllowedGroupsModal"
      :user="allowedGroupsUser"
      @close="closeAllowedGroupsModal"
      @success="loadUsers"
    />
    <UserBalanceModal
      :show="showBalanceModal"
      :user="balanceUser"
      :operation="balanceOperation"
      :z-index="balanceDialogZIndex"
      @close="closeBalanceModal"
      @success="handleBalanceSuccess"
    />
    <UserBalanceHistoryModal
      :show="showBalanceHistoryModal"
      :user="balanceHistoryUser"
      :refresh-key="balanceHistoryRefreshKey"
      @close="closeBalanceHistoryModal"
      @deposit="handleDepositFromHistory"
      @withdraw="handleWithdrawFromHistory"
    />
    <GroupReplaceModal
      :show="showGroupReplaceModal"
      :user="groupReplaceUser"
      :old-group="groupReplaceOldGroup"
      :all-groups="allGroups"
      @close="closeGroupReplaceModal"
      @success="loadUsers"
    />
    <UserAttributesConfigModal :show="showAttributesModal" @close="handleAttributesModalClose" />
  </AppLayout>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useTableSelection } from '@/composables/useTableSelection'
import { Z_INDEX } from '@/constants/overlay'
import { formatDateTime } from '@/utils/format'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import Icon from '@/components/icons/Icon.vue'

import { adminAPI } from '@/api/admin'
import type { AdminUser } from '@/api/admin/users.types'
import type { AdminGroup } from '@/api/groups.types'
import type { UserAttributeDefinition } from '@/api/admin/userAttributes.types'
import type { BatchUserUsageStats } from '@/api/admin/dashboard'
import type { Column } from '@/components/common/table/types'
import type { SelectOption } from '@/components/common/Select.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import Select from '@/components/common/Select.vue'
import { buildApiKeyGroupFilterOptions } from './apiKeyGroupFilterOptions'
import UserAttributesConfigModal from '@/components/user/UserAttributesConfigModal.vue'
import UserConcurrencyCell from '@/components/user/UserConcurrencyCell.vue'
import PlatformUsageBreakdown from '@/components/user/PlatformUsageBreakdown.vue'
import PlatformCostCell from '@/components/user/PlatformCostCell.vue'
import UserCreateModal from '@/components/admin/user/UserCreateModal.vue'
import UserEditModal from '@/components/admin/user/UserEditModal.vue'
import BulkEditUserModal from '@/components/admin/user/BulkEditUserModal.vue'
import UserApiKeysModal from '@/components/admin/user/UserApiKeysModal.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import UserBalanceModal from '@/components/admin/user/UserBalanceModal.vue'
import UserBalanceHistoryModal from '@/components/admin/user/UserBalanceHistoryModal.vue'
import GroupReplaceModal from '@/components/admin/user/GroupReplaceModal.vue'

// UsersView.vue manages administrator user listing, configurable columns and filters,
// row actions, and bulk selection. The server orders and paginates user rows, while an
// optional usage sort reorders the loaded page after secondary usage data arrives.
// Equal usage values keep their current server order through an index tie-breaker.
//
// Column preferences use versioned additions so newly hidden usage columns can be
// applied to existing saved settings. The primary list request owns cancellation, and
// deferred usage/attribute loads use a separate generation to reject outdated results.
// Showing optional columns requests their supporting data, while stored filters and
// column visibility determine which optional lookups run when the page mounts.

// The translation function supplies user-table labels and operation messages throughout
// this administrator page and its dialogs.
const { t } = useI18n()

// Application notifications and balance-unit formatting support list failures, account
// actions, and displayed monetary values on the page.
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

// attributeColumns exposes each enabled custom definition as a nonsortable column using
// its stable attribute identifier in the key.
const attributeColumns = computed<Column[]>(() =>
  attributeDefinitions.value
    .filter((def) => def.enabled)
    .map((def) => ({
      key: `attr_${def.id}`,
      label: def.name,
      sortable: false,
    })),
)

// getAttributeValue formats a stored custom attribute using its enabled definition.
// Missing values display a dash; select values use option labels, and multi-select JSON
// arrays become comma-separated labels with raw values retained when parsing or option
// lookup fails.
const getAttributeValue = (userId: number, attrId: number): string => {
  const userAttrs = userAttributeValues.value[userId]
  if (!userAttrs) return '-'
  const value = userAttrs[attrId]
  if (!value) return '-'

  const def = attributeDefinitions.value.find((d) => d.id === attrId)
  if (!def) return value

  if (def.type === 'multi_select' && value) {
    try {
      const arr = JSON.parse(value)
      if (Array.isArray(arr)) {
        return arr
          .map((v) => {
            const opt = def.options?.find((o) => o.value === v)
            return opt?.label || v
          })
          .join(', ')
      }
    } catch {
      // Malformed stored multi-select JSON remains visible as raw text, allowing
      // administrators to inspect the persisted value instead of losing it during
      // formatting.
      return value
    }
  }

  if (def.type === 'select' && value && def.options) {
    const opt = def.options.find((o) => o.value === value)
    return opt?.label || value
  }

  return value
}

// allColumns combines fixed user fields with custom attributes and platform usage
// columns for visibility settings and table rendering.
const allColumns = computed<Column[]>(() => [
  { key: 'email', label: t('admin.users.columns.user'), sortable: true },
  { key: 'id', label: t('admin.users.columns.id'), sortable: true },
  { key: 'username', label: t('admin.users.columns.username'), sortable: true },
  { key: 'notes', label: t('admin.users.columns.notes'), sortable: false },
  ...attributeColumns.value,
  { key: 'role', label: t('admin.users.columns.role'), sortable: true },
  { key: 'groups', label: t('admin.users.columns.groups'), sortable: false },
  { key: 'subscriptions', label: t('admin.users.columns.subscriptions'), sortable: false },
  { key: 'balance', label: t('admin.users.columns.balance'), sortable: true },
  {
    key: 'balance_recharge_multiplier',
    label: t('admin.users.columns.balanceRechargeMultiplier'),
    sortable: true,
  },
  { key: 'usage', label: t('admin.users.columns.usage'), sortable: false },
  { key: 'usage_anthropic', label: t('admin.users.columns.usageAnthropic'), sortable: false },
  { key: 'usage_openai', label: t('admin.users.columns.usageOpenAI'), sortable: false },
  { key: 'usage_gemini', label: t('admin.users.columns.usageGemini'), sortable: false },
  { key: 'usage_antigravity', label: t('admin.users.columns.usageAntigravity'), sortable: false },
  { key: 'usage_qoder', label: t('admin.users.columns.usageQoder'), sortable: false },
  { key: 'concurrency', label: t('admin.users.columns.concurrency'), sortable: true },
  { key: 'status', label: t('admin.users.columns.status'), sortable: true },
  { key: 'last_active_at', label: t('admin.users.columns.lastActive'), sortable: true },
  { key: 'last_used_at', label: t('admin.users.columns.lastUsed'), sortable: true },
  { key: 'created_at', label: t('admin.users.columns.created'), sortable: true },
  { key: 'actions', label: t('admin.users.columns.actions'), sortable: false },
])

// toggleableColumns offers every optional field while email and actions remain present
// in the user table.
const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => col.key !== 'email' && col.key !== 'actions'),
)

// hiddenColumns stores keys excluded from display; defaults, retired keys, and
// forced-visible keys govern restoring this preference.
const hiddenColumns = reactive<Set<string>>(new Set())

const DEFAULT_HIDDEN_COLUMNS = [
  'notes',
  'groups',
  'subscriptions',
  'usage',
  'concurrency',
  'usage_anthropic',
  'usage_openai',
  'usage_gemini',
  'usage_antigravity',
  'usage_qoder',
]
const REMOVED_COLUMNS = new Set(['last_login_at'])
const FORCED_VISIBLE_COLUMNS = new Set<string>()

// Column preference keys and version additions migrate saved hidden-column choices when
// new optional columns acquire default-hidden behavior.
const HIDDEN_COLUMNS_KEY = 'user-hidden-columns'
const COLUMN_SETTINGS_VERSION_KEY = 'user-column-settings-version'
const COLUMN_SETTINGS_VERSION = 4
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['usage_anthropic', 'usage_openai', 'usage_gemini', 'usage_antigravity'],
  4: ['usage_qoder'],
}

// loadSavedColumns restores hidden keys and applies unapplied column-preference
// versions. Removed and forced-visible keys are filtered from stored values before
// version additions are considered. Each unseen version contributes its default-hidden
// keys, and changed preferences are saved with the current version. Missing storage
// applies first-visit defaults; parsing or storage failures log the error and add
// defaults to the in-memory set so rendering can continue.
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed
        .filter((key) => !REMOVED_COLUMNS.has(key) && !FORCED_VISIBLE_COLUMNS.has(key))
        .forEach((key) => hiddenColumns.add(key))

      // Apply each unseen hidden-column addition once so older saved choices gain
      // newly default-hidden columns while keeping their existing selections.
      const storedVersion = Number(localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? '1')
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        let mutated = false
        for (let v = storedVersion + 1; v <= COLUMN_SETTINGS_VERSION; v++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[v] ?? []) {
            if (REMOVED_COLUMNS.has(key) || FORCED_VISIBLE_COLUMNS.has(key)) continue
            if (!hiddenColumns.has(key)) {
              hiddenColumns.add(key)
              mutated = true
            }
          }
        }
        // Persist migrated hidden keys before advancing the version so later visits
        // know which default-hidden additions have already been applied.
        if (mutated) saveColumnsToStorage()
        else localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
      localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
    }
  } catch (e) {
    // Use default hidden columns after storage or JSON failure so saved display
    // preferences cannot prevent the account table from rendering.
    console.error('Failed to load saved columns:', e)
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

// saveColumnsToStorage persists the current hidden-column keys and then their schema
// version. The version describes which default-hidden additions have been applied,
// so the keys are written before that marker advances. Both writes share one error
// handler, but browser storage does not make them atomic; a failure can leave an
// earlier successful write in place. The current in-memory visibility remains
// active, and the error is logged for diagnosis.
const saveColumnsToStorage = () => {
  try {
    // Write hidden keys before their version marker because the marker describes
    // which column additions are already represented by the saved set.
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
  } catch (e) {
    // A storage error keeps the current hidden set in memory so administrators can
    // continue using the chosen columns during this session.
    console.error('Failed to save columns:', e)
  }
}

// isForcedVisibleColumn reports membership in the configured fixed-visibility key set.
const isForcedVisibleColumn = (key: string) => FORCED_VISIBLE_COLUMNS.has(key)
// toggleColumn changes visibility unless the column is forced visible, then persists
// preferences. Revealing usage or attribute columns refreshes current-page secondary
// data, changing subscription visibility reloads users, and revealing groups requests
// their choices.
const toggleColumn = (key: string) => {
  if (FORCED_VISIBLE_COLUMNS.has(key)) return
  const wasHidden = hiddenColumns.has(key)
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
  // Revealing a secondary-data column starts its missing lookup so cells can populate
  // even when the primary user page has not changed.
  if (wasHidden && (key === 'usage' || key.startsWith('usage_') || key.startsWith('attr_'))) {
    refreshCurrentPageSecondaryData()
  }
  if (key === 'subscriptions') {
    loadUsers()
  }
  if (wasHidden && key === 'groups') {
    loadAllGroups()
  }
}

// isColumnVisible reports whether a column key is absent from hidden settings.
const isColumnVisible = (key: string) => !hiddenColumns.has(key)
// USAGE_COLUMN_KEYS fixes header control order, while USAGE_COLUMN_PLATFORMS maps
// aggregate usage to null and platform columns to provider families.
const USAGE_COLUMN_KEYS: readonly string[] = [
  'usage',
  'usage_anthropic',
  'usage_openai',
  'usage_gemini',
  'usage_antigravity',
  'usage_qoder',
]
const USAGE_COLUMN_PLATFORMS: Record<string, string | null> = {
  usage: null,
  usage_anthropic: 'anthropic',
  usage_openai: 'openai',
  usage_gemini: 'gemini',
  usage_antigravity: 'antigravity',
  usage_qoder: 'qoder',
}
const PLATFORM_USAGE_COLUMNS = USAGE_COLUMN_KEYS.filter((k) => k !== 'usage')
const hasVisibleUsageColumn = computed(
  () => !hiddenColumns.has('usage') || PLATFORM_USAGE_COLUMNS.some((k) => !hiddenColumns.has(k)),
)
// Visibility predicates decide whether group and attribute supporting data are needed
// for the currently displayed user columns.
const hasVisibleGroupsColumn = computed(() => !hiddenColumns.has('groups'))
const hasVisibleAttributeColumns = computed(() =>
  attributeDefinitions.value.some((def) => def.enabled && !hiddenColumns.has(`attr_${def.id}`)),
)

// columns excludes hidden keys while retaining email and actions for every row of the
// loaded user page.
const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (col) => col.key === 'email' || col.key === 'actions' || !hiddenColumns.has(col.key),
  ),
)

// Primary user rows, their loading indicator, search text, and saved-sort key support
// the server-paginated table controls.
const users = ref<AdminUser[]>([])
const loading = ref(false)
const searchQuery = ref('')
const USER_SORT_STORAGE_KEY = 'admin-users-table-sort'
// loadInitialSortState restores a supported server sort from browser storage. Missing
// or invalid saved keys use descending creation time; accepted keys use ascending only
// when the stored order is exactly asc.
const loadInitialSortState = (): {
  sort_by: string // server sorting field
  sort_order: 'asc' | 'desc' // server sorting direction
} => {
  const fallback = { sort_by: 'created_at', sort_order: 'desc' as 'asc' | 'desc' }
  const sortable = new Set([
    'email',
    'id',
    'username',
    'role',
    'balance',
    'balance_recharge_multiplier',
    'concurrency',
    'status',
    'last_used_at',
    'last_active_at',
    'created_at',
  ])
  try {
    const raw = localStorage.getItem(USER_SORT_STORAGE_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as {
      key?: string // selected column key
      order?: string // selected ordering direction
    }
    const key = typeof parsed.key === 'string' ? parsed.key : ''
    // Saved keys must belong to the server-supported sort set; invalid preferences
    // use creation order rather than requesting an unsupported column.
    if (!sortable.has(key)) return fallback
    return {
      sort_by: key,
      sort_order: parsed.order === 'asc' ? 'asc' : 'desc',
    }
  } catch {
    // Unreadable or malformed browser sort preferences use the default server
    // ordering so initialization can continue with a valid request.
    return fallback
  }
}
// sortState starts from the validated browser preference and supplies server ordering
// independently of current-page usage sorting.
const sortState = reactive(loadInitialSortState())

// allGroups supplies active choices for the group column and authorized-group filter,
// with lazy loading when those controls need them.
const allGroups = ref<AdminGroup[]>([])
// loadAllGroups fetches active groups when the current collection is empty. Failures
// are logged and keep the existing collection, so the group column and authorization
// filter can retry on a later call.
const loadAllGroups = async () => {
  if (allGroups.value.length > 0) return
  try {
    allGroups.value = await adminAPI.groups.getAll()
  } catch (e) {
    // Keep the current group collection when lookup fails, allowing the existing user
    // table to remain visible and a later call to retry.
    console.error('Failed to load groups:', e)
  }
}

// allGroupsForApiKeyFilter includes inactive groups so key bindings can be filtered
// even when their referenced group is disabled.
const allGroupsForApiKeyFilter = ref<AdminGroup[]>([])
// loadAllGroupsForApiKeyFilter loads groups including inactive entries when its
// collection is empty. This lets administrators find users whose keys still reference
// disabled groups; failures are logged and leave current choices available.
const loadAllGroupsForApiKeyFilter = async () => {
  if (allGroupsForApiKeyFilter.value.length > 0) return
  try {
    allGroupsForApiKeyFilter.value = await adminAPI.groups.getAllIncludingInactive()
  } catch (e) {
    // Keep the current key-group choices after lookup failure so existing filters
    // remain usable and a later opening can retry an empty collection.
    console.error('Failed to load groups for API key filter:', e)
  }
}
// getUserGroups separates the user's accessible active groups into exclusive and
// public collections. Inactive groups are skipped before either access rule is
// evaluated. Exclusive groups require their identifiers in allowed_groups, while
// public groups remain available unless their identifiers appear in
// disabled_public_groups. The returned arrays keep the loaded collection's order
// within each category, allowing the template to display counts, tooltips, and
// replacement actions against the same membership decision.
const getUserGroups = (user: AdminUser) => {
  const exclusive: AdminGroup[] = []
  const publicGroups: AdminGroup[] = []
  const disabledPublicGroups = new Set(user.disabled_public_groups || [])
  for (const g of allGroups.value) {
    if (g.status !== 'active') continue
    if (g.is_exclusive) {
      if (user.allowed_groups?.includes(g.id)) {
        exclusive.push(g)
      }
    } else if (!disabledPublicGroups.has(g.id)) {
      publicGroups.push(g)
    }
  }
  return { exclusive, publicGroups }
}

// Authorized-group choices include active exclusive groups and send their names for
// fuzzy matching in the user-list endpoint.
const groupFilterOptions = computed(() => {
  const options: {
    value: string // selected option value
    label: string // displayed option label
  }[] = [{ value: '', label: t('admin.users.allAuthorizedGroups') }]
  for (const g of allGroups.value) {
    if (g.status !== 'active' || !g.is_exclusive) continue
    options.push({ value: g.name, label: g.name })
  }
  return options
})

// API-key group choices use numeric identifiers and include exclusive, public, and
// disabled sections from the complete group collection.
const apiKeyGroupFilterOptions = computed(
  () =>
    buildApiKeyGroupFilterOptions(allGroupsForApiKeyFilter.value, {
      all: t('admin.users.allApiKeyGroups'),
      exclusive: t('admin.users.apiKeyGroupExclusive'),
      public: t('admin.users.apiKeyGroupPublic'),
      disabled: t('admin.users.apiKeyGroupDisabled'),
    }) as SelectOption[],
)

// Built-in conditions and attribute values store server query filters; empty text and
// null API-key group mean unselected conditions.
const filters = reactive({
  role: '', // account role condition
  status: '', // configured lifecycle status
  // group sends a name for fuzzy authorized-group matching; empty text omits this
  // condition from the user-list query.
  group: '', // authorized group name
  // apiKeyGroup filters bindings by numeric group identifier, including inactive
  // choices; null omits this independent condition from the user-list query.
  apiKeyGroup: null as number | null, // bound key-group identifier
})
const activeAttributeFilters = reactive<Record<number, string>>({})

// visibleFilters records which built-in or attr_<id> controls are shown independently
// of their current matching values.
const visibleFilters = reactive<Set<string>>(new Set())

// Filter and column dropdown flags pair with DOM references used by the document click
// handler to close them.
const showFilterDropdown = ref(false)
const showColumnDropdown = ref(false)

const filterDropdownRef = ref<HTMLElement | null>(null)
const columnDropdownRef = ref<HTMLElement | null>(null)

// Browser storage keeps condition values and visible-control keys separately so both
// can be restored when this page mounts.
const FILTER_VALUES_KEY = 'user-filter-values'
const VISIBLE_FILTERS_KEY = 'user-visible-filters'

// filterableAttributes and builtInFilters describe the available filter controls using
// enabled definitions and the current translated labels.
const filterableAttributes = computed(() => attributeDefinitions.value.filter((def) => def.enabled))

const builtInFilters = computed(() => [
  { key: 'role', name: t('admin.users.columns.role'), type: 'select' as const },
  { key: 'status', name: t('admin.users.columns.status'), type: 'select' as const },
  { key: 'group', name: t('admin.users.authorizedGroupFilter'), type: 'select' as const },
  { key: 'apiKeyGroup', name: t('admin.users.apiKeyGroupFilter'), type: 'select' as const },
])

// loadSavedFilters restores visible controls and saved filter values from browser
// storage. Numeric API-key group identifiers and custom-attribute values are restored
// separately; parse or storage failures are logged while already applied values remain
// in memory.
const loadSavedFilters = () => {
  try {
    const savedVisible = localStorage.getItem(VISIBLE_FILTERS_KEY)
    if (savedVisible) {
      const parsed = JSON.parse(savedVisible) as string[]
      parsed.forEach((key) => visibleFilters.add(key))
    }
    const savedValues = localStorage.getItem(FILTER_VALUES_KEY)
    if (savedValues) {
      const parsed = JSON.parse(savedValues)
      if (parsed.role) filters.role = parsed.role
      if (parsed.status) filters.status = parsed.status
      if (parsed.group) filters.group = parsed.group
      if (typeof parsed.apiKeyGroup === 'number') filters.apiKeyGroup = parsed.apiKeyGroup
      if (parsed.attributes) {
        Object.assign(activeAttributeFilters, parsed.attributes)
      }
    }
  } catch (e) {
    // Storage parsing may fail after some entries have been applied; retain those
    // in-memory conditions and allow the user page to continue initializing.
    console.error('Failed to load saved filters:', e)
  }
}

// saveFiltersToStorage writes visible controls followed by the current filter values.
// Storage errors are logged while active in-memory conditions remain usable; the two
// browser entries are written separately and may persist independently.
const saveFiltersToStorage = () => {
  try {
    // Persist visible-control keys before condition values so the saved UI and query
    // drafts follow the same application order used while restoring preferences.
    localStorage.setItem(VISIBLE_FILTERS_KEY, JSON.stringify([...visibleFilters]))
    const values = {
      role: filters.role,
      status: filters.status,
      group: filters.group,
      apiKeyGroup: filters.apiKeyGroup,
      attributes: activeAttributeFilters,
    }
    localStorage.setItem(FILTER_VALUES_KEY, JSON.stringify(values))
  } catch (e) {
    // Storage errors leave active in-memory filters usable, while separate entries
    // may retain whichever write completed before the failure occurred.
    console.error('Failed to save filters:', e)
  }
}

// getAttributeDefinition returns the currently loaded definition matching an attribute
// identifier.
const getAttributeDefinition = (attrId: number): UserAttributeDefinition | undefined => {
  return attributeDefinitions.value.find((d) => d.id === attrId)
}
// usageStats holds batch usage keyed by user identity for aggregate/platform cells and
// optional current-page cost sorting.
const usageStats = ref<Record<string, BatchUserUsageStats>>({})

// getPlatformUsage finds one user's loaded usage entry for the requested platform.
const getPlatformUsage = (userId: number, platform: string) =>
  usageStats.value[userId]?.by_platform?.find((p) => p.platform === platform)

// UsageMetric selects today or total cost for current-page sorting.
type UsageMetric = 'today' | 'total'
// UsageSortState records a local cost ordering, with null selecting the server row
// order.
type UsageSortState = {
  key: string // selected column key
  metric: UsageMetric // selected cost period
  order: 'asc' | 'desc' // selected ordering direction
} | null
// Usage-sort storage and its single open metric menu remain separate from server
// sorting and column visibility preferences.
const USAGE_SORT_STORAGE_KEY = 'admin-users-usage-sort'
const openUsageSortMenu = ref<string | null>(null)

// loadInitialUsageSort restores a recognized usage-column sort or returns null. Unknown
// keys and unreadable storage disable local sorting; recognized entries default to
// today's metric and descending order unless supported alternatives are stored.
const loadInitialUsageSort = (): UsageSortState => {
  try {
    const raw = localStorage.getItem(USAGE_SORT_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<{
      key: string // selected column key
      metric: string // selected cost period
      order: string // selected ordering direction
    }>
    if (!parsed.key || !USAGE_COLUMN_KEYS.includes(parsed.key)) return null
    const metric: UsageMetric = parsed.metric === 'total' ? 'total' : 'today'
    const order: 'asc' | 'desc' = parsed.order === 'asc' ? 'asc' : 'desc'
    return { key: parsed.key, metric, order }
  } catch {
    // Malformed or unavailable saved usage sorting disables local ordering so the
    // server page can still be rendered in its returned order.
    return null
  }
}
// usageSort holds the restored current-page cost ordering, or null when the loaded
// server row order applies.
const usageSort = ref<UsageSortState>(loadInitialUsageSort())
// persistUsageSort writes the current local usage sort or removes its stored key.
// Storage errors are logged while in-memory sorting remains active, allowing the
// current table order to follow the selection.
const persistUsageSort = () => {
  try {
    if (usageSort.value) {
      localStorage.setItem(USAGE_SORT_STORAGE_KEY, JSON.stringify(usageSort.value))
    } else {
      localStorage.removeItem(USAGE_SORT_STORAGE_KEY)
    }
  } catch (e) {
    // Retain the current in-memory ordering when browser storage rejects the
    // preference write, allowing the selected header action to affect this session.
    console.error('Failed to persist usage sort:', e)
  }
}
// clearUsageSort clears local usage ordering and its menu before updating storage. With
// no active usage sort it returns immediately, allowing server sort handlers to call it
// unconditionally before loading rows.
const clearUsageSort = () => {
  if (!usageSort.value) return
  usageSort.value = null
  openUsageSortMenu.value = null
  persistUsageSort()
}

// isUsageSortActive reports whether the selected column and metric match local sorting.
const isUsageSortActive = (key: string, metric: UsageMetric) =>
  !!usageSort.value && usageSort.value.key === key && usageSort.value.metric === metric
// getUsageSortOrder returns the active direction for a matching column and metric. It
// returns null for other pairs so each header control can distinguish its inactive
// state from either selected direction.
const getUsageSortOrder = (key: string, metric: UsageMetric): 'asc' | 'desc' | null =>
  isUsageSortActive(key, metric) ? usageSort.value!.order : null

// toggleUsageSort cycles the selected column and metric through descending, ascending,
// and off. Choosing a different pair starts descending; the resulting preference is
// persisted and the metric menu closes after selection.
const toggleUsageSort = (key: string, metric: UsageMetric) => {
  const cur = usageSort.value
  if (cur && cur.key === key && cur.metric === metric) {
    usageSort.value = cur.order === 'desc' ? { key, metric, order: 'asc' } : null
  } else {
    usageSort.value = { key, metric, order: 'desc' }
  }
  persistUsageSort()
  openUsageSortMenu.value = null
}

// toggleUsageSortMenu toggles one column's metric menu without changing the selected
// ordering.
const toggleUsageSortMenu = (key: string) => {
  openUsageSortMenu.value = openUsageSortMenu.value === key ? null : key
}

// getUsageValue reads the selected aggregate or platform cost for a user. Missing user
// or platform data becomes zero, and the metric selects today's cost or the returned
// total used by local sorting.
const getUsageValue = (userId: number, key: string, metric: UsageMetric): number => {
  const stats = usageStats.value[userId]
  // A user with no loaded usage contributes zero to local cost sorting until its
  // secondary response supplies an observed value.
  if (!stats) return 0
  const platform = USAGE_COLUMN_PLATFORMS[key]
  if (platform === null) {
    return metric === 'today' ? (stats.today_actual_cost ?? 0) : (stats.total_actual_cost ?? 0)
  }
  const p = stats.by_platform?.find((x) => x.platform === platform)
  // An absent platform entry contributes zero for this column so users can still be
  // compared while platform-specific observations are missing.
  if (!p) return 0
  return metric === 'today' ? (p.today_actual_cost ?? 0) : (p.total_actual_cost ?? 0)
}

// sortedUsers applies the selected usage ordering to the currently loaded user page.
// With no local sort it returns the server array directly; active sorting copies the
// array before comparing costs. Usage values are obtained from the latest secondary
// data and missing observations count as zero. Original row indices break equal-cost
// ties, keeping the server's relative order for those users while new usage values
// arrive.
const sortedUsers = computed(() => {
  const s = usageSort.value
  if (!s) return users.value
  // Sort a copied page and use original indices to break equal-cost ties so
  // asynchronous usage values cannot arbitrarily shuffle equal rows.
  return [...users.value]
    .map((row, index) => ({ row, index }))
    .sort((a, b) => {
      const av = getUsageValue(a.row.id, s.key, s.metric)
      const bv = getUsageValue(b.row.id, s.key, s.metric)
      if (av !== bv) return s.order === 'asc' ? av - bv : bv - av
      return a.index - b.index
    })
    .map((x) => x.row)
})

// The selection composable retains user identifiers across page changes and clears them
// explicitly after successful bulk updates.
const {
  selectedIds,
  selectedCount,
  setSelectedIds,
  clear: clearSelection,
} = useTableSelection<AdminUser>({
  rows: sortedUsers,
  getId: (user) => user.id,
})

// handleSelectedKeysUpdate retains numeric identifiers from table selection events and
// stores them.
const handleSelectedKeysUpdate = (keys: Array<string | number>) => {
  setSelectedIds(keys.filter((key): key is number => typeof key === 'number'))
}

// getUserSelectionLabel formats the accessible bulk-selection label using the user's
// email.
const getUserSelectionLabel = (user: AdminUser) =>
  t('admin.users.bulkLimits.selectUser', { email: user.email })

// Enabled definitions and per-user attribute values feed dynamic columns, while
// pagination stores the primary list response counts.
const attributeDefinitions = ref<UserAttributeDefinition[]>([])
const userAttributeValues = ref<Record<number, Record<number, string>>>({})
const pagination = reactive({
  page: 1, // requested page number
  page_size: getPersistedPageSize(), // requested row count
  total: 0, // matching row count
  pages: 0, // matching page count
})

// Dialog visibility and selected account records isolate creation, editing, bulk
// updates, deletion, key inspection, and attribute configuration.
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showBulkEditModal = ref(false)
const showDeleteDialog = ref(false)
const showApiKeysModal = ref(false)
const showAttributesModal = ref(false)
const editingUser = ref<AdminUser | null>(null)
const deletingUser = ref<AdminUser | null>(null)
const viewingUser = ref<AdminUser | null>(null)

// The primary controller cancels list requests, while secondaryDataSeq invalidates
// deferred usage and attribute work when its page becomes obsolete.
let abortController: AbortController | null = null
let secondaryDataSeq = 0

// loadUsersSecondaryData fetches usage and attribute values required by visible
// columns. The supplied user identifiers describe one loaded page, and optional
// abort/generation guards prevent results from being applied after that page becomes
// obsolete. Usage and attribute requests run independently, logging their own failures
// while keeping other work available. The function waits for all scheduled tasks to
// settle, and returns immediately when the identifier list is empty.
const loadUsersSecondaryData = async (
  userIds: number[],
  signal?: AbortSignal,
  expectedSeq?: number,
) => {
  if (userIds.length === 0) return

  const tasks: Promise<void>[] = []

  if (hasVisibleUsageColumn.value) {
    tasks.push(
      (async () => {
        try {
          const usageResponse = await adminAPI.dashboard.getBatchUsersUsage(userIds)
          // The primary page may have been replaced while optional data was loading;
          // aborted page work cannot publish or report into its successor.
          if (signal?.aborted) return
          // An optional refresh can supersede earlier lookups without aborting their
          // transport, so generation checks also guard publication to the current
          // page.
          if (typeof expectedSeq === 'number' && expectedSeq !== secondaryDataSeq) return
          usageStats.value = usageResponse.stats
        } catch (e) {
          // The primary page may have been replaced while optional data was loading;
          // aborted page work cannot publish or report into its successor.
          if (signal?.aborted) return
          // Keep other optional work and primary rows available after usage lookup
          // failure; the missing usage cells retain their current values.
          console.error('Failed to load usage stats:', e)
        }
      })(),
    )
  }

  if (attributeDefinitions.value.length > 0 && hasVisibleAttributeColumns.value) {
    tasks.push(
      (async () => {
        try {
          const attrResponse = await adminAPI.userAttributes.getBatchUserAttributes(userIds)
          // The primary page may have been replaced while optional data was loading;
          // aborted page work cannot publish or report into its successor.
          if (signal?.aborted) return
          // An optional refresh can supersede earlier lookups without aborting their
          // transport, so generation checks also guard publication to the current
          // page.
          if (typeof expectedSeq === 'number' && expectedSeq !== secondaryDataSeq) return
          userAttributeValues.value = attrResponse.attributes
        } catch (e) {
          // The primary page may have been replaced while optional data was loading;
          // aborted page work cannot publish or report into its successor.
          if (signal?.aborted) return
          // Attribute lookup failure leaves the primary page and independent usage
          // work available, with current attribute values retained until another
          // successful refresh.
          console.error('Failed to load user attribute values:', e)
        }
      })(),
    )
  }

  if (tasks.length > 0) {
    await Promise.allSettled(tasks)
  }
}

// refreshCurrentPageSecondaryData starts a new generation for the current page's
// optional data. Empty pages return immediately, while advancing secondaryDataSeq
// invalidates older optional results before the new requests begin without a list
// signal.
const refreshCurrentPageSecondaryData = () => {
  const userIds = users.value.map((u) => u.id)
  if (userIds.length === 0) return
  // Advance optional-data ownership before dispatch so an older lookup cannot publish
  // into the refreshed current-page usage or attribute maps.
  const seq = ++secondaryDataSeq
  void loadUsersSecondaryData(userIds, undefined, seq)
}

// The active menu identifier and viewport coordinates describe one floating user-action
// menu at a time.
const activeMenuId = ref<number | null>(null)
const menuPosition = ref<{
  top: number // viewport top pixels
  left: number // viewport left pixels
} | null>(null)

// openActionMenu toggles a user action menu using the row trigger and viewport. Narrow
// screens center around the trigger with vertical flipping, while wider screens
// position near the pointer and clamp to available space.
const openActionMenu = (user: AdminUser, e: MouseEvent) => {
  if (activeMenuId.value === user.id) {
    closeActionMenu()
  } else {
    const target = e.currentTarget as HTMLElement
    if (!target) {
      closeActionMenu()
      return
    }

    const rect = target.getBoundingClientRect()
    // The position calculation reserves the menu footprint and viewport padding
    // before clamping the floating action panel around its trigger.
    const menuWidth = 200
    const menuHeight = 240
    const padding = 8
    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight

    let left, top

    // Below the desktop breakpoint, anchor around the trigger and flip vertically
    // when necessary so the action panel stays within the viewport.
    if (viewportWidth < 768) {
      left = Math.max(
        padding,
        Math.min(rect.left + rect.width / 2 - menuWidth / 2, viewportWidth - menuWidth - padding),
      )

      top = rect.bottom + 4

      // When the preferred placement exceeds available height, adjust the panel
      // before publishing coordinates so its actions remain reachable inside the
      // viewport.
      if (top + menuHeight > viewportHeight - padding) {
        top = rect.top - menuHeight - 4
        // If neither vertical side fits, clamp to the viewport inset so the panel
        // begins at a visible coordinate.
        if (top < padding) {
          top = padding
        }
      }
    } else {
      left = Math.max(padding, Math.min(e.clientX - menuWidth, viewportWidth - menuWidth - padding))
      top = e.clientY
      // When the preferred placement exceeds available height, adjust the panel
      // before publishing coordinates so its actions remain reachable inside the
      // viewport.
      if (top + menuHeight > viewportHeight - padding) {
        top = viewportHeight - menuHeight - padding
      }
    }

    menuPosition.value = { top, left }
    activeMenuId.value = user.id
  }
}

// closeActionMenu clears the active user action menu and its coordinates.
const closeActionMenu = () => {
  activeMenuId.value = null
  menuPosition.value = null
}

// handleClickOutside closes action, filter, column, usage-sort, and expanded-group
// menus as applicable. Element containment protects the corresponding menu controls,
// while the expanded group selection closes whenever this document handler runs.
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!target.closest('.action-menu-trigger') && !target.closest('.action-menu-content')) {
    closeActionMenu()
  }
  if (filterDropdownRef.value && !filterDropdownRef.value.contains(target)) {
    showFilterDropdown.value = false
  }
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
  if (openUsageSortMenu.value !== null && !target.closest('.usage-sort-trigger')) {
    openUsageSortMenu.value = null
  }
  if (expandedGroupUserId.value !== null) {
    expandedGroupUserId.value = null
  }
}

// Group-permission dialog visibility and its selected user control the child editor
// until the dialog is closed.
const showAllowedGroupsModal = ref(false)
const allowedGroupsUser = ref<AdminUser | null>(null)

// expandedGroupUserId selects one row whose exclusive-group list is expanded, or null
// when all such lists are closed.
const expandedGroupUserId = ref<number | null>(null)
// toggleExpandedGroup toggles the expanded exclusive-group list for one user
// identifier.
const toggleExpandedGroup = (userId: number) => {
  expandedGroupUserId.value = expandedGroupUserId.value === userId ? null : userId
}

// Group replacement retains its dialog visibility, account, and old group so the child
// can submit the intended substitution.
const showGroupReplaceModal = ref(false)
const groupReplaceUser = ref<AdminUser | null>(null)
const groupReplaceOldGroup = ref<{
  id: number // server record identifier
  name: string // displayed name text
} | null>(null)

// Balance dialog visibility, account, and addition/subtraction mode determine the
// operation offered by the shared child editor.
const showBalanceModal = ref(false)
const balanceUser = ref<AdminUser | null>(null)
const balanceOperation = ref<'add' | 'subtract'>('add')

// Balance-history visibility and its account also supply the target when history
// actions open deposit or withdrawal. The refresh key advances after each completed
// edit so the open history reloads the new entry and totals.
const showBalanceHistoryModal = ref(false)
const balanceHistoryUser = ref<AdminUser | null>(null)
const balanceHistoryRefreshKey = ref(0)

// balanceDialogZIndex lifts the balance editor above the history dialog that can open
// it. Both dialogs otherwise share the default modal layer, where teleport order paints
// the later mounted history surface on top.
const balanceDialogZIndex = computed(() =>
  showBalanceHistoryModal.value ? Z_INDEX.MODAL_NESTED : Z_INDEX.MODAL,
)

// getDaysRemaining returns the ceiling of days between now and subscription expiry.
// Expired values may be zero or negative because this helper exposes the rounded
// difference directly to its display callers.
const getDaysRemaining = (expiresAt: string): number => {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diffMs = expires.getTime() - now.getTime()
  // Convert elapsed milliseconds to days and round partial days upward.
  return Math.ceil(diffMs / (1000 * 60 * 60 * 24))
}

// loadAttributeDefinitions requests enabled custom fields used by columns and filters.
// Failures are logged while current definitions remain available, allowing the user
// table to continue loading its primary rows independently.
const loadAttributeDefinitions = async () => {
  try {
    attributeDefinitions.value = await adminAPI.userAttributes.listEnabledDefinitions()
  } catch (e) {
    // Retain existing definitions when the lookup fails so table initialization can
    // continue and current custom fields remain available for rendering.
    console.error('Failed to load attribute definitions:', e)
  }
}

// handleAttributesModalClose hides attribute configuration and refreshes definitions
// before reloading users. Waiting for definitions lets the subsequent list refresh
// decide which secondary attribute values are needed for the visible columns.
const handleAttributesModalClose = async () => {
  showAttributesModal.value = false
  await loadAttributeDefinitions()
  loadUsers()
}

// loadUsers aborts the previous list request and fetches filtered, sorted user rows.
// Accepted responses replace rows and pagination and clear usage and attribute caches
// for that page. A short deferred callback starts optional data only if its signal and
// generation still match, allowing primary rows to render first. Cancellation errors
// remain silent, while other failures report a message; loading cleanup belongs to the
// current list controller.
const loadUsers = async () => {
  // Cancel the preceding primary page before starting its replacement so deferred
  // callbacks can also recognize the aborted page through its captured signal.
  abortController?.abort()
  const currentAbortController = new AbortController()
  abortController = currentAbortController
  const { signal } = currentAbortController
  loading.value = true
  try {
    const attrFilters: Record<number, string> = {}
    for (const [attrId, value] of Object.entries(activeAttributeFilters)) {
      if (value) {
        attrFilters[Number(attrId)] = value
      }
    }

    const response = await adminAPI.users.list(
      pagination.page,
      pagination.page_size,
      {
        role: filters.role as any,
        status: filters.status as any,
        search: searchQuery.value || undefined,
        group_name: filters.group || undefined,
        api_key_group_id: filters.apiKeyGroup ?? undefined,
        attributes: Object.keys(attrFilters).length > 0 ? attrFilters : undefined,
        include_subscriptions: true,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      { signal },
    )
    // Discard a response after its controller was aborted because pagination,
    // filtering, or teardown has already replaced that primary page.
    if (signal.aborted) {
      return
    }
    users.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
    // Clear secondary maps when replacing user rows so observations from the
    // preceding page are not presented as data for the new page.
    usageStats.value = {}
    userAttributeValues.value = {}

    if (response.items.length > 0) {
      const userIds = response.items.map((u) => u.id)
      // Advance optional-data ownership before dispatch so an older lookup cannot
      // publish into the refreshed current-page usage or attribute maps.
      const seq = ++secondaryDataSeq
      // Defer optional lookups for fifty milliseconds so primary rows can render
      // first; the captured signal and generation still gate the delayed callback.
      window.setTimeout(() => {
        if (signal.aborted || seq !== secondaryDataSeq) return
        void loadUsersSecondaryData(userIds, signal, seq)
      }, 50)
    }
  } catch (error: any) {
    const errorInfo = error as {
      name?: string // transport exception name
      code?: string // transport error code
    }
    // Cancellation is expected when user queries are replaced, so suppress those
    // transport errors and reserve feedback for failed list operations.
    if (
      errorInfo?.name === 'AbortError' ||
      errorInfo?.name === 'CanceledError' ||
      errorInfo?.code === 'ERR_CANCELED'
    ) {
      return
    }
    const message = error.response?.data?.detail || error.message || t('admin.users.failedToLoad')
    appStore.showError(message)
    console.error('Error loading users:', error)
  } finally {
    // Only the current primary request releases list loading, because an older
    // request can finish after a newer query has started.
    if (abortController === currentAbortController) {
      loading.value = false
    }
  }
}

// handleBulkLimitsSuccess clears selected identifiers and awaits a fresh user page.
const handleBulkLimitsSuccess = async () => {
  clearSelection()
  await loadUsers()
}

// searchTimeout owns the delayed first-page query and is cancelled by later typing or
// component teardown.
let searchTimeout: ReturnType<typeof setTimeout>
// handleSearch replaces the pending timer with a delayed first-page user search.
// Unmount clears this timer and aborts the primary request, preventing queued search
// work from starting after page teardown.
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadUsers()
  }, 300)
}

// handlePageChange clamps the requested page to the known range before loading it. A
// missing page count permits page one, so callers cannot request a zero or negative
// page through this handler.
const handlePageChange = (page: number) => {
  // Clamp page controls to the available server page range, using page one when total
  // pages has not been established.
  const validPage = Math.max(1, Math.min(page, pagination.pages || 1))
  pagination.page = validPage
  loadUsers()
}

// handlePageSizeChange stores the selected page size and reloads from page one.
// Resetting the current page before requesting rows presents the beginning of the
// newly sized result set.
const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadUsers()
}

// handleSort clears local usage ordering before applying a server sort and reloading.
// This restores server row order as the table's active ordering and resets pagination
// to the first matching page.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  clearUsageSort()
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadUsers()
}

// getAttributeDefinitionName returns a field label or its identifier as display text.
// Missing definitions use the identifier so saved attribute filters can still be
// labeled when their definition is unavailable.
const getAttributeDefinitionName = (attrId: number): string => {
  const def = attributeDefinitions.value.find((d) => d.id === attrId)
  return def?.name || String(attrId)
}

// toggleBuiltInFilter shows or hides a built-in condition and saves the resulting
// filter state. Hiding clears its value; showing group controls requests their choices
// before the first-page list reload is started.
const toggleBuiltInFilter = (key: string) => {
  if (visibleFilters.has(key)) {
    visibleFilters.delete(key)
    if (key === 'role') filters.role = ''
    if (key === 'status') filters.status = ''
    if (key === 'group') filters.group = ''
    if (key === 'apiKeyGroup') filters.apiKeyGroup = null
  } else {
    visibleFilters.add(key)
    if (key === 'group') loadAllGroups()
    if (key === 'apiKeyGroup') loadAllGroupsForApiKeyFilter()
  }
  saveFiltersToStorage()
  pagination.page = 1
  loadUsers()
}

// toggleAttributeFilter shows or removes a custom-field filter and persists its state.
// New controls start with an empty value, while removing a control deletes its matching
// condition before page one reloads.
const toggleAttributeFilter = (attr: UserAttributeDefinition) => {
  const key = `attr_${attr.id}`
  if (visibleFilters.has(key)) {
    visibleFilters.delete(key)
    delete activeAttributeFilters[attr.id]
  } else {
    visibleFilters.add(key)
    activeAttributeFilters[attr.id] = ''
  }
  saveFiltersToStorage()
  pagination.page = 1
  loadUsers()
}

// updateAttributeFilter changes the draft condition for one custom attribute
// identifier.
const updateAttributeFilter = (attrId: number, value: string) => {
  activeAttributeFilters[attrId] = value
}

// applyFilter persists current conditions and reloads the presently selected user page.
const applyFilter = () => {
  saveFiltersToStorage()
  loadUsers()
}

// handleEdit selects a user and opens the account editing modal.
const handleEdit = (user: AdminUser) => {
  editingUser.value = user
  showEditModal.value = true
}

// closeEditModal hides account editing and releases the selected user record.
const closeEditModal = () => {
  showEditModal.value = false
  editingUser.value = null
}

// handleToggleStatus switches the user's active state and reloads after success.
// Request failure displays the endpoint detail or translated message, keeping the
// current rows available until a successful reload replaces them.
const handleToggleStatus = async (user: AdminUser) => {
  const newStatus = user.status === 'active' ? 'disabled' : 'active'
  try {
    await adminAPI.users.toggleStatus(user.id, newStatus)
    appStore.showSuccess(
      newStatus === 'active' ? t('admin.users.userEnabled') : t('admin.users.userDisabled'),
    )
    loadUsers()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.users.failedToToggle'))
    console.error('Error toggling user status:', error)
  }
}

// handleViewApiKeys selects the user whose API keys the modal displays.
const handleViewApiKeys = (user: AdminUser) => {
  viewingUser.value = user
  showApiKeysModal.value = true
}

// closeApiKeysModal hides API-key inspection and clears its selected user.
const closeApiKeysModal = () => {
  showApiKeysModal.value = false
  viewingUser.value = null
}

// handleAllowedGroups selects the user whose group permissions the modal edits.
const handleAllowedGroups = (user: AdminUser) => {
  allowedGroupsUser.value = user
  showAllowedGroupsModal.value = true
}

// closeAllowedGroupsModal hides group permissions and clears the selected account.
const closeAllowedGroupsModal = () => {
  showAllowedGroupsModal.value = false
  allowedGroupsUser.value = null
}

// openGroupReplace closes the expanded list and selects a user/group replacement
// target.
const openGroupReplace = (
  user: AdminUser,
  group: {
    id: number // server record identifier
    name: string // displayed name text
  },
) => {
  expandedGroupUserId.value = null
  groupReplaceUser.value = user
  groupReplaceOldGroup.value = group
  showGroupReplaceModal.value = true
}

// closeGroupReplaceModal hides replacement and clears its selected user and group.
const closeGroupReplaceModal = () => {
  showGroupReplaceModal.value = false
  groupReplaceUser.value = null
  groupReplaceOldGroup.value = null
}

// handleDelete selects the account awaiting confirmation of a deletion request.
const handleDelete = (user: AdminUser) => {
  deletingUser.value = user
  showDeleteDialog.value = true
}

// confirmDelete removes the selected account and reloads users after success. With no
// selected account it returns immediately; failure retains confirmation state and
// reports the endpoint detail or translated error.
const confirmDelete = async () => {
  if (!deletingUser.value) return
  try {
    await adminAPI.users.delete(deletingUser.value.id)
    appStore.showSuccess(t('common.success'))
    showDeleteDialog.value = false
    deletingUser.value = null
    loadUsers()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.users.failedToDelete'))
    console.error('Error deleting user:', error)
  }
}

// handleDeposit selects the account and opens its balance dialog in addition mode.
const handleDeposit = (user: AdminUser) => {
  balanceUser.value = user
  balanceOperation.value = 'add'
  showBalanceModal.value = true
}

// handleWithdraw selects the account and opens its balance dialog in subtraction mode.
const handleWithdraw = (user: AdminUser) => {
  balanceUser.value = user
  balanceOperation.value = 'subtract'
  showBalanceModal.value = true
}

// closeBalanceModal hides balance adjustment and clears the selected account record.
const closeBalanceModal = () => {
  showBalanceModal.value = false
  balanceUser.value = null
}

// handleBalanceSuccess applies a completed adjustment to the open history dialog, asks it
// to reload its records, and refreshes the user table. The edit response can leave out
// list-only fields, so the update merges over the retained row record instead of
// replacing it.
const handleBalanceSuccess = (updatedUser: AdminUser) => {
  if (balanceHistoryUser.value?.id === updatedUser.id) {
    balanceHistoryUser.value = { ...balanceHistoryUser.value, ...updatedUser }
  }
  balanceHistoryRefreshKey.value += 1
  loadUsers()
}

// handleBalanceHistory selects the account whose balance history the modal displays.
const handleBalanceHistory = (user: AdminUser) => {
  balanceHistoryUser.value = user
  showBalanceHistoryModal.value = true
}

// closeBalanceHistoryModal hides balance history and clears its selected account.
const closeBalanceHistoryModal = () => {
  showBalanceHistoryModal.value = false
  balanceHistoryUser.value = null
}

// handleDepositFromHistory opens balance addition for the history dialog's selected
// account. With no selected history account it returns without opening another modal,
// so the action requires an active history target.
const handleDepositFromHistory = () => {
  if (balanceHistoryUser.value) {
    handleDeposit(balanceHistoryUser.value)
  }
}

// handleWithdrawFromHistory opens balance subtraction for the history dialog's selected
// account. With no selected history account it returns without opening another modal,
// so the action requires an active history target.
const handleWithdrawFromHistory = () => {
  if (balanceHistoryUser.value) {
    handleWithdraw(balanceHistoryUser.value)
  }
}

// handleScroll closes the floating user menu when captured scrolling changes its
// position.
const handleScroll = () => {
  closeActionMenu()
}

// Load custom definitions before restoring preferences so visibility-dependent
// optional lookups can use the enabled fields when the first user page arrives.
onMounted(async () => {
  await loadAttributeDefinitions()
  loadSavedFilters()
  loadSavedColumns()
  loadUsers()
  if (hasVisibleGroupsColumn.value || visibleFilters.has('group')) {
    loadAllGroups()
  }
  if (visibleFilters.has('apiKeyGroup')) {
    loadAllGroupsForApiKeyFilter()
  }
  // Install the matching click and capture-scroll handlers after setup; unmount
  // removes these same function references and scroll capture mode.
  document.addEventListener('click', handleClickOutside)
  window.addEventListener('scroll', handleScroll, true)
})

// Remove global listeners and cancel search/list work during teardown so detached
// menus and obsolete primary requests cannot keep updating this page.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  window.removeEventListener('scroll', handleScroll, true)
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
