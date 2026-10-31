<script setup lang="ts">
import type { Component } from 'vue'

withDefaults(
  defineProps<{
    label: string
    value: string | number
    hint?: string
    icon?: Component
    tone?: 'default' | 'indigo' | 'amber' | 'red' | 'emerald'
  }>(),
  { tone: 'default' },
)

const tones = {
  default: 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300',
  indigo: 'bg-indigo-50 text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-400',
  amber: 'bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-400',
  red: 'bg-red-50 text-red-600 dark:bg-red-500/10 dark:text-red-400',
  emerald: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-400',
}
</script>

<template>
  <div class="card p-4 sm:p-5">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <p class="text-[11px] font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">{{ label }}</p>
        <p class="mt-2 text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">{{ value }}</p>
      </div>
      <div v-if="icon" class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg" :class="tones[tone]">
        <component :is="icon" class="h-4 w-4" />
      </div>
    </div>
    <p v-if="hint" class="mt-2 text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">{{ hint }}</p>
  </div>
</template>
