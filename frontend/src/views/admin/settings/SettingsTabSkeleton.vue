<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import EmailTemplateSkeleton from './EmailTemplateSkeleton.vue'
import type { GatewaySection, SettingsTab } from './useNavigationSettings'

// SettingsTabSkeleton.vue reserves the first settings sections for a deep link
// while bulk settings are pending. Headings and control groups follow the
// requested tab, and the gateway subsection selects its own card title and
// row count. Email uses public feature availability for its optional SMTP card.

// The requested tab and subsection come from the same navigation state as the loaded forms.
const props = defineProps<{
  tab: SettingsTab // Selected settings tab.
  gatewaySection: GatewaySection // Selected gateway subsection.
  smtpEnabled: boolean | null // Known public email verification flag.
}>()

// The translator renders headings and field labels already known before settings arrive.
const { t } = useI18n()

// gatewayCards maps provider sections to their leading cards and the visible loading row count.
const gatewayCards: Record<GatewaySection, { title: string; description: string; rows: number }> = {
  general: {
    title: 'overloadCooldown.title',
    description: 'overloadCooldown.description',
    rows: 3,
  },
  openai: {
    title: 'openAI403Cooldown.title',
    description: 'openAI403Cooldown.description',
    rows: 3,
  },
  anthropic: { title: 'betaPolicy.title', description: 'betaPolicy.description', rows: 5 },
  grok: {
    title: 'gatewayForwarding.grokTitle',
    description: 'gatewayForwarding.grokDescription',
    rows: 3,
  },
  antigravity: {
    title: 'gatewayForwarding.antigravityTitle',
    description: 'gatewayForwarding.antigravityDescription',
    rows: 3,
  },
  ollamaCloud: {
    title: 'ollamaCloudUsage.title',
    description: 'ollamaCloudUsage.description',
    rows: 3,
  },
}

// gatewayCard follows subsection navigation while the policy and forwarding controllers load.
const gatewayCard = computed(() => gatewayCards[props.gatewaySection])
</script>

<template>
  <template v-if="tab === 'security'">
    <SettingsCard
      :title="t('admin.settings.adminApiKey.title')"
      :description="t('admin.settings.adminApiKey.description')"
    >
      <SettingsSection>
        <SettingsNotice tone="warning">{{
          t('admin.settings.adminApiKey.securityWarning')
        }}</SettingsNotice>
        <div class="flex items-center justify-between gap-4">
          <Skeleton :width="144" :height="16" class="min-w-0" />
          <Skeleton :width="84" :height="36" class="shrink-0 rounded-control" />
        </div>
      </SettingsSection>
    </SettingsCard>
    <SettingsCard
      :title="t('admin.settings.registration.title')"
      :description="t('admin.settings.registration.description')"
    >
      <SettingsSection>
        <div
          v-for="key in ['enableRegistration', 'emailVerification', 'promoCode', 'invitationCode']"
          :key="key"
          class="flex items-start justify-between gap-4"
        >
          <div class="min-w-0">
            <p class="text-sm font-medium">{{ t(`admin.settings.registration.${key}`) }}</p>
            <p class="input-hint">{{ t(`admin.settings.registration.${key}Hint`) }}</p>
          </div>
          <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
        </div>
      </SettingsSection>
    </SettingsCard>
  </template>

  <SettingsCard
    v-else-if="tab === 'agreement'"
    :title="t('admin.settings.loginAgreement.title')"
    :description="t('admin.settings.loginAgreement.description')"
  >
    <template #actions><Skeleton :width="44" :height="24" class="rounded-full" /></template>
    <SettingsSection>
      <div>
        <p class="input-label">{{ t('admin.settings.loginAgreement.mode') }}</p>
        <div class="flex gap-2">
          <Skeleton :width="104" :height="32" class="rounded-control" />
          <Skeleton :width="104" :height="32" class="rounded-control" />
        </div>
      </div>
      <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <p class="input-label">{{ t('admin.settings.loginAgreement.updatedAt') }}</p>
        <Skeleton :width="0" :height="36" class="w-full rounded-control sm:w-56" />
      </div>
    </SettingsSection>
    <SettingsSection
      :title="t('admin.settings.loginAgreement.documents')"
      :hint="t('admin.settings.loginAgreement.documentsHint')"
    >
      <div
        v-for="doc in 2"
        :key="doc"
        class="space-y-3 rounded-surface border border-gray-200 p-4 dark:border-dark-700"
      >
        <Skeleton :width="138" :height="16" />
        <Skeleton :height="36" class="rounded-control" />
        <Skeleton :height="72" class="rounded-control" />
      </div>
    </SettingsSection>
  </SettingsCard>

  <template v-else-if="tab === 'features'">
    <SettingsCard
      v-for="feature in ['team', 'creative']"
      :key="feature"
      :title="t(`admin.settings.features.${feature}.title`)"
      :description="t(`admin.settings.features.${feature}.description`)"
    >
      <SettingsSection>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-sm font-medium">{{ t(`admin.settings.features.${feature}.enabled`) }}</p>
            <p class="input-hint">{{ t(`admin.settings.features.${feature}.enabledHint`) }}</p>
          </div>
          <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
        </div>
        <template v-if="feature === 'creative'">
          <div class="flex flex-col gap-2 sm:flex-row sm:justify-between">
            <div>
              <p class="text-sm font-medium">{{
                t('admin.settings.features.creative.workerCount')
              }}</p>
              <p class="input-hint">{{ t('admin.settings.features.creative.workerCountHint') }}</p>
            </div>
            <Skeleton :width="0" :height="36" class="w-full rounded-control sm:w-56" />
          </div>
          <div>
            <p class="input-label">{{ t('admin.settings.features.creative.workerUsage') }}</p>
            <Skeleton :height="10" class="rounded-full" />
          </div>
        </template>
      </SettingsSection>
    </SettingsCard>
  </template>

  <SettingsCard
    v-else-if="tab === 'users'"
    :title="t('admin.settings.defaults.title')"
    :description="t('admin.settings.defaults.description')"
  >
    <SettingsSection>
      <div class="grid gap-4 md:grid-cols-2">
        <div
          v-for="key in [
            'defaultBalance',
            'defaultConcurrency',
            'defaultUserRpmLimit',
            'defaultUserApiKeyLimit',
          ]"
          :key="key"
        >
          <p class="input-label">{{ t(`admin.settings.defaults.${key}`) }}</p>
          <Skeleton :height="36" class="rounded-control" />
          <p class="input-hint">{{ t(`admin.settings.defaults.${key}Hint`) }}</p>
        </div>
      </div>
    </SettingsSection>
    <SettingsSection
      :title="t('admin.settings.defaults.defaultSubscriptions')"
      :hint="t('admin.settings.defaults.defaultSubscriptionsHint')"
    >
      <Skeleton :width="160" :height="36" class="rounded-control" />
    </SettingsSection>
  </SettingsCard>

  <SettingsCard
    v-else-if="tab === 'gateway'"
    :title="t(`admin.settings.${gatewayCard.title}`)"
    :description="t(`admin.settings.${gatewayCard.description}`)"
  >
    <SettingsSection>
      <ContentSkeleton variant="form" :rows="gatewayCard.rows" />
    </SettingsSection>
  </SettingsCard>

  <SettingsCard
    v-else-if="tab === 'payment'"
    :title="t('admin.settings.payment.title')"
    :description="t('admin.settings.payment.description')"
  >
    <SettingsSection>
      <div class="flex items-start justify-between gap-4">
        <div>
          <p class="text-sm font-medium">{{ t('admin.settings.payment.enabled') }}</p>
          <p class="input-hint">{{ t('admin.settings.payment.enabledHint') }}</p>
        </div>
        <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
      </div>
    </SettingsSection>
  </SettingsCard>

  <template v-else-if="tab === 'email'">
    <SettingsCard
      v-if="smtpEnabled"
      :title="t('admin.settings.smtp.title')"
      :description="t('admin.settings.smtp.description')"
    >
      <template #actions><Skeleton :width="116" :height="36" class="rounded-control" /></template>
      <SettingsSection>
        <div class="grid gap-4 md:grid-cols-2">
          <div
            v-for="key in ['host', 'port', 'username', 'password', 'fromEmail', 'fromName']"
            :key="key"
          >
            <p class="input-label">{{ t(`admin.settings.smtp.${key}`) }}</p>
            <Skeleton :height="36" class="rounded-control" />
          </div>
        </div>
      </SettingsSection>
    </SettingsCard>
    <SettingsCard
      v-if="smtpEnabled"
      :title="t('admin.settings.testEmail.title')"
      :description="t('admin.settings.testEmail.description')"
    >
      <SettingsSection>
        <p class="input-label">{{ t('admin.settings.testEmail.recipientEmail') }}</p>
        <div class="flex gap-2">
          <Skeleton :height="36" class="min-w-0 rounded-control" />
          <Skeleton :width="102" :height="36" class="shrink-0 rounded-control" />
        </div>
      </SettingsSection>
    </SettingsCard>
    <SettingsNotice v-else-if="smtpEnabled === false">
      <p class="font-medium">{{ t('admin.settings.emailTabDisabledTitle') }}</p>
      <p>{{ t('admin.settings.emailTabDisabledHint') }}</p>
    </SettingsNotice>
    <SettingsCard
      :title="t('admin.settings.subscriptionExpiryNotify.title')"
      :description="t('admin.settings.subscriptionExpiryNotify.description')"
    >
      <SettingsSection>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-sm font-medium">{{
              t('admin.settings.subscriptionExpiryNotify.enabled')
            }}</p>
            <p class="input-hint">{{ t('admin.settings.subscriptionExpiryNotify.enabledHint') }}</p>
          </div>
          <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
        </div>
      </SettingsSection>
    </SettingsCard>
    <SettingsCard
      :title="t('admin.settings.emailTemplates.title')"
      :description="t('admin.settings.emailTemplates.description')"
    >
      <template #actions>
        <div class="flex w-full flex-wrap gap-2 sm:w-auto">
          <Skeleton :width="118" :height="36" class="rounded-control" />
          <Skeleton :width="104" :height="36" class="rounded-control" />
        </div>
      </template>
      <EmailTemplateSkeleton show-selectors />
    </SettingsCard>
  </template>
</template>
