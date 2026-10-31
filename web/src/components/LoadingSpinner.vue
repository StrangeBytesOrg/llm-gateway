<script setup lang="ts">
import { Loader2 } from '@lucide/vue'

withDefaults(defineProps<{ label?: string }>(), { label: 'Loading' })
</script>

<template>
  <span class="inline-flex items-center gap-2 text-sm text-zinc-500 dark:text-zinc-400">
    <Loader2 class="h-4 w-4 animate-spin" />
    {{ label }}
  </span>
</template>
