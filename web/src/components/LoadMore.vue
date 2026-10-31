<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'

withDefaults(defineProps<{ loading: boolean; label?: string }>(), { label: 'Load more' })
defineEmits<{ load: [] }>()
</script>

<template>
  <div class="flex justify-center border-t border-zinc-200 p-4 dark:border-zinc-800">
    <button class="btn btn-secondary" :disabled="loading" @click="$emit('load')">
      <RefreshCw v-if="loading" class="h-4 w-4 animate-spin" />
      {{ loading ? 'Loading…' : label }}
    </button>
  </div>
</template>
