<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'

// PurchaseSkeleton.vue reserves space for the plan catalog or checkout before
// the payment configuration arrives. The incoming destination selects the
// arrangement, and the page replaces all placeholders once it knows which plans
// and payment methods are available.

// The destination props describe the route's intent before checkout configuration is available.
defineProps<{
  plans: boolean // Plan catalog destination.
  planCheckout: boolean // Requested plan confirmation.
}>()

// The translator provides one loading status label for the current checkout destination.
const { t } = useI18n()
</script>

<template>
  <div role="status" :aria-label="t('common.loading')" aria-busy="true" data-loading-skeleton>
    <div
      v-if="plans"
      class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
      aria-hidden="true"
    >
      <article v-for="plan in 3" :key="plan" class="card p-6">
        <div class="sm:h-12">
          <Skeleton :width="144" :height="20" class="max-w-full" />
        </div>
        <Skeleton width="80%" :height="14" class="mt-1" />
        <Skeleton :width="92" :height="36" class="mt-5" />
        <div class="mt-5 grid grid-cols-2 gap-2">
          <div
            v-for="quota in 2"
            :key="quota"
            class="space-y-2 rounded-control bg-gray-50 px-3 py-2.5 dark:bg-dark-950"
          >
            <Skeleton width="62%" :height="12" />
            <Skeleton width="80%" :height="14" />
          </div>
        </div>
        <Skeleton :height="36" class="mt-5" />
      </article>
    </div>

    <div
      v-else
      class="grid gap-4 xl:grid-cols-[minmax(0,1fr)_21rem] xl:items-start"
      aria-hidden="true"
    >
      <div class="space-y-4">
        <div v-if="planCheckout" class="card space-y-4 p-4 sm:p-6">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-6">
            <div class="min-w-0 flex-1 space-y-2">
              <Skeleton :width="96" :height="12" />
              <Skeleton :width="170" :height="28" class="max-w-full" />
              <Skeleton :width="220" :height="14" class="max-w-full" />
            </div>
            <Skeleton :width="100" :height="32" />
          </div>
          <div class="flex flex-wrap gap-4 border-t border-gray-100 pt-3 dark:border-dark-700">
            <Skeleton v-for="quota in 3" :key="quota" :width="72" :height="16" />
          </div>
        </div>
        <div v-else class="card space-y-4 p-4 sm:p-6">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{
            t('payment.amountLabel')
          }}</h2>
          <div class="grid grid-cols-3 gap-2">
            <Skeleton v-for="amount in 9" :key="amount" :height="36" class="rounded-control" />
          </div>
          <div>
            <p class="mb-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">{{
              t('payment.customAmount')
            }}</p>
            <Skeleton :height="36" class="rounded-control" />
          </div>
        </div>
        <div class="card space-y-4 p-4 sm:p-6">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{
            t('payment.paymentMethod')
          }}</h2>
          <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
            <div
              v-for="method in 2"
              :key="method"
              class="flex items-center gap-3 rounded-control border border-gray-200 px-3 py-2 dark:border-dark-700"
            >
              <Skeleton :width="24" :height="24" class="shrink-0" />
              <div class="min-w-0 flex-1 space-y-1.5">
                <Skeleton :width="88" :height="14" class="max-w-full" />
                <Skeleton :width="65" :height="12" class="max-w-full" />
              </div>
            </div>
          </div>
        </div>
      </div>
      <div class="card space-y-3 p-4 sm:p-6">
        <Skeleton :width="104" :height="20" />
        <div v-for="line in 2" :key="line" class="flex justify-between gap-3">
          <Skeleton :width="88" :height="14" />
          <Skeleton :width="56" :height="14" />
        </div>
        <div
          class="flex items-end justify-between gap-3 border-t border-gray-100 pt-3 dark:border-dark-700"
        >
          <Skeleton :width="76" :height="14" />
          <Skeleton :width="96" :height="28" />
        </div>
        <Skeleton :height="36" class="rounded-control" />
      </div>
    </div>
  </div>
</template>
