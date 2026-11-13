<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-2 xl:flex-row xl:items-start xl:justify-between">
          <!-- Left: Search + Filters -->
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-gray-500" />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.proxies.searchProxies')"
                class="input input-has-icon"
                @input="handleSearch"
              />
            </div>
            <FilterDropdown :active-count="activeFilterCount" @reset="resetProxyFilters">
              <FilterField :label="t('admin.proxies.columns.protocol')" icon="link">
                <FilterOptions
                  v-model="filters.protocol"
                  :options="protocolOptions"
                  @change="loadProxies"
                />
              </FilterField>
              <FilterField :label="t('admin.proxies.columns.status')" icon="checkCircle">
                <FilterOptions
                  v-model="filters.status"
                  :options="statusOptions"
                  @change="loadProxies"
                />
              </FilterField>
            </FilterDropdown>
          </div>

          <!-- Right: All action buttons -->
          <div class="flex w-full flex-wrap items-center justify-end gap-2 xl:w-auto">
            <button
              @click="loadProxies"
              :disabled="loading"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <div ref="moreActionsDropdownRef" class="relative shrink-0">
              <button
                type="button"
                class="btn btn-secondary shrink-0 whitespace-nowrap"
                :aria-expanded="showMoreActionsDropdown"
                :title="t('admin.proxies.moreActions')"
                @click="showMoreActionsDropdown = !showMoreActionsDropdown"
              >
                <Icon name="more" size="sm" />
                {{ t('admin.proxies.moreActions') }}
                <Icon name="chevronDown" size="xs" :animate-on-hover="false" />
              </button>
              <MotionTransition name="dropdown-fade">
                <div
                  v-if="showMoreActionsDropdown"
                  :inert="!showMoreActionsDropdown || undefined"
                  class="absolute right-0 top-full z-modal-nested mt-2 w-56 rounded-control border border-gray-200 bg-white p-1 shadow-xl dark:border-dark-600 dark:bg-dark-900"
                  @click.stop
                >
                  <button
                    type="button"
                    class="dropdown-item-sm rounded-control disabled:cursor-not-allowed disabled:opacity-50"
                    :disabled="batchTesting || loading"
                    @click="
                      showMoreActionsDropdown = false;
                      handleBatchTest();
                    "
                  >
                    <Icon name="play" size="sm" />
                    {{ t('admin.proxies.testConnection') }}
                  </button>
                  <button
                    type="button"
                    class="dropdown-item-sm rounded-control disabled:cursor-not-allowed disabled:opacity-50"
                    :disabled="batchQualityChecking || loading"
                    @click="
                      showMoreActionsDropdown = false;
                      handleBatchQualityCheck();
                    "
                  >
                    <Icon
                      name="shield"
                      size="sm"
                      :class="batchQualityChecking ? 'animate-pulse' : ''"
                    />
                    {{ t('admin.proxies.batchQualityCheck') }}
                  </button>
                  <button
                    type="button"
                    class="dropdown-item-sm rounded-control"
                    @click="
                      showMoreActionsDropdown = false;
                      showImportData = true;
                    "
                  >
                    <Icon name="upload" size="sm" />
                    {{ t('admin.proxies.dataImport') }}
                  </button>
                  <button
                    type="button"
                    class="dropdown-item-sm rounded-control"
                    @click="
                      showMoreActionsDropdown = false;
                      showExportDataDialog = true;
                    "
                  >
                    <Icon name="download" size="sm" />
                    {{
                      selectedCount > 0
                        ? t('admin.proxies.dataExportSelected')
                        : t('admin.proxies.dataExport')
                    }}
                  </button>
                </div>
              </MotionTransition>
            </div>
            <button
              @click="openBatchDelete"
              :disabled="selectedCount === 0"
              class="btn btn-danger shrink-0 whitespace-nowrap"
              :title="t('admin.proxies.batchDeleteAction')"
            >
              <Icon name="trash" size="sm" class="mr-2" />
              {{ t('admin.proxies.batchDeleteAction') }}
            </button>
            <button
              @click="showCreateModal = true"
              class="btn btn-primary shrink-0 whitespace-nowrap"
            >
              <Icon name="plus" size="sm" class="mr-2" />
              {{ t('admin.proxies.createProxy') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <div ref="proxyTableRef" class="flex min-h-0 flex-1 flex-col overflow-hidden">
          <DataTable
            column-order-storage-key="admin-proxies-column-order"
            :columns="columns"
            :data="proxies"
            :loading="loading"
            :server-side-sort="true"
            default-sort-key="id"
            default-sort-order="desc"
            @sort="handleSort"
          >
            <template #header-select>
              <input
                type="checkbox"
                class="h-4 w-4 cursor-pointer rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="allVisibleSelected"
                @click.stop
                @change="toggleSelectAllVisible($event)"
              />
            </template>

            <template #cell-select="{ row }">
              <input
                type="checkbox"
                class="h-4 w-4 cursor-pointer rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="selectedProxyIds.has(row.id)"
                @click.stop
                @change="toggleSelectRow(row.id, $event)"
              />
            </template>

            <template #cell-name="{ value }">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
            </template>

            <template #cell-protocol="{ value }">
              <span
                v-if="value"
                :class="['badge', value.startsWith('socks5') ? 'badge-primary' : 'badge-gray']"
              >
                {{ value.toUpperCase() }}
              </span>
              <span v-else class="text-sm text-gray-400">-</span>
            </template>

            <template #cell-address="{ row }">
              <div class="flex items-center gap-1.5">
                <code class="code text-xs">{{ row.host }}:{{ row.port }}</code>
                <div class="relative">
                  <button
                    type="button"
                    class="rounded-compact p-0.5 text-gray-400 hover:text-primary-600 dark:hover:text-primary-400"
                    :title="t('admin.proxies.copyProxyUrl')"
                    @click.stop="copyProxyUrl(row)"
                    @contextmenu.prevent="toggleCopyMenu(row.id)"
                  >
                    <Icon name="copy" size="sm" />
                  </button>
                  <!-- Right-click opens proxy copy format choices. -->
                  <MotionTransition name="dropdown-fade">
                    <div
                      v-if="copyMenuProxyId === row.id"
                      :inert="!(copyMenuProxyId === row.id) || undefined"
                      class="dropdown left-0 top-full z-50 mt-1 w-auto min-w-[180px]"
                    >
                      <button
                        v-for="fmt in getCopyFormats(row)"
                        :key="fmt.label"
                        class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-gray-100 dark:hover:bg-dark-800"
                        @click.stop="copyFormat(fmt.value)"
                      >
                        <span class="truncate font-mono text-gray-600 dark:text-gray-300">{{
                          fmt.label
                        }}</span>
                      </button>
                    </div>
                  </MotionTransition>
                </div>
              </div>
            </template>

            <template #cell-auth="{ row }">
              <div v-if="row.username || row.password" class="flex items-center gap-1.5">
                <div class="flex flex-col text-xs">
                  <span v-if="row.username" class="text-gray-700 dark:text-gray-200">{{
                    row.username
                  }}</span>
                  <span v-if="row.password" class="font-mono text-gray-500 dark:text-gray-400">
                    {{ visiblePasswordIds.has(row.id) ? row.password : '••••••' }}
                  </span>
                </div>
                <button
                  v-if="row.password"
                  type="button"
                  class="ml-1 rounded-compact p-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                  @click.stop="
                    visiblePasswordIds.has(row.id)
                      ? visiblePasswordIds.delete(row.id)
                      : visiblePasswordIds.add(row.id)
                  "
                >
                  <Icon :name="visiblePasswordIds.has(row.id) ? 'eyeOff' : 'eye'" size="sm" />
                </button>
              </div>
              <span v-else class="text-sm text-gray-400">-</span>
            </template>

            <template #cell-location="{ row }">
              <div class="flex items-center gap-2">
                <img
                  v-if="row.country_code"
                  :src="flagUrl(row.country_code)"
                  :alt="row.country || row.country_code"
                  class="h-4 w-6 rounded-compact"
                />
                <!-- Location text establishes the baseline used by the label in the mobile table card. -->
                <span
                  v-if="formatLocation(row)"
                  class="self-baseline text-sm text-gray-700 dark:text-gray-200"
                >
                  {{ formatLocation(row) }}
                </span>
                <span v-else class="self-baseline text-sm text-gray-400">-</span>
              </div>
            </template>

            <template #cell-provider_count="{ row, value }">
              <button
                v-if="(value || 0) > 0"
                type="button"
                class="inline-flex items-center rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-primary-700 hover:bg-gray-200 dark:bg-dark-700 dark:text-primary-300 dark:hover:bg-dark-600"
                @click="openProvidersModal(row)"
              >
                {{ t('admin.groups.providersCount', { count: value || 0 }) }}
              </button>
              <span
                v-else
                class="inline-flex items-center rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
              >
                {{ t('admin.groups.providersCount', { count: 0 }) }}
              </span>
            </template>

            <template #cell-latency="{ row }">
              <div class="flex flex-col gap-1">
                <span
                  v-if="row.latency_status === 'failed'"
                  class="badge badge-danger"
                  :title="row.latency_message || undefined"
                >
                  {{ t('admin.proxies.latencyFailed') }}
                </span>
                <span
                  v-else-if="typeof row.latency_ms === 'number'"
                  :class="['badge', row.latency_ms < 200 ? 'badge-success' : 'badge-warning']"
                >
                  {{ row.latency_ms }}ms
                </span>
                <span v-else class="text-sm text-gray-400">-</span>
                <div
                  v-if="typeof row.quality_checked === 'number'"
                  class="flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400"
                  :title="row.quality_summary || undefined"
                >
                  <span>{{
                    t('admin.proxies.qualityInline', {
                      grade: row.quality_grade || '-',
                      score: row.quality_score ?? '-',
                    })
                  }}</span>
                  <span class="badge" :class="qualityOverallClass(row.quality_status)">
                    {{ qualityOverallLabel(row.quality_status) }}
                  </span>
                </div>
              </div>
            </template>

            <template #cell-expiry="{ row }">
              <span v-if="!row.expires_at" class="text-sm text-gray-400">{{
                t('admin.proxies.neverExpires')
              }}</span>
              <div v-else class="flex flex-col text-xs">
                <span class="text-gray-700 dark:text-gray-200">{{
                  formatDateTime(row.expires_at)
                }}</span>
                <span :class="expiryBadgeClass(row)">{{ expiryLabel(row) }}</span>
              </div>
            </template>

            <template #cell-created_at="{ row }">
              <span class="text-xs text-gray-600 dark:text-gray-300">{{
                formatDateTime(row.created_at)
              }}</span>
            </template>

            <template #cell-status="{ value }">
              <span
                :class="[
                  'badge',
                  value === 'active'
                    ? 'badge-success'
                    : value === 'expired'
                      ? 'badge-danger'
                      : 'badge-danger',
                ]"
              >
                {{ t('admin.providers.status.' + value) }}
              </span>
            </template>

            <template #cell-actions="{ row }">
              <div class="flex items-center gap-1">
                <button
                  @click="handleTestConnection(row)"
                  :disabled="testingProxyIds.has(row.id)"
                  class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-emerald-50 hover:text-emerald-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400"
                >
                  <Icon
                    name="loader"
                    size="sm"
                    :animate-on-hover="false"
                    v-if="testingProxyIds.has(row.id)"
                    class="h-4 w-4 animate-spin"
                  />
                  <Icon v-else name="checkCircle" size="sm" :animate-on-hover="false" />
                  <span class="text-xs">{{ t('admin.proxies.testConnection') }}</span>
                </button>
                <button
                  @click="handleQualityCheck(row)"
                  :disabled="qualityCheckingProxyIds.has(row.id)"
                  class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-primary-500/8 dark:hover:text-primary-500"
                >
                  <Icon
                    name="loader"
                    size="sm"
                    :animate-on-hover="false"
                    v-if="qualityCheckingProxyIds.has(row.id)"
                    class="h-4 w-4 animate-spin"
                  />
                  <Icon v-else name="shield" size="sm" />
                  <span class="text-xs">{{ t('admin.proxies.qualityCheck') }}</span>
                </button>
                <button
                  @click="handleEdit(row)"
                  class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                >
                  <Icon name="edit" size="sm" />
                  <span class="text-xs">{{ t('common.edit') }}</span>
                </button>
                <button
                  @click="handleDelete(row)"
                  class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                >
                  <Icon name="trash" size="sm" />
                  <span class="text-xs">{{ t('common.delete') }}</span>
                </button>
              </div>
            </template>

            <template #empty>
              <EmptyState
                :title="t('admin.proxies.noProxiesYet')"
                :description="t('admin.proxies.createFirstProxy')"
                :action-text="t('admin.proxies.createProxy')"
                @action="showCreateModal = true"
              />
            </template>
          </DataTable>
        </div>
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

    <!-- Create Proxy Modal -->
    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.proxies.createProxy')"
      width="normal"
      @close="closeCreateModal"
    >
      <!-- Proxy creation mode. -->
      <div class="mb-6 flex border-b border-gray-200 dark:border-dark-600">
        <button
          type="button"
          @click="createMode = 'standard'"
          :class="[
            '-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors',
            createMode === 'standard'
              ? 'border-primary-500 text-primary-600 dark:text-primary-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300',
          ]"
        >
          <Icon name="plus" size="sm" class="mr-1.5 inline" />
          {{ t('admin.proxies.standardAdd') }}
        </button>
        <button
          type="button"
          @click="createMode = 'batch'"
          :class="[
            '-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors',
            createMode === 'batch'
              ? 'border-primary-500 text-primary-600 dark:text-primary-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300',
          ]"
        >
          <Icon name="list" size="sm" class="mr-1.5 inline h-4 w-4" />
          {{ t('admin.proxies.batchAdd') }}
        </button>
      </div>

      <!-- Standard Add Form -->
      <form
        v-if="createMode === 'standard'"
        id="create-proxy-form"
        @submit.prevent="handleCreateProxy"
        class="space-y-5"
      >
        <div>
          <label class="input-label">{{ t('admin.proxies.name') }}</label>
          <input
            v-model="createForm.name"
            type="text"
            required
            class="input"
            :placeholder="t('admin.proxies.enterProxyName')"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.protocol') }}</label>
          <Select v-model="createForm.protocol" :options="protocolSelectOptions" />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.proxies.host') }}</label>
            <input
              v-model="createForm.host"
              type="text"
              required
              :placeholder="t('admin.proxies.form.hostPlaceholder')"
              class="input"
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.proxies.port') }}</label>
            <input
              v-model.number="createForm.port"
              type="number"
              required
              min="1"
              max="65535"
              :placeholder="t('admin.proxies.form.portPlaceholder')"
              class="input"
            />
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.username') }}</label>
          <input
            v-model="createForm.username"
            type="text"
            class="input"
            :placeholder="t('admin.proxies.optionalAuth')"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.password') }}</label>
          <div class="relative">
            <input
              v-model="createForm.password"
              :type="createPasswordVisible ? 'text' : 'password'"
              class="input pr-10"
              :placeholder="t('admin.proxies.optionalAuth')"
            />
            <button
              type="button"
              class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
              @click="createPasswordVisible = !createPasswordVisible"
            >
              <Icon :name="createPasswordVisible ? 'eyeOff' : 'eye'" size="sm" />
            </button>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.expiresAt') }}</label>
          <div class="mb-2 flex flex-wrap gap-2">
            <button
              v-for="d in EXPIRY_PRESETS"
              :key="d"
              type="button"
              class="btn btn-sm"
              :class="
                createForm.expires_at === addDaysToBase('', d) ? 'btn-primary' : 'btn-secondary'
              "
              @click="createExpiresDays = d"
            >
              {{ t('admin.proxies.nDays', { days: d }) }}
            </button>
          </div>
          <input
            v-model.number="createExpiresDays"
            type="number"
            min="0"
            class="input mb-2"
            :placeholder="t('admin.proxies.expiryDaysPlaceholder')"
          />
          <input v-model="createForm.expires_at" type="date" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.fallbackMode') }}</label>
          <Select
            v-model="createForm.fallback_mode"
            :options="[
              { label: t('admin.proxies.fallbackNone'), value: 'none' },
              { label: t('admin.proxies.fallbackProxy'), value: 'proxy' },
              { label: t('admin.proxies.fallbackDirect'), value: 'direct' },
            ]"
          />
        </div>
        <div v-if="createForm.fallback_mode === 'proxy'">
          <label class="input-label">{{ t('admin.proxies.backupProxy') }}</label>
          <Select v-model="createForm.backup_proxy_id" :options="backupProxyOptions()" />
        </div>
      </form>

      <!-- Batch Add Form -->
      <div v-else class="space-y-5">
        <div>
          <label class="input-label">{{ t('admin.proxies.batchInput') }}</label>
          <textarea
            v-model="batchInput"
            rows="10"
            class="input font-mono text-sm"
            :placeholder="t('admin.proxies.batchInputPlaceholder')"
            @input="parseBatchInput"
          ></textarea>
          <p class="input-hint mt-2">
            {{ t('admin.proxies.batchInputHint') }}
          </p>
        </div>

        <!-- Parse Result -->
        <div
          v-if="batchParseResult.total > 0"
          class="rounded-control bg-gray-50 p-4 dark:bg-dark-700"
        >
          <div class="flex items-center gap-4 text-sm">
            <div class="flex items-center gap-1.5">
              <Icon
                name="checkCircle"
                size="sm"
                :stroke-width="2"
                class="text-primary-500"
                :animate-on-hover="false"
              />
              <span class="text-gray-700 dark:text-gray-300">
                {{ t('admin.proxies.parsedCount', { count: batchParseResult.valid }) }}
              </span>
            </div>
            <div v-if="batchParseResult.invalid > 0" class="flex items-center gap-1.5">
              <Icon name="exclamationCircle" size="sm" :stroke-width="2" class="text-amber-500" />
              <span class="text-amber-600 dark:text-amber-400">
                {{ t('admin.proxies.invalidCount', { count: batchParseResult.invalid }) }}
              </span>
            </div>
            <div v-if="batchParseResult.duplicate > 0" class="flex items-center gap-1.5">
              <Icon name="copy" size="sm" class="h-4 w-4 text-gray-400" />
              <span class="text-gray-500 dark:text-gray-400">
                {{ t('admin.proxies.duplicateCount', { count: batchParseResult.duplicate }) }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeCreateModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            v-if="createMode === 'standard'"
            type="submit"
            form="create-proxy-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ submitting ? t('admin.proxies.creating') : t('common.create') }}
          </button>
          <button
            v-else
            @click="handleBatchCreate"
            type="button"
            :disabled="submitting || batchParseResult.valid === 0"
            class="btn btn-primary"
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
                ? t('admin.proxies.importing')
                : t('admin.proxies.importProxies', { count: batchParseResult.valid })
            }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Proxy Modal -->
    <BaseDialog
      :show="showEditModal"
      :title="t('admin.proxies.editProxy')"
      width="normal"
      @close="closeEditModal"
    >
      <form
        v-if="editingProxy"
        id="edit-proxy-form"
        @submit.prevent="handleUpdateProxy"
        class="space-y-5"
      >
        <div>
          <label class="input-label">{{ t('admin.proxies.name') }}</label>
          <input v-model="editForm.name" type="text" required class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.protocol') }}</label>
          <Select v-model="editForm.protocol" :options="protocolSelectOptions" />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.proxies.host') }}</label>
            <input v-model="editForm.host" type="text" required class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.proxies.port') }}</label>
            <input
              v-model.number="editForm.port"
              type="number"
              required
              min="1"
              max="65535"
              class="input"
            />
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.username') }}</label>
          <input v-model="editForm.username" type="text" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.password') }}</label>
          <div class="relative">
            <input
              v-model="editForm.password"
              :type="editPasswordVisible ? 'text' : 'password'"
              :placeholder="t('admin.proxies.leaveEmptyToKeep')"
              class="input pr-10"
              @input="editPasswordDirty = true"
            />
            <button
              type="button"
              class="absolute right-3 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
              @click="editPasswordVisible = !editPasswordVisible"
            >
              <Icon :name="editPasswordVisible ? 'eyeOff' : 'eye'" size="sm" />
            </button>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.status') }}</label>
          <Select v-model="editForm.status" :options="editStatusOptions" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.expiresAt') }}</label>
          <div class="mb-2 flex flex-wrap gap-2">
            <button
              v-for="d in EXPIRY_PRESETS"
              :key="d"
              type="button"
              class="btn btn-sm"
              :class="
                editForm.expires_at === addDaysToBase(editBaseDate, d)
                  ? 'btn-primary'
                  : 'btn-secondary'
              "
              @click="editExpiresDays = d"
            >
              {{ t('admin.proxies.nDays', { days: d }) }}
            </button>
          </div>
          <input
            v-model.number="editExpiresDays"
            type="number"
            min="0"
            class="input mb-2"
            :placeholder="t('admin.proxies.expiryDaysPlaceholder')"
          />
          <input v-model="editForm.expires_at" type="date" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.proxies.fallbackMode') }}</label>
          <Select
            v-model="editForm.fallback_mode"
            :options="[
              { label: t('admin.proxies.fallbackNone'), value: 'none' },
              { label: t('admin.proxies.fallbackProxy'), value: 'proxy' },
              { label: t('admin.proxies.fallbackDirect'), value: 'direct' },
            ]"
          />
        </div>
        <div v-if="editForm.fallback_mode === 'proxy'">
          <label class="input-label">{{ t('admin.proxies.backupProxy') }}</label>
          <Select
            v-model="editForm.backup_proxy_id"
            :options="backupProxyOptions(editingProxy?.id)"
          />
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeEditModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            v-if="editingProxy"
            type="submit"
            form="edit-proxy-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="submitting"
              class="-ml-1 mr-2 h-4 w-4 animate-spin"
            />
            {{ submitting ? t('admin.proxies.updating') : t('common.update') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.proxies.deleteProxy')"
      :message="t('admin.proxies.deleteConfirm', { name: deletingProxy?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Batch Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showBatchDeleteDialog"
      :title="t('admin.proxies.batchDelete')"
      :message="t('admin.proxies.batchDeleteConfirm', { count: selectedCount })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmBatchDelete"
      @cancel="showBatchDeleteDialog = false"
    />
    <ConfirmDialog
      :show="showExportDataDialog"
      :title="t('admin.proxies.dataExport')"
      :message="t('admin.proxies.dataExportConfirmMessage')"
      :confirm-text="t('admin.proxies.dataExportConfirm')"
      :cancel-text="t('common.cancel')"
      @confirm="handleExportData"
      @cancel="showExportDataDialog = false"
    />

    <ImportDataModal
      :show="showImportData"
      @close="showImportData = false"
      @imported="handleDataImported"
    />

    <BaseDialog
      :show="showQualityReportDialog"
      :title="t('admin.proxies.qualityReportTitle')"
      width="normal"
      @close="closeQualityReportDialog"
    >
      <div v-if="qualityReport" class="space-y-4">
        <div
          class="rounded-surface border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="flex items-center justify-between gap-4">
            <div>
              <div class="text-sm text-gray-500 dark:text-gray-400">
                {{ qualityReportProxy?.name || '-' }}
              </div>
              <div class="mt-1 text-sm text-gray-700 dark:text-gray-200">
                {{ qualityReport.summary }}
              </div>
            </div>
            <div class="text-right">
              <div class="text-2xl font-semibold text-gray-900 dark:text-white">
                {{ qualityReport.score }}
              </div>
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.proxies.qualityGrade', { grade: qualityReport.grade }) }}
              </div>
            </div>
          </div>
          <div class="mt-3 grid grid-cols-2 gap-2 text-xs text-gray-600 dark:text-gray-300">
            <div>{{ t('admin.proxies.qualityExitIP') }}: {{ qualityReport.exit_ip || '-' }}</div>
            <div>{{ t('admin.proxies.qualityCountry') }}: {{ qualityReport.country || '-' }}</div>
            <div>
              {{ t('admin.proxies.qualityBaseLatency') }}:
              {{
                typeof qualityReport.base_latency_ms === 'number'
                  ? `${qualityReport.base_latency_ms}ms`
                  : '-'
              }}
            </div>
            <div>
              {{ t('admin.proxies.qualityCheckedAt') }}:
              <!-- Convert report Unix seconds to browser milliseconds for localized display. -->
              {{ new Date(qualityReport.checked_at * 1000).toLocaleString() }}
            </div>
          </div>
        </div>

        <div
          class="max-h-80 overflow-auto rounded-control border border-gray-200 dark:border-dark-600"
        >
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
              <tr>
                <th class="whitespace-nowrap px-3 py-2 text-left">
                  {{ t('admin.proxies.qualityTableTarget') }}
                </th>
                <th class="whitespace-nowrap px-3 py-2 text-left">
                  {{ t('admin.proxies.qualityTableStatus') }}
                </th>
                <th class="whitespace-nowrap px-3 py-2 text-left">HTTP</th>
                <th class="whitespace-nowrap px-3 py-2 text-left">
                  {{ t('admin.proxies.qualityTableLatency') }}
                </th>
                <th class="px-3 py-2 text-left">{{ t('admin.proxies.qualityTableMessage') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
              <tr v-for="item in qualityReport.items" :key="item.target">
                <td class="whitespace-nowrap px-3 py-2 text-gray-900 dark:text-white">
                  {{ qualityTargetLabel(item.target) }}
                </td>
                <td class="whitespace-nowrap px-3 py-2">
                  <span class="badge whitespace-nowrap" :class="qualityStatusClass(item.status)">{{
                    qualityStatusLabel(item.status)
                  }}</span>
                </td>
                <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">
                  {{ item.http_status ?? '-' }}
                </td>
                <td class="whitespace-nowrap px-3 py-2 text-gray-600 dark:text-gray-300">
                  {{ typeof item.latency_ms === 'number' ? `${item.latency_ms}ms` : '-' }}
                </td>
                <td class="px-3 py-2 text-gray-600 dark:text-gray-300">
                  <span>{{ item.message || '-' }}</span>
                  <span v-if="item.cf_ray" class="ml-1 text-xs text-gray-400"
                    >(cf-ray: {{ item.cf_ray }})</span
                  >
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button @click="closeQualityReportDialog" class="btn btn-secondary">
            {{ t('common.close') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Proxy Providers Dialog -->
    <BaseDialog
      :show="showProvidersModal"
      :title="t('admin.proxies.providersTitle', { name: providersProxy?.name || '' })"
      width="normal"
      @close="closeProvidersModal"
    >
      <div
        v-if="!providersLoading && proxyProviders.length === 0"
        class="py-6 text-center text-sm text-gray-500"
      >
        {{ t('admin.proxies.providersEmpty') }}
      </div>
      <div v-else class="max-h-80 overflow-auto">
        <table
          :aria-busy="providersLoading"
          class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700"
        >
          <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            <tr>
              <th class="px-4 py-2 text-left">{{ t('admin.proxies.providerName') }}</th>
              <th class="px-4 py-2 text-left">{{ t('admin.providers.columns.platformType') }}</th>
              <th class="px-4 py-2 text-left">{{ t('admin.proxies.providerNotes') }}</th>
            </tr>
          </thead>
          <TableSkeletonBody v-if="providersLoading" :columns="3" cell-class="px-3 py-2" />
          <tbody
            v-else
            class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"
          >
            <tr v-for="provider in proxyProviders" :key="provider.id">
              <td class="px-4 py-2 font-medium text-gray-900 dark:text-white">
                {{ provider.name }}
              </td>
              <td class="px-4 py-2">
                <PlatformTypeBadge :platform="provider.platform" :type="provider.type" />
              </td>
              <td class="px-4 py-2 text-gray-600 dark:text-gray-300">
                {{ provider.notes || '-' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button @click="closeProvidersModal" class="btn btn-secondary">
            {{ t('common.close') }}
          </button>
        </div>
      </template>
    </BaseDialog>
    <TotpStepUpDialog :controller="proxyExportStepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  Proxy,
  ProxyProviderSummary,
  ProxyProtocol,
  ProxyQualityCheckResult,
} from '@/api/admin/proxies.types'
import type { Column } from '@/components/common/table/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ImportDataModal from '@/components/admin/proxy/ImportDataModal.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import type { FilterOption } from '@/components/common/filterPanel'
import Icon from '@/components/icons/Icon.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useSwipeSelect } from '@/composables/useSwipeSelect'
import { useTableSelection } from '@/composables/useTableSelection'
import {
  useStepUp,
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
} from '@/composables/useStepUp'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { formatDateTime } from '@/utils/format'
import { proxyExpiryBadgeClass, proxyExpiryLabelKey } from '@/utils/proxyExpiry'

// ProxiesView.vue manages proxy records, imports, exports, connection checks, and provider
// associations. Create and edit drafts include authentication and expiry settings, while table
// selection drives optional batch operations across pages. Password edits track whether the
// administrator changed the field before including it in an update.
//
// List reads reject superseded responses using controller identity. Connection and quality
// batches allocate work from shared indexes before awaiting requests, limiting concurrent
// checks while preserving per-proxy progress. Expiry controls convert between local date text
// and day counts, and payloads use Unix seconds. Exports run through step-up verification and
// release their temporary Blob URL after the download click.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()
// appStore displays operation notifications after requests complete or validation prevents the
// requested action.
const appStore = useAppStore()
// copyToClipboard publishes the chosen proxy representation through the shared clipboard helper
// and supplies the corresponding success notification.
const { copyToClipboard } = useClipboard()
// proxyExportStepUp owns verification for credential-bearing exports and exposes dialog state
// consumed by the mounted step-up confirmation component.
const proxyExportStepUp = useStepUp()

// columns defines the displayed table fields and their translated headings, ordering, and
// sorting controls.
const columns = computed<Column[]>(() => [
  { key: 'select', label: '', sortable: false },
  { key: 'name', label: t('admin.proxies.columns.name'), sortable: true },
  { key: 'protocol', label: t('admin.proxies.columns.protocol'), sortable: true },
  { key: 'address', label: t('admin.proxies.columns.address'), sortable: false },
  { key: 'auth', label: t('admin.proxies.columns.auth'), sortable: false },
  { key: 'location', label: t('admin.proxies.columns.location'), sortable: false },
  { key: 'provider_count', label: t('admin.proxies.columns.providers'), sortable: true },
  { key: 'latency', label: t('admin.proxies.columns.latency'), sortable: false },
  { key: 'expiry', label: t('admin.proxies.columns.expiry'), sortable: true },
  { key: 'created_at', label: t('admin.proxies.columns.createdAt'), sortable: true },
  { key: 'status', label: t('admin.proxies.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.proxies.columns.actions'), sortable: false },
])

// Protocol and status filter choices include unrestricted values, while form selector options
// restrict the values administrators may persist.
const protocolOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.proxies.allProtocols') },
  { value: 'http', label: 'HTTP', icon: 'globe' },
  { value: 'https', label: 'HTTPS', icon: 'lock' },
  { value: 'socks5', label: 'SOCKS5', icon: 'swap' },
  { value: 'socks5h', label: 'SOCKS5H', icon: 'server' },
])

// statusOptions supplies translated activation choices for filtering records, including the
// empty choice that clears filtering; dot colors match the status column badges.
const statusOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.proxies.allStatus') },
  { value: 'active', label: t('admin.providers.status.active'), dot: 'green' },
  { value: 'inactive', label: t('admin.providers.status.inactive'), dot: 'red' },
  { value: 'expired', label: t('admin.proxies.expired'), dot: 'red' },
])

// protocolSelectOptions lists supported transports for create and edit controls,
// using translated labels while retaining the API protocol values.
const protocolSelectOptions = computed(() => [
  { value: 'http', label: t('admin.proxies.protocols.http') },
  { value: 'https', label: t('admin.proxies.protocols.https') },
  { value: 'socks5', label: t('admin.proxies.protocols.socks5') },
  { value: 'socks5h', label: t('admin.proxies.protocols.socks5h') },
])

// editStatusOptions supplies the activation values administrators may save through the current
// record editor controls.
const editStatusOptions = computed(() => [
  { value: 'active', label: t('admin.providers.status.active') },
  { value: 'inactive', label: t('admin.providers.status.inactive') },
])

// proxies stores the current server page; visiblePasswordIds and copyMenuProxyId independently
// control credential visibility and per-row copy options.
const proxies = ref<Proxy[]>([])
const visiblePasswordIds = reactive(new Set<number>())
const copyMenuProxyId = ref<number | null>(null)
// loading tracks the current list request so the table can present pending data to
// administrators.
const loading = ref(false)
// searchQuery stores the editable query text used when the page requests its next filtered
// list.
const searchQuery = ref('')
// filters stores the selected list constraints, while pagination and sortState supply the
// requested page and ordering.
const filters = reactive({
  protocol: '', // empty selects all protocols
  status: '', // empty selects all statuses
})
// showMoreActionsDropdown and its element reference coordinate the batch-action menu and keep
// clicks inside that menu from dismissing it prematurely.
const showMoreActionsDropdown = ref(false)
const moreActionsDropdownRef = ref<HTMLElement | null>(null)
// pagination stores the current page, page size, and server totals used by the table controls.
const pagination = reactive({
  page: 1, // requested page number
  page_size: getPersistedPageSize(), // requested row count
  total: 0, // matching record count
  pages: 0, // matching page count
})
// sortState stores the selected server ordering so filter and page changes reuse the same sort.
const sortState = reactive({
  sort_by: 'id', // server sorting field
  sort_order: 'desc' as 'asc' | 'desc', // server sorting direction
})

// activeFilterCount counts selected protocol and status constraints for the filter button,
// separately from the current text search.
const activeFilterCount = computed(() => [filters.protocol, filters.status].filter(Boolean).length)

// resetProxyFilters clears protocol and status and reloads the first proxy page.
const resetProxyFilters = () => {
  filters.protocol = ''
  filters.status = ''
  pagination.page = 1
  loadProxies()
}

// Create, edit, import, deletion, export, provider, and quality dialog flags retain their
// visibility independently from selected records and form fields.
const showCreateModal = ref(false)
const createPasswordVisible = ref(false)
const showEditModal = ref(false)
const editPasswordVisible = ref(false)
// editPasswordDirty includes the password only after user edits. A blank edited value
// sends null, which the current backend treats as retaining the saved credential.
const editPasswordDirty = ref(false)
const showImportData = ref(false)
// showDeleteDialog controls deletion confirmation, while the selected record identifies the
// target used after confirmation.
const showDeleteDialog = ref(false)
const showBatchDeleteDialog = ref(false)
const showExportDataDialog = ref(false)
const showProvidersModal = ref(false)
// submitting records an active form persistence attempt so submission controls can display
// their busy state.
const submitting = ref(false)
// exportingData marks the credential export attempt, while per-proxy check sets and batch flags
// drive independent connection and quality progress indicators.
const exportingData = ref(false)
const testingProxyIds = ref<Set<number>>(new Set())
const qualityCheckingProxyIds = ref<Set<number>>(new Set())
const batchTesting = ref(false)
const batchQualityChecking = ref(false)
// proxyTableRef attaches swipe selection to the rendered table, and the selection controller
// retains chosen IDs independently from the current page rows.
const proxyTableRef = ref<HTMLElement | null>(null)
const {
  selectedSet: selectedProxyIds,
  selectedCount,
  allVisibleSelected,
  isSelected,
  select,
  deselect,
  clear: clearSelectedProxies,
  removeMany: removeSelectedProxies,
  toggleVisible,
  batchUpdate,
} = useTableSelection<Proxy>({
  rows: proxies,
  getId: (proxy) => proxy.id,
})
useSwipeSelect(proxyTableRef, {
  isSelected,
  select,
  deselect,
  batchUpdate,
})
// providersProxy and proxyProviders identify the association dialog target and rows;
// providersLoading marks its pending lookup for the visible panel.
const providersProxy = ref<Proxy | null>(null)
const proxyProviders = ref<ProxyProviderSummary[]>([])
const providersLoading = ref(false)
// editingProxy and deletingProxy hold operation targets, while the quality report target and
// result identify the currently displayed diagnostic report.
const editingProxy = ref<Proxy | null>(null)
const deletingProxy = ref<Proxy | null>(null)
const showQualityReportDialog = ref(false)
const qualityReportProxy = ref<Proxy | null>(null)
const qualityReport = ref<ProxyQualityCheckResult | null>(null)

// createMode selects individual or batch entry; batchInput and batchParseResult retain raw
// lines, accepted proxy fields, and invalid or duplicate counts.
const createMode = ref<'standard' | 'batch'>('standard')
const batchInput = ref('')
const batchParseResult = reactive({
  total: 0, // nonempty input count
  valid: 0, // accepted unique count
  invalid: 0, // invalid URL count
  duplicate: 0, // repeated connection count
  proxies: [] as Array<{
    protocol: ProxyProtocol // proxy transport protocol
    host: string // stored proxy host
    port: number // proxy TCP port
    username: string // proxy authentication username
    password: string // proxy authentication password
  }>, // accepted connection drafts
})

// createForm holds connection credentials, expiry date text, and replacement settings that are
// normalized into a create request after validation.
// expires_at sets the expiry date. After expiry, fallback_mode none retains provider
// bindings, direct clears them, and proxy follows backup_proxy_id to a replacement.
const createForm = reactive({
  name: '', // administrator display name
  protocol: 'http' as ProxyProtocol, // proxy transport protocol
  host: '', // proxy endpoint host
  port: 8080, // proxy TCP port
  // Whitespace is trimmed on creation; blank input sends null so the new proxy has
  // no username.
  username: '', // authentication username draft
  // Whitespace is trimmed on creation; blank input sends null so the new proxy has
  // no password.
  password: '', // authentication password draft
  // A populated date is converted from browser milliseconds to Unix seconds; blank
  // input sends null for no expiry.
  expires_at: '' as string, // date input text
  fallback_mode: 'none' as 'none' | 'proxy' | 'direct', // expiry routing choice
  // This selection is submitted only when fallback_mode is proxy; other modes send
  // null for the replacement identifier.
  backup_proxy_id: null as number | null, // replacement proxy identifier
  expiry_warn_days: 7 as number, // expiry warning days
})

// editForm holds the selected proxy settings; password dirty tracking separately determines
// whether its password field enters the update payload.
// expires_at sets the expiry date. After expiry, fallback_mode none retains provider
// bindings, direct clears them, and proxy follows backup_proxy_id to a replacement.
const editForm = reactive({
  name: '', // administrator display name
  protocol: 'http' as ProxyProtocol, // proxy transport protocol
  host: '', // proxy endpoint host
  port: 8080, // proxy TCP port
  // Whitespace is trimmed on update; blank input sends null and the current
  // backend retains the saved username.
  username: '', // authentication username draft
  // Only a dirty password is submitted; blank edited text sends null and the
  // current backend retains the saved secret.
  password: '', // authentication password draft
  status: 'active' as 'active' | 'inactive' | 'expired', // configured activation status
  // A populated date is converted from browser milliseconds to Unix seconds; blank
  // input sends null to clear expiry.
  expires_at: '' as string, // date input text
  fallback_mode: 'none' as 'none' | 'proxy' | 'direct', // expiry routing choice
  // This selection is submitted only when fallback_mode is proxy; other modes send
  // null for the replacement identifier.
  backup_proxy_id: null as number | null, // replacement proxy identifier
  expiry_warn_days: 7 as number, // expiry warning days
})

// allProxiesForBackup supplies cross-page replacement choices so backup selectors can reference
// records absent from the current proxy table page.
const allProxiesForBackup = ref<Proxy[]>([])
// loadBackupProxyOptions fetches the complete proxy list used by backup selectors.
const loadBackupProxyOptions = async () => {
  allProxiesForBackup.value = await adminAPI.proxies.getAllWithCount()
}
// backupProxyOptions returns labeled backup choices excluding an optional current proxy ID.
// Labels include the proxy name and address so administrators can distinguish records sharing a
// similar display name.
const backupProxyOptions = (excludeId?: number) =>
  allProxiesForBackup.value
    .filter((p) => p.id !== excludeId)
    .map((p) => ({ label: `${p.name} (${p.host}:${p.port})`, value: p.id }))

// abortController owns the active list read and lets the next query cancel work for the
// previous selection.
let abortController: AbortController | null = null

// isAbortError recognizes browser and HTTP-client cancellation errors by their marker fields.
const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const maybeError = error as {
    name?: string // transport exception name
    code?: string // HTTP-client error code
  }
  return maybeError.name === 'AbortError' || maybeError.code === 'ERR_CANCELED'
}

// toggleSelectRow forwards the checkbox state into the table selection store.
const toggleSelectRow = (id: number, event: Event) => {
  const target = event.target as HTMLInputElement
  if (target.checked) {
    select(id)
    return
  }
  deselect(id)
}

// toggleSelectAllVisible applies the header checkbox state to the current visible rows.
const toggleSelectAllVisible = (event: Event) => {
  const target = event.target as HTMLInputElement
  toggleVisible(target.checked)
}

// buildProxyQueryFilters constructs current proxy filters and ordering for API requests.
const buildProxyQueryFilters = () => ({
  protocol: filters.protocol || undefined, // optional transport filter
  status: (filters.status || undefined) as 'active' | 'inactive' | 'expired' | undefined, // optional activation filter
  search: searchQuery.value || undefined, // optional text query
  sort_by: sortState.sort_by, // server sorting field
  sort_order: sortState.sort_order, // server sorting direction
})

// loadProxies fetches the selected proxy page and supersedes the previous list read. It aborts
// the old controller, then accepts results only while the new controller remains current and
// un-aborted. Accepted rows update page totals and page count together.
//
// Cancellation errors are consumed so query changes do not display failure notices. Finally
// clears the loading flag and controller only when that request still owns them, keeping a
// newer read's progress state intact.
const loadProxies = async () => {
  // Abort the previous table query before registering its replacement so a completed stale
  // response cannot publish records for old filters.
  if (abortController) {
    abortController.abort()
  }
  const currentAbortController = new AbortController()
  abortController = currentAbortController
  loading.value = true
  try {
    const response = await adminAPI.proxies.list(
      pagination.page,
      pagination.page_size,
      buildProxyQueryFilters(),
      { signal: currentAbortController.signal },
    )
    if (currentAbortController.signal.aborted || abortController !== currentAbortController) {
      return
    }
    proxies.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error) {
    // Superseded list reads are expected cancellations; consume them here so a
    // new filter request does not also display an obsolete failure notification.
    if (isAbortError(error)) {
      return
    }
    appStore.showError(t('admin.proxies.failedToLoad'))
    console.error('Error loading proxies:', error)
  } finally {
    if (abortController === currentAbortController) {
      loading.value = false
      abortController = null
    }
  }
}

// searchTimeout owns the pending search debounce timer, which a subsequent input clears before
// scheduling another query.
let searchTimeout: ReturnType<typeof setTimeout>
// handleSearch debounces proxy text search and reloads from the first page. Each input clears
// the previous timer, while loadProxies cancels older list requests when the latest timer
// starts its query.
const handleSearch = () => {
  // Replace the pending delay on each input so only the latest search text
  // schedules a first-page reload after typing pauses.
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadProxies()
  }, 300)
}

// handlePageChange selects the requested proxy page and reloads its records.
const handlePageChange = (page: number) => {
  pagination.page = page
  loadProxies()
}

// handlePageSizeChange applies the selected row count and reloads from the first page.
// Resetting the page before the request prevents a previous page offset from being
// reused with the changed size.
const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadProxies()
}

// handleSort stores the selected server field and direction before reloading the first
// page. Starting from page one presents the beginning of the newly ordered result set
// to the administrator.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadProxies()
}

// closeCreateModal hides creation and restores proxy, password, and batch-import draft
// defaults.
const closeCreateModal = () => {
  showCreateModal.value = false
  createMode.value = 'standard'
  createForm.name = ''
  createForm.protocol = 'http'
  createForm.host = ''
  createForm.port = 8080
  createForm.username = ''
  createForm.password = ''
  createForm.expires_at = ''
  createForm.fallback_mode = 'none'
  createForm.backup_proxy_id = null
  createForm.expiry_warn_days = 7
  createPasswordVisible.value = false
  batchInput.value = ''
  batchParseResult.total = 0
  batchParseResult.valid = 0
  batchParseResult.invalid = 0
  batchParseResult.duplicate = 0
  batchParseResult.proxies = []
}

// handleDataImported closes the import dialog and reloads the proxy table.
const handleDataImported = () => {
  showImportData.value = false
  loadProxies()
}

// parseProxyUrl parses a supported proxy URL into connection fields or returns null. HTTP,
// HTTPS, SOCKS5, and SOCKS5H URLs require a port within the valid TCP range, with optional
// username-password credentials. The host can be a domain, IPv4 address, or bracketed IPv6
// literal.
//
// The parser removes IPv6 brackets from the stored host and lowercases the protocol.
// Credentials and host text are trimmed after matching. Invalid syntax and empty input return
// null for the batch importer to count as invalid entries.
const parseProxyUrl = (
  line: string,
): {
  protocol: ProxyProtocol // proxy transport protocol
  host: string // stored proxy host
  port: number // proxy TCP port
  username: string // proxy authentication username
  password: string // proxy authentication password
} | null => {
  const trimmed = line.trim()
  if (!trimmed) return null

  // The accepted URL transports are HTTP, HTTPS, SOCKS5, and SOCKS5H, each followed by an
  // explicit port.
  // The host alternative separates bracketed IPv6 literals from domain and IPv4 text so the
  // trailing colon identifies the port.
  const regex =
    /^(https?|socks5h?):\/\/(?:([^:@\[\]]+):([^@\[\]]+)@)?(\[[0-9a-f:.]+\]|[^:\[\]]+):(\d+)$/i
  const match = trimmed.match(regex)

  if (!match) return null

  const [, protocol, username, password, rawHost, port] = match
  const portNum = parseInt(port, 10)

  if (portNum < 1 || portNum > 65535) return null

  // Stored hosts omit IPv6 brackets because address reconstruction adds the required host-port
  // framing when building connection URLs downstream.
  const host = rawHost.replace(/^\[|\]$/g, '').trim()

  return {
    protocol: protocol.toLowerCase() as ProxyProtocol,
    host,
    port: portNum,
    username: username?.trim() || '',
    password: password?.trim() || '',
  }
}

// parseBatchInput replaces the batch draft with accepted connections and counts from
// nonempty pasted lines. Invalid URLs count separately from duplicates. Identity
// combines host, port, username, and password, so differing protocol text alone does
// not distinguish two accepted entries. The first valid occurrence supplies the
// retained transport and credentials. All counts and accepted rows are published after
// parsing completes, giving the import action a set whose size matches the accepted
// count displayed in the preview.
const parseBatchInput = () => {
  const lines = batchInput.value.split('\n').filter((l) => l.trim())
  const seen = new Set<string>()
  // proxies accumulates accepted entries for this pasted batch before publishing the
  // parsed draft and its counts together.
  const proxies: typeof batchParseResult.proxies = []
  let invalid = 0
  let duplicate = 0

  for (const line of lines) {
    const parsed = parseProxyUrl(line)
    if (!parsed) {
      invalid++
      continue
    }

    // Deduplicate by endpoint and credentials within this pasted batch so repeated connection
    // entries count as duplicates even when their protocol spelling differs.
    const key = `${parsed.host}:${parsed.port}:${parsed.username}:${parsed.password}`
    if (seen.has(key)) {
      duplicate++
      continue
    }
    seen.add(key)
    proxies.push(parsed)
  }

  batchParseResult.total = lines.length
  batchParseResult.valid = proxies.length
  batchParseResult.invalid = invalid
  batchParseResult.duplicate = duplicate
  batchParseResult.proxies = proxies
}

// handleBatchCreate submits parsed proxies when at least one valid entry exists. It reports
// created and skipped counts, closes the import draft after success, and retains the draft with
// an error notification on failure.
const handleBatchCreate = async () => {
  if (batchParseResult.valid === 0) return

  submitting.value = true
  try {
    // Await batch persistence before closing the parsed draft and reloading rows,
    // so failed requests retain the accepted input for an administrator retry.
    const result = await adminAPI.proxies.batchCreate(batchParseResult.proxies)
    const created = result.created || 0
    const skipped = result.skipped || 0

    if (created > 0) {
      appStore.showSuccess(t('admin.proxies.batchImportSuccess', { created, skipped }))
    } else {
      appStore.showInfo(t('admin.proxies.batchImportAllSkipped', { skipped }))
    }

    closeCreateModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToImport'))
    console.error('Error batch creating proxies:', error)
  } finally {
    submitting.value = false
  }
}

// handleCreateProxy validates the required proxy name, host, and port before creation. It
// converts expiry dates to Unix seconds, clears empty credentials, and includes a backup ID
// only for proxy fallback mode. Successful creation resets the dialog and reloads rows.
const handleCreateProxy = async () => {
  if (!createForm.name.trim()) {
    appStore.showError(t('admin.proxies.nameRequired'))
    return
  }
  if (!createForm.host.trim()) {
    appStore.showError(t('admin.proxies.hostRequired'))
    return
  }
  if (createForm.port < 1 || createForm.port > 65535) {
    appStore.showError(t('admin.proxies.portInvalid'))
    return
  }
  submitting.value = true
  try {
    // Await creation before resetting the dialog and refreshing rows, so the
    // submitted draft remains available if the server rejects this proxy.
    await adminAPI.proxies.create({
      name: createForm.name.trim(), // trimmed display name
      protocol: createForm.protocol, // proxy transport protocol
      host: createForm.host.trim(), // trimmed endpoint host
      port: createForm.port, // proxy TCP port
      // Blank input sends null; the create endpoint stores the new proxy without
      // an authentication username.
      username: createForm.username.trim() || null, // nullable authentication username
      // Blank input sends null; the create endpoint stores the new proxy without
      // an authentication password.
      password: createForm.password.trim() || null, // nullable authentication password
      // Convert browser milliseconds to Unix seconds for the API; blank date text
      // sends null for no expiry on this request.
      expires_at: createForm.expires_at
        ? Math.floor(new Date(createForm.expires_at).getTime() / 1000)
        : null,
      fallback_mode: createForm.fallback_mode, // expiry routing choice
      // Submit the selected replacement only for proxy fallback; all other modes
      // send null for this identifier.
      backup_proxy_id: createForm.fallback_mode === 'proxy' ? createForm.backup_proxy_id : null, // replacement proxy identifier
      expiry_warn_days: createForm.expiry_warn_days, // expiry warning days
    })
    appStore.showSuccess(t('admin.proxies.proxyCreated'))
    closeCreateModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToCreate'))
    console.error('Error creating proxy:', error)
  } finally {
    submitting.value = false
  }
}

// handleEdit hydrates a proxy draft and resets password visibility and dirty tracking. Expired
// records begin with inactive status, stored expiry becomes date text, and missing expiry
// options receive their editing defaults.
const handleEdit = (proxy: Proxy) => {
  editingProxy.value = proxy
  editForm.name = proxy.name
  editForm.protocol = proxy.protocol
  editForm.host = proxy.host
  editForm.port = proxy.port
  editForm.username = proxy.username || ''
  editForm.password = proxy.password || ''
  editForm.status = proxy.status === 'expired' ? 'inactive' : proxy.status
  editForm.expires_at = proxy.expires_at ? proxy.expires_at.slice(0, 10) : ''
  editForm.fallback_mode = proxy.fallback_mode || 'none'
  editForm.backup_proxy_id = proxy.backup_proxy_id ?? null
  editForm.expiry_warn_days = proxy.expiry_warn_days ?? 7
  editPasswordVisible.value = false
  editPasswordDirty.value = false
  showEditModal.value = true
}

// closeEditModal hides editing and clears selected proxy and password control state.
const closeEditModal = () => {
  showEditModal.value = false
  editingProxy.value = null
  editPasswordVisible.value = false
  editPasswordDirty.value = false
}

// handleUpdateProxy validates and persists the current proxy draft. It converts optional expiry
// and backup values for the API and includes password only when the user edited that field.
// A blank edited password sends null, which the backend treats as retaining the saved
// secret; untouched passwords are omitted. Empty usernames also retain saved values.
//
// After persistence succeeds, the editor closes and the list reloads. Missing selected proxy or
// invalid required fields prevents submission, and API failures retain editing state while
// displaying the server detail or translated error.
const handleUpdateProxy = async () => {
  if (!editingProxy.value) return
  if (!editForm.name.trim()) {
    appStore.showError(t('admin.proxies.nameRequired'))
    return
  }
  if (!editForm.host.trim()) {
    appStore.showError(t('admin.proxies.hostRequired'))
    return
  }
  if (editForm.port < 1 || editForm.port > 65535) {
    appStore.showError(t('admin.proxies.portInvalid'))
    return
  }

  submitting.value = true
  try {
    const updateData: any = {
      name: editForm.name.trim(), // trimmed display name
      protocol: editForm.protocol, // proxy transport protocol
      host: editForm.host.trim(), // trimmed endpoint host
      port: editForm.port, // proxy TCP port
      // Blank input sends null; the update endpoint retains the saved username for
      // that empty decoded value.
      username: editForm.username.trim() || null, // nullable authentication username
      status: editForm.status, // configured activation status
      // Convert browser milliseconds to Unix seconds for the API; blank date text
      // sends null for expiry clearing on this request.
      expires_at: editForm.expires_at
        ? Math.floor(new Date(editForm.expires_at).getTime() / 1000)
        : null,
      fallback_mode: editForm.fallback_mode, // expiry routing choice
      // Submit the selected replacement only for proxy fallback; all other modes
      // send null for this identifier.
      backup_proxy_id: editForm.fallback_mode === 'proxy' ? editForm.backup_proxy_id : null, // replacement proxy identifier
      expiry_warn_days: editForm.expiry_warn_days, // expiry warning days
    }

    // Include a password only after an edit. Blank text sends null on the wire;
    // the current backend retains its saved secret for that empty decoded value.
    if (editPasswordDirty.value) {
      updateData.password = editForm.password.trim() || null
    }

    // Await the update before closing the editor and refreshing rows, so failed
    // persistence leaves the selected record and edited values available for retry.
    await adminAPI.proxies.update(editingProxy.value.id, updateData)
    appStore.showSuccess(t('admin.proxies.proxyUpdated'))
    closeEditModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToUpdate'))
    console.error('Error updating proxy:', error)
  } finally {
    submitting.value = false
  }
}

// applyLatencyResult applies a connection result to a currently displayed proxy row. Success
// updates latency and location, while failure clears previously displayed measurements and
// location fields before storing the result message.
const applyLatencyResult = (
  proxyId: number,
  result: {
    success: boolean // connection check success flag
    latency_ms?: number // connection latency milliseconds
    message?: string // connection result message
    ip_address?: string // observed exit address
    country?: string // exit country name
    country_code?: string // exit country code
    region?: string // exit region name
    city?: string // exit city name
  },
) => {
  const target = proxies.value.find((proxy) => proxy.id === proxyId)
  if (!target) return
  if (result.success) {
    target.latency_status = 'success'
    target.latency_ms = result.latency_ms
    target.ip_address = result.ip_address
    target.country = result.country
    target.country_code = result.country_code
    target.region = result.region
    target.city = result.city
  } else {
    target.latency_status = 'failed'
    target.latency_ms = undefined
    target.ip_address = undefined
    target.country = undefined
    target.country_code = undefined
    target.region = undefined
    target.city = undefined
  }
  target.latency_message = result.message
}

// summarizeQualityStatus selects the displayed quality category from the report
// counts. Challenge takes precedence over failure, and failure takes precedence over
// warning, so a report with several kinds of adverse result receives the first
// matching category. A report with none of those positive counts is classified as
// healthy. applyQualityResult stores that category on the visible proxy row alongside
// the score, grade, summary, and check timestamp from the same report.
const summarizeQualityStatus = (result: ProxyQualityCheckResult): Proxy['quality_status'] => {
  if (result.challenge_count > 0) return 'challenge'
  if (result.failed_count > 0) return 'failed'
  if (result.warn_count > 0) return 'warn'
  return 'healthy'
}

// applyQualityResult updates the matching visible proxy with its latest quality summary.
const applyQualityResult = (proxyId: number, result: ProxyQualityCheckResult) => {
  const target = proxies.value.find((proxy) => proxy.id === proxyId)
  if (!target) return
  target.quality_status = summarizeQualityStatus(result)
  target.quality_score = result.score
  target.quality_grade = result.grade
  target.quality_summary = result.summary
  target.quality_checked = result.checked_at
}

// formatLocation joins the proxy country and city when those labels are present.
const formatLocation = (proxy: Proxy) => {
  const parts = [proxy.country, proxy.city].filter(Boolean) as string[]
  return parts.join(' · ')
}

// flagUrl builds the country flag asset URL from a lowercase country code.
const flagUrl = (code: string) => `https://unpkg.com/flag-icons/flags/4x3/${code.toLowerCase()}.svg`

// startTestingProxy publishes a new progress set containing the proxy connection-check ID.
const startTestingProxy = (proxyId: number) => {
  testingProxyIds.value = new Set([...testingProxyIds.value, proxyId])
}

// stopTestingProxy publishes a progress set with the completed connection-check ID removed.
const stopTestingProxy = (proxyId: number) => {
  const next = new Set(testingProxyIds.value)
  next.delete(proxyId)
  testingProxyIds.value = next
}

// startQualityCheckingProxy publishes a progress set containing the proxy quality-check ID.
const startQualityCheckingProxy = (proxyId: number) => {
  qualityCheckingProxyIds.value = new Set([...qualityCheckingProxyIds.value, proxyId])
}

// stopQualityCheckingProxy removes the completed proxy from the quality-check progress set.
const stopQualityCheckingProxy = (proxyId: number) => {
  const next = new Set(qualityCheckingProxyIds.value)
  next.delete(proxyId)
  qualityCheckingProxyIds.value = next
}

// runProxyTest runs a connection check and updates its visible proxy measurements. The notify
// flag controls per-proxy notifications, allowing batch callers to suppress them. API failures
// publish a failed latency result and return null; finally clears the proxy's progress marker.
const runProxyTest = async (proxyId: number, notify: boolean) => {
  startTestingProxy(proxyId)
  try {
    const result = await adminAPI.proxies.testProxy(proxyId)
    applyLatencyResult(proxyId, result)
    if (notify) {
      if (result.success) {
        const message = result.latency_ms
          ? t('admin.proxies.proxyWorkingWithLatency', { latency: result.latency_ms })
          : t('admin.proxies.proxyWorking')
        appStore.showSuccess(message)
      } else {
        appStore.showError(result.message || t('admin.proxies.proxyTestFailed'))
      }
    }
    return result
  } catch (error: any) {
    const message = error.response?.data?.detail || t('admin.proxies.failedToTest')
    // A transport exception supplies no check result, so publish failure to clear measurements
    // from an earlier successful check before returning the batch caller's null result.
    applyLatencyResult(proxyId, { success: false, message })
    if (notify) {
      appStore.showError(message)
    }
    console.error('Error testing proxy:', error)
    return null
  } finally {
    stopTestingProxy(proxyId)
  }
}

// handleTestConnection runs the selected proxy check with individual result notifications
// enabled.
const handleTestConnection = async (proxy: Proxy) => {
  await runProxyTest(proxy.id, true)
}

// handleQualityCheck fetches and displays one proxy quality report. A passing base-connectivity
// step updates latency and location, then overall quality updates the row. Errors produce a
// notification, and finally clears the quality progress marker.
const handleQualityCheck = async (proxy: Proxy) => {
  startQualityCheckingProxy(proxy.id)
  try {
    const result = await adminAPI.proxies.checkProxyQuality(proxy.id)
    qualityReportProxy.value = proxy
    qualityReport.value = result
    showQualityReportDialog.value = true

    const baseStep = result.items.find((item) => item.target === 'base_connectivity')
    if (baseStep && baseStep.status === 'pass') {
      applyLatencyResult(proxy.id, {
        success: true, // connection probe success
        latency_ms: result.base_latency_ms, // connection latency milliseconds
        message: result.summary, // displayed probe message
        ip_address: result.exit_ip, // observed exit address
        country: result.country, // exit country name
        country_code: result.country_code, // exit country code
      })
    }
    applyQualityResult(proxy.id, result)

    appStore.showSuccess(
      t('admin.proxies.qualityCheckDone', { score: result.score, grade: result.grade }),
    )
  } catch (error: any) {
    const message = error.response?.data?.detail || t('admin.proxies.qualityCheckFailed')
    appStore.showError(message)
    console.error('Error checking proxy quality:', error)
  } finally {
    stopQualityCheckingProxy(proxy.id)
  }
}

// runBatchProxyQualityChecks checks the supplied IDs with at most three workers and returns
// category counts. Each worker claims an index synchronously before awaiting its request, so
// workers process distinct positions. Results update visible rows, while missing rows still
// contribute to the aggregate counts.
//
// Challenge, failed, warning, and healthy categories are mutually selected in that order. A
// rejected request increments failure, and finally clears each proxy's progress marker. The
// returned summary waits for all workers; an empty input returns zero counts immediately.
const runBatchProxyQualityChecks = async (ids: number[]) => {
  if (ids.length === 0) return { total: 0, healthy: 0, warn: 0, challenge: 0, failed: 0 }

  // Limit simultaneous quality checks to three network requests.
  const concurrency = 3
  let index = 0
  let healthy = 0
  let warn = 0
  let challenge = 0
  let failed = 0

  // worker claims the next shared index before awaiting a quality request, giving
  // concurrently progressing workers distinct input positions. It updates visible
  // results when a matching proxy exists and counts each response using challenge,
  // failure, warning, then healthy precedence. Rejected requests increase failure
  // count while other workers continue. Each claimed proxy releases its busy marker
  // in finally, and the worker continues claiming positions until the batch is exhausted.
  const worker = async () => {
    while (index < ids.length) {
      // Claim the next index before awaiting network work so concurrently progressing workers
      // each receive a distinct position in this batch.
      const current = ids[index]
      index++
      startQualityCheckingProxy(current)
      try {
        const result = await adminAPI.proxies.checkProxyQuality(current)
        const target = proxies.value.find((proxy) => proxy.id === current)
        if (target) {
          const baseStep = result.items.find((item) => item.target === 'base_connectivity')
          if (baseStep && baseStep.status === 'pass') {
            applyLatencyResult(current, {
              success: true, // connection probe success
              latency_ms: result.base_latency_ms, // connection latency milliseconds
              message: result.summary, // displayed probe message
              ip_address: result.exit_ip, // observed exit address
              country: result.country, // exit country name
              country_code: result.country_code, // exit country code
            })
          }
        }
        applyQualityResult(current, result)
        if (result.challenge_count > 0) {
          challenge++
        } else if (result.failed_count > 0) {
          failed++
        } else if (result.warn_count > 0) {
          warn++
        } else {
          healthy++
        }
        // Count an individual rejected check as failed so other workers can continue and the
        // final summary still accounts for that proxy.
      } catch {
        failed++
      } finally {
        stopQualityCheckingProxy(current)
      }
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, ids.length) }, () => worker())
  await Promise.all(workers)
  return {
    total: ids.length, // requested check count
    healthy, // healthy report count
    warn, // warning report count
    challenge, // challenge report count
    failed, // failed check count
  }
}

// closeQualityReportDialog hides the report and clears its proxy and result references.
const closeQualityReportDialog = () => {
  showQualityReportDialog.value = false
  qualityReportProxy.value = null
  qualityReport.value = null
}

// qualityStatusClass maps a check-step status to its badge color class. Passed steps use
// success, warnings use warning, and challenge or unrecognized statuses use the danger class
// for display.
const qualityStatusClass = (status: string) => {
  if (status === 'pass') return 'badge-success'
  if (status === 'warn') return 'badge-warning'
  if (status === 'challenge') return 'badge-danger'
  return 'badge-danger'
}

// qualityStatusLabel translates the displayed check-step status. Pass, warning, and challenge
// use their matching labels, while every remaining value uses the failure label in the report
// table.
const qualityStatusLabel = (status: string) => {
  if (status === 'pass') return t('admin.proxies.qualityStatusPass')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

// Expiry presets count from today during creation and from the proxy creation date during
// editing; expires_at date text remains the draft source.
const EXPIRY_PRESETS = [7, 30, 90, 180]
// toLocalDateStr formats a Date using its local calendar year, month, and day.
const toLocalDateStr = (dt: Date): string => {
  const y = dt.getFullYear()
  const m = String(dt.getMonth() + 1).padStart(2, '0')
  const d = String(dt.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

// baseDateOrToday creates local midnight for a supplied calendar date or today. Empty text
// chooses the current date, and resetting time components lets expiry controls add days from
// their selected base.
const baseDateOrToday = (baseDateStr: string): Date => {
  const base = baseDateStr ? new Date(`${baseDateStr}T00:00:00`) : new Date()
  base.setHours(0, 0, 0, 0)
  return base
}

// addDaysToBase returns a local calendar date after the requested positive day count. Empty,
// zero, or negative counts return empty text for no expiry, while valid counts use calendar
// date arithmetic.
const addDaysToBase = (baseDateStr: string, n: number | null): string => {
  const days = Number(n)
  if (!days || days <= 0) return ''
  const dt = baseDateOrToday(baseDateStr)
  dt.setDate(dt.getDate() + days)
  return toLocalDateStr(dt)
}

// daysFromBase converts expiry date text into a rounded day count from local midnight. Missing
// target dates return null, and rounding elapsed milliseconds accommodates the nearby hour
// differences introduced by daylight-saving transitions.
const daysFromBase = (baseDateStr: string, targetDateStr: string): number | null => {
  if (!targetDateStr) return null
  const target = new Date(`${targetDateStr}T00:00:00`)
  // Round elapsed milliseconds to whole days across nearby daylight-saving hour shifts.
  return Math.round((target.getTime() - baseDateOrToday(baseDateStr).getTime()) / 86400000)
}
// Editing derives the base calendar date from created_at; an absent timestamp leaves the
// day-count helper using today as its base.
const editBaseDate = computed(() =>
  editingProxy.value?.created_at ? editingProxy.value.created_at.slice(0, 10) : '',
)
// createExpiresDays and editExpiresDays expose day-count controls backed by expires_at date
// text, using today or the saved creation date as their base.
const createExpiresDays = computed<number | null>({
  get: () => daysFromBase('', createForm.expires_at),
  set: (v) => {
    createForm.expires_at = addDaysToBase('', v)
  },
})
const editExpiresDays = computed<number | null>({
  get: () => daysFromBase(editBaseDate.value, editForm.expires_at),
  set: (v) => {
    editForm.expires_at = addDaysToBase(editBaseDate.value, v)
  },
})

// expiryLabel translates the shared proxy expiry label and its optional parameters.
const expiryLabel = (row: Proxy): string => {
  const { key, params } = proxyExpiryLabelKey(row.expires_at, row.status)
  return params ? t(key, params) : t(key)
}

// expiryBadgeClass delegates proxy expiry coloring to the shared expiry display helper.
const expiryBadgeClass = (row: Proxy): string => proxyExpiryBadgeClass(row.expires_at, row.status)

// qualityOverallClass selects a badge class for the proxy's aggregate quality status. Healthy
// and warning have their own colors, while challenge, failed, and missing statuses use danger
// styling.
const qualityOverallClass = (status?: string) => {
  if (status === 'healthy') return 'badge-success'
  if (status === 'warn') return 'badge-warning'
  if (status === 'challenge') return 'badge-danger'
  return 'badge-danger'
}

// qualityOverallLabel translates the aggregate proxy quality state. Healthy, warning, and
// challenge receive their matching labels, while missing or other states display the failure
// label in the table.
const qualityOverallLabel = (status?: string) => {
  if (status === 'healthy') return t('admin.proxies.qualityStatusHealthy')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

// qualityTargetLabel translates base connectivity and labels known vendor check targets.
// Unknown targets retain their raw value, allowing returned report items to remain visible when
// they lack a local label.
const qualityTargetLabel = (target: string) => {
  switch (target) {
    case 'base_connectivity':
      return t('admin.proxies.qualityTargetBase')
    case 'openai':
      return 'OpenAI'
    case 'anthropic':
      return 'Anthropic'
    case 'gemini':
      return 'Gemini'
    case 'grok':
      return 'Grok'
    default:
      return target
  }
}

// fetchAllProxiesForBatch reads matching proxies across pages of two hundred records. It
// retains the current filters and ordering on each query, accumulates returned rows, and
// propagates failures to the batch action.
const fetchAllProxiesForBatch = async (): Promise<Proxy[]> => {
  // Read two hundred records per background request for batch target collection;
  // this size is independent of the visible table page size.
  const pageSize = 200
  const result: Proxy[] = []
  let page = 1
  let totalPages = 1

  while (page <= totalPages) {
    const response = await adminAPI.proxies.list(page, pageSize, {
      protocol: filters.protocol || undefined, // optional transport filter
      status: filters.status as any, // optional activation filter
      search: searchQuery.value || undefined, // optional text query
      sort_by: sortState.sort_by, // server sorting field
      sort_order: sortState.sort_order, // server sorting direction
    })
    result.push(...response.items)
    totalPages = response.pages || 1
    page++
  }

  return result
}

// runBatchProxyTests runs connection checks with at most five concurrent workers. Each worker
// claims a shared input index before awaiting runProxyTest, so the supplied positions are
// processed once within this invocation. Individual notifications are disabled while each check
// still updates visible row state.
//
// The batch waits for every worker to finish, and empty input returns immediately. Per-request
// failures are handled by runProxyTest and therefore do not prevent remaining input IDs from
// being processed.
const runBatchProxyTests = async (ids: number[]) => {
  if (ids.length === 0) return
  // Limit simultaneous connection tests to five network requests.
  const concurrency = 5
  let index = 0

  // worker claims the next shared input position before awaiting runProxyTest, so
  // other workers can progress during that request without claiming the same position.
  // Each delegated check updates visible row feedback and manages its busy marker.
  // Passing false suppresses individual notifications during the batch. runProxyTest
  // handles request failures, letting this worker continue through remaining IDs until
  // no positions remain; the enclosing Promise.all waits for every worker to finish.
  const worker = async () => {
    while (index < ids.length) {
      // Claim the next index before awaiting network work so concurrently progressing workers
      // each receive a distinct position in this batch.
      const current = ids[index]
      index++
      await runProxyTest(current, false)
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, ids.length) }, () => worker())
  await Promise.all(workers)
}

// handleBatchTest checks selected proxy IDs or all proxies matching the current filters. A
// page-level busy flag suppresses repeated batch starts, empty selections report an
// informational result, and completed work refreshes the list before the flag is released.
const handleBatchTest = async () => {
  if (batchTesting.value) return

  batchTesting.value = true
  try {
    let ids: number[] = []
    if (selectedCount.value > 0) {
      ids = Array.from(selectedProxyIds.value)
    } else {
      const allProxies = await fetchAllProxiesForBatch()
      ids = allProxies.map((proxy) => proxy.id)
    }

    if (ids.length === 0) {
      appStore.showInfo(t('admin.proxies.batchTestEmpty'))
      return
    }

    await runBatchProxyTests(ids)
    appStore.showSuccess(t('admin.proxies.batchTestDone', { count: ids.length }))
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchTestFailed'))
    console.error('Error batch testing proxies:', error)
  } finally {
    batchTesting.value = false
  }
}

// handleBatchQualityCheck checks selected IDs or every currently matching proxy and displays
// aggregate categories. A pending batch suppresses repeated starts, an empty input reports an
// informational message, and failures display an error while finally releases batch state.
const handleBatchQualityCheck = async () => {
  if (batchQualityChecking.value) return

  batchQualityChecking.value = true
  try {
    let ids: number[] = []
    if (selectedCount.value > 0) {
      ids = Array.from(selectedProxyIds.value)
    } else {
      const allProxies = await fetchAllProxiesForBatch()
      ids = allProxies.map((proxy) => proxy.id)
    }

    if (ids.length === 0) {
      appStore.showInfo(t('admin.proxies.batchQualityEmpty'))
      return
    }

    const summary = await runBatchProxyQualityChecks(ids)
    appStore.showSuccess(
      t('admin.proxies.batchQualityDone', {
        count: summary.total,
        healthy: summary.healthy,
        warn: summary.warn,
        challenge: summary.challenge,
        failed: summary.failed,
      }),
    )
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchQualityFailed'))
    console.error('Error batch checking quality:', error)
  } finally {
    batchQualityChecking.value = false
  }
}

// formatExportTimestamp returns a local timestamp used to name the downloaded proxy file.
const formatExportTimestamp = () => {
  const now = new Date()
  // pad2 formats a timestamp component with at least two decimal digits.
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`
}

// handleExportData exports selected proxies or the current query through step-up verification.
// A pending export suppresses another start. Successful data becomes a JSON Blob whose
// temporary URL is clicked as a named download and then revoked.
//
// User-canceled verification produces no error notification, while blocked verification and
// other failures use their corresponding messages. Finally releases export state and closes
// confirmation regardless of the outcome, leaving selection and filters available for a later
// attempt.
const handleExportData = async () => {
  if (exportingData.value) return
  exportingData.value = true
  try {
    const dataPayload = await proxyExportStepUp.run(() =>
      adminAPI.proxies.exportData(
        selectedCount.value > 0
          ? { ids: Array.from(selectedProxyIds.value) }
          : {
              filters: buildProxyQueryFilters(),
            },
      ),
    )
    const timestamp = formatExportTimestamp()
    const filename = `tokenrouter-proxy-${timestamp}.json`
    const blob = new Blob([JSON.stringify(dataPayload, null, 2)], { type: 'application/json' })
    // The download needs a temporary browser URL; revoke it after the link click so the
    // completed export does not retain the Blob reference.
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    link.click()
    URL.revokeObjectURL(url)
    appStore.showSuccess(t('admin.proxies.dataExported'))
  } catch (error: any) {
    if (isStepUpCancelled(error)) {
      // Canceling step-up verification is a user dismissal, so leave error notifications
      // unchanged and let finally close the export confirmation.
    } else if (isStepUpBlocked(error)) {
      appStore.showError(
        stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled'),
      )
    } else {
      appStore.showError(error?.message || t('admin.proxies.dataExportFailed'))
    }
  } finally {
    exportingData.value = false
    showExportDataDialog.value = false
  }
}

// handleDelete opens deletion confirmation for a proxy with no linked providers. A positive
// provider count reports an in-use error and leaves the dialog closed, preventing that known
// invalid deletion request.
const handleDelete = (proxy: Proxy) => {
  if ((proxy.provider_count || 0) > 0) {
    appStore.showError(t('admin.proxies.deleteBlockedInUse'))
    return
  }
  deletingProxy.value = proxy
  showDeleteDialog.value = true
}

// openBatchDelete opens confirmation when at least one proxy is selected. Empty selection
// leaves the dialog closed so confirmation cannot begin a batch request with no target
// identifiers.
const openBatchDelete = () => {
  if (selectedCount.value === 0) {
    return
  }
  showBatchDeleteDialog.value = true
}

// confirmDelete removes the selected proxy and clears its selection after success. It then
// closes confirmation and reloads the table; missing selection skips work, while errors keep
// the target available for retry.
const confirmDelete = async () => {
  if (!deletingProxy.value) return

  try {
    // Await deletion before removing selection and closing confirmation, so a
    // failed request retains its target and the table can be refreshed after success.
    await adminAPI.proxies.delete(deletingProxy.value.id)
    appStore.showSuccess(t('admin.proxies.proxyDeleted'))
    showDeleteDialog.value = false
    removeSelectedProxies([deletingProxy.value.id])
    deletingProxy.value = null
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToDelete'))
    console.error('Error deleting proxy:', error)
  }
}

// confirmBatchDelete submits the current selected IDs and reports deleted or skipped counts.
// Empty selection closes confirmation immediately. Successful responses clear selection and
// reload the table, while failures retain the selection and display an error.
const confirmBatchDelete = async () => {
  const ids = Array.from(selectedProxyIds.value)
  if (ids.length === 0) {
    showBatchDeleteDialog.value = false
    return
  }

  try {
    // Wait for batch deletion before clearing selection and refreshing rows, so
    // rejected requests leave the selected targets available for another attempt.
    const result = await adminAPI.proxies.batchDelete(ids)
    const deleted = result.deleted_ids?.length || 0
    const skipped = result.skipped?.length || 0

    if (deleted > 0) {
      appStore.showSuccess(t('admin.proxies.batchDeleteDone', { deleted, skipped }))
    } else if (skipped > 0) {
      appStore.showInfo(t('admin.proxies.batchDeleteSkipped', { skipped }))
    }

    clearSelectedProxies()
    showBatchDeleteDialog.value = false
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchDeleteFailed'))
    console.error('Error batch deleting proxies:', error)
  }
}

// openProvidersModal displays the selected proxy and loads its provider associations. Previous
// rows are cleared before the request, failures display an error, and the loading flag is
// released after completion.
const openProvidersModal = async (proxy: Proxy) => {
  providersProxy.value = proxy
  proxyProviders.value = []
  providersLoading.value = true
  showProvidersModal.value = true

  try {
    proxyProviders.value = await adminAPI.proxies.getProxyProviders(proxy.id)
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.providersFailed'))
    console.error('Error loading proxy providers:', error)
  } finally {
    providersLoading.value = false
  }
}

// closeProvidersModal hides provider associations and clears their proxy and row references.
const closeProvidersModal = () => {
  showProvidersModal.value = false
  providersProxy.value = null
  proxyProviders.value = []
}

// buildAuthPart percent-encodes available proxy credentials for a copied URL. Username-only and
// password-only records retain their respective URL user-info forms, while absent credentials
// produce an empty authentication segment.
function buildAuthPart(row: any): string {
  const user = row.username ? encodeURIComponent(row.username) : ''
  const pass = row.password ? encodeURIComponent(row.password) : ''
  if (user && pass) return `${user}:${pass}@`
  if (user) return `${user}@`
  if (pass) return `:${pass}@`
  return ''
}

// buildProxyUrl combines the proxy protocol, encoded credentials, stored host, and port.
function buildProxyUrl(row: any): string {
  return `${row.protocol}://${buildAuthPart(row)}${row.host}:${row.port}`
}

// getCopyFormats returns the full URL and address copy choices for a proxy. Records with
// credentials additionally offer the URL with its protocol removed, keeping the encoded
// authentication segment available.
function getCopyFormats(row: any) {
  const hasAuth = row.username || row.password
  const fullUrl = buildProxyUrl(row)
  const formats = [{ label: fullUrl, value: fullUrl }]
  if (hasAuth) {
    const withoutProtocol = fullUrl.replace(/^[^:]+:\/\//, '')
    formats.push({ label: withoutProtocol, value: withoutProtocol })
  }
  formats.push({ label: `${row.host}:${row.port}`, value: `${row.host}:${row.port}` })
  return formats
}

// copyProxyUrl copies the assembled proxy URL and closes its format menu.
function copyProxyUrl(row: any) {
  copyToClipboard(buildProxyUrl(row), t('admin.proxies.urlCopied'))
  copyMenuProxyId.value = null
}

// toggleCopyMenu opens the requested proxy menu or closes it when already selected.
function toggleCopyMenu(id: number) {
  copyMenuProxyId.value = copyMenuProxyId.value === id ? null : id
}

// copyFormat copies the chosen proxy representation and closes the format menu.
function copyFormat(value: string) {
  copyToClipboard(value, t('admin.proxies.urlCopied'))
  copyMenuProxyId.value = null
}

// closeCopyMenu clears the proxy identifier controlling the open copy menu.
function closeCopyMenu() {
  copyMenuProxyId.value = null
}

// handleProxyClickOutside closes copy menus and hides actions when clicked outside their
// container.
function handleProxyClickOutside(event: MouseEvent) {
  const target = event.target
  closeCopyMenu()
  if (target instanceof Node && moreActionsDropdownRef.value?.contains(target)) return
  showMoreActionsDropdown.value = false
}

onMounted(() => {
  loadProxies()
  loadBackupProxyOptions()
  document.addEventListener('click', handleProxyClickOutside)
})

// Clear delayed searches, abort the current table request, and remove the document listener
// so the page releases its list and menu work.
onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
  document.removeEventListener('click', handleProxyClickOutside)
})
</script>
