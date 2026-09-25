<template>
  <div ref="dropdownRef" class="relative" data-tour="keys-scope-switch">
    <button
      type="button"
      class="btn btn-secondary h-9 w-9 p-0 md:w-auto md:min-h-0 md:px-3 md:py-1.5"
      :title="t('team.scopeSwitch')"
      :aria-expanded="open"
      aria-haspopup="menu"
      data-test="scope-dropdown-trigger"
      @click="open = !open"
    >
      <Icon :name="scope === 'team' ? 'users' : 'user'" size="md" class="md:mr-1.5" />
      <span class="hidden md:inline">{{ currentLabel }}</span>
      <Icon name="chevronDown" size="xs" class="ml-1 hidden md:inline" />
    </button>

    <div
      v-if="open"
      class="absolute right-0 top-full z-50 mt-2 w-48 rounded-control border border-gray-200 bg-white p-2 shadow-xl dark:border-gray-700 dark:bg-gray-800"
      role="menu"
      data-test="scope-dropdown-menu"
    >
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        role="menuitemradio"
        :aria-checked="scope === option.value"
        class="flex w-full items-center gap-2 rounded-control px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-700"
        :data-test="`scope-option-${option.value}`"
        @click="setScope(option.value)"
      >
        <Icon :name="option.icon" size="sm" class="text-gray-400 dark:text-gray-500" />
        <span class="flex-1">{{ option.label }}</span>
        <Icon
          v-if="scope === option.value"
          name="check"
          size="sm"
          class="text-primary-500"
          :stroke-width="2"
        />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'

export type DataScope = 'personal' | 'team'

const props = defineProps<{ modelValue: DataScope }>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: DataScope): void
  (event: 'change', value: DataScope): void
}>()

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const open = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const scope = computed(() => props.modelValue)
const options = computed(() => [
  { value: 'personal' as const, label: t('team.personalKeys'), icon: 'user' as const },
  { value: 'team' as const, label: t('team.teamKeys'), icon: 'users' as const },
])
const currentLabel = computed(() => options.value.find((option) => option.value === scope.value)?.label || '')

// 作用域写入 URL，刷新页面后仍能恢复当前密钥列表。
const setScope = async (value: DataScope) => {
  open.value = false
  if (value === scope.value) return
  emit('update:modelValue', value)
  await router.replace({ query: { ...route.query, scope: value } })
  emit('change', value)
}

const closeOnOutsideClick = (event: MouseEvent) => {
  if (!dropdownRef.value?.contains(event.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('click', closeOnOutsideClick, true))
onUnmounted(() => document.removeEventListener('click', closeOnOutsideClick, true))
</script>
