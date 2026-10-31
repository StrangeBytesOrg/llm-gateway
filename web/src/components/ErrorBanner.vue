<script setup lang="ts">
defineProps<{ message?: string }>()
</script>

<template>
  <div
    v-if="message"
    class="mb-4 rounded-lg border border-red-200 bg-red-50 px-3.5 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300"
  >
    {{ message }}
  </div>
</template>
