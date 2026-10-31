<script setup lang="ts">
import LoadingSpinner from './LoadingSpinner.vue'

withDefaults(
  defineProps<{
    pending: boolean
    error?: string
    empty?: boolean
    label?: string
    pendingClass?: string
    errorClass?: string
  }>(),
  {
    error: '',
    empty: false,
    label: 'Loading',
    pendingClass: 'flex items-center justify-center py-20',
    errorClass: 'px-4 py-10 text-center text-sm text-red-600 dark:text-red-400',
  },
)
</script>

<template>
  <div v-if="pending" :class="pendingClass">
    <LoadingSpinner :label="label" />
  </div>
  <div v-else-if="error" :class="errorClass">{{ error }}</div>
  <slot v-else-if="!empty" />
  <slot v-else name="empty" />
</template>
