<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@pinia/colada'
import {
  Activity,
  ArrowUpRight,
  CircleDollarSign,
  KeyRound,
  RefreshCw,
  ShieldAlert,
  Users,
} from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadingSpinner from '../components/LoadingSpinner.vue'
import { messageOf } from '../api/client'
import { formatDate, formatRelative, formatUSD, truncate } from '../lib/format'
import { overviewQuery, recentAuditQuery, recentRequestsQuery, readyQuery } from '../lib/queries/system'

const {
  data: overview,
  isPending: overviewPending,
  error: overviewError,
  refetch: refetchOverview,
} = useQuery(overviewQuery)
const { data: requestsPage, refetch: refetchRequests } = useQuery(recentRequestsQuery)
const { data: auditPage, refetch: refetchAudit } = useQuery(recentAuditQuery)
const { data: readyData, refetch: refetchReady } = useQuery(readyQuery)

const requests = computed(() => requestsPage.value?.data ?? [])
const audit = computed(() => auditPage.value?.data ?? [])
const ready = computed<'ready' | 'unavailable'>(() => (readyData.value ? 'ready' : 'unavailable'))
const error = computed(() => messageOf(overviewError.value, 'Unable to load the overview.'))
const attention = computed(() => (overview.value?.pending_requests ?? 0) + (overview.value?.unresolved_requests ?? 0))

function load(): void {
  refetchOverview()
  refetchRequests()
  refetchAudit()
  refetchReady()
}
</script>

<template>
  <div>
    <PageHeader
      eyebrow="Dashboard"
      title="Overview"
      description="A quick read on access, activity, and accounting across the gateway."
    >
      <template #actions>
        <StatusBadge :status="ready" />
        <button class="btn btn-secondary" :disabled="overviewPending" @click="load">
          <RefreshCw class="h-4 w-4" :class="overviewPending ? 'animate-spin' : ''" />
          Refresh
        </button>
      </template>
    </PageHeader>

    <ErrorBanner :message="error" />

    <div v-if="overviewPending" class="card flex items-center justify-center py-20">
      <LoadingSpinner label="Loading overview" />
    </div>

    <template v-else-if="overview">
      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <StatCard
          label="Users"
          :value="overview.users"
          :hint="`${overview.enabled_users} enabled`"
          :icon="Users"
          tone="indigo"
        />
        <StatCard
          label="Active keys"
          :value="overview.active_keys"
          hint="Current, unrevoked credentials"
          :icon="KeyRound"
          tone="emerald"
        />
        <StatCard
          label="Requests · 24h"
          :value="overview.requests_last_24h"
          :hint="`${overview.requests} retained in total`"
          :icon="Activity"
          tone="default"
        />
        <StatCard
          label="Spend · 24h"
          :value="formatUSD(overview.spend_last_24h_usd)"
          hint="Ledger movement, including corrections"
          :icon="CircleDollarSign"
          tone="indigo"
        />
        <StatCard
          label="Needs attention"
          :value="attention"
          :hint="`${overview.pending_requests} pending · ${overview.unresolved_requests} unresolved`"
          :icon="ShieldAlert"
          :tone="overview.unresolved_requests > 0 ? 'red' : attention > 0 ? 'amber' : 'emerald'"
        />
      </div>

      <div class="mt-5 grid gap-4 xl:grid-cols-3">
        <section class="card overflow-hidden xl:col-span-2">
          <div class="flex items-center justify-between border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
            <div>
              <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Recent requests</h2>
              <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Newest inference activity across all users.</p>
            </div>
            <RouterLink :to="{ name: 'requests' }" class="btn btn-ghost btn-sm">
              View all
              <ArrowUpRight class="h-3.5 w-3.5" />
            </RouterLink>
          </div>
          <div v-if="requests.length === 0" class="px-4">
            <EmptyState title="No requests yet" description="Inference requests will appear here as users call the gateway." :icon="Activity" />
          </div>
          <div v-else class="overflow-x-auto">
            <table class="w-full">
              <thead>
                <tr class="border-b border-zinc-200 dark:border-zinc-800">
                  <th class="table-head">Request</th>
                  <th class="table-head">Model</th>
                  <th class="table-head">Accounting</th>
                  <th class="table-head text-right">Cost</th>
                  <th class="table-head text-right">Created</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="request in requests"
                  :key="request.id"
                  class="border-b border-zinc-100 last:border-0 transition hover:bg-zinc-50 dark:border-zinc-800/70 dark:hover:bg-zinc-800/40"
                >
                  <td class="table-cell">
                    <RouterLink :to="{ name: 'request', params: { id: request.id } }" class="mono text-indigo-600 hover:underline dark:text-indigo-400">
                      {{ truncate(request.id, 18) }}
                    </RouterLink>
                    <p class="mt-0.5 text-[11px] text-zinc-500 dark:text-zinc-400">{{ truncate(request.user_id, 28) }}</p>
                  </td>
                  <td class="table-cell">
                    <p class="font-medium text-zinc-800 dark:text-zinc-200">{{ request.model }}</p>
                    <p class="text-[11px] text-zinc-500 dark:text-zinc-400">{{ request.provider }}</p>
                  </td>
                  <td class="table-cell"><StatusBadge :status="request.accounting" /></td>
                  <td class="table-cell text-right font-medium text-zinc-800 dark:text-zinc-200">
                    {{ request.cost_usd ? formatUSD(request.cost_usd) : '—' }}
                  </td>
                  <td class="table-cell text-right text-xs text-zinc-500 dark:text-zinc-400" :title="formatDate(request.created_unix)">
                    {{ formatRelative(request.created_unix) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="card overflow-hidden">
          <div class="flex items-center justify-between border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
            <div>
              <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Recent events</h2>
              <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Administrative audit trail.</p>
            </div>
            <RouterLink :to="{ name: 'audit' }" class="btn btn-ghost btn-sm">
              View all
              <ArrowUpRight class="h-3.5 w-3.5" />
            </RouterLink>
          </div>
          <div v-if="audit.length === 0" class="px-4">
            <EmptyState title="No events yet" description="User and key changes will be recorded here." :icon="ShieldAlert" />
          </div>
          <ul v-else class="divide-y divide-zinc-100 dark:divide-zinc-800/70">
            <li v-for="entry in audit" :key="entry.id" class="px-4 py-3">
              <div class="flex items-start justify-between gap-3">
                <div class="min-w-0">
                  <p class="text-xs font-medium text-zinc-800 dark:text-zinc-200">{{ entry.action }}</p>
                  <p class="mt-0.5 truncate text-[11px] text-zinc-500 dark:text-zinc-400">{{ entry.target }}</p>
                </div>
                <span class="shrink-0 text-[11px] text-zinc-400 dark:text-zinc-500" :title="formatDate(entry.created_unix)">
                  {{ formatRelative(entry.created_unix) }}
                </span>
              </div>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </div>
</template>
