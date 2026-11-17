<template>
  <SettingsCard
    :title="t('admin.settings.emailTemplates.title')"
    :description="t('admin.settings.emailTemplates.description')"
  >
    <template #actions>
      <div class="flex w-full flex-wrap gap-2 sm:w-auto">
        <button
          type="button"
          class="btn btn-secondary btn-sm h-9"
          :disabled="previewing || !canPreview"
          @click="refreshPreview"
        >
          {{
            previewing
              ? t('admin.settings.emailTemplates.previewing')
              : t('admin.settings.emailTemplates.preview')
          }}
        </button>
        <button
          type="button"
          class="btn btn-secondary btn-sm h-9"
          :disabled="!canEdit"
          @click="restoreOfficial"
        >
          {{
            restoring
              ? t('admin.settings.emailTemplates.restoring')
              : t('admin.settings.emailTemplates.restoreOfficial')
          }}
        </button>
      </div>
    </template>

    <div class="space-y-6">
      <EmailTemplateSkeleton v-if="loadingList" show-selectors />

      <div v-else-if="listFailed" class="flex flex-wrap items-center gap-3" role="alert">
        <p class="flex-1 text-sm text-gray-600 dark:text-dark-300">{{
          t('admin.settings.emailTemplates.listUnavailable')
        }}</p>
        <button type="button" class="btn btn-secondary" @click="loadTemplateList">{{
          t('common.retry')
        }}</button>
      </div>
      <template v-else>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label" for="email-template-event">
              {{ t('admin.settings.emailTemplates.event') }}
            </label>
            <Select
              id="email-template-event"
              v-model="selectedEvent"
              :options="emailTemplateEventSelectOptions"
              :disabled="writingTemplate || eventOptions.length === 0"
            />
          </div>
          <div>
            <label class="input-label" for="email-template-locale">
              {{ t('admin.settings.emailTemplates.locale') }}
            </label>
            <Select
              id="email-template-locale"
              v-model="selectedLocale"
              :options="emailTemplateLocaleSelectOptions"
              :disabled="writingTemplate || localeOptions.length === 0"
            />
          </div>
        </div>

        <div
          v-if="selectedEventMeta"
          class="rounded-surface border border-primary-100 bg-primary-50/70 p-4 dark:border-primary-900/50 dark:bg-primary-950/20"
        >
          <div class="flex flex-wrap items-center gap-2">
            <div class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ selectedEventMeta.label }}
            </div>
            <span
              class="rounded-full bg-white px-2.5 py-1 text-xs font-medium text-gray-600 shadow-sm ring-1 ring-gray-200 dark:bg-dark-800 dark:text-gray-300 dark:ring-dark-600"
            >
              {{ selectedEventMeta.categoryLabel }}
            </span>
            <span
              class="rounded-full px-2.5 py-1 text-xs font-medium"
              :class="
                selectedEventMeta.optional
                  ? 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300'
                  : 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300'
              "
            >
              {{
                selectedEventMeta.optional
                  ? localText('可退订通知', 'Optional')
                  : localText('事务邮件', 'Transactional')
              }}
            </span>
          </div>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">
            {{ selectedEventMeta.timing }}
          </p>
          <p v-if="selectedEventDescription" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ selectedEventDescription }}
          </p>
        </div>

        <div
          v-if="!eventOptions.length || !localeOptions.length"
          class="rounded-control border border-amber-200 bg-amber-50 p-4 text-sm text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
        >
          {{ t('admin.settings.emailTemplates.empty') }}
        </div>

        <EmailTemplateSkeleton v-else-if="loadingTemplate" />

        <div v-else-if="templateFailed" class="flex flex-wrap items-center gap-3" role="alert">
          <p class="flex-1 text-sm text-gray-600 dark:text-dark-300">{{
            t('admin.settings.emailTemplates.templateUnavailable')
          }}</p>
          <button type="button" class="btn btn-secondary" @click="loadTemplate">{{
            t('common.retry')
          }}</button>
        </div>

        <div v-else-if="hasCurrentTemplate" class="grid grid-cols-1 gap-6 xl:grid-cols-2">
          <div class="space-y-4">
            <div>
              <label class="input-label" for="email-template-subject">
                {{ t('admin.settings.emailTemplates.subject') }}
              </label>
              <input
                id="email-template-subject"
                v-model="subject"
                type="text"
                class="input"
                :disabled="!canEdit"
                :placeholder="t('admin.settings.emailTemplates.subjectPlaceholder')"
              />
            </div>

            <div>
              <label class="input-label" for="email-template-html">
                {{ t('admin.settings.emailTemplates.html') }}
              </label>
              <textarea
                id="email-template-html"
                v-model="html"
                rows="18"
                class="input min-h-[28rem] resize-y font-mono text-sm leading-6"
                :disabled="!canEdit"
                :placeholder="t('admin.settings.emailTemplates.htmlPlaceholder')"
              ></textarea>
            </div>

            <div
              class="rounded-surface border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/60"
            >
              <div class="text-sm font-medium text-gray-900 dark:text-white">
                {{ t('admin.settings.emailTemplates.placeholders') }}
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.settings.emailTemplates.placeholdersHelp') }}
              </p>
              <div class="mt-3 flex flex-wrap gap-2">
                <button
                  v-for="placeholder in placeholderList"
                  :key="placeholder"
                  type="button"
                  class="rounded-full border border-gray-200 bg-white px-3 py-1 font-mono text-xs text-gray-700 transition-colors hover:border-primary-300 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-200 dark:hover:border-primary-500 dark:hover:text-primary-300"
                  @click="copyPlaceholder(placeholder)"
                >
                  {{ placeholder }}
                </button>
              </div>
            </div>
          </div>

          <div class="space-y-4">
            <div
              class="rounded-control border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800"
            >
              <div
                class="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-dark-700"
              >
                <div>
                  <div class="text-sm font-medium text-gray-900 dark:text-white">
                    {{ t('admin.settings.emailTemplates.livePreview') }}
                  </div>
                  <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ previewSubject || t('admin.settings.emailTemplates.noPreview') }}
                  </div>
                </div>
                <span
                  v-if="isCustomTemplate"
                  class="rounded-full bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
                >
                  {{ t('admin.settings.emailTemplates.customized') }}
                </span>
              </div>
              <div class="bg-gray-100 p-3 dark:bg-dark-900">
                <Skeleton
                  v-if="previewing && !previewHtml"
                  height="36rem"
                  class="rounded-control"
                />
                <iframe
                  v-else
                  class="h-[36rem] w-full rounded-control border border-gray-200 bg-white dark:border-dark-700"
                  sandbox=""
                  :srcdoc="previewHtml"
                  :title="t('admin.settings.emailTemplates.livePreview')"
                ></iframe>
              </div>
            </div>

            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.settings.emailTemplates.previewSecurityHint') }}
            </p>
          </div>
        </div>
      </template>
    </div>
  </SettingsCard>
</template>

<script setup lang="ts">
import EmailTemplateSkeleton from './EmailTemplateSkeleton.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type {
  EmailTemplateDetail,
  EmailTemplateEventOption,
  EmailTemplateOption,
} from '@/api/admin/settings'
import Select from '@/components/common/Select.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import { useDirtyTracker } from '@/composables/useDirtyTracker'
import { useSettingsSaveTarget } from '@/composables/useSettingsSaveRegistry'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

/**
 * EmailTemplateEditor.vue edits and previews one email template selected by
 * event and locale. The server supplies available identities, template
 * content, and supported placeholders; local metadata supplies translated
 * event descriptions where known. Initial locale selection prefers the
 * interface locale, then its language, then the first server option. Template
 * subject and HTML have their own save-registry snapshot, separate from SMTP
 * settings.
 *
 * Content becomes editable only after a successful read for its event and
 * locale. Selection changes clear the editor immediately, and request counters
 * keep earlier responses from publishing content or previews for a different
 * selection. Saving and restoring hold the selection and draft steady until
 * the write settles. The preview can complete independently of that write.
 */

// Interface translations and application notifications serve both template
// operations and the local event-description metadata shown beside the
// editor.
const { t, locale } = useI18n()
const appStore = useAppStore()

// fallbackPlaceholders supplies discoverable template tokens when the
// selected server response does not advertise any event-specific
// placeholders.
const fallbackPlaceholders = [
  '{{site_name}}',
  '{{recipient_name}}',
  '{{recipient_email}}',
  '{{verification_code}}',
  '{{expires_in_minutes}}',
  '{{reset_url}}',
  '{{team_name}}',
  '{{invitation_url}}',
  '{{expires_at}}',
  '{{subscription_group}}',
  '{{subscription_days}}',
  '{{expiry_time}}',
  '{{days_remaining}}',
  '{{current_balance}}',
  '{{threshold}}',
  '{{recharge_url}}',
  '{{recharge_amount}}',
  '{{order_id}}',
  '{{unsubscribe_url}}',
  '{{provider_id}}',
  '{{provider_name}}',
  '{{platform}}',
  '{{quota_dimension}}',
  '{{quota_used}}',
  '{{quota_limit}}',
  '{{quota_remaining}}',
  '{{quota_threshold}}',
  '{{triggered_at}}',
  '{{group_name}}',
  '{{moderation_category}}',
  '{{moderation_score}}',
  '{{violation_count}}',
  '{{ban_threshold}}',
  '{{rule_name}}',
  '{{severity}}',
  '{{alert_status}}',
  '{{metric_type}}',
  '{{operator}}',
  '{{metric_value}}',
  '{{threshold_value}}',
  '{{alert_description}}',
  '{{report_name}}',
  '{{report_type}}',
  '{{report_start_time}}',
  '{{report_end_time}}',
  '{{report_summary_display}}',
  '{{report_detail_display}}',
  '{{report_total_requests}}',
  '{{report_success_count}}',
  '{{report_sla_error_count}}',
  '{{report_business_limited_count}}',
  '{{report_sla}}',
  '{{report_error_rate}}',
  '{{report_upstream_error_rate}}',
  '{{report_upstream_error_count_excl_429_529}}',
  '{{report_upstream_429_count}}',
  '{{report_upstream_529_count}}',
  '{{report_latency_p50}}',
  '{{report_latency_p99}}',
  '{{report_ttft_p50}}',
  '{{report_ttft_p99}}',
  '{{report_tokens}}',
  '{{report_qps_current}}',
  '{{report_qps_peak}}',
  '{{report_qps_avg}}',
  '{{report_tps_current}}',
  '{{report_tps_peak}}',
  '{{report_tps_avg}}',
  '{{report_html}}',
]

// Request flags keep the list, selected template, preview, and writes independent.
const loadingList = ref(true)
const listFailed = ref(false)
const loadingTemplate = ref(false)
const templateFailed = ref(false)
const previewing = ref(false)
const restoring = ref(false)
const savingTemplate = ref(false)
const writingTemplate = computed(() => savingTemplate.value || restoring.value)

// The selection contains the exact server event and locale used by every editor operation.
const eventOptions = ref<EmailTemplateOption[]>([])
const localeOptions = ref<string[]>([])
const selectedEvent = ref('')
const selectedLocale = ref('')
const selection = computed(() => ({ event: selectedEvent.value, locale: selectedLocale.value }))

// loadedSelection belongs to the response that populated the draft. Each read and preview
// counter advances independently, preventing late completions from replacing current content.
const loadedSelection = shallowRef<typeof selection.value | null>(null)
const hasCurrentTemplate = computed(() => loadedSelection.value === selection.value)
let templateGeneration = 0
let previewGeneration = 0

// disposed prevents a pending index request from starting another read after editor teardown.
let disposed = false
// Editable content and preview output have separate refs so server-rendered
// preview HTML does not replace the administrator's template source.
const subject = ref('')
const html = ref('')
const isCustomTemplate = ref(false)
const placeholders = ref<string[]>([])
const previewSubject = ref('')
const previewHtml = ref('')
// initializingSelection suppresses selection watchers while list
// initialization performs its explicit first template load.
const initializingSelection = ref(false)

/**
 * EventDisplayMeta holds localized explanatory text for a known email event.
 */
interface EventDisplayMeta {
  label: string // Local event display name.
  timing: string // Event delivery trigger explanation.
  categoryLabel: string // Local event category name.
}

/**
 * localText selects Chinese or English interface copy from a supplied pair.
 * Any interface locale beginning with zh uses Chinese, while all remaining
 * locale values use the English text argument.
 */
function localText(zh: string, en: string): string {
  return locale.value.toLowerCase().startsWith('zh') ? zh : en
}

// Chinese event metadata overrides server labels for recognized event IDs
// while keeping account, billing, operations, and plugin notification meanings
// distinct.
const eventDisplayMeta: Record<string, EventDisplayMeta> = {
  plugin_message: {
    label: '插件消息',
    timing: '管理员安装的插件向用户发送消息时投递，用户可退订此类邮件。',
    categoryLabel: '插件',
  },
  'auth.verify_code': {
    label: '邮箱验证码',
    timing: '注册、绑定邮箱、OAuth 补全邮箱或 TOTP 邮箱校验时发送。',
    categoryLabel: '认证安全',
  },
  'auth.password_reset': {
    label: '密码重置',
    timing: '用户请求密码重置链接时发送。',
    categoryLabel: '认证安全',
  },
  'notification_email.verify_code': {
    label: '通知邮箱验证码',
    timing: '用户添加并验证额外通知邮箱时发送。',
    categoryLabel: '认证安全',
  },
  'team.ownership_transfer': {
    label: '团队所有权转让',
    timing: '团队所有者发起所有权转让时发送。',
    categoryLabel: '团队',
  },
  'team.invitation': {
    label: '团队邀请',
    timing: '团队所有者邀请指定邮箱加入团队，或重新发送团队邀请时发送。',
    categoryLabel: '团队',
  },
  'subscription.purchase_success': {
    label: '订阅开通成功',
    timing: '订阅订单完成支付并成功开通或续期后发送。',
    categoryLabel: '订阅',
  },
  'subscription.expiry_reminder': {
    label: '订阅到期提醒',
    timing:
      '后台任务在订阅仍有效且距离到期剩余 7 天、3 天、1 天时各发送一次，可通过邮件设置中的开关关闭。',
    categoryLabel: '订阅',
  },
  'balance.low': {
    label: '余额不足提醒',
    timing: '用户余额低于全局或个人配置的提醒阈值时发送。',
    categoryLabel: '计费',
  },
  'balance.recharge_success': {
    label: '余额充值成功',
    timing: '余额充值订单支付完成并入账后发送。',
    categoryLabel: '计费',
  },
  'provider.upstream_balance_low': {
    label: '上游余额提醒',
    timing:
      '提供商开启余额提醒后，每次定时检查发现上游钱包余额低于阈值时，发送到该提供商设置的收件邮箱。',
    categoryLabel: '管理告警',
  },
  'provider.quota_alert': {
    label: '提供商限额告警',
    timing: '上游提供商的用量达到配置的额度告警阈值时发送给管理员通知邮箱。',
    categoryLabel: '管理告警',
  },
  'content_moderation.violation_notice': {
    label: '内容审计违规提醒',
    timing: '用户请求命中内容审计或风控规则、但尚未被禁用时发送。',
    categoryLabel: '风控',
  },
  'content_moderation.account_disabled': {
    label: '内容审计禁用账号',
    timing: '内容审计违规次数达到封禁阈值并自动禁用用户账号时发送。',
    categoryLabel: '风控',
  },
  'ops.alert': {
    label: '运维告警',
    timing: '运维监控规则触发告警并满足邮件通知配置时发送给运维收件人。',
    categoryLabel: '运维',
  },
  'ops.scheduled_report': {
    label: '运维定时报表',
    timing:
      '运维日报、周报、错误摘要或提供商健康报表到达配置的发送时间时发送；日报和周报的完整指标均可在模板中编辑。',
    categoryLabel: '运维',
  },
}

// English event metadata supplies the same known event descriptions whenever
// the interface locale does not begin with zh.
const eventDisplayMetaEn: Record<string, EventDisplayMeta> = {
  plugin_message: {
    label: 'Plugin Message',
    timing:
      'Sent when an administrator-installed plugin sends a user message. Recipients can unsubscribe.',
    categoryLabel: 'Plugin',
  },
  'auth.verify_code': {
    label: 'Email Verification Code',
    timing:
      'Sent for registration, email binding, OAuth pending email completion, or TOTP email verification.',
    categoryLabel: 'Auth',
  },
  'auth.password_reset': {
    label: 'Password Reset',
    timing: 'Sent when a user requests a password reset link.',
    categoryLabel: 'Auth',
  },
  'notification_email.verify_code': {
    label: 'Notification Email Verification',
    timing: 'Sent when a user adds and verifies an extra notification email address.',
    categoryLabel: 'Auth',
  },
  'team.ownership_transfer': {
    label: 'Team Ownership Transfer',
    timing: 'Sent when the team owner requests an ownership transfer.',
    categoryLabel: 'Team',
  },
  'team.invitation': {
    label: 'Team Invitation',
    timing:
      'Sent when a team owner invites an email address or reissues an existing team invitation.',
    categoryLabel: 'Team',
  },
  'subscription.purchase_success': {
    label: 'Subscription Activated',
    timing:
      'Sent after a subscription order is paid and the subscription is activated or extended.',
    categoryLabel: 'Subscription',
  },
  'subscription.expiry_reminder': {
    label: 'Subscription Expiry Reminder',
    timing:
      'Sent by the background job when an active subscription has 7, 3, or 1 day remaining. It can be disabled in Email settings.',
    categoryLabel: 'Subscription',
  },
  'balance.low': {
    label: 'Low Balance Alert',
    timing: "Sent when a user's balance drops below the global or personal reminder threshold.",
    categoryLabel: 'Billing',
  },
  'balance.recharge_success': {
    label: 'Balance Recharge Success',
    timing: 'Sent after a balance recharge order is paid and credited.',
    categoryLabel: 'Billing',
  },
  'provider.upstream_balance_low': {
    label: 'Upstream Balance Reminder',
    timing:
      "Sent to the provider's reminder recipient on every scheduled check that finds the upstream wallet balance below its threshold.",
    categoryLabel: 'Admin',
  },
  'provider.quota_alert': {
    label: 'Provider Quota Alert',
    timing:
      'Sent to admin notification emails when an upstream provider reaches the configured quota alert threshold.',
    categoryLabel: 'Admin',
  },
  'content_moderation.violation_notice': {
    label: 'Risk Control Violation Notice',
    timing:
      'Sent when a user request triggers content moderation or risk-control rules but the user account is not disabled yet.',
    categoryLabel: 'Risk Control',
  },
  'content_moderation.account_disabled': {
    label: 'Risk Control Account Disabled',
    timing:
      'Sent when content moderation reaches the ban threshold and automatically disables the user account.',
    categoryLabel: 'Risk Control',
  },
  'ops.alert': {
    label: 'Ops Alert',
    timing:
      'Sent to ops recipients when an ops monitoring rule fires and email notification settings allow it.',
    categoryLabel: 'Ops',
  },
  'ops.scheduled_report': {
    label: 'Ops Scheduled Report',
    timing:
      'Sent when a configured daily, weekly, error digest, or provider health report reaches its scheduled send time. Every daily and weekly summary metric is editable in this template.',
    categoryLabel: 'Ops',
  },
}

/**
 * normalizeEventOption converts legacy string event identities into option
 * objects for selectors. Existing object options are returned by reference,
 * retaining their server-provided label, category, description, and optional
 * status fields.
 */
function normalizeEventOption(option: EmailTemplateEventOption): EmailTemplateOption {
  if (typeof option === 'string') {
    return { value: option }
  }
  return option
}

/**
 * eventMetaFor combines local event descriptions with the server option
 * selected for display. A missing option returns null, and the interface
 * locale chooses the Chinese or English metadata table independently of the
 * template locale being edited.
 *
 * For known events, nonempty local labels, timing descriptions, and category
 * names take precedence over server fields. Unknown entries use server
 * metadata and ultimately the event ID or formatted category. Optional status
 * always follows the server option rather than the local description table.
 */
function eventMetaFor(option?: EmailTemplateOption | null) {
  if (!option) return null
  // Interface language selects descriptive copy independently from the
  // template locale, so an administrator can edit another language using
  // familiar labels.
  const displayMeta = (
    locale.value.toLowerCase().startsWith('zh') ? eventDisplayMeta : eventDisplayMetaEn
  )[option.value]
  const label = displayMeta?.label || option.label || option.value
  const timing = displayMeta?.timing || option.description || ''
  const categoryLabel = displayMeta?.categoryLabel || formatCategory(option.category || '')
  return {
    label,
    timing,
    categoryLabel,
    optional: option.optional === true,
  }
}

/**
 * formatEventOptionLabel returns the localized display label for a server
 * event option. If metadata cannot be produced, it uses the server label or
 * event identity so the selector keeps an identifiable value.
 */
function formatEventOptionLabel(option: EmailTemplateOption): string {
  const meta = eventMetaFor(option)
  if (!meta) return option.label || option.value
  return meta.label
}

/**
 * formatCategory translates recognized category names using the interface
 * locale after trimming and lowercasing for lookup. Empty categories use
 * Notification, while unknown nonempty categories retain the exact supplied
 * text.
 */
function formatCategory(category: string): string {
  const normalized = category.trim().toLowerCase()
  if (!normalized) return localText('通知', 'Notification')
  const labels: Record<
    string,
    {
      zh: string // Chinese category display text.
      en: string // English category display text.
    }
  > = {
    auth: { zh: '认证安全', en: 'Auth' },
    team: { zh: '团队', en: 'Team' },
    subscription: { zh: '订阅', en: 'Subscription' },
    billing: { zh: '计费', en: 'Billing' },
    admin: { zh: '管理告警', en: 'Admin' },
    risk_control: { zh: '风控', en: 'Risk Control' },
    ops: { zh: '运维', en: 'Ops' },
  }
  const item = labels[normalized]
  return item ? localText(item.zh, item.en) : category
}

// Selected event metadata and descriptions combine the server choice with
// interface-language text for the editor's contextual guidance.
const selectedEventOption = computed(() => {
  return eventOptions.value.find((option) => option.value === selectedEvent.value) || null
})

const selectedEventMeta = computed(() => eventMetaFor(selectedEventOption.value))

const selectedEventDescription = computed(() => {
  return selectedEventOption.value?.description || ''
})

// Placeholder and selector lists adapt server data for template controls
// while retaining the original event and locale values in selections.
const placeholderList = computed(() => {
  // Empty server placeholder lists use the shared token catalog so the editor
  // still offers insertable values when event metadata is absent.
  const combined = placeholders.value.length ? placeholders.value : fallbackPlaceholders
  return Array.from(
    new Set(combined.map((item) => formatPlaceholder(item)).filter((item) => item.length > 0)),
  )
})

const emailTemplateEventSelectOptions = computed(() =>
  eventOptions.value.map((option) => ({
    value: option.value,
    label: formatEventOptionLabel(option),
  })),
)

const emailTemplateLocaleSelectOptions = computed(() =>
  localeOptions.value.map((localeOption) => ({
    value: localeOption,
    label: formatLocale(localeOption),
  })),
)

/**
 * formatPlaceholder trims a placeholder and adds template delimiters when
 * needed. Empty input returns an empty string for filtering, while a value
 * already enclosed in double braces retains its existing spelling.
 */
function formatPlaceholder(placeholder: string): string {
  const trimmed = placeholder.trim()
  if (!trimmed) return ''
  if (trimmed.startsWith('{{') && trimmed.endsWith('}}')) return trimmed
  return `{{${trimmed}}}`
}

// The editor and writes require a draft loaded for the current event and locale.
const canEdit = computed(
  () => hasCurrentTemplate.value && !loadingTemplate.value && !writingTemplate.value,
)
const canSave = computed(
  () => canEdit.value && subject.value.trim().length > 0 && html.value.trim().length > 0,
)
const canPreview = computed(() => hasCurrentTemplate.value && html.value.trim().length > 0)

/**
 * formatLocale labels Chinese and English language variants using translated
 * interface names. Other language codes are returned unchanged, allowing
 * locale options supplied by the server to remain selectable and identifiable.
 */
function formatLocale(locale: string): string {
  const lower = locale.toLowerCase()
  if (lower === 'zh' || lower.startsWith('zh-')) {
    return t('admin.settings.emailTemplates.localeZh')
  }
  if (lower === 'en' || lower.startsWith('en-')) {
    return t('admin.settings.emailTemplates.localeEn')
  }
  return locale
}

/**
 * selectInitialLocale selects a server locale using case-insensitive exact
 * matching, then matching language subtags. If neither matches the interface
 * locale, it returns the first available value or an empty string for an empty
 * list.
 */
function selectInitialLocale(locales: string[]): string {
  const currentLocale = locale.value.toLowerCase()
  const exactMatch = locales.find(
    (availableLocale) => availableLocale.toLowerCase() === currentLocale,
  )
  if (exactMatch) return exactMatch

  // Match the language before choosing a different locale, allowing regional
  // interface variants to select a supported template in that language.
  const currentLanguage = currentLocale.split('-')[0]
  const languageMatch = locales.find(
    (availableLocale) => availableLocale.toLowerCase().split('-')[0] === currentLanguage,
  )
  if (languageMatch) return languageMatch

  // The server option order supplies the final default; an empty list leaves
  // selection unset and prevents a template request.
  return locales[0] || ''
}

/**
 * applyTemplate installs the returned content for the captured selection.
 * Callers check request ownership first. The loaded identity and clean snapshot
 * are published with the subject and HTML before a preview request starts.
 */
function applyTemplate(template: EmailTemplateDetail, target: typeof selection.value) {
  subject.value = template.subject
  html.value = template.html
  isCustomTemplate.value = template.is_custom === true
  placeholders.value = template.placeholders || []
  loadedSelection.value = target
  markClean()
}

/**
 * loadTemplate replaces the editor for the current event and locale. It clears
 * the old draft and preview when selection changes so a failed request cannot
 * carry their content into the new identity. Only the latest read for the
 * current selection can publish content, report failure, or release its loading
 * indicator. A successful read starts preview rendering independently.
 */
async function loadTemplate() {
  const target = selection.value
  // Invalidate previous content and preview requests before installing the new
  // draft state, so a late response cannot make an unrelated selection editable.
  const generation = ++templateGeneration
  previewGeneration += 1
  previewing.value = false
  loadedSelection.value = null
  templateFailed.value = false
  subject.value = ''
  html.value = ''
  isCustomTemplate.value = false
  placeholders.value = []
  previewSubject.value = ''
  previewHtml.value = ''
  markClean()
  loadingTemplate.value = Boolean(target.event && target.locale)
  if (!loadingTemplate.value) return
  try {
    const template = await adminAPI.settings.getEmailTemplate(target.event, target.locale)
    if (generation !== templateGeneration || target !== selection.value) return
    applyTemplate(template, target)
    void refreshPreview()
  } catch (err: unknown) {
    if (generation !== templateGeneration || target !== selection.value) return
    templateFailed.value = true
    appStore.showError(
      extractApiErrorMessage(err, t('admin.settings.emailTemplates.templateUnavailable')),
    )
  } finally {
    if (generation === templateGeneration) loadingTemplate.value = false
  }
}

/**
 * loadTemplateList reads the available identities and starts the first content
 * request. It sets event and locale together with the selection watcher
 * suspended so the editor requests only the chosen pair. A failed list read
 * leaves its own retry state, separate from failures for individual templates.
 */
async function loadTemplateList() {
  loadingList.value = true
  listFailed.value = false
  try {
    const response = await adminAPI.settings.getEmailTemplates()
    if (disposed) return
    eventOptions.value = response.events.map(normalizeEventOption)
    localeOptions.value = response.locales
    // Suspend the synchronous watcher across both assignments; either one
    // alone could point at a template the user never selected.
    initializingSelection.value = true
    selectedEvent.value = eventOptions.value[0]?.value || ''
    selectedLocale.value = selectInitialLocale(response.locales)
    initializingSelection.value = false
    void loadTemplate()
  } catch (err: unknown) {
    if (disposed) return
    listFailed.value = true
    appStore.showError(
      extractApiErrorMessage(err, t('admin.settings.emailTemplates.listUnavailable')),
    )
  } finally {
    initializingSelection.value = false
    loadingList.value = false
  }
}

/**
 * saveTemplate writes subject and HTML for the currently loaded identity.
 * Invalid or unavailable drafts return false without an API call. The write
 * holds the fields and selection steady until it finishes, then applies the
 * returned template only if the editor still owns that selection. A preview
 * failure leaves the persisted write successful.
 */
async function saveTemplate(): Promise<boolean> {
  if (!canSave.value) {
    appStore.showError(t('admin.settings.emailTemplates.validationRequired'))
    return false
  }
  const target = selection.value
  savingTemplate.value = true
  try {
    const template = await adminAPI.settings.updateEmailTemplate(target.event, target.locale, {
      subject: subject.value,
      html: html.value,
    })
    if (hasCurrentTemplate.value && target === selection.value) {
      applyTemplate(template, target)
      void refreshPreview()
    }
    return true
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
    return false
  } finally {
    savingTemplate.value = false
  }
}

/**
 * refreshPreview renders the current draft in the selected event and locale.
 * Only the latest preview for that selection can update the panel or its busy
 * flag. A failed render keeps any existing preview of the same selection and
 * reports the error without affecting the editable template or save state.
 */
async function refreshPreview() {
  if (!canPreview.value) return
  const target = selection.value
  // A manual preview and post-save preview can overlap; the later request owns
  // the panel, even if the older renderer finishes after it.
  const generation = ++previewGeneration
  previewing.value = true
  try {
    const preview = await adminAPI.settings.previewEmailTemplate({
      event: target.event,
      locale: target.locale,
      subject: subject.value,
      html: html.value,
    })
    if (generation !== previewGeneration || target !== selection.value) return
    previewSubject.value = preview.subject
    previewHtml.value = preview.html
  } catch (err: unknown) {
    if (generation !== previewGeneration || target !== selection.value) return
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    if (generation === previewGeneration) previewing.value = false
  }
}

/**
 * restoreOfficial resets the currently loaded template after browser confirmation.
 * It holds the draft and selectors during the write and applies the restored
 * content only to its matching selection. Canceling the prompt leaves the draft
 * unchanged, and a later preview failure does not reverse a successful reset.
 */
async function restoreOfficial() {
  if (!canEdit.value || !window.confirm(t('admin.settings.emailTemplates.restoreConfirm'))) return
  const target = selection.value
  restoring.value = true
  try {
    const template = await adminAPI.settings.restoreOfficialEmailTemplate(
      target.event,
      target.locale,
    )
    if (hasCurrentTemplate.value && target === selection.value) {
      applyTemplate(template, target)
      void refreshPreview()
    }
    appStore.showSuccess(t('admin.settings.emailTemplates.restoreSuccess'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    restoring.value = false
  }
}

/**
 * copyPlaceholder writes the supplied placeholder text to the browser
 * clipboard and reports success. Clipboard failures produce a generic error,
 * allowing the editor to remain usable when browser permissions deny the
 * write.
 */
async function copyPlaceholder(placeholder: string) {
  try {
    await navigator.clipboard.writeText(placeholder)
    appStore.showSuccess(t('admin.settings.emailTemplates.placeholderCopied'))
  } catch {
    appStore.showError(t('common.error'))
  }
}

// The template save target compares subject and HTML with the most recently
// applied server template, independently of bulk SMTP settings.
const { dirty, markClean } = useDirtyTracker({
  template: () => ({ subject: subject.value, html: html.value }),
})
useSettingsSaveTarget('emailTemplate', { dirty, save: saveTemplate, discard: loadTemplate })

// Invalidate the draft synchronously when a selector changes so a save
// triggered in the same tick cannot submit content for the previous identity.
watch(
  selection,
  () => {
    if (!initializingSelection.value) void loadTemplate()
  },
  { flush: 'sync' },
)

// Responses that outlive the editor must not publish their content or previews.
onBeforeUnmount(() => {
  disposed = true
  templateGeneration += 1
  previewGeneration += 1
  loadedSelection.value = null
})

// Load available identities on mount; hiding the settings panel retains this
// editor and its current draft instead of repeating initialization.
onMounted(() => {
  void loadTemplateList()
})
</script>
