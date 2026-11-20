<template>
  <BaseDialog :show="show" :title="t('admin.users.userApiKeys')" width="wide" @close="handleClose">
    <div v-if="user" class="space-y-4">
      <div class="flex items-center gap-3 rounded-surface bg-gray-50 p-4 dark:bg-dark-700">
        <UserAvatar
          :avatar-url="user.avatar_url || ''"
          :user-id="user.id"
          :alt="user.email"
          size-class="h-10 w-10"
        />
        <div>
          <p class="font-medium text-gray-900 dark:text-white">{{ user.email }}</p>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ user.username }}</p>
        </div>
      </div>
      <ContentSkeleton v-if="loading" variant="list" :rows="3" class="py-4" />
      <div v-else-if="apiKeys.length === 0" class="py-8 text-center">
        <p class="text-sm text-gray-500">{{ t('admin.users.noApiKeys') }}</p>
      </div>
      <div v-else class="max-h-96 space-y-3 overflow-y-auto" @scroll="closeGroupSelector">
        <div
          v-for="key in apiKeys"
          :key="key.id"
          class="rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="flex items-start justify-between">
            <div class="min-w-0 flex-1">
              <div class="mb-1 flex items-center gap-2">
                <span class="font-medium text-gray-900 dark:text-white">{{ key.name }}</span
                ><span
                  :class="[
                    'badge text-xs',
                    key.status === 'active' ? 'badge-success' : 'badge-danger',
                  ]"
                  >{{ key.status }}</span
                >
              </div>
              <!-- Keep twenty leading and eight trailing characters in the credential preview. -->
              <p class="truncate font-mono text-sm text-gray-500">
                {{ key.key.substring(0, 20) }}...{{ key.key.substring(key.key.length - 8) }}
              </p>
            </div>
          </div>
          <div class="mt-3 flex flex-wrap items-start gap-4 text-xs text-gray-500">
            <!-- Composite keys display each prefix-to-group mapping because their routing spans multiple groups. -->
            <div
              v-if="key.is_composite"
              data-testid="composite-group-mappings"
              class="flex w-full min-w-0 items-start gap-1 sm:w-auto sm:min-w-80 sm:flex-1"
            >
              <span class="shrink-0 py-1">{{ t('admin.users.group') }}:</span>
              <div v-if="key.composite_groups?.length" class="flex min-w-0 flex-wrap gap-1.5">
                <span
                  v-for="binding in key.composite_groups"
                  :key="`${key.id}-${binding.group_id}-${binding.prefix}`"
                  class="inline-flex max-w-full min-w-0 items-center gap-1 rounded-compact border border-gray-200 bg-gray-50 px-1.5 py-1 dark:border-dark-600 dark:bg-dark-700"
                >
                  <span
                    class="max-w-24 truncate font-mono font-semibold text-primary-700 dark:text-primary-300"
                    >{{ binding.prefix }}</span
                  >
                  <span class="text-gray-300 dark:text-dark-500">/</span>
                  <span class="max-w-36 truncate text-gray-600 dark:text-dark-300">{{
                    binding.group?.name || `#${binding.group_id}`
                  }}</span>
                </span>
              </div>
              <span v-else class="py-1 text-gray-400 italic">{{ t('admin.users.none') }}</span>
            </div>
            <div v-else class="flex items-center gap-1">
              <span>{{ t('admin.users.group') }}:</span>
              <button
                :ref="(el) => setGroupButtonRef(key.id, el)"
                data-testid="api-key-group-selector"
                @click="openGroupSelector(key)"
                class="-mx-1 -my-0.5 flex cursor-pointer items-center gap-1 rounded-control px-1 py-0.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
                :disabled="updatingKeyIds.has(key.id)"
              >
                <GroupBadge
                  v-if="key.group_id && key.group"
                  :name="key.group.name"
                  :display-brand="key.group.display_brand"
                  :rate-multiplier="key.group.rate_multiplier"
                />
                <span v-else class="text-gray-400 italic">{{ t('admin.users.none') }}</span>
                <Icon
                  name="loader"
                  size="xs"
                  :animate-on-hover="false"
                  v-if="updatingKeyIds.has(key.id)"
                  class="h-3 w-3 animate-spin text-primary-500"
                />
                <Icon
                  name="sort"
                  size="xs"
                  :animate-on-hover="false"
                  v-else
                  class="h-3 w-3 text-gray-400"
                />
              </button>
            </div>
            <div class="flex items-center gap-1">
              <span
                >{{ t('admin.users.columns.created') }}: {{ formatDateTime(key.created_at) }}</span
              >
            </div>
          </div>
        </div>
      </div>
    </div>
  </BaseDialog>

  <!-- Group Selector Dropdown -->
  <Teleport to="body">
    <MotionTransition name="dropdown-fade">
      <div
        v-if="groupSelectorKeyId !== null && dropdownPosition"
        :inert="!(groupSelectorKeyId !== null && dropdownPosition) || undefined"
        ref="dropdownRef"
        class="dropdown animate-in fade-in slide-in-from-top-2 fixed z-teleport-dropdown w-64 overflow-hidden duration-normal py-0"
        :style="{
          top: dropdownPosition.top !== undefined ? dropdownPosition.top + 'px' : undefined,
          bottom:
            dropdownPosition.bottom !== undefined ? dropdownPosition.bottom + 'px' : undefined,
          left: dropdownPosition.left + 'px',
        }"
      >
        <div class="max-h-64 overflow-y-auto p-1.5">
          <!-- Unbind option -->
          <button
            @click="changeGroup(selectedKeyForGroup!, null)"
            :class="[
              'flex w-full items-center rounded-control px-3 py-2 text-sm transition-colors',
              !selectedKeyForGroup?.group_id
                ? 'bg-primary-50 dark:bg-primary-500/8 dark:text-primary-500'
                : 'hover:bg-gray-100 dark:hover:bg-dark-700',
            ]"
          >
            <span class="text-gray-500 italic">{{ t('admin.users.none') }}</span>
            <Icon
              name="check"
              size="sm"
              :animate-on-hover="false"
              v-if="!selectedKeyForGroup?.group_id"
              class="ml-auto h-4 w-4 shrink-0 text-primary-600 dark:text-primary-400"
            />
          </button>
          <!-- Group options -->
          <button
            v-for="group in allGroups"
            :key="group.id"
            @click="changeGroup(selectedKeyForGroup!, group.id)"
            :class="[
              'flex w-full items-center justify-between rounded-control px-3 py-2 text-sm transition-colors',
              selectedKeyForGroup?.group_id === group.id
                ? 'bg-primary-50 dark:bg-primary-500/8 dark:text-primary-500'
                : 'hover:bg-gray-100 dark:hover:bg-dark-700',
            ]"
          >
            <GroupOptionItem
              :name="group.name"
              :display-brand="group.display_brand"
              :rate-multiplier="group.rate_multiplier"
              :description="group.description"
              :selected="selectedKeyForGroup?.group_id === group.id"
            />
          </button>
        </div>
      </div>
    </MotionTransition>
  </Teleport>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import Icon from '@/components/icons/Icon.vue'
import { ref, computed, watch, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import type { AdminUser } from '@/api/admin/users.types'
import type { AdminGroup } from '@/api/groups.types'
import type { ApiKey } from '@/api/keys.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'

// UserApiKeysModal.vue lists a selected user's API keys and lets administrators change
// a regular key's group. Composite keys display prefix mappings and skip the ordinary
// group selector. Trigger elements position a teleported menu, and document listeners
// close it on Escape or an outside click.

// props and emit connect the selected user and modal visibility to the parent's close
// handling for this API key list.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  // user supplies the selected account; null leaves identity-dependent operations waiting
  // until the parent chooses an account.
  user: AdminUser | null
}>()
const emit = defineEmits(['close'])
// The translation and notification services provide key labels and report whether
// changing a group also granted the user access.
const { t } = useI18n()
const appStore = useAppStore()

// apiKeys and allGroups hold separately loaded key rows and group choices used by the
// selected user's regular-key group selector.
const apiKeys = ref<ApiKey[]>([])
const allGroups = ref<AdminGroup[]>([])
// loading drives key-list feedback, while updatingKeyIds tracks individual group
// updates so unrelated keys can retain their own pending states.
const loading = ref(false)
const updatingKeyIds = ref(new Set<number>())
// groupSelectorKeyId and dropdownPosition identify the active regular-key menu; closing
// clears both selection and viewport coordinates before another menu can open.
const groupSelectorKeyId = ref<number | null>(null)
const dropdownPosition = ref<{
  top?: number // Viewport top offset in pixels.
  bottom?: number // Viewport bottom offset in pixels.
  left: number // Viewport left offset in pixels.
} | null>(null)
// dropdownRef and groupButtonRefs identify the panel and its trigger elements, allowing
// outside-click handling to distinguish menu interactions from unrelated page clicks.
const dropdownRef = ref<HTMLElement | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())

// selectedKeyForGroup resolves the open menu's key against current rows, producing null
// when the selection is cleared or absent from the list.
const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

// setGroupButtonRef stores mounted HTMLElement triggers by key ID and deletes other
// refs. Vue calls it as rows mount or unmount, so menu positioning uses currently
// available elements.
const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

// Opening starts independent key and catalog loads; hiding clears the floating
// selection so a menu does not remain detached from its modal.
watch(
  () => props.show,
  (v) => {
    if (v && props.user) {
      load()
      loadGroups()
    } else {
      closeGroupSelector()
    }
  },
)

// load fetches keys for the selected user and clears cached trigger elements first.
// Missing response items become an empty list; errors are logged, and loading feedback
// ends after the request settles.
const load = async () => {
  if (!props.user) return
  loading.value = true
  // Rows are about to be replaced by the key response, so discard cached trigger
  // elements and let mounted refs register the current buttons.
  groupButtonRefs.value.clear()
  try {
    const res = await adminAPI.users.getUserApiKeys(props.user.id)
    apiKeys.value = res.items || []
  } catch (error) {
    // Keep the last key rows when loading fails and record the error; the finally block
    // still ends the modal's loading feedback.
    console.error('Failed to load API keys:', error)
  } finally {
    loading.value = false
  }
}

// loadGroups fetches the group catalog used by regular-key menus. Failed requests are
// logged and retain the previous choices, allowing the key-list request to complete
// independently of the catalog.
const loadGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    allGroups.value = groups
  } catch (error) {
    // Catalog loading is independent of the key list, so failure retains available
    // choices and is logged without discarding displayed keys.
    console.error('Failed to load groups:', error)
  }
}

// openGroupSelector toggles the menu for a regular API key. Composite keys return
// immediately because their groups are controlled by prefix mappings; mounted triggers
// provide viewport coordinates for the teleported panel.
const openGroupSelector = (key: ApiKey) => {
  // Composite groups are maintained through prefix mappings, so opening this
  // single-group selector would expose an incompatible mutation path for that key.
  if (key.is_composite) return
  if (groupSelectorKeyId.value === key.id) {
    closeGroupSelector()
  } else {
    const buttonEl = groupButtonRefs.value.get(key.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()

      // The comfortable panel height includes the list's 256 pixels plus sixteen pixels
      // of padding, allowing the placement helper to flip when needed.
      const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, {
        align: 'left', // Panel horizontal alignment.
        maxWidth: 256, // Panel width in pixels.
        viewportPadding: 8, // Viewport clearance in pixels.
        gap: 4, // Trigger spacing in pixels.
        maxHeightRatio: 1, // Allowed viewport-height fraction.
        minComfortableHeight: 256 + 16, // Preferred height in pixels.
      })
      dropdownPosition.value = {
        top: position.top ?? undefined, // Viewport top offset in pixels.
        bottom: position.bottom ?? undefined, // Viewport bottom offset in pixels.
        left: position.left, // Viewport left offset in pixels.
      }
    }
    groupSelectorKeyId.value = key.id
  }
}

// closeGroupSelector clears the selected key and its floating menu position.
const closeGroupSelector = () => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
}

// changeGroup closes the selector and submits a changed regular-key group assignment.
// Equivalent assignments are skipped; accepted responses replace the matching key row
// and report any automatically granted group access, then clear that key's pending
// marker.
const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  closeGroupSelector()
  if (key.group_id === newGroupId || (!key.group_id && newGroupId === null)) return

  updatingKeyIds.value.add(key.id)
  try {
    // Use the server's returned key after persistence, since changing the group can
    // also grant user access that must be reflected in feedback.
    const result = await adminAPI.apiKeys.updateApiKeyGroup(key.id, newGroupId)

    const idx = apiKeys.value.findIndex((k) => k.id === key.id)
    if (idx !== -1) {
      apiKeys.value[idx] = result.api_key
    }
    if (result.auto_granted_group_access && result.granted_group_name) {
      appStore.showSuccess(
        t('admin.users.groupChangedWithGrant', {
          group: result.granted_group_name, // Displayed group name.
        }),
      )
    } else {
      appStore.showSuccess(t('admin.users.groupChangedSuccess'))
    }
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.users.groupChangeFailed'))
  } finally {
    updatingKeyIds.value.delete(key.id)
  }
}

// handleKeyDown closes an open group selector on Escape and stops propagation. The
// document listener runs in capture mode so the selector handles Escape before the
// surrounding modal can receive it.
const handleKeyDown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && groupSelectorKeyId.value !== null) {
    event.stopPropagation()
    closeGroupSelector()
  }
}

// handleClickOutside closes the selector when a click misses the panel and every group
// trigger. Trigger clicks remain available to the toggle handler, which decides whether
// to open or close a menu.
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (dropdownRef.value && !dropdownRef.value.contains(target)) {
    for (const el of groupButtonRefs.value.values()) {
      if (el.contains(target)) return
    }
    closeGroupSelector()
  }
}

// handleClose clears the group selector before requesting that the parent close the
// modal. Clearing the floating selection first prevents its viewport coordinates from
// remaining attached to a hidden modal.
const handleClose = () => {
  closeGroupSelector()
  emit('close')
}

// Install document listeners after mounting so outside clicks and capture-phase Escape
// can dismiss the teleported group selector while the modal exists.
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleKeyDown, true)
})

// Remove both document listeners with their original capture settings so a destroyed
// modal cannot react to later clicks or key presses.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleKeyDown, true)
})
</script>
