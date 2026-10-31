<script setup lang="ts">
import { AlertCircle, CheckCircle2, Info, X } from '@lucide/vue'
import { dismissToast, useToasts } from '../lib/toast'

const messages = useToasts()

const icons = {
  success: CheckCircle2,
  error: AlertCircle,
  info: Info,
}
</script>

<template>
  <div class="pointer-events-none fixed right-4 top-4 z-[100] flex w-full max-w-sm flex-col gap-2" aria-live="polite">
    <TransitionGroup
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="-translate-y-2 opacity-0"
      enter-to-class="translate-y-0 opacity-100"
      leave-active-class="transition duration-150 ease-in"
      leave-from-class="translate-y-0 opacity-100"
      leave-to-class="-translate-y-1 opacity-0"
    >
      <div
        v-for="message in messages"
        :key="message.id"
        class="pointer-events-auto flex items-start gap-3 rounded-xl border bg-white p-3.5 shadow-lg animate-toast-in dark:bg-zinc-900"
        :class="
          message.tone === 'error'
            ? 'border-red-200 dark:border-red-900/60'
            : message.tone === 'info'
              ? 'border-sky-200 dark:border-sky-900/60'
              : 'border-emerald-200 dark:border-emerald-900/60'
        "
      >
        <component
          :is="icons[message.tone]"
          class="mt-0.5 h-4 w-4 shrink-0"
          :class="
            message.tone === 'error'
              ? 'text-red-500'
              : message.tone === 'info'
                ? 'text-sky-500'
                : 'text-emerald-500'
          "
        />
        <div class="min-w-0 flex-1">
          <p class="text-sm font-medium text-zinc-900 dark:text-zinc-100">{{ message.title }}</p>
          <p v-if="message.description" class="mt-0.5 text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">
            {{ message.description }}
          </p>
        </div>
        <button
          class="rounded-md p-1 text-zinc-400 transition hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
          @click="dismissToast(message.id)"
        >
          <X class="h-3.5 w-3.5" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
