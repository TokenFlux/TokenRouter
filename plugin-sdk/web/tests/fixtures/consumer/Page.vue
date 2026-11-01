<script setup lang="ts">
import { ref } from 'vue'
import { TrButton, TrCard, TrInput, useHost, useMessages } from '@tokenflux/tokenrouter-plugin-sdk'

// Page.vue checks the SDK's generated types from a compiled Vue consumer.
// The button exercises generic host requests and component event props, while
// text binding and translations cover declarations that plain TypeScript
// entrypoints cannot validate inside Vue templates.

// These values belong only to the build fixture; no host is contacted while
// typechecking or bundling because the component setup is never mounted.
const host = useHost()
const text = ref('')
const t = useMessages({ en: { send: 'Send' } })

/** send exercises the generic request result and the public host toast contract.
 * It runs only if a test explicitly mounts this fixture. The build smoke test
 * compiles the callback without performing a request or mutating an account.
 */
async function send(): Promise<void> {
  const reply = await host.request<{ message: string }>('POST', '/echo', {
    body: { message: text.value },
  })
  host.toast.info(reply.message)
}
</script>

<template>
  <TrCard class="consumer">
    <TrInput v-model="text" />
    <TrButton variant="primary" @click="send">{{ t('send') }}</TrButton>
  </TrCard>
</template>

<style>
.consumer {
  color: var(--tr-color-text);
}
</style>
