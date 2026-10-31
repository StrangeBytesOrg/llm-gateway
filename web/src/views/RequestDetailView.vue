<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@pinia/colada'
import {
  ArrowLeft,
  CheckCircle2,
  CircleDollarSign,
  Clock3,
  Code2,
  RefreshCw,
  RotateCcw,
  Server,
} from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import LoadingSpinner from '../components/LoadingSpinner.vue'
import CopyButton from '../components/CopyButton.vue'
import Modal from '../components/Modal.vue'
import EmptyState from '../components/EmptyState.vue'
import { ApiError, messageOf } from '../api/client'
import { decodeId, encodeId } from '../lib/encoding'
import { formatDate, formatDuration, formatUSD, trimUSD } from '../lib/format'
import { ledgerQuery, requestQuery, useResolveRequest } from '../lib/queries/requests'

const props = defineProps<{ id: string }>()

const requestId = computed(() => {
  try {
    return decodeId(props.id)
  } catch {
    return ''
  }
})

const {
  data: request,
  isPending: requestPending,
  error: requestError,
  refetch: refetchRequest,
} = useQuery(() => requestQuery(requestId.value))
const { data: ledgerPage, refetch: refetchLedger } = useQuery(() => ledgerQuery(requestId.value))
const ledger = computed(() => ledgerPage.value?.data ?? [])
const loading = computed(() => (requestId.value ? requestPending.value : false))
const error = computed(() => {
  if (!requestId.value) return 'Invalid request ID.'
  return messageOf(requestError.value, 'Unable to load this request.')
})

const resolveOpen = ref(false)
const resolveForm = reactive({ total_usd: '0.00', reason: '' })
const {
  isLoading: resolving,
  error: resolveErrorRaw,
  mutateAsync: resolveRequestAsync,
  reset: resetResolve,
} = useResolveRequest()
const resolveError = computed(() => {
  const err = resolveErrorRaw.value
  if (!err) return ''
  if (err instanceof ApiError && err.status === 409) return 'Only finished requests can be resolved.'
  return messageOf(err, 'Unable to resolve this request.')
})

const usageJSON = computed(() => {
  if (!request.value) return ''
  try {
    return JSON.stringify(request.value.usage ?? {}, null, 2)
  } catch {
    return '{}'
  }
})

function load(): void {
  refetchRequest()
  refetchLedger()
}

function openResolve(): void {
  if (!request.value) return
  resetResolve()
  resolveForm.total_usd = request.value.cost_usd ? trimUSD(request.value.cost_usd) : '0.00'
  resolveForm.reason = ''
  resolveOpen.value = true
}

async function resolve(): Promise<void> {
  if (!request.value || resolving.value) return
  resetResolve()
  try {
    await resolveRequestAsync({
      id: request.value.id,
      body: {
        total_usd: resolveForm.total_usd,
        reason: resolveForm.reason,
      },
    })
    resolveOpen.value = false
  } catch {
    // The mutation error is rendered from `resolveError`.
  }
}
</script>

<template>
  <div>
    <div class="mb-4">
      <RouterLink :to="{ name: 'requests' }" class="inline-flex items-center gap-1.5 text-xs font-medium text-zinc-500 transition hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200">
        <ArrowLeft class="h-3.5 w-3.5" />
        All requests
      </RouterLink>
    </div>

    <div v-if="loading" class="card flex items-center justify-center py-24">
      <LoadingSpinner label="Loading request" />
    </div>

    <div v-else-if="error" class="card px-6 py-16 text-center">
      <p class="text-sm font-medium text-zinc-900 dark:text-zinc-100">{{ error }}</p>
      <RouterLink :to="{ name: 'requests' }" class="btn btn-secondary mt-4">Back to requests</RouterLink>
    </div>

    <template v-else-if="request">
      <PageHeader :title="request.id" description="Inference lifecycle, accounting state, usage, and ledger entries." eyebrow="Request detail">
        <template #actions>
          <StatusBadge :status="request.lifecycle" />
          <StatusBadge :status="request.accounting" />
          <button class="btn btn-secondary" :disabled="loading" @click="load">
            <RefreshCw class="h-4 w-4" />
            Refresh
          </button>
          <button class="btn btn-primary" :disabled="!request.finished_unix" @click="openResolve">
            <RotateCcw class="h-4 w-4" />
            Resolve cost
          </button>
        </template>
      </PageHeader>

      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Cost" :value="request.cost_usd ? formatUSD(request.cost_usd) : 'Not settled'" :hint="request.raw_cost || 'No raw provider cost'" :icon="CircleDollarSign" tone="indigo" />
        <StatCard label="Lifecycle" :value="request.lifecycle" :hint="request.error_code || 'No error code'" :icon="CheckCircle2" :tone="request.error_code ? 'red' : 'emerald'" />
        <StatCard label="Attempts" :value="request.attempts" hint="Automatic reconciliation attempts" :icon="RotateCcw" tone="default" />
        <StatCard label="Duration" :value="formatDuration(request.created_unix, request.finished_unix)" hint="Created to finished" :icon="Clock3" tone="default" />
      </div>

      <div class="mt-5 grid gap-4 xl:grid-cols-3">
        <section class="card xl:col-span-2">
          <div class="flex items-center justify-between border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
            <div>
              <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Metadata</h2>
              <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Admission-time routing and accounting references.</p>
            </div>
            <CopyButton :value="request.id" label="Copy request ID" :compact="false" />
          </div>
          <dl class="grid gap-x-6 gap-y-4 p-4 sm:grid-cols-2">
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">User</dt>
              <dd class="mt-1">
                <RouterLink :to="{ name: 'user', params: { id: encodeId(request.user_id) } }" class="mono text-indigo-600 hover:underline dark:text-indigo-400">
                  {{ request.user_id }}
                </RouterLink>
              </dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Key ID</dt>
              <dd class="mono mt-1 text-zinc-700 dark:text-zinc-300">{{ request.key_id }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Model</dt>
              <dd class="mt-1 text-sm font-medium text-zinc-800 dark:text-zinc-200">{{ request.model }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Upstream</dt>
              <dd class="mt-1 text-sm text-zinc-700 dark:text-zinc-300">{{ request.provider }} · {{ request.upstream_model }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Model access group</dt>
              <dd class="mt-1 text-sm text-zinc-700 dark:text-zinc-300">{{ request.model_access_group }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Generation ID</dt>
              <dd class="mono mt-1 break-all text-zinc-700 dark:text-zinc-300">{{ request.generation_id || '—' }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Created</dt>
              <dd class="mt-1 text-sm text-zinc-700 dark:text-zinc-300">{{ formatDate(request.created_unix) }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Finished</dt>
              <dd class="mt-1 text-sm text-zinc-700 dark:text-zinc-300">{{ formatDate(request.finished_unix) }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Budget policy</dt>
              <dd class="mono mt-1 break-all text-zinc-700 dark:text-zinc-300">{{ request.budget_policy_id }}</dd>
            </div>
            <div>
              <dt class="text-[11px] font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Period</dt>
              <dd class="mono mt-1 break-all text-zinc-700 dark:text-zinc-300">{{ request.period_id }}</dd>
            </div>
          </dl>
        </section>

        <section class="card overflow-hidden">
          <div class="flex items-center gap-2 border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
            <Code2 class="h-4 w-4 text-zinc-400" />
            <div>
              <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Usage</h2>
              <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Persisted provider usage payload.</p>
            </div>
          </div>
          <pre class="max-h-80 overflow-auto bg-zinc-950 p-4 font-mono text-[11px] leading-relaxed text-zinc-300">{{ usageJSON }}</pre>
        </section>
      </div>

      <section class="card mt-4 overflow-hidden">
        <div class="flex items-center justify-between border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
          <div>
            <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Ledger</h2>
            <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Original charge and signed manual adjustments.</p>
          </div>
          <span class="text-xs text-zinc-400 dark:text-zinc-500">{{ ledger.length }} {{ ledger.length === 1 ? 'entry' : 'entries' }}</span>
        </div>
        <div v-if="ledger.length === 0" class="px-4">
          <EmptyState title="No ledger entries" description="The request has not been settled yet." :icon="Server" />
        </div>
        <div v-else class="overflow-x-auto">
          <table class="w-full">
            <thead>
              <tr class="border-b border-zinc-200 dark:border-zinc-800">
                <th class="table-head">ID</th>
                <th class="table-head">Source</th>
                <th class="table-head">Reason</th>
                <th class="table-head text-right">Amount</th>
                <th class="table-head text-right">Created</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in ledger" :key="entry.id" class="border-b border-zinc-100 last:border-0 dark:border-zinc-800/70">
                <td class="table-cell mono text-zinc-500 dark:text-zinc-400">{{ entry.id }}</td>
                <td class="table-cell text-zinc-700 dark:text-zinc-300">{{ entry.source }}</td>
                <td class="table-cell text-xs text-zinc-500 dark:text-zinc-400">{{ entry.reason || '—' }}</td>
                <td class="table-cell text-right font-medium" :class="Number(entry.amount_usd) < 0 ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'">
                  {{ formatUSD(entry.amount_usd) }}
                </td>
                <td class="table-cell text-right text-xs text-zinc-500 dark:text-zinc-400">{{ formatDate(entry.created_unix) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </template>

    <Modal v-model:open="resolveOpen" title="Resolve request cost" description="Set the absolute replacement total for this finished request. The ledger records only the difference." size="md">
      <form id="resolve-form" class="space-y-4" @submit.prevent="resolve">
        <div>
          <label class="label" for="resolve-total">Total (USD)</label>
          <input id="resolve-total" v-model="resolveForm.total_usd" class="input" inputmode="decimal" required />
          <p class="hint">This is the new total, not an additional charge.</p>
        </div>
        <div>
          <label class="label" for="resolve-reason">Reason</label>
          <textarea id="resolve-reason" v-model="resolveForm.reason" class="input min-h-24" maxlength="1000" required placeholder="Provider invoice reconciliation…" />
        </div>
        <div v-if="resolveError" class="rounded-lg border border-red-200 bg-red-50 px-3.5 py-3 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300">
          {{ resolveError }}
        </div>
      </form>
      <template #footer>
        <button class="btn btn-secondary" :disabled="resolving" @click="resolveOpen = false">Cancel</button>
        <button form="resolve-form" type="submit" class="btn btn-primary" :disabled="resolving">
          {{ resolving ? 'Resolving…' : 'Resolve cost' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
