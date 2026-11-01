<script setup lang="ts">
import { TrAmount } from '@tokenflux/tokenrouter-plugin-sdk'

// LazyPage.vue contributes a second SFC through a dynamic source import.
// The build test requires the preset to inline this component into the same
// IIFE while keeping the SDK component supplied by the host global.
</script>

<template>
  <TrAmount value="0.00" />
</template>
