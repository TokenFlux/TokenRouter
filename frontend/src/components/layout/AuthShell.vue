<template>
  <div class="ba-theme-shell relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <!-- 认证路由共享背景，页面切换时保持动画实例。 -->
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>
    <AuthBackground />

    <div class="relative z-10 w-full max-w-md">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import AuthBackground from '@/components/auth/AuthBackground.vue'
</script>
