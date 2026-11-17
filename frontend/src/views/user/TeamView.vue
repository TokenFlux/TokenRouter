<template>
  <AppLayout :hide-page-heading="loading || !!teamContext">
    <div class="space-y-4">
      <div v-if="transferToken" class="border-b border-gray-200 pb-6 dark:border-dark-700">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('team.transferActionTitle') }}
        </h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t('team.transferActionDescription') }}
        </p>
        <div class="mt-4 flex gap-3">
          <button
            class="btn btn-primary"
            :disabled="resolvingToken"
            @click="resolvePendingToken('accepted')"
          >
            <Icon name="check" size="sm" :animate-on-hover="false" />
            {{ t('team.accept') }}
          </button>
          <button
            class="btn btn-secondary"
            :disabled="resolvingToken"
            @click="resolvePendingToken('declined')"
          >
            <Icon name="x" size="sm" />
            {{ t('team.decline') }}
          </button>
        </div>
      </div>

      <div
        v-if="loading"
        class="space-y-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
        data-loading-skeleton
      >
        <div class="space-y-2" aria-hidden="true">
          <header class="flex flex-wrap items-end justify-between gap-4">
            <div class="min-w-0 space-y-2">
              <div class="flex items-center gap-3">
                <Skeleton :width="144" :height="28" class="min-w-0" />
                <Skeleton :width="62" :height="20" class="shrink-0 rounded-full" />
              </div>
              <Skeleton :width="156" :height="14" />
            </div>
            <div class="flex gap-2">
              <Skeleton :width="96" :height="36" class="rounded-control" />
              <Skeleton :width="86" :height="36" class="rounded-control" />
            </div>
          </header>
          <div class="flex gap-1 overflow-hidden border-b border-gray-200 dark:border-dark-700">
            <div v-for="tab in 3" :key="tab" class="flex h-9 items-center px-4">
              <Skeleton :width="52" :height="14" />
            </div>
          </div>
        </div>
        <section class="card overflow-hidden" aria-hidden="true">
          <div
            class="flex items-center justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700"
          >
            <Skeleton :width="96" :height="20" />
            <Skeleton :width="72" :height="14" />
          </div>
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <div
              v-for="member in 3"
              :key="member"
              class="flex flex-wrap items-center justify-between gap-4 p-5"
            >
              <div class="space-y-2">
                <div class="flex items-center gap-2">
                  <Skeleton :width="96" :height="16" />
                  <Skeleton :width="44" :height="18" class="rounded-full" />
                </div>
                <Skeleton :width="145" :height="14" />
              </div>
              <Skeleton :width="72" :height="28" class="rounded-control" />
            </div>
          </div>
        </section>
      </div>

      <LoadErrorState
        v-else-if="teamLoadError && !teamContext"
        class="card"
        :message="teamLoadError"
        @retry="loadInitialTeam"
      />

      <div v-else-if="!teamContext" class="mx-auto max-w-xl py-10">
        <div class="mb-8 text-center">
          <div
            class="mx-auto flex h-14 w-14 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-900/20 dark:text-primary-400"
          >
            <Icon name="users" size="xl" />
          </div>
          <h1 class="mt-5 page-title">{{ t('team.createTitle') }}</h1>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('team.createDescription') }}
          </p>
          <button class="btn btn-secondary mt-5" type="button" @click="startTeamGuide">
            <Icon name="questionCircle" size="sm" />
            {{ t('team.guideButton') }}
          </button>
        </div>
        <form class="space-y-4" data-tour="team-create-form" @submit.prevent="createTeam">
          <div>
            <label class="input-label">{{ t('team.name') }}</label>
            <input v-model.trim="createName" class="input" maxlength="100" required />
          </div>
          <button type="submit" class="btn btn-primary w-full" :disabled="submitting">
            <Icon name="plus" size="sm" />
            {{ t('team.create') }}
          </button>
        </form>
      </div>

      <template v-else>
        <div class="space-y-2">
          <header class="flex flex-wrap items-end justify-between gap-4">
            <div>
              <div class="flex flex-wrap items-center gap-3">
                <h1 class="page-title">{{ teamContext.team.name }}</h1>
                <span
                  class="badge"
                  :class="teamContext.team.status === 'active' ? 'badge-success' : 'badge-danger'"
                >
                  {{
                    teamContext.team.status === 'active'
                      ? t('team.statusActive')
                      : t('team.statusSuspended')
                  }}
                </span>
              </div>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ isOwner ? t('team.owner') : t('team.member') }} ·
                {{ t('team.memberCount', { count: teamContext.team.member_count + 1 }) }}
              </p>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button class="btn btn-secondary" type="button" @click="startTeamGuide">
                <Icon name="questionCircle" size="sm" />
                {{ t('team.guideButton') }}
              </button>
              <button class="btn btn-secondary" :disabled="refreshing" @click="refreshAll">
                <Icon name="refresh" size="sm" />
                {{ t('common.refresh') }}
              </button>
            </div>
          </header>

          <nav
            class="flex gap-1 overflow-x-auto border-b border-gray-200 dark:border-dark-700"
            aria-label="Team sections"
          >
            <button
              v-for="tab in visibleTabs"
              :key="tab.value"
              type="button"
              class="inline-flex h-9 shrink-0 items-center border-b-2 px-4 py-1.5 text-sm font-medium transition-colors"
              :class="
                activeTab === tab.value
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'
              "
              :data-tour="tab.value === 'settings' ? 'team-settings-tab' : undefined"
              @click="activeTab = tab.value"
            >
              {{ tab.label }}
            </button>
          </nav>
        </div>

        <section v-if="activeTab === 'overview'" v-content-reveal class="space-y-4">
          <div v-if="!isOwner" class="card p-6" data-tour="team-limit-progress">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('team.limitProgress') }}
            </h2>
            <div class="mt-5 grid gap-5 md:grid-cols-3">
              <div v-for="limit in memberLimits" :key="limit.label">
                <div class="flex items-center justify-between gap-3 text-sm">
                  <span class="text-gray-600 dark:text-gray-300">{{ limit.label }}</span>
                  <span class="font-medium text-gray-900 dark:text-white">
                    <template v-if="limit.limit > 0">
                      <BalanceAmount :amount="limit.used" /> /
                      <BalanceAmount :amount="limit.limit" />
                    </template>
                    <template v-else>{{ t('team.unlimited') }}</template>
                  </span>
                </div>
                <div class="mt-2 h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                  <div
                    class="h-full rounded-full"
                    :class="
                      limit.percent >= 100
                        ? 'bg-red-500'
                        : limit.percent >= 80
                          ? 'bg-amber-500'
                          : 'bg-emerald-500'
                    "
                    :style="{ width: `${limit.percent}%` }"
                  />
                </div>
              </div>
            </div>
          </div>

          <section v-if="isOwner" class="card overflow-hidden" data-tour="team-members">
            <div
              class="flex items-center justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700"
            >
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('team.members') }}
              </h2>
              <span class="text-sm text-gray-500 dark:text-gray-400">{{
                t('team.memberCount', { count: members.length })
              }}</span>
            </div>
            <div v-if="members.length === 0" class="py-12 text-center text-sm text-gray-500">
              {{ t('team.noMembers') }}
            </div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
              <div
                v-for="member in members"
                :key="member.user_id"
                class="flex flex-wrap items-center justify-between gap-4 p-5"
              >
                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <p class="truncate font-medium text-gray-900 dark:text-white">
                      {{ member.username || member.email }}
                    </p>
                    <span
                      class="badge"
                      :class="member.role === 'owner' ? 'badge-primary' : 'badge-gray'"
                      >{{ member.role === 'owner' ? t('team.owner') : t('team.member') }}</span
                    >
                  </div>
                  <p class="mt-1 truncate text-sm text-gray-500">{{ member.email }}</p>
                  <p
                    v-if="member.role === 'member'"
                    class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500"
                  >
                    <span class="inline-flex items-center gap-1"
                      >{{ t('team.daily') }} <BalanceAmount :amount="member.daily_usage_usd" /> /
                      <BalanceAmount
                        v-if="member.daily_limit_usd > 0"
                        :amount="member.daily_limit_usd"
                      /><span v-else>{{ t('team.unlimited') }}</span></span
                    >
                    <span aria-hidden="true">·</span>
                    <span class="inline-flex items-center gap-1"
                      >{{ t('team.weekly') }} <BalanceAmount :amount="member.weekly_usage_usd" /> /
                      <BalanceAmount
                        v-if="member.weekly_limit_usd > 0"
                        :amount="member.weekly_limit_usd"
                      /><span v-else>{{ t('team.unlimited') }}</span></span
                    >
                    <span aria-hidden="true">·</span>
                    <span class="inline-flex items-center gap-1"
                      >{{ t('team.monthly') }}
                      <BalanceAmount :amount="member.monthly_usage_usd" /> /
                      <BalanceAmount
                        v-if="member.monthly_limit_usd > 0"
                        :amount="member.monthly_limit_usd"
                      /><span v-else>{{ t('team.unlimited') }}</span></span
                    >
                  </p>
                </div>
                <div v-if="member.role === 'member'" class="flex flex-wrap gap-2">
                  <button class="btn btn-secondary btn-sm" @click="openLimitEditor(member)">
                    <Icon name="edit" size="sm" />{{ t('team.editLimits') }}
                  </button>
                  <button class="btn btn-secondary btn-sm" @click="askTransfer(member)">
                    <Icon name="swap" size="sm" />{{ t('team.transfer') }}
                  </button>
                  <button class="btn btn-danger btn-sm" @click="askRemove(member)">
                    <Icon name="trash" size="sm" />{{ t('team.remove') }}
                  </button>
                </div>
              </div>
            </div>
          </section>

          <section v-if="isOwner" class="space-y-4" data-tour="team-invitations">
            <div class="space-y-2">
              <div class="flex items-center justify-between gap-4">
                <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                  {{ t('team.invitations') }}
                </h2>
                <span class="text-sm text-gray-500 dark:text-gray-400">{{
                  invitations.length
                }}</span>
              </div>
              <form class="flex flex-col gap-3 sm:flex-row" @submit.prevent="sendInvitation">
                <input
                  v-model.trim="inviteEmail"
                  type="email"
                  class="input flex-1"
                  :placeholder="t('team.inviteEmail')"
                  required
                />
                <button class="btn btn-primary" :disabled="submitting">
                  <Icon name="mail" size="sm" />{{ t('team.sendInvite') }}
                </button>
              </form>
            </div>
            <div class="card divide-y divide-gray-100 dark:divide-dark-700">
              <div v-if="invitations.length === 0" class="py-12 text-center text-sm text-gray-500">
                {{ t('team.noInvitations') }}
              </div>
              <div
                v-for="invitation in invitations"
                :key="invitation.id"
                class="flex flex-wrap items-center justify-between gap-4 p-5"
              >
                <div>
                  <p class="font-medium text-gray-900 dark:text-white">{{ invitation.email }}</p>
                  <p class="mt-1 text-sm text-gray-500">
                    {{ statusLabel(invitation.status) }} · {{ t('team.expiresAt') }}
                    {{ formatDateTime(invitation.expires_at) }}
                  </p>
                </div>
                <div v-if="invitation.status === 'pending'" class="flex gap-2">
                  <button
                    class="btn btn-secondary btn-sm"
                    @click="reissueInvitation(invitation.id)"
                  >
                    <Icon name="refresh" size="sm" />{{ t('team.reissue') }}</button
                  ><button class="btn btn-danger btn-sm" @click="revokeInvitation(invitation.id)">
                    <Icon name="x" size="sm" />{{ t('team.revoke') }}
                  </button>
                </div>
              </div>
            </div>
          </section>
        </section>

        <section v-else-if="activeTab === 'keys'" class="space-y-4">
          <div class="card overflow-hidden">
            <div
              class="flex items-center justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700"
            >
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('team.keys') }}
              </h2>
              <span class="text-sm text-gray-500 dark:text-gray-400">{{ teamKeys.length }}</span>
            </div>
            <div v-if="teamKeys.length === 0" class="py-12 text-center text-sm text-gray-500">
              {{ t('team.noKeys') }}
            </div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
              <div
                v-for="key in teamKeys"
                :key="key.id"
                class="flex flex-wrap items-center justify-between gap-4 p-5"
              >
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <p class="font-medium text-gray-900 dark:text-white">{{ key.name }}</p>
                    <span
                      class="badge"
                      :class="
                        key.team_owner_disabled || key.status !== 'active'
                          ? 'badge-gray'
                          : 'badge-success'
                      "
                    >
                      {{
                        key.team_owner_disabled ? t('team.ownerDisabled') : statusLabel(key.status)
                      }}
                    </span>
                  </div>
                  <p class="mt-1 truncate font-mono text-xs text-gray-500">{{ key.masked_key }}</p>
                  <p class="mt-1 text-xs text-gray-500">
                    {{ key.user_email }} · {{ key.group_name || t('team.noGroup') }}
                  </p>
                </div>
                <div class="flex flex-wrap gap-2">
                  <button
                    v-if="key.team_owner_disabled || key.status !== 'active'"
                    class="btn btn-secondary btn-sm"
                    @click="askEnableKey(key)"
                  >
                    <Icon name="play" size="sm" />{{ t('team.enable') }}
                  </button>
                  <button v-else class="btn btn-secondary btn-sm" @click="askDisableKey(key)">
                    <Icon name="ban" size="sm" />{{ t('team.disable') }}
                  </button>
                  <button class="btn btn-danger btn-sm" @click="askDeleteKey(key)">
                    <Icon name="trash" size="sm" />{{ t('common.delete') }}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section v-else class="space-y-4">
          <div v-if="isOwner" class="card p-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('team.name') }}
            </h2>
            <form class="mt-4 flex flex-col gap-3 sm:flex-row" @submit.prevent="renameTeam">
              <input
                v-model.trim="renameName"
                class="input flex-1"
                required
                maxlength="100"
              /><button class="btn btn-primary" :disabled="submitting">
                {{ t('team.rename') }}
              </button>
            </form>
          </div>
          <div v-if="isOwner" class="card p-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('team.defaultMemberLimits') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('team.defaultMemberLimitsDescription') }}
            </p>
            <form class="mt-5" @submit.prevent="saveDefaultMemberLimits">
              <div class="grid gap-4 md:grid-cols-3">
                <div v-for="field in defaultLimitFields" :key="field.key">
                  <label class="input-label">{{ field.label }}</label>
                  <div class="relative">
                    <span
                      class="pointer-events-none absolute left-3 top-1/2 inline-flex -translate-y-1/2 items-center text-sm text-gray-400"
                    >
                      <BalanceIcon v-if="hasCustomBalanceIcon" size="sm" />
                      <span v-else>{{ balanceUnitSymbol }}</span>
                    </span>
                    <input
                      v-model.number="defaultLimitForm[field.key]"
                      type="number"
                      min="0"
                      step="0.01"
                      class="input pl-10"
                      required
                    />
                  </div>
                </div>
              </div>
              <div class="mt-5 flex justify-end">
                <button class="btn btn-primary" :disabled="submitting">
                  {{ t('team.saveDefaultLimits') }}
                </button>
              </div>
            </form>
          </div>
          <!-- Keep lifecycle actions beside their explanation in the same settings row. -->
          <div class="card overflow-hidden">
            <div
              v-if="isOwner"
              class="flex flex-col gap-4 px-5 py-5 sm:flex-row sm:items-end sm:justify-between"
            >
              <div class="flex min-w-0 items-start gap-3">
                <div
                  class="flex h-10 w-10 shrink-0 items-center justify-center rounded-control"
                  :class="
                    teamContext.team.status === 'active'
                      ? 'bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-400'
                      : 'bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-400'
                  "
                >
                  <Icon
                    :name="teamContext.team.status === 'active' ? 'checkCircle' : 'ban'"
                    size="md"
                  />
                </div>
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                      {{ t('team.teamStatus') }}
                    </h2>
                    <span
                      class="badge"
                      :class="
                        teamContext.team.status === 'active' ? 'badge-success' : 'badge-danger'
                      "
                    >
                      {{
                        teamContext.team.status === 'active'
                          ? t('team.statusActive')
                          : t('team.statusSuspended')
                      }}
                    </span>
                  </div>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    {{ t('team.teamStatusDescription') }}
                  </p>
                </div>
              </div>
              <button
                type="button"
                class="btn btn-secondary w-full shrink-0 justify-center sm:w-auto sm:min-w-[9rem]"
                @click="askSetStatus"
              >
                <Icon :name="teamContext.team.status === 'active' ? 'ban' : 'play'" size="sm" />
                {{ teamContext.team.status === 'active' ? t('team.pause') : t('team.resume') }}
              </button>
            </div>
            <div
              class="flex flex-col gap-4 bg-gray-50/70 px-5 py-5 dark:bg-dark-800/40 sm:flex-row sm:items-end sm:justify-between"
              :class="isOwner ? 'border-t border-gray-200 dark:border-dark-700' : ''"
            >
              <div class="flex min-w-0 items-start gap-3">
                <div
                  class="flex h-10 w-10 shrink-0 items-center justify-center rounded-control bg-red-50 text-red-600 dark:bg-red-900/20 dark:text-red-400"
                >
                  <Icon :name="isOwner ? 'exclamationTriangle' : 'arrowRight'" size="md" />
                </div>
                <div class="min-w-0">
                  <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                    {{ isOwner ? t('team.dissolve') : t('team.leave') }}
                  </h2>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    {{ isOwner ? t('team.dissolveMessage') : t('team.leaveMessage') }}
                  </p>
                </div>
              </div>
              <button
                v-if="isOwner"
                type="button"
                class="btn btn-danger w-full shrink-0 justify-center sm:w-auto sm:min-w-[9rem]"
                @click="askDissolve"
              >
                <Icon name="trash" size="sm" />
                {{ t('team.dissolve') }}
              </button>
              <button
                v-else
                type="button"
                class="btn btn-danger w-full shrink-0 justify-center sm:w-auto sm:min-w-[9rem]"
                @click="askLeave"
              >
                <Icon name="arrowRight" size="sm" />
                {{ t('team.leave') }}
              </button>
            </div>
          </div>
        </section>
      </template>
    </div>

    <TeamInvitationDialog
      :show="Boolean(invitationToken)"
      :loading="invitationPreviewLoading"
      :resolving="resolvingToken"
      :preview="invitationPreview"
      :error="invitationPreviewError"
      @close="closeInvitationDialog"
      @resolve="resolvePendingToken"
    />

    <BaseDialog
      :show="Boolean(limitTarget)"
      :title="t('team.editLimits')"
      width="narrow"
      @close="limitTarget = null"
    >
      <form id="team-limit-form" class="space-y-4" @submit.prevent="saveLimits">
        <div v-for="field in limitFields" :key="field.key">
          <label class="input-label">{{ field.label }}</label
          ><input
            v-model.number="limitForm[field.key]"
            type="number"
            min="0"
            step="0.01"
            class="input"
          />
        </div>
        <div class="border-t border-gray-200 pt-4 dark:border-dark-700">
          <p class="input-label">{{ t('team.resetUsage') }}</p>
          <div class="flex flex-wrap gap-4 text-sm text-gray-600 dark:text-gray-300">
            <label v-for="period in resetPeriods" :key="period.key" class="flex items-center gap-2"
              ><input
                v-model="resetForm[period.key]"
                type="checkbox"
                class="h-4 w-4 rounded-compact border-gray-300 text-primary-600"
              />{{ period.label }}</label
            >
          </div>
        </div>
      </form>
      <template #footer
        ><div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="limitTarget = null">
            {{ t('common.cancel') }}</button
          ><button form="team-limit-form" type="submit" class="btn btn-primary">
            {{ t('team.saveLimits') }}
          </button>
        </div></template
      >
    </BaseDialog>

    <ConfirmDialog
      :show="Boolean(confirmAction)"
      :title="confirmAction?.title || ''"
      :message="confirmAction?.message || ''"
      :danger="confirmAction?.danger"
      @cancel="confirmAction = null"
      @confirm="runConfirmedAction"
    />
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import Skeleton from '@/components/common/Skeleton.vue'
import { vContentReveal } from '@/directives/contentReveal'

import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import LoadErrorState from '@/components/common/LoadErrorState.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TeamInvitationDialog from '@/components/team/TeamInvitationDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import BalanceAmount from '@/components/common/BalanceAmount.vue'
import BalanceIcon from '@/components/common/BalanceIcon.vue'
import {
  teamAPI,
  type TeamAPIKey,
  type TeamContext,
  type TeamInvitation,
  type TeamInvitationPreview,
  type TeamMembership,
} from '@/api/team'
import { useAppStore } from '@/stores/app'
import { useOnboardingStore } from '@/stores/onboarding'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { formatDateTime } from '@/utils/format'

// TeamView.vue manages team membership, invitations, owner key operations, and member
// spending limits. Initial invitation preview loading runs independently from current-team
// loading so a user can inspect an incoming invitation even before membership context is
// available. Role and team status determine which management lists are requested and
// displayed.
//
// Confirmed operations can require step-up authentication, and cancellation is handled
// separately from server errors. Member-limit saving persists quota values before optional
// usage resets, so partial completion remains possible across those requests. Invitation and
// transfer resolution consume URL tokens before refreshing context, while each form or
// confirmation retains the page state required by its selected action.

// TeamTab identifies the overview, owner key list, or team settings section.
type TeamTab = 'overview' | 'keys' | 'settings'
// LimitKey identifies the spending cap field for each member quota window.
type LimitKey = 'daily_limit_usd' | 'weekly_limit_usd' | 'monthly_limit_usd'
// ResetKey identifies which member usage windows a limit edit should reset.
type ResetKey = 'daily' | 'weekly' | 'monthly'

// Route tokens, team requests, step-up authentication, onboarding, and balance display
// dependencies coordinate account membership operations from this page.
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const onboardingStore = useOnboardingStore()
const stepUp = useStepUp()
const { balanceUnitSymbol, hasCustomBalanceIcon } = useBalanceDisplay()
// Loading and operation flags distinguish initial data, refresh, form submission, and
// invitation resolution; preview state is loaded independently from current membership.
const loading = ref(true)
const teamLoadError = ref('')
const refreshing = ref(false)
const submitting = ref(false)
const resolvingToken = ref(false)
const invitationPreviewLoading = ref(false)
const invitationPreview = ref<TeamInvitationPreview | null>(null)
const invitationPreviewError = ref('')
// teamContext owns the current membership and team; member, invitation, and key lists are
// populated according to that membership's role and team status.
const teamContext = ref<TeamContext | null>(null)
const members = ref<TeamMembership[]>([])
const invitations = ref<TeamInvitation[]>([])
const teamKeys = ref<TeamAPIKey[]>([])
// Tab and form drafts retain page-local editing state; defaultLimitForm sends daily, weekly,
// and monthly limits when the owner saves team defaults.
const activeTab = ref<TeamTab>('overview')
const createName = ref('')
const renameName = ref('')
const inviteEmail = ref('')
const defaultLimitForm = reactive<Record<LimitKey, number>>({
  daily_limit_usd: 0,
  weekly_limit_usd: 0,
  monthly_limit_usd: 0,
})
// Route query tokens select an invitation preview or ownership-transfer confirmation, while
// isOwner controls management tabs and owner-only requests.
const invitationToken = computed(() =>
  typeof route.query.invitation === 'string' ? route.query.invitation : '',
)
const transferToken = computed(() =>
  typeof route.query.transfer === 'string' ? route.query.transfer : '',
)
const isOwner = computed(() => teamContext.value?.membership.role === 'owner')

// loadInvitationPreview retrieves the invitation described by the current URL independently
// of membership loading. It clears prior preview feedback first, skips empty tokens, and
// exposes server failure text within the confirmation dialog.
const loadInvitationPreview = async () => {
  invitationPreview.value = null
  invitationPreviewError.value = ''
  if (!invitationToken.value) return
  invitationPreviewLoading.value = true
  try {
    invitationPreview.value = await teamAPI.previewInvitation(invitationToken.value)
  } catch (error: any) {
    invitationPreviewError.value = error?.message || t('team.invitationLoadFailed')
  } finally {
    invitationPreviewLoading.value = false
  }
}

// closeInvitationDialog removes the invitation token while preserving other route query
// values. It clears preview data and errors after navigation finishes so the dialog no
// longer represents that invitation.
const closeInvitationDialog = async () => {
  const query = { ...route.query }
  delete query.invitation
  await router.replace({ query })
  invitationPreview.value = null
  invitationPreviewError.value = ''
}

// startTeamGuide opens overview when a team exists and starts the appropriate onboarding
// sequence. The global guide waits for DOM updates after tab selection before locating its
// overview anchors.
const startTeamGuide = () => {
  if (teamContext.value) activeTab.value = 'overview'
  onboardingStore.startTeamGuide({
    isOwner: isOwner.value,
    hasTeam: Boolean(teamContext.value),
  })
}

// visibleTabs exposes the key-management section to owners while overview and settings
// remain available to all team members.
const visibleTabs = computed(() => {
  const tabs: Array<{
    value: TeamTab // Team section tab identity.
    label: string // Localized team section label.
  }> = [{ value: 'overview', label: t('team.overview') }]
  if (isOwner.value) tabs.push({ value: 'keys', label: t('team.keys') })
  tabs.push({ value: 'settings', label: t('team.settings') })
  return tabs
})
// defaultLimitFields pairs each editable team default quota with its translated daily,
// weekly, or monthly label for the settings form.
const defaultLimitFields = computed(() => [
  { key: 'daily_limit_usd' as const, label: t('team.defaultDailyLimit') },
  { key: 'weekly_limit_usd' as const, label: t('team.defaultWeeklyLimit') },
  { key: 'monthly_limit_usd' as const, label: t('team.defaultMonthlyLimit') },
])

// statusLabel localizes recognized team, membership, and invitation states for the page. An
// unknown status is returned as supplied so new server states remain visible rather than
// becoming blank.
const statusLabel = (status: string) => {
  const labels: Record<string, string> = {
    active: t('team.statusActive'),
    inactive: t('team.statusInactive'),
    disabled: t('team.statusDisabled'),
    pending: t('team.statusPending'),
    accepted: t('team.statusAccepted'),
    declined: t('team.statusDeclined'),
    revoked: t('team.statusRevoked'),
    expired: t('team.statusExpired'),
  }
  return labels[status] || status
}
// memberLimits displays the current membership's quota windows with capped percentages,
// treating nonpositive limits as an unfilled unlimited bar.
const memberLimits = computed(() => {
  const membership = teamContext.value?.membership
  if (!membership) return []
  return [
    { label: t('team.daily'), used: membership.daily_usage_usd, limit: membership.daily_limit_usd },
    {
      label: t('team.weekly'),
      used: membership.weekly_usage_usd,
      limit: membership.weekly_limit_usd,
    },
    {
      label: t('team.monthly'),
      used: membership.monthly_usage_usd,
      limit: membership.monthly_limit_usd,
    },
  ].map((item) => ({
    ...item,
    percent: item.limit > 0 ? Math.min(100, (item.used / item.limit) * 100) : 0,
  }))
})
// isNoTeamError recognizes missing-team or membership responses as the account's empty team
// state.
const isNoTeamError = (error: any) =>
  error?.reason === 'TEAM_NOT_FOUND' ||
  error?.reason === 'TEAM_MEMBERSHIP_REQUIRED' ||
  error?.response?.status === 404
// loadContext loads the current team and copies its editable name and default limits into
// drafts. Missing membership clears teamContext, while other failures propagate to the page
// refresh or mount handler.
const loadContext = async () => {
  try {
    teamContext.value = await teamAPI.current()
    renameName.value = teamContext.value.team.name
    defaultLimitForm.daily_limit_usd = teamContext.value.team.default_daily_limit_usd
    defaultLimitForm.weekly_limit_usd = teamContext.value.team.default_weekly_limit_usd
    defaultLimitForm.monthly_limit_usd = teamContext.value.team.default_monthly_limit_usd
  } catch (error) {
    // Missing membership is the page's empty-team state, so clear its context;
    // propagate other failures to the caller's page-level error handling.
    if (isNoTeamError(error)) teamContext.value = null
    else throw error
  }
}
// loadTeamData loads management lists appropriate to the current context. Inactive teams
// clear members and invitations but still permit owner key retrieval; active owners request
// all three lists concurrently, with each successful list publishing independently.
const loadTeamData = async () => {
  if (!teamContext.value) return
  if (teamContext.value.team.status !== 'active') {
    members.value = []
    invitations.value = []
    teamKeys.value = isOwner.value ? await teamAPI.keys() : []
    return
  }
  const requests: Promise<unknown>[] = []
  if (isOwner.value) {
    requests.push(
      teamAPI.members().then((value) => {
        members.value = value
      }),
    )
    requests.push(
      teamAPI.invitations().then((value) => {
        invitations.value = value
      }),
    )
    requests.push(
      teamAPI.keys().then((value) => {
        teamKeys.value = value
      }),
    )
  }
  await Promise.all(requests)
}
// refreshAll reloads team context before requesting the lists authorized by its resulting
// role and status. It reports errors through the app store and clears the refresh indicator
// after either outcome.
const refreshAll = async () => {
  refreshing.value = true
  try {
    await loadContext()
    await loadTeamData()
  } catch (error: any) {
    appStore.showError(error?.message || t('team.loadFailed'))
  } finally {
    refreshing.value = false
  }
}

// createTeam submits the name draft, publishes the created context, and loads its management
// data. A later data-load failure can follow successful creation, with the shared submission
// indicator reset afterward.
const createTeam = async () => {
  submitting.value = true
  try {
    teamContext.value = await teamAPI.create(createName.value)
    appStore.showSuccess(t('team.created'))
    await loadTeamData()
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    submitting.value = false
  }
}
// renameTeam saves the name draft and publishes the returned team context. It reports API
// failure while retaining the draft, and releases the shared submission flag when the
// request finishes.
const renameTeam = async () => {
  submitting.value = true
  try {
    teamContext.value = await teamAPI.rename(renameName.value)
    appStore.showSuccess(t('team.updated'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    submitting.value = false
  }
}
// saveDefaultMemberLimits saves all three owner-configured default quota values and
// publishes the returned context. The shared submitting flag covers the request, and
// failures report an error while retaining the drafts.
const saveDefaultMemberLimits = async () => {
  submitting.value = true
  try {
    teamContext.value = await teamAPI.updateDefaultMemberLimits({
      default_daily_limit_usd: defaultLimitForm.daily_limit_usd,
      default_weekly_limit_usd: defaultLimitForm.weekly_limit_usd,
      default_monthly_limit_usd: defaultLimitForm.monthly_limit_usd,
    })
    appStore.showSuccess(t('team.defaultLimitsUpdated'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    submitting.value = false
  }
}
// sendInvitation creates an invitation for the email draft and then reloads the invitation
// list. Successful creation clears the address before refresh, so list failure can occur
// after the invitation already exists.
const sendInvitation = async () => {
  submitting.value = true
  try {
    await teamAPI.invite(inviteEmail.value)
    inviteEmail.value = ''
    invitations.value = await teamAPI.invitations()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    submitting.value = false
  }
}
// reissueInvitation reissues the selected invitation and refreshes the owner list before
// reporting success. An error may come from either the mutation or its following list
// refresh.
const reissueInvitation = async (id: number) => {
  try {
    await teamAPI.reissueInvitation(id)
    invitations.value = await teamAPI.invitations()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  }
}
// revokeInvitation revokes the selected invitation and refreshes the owner list before
// reporting success. An error may come from either the mutation or its following list
// refresh.
const revokeInvitation = async (id: number) => {
  try {
    await teamAPI.revokeInvitation(id)
    invitations.value = await teamAPI.invitations()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  }
}

// limitTarget identifies the edited member, limitForm copies its quota values, and resetForm
// independently selects usage windows to reset after those limits save.
const limitTarget = ref<TeamMembership | null>(null)
const limitForm = reactive<Record<LimitKey, number>>({
  daily_limit_usd: 0,
  weekly_limit_usd: 0,
  monthly_limit_usd: 0,
})
const resetForm = reactive<Record<ResetKey, boolean>>({
  daily: false,
  weekly: false,
  monthly: false,
})
// limitFields and resetPeriods pair quota and reset field identities with matching period
// labels for the member editor.
const limitFields = computed(() => [
  { key: 'daily_limit_usd' as const, label: t('team.daily') },
  { key: 'weekly_limit_usd' as const, label: t('team.weekly') },
  { key: 'monthly_limit_usd' as const, label: t('team.monthly') },
])
const resetPeriods = computed(() => [
  { key: 'daily' as const, label: t('team.daily') },
  { key: 'weekly' as const, label: t('team.weekly') },
  { key: 'monthly' as const, label: t('team.monthly') },
])
// openLimitEditor copies the selected member limits and clears all pending reset choices.
const openLimitEditor = (member: TeamMembership) => {
  limitTarget.value = member
  limitForm.daily_limit_usd = member.daily_limit_usd
  limitForm.weekly_limit_usd = member.weekly_limit_usd
  limitForm.monthly_limit_usd = member.monthly_limit_usd
  resetForm.daily = resetForm.weekly = resetForm.monthly = false
}
// saveLimits writes the selected member's quota values before optionally resetting usage
// windows. These are separate persistent operations, so a reset failure can leave the new
// limits saved while the editor remains open. After both succeed, the editor closes and page
// data refreshes before success feedback. The target and drafts remain page-owned during
// awaiting, so the UI is responsible for keeping the edited member stable through
// submission.
const saveLimits = async () => {
  if (!limitTarget.value) return
  try {
    await teamAPI.updateLimits(limitTarget.value.user_id, { ...limitForm })
    // Limits are persisted before optional usage resets. A reset failure leaves the saved
    // caps in place and keeps the editor available to retry.
    if (resetForm.daily || resetForm.weekly || resetForm.monthly)
      await teamAPI.resetUsage(limitTarget.value.user_id, { ...resetForm })
    limitTarget.value = null
    await refreshAll()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  }
}

// ConfirmAction carries the confirmation text and deferred asynchronous team operation
// selected by a page action.
type ConfirmAction = {
  title: string // Confirmation dialog heading.
  message: string // Operation consequence explanation.
  danger?: boolean // Destructive action visual flag.
  // run performs the selected operation after confirmation; its promise includes
  // any required step-up flow and refresh work before success feedback is shown.
  run: () => Promise<void>
}
// confirmAction stores one pending confirmation; executing it clears the dialog before
// awaiting the selected operation or any step-up authentication.
const confirmAction = ref<ConfirmAction | null>(null)
// runConfirmedAction consumes the pending confirmation and executes its deferred operation.
// Step-up cancellation remains quiet, other failures show an error, and resolved operations
// receive shared success feedback after their own refresh work.
const runConfirmedAction = async () => {
  const action = confirmAction.value
  confirmAction.value = null
  if (!action) return
  try {
    await action.run()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    // Closing step-up authentication cancels the pending action intentionally;
    // keep that path quiet while reporting failures from attempted team operations.
    if (!isStepUpCancelled(error)) appStore.showError(error?.message || t('common.error'))
  }
}
// askRemove prepares confirmation to remove the member and then refresh team data.
const askRemove = (member: TeamMembership) => {
  confirmAction.value = {
    title: t('team.removeTitle'),
    message: t('team.removeMessage'),
    danger: true,
    run: async () => {
      await teamAPI.removeMember(member.user_id)
      await refreshAll()
    },
  }
}
// askTransfer prepares confirmation for a step-up protected ownership-transfer request.
const askTransfer = (member: TeamMembership) => {
  confirmAction.value = {
    title: t('team.transferTitle'),
    message: t('team.transferMessage'),
    run: async () => {
      await stepUp.run(() => teamAPI.startTransfer(member.user_id))
    },
  }
}
// askLeave prepares confirmation to leave the team and clear local membership.
const askLeave = () => {
  confirmAction.value = {
    title: t('team.leaveTitle'),
    message: t('team.leaveMessage'),
    danger: true,
    run: async () => {
      await teamAPI.leave()
      teamContext.value = null
    },
  }
}
// askDissolve prepares confirmation for step-up protected team dissolution and clears local
// context after success.
const askDissolve = () => {
  confirmAction.value = {
    title: t('team.dissolveTitle'),
    message: t('team.dissolveMessage'),
    danger: true,
    run: async () => {
      await stepUp.run(() => teamAPI.dissolve())
      teamContext.value = null
    },
  }
}
// askSetStatus prepares a pause or resume operation for the current team. The confirmed
// action uses step-up authentication, publishes the returned context, and reloads lists
// according to its new status.
const askSetStatus = () => {
  if (!teamContext.value) return
  const nextStatus = teamContext.value.team.status === 'active' ? 'suspended' : 'active'
  confirmAction.value = {
    title: nextStatus === 'suspended' ? t('team.pauseTitle') : t('team.resumeTitle'),
    message: nextStatus === 'suspended' ? t('team.pauseMessage') : t('team.resumeMessage'),
    danger: nextStatus === 'suspended',
    run: async () => {
      teamContext.value = await stepUp.run(() => teamAPI.setStatus(nextStatus))
      await loadTeamData()
    },
  }
}

// askDisableKey prepares confirmation to disable a team key and refresh the key list.
const askDisableKey = (key: TeamAPIKey) => {
  confirmAction.value = {
    title: t('team.disableKeyTitle'),
    message: t('team.disableKeyMessage'),
    run: async () => {
      await teamAPI.disableKey(key.id)
      teamKeys.value = await teamAPI.keys()
    },
  }
}
// askEnableKey prepares confirmation to enable a team key and refresh the key list.
const askEnableKey = (key: TeamAPIKey) => {
  confirmAction.value = {
    title: t('team.enableKeyTitle'),
    message: t('team.enableKeyMessage'),
    run: async () => {
      await teamAPI.enableKey(key.id)
      teamKeys.value = await teamAPI.keys()
    },
  }
}
// askDeleteKey prepares confirmation to delete a team key and refresh the key list.
const askDeleteKey = (key: TeamAPIKey) => {
  confirmAction.value = {
    title: t('team.deleteKeyTitle'),
    message: t('team.deleteKeyMessage'),
    danger: true,
    run: async () => {
      await teamAPI.deleteKey(key.id)
      teamKeys.value = await teamAPI.keys()
    },
  }
}

// resolvePendingToken accepts or declines the invitation or transfer identified by the URL,
// preferring an invitation when both tokens exist. Transfer resolution uses step-up
// authentication. After success it removes the query, clears preview state, and refreshes
// the current membership before reporting completion. Cancellation of step-up remains quiet,
// while other failures preserve the unresolved route state if they occur before navigation
// completes.
const resolvePendingToken = async (resolution: 'accepted' | 'declined') => {
  resolvingToken.value = true
  try {
    if (invitationToken.value) await teamAPI.resolveInvitation(invitationToken.value, resolution)
    else if (transferToken.value)
      await stepUp.run(() => teamAPI.resolveTransfer(transferToken.value, resolution))
    await router.replace({ query: {} })
    invitationPreview.value = null
    invitationPreviewError.value = ''
    await refreshAll()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    // Closing step-up authentication cancels the pending action intentionally;
    // keep that path quiet while reporting failures from attempted team operations.
    if (!isStepUpCancelled(error)) appStore.showError(error?.message || t('common.error'))
  } finally {
    resolvingToken.value = false
  }
}

// loadInitialTeam reads membership before deciding whether to show team setup.
// A failed membership request leaves a retry, while a known team keeps its
// shell if one of its secondary lists fails.
async function loadInitialTeam() {
  loading.value = true
  teamLoadError.value = ''
  try {
    await loadContext()
    await loadTeamData()
  } catch (error: any) {
    teamLoadError.value = error?.message || t('team.loadFailed')
    if (teamContext.value) appStore.showError(teamLoadError.value)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadInvitationPreview()
  void loadInitialTeam()
})
useLocaleRefresh(loadTeamData)
</script>
