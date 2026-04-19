<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-medium text-gray-900 dark:text-white">
        {{ t('profile.editProfile') }}
      </h2>
    </div>
    <div class="px-6 py-6">
      <form @submit.prevent="handleUpdateProfile" class="space-y-4">
        <div>
          <label for="email" class="input-label">
            {{ t('auth.emailLabel') }}
          </label>
          <input
            id="email"
            v-model="email"
            type="email"
            class="input"
            :placeholder="t('auth.emailPlaceholder')"
          />
        </div>

        <div>
          <label for="username" class="input-label">
            {{ t('profile.username') }}
          </label>
          <input
            id="username"
            v-model="username"
            type="text"
            class="input"
            :placeholder="t('profile.enterUsername')"
          />
        </div>

        <div class="flex justify-end pt-4">
          <button type="submit" :disabled="loading" class="btn btn-primary">
            {{ loading ? t('profile.updating') : t('profile.updateProfile') }}
          </button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { userAPI } from '@/api'

const props = defineProps<{
  initialEmail: string
  initialUsername: string
}>()

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const email = ref(props.initialEmail)
const username = ref(props.initialUsername)
const loading = ref(false)

watch(() => props.initialEmail, (val) => {
  email.value = val
})

watch(() => props.initialUsername, (val) => {
  username.value = val
})

const handleUpdateProfile = async () => {
  const trimmedEmail = email.value.trim()
  if (!trimmedEmail) {
    appStore.showError(t('auth.emailRequired'))
    return
  }

  const trimmedUsername = username.value.trim()
  if (!trimmedUsername) {
    appStore.showError(t('profile.usernameRequired'))
    return
  }

  loading.value = true
  try {
    const updatedUser = await userAPI.updateProfile({
      email: trimmedEmail,
      username: trimmedUsername
    })
    authStore.user = updatedUser
    appStore.showSuccess(t('profile.updateSuccess'))
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('profile.updateFailed'))
  } finally {
    loading.value = false
  }
}
</script>
