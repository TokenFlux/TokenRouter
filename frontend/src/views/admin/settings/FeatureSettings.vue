<template>
  <SettingsCard
    :title="t('admin.settings.features.team.title')"
    :description="t('admin.settings.features.team.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="team-enabled"
        v-model="form.team_enabled"
        :label="t('admin.settings.features.team.enabled')"
        :hint="t('admin.settings.features.team.enabledHint')"
      />
    </SettingsSection>
  </SettingsCard>

  <!-- Creative workspace settings. -->
  <SettingsCard
    :title="t('admin.settings.features.creative.title')"
    :description="t('admin.settings.features.creative.description')"
  >
    <SettingsSection>
      <SettingToggleRow
        id="creative-enabled"
        v-model="form.creative_enabled"
        :label="t('admin.settings.features.creative.enabled')"
        :hint="t('admin.settings.features.creative.enabledHint')"
      />
      <SettingRow
        id="creative-worker-count"
        field
        label-for="creative-worker-count"
        :label="t('admin.settings.features.creative.workerCount')"
        :hint="t('admin.settings.features.creative.workerCountHint')"
      >
        <input
          id="creative-worker-count"
          v-model.number="form.creative_worker_count"
          type="number"
          min="1"
          step="1"
          required
          class="input"
        />
      </SettingRow>
      <!--
        Usage combines the controller status snapshot with draft capacity when runtime
        capacity is unavailable, keeping unsaved worker changes previewable.
      -->
      <div>
        <p class="input-label">{{ t('admin.settings.features.creative.workerUsage') }}</p>
        <div class="flex items-center gap-3">
          <div class="h-2.5 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
            <div
              class="h-full rounded-full bg-emerald-500 transition-[width,background-color] duration-layout"
              :style="{ width: `${creativeWorkerUsagePercent}%` }"
            ></div>
          </div>
          <span class="shrink-0 text-sm tabular-nums text-gray-900 dark:text-dark-50">
            {{ creativeWorkerUsageText }}
          </span>
        </div>
      </div>
    </SettingsSection>

    <SettingsSection>
      <RuleListEditor
        :items="form.creative_model_settings"
        title-style="section"
        :title="t('admin.settings.features.creative.modelSettings.title')"
        :hint="t('admin.settings.features.creative.modelSettings.description')"
        :add-label="t('admin.settings.features.creative.modelSettings.add')"
        :remove-label="t('admin.settings.features.creative.modelSettings.remove')"
        :empty-text="t('admin.settings.features.creative.modelSettings.empty')"
        :add-disabled="
          creativeModelCandidatesLoading ||
          !creativeModelCandidates.some(
            (candidate) =>
              !form.creative_model_settings.some(
                (item) => creativeModelSettingKey(item) === creativeModelSettingKey(candidate),
              ),
          )
        "
        test-id="creative-model-settings"
        @add="addCreativeModelSetting"
        @remove="removeCreativeModelSetting"
      >
        <template #header-extra>
          <p v-if="creativeModelCandidatesLoading" class="input-hint">
            {{ t('admin.settings.features.creative.modelSettings.loading') }}
          </p>
          <SettingsNotice v-else-if="creativeModelCandidatesError" tone="warning">
            {{ t('admin.settings.features.creative.modelSettings.loadError') }}
          </SettingsNotice>
          <div
            class="hidden grid-cols-[minmax(0,1fr)_auto] items-center gap-2 text-xs font-medium text-gray-500 sm:grid dark:text-dark-300"
          >
            <div class="grid grid-cols-2 items-center gap-4">
              <span>{{ t('admin.settings.features.creative.modelSettings.modelColumn') }}</span>
              <span>{{
                t('admin.settings.features.creative.modelSettings.operationsColumn')
              }}</span>
            </div>
            <span class="w-9" aria-hidden="true"></span>
          </div>
        </template>
        <template #row="{ item, index }">
          <div class="grid grid-cols-1 items-center gap-3 sm:grid-cols-2 sm:gap-4">
            <div class="min-w-0">
              <Select
                :model-value="creativeModelSettingKey(item)"
                :options="creativeModelOptionsForRow(index)"
                :placeholder="t('admin.settings.features.creative.modelSettings.selectModel')"
                :aria-label="t('admin.settings.features.creative.modelSettings.modelColumn')"
                :searchable="'auto'"
                class="w-full sm:max-w-xs"
                @change="onCreativeModelSelected(index, $event)"
              />
              <p
                v-if="!creativeCandidateForSetting(item)"
                class="input-hint text-amber-600 dark:text-amber-400"
              >
                {{ t('admin.settings.features.creative.modelSettings.unavailableHint') }}
              </p>
            </div>
            <!--
              The disabled rule prevents removing the last selected operation; click
              handlers rely on that guard when changing the operation list.
            -->
            <div class="flex flex-wrap items-center gap-2">
              <button
                v-for="operation in creativeOperationChoices"
                :key="operation"
                type="button"
                class="inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition-colors"
                :class="[
                  item.operations.includes(operation)
                    ? 'border-primary-500/60 bg-primary-50 text-primary-700 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                    : 'border-gray-200 text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-600 dark:text-dark-300 dark:hover:border-dark-400 dark:hover:text-dark-100',
                  creativeOperationCheckboxDisabled(index, operation) &&
                    'cursor-not-allowed opacity-50',
                ]"
                :disabled="creativeOperationCheckboxDisabled(index, operation)"
                :aria-pressed="item.operations.includes(operation)"
                @click="
                  toggleCreativeOperation(index, operation, !item.operations.includes(operation))
                "
              >
                <Icon
                  v-if="item.operations.includes(operation)"
                  name="check"
                  size="xs"
                  :animate-on-hover="false"
                />
                {{ t(`admin.settings.features.creative.modelSettings.operations.${operation}`) }}
              </button>
            </div>
          </div>
        </template>
      </RuleListEditor>
    </SettingsSection>
  </SettingsCard>

  <!-- Affiliate rebate settings. -->
  <SettingsCard
    :title="t('admin.settings.features.affiliate.title')"
    :description="
      t('admin.settings.features.affiliate.description', { unitName: previewBalanceUnitName })
    "
  >
    <SettingsSection>
      <SettingToggleRow
        id="affiliate-enabled"
        v-model="form.affiliate_enabled"
        :label="t('admin.settings.features.affiliate.enabled')"
        :hint="t('admin.settings.features.affiliate.enabledHint')"
      />
      <Collapse :open="form.affiliate_enabled">
        <SettingsSubpanel>
          <SettingToggleRow
            id="affiliate-admin-recharge-enabled"
            v-model="form.affiliate_admin_recharge_enabled"
            :label="t('admin.settings.features.affiliate.adminRechargeRebate')"
            :hint="t('admin.settings.features.affiliate.adminRechargeRebateHint')"
          />
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="affiliate-rebate-rate" class="input-label">
                {{ t('admin.settings.features.affiliate.rebateRate') }}
              </label>
              <div class="input-icon-wrap">
                <input
                  id="affiliate-rebate-rate"
                  v-model.number="form.affiliate_rebate_rate"
                  type="number"
                  min="0"
                  max="100"
                  step="0.01"
                  class="input input-has-icon-right"
                  placeholder="20"
                />
                <span
                  class="input-icon-right text-sm text-gray-400 dark:text-dark-400"
                  aria-hidden="true"
                  >%</span
                >
              </div>
              <p class="input-hint">
                {{
                  t('admin.settings.features.affiliate.rebateRateHint', {
                    unitName: previewBalanceUnitName,
                  })
                }}
              </p>
            </div>
            <div>
              <label for="affiliate-rebate-freeze-hours" class="input-label">
                {{ t('admin.settings.features.affiliate.freezeHours') }}
              </label>
              <input
                id="affiliate-rebate-freeze-hours"
                v-model.number="form.affiliate_rebate_freeze_hours"
                type="number"
                min="0"
                max="720"
                step="1"
                class="input"
              />
              <p class="input-hint">
                {{ t('admin.settings.features.affiliate.freezeHoursDesc') }}
              </p>
            </div>
            <div>
              <label for="affiliate-rebate-duration-days" class="input-label">
                {{ t('admin.settings.features.affiliate.durationDays') }}
              </label>
              <input
                id="affiliate-rebate-duration-days"
                v-model.number="form.affiliate_rebate_duration_days"
                type="number"
                min="0"
                max="3650"
                step="1"
                class="input"
              />
              <p class="input-hint">
                {{ t('admin.settings.features.affiliate.durationDaysDesc') }}
              </p>
            </div>
            <div>
              <label for="affiliate-rebate-per-invitee-cap" class="input-label">
                {{
                  t('admin.settings.features.affiliate.perInviteeCap', {
                    unitName: previewBalanceUnitName,
                  })
                }}
              </label>
              <input
                id="affiliate-rebate-per-invitee-cap"
                v-model.number="form.affiliate_rebate_per_invitee_cap"
                type="number"
                min="0"
                step="0.01"
                class="input"
              />
              <p class="input-hint">
                {{
                  t('admin.settings.features.affiliate.perInviteeCapDesc', {
                    unitName: previewBalanceUnitName,
                  })
                }}
              </p>
            </div>
          </div>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <!-- Risk control settings. -->
  <SettingsCard
    :title="t('admin.settings.features.riskControl.title')"
    :description="t('admin.settings.features.riskControl.description')"
  >
    <template #actions>
      <router-link to="/admin/risk-control" class="btn btn-secondary btn-sm h-9 shrink-0">
        {{ t('admin.settings.features.riskControl.configureLink') }}
      </router-link>
    </template>
    <SettingsSection>
      <SettingToggleRow
        id="risk-control-enabled"
        v-model="form.risk_control_enabled"
        :label="t('admin.settings.features.riskControl.enabled')"
        :hint="t('admin.settings.features.riskControl.enabledHint')"
      />
      <SettingToggleRow
        id="cyber-session-block-enabled"
        v-model="form.cyber_session_block_enabled"
        :label="t('admin.settings.features.riskControl.cyberSessionBlockEnabled')"
        :hint="t('admin.settings.features.riskControl.cyberSessionBlockEnabledHint')"
      />
      <Collapse :open="form.cyber_session_block_enabled" unmount-on-hide>
        <SettingsSubpanel>
          <SettingRow
            id="cyber-session-block-ttl"
            field
            label-for="cyber-session-block-ttl"
            :label="t('admin.settings.features.riskControl.cyberSessionBlockTTLSeconds')"
            :hint="t('admin.settings.features.riskControl.cyberSessionBlockTTLSecondsHint')"
          >
            <input
              id="cyber-session-block-ttl"
              v-model.number="form.cyber_session_block_ttl_seconds"
              type="number"
              min="1"
              step="1"
              class="input"
            />
          </SettingRow>
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>

  <SettingsCard
    :title="t('admin.settings.features.bstats.title')"
    :description="t('admin.settings.features.bstats.description')"
    data-testid="bstats-settings"
  >
    <template #actions>
      <Toggle
        id="bstats-enabled"
        v-model="form.bstats_enabled"
        :aria-label="t('admin.settings.features.bstats.enabled')"
      />
    </template>
    <SettingsSection>
      <SettingsNotice tone="info">
        <p>
          {{ t('admin.settings.features.bstats.dataHint') }}
          <a
            href="https://bstats.org/plugin/server-implementation/tokenrouter/35120"
            target="_blank"
            rel="noopener noreferrer"
            class="text-link"
          >
            {{ t('admin.settings.features.bstats.viewStatistics') }}
          </a>
        </p>
      </SettingsNotice>
      <Collapse :open="form.bstats_enabled">
        <SettingsSubpanel>
          <SettingToggleRow
            id="bstats-log-errors"
            v-model="form.bstats_log_errors"
            :label="t('admin.settings.features.bstats.logErrors')"
            :hint="t('admin.settings.features.bstats.logErrorsHint')"
          />
          <SettingToggleRow
            id="bstats-log-sent-data"
            v-model="form.bstats_log_sent_data"
            :label="t('admin.settings.features.bstats.logSentData')"
            :hint="t('admin.settings.features.bstats.logSentDataHint')"
          />
          <SettingToggleRow
            id="bstats-log-response-status-text"
            v-model="form.bstats_log_response_status_text"
            :label="t('admin.settings.features.bstats.logResponse')"
            :hint="t('admin.settings.features.bstats.logResponseHint')"
          />
        </SettingsSubpanel>
      </Collapse>
    </SettingsSection>
  </SettingsCard>
</template>

<script setup lang="ts">
import { toRef } from 'vue'
import Toggle from '@/components/common/Toggle.vue'
import Collapse from '@/components/common/Collapse.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import type { useFeaturesSettings, FeaturesSettingsForm } from './useFeaturesSettings'

/**
 * FeatureSettings.vue renders team, creative, affiliate, risk-control, and bStats settings using
 * the page-owned draft and feature controller. Creative rows expose candidates and
 * supported operations from the controller, including unavailable historical selections,
 * while worker usage displays its observed status. The shared page save action persists
 * edited values across tabs.
 *
 * Capability buttons disable unsupported operations and removal of the final selected
 * operation before forwarding clicks. The affiliate cap label follows the draft balance
 * unit passed by the general controller. Panel hiding retains its draft, while the cyber session
 * expiry input can unmount with its collapse because the editable value remains in
 * the shared form.
 */
// The parent supplies one controller and its shared draft for this mounted
// panel; template writes mutate that draft before the page save action runs.
const props = defineProps<{
  // controller retains page-scoped refs and actions; its closures target the
  // same form object provided below for this panel's editable controls.
  controller: ReturnType<typeof useFeaturesSettings>
  // form is the parent-owned reactive draft; nested writes participate in the
  // bulk dirty tracker and remain visible across tab changes.
  form: FeaturesSettingsForm
  // previewBalanceUnitName follows unsaved balance-unit edits from the general
  // controller and labels the affiliate cap in the active locale.
  previewBalanceUnitName: string
}>()
// Controller bindings retain their refs and closures so template events use
// the page state that hydration, validation, and shared saving also consume.
const {
  creativeOperationChoices,
  creativeModelCandidates,
  creativeModelCandidatesLoading,
  creativeModelCandidatesError,
  creativeWorkerUsagePercent,
  creativeWorkerUsageText,
  creativeModelSettingKey,
  creativeModelOptionsForRow,
  creativeCandidateForSetting,
  creativeOperationCheckboxDisabled,
  onCreativeModelSelected,
  addCreativeModelSetting,
  removeCreativeModelSetting,
  toggleCreativeOperation,
} = props.controller
// The draft alias keeps nested v-model writes on the parent-owned reactive
// object; the parent maintains its identity throughout this page lifetime.
const { form } = props
// A prop ref keeps the affiliate cap label reactive when the general panel
// edits the balance unit and supplies a new preview name.
const previewBalanceUnitName = toRef(props, 'previewBalanceUnitName')
// Translation lookups are evaluated by the template so labels follow the
// active interface language while stored setting values retain their API meaning.
const { t } = useI18n()
</script>
