<script setup lang="ts">
import { AlertTriangle } from '@lucide/vue'
import Modal from './Modal.vue'
import CopyButton from './CopyButton.vue'

const open = defineModel<boolean>('open', { required: true })
defineProps<{ alias: string; secret: string }>()
</script>

<template>
  <Modal
    v-model:open="open"
    title="Save this key secret"
    description="The gateway stores only a hash. This value cannot be retrieved again."
    size="lg"
  >
    <div class="space-y-4">
      <div>
        <p class="label">Key alias</p>
        <p class="mono text-zinc-700 dark:text-zinc-300">{{ alias }}</p>
      </div>
      <div>
        <div class="mb-1.5 flex items-center justify-between">
          <p class="label mb-0">Secret</p>
          <CopyButton :value="secret" label="Copy secret" :compact="false" />
        </div>
        <div class="break-all rounded-lg border border-zinc-800 bg-zinc-950 px-3.5 py-3 font-mono text-xs leading-relaxed text-zinc-100">
          {{ secret }}
        </div>
      </div>
      <div class="flex gap-2.5 rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs leading-relaxed text-amber-800 dark:border-amber-900/60 dark:bg-amber-500/10 dark:text-amber-200">
        <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" />
        <p>Copy this secret into the client now. Rotating the key later replaces it and invalidates the previous value.</p>
      </div>
    </div>
    <template #footer>
      <button class="btn btn-primary" @click="open = false">I saved the secret</button>
    </template>
  </Modal>
</template>
