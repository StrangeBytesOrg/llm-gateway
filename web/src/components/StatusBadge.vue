<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()

const tone = computed(() => {
  const status = props.status.toLowerCase()
  if (['active', 'enabled', 'settled', 'completed', 'ready', 'ok'].includes(status)) {
    return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-500/10 dark:text-emerald-300'
  }
  if (['pending', 'admitted', 'streaming', 'interrupted', 'expired'].includes(status)) {
    return 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/60 dark:bg-amber-500/10 dark:text-amber-300'
  }
  if (['revoked', 'disabled', 'unresolved', 'failed', 'error', 'blocked'].includes(status)) {
    return 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300'
  }
  return 'border-zinc-200 bg-zinc-50 text-zinc-600 dark:border-zinc-700 dark:bg-zinc-800 dark:text-zinc-300'
})

const dot = computed(() => {
  const status = props.status.toLowerCase()
  if (['active', 'enabled', 'settled', 'completed', 'ready', 'ok'].includes(status)) return 'bg-emerald-500'
  if (['pending', 'admitted', 'streaming', 'interrupted', 'expired'].includes(status)) return 'bg-amber-500'
  if (['revoked', 'disabled', 'unresolved', 'failed', 'error', 'blocked'].includes(status)) return 'bg-red-500'
  return 'bg-zinc-400'
})
</script>

<template>
  <span
    class="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium capitalize"
    :class="tone"
  >
    <span class="h-1.5 w-1.5 rounded-full" :class="dot" />
    {{ status.replaceAll('_', ' ') }}
  </span>
</template>
