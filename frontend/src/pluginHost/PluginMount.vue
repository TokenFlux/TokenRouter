<script setup lang="ts">
import { provide, type Component } from 'vue'
import type { PluginPage, PluginUI } from '@/api/plugins.types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { PLUGIN_HOST_KEY } from './context'
import { usePluginHost } from './hostApi'

// PluginMount.vue binds one accepted page component to its scoped host capabilities.
// The owning page keys this mount by account, release, and page identity; replacing
// it disposes pending confirmations before another component receives its own host
// context. Native confirmation rendering remains outside the plugin's content root.

// Props selects one immutable mount identity; the parent replaces this component
// when an installation, audience, account, or registered page changes.
const props = defineProps<{
  plugin: PluginUI // Accepted installation frontend description.
  page: PluginPage // Selected authorized page declaration.
  component: Component // Registered page component implementation.
}>()
const { host, confirmation, settleConfirmation } = usePluginHost(props.plugin, props.page)
provide(PLUGIN_HOST_KEY, host)
</script>

<template>
  <div class="tr-plugin-root" :data-tr-plugin="plugin.id">
    <component :is="component" />
  </div>
  <ConfirmDialog
    :show="confirmation !== null"
    :title="confirmation?.title || ''"
    :message="confirmation?.message || ''"
    :danger="confirmation?.danger"
    @confirm="settleConfirmation(true)"
    @cancel="settleConfirmation(false)"
  />
</template>
