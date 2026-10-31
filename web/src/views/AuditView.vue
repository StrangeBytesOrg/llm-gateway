<script setup lang="ts">
import { computed, ref } from 'vue'
import { useInfiniteQuery } from '@pinia/colada'
import { RefreshCw, ScrollText, Search } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import EmptyState from '../components/EmptyState.vue'
import AsyncState from '../components/AsyncState.vue'
import LoadMore from '../components/LoadMore.vue'
import Modal from '../components/Modal.vue'
import { messageOf } from '../api/client'
import { formatDate, formatRelative, truncate } from '../lib/format'
import { auditInfiniteQuery } from '../lib/queries/audit'
import type { AuditEntry } from '../types'

const {
  data,
  isPending,
  isLoading,
  error,
  hasNextPage,
  loadNextPage,
  refetch,
} = useInfiniteQuery(auditInfiniteQuery)
const entries = computed(() => data.value?.pages.flatMap((page) => page.data) ?? [])
const errorMessage = computed(() => messageOf(error.value, 'Unable to load the audit log.'))
const search = ref('')
const selected = ref<AuditEntry | null>(null)
const detailOpen = ref(false)

const filtered = computed(() => {
  const needle = search.value.trim().toLowerCase()
  if (!needle) return entries.value
  return entries.value.filter(
    (entry) => entry.action.toLowerCase().includes(needle) || entry.target.toLowerCase().includes(needle),
  )
})

const detailJSON = computed(() => {
  if (!selected.value) return ''
  try {
    return JSON.stringify(selected.value.detail ?? {}, null, 2)
  } catch {
    return '{}'
  }
})

function showDetail(entry: AuditEntry): void {
  selected.value = entry
  detailOpen.value = true
}
</script>

<template>
  <div>
    <PageHeader
      eyebrow="Events"
      title="Audit log"
      :description="`${entries.length}${hasNextPage ? '+' : ''} administrative events loaded. Newest records first, in ID order.`"
    >
      <template #actions>
        <button class="btn btn-secondary" :disabled="isLoading" @click="refetch()">
          <RefreshCw class="h-4 w-4" :class="isLoading ? 'animate-spin' : ''" />
          Refresh
        </button>
      </template>
    </PageHeader>

    <div class="card overflow-hidden">
      <div class="border-b border-zinc-200 p-4 dark:border-zinc-800">
        <div class="relative max-w-sm">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400" />
          <input v-model="search" class="input pl-9" placeholder="Filter loaded events by action or target…" />
        </div>
      </div>

      <AsyncState
        :pending="isPending"
        :error="errorMessage"
        :empty="filtered.length === 0"
        label="Loading audit log"
      >
        <div class="overflow-x-auto">
          <table class="w-full">
          <thead>
            <tr class="border-b border-zinc-200 dark:border-zinc-800">
              <th class="table-head">ID</th>
              <th class="table-head">Action</th>
              <th class="table-head">Target</th>
              <th class="table-head text-right">Created</th>
              <th class="table-head text-right">Detail</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="entry in filtered"
              :key="entry.id"
              class="cursor-pointer border-b border-zinc-100 last:border-0 transition hover:bg-zinc-50 focus:outline-none focus-visible:bg-zinc-50 dark:border-zinc-800/70 dark:hover:bg-zinc-800/40 dark:focus-visible:bg-zinc-800/40"
              tabindex="0"
              @click="showDetail(entry)"
              @keydown.enter="showDetail(entry)"
            >
              <td class="table-cell mono text-zinc-400 dark:text-zinc-500">#{{ entry.id }}</td>
              <td class="table-cell">
                <span class="inline-flex rounded-md bg-zinc-100 px-2 py-0.5 font-mono text-[11px] font-medium text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300">
                  {{ entry.action }}
                </span>
              </td>
              <td class="table-cell mono text-zinc-600 dark:text-zinc-400">{{ truncate(entry.target, 44) }}</td>
              <td class="table-cell text-right text-xs text-zinc-500 dark:text-zinc-400" :title="formatDate(entry.created_unix)">
                {{ formatRelative(entry.created_unix) }}
              </td>
              <td class="table-cell text-right text-xs font-medium text-indigo-600 dark:text-indigo-400">View</td>
            </tr>
          </tbody>
          </table>
        </div>
        <template #empty>
          <div class="px-4">
            <EmptyState
              :title="entries.length === 0 ? 'No audit events yet' : 'No matching events'"
              :description="entries.length === 0 ? 'User, key, and accounting changes are recorded here.' : 'Try a different search term.'"
              :icon="ScrollText"
            />
          </div>
        </template>
      </AsyncState>

      <LoadMore
        v-if="hasNextPage && !isPending"
        :loading="isLoading"
        label="Load more events"
        @load="loadNextPage"
      />
    </div>

    <Modal
      v-model:open="detailOpen"
      :title="selected ? `Event #${selected.id}` : 'Event'"
      :description="selected?.action"
      size="lg"
    >
      <div v-if="selected" class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <p class="label">Target</p>
            <p class="mono break-all text-zinc-700 dark:text-zinc-300">{{ selected.target }}</p>
          </div>
          <div>
            <p class="label">Created</p>
            <p class="text-sm text-zinc-700 dark:text-zinc-300">{{ formatDate(selected.created_unix) }}</p>
          </div>
        </div>
        <div>
          <p class="label">Detail</p>
          <pre class="max-h-96 overflow-auto rounded-lg border border-zinc-800 bg-zinc-950 p-4 font-mono text-[11px] leading-relaxed text-zinc-300">{{ detailJSON }}</pre>
        </div>
      </div>
      <template #footer>
        <button class="btn btn-secondary" @click="detailOpen = false">Close</button>
      </template>
    </Modal>
  </div>
</template>
