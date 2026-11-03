<template>
  <div class="relative">
    <!-- Administrators can open update, restart, and rollback controls from the version badge. -->
    <template v-if="isAdmin">
      <button
        ref="badgeTriggerRef"
        type="button"
        data-testid="version-badge-trigger"
        @click="toggleDropdown"
        class="flex items-center gap-1.5 rounded-control px-2 py-1 text-xs transition-colors"
        :class="[
          hasUpdate
            ? 'bg-amber-100 text-amber-700 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:hover:bg-amber-900/50'
            : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-400 dark:hover:bg-dark-700',
        ]"
        :title="hasUpdate ? t('version.updateAvailable') : t('version.upToDate')"
      >
        <span v-if="currentVersion" class="font-medium">v{{ currentVersion }}</span>
        <span
          v-else
          class="h-4 w-12 animate-pulse rounded-compact bg-gray-200 font-medium dark:bg-dark-600"
        ></span>
        <!-- Update indicator -->
        <span v-if="hasUpdate" class="h-2 w-2 rounded-full bg-amber-500" aria-hidden="true"></span>
      </button>

      <!-- Dropdown -->
      <MotionTransition name="dropdown-fade">
        <div
          v-if="dropdownOpen"
          ref="dropdownRef"
          class="dropdown left-0 z-50 mt-2 max-w-[calc(100vw-2rem)] overflow-hidden whitespace-normal py-0 transition duration-normal"
          :class="rollbackPanelOpen && isReleaseBuild ? 'w-80' : 'w-64'"
        >
          <!-- Header with refresh button -->
          <div
            class="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-dark-700"
          >
            <span class="text-sm font-medium text-gray-700 dark:text-dark-300">{{
              t('version.currentVersion')
            }}</span>
            <!-- The header exposes restart independently of the update action. -->
            <div class="flex items-center gap-1">
              <button
                @click="handleRestart"
                class="rounded-control p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-dark-700 dark:hover:text-dark-200"
                :disabled="restarting"
                :title="restarting ? t('version.restarting') : t('version.restartNow')"
              >
                <Icon
                  :name="restarting ? 'refresh' : 'power'"
                  size="sm"
                  :stroke-width="2"
                  :class="{ 'animate-spin': restarting }"
                />
              </button>
              <button
                @click="refreshVersion(true)"
                class="rounded-control p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-dark-200"
                :disabled="loading"
                :title="t('version.refresh')"
              >
                <Icon
                  name="refresh"
                  size="sm"
                  :stroke-width="2"
                  :class="{ 'animate-spin': loading }"
                />
              </button>
            </div>
          </div>

          <div class="p-4">
            <!-- Loading state -->
            <ContentSkeleton v-if="loading" variant="detail" :rows="4" class="py-4" />

            <!-- Content -->
            <template v-else>
              <!-- Version display - centered and prominent -->
              <div class="mb-4 text-center">
                <div class="inline-flex items-center gap-2">
                  <span
                    v-if="currentVersion"
                    class="text-2xl font-bold text-gray-900 dark:text-white"
                    >v{{ currentVersion }}</span
                  >
                  <span v-else class="text-2xl font-bold text-gray-400 dark:text-dark-500">--</span>
                  <!-- Show check mark when up to date -->
                  <span
                    v-if="!hasUpdate"
                    class="flex h-5 w-5 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30"
                  >
                    <Icon
                      name="check"
                      size="xs"
                      :animate-on-hover="false"
                      class="h-3 w-3 text-green-600 dark:text-green-400"
                    />
                  </span>
                </div>
                <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                  {{
                    hasUpdate
                      ? t('version.latestVersion') + ': v' + latestVersion
                      : t('version.upToDate')
                  }}
                </p>
              </div>

              <!-- Priority 1: Update error (must check before hasUpdate) -->
              <div v-if="updateError" class="space-y-2">
                <div
                  class="flex items-center gap-3 rounded-control border border-red-200 bg-red-50 p-3 dark:border-red-800/50 dark:bg-red-900/20"
                >
                  <div
                    class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/50"
                  >
                    <Icon
                      name="x"
                      size="sm"
                      :stroke-width="2"
                      class="text-red-600 dark:text-red-400"
                    />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-sm font-medium text-red-700 dark:text-red-300">
                      {{ t('version.updateFailed') }}
                    </p>
                    <p class="truncate text-xs text-red-600/70 dark:text-red-400/70">
                      {{ updateError }}
                    </p>
                  </div>
                </div>

                <!-- Retry button -->
                <button @click="handleUpdate" :disabled="updating" class="btn btn-danger w-full">
                  {{ t('version.retry') }}
                </button>
              </div>

              <!-- Priority 2: Update success - need restart -->
              <div v-else-if="updateSuccess && needRestart" class="space-y-2">
                <div
                  class="flex items-center gap-3 rounded-control border border-green-200 bg-green-50 p-3 dark:border-green-800/50 dark:bg-green-900/20"
                >
                  <div
                    class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/50"
                  >
                    <Icon
                      name="check"
                      size="sm"
                      :animate-on-hover="false"
                      class="h-4 w-4 text-green-600 dark:text-green-400"
                    />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-sm font-medium text-green-700 dark:text-green-300">
                      {{
                        successKind === 'rollback'
                          ? t('version.rollbackComplete')
                          : t('version.updateComplete')
                      }}
                    </p>
                    <p class="text-xs text-green-600/70 dark:text-green-400/70">
                      {{ t('version.restartRequired') }}
                    </p>
                  </div>
                </div>

                <!-- Restart button with countdown -->
                <button
                  @click="handleRestart"
                  :disabled="restarting"
                  class="btn btn-success w-full"
                >
                  <Icon
                    name="loader"
                    size="sm"
                    :animate-on-hover="false"
                    v-if="restarting"
                    class="h-4 w-4 animate-spin"
                  />
                  <Icon name="refresh" size="sm" v-else class="h-4 w-4" />
                  <template v-if="restarting">
                    <span>{{ t('version.restarting') }}</span>
                    <span v-if="restartCountdown > 0" class="tabular-nums"
                      >({{ restartCountdown }}s)</span
                    >
                  </template>
                  <span v-else>{{ t('version.restartNow') }}</span>
                </button>
              </div>

              <!-- Priority 3: Update available for source build - show git pull hint and changelog modal entry -->
              <div v-else-if="hasUpdate && !isReleaseBuild" class="space-y-2">
                <div
                  class="flex items-center gap-3 rounded-control border border-amber-200 bg-amber-50 p-3 dark:border-amber-800/50 dark:bg-amber-900/20"
                >
                  <div
                    class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-900/50"
                  >
                    <Icon
                      name="download"
                      size="sm"
                      :stroke-width="2"
                      class="text-amber-600 dark:text-amber-400"
                    />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-sm font-medium text-amber-700 dark:text-amber-300">
                      {{ t('version.updateAvailable') }}
                    </p>
                    <p class="text-xs text-amber-600/70 dark:text-amber-400/70">
                      {{ targetVersionLabel }}
                    </p>
                  </div>
                </div>
                <!-- Source build hint -->
                <div
                  class="flex items-center gap-2 rounded-control border border-blue-200 bg-blue-50 p-2 dark:border-blue-800/50 dark:bg-blue-900/20"
                >
                  <Icon
                    name="infoCircle"
                    size="xs"
                    class="h-3.5 w-3.5 flex-shrink-0 text-blue-500 dark:text-blue-400"
                  />
                  <p class="text-xs text-blue-600 dark:text-blue-400">
                    {{ t('version.sourceModeHint') }}
                  </p>
                </div>
                <!-- Open in-app changelog modal -->
                <button
                  type="button"
                  data-testid="version-view-changelog"
                  @click="openChangelogModal"
                  class="btn btn-secondary w-full"
                >
                  <Icon name="document" size="sm" :stroke-width="2" />
                  {{ t('version.viewChangelog') }}
                </button>
              </div>

              <!-- Priority 4: Update available for release build - show update button and changelog modal entry -->
              <div v-else-if="hasUpdate && isReleaseBuild" class="space-y-2">
                <!-- Update info card -->
                <div
                  class="flex items-center gap-3 rounded-control border border-amber-200 bg-amber-50 p-3 dark:border-amber-800/50 dark:bg-amber-900/20"
                >
                  <div
                    class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-900/50"
                  >
                    <Icon
                      name="download"
                      size="sm"
                      :stroke-width="2"
                      class="text-amber-600 dark:text-amber-400"
                    />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="text-sm font-medium text-amber-700 dark:text-amber-300">
                      {{ t('version.updateAvailable') }}
                    </p>
                    <p class="text-xs text-amber-600/70 dark:text-amber-400/70">
                      {{ targetVersionLabel }}
                    </p>
                  </div>
                </div>

                <!-- The update action uses the shared primary button recipe. -->
                <button @click="handleUpdate" :disabled="updating" class="btn btn-primary w-full">
                  <Icon
                    name="loader"
                    size="sm"
                    :animate-on-hover="false"
                    v-if="updating"
                    class="h-4 w-4 animate-spin"
                  />
                  <Icon v-else name="download" size="sm" :stroke-width="2" />
                  {{ updating ? t('version.updating') : t('version.updateNow') }}
                </button>

                <!-- Open in-app changelog modal -->
                <button
                  type="button"
                  data-testid="version-view-changelog"
                  @click="openChangelogModal"
                  class="flex w-full items-center justify-center gap-1.5 rounded-control py-1.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-dark-100"
                >
                  <Icon name="document" size="xs" :stroke-width="2" />
                  {{ t('version.viewChangelog') }}
                </button>
              </div>

              <!-- Current versions show the repository link and rollback controls. -->
              <div v-else class="space-y-2">
                <a
                  v-if="safeReleaseUrl"
                  :href="safeReleaseUrl"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="flex items-center justify-center gap-2 py-2 text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200"
                >
                  <svg class="h-4 w-4" fill="currentColor" viewBox="0 0 24 24">
                    <path
                      fill-rule="evenodd"
                      clip-rule="evenodd"
                      d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.17 6.839 9.49.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.464-1.11-1.464-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0112 6.836c.85.004 1.705.114 2.504.336 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.167 22 16.418 22 12c0-5.523-4.477-10-10-10z"
                    />
                  </svg>
                  {{ t('version.viewRelease') }}
                </a>

                <!-- Rollback panel trigger. -->
                <div class="border-t border-gray-100 pt-2 dark:border-dark-700">
                  <button
                    @click="toggleRollbackPanel"
                    class="group flex w-full items-center justify-between rounded-control px-2 py-1.5 text-xs text-gray-400 transition-colors hover:bg-gray-50 hover:text-gray-600 dark:text-dark-500 dark:hover:bg-dark-700/50 dark:hover:text-dark-300"
                  >
                    <span class="flex items-center gap-1.5">
                      <Icon name="clock" size="xs" :stroke-width="2" />
                      {{ t('version.rollback') }}
                    </span>
                    <Icon
                      name="chevronDown"
                      size="xs"
                      :stroke-width="2"
                      class="transition-transform duration-normal"
                      :class="{ 'rotate-180': rollbackPanelOpen }"
                      :animate-on-hover="false"
                    />
                  </button>

                  <Collapse :open="rollbackPanelOpen" unmount-on-hide>
                    <div class="mt-2 space-y-2">
                      <!-- Source builds display Git rollback guidance. -->
                      <div
                        v-if="!isReleaseBuild"
                        class="flex items-center gap-2 rounded-control border border-blue-200 bg-blue-50 p-2 dark:border-blue-800/50 dark:bg-blue-900/20"
                      >
                        <Icon
                          name="infoCircle"
                          size="xs"
                          class="h-3.5 w-3.5 flex-shrink-0 text-blue-500 dark:text-blue-400"
                        />
                        <p
                          class="min-w-0 flex-1 text-xs leading-4 text-blue-600 dark:text-blue-400"
                        >
                          {{ t('version.rollbackSourceHint') }}
                        </p>
                      </div>

                      <!-- Historical versions are loading. -->
                      <ContentSkeleton
                        v-else-if="rollbackVersionsLoading"
                        variant="list"
                        :rows="3"
                        class="py-4"
                      />

                      <!-- Version loading failure and retry action. -->
                      <div v-else-if="rollbackVersionsError" class="space-y-2">
                        <p
                          class="rounded-control border border-red-200 bg-red-50 p-2.5 text-xs text-red-600 dark:border-red-800/50 dark:bg-red-900/20 dark:text-red-400"
                        >
                          {{ rollbackVersionsError }}
                        </p>
                        <button
                          @click="loadRollbackVersions"
                          class="w-full rounded-control border border-gray-200 py-1.5 text-xs text-gray-500 transition-colors hover:bg-gray-50 hover:text-gray-700 dark:border-dark-700 dark:text-dark-400 dark:hover:bg-dark-700/50 dark:hover:text-dark-200"
                        >
                          {{ t('version.retry') }}
                        </button>
                      </div>

                      <!-- No rollback versions are available. -->
                      <p
                        v-else-if="rollbackVersions.length === 0"
                        class="py-3 text-center text-xs text-gray-400 dark:text-dark-500"
                      >
                        {{ t('version.noRollbackVersions') }}
                      </p>

                      <!-- Available rollback versions. -->
                      <template v-else>
                        <p class="px-0.5 text-xs text-gray-400 dark:text-dark-500">
                          {{ t('version.rollbackSelectVersion') }}
                        </p>

                        <button
                          v-for="item in rollbackVersions"
                          :key="item.version"
                          @click="selectRollbackVersion(item.version)"
                          :disabled="rollingBack"
                          class="flex w-full items-center justify-between rounded-control border px-3 py-2 text-left transition disabled:cursor-not-allowed disabled:opacity-60"
                          :class="
                            selectedRollbackVersion === item.version
                              ? 'border-amber-300 bg-amber-50 shadow-sm dark:border-amber-700 dark:bg-amber-900/20'
                              : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50 dark:border-dark-700 dark:hover:border-dark-600 dark:hover:bg-dark-700/40'
                          "
                        >
                          <span class="flex items-center gap-2">
                            <span
                              class="flex h-3.5 w-3.5 items-center justify-center rounded-full border transition-colors"
                              :class="
                                selectedRollbackVersion === item.version
                                  ? 'border-amber-500'
                                  : 'border-gray-300 dark:border-dark-500'
                              "
                            >
                              <span
                                v-if="selectedRollbackVersion === item.version"
                                class="h-1.5 w-1.5 rounded-full bg-amber-500"
                              ></span>
                            </span>
                            <span
                              class="text-sm font-semibold"
                              :class="
                                selectedRollbackVersion === item.version
                                  ? 'text-amber-700 dark:text-amber-300'
                                  : 'text-gray-700 dark:text-dark-200'
                              "
                              >v{{ item.version }}</span
                            >
                          </span>
                          <span class="text-xs tabular-nums text-gray-400 dark:text-dark-500">
                            {{ formatPublishedAt(item.published_at) }}
                          </span>
                        </button>

                        <!-- Selecting a version reveals deployment-specific commands and the rollback action. -->
                        <Collapse :open="!!selectedRollbackVersion" unmount-on-hide>
                          <div class="space-y-2">
                            <p class="px-0.5 text-xs text-gray-400 dark:text-dark-500">
                              {{ t('version.manualRollbackCommand') }}
                            </p>

                            <!-- Command panel with deployment tabs. -->
                            <div
                              class="overflow-hidden rounded-control border border-gray-200 dark:border-dark-600"
                            >
                              <div
                                class="flex items-center justify-between border-b border-gray-200 bg-gray-100 px-2 py-1.5 dark:border-dark-600 dark:bg-dark-700"
                              >
                                <div v-segmented class="segmented">
                                  <button
                                    v-for="tab in manualTabs"
                                    :key="tab.key"
                                    type="button"
                                    @click="manualTab = tab.key"
                                    class="segmented-item px-2 py-0.5 text-xs"
                                    :class="{ 'segmented-item-active': manualTab === tab.key }"
                                  >
                                    {{ tab.label }}
                                  </button>
                                </div>
                                <button
                                  @click="copyToClipboard(activeManualCommand)"
                                  class="flex items-center gap-1 rounded-compact px-1.5 py-0.5 text-xs text-gray-400 transition-colors hover:bg-gray-200 hover:text-gray-600 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-200"
                                >
                                  <Icon
                                    :name="copied ? 'check' : 'copy'"
                                    size="xs"
                                    :stroke-width="2"
                                    :class="copied ? 'text-green-500' : ''"
                                  />
                                  {{ copied ? t('version.copied') : t('version.copyCommand') }}
                                </button>
                              </div>
                              <code
                                class="block select-all whitespace-pre-wrap break-all bg-gray-50 p-2.5 font-mono text-xs leading-relaxed text-gray-600 dark:bg-dark-900 dark:text-dark-300"
                                >{{ activeManualCommand }}</code
                              >
                            </div>

                            <p
                              class="flex items-start gap-1.5 px-0.5 text-xs leading-4 text-amber-600 dark:text-amber-400"
                            >
                              <Icon
                                name="exclamationTriangle"
                                size="xs"
                                :stroke-width="2"
                                class="mt-px flex-shrink-0"
                              />
                              {{ t('version.rollbackWarning') }}
                            </p>

                            <p
                              v-if="rollbackError"
                              class="rounded-control border border-red-200 bg-red-50 p-2 text-xs text-red-600 dark:border-red-800/50 dark:bg-red-900/20 dark:text-red-400"
                            >
                              {{ rollbackError }}
                            </p>

                            <button
                              @click="handleRollback"
                              :disabled="rollingBack"
                              class="btn btn-warning w-full"
                            >
                              <Icon
                                name="loader"
                                size="sm"
                                :animate-on-hover="false"
                                v-if="rollingBack"
                                class="h-4 w-4 animate-spin"
                              />
                              <Icon v-else name="clock" size="sm" :stroke-width="2" />
                              <span>{{
                                rollingBack
                                  ? t('version.rollingBack')
                                  : t('version.rollbackConfirm', {
                                      version: 'v' + selectedRollbackVersion,
                                    })
                              }}</span>
                            </button>
                          </div>
                        </Collapse>
                      </template>
                    </div>
                  </Collapse>
                </div>
              </div>
            </template>
          </div>
        </div>
      </MotionTransition>

      <!-- In-app changelog modal for the latest target release -->
      <BaseDialog
        :show="changelogModalOpen"
        :title="changelogDialogTitle"
        :subtitle="changelogDialogSubtitle"
        width="wide"
        :trap-focus="true"
        :close-on-escape="true"
        :close-on-click-outside="true"
        :z-index="Z_INDEX.MODAL_NESTED"
        :panel-data="{ 'data-testid': 'version-changelog-modal' }"
        @close="closeChangelogModal"
      >
        <template #header-icon>
          <div
            class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-400"
          >
            <Icon name="document" size="sm" :stroke-width="2" />
          </div>
        </template>

        <!-- Target release metadata summary -->
        <div
          data-testid="changelog-release-meta"
          class="flex flex-wrap items-center justify-between gap-3 rounded-surface border border-gray-200 bg-gray-50/80 px-4 py-3 dark:border-dark-600 dark:bg-dark-950"
        >
          <div class="flex min-w-0 flex-wrap items-center gap-2">
            <span
              v-if="targetVersionLabel"
              data-testid="changelog-target-version"
              class="inline-flex items-center gap-1.5 rounded-compact bg-amber-100 px-2.5 py-1 font-mono text-xs font-semibold text-amber-800 dark:bg-amber-900/30 dark:text-amber-300"
            >
              <Icon name="tag" size="xs" :stroke-width="2" />
              {{ targetVersionLabel }}
            </span>
            <span
              v-if="currentVersion"
              class="text-xs tabular-nums text-gray-500 dark:text-dark-400"
            >
              {{ t('version.currentVersion') }}: v{{ currentVersion }}
            </span>
            <span
              v-if="!isReleaseBuild"
              class="inline-flex items-center rounded-compact bg-blue-50 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/20 dark:text-blue-300"
            >
              {{ t('version.sourceMode') }}
            </span>
            <span
              v-if="releaseTitle"
              class="truncate text-xs font-medium text-gray-700 dark:text-dark-200"
            >
              {{ releaseTitle }}
            </span>
          </div>

          <div
            v-if="formattedReleaseDate || safeReleaseUrl"
            class="flex flex-wrap items-center gap-3 text-xs text-gray-500 dark:text-dark-400"
          >
            <span
              v-if="formattedReleaseDate"
              data-testid="changelog-published-at"
              class="inline-flex items-center gap-1.5 tabular-nums"
            >
              <Icon name="calendar" size="xs" :stroke-width="2" />
              <time :datetime="releaseInfo?.published_at">{{ formattedReleaseDate }}</time>
            </span>
            <a
              v-if="safeReleaseUrl"
              :href="safeReleaseUrl"
              target="_blank"
              rel="noopener noreferrer"
              data-testid="changelog-github-link"
              class="inline-flex items-center gap-1 font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
            >
              <span>{{ t('version.viewRelease') }}</span>
              <Icon name="externalLink" size="xs" :stroke-width="2" />
            </a>
          </div>
        </div>

        <!-- Sanitized bilingual release notes -->
        <article
          v-if="hasChangelogNotes"
          data-testid="changelog-markdown-body"
          class="changelog-markdown mt-4 rounded-surface border border-gray-100 bg-white px-4 py-4 text-sm leading-6 text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-100 sm:px-5"
          v-html="sanitizedChangelogHtml"
        ></article>

        <!-- Empty state when release notes are absent -->
        <div
          v-else
          data-testid="changelog-empty-state"
          class="mt-4 flex flex-col items-center justify-center rounded-surface border border-dashed border-gray-200 bg-gray-50/50 px-6 py-10 text-center dark:border-dark-600 dark:bg-dark-950/60"
        >
          <div
            class="mb-3 flex h-12 w-12 items-center justify-center rounded-surface bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-400"
          >
            <Icon name="document" size="lg" />
          </div>
          <p class="text-sm font-semibold text-gray-900 dark:text-dark-50">
            {{ t('version.noReleaseNotes') }}
          </p>
          <p class="mt-1 max-w-md text-xs leading-5 text-gray-500 dark:text-dark-400">
            {{ t('version.noReleaseNotesDesc') }}
          </p>
        </div>

        <template #footer>
          <button
            type="button"
            data-testid="changelog-modal-close"
            class="btn btn-secondary"
            @click="closeChangelogModal"
          >
            {{ t('common.close') }}
          </button>
        </template>
      </BaseDialog>
    </template>
  </div>
</template>

<script setup lang="ts">
import { getLocale } from '@/i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Collapse from '@/components/common/Collapse.vue'

import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAuthStore, useAppStore } from '@/stores'
import {
  performUpdate,
  restartService,
  getRollbackVersions,
  rollback as rollbackAPI,
  type RollbackVersionInfo,
} from '@/api/admin/system'
import { useClipboard } from '@/composables/useClipboard'
import Icon from '@/components/icons/Icon.vue'
import { Z_INDEX } from '@/constants/overlay'
import { vSegmented } from '@/directives/segmented'
import { sanitizeUrl } from '@/utils/url'

// VersionBadge.vue exposes version discovery, in-app changelog inspection,
// update, rollback, and restart controls to administrators. Shared application
// state supplies cached release metadata, so opening the changelog dialog reads
// the latest target version and bilingual Markdown notes directly from the store
// without issuing another GitHub request.
//
// Markdown release notes retain English sections and collapsible Chinese
// details blocks while passing through DOMPurify and strict HTTP(S) URL checks.
// Update and rollback responses determine whether a restart prompt is required,
// and successful binary changes invalidate version metadata before another
// check. Restart runs an eight-second countdown and health probes before
// reloading the page, while manual rollback commands render v-prefixed script
// tags and unprefixed container image tags.

// Release repository and container image coordinates build the manual rollback
// commands shown for the selected version.
const GITHUB_REPO = 'TokenFlux/TokenRouter'
// Container release tags omit the v prefix that installer commands use for GitHub release tags.
const DOCKER_IMAGE = 'ghcr.io/tokenflux/tokenrouter'

// The translation function supplies version status, action labels, and
// installation command guidance in the active locale.
const { t } = useI18n()

// props provides a site version label while the shared application store obtains
// current installation metadata.
const props = defineProps<{
  version?: string // Initial site version label.
}>()

// Authentication and application stores provide administrator access and shared
// cached version metadata for the header.
const authStore = useAuthStore()
const appStore = useAppStore()

const isAdmin = computed(() => authStore.isAdmin)

// Dropdown, badge, and changelog state coordinate menu dismissal before modal
// opening, with the persistent badge serving as the dialog's return-focus target.
const dropdownOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const badgeTriggerRef = ref<HTMLButtonElement | null>(null)
const changelogModalOpen = ref(false)

// Cached version computations read shared loading and release metadata, using
// the site prop when current version is absent.
const loading = computed(() => appStore.versionLoading)
// currentVersion prefers fetched store metadata, then the site-supplied label,
// and leaves the badge skeleton visible when both sources are empty.
const currentVersion = computed(() => appStore.currentVersion || props.version || '')
const latestVersion = computed(() => appStore.latestVersion)
const hasUpdate = computed(() => appStore.hasUpdate)
const releaseInfo = computed(() => appStore.releaseInfo)
const buildType = computed(() => appStore.buildType)

// Update and restart state drive operation feedback, prevent duplicate requests,
// and show the countdown before service health checks.
const updating = ref(false)
const restarting = ref(false)
const needRestart = ref(false)
const updateError = ref('')
const updateSuccess = ref(false)
const restartCountdown = ref(0)
// successKind selects the completion message shown before the requested restart.
const successKind = ref<'update' | 'rollback'>('update')

// Rollback list, selection, and request state keep errors and busy indicators
// scoped to the rollback panel.
const rollbackPanelOpen = ref(false)
const rollbackVersions = ref<RollbackVersionInfo[]>([])
const rollbackVersionsLoading = ref(false)
const rollbackVersionsError = ref('')
const selectedRollbackVersion = ref('')
const rollingBack = ref(false)
const rollbackError = ref('')

// Clipboard state provides transient feedback for the displayed manual command
// and performs the requested copy operation.
const { copied, copyToClipboard } = useClipboard()

// manualTab selects installer or Docker instructions for the currently selected
// historical release version.
const manualTab = ref<'script' | 'docker'>('script')

// Manual command computations use the selected release and deployment tab to
// produce the corresponding installation instructions.
const manualTabs = computed(() => [
  { key: 'script' as const, label: t('version.deployScript') },
  { key: 'docker' as const, label: t('version.deployDocker') },
])

const scriptRollbackCommand = computed(() => {
  if (!selectedRollbackVersion.value) return ''
  const tag = `v${selectedRollbackVersion.value}`
  return `curl -sSL https://raw.githubusercontent.com/${GITHUB_REPO}/${tag}/deploy/install.sh | sudo bash -s -- rollback ${tag}`
})

const dockerRollbackCommand = computed(() => {
  if (!selectedRollbackVersion.value) return ''
  return [
    `# ${t('version.dockerEditCompose')}`,
    `image: ${DOCKER_IMAGE}:${selectedRollbackVersion.value}`,
    '',
    `# ${t('version.dockerRecreate')}`,
    'docker compose up -d',
  ].join('\n')
})

const activeManualCommand = computed(() =>
  manualTab.value === 'docker' ? dockerRollbackCommand.value : scriptRollbackCommand.value,
)

// isReleaseBuild gates the binary update and rollback controls for release
// deployments; administrators can refresh version metadata from source builds too.
const isReleaseBuild = computed(() => buildType.value === 'release')

// Changelog computations derive the v-prefixed target tag, dialog headings,
// validated metadata, and sanitized bilingual HTML from cached release state.
const targetVersionLabel = computed(() => {
  const raw = (latestVersion.value || '').trim()
  if (!raw) return ''
  return raw.startsWith('v') || raw.startsWith('V') ? `v${raw.slice(1)}` : `v${raw}`
})

const changelogDialogTitle = computed(() =>
  targetVersionLabel.value
    ? `${t('version.releaseNotes')} · ${targetVersionLabel.value}`
    : t('version.releaseNotes'),
)

const changelogDialogSubtitle = computed(() =>
  targetVersionLabel.value
    ? `${t('version.latestVersion')}: ${targetVersionLabel.value}`
    : undefined,
)

const releaseTitle = computed(() => {
  const name = (releaseInfo.value?.name || '').trim()
  if (!name) return ''
  const rawVersion = (latestVersion.value || '').trim()
  if (name === rawVersion || name === targetVersionLabel.value) return ''
  return name
})

const formattedReleaseDate = computed(() =>
  formatPublishedAt(releaseInfo.value?.published_at || ''),
)

const safeReleaseUrl = computed(() => sanitizeUrl(releaseInfo.value?.html_url || ''))

const sanitizedChangelogHtml = computed(() => renderReleaseMarkdown(releaseInfo.value?.body || ''))

const hasChangelogNotes = computed(() => Boolean(sanitizedChangelogHtml.value))

// renderReleaseMarkdown converts GitHub Flavored Markdown into sanitized HTML
// while retaining collapsible details and summary blocks. Passing the original
// non-empty body directly to marked preserves literal code in multi-delimiter
// fences, block quotes, and indented blocks. DOMPurify strips executable tags
// and event handlers, and remaining anchor and image URLs must pass HTTP(S)
// validation before the markup is rendered.
function renderReleaseMarkdown(markdown: string): string {
  if (!markdown.trim()) return ''

  const rawHtml = marked.parse(markdown, {
    async: false,
    gfm: true,
    breaks: true,
  }) as string
  const sanitized = DOMPurify.sanitize(rawHtml, {
    ADD_TAGS: ['details', 'summary'],
    FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form'],
  })

  const template = document.createElement('template')
  template.innerHTML = sanitized

  for (const anchor of Array.from(template.content.querySelectorAll('a'))) {
    const safeHref = sanitizeUrl(anchor.getAttribute('href') || '')
    if (!safeHref) {
      // Remove non-HTTP(S) or placeholder link targets while preserving the anchor
      // label text so unsafe schemes cannot execute on click.
      anchor.removeAttribute('href')
      anchor.removeAttribute('target')
      anchor.removeAttribute('rel')
    } else {
      anchor.setAttribute('href', safeHref)
      anchor.setAttribute('target', '_blank')
      anchor.setAttribute('rel', 'noopener noreferrer')
    }
  }

  for (const image of Array.from(template.content.querySelectorAll('img'))) {
    const safeSrc = sanitizeUrl(image.getAttribute('src') || '')
    if (!safeSrc) {
      // Drop images with invalid or non-HTTP(S) sources so broken placeholders and
      // local paths do not appear inside release notes.
      image.remove()
    } else {
      image.setAttribute('src', safeSrc)
      image.setAttribute('loading', 'lazy')
    }
  }

  return template.innerHTML.trim()
}

// openChangelogModal opens cached release notes after dismissing maintenance
// controls. It focuses the persistent version badge before opening so the shared
// dialog lifecycle can return focus even though the menu trigger is removed.
function openChangelogModal() {
  // The menu is removed during opening, so capture a surviving focus target before
  // BaseDialog records its owner and the menu's leave transition marks controls inert.
  badgeTriggerRef.value?.focus({ preventScroll: true })
  closeDropdown()
  changelogModalOpen.value = true
}

// closeChangelogModal dismisses the changelog dialog while leaving focus
// restoration to BaseDialog's shared dialog lifecycle on transition completion.
function closeChangelogModal() {
  changelogModalOpen.value = false
}

// toggleDropdown switches the visibility of the administrator version panel.
function toggleDropdown() {
  dropdownOpen.value = !dropdownOpen.value
}

// closeDropdown hides the administrator version panel after an outside
// interaction.
function closeDropdown() {
  dropdownOpen.value = false
}

// refreshVersion clears local update and rollback feedback before requesting
// release metadata. Administrators may force refresh; other users return
// immediately and leave the cached application state unchanged.
async function refreshVersion(force = true) {
  if (!isAdmin.value) return

  // Reset update states when refreshing
  changelogModalOpen.value = false
  updateError.value = ''
  updateSuccess.value = false
  needRestart.value = false
  resetRollbackState()

  await appStore.fetchVersion(force)
}

// handleUpdate performs one binary update and records whether the response
// requests restart. Concurrent invocations return immediately; success clears
// version metadata, while failures remain in the local update error state.
async function handleUpdate() {
  if (updating.value) return

  updating.value = true
  updateError.value = ''
  updateSuccess.value = false

  try {
    const result = await performUpdate()
    successKind.value = 'update'
    updateSuccess.value = true
    needRestart.value = result.need_restart
    // Invalidate the cached version after update succeeds so subsequent checks
    // request metadata for the installed binary.
    appStore.clearVersionCache()
  } catch (error: unknown) {
    const err = error as {
      response?: {
        data?: {
          message?: string // Available error message text.
        } // HTTP failure response body.
      } // HTTP failure response details.
      message?: string // Available error message text.
    }
    updateError.value = err.response?.data?.message || err.message || t('version.updateFailed')
  } finally {
    updating.value = false
  }
}

// resetRollbackState clears the rollback list, selection, errors, and active
// deployment tab. It closes the panel so the next opening can request available
// versions for the refreshed installation.
function resetRollbackState() {
  rollbackPanelOpen.value = false
  rollbackVersions.value = []
  rollbackVersionsError.value = ''
  selectedRollbackVersion.value = ''
  rollbackError.value = ''
  manualTab.value = 'script'
}

// toggleRollbackPanel opens or closes rollback controls for administrators.
// Release builds load available versions when the panel first opens with an
// empty list and no existing list request.
async function toggleRollbackPanel() {
  if (!isAdmin.value) return
  rollbackPanelOpen.value = !rollbackPanelOpen.value
  // Load version choices only for release builds because source installations
  // use the Git guidance rendered in the panel.
  if (
    rollbackPanelOpen.value &&
    isReleaseBuild.value &&
    rollbackVersions.value.length === 0 &&
    !rollbackVersionsLoading.value
  ) {
    await loadRollbackVersions()
  }
}

// loadRollbackVersions requests historical release choices for an administrator
// and records list errors locally. An omitted versions array becomes an empty
// list, and the loading marker clears after success or failure.
async function loadRollbackVersions() {
  if (!isAdmin.value) return
  rollbackVersionsLoading.value = true
  rollbackVersionsError.value = ''
  try {
    const data = await getRollbackVersions()
    rollbackVersions.value = data.versions || []
  } catch (error: unknown) {
    const err = error as {
      response?: {
        data?: {
          message?: string // Available error message text.
        } // HTTP failure response body.
      } // HTTP failure response details.
      message?: string // Available error message text.
    }
    rollbackVersionsError.value =
      err.response?.data?.message || err.message || t('version.loadVersionsFailed')
  } finally {
    rollbackVersionsLoading.value = false
  }
}

// selectRollbackVersion toggles the requested release selection and clears the
// previous rollback error. While a rollback request is active, the selected
// version remains fixed until that request finishes.
function selectRollbackVersion(version: string) {
  if (rollingBack.value) return
  rollbackError.value = ''
  selectedRollbackVersion.value = selectedRollbackVersion.value === version ? '' : version
}

// formatPublishedAt formats a valid release timestamp with the active
// application locale. Missing or invalid timestamp text produces an empty label
// so the list can render without a date.
function formatPublishedAt(publishedAt: string): string {
  if (!publishedAt) return ''
  const date = new Date(publishedAt)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleDateString(getLocale())
}

// handleRollback installs the selected historical release for an administrator
// and records its restart requirement. Duplicate or empty selections return
// immediately; successful rollback closes the panel and invalidates cached
// version metadata.
async function handleRollback() {
  if (!isAdmin.value) return
  if (rollingBack.value || !selectedRollbackVersion.value) return

  rollingBack.value = true
  rollbackError.value = ''

  try {
    const result = await rollbackAPI(selectedRollbackVersion.value)
    successKind.value = 'rollback'
    updateSuccess.value = true
    needRestart.value = result.need_restart
    rollbackPanelOpen.value = false
    // Invalidate the cached version after rollback succeeds so subsequent checks
    // request metadata for the installed binary.
    appStore.clearVersionCache()
  } catch (error: unknown) {
    const err = error as {
      response?: {
        data?: {
          message?: string // Available error message text.
        } // HTTP failure response body.
      } // HTTP failure response details.
      message?: string // Available error message text.
    }
    rollbackError.value = err.response?.data?.message || err.message || t('version.rollbackFailed')
  } finally {
    rollingBack.value = false
  }
}

// handleRestart requests a service restart and begins an eight-second countdown
// before health checks. Duplicate calls return while restart is active. A
// rejected request is logged and the countdown still proceeds because the
// restarting service can disconnect its request. Once the timer reaches zero it
// releases the interval and delegates health probing and page reload to
// checkServiceAndReload, which reloads even when the service remains
// unavailable.
async function handleRestart() {
  if (restarting.value) return

  restarting.value = true
  // Leave eight seconds for shutdown and initial service startup.
  restartCountdown.value = 8

  try {
    await restartService()
    // Service will restart, page will reload automatically or show disconnected
  } catch {
    // A restart can disconnect its own request; continue the countdown so the
    // browser still probes health and reloads.
    console.log('Service restarting...')
  }

  // The interval remains active until zero, then releases itself before handing
  // recovery to the health probe loop.
  const countdownInterval = setInterval(() => {
    restartCountdown.value--
    if (restartCountdown.value <= 0) {
      clearInterval(countdownInterval)
      // Try to check if service is back before reload
      checkServiceAndReload()
    }
  }, 1000)
}

// checkServiceAndReload probes service health up to five times and then reloads
// the current page. Successful responses reload immediately; failed requests and
// unsuccessful status codes allow another attempt with a one-second pause
// between attempts.
async function checkServiceAndReload() {
  // Bound health probing before reloading the page for reconnection.
  const maxRetries = 5
  // Space readiness probes by one second during service startup.
  const retryDelay = 1000

  for (let i = 0; i < maxRetries; i++) {
    try {
      const response = await fetch('/health', {
        method: 'GET',
        cache: 'no-cache',
      })
      if (response.ok) {
        // Service is back, reload page
        window.location.reload()
        return
      }
    } catch {
      // A failed health request leaves the countdown recovery loop running so a
      // service that is still starting receives another attempt.
    }

    if (i < maxRetries - 1) {
      await new Promise((resolve) => setTimeout(resolve, retryDelay))
    }
  }

  // Reload after all attempts so the browser can reconnect even when health
  // probes could not observe a successful response.
  window.location.reload()
}

// handleClickOutside closes the version menu for clicks outside its content and
// outside buttons. Opening the changelog already dismisses the menu, so modal
// dismissal does not depend on retaining a menu control for focus restoration.
function handleClickOutside(event: MouseEvent) {
  const target = event.target as Node | null
  const element = target instanceof Element ? target : null
  const button = element?.closest('button')
  if (
    dropdownRef.value &&
    target &&
    !dropdownRef.value.contains(target) &&
    !button?.contains(target)
  ) {
    closeDropdown()
  }
}

onMounted(() => {
  if (isAdmin.value) {
    // Use cached version if available, otherwise fetch
    appStore.fetchVersion(false)
  }
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
/* Rendered release Markdown arrives via v-html, so element typography and details disclosure rules use :deep. */
.changelog-markdown > :deep(:first-child) {
  @apply mt-0;
}

.changelog-markdown > :deep(:last-child) {
  @apply mb-0;
}

.changelog-markdown :deep(h1) {
  @apply mb-3 mt-5 border-b border-gray-100 pb-2 text-lg font-semibold text-gray-900 dark:border-dark-700 dark:text-dark-50;
}

.changelog-markdown :deep(h2) {
  @apply mb-2.5 mt-5 border-b border-gray-100 pb-1.5 text-base font-semibold text-gray-900 dark:border-dark-700 dark:text-dark-50;
}

.changelog-markdown :deep(h3),
.changelog-markdown :deep(h4) {
  @apply mb-2 mt-4 text-sm font-semibold text-gray-900 dark:text-dark-50;
}

.changelog-markdown :deep(p),
.changelog-markdown :deep(ul),
.changelog-markdown :deep(ol),
.changelog-markdown :deep(pre),
.changelog-markdown :deep(blockquote),
.changelog-markdown :deep(table) {
  @apply my-2.5;
}

.changelog-markdown :deep(ul) {
  @apply list-disc space-y-1 pl-5;
}

.changelog-markdown :deep(ol) {
  @apply list-decimal space-y-1 pl-5;
}

.changelog-markdown :deep(li) {
  @apply pl-1 leading-6;
}

.changelog-markdown :deep(li::marker) {
  @apply text-gray-400 dark:text-dark-500;
}

.changelog-markdown :deep(a[href]) {
  @apply font-medium text-primary-600 underline underline-offset-2 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300;
}

.changelog-markdown :deep(strong) {
  @apply font-semibold text-gray-900 dark:text-dark-50;
}

.changelog-markdown :deep(code) {
  @apply rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-800 dark:bg-dark-800 dark:text-dark-100;
}

.changelog-markdown :deep(pre) {
  @apply overflow-x-auto rounded-control border border-gray-200 bg-gray-50 p-3.5 dark:border-dark-600 dark:bg-dark-950;
}

.changelog-markdown :deep(pre code) {
  @apply bg-transparent p-0 text-xs text-gray-800 dark:text-dark-100;
}

.changelog-markdown :deep(blockquote) {
  @apply rounded-r-compact border-l-2 border-primary-500 bg-gray-50 px-3.5 py-2 text-gray-600 dark:border-primary-400 dark:bg-dark-950 dark:text-dark-300;
}

.changelog-markdown :deep(hr) {
  @apply my-4 border-gray-200 dark:border-dark-700;
}

/* Constrain long identifiers and wide tables inside the reading column while preserving local scroll on code blocks. */
.changelog-markdown {
  @apply min-w-0 max-w-full break-words;
}

.changelog-markdown :deep(table) {
  @apply w-full table-fixed overflow-hidden rounded-control border border-gray-200 text-xs dark:border-dark-600;
}

.changelog-markdown :deep(th) {
  @apply break-words border-b border-gray-200 bg-gray-50 px-3 py-2 text-left font-semibold text-gray-900 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-50;
}

.changelog-markdown :deep(td) {
  @apply break-words border-b border-gray-100 px-3 py-2 align-top text-gray-700 dark:border-dark-700 dark:text-dark-200;
}

.changelog-markdown :deep(tr:last-child td) {
  @apply border-b-0;
}

.changelog-markdown :deep(img) {
  @apply my-3 max-w-full rounded-control border border-gray-200 dark:border-dark-600;
}

/* Collapsible details blocks house the Simplified Chinese translation in GitHub Releases. */
.changelog-markdown :deep(details) {
  @apply mt-4 overflow-hidden rounded-surface border border-gray-200 bg-gray-50/80 px-4 py-3 dark:border-dark-600 dark:bg-dark-950;
}

.changelog-markdown :deep(summary) {
  @apply cursor-pointer select-none rounded-compact font-medium text-gray-900 transition-colors hover:text-primary-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40 dark:text-dark-50 dark:hover:text-primary-400;
}

.changelog-markdown :deep(details[open] > summary) {
  @apply mb-3 border-b border-gray-200 pb-2.5 dark:border-dark-600;
}
</style>
