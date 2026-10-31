<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useInfiniteQuery } from '@pinia/colada'
import { Activity, ChevronRight, FilterX, RefreshCw, Search } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import StatusBadge from '../components/StatusBadge.vue'
import EmptyState from '../components/EmptyState.vue'
import AsyncState from '../components/AsyncState.vue'
import LoadMore from '../components/LoadMore.vue'
import { ApiError, messageOf } from '../api/client'
import { encodeId } from '../lib/encoding'
import { formatDate, formatRelative, formatUSD, truncate } from '../lib/format'
import { requestsInfiniteQuery } from '../lib/queries/requests'
import type { Request, RequestFilters } from '../types'

const router = useRouter()
type AccountingFilter = '' | NonNullable<RequestFilters['accounting']>
const userFilter = ref('')
const accountingFilter = ref<AccountingFilter>('')
const appliedUser = ref('')
const appliedAccounting = ref<AccountingFilter>('')

const {
  data,
  isPending,
  isLoading,
  error,
  hasNextPage,
  loadNextPage,
  refetch,
} = useInfiniteQuery(() =>
  requestsInfiniteQuery({
    ...(appliedUser.value ? { user_id: appliedUser.value } : {}),
    ...(appliedAccounting.value ? { accounting: appliedAccounting.value } : {}),
  }),
)
const requests = computed(() => data.value?.pages.flatMap((page) => page.data) ?? [])
const errorMessage = computed(() => {
  const err = error.value
  if (err instanceof ApiError && err.status === 404) return 'No user matches that exact ID.'
  return messageOf(err, 'Unable to load requests.')
})

function openRequest(request: Request): void {
  router.push({ name: 'request', params: { id: encodeId(request.id) } })
}

function apply(): void {
  const nextUser = userFilter.value.trim()
  const nextAccounting = accountingFilter.value
  if (nextUser === appliedUser.value && nextAccounting === appliedAccounting.value) {
    refetch()
    return
  }
  appliedUser.value = nextUser
  appliedAccounting.value = nextAccounting
}

function clear(): void {
  userFilter.value = ''
  accountingFilter.value = ''
  appliedUser.value = ''
  appliedAccounting.value = ''
}
</script>

<template>
  <div>
    <PageHeader
      eyebrow="Activity"
      title="Requests"
      :description="`${requests.length}${hasNextPage ? '+' : ''} request records loaded. Newest first, with lifecycle and accounting state.`"
    >
      <template #actions>
        <button class="btn btn-secondary" :disabled="isLoading" @click="refetch()">
          <RefreshCw class="h-4 w-4" :class="isLoading ? 'animate-spin' : ''" />
          Refresh
        </button>
      </template>
    </PageHeader>

    <div class="card overflow-hidden">
      <div class="flex flex-col gap-3 border-b border-zinc-200 p-4 lg:flex-row lg:items-center dark:border-zinc-800">
        <form class="flex flex-1 flex-col gap-2 sm:flex-row" @submit.prevent="apply">
          <div class="relative min-w-0 flex-1">
            <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400" />
            <input v-model="userFilter" class="input mono pl-9" placeholder="Exact user ID…" />
          </div>
          <select v-model="accountingFilter" class="input sm:w-44">
            <option value="">All accounting</option>
            <option value="pending">Pending</option>
            <option value="settled">Settled</option>
            <option value="unresolved">Unresolved</option>
          </select>
          <button type="submit" class="btn btn-secondary">Apply</button>
          <button v-if="appliedUser || appliedAccounting" type="button" class="btn btn-ghost" title="Clear filters" @click="clear">
            <FilterX class="h-4 w-4" />
            Clear
          </button>
        </form>
      </div>

      <AsyncState
        :pending="isPending"
        :error="errorMessage"
        :empty="requests.length === 0"
        label="Loading requests"
      >
        <div class="overflow-x-auto">
          <table class="w-full">
          <thead>
            <tr class="border-b border-zinc-200 dark:border-zinc-800">
              <th class="table-head">Request</th>
              <th class="table-head">User</th>
              <th class="table-head">Model</th>
              <th class="table-head">Lifecycle</th>
              <th class="table-head">Accounting</th>
              <th class="table-head text-right">Cost</th>
              <th class="table-head text-right">Created</th>
              <th class="w-10" />
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="request in requests"
              :key="request.id"
              class="group cursor-pointer border-b border-zinc-100 last:border-0 transition hover:bg-zinc-50 focus:outline-none focus-visible:bg-zinc-50 dark:border-zinc-800/70 dark:hover:bg-zinc-800/40 dark:focus-visible:bg-zinc-800/40"
              tabindex="0"
              @click="openRequest(request)"
              @keydown.enter="openRequest(request)"
            >
              <td class="table-cell">
                <p class="mono font-medium text-zinc-800 dark:text-zinc-200">{{ truncate(request.id, 24) }}</p>
                <p class="mt-0.5 text-[11px] text-zinc-400 dark:text-zinc-500">{{ request.generation_id || request.period_id }}</p>
              </td>
              <td class="table-cell mono text-zinc-600 dark:text-zinc-400">{{ truncate(request.user_id, 30) }}</td>
              <td class="table-cell">
                <p class="font-medium text-zinc-800 dark:text-zinc-200">{{ request.model }}</p>
                <p class="text-[11px] text-zinc-500 dark:text-zinc-400">{{ request.provider }} · {{ request.upstream_model }}</p>
              </td>
              <td class="table-cell"><StatusBadge :status="request.lifecycle" /></td>
              <td class="table-cell"><StatusBadge :status="request.accounting" /></td>
              <td class="table-cell text-right font-medium text-zinc-800 dark:text-zinc-200">
                {{ request.cost_usd ? formatUSD(request.cost_usd) : '—' }}
              </td>
              <td class="table-cell text-right text-xs text-zinc-500 dark:text-zinc-400" :title="formatDate(request.created_unix)">
                {{ formatRelative(request.created_unix) }}
              </td>
              <td class="table-cell text-right">
                <ChevronRight class="ml-auto h-4 w-4 text-zinc-300 transition group-hover:text-zinc-500 dark:text-zinc-600 dark:group-hover:text-zinc-400" />
              </td>
            </tr>
          </tbody>
          </table>
        </div>
        <template #empty>
          <div class="px-4">
            <EmptyState
              title="No requests found"
              description="Inference requests appear here after users call the gateway. Adjust the filters if needed."
              :icon="Activity"
            />
          </div>
        </template>
      </AsyncState>

      <LoadMore
        v-if="hasNextPage && !isPending"
        :loading="isLoading"
        label="Load more requests"
        @load="loadNextPage"
      />
    </div>
  </div>
</template>
