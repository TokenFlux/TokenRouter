<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div
          class="flex flex-col justify-between gap-4 lg:flex-row lg:items-start"
        >
          <!-- 左侧：模糊搜索和筛选项，可自动换行。 -->
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-3">
            <div class="relative min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon
                name="search"
                size="md"
                class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.groups.searchGroups')"
                class="input pl-10"
                @input="handleSearch"
              />
            </div>
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
                <span v-if="activeFilterCount > 0" class="absolute -right-1 -top-1 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary-100 px-1.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ activeFilterCount }}</span>
              </button>
              <div v-if="showFilterDropdown" class="absolute left-auto right-0 top-full z-[60] mt-2 w-72 rounded-xl border border-gray-200 bg-white p-4 shadow-xl dark:border-dark-600 dark:bg-dark-900 sm:left-0 sm:right-auto" @click.stop>
                <div class="mb-3 flex items-center justify-between">
                  <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('common.filter') }}</div>
                  <button v-if="activeFilterCount > 0" type="button" class="text-xs font-medium text-primary-600 dark:text-primary-400" @click="resetGroupFilters">{{ t('common.reset') }}</button>
                </div>
                <div class="space-y-3">
                  <Select v-model="filters.platform" :options="platformFilterOptions" :placeholder="t('admin.groups.allPlatforms')" @change="loadGroups" />
                  <Select v-model="filters.status" :options="statusOptions" :placeholder="t('admin.groups.allStatus')" @change="loadGroups" />
                </div>
              </div>
            </div>
          </div>

          <!-- 右侧：刷新、排序和创建等操作。 -->
          <div
            class="flex w-full flex-shrink-0 flex-wrap items-center justify-end gap-3 lg:w-auto"
          >
            <button
              @click="loadGroups"
              :disabled="loading"
              class="btn btn-secondary h-9 w-9 shrink-0 p-0"
              :title="t('common.refresh')"
            >
              <Icon
                name="refresh"
                size="md"
                :class="loading ? 'animate-spin' : ''"
              />
            </button>
            <div class="relative" ref="columnDropdownRef">
              <button
                @click="showColumnDropdown = !showColumnDropdown"
                class="btn btn-secondary h-9 w-9 shrink-0 p-0"
                :title="t('admin.groups.columnSettings')"
              >
                <Icon name="grid" size="md" />
                <span class="hidden">{{ t("admin.groups.columnSettings") }}</span>
              </button>
              <div
                v-if="showColumnDropdown"
                class="absolute right-0 top-full z-50 mt-1 max-h-80 w-48 overflow-y-auto rounded-lg border border-gray-200 bg-white py-1 shadow-lg dark:border-dark-600 dark:bg-dark-800"
              >
                <button
                  v-for="col in toggleableColumns"
                  :key="col.key"
                  @click="toggleColumn(col.key)"
                  class="flex w-full items-center justify-between px-4 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700"
                >
                  <span>{{ col.label }}</span>
                  <Icon
                    v-if="isColumnVisible(col.key)"
                    name="check"
                    size="sm"
                    class="text-primary-500"
                    :stroke-width="2"
                  />
                </button>
              </div>
            </div>
            <button
              @click="openSortModal"
              class="btn btn-secondary h-9 w-9 shrink-0 p-0"
              :title="t('admin.groups.sortOrder')"
            >
              <Icon name="arrowsUpDown" size="md" />
            </button>
            <button
              @click="openCreateModal"
              class="btn btn-primary h-9 whitespace-nowrap"
              data-tour="groups-create-btn"
            >
              <Icon name="plus" size="md" class="mr-2" />
              {{ t("admin.groups.createGroup") }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="groups"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="sort_order"
          default-sort-order="asc"
          @sort="handleSort"
        >
          <template #cell-name="{ value, row }">
            <div class="flex items-center gap-2">
              <span class="font-medium text-gray-900 dark:text-white">{{
                value
              }}</span>
              <span
                v-if="row.is_default"
                class="inline-flex items-center rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400"
              >
                {{ t("admin.groups.defaultGroup.badge") }}
              </span>
            </div>
          </template>

          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400"
              >#{{ value }}</span
            >
          </template>

          <template #cell-platform="{ value }">
            <span
              :class="[
                'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium',
                value === 'anthropic'
                  ? 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
                  : value === 'openai'
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
                    : value === 'antigravity'
                      ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
                      : value === 'grok'
                        ? 'bg-zinc-200 text-zinc-800 dark:bg-zinc-700 dark:text-zinc-100'
                        : value === 'kimi'
                          ? 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-400'
                          : value === 'zhipu'
                            ? 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-400'
                            : value === 'deepseek'
                              ? 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400'
                              : 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
              ]"
            >
              <PlatformIcon :platform="value" size="xs" />
              {{ t("admin.groups.platforms." + value) }}
            </span>
          </template>

          <template #cell-display_brand="{ value }">
            <span v-if="value" :class="displayBrandBadgeClass(value)">
              <ProviderIcon :brand="String(value)" size="14px" />
              {{ displayBrandLabel(value) }}
            </span>
            <span v-else class="text-sm text-gray-700 dark:text-gray-300">-</span>
          </template>

          <template #cell-rate_multiplier="{ value }">
            <span class="text-sm text-gray-700 dark:text-gray-300"
              >{{ value }}x</span
            >
          </template>

          <template #cell-is_exclusive="{ value }">
            <span :class="['badge', value ? 'badge-primary' : 'badge-gray']">
              {{
                value ? t("admin.groups.exclusive") : t("admin.groups.public")
              }}
            </span>
          </template>

          <template #cell-session_isolation_enabled="{ value }">
            <span :class="['badge', value ? 'badge-warning' : 'badge-gray']">
              {{
                value
                  ? t("admin.groups.sessionIsolation.enabled")
                  : t("admin.groups.sessionIsolation.disabled")
              }}
            </span>
          </template>

          <template #cell-account_count="{ row }">
            <div class="space-y-0.5 text-xs">
              <div>
                <span class="text-gray-500 dark:text-gray-400">{{
                  t("admin.groups.accountsAvailable")
                }}</span>
                <span
                  class="ml-1 font-medium text-emerald-600 dark:text-emerald-400"
                  >{{ row.active_account_count || 0 }}</span
                >
                <span
                  class="ml-1 inline-flex items-center rounded bg-gray-100 px-1.5 py-0.5 font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
                  >{{ t("admin.groups.accountsUnit") }}</span
                >
              </div>
              <div v-if="row.rate_limited_account_count">
                <span class="text-gray-500 dark:text-gray-400">{{
                  t("admin.groups.accountsRateLimited")
                }}</span>
                <span
                  class="ml-1 font-medium text-amber-600 dark:text-amber-400"
                  >{{ row.rate_limited_account_count }}</span
                >
                <span
                  class="ml-1 inline-flex items-center rounded bg-gray-100 px-1.5 py-0.5 font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
                  >{{ t("admin.groups.accountsUnit") }}</span
                >
              </div>
              <div>
                <span class="text-gray-500 dark:text-gray-400">{{
                  t("admin.groups.accountsTotal")
                }}</span>
                <span
                  class="ml-1 font-medium text-gray-700 dark:text-gray-300"
                  >{{ row.account_count || 0 }}</span
                >
                <span
                  class="ml-1 inline-flex items-center rounded bg-gray-100 px-1.5 py-0.5 font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
                  >{{ t("admin.groups.accountsUnit") }}</span
                >
              </div>
            </div>
          </template>

          <template #cell-capacity="{ row }">
            <GroupCapacityBadge
              v-if="capacityMap.get(row.id)"
              :concurrency-used="capacityMap.get(row.id)!.concurrencyUsed"
              :concurrency-max="capacityMap.get(row.id)!.concurrencyMax"
              :sessions-used="capacityMap.get(row.id)!.sessionsUsed"
              :sessions-max="capacityMap.get(row.id)!.sessionsMax"
              :rpm-used="capacityMap.get(row.id)!.rpmUsed"
              :rpm-max="capacityMap.get(row.id)!.rpmMax"
            />
            <span v-else class="text-xs text-gray-400">—</span>
          </template>

          <template #cell-usage="{ row }">
            <div v-if="usageLoading" class="text-xs text-gray-400">—</div>
            <div v-else class="space-y-0.5 text-xs">
              <div class="text-gray-500 dark:text-gray-400">
                <span class="text-gray-400 dark:text-gray-500">{{
                  t("admin.groups.usageToday")
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300"
                  >{{
                    formatGroupBalance(usageMap.get(row.id)?.today_cost ?? 0)
                  }}</span
                >
              </div>
              <div class="text-gray-500 dark:text-gray-400">
                <span class="text-gray-400 dark:text-gray-500">{{
                  t("admin.groups.usageYesterday")
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300"
                  >{{
                    formatGroupBalance(usageMap.get(row.id)?.yesterday_cost ?? 0)
                  }}</span
                >
              </div>
              <div class="text-gray-500 dark:text-gray-400">
                <span class="text-gray-400 dark:text-gray-500">{{
                  t("admin.groups.usageTotal")
                }}</span>
                <span class="ml-1 font-medium text-gray-700 dark:text-gray-300"
                  >{{
                    formatGroupBalance(usageMap.get(row.id)?.total_cost ?? 0)
                  }}</span
                >
              </div>
            </div>
          </template>

          <template #cell-status="{ value }">
            <span
              :class="[
                'badge',
                value === 'active' ? 'badge-success' : 'badge-danger',
              ]"
            >
              {{ t("admin.accounts.status." + value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t("common.edit") }}</span>
              </button>
              <button
                type="button"
                data-testid="group-more"
                :title="t('common.more')"
                :aria-label="t('common.more')"
                aria-haspopup="menu"
                :aria-expanded="actionMenuGroup?.id === row.id"
                :aria-controls="actionMenuGroup?.id === row.id ? `group-action-menu-${row.id}` : undefined"
                @click="openGroupActionMenu(row, $event)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
              >
                <Icon name="more" size="sm" />
                <span class="text-xs">{{ t("common.more") }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.groups.noGroupsYet')"
              :description="t('admin.groups.createFirstGroup')"
              :action-text="t('admin.groups.createGroup')"
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

    <GroupActionMenu
      :show="actionMenuGroup !== null"
      :group="actionMenuGroup"
      :position="actionMenuPosition"
      :duplicating="actionMenuGroup !== null && duplicatingGroupIds.has(actionMenuGroup.id)"
      @close="closeGroupActionMenu"
      @duplicate="handleDuplicate"
      @rate-multipliers="handleRateMultipliers"
      @rpm-overrides="handleRPMOverrides"
      @delete="handleDelete"
    />

    <!-- Create Group Modal -->
    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.groups.createGroup')"
      width="wide"
      @close="closeCreateModal"
    >
      <form
        id="create-group-form"
        @submit.prevent="handleCreateGroup"
        novalidate
        class="group-dialog-form"
      >

        <GroupFormTabs ref="createGroupTabsRef" :platform="createForm.platform" id-prefix="create-group">
          <template #general>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.tabs.identity') }}</h4>
            <div data-group-field="name">
              <label class="input-label">{{ t("admin.groups.form.name") }}</label>
              <input
                v-model="createForm.name"
                type="text"
                required
                class="input"
                :placeholder="t('admin.groups.enterGroupName')"
                data-tour="group-form-name"
              />
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.description")
              }}</label>
              <textarea
                v-model="createForm.description"
                rows="3"
                class="input"
                :placeholder="t('admin.groups.optionalDescription')"
              ></textarea>
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.displayBrand")
              }}</label>
              <Select
                v-model="createForm.display_brand"
                :options="providerBrandOptions"
                :placeholder="t('admin.groups.displayBrandPlaceholder')"
                :search-placeholder="t('admin.groups.displayBrandPlaceholder')"
                :creatable-prefix="t('admin.groups.displayBrandCreatablePrefix')"
                searchable
                creatable
              />
              <p class="input-hint">{{ t("admin.groups.displayBrandHint") }}</p>
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.platform")
              }}</label>
              <Select
                v-model="createForm.platform"
                :options="platformOptions"
                data-tour="group-form-platform"
                @change="createForm.copy_accounts_from_group_ids = []"
              />
              <p class="input-hint">{{ t("admin.groups.platformHint") }}</p>
            </div>
            <div data-tour="group-form-exclusive">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.form.exclusive") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <!-- Tooltip Popover -->
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="mb-2 text-xs font-medium">
                        {{ t("admin.groups.exclusiveTooltip.title") }}
                      </p>
                      <p class="mb-2 text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.exclusiveTooltip.description") }}
                      </p>
                      <div class="rounded bg-gray-800 p-2 dark:bg-gray-700">
                        <p class="text-xs leading-relaxed text-gray-300">
                          <span
                            class="inline-flex items-center gap-1 text-primary-400"
                            ><Icon name="lightbulb" size="xs" />
                            {{ t("admin.groups.exclusiveTooltip.example") }}</span
                          >
                          {{ t("admin.groups.exclusiveTooltip.exampleContent") }}
                        </p>
                      </div>
                      <!-- Arrow -->
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="createForm.is_exclusive"
                  data-group-setting="is_exclusive"
                  :aria-label="t('admin.groups.form.exclusive')"
                  @update:model-value="createForm.is_exclusive = !createForm.is_exclusive"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    createForm.is_exclusive
                      ? t("admin.groups.exclusive")
                      : t("admin.groups.public")
                  }}
                </span>
              </div>
            </div>
            <div>
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.defaultGroup.title") }}
                </label>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="createForm.is_default"
                  data-group-setting="is_default"
                  :aria-label="t('admin.groups.defaultGroup.title')"
                  @update:model-value="createForm.is_default = !createForm.is_default"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    createForm.is_default
                      ? t("admin.groups.defaultGroup.enabled")
                      : t("admin.groups.defaultGroup.disabled")
                  }}
                </span>
              </div>
              <p class="input-hint">{{ t("admin.groups.defaultGroup.hint") }}</p>
            </div>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.tabs.scheduling') }}</h4>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.schedulerType")
              }}</label>
              <Select
                v-model="createForm.scheduler_type"
                :options="schedulerTypeOptions"
              />
              <p class="input-hint">{{ t("admin.groups.scheduler.hint") }}</p>
              <div
                v-if="createForm.scheduler_type === 'advanced'"
                class="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary-900/10 bg-primary-50/60 px-3 py-2.5 dark:border-dark-600 dark:bg-dark-800/70"
              >
                <div class="min-w-0 text-xs text-primary-900/70 dark:text-dark-200/80">
                  <span class="font-medium text-primary-900 dark:text-dark-50">{{ t('admin.groups.advancedSchedulerOverrides.label') }}</span>
                  <span class="ml-2">{{ formatAdvancedSchedulerOverridesSummary(createForm.advanced_scheduler_overrides) }}</span>
                </div>
                <button
                  type="button"
                  class="btn btn-secondary shrink-0 px-3 py-1.5 text-xs"
                  @click="openAdvancedSchedulerOverrides('create')"
                >
                  <Icon name="cog" size="sm" />
                  {{ t('admin.groups.advancedSchedulerOverrides.configure') }}
                </button>
              </div>
            </div>
            <div v-if="copyAccountsGroupOptions.length > 0">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.copyAccounts.title") }}
                </label>
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.copyAccounts.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <!-- 已选分组标签 -->
              <div
                v-if="createForm.copy_accounts_from_group_ids.length > 0"
                class="flex flex-wrap gap-1.5 mb-2"
              >
                <span
                  v-for="groupId in createForm.copy_accounts_from_group_ids"
                  :key="groupId"
                  class="inline-flex items-center gap-1 rounded-full bg-primary-100 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                >
                  {{
                    copyAccountsGroupOptions.find((o) => o.value === groupId)
                      ?.label || `#${groupId}`
                  }}
                  <button
                    type="button"
                    @click="
                      createForm.copy_accounts_from_group_ids =
                        createForm.copy_accounts_from_group_ids.filter(
                          (id) => id !== groupId,
                        )
                    "
                    class="ml-0.5 text-primary-500 hover:text-primary-700 dark:hover:text-primary-200"
                  >
                    <Icon name="x" size="xs" />
                  </button>
                </span>
              </div>
              <!-- 分组选择下拉 -->
              <Select
                :model-value="null"
                :options="copyAccountsGroupSelectOptions"
                :placeholder="t('admin.groups.copyAccounts.selectPlaceholder')"
                @change="addCreateCopyAccountsGroup"
              />
              <p class="input-hint">{{ t("admin.groups.copyAccounts.hint") }}</p>
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.unavailableFallback.title")
              }}</label>
              <Select
                v-model="createForm.unavailable_fallback_group_id"
                :options="unavailableFallbackGroupOptions"
                :placeholder="t('admin.groups.unavailableFallback.noFallback')"
              />
              <p class="input-hint">
                {{ t("admin.groups.unavailableFallback.hint") }}
              </p>
            </div>
            <div>
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.sessionIsolation.title") }}
                </label>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="createForm.session_isolation_enabled"
                  data-group-setting="session_isolation_enabled"
                  :aria-label="t('admin.groups.sessionIsolation.title')"
                  @update:model-value="createForm.session_isolation_enabled = !createForm.session_isolation_enabled"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    createForm.session_isolation_enabled
                      ? t("admin.groups.sessionIsolation.enabledText")
                      : t("admin.groups.sessionIsolation.disabledText")
                  }}
                </span>
              </div>
              <p class="input-hint">{{ t("admin.groups.sessionIsolation.hint") }}</p>
            </div>
            <div class="border-t pt-4" data-group-field="probe">
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t("admin.groups.availabilityProbe.title") }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.groups.availabilityProbe.hint") }}
                  </p>
                </div>
                <Toggle
                  :model-value="createForm.availability_probe_enabled"
                  data-group-setting="availability_probe_enabled"
                  :aria-label="t('admin.groups.availabilityProbe.title')"
                  @update:model-value="createForm.availability_probe_enabled = !createForm.availability_probe_enabled"
                />
              </div>
              <div
                v-if="createForm.availability_probe_enabled"
                class="grid gap-4 rounded-lg border border-gray-200 bg-gray-50/50 p-4 dark:border-dark-600 dark:bg-dark-800/40 md:grid-cols-2"
              >
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.model") }}</label>
                  <Select
                    data-group-field="probe-model"
                    v-model="createForm.availability_probe_model_id"
                    :options="createAvailabilityProbeModelOptions"
                    searchable
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.interval") }}</label>
                  <input
                    v-model.number="createForm.availability_probe_interval_minutes"
                    type="number"
                    min="1"
                    max="1440"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.timeout") }}</label>
                  <input
                    v-model.number="createForm.availability_probe_timeout_seconds"
                    type="number"
                    min="5"
                    max="120"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.maxRetries") }}</label>
                  <input
                    v-model.number="createForm.availability_probe_max_retries"
                    type="number"
                    min="0"
                    max="10"
                    step="1"
                    class="input"
                  />
                </div>
                <div class="md:col-span-2">
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.userAgent") }}</label>
                  <input
                    v-model="createForm.availability_probe_user_agent"
                    type="text"
                    maxlength="512"
                    class="input"
                    :placeholder="t('admin.groups.availabilityProbe.userAgentPlaceholder')"
                  />
                </div>
                <div class="md:col-span-2">
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.prompt") }}</label>
                  <textarea
                    data-group-field="probe-prompt"
                    v-model="createForm.availability_probe_prompt"
                    rows="3"
                    class="input"
                    :placeholder="t('admin.groups.availabilityProbe.promptPlaceholder')"
                  />
                </div>
              </div>
            </div>
          </template>
          <template #platform>
            <div
              v-if="supportsGroupOpenAIFast(createForm.platform)"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
              data-testid="create-openai-fast"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t("admin.groups.openaiFast.title") }}
              </h4>
              <!-- 互斥策略防止加速与关闭配置冲突。 -->
              <Select v-model="createForm.openai_fast_policy" data-group-setting="openai_fast_policy"
                :aria-label="t('admin.groups.openaiFast.policy')" :options="groupOpenAIFastPolicyOptions" />
              <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">
                {{ t("admin.groups.openaiFast.hint") }}
              </p>

            </div>
            <ReasoningEffortPolicyFields
              data-group-field="reasoning"
              v-if="createForm.platform === 'openai' || createForm.platform === 'anthropic'"
              ref="createReasoningEffortPolicyRef"
              id-prefix="create-group-reasoning"
              :platform="createForm.platform"
              v-model:max-effort="createForm.max_reasoning_effort"
              v-model:over-limit="createForm.max_reasoning_effort_over_limit"
              v-model:mappings="createForm.reasoning_effort_mappings"
            />
            <div
              v-if="
                ['openai', 'antigravity', 'anthropic', 'gemini'].includes(
                  createForm.platform,
                )
              "
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4 space-y-4"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t('admin.groups.accountFilters.title') }}
              </h4>

              <!-- require_oauth_only toggle -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="text-sm text-gray-600 dark:text-gray-400"
                    >{{ t('admin.groups.accountFilters.oauthOnly') }}</label
                  >
                  <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                    {{
                      createForm.require_oauth_only
                        ? "已启用 — 排除 API Key 类型账号"
                        : "未启用"
                    }}
                  </p>
                </div>
                <Toggle
                  :model-value="createForm.require_oauth_only"
                  data-group-setting="require_oauth_only"
                  :aria-label="t('admin.groups.accountFilters.oauthOnly')"
                  @update:model-value="createForm.require_oauth_only = !createForm.require_oauth_only"
                />
              </div>

              <!-- require_privacy_set toggle -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="text-sm text-gray-600 dark:text-gray-400"
                    >{{ t('admin.groups.accountFilters.privacyRequired') }}</label
                  >
                  <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                    {{
                      createForm.require_privacy_set
                        ? "已启用 — Privacy 未设置的账号将被排除"
                        : "未启用"
                    }}
                  </p>
                </div>
                <Toggle
                  :model-value="createForm.require_privacy_set"
                  data-group-setting="require_privacy_set"
                  :aria-label="t('admin.groups.accountFilters.privacyRequired')"
                  @update:model-value="createForm.require_privacy_set = !createForm.require_privacy_set"
                />
              </div>
            </div>
            <div v-if="createForm.platform === 'anthropic'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.modelRouting.title") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-80 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.modelRouting.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <!-- 启用开关 -->
              <div class="flex items-center gap-3 mb-3">
                <Toggle
                  :model-value="createForm.model_routing_enabled"
                  data-group-setting="model_routing_enabled"
                  :aria-label="t('admin.groups.modelRouting.title')"
                  @update:model-value="createForm.model_routing_enabled = !createForm.model_routing_enabled"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    createForm.model_routing_enabled
                      ? t("admin.groups.modelRouting.enabled")
                      : t("admin.groups.modelRouting.disabled")
                  }}
                </span>
              </div>
              <p
                v-if="!createForm.model_routing_enabled"
                class="text-xs text-gray-500 dark:text-gray-400 mb-3"
              >
                {{ t("admin.groups.modelRouting.disabledHint") }}
              </p>
              <p v-else class="text-xs text-gray-500 dark:text-gray-400 mb-3">
                {{ t("admin.groups.modelRouting.noRulesHint") }}
              </p>
              <!-- 路由规则列表（仅在启用时显示） -->
              <div v-if="createForm.model_routing_enabled" class="space-y-3">
                <div
                  v-for="rule in createModelRoutingRules"
                  :key="getCreateRuleRenderKey(rule)"
                  class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
                >
                  <div class="flex items-start gap-3">
                    <div class="flex-1 space-y-2">
                      <div>
                        <label class="input-label text-xs">{{
                          t("admin.groups.modelRouting.modelPattern")
                        }}</label>
                        <input
                          v-model="rule.pattern"
                          type="text"
                          class="input text-sm"
                          :placeholder="
                            t('admin.groups.modelRouting.modelPatternPlaceholder')
                          "
                        />
                      </div>
                      <div>
                        <label class="input-label text-xs">{{
                          t("admin.groups.modelRouting.accounts")
                        }}</label>
                        <!-- 已选账号标签 -->
                        <div
                          v-if="rule.accounts.length > 0"
                          class="flex flex-wrap gap-1.5 mb-2"
                        >
                          <span
                            v-for="account in rule.accounts"
                            :key="account.id"
                            class="inline-flex items-center gap-1 rounded-full bg-primary-100 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                          >
                            {{ account.name }}
                            <button
                              type="button"
                              @click="removeSelectedAccount(rule, account.id)"
                              class="ml-0.5 text-primary-500 hover:text-primary-700 dark:hover:text-primary-200"
                            >
                              <Icon name="x" size="xs" />
                            </button>
                          </span>
                        </div>
                        <!-- 账号搜索输入框 -->
                        <div class="relative account-search-container">
                          <input
                            v-model="
                              accountSearchKeyword[getCreateRuleSearchKey(rule)]
                            "
                            type="text"
                            class="input text-sm"
                            :placeholder="
                              t(
                                'admin.groups.modelRouting.searchAccountPlaceholder',
                              )
                            "
                            @input="searchAccountsByRule(rule)"
                            @focus="onAccountSearchFocus(rule)"
                          />
                          <!-- 搜索结果下拉框 -->
                          <div
                            v-if="
                              showAccountDropdown[getCreateRuleSearchKey(rule)] &&
                              accountSearchResults[getCreateRuleSearchKey(rule)]
                                ?.length > 0
                            "
                            class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:border-dark-600 dark:bg-dark-800"
                          >
                            <button
                              v-for="account in accountSearchResults[
                                getCreateRuleSearchKey(rule)
                              ]"
                              :key="account.id"
                              type="button"
                              @click="selectAccount(rule, account)"
                              class="w-full px-3 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
                              :class="{
                                'opacity-50': rule.accounts.some(
                                  (a) => a.id === account.id,
                                ),
                              }"
                              :disabled="
                                rule.accounts.some((a) => a.id === account.id)
                              "
                            >
                              <span>{{ account.name }}</span>
                              <span class="ml-2 text-xs text-gray-400"
                                >#{{ account.id }}</span
                              >
                            </button>
                          </div>
                        </div>
                        <p class="text-xs text-gray-400 mt-1">
                          {{ t("admin.groups.modelRouting.accountsHint") }}
                        </p>
                      </div>
                    </div>
                    <button
                      type="button"
                      @click="removeCreateRoutingRule(rule)"
                      class="mt-5 p-1.5 text-gray-400 hover:text-red-500 transition-colors"
                      :title="t('admin.groups.modelRouting.removeRule')"
                    >
                      <Icon name="trash" size="sm" />
                    </button>
                  </div>
                </div>
              </div>
              <!-- 添加规则按钮（仅在启用时显示） -->
              <button
                v-if="createForm.model_routing_enabled"
                type="button"
                @click="addCreateRoutingRule"
                class="mt-3 flex items-center gap-1.5 text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
              >
                <Icon name="plus" size="sm" />
                {{ t("admin.groups.modelRouting.addRule") }}
              </button>
            </div>
            <div
              v-if="
                ['anthropic', 'antigravity'].includes(createForm.platform)
              "
              class="border-t pt-4"
            >
              <label class="input-label">{{
                t("admin.groups.invalidRequestFallback.title")
              }}</label>
              <Select
                v-model="createForm.fallback_group_id_on_invalid_request"
                :options="invalidRequestFallbackOptions"
                :placeholder="t('admin.groups.invalidRequestFallback.noFallback')"
              />
              <p class="input-hint">
                {{ t("admin.groups.invalidRequestFallback.hint") }}
              </p>
            </div>
            <div v-if="createForm.platform === 'antigravity'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.supportedScopes.title") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.supportedScopes.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="space-y-2">
                <div class="flex items-center justify-between gap-4">
                  <label for="create-group-claude" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.supportedScopes.claude') }}</label>
                  <Toggle
                    id="create-group-claude"
                    :model-value="createForm.supported_model_scopes.includes('claude')"
                    @update:model-value="toggleCreateScope('claude')"
                    :aria-label="t('admin.groups.supportedScopes.claude')"
                    data-group-setting="claude"
                  />
                </div>
                <div class="flex items-center justify-between gap-4">
                  <label for="create-group-gemini-text" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.supportedScopes.geminiText') }}</label>
                  <Toggle
                    id="create-group-gemini-text"
                    :model-value="createForm.supported_model_scopes.includes('gemini_text')"
                    @update:model-value="toggleCreateScope('gemini_text')"
                    :aria-label="t('admin.groups.supportedScopes.geminiText')"
                    data-group-setting="gemini_text"
                  />
                </div>
                <div class="flex items-center justify-between gap-4">
                  <label for="create-group-gemini-image" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.supportedScopes.geminiImage') }}</label>
                  <Toggle
                    id="create-group-gemini-image"
                    :model-value="createForm.supported_model_scopes.includes('gemini_image')"
                    @update:model-value="toggleCreateScope('gemini_image')"
                    :aria-label="t('admin.groups.supportedScopes.geminiImage')"
                    data-group-setting="gemini_image"
                  />
                </div>
              </div>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.groups.supportedScopes.hint") }}
              </p>
            </div>
            <div v-if="createForm.platform === 'antigravity'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.mcpXml.title") }}
                </label>
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.mcpXml.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="createForm.mcp_xml_inject"
                  data-group-setting="mcp_xml_inject"
                  :aria-label="t('admin.groups.mcpXml.title')"
                  @update:model-value="createForm.mcp_xml_inject = !createForm.mcp_xml_inject"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    createForm.mcp_xml_inject
                      ? t("admin.groups.mcpXml.enabled")
                      : t("admin.groups.mcpXml.disabled")
                  }}
                </span>
              </div>
            </div>
          </template>
          <template #pricing>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.rateMultiplier")
              }}</label>
              <input
                v-model.number="createForm.rate_multiplier"
                type="number"
                step="0.001"
                min="0.001"
                required
                class="input"
                data-tour="group-form-multiplier"
              />
              <p class="input-hint">{{ t("admin.groups.rateMultiplierHint") }}</p>
            </div>
            <div class="border-t pt-4">
              <div class="mb-4 flex items-center justify-between gap-4">
                <label for="create-group-peak-rate-enabled" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.peakRate.enable') }}</label>
                <Toggle
                  id="create-group-peak-rate-enabled"
                  v-model="createForm.peak_rate_enabled"
                  :aria-label="t('admin.groups.peakRate.enable')"
                  data-group-setting="peak_rate_enabled"
                />
              </div>
              <div
                v-if="createForm.peak_rate_enabled"
                class="mb-4 grid grid-cols-1 gap-3 sm:grid-cols-3"
              >
                <div>
                  <label class="input-label">{{ t("admin.groups.peakRate.peakStart") }}</label>
                  <input
                    v-model="createForm.peak_start"
                    type="time"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.peakRate.peakEnd") }}</label>
                  <input
                    v-model="createForm.peak_end"
                    type="time"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.peakRate.peakMultiplier") }}</label>
                  <input
                    v-model.number="createForm.peak_rate_multiplier"
                    type="number"
                    step="0.001"
                    min="0"
                    class="input"
                    placeholder="1"
                    :title="t('admin.groups.peakRate.multiplierHint')"
                  />
                </div>
              </div>
            </div>
            <div class="border-t border-gray-200 pt-4 mt-4 dark:border-dark-400">
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t("admin.groups.modelPricing.title") }}</h4>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t("admin.groups.modelPricing.description") }}</p>
                </div>
                <button type="button" class="btn btn-secondary shrink-0 whitespace-nowrap" @click="addGroupPricing(createForm.model_pricing)">
                  <Icon name="plus" size="sm" class="mr-1" />{{ t("admin.groups.modelPricing.add") }}
                </button>
              </div>
              <div class="mt-3 flex items-center justify-between gap-4">
                <div class="min-w-0">
                  <label for="create-group-long-context-pricing-enabled" class="block text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.modelPricing.longContext') }}</label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.modelPricing.longContextHint') }}</p>
                </div>
                <Toggle
                  id="create-group-long-context-pricing-enabled"
                  v-model="createForm.long_context_pricing_enabled"
                  :aria-label="t('admin.groups.modelPricing.longContext')"
                  data-group-setting="long_context_pricing_enabled"
                />
              </div>
              <div class="mt-3 space-y-2" data-group-field="model-pricing">
                <PricingEntryCard v-for="(entry, index) in createForm.model_pricing" :key="index" :entry="entry" :platform="createForm.platform" enable-tier-multipliers enable-time-pricing @update="createForm.model_pricing[index] = $event" @remove="createForm.model_pricing.splice(index, 1)" />
              </div>
            </div>
            <div
              v-if="supportsGroupOpenAIFast(createForm.platform)"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
              data-testid="create-free-openai-fast-section"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t("admin.groups.openaiFast.title") }}
              </h4>

              <div class="mt-4 flex items-center justify-between">
                <label class="text-sm text-gray-600 dark:text-gray-400">
                  {{ t("admin.groups.openaiFast.free") }}
                </label>
                <Toggle
                  :model-value="createForm.free_openai_fast"
                  data-group-setting="free_openai_fast"
                  :aria-label="t('admin.groups.openaiFast.free')"
                  data-testid="create-free-openai-fast"
                  @update:model-value="createForm.free_openai_fast = !createForm.free_openai_fast"
                />
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.groups.openaiFast.freeHint") }}
              </p>
            </div>
            <div v-if="createForm.platform === 'gemini' && createForm.allowed_protocols.includes('image_batches')" class="border-t border-gray-200 pt-4 dark:border-dark-700">
                <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.tabs.batchPricing') }}</h4>
                <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.groups.imagePricing.batchSectionHint") }}
                </p>
                <div
                  v-if="createForm.allowed_protocols.includes('image_batches')"
                  class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2"
                >
                  <div>
                    <label class="input-label">{{
                      t("admin.groups.imagePricing.batchDiscountMultiplier")
                    }}</label>
                    <input
                      v-model.number="createForm.batch_image_discount_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      class="input"
                      placeholder="0.5"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.groups.imagePricing.batchHoldMultiplier")
                    }}</label>
                    <input
                      v-model.number="createForm.batch_image_hold_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      class="input"
                      placeholder="0.6"
                    />
                  </div>
                </div>
              </div>

            <div
              v-if="createForm.platform === 'openai'"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t("admin.groups.webSearchPricing.title") }}
              </h4>
              <div>
                <label class="input-label">{{
                  t("admin.groups.webSearchPricing.pricePerCall")
                }}</label>
                <input
                  v-model.number="createForm.web_search_price_per_call"
                  type="number"
                  step="0.001"
                  min="0"
                  placeholder="0.01"
                  class="input"
                />
                <p class="input-hint">
                  {{ t("admin.groups.webSearchPricing.pricePerCallHint") }}
                </p>
                <div
                  class="mt-2 rounded-lg bg-gray-50 p-3 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300"
                >
                  {{
                    t("admin.groups.webSearchPricing.finalPricePreview", {
                      price: createWebSearchFinalPricePreview,
                    })
                  }}
                </div>
              </div>
            </div>
            <div
              v-if="createForm.platform === 'grok'"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {{ t("admin.groups.explicitPricing.title") }}
              </h4>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">
                {{ t("admin.groups.explicitPricing.description") }}
              </p>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <div>
                  <label class="input-label">{{ t("admin.groups.explicitPricing.searchPricePer1k") }}</label>
                  <input
                    v-model.number="createForm.search_price_per_1k"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.explicitPricing.pricePlaceholder')"
                    data-testid="create-search-price"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.voicePricing.audioRealtimePerMin") }}</label>
                  <input
                    v-model.number="createForm.audio_realtime_price_per_min"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="create-audio-realtime-price"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.voicePricing.audioTtsPerMillionChars") }}</label>
                  <input
                    v-model.number="createForm.audio_tts_price_per_million_chars"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="create-audio-tts-price"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.voicePricing.audioSttPerHour") }}</label>
                  <input
                    v-model.number="createForm.audio_stt_price_per_hour"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="create-audio-stt-price"
                  />
                </div>
              </div>
            </div>
          </template>
          <template #protocol>
            <GroupClientProtocolSelector
              v-model="createForm.allowed_protocols"
              v-model:fallbacks="createForm.protocol_fallbacks"
              v-model:image-policy="createForm.responses_image_policy"
              :platform="createForm.platform"
              class="mt-4"
            />
            <div
              v-if="createForm.platform === 'openai' && createMessagesDispatchEnabled"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
            >
              <div>
                <div
                  class="space-y-3"
                >
                  <div
                    class="space-y-1"
                  >
                    <div class="flex items-center gap-2">
                      <label
                        class="text-sm font-medium text-gray-900 dark:text-white"
                        >{{
                          t("admin.groups.openaiMessages.familyMappingTitle")
                        }}</label
                      >
                    </div>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.groups.openaiMessages.familyMappingHint") }}
                    </p>
                  </div>
                  <div class="space-y-4">
                    <div class="grid gap-4 md:grid-cols-3">
                      <div>
                        <label class="input-label">{{
                          t("admin.groups.openaiMessages.opusModel")
                        }}</label>
                        <input
                          v-model="createForm.opus_mapped_model"
                          type="text"
                          :placeholder="
                            t('admin.groups.openaiMessages.opusModelPlaceholder')
                          "
                          class="input"
                        />
                      </div>
                      <div>
                        <label class="input-label">{{
                          t("admin.groups.openaiMessages.sonnetModel")
                        }}</label>
                        <input
                          v-model="createForm.sonnet_mapped_model"
                          type="text"
                          :placeholder="
                            t('admin.groups.openaiMessages.sonnetModelPlaceholder')
                          "
                          class="input"
                        />
                      </div>
                      <div>
                        <label class="input-label">{{
                          t("admin.groups.openaiMessages.haikuModel")
                        }}</label>
                        <input
                          v-model="createForm.haiku_mapped_model"
                          type="text"
                          :placeholder="
                            t('admin.groups.openaiMessages.haikuModelPlaceholder')
                          "
                          class="input"
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <div
                  class="mt-5 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600"
                >
                  <div
                    class="space-y-1"
                  >
                    <div class="flex items-start justify-between gap-3">
                      <div>
                        <div class="flex items-center gap-2">
                          <label
                            class="text-sm font-medium text-gray-900 dark:text-white"
                            >{{
                              t("admin.groups.openaiMessages.exactMappingTitle")
                            }}</label
                          >
                        </div>
                        <p
                          class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                        >
                          {{ t("admin.groups.openaiMessages.exactMappingHint") }}
                        </p>
                      </div>
                    </div>
                  </div>

                  <div class="space-y-3">
                    <div
                      v-if="createForm.exact_model_mappings.length === 0"
                      class="flex flex-wrap items-center justify-between gap-3 py-2 text-sm text-gray-500 dark:text-gray-400"
                    >
                      <span>{{
                        t("admin.groups.openaiMessages.noExactMappings")
                      }}</span>
                      <button
                        type="button"
                        @click="addCreateMessagesDispatchMapping"
                        class="flex items-center gap-1.5 text-sm font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                      >
                        <Icon name="plus" size="sm" />
                        {{ t("admin.groups.openaiMessages.addExactMapping") }}
                      </button>
                    </div>

                    <div v-else class="space-y-3">
                      <div
                        v-for="row in createForm.exact_model_mappings"
                        :key="getCreateMessagesDispatchRowKey(row)"
                        class="group relative rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-800"
                      >
                        <div class="flex items-center gap-4">
                          <div
                            class="grid min-w-0 flex-1 gap-4 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] md:items-start"
                          >
                            <div>
                              <label class="input-label">{{
                                t("admin.groups.openaiMessages.claudeModel")
                              }}</label>
                              <input
                                v-model="row.claude_model"
                                type="text"
                                :placeholder="
                                  t(
                                    'admin.groups.openaiMessages.claudeModelPlaceholder',
                                  )
                                "
                                class="input bg-gray-50 focus:bg-white dark:bg-dark-800 dark:focus:bg-dark-900"
                              />
                            </div>
                            <div
                              class="hidden md:flex md:justify-center md:pt-7 text-primary-300 dark:text-primary-700"
                            >
                              <Icon
                                name="arrowRight"
                                size="sm"
                                class="transition-transform group-hover:translate-x-1"
                              />
                            </div>
                            <div>
                              <label class="input-label">{{
                                t("admin.groups.openaiMessages.targetModel")
                              }}</label>
                              <input
                                v-model="row.target_model"
                                type="text"
                                :placeholder="
                                  t(
                                    'admin.groups.openaiMessages.targetModelPlaceholder',
                                  )
                                "
                                class="input bg-gray-50 focus:bg-white dark:bg-dark-800 dark:focus:bg-dark-900"
                              />
                            </div>
                          </div>
                          <button
                            type="button"
                            @click="removeCreateMessagesDispatchMapping(row)"
                            class="mt-6 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                            :title="
                              t('admin.groups.openaiMessages.removeExactMapping')
                            "
                          >
                            <Icon name="trash" size="sm" />
                          </button>
                        </div>
                      </div>

                      <button
                        type="button"
                        @click="addCreateMessagesDispatchMapping"
                        class="flex min-h-9 w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed border-gray-300 bg-white py-1.5 text-sm font-medium text-gray-500 transition-all hover:border-primary-300 hover:bg-primary-50/50 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-400 dark:hover:border-primary-800 dark:hover:bg-primary-900/20 dark:hover:text-primary-400"
                      >
                        <Icon name="plus" size="sm" />
                        {{ t("admin.groups.openaiMessages.addExactMapping") }}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <div v-if="createForm.platform === 'anthropic'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.claudeCode.title") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.claudeCode.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="createForm.claude_code_only"
                  data-group-setting="claude_code_only"
                  :aria-label="t('admin.groups.claudeCode.title')"
                  @update:model-value="createForm.claude_code_only = !createForm.claude_code_only"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    createForm.claude_code_only
                      ? t("admin.groups.claudeCode.enabled")
                      : t("admin.groups.claudeCode.disabled")
                  }}
                </span>
              </div>
              <!-- 降级分组选择（仅当启用 claude_code_only 时显示） -->
              <div v-if="createForm.claude_code_only" class="mt-3">
                <label class="input-label">{{
                  t("admin.groups.claudeCode.fallbackGroup")
                }}</label>
                <Select
                  v-model="createForm.fallback_group_id"
                  :options="fallbackGroupOptions"
                  :placeholder="t('admin.groups.claudeCode.noFallback')"
                />
                <p class="input-hint">
                  {{ t("admin.groups.claudeCode.fallbackHint") }}
                </p>
              </div>
            </div>
            <div class="border-t pt-4">
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{
                      t("admin.groups.modelsList.title", {
                        endpoint: modelsListEndpoint(createForm.platform),
                      })
                    }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.groups.modelsList.hint", {
                        endpoint: modelsListEndpoint(createForm.platform),
                      })
                    }}
                  </p>
                </div>
                <Toggle
                  :model-value="createModelsListState.enabled"
                  data-group-setting="enabled"
                  :aria-label="t('admin.groups.modelsList.title')"
                  @update:model-value="createModelsListState.enabled = !createModelsListState.enabled"
                />
              </div>
              <div
                v-if="createModelsListState.enabled"
                class="overflow-hidden rounded-lg border border-gray-200 bg-gray-50/50 dark:border-dark-600 dark:bg-dark-800/40"
              >
                <div
                  v-if="!createModelsListLoading && createModelsListState.items.length > 0"
                  class="flex items-center justify-between gap-2 border-b border-gray-200 bg-gray-50 px-3 py-2 text-xs dark:border-dark-600 dark:bg-dark-800"
                >
                  <span class="text-gray-500 dark:text-gray-400">
                    已选 {{ createModelsListSelectedCount }} /
                    {{ createModelsListState.items.length }}
                  </span>
                  <div class="flex items-center gap-1.5">
                    <button
                      type="button"
                      class="rounded px-2 py-1 font-medium text-primary-600 transition-colors hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-900/20"
                      @click="selectAllModelsListItems(createModelsListState)"
                    >
                      全选
                    </button>
                    <button
                      type="button"
                      class="rounded px-2 py-1 font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700"
                      @click="invertModelsListSelection(createModelsListState)"
                    >
                      反选
                    </button>
                  </div>
                </div>
                <div
                  class="max-h-64 space-y-2 overflow-y-auto p-2"
                >
                  <p v-if="createModelsListLoading" class="text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.groups.modelsList.loading") }}
                  </p>
                  <p
                    v-else-if="createModelsListState.items.length === 0"
                    class="text-xs text-gray-500 dark:text-gray-400"
                  >
                    {{ t("admin.groups.modelsList.empty") }}
                  </p>
                  <div
                    v-for="(item, index) in createModelsListState.items"
                    :key="item.id"
                    class="flex items-center gap-2 rounded border border-gray-200 bg-white px-3 py-2 dark:border-dark-600 dark:bg-dark-800"
                  >
                    <span class="min-w-0 flex-1 break-all text-sm text-gray-700 dark:text-gray-300">
                      {{ item.id }}
                    </span>
                    <Toggle
                      v-model="item.selected"
                      :aria-label="item.id"
                      :data-model-visibility="item.id"
                    />
                    <button
                      type="button"
                      :disabled="index === 0"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-40 dark:hover:bg-dark-600 dark:hover:text-gray-200"
                      @click="moveCreateModelsListItem(index, index - 1)"
                    >
                      <Icon name="arrowUp" size="sm" />
                    </button>
                    <button
                      type="button"
                      :disabled="index === createModelsListState.items.length - 1"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-40 dark:hover:bg-dark-600 dark:hover:text-gray-200"
                      @click="moveCreateModelsListItem(index, index + 1)"
                    >
                      <Icon name="arrowDown" size="sm" />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </GroupFormTabs>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3 pt-4">
          <button
            @click="closeCreateModal"
            type="button"
            class="btn btn-secondary"
          >
            {{ t("common.cancel") }}
          </button>
          <button
            type="submit"
            form="create-group-form"
            :disabled="submitting || !protocolCatalog"
            class="btn btn-primary"
            data-tour="group-form-submit"
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
            {{ submitting ? t("admin.groups.creating") : t("common.create") }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Group Modal -->
    <BaseDialog
      :show="showEditModal"
      :title="t('admin.groups.editGroup')"
      width="wide"
      @close="closeEditModal"
    >
      <form
        v-if="editingGroup"
        id="edit-group-form"
        @submit.prevent="handleUpdateGroup"
        novalidate
        class="group-dialog-form"
      >

        <GroupFormTabs ref="editGroupTabsRef" :platform="editForm.platform" id-prefix="edit-group">
          <template #general>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.tabs.identity') }}</h4>
            <div data-group-field="name">
              <label class="input-label">{{ t("admin.groups.form.name") }}</label>
              <input
                v-model="editForm.name"
                type="text"
                required
                class="input"
                data-tour="edit-group-form-name"
              />
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.description")
              }}</label>
              <textarea
                v-model="editForm.description"
                rows="3"
                class="input"
              ></textarea>
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.displayBrand")
              }}</label>
              <Select
                v-model="editForm.display_brand"
                :options="providerBrandOptions"
                :placeholder="t('admin.groups.displayBrandPlaceholder')"
                :search-placeholder="t('admin.groups.displayBrandPlaceholder')"
                :creatable-prefix="t('admin.groups.displayBrandCreatablePrefix')"
                searchable
                creatable
              />
              <p class="input-hint">{{ t("admin.groups.displayBrandHint") }}</p>
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.platform")
              }}</label>
              <Select
                v-model="editForm.platform"
                :options="platformOptions"
                :disabled="true"
                data-tour="group-form-platform"
              />
              <p class="input-hint">{{ t("admin.groups.platformNotEditable") }}</p>
            </div>
            <div>
              <label class="input-label">{{ t("admin.groups.form.status") }}</label>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="editForm.status === 'active'"
                  data-group-setting="status"
                  data-testid="edit-group-status-toggle"
                  :aria-label="t('admin.groups.form.status')"
                  @update:model-value="editForm.status = editForm.status === 'active' ? 'inactive' : 'active'"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.status === 'active'
                      ? t("admin.accounts.status.active")
                      : t("admin.accounts.status.inactive")
                  }}
                </span>
              </div>
            </div>
            <div>
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.form.exclusive") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <!-- Tooltip Popover -->
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="mb-2 text-xs font-medium">
                        {{ t("admin.groups.exclusiveTooltip.title") }}
                      </p>
                      <p class="mb-2 text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.exclusiveTooltip.description") }}
                      </p>
                      <div class="rounded bg-gray-800 p-2 dark:bg-gray-700">
                        <p class="text-xs leading-relaxed text-gray-300">
                          <span
                            class="inline-flex items-center gap-1 text-primary-400"
                            ><Icon name="lightbulb" size="xs" />
                            {{ t("admin.groups.exclusiveTooltip.example") }}</span
                          >
                          {{ t("admin.groups.exclusiveTooltip.exampleContent") }}
                        </p>
                      </div>
                      <!-- Arrow -->
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="editForm.is_exclusive"
                  data-group-setting="is_exclusive"
                  :aria-label="t('admin.groups.form.exclusive')"
                  @update:model-value="editForm.is_exclusive = !editForm.is_exclusive"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.is_exclusive
                      ? t("admin.groups.exclusive")
                      : t("admin.groups.public")
                  }}
                </span>
              </div>
            </div>
            <div>
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.defaultGroup.title") }}
                </label>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="editForm.is_default"
                  data-group-setting="is_default"
                  :disabled="editForm.status !== 'active'"
                  :aria-label="t('admin.groups.defaultGroup.title')"
                  @update:model-value="editForm.is_default = !editForm.is_default"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.is_default
                      ? t("admin.groups.defaultGroup.enabled")
                      : t("admin.groups.defaultGroup.disabled")
                  }}
                </span>
              </div>
              <p class="input-hint">{{ t("admin.groups.defaultGroup.hint") }}</p>
            </div>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.tabs.scheduling') }}</h4>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.schedulerType")
              }}</label>
              <Select
                v-model="editForm.scheduler_type"
                :options="schedulerTypeOptions"
              />
              <p class="input-hint">{{ t("admin.groups.scheduler.hint") }}</p>
              <div
                v-if="editForm.scheduler_type === 'advanced'"
                class="mt-3 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary-900/10 bg-primary-50/60 px-3 py-2.5 dark:border-dark-600 dark:bg-dark-800/70"
              >
                <div class="min-w-0 text-xs text-primary-900/70 dark:text-dark-200/80">
                  <span class="font-medium text-primary-900 dark:text-dark-50">{{ t('admin.groups.advancedSchedulerOverrides.label') }}</span>
                  <span class="ml-2">{{ formatAdvancedSchedulerOverridesSummary(editForm.advanced_scheduler_overrides) }}</span>
                </div>
                <button
                  type="button"
                  class="btn btn-secondary shrink-0 px-3 py-1.5 text-xs"
                  @click="openAdvancedSchedulerOverrides('edit')"
                >
                  <Icon name="cog" size="sm" />
                  {{ t('admin.groups.advancedSchedulerOverrides.configure') }}
                </button>
              </div>
            </div>
            <div v-if="copyAccountsGroupOptionsForEdit.length > 0">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.copyAccounts.title") }}
                </label>
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.copyAccounts.tooltipEdit") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <!-- 已选分组标签 -->
              <div
                v-if="editForm.copy_accounts_from_group_ids.length > 0"
                class="flex flex-wrap gap-1.5 mb-2"
              >
                <span
                  v-for="groupId in editForm.copy_accounts_from_group_ids"
                  :key="groupId"
                  class="inline-flex items-center gap-1 rounded-full bg-primary-100 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                >
                  {{
                    copyAccountsGroupOptionsForEdit.find((o) => o.value === groupId)
                      ?.label || `#${groupId}`
                  }}
                  <button
                    type="button"
                    @click="
                      editForm.copy_accounts_from_group_ids =
                        editForm.copy_accounts_from_group_ids.filter(
                          (id) => id !== groupId,
                        )
                    "
                    class="ml-0.5 text-primary-500 hover:text-primary-700 dark:hover:text-primary-200"
                  >
                    <Icon name="x" size="xs" />
                  </button>
                </span>
              </div>
              <!-- 分组选择下拉 -->
              <Select
                :model-value="null"
                :options="copyAccountsGroupSelectOptionsForEdit"
                :placeholder="t('admin.groups.copyAccounts.selectPlaceholder')"
                @change="addEditCopyAccountsGroup"
              />
              <p class="input-hint">
                {{ t("admin.groups.copyAccounts.hintEdit") }}
              </p>
            </div>
            <div>
              <label class="input-label">{{
                t("admin.groups.unavailableFallback.title")
              }}</label>
              <Select
                v-model="editForm.unavailable_fallback_group_id"
                :options="unavailableFallbackGroupOptionsForEdit"
                :placeholder="t('admin.groups.unavailableFallback.noFallback')"
              />
              <p class="input-hint">
                {{ t("admin.groups.unavailableFallback.hint") }}
              </p>
            </div>
            <div>
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.sessionIsolation.title") }}
                </label>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="editForm.session_isolation_enabled"
                  data-group-setting="session_isolation_enabled"
                  :aria-label="t('admin.groups.sessionIsolation.title')"
                  @update:model-value="editForm.session_isolation_enabled = !editForm.session_isolation_enabled"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.session_isolation_enabled
                      ? t("admin.groups.sessionIsolation.enabledText")
                      : t("admin.groups.sessionIsolation.disabledText")
                  }}
                </span>
              </div>
              <p class="input-hint">{{ t("admin.groups.sessionIsolation.hint") }}</p>
            </div>
            <div class="border-t pt-4" data-group-field="probe">
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t("admin.groups.availabilityProbe.title") }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.groups.availabilityProbe.hint") }}
                  </p>
                </div>
                <Toggle
                  :model-value="editForm.availability_probe_enabled"
                  data-group-setting="availability_probe_enabled"
                  :aria-label="t('admin.groups.availabilityProbe.title')"
                  @update:model-value="editForm.availability_probe_enabled = !editForm.availability_probe_enabled"
                />
              </div>
              <div
                v-if="editForm.availability_probe_enabled"
                class="grid gap-4 rounded-lg border border-gray-200 bg-gray-50/50 p-4 dark:border-dark-600 dark:bg-dark-800/40 md:grid-cols-2"
              >
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.model") }}</label>
                  <Select
                    data-group-field="probe-model"
                    v-model="editForm.availability_probe_model_id"
                    :options="editAvailabilityProbeModelOptions"
                    searchable
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.interval") }}</label>
                  <input
                    v-model.number="editForm.availability_probe_interval_minutes"
                    type="number"
                    min="1"
                    max="1440"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.timeout") }}</label>
                  <input
                    v-model.number="editForm.availability_probe_timeout_seconds"
                    type="number"
                    min="5"
                    max="120"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.maxRetries") }}</label>
                  <input
                    v-model.number="editForm.availability_probe_max_retries"
                    type="number"
                    min="0"
                    max="10"
                    step="1"
                    class="input"
                  />
                </div>
                <div class="md:col-span-2">
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.userAgent") }}</label>
                  <input
                    v-model="editForm.availability_probe_user_agent"
                    type="text"
                    maxlength="512"
                    class="input"
                    :placeholder="t('admin.groups.availabilityProbe.userAgentPlaceholder')"
                  />
                </div>
                <div class="md:col-span-2">
                  <label class="input-label">{{ t("admin.groups.availabilityProbe.prompt") }}</label>
                  <textarea
                    data-group-field="probe-prompt"
                    v-model="editForm.availability_probe_prompt"
                    rows="3"
                    class="input"
                    :placeholder="t('admin.groups.availabilityProbe.promptPlaceholder')"
                  />
                </div>
              </div>
            </div>
          </template>
          <template #platform>
            <div
              v-if="supportsGroupOpenAIFast(editForm.platform)"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
              data-testid="edit-openai-fast"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t("admin.groups.openaiFast.title") }}
              </h4>
              <!-- 互斥策略防止加速与关闭配置冲突。 -->
              <Select v-model="editForm.openai_fast_policy" data-group-setting="openai_fast_policy"
                :aria-label="t('admin.groups.openaiFast.policy')" :options="groupOpenAIFastPolicyOptions" />
              <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">
                {{ t("admin.groups.openaiFast.hint") }}
              </p>

            </div>
            <ReasoningEffortPolicyFields
              data-group-field="reasoning"
              v-if="editForm.platform === 'openai' || editForm.platform === 'anthropic'"
              ref="editReasoningEffortPolicyRef"
              id-prefix="edit-group-reasoning"
              :platform="editForm.platform"
              v-model:max-effort="editForm.max_reasoning_effort"
              v-model:over-limit="editForm.max_reasoning_effort_over_limit"
              v-model:mappings="editForm.reasoning_effort_mappings"
            />
            <div
              v-if="
                ['openai', 'antigravity', 'anthropic', 'gemini'].includes(
                  editForm.platform,
                )
              "
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4 space-y-4"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t('admin.groups.accountFilters.title') }}
              </h4>

              <!-- require_oauth_only toggle -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="text-sm text-gray-600 dark:text-gray-400"
                    >{{ t('admin.groups.accountFilters.oauthOnly') }}</label
                  >
                  <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                    {{
                      editForm.require_oauth_only
                        ? "已启用 — 排除 API Key 类型账号"
                        : "未启用"
                    }}
                  </p>
                </div>
                <Toggle
                  :model-value="editForm.require_oauth_only"
                  data-group-setting="require_oauth_only"
                  :aria-label="t('admin.groups.accountFilters.oauthOnly')"
                  @update:model-value="editForm.require_oauth_only = !editForm.require_oauth_only"
                />
              </div>

              <!-- require_privacy_set toggle -->
              <div class="flex items-center justify-between">
                <div>
                  <label class="text-sm text-gray-600 dark:text-gray-400"
                    >{{ t('admin.groups.accountFilters.privacyRequired') }}</label
                  >
                  <p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
                    {{
                      editForm.require_privacy_set
                        ? "已启用 — Privacy 未设置的账号将被排除"
                        : "未启用"
                    }}
                  </p>
                </div>
                <Toggle
                  :model-value="editForm.require_privacy_set"
                  data-group-setting="require_privacy_set"
                  :aria-label="t('admin.groups.accountFilters.privacyRequired')"
                  @update:model-value="editForm.require_privacy_set = !editForm.require_privacy_set"
                />
              </div>
            </div>
            <div v-if="editForm.platform === 'anthropic'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.modelRouting.title") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-80 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.modelRouting.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <!-- 启用开关 -->
              <div class="flex items-center gap-3 mb-3">
                <Toggle
                  :model-value="editForm.model_routing_enabled"
                  data-group-setting="model_routing_enabled"
                  :aria-label="t('admin.groups.modelRouting.title')"
                  @update:model-value="editForm.model_routing_enabled = !editForm.model_routing_enabled"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.model_routing_enabled
                      ? t("admin.groups.modelRouting.enabled")
                      : t("admin.groups.modelRouting.disabled")
                  }}
                </span>
              </div>
              <p
                v-if="!editForm.model_routing_enabled"
                class="text-xs text-gray-500 dark:text-gray-400 mb-3"
              >
                {{ t("admin.groups.modelRouting.disabledHint") }}
              </p>
              <p v-else class="text-xs text-gray-500 dark:text-gray-400 mb-3">
                {{ t("admin.groups.modelRouting.noRulesHint") }}
              </p>
              <!-- 路由规则列表（仅在启用时显示） -->
              <div v-if="editForm.model_routing_enabled" class="space-y-3">
                <div
                  v-for="rule in editModelRoutingRules"
                  :key="getEditRuleRenderKey(rule)"
                  class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
                >
                  <div class="flex items-start gap-3">
                    <div class="flex-1 space-y-2">
                      <div>
                        <label class="input-label text-xs">{{
                          t("admin.groups.modelRouting.modelPattern")
                        }}</label>
                        <input
                          v-model="rule.pattern"
                          type="text"
                          class="input text-sm"
                          :placeholder="
                            t('admin.groups.modelRouting.modelPatternPlaceholder')
                          "
                        />
                      </div>
                      <div>
                        <label class="input-label text-xs">{{
                          t("admin.groups.modelRouting.accounts")
                        }}</label>
                        <!-- 已选账号标签 -->
                        <div
                          v-if="rule.accounts.length > 0"
                          class="flex flex-wrap gap-1.5 mb-2"
                        >
                          <span
                            v-for="account in rule.accounts"
                            :key="account.id"
                            class="inline-flex items-center gap-1 rounded-full bg-primary-100 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                          >
                            {{ account.name }}
                            <button
                              type="button"
                              @click="removeSelectedAccount(rule, account.id, true)"
                              class="ml-0.5 text-primary-500 hover:text-primary-700 dark:hover:text-primary-200"
                            >
                              <Icon name="x" size="xs" />
                            </button>
                          </span>
                        </div>
                        <!-- 账号搜索输入框 -->
                        <div class="relative account-search-container">
                          <input
                            v-model="
                              accountSearchKeyword[getEditRuleSearchKey(rule)]
                            "
                            type="text"
                            class="input text-sm"
                            :placeholder="
                              t(
                                'admin.groups.modelRouting.searchAccountPlaceholder',
                              )
                            "
                            @input="searchAccountsByRule(rule, true)"
                            @focus="onAccountSearchFocus(rule, true)"
                          />
                          <!-- 搜索结果下拉框 -->
                          <div
                            v-if="
                              showAccountDropdown[getEditRuleSearchKey(rule)] &&
                              accountSearchResults[getEditRuleSearchKey(rule)]
                                ?.length > 0
                            "
                            class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-lg border bg-white shadow-lg dark:border-dark-600 dark:bg-dark-800"
                          >
                            <button
                              v-for="account in accountSearchResults[
                                getEditRuleSearchKey(rule)
                              ]"
                              :key="account.id"
                              type="button"
                              @click="selectAccount(rule, account, true)"
                              class="w-full px-3 py-2 text-left text-sm hover:bg-gray-100 dark:hover:bg-dark-700"
                              :class="{
                                'opacity-50': rule.accounts.some(
                                  (a) => a.id === account.id,
                                ),
                              }"
                              :disabled="
                                rule.accounts.some((a) => a.id === account.id)
                              "
                            >
                              <span>{{ account.name }}</span>
                              <span class="ml-2 text-xs text-gray-400"
                                >#{{ account.id }}</span
                              >
                            </button>
                          </div>
                        </div>
                        <p class="text-xs text-gray-400 mt-1">
                          {{ t("admin.groups.modelRouting.accountsHint") }}
                        </p>
                      </div>
                    </div>
                    <button
                      type="button"
                      @click="removeEditRoutingRule(rule)"
                      class="mt-5 p-1.5 text-gray-400 hover:text-red-500 transition-colors"
                      :title="t('admin.groups.modelRouting.removeRule')"
                    >
                      <Icon name="trash" size="sm" />
                    </button>
                  </div>
                </div>
              </div>
              <!-- 添加规则按钮（仅在启用时显示） -->
              <button
                v-if="editForm.model_routing_enabled"
                type="button"
                @click="addEditRoutingRule"
                class="mt-3 flex items-center gap-1.5 text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
              >
                <Icon name="plus" size="sm" />
                {{ t("admin.groups.modelRouting.addRule") }}
              </button>
            </div>
            <div
              v-if="
                ['anthropic', 'antigravity'].includes(editForm.platform)
              "
              class="border-t pt-4"
            >
              <label class="input-label">{{
                t("admin.groups.invalidRequestFallback.title")
              }}</label>
              <Select
                v-model="editForm.fallback_group_id_on_invalid_request"
                :options="invalidRequestFallbackOptionsForEdit"
                :placeholder="t('admin.groups.invalidRequestFallback.noFallback')"
              />
              <p class="input-hint">
                {{ t("admin.groups.invalidRequestFallback.hint") }}
              </p>
            </div>
            <div v-if="editForm.platform === 'antigravity'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.supportedScopes.title") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.supportedScopes.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="space-y-2">
                <div class="flex items-center justify-between gap-4">
                  <label for="edit-group-claude" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.supportedScopes.claude') }}</label>
                  <Toggle
                    id="edit-group-claude"
                    :model-value="editForm.supported_model_scopes.includes('claude')"
                    @update:model-value="toggleEditScope('claude')"
                    :aria-label="t('admin.groups.supportedScopes.claude')"
                    data-group-setting="claude"
                  />
                </div>
                <div class="flex items-center justify-between gap-4">
                  <label for="edit-group-gemini-text" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.supportedScopes.geminiText') }}</label>
                  <Toggle
                    id="edit-group-gemini-text"
                    :model-value="editForm.supported_model_scopes.includes('gemini_text')"
                    @update:model-value="toggleEditScope('gemini_text')"
                    :aria-label="t('admin.groups.supportedScopes.geminiText')"
                    data-group-setting="gemini_text"
                  />
                </div>
                <div class="flex items-center justify-between gap-4">
                  <label for="edit-group-gemini-image" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.supportedScopes.geminiImage') }}</label>
                  <Toggle
                    id="edit-group-gemini-image"
                    :model-value="editForm.supported_model_scopes.includes('gemini_image')"
                    @update:model-value="toggleEditScope('gemini_image')"
                    :aria-label="t('admin.groups.supportedScopes.geminiImage')"
                    data-group-setting="gemini_image"
                  />
                </div>
              </div>
              <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.groups.supportedScopes.hint") }}
              </p>
            </div>
            <div v-if="editForm.platform === 'antigravity'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.mcpXml.title") }}
                </label>
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.mcpXml.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="editForm.mcp_xml_inject"
                  data-group-setting="mcp_xml_inject"
                  :aria-label="t('admin.groups.mcpXml.title')"
                  @update:model-value="editForm.mcp_xml_inject = !editForm.mcp_xml_inject"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.mcp_xml_inject
                      ? t("admin.groups.mcpXml.enabled")
                      : t("admin.groups.mcpXml.disabled")
                  }}
                </span>
              </div>
            </div>
          </template>
          <template #pricing>
            <div>
              <label class="input-label">{{
                t("admin.groups.form.rateMultiplier")
              }}</label>
              <input
                v-model.number="editForm.rate_multiplier"
                type="number"
                step="0.001"
                min="0.001"
                required
                class="input"
                data-tour="group-form-multiplier"
              />
            </div>
            <div class="border-t pt-4">
              <div class="mb-4 flex items-center justify-between gap-4">
                <label for="edit-group-peak-rate-enabled" class="min-w-0 text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.peakRate.enable') }}</label>
                <Toggle
                  id="edit-group-peak-rate-enabled"
                  v-model="editForm.peak_rate_enabled"
                  :aria-label="t('admin.groups.peakRate.enable')"
                  data-group-setting="peak_rate_enabled"
                />
              </div>
              <div
                v-if="editForm.peak_rate_enabled"
                class="mb-4 grid grid-cols-1 gap-3 sm:grid-cols-3"
              >
                <div>
                  <label class="input-label">{{ t("admin.groups.peakRate.peakStart") }}</label>
                  <input
                    v-model="editForm.peak_start"
                    type="time"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.peakRate.peakEnd") }}</label>
                  <input
                    v-model="editForm.peak_end"
                    type="time"
                    class="input"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.peakRate.peakMultiplier") }}</label>
                  <input
                    v-model.number="editForm.peak_rate_multiplier"
                    type="number"
                    step="0.001"
                    min="0"
                    class="input"
                    placeholder="1"
                    :title="t('admin.groups.peakRate.multiplierHint')"
                  />
                </div>
              </div>
            </div>
            <div class="border-t border-gray-200 pt-4 mt-4 dark:border-dark-400">
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t("admin.groups.modelPricing.title") }}</h4>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t("admin.groups.modelPricing.description") }}</p>
                </div>
                <button type="button" class="btn btn-secondary shrink-0 whitespace-nowrap" @click="addGroupPricing(editForm.model_pricing)">
                  <Icon name="plus" size="sm" class="mr-1" />{{ t("admin.groups.modelPricing.add") }}
                </button>
              </div>
              <div class="mt-3 flex items-center justify-between gap-4">
                <div class="min-w-0">
                  <label for="edit-group-long-context-pricing-enabled" class="block text-sm text-gray-700 dark:text-gray-300">{{ t('admin.groups.modelPricing.longContext') }}</label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.groups.modelPricing.longContextHint') }}</p>
                </div>
                <Toggle
                  id="edit-group-long-context-pricing-enabled"
                  v-model="editForm.long_context_pricing_enabled"
                  :aria-label="t('admin.groups.modelPricing.longContext')"
                  data-group-setting="long_context_pricing_enabled"
                />
              </div>
              <div class="mt-3 space-y-2" data-group-field="model-pricing">
                <PricingEntryCard v-for="(entry, index) in editForm.model_pricing" :key="index" :entry="entry" :platform="editForm.platform" enable-tier-multipliers enable-time-pricing @update="editForm.model_pricing[index] = $event" @remove="editForm.model_pricing.splice(index, 1)" />
              </div>
            </div>
            <div
              v-if="supportsGroupOpenAIFast(editForm.platform)"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
              data-testid="edit-free-openai-fast-section"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t("admin.groups.openaiFast.title") }}
              </h4>

              <div class="mt-4 flex items-center justify-between">
                <label class="text-sm text-gray-600 dark:text-gray-400">
                  {{ t("admin.groups.openaiFast.free") }}
                </label>
                <Toggle
                  :model-value="editForm.free_openai_fast"
                  data-group-setting="free_openai_fast"
                  :aria-label="t('admin.groups.openaiFast.free')"
                  data-testid="edit-free-openai-fast"
                  @update:model-value="editForm.free_openai_fast = !editForm.free_openai_fast"
                />
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t("admin.groups.openaiFast.freeHint") }}
              </p>
            </div>
            <div v-if="editForm.platform === 'gemini' && editForm.allowed_protocols.includes('image_batches')" class="border-t border-gray-200 pt-4 dark:border-dark-700">
                <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.groups.tabs.batchPricing') }}</h4>
                <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
                  {{ t("admin.groups.imagePricing.batchSectionHint") }}
                </p>
                <div
                  v-if="editForm.allowed_protocols.includes('image_batches')"
                  class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2"
                >
                  <div>
                    <label class="input-label">{{
                      t("admin.groups.imagePricing.batchDiscountMultiplier")
                    }}</label>
                    <input
                      v-model.number="editForm.batch_image_discount_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      class="input"
                      placeholder="0.5"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{
                      t("admin.groups.imagePricing.batchHoldMultiplier")
                    }}</label>
                    <input
                      v-model.number="editForm.batch_image_hold_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      class="input"
                      placeholder="0.6"
                    />
                  </div>
                </div>
              </div>

            <div
              v-if="editForm.platform === 'openai'"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-3">
                {{ t("admin.groups.webSearchPricing.title") }}
              </h4>
              <div>
                <label class="input-label">{{
                  t("admin.groups.webSearchPricing.pricePerCall")
                }}</label>
                <input
                  v-model.number="editForm.web_search_price_per_call"
                  type="number"
                  step="0.001"
                  min="0"
                  placeholder="0.01"
                  class="input"
                />
                <p class="input-hint">
                  {{ t("admin.groups.webSearchPricing.pricePerCallHint") }}
                </p>
                <div
                  class="mt-2 rounded-lg bg-gray-50 p-3 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300"
                >
                  {{
                    t("admin.groups.webSearchPricing.finalPricePreview", {
                      price: editWebSearchFinalPricePreview,
                    })
                  }}
                </div>
              </div>
            </div>
            <div
              v-if="editForm.platform === 'grok'"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
            >
              <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {{ t("admin.groups.explicitPricing.title") }}
              </h4>
              <p class="text-xs text-gray-500 dark:text-gray-400 mb-3">
                {{ t("admin.groups.explicitPricing.description") }}
              </p>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <div>
                  <label class="input-label">{{ t("admin.groups.explicitPricing.searchPricePer1k") }}</label>
                  <input
                    v-model.number="editForm.search_price_per_1k"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.explicitPricing.pricePlaceholder')"
                    data-testid="edit-search-price"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.voicePricing.audioRealtimePerMin") }}</label>
                  <input
                    v-model.number="editForm.audio_realtime_price_per_min"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="edit-audio-realtime-price"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.voicePricing.audioTtsPerMillionChars") }}</label>
                  <input
                    v-model.number="editForm.audio_tts_price_per_million_chars"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="edit-audio-tts-price"
                  />
                </div>
                <div>
                  <label class="input-label">{{ t("admin.groups.voicePricing.audioSttPerHour") }}</label>
                  <input
                    v-model.number="editForm.audio_stt_price_per_hour"
                    type="number"
                    step="0.000001"
                    min="0"
                    class="input"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="edit-audio-stt-price"
                  />
                </div>
              </div>
            </div>
          </template>
          <template #protocol>
            <GroupClientProtocolSelector
              v-model="editForm.allowed_protocols"
              v-model:fallbacks="editForm.protocol_fallbacks"
              v-model:image-policy="editForm.responses_image_policy"
              :platform="editForm.platform"
              class="mt-4"
            />
            <div
              v-if="editForm.platform === 'openai' && editMessagesDispatchEnabled"
              class="border-t border-gray-200 dark:border-dark-400 pt-4 mt-4"
            >
              <div>
                <div
                  class="space-y-3"
                >
                  <div
                    class="space-y-1"
                  >
                    <div class="flex items-center gap-2">
                      <label
                        class="text-sm font-medium text-gray-900 dark:text-white"
                        >{{
                          t("admin.groups.openaiMessages.familyMappingTitle")
                        }}</label
                      >
                    </div>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                      {{ t("admin.groups.openaiMessages.familyMappingHint") }}
                    </p>
                  </div>
                  <div class="space-y-4">
                    <div class="grid gap-4 md:grid-cols-3">
                      <div>
                        <label class="input-label">{{
                          t("admin.groups.openaiMessages.opusModel")
                        }}</label>
                        <input
                          v-model="editForm.opus_mapped_model"
                          type="text"
                          :placeholder="
                            t('admin.groups.openaiMessages.opusModelPlaceholder')
                          "
                          class="input"
                        />
                      </div>
                      <div>
                        <label class="input-label">{{
                          t("admin.groups.openaiMessages.sonnetModel")
                        }}</label>
                        <input
                          v-model="editForm.sonnet_mapped_model"
                          type="text"
                          :placeholder="
                            t('admin.groups.openaiMessages.sonnetModelPlaceholder')
                          "
                          class="input"
                        />
                      </div>
                      <div>
                        <label class="input-label">{{
                          t("admin.groups.openaiMessages.haikuModel")
                        }}</label>
                        <input
                          v-model="editForm.haiku_mapped_model"
                          type="text"
                          :placeholder="
                            t('admin.groups.openaiMessages.haikuModelPlaceholder')
                          "
                          class="input"
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <div
                  class="mt-5 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600"
                >
                  <div
                    class="space-y-1"
                  >
                    <div class="flex items-start justify-between gap-3">
                      <div>
                        <div class="flex items-center gap-2">
                          <label
                            class="text-sm font-medium text-gray-900 dark:text-white"
                            >{{
                              t("admin.groups.openaiMessages.exactMappingTitle")
                            }}</label
                          >
                        </div>
                        <p
                          class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                        >
                          {{ t("admin.groups.openaiMessages.exactMappingHint") }}
                        </p>
                      </div>
                    </div>
                  </div>

                  <div class="space-y-3">
                    <div
                      v-if="editForm.exact_model_mappings.length === 0"
                      class="flex flex-wrap items-center justify-between gap-3 py-2 text-sm text-gray-500 dark:text-gray-400"
                    >
                      <span>{{
                        t("admin.groups.openaiMessages.noExactMappings")
                      }}</span>
                      <button
                        type="button"
                        @click="addEditMessagesDispatchMapping"
                        class="flex items-center gap-1.5 text-sm font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
                      >
                        <Icon name="plus" size="sm" />
                        {{ t("admin.groups.openaiMessages.addExactMapping") }}
                      </button>
                    </div>

                    <div v-else class="space-y-3">
                      <div
                        v-for="row in editForm.exact_model_mappings"
                        :key="getEditMessagesDispatchRowKey(row)"
                        class="group relative rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-800"
                      >
                        <div class="flex items-center gap-4">
                          <div
                            class="grid min-w-0 flex-1 gap-4 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] md:items-start"
                          >
                            <div>
                              <label class="input-label">{{
                                t("admin.groups.openaiMessages.claudeModel")
                              }}</label>
                              <input
                                v-model="row.claude_model"
                                type="text"
                                :placeholder="
                                  t(
                                    'admin.groups.openaiMessages.claudeModelPlaceholder',
                                  )
                                "
                                class="input bg-gray-50 focus:bg-white dark:bg-dark-800 dark:focus:bg-dark-900"
                              />
                            </div>
                            <div
                              class="hidden md:flex md:justify-center md:pt-7 text-primary-300 dark:text-primary-700"
                            >
                              <Icon
                                name="arrowRight"
                                size="sm"
                                class="transition-transform group-hover:translate-x-1"
                              />
                            </div>
                            <div>
                              <label class="input-label">{{
                                t("admin.groups.openaiMessages.targetModel")
                              }}</label>
                              <input
                                v-model="row.target_model"
                                type="text"
                                :placeholder="
                                  t(
                                    'admin.groups.openaiMessages.targetModelPlaceholder',
                                  )
                                "
                                class="input bg-gray-50 focus:bg-white dark:bg-dark-800 dark:focus:bg-dark-900"
                              />
                            </div>
                          </div>
                          <button
                            type="button"
                            @click="removeEditMessagesDispatchMapping(row)"
                            class="mt-6 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                            :title="
                              t('admin.groups.openaiMessages.removeExactMapping')
                            "
                          >
                            <Icon name="trash" size="sm" />
                          </button>
                        </div>
                      </div>

                      <button
                        type="button"
                        @click="addEditMessagesDispatchMapping"
                        class="flex min-h-9 w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed border-gray-300 bg-white py-1.5 text-sm font-medium text-gray-500 transition-all hover:border-primary-300 hover:bg-primary-50/50 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-400 dark:hover:border-primary-800 dark:hover:bg-primary-900/20 dark:hover:text-primary-400"
                      >
                        <Icon name="plus" size="sm" />
                        {{ t("admin.groups.openaiMessages.addExactMapping") }}
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <div v-if="editForm.platform === 'anthropic'" class="border-t pt-4">
              <div class="relative mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t("admin.groups.claudeCode.title") }}
                </label>
                <!-- Help Tooltip -->
                <div class="group inline-flex">
                  <Icon
                    name="questionCircle"
                    size="sm"
                    :stroke-width="2"
                    class="cursor-help text-gray-400 transition-colors hover:text-primary-500 dark:text-gray-500 dark:hover:text-primary-400"
                  />
                  <div
                    class="pointer-events-none absolute bottom-full left-0 z-50 mb-2 w-72 max-w-full opacity-0 transition-all duration-200 group-hover:pointer-events-auto group-hover:opacity-100"
                  >
                    <div
                      class="rounded-lg bg-gray-900 p-3 text-white shadow-lg dark:bg-gray-800"
                    >
                      <p class="text-xs leading-relaxed text-gray-300">
                        {{ t("admin.groups.claudeCode.tooltip") }}
                      </p>
                      <div
                        class="absolute -bottom-1.5 left-3 h-3 w-3 rotate-45 bg-gray-900 dark:bg-gray-800"
                      ></div>
                    </div>
                  </div>
                </div>
              </div>
              <div class="flex items-center gap-3">
                <Toggle
                  :model-value="editForm.claude_code_only"
                  data-group-setting="claude_code_only"
                  :aria-label="t('admin.groups.claudeCode.title')"
                  @update:model-value="editForm.claude_code_only = !editForm.claude_code_only"
                />
                <span class="text-sm text-gray-500 dark:text-gray-400">
                  {{
                    editForm.claude_code_only
                      ? t("admin.groups.claudeCode.enabled")
                      : t("admin.groups.claudeCode.disabled")
                  }}
                </span>
              </div>
              <!-- 降级分组选择（仅当启用 claude_code_only 时显示） -->
              <div v-if="editForm.claude_code_only" class="mt-3">
                <label class="input-label">{{
                  t("admin.groups.claudeCode.fallbackGroup")
                }}</label>
                <Select
                  v-model="editForm.fallback_group_id"
                  :options="fallbackGroupOptionsForEdit"
                  :placeholder="t('admin.groups.claudeCode.noFallback')"
                />
                <p class="input-hint">
                  {{ t("admin.groups.claudeCode.fallbackHint") }}
                </p>
              </div>
            </div>
            <div class="border-t pt-4">
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{
                      t("admin.groups.modelsList.title", {
                        endpoint: modelsListEndpoint(editForm.platform),
                      })
                    }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{
                      t("admin.groups.modelsList.hint", {
                        endpoint: modelsListEndpoint(editForm.platform),
                      })
                    }}
                  </p>
                </div>
                <Toggle
                  :model-value="editModelsListState.enabled"
                  data-group-setting="enabled"
                  :aria-label="t('admin.groups.modelsList.title')"
                  @update:model-value="editModelsListState.enabled = !editModelsListState.enabled"
                />
              </div>
              <div
                v-if="editModelsListState.enabled"
                class="overflow-hidden rounded-lg border border-gray-200 bg-gray-50/50 dark:border-dark-600 dark:bg-dark-800/40"
              >
                <div
                  v-if="!editModelsListLoading && editModelsListState.items.length > 0"
                  class="flex items-center justify-between gap-2 border-b border-gray-200 bg-gray-50 px-3 py-2 text-xs dark:border-dark-600 dark:bg-dark-800"
                >
                  <span class="text-gray-500 dark:text-gray-400">
                    已选 {{ editModelsListSelectedCount }} /
                    {{ editModelsListState.items.length }}
                  </span>
                  <div class="flex items-center gap-1.5">
                    <button
                      type="button"
                      class="rounded px-2 py-1 font-medium text-primary-600 transition-colors hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-900/20"
                      @click="selectAllModelsListItems(editModelsListState)"
                    >
                      全选
                    </button>
                    <button
                      type="button"
                      class="rounded px-2 py-1 font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700"
                      @click="invertModelsListSelection(editModelsListState)"
                    >
                      反选
                    </button>
                  </div>
                </div>
                <div
                  class="max-h-64 space-y-2 overflow-y-auto p-2"
                >
                  <p v-if="editModelsListLoading" class="text-xs text-gray-500 dark:text-gray-400">
                    {{ t("admin.groups.modelsList.loading") }}
                  </p>
                  <p
                    v-else-if="editModelsListState.items.length === 0"
                    class="text-xs text-gray-500 dark:text-gray-400"
                  >
                    {{ t("admin.groups.modelsList.empty") }}
                  </p>
                  <div
                    v-for="(item, index) in editModelsListState.items"
                    :key="item.id"
                    class="flex items-center gap-2 rounded border border-gray-200 bg-white px-3 py-2 dark:border-dark-600 dark:bg-dark-800"
                  >
                    <span class="min-w-0 flex-1 break-all text-sm text-gray-700 dark:text-gray-300">
                      {{ item.id }}
                    </span>
                    <Toggle
                      v-model="item.selected"
                      :aria-label="item.id"
                      :data-model-visibility="item.id"
                    />
                    <button
                      type="button"
                      :disabled="index === 0"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-40 dark:hover:bg-dark-600 dark:hover:text-gray-200"
                      @click="moveEditModelsListItem(index, index - 1)"
                    >
                      <Icon name="arrowUp" size="sm" />
                    </button>
                    <button
                      type="button"
                      :disabled="index === editModelsListState.items.length - 1"
                      class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-40 dark:hover:bg-dark-600 dark:hover:text-gray-200"
                      @click="moveEditModelsListItem(index, index + 1)"
                    >
                      <Icon name="arrowDown" size="sm" />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </template>
        </GroupFormTabs>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3 pt-4">
          <button
            @click="closeEditModal"
            type="button"
            class="btn btn-secondary"
          >
            {{ t("common.cancel") }}
          </button>
          <button
            type="submit"
            form="edit-group-form"
            :disabled="submitting || !protocolCatalog"
            class="btn btn-primary"
            data-tour="group-form-submit"
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
            {{ submitting ? t("admin.groups.updating") : t("common.update") }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.groups.deleteGroup')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <ConfirmDialog
      :show="showUnsupportedLiveConfirm"
      :title="t('admin.groups.openaiLive.unsupportedTitle')"
      :message="t('admin.groups.openaiLive.unsupportedMessage')"
      :confirm-text="t('admin.groups.openaiLive.enableAnyway')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmUnsupportedLive"
      @cancel="cancelUnsupportedLive"
    />

    <!-- Sort Order Modal -->
    <BaseDialog
      :show="showSortModal"
      :title="t('admin.groups.sortOrder')"
      width="normal"
      @close="closeSortModal"
    >
      <div class="space-y-4">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t("admin.groups.sortOrderHint") }}
        </p>
        <VueDraggable
          v-model="sortableGroups"
          :animation="200"
          class="space-y-2"
        >
          <div
            v-for="group in sortableGroups"
            :key="group.id"
            class="flex cursor-grab items-center gap-3 rounded-lg border border-gray-200 bg-white p-3 transition-shadow hover:shadow-md active:cursor-grabbing dark:border-dark-600 dark:bg-dark-700"
          >
            <div class="text-gray-400">
              <Icon name="menu" size="md" />
            </div>
            <div class="flex-1">
              <div class="font-medium text-gray-900 dark:text-white">
                {{ group.name }}
              </div>
              <div class="text-xs text-gray-500 dark:text-gray-400">
                <span
                  :class="[
                    'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
                    group.platform === 'anthropic'
                      ? 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400'
                      : group.platform === 'openai'
                        ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
                        : group.platform === 'antigravity'
                          ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
                          : group.platform === 'grok'
                            ? 'bg-zinc-200 text-zinc-800 dark:bg-zinc-700 dark:text-zinc-100'
                            : group.platform === 'kimi'
                              ? 'bg-pink-100 text-pink-700 dark:bg-pink-900/30 dark:text-pink-400'
                              : group.platform === 'zhipu'
                                ? 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-400'
                                : group.platform === 'deepseek'
                                  ? 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400'
                                  : 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
                  ]"
                >
                  {{ t("admin.groups.platforms." + group.platform) }}
                </span>
              </div>
            </div>
            <div class="text-sm text-gray-400">#{{ group.id }}</div>
          </div>
        </VueDraggable>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3 pt-4">
          <button
            @click="closeSortModal"
            type="button"
            class="btn btn-secondary"
          >
            {{ t("common.cancel") }}
          </button>
          <button
            @click="saveSortOrder"
            :disabled="sortSubmitting"
            class="btn btn-primary"
          >
            <svg
              v-if="sortSubmitting"
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
            {{ sortSubmitting ? t("common.saving") : t("common.save") }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Group Rate Multipliers Modal -->
    <GroupRateMultipliersModal
      :show="showRateMultipliersModal"
      :group="rateMultipliersGroup"
      @close="showRateMultipliersModal = false"
      @success="loadGroups"
    />

    <!-- Group RPM Overrides Modal -->
    <GroupRPMOverridesModal
      :show="showRPMOverridesModal"
      :group="rpmOverridesGroup"
      @close="showRPMOverridesModal = false"
      @success="loadGroups"
    />

    <GroupAdvancedSchedulerOverridesModal
      :show="showAdvancedSchedulerOverridesModal"
      :model-value="advancedSchedulerOverridesDraft"
      @close="closeAdvancedSchedulerOverrides"
      @save="saveAdvancedSchedulerOverrides"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, nextTick, onMounted, onUnmounted, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useAppStore } from "@/stores/app";
import { useOnboardingStore } from "@/stores/onboarding";
import { adminAPI } from "@/api/admin";
import { useBalanceDisplay } from "@/composables/useBalanceDisplay";
import type {
  AdminGroup,
  GroupAvailabilityProbeConfig,
  ProtocolID,
  GroupPlatform,
  GroupSchedulerType,
  GroupAdvancedSchedulerOverrides,
} from "@/types";
import type { Column } from "@/components/common/types";
import AppLayout from "@/components/layout/AppLayout.vue";
import TablePageLayout from "@/components/layout/TablePageLayout.vue";
import DataTable from "@/components/common/DataTable.vue";
import Pagination from "@/components/common/Pagination.vue";
import BaseDialog from "@/components/common/BaseDialog.vue";
import ConfirmDialog from "@/components/common/ConfirmDialog.vue";
import EmptyState from "@/components/common/EmptyState.vue";
import Select from "@/components/common/Select.vue";
import Toggle from "@/components/common/Toggle.vue";
import PlatformIcon from "@/components/common/PlatformIcon.vue";
import ProviderIcon from "@/components/common/ProviderIcon.vue";
import Icon from "@/components/icons/Icon.vue";
import GroupRateMultipliersModal from "@/components/admin/group/GroupRateMultipliersModal.vue";
import GroupActionMenu from "@/components/admin/group/GroupActionMenu.vue";
import GroupRPMOverridesModal from "@/components/admin/group/GroupRPMOverridesModal.vue";
import GroupCapacityBadge from "@/components/common/GroupCapacityBadge.vue";
import ReasoningEffortPolicyFields from "@/components/admin/group/ReasoningEffortPolicyFields.vue";
import { loadProtocolCatalog, protocolCatalog } from '@/api/admin/protocolCapabilities';
import GroupClientProtocolSelector from "@/components/admin/group/GroupClientProtocolSelector.vue";
import GroupAdvancedSchedulerOverridesModal from "@/components/admin/group/GroupAdvancedSchedulerOverridesModal.vue";
import GroupFormTabs from "@/components/admin/group/GroupFormTabs.vue";
import PricingEntryCard from "@/components/admin/channel/PricingEntryCard.vue";
import type { PricingFormEntry } from "@/components/admin/channel/types";
import { createDefaultTimePricingForm } from "@/components/admin/channel/types";
import { pricingEntryFromAPI, pricingEntryToAPI, validatePricingForm } from "@/components/admin/channel/pricingForm";
import type { ChannelModelPricing } from "@/api/admin/channels";
import { VueDraggable } from "vue-draggable-plus";
import { createStableObjectKeyResolver } from "@/utils/stableObjectKey";
import {
  defaultProviderBrandOptions,
  providerBrandDisplayName,
  resolveProviderBrand,
} from "@/utils/providerBrand";
import { extractApiErrorMessage } from "@/utils/apiError";
import {
  defaultGroupClientProtocols,
  effectiveGroupClientProtocols,
  hasGroupClientProtocol,
} from "@/utils/groupClientProtocols";
import { useKeyedDebouncedSearch } from "@/composables/useKeyedDebouncedSearch";
import { getPersistedPageSize } from "@/composables/usePersistedPageSize";
import {
  createDefaultMessagesDispatchFormState,
  messagesDispatchConfigToFormState,
  messagesDispatchFormStateToConfig,
  resetMessagesDispatchFormState,
  type MessagesDispatchMappingRow,
} from "./groupsMessagesDispatch";
import {
  buildModelsListConfig,
  createModelsListState as createInitialModelsListState,
  getAvailabilityProbeCandidateModels,
  invertModelsListSelection,
  moveModelsListItem,
  selectAllModelsListItems,
  setModelsListCandidates,
} from "./groupsModelsList";
import { createModelsListCandidatesTracker } from "./groupsModelsListCandidates";
import { normalizeSupportedModelScopesForPlatform } from "./groupsSupportedModelScopes";
import {
  normalizeGroupOpenAIFast,
  normalizeGroupOpenAIFastPolicy,
  supportsGroupOpenAIFast,
} from "./groupsOpenAIFast";
import {
  GROUP_PLATFORM_OPTIONS,
} from "@/constants/platforms";
import {
  normalizeReasoningEffortForPlatform,
  normalizeReasoningEffortOverLimit,
  reasoningEffortMappingsToAPI,
  reasoningEffortMappingsToRows,
  reasoningEffortOverLimitDowngrade,
  type ReasoningEffortMappingRow,
} from "./groupsReasoningEffort";

// 分组与渠道共享完整价卡，空值表示继承，不隐式写入 1 倍。
const emptyGroupPricing = (): PricingFormEntry => ({
  models: [], billing_mode: "token", price_multiplier: null,
  fast_mode_multiplier: null, fast_multiplier: null, flex_multiplier: null,
  max_reasoning_effort_multiplier: null,
  input_price: null, output_price: null, cache_write_price: null, cache_write_1h_price: null,
  cache_read_price: null, image_input_price: null, image_output_price: null,
  per_request_price: null, intervals: [], time_pricing: createDefaultTimePricingForm(),
});
const addGroupPricing = (entries: PricingFormEntry[]) => entries.push(emptyGroupPricing());
const groupPricingFromAPI = (pricing: ChannelModelPricing[] | undefined): PricingFormEntry[] =>
  (pricing || []).map(pricingEntryFromAPI);
const groupPricingToAPI = (pricing: PricingFormEntry[], platform: string): ChannelModelPricing[] =>
  pricing.filter(entry => entry.models.length > 0).map(entry => pricingEntryToAPI(entry, platform));

const { t } = useI18n();
const appStore = useAppStore();
const onboardingStore = useOnboardingStore();
const { formatBalanceAmount } = useBalanceDisplay();
const providerBrandOptions = defaultProviderBrandOptions;

const ALWAYS_VISIBLE_COLUMNS = new Set(["name", "actions"]);
// 首次加载或列结构升级后默认隐藏的列。
const DEFAULT_HIDDEN_COLUMNS = ["id"];
const HIDDEN_COLUMNS_KEY = "group-hidden-columns";
// 新增默认隐藏列时递增版本，让已有管理员只执行一次迁移。
const COLUMN_SETTINGS_VERSION_KEY = "group-column-settings-version";
const COLUMN_SETTINGS_VERSION = 2;
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ["id"],
};

const allColumns = computed<Column[]>(() => [
  { key: "name", label: t("admin.groups.columns.name"), sortable: true },
  { key: "id", label: t("admin.groups.columns.id"), sortable: true },
  {
    key: "platform",
    label: t("admin.groups.columns.platform"),
    sortable: true,
  },
  {
    key: "display_brand",
    label: t("admin.groups.columns.displayBrand"),
    sortable: true,
  },
  {
    key: "rate_multiplier",
    label: t("admin.groups.columns.rateMultiplier"),
    sortable: true,
  },
  {
    key: "is_exclusive",
    label: t("admin.groups.columns.exclusive"),
    sortable: true,
  },
  {
    key: "session_isolation_enabled",
    label: t("admin.groups.columns.sessionIsolation"),
    sortable: true,
  },
  {
    key: "account_count",
    label: t("admin.groups.columns.accounts"),
    sortable: true,
  },
  {
    key: "capacity",
    label: t("admin.groups.columns.capacity"),
    sortable: false,
  },
  { key: "usage", label: t("admin.groups.columns.usage"), sortable: false },
  { key: "status", label: t("admin.groups.columns.status"), sortable: true },
  { key: "actions", label: t("admin.groups.columns.actions"), sortable: false },
]);

const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => !ALWAYS_VISIBLE_COLUMNS.has(col.key)),
);
const hiddenColumns = reactive<Set<string>>(new Set());
const showColumnDropdown = ref(false);
const columnDropdownRef = ref<HTMLElement | null>(null);
const showFilterDropdown = ref(false);
const filterDropdownRef = ref<HTMLElement | null>(null);

const getValidHiddenColumnKeys = () =>
  new Set(toggleableColumns.value.map((col) => col.key));

const activeFilterCount = computed(
  () => [filters.platform, filters.status].filter(Boolean).length,
);

const resetGroupFilters = () => {
  filters.platform = "";
  filters.status = "";
  loadGroups();
};

const loadSavedColumns = () => {
  hiddenColumns.clear();
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY);
    const validKeys = getValidHiddenColumnKeys();

    if (saved) {
      const parsed = JSON.parse(saved);
      if (Array.isArray(parsed)) {
        parsed
          .filter(
            (key): key is string =>
              typeof key === "string" && validKeys.has(key),
          )
          .forEach((key) => hiddenColumns.add(key));
      }

      // 已有管理员自动隐藏本次升级新增的默认隐藏列。
      const parsedVersion = Number(
        localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? "1",
      );
      const storedVersion = Number.isSafeInteger(parsedVersion) && parsedVersion >= 1
        ? parsedVersion
        : 1;
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        let mutated = false;
        for (let version = storedVersion + 1; version <= COLUMN_SETTINGS_VERSION; version++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[version] ?? []) {
            if (validKeys.has(key) && !hiddenColumns.has(key)) {
              hiddenColumns.add(key);
              mutated = true;
            }
          }
        }
        if (mutated) {
          saveColumnsToStorage();
        } else {
          localStorage.setItem(
            COLUMN_SETTINGS_VERSION_KEY,
            String(COLUMN_SETTINGS_VERSION),
          );
        }
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => {
        if (validKeys.has(key)) hiddenColumns.add(key);
      });
      saveColumnsToStorage();
    }
  } catch (error) {
    console.error("Failed to load group column settings:", error);
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key));
  }
};

const saveColumnsToStorage = () => {
  try {
    const validKeys = getValidHiddenColumnKeys();
    const keys = [...hiddenColumns].filter((key) => validKeys.has(key));
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify(keys));
    localStorage.setItem(
      COLUMN_SETTINGS_VERSION_KEY,
      String(COLUMN_SETTINGS_VERSION),
    );
  } catch (error) {
    console.error("Failed to save group column settings:", error);
  }
};

const isColumnVisible = (key: string) => !hiddenColumns.has(key);
const hasVisibleUsageColumn = computed(() => isColumnVisible("usage"));
const hasVisibleCapacityColumn = computed(() => isColumnVisible("capacity"));

const toggleColumn = (key: string) => {
  const validKeys = getValidHiddenColumnKeys();
  if (!validKeys.has(key)) return;

  const wasHidden = hiddenColumns.has(key);
  if (wasHidden) {
    hiddenColumns.delete(key);
  } else {
    hiddenColumns.add(key);
  }
  saveColumnsToStorage();

  if (wasHidden && key === "usage") {
    loadUsageSummary();
  }
  if (wasHidden && key === "capacity") {
    loadCapacitySummary();
  }
};

const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (col) => ALWAYS_VISIBLE_COLUMNS.has(col.key) || !hiddenColumns.has(col.key),
  ),
);

if (typeof window !== "undefined") {
  loadSavedColumns();
}

// Filter options
const statusOptions = computed(() => [
  { value: "", label: t("admin.groups.allStatus") },
  { value: "active", label: t("admin.accounts.status.active") },
  { value: "inactive", label: t("admin.accounts.status.inactive") },
]);

const platformOptions = computed(() => [...GROUP_PLATFORM_OPTIONS]);

const schedulerTypeOptions = computed(() => [
  { value: "basic", label: t("admin.groups.scheduler.basic") },
  { value: "advanced", label: t("admin.groups.scheduler.advanced") },
]);

const cloneAdvancedSchedulerOverrides = (
  value?: GroupAdvancedSchedulerOverrides,
): GroupAdvancedSchedulerOverrides => ({ ...(value || {}) });

const formatAdvancedSchedulerOverridesSummary = (
  value?: GroupAdvancedSchedulerOverrides,
) => {
  const count = Object.keys(value || {}).length;
  return count === 0
    ? t("admin.groups.advancedSchedulerOverrides.allInherited")
    : t("admin.groups.advancedSchedulerOverrides.overriddenCount", { count });
};

const openAdvancedSchedulerOverrides = (target: "create" | "edit") => {
  advancedSchedulerOverridesTarget.value = target;
  advancedSchedulerOverridesDraft.value = cloneAdvancedSchedulerOverrides(
    target === "create"
      ? createForm.advanced_scheduler_overrides
      : editForm.advanced_scheduler_overrides,
  );
  showAdvancedSchedulerOverridesModal.value = true;
};

const closeAdvancedSchedulerOverrides = () => {
  showAdvancedSchedulerOverridesModal.value = false;
  advancedSchedulerOverridesTarget.value = null;
};

const saveAdvancedSchedulerOverrides = (value: GroupAdvancedSchedulerOverrides) => {
  if (advancedSchedulerOverridesTarget.value === "create") {
    createForm.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(value);
  } else if (advancedSchedulerOverridesTarget.value === "edit") {
    editForm.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(value);
  }
  closeAdvancedSchedulerOverrides();
};

const platformFilterOptions = computed(() => [
  { value: "", label: t("admin.groups.allPlatforms") },
  ...GROUP_PLATFORM_OPTIONS,
]);

// 降级分组选项（创建时）- 仅包含 anthropic 平台且未启用 claude_code_only 的分组
const fallbackGroupOptions = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.claudeCode.noFallback") },
  ];
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      !g.claude_code_only &&
      g.status === "active",
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 降级分组选项（编辑时）- 排除自身
const fallbackGroupOptionsForEdit = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.claudeCode.noFallback") },
  ];
  const currentId = editingGroup.value?.id;
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      !g.claude_code_only &&
      g.status === "active" &&
      g.id !== currentId,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 不可用回退分组选项（创建时）：仅允许同平台且启用中的分组。
const unavailableFallbackGroupOptions = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.unavailableFallback.noFallback") },
  ];
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) => g.platform === createForm.platform && g.status === "active",
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 不可用回退分组选项（编辑时）：排除当前分组，避免配置自回退。
const unavailableFallbackGroupOptionsForEdit = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.unavailableFallback.noFallback") },
  ];
  const currentId = editingGroup.value?.id;
  const eligibleGroups = unavailableFallbackGroups.value.filter(
    (g) =>
      g.platform === editForm.platform &&
      g.status === "active" &&
      g.id !== currentId,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 无效请求兜底分组选项（创建时）- 仅包含 anthropic 平台且未配置兜底的分组
const invalidRequestFallbackOptions = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.invalidRequestFallback.noFallback") },
  ];
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      g.status === "active" &&
      g.fallback_group_id_on_invalid_request === null,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 无效请求兜底分组选项（编辑时）- 排除自身
const invalidRequestFallbackOptionsForEdit = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.invalidRequestFallback.noFallback") },
  ];
  const currentId = editingGroup.value?.id;
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      g.status === "active" &&
      g.fallback_group_id_on_invalid_request === null &&
      g.id !== currentId,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 复制账号的源分组选项（创建时）- 仅包含相同平台且有账号的分组
const copyAccountsGroupOptions = computed(() => {
  const eligibleGroups = groups.value.filter(
    (g) => g.platform === createForm.platform && (g.account_count || 0) > 0,
  );
  return eligibleGroups.map((g) => ({
    value: g.id,
    label: `${g.name} (${g.account_count || 0} 个账号)`,
  }));
});

const copyAccountsGroupSelectOptions = computed(() =>
  copyAccountsGroupOptions.value.map((option) => ({
    ...option,
    disabled: createForm.copy_accounts_from_group_ids.includes(option.value),
  })),
);

// 复制账号的源分组选项（编辑时）- 仅包含相同平台且有账号的分组，排除自身
const copyAccountsGroupOptionsForEdit = computed(() => {
  const currentId = editingGroup.value?.id;
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === editForm.platform &&
      (g.account_count || 0) > 0 &&
      g.id !== currentId,
  );
  return eligibleGroups.map((g) => ({
    value: g.id,
    label: `${g.name} (${g.account_count || 0} 个账号)`,
  }));
});

const copyAccountsGroupSelectOptionsForEdit = computed(() =>
  copyAccountsGroupOptionsForEdit.value.map((option) => ({
    ...option,
    disabled: editForm.copy_accounts_from_group_ids.includes(option.value),
  })),
);

function addCreateCopyAccountsGroup(value: string | number | boolean | null) {
  const groupId = Number(value);
  if (groupId && !createForm.copy_accounts_from_group_ids.includes(groupId)) {
    createForm.copy_accounts_from_group_ids.push(groupId);
  }
}

function addEditCopyAccountsGroup(value: string | number | boolean | null) {
  const groupId = Number(value);
  if (groupId && !editForm.copy_accounts_from_group_ids.includes(groupId)) {
    editForm.copy_accounts_from_group_ids.push(groupId);
  }
}

const groups = ref<AdminGroup[]>([]);
// 不可用回退分组需要跨分页选择，因此单独保存全量 active 分组选项来源。
const unavailableFallbackGroups = ref<AdminGroup[]>([]);
const loading = ref(false);
const usageMap = ref<Map<number, { today_cost: number; yesterday_cost: number; total_cost: number }>>(
  new Map(),
);
const usageLoading = ref(false);
const capacityMap = ref<
  Map<
    number,
    {
      concurrencyUsed: number;
      concurrencyMax: number;
      sessionsUsed: number;
      sessionsMax: number;
      rpmUsed: number;
      rpmMax: number;
    }
  >
>(new Map());
const searchQuery = ref("");
const filters = reactive({
  platform: "",
  status: "",
});
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0,
});
const sortState = reactive({
  sort_by: "sort_order",
  sort_order: "asc" as "asc" | "desc",
});

let abortController: AbortController | null = null;

const showCreateModal = ref(false);
const showEditModal = ref(false);
const showDeleteDialog = ref(false);
const pendingLiveForm = ref<"create" | "edit" | null>(null);
const showUnsupportedLiveConfirm = computed(
  () => pendingLiveForm.value !== null,
);
const liveCapability = ref<{ supported: boolean; reason?: string } | null>(null);
let liveCapabilityRequest: Promise<{
  supported: boolean;
  reason?: string;
}> | null = null;
const showSortModal = ref(false);
const submitting = ref(false);
const sortSubmitting = ref(false);
const editingGroup = ref<AdminGroup | null>(null);
const deletingGroup = ref<AdminGroup | null>(null);
const duplicatingGroupIds = reactive(new Set<number>());
const actionMenuGroup = ref<AdminGroup | null>(null);
const actionMenuPosition = ref<{ top: number; left: number } | null>(null);

// 与密钥菜单一致，使用视口坐标和 body 浮层，避免卡片或固定操作列裁切菜单。
const openGroupActionMenu = (group: AdminGroup, event: MouseEvent) => {
  if (actionMenuGroup.value?.id === group.id) {
    closeGroupActionMenu();
    return;
  }
  const target = event.currentTarget as HTMLElement | null;
  if (!target) return;
  const rect = target.getBoundingClientRect();
  const width = 192;
  const height = 162;
  const padding = 8;
  const left = Math.max(padding, Math.min(rect.right - width, window.innerWidth - width - padding));
  let top = rect.bottom + 4;
  if (top + height > window.innerHeight - padding) top = Math.max(padding, rect.top - height - 4);
  actionMenuGroup.value = group;
  actionMenuPosition.value = { top, left };
};

const closeGroupActionMenu = () => {
  actionMenuGroup.value = null;
  actionMenuPosition.value = null;
};
const showRateMultipliersModal = ref(false);
const rateMultipliersGroup = ref<AdminGroup | null>(null);
const showRPMOverridesModal = ref(false);
const rpmOverridesGroup = ref<AdminGroup | null>(null);
const showAdvancedSchedulerOverridesModal = ref(false);
const advancedSchedulerOverridesTarget = ref<"create" | "edit" | null>(null);
const advancedSchedulerOverridesDraft = ref<GroupAdvancedSchedulerOverrides>({});
const sortableGroups = ref<AdminGroup[]>([]);
const createMessagesDispatchDefaults = createDefaultMessagesDispatchFormState();
const editMessagesDispatchDefaults = createDefaultMessagesDispatchFormState();
const createModelsListState = reactive(createInitialModelsListState());
const editModelsListState = reactive(createInitialModelsListState());
// 根据分组平台显示实际使用的模型列表端点。
const modelsListEndpoint = (platform: string) =>
  platform === "gemini" ? "/v1beta/models" : "/v1/models";
const createModelsListLoading = ref(false);
const editModelsListLoading = ref(false);
type ReasoningEffortPolicyFieldsExpose = {
  validate: () => boolean;
  resetValidation: () => void;
};
const createReasoningEffortPolicyRef = ref<ReasoningEffortPolicyFieldsExpose | null>(null);
const editReasoningEffortPolicyRef = ref<ReasoningEffortPolicyFieldsExpose | null>(null);
const createGroupTabsRef = ref<InstanceType<typeof GroupFormTabs> | null>(null);
const editGroupTabsRef = ref<InstanceType<typeof GroupFormTabs> | null>(null);
const modelsListCandidatesTracker = createModelsListCandidatesTracker();
const createModelsListSelectedCount = computed(
  () => createModelsListState.items.filter((item) => item.selected).length,
);
const editModelsListSelectedCount = computed(
  () => editModelsListState.items.filter((item) => item.selected).length,
);
const createAvailabilityProbeModelOptions = computed(() =>
  buildAvailabilityProbeModelOptions(getAvailabilityProbeCandidateModels(createModelsListState)),
);
const editAvailabilityProbeModelOptions = computed(() =>
  buildAvailabilityProbeModelOptions(getAvailabilityProbeCandidateModels(editModelsListState)),
);

// 两个表单共用选项与翻译。
const groupOpenAIFastPolicyOptions = computed(() => [
 {value:"follow_request",label:t("admin.groups.openaiFast.followRequest")},
 {value:"force_priority",label:t("admin.groups.openaiFast.force")},
 {value:"force_ultrafast",label:t("admin.groups.openaiFast.forceUltrafast")},
 {value:"force_off",label:t("admin.groups.openaiFast.forceOff")},
]);

const createForm = reactive({
  name: "",
  description: "",
  display_brand: "",
  platform: "anthropic" as GroupPlatform,
  scheduler_type: "basic" as GroupSchedulerType,
  advanced_scheduler_overrides: {} as GroupAdvancedSchedulerOverrides,
  protocol_fallbacks: {} as Partial<Record<ProtocolID, ProtocolID>>,
  responses_image_policy: "inherit" as "inherit" | "enabled" | "disabled" | "block",
  allowed_protocols: defaultGroupClientProtocols("anthropic") as ProtocolID[],
  rate_multiplier: 1.0,
  is_exclusive: false,
  is_default: false,
  // 会话隔离开关
  session_isolation_enabled: false,
  long_context_pricing_enabled: true,
  model_pricing: [] as PricingFormEntry[],
  // 图片生成计费配置
  allow_image_generation: false,
  allow_batch_image_generation: false,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  // 视频生成计费配置（仅 Grok 平台）
  // Codex 网页搜索按次计费（仅 openai 平台使用）；null = 使用默认价 0.01
  web_search_price_per_call: null as number | null,
  search_price_per_1k: null as number | null,
  audio_realtime_price_per_min: null as number | null,
  audio_tts_price_per_million_chars: null as number | null,
  audio_stt_price_per_hour: null as number | null,
  // 高峰时段倍率配置
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1.0,
  // Claude Code 客户端限制（仅 anthropic 平台使用）
  claude_code_only: false,
  fallback_group_id: null as number | null,
  fallback_group_id_on_invalid_request: null as number | null,
  // 分组不可用时优先使用的指定回退分组。
  unavailable_fallback_group_id: null as number | null,
  // OpenAI Messages 模型映射（仅 openai 平台使用）
  allow_live: false,
  // OpenAI 分组级 Fast 强制策略
  openai_fast_policy: "follow_request",
  // OpenAI 分组级免费 Fast 计费策略
  free_openai_fast: false,
  opus_mapped_model: createMessagesDispatchDefaults.opus_mapped_model,
  sonnet_mapped_model: createMessagesDispatchDefaults.sonnet_mapped_model,
  haiku_mapped_model: createMessagesDispatchDefaults.haiku_mapped_model,
  exact_model_mappings: [] as MessagesDispatchMappingRow[],
  // 账号过滤控制（OpenAI/Antigravity 平台）
  require_oauth_only: false,
  require_privacy_set: false,
  // 模型路由开关
  model_routing_enabled: false,
  // 支持的模型系列（仅 antigravity 平台）
  supported_model_scopes: ["claude", "gemini_text", "gemini_image"] as string[],
  // MCP XML 协议注入开关（仅 antigravity 平台）
  mcp_xml_inject: true,
  // 从分组复制账号
  copy_accounts_from_group_ids: [] as number[],
  // 分组级 RPM 限制（每用户每分钟最大请求数；0 = 不限制）
  rpm_limit: 0 as number,
  max_reasoning_effort: "",
  max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
  reasoning_effort_mappings: [] as ReasoningEffortMappingRow[],
  // 分组主动可用性探测配置
  availability_probe_enabled: false,
  availability_probe_model_id: "",
  availability_probe_prompt: "hi",
  availability_probe_interval_minutes: 30,
  availability_probe_timeout_seconds: 30,
  availability_probe_max_retries: 3,
  availability_probe_user_agent: "",
});

// 简单账号类型（用于模型路由选择）
interface SimpleAccount {
  id: number;
  name: string;
}

// 模型路由规则类型
interface ModelRoutingRule {
  pattern: string;
  accounts: SimpleAccount[]; // 选中的账号对象数组
}

// 创建表单的模型路由规则
const createModelRoutingRules = ref<ModelRoutingRule[]>([]);

// 编辑表单的模型路由规则
const editModelRoutingRules = ref<ModelRoutingRule[]>([]);

// 规则对象稳定 key（避免使用 index 导致状态错位）
const resolveCreateRuleKey =
  createStableObjectKeyResolver<ModelRoutingRule>("create-rule");
const resolveEditRuleKey =
  createStableObjectKeyResolver<ModelRoutingRule>("edit-rule");
const resolveCreateMessagesDispatchRowKey =
  createStableObjectKeyResolver<MessagesDispatchMappingRow>(
    "create-messages-dispatch-row",
  );
const resolveEditMessagesDispatchRowKey =
  createStableObjectKeyResolver<MessagesDispatchMappingRow>(
    "edit-messages-dispatch-row",
  );

const getCreateRuleRenderKey = (rule: ModelRoutingRule) =>
  resolveCreateRuleKey(rule);
const getEditRuleRenderKey = (rule: ModelRoutingRule) =>
  resolveEditRuleKey(rule);
const getCreateMessagesDispatchRowKey = (row: MessagesDispatchMappingRow) =>
  resolveCreateMessagesDispatchRowKey(row);
const getEditMessagesDispatchRowKey = (row: MessagesDispatchMappingRow) =>
  resolveEditMessagesDispatchRowKey(row);

const getCreateRuleSearchKey = (rule: ModelRoutingRule) =>
  `create-${resolveCreateRuleKey(rule)}`;
const getEditRuleSearchKey = (rule: ModelRoutingRule) =>
  `edit-${resolveEditRuleKey(rule)}`;

const getRuleSearchKey = (rule: ModelRoutingRule, isEdit: boolean = false) => {
  return isEdit ? getEditRuleSearchKey(rule) : getCreateRuleSearchKey(rule);
};

// 账号搜索相关状态
const accountSearchKeyword = ref<Record<string, string>>({});
const accountSearchResults = ref<Record<string, SimpleAccount[]>>({});
const showAccountDropdown = ref<Record<string, boolean>>({});

const clearAccountSearchStateByKey = (key: string) => {
  delete accountSearchKeyword.value[key];
  delete accountSearchResults.value[key];
  delete showAccountDropdown.value[key];
};

const clearAllAccountSearchState = () => {
  accountSearchKeyword.value = {};
  accountSearchResults.value = {};
  showAccountDropdown.value = {};
};

const accountSearchRunner = useKeyedDebouncedSearch<SimpleAccount[]>({
  delay: 300,
  search: async (keyword, { signal }) => {
    const res = await adminAPI.accounts.list(
      1,
      20,
      {
        search: keyword,
        platform: "anthropic",
      },
      { signal },
    );
    return res.items.map((account) => ({ id: account.id, name: account.name }));
  },
  onSuccess: (key, result) => {
    accountSearchResults.value[key] = result;
  },
  onError: (key) => {
    accountSearchResults.value[key] = [];
  },
});

// 搜索账号（仅限 anthropic 平台）
const searchAccounts = (key: string) => {
  accountSearchRunner.trigger(key, accountSearchKeyword.value[key] || "");
};

const searchAccountsByRule = (
  rule: ModelRoutingRule,
  isEdit: boolean = false,
) => {
  searchAccounts(getRuleSearchKey(rule, isEdit));
};

// 选择账号
const selectAccount = (
  rule: ModelRoutingRule,
  account: SimpleAccount,
  isEdit: boolean = false,
) => {
  if (!rule) return;

  // 检查是否已选择
  if (!rule.accounts.some((a) => a.id === account.id)) {
    rule.accounts.push(account);
  }

  // 清空搜索
  const key = getRuleSearchKey(rule, isEdit);
  accountSearchKeyword.value[key] = "";
  showAccountDropdown.value[key] = false;
};

// 移除已选账号
const removeSelectedAccount = (
  rule: ModelRoutingRule,
  accountId: number,
  _isEdit: boolean = false,
) => {
  if (!rule) return;

  rule.accounts = rule.accounts.filter((a) => a.id !== accountId);
};

// 切换创建表单的模型系列选择
const toggleCreateScope = (scope: string) => {
  const idx = createForm.supported_model_scopes.indexOf(scope);
  if (idx === -1) {
    createForm.supported_model_scopes.push(scope);
  } else {
    createForm.supported_model_scopes.splice(idx, 1);
  }
};

// 切换编辑表单的模型系列选择
const toggleEditScope = (scope: string) => {
  const idx = editForm.supported_model_scopes.indexOf(scope);
  if (idx === -1) {
    editForm.supported_model_scopes.push(scope);
  } else {
    editForm.supported_model_scopes.splice(idx, 1);
  }
};

// 处理账号搜索输入框聚焦
const onAccountSearchFocus = (
  rule: ModelRoutingRule,
  isEdit: boolean = false,
) => {
  const key = getRuleSearchKey(rule, isEdit);
  showAccountDropdown.value[key] = true;
  // 如果没有搜索结果，触发一次搜索
  if (!accountSearchResults.value[key]?.length) {
    searchAccounts(key);
  }
};

// 添加创建表单的路由规则
const addCreateRoutingRule = () => {
  createModelRoutingRules.value.push({ pattern: "", accounts: [] });
};

// 删除创建表单的路由规则
const removeCreateRoutingRule = (rule: ModelRoutingRule) => {
  const index = createModelRoutingRules.value.indexOf(rule);
  if (index === -1) return;

  const key = getCreateRuleSearchKey(rule);
  accountSearchRunner.clearKey(key);
  clearAccountSearchStateByKey(key);
  createModelRoutingRules.value.splice(index, 1);
};

// 添加编辑表单的路由规则
const addEditRoutingRule = () => {
  editModelRoutingRules.value.push({ pattern: "", accounts: [] });
};

// 删除编辑表单的路由规则
const removeEditRoutingRule = (rule: ModelRoutingRule) => {
  const index = editModelRoutingRules.value.indexOf(rule);
  if (index === -1) return;

  const key = getEditRuleSearchKey(rule);
  accountSearchRunner.clearKey(key);
  clearAccountSearchStateByKey(key);
  editModelRoutingRules.value.splice(index, 1);
};

const resetModelsListState = (
  state: typeof createModelsListState,
  config?: Parameters<typeof createInitialModelsListState>[0],
) => {
  const fresh = createInitialModelsListState(config);
  state.enabled = fresh.enabled;
  state.savedModels = fresh.savedModels;
  state.candidateModels = fresh.candidateModels;
  state.items = fresh.items;
};

const loadModelsListCandidates = async (
  mode: "create" | "edit",
  groupID: number,
  platform: GroupPlatform,
) => {
  const request = { mode, groupID, platform };
  const requestID = modelsListCandidatesTracker.next(request);
  const state = mode === "create" ? createModelsListState : editModelsListState;
  const loadingRef = mode === "create" ? createModelsListLoading : editModelsListLoading;
  loadingRef.value = true;
  try {
    const models = await adminAPI.groups.getModelsListCandidates(groupID, platform);
    if (!modelsListCandidatesTracker.isCurrent(requestID, request)) {
      return;
    }
    setModelsListCandidates(state, models);
  } catch (error) {
    if (!modelsListCandidatesTracker.isCurrent(requestID, request)) {
      return;
    }
    console.error("Error loading group models list candidates:", error);
  } finally {
    if (modelsListCandidatesTracker.isCurrent(requestID, request)) {
      loadingRef.value = false;
    }
  }
};

const moveCreateModelsListItem = (fromIndex: number, toIndex: number) => {
  moveModelsListItem(createModelsListState, fromIndex, toIndex);
};

const moveEditModelsListItem = (fromIndex: number, toIndex: number) => {
  moveModelsListItem(editModelsListState, fromIndex, toIndex);
};

function buildAvailabilityProbeModelOptions(models: string[]) {
  const seen = new Set<string>();
  const options = [{ value: "", label: t("admin.groups.availabilityProbe.selectModel") }];
  for (const raw of models) {
    const model = raw.trim();
    if (!model || seen.has(model)) {
      continue;
    }
    seen.add(model);
    options.push({ value: model, label: model });
  }
  return options;
}

const isAvailabilityProbeModelAvailable = (
  modelID: string,
  options: ReturnType<typeof buildAvailabilityProbeModelOptions>,
) => {
  // 空值代表尚未选择，始终允许保留。
  return !modelID || options.some((option) => option.value === modelID);
};

const resetAvailabilityProbeFormState = (
  form: typeof createForm | typeof editForm,
  config?: GroupAvailabilityProbeConfig | null,
) => {
  form.availability_probe_enabled = config?.enabled ?? false;
  form.availability_probe_model_id = config?.model_id ?? "";
  form.availability_probe_prompt = config?.prompt ?? "hi";
  form.availability_probe_interval_minutes = config?.interval_minutes ?? 30;
  form.availability_probe_timeout_seconds = config?.timeout_seconds ?? 30;
  form.availability_probe_max_retries = config?.max_retries ?? 3;
  form.availability_probe_user_agent = config?.user_agent ?? "";
};

const buildAvailabilityProbeConfig = (
  form: typeof createForm | typeof editForm,
): GroupAvailabilityProbeConfig => {
  if (!form.availability_probe_enabled) {
    return { enabled: false };
  }

  const modelID = form.availability_probe_model_id.trim();
  const prompt = form.availability_probe_prompt.trim();
  if (!modelID) {
    throw new Error(t("admin.groups.availabilityProbe.modelRequired"));
  }
  if (!prompt) {
    throw new Error(t("admin.groups.availabilityProbe.promptRequired"));
  }

  return {
    enabled: true,
    model_id: modelID,
    prompt,
    interval_minutes: Number(form.availability_probe_interval_minutes) || 30,
    timeout_seconds: Number(form.availability_probe_timeout_seconds) || 30,
    // Number("") 为 0，这里有意保留 0 次重试的显式配置。
    max_retries: Number(form.availability_probe_max_retries),
    user_agent: form.availability_probe_user_agent.trim(),
  };
};

// 将 UI 格式的路由规则转换为 API 格式
const convertRoutingRulesToApiFormat = (
  rules: ModelRoutingRule[],
): Record<string, number[]> | null => {
  const result: Record<string, number[]> = {};
  let hasValidRules = false;

  for (const rule of rules) {
    const pattern = rule.pattern.trim();
    if (!pattern) continue;

    const accountIds = rule.accounts.map((a) => a.id).filter((id) => id > 0);

    if (accountIds.length > 0) {
      result[pattern] = accountIds;
      hasValidRules = true;
    }
  }

  return hasValidRules ? result : null;
};

// 将 API 格式的路由规则转换为 UI 格式（需要加载账号名称）
const convertApiFormatToRoutingRules = async (
  apiFormat: Record<string, number[]> | null,
): Promise<ModelRoutingRule[]> => {
  if (!apiFormat) return [];

  const rules: ModelRoutingRule[] = [];
  for (const [pattern, accountIds] of Object.entries(apiFormat)) {
    // 加载账号信息
    const accounts: SimpleAccount[] = [];
    for (const id of accountIds) {
      try {
        const account = await adminAPI.accounts.getById(id);
        accounts.push({ id: account.id, name: account.name });
      } catch {
        // 如果账号不存在，仍然显示 ID
        accounts.push({ id, name: `#${id}` });
      }
    }
    rules.push({ pattern, accounts });
  }
  return rules;
};

const editForm = reactive({
  name: "",
  description: "",
  display_brand: "",
  platform: "anthropic" as GroupPlatform,
  scheduler_type: "basic" as GroupSchedulerType,
  advanced_scheduler_overrides: {} as GroupAdvancedSchedulerOverrides,
  protocol_fallbacks: {} as Partial<Record<ProtocolID, ProtocolID>>,
  responses_image_policy: "inherit" as "inherit" | "enabled" | "disabled" | "block",
  allowed_protocols: defaultGroupClientProtocols("anthropic") as ProtocolID[],
  rate_multiplier: 1.0,
  is_exclusive: false,
  is_default: false,
  // 会话隔离开关
  session_isolation_enabled: false,
  status: "active" as "active" | "inactive",
  long_context_pricing_enabled: true,
  model_pricing: [] as PricingFormEntry[],
  // 图片生成计费配置
  allow_image_generation: false,
  allow_batch_image_generation: false,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  // 视频生成计费配置（仅 Grok 平台）
  // Codex 网页搜索按次计费（仅 openai 平台使用）；null = 使用默认价 0.01
  web_search_price_per_call: null as number | null,
  search_price_per_1k: null as number | null,
  audio_realtime_price_per_min: null as number | null,
  audio_tts_price_per_million_chars: null as number | null,
  audio_stt_price_per_hour: null as number | null,
  // 高峰时段倍率配置
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1.0,
  // Claude Code 客户端限制（仅 anthropic 平台使用）
  claude_code_only: false,
  fallback_group_id: null as number | null,
  fallback_group_id_on_invalid_request: null as number | null,
  // 分组不可用时优先使用的指定回退分组。
  unavailable_fallback_group_id: null as number | null,
  // OpenAI Messages 模型映射（仅 openai 平台使用）
  allow_live: false,
  // OpenAI 分组级 Fast 强制策略
  openai_fast_policy: "follow_request",
  // OpenAI 分组级免费 Fast 计费策略
  free_openai_fast: false,
  default_mapped_model: '',
  opus_mapped_model: editMessagesDispatchDefaults.opus_mapped_model,
  sonnet_mapped_model: editMessagesDispatchDefaults.sonnet_mapped_model,
  haiku_mapped_model: editMessagesDispatchDefaults.haiku_mapped_model,
  exact_model_mappings: [] as MessagesDispatchMappingRow[],
  // 账号过滤控制（OpenAI/Antigravity 平台）
  require_oauth_only: false,
  require_privacy_set: false,
  // 模型路由开关
  model_routing_enabled: false,
  // 支持的模型系列（仅 antigravity 平台）
  supported_model_scopes: ["claude", "gemini_text", "gemini_image"] as string[],
  // MCP XML 协议注入开关（仅 antigravity 平台）
  mcp_xml_inject: true,
  // 从分组复制账号
  copy_accounts_from_group_ids: [] as number[],
  // 分组级 RPM 限制（每用户每分钟最大请求数；0 = 不限制）
  rpm_limit: 0 as number,
  max_reasoning_effort: "",
  max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
  reasoning_effort_mappings: [] as ReasoningEffortMappingRow[],
  // 分组主动可用性探测配置
  availability_probe_enabled: false,
  availability_probe_model_id: "",
  availability_probe_prompt: "hi",
  availability_probe_interval_minutes: 30,
  availability_probe_timeout_seconds: 30,
  availability_probe_max_retries: 3,
  availability_probe_user_agent: "",
});

const createMessagesDispatchEnabled = computed(() =>
  hasGroupClientProtocol(
    createForm.allowed_protocols,
    "anthropic_messages",
  ),
);
const editMessagesDispatchEnabled = computed(() =>
  hasGroupClientProtocol(
    editForm.allowed_protocols,
    "anthropic_messages",
  ),
);

type BatchImagePricingFormState = {
  platform: GroupPlatform;
  allowed_protocols: ProtocolID[];
  allow_image_generation: boolean;
  allow_batch_image_generation: boolean;
  rate_multiplier: number;
  batch_image_discount_multiplier: number;
  batch_image_hold_multiplier: number;
  peak_rate_enabled: boolean;
  peak_start: string;
  peak_end: string;
  peak_rate_multiplier: number;
};

const normalizePreviewNumber = (value: number | string | null | undefined, fallback = 0) => {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
};

const parsePreviewPrice = (value: number | string | null | undefined) => {
  if (value === null || value === undefined || value === "") {
    return null;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
};

const formatToolPricePreview = (value: number | string | null | undefined) => {
  if (value === null || value === undefined || value === "") {
    return t("admin.groups.webSearchPricing.notConfigured");
  }
  const price = Number(value);
  if (!Number.isFinite(price) || price < 0) {
    return t("admin.groups.webSearchPricing.notConfigured");
  }
  return `$${price.toFixed(6).replace(/0+$/, "").replace(/\.$/, "")}`;
};

// Codex 网页搜索单次默认价（与后端 defaultWebSearchPricePerCall 一致，官方 $10/1000 次）
const DEFAULT_WEB_SEARCH_PRICE_PER_CALL = 0.01;

const buildWebSearchFinalPricePreview = (form: {
  web_search_price_per_call: number | string | null;
  rate_multiplier: number | string | null;
}) => {
  const basePrice =
    parsePreviewPrice(form.web_search_price_per_call) ??
    DEFAULT_WEB_SEARCH_PRICE_PER_CALL;
  const multiplier = normalizePreviewNumber(form.rate_multiplier, 1);
  return formatToolPricePreview(basePrice * multiplier);
};

const createWebSearchFinalPricePreview = computed(() =>
  buildWebSearchFinalPricePreview(createForm),
);
const editWebSearchFinalPricePreview = computed(() =>
  buildWebSearchFinalPricePreview(editForm),
);

const resetDisabledBatchImagePricing = (
  form: Pick<
    BatchImagePricingFormState,
    "platform" | "allowed_protocols" | "batch_image_discount_multiplier" | "batch_image_hold_multiplier"
  >,
) => {
  if (form.platform !== "gemini" || !form.allowed_protocols.includes("image_batches")) {
    form.batch_image_discount_multiplier = 0.5;
    form.batch_image_hold_multiplier = 0.6;
  }
};

// 根据分组类型返回不同的删除确认消息
const deleteConfirmMessage = computed(() => {
  if (!deletingGroup.value) {
    return "";
  }
  return t("admin.groups.deleteConfirm", { name: deletingGroup.value.name });
});

const loadLiveCapability = async () => {
  if (liveCapability.value) return liveCapability.value;
  if (!liveCapabilityRequest) {
    liveCapabilityRequest = adminAPI.groups
      .getLiveCapability()
      .catch(() => ({ supported: false }))
      .finally(() => {
        liveCapabilityRequest = null;
      });
  }
  liveCapability.value = await liveCapabilityRequest;
  return liveCapability.value ?? { supported: false };
};



const confirmUnsupportedLive = () => {
  if (pendingLiveForm.value === "create") createForm.allow_live = true;
  if (pendingLiveForm.value === "edit") editForm.allow_live = true;
  pendingLiveForm.value = null;
};

const cancelUnsupportedLive = () => {
  pendingLiveForm.value = null;
};

const loadGroups = async () => {
  if (abortController) {
    abortController.abort();
  }
  const currentController = new AbortController();
  abortController = currentController;
  const { signal } = currentController;
  loading.value = true;
  try {
    const response = await adminAPI.groups.list(
      pagination.page,
      pagination.page_size,
      {
        platform: (filters.platform as GroupPlatform) || undefined,
        status: filters.status as any,
        search: searchQuery.value.trim() || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      { signal },
    );
    if (signal.aborted) return;
    groups.value = response.items;
    pagination.total = response.total;
    pagination.pages = response.pages;
    if (hasVisibleUsageColumn.value) {
      loadUsageSummary();
    } else {
      usageLoading.value = false;
    }
    if (hasVisibleCapacityColumn.value) {
      loadCapacitySummary();
    }
  } catch (error: any) {
    if (
      signal.aborted ||
      error?.name === "AbortError" ||
      error?.code === "ERR_CANCELED"
    ) {
      return;
    }
    appStore.showError(t("admin.groups.failedToLoad"));
    console.error("Error loading groups:", error);
  } finally {
    if (abortController === currentController && !signal.aborted) {
      loading.value = false;
    }
  }
};

const loadUnavailableFallbackGroups = async () => {
  try {
    unavailableFallbackGroups.value = await adminAPI.groups.getAll();
  } catch (error) {
    console.error("Error loading unavailable fallback groups:", error);
  }
};

const formatGroupBalance = (cost: number | null | undefined): string =>
  formatBalanceAmount(cost, { fractionDigits: 2 });


const normalizeDisplayBrand = (value: string): string => value.trim().slice(0, 50);

const displayBrandLabel = (value: unknown): string =>
  providerBrandDisplayName(String(value || ""));

const displayBrandBadgeClass = (value: unknown): string => {
  const base =
    "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium";
  return `${base} ${resolveProviderBrand(String(value || "")).badgeClass}`;
};

const loadUsageSummary = async () => {
  if (!hasVisibleUsageColumn.value) {
    usageLoading.value = false;
    return;
  }
  usageLoading.value = true;
  try {
    const data = await adminAPI.groups.getUsageSummary();
    const map = new Map<number, { today_cost: number; yesterday_cost: number; total_cost: number }>();
    for (const item of data) {
      map.set(item.group_id, {
        today_cost: item.today_cost,
        yesterday_cost: item.yesterday_cost,
        total_cost: item.total_cost,
      });
    }
    usageMap.value = map;
  } catch (error) {
    console.error("Error loading group usage summary:", error);
  } finally {
    usageLoading.value = false;
  }
};

const loadCapacitySummary = async () => {
  if (!hasVisibleCapacityColumn.value) {
    return;
  }
  try {
    const data = await adminAPI.groups.getCapacitySummary();
    const map = new Map<
      number,
      {
        concurrencyUsed: number;
        concurrencyMax: number;
        sessionsUsed: number;
        sessionsMax: number;
        rpmUsed: number;
        rpmMax: number;
      }
    >();
    for (const item of data) {
      map.set(item.group_id, {
        concurrencyUsed: item.concurrency_used,
        concurrencyMax: item.concurrency_max,
        sessionsUsed: item.sessions_used,
        sessionsMax: item.sessions_max,
        rpmUsed: item.rpm_used,
        rpmMax: item.rpm_max,
      });
    }
    capacityMap.value = map;
  } catch (error) {
    console.error("Error loading group capacity summary:", error);
  }
};

let searchTimeout: ReturnType<typeof setTimeout>;
const handleSearch = () => {
  clearTimeout(searchTimeout);
  searchTimeout = setTimeout(() => {
    pagination.page = 1;
    loadGroups();
  }, 300);
};

const handlePageChange = (page: number) => {
  pagination.page = page;
  loadGroups();
};

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize;
  pagination.page = 1;
  loadGroups();
};

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key;
  sortState.sort_order = order;
  pagination.page = 1;
  loadGroups();
};

const openCreateModal = () => {
  showCreateModal.value = true;
  loadModelsListCandidates("create", 0, createForm.platform);
};

const closeCreateModal = () => {
  showCreateModal.value = false;
  createModelRoutingRules.value.forEach((rule) => {
    accountSearchRunner.clearKey(getCreateRuleSearchKey(rule));
  });
  clearAllAccountSearchState();
  createForm.name = "";
  createForm.description = "";
  createForm.display_brand = "";
  createForm.platform = "anthropic";
  createForm.scheduler_type = "basic";
  createForm.advanced_scheduler_overrides = {};
  createForm.allowed_protocols = defaultGroupClientProtocols("anthropic");
  createForm.protocol_fallbacks = { ...protocolCatalog.value?.groups.find(group => group.platform === "anthropic")?.default_fallbacks };
  createForm.responses_image_policy = "inherit";
  createForm.rate_multiplier = 1.0;
  createForm.is_exclusive = false;
  createForm.is_default = false;
  createForm.session_isolation_enabled = false;
  createForm.allow_image_generation = false;
  createForm.allow_batch_image_generation = false;
  createForm.batch_image_discount_multiplier = 0.5;
  createForm.batch_image_hold_multiplier = 0.6;
  createForm.long_context_pricing_enabled = true;
  createForm.model_pricing = [];
  createForm.web_search_price_per_call = null;
  createForm.search_price_per_1k = null;
  createForm.audio_realtime_price_per_min = null;
  createForm.audio_tts_price_per_million_chars = null;
  createForm.audio_stt_price_per_hour = null;
  createForm.peak_rate_enabled = false;
  createForm.peak_start = "";
  createForm.peak_end = "";
  createForm.peak_rate_multiplier = 1.0;
  createForm.claude_code_only = false;
  createForm.fallback_group_id = null;
  createForm.fallback_group_id_on_invalid_request = null;
  createForm.unavailable_fallback_group_id = null;
  resetMessagesDispatchFormState(createForm);
  createForm.allow_live = false;
  createForm.openai_fast_policy = "follow_request";
  createForm.free_openai_fast = false;
  createForm.require_oauth_only = false;
  createForm.require_privacy_set = false;
  createForm.supported_model_scopes = ["claude", "gemini_text", "gemini_image"];
  createForm.mcp_xml_inject = true;
  createForm.copy_accounts_from_group_ids = [];
  createForm.rpm_limit = 0;
  createForm.max_reasoning_effort = "";
  createForm.max_reasoning_effort_over_limit = reasoningEffortOverLimitDowngrade;
  createForm.reasoning_effort_mappings = [];
  createReasoningEffortPolicyRef.value?.resetValidation();
  resetAvailabilityProbeFormState(createForm);
  resetModelsListState(createModelsListState);
  createModelRoutingRules.value = [];
};

const normalizeNonnegativeMultiplier = (
  value: number | string | null | undefined,
): number => {
  if (value === null || value === undefined || value === "") {
    return 1;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 1;
};

// 创建请求中空字符串表示未配置，转换为后端可解析的 null。
const emptyToNull = <T>(value: T | ""): T | null =>
  value === "" ? null : value;

// 整份表单统一校验，业务校验失败也要定位到对应页签中的字段。
const validateGroupForm = async (target: "create" | "edit"): Promise<boolean> => {
  const form = target === "create" ? createForm : editForm;
  const tabs = target === "create" ? createGroupTabsRef.value : editGroupTabsRef.value;
  const reasoning = target === "create"
    ? createReasoningEffortPolicyRef.value
    : editReasoningEffortPolicyRef.value;
  if (tabs && !(await tabs.validate())) return false;
  if (!form.name.trim()) {
    appStore.showError(t("admin.groups.nameRequired"));
    await tabs?.revealField('[data-group-field="name"]');
    return false;
  }
  const pricingError = validatePricingForm(form.model_pricing, t);
  if (pricingError) {
    appStore.showError(pricingError);
    await tabs?.revealField('[data-group-field="model-pricing"]');
    return false;
  }
  if ((form.platform === "openai" || form.platform === "anthropic") && reasoning && !reasoning.validate()) {
    await nextTick();
    await tabs?.revealField('[data-group-field="reasoning"] [role="alert"]');
    return false;
  }
  try {
    buildAvailabilityProbeConfig(form);
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error));
    await tabs?.revealField(form.availability_probe_model_id.trim()
      ? '[data-group-field="probe-prompt"]'
      : '[data-group-field="probe-model"]');
    return false;
  }
  return true;
};

const handleCreateGroup = async () => {
  if (submitting.value || !(await validateGroupForm("create"))) return;
  if (submitting.value || !showCreateModal.value) return;
  submitting.value = true;
  try {
    const availabilityProbeConfig = buildAvailabilityProbeConfig(createForm);
    // 构建请求数据，包含模型路由配置
    const requestData = {
      ...createForm,
      allow_image_generation: undefined,
      allow_batch_image_generation: undefined,
      allow_live: undefined,
      allowed_protocols: [...createForm.allowed_protocols],
      protocol_fallbacks: { ...createForm.protocol_fallbacks },
      responses_image_policy: createForm.responses_image_policy,
      display_brand: normalizeDisplayBrand(createForm.display_brand),
      model_pricing: groupPricingToAPI(
        createForm.model_pricing,
        createForm.platform,
      ),
      model_routing: convertRoutingRulesToApiFormat(
        createModelRoutingRules.value,
      ),
      models_list_config: buildModelsListConfig(createModelsListState),
      availability_probe_config: availabilityProbeConfig,
      supported_model_scopes: normalizeSupportedModelScopesForPlatform(
        createForm.platform,
        createForm.supported_model_scopes,
      ),
      openai_fast_policy: normalizeGroupOpenAIFastPolicy(
        createForm.platform,
        createForm.openai_fast_policy,
      ),
      free_openai_fast: normalizeGroupOpenAIFast(
        createForm.platform,
        createForm.free_openai_fast,
      ),
      max_reasoning_effort_over_limit: normalizeReasoningEffortOverLimit(
        createForm.max_reasoning_effort_over_limit,
      ),
      messages_dispatch_model_config:
        createForm.platform === "openai"
          ? messagesDispatchFormStateToConfig({
              opus_mapped_model: createForm.opus_mapped_model,
              sonnet_mapped_model: createForm.sonnet_mapped_model,
              haiku_mapped_model: createForm.haiku_mapped_model,
              exact_model_mappings: createForm.exact_model_mappings,
            })
          : undefined,
      reasoning_effort_mappings: reasoningEffortMappingsToAPI(
        createForm.reasoning_effort_mappings,
      ),
    };
    delete (requestData as any).availability_probe_enabled;
    delete (requestData as any).availability_probe_model_id;
    delete (requestData as any).availability_probe_prompt;
    delete (requestData as any).availability_probe_interval_minutes;
    delete (requestData as any).availability_probe_timeout_seconds;
    delete (requestData as any).availability_probe_max_retries;
    delete (requestData as any).availability_probe_user_agent;

    resetDisabledBatchImagePricing(requestData);
    requestData.batch_image_discount_multiplier = normalizeNonnegativeMultiplier(
      requestData.batch_image_discount_multiplier,
    );
    requestData.batch_image_hold_multiplier = normalizeNonnegativeMultiplier(
      requestData.batch_image_hold_multiplier,
    );

    // 工具与语音价格输入清空时 v-model.number 产生 ""，直接提交会被后端 *float64 反序列化拒绝（400），
    // 创建时按"未配置"（null）处理。
    requestData.search_price_per_1k = emptyToNull(
      requestData.search_price_per_1k,
    );
    requestData.audio_realtime_price_per_min = emptyToNull(
      requestData.audio_realtime_price_per_min,
    );
    requestData.audio_tts_price_per_million_chars = emptyToNull(
      requestData.audio_tts_price_per_million_chars,
    );
    requestData.audio_stt_price_per_hour = emptyToNull(
      requestData.audio_stt_price_per_hour,
    );
    requestData.web_search_price_per_call = emptyToNull(
      requestData.web_search_price_per_call,
    );
    requestData.peak_rate_enabled = createForm.peak_rate_enabled;
    requestData.peak_start = createForm.peak_start;
    requestData.peak_end = createForm.peak_end;
    requestData.peak_rate_multiplier = normalizeNonnegativeMultiplier(
      createForm.peak_rate_multiplier,
    );
    await adminAPI.groups.create(requestData);
    appStore.showSuccess(t("admin.groups.groupCreated"));
    closeCreateModal();
    loadGroups();
    loadUnavailableFallbackGroups();
    // Only advance tour if active, on submit step, and creation succeeded
    if (onboardingStore.isCurrentStep('[data-tour="group-form-submit"]')) {
      onboardingStore.nextStep(500);
    }
    } catch (error: any) {
      appStore.showError(
        extractApiErrorMessage(error, t("admin.groups.failedToCreate")),
      );
    console.error("Error creating group:", error);
    // Don't advance tour on error
  } finally {
    submitting.value = false;
  }
};

const handleEdit = async (group: AdminGroup) => {
  editingGroup.value = group;
  editForm.name = group.name;
  editForm.description = group.description || "";
  editForm.display_brand = group.display_brand || "";
  editForm.platform = group.platform;
  editForm.scheduler_type = group.scheduler_type ?? "basic";
  editForm.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(
    group.advanced_scheduler_overrides,
  );
  editForm.rate_multiplier = group.rate_multiplier;
  editForm.is_exclusive = group.is_exclusive;
  editForm.is_default = group.is_default ?? false;
  editForm.session_isolation_enabled =
    group.session_isolation_enabled ?? false;
  editForm.status = group.status;
  editForm.long_context_pricing_enabled =
    group.long_context_pricing_enabled ?? true;
  editForm.model_pricing = groupPricingFromAPI(group.model_pricing);
  editForm.allow_image_generation = group.allowed_protocols?.some(id => ['openai_images_generations','openai_images_edits','image_batches'].includes(id)) ?? false;
  editForm.allow_batch_image_generation =
    group.allowed_protocols?.includes('image_batches') ?? false;
  editForm.batch_image_discount_multiplier =
    group.batch_image_discount_multiplier ?? 0.5;
  editForm.batch_image_hold_multiplier = group.batch_image_hold_multiplier ?? 0.6;

  editForm.web_search_price_per_call = group.web_search_price_per_call ?? null;
  editForm.search_price_per_1k = group.search_price_per_1k ?? null;
  editForm.audio_realtime_price_per_min = group.audio_realtime_price_per_min ?? null;
  editForm.audio_tts_price_per_million_chars = group.audio_tts_price_per_million_chars ?? null;
  editForm.audio_stt_price_per_hour = group.audio_stt_price_per_hour ?? null;
  editForm.peak_rate_enabled = group.peak_rate_enabled ?? false;
  editForm.peak_start = group.peak_start ?? "";
  editForm.peak_end = group.peak_end ?? "";
  editForm.peak_rate_multiplier = group.peak_rate_multiplier ?? 1.0;
  editForm.claude_code_only = group.claude_code_only || false;
  editForm.fallback_group_id = group.fallback_group_id;
  editForm.fallback_group_id_on_invalid_request =
    group.fallback_group_id_on_invalid_request;
  editForm.unavailable_fallback_group_id =
    group.unavailable_fallback_group_id;
  const messagesDispatchFormState = messagesDispatchConfigToFormState(
    group.messages_dispatch_model_config,
  );
  editForm.allowed_protocols = effectiveGroupClientProtocols(
    group.platform,
    group.allowed_protocols,
  );
  editForm.protocol_fallbacks = { ...group.protocol_fallbacks };
  editForm.responses_image_policy = group.responses_image_policy ?? "inherit";
  editForm.allow_live = group.allowed_protocols?.includes('openai_live') ?? false;
  editForm.openai_fast_policy = normalizeGroupOpenAIFastPolicy(
    group.platform,
    group.openai_fast_policy ?? (group.force_openai_fast ? "force_priority" : "follow_request"),
  );
  editForm.free_openai_fast = normalizeGroupOpenAIFast(
    group.platform,
    group.free_openai_fast ?? false,
  );
  editForm.opus_mapped_model = messagesDispatchFormState.opus_mapped_model;
  editForm.sonnet_mapped_model = messagesDispatchFormState.sonnet_mapped_model;
  editForm.haiku_mapped_model = messagesDispatchFormState.haiku_mapped_model;
  editForm.exact_model_mappings =
    messagesDispatchFormState.exact_model_mappings;
  editForm.require_oauth_only = group.require_oauth_only ?? false;
  editForm.require_privacy_set = group.require_privacy_set ?? false;
  editForm.model_routing_enabled = group.model_routing_enabled || false;
  editForm.supported_model_scopes = group.supported_model_scopes || [
    "claude",
    "gemini_text",
    "gemini_image",
  ];
  editForm.mcp_xml_inject = group.mcp_xml_inject ?? true;
  editForm.copy_accounts_from_group_ids = []; // 复制账号字段每次编辑时重置为空
  editForm.rpm_limit = group.rpm_limit ?? 0;
  editForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(
    group.platform,
    group.max_reasoning_effort,
  );
  editForm.max_reasoning_effort_over_limit = normalizeReasoningEffortOverLimit(
    group.max_reasoning_effort_over_limit,
  );
  editForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
    group.reasoning_effort_mappings,
    group.platform,
  );
  resetAvailabilityProbeFormState(editForm, group.availability_probe_config);
  resetModelsListState(editModelsListState, group.models_list_config);
  // 加载模型路由规则（异步加载账号名称）
  editModelRoutingRules.value = await convertApiFormatToRoutingRules(
    group.model_routing,
  );
  loadModelsListCandidates("edit", group.id, group.platform);
  showEditModal.value = true;
};

const closeEditModal = () => {
  editModelRoutingRules.value.forEach((rule) => {
    accountSearchRunner.clearKey(getEditRuleSearchKey(rule));
  });
  clearAllAccountSearchState();
  showEditModal.value = false;
  editingGroup.value = null;
  editForm.max_reasoning_effort = "";
  editForm.max_reasoning_effort_over_limit = reasoningEffortOverLimitDowngrade;
  editForm.reasoning_effort_mappings = [];
  editReasoningEffortPolicyRef.value?.resetValidation();
  editModelRoutingRules.value = [];
  editForm.is_default = false;
  editForm.scheduler_type = "basic";
  editForm.advanced_scheduler_overrides = {};
  editForm.session_isolation_enabled = false;
  editForm.unavailable_fallback_group_id = null;
  editForm.copy_accounts_from_group_ids = [];
  resetAvailabilityProbeFormState(editForm);
  editForm.peak_rate_enabled = false;
  editForm.peak_start = "";
  editForm.peak_end = "";
  editForm.peak_rate_multiplier = 1.0;

  editForm.long_context_pricing_enabled = true;
  editForm.model_pricing = [];
  editForm.web_search_price_per_call = null;
  editForm.search_price_per_1k = null;
  editForm.audio_realtime_price_per_min = null;
  editForm.audio_tts_price_per_million_chars = null;
  editForm.audio_stt_price_per_hour = null;
  resetMessagesDispatchFormState(editForm);
  editForm.allow_live = false;
  editForm.openai_fast_policy = "follow_request";
  editForm.free_openai_fast = false;
  resetModelsListState(editModelsListState);
};

const handleUpdateGroup = async () => {
  if (!editingGroup.value) return;
  if (submitting.value || !(await validateGroupForm("edit"))) return;
  if (submitting.value || !showEditModal.value || !editingGroup.value) return;

  submitting.value = true;
  try {
    const availabilityProbeConfig = buildAvailabilityProbeConfig(editForm);
    // 转换 fallback_group_id: null -> 0 (后端使用 0 表示清除)
    const payload = {
      ...editForm,
      allow_image_generation: undefined,
      allow_batch_image_generation: undefined,
      allow_live: undefined,
      allowed_protocols: [...editForm.allowed_protocols],
      protocol_fallbacks: { ...editForm.protocol_fallbacks },
      responses_image_policy: editForm.responses_image_policy,
      display_brand: normalizeDisplayBrand(editForm.display_brand),
      model_pricing: groupPricingToAPI(
        editForm.model_pricing,
        editForm.platform,
      ),
      fallback_group_id:
        editForm.fallback_group_id === null ? 0 : editForm.fallback_group_id,
      fallback_group_id_on_invalid_request:
        editForm.fallback_group_id_on_invalid_request === null
          ? 0
          : editForm.fallback_group_id_on_invalid_request,
      unavailable_fallback_group_id:
        editForm.unavailable_fallback_group_id === null
          ? 0
          : editForm.unavailable_fallback_group_id,
      model_routing: convertRoutingRulesToApiFormat(
        editModelRoutingRules.value,
      ),
      models_list_config: buildModelsListConfig(editModelsListState),
      availability_probe_config: availabilityProbeConfig,
      supported_model_scopes: normalizeSupportedModelScopesForPlatform(
        editForm.platform,
        editForm.supported_model_scopes,
      ),
      openai_fast_policy: normalizeGroupOpenAIFastPolicy(
        editForm.platform,
        editForm.openai_fast_policy,
      ),
      free_openai_fast: normalizeGroupOpenAIFast(
        editForm.platform,
        editForm.free_openai_fast,
      ),
      max_reasoning_effort_over_limit: normalizeReasoningEffortOverLimit(
        editForm.max_reasoning_effort_over_limit,
      ),
      messages_dispatch_model_config:
        editForm.platform === "openai"
          ? messagesDispatchFormStateToConfig({
              opus_mapped_model: editForm.opus_mapped_model,
              sonnet_mapped_model: editForm.sonnet_mapped_model,
              haiku_mapped_model: editForm.haiku_mapped_model,
              exact_model_mappings: editForm.exact_model_mappings,
            })
          : undefined,
      reasoning_effort_mappings: reasoningEffortMappingsToAPI(
        editForm.reasoning_effort_mappings,
      ),
    };
    delete (payload as any).availability_probe_enabled;
    delete (payload as any).availability_probe_model_id;
    delete (payload as any).availability_probe_prompt;
    delete (payload as any).availability_probe_interval_minutes;
    delete (payload as any).availability_probe_timeout_seconds;
    delete (payload as any).availability_probe_max_retries;
    delete (payload as any).availability_probe_user_agent;

    resetDisabledBatchImagePricing(payload);
    payload.batch_image_discount_multiplier = normalizeNonnegativeMultiplier(
      payload.batch_image_discount_multiplier,
    );
    payload.batch_image_hold_multiplier = normalizeNonnegativeMultiplier(
      payload.batch_image_hold_multiplier,
    );

    // 工具与语音价格输入清空时 v-model.number 产生 ""，直接提交会被后端 *float64 反序列化拒绝（400）。
    // 更新语义中 null 表示"不修改"，因此清空后的字段发送 -1：后端 normalizePrice 将负价归一为
    // NULL，从而真正清除已配置的价格。
    const emptyPriceToClear = (v: any) => (v === "" || v === null ? -1 : v);
    payload.search_price_per_1k = emptyPriceToClear(
      payload.search_price_per_1k,
    );
    payload.audio_realtime_price_per_min = emptyPriceToClear(
      payload.audio_realtime_price_per_min,
    );
    payload.audio_tts_price_per_million_chars = emptyPriceToClear(
      payload.audio_tts_price_per_million_chars,
    );
    payload.audio_stt_price_per_hour = emptyPriceToClear(
      payload.audio_stt_price_per_hour,
    );
    payload.web_search_price_per_call = emptyPriceToClear(
      payload.web_search_price_per_call,
    );
    payload.peak_rate_enabled = editForm.peak_rate_enabled;
    payload.peak_start = editForm.peak_start;
    payload.peak_end = editForm.peak_end;
    payload.peak_rate_multiplier = normalizeNonnegativeMultiplier(
      editForm.peak_rate_multiplier,
    );
    await adminAPI.groups.update(editingGroup.value.id, payload);
    appStore.showSuccess(t("admin.groups.groupUpdated"));
    closeEditModal();
    loadGroups();
    loadUnavailableFallbackGroups();
    } catch (error: any) {
      appStore.showError(
        extractApiErrorMessage(error, t("admin.groups.failedToUpdate")),
      );
    console.error("Error updating group:", error);
  } finally {
    submitting.value = false;
  }
};

const addCreateMessagesDispatchMapping = () => {
  createForm.exact_model_mappings.push({ claude_model: "", target_model: "" });
};

const removeCreateMessagesDispatchMapping = (
  row: MessagesDispatchMappingRow,
) => {
  const index = createForm.exact_model_mappings.indexOf(row);
  if (index !== -1) {
    createForm.exact_model_mappings.splice(index, 1);
  }
};

const addEditMessagesDispatchMapping = () => {
  editForm.exact_model_mappings.push({ claude_model: "", target_model: "" });
};

const removeEditMessagesDispatchMapping = (row: MessagesDispatchMappingRow) => {
  const index = editForm.exact_model_mappings.indexOf(row);
  if (index !== -1) {
    editForm.exact_model_mappings.splice(index, 1);
  }
};

const handleRateMultipliers = (group: AdminGroup) => {
  rateMultipliersGroup.value = group;
  showRateMultipliersModal.value = true;
};

const handleRPMOverrides = (group: AdminGroup) => {
  rpmOverridesGroup.value = group;
  showRPMOverridesModal.value = true;
};

const handleDuplicate = async (group: AdminGroup) => {
  if (duplicatingGroupIds.has(group.id)) return;

  duplicatingGroupIds.add(group.id);
  try {
    const duplicate = await adminAPI.groups.duplicate(group.id);
    appStore.showSuccess(
      t("admin.groups.duplicateSuccess", { name: duplicate.name }),
    );
    await loadGroups();
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.groups.duplicateFailed")),
    );
  } finally {
    duplicatingGroupIds.delete(group.id);
  }
};

const handleDelete = (group: AdminGroup) => {
  deletingGroup.value = group;
  showDeleteDialog.value = true;
};

const confirmDelete = async () => {
  if (!deletingGroup.value) return;

  try {
    await adminAPI.groups.delete(deletingGroup.value.id);
    appStore.showSuccess(t("admin.groups.groupDeleted"));
    showDeleteDialog.value = false;
    deletingGroup.value = null;
    loadGroups();
    loadUnavailableFallbackGroups();
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail || t("admin.groups.failedToDelete"),
    );
    console.error("Error deleting group:", error);
  }
};

watch(
  () => editForm.status,
  (newVal) => {
    if (newVal !== "active") {
      editForm.is_default = false;
    }
  },
);

watch(
  () => createForm.platform,
  (newVal) => {
    createForm.allowed_protocols = defaultGroupClientProtocols(newVal);
    createForm.protocol_fallbacks = { ...protocolCatalog.value?.groups.find(group => group.platform === newVal)?.default_fallbacks };
    createForm.unavailable_fallback_group_id = null;
    if (!["anthropic", "antigravity"].includes(newVal)) {
      createForm.fallback_group_id_on_invalid_request = null;
    }
    if (newVal !== "openai") {
      resetMessagesDispatchFormState(createForm);
      createForm.allow_live = false;
    }
    createForm.openai_fast_policy = normalizeGroupOpenAIFastPolicy(
      newVal,
      createForm.openai_fast_policy,
    );
    createForm.free_openai_fast = normalizeGroupOpenAIFast(
      newVal,
      createForm.free_openai_fast,
    );
    createForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(
      newVal,
      createForm.max_reasoning_effort,
    );
    createForm.max_reasoning_effort_over_limit = normalizeReasoningEffortOverLimit(
      createForm.max_reasoning_effort_over_limit,
    );
    createForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
      reasoningEffortMappingsToAPI(createForm.reasoning_effort_mappings),
      newVal,
    );
    createReasoningEffortPolicyRef.value?.resetValidation();
    if (!["openai", "antigravity", "anthropic", "gemini"].includes(newVal)) {
      createForm.require_oauth_only = false;
      createForm.require_privacy_set = false;
    }
    resetDisabledBatchImagePricing(createForm);
    resetModelsListState(createModelsListState);
    loadModelsListCandidates("create", 0, newVal);
  },
);

// 编辑加载会在设置平台后覆盖服务端值；用户切换平台时先使用新平台默认协议。
watch(
  () => editForm.platform,
  (newVal) => {
    editForm.allowed_protocols = defaultGroupClientProtocols(newVal);
    editForm.protocol_fallbacks = { ...protocolCatalog.value?.groups.find(group => group.platform === newVal)?.default_fallbacks };
  },
  { flush: "sync" },
);

watch(createAvailabilityProbeModelOptions, (options) => {
  if (!createModelsListState.enabled && createModelsListState.items.length === 0) {
    return;
  }
  if (
    !isAvailabilityProbeModelAvailable(
      createForm.availability_probe_model_id,
      options,
    )
  ) {
    createForm.availability_probe_model_id = "";
  }
});

watch(editAvailabilityProbeModelOptions, (options) => {
  if (!editModelsListState.enabled && editModelsListState.items.length === 0) {
    return;
  }
  if (
    !isAvailabilityProbeModelAvailable(
      editForm.availability_probe_model_id,
      options,
    )
  ) {
    editForm.availability_probe_model_id = "";
  }
});

watch(
  () => createForm.allow_image_generation,
  () => {
    resetDisabledBatchImagePricing(createForm);
  },
);

watch(
  () => createForm.allowed_protocols.join(","),
  () => {
    resetDisabledBatchImagePricing(createForm);
  },
);

watch(
  () => editForm.platform,
  (newVal) => {
    if (
      editForm.unavailable_fallback_group_id &&
      !unavailableFallbackGroups.value.some(
        (g) =>
          g.id === editForm.unavailable_fallback_group_id &&
          g.platform === newVal &&
          g.status === "active",
      )
    ) {
      editForm.unavailable_fallback_group_id = null;
    }
    if (!["anthropic", "antigravity"].includes(newVal)) {
      editForm.fallback_group_id_on_invalid_request = null;
    }
    if (newVal !== "openai") {
      resetMessagesDispatchFormState(editForm);
      editForm.allow_live = false;
    }
    editForm.openai_fast_policy = normalizeGroupOpenAIFastPolicy(
      newVal,
      editForm.openai_fast_policy,
    );
    editForm.free_openai_fast = normalizeGroupOpenAIFast(
      newVal,
      editForm.free_openai_fast,
    );
    editForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(
      newVal,
      editForm.max_reasoning_effort,
    );
    editForm.max_reasoning_effort_over_limit = normalizeReasoningEffortOverLimit(
      editForm.max_reasoning_effort_over_limit,
    );
    editForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
      reasoningEffortMappingsToAPI(editForm.reasoning_effort_mappings),
      newVal,
    );
    editReasoningEffortPolicyRef.value?.resetValidation();
    if (!["openai", "antigravity", "anthropic", "gemini"].includes(newVal)) {
      editForm.require_oauth_only = false;
      editForm.require_privacy_set = false;
    }
    resetDisabledBatchImagePricing(editForm);
    if (editingGroup.value) {
      resetModelsListState(editModelsListState, editForm.platform === editingGroup.value.platform ? editingGroup.value.models_list_config : undefined);
      loadModelsListCandidates("edit", editingGroup.value.id, newVal);
    }
  },
);

watch(
  () => editForm.allow_image_generation,
  () => {
    resetDisabledBatchImagePricing(editForm);
  },
);

watch(
  () => editForm.allowed_protocols.join(","),
  () => {
    resetDisabledBatchImagePricing(editForm);
  },
);

watch(
  () => editForm.platform,
  (newVal) => {
    if (!['anthropic', 'antigravity'].includes(newVal)) {
      editForm.fallback_group_id_on_invalid_request = null
    }
    if (newVal !== 'openai') {
      editForm.allow_live = false
      editForm.default_mapped_model = ''
    }
    editForm.openai_fast_policy = normalizeGroupOpenAIFastPolicy(
      newVal,
      editForm.openai_fast_policy,
    )
    editForm.free_openai_fast = normalizeGroupOpenAIFast(
      newVal,
      editForm.free_openai_fast,
    )
  }
)

// 点击外部关闭账号搜索下拉框
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  // 检查是否点击在下拉框或输入框内
  if (!target.closest(".account-search-container")) {
    Object.keys(showAccountDropdown.value).forEach((key) => {
      showAccountDropdown.value[key] = false;
    });
  }
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false;
  }
  if (filterDropdownRef.value && !filterDropdownRef.value.contains(target)) {
    showFilterDropdown.value = false;
  }
};

// 打开排序弹窗
const openSortModal = async () => {
  try {
    // 获取所有分组（不分页）
    const allGroups = await adminAPI.groups.getAll();
    // 按 sort_order 排序
    sortableGroups.value = [...allGroups].sort(
      (a, b) => a.sort_order - b.sort_order,
    );
    showSortModal.value = true;
  } catch (error) {
    appStore.showError(t("admin.groups.failedToLoad"));
    console.error("Error loading groups for sorting:", error);
  }
};

// 关闭排序弹窗
const closeSortModal = () => {
  showSortModal.value = false;
  sortableGroups.value = [];
};

// 保存排序
const saveSortOrder = async () => {
  sortSubmitting.value = true;
  try {
    const updates = sortableGroups.value.map((g, index) => ({
      id: g.id,
      sort_order: index * 10,
    }));
    await adminAPI.groups.updateSortOrder(updates);
    appStore.showSuccess(t("admin.groups.sortOrderUpdated"));
    closeSortModal();
    loadGroups();
    loadUnavailableFallbackGroups();
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail || t("admin.groups.failedToUpdateSortOrder"),
    );
    console.error("Error updating sort order:", error);
  } finally {
    sortSubmitting.value = false;
  }
};

onMounted(async () => {
  try { await loadProtocolCatalog(); } catch { appStore.showError(t("admin.protocols.loadError")); }
  loadGroups();
  loadUnavailableFallbackGroups();
  void loadLiveCapability();
  loadModelsListCandidates("create", 0, createForm.platform);
  document.addEventListener("click", handleClickOutside);
});

onUnmounted(() => {
  document.removeEventListener("click", handleClickOutside);
  accountSearchRunner.clearAll();
  clearAllAccountSearchState();
});
</script>
