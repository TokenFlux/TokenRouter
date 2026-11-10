<template>
  <BaseDialog
    :show="show"
    :title="t('admin.tlsFingerprintProfiles.title')"
    width="wide"
    @close="$emit('close')"
  >
    <div class="space-y-4">
      <!-- Header actions. -->
      <div class="flex items-center justify-between">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.tlsFingerprintProfiles.description') }}
        </p>
        <button @click="openCreateModal" class="btn btn-primary btn-sm">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('admin.tlsFingerprintProfiles.createProfile') }}
        </button>
      </div>

      <!-- Collector session and captured profile observations. -->
      <div
        class="rounded-surface border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800/60"
      >
        <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <div class="flex items-center gap-2">
              <Icon name="beaker" size="sm" class="text-primary-600 dark:text-primary-400" />
              <h4 class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('admin.tlsFingerprintProfiles.collector.title') }}
              </h4>
              <span
                :class="[
                  'rounded-full px-2 py-0.5 text-xs font-medium',
                  isCollectorRunning
                    ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
                    : 'bg-gray-200 text-gray-600 dark:bg-dark-600 dark:text-gray-300',
                ]"
              >
                {{
                  isCollectorRunning
                    ? t('admin.tlsFingerprintProfiles.collector.running')
                    : t('admin.tlsFingerprintProfiles.collector.stopped')
                }}
              </span>
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintProfiles.collector.description') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="collectorLoading"
              @click="loadCollectorStatus"
            >
              <Icon
                name="refresh"
                size="sm"
                :class="['mr-1', collectorLoading ? 'animate-spin' : '']"
              />
              {{ t('common.refresh') }}
            </button>
            <button
              type="button"
              :class="['btn btn-sm', isCollectorRunning ? 'btn-secondary' : 'btn-primary']"
              :disabled="collectorActionLoading"
              @click="toggleCollector"
            >
              <Icon
                :name="collectorActionLoading ? 'refresh' : isCollectorRunning ? 'x' : 'play'"
                size="sm"
                :class="['mr-1', collectorActionLoading ? 'animate-spin' : '']"
              />
              {{
                isCollectorRunning
                  ? t('admin.tlsFingerprintProfiles.collector.stop')
                  : t('admin.tlsFingerprintProfiles.collector.start')
              }}
            </button>
          </div>
        </div>

        <div class="mt-3 grid gap-3 text-xs sm:grid-cols-3">
          <div>
            <div class="text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintProfiles.collector.listenAddress') }}
            </div>
            <div class="mt-1 font-mono text-gray-900 dark:text-gray-100">
              {{ collectorStatus?.listen_address || '—' }}
            </div>
          </div>
          <div>
            <div class="text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintProfiles.collector.publicBaseURL') }}
            </div>
            <div class="mt-1 break-all font-mono text-gray-900 dark:text-gray-100">
              {{ collectorStatus?.public_base_url || '—' }}
            </div>
          </div>
          <div>
            <div class="text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintProfiles.collector.certificate') }}
            </div>
            <div class="mt-1 text-gray-900 dark:text-gray-100">
              {{
                collectorStatus?.using_generated_cert
                  ? t('admin.tlsFingerprintProfiles.collector.generatedCert')
                  : t('admin.tlsFingerprintProfiles.collector.configuredCert')
              }}
            </div>
          </div>
        </div>

        <p v-if="collectorStatus?.last_error" class="mt-3 text-xs text-red-600 dark:text-red-400">
          {{ collectorStatus.last_error }}
        </p>

        <div v-if="isCollectorRunning" class="mt-4 space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="btn btn-primary btn-sm"
              :disabled="collectorSessionLoading"
              @click="createCollectorSession"
            >
              <Icon name="plus" size="sm" class="mr-1" />
              {{ t('admin.tlsFingerprintProfiles.collector.createSession') }}
            </button>
            <button
              v-if="collectorSession"
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="collectorCapturesLoading"
              @click="refreshCollectorCaptures"
            >
              <Icon
                name="refresh"
                size="sm"
                :class="['mr-1', collectorCapturesLoading ? 'animate-spin' : '']"
              />
              {{ t('admin.tlsFingerprintProfiles.collector.refreshCaptures') }}
            </button>
          </div>

          <div
            v-if="collectorSession"
            class="space-y-3 rounded-surface border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900/60"
          >
            <div class="grid gap-3 text-xs sm:grid-cols-2">
              <div>
                <div class="text-gray-500 dark:text-gray-400">
                  {{ t('admin.tlsFingerprintProfiles.collector.captureURL') }}
                </div>
                <div class="mt-1 break-all font-mono text-gray-900 dark:text-gray-100">
                  {{ collectorSession.capture_url }}
                </div>
              </div>
              <div>
                <div class="text-gray-500 dark:text-gray-400">
                  {{ t('admin.tlsFingerprintProfiles.collector.expiresAt') }}
                </div>
                <div class="mt-1 text-gray-900 dark:text-gray-100">
                  {{ formatDateTime(collectorSession.expires_at) }}
                </div>
              </div>
            </div>

            <div class="grid gap-3 lg:grid-cols-3">
              <div class="min-w-0">
                <div class="mb-1 flex items-center justify-between">
                  <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{
                    t('admin.tlsFingerprintProfiles.collector.claudeCommand')
                  }}</span>
                  <button
                    type="button"
                    class="btn btn-secondary btn-xs"
                    @click="copyText(claudeCommand)"
                  >
                    <Icon name="copy" size="xs" class="mr-1" />
                    {{ t('common.copy') }}
                  </button>
                </div>
                <pre
                  class="max-h-32 overflow-y-auto whitespace-pre-wrap break-all rounded-compact bg-gray-900 p-2 text-xs leading-relaxed text-gray-100"
                  >{{ claudeCommand }}</pre>
              </div>
              <div class="min-w-0">
                <div class="mb-1 flex items-center justify-between">
                  <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{
                    t('admin.tlsFingerprintProfiles.collector.codexCommand')
                  }}</span>
                  <button
                    type="button"
                    class="btn btn-secondary btn-xs"
                    @click="copyText(codexCommand)"
                  >
                    <Icon name="copy" size="xs" class="mr-1" />
                    {{ t('common.copy') }}
                  </button>
                </div>
                <pre
                  class="max-h-32 overflow-y-auto whitespace-pre-wrap break-all rounded-compact bg-gray-900 p-2 text-xs leading-relaxed text-gray-100"
                  >{{ codexCommand }}</pre>
              </div>
              <div class="min-w-0">
                <div class="mb-1 flex items-center justify-between">
                  <span class="text-xs font-medium text-gray-700 dark:text-gray-300">{{
                    t('admin.tlsFingerprintProfiles.collector.codexConfig')
                  }}</span>
                  <button
                    type="button"
                    class="btn btn-secondary btn-xs"
                    @click="copyText(codexConfigSnippet)"
                  >
                    <Icon name="copy" size="xs" class="mr-1" />
                    {{ t('common.copy') }}
                  </button>
                </div>
                <pre
                  class="max-h-32 overflow-y-auto whitespace-pre-wrap break-all rounded-compact bg-gray-900 p-2 text-xs leading-relaxed text-gray-100"
                  >{{ codexConfigSnippet }}</pre>
              </div>
            </div>

            <div v-if="activeCAPEM" class="flex flex-wrap items-center gap-2">
              <button type="button" class="btn btn-secondary btn-sm" @click="copyText(activeCAPEM)">
                <Icon name="copy" size="sm" class="mr-1" />
                {{ t('admin.tlsFingerprintProfiles.collector.copyCA') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" @click="downloadCA">
                <Icon name="download" size="sm" class="mr-1" />
                {{ t('admin.tlsFingerprintProfiles.collector.downloadCA') }}
              </button>
            </div>
          </div>

          <div v-if="collectorSession" class="space-y-2" data-testid="tls-collector-captures">
            <div class="flex items-center justify-between">
              <h5 class="text-xs font-medium uppercase text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintProfiles.collector.captures') }}
              </h5>
              <span class="text-xs text-gray-500 dark:text-gray-400">
                {{
                  t('admin.tlsFingerprintProfiles.collector.captureCount', {
                    count: collectorCaptures.length,
                  })
                }}
              </span>
            </div>
            <div
              v-if="collectorCaptures.length === 0"
              class="rounded-control border border-dashed border-gray-300 px-3 py-4 text-center text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400"
            >
              {{ t('admin.tlsFingerprintProfiles.collector.noCaptures') }}
            </div>
            <div
              v-for="record in collectorCaptures"
              :key="record.id"
              class="rounded-surface border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900/60"
            >
              <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div class="min-w-0 text-xs">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="font-medium text-gray-900 dark:text-white">{{
                      formatClientKind(record.client_kind)
                    }}</span>
                    <span class="font-mono text-gray-500 dark:text-gray-400">{{
                      record.ja3_hash
                    }}</span>
                  </div>
                  <div class="mt-1 break-all text-gray-500 dark:text-gray-400">
                    {{ record.user_agent || '—' }}
                  </div>
                  <div class="mt-1 text-gray-500 dark:text-gray-400">
                    {{ formatDateTime(record.captured_at) }} · {{ record.http_proto || '—' }} · ALPN
                    {{ record.negotiated_alpn || '—' }}
                  </div>
                </div>
                <div class="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    class="btn btn-secondary btn-xs"
                    @click="copyText(record.yaml)"
                  >
                    <Icon name="copy" size="xs" class="mr-1" />
                    {{ t('admin.tlsFingerprintProfiles.collector.copyYaml') }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-primary btn-xs"
                    @click="applyCapture(record)"
                  >
                    <Icon name="check" size="xs" class="mr-1" :animate-on-hover="false" />
                    {{ t('admin.tlsFingerprintProfiles.collector.applyCapture') }}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Saved TLS fingerprint profiles. -->

      <EmptyState
        v-if="!loading && profiles.length === 0"
        :title="t('admin.tlsFingerprintProfiles.noProfiles')"
        :description="t('admin.tlsFingerprintProfiles.createFirstProfile')"
      >
        <template #icon>
          <Icon name="shield" class="empty-state-icon h-10 w-10" />
        </template>
      </EmptyState>

      <div
        v-else
        class="max-h-96 overflow-auto rounded-control border border-gray-200 dark:border-dark-600"
      >
        <table
          :aria-busy="loading"
          class="min-w-full divide-y divide-gray-200 dark:divide-dark-700"
        >
          <thead class="sticky top-0 bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintProfiles.columns.name') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintProfiles.columns.description') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintProfiles.columns.grease') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintProfiles.columns.alpn') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintProfiles.columns.actions') }}
              </th>
            </tr>
          </thead>
          <TableSkeletonBody v-if="loading" :columns="5" cell-class="px-3 py-2" />
          <tbody
            v-else
            class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-800"
          >
            <tr
              v-for="profile in profiles"
              :key="profile.id"
              class="hover:bg-gray-50 dark:hover:bg-dark-700"
            >
              <td class="px-3 py-2">
                <div class="font-medium text-gray-900 dark:text-white text-sm">
                  {{ profile.name }}
                </div>
              </td>
              <td class="px-3 py-2">
                <div
                  v-if="profile.description"
                  class="text-sm text-gray-500 dark:text-gray-400 max-w-xs truncate"
                >
                  {{ profile.description }}
                </div>
                <div v-else class="text-xs text-gray-400 dark:text-gray-600">—</div>
              </td>
              <td class="px-3 py-2">
                <Icon
                  :name="profile.enable_grease ? 'check' : 'lock'"
                  size="sm"
                  :class="profile.enable_grease ? 'text-green-500' : 'text-gray-400'"
                />
              </td>
              <td class="px-3 py-2">
                <div v-if="profile.alpn_protocols?.length" class="flex flex-wrap gap-1">
                  <span
                    v-for="proto in profile.alpn_protocols.slice(0, 3)"
                    :key="proto"
                    class="badge badge-primary text-xs"
                  >
                    {{ proto }}
                  </span>
                  <span v-if="profile.alpn_protocols.length > 3" class="text-xs text-gray-500">
                    +{{ profile.alpn_protocols.length - 3 }}
                  </span>
                </div>
                <div v-else class="text-xs text-gray-400 dark:text-gray-600">—</div>
              </td>
              <td class="px-3 py-2">
                <div class="flex items-center gap-1">
                  <button
                    @click="handleEdit(profile)"
                    class="p-1 text-gray-500 hover:text-primary-600 dark:hover:text-primary-400"
                    :title="t('common.edit')"
                  >
                    <Icon name="edit" size="sm" />
                  </button>
                  <button
                    type="button"
                    @click="handleCopyYaml(profile)"
                    class="p-1 text-gray-500 hover:text-primary-600 dark:hover:text-primary-400"
                    :title="t('admin.tlsFingerprintProfiles.copyYaml')"
                    :aria-label="t('admin.tlsFingerprintProfiles.copyYaml')"
                  >
                    <Icon name="copy" size="sm" />
                  </button>
                  <button
                    @click="handleDelete(profile)"
                    class="p-1 text-gray-500 hover:text-red-600 dark:hover:text-red-400"
                    :title="t('common.delete')"
                  >
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button @click="$emit('close')" class="btn btn-secondary">
          {{ t('common.close') }}
        </button>
      </div>
    </template>

    <!-- Shared profile create and edit dialog. -->
    <BaseDialog
      :show="showCreateModal || showEditModal"
      :title="
        showEditModal
          ? t('admin.tlsFingerprintProfiles.editProfile')
          : t('admin.tlsFingerprintProfiles.createProfile')
      "
      width="wide"
      :z-index="60"
      @close="closeFormModal"
    >
      <form @submit.prevent="handleSubmit" class="space-y-4">
        <!-- Profile YAML input. -->
        <div>
          <label class="input-label">{{ t('admin.tlsFingerprintProfiles.form.pasteYaml') }}</label>
          <textarea
            v-model="yamlInput"
            rows="4"
            class="input font-mono text-xs"
            :placeholder="t('admin.tlsFingerprintProfiles.form.pasteYamlPlaceholder')"
            @paste="handleYamlPaste"
          />
          <div class="mt-1 flex items-center gap-2">
            <button type="button" @click="parseYamlInput" class="btn btn-secondary btn-sm">
              {{ t('admin.tlsFingerprintProfiles.form.parseYaml') }}
            </button>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintProfiles.form.pasteYamlHint') }}
              <a
                href="https://tls.sub2api.org"
                target="_blank"
                rel="noopener noreferrer"
                class="text-link"
                >{{ t('admin.tlsFingerprintProfiles.form.openCollector') }}</a
              >
            </p>
          </div>
        </div>

        <hr class="border-gray-200 dark:border-dark-600" />

        <!-- Profile identity and description. -->
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.tlsFingerprintProfiles.form.name') }}</label>
            <input
              v-model="form.name"
              type="text"
              required
              class="input"
              :placeholder="t('admin.tlsFingerprintProfiles.form.namePlaceholder')"
            />
          </div>
          <div>
            <label class="input-label">{{
              t('admin.tlsFingerprintProfiles.form.description')
            }}</label>
            <input
              v-model="form.description"
              type="text"
              class="input"
              :placeholder="t('admin.tlsFingerprintProfiles.form.descriptionPlaceholder')"
            />
          </div>
        </div>

        <!-- GREASE selection. -->
        <div class="flex items-center gap-3">
          <Toggle v-model="form.enable_grease" size="sm" off-tone="soft" />
          <div>
            <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.tlsFingerprintProfiles.form.enableGrease') }}
            </span>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintProfiles.form.enableGreaseHint') }}
            </p>
          </div>
        </div>

        <!-- Ordered TLS identifier arrays. -->
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.cipherSuites')
            }}</label>
            <textarea
              v-model="fieldInputs.cipher_suites"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'0x1301, 0x1302, 0xc02c'"
            />
            <p class="input-hint text-xs">
              {{ t('admin.tlsFingerprintProfiles.form.cipherSuitesHint') }}
            </p>
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.curves')
            }}</label>
            <textarea
              v-model="fieldInputs.curves"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'29, 23, 24'"
            />
            <p class="input-hint text-xs">
              {{ t('admin.tlsFingerprintProfiles.form.curvesHint') }}
            </p>
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.signatureAlgorithms')
            }}</label>
            <textarea
              v-model="fieldInputs.signature_algorithms"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'0x0403, 0x0804, 0x0401'"
            />
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.supportedVersions')
            }}</label>
            <textarea
              v-model="fieldInputs.supported_versions"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'0x0304, 0x0303'"
            />
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.keyShareGroups')
            }}</label>
            <textarea
              v-model="fieldInputs.key_share_groups"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'29, 23'"
            />
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.extensions')
            }}</label>
            <textarea
              v-model="fieldInputs.extensions"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'0x0000, 0x0005, 0x000a'"
            />
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.pointFormats')
            }}</label>
            <textarea
              v-model="fieldInputs.point_formats"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'0'"
            />
          </div>

          <div>
            <label class="input-label text-xs">{{
              t('admin.tlsFingerprintProfiles.form.pskModes')
            }}</label>
            <textarea
              v-model="fieldInputs.psk_modes"
              rows="2"
              class="input font-mono text-xs"
              :placeholder="'1'"
            />
          </div>
        </div>

        <!-- Advertised ALPN protocol list. -->
        <div>
          <label class="input-label text-xs">{{
            t('admin.tlsFingerprintProfiles.form.alpnProtocols')
          }}</label>
          <textarea
            v-model="fieldInputs.alpn_protocols"
            rows="2"
            class="input font-mono text-xs"
            :placeholder="'h2, http/1.1'"
          />
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeFormModal" type="button" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button @click="handleSubmit" :disabled="submitting" class="btn btn-primary">
            <Icon
              v-if="submitting"
              name="refresh"
              size="sm"
              class="mr-1 animate-spin"
              :animate-on-hover="false"
            />
            {{ showEditModal ? t('common.update') : t('common.create') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Deletion confirmation. -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.tlsFingerprintProfiles.deleteProfile')"
      :message="
        t('admin.tlsFingerprintProfiles.deleteConfirmMessage', { name: deletingProfile?.name })
      "
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import Toggle from '@/components/common/Toggle.vue'
import { computed, onUnmounted, ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type {
  TLSFingerprintCaptureRecord,
  TLSFingerprintCollectorSession,
  TLSFingerprintCollectorStatus,
  TLSFingerprintProfile,
} from '@/api/admin/tlsFingerprintProfile'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import EmptyState from '@/components/common/EmptyState.vue'

/**
 * TLSFingerprintProfilesModal.vue manages TLS fingerprint profiles and collector sessions used
 * to capture candidate profiles from clients. Profile arrays are edited as comma-separated
 * text, then parsed into numeric or protocol arrays for persistence. Captures can populate a
 * create draft, while exported YAML carries the same recognized profile fields for manual
 * reuse.
 *
 * Collector sessions own a token used by periodic capture queries. Starting polling replaces
 * the interval and performs an immediate query; hiding or unmounting stops future ticks but
 * does not abort an outstanding query. The YAML reader accepts a limited flat field syntax
 * rather than general YAML, and it updates draft fields as it encounters them. Successful
 * profile writes close and reset the draft before list refresh.
 */

/**
 * props controls the profile-management dialog; opening refreshes profiles and collector status
 * while hiding releases the local polling interval.
 */
const props = defineProps<{
  show: boolean // parent-controlled profiles dialog visibility
}>()

/**
 * emit requests outer-dialog closure from the parent, while profile editors and collector state
 * remain owned by this component.
 */
const emit = defineEmits<{
  close: [] // request parent dialog closure
}>()

void emit // The template emits close through $emit; retain this script reference for type checking.

/**
 * Translation and application toasts describe profile operations, YAML parsing, clipboard
 * actions, and collector request outcomes within the dialog.
 */
const { t } = useI18n()
const appStore = useAppStore()

/**
 * Profile records, editor flags, selected records, collector observations, request flags, and
 * the polling handle belong to the mounted management dialog.
 */
const profiles = ref<TLSFingerprintProfile[]>([])
const loading = ref(false)
const submitting = ref(false)
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const editingProfile = ref<TLSFingerprintProfile | null>(null)
const deletingProfile = ref<TLSFingerprintProfile | null>(null)
const yamlInput = ref('')
const collectorStatus = ref<TLSFingerprintCollectorStatus | null>(null)
const collectorSession = ref<TLSFingerprintCollectorSession | null>(null)
const collectorCaptures = ref<TLSFingerprintCaptureRecord[]>([])
const collectorLoading = ref(false)
const collectorActionLoading = ref(false)
const collectorSessionLoading = ref(false)
const collectorCapturesLoading = ref(false)
let collectorPollTimer: number | null = null

/**
 * fieldInputs keeps ordered TLS arrays as editable comma-separated strings; submission parses
 * them according to each field numeric or protocol representation.
 */
const fieldInputs = reactive({
  cipher_suites: '', // ordered cipher suite identifiers
  curves: '', // ordered supported curve identifiers
  point_formats: '', // ordered point format values
  signature_algorithms: '', // ordered signature algorithm identifiers
  alpn_protocols: '', // ordered advertised ALPN protocols
  supported_versions: '', // ordered supported TLS versions
  key_share_groups: '', // ordered key share groups
  psk_modes: '', // ordered PSK exchange modes
  extensions: '', // ordered TLS extension identifiers
})

/**
 * form owns profile metadata and GREASE selection independently of array text, while
 * collector-derived drafts populate both representations before persistence.
 */
const form = reactive({
  name: '', // editable profile display name
  description: null as string | null, // optional profile explanatory text
  enable_grease: false, // insert GREASE values preference
})

/**
 * Collector presentation derives running state and certificate text, preferring the active
 * session certificate before collector-level metadata for command and download examples.
 */
const isCollectorRunning = computed(() => Boolean(collectorStatus.value?.running))
const activeCAPEM = computed(
  () => collectorSession.value?.ca_pem || collectorStatus.value?.ca_pem || '',
)
/**
 * Client command examples target the active capture session; Claude includes its session token,
 * while Codex snippets configure collector base URLs and optional CA location.
 */
const claudeCommand = computed(() => {
  if (!collectorSession.value) return ''
  const tlsPrefix = activeCAPEM.value ? 'NODE_TLS_REJECT_UNAUTHORIZED=0 ' : ''
  const settings = {
    env: {
      ANTHROPIC_BASE_URL: collectorSession.value.capture_url,
      ANTHROPIC_AUTH_TOKEN: collectorSession.value.token,
    },
  }
  // Put collector settings in the command-specific settings object so the example supplies the
  // capture URL and token together to Claude Code.
  return `${tlsPrefix}claude --settings '${JSON.stringify(settings)}' "test"`
})
/**
 * Codex command and configuration snippets direct both OpenAI and ChatGPT requests to the
 * active collector URL using the displayed certificate setup when available.
 */
const codexCommand = computed(() => {
  if (!collectorSession.value) return ''
  const caPrefix = activeCAPEM.value
    ? 'CODEX_CA_CERTIFICATE=/path/to/tokenrouter-tls-collector-ca.pem '
    : ''
  const captureURL = collectorSession.value.capture_url
  return `${caPrefix}codex -c 'openai_base_url="${captureURL}"' -c 'chatgpt_base_url="${captureURL}"'`
})
const codexConfigSnippet = computed(() => {
  if (!collectorSession.value) return ''
  return [
    `openai_base_url = "${collectorSession.value.capture_url}"`,
    `chatgpt_base_url = "${collectorSession.value.capture_url}"`,
  ].join('\n')
})

// Refresh profile and collector observations on open, and release the polling interval on hide
// while already-issued requests finish independently.
watch(
  () => props.show,
  (newVal) => {
    if (newVal) {
      loadProfiles()
      loadCollectorStatus()
    } else {
      stopCollectorPolling()
    }
  },
  { immediate: true },
)

onUnmounted(() => {
  stopCollectorPolling()
})

/**
 * loadProfiles refreshes saved TLS fingerprint profiles for the list. Failure reports a toast
 * and retains existing records, while the loading flag clears when the request settles.
 */
async function loadProfiles() {
  loading.value = true
  try {
    profiles.value = await adminAPI.tlsFingerprintProfiles.list()
  } catch (error) {
    appStore.showError(t('admin.tlsFingerprintProfiles.loadFailed'))
    console.error('Error loading TLS fingerprint profiles:', error)
  } finally {
    loading.value = false
  }
}

/**
 * resetForm clears profile identity, description, GREASE selection, array editors, and pasted
 * YAML.
 */
const resetForm = () => {
  form.name = ''
  form.description = null
  form.enable_grease = false
  fieldInputs.cipher_suites = ''
  fieldInputs.curves = ''
  fieldInputs.point_formats = ''
  fieldInputs.signature_algorithms = ''
  fieldInputs.alpn_protocols = ''
  fieldInputs.supported_versions = ''
  fieldInputs.key_share_groups = ''
  fieldInputs.psk_modes = ''
  fieldInputs.extensions = ''
  yamlInput.value = ''
}

/**
 * openCreateModal resets the shared profile draft and opens the create dialog.
 */
const openCreateModal = () => {
  resetForm()
  showCreateModal.value = true
}

/**
 * loadCollectorStatus refreshes collector availability and certificate metadata. Failure
 * reports an error while retaining the previous status, and collector-loading feedback clears
 * after the status request settles.
 */
async function loadCollectorStatus() {
  collectorLoading.value = true
  try {
    collectorStatus.value = await adminAPI.tlsFingerprintProfiles.collectorStatus()
  } catch (error) {
    appStore.showError(t('admin.tlsFingerprintProfiles.collector.statusFailed'))
    console.error('Error loading TLS fingerprint collector status:', error)
  } finally {
    collectorLoading.value = false
  }
}

/**
 * toggleCollector starts or stops the collector according to the current status. Successful
 * stop clears session and captures and releases polling; both paths refresh status before the
 * action flag clears.
 */
const toggleCollector = async () => {
  collectorActionLoading.value = true
  try {
    if (isCollectorRunning.value) {
      await adminAPI.tlsFingerprintProfiles.stopCollector()
      // Clear session-dependent observations after the collector stops, then release the
      // interval so subsequent ticks cannot query the discarded session token.
      collectorSession.value = null
      collectorCaptures.value = []
      stopCollectorPolling()
      appStore.showSuccess(t('admin.tlsFingerprintProfiles.collector.stopSuccess'))
    } else {
      collectorStatus.value = await adminAPI.tlsFingerprintProfiles.startCollector()
      appStore.showSuccess(t('admin.tlsFingerprintProfiles.collector.startSuccess'))
    }
    await loadCollectorStatus()
  } catch (error: any) {
    const message =
      error?.message ||
      error?.response?.data?.detail ||
      t('admin.tlsFingerprintProfiles.collector.actionFailed')
    appStore.showError(message)
    console.error('Error toggling TLS fingerprint collector:', error)
  } finally {
    collectorActionLoading.value = false
  }
}

/**
 * createCollectorSession creates a capture session, clears prior captures, and begins polling
 * with the returned token. Failure retains the previous session and reports an error; the
 * session action flag clears after settlement.
 */
const createCollectorSession = async () => {
  collectorSessionLoading.value = true
  try {
    collectorSession.value = await adminAPI.tlsFingerprintProfiles.createCollectorSession()
    collectorCaptures.value = []
    startCollectorPolling()
    appStore.showSuccess(t('admin.tlsFingerprintProfiles.collector.sessionCreated'))
  } catch (error: any) {
    const message =
      error?.message ||
      error?.response?.data?.detail ||
      t('admin.tlsFingerprintProfiles.collector.sessionFailed')
    appStore.showError(message)
    console.error('Error creating TLS fingerprint collector session:', error)
  } finally {
    collectorSessionLoading.value = false
  }
}

/**
 * refreshCollectorCaptures fetches captures for the currently available session token. Failures
 * are logged while prior captures remain visible; calls can overlap because the loading flag is
 * feedback rather than a dispatch lock.
 */
const refreshCollectorCaptures = async () => {
  if (!collectorSession.value) return
  collectorCapturesLoading.value = true
  try {
    // Each response replaces the current capture list; the polling interval may have dispatched
    // another request before this one settles.
    collectorCaptures.value = await adminAPI.tlsFingerprintProfiles.listCollectorCaptures(
      collectorSession.value.token,
    )
  } catch (error: any) {
    console.error('Error loading TLS fingerprint captures:', error)
  } finally {
    collectorCapturesLoading.value = false
  }
}

/**
 * startCollectorPolling replaces the current interval and requests captures immediately, then
 * every three seconds. Interval cleanup stops future dispatch but does not cancel requests
 * already in progress or guard late responses.
 */
const startCollectorPolling = () => {
  stopCollectorPolling()
  // Replace the prior interval before dispatching this session so only one future tick source
  // remains owned by the dialog.
  collectorPollTimer = window.setInterval(() => {
    refreshCollectorCaptures()
  }, 3000)
  refreshCollectorCaptures()
}

/**
 * stopCollectorPolling clears the active interval and its handle when present. Hiding,
 * unmounting, and stopping the collector call it to prevent future polling ticks while
 * outstanding requests settle independently.
 */
function stopCollectorPolling() {
  if (collectorPollTimer) {
    // Stop future capture requests when ownership ends; already dispatched promises still
    // follow their normal response publication path.
    window.clearInterval(collectorPollTimer)
    collectorPollTimer = null
  }
}

/**
 * fillFormFromProfile copies profile values into the editable draft and formats arrays as text.
 * TLS identifiers use hexadecimal where configured, while byte-like and group values use their
 * plain numeric representation.
 */
const fillFormFromProfile = (profile: TLSFingerprintProfile) => {
  form.name = profile.name
  form.description = profile.description
  form.enable_grease = profile.enable_grease
  fieldInputs.cipher_suites = formatNumericArray(profile.cipher_suites)
  fieldInputs.curves = formatPlainNumericArray(profile.curves)
  fieldInputs.point_formats = formatPlainNumericArray(profile.point_formats)
  fieldInputs.signature_algorithms = formatNumericArray(profile.signature_algorithms)
  fieldInputs.alpn_protocols = (profile.alpn_protocols ?? []).join(', ')
  fieldInputs.supported_versions = formatNumericArray(profile.supported_versions)
  fieldInputs.key_share_groups = formatPlainNumericArray(profile.key_share_groups)
  fieldInputs.psk_modes = formatPlainNumericArray(profile.psk_modes)
  fieldInputs.extensions = formatNumericArray(profile.extensions)
}

/**
 * applyCapture fills a new-profile draft from a capture that includes profile data. It retains
 * the capture YAML, clears edit ownership, and opens creation so saving creates a profile from
 * the observation.
 */
const applyCapture = (record: TLSFingerprintCaptureRecord) => {
  if (!record.profile) return
  fillFormFromProfile(record.profile)
  yamlInput.value = record.yaml || ''
  editingProfile.value = null
  showEditModal.value = false
  showCreateModal.value = true
  appStore.showSuccess(t('admin.tlsFingerprintProfiles.collector.applied'))
}

/**
 * copyText writes nonempty text to the browser clipboard and reports the result. Clipboard
 * rejection produces a localized error toast while leaving the source text available in the
 * collector or profile view.
 */
const copyText = async (text: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    appStore.showSuccess(t('admin.tlsFingerprintProfiles.collector.copied'))
  } catch (error) {
    appStore.showError(t('admin.tlsFingerprintProfiles.collector.copyFailed'))
    console.error('Error copying text:', error)
  }
}

/**
 * downloadCA downloads the currently selected collector CA as a PEM file. It creates a
 * temporary Blob URL and anchor, triggers the download, then removes the anchor and revokes the
 * URL.
 */
const downloadCA = () => {
  if (!activeCAPEM.value) return
  const blob = new Blob([activeCAPEM.value], { type: 'application/x-pem-file' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'tokenrouter-tls-collector-ca.pem'
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  // Release the temporary download resource after the synthetic click so repeated certificate
  // downloads do not retain Blob URL allocations.
  URL.revokeObjectURL(url)
}

/**
 * formatDateTime renders a present collector timestamp with the browser locale. Missing values
 * use an em dash, while date parsing behavior is left to the browser Date implementation.
 */
const formatDateTime = (value?: string) => {
  if (!value) return '—'
  return new Date(value).toLocaleString()
}

/**
 * formatClientKind labels recognized Claude Code and Codex client captures. Other identifiers
 * receive the localized unknown-client label so captures from an unrecognized client still have
 * display text.
 */
const formatClientKind = (kind: string) => {
  if (kind === 'claude_code') return 'Claude Code'
  if (kind === 'codex') return 'Codex CLI'
  return t('admin.tlsFingerprintProfiles.collector.unknownClient')
}

/**
 * parseYamlInput applies recognized flat profile fields from the pasted YAML-like text to the
 * current draft. It skips blank lines, comments, and unmatched structures, accepts inline
 * comma-separated arrays, and parses simple quoted scalars. A nonempty parsed name determines
 * the success toast.
 *
 * Assignments occur while lines are scanned, so a missing name can report failure after other
 * recognized fields have already changed. Numeric-array text is retained for later numeric
 * parsing during submission. This reader supports the profile exporter structure and simple
 * collector output, not arbitrary nested YAML, multiline scalars, or general quoted-array
 * parsing.
 */
const parseYamlInput = () => {
  const text = yamlInput.value.trim()
  if (!text) return

  // Read recognized flat field lines from the export shape; unsupported YAML structures are
  // skipped instead of interpreted as nested configuration.
  const lines = text.split('\n')

  let foundName = false

  for (const line of lines) {
    const trimmed = line.trim()
    // Empty lines and YAML comments contain no profile fields.
    if (!trimmed || trimmed.startsWith('#')) continue

    // Accept scalar or inline-array values on a single field line.
    const match = trimmed.match(/^(\w+):\s*(.+)$/)
    if (!match) continue

    const [, key, rawValue] = match
    const value = rawValue.trim()

    switch (key) {
      case 'name': {
        // Decode the exported scalar before deciding whether a usable profile name was found,
        // keeping quoted names in their editable text representation.
        const unquoted = parseYamlScalar(value)
        if (unquoted) {
          form.name = unquoted
          foundName = true
        }
        break
      }
      case 'description':
        form.description = parseYamlScalar(value) || null
        break
      case 'enable_grease':
        form.enable_grease = value === 'true'
        break
      case 'cipher_suites':
      case 'curves':
      case 'point_formats':
      case 'signature_algorithms':
      case 'supported_versions':
      case 'key_share_groups':
      case 'psk_modes':
      case 'extensions': {
        // Keep numeric array tokens as editor text here; submission later applies the decimal
        // or hexadecimal parser to build the API array.
        const arrMatch = value.match(/^\[(.*)?\]$/)
        if (arrMatch) {
          const inner = arrMatch[1] || ''
          fieldInputs[key as keyof typeof fieldInputs] = inner
            .split(',')
            .map((s) => s.trim())
            .filter((s) => s.length > 0)
            .join(', ')
        }
        break
      }
      case 'alpn_protocols': {
        // Convert the simple inline protocol array to comma-separated editor text; this limited
        // reader treats commas as separators even inside quoted values.
        const arrMatch = value.match(/^\[(.*)?\]$/)
        if (arrMatch) {
          const inner = arrMatch[1] || ''
          fieldInputs.alpn_protocols = inner
            .split(',')
            .map((s) => s.trim().replace(/^["']|["']$/g, ''))
            .filter((s) => s.length > 0)
            .join(', ')
        }
        break
      }
    }
  }

  if (foundName) {
    appStore.showSuccess(t('admin.tlsFingerprintProfiles.form.yamlParsed'))
  } else {
    appStore.showError(t('admin.tlsFingerprintProfiles.form.yamlParseFailed'))
  }
}

/**
 * handleYamlPaste schedules parsing fifty milliseconds after the paste event. The delay lets
 * v-model receive pasted text before the parser reads yamlInput; the timeout is not retained
 * for cancellation.
 */
const handleYamlPaste = () => {
  setTimeout(() => parseYamlInput(), 50)
}

/**
 * closeFormModal closes both profile editors, clears edit selection, and resets their shared
 * draft. Successful writes invoke it before refreshing the profile list, while failed writes
 * leave the draft available.
 */
const closeFormModal = () => {
  showCreateModal.value = false
  showEditModal.value = false
  editingProfile.value = null
  resetForm()
}

/**
 * parseNumericArray parses comma-separated decimal or hexadecimal integers and drops NaN
 * entries. Blank input produces an empty array; numeric-prefix parsing follows parseInt and
 * does not validate the entire token.
 */
const parseNumericArray = (input: string): number[] => {
  if (!input.trim()) return []
  return input
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
    .map((s) => (s.startsWith('0x') || s.startsWith('0X') ? parseInt(s, 16) : parseInt(s, 10)))
    .filter((n) => !isNaN(n))
}

/**
 * parseStringArray returns trimmed nonempty comma-separated protocol strings. Blank input
 * produces an empty array, and ordering is retained for the fingerprint fields that consume
 * this editor representation.
 */
const parseStringArray = (input: string): string[] => {
  if (!input.trim()) return []
  return input
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

/**
 * formatHex formats a numeric TLS identifier as at least four hexadecimal digits.
 */
const formatHex = (n: number): string => '0x' + n.toString(16).padStart(4, '0')

/**
 * formatNumericArray renders numeric TLS identifiers as comma-separated hexadecimal text. Null
 * or undefined arrays become empty text, matching the array editor representation used when a
 * profile field is absent.
 */
const formatNumericArray = (arr: number[] | null | undefined): string =>
  (arr ?? []).map(formatHex).join(', ')

/**
 * formatPlainNumericArray joins numeric values using their ordinary decimal representation.
 * Missing arrays become empty text, matching the byte-like and group fields that use plain
 * numeric editing.
 */
const formatPlainNumericArray = (arr: number[] | null | undefined): string => (arr ?? []).join(', ')

/**
 * parseYamlScalar reads simple quoted or plain scalar text and treats empty, null, or tilde as
 * empty. Double quotes use JSON decoding with outer-quote removal on failure; single quotes
 * collapse doubled apostrophes.
 */
const parseYamlScalar = (value: string): string => {
  const trimmed = value.trim()
  if (!trimmed || trimmed === 'null' || trimmed === '~') return ''

  if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      return JSON.parse(trimmed)
    } catch {
      // If quoted text is not valid JSON, keep its inner text available for manual correction
      // instead of failing the entire paste.
      return trimmed.replace(/^"|"$/g, '')
    }
  }

  if (trimmed.startsWith("'") && trimmed.endsWith("'")) {
    return trimmed.slice(1, -1).replace(/''/g, "'")
  }

  return trimmed
}

/**
 * formatYamlString encodes profile text as a JSON-quoted YAML scalar.
 */
const formatYamlString = (value: string): string => JSON.stringify(value)

/**
 * formatYamlNumericArray exports a numeric array with a caller-selected element formatter.
 * Hexadecimal is the default, and missing arrays become empty bracketed lists for the profile
 * export fields.
 */
const formatYamlNumericArray = (
  arr: number[] | null | undefined,
  formatter: (n: number) => string = formatHex,
): string => {
  return `[${(arr ?? []).map(formatter).join(', ')}]`
}

/**
 * formatYamlStringArray exports protocol strings as a bracketed list of JSON-quoted values.
 * Missing arrays produce an empty list so the exported profile retains its expected field
 * structure.
 */
const formatYamlStringArray = (arr: string[] | null | undefined): string => {
  return `[${(arr ?? []).map(formatYamlString).join(', ')}]`
}

/**
 * formatDecimal renders a numeric profile field as ordinary decimal text.
 */
const formatDecimal = (n: number): string => String(n)

/**
 * buildProfileYaml exports the recognized profile fields using quoted scalars and inline
 * arrays. Numeric identifiers retain each field formatter, and null arrays become empty lists
 * for reuse through the paste editor.
 */
const buildProfileYaml = (profile: TLSFingerprintProfile): string => {
  return [
    'tls_fingerprint_profile:',
    `  name: ${formatYamlString(profile.name)}`,
    `  description: ${formatYamlString(profile.description || '')}`,
    `  enable_grease: ${profile.enable_grease ? 'true' : 'false'}`,
    `  cipher_suites: ${formatYamlNumericArray(profile.cipher_suites)}`,
    `  curves: ${formatYamlNumericArray(profile.curves, formatDecimal)}`,
    `  point_formats: ${formatYamlNumericArray(profile.point_formats, formatDecimal)}`,
    `  signature_algorithms: ${formatYamlNumericArray(profile.signature_algorithms)}`,
    `  alpn_protocols: ${formatYamlStringArray(profile.alpn_protocols)}`,
    `  supported_versions: ${formatYamlNumericArray(profile.supported_versions)}`,
    `  key_share_groups: ${formatYamlNumericArray(profile.key_share_groups, formatDecimal)}`,
    `  psk_modes: ${formatYamlNumericArray(profile.psk_modes, formatDecimal)}`,
    `  extensions: ${formatYamlNumericArray(profile.extensions)}`,
  ].join('\n')
}

/**
 * handleCopyYaml exports the selected profile and copies its YAML text.
 */
const handleCopyYaml = (profile: TLSFingerprintProfile) => {
  copyText(buildProfileYaml(profile))
}

/**
 * handleEdit selects a saved profile, fills the shared draft, and opens editing.
 */
const handleEdit = (profile: TLSFingerprintProfile) => {
  editingProfile.value = profile
  fillFormFromProfile(profile)
  showEditModal.value = true
}

/**
 * handleDelete selects a saved profile and opens its deletion confirmation.
 */
const handleDelete = (profile: TLSFingerprintProfile) => {
  deletingProfile.value = profile
  showDeleteDialog.value = true
}

/**
 * handleSubmit validates a nonblank name, parses editor arrays, and creates or updates the
 * selected profile. Success closes and resets the draft before reloading profiles; failure
 * retains input and reports server detail.
 */
const handleSubmit = async () => {
  if (!form.name.trim()) {
    appStore.showError(t('admin.tlsFingerprintProfiles.form.name') + ' ' + t('common.required'))
    return
  }

  submitting.value = true
  try {
    const data = {
      name: form.name.trim(),
      description: form.description?.trim() || null,
      enable_grease: form.enable_grease,
      cipher_suites: parseNumericArray(fieldInputs.cipher_suites),
      curves: parseNumericArray(fieldInputs.curves),
      point_formats: parseNumericArray(fieldInputs.point_formats),
      signature_algorithms: parseNumericArray(fieldInputs.signature_algorithms),
      alpn_protocols: parseStringArray(fieldInputs.alpn_protocols),
      supported_versions: parseNumericArray(fieldInputs.supported_versions),
      key_share_groups: parseNumericArray(fieldInputs.key_share_groups),
      psk_modes: parseNumericArray(fieldInputs.psk_modes),
      extensions: parseNumericArray(fieldInputs.extensions),
    }

    if (showEditModal.value && editingProfile.value) {
      await adminAPI.tlsFingerprintProfiles.update(editingProfile.value.id, data)
      appStore.showSuccess(t('admin.tlsFingerprintProfiles.updateSuccess'))
    } else {
      await adminAPI.tlsFingerprintProfiles.create(data)
      appStore.showSuccess(t('admin.tlsFingerprintProfiles.createSuccess'))
    }

    closeFormModal()
    loadProfiles()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.tlsFingerprintProfiles.saveFailed'))
    console.error('Error saving TLS fingerprint profile:', error)
  } finally {
    submitting.value = false
  }
}

/**
 * confirmDelete deletes the selected profile before clearing confirmation and refreshing the
 * list. Request failure retains the confirmation target and reports feedback so the
 * administrator can inspect or retry.
 */
const confirmDelete = async () => {
  if (!deletingProfile.value) return

  try {
    await adminAPI.tlsFingerprintProfiles.delete(deletingProfile.value.id)
    appStore.showSuccess(t('admin.tlsFingerprintProfiles.deleteSuccess'))
    showDeleteDialog.value = false
    deletingProfile.value = null
    loadProfiles()
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail || t('admin.tlsFingerprintProfiles.deleteFailed'),
    )
    console.error('Error deleting TLS fingerprint profile:', error)
  }
}
</script>
