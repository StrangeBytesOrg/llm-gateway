<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@pinia/colada'
import {
  Database,
  Download,
  ExternalLink,
  KeyRound,
  RefreshCw,
  Server,
  ShieldCheck,
  UserRound,
} from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import StatusBadge from '../components/StatusBadge.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadingSpinner from '../components/LoadingSpinner.vue'
import CopyButton from '../components/CopyButton.vue'
import { messageOf } from '../api/client'
import { sessionQuery } from '../lib/auth'
import { formatDate } from '../lib/format'
import { configQuery, readyQuery, useCreateBackup } from '../lib/queries/system'

const {
  data: config,
  isPending: configPending,
  error: configError,
  refetch: refetchConfig,
} = useQuery(configQuery)
const { data: session, refetch: refetchSession } = useQuery(sessionQuery)
const { data: readyData, refetch: refetchReady } = useQuery(readyQuery)
const backup = useCreateBackup()
const { isLoading: backingUp, data: backupResult, mutate: createBackup } = backup

const groups = computed(() =>
  Object.entries(config.value?.model_access_groups ?? {}).sort(([a], [b]) => a.localeCompare(b)),
)
const ready = computed<'ready' | 'unavailable'>(() => (readyData.value ? 'ready' : 'unavailable'))
const error = computed(() => messageOf(configError.value, 'Unable to load configuration.'))
const lastBackup = computed(() => backupResult.value?.filename ?? '')
const contractURL = computed(() => `${window.location.origin}/openapi.json`)

function load(): void {
  refetchConfig()
  refetchSession()
  refetchReady()
}
</script>

<template>
  <div>
    <PageHeader
      eyebrow="System"
      title="Settings"
      description="Session details, non-secret gateway configuration, and local maintenance actions."
    >
      <template #actions>
        <StatusBadge :status="ready" />
        <button class="btn btn-secondary" :disabled="configPending" @click="load">
          <RefreshCw class="h-4 w-4" :class="configPending ? 'animate-spin' : ''" />
          Refresh
        </button>
      </template>
    </PageHeader>

    <ErrorBanner :message="error" />

    <div v-if="configPending" class="card flex items-center justify-center py-20">
      <LoadingSpinner label="Loading settings" />
    </div>

    <div v-else class="grid gap-4 xl:grid-cols-2">
      <section class="card">
        <div class="flex items-center gap-2 border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
          <UserRound class="h-4 w-4 text-zinc-400" />
          <div>
            <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Admin session</h2>
            <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Credentials and expiry for this browser session.</p>
          </div>
        </div>
        <dl class="grid gap-4 p-4 sm:grid-cols-2">
          <div>
            <dt class="label">Username</dt>
            <dd class="text-sm font-medium text-zinc-800 dark:text-zinc-200">{{ session?.username || config?.admin_username || '—' }}</dd>
          </div>
          <div>
            <dt class="label">Authentication</dt>
            <dd class="text-sm text-zinc-700 dark:text-zinc-300">{{ session?.kind === 'token' ? 'Admin token' : 'Session token' }}</dd>
          </div>
          <div>
            <dt class="label">Expires</dt>
            <dd class="text-sm text-zinc-700 dark:text-zinc-300">{{ session?.expires_unix ? formatDate(session.expires_unix) : 'Does not expire' }}</dd>
          </div>
          <div>
            <dt class="label">Session policy</dt>
            <dd class="text-sm text-zinc-700 dark:text-zinc-300">12 hours · process memory</dd>
          </div>
        </dl>
      </section>

      <section class="card">
        <div class="flex items-center gap-2 border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
          <Server class="h-4 w-4 text-zinc-400" />
          <div>
            <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Gateway</h2>
            <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Readiness and the generated admin contract.</p>
          </div>
        </div>
        <div class="space-y-4 p-4">
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium text-zinc-800 dark:text-zinc-200">Readiness</p>
              <p class="text-xs text-zinc-500 dark:text-zinc-400">Database connectivity and admission state.</p>
            </div>
            <StatusBadge :status="ready" />
          </div>
          <div class="divider" />
          <div>
            <p class="text-sm font-medium text-zinc-800 dark:text-zinc-200">Admin OpenAPI contract</p>
            <div class="mt-1 flex items-center gap-2">
              <a :href="contractURL" target="_blank" rel="noreferrer" class="link mono break-all text-xs">{{ contractURL }}</a>
              <CopyButton :value="contractURL" label="Copy contract URL" />
              <a :href="contractURL" target="_blank" rel="noreferrer" class="text-zinc-400 transition hover:text-zinc-700 dark:hover:text-zinc-200">
                <ExternalLink class="h-3.5 w-3.5" />
              </a>
            </div>
          </div>
          <div class="divider" />
          <div class="flex items-center justify-between gap-3">
            <div>
              <p class="text-sm font-medium text-zinc-800 dark:text-zinc-200">Default model access group</p>
              <p class="text-xs text-zinc-500 dark:text-zinc-400">Assigned to new users when the request omits a group.</p>
            </div>
            <span class="inline-flex rounded-md bg-zinc-100 px-2 py-0.5 text-[11px] font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
              {{ config?.default_model_access_group || '—' }}
            </span>
          </div>
        </div>
      </section>

      <section class="card xl:col-span-2">
        <div class="flex items-center gap-2 border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
          <KeyRound class="h-4 w-4 text-zinc-400" />
          <div>
            <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Model access groups</h2>
            <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Defined in configuration. Changes require a gateway restart.</p>
          </div>
        </div>
        <div v-if="groups.length === 0" class="p-4 text-sm text-zinc-500 dark:text-zinc-400">No groups reported.</div>
        <div v-else class="divide-y divide-zinc-100 dark:divide-zinc-800/70">
          <div v-for="[name, group] in groups" :key="name" class="flex flex-col gap-2 p-4 sm:flex-row sm:items-start sm:justify-between">
            <div class="flex items-center gap-2">
              <span class="mono text-sm font-medium text-zinc-800 dark:text-zinc-200">{{ name }}</span>
              <span v-if="name === config?.default_model_access_group" class="rounded-full bg-indigo-50 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-300">
                default
              </span>
            </div>
            <div class="flex flex-wrap items-center gap-1.5">
              <span class="text-[11px] text-zinc-400 dark:text-zinc-500">Models:</span>
              <span
                v-for="model in group.models"
                :key="model"
                class="rounded-md bg-zinc-100 px-2 py-0.5 font-mono text-[11px] text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
              >
                {{ model }}
              </span>
              <span v-if="group.models.length === 0" class="text-xs italic text-zinc-400 dark:text-zinc-500">No model access</span>
            </div>
          </div>
        </div>
      </section>

      <section class="card xl:col-span-2">
        <div class="flex items-center gap-2 border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
          <Database class="h-4 w-4 text-zinc-400" />
          <div>
            <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Backups</h2>
            <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Create and verify a local SQLite snapshot under the configured backup directory.</p>
          </div>
        </div>
        <div class="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p class="text-sm text-zinc-700 dark:text-zinc-300">
              Automatic backups run on the configured interval. Manual snapshots are useful before maintenance.
            </p>
            <p v-if="lastBackup" class="mt-1 flex items-center gap-1.5 text-xs text-emerald-600 dark:text-emerald-400">
              <ShieldCheck class="h-3.5 w-3.5" />
              {{ lastBackup }}
            </p>
          </div>
          <button class="btn btn-primary shrink-0" :disabled="backingUp" @click="createBackup()">
            <Download class="h-4 w-4" :class="backingUp ? 'animate-bounce' : ''" />
            {{ backingUp ? 'Creating backup…' : 'Create backup' }}
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
