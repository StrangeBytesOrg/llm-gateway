<script setup lang="ts">
import { ref } from 'vue'
import { Check, Copy } from '@lucide/vue'
import { toast } from '../lib/toast'

const props = withDefaults(defineProps<{ value: string; label?: string; compact?: boolean }>(), { compact: true })
const copied = ref(false)
let timer: number | undefined

async function copy(): Promise<void> {
  try {
    await navigator.clipboard.writeText(props.value)
    copied.value = true
    window.clearTimeout(timer)
    timer = window.setTimeout(() => (copied.value = false), 1600)
  } catch {
    toast('Could not copy', 'Clipboard access was blocked by the browser.', 'error')
  }
}
</script>

<template>
  <button
    class="inline-flex items-center gap-1.5 rounded-md text-zinc-400 transition hover:text-zinc-700 dark:hover:text-zinc-200"
    :class="compact ? 'p-1' : 'px-2 py-1 text-xs'"
    :title="label || 'Copy to clipboard'"
    :aria-label="label || 'Copy to clipboard'"
    @click="copy"
  >
    <Check v-if="copied" class="h-3.5 w-3.5 text-emerald-500" />
    <Copy v-else class="h-3.5 w-3.5" />
    <span v-if="!compact">{{ copied ? 'Copied' : label || 'Copy' }}</span>
  </button>
</template>
