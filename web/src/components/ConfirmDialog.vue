<script setup lang="ts">
import Modal from './Modal.vue'

const open = defineModel<boolean>('open', { required: true })
withDefaults(
  defineProps<{
    title: string
    description?: string
    confirmLabel?: string
    tone?: 'danger' | 'primary'
    loading?: boolean
  }>(),
  { confirmLabel: 'Confirm', tone: 'danger', loading: false },
)
defineEmits<{ confirm: [] }>()
</script>

<template>
  <Modal v-model:open="open" :title="title" :description="description" size="sm">
    <slot />
    <template #footer>
      <button class="btn btn-secondary" :disabled="loading" @click="open = false">Cancel</button>
      <button
        class="btn"
        :class="tone === 'danger' ? 'btn-danger' : 'btn-primary'"
        :disabled="loading"
        @click="$emit('confirm')"
      >
        {{ loading ? 'Working…' : confirmLabel }}
      </button>
    </template>
  </Modal>
</template>
