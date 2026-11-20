<template>
  <SettingsSection
    data-provider-field="model-mapping"
    :title="t('admin.providers.modelRestriction')"
    :hint="hint ?? t('admin.providers.modelRestrictionCombinedHint')"
  >
    <SettingsSegmented
      v-model="mode"
      block
      :ariaLabel="t('admin.providers.modelRestriction')"
      :options="modeOptions"
    />

    <div v-if="mode === 'whitelist'" v-content-reveal class="space-y-2">
      <ModelWhitelistSelector
        :model-value="allowedModels"
        :platform="platform"
        :platforms="platforms"
        :provider-id="providerId"
        :models="models"
        :known-route-keys="knownRouteKeys"
        :sync-credentials="syncCredentials"
        @update:model-value="allowedModels = $event"
        @synced="emit('synced', $event)"
      />
      <p class="input-hint">
        {{ t('admin.providers.selectedModels', { count: allowedModels.length }) }}
        <span v-if="allowedModels.length === 0">{{ t('admin.providers.supportsAllModels') }}</span>
      </p>
    </div>

    <ProviderModelMappingEditor
      v-else
      v-model="mappings"
      v-model:strip-effort="stripEffort"
      support-effort
      :source-placeholder="sourcePlaceholder"
      :target-placeholder="targetPlaceholder"
      :wildcard-validation="wildcardValidation"
      @add="emit('add')"
      @remove="emit('remove')"
    />
  </SettingsSection>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import { vContentReveal } from '@/directives/contentReveal'
import type { ModelMappingRow } from '@/utils/modelMappingRules'
import type { SyncUpstreamModelsResult, SyncUpstreamPreviewParams } from '@/api/admin/providers'
import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'
import ProviderModelMappingEditor from '../ProviderModelMappingEditor.vue'

// ModelRestrictionFields.vue switches between model whitelist and mapping editors while
// keeping both parent-owned drafts available. Whitelist suggestions may come from
// explicit candidates or the catalog, and provider identity or preview credentials are
// forwarded to the selector for upstream synchronization, whose accepted results are
// re-emitted so the parent can keep response data beyond the model IDs.

// ModelRestrictionMode selects the whitelist editor or request-to-upstream mapping
// editor.
export type ModelRestrictionMode = 'whitelist' | 'mapping'

defineProps<{
  platform?: string // Provider platform identifier.
  platforms?: string[] // Candidate provider platform identifiers.
  // Saved provider identifier; when present, upstream synchronization uses it before any
  // preview credentials.
  providerId?: number
  // Explicit candidates override catalog lookup, including an empty array that
  // intentionally supplies no suggestions.
  models?: string[]
  knownRouteKeys?: string[] // Current upstream keys accepted by existing whitelists.
  // Unsaved credentials used for preview synchronization when a saved provider identifier
  // is unavailable.
  syncCredentials?: SyncUpstreamPreviewParams
  sourcePlaceholder?: string // Request model input placeholder.
  targetPlaceholder?: string // Upstream model input placeholder.
  wildcardValidation?: boolean // Live wildcard rule diagnostics on mapping rows.
  hint?: string // Supplemental field help text.
}>()
// mode binds the parent-owned whitelist or model-mapping editor mode to the shared
// controls through its model update event.
const mode = defineModel<ModelRestrictionMode>('mode', { required: true })
// allowedModels binds the parent-owned selected allowed model ID array to the shared
// controls through its model update event.
// An empty allowedModels array is the form unlimited-model draft; it is distinct from
// an empty models prop, which supplies zero suggestions to the selector.
const allowedModels = defineModel<string[]>('allowedModels', { required: true })
// mappings binds the parent-owned request-to-upstream model mapping rows to the shared
// controls through its model update event.
const mappings = defineModel<ModelMappingRow[]>('mappings', { required: true })
// stripEffort binds the optional wire-effort stripping switch when effort mappings match.
const stripEffort = defineModel<boolean | undefined>('stripEffort', { default: undefined })
// emit publishes typed draft changes and actions to the parent that owns this component.
const emit = defineEmits<{
  add: [] // Mapping row addition notification.
  remove: [] // Mapping row removal notification.
  synced: [result: SyncUpstreamModelsResult] // Accepted upstream synchronization response.
}>()

// t resolves interface labels from the active component locale while reactive consumers
// update their displayed text.
const { t } = useI18n()
// modeOptions supplies whitelist and mapping labels with stable test selectors for
// switching between the two retained draft editors.
const modeOptions = computed(() => [
  {
    value: 'whitelist' as const,
    label: t('admin.providers.modelWhitelist'),
    icon: 'checkCircle' as const,
    testid: 'model-restriction-mode-whitelist',
  },
  {
    value: 'mapping' as const,
    label: t('admin.providers.modelMapping'),
    icon: 'swap' as const,
    testid: 'model-restriction-mode-mapping',
  },
])
</script>
