<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
            <div
              class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64"
              data-filter-user-search
            >
              <Icon name="search" size="md" class="input-icon text-gray-400" />
              <input
                v-model="filterUserKeyword"
                type="text"
                :placeholder="t('admin.users.searchUsers')"
                class="input input-has-icon pr-8"
                @input="debounceSearchFilterUsers"
                @focus="showFilterUserDropdown = true"
              />
              <button
                v-if="selectedFilterUser"
                @click="clearFilterUser"
                type="button"
                class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                :title="t('common.clear')"
              >
                <Icon name="x" size="sm" :stroke-width="2" />
              </button>

              <MotionTransition name="dropdown-fade">
                <div
                  v-if="
                    showFilterUserDropdown && (filterUserResults.length > 0 || filterUserKeyword)
                  "
                  :inert="
                    !(
                      showFilterUserDropdown &&
                      (filterUserResults.length > 0 || filterUserKeyword)
                    ) || undefined
                  "
                  class="dropdown z-50 mt-1 max-h-menu-sm w-full overflow-auto py-0"
                >
                  <div
                    v-if="filterUserLoading"
                    class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400"
                  >
                    {{ t('common.loading') }}
                  </div>
                  <div
                    v-else-if="filterUserResults.length === 0 && filterUserKeyword"
                    class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400"
                  >
                    {{ t('common.noOptionsFound') }}
                  </div>
                  <button
                    v-for="user in filterUserResults"
                    :key="user.id"
                    type="button"
                    @click="selectFilterUser(user)"
                    class="dropdown-item"
                  >
                    <span class="font-medium text-gray-900 dark:text-white">{{ user.email }}</span>
                    <span class="text-gray-500 dark:text-gray-400">#{{ user.id }}</span>
                  </button>
                </div>
              </MotionTransition>
            </div>

            <FilterDropdown :active-count="activeFilterCount" @reset="resetSubscriptionFilters">
              <FilterField :label="t('admin.subscriptions.columns.status')" icon="checkCircle">
                <FilterOptions
                  v-model="filters.status"
                  :options="statusOptions"
                  @change="applyFilters"
                />
              </FilterField>
              <FilterField :label="t('admin.subscriptions.form.group')" icon="badge">
                <FilterOptions
                  v-model="filters.plan_id"
                  :options="planOptions"
                  @change="applyFilters"
                />
              </FilterField>
            </FilterDropdown>
          </div>

          <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
            <button
              @click="loadSubscriptions"
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
                  class="dropdown right-0 z-50 mt-2 w-48 origin-top-right py-0"
                >
                  <div class="p-2">
                    <div class="mb-2 border-b border-gray-200 pb-2 dark:border-dark-600">
                      <div class="px-3 py-1 text-xs font-medium text-gray-500 dark:text-gray-400">
                        {{ t('admin.subscriptions.columns.user') }}
                      </div>
                      <button
                        @click="setUserColumnMode('email')"
                        class="dropdown-item-sm justify-between rounded-control"
                      >
                        <span>{{ t('admin.users.columns.email') }}</span>
                        <Icon
                          v-if="userColumnMode === 'email'"
                          name="check"
                          size="sm"
                          class="text-primary-500"
                          :animate-on-hover="false"
                        />
                      </button>
                      <button
                        @click="setUserColumnMode('username')"
                        class="dropdown-item-sm justify-between rounded-control"
                      >
                        <span>{{ t('admin.users.columns.username') }}</span>
                        <Icon
                          v-if="userColumnMode === 'username'"
                          name="check"
                          size="sm"
                          class="text-primary-500"
                          :animate-on-hover="false"
                        />
                      </button>
                    </div>

                    <button
                      v-for="col in toggleableColumns"
                      :key="col.key"
                      @click="toggleColumn(col.key)"
                      class="dropdown-item-sm justify-between rounded-control"
                    >
                      <span>{{ col.label }}</span>
                      <Icon
                        v-if="isColumnVisible(col.key)"
                        name="check"
                        size="sm"
                        class="text-primary-500"
                        :animate-on-hover="false"
                      />
                    </button>
                  </div>
                </div>
              </MotionTransition>
            </div>
            <button
              @click="showGuideModal = true"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('admin.subscriptions.guide.showGuide')"
            >
              <Icon name="questionCircle" size="sm" />
            </button>
            <button @click="showAssignModal = true" class="btn btn-primary whitespace-nowrap">
              <Icon name="plus" size="sm" class="mr-2" />
              {{ t('admin.subscriptions.assignSubscription') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-subscriptions-column-order"
          :columns="columns"
          :data="subscriptions"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-user="{ row }">
            <div class="flex items-center gap-2">
              <UserAvatar
                :avatar-url="row.user?.avatar_url || ''"
                :user-id="row.user_id"
                :alt="row.user?.email || ''"
                size-class="h-8 w-8"
              />

              <!-- The 32-pixel line matches the avatar height while the text baseline remains available for mobile-card label alignment. -->
              <span class="self-baseline font-medium leading-8 text-gray-900 dark:text-white">
                {{
                  userColumnMode === 'email'
                    ? row.user?.email || t('admin.redeem.userPrefix', { id: row.user_id })
                    : row.user?.username || '-'
                }}
              </span>
            </div>
          </template>

          <template #cell-group="{ row }">
            <span v-if="row.plan" class="text-sm font-medium text-gray-900 dark:text-white">
              {{ row.plan.name }}
            </span>
            <span v-else-if="row.plan_id" class="text-sm font-medium text-gray-900 dark:text-white">
              {{ `Plan #${row.plan_id}` }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-usage="{ row }">
            <div class="min-w-[280px] space-y-2">
              <div v-if="row.daily_limit_usd" class="usage-row">
                <div class="flex items-center gap-2">
                  <span class="usage-label">{{ t('admin.subscriptions.daily') }}</span>
                  <div class="h-1.5 flex-1 rounded-full bg-gray-200 dark:bg-dark-600">
                    <div
                      class="h-1.5 rounded-full transition-[width,background-color]"
                      :class="getProgressClass(row.daily_usage_usd, row.daily_limit_usd)"
                      :style="{
                        width: getProgressWidth(row.daily_usage_usd, row.daily_limit_usd),
                      }"
                    ></div>
                  </div>
                  <span class="usage-amount">
                    {{ formatSubscriptionBalance(row.daily_usage_usd) }}
                    <span class="text-gray-400">/</span>
                    {{ formatSubscriptionBalance(row.daily_limit_usd) }}
                  </span>
                </div>
                <div class="reset-info" v-if="row.daily_window_start">
                  <Icon name="clock" size="xs" class="h-3 w-3" />
                  <span>{{ formatDailyUsageWindow(row) }}</span>
                </div>
              </div>

              <div v-if="row.weekly_limit_usd" class="usage-row">
                <div class="flex items-center gap-2">
                  <span class="usage-label">{{ t('admin.subscriptions.weekly') }}</span>
                  <div class="h-1.5 flex-1 rounded-full bg-gray-200 dark:bg-dark-600">
                    <div
                      class="h-1.5 rounded-full transition-[width,background-color]"
                      :class="getProgressClass(row.weekly_usage_usd, row.weekly_limit_usd)"
                      :style="{
                        width: getProgressWidth(row.weekly_usage_usd, row.weekly_limit_usd),
                      }"
                    ></div>
                  </div>
                  <span class="usage-amount">
                    {{ formatSubscriptionBalance(row.weekly_usage_usd) }}
                    <span class="text-gray-400">/</span>
                    {{ formatSubscriptionBalance(row.weekly_limit_usd) }}
                  </span>
                </div>
                <div class="reset-info" v-if="row.weekly_window_start">
                  <Icon name="clock" size="xs" class="h-3 w-3" />
                  <span>{{ formatUsageWindow(row, row.weekly_window_start, 'weekly') }}</span>
                </div>
              </div>

              <div v-if="row.monthly_limit_usd" class="usage-row">
                <div class="flex items-center gap-2">
                  <span class="usage-label">{{ t('admin.subscriptions.monthly') }}</span>
                  <div class="h-1.5 flex-1 rounded-full bg-gray-200 dark:bg-dark-600">
                    <div
                      class="h-1.5 rounded-full transition-[width,background-color]"
                      :class="getProgressClass(row.monthly_usage_usd, row.monthly_limit_usd)"
                      :style="{
                        width: getProgressWidth(row.monthly_usage_usd, row.monthly_limit_usd),
                      }"
                    ></div>
                  </div>
                  <span class="usage-amount">
                    {{ formatSubscriptionBalance(row.monthly_usage_usd) }}
                    <span class="text-gray-400">/</span>
                    {{ formatSubscriptionBalance(row.monthly_limit_usd) }}
                  </span>
                </div>
                <div class="reset-info" v-if="row.monthly_window_start">
                  <Icon name="clock" size="xs" class="h-3 w-3" />
                  <span>{{ formatUsageWindow(row, row.monthly_window_start, 'monthly') }}</span>
                </div>
              </div>

              <div
                v-if="!row.daily_limit_usd && !row.weekly_limit_usd && !row.monthly_limit_usd"
                class="flex items-center gap-2 rounded-control bg-gradient-to-r from-emerald-50 to-teal-50 px-3 py-2 dark:from-emerald-900/20 dark:to-teal-900/20"
              >
                <span class="text-lg text-emerald-600 dark:text-emerald-400">∞</span>
                <span class="text-xs font-medium text-emerald-700 dark:text-emerald-300">
                  {{ t('admin.subscriptions.unlimited') }}
                </span>
              </div>
            </div>
          </template>

          <template #cell-expires_at="{ value }">
            <div v-if="value">
              <span
                class="text-sm"
                :class="
                  isExpiringSoon(value)
                    ? 'text-orange-600 dark:text-orange-400'
                    : 'text-gray-700 dark:text-gray-300'
                "
              >
                {{ formatDateTimeToMinute(value) }}
              </span>
              <template
                v-for="remainingExpiry in [formatRemainingExpiry(value)]"
                :key="remainingExpiry ?? 'expired'"
              >
                <div v-if="remainingExpiry" class="text-xs text-gray-500">
                  {{ remainingExpiry }}
                </div>
              </template>
            </div>
            <span v-else class="text-sm text-gray-500">{{
              t('admin.subscriptions.noExpiration')
            }}</span>
          </template>

          <template #cell-status="{ value }">
            <span
              :class="[
                'badge',
                value === 'active'
                  ? 'badge-success'
                  : value === 'pending'
                    ? 'badge-primary'
                    : value === 'expired'
                      ? 'badge-warning'
                      : value === 'suspended'
                        ? 'badge-danger'
                        : 'badge-gray',
              ]"
            >
              {{ t(`admin.subscriptions.status.${value}`) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                v-if="row.status === 'active' || row.status === 'expired'"
                @click="handleExtend(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 dark:hover:bg-primary-500/8 dark:hover:text-primary-500"
              >
                <Icon name="calendar" size="sm" />
                <span class="text-xs">{{ t('admin.subscriptions.adjust') }}</span>
              </button>
              <button
                v-if="row.status === 'active'"
                @click="handleResetQuota(row)"
                :disabled="resettingQuota && resettingSubscription?.id === row.id"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-orange-50 hover:text-orange-600 dark:hover:bg-orange-900/20 dark:hover:text-orange-400 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Icon name="refresh" size="sm" />
                <span class="text-xs">{{ t('admin.subscriptions.resetQuota') }}</span>
              </button>
              <button
                v-if="row.status === 'active' || row.status === 'pending'"
                @click="handleRevoke(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <Icon name="ban" size="sm" />
                <span class="text-xs">{{ revokeActionText(row) }}</span>
              </button>
              <button
                v-if="row.status === 'revoked'"
                @click="handleRestore(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-green-50 hover:text-green-600 dark:hover:bg-green-900/20 dark:hover:text-green-400"
              >
                <Icon name="refresh" size="sm" />
                <span class="text-xs">{{ t('admin.subscriptions.restore') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.subscriptions.noSubscriptionsYet')"
              :description="t('admin.subscriptions.assignFirstSubscription')"
              :action-text="t('admin.subscriptions.assignSubscription')"
              @action="showAssignModal = true"
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

    <BaseDialog
      :show="showAssignModal"
      :title="t('admin.subscriptions.assignSubscription')"
      width="normal"
      @close="closeAssignModal"
    >
      <form
        id="assign-subscription-form"
        @submit.prevent="handleAssignSubscription"
        class="space-y-5"
      >
        <div>
          <label class="input-label">{{ t('admin.subscriptions.form.user') }}</label>
          <div class="relative" data-assign-user-search>
            <input
              v-model="userSearchKeyword"
              type="text"
              class="input pr-8"
              :placeholder="t('admin.usage.searchUserPlaceholder')"
              @input="debounceSearchUsers"
              @focus="showUserDropdown = true"
            />
            <button
              v-if="selectedUser"
              @click="clearUserSelection"
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
            >
              <Icon name="x" size="sm" :stroke-width="2" />
            </button>

            <MotionTransition name="dropdown-fade">
              <div
                v-if="showUserDropdown && (userSearchResults.length > 0 || userSearchKeyword)"
                :inert="
                  !(showUserDropdown && (userSearchResults.length > 0 || userSearchKeyword)) ||
                  undefined
                "
                class="dropdown z-50 mt-1 max-h-menu-sm w-full overflow-auto py-0"
              >
                <div
                  v-if="userSearchLoading"
                  class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400"
                >
                  {{ t('common.loading') }}
                </div>
                <div
                  v-else-if="userSearchResults.length === 0 && userSearchKeyword"
                  class="px-4 py-3 text-sm text-gray-500 dark:text-gray-400"
                >
                  {{ t('common.noOptionsFound') }}
                </div>
                <button
                  v-for="user in userSearchResults"
                  :key="user.id"
                  type="button"
                  @click="selectUser(user)"
                  class="dropdown-item"
                >
                  <span class="font-medium text-gray-900 dark:text-white">{{ user.email }}</span>
                  <span class="text-gray-500 dark:text-gray-400">#{{ user.id }}</span>
                </button>
              </div>
            </MotionTransition>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('payment.admin.planName') }}</label>
          <Select
            v-model="assignForm.plan_id"
            :options="subscriptionPlanOptions"
            :placeholder="t('admin.announcements.form.selectPackages')"
          />
          <p class="input-hint">{{ t('admin.announcements.form.selectPackages') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.subscriptions.form.validityDays') }}</label>
          <input v-model.number="assignForm.validity_days" type="number" min="1" class="input" />
          <p class="input-hint">{{ t('admin.subscriptions.validityHint') }}</p>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeAssignModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="assign-subscription-form"
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
            {{ submitting ? t('admin.subscriptions.assigning') : t('admin.subscriptions.assign') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showExtendModal"
      :title="t('admin.subscriptions.adjustSubscription')"
      width="narrow"
      @close="closeExtendModal"
    >
      <form
        v-if="extendingSubscription"
        id="extend-subscription-form"
        @submit.prevent="handleExtendSubscription"
        class="space-y-5"
      >
        <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-700">
          <p class="text-sm text-gray-600 dark:text-gray-400">
            {{ t('admin.subscriptions.adjustingFor') }}
            <span class="font-medium text-gray-900 dark:text-white">{{
              extendingSubscription.user?.email
            }}</span>
          </p>
          <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">
            {{ t('admin.subscriptions.currentExpiration') }}:
            <span class="font-medium text-gray-900 dark:text-white">
              {{
                extendingSubscription.expires_at
                  ? formatDateTimeToMinute(extendingSubscription.expires_at)
                  : t('admin.subscriptions.noExpiration')
              }}
            </span>
          </p>
          <p
            v-if="extendingSubscription.expires_at"
            class="mt-1 text-sm text-gray-600 dark:text-gray-400"
          >
            {{ t('admin.subscriptions.remainingDays') }}:
            <span class="font-medium text-gray-900 dark:text-white">
              {{ getDaysRemaining(extendingSubscription.expires_at) ?? 0 }}
            </span>
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.subscriptions.form.targetValidityDays') }}</label>
          <div class="flex items-center gap-2">
            <input
              v-model.number="extendForm.days"
              type="number"
              min="1"
              required
              class="input text-center"
              :placeholder="t('admin.subscriptions.adjustDaysPlaceholder')"
            />
          </div>
          <p class="input-hint">{{ t('admin.subscriptions.adjustHint') }}</p>
        </div>
      </form>
      <template #footer>
        <div v-if="extendingSubscription" class="flex justify-end gap-3">
          <button @click="closeExtendModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="extend-subscription-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            {{ submitting ? t('admin.subscriptions.adjusting') : t('admin.subscriptions.adjust') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showRevokeDialog"
      :title="revokeDialogTitle"
      :message="revokeDialogMessage"
      :confirm-text="revokeDialogConfirmText"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmRevoke"
      @cancel="showRevokeDialog = false"
    />

    <ConfirmDialog
      :show="showRestoreDialog"
      :title="t('admin.subscriptions.restoreSubscription')"
      :message="
        t('admin.subscriptions.restoreConfirm', { user: restoringSubscription?.user?.email })
      "
      :confirm-text="t('admin.subscriptions.restore')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmRestore"
      @cancel="showRestoreDialog = false"
    />

    <ConfirmDialog
      :show="showResetQuotaConfirm"
      :title="t('admin.subscriptions.resetQuotaTitle')"
      :message="
        t('admin.subscriptions.resetQuotaConfirm', { user: resettingSubscription?.user?.email })
      "
      :confirm-text="t('admin.subscriptions.resetQuota')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmResetQuota"
      @cancel="showResetQuotaConfirm = false"
    />

    <teleport to="body">
      <MotionTransition name="modal">
        <div
          v-if="showGuideModal"
          class="fixed inset-0 z-50 flex items-center justify-center p-4"
          @mousedown.self="showGuideModal = false"
        >
          <div class="fixed inset-0 bg-[var(--overlay-bg)]" @click="showGuideModal = false"></div>
          <div
            class="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-surface bg-white p-6 shadow-2xl dark:bg-dark-900 sm:rounded-dialog"
          >
            <button
              type="button"
              class="absolute right-4 top-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
              @click="showGuideModal = false"
            >
              <Icon name="x" size="sm" />
            </button>

            <h2 class="mb-4 text-lg font-bold text-gray-900 dark:text-white">
              {{ t('admin.subscriptions.guide.title') }}
            </h2>
            <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.subscriptions.guide.subtitle') }}
            </p>

            <div class="mb-5">
              <h3
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
              >
                <span
                  class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                  >1</span
                >
                {{ t('admin.subscriptions.guide.step1.title') }}
              </h3>
              <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{{ t('admin.subscriptions.guide.step1.line1') }}</li>
                <li>{{ t('admin.subscriptions.guide.step1.line2') }}</li>
                <li>{{ t('admin.subscriptions.guide.step1.line3') }}</li>
              </ol>
              <div class="ml-8 mt-2">
                <router-link
                  to="/admin/groups"
                  @click="showGuideModal = false"
                  class="inline-flex items-center gap-1 text-sm font-medium text-link"
                >
                  {{ t('admin.subscriptions.guide.step1.link') }}
                  <Icon name="arrowRight" size="xs" />
                </router-link>
              </div>
            </div>

            <div class="mb-5">
              <h3
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
              >
                <span
                  class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                  >2</span
                >
                {{ t('admin.subscriptions.guide.step2.title') }}
              </h3>
              <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{{ t('admin.subscriptions.guide.step2.line1') }}</li>
                <li>{{ t('admin.subscriptions.guide.step2.line2') }}</li>
                <li>{{ t('admin.subscriptions.guide.step2.line3') }}</li>
              </ol>
            </div>

            <div class="mb-5">
              <h3
                class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
              >
                <span
                  class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                  >3</span
                >
                {{ t('admin.subscriptions.guide.step3.title') }}
              </h3>
              <div
                class="ml-8 overflow-hidden rounded-control border border-gray-200 dark:border-dark-600"
              >
                <table class="w-full text-sm">
                  <tbody>
                    <tr
                      v-for="(row, i) in guideActionRows"
                      :key="i"
                      class="border-b border-gray-100 dark:border-dark-700 last:border-0"
                    >
                      <td
                        class="whitespace-nowrap bg-gray-50 px-3 py-2 font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300"
                      >
                        {{ row.action }}
                      </td>
                      <td class="px-3 py-2 text-gray-600 dark:text-gray-400">{{ row.desc }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <div
              class="rounded-control bg-blue-50 p-3 text-xs text-blue-700 dark:bg-blue-900/20 dark:text-blue-300"
            >
              {{ t('admin.subscriptions.guide.tip') }}
            </div>

            <div class="mt-4 text-right">
              <button type="button" class="btn btn-primary btn-sm" @click="showGuideModal = false">
                {{ t('common.close') }}
              </button>
            </div>
          </div>
        </div>
      </MotionTransition>
    </teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { UserSubscription } from '@/api/subscriptions.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import type { SimpleUser } from '@/api/admin/usage'
import type { Column } from '@/components/common/table/types'
import { formatDateTimeToMinute } from '@/utils/format'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import type { FilterOption } from '@/components/common/filterPanel'
import Icon from '@/components/icons/Icon.vue'
import {
  getRemainingDurationParts,
  getRemainingExpiryDuration,
  isOneTimeDailyQuota,
  type RemainingDurationParts,
} from '@/utils/subscriptionQuota'

// SubscriptionsView.vue lists user subscriptions and supports assignment, validity
// changes, quota reset, revocation, and restoration. User selection belongs either to
// the toolbar filter or the assignment dialog, with separate search drafts and debounce
// timers. Column visibility and user-label preferences are stored in browser storage.
//
// Quota labels combine subscription expiry with daily, weekly, or fixed thirty-day
// window lengths. When another complete window cannot fit before expiry, the display
// shows quota ending at subscription expiry. One-time daily quotas use expiry directly.
// List replacement checks the current request controller, while the separate plan and
// user-search requests manage their own results and loading flags.

// Translations, application messages, and balance formatting supply labels and
// operation feedback across subscription management controls.
const { t } = useI18n()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

// PlanOption describes a subscription choice with its numeric identifier and optional
// description.
interface PlanOption {
  value: number // selected plan identifier
  label: string // displayed option label
  description: string | null // optional plan description
  // Additional option metadata accepted by the shared select component.
  [key: string]: unknown
}

// showGuideModal controls the explanatory dialog, and guideActionRows supplies
// translated descriptions of adjustment, quota reset, and revocation.
const showGuideModal = ref(false)

// guideActionRows derives administrator action descriptions from the current locale for
// the subscription management guide dialog.
const guideActionRows = computed(() => [
  {
    action: t('admin.subscriptions.guide.actions.adjust'),
    desc: t('admin.subscriptions.guide.actions.adjustDesc'),
  },
  {
    action: t('admin.subscriptions.guide.actions.resetQuota'),
    desc: t('admin.subscriptions.guide.actions.resetQuotaDesc'),
  },
  {
    action: t('admin.subscriptions.guide.actions.revoke'),
    desc: t('admin.subscriptions.guide.actions.revokeDesc'),
  },
])

// formatSubscriptionBalance renders subscription usage with two fractional balance-unit
// digits.
const formatSubscriptionBalance = (value: number | null | undefined): string =>
  formatBalanceAmount(value, { fractionDigits: 2 })

// isPendingSubscription reports whether a selected subscription is awaiting activation.
const isPendingSubscription = (subscription: UserSubscription | null): boolean =>
  subscription?.status === 'pending'

// revokeActionText chooses cancellation wording for pending subscriptions and
// revocation otherwise.
const revokeActionText = (subscription: UserSubscription): string =>
  isPendingSubscription(subscription)
    ? t('admin.subscriptions.cancelPending')
    : t('admin.subscriptions.revoke')

// Revocation dialog labels derive from the selected subscription so pending items
// consistently use cancellation language throughout confirmation.
const revokeDialogTitle = computed(() =>
  isPendingSubscription(revokingSubscription.value)
    ? t('admin.subscriptions.cancelPendingSubscription')
    : t('admin.subscriptions.revokeSubscription'),
)

const revokeDialogMessage = computed(() => {
  const key = isPendingSubscription(revokingSubscription.value)
    ? 'admin.subscriptions.cancelPendingConfirm'
    : 'admin.subscriptions.revokeConfirm'
  return t(key, { user: revokingSubscription.value?.user?.email })
})

const revokeDialogConfirmText = computed(() =>
  isPendingSubscription(revokingSubscription.value)
    ? t('admin.subscriptions.cancelPending')
    : t('admin.subscriptions.revoke'),
)

// The selected user identity label is persisted under USER_COLUMN_MODE_KEY and starts
// with email when no preference is restored.
const userColumnMode = ref<'email' | 'username'>('email')
const USER_COLUMN_MODE_KEY = 'subscription-user-column-mode'

// loadUserColumnMode restores an accepted email or username preference from browser
// storage. Missing, invalid, or unreadable values leave the current default in place,
// and storage failures are logged for diagnosis.
const loadUserColumnMode = () => {
  try {
    const saved = localStorage.getItem(USER_COLUMN_MODE_KEY)
    if (saved === 'email' || saved === 'username') {
      userColumnMode.value = saved
    }
  } catch (e) {
    // Keep the current email/username mode when storage is unreadable so a browser
    // storage restriction does not prevent table rendering.
    console.error('Failed to load user column mode:', e)
  }
}

// saveUserColumnMode persists the currently selected user label mode in browser
// storage. Storage failures are logged while the in-memory preference remains active
// for rendering the table during the current session.
const saveUserColumnMode = () => {
  try {
    localStorage.setItem(USER_COLUMN_MODE_KEY, userColumnMode.value)
  } catch (e) {
    // Keep the selected display mode in memory when storage fails so the current
    // session can continue using the chosen label.
    console.error('Failed to save user column mode:', e)
  }
}

// setUserColumnMode updates the displayed user identity and persists the preference.
const setUserColumnMode = (mode: 'email' | 'username') => {
  userColumnMode.value = mode
  saveUserColumnMode()
}

// Column definitions include user identity and actions, which remain visible while
// other subscription columns can be hidden.
const allColumns = computed<Column[]>(() => [
  {
    key: 'user',
    label:
      userColumnMode.value === 'email'
        ? t('admin.subscriptions.columns.user')
        : t('admin.users.columns.username'),
    sortable: false,
  },
  { key: 'group', label: t('payment.admin.planName'), sortable: false },
  { key: 'usage', label: t('admin.subscriptions.columns.usage'), sortable: false },
  { key: 'expires_at', label: t('admin.subscriptions.columns.expires'), sortable: true },
  { key: 'status', label: t('admin.subscriptions.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.subscriptions.columns.actions'), sortable: false },
])

// toggleableColumns excludes the fixed user and action columns from the visibility
// controls shown in the settings menu.
const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => col.key !== 'user' && col.key !== 'actions'),
)

// Hidden keys and their default set control optional column visibility and are
// persisted under the subscription-specific storage key.
const hiddenColumns = reactive<Set<string>>(new Set())

const DEFAULT_HIDDEN_COLUMNS: string[] = []

const HIDDEN_COLUMNS_KEY = 'subscription-hidden-columns'

// loadSavedColumns adds stored hidden keys or the default hidden columns. Storage or
// parsing failures are logged and apply defaults to the current set so the table can
// remain usable.
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed.forEach((key) => hiddenColumns.add(key))
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
    }
  } catch (e) {
    // Apply the default hidden-column set after storage or JSON failure so
    // optional-column settings cannot block rendering the subscription table.
    console.error('Failed to load saved columns:', e)
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

// saveColumnsToStorage serializes the hidden-column set into browser storage for later
// visits. If writing fails, the current visibility choices remain in memory and the
// failure is logged for diagnosis.
const saveColumnsToStorage = () => {
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
  } catch (e) {
    // Storage failure leaves the in-memory hidden set active, so report the failure
    // to the console while retaining current column choices.
    console.error('Failed to save columns:', e)
  }
}

// toggleColumn toggles one hidden key and persists the resulting column preferences.
const toggleColumn = (key: string) => {
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
}

// isColumnVisible reports whether the hidden-column set excludes the requested key.
const isColumnVisible = (key: string) => !hiddenColumns.has(key)

// columns applies the hidden-key set while retaining the user and actions columns for
// every subscription row.
const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (col) => col.key === 'user' || col.key === 'actions' || !hiddenColumns.has(col.key),
  ),
)

// Column-settings visibility and its DOM reference support closing the menu when a
// document click occurs outside it.
const showColumnDropdown = ref(false)
const columnDropdownRef = ref<HTMLElement | null>(null)

// Status choices include an unfiltered option and the supported subscription lifecycle
// values used by server listing.
const statusOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.subscriptions.allStatus') },
  { value: 'active', label: t('admin.subscriptions.status.active'), dot: 'green' },
  { value: 'pending', label: t('admin.subscriptions.status.pending'), dot: 'brand' },
  { value: 'expired', label: t('admin.subscriptions.status.expired'), dot: 'amber' },
  { value: 'suspended', label: t('admin.subscriptions.status.suspended'), dot: 'red' },
  { value: 'revoked', label: t('admin.subscriptions.status.revoked'), dot: 'gray' },
])

// Subscription rows and plan choices load separately; abortController identifies the
// replaceable main-list request and its loading state.
const subscriptions = ref<UserSubscription[]>([])
const plans = ref<SubscriptionPlan[]>([])
const loading = ref(false)
let abortController: AbortController | null = null

// Toolbar user-search drafts, results, loading, selection, and timer remain independent
// from the assignment dialog user search.
const filterUserKeyword = ref('')
const filterUserResults = ref<SimpleUser[]>([])
const filterUserLoading = ref(false)
const showFilterUserDropdown = ref(false)
const selectedFilterUser = ref<SimpleUser | null>(null)
let filterUserSearchTimeout: ReturnType<typeof setTimeout> | null = null

// Assignment user-search drafts and suggestions populate assignForm.user_id after
// selection, with a dedicated debounce timer for input changes.
const userSearchKeyword = ref('')
const userSearchResults = ref<SimpleUser[]>([])
const userSearchLoading = ref(false)
const showUserDropdown = ref(false)
const selectedUser = ref<SimpleUser | null>(null)
let userSearchTimeout: ReturnType<typeof setTimeout> | null = null

// List conditions default to active status; empty plan/status and a null user
// identifier represent unselected filters.
const filters = reactive({
  status: 'active', // configured lifecycle status
  plan_id: '', // selected plan identifier
  user_id: null as number | null, // selected user identifier
})

// activeFilterCount counts the status and plan panel conditions while the independent
// toolbar user selection remains separate.
const activeFilterCount = computed(() => [filters.status, filters.plan_id].filter(Boolean).length)

// resetSubscriptionFilters clears status and plan filters and reloads page one. The
// separately selected toolbar user remains active, so panel reset continues to restrict
// results to that user when selected.
const resetSubscriptionFilters = () => {
  filters.status = ''
  filters.plan_id = ''
  applyFilters()
}

// Server sort and pagination state travel with every subscription list request and
// start with newest creation time.
const sortState = reactive({
  sort_by: 'created_at', // server sorting field
  sort_order: 'desc' as 'asc' | 'desc', // server sorting direction
})

const pagination = reactive({
  page: 1, // requested page number
  page_size: getPersistedPageSize(), // requested row count
  total: 0, // matching row count
  pages: 0, // matching page count
})

// Dialog flags and selected subscription records separate assignment, validity
// adjustment, revocation, restoration, and quota-reset operations.
const showAssignModal = ref(false)
const showExtendModal = ref(false)
const showRevokeDialog = ref(false)
const showRestoreDialog = ref(false)
const showResetQuotaConfirm = ref(false)
const submitting = ref(false)
const resettingSubscription = ref<UserSubscription | null>(null)
const resettingQuota = ref(false)
const extendingSubscription = ref<UserSubscription | null>(null)
const revokingSubscription = ref<UserSubscription | null>(null)
const restoringSubscription = ref<UserSubscription | null>(null)

// Assignment requires user and plan identifiers plus validity days, while the
// adjustment draft contains its requested duration.
const assignForm = reactive({
  user_id: null as number | null, // selected user identifier
  plan_id: null as number | null, // selected plan identifier
  validity_days: 30, // assigned validity days
})

const extendForm = reactive({
  days: 30, // requested validity days
})

// Plan-filter options use string identifiers, and subscriptionPlanOptions supplies
// numeric identifiers for the assignment control and its descriptions.
const planOptions = computed(() => [
  { value: '', label: t('admin.announcements.form.selectPackages') },
  ...plans.value.map((plan) => ({ value: plan.id.toString(), label: plan.name })),
])

const subscriptionPlanOptions = computed<PlanOption[]>(() =>
  plans.value.map((plan) => ({
    value: plan.id,
    label: plan.name,
    description: plan.description,
  })),
)

// applyFilters requests the first subscription page using the current conditions.
const applyFilters = () => {
  pagination.page = 1
  loadSubscriptions()
}

// loadSubscriptions requests matching subscriptions after aborting the previous list
// load. A response publishes rows and pagination only if its signal is active and its
// controller remains current. Cancellation errors are suppressed, while other request
// failures display an application error. Cleanup releases loading only for the current
// controller, so a completed older request cannot clear the indicator belonging to a
// newer reload.
const loadSubscriptions = async () => {
  // Cancel the previous list request before installing a new controller so changed
  // filters can supersede in-flight subscription results.
  if (abortController) {
    abortController.abort()
  }
  const requestController = new AbortController()
  abortController = requestController
  const { signal } = requestController

  loading.value = true
  try {
    const response = await adminAPI.subscriptions.list(
      pagination.page,
      pagination.page_size,
      {
        status: (filters.status as any) || undefined,
        plan_id: filters.plan_id ? parseInt(filters.plan_id) : undefined,
        user_id: filters.user_id || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      {
        signal,
      },
    )
    // Publish rows only while this request still owns the list, because a later
    // reload may have replaced its controller.
    if (signal.aborted || abortController !== requestController) return
    subscriptions.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error: any) {
    // Ignore cancellation raised by replacement requests so normal filter changes do
    // not display a subscription-loading error to the administrator.
    if (signal.aborted || error?.name === 'AbortError' || error?.code === 'ERR_CANCELED') {
      return
    }
    appStore.showError(t('admin.subscriptions.failedToLoad'))
    console.error('Error loading subscriptions:', error)
  } finally {
    // The current controller owns loading cleanup; earlier finalizers leave the
    // replacement request visible as pending until it settles.
    if (abortController === requestController) {
      loading.value = false
      abortController = null
    }
  }
}

// loadPlans populates subscription plan choices from the payment administration
// endpoint. Failures are logged and retain current choices, allowing the independently
// loaded subscription table to continue displaying its rows.
const loadPlans = async () => {
  try {
    const response = await adminAPI.payment.getPlans()
    plans.value = response.data || []
  } catch (error) {
    // A plan lookup failure leaves the current options available and permits the
    // independently fetched subscription list to remain usable.
    console.error('Error loading plans:', error)
  }
}

// debounceSearchFilterUsers replaces the pending toolbar-user lookup with a delayed
// search. The component owns this timer and clears it during unmount alongside the
// independent assignment-user lookup timer when present.
const debounceSearchFilterUsers = () => {
  if (filterUserSearchTimeout) {
    clearTimeout(filterUserSearchTimeout)
  }
  filterUserSearchTimeout = setTimeout(searchFilterUsers, 300)
}

// searchFilterUsers resolves the trimmed toolbar keyword into user suggestions. Editing
// away from a selected email clears its user filter and reloads subscriptions; empty
// input or request failure clears the suggestion list.
const searchFilterUsers = async () => {
  const keyword = filterUserKeyword.value.trim()

  // Editing the selected email removes the stored user identifier so subsequent list
  // queries cannot silently retain a different identity than the search text.
  if (selectedFilterUser.value && keyword !== selectedFilterUser.value.email) {
    selectedFilterUser.value = null
    filters.user_id = null
    applyFilters()
  }

  if (!keyword) {
    filterUserResults.value = []
    return
  }

  filterUserLoading.value = true
  try {
    filterUserResults.value = await adminAPI.usage.searchUsers(keyword)
  } catch (error) {
    // Failed user searches clear suggestions so stale results cannot be presented as
    // matches for the newly entered search keyword.
    console.error('Failed to search users:', error)
    filterUserResults.value = []
  } finally {
    filterUserLoading.value = false
  }
}

// selectFilterUser stores a suggested user's identifier and email in the toolbar
// filter. It hides suggestions and requests page one so the displayed records
// correspond to the selected account.
const selectFilterUser = (user: SimpleUser) => {
  selectedFilterUser.value = user
  filterUserKeyword.value = user.email
  showFilterUserDropdown.value = false
  filters.user_id = user.id
  applyFilters()
}

// clearFilterUser clears the toolbar user selection and reloads subscriptions without
// it.
const clearFilterUser = () => {
  selectedFilterUser.value = null
  filterUserKeyword.value = ''
  filterUserResults.value = []
  showFilterUserDropdown.value = false
  filters.user_id = null
  applyFilters()
}

// debounceSearchUsers replaces the assignment-user lookup timer with a delayed search.
// The timer is separate from toolbar filtering and is cleared during unmount to prevent
// queued dialog searches after teardown.
const debounceSearchUsers = () => {
  if (userSearchTimeout) {
    clearTimeout(userSearchTimeout)
  }
  userSearchTimeout = setTimeout(searchUsers, 300)
}

// searchUsers resolves the assignment keyword into selectable user suggestions. Editing
// a selected email clears the assigned user identifier; empty keywords or request
// failures clear results while leaving the draft available.
const searchUsers = async () => {
  const keyword = userSearchKeyword.value.trim()

  // Clear the assigned user identifier after the selected email changes so the form
  // cannot submit an identity hidden by edited search text.
  if (selectedUser.value && keyword !== selectedUser.value.email) {
    selectedUser.value = null
    assignForm.user_id = null
  }

  if (!keyword) {
    userSearchResults.value = []
    return
  }

  userSearchLoading.value = true
  try {
    userSearchResults.value = await adminAPI.usage.searchUsers(keyword)
  } catch (error) {
    // Failed user searches clear suggestions so stale results cannot be presented as
    // matches for the newly entered search keyword.
    console.error('Failed to search users:', error)
    userSearchResults.value = []
  } finally {
    userSearchLoading.value = false
  }
}

// selectUser copies a suggested identifier and email into the assignment draft and
// closes its suggestion dropdown. This supplies the user_id that assignment
// validation requires before a request can be submitted.
const selectUser = (user: SimpleUser) => {
  selectedUser.value = user
  userSearchKeyword.value = user.email
  showUserDropdown.value = false
  assignForm.user_id = user.id
}

// clearUserSelection clears assignment identity, search text, and current user
// suggestions.
const clearUserSelection = () => {
  selectedUser.value = null
  userSearchKeyword.value = ''
  userSearchResults.value = []
  assignForm.user_id = null
}

// handlePageChange selects a subscription page and requests its matching rows.
const handlePageChange = (page: number) => {
  pagination.page = page
  loadSubscriptions()
}

// handlePageSizeChange stores the selected page size and reloads from page one.
// Resetting the current page before requesting rows presents the beginning of the
// newly sized result set.
const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadSubscriptions()
}

// handleSort records the selected server field and direction, then reloads from the
// first page. The page reset presents the beginning of the reordered records under
// the current filters.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadSubscriptions()
}

// closeAssignModal hides assignment and restores empty user and plan selections with
// thirty validity days. It also clears the user-search keyword, results, and
// dropdown so another opening starts with an unselected account.
const closeAssignModal = () => {
  showAssignModal.value = false
  assignForm.user_id = null
  assignForm.plan_id = null
  assignForm.validity_days = 30
  selectedUser.value = null
  userSearchKeyword.value = ''
  userSearchResults.value = []
  showUserDropdown.value = false
}

// handleAssignSubscription validates a selected user, plan, and positive validity
// duration. Successful assignment closes and resets the dialog before reloading the
// list; failures retain the draft and release submission feedback.
const handleAssignSubscription = async () => {
  if (!assignForm.user_id) {
    appStore.showError(t('admin.subscriptions.pleaseSelectUser'))
    return
  }
  if (!assignForm.plan_id) {
    appStore.showError(t('admin.announcements.form.selectPackages'))
    return
  }
  if (!assignForm.validity_days || assignForm.validity_days < 1) {
    appStore.showError(t('admin.subscriptions.validityDaysRequired'))
    return
  }

  submitting.value = true
  try {
    await adminAPI.subscriptions.assign({
      user_id: assignForm.user_id,
      plan_id: assignForm.plan_id,
      validity_days: assignForm.validity_days,
    })
    appStore.showSuccess(t('admin.subscriptions.subscriptionAssigned'))
    closeAssignModal()
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToAssign'))
    console.error('Error assigning subscription:', error)
  } finally {
    submitting.value = false
  }
}

// handleExtend selects a subscription and opens its validity editor with calculated
// target days. Future subscriptions use their own start as the duration anchor,
// while active subscriptions begin from remaining time.
const handleExtend = (subscription: UserSubscription) => {
  extendingSubscription.value = subscription
  extendForm.days = getTargetValidityDays(subscription)
  showExtendModal.value = true
}

// closeExtendModal hides validity adjustment and releases the selected subscription.
const closeExtendModal = () => {
  showExtendModal.value = false
  extendingSubscription.value = null
}

// handleExtendSubscription submits a finite validity duration of at least one day. It
// requires a selected subscription; success closes the dialog and reloads, while
// failure leaves the selected draft available.
const handleExtendSubscription = async () => {
  if (!extendingSubscription.value) return

  if (!Number.isFinite(extendForm.days) || extendForm.days < 1) {
    appStore.showError(t('admin.subscriptions.validityDaysRequired'))
    return
  }

  submitting.value = true
  try {
    await adminAPI.subscriptions.extend(extendingSubscription.value.id, {
      days: extendForm.days,
    })
    appStore.showSuccess(t('admin.subscriptions.subscriptionAdjusted'))
    closeExtendModal()
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToAdjust'))
    console.error('Error adjusting subscription:', error)
  } finally {
    submitting.value = false
  }
}

// handleRevoke selects a subscription and opens its cancellation or revocation
// confirmation.
const handleRevoke = (subscription: UserSubscription) => {
  revokingSubscription.value = subscription
  showRevokeDialog.value = true
}

// confirmRevoke revokes the selected subscription and chooses feedback from its pending
// status. Success clears the confirmation target and reloads the list; errors retain
// the target and display the request failure.
const confirmRevoke = async () => {
  if (!revokingSubscription.value) return

  const pending = isPendingSubscription(revokingSubscription.value)
  try {
    await adminAPI.subscriptions.revoke(revokingSubscription.value.id)
    appStore.showSuccess(
      pending
        ? t('admin.subscriptions.pendingSubscriptionCancelled')
        : t('admin.subscriptions.subscriptionRevoked'),
    )
    showRevokeDialog.value = false
    revokingSubscription.value = null
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToRevoke'))
    console.error('Error revoking subscription:', error)
  }
}

// handleRestore selects the subscription awaiting confirmation of a restoration
// request.
const handleRestore = (subscription: UserSubscription) => {
  restoringSubscription.value = subscription
  showRestoreDialog.value = true
}

// confirmRestore restores the selected subscription and reloads after success. With no
// selection it returns immediately, while a failed request retains confirmation state
// and reports the endpoint detail or translated error.
const confirmRestore = async () => {
  if (!restoringSubscription.value) return

  try {
    await adminAPI.subscriptions.restore(restoringSubscription.value.id)
    appStore.showSuccess(t('admin.subscriptions.subscriptionRestored'))
    showRestoreDialog.value = false
    restoringSubscription.value = null
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToRestore'))
    console.error('Error restoring subscription:', error)
  }
}

// handleResetQuota selects a subscription and opens its quota-reset confirmation
// dialog.
const handleResetQuota = (subscription: UserSubscription) => {
  resettingSubscription.value = subscription
  showResetQuotaConfirm.value = true
}

// confirmResetQuota resets daily, weekly, and monthly quotas for the selected
// subscription. Duplicate submissions are ignored while resettingQuota is set; success
// clears confirmation and awaits a list refresh before releasing that flag.
const confirmResetQuota = async () => {
  if (!resettingSubscription.value) return
  if (resettingQuota.value) return
  resettingQuota.value = true
  try {
    await adminAPI.subscriptions.resetQuota(resettingSubscription.value.id, {
      daily: true,
      weekly: true,
      monthly: true,
    })
    appStore.showSuccess(t('admin.subscriptions.quotaResetSuccess'))
    showResetQuotaConfirm.value = false
    resettingSubscription.value = null
    await loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToResetQuota'))
    console.error('Error resetting quota:', error)
  } finally {
    resettingQuota.value = false
  }
}

// getDaysRemaining returns whole days rounded upward or null after expiry. The display
// uses this result for its warning threshold, so any positive partial day still counts
// as one remaining day.
const getDaysRemaining = (expiresAt: string): number | null => {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  if (diff < 0) return null
  // Round a partial remaining day upward for the expiry warning display.
  return Math.ceil(diff / (1000 * 60 * 60 * 24))
}

// formatRemainingExpiry renders remaining expiry in days, hours/minutes, or minutes. It
// delegates duration calculation and returns null when that helper finds no remaining
// duration for the supplied expiry timestamp.
const formatRemainingExpiry = (expiresAt: string): string | null => {
  const duration = getRemainingExpiryDuration(expiresAt)
  if (!duration) return null
  if (duration.unit === 'days') {
    return t('admin.subscriptions.daysRemaining', { days: duration.days })
  }
  if (duration.hours) {
    return t('admin.subscriptions.hoursMinutesRemaining', {
      hours: duration.hours,
      minutes: duration.minutes,
    })
  }
  return t('admin.subscriptions.minutesRemaining', { minutes: duration.minutes })
}

// getTargetValidityDays calculates the editor's validity duration from the later of now
// and subscription start. Future subscriptions therefore use their own start as the
// anchor, while active subscriptions use remaining time. The result rounds partial days
// upward and has a minimum of one day. This prevents the adjustment draft from counting
// the waiting interval before a future subscription begins as part of its active
// validity.
const getTargetValidityDays = (subscription: UserSubscription): number => {
  const startsAt = new Date(subscription.starts_at)
  const expiresAt = new Date(subscription.expires_at)
  const now = new Date()
  // Future subscriptions measure validity from their start, preventing the waiting
  // interval from inflating the adjustment dialog duration before activation.
  const anchor = now < startsAt ? startsAt : now
  const diff = expiresAt.getTime() - anchor.getTime()
  // Round partial days upward and keep the editor duration at least one day.
  return Math.max(1, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

// isExpiringSoon reports whether an unexpired subscription has at most seven rounded
// days.
const isExpiringSoon = (expiresAt: string): boolean => {
  const days = getDaysRemaining(expiresAt)
  // The expiry warning covers up to seven rounded remaining days.
  return days !== null && days <= 7
}

// getProgressWidth returns a percentage width capped at one hundred percent. Missing or
// zero limits produce zero width, and absent usage counts as zero for the table's quota
// progress display.
const getProgressWidth = (used: number | null | undefined, limit: number | null): string => {
  if (!limit || limit === 0) return '0%'
  const usedValue = used ?? 0
  // Cap bar width at full scale when recorded usage exceeds its configured quota.
  const percentage = Math.min((usedValue / limit) * 100, 100)
  return `${percentage}%`
}

// getProgressClass colors quota usage by its share of a configured limit. Missing or
// zero limits use a neutral color, while seventy and ninety percent select warning and
// danger colors respectively.
const getProgressClass = (used: number | null | undefined, limit: number | null): string => {
  if (!limit || limit === 0) return 'bg-gray-400'
  const usedValue = used ?? 0
  const percentage = (usedValue / limit) * 100
  // Use warning colors from seventy percent and danger from ninety percent usage.
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
}

// formatResetDuration chooses translated reset text from day, hour, and minute parts.
const formatResetDuration = (parts: RemainingDurationParts): string => {
  if (parts.days > 0) {
    return t('admin.subscriptions.resetInDaysHours', { days: parts.days, hours: parts.hours })
  }
  if (parts.hours > 0) {
    return t('admin.subscriptions.resetInHoursMinutes', {
      hours: parts.hours,
      minutes: parts.minutes,
    })
  }
  return t('admin.subscriptions.resetInMinutes', { minutes: parts.minutes })
}

// formatQuotaEndDuration chooses translated expiry text from day, hour, and minute
// parts.
const formatQuotaEndDuration = (parts: RemainingDurationParts): string => {
  if (parts.days > 0) {
    return t('admin.subscriptions.quotaEndsInDaysHours', { days: parts.days, hours: parts.hours })
  }
  if (parts.hours > 0) {
    return t('admin.subscriptions.quotaEndsInHoursMinutes', {
      hours: parts.hours,
      minutes: parts.minutes,
    })
  }
  return t('admin.subscriptions.quotaEndsInMinutes', { minutes: parts.minutes })
}

// formatDailyUsageWindow shows subscription expiry for a one-time daily quota. Other
// subscriptions use the daily window formatter, and a missing remaining duration is
// displayed as an inactive window in the table.
const formatDailyUsageWindow = (subscription: UserSubscription): string => {
  if (isOneTimeDailyQuota(subscription)) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    return parts ? formatQuotaEndDuration(parts) : t('admin.subscriptions.windowNotActive')
  }
  return formatUsageWindow(subscription, subscription.daily_window_start, 'daily')
}

// formatUsageWindow selects expiry wording using this view's fixed-duration window
// heuristic. Otherwise it delegates to the ordinary reset formatter; missing duration
// for an ending window produces the inactive-window label.
const formatUsageWindow = (
  subscription: UserSubscription,
  windowStart: string | null,
  period: 'daily' | 'weekly' | 'monthly',
): string => {
  if (isQuotaWindowEndingAtSubscriptionExpiry(subscription, windowStart, period)) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    return parts ? formatQuotaEndDuration(parts) : t('admin.subscriptions.windowNotActive')
  }
  return formatResetTime(windowStart, period)
}

// formatResetTime renders time remaining until a fixed daily, weekly, or monthly reset.
// Monthly means thirty days here; missing starts and elapsed durations display the
// inactive-window label supplied by the translations.
const formatResetTime = (
  windowStart: string | null,
  period: 'daily' | 'weekly' | 'monthly',
): string => {
  if (!windowStart) return t('admin.subscriptions.windowNotActive')

  const start = new Date(windowStart)
  const now = new Date()

  // Quota windows use fixed elapsed durations: one day, seven days, or thirty days,
  // including the period labeled monthly in the interface.
  let resetTime: Date
  switch (period) {
    case 'daily':
      resetTime = new Date(start.getTime() + 24 * 60 * 60 * 1000)
      break
    case 'weekly':
      resetTime = new Date(start.getTime() + 7 * 24 * 60 * 60 * 1000)
      break
    case 'monthly':
      resetTime = new Date(start.getTime() + 30 * 24 * 60 * 60 * 1000)
      break
  }

  const parts = getRemainingDurationParts(resetTime, now)
  return parts ? formatResetDuration(parts) : t('admin.subscriptions.windowNotActive')
}

// isQuotaWindowEndingAtSubscriptionExpiry compares subscription expiry with two
// quota-window lengths after the current start to choose display wording. It returns true
// when that computed end extends beyond expiry. Missing or invalid timestamps return
// false. Daily, weekly, and monthly use fixed one-, seven-, and thirty-day durations,
// with equality treated as a full fit. This display heuristic does not inspect outer
// quota limits; backend eligibility can permit shorter daily or weekly tails protected
// by positive outer limits.
const isQuotaWindowEndingAtSubscriptionExpiry = (
  subscription: UserSubscription,
  windowStart: string | null,
  period: 'daily' | 'weekly' | 'monthly',
): boolean => {
  if (!windowStart) return false
  const start = new Date(windowStart).getTime()
  const expiresAt = new Date(subscription.expires_at).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(expiresAt)) return false

  const windowMsByPeriod = {
    daily: 24 * 60 * 60 * 1000,
    weekly: 7 * 24 * 60 * 60 * 1000,
    monthly: 30 * 24 * 60 * 60 * 1000,
  }
  const windowMs = windowMsByPeriod[period]
  // Choose expiry wording from timestamps and fixed period lengths here; backend
  // eligibility also considers outer quota protection that this display omits.
  const nextWindowStart = start + windowMs
  return nextWindowStart + windowMs > expiresAt
}

// handleClickOutside closes user-search and column dropdowns when their owning elements
// exclude the click. It is installed on document during mount and removed during
// unmount with the same function reference.
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!target.closest('[data-assign-user-search]')) showUserDropdown.value = false
  if (!target.closest('[data-filter-user-search]')) showFilterUserDropdown.value = false
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
}

// Restore display preferences before starting the list and plan loads, then install
// the document listener used to close open dropdowns.
onMounted(() => {
  loadUserColumnMode()
  loadSavedColumns()
  loadSubscriptions()
  loadPlans()
  document.addEventListener('click', handleClickOutside)
})

// Remove the document click listener and clear both queued user searches so those
// callbacks do not run after this component unmounts.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  if (filterUserSearchTimeout) {
    clearTimeout(filterUserSearchTimeout)
  }
  if (userSearchTimeout) {
    clearTimeout(userSearchTimeout)
  }
})
</script>

<style scoped>
.usage-row {
  @apply space-y-1;
}

.usage-label {
  @apply w-10 flex-shrink-0 text-xs font-medium text-gray-500 dark:text-gray-400;
}

.usage-amount {
  @apply whitespace-nowrap text-xs tabular-nums text-gray-600 dark:text-gray-300;
}

.reset-info {
  @apply flex items-center gap-1 pl-12 text-xs text-blue-600 dark:text-blue-400;
}
</style>
