<script setup lang="ts">
defineProps<{
  title: string
  description?: string
  eyebrow?: string
}>()
</script>

<template>
  <header class="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
    <div class="min-w-0">
      <p v-if="eyebrow" class="mb-1 text-[11px] font-semibold uppercase tracking-widest text-indigo-600 dark:text-indigo-400">
        {{ eyebrow }}
      </p>
      <h1 class="text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">{{ title }}</h1>
      <p v-if="description" class="mt-1 max-w-2xl text-sm leading-relaxed text-zinc-500 dark:text-zinc-400">
        {{ description }}
      </p>
    </div>
    <div v-if="$slots.actions" class="flex shrink-0 flex-wrap items-center gap-2">
      <slot name="actions" />
    </div>
  </header>
</template>
