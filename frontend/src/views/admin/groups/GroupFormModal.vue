<template>
  <BaseDialog
    :show="show"
    :body-scroll="false"
    :close-on-escape="!showAdvancedSchedulerOverridesModal"
    :title="mode === 'create' ? t('admin.groups.createGroup') : t('admin.groups.editGroup')"
    width="wide"
    @close="handleClose"
  >
    <form
      v-if="mode === 'create' || group"
      :id="`${mode}-group-form`"
      novalidate
      class="flex min-h-0 flex-1 flex-col"
      @submit.prevent="handleSubmit"
    >
      <GroupSettingsForm
        ref="settingsRef"
        :mode="mode"
        :model-value="form"
        :options="{
          ...groupSelectOptions,
          probeModels: availabilityProbeModelOptions,
        }"
        :routing-rules="modelRoutingRules"
        :models-list="modelsListState"
        :models-list-loading="modelsListLoading"
        :provider-search="{
          keywords: providerSearchKeyword,
          results: providerSearchResults,
          open: showProviderDropdown,
        }"
        :get-rule-key="getRuleSearchKey"
        @patch="Object.assign(form, $event)"
        @configure-scheduler="openAdvancedSchedulerOverrides"
        @add-rule="addRoutingRule"
        @remove-rule="removeRoutingRule"
        @rule-pattern="setRulePattern"
        @search-providers="updateProviderSearch"
        @focus-providers="onProviderSearchFocus"
        @select-provider="selectProvider"
        @remove-provider="removeSelectedProvider"
        @models-enabled="modelsListState.enabled = $event"
        @select-model="(id, value) => setModelSelection(id, value)"
        @select-all-models="selectAllModelsListItems(modelsListState)"
        @invert-models="invertModelsListSelection(modelsListState)"
        @move-model="(from, to) => moveModelsListItem(modelsListState, from, to)"
      />
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          :form="`${mode}-group-form`"
          :disabled="submitting || !protocolCatalog"
          class="btn btn-primary"
          data-tour="group-form-submit"
        >
          <Icon
            v-if="submitting"
            name="loader"
            size="sm"
            :animate-on-hover="false"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
          />
          {{
            mode === 'create'
              ? submitting
                ? t('admin.groups.creating')
                : t('common.create')
              : submitting
                ? t('admin.groups.updating')
                : t('common.update')
          }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <GroupAdvancedSchedulerOverridesModal
    :show="showAdvancedSchedulerOverridesModal"
    :model-value="advancedSchedulerOverridesDraft"
    @close="closeAdvancedSchedulerOverrides"
    @save="saveAdvancedSchedulerOverrides"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useOnboardingStore } from '@/stores/onboarding'
import { adminAPI } from '@/api/admin'
import { protocolCatalog } from '@/api/admin/protocolCapabilities'
import type { AdminGroup, GroupAdvancedSchedulerOverrides } from '@/api/groups.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import GroupAdvancedSchedulerOverridesModal from '@/components/admin/group/GroupAdvancedSchedulerOverridesModal.vue'
import GroupSettingsForm from '@/components/admin/group/GroupSettingsForm.vue'
import type {
  GroupModelRoutingRule,
  GroupRoutingProvider,
} from '@/components/admin/group/groupSettingsTypes'
import {
  createModelsListState as createInitialModelsListState,
  getAvailabilityProbeCandidateModels,
  invertModelsListSelection,
  moveModelsListItem,
  selectAllModelsListItems,
  setModelsListCandidates,
} from '@/components/admin/group/groupsModelsList'
import { createModelsListCandidatesTracker } from '@/components/admin/group/groupsModelsListCandidates'
import { useKeyedDebouncedSearch } from '@/composables/useKeyedDebouncedSearch'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import { extractApiErrorMessage } from '@/utils/apiError'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import {
  buildAvailabilityProbeConfig,
  buildCreateGroupRequest,
  buildGroupFormGroupOptions,
  buildPlaceholderRoutingRules,
  buildUpdateGroupRequest,
  cloneAdvancedSchedulerOverrides,
  createGroupFormDraft,
  hydrateGroupFormDraft,
  hydrateRoutingRulesConcurrently,
  initializeGroupProtocolDefaults,
  isAvailabilityProbeProtocolAvailable,
  resetGroupFormDraft,
} from './groupFormDraft'

// GroupFormModal.vue owns group creation and editing inside one shared dialog.
// Opening in edit mode hydrates the group draft immediately with placeholder
// `#id` routing entries and resolves provider display names concurrently in the
// background so large routing maps never block dialog presentation.
//
// Model-candidate lookups, routing-rule provider hydration, and per-rule
// debounced provider searches each carry cancellation or sequence guards so
// closing the dialog, switching groups, or deleting a rule discards stale
// responses before they can mutate active editor state.

// Props supplies dialog visibility, editor mode, the target group in edit mode,
// and the full group list used to filter fallback and provider-copy options.
interface Props {
  show: boolean // Parent-controlled dialog visibility.
  mode: 'create' | 'edit' // Selected group workflow mode.
  group?: AdminGroup | null // Target group record when mode is edit.
  allGroups: AdminGroup[] // Complete unpaginated group list for option selectors.
}

const props = withDefaults(defineProps<Props>(), {
  group: null,
})

const emit = defineEmits<{
  close: [] // Requests parent dialog dismissal.
  saved: [mode: 'create' | 'edit'] // Notifies parent after create or update succeeds.
}>()

const { t } = useI18n()
const appStore = useAppStore()
const onboardingStore = useOnboardingStore()

// form, modelRoutingRules, and modelsListState hold the active dialog draft.
const form = reactive(createGroupFormDraft())
const modelRoutingRules = ref<GroupModelRoutingRule[]>([])
const modelsListState = reactive(createInitialModelsListState())
const modelsListLoading = ref(false)
const submitting = ref(false)
const settingsRef = ref<InstanceType<typeof GroupSettingsForm> | null>(null)

// protocolDefaultsPending tracks whether a create draft still awaits initial
// protocol-catalog loading before applying catalog defaults once.
const protocolDefaultsPending = ref(!initializeGroupProtocolDefaults(form, protocolCatalog.value))

// showAdvancedSchedulerOverridesModal and advancedSchedulerOverridesDraft
// isolate nested scheduler parameter edits until confirmed.
const showAdvancedSchedulerOverridesModal = ref(false)
const advancedSchedulerOverridesDraft = ref<GroupAdvancedSchedulerOverrides>({})

// routingHydrationSeq and routingRulesTouched prevent late concurrent provider
// name lookups from overwriting newer openings or administrator rule edits.
let routingHydrationSeq = 0
let routingRulesTouched = false

// modelsListCandidatesTracker rejects out-of-order model candidate responses.
const modelsListCandidatesTracker = createModelsListCandidatesTracker()

// resolveRuleKey and getRuleSearchKey assign stable search keys to routing rule objects.
const resolveRuleKey = createStableObjectKeyResolver<GroupModelRoutingRule>('group-rule')
const getRuleSearchKey = (rule: GroupModelRoutingRule) => `${props.mode}-${resolveRuleKey(rule)}`

// Keyed search maps hold query text, suggestions, and dropdown visibility per rule.
const providerSearchKeyword = ref<Record<string, string>>({})
const providerSearchResults = ref<Record<string, GroupRoutingProvider[]>>({})
const showProviderDropdown = ref<Record<string, boolean>>({})

// clearProviderSearchStateByKey removes search state for a single deleted rule.
const clearProviderSearchStateByKey = (key: string) => {
  delete providerSearchKeyword.value[key]
  delete providerSearchResults.value[key]
  delete showProviderDropdown.value[key]
}

// clearAllProviderSearchState resets all rule search maps when closing or switching drafts.
const clearAllProviderSearchState = () => {
  providerSearchKeyword.value = {}
  providerSearchResults.value = {}
  showProviderDropdown.value = {}
}

// providerSearchRunner debounces per-rule provider queries and cancels superseded requests.
const providerSearchRunner = useKeyedDebouncedSearch<GroupRoutingProvider[]>({
  delay: SEARCH_DEBOUNCE_MS,
  search: async (keyword, { signal }) => {
    const res = await adminAPI.providers.list(1, 20, { search: keyword }, { signal })
    return res.items.map((provider) => ({ id: provider.id, name: provider.name }))
  },
  onSuccess: (key, result) => {
    providerSearchResults.value[key] = result
  },
  onError: (key) => {
    // Clear stale suggestions when a keyed lookup fails.
    providerSearchResults.value[key] = []
  },
})

// groupSelectOptions computes copy-source and fallback options, excluding the
// edited group from self-referential choices in edit mode.
const groupSelectOptions = computed(() =>
  buildGroupFormGroupOptions(
    props.allGroups,
    t,
    props.mode === 'edit' ? (props.group?.id ?? null) : null,
  ),
)

// buildAvailabilityProbeModelOptions returns deduplicated model choices after the empty option.
function buildAvailabilityProbeModelOptions(models: string[]) {
  const seen = new Set<string>()
  const options = [{ value: '', label: t('admin.groups.availabilityProbe.selectModel') }]
  for (const raw of models) {
    const model = raw.trim()
    if (!model || seen.has(model)) continue
    seen.add(model)
    options.push({ value: model, label: model })
  }
  return options
}

const availabilityProbeModelOptions = computed(() =>
  buildAvailabilityProbeModelOptions(getAvailabilityProbeCandidateModels(modelsListState)),
)

watch(availabilityProbeModelOptions, (options) => {
  if (!modelsListState.enabled && modelsListState.items.length === 0) {
    return
  }
  if (
    form.availability_probe_model_id &&
    !options.some((option) => option.value === form.availability_probe_model_id)
  ) {
    form.availability_probe_model_id = ''
  }
})

watch(protocolCatalog, (catalog) => {
  if (catalog && protocolDefaultsPending.value && props.mode === 'create') {
    initializeGroupProtocolDefaults(form, catalog)
    protocolDefaultsPending.value = false
  }
})

// resetModelsListState restores modelsListState in place from optional saved configuration.
function resetModelsListState(config?: Parameters<typeof createInitialModelsListState>[0]) {
  const fresh = createInitialModelsListState(config)
  modelsListState.enabled = fresh.enabled
  modelsListState.savedModels = fresh.savedModels
  modelsListState.candidateModels = fresh.candidateModels
  modelsListState.items = fresh.items
}

// loadModelsListCandidates fetches candidate models for the current mode and group ID,
// discarding responses superseded by a newer dialog session.
async function loadModelsListCandidates(mode: 'create' | 'edit', groupID: number) {
  const request = { mode, groupID }
  const requestID = modelsListCandidatesTracker.next(request)
  modelsListLoading.value = true
  try {
    const models = await adminAPI.groups.getModelsListCandidates(groupID)
    // Candidate requests can finish out of order; ignore superseded responses.
    if (!modelsListCandidatesTracker.isCurrent(requestID, request)) return
    setModelsListCandidates(modelsListState, models)
  } catch (error) {
    if (!modelsListCandidatesTracker.isCurrent(requestID, request)) return
    console.error('Error loading group models list candidates:', error)
  } finally {
    if (modelsListCandidatesTracker.isCurrent(requestID, request)) {
      modelsListLoading.value = false
    }
  }
}

// resetEditorDraft cancels pending searches, invalidates background hydrations,
// and restores clean creation defaults on the shared draft.
function resetEditorDraft() {
  routingHydrationSeq += 1
  routingRulesTouched = false
  modelRoutingRules.value.forEach((rule) => {
    providerSearchRunner.clearKey(getRuleSearchKey(rule))
  })
  providerSearchRunner.clearAll()
  clearAllProviderSearchState()
  protocolDefaultsPending.value = !resetGroupFormDraft(form, protocolCatalog.value)
  settingsRef.value?.resetValidation()
  resetModelsListState()
  modelRoutingRules.value = []
  closeAdvancedSchedulerOverrides()
}

// startEditSession hydrates the target group synchronously with placeholder
// routing rules, then resolves provider display names concurrently while
// preserving responsiveness and cancellation safety.
function startEditSession(targetGroup: AdminGroup) {
  const currentSeq = ++routingHydrationSeq
  routingRulesTouched = false
  providerSearchRunner.clearAll()
  clearAllProviderSearchState()
  protocolDefaultsPending.value = false
  hydrateGroupFormDraft(form, targetGroup)
  settingsRef.value?.resetValidation()
  resetModelsListState(targetGroup.models_list_config)
  modelRoutingRules.value = buildPlaceholderRoutingRules(targetGroup.model_routing)
  void loadModelsListCandidates('edit', targetGroup.id)
  void hydrateRoutingRulesConcurrently(
    targetGroup.model_routing,
    (id) => adminAPI.providers.getById(id),
    () =>
      props.show &&
      props.mode === 'edit' &&
      props.group?.id === targetGroup.id &&
      routingHydrationSeq === currentSeq &&
      !routingRulesTouched,
  ).then((resolved) => {
    if (resolved) {
      modelRoutingRules.value = resolved
    }
  })
}

watch(
  [() => props.show, () => props.mode, () => props.group],
  ([show, mode, targetGroup], [wasShow, prevMode, prevGroup]) => {
    if (!show) {
      if (wasShow) {
        resetEditorDraft()
      }
      return
    }
    if (mode === 'edit') {
      if (targetGroup && (!wasShow || prevMode !== 'edit' || prevGroup !== targetGroup)) {
        startEditSession(targetGroup)
      }
      return
    }
    if (!wasShow || prevMode !== 'create') {
      resetEditorDraft()
      void loadModelsListCandidates('create', 0)
    }
  },
  { immediate: true },
)

// openAdvancedSchedulerOverrides copies current scheduler overrides into the nested modal draft.
const openAdvancedSchedulerOverrides = () => {
  advancedSchedulerOverridesDraft.value = cloneAdvancedSchedulerOverrides(
    form.advanced_scheduler_overrides,
  )
  showAdvancedSchedulerOverridesModal.value = true
}

// closeAdvancedSchedulerOverrides hides the nested scheduler parameter dialog.
const closeAdvancedSchedulerOverrides = () => {
  showAdvancedSchedulerOverridesModal.value = false
}

// saveAdvancedSchedulerOverrides commits nested scheduler overrides back into the form draft.
const saveAdvancedSchedulerOverrides = (value: GroupAdvancedSchedulerOverrides) => {
  form.advanced_scheduler_overrides = cloneAdvancedSchedulerOverrides(value)
  closeAdvancedSchedulerOverrides()
}

// updateProviderSearch records a rule's search keyword and triggers its debounced lookup.
const updateProviderSearch = (rule: GroupModelRoutingRule, keyword: string) => {
  const key = getRuleSearchKey(rule)
  providerSearchKeyword.value[key] = keyword
  providerSearchRunner.trigger(key, keyword)
}

// onProviderSearchFocus opens a rule's dropdown and triggers an initial lookup when empty.
const onProviderSearchFocus = (rule: GroupModelRoutingRule) => {
  const key = getRuleSearchKey(rule)
  showProviderDropdown.value[key] = true
  if (!providerSearchResults.value[key]?.length) {
    providerSearchRunner.trigger(key, providerSearchKeyword.value[key] || '')
  }
}

// selectProvider adds a unique provider to a routing rule and closes its dropdown.
const selectProvider = (rule: GroupModelRoutingRule, provider: GroupRoutingProvider) => {
  if (!rule) return
  routingRulesTouched = true
  if (!rule.providers.some((existing) => existing.id === provider.id)) {
    rule.providers.push(provider)
  }
  const key = getRuleSearchKey(rule)
  providerSearchKeyword.value[key] = ''
  showProviderDropdown.value[key] = false
}

// removeSelectedProvider removes a provider chip from the specified routing rule.
const removeSelectedProvider = (rule: GroupModelRoutingRule, providerId: number) => {
  if (!rule) return
  routingRulesTouched = true
  rule.providers = rule.providers.filter((provider) => provider.id !== providerId)
}

// setRulePattern updates a routing rule's model pattern and marks rules edited.
const setRulePattern = (rule: GroupModelRoutingRule, value: string) => {
  routingRulesTouched = true
  rule.pattern = value
}

// addRoutingRule appends an empty model routing rule to the active draft.
const addRoutingRule = () => {
  routingRulesTouched = true
  modelRoutingRules.value.push({ pattern: '', providers: [] })
}

// removeRoutingRule cancels pending searches for a rule before removing it from the draft.
const removeRoutingRule = (rule: GroupModelRoutingRule) => {
  const index = modelRoutingRules.value.indexOf(rule)
  if (index === -1) return
  routingRulesTouched = true
  const key = getRuleSearchKey(rule)
  // Invalidate pending search work before clearing state so late responses cannot recreate entries.
  providerSearchRunner.clearKey(key)
  clearProviderSearchStateByKey(key)
  modelRoutingRules.value.splice(index, 1)
}

// setModelSelection updates selected visibility for a single custom model-list item.
function setModelSelection(id: string, value: boolean) {
  const item = modelsListState.items.find((entry) => entry.id === id)
  if (item) item.selected = value
}

// handleClose requests parent dismissal; the show watcher resets draft state once closed.
const handleClose = () => {
  emit('close')
}

// validateForm runs child tab validation, name presence checks, and probe validation,
// revealing the first invalid control across hidden tabs.
async function validateForm(): Promise<boolean> {
  if (settingsRef.value && !(await settingsRef.value.validate())) return false
  if (!form.name.trim()) {
    appStore.showError(t('admin.groups.nameRequired'))
    await settingsRef.value?.revealField('[data-group-field="name"]')
    return false
  }
  try {
    buildAvailabilityProbeConfig(form, protocolCatalog.value, t)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error))
    await settingsRef.value?.revealField(
      !isAvailabilityProbeProtocolAvailable(form, protocolCatalog.value)
        ? '[data-group-field="probe-protocol"]'
        : form.availability_probe_model_id.trim()
          ? '[data-group-field="probe-prompt"]'
          : '[data-group-field="probe-model"]',
    )
    return false
  }
  return true
}

// handleSubmit validates the active group draft and submits either create or update.
const handleSubmit = async () => {
  if (props.mode === 'edit' && !props.group) return
  if (!protocolCatalog.value || submitting.value || !(await validateForm())) return
  if (submitting.value || !props.show) return
  if (props.mode === 'edit' && !props.group) return

  submitting.value = true
  try {
    if (props.mode === 'create') {
      const requestData = buildCreateGroupRequest(
        form,
        modelRoutingRules.value,
        modelsListState,
        protocolCatalog.value,
        t,
      )
      await adminAPI.groups.create(requestData)
      appStore.showSuccess(t('admin.groups.groupCreated'))
      resetEditorDraft()
      emit('close')
      emit('saved', 'create')
      if (onboardingStore.isCurrentStep('[data-tour="group-form-submit"]')) {
        onboardingStore.nextStep(500)
      }
      return
    }

    const payload = buildUpdateGroupRequest(
      form,
      modelRoutingRules.value,
      modelsListState,
      protocolCatalog.value,
      t,
    )
    await adminAPI.groups.update(props.group!.id, payload)
    appStore.showSuccess(t('admin.groups.groupUpdated'))
    resetEditorDraft()
    emit('close')
    emit('saved', 'edit')
  } catch (error: unknown) {
    const fallbackKey =
      props.mode === 'create' ? 'admin.groups.failedToCreate' : 'admin.groups.failedToUpdate'
    appStore.showError(extractApiErrorMessage(error, t(fallbackKey)))
    console.error(`Error ${props.mode === 'create' ? 'creating' : 'updating'} group:`, error)
  } finally {
    submitting.value = false
  }
}

// handleClickOutside closes open provider search dropdowns when clicking outside search inputs.
const handleClickOutside = (event: MouseEvent) => {
  if (!props.show) return
  const target = event.target as HTMLElement | null
  if (!target?.closest('.provider-search-container')) {
    Object.keys(showProviderDropdown.value).forEach((key) => {
      showProviderDropdown.value[key] = false
    })
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  providerSearchRunner.clearAll()
  clearAllProviderSearchState()
})
</script>
