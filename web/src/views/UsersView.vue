<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useInfiniteQuery, useQuery } from '@pinia/colada'
import { ChevronRight, Plus, RefreshCw, Search, Users } from '@lucide/vue'
import PageHeader from '../components/PageHeader.vue'
import StatusBadge from '../components/StatusBadge.vue'
import EmptyState from '../components/EmptyState.vue'
import AsyncState from '../components/AsyncState.vue'
import LoadMore from '../components/LoadMore.vue'
import Modal from '../components/Modal.vue'
import { ApiError, messageOf } from '../api/client'
import { encodeId } from '../lib/encoding'
import { formatDate, formatUSD, fromDateTimeLocal, toDateTimeLocal } from '../lib/format'
import { configQuery } from '../lib/queries/system'
import { useCreateUser, usersInfiniteQuery } from '../lib/queries/users'
import type { User } from '../types'

const router = useRouter()
const {
  data: usersData,
  isPending: usersPending,
  isLoading: usersLoading,
  error: usersError,
  hasNextPage,
  loadNextPage,
  refetch: refetchUsers,
} = useInfiniteQuery(usersInfiniteQuery)
const users = computed(() => usersData.value?.pages.flatMap((page) => page.data) ?? [])
const { data: config } = useQuery(configQuery)
const {
  isLoading: creating,
  error: createError,
  mutateAsync: createUserAsync,
  reset: resetCreate,
} = useCreateUser()
const error = computed(() => messageOf(usersError.value, 'Unable to load users.'))
const formError = computed(() => {
  const err = createError.value
  if (!err) return ''
  if (err instanceof ApiError && err.status === 409) return 'A user with that ID already exists.'
  return messageOf(err, 'Unable to create the user.')
})
const search = ref('')
const statusFilter = ref<'all' | 'enabled' | 'disabled'>('all')
const createOpen = ref(false)

const form = reactive({
  id: '',
  budget_usd: '10.00',
  cadence: 'monthly' as 'weekly' | 'monthly',
  anchor: toDateTimeLocal(Math.floor(Date.now() / 1000)),
  model_access_group: '',
})

const filtered = computed(() => {
  const needle = search.value.trim().toLowerCase()
  return users.value.filter((user) => {
    if (statusFilter.value === 'enabled' && !user.enabled) return false
    if (statusFilter.value === 'disabled' && user.enabled) return false
    if (!needle) return true
    return user.id.toLowerCase().includes(needle) || user.model_access_group.toLowerCase().includes(needle)
  })
})

const groups = computed(() => Object.keys(config.value?.model_access_groups ?? {}).sort())

function openUser(user: User): void {
  router.push({ name: 'user', params: { id: encodeId(user.id) } })
}

function openCreate(): void {
  resetCreate()
  form.id = ''
  form.budget_usd = '10.00'
  form.cadence = 'monthly'
  form.anchor = toDateTimeLocal(Math.floor(Date.now() / 1000))
  form.model_access_group = config.value?.default_model_access_group ?? ''
  createOpen.value = true
}

function closeCreate(): void {
  createOpen.value = false
}

async function submitCreate(): Promise<void> {
  if (creating.value) return
  resetCreate()
  try {
    await createUserAsync({
      budget_usd: form.budget_usd,
      cadence: form.cadence,
      anchor: fromDateTimeLocal(form.anchor),
      ...(form.id.trim() ? { id: form.id.trim() } : {}),
      ...(form.model_access_group ? { model_access_group: form.model_access_group } : {}),
    })
    createOpen.value = false
  } catch {
    // The mutation error is rendered from `formError`.
  }
}
</script>

<template>
  <div>
    <PageHeader
      eyebrow="Access"
      title="Users"
      :description="`${users.length}${hasNextPage ? '+' : ''} loaded. Manage recurring budgets, model access, and enablement. Search filters loaded users.`"
    >
      <template #actions>
        <button class="btn btn-secondary" :disabled="usersLoading" @click="refetchUsers()">
          <RefreshCw class="h-4 w-4" :class="usersLoading ? 'animate-spin' : ''" />
          Refresh
        </button>
        <button class="btn btn-primary" @click="openCreate">
          <Plus class="h-4 w-4" />
          Create user
        </button>
      </template>
    </PageHeader>

    <div class="card overflow-hidden">
      <div class="flex flex-col gap-3 border-b border-zinc-200 p-4 sm:flex-row sm:items-center sm:justify-between dark:border-zinc-800">
        <div class="relative max-w-sm flex-1">
          <Search class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-zinc-400" />
          <input v-model="search" class="input pl-9" placeholder="Filter loaded users by ID or group…" />
        </div>
        <div class="flex items-center gap-2">
          <select v-model="statusFilter" class="input w-auto min-w-32">
            <option value="all">All statuses</option>
            <option value="enabled">Enabled</option>
            <option value="disabled">Disabled</option>
          </select>
        </div>
      </div>

      <AsyncState
        :pending="usersPending"
        :error="error"
        :empty="filtered.length === 0"
        label="Loading users"
      >
        <div class="overflow-x-auto">
          <table class="w-full">
          <thead>
            <tr class="border-b border-zinc-200 dark:border-zinc-800">
              <th class="table-head">User</th>
              <th class="table-head">Model access</th>
              <th class="table-head">Budget</th>
              <th class="table-head">Cadence</th>
              <th class="table-head">Status</th>
              <th class="table-head text-right">Created</th>
              <th class="w-10" />
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="user in filtered"
              :key="user.id"
              class="group cursor-pointer border-b border-zinc-100 last:border-0 transition hover:bg-zinc-50 focus:outline-none focus-visible:bg-zinc-50 dark:border-zinc-800/70 dark:hover:bg-zinc-800/40 dark:focus-visible:bg-zinc-800/40"
              tabindex="0"
              @click="openUser(user)"
              @keydown.enter="openUser(user)"
            >
              <td class="table-cell">
                <p class="mono font-medium text-zinc-800 dark:text-zinc-200">{{ user.id }}</p>
                <p class="mt-0.5 text-[11px] text-zinc-400 dark:text-zinc-500">{{ user.budget_policy_id }}</p>
              </td>
              <td class="table-cell">
                <span class="inline-flex rounded-md bg-zinc-100 px-2 py-0.5 text-[11px] font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                  {{ user.model_access_group }}
                </span>
              </td>
              <td class="table-cell font-medium text-zinc-800 dark:text-zinc-200">{{ formatUSD(user.budget_usd) }}</td>
              <td class="table-cell capitalize text-zinc-600 dark:text-zinc-400">{{ user.cadence }}</td>
              <td class="table-cell"><StatusBadge :status="user.enabled ? 'enabled' : 'disabled'" /></td>
              <td class="table-cell text-right text-xs text-zinc-500 dark:text-zinc-400">{{ formatDate(user.created_unix) }}</td>
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
              :title="users.length === 0 ? 'No users yet' : 'No matching users'"
              :description="users.length === 0 ? 'Create the first user to issue keys and set a recurring budget.' : 'Try a different search or status filter.'"
              :icon="Users"
            >
              <button v-if="users.length === 0" class="btn btn-primary" @click="openCreate">
                <Plus class="h-4 w-4" />
                Create user
              </button>
            </EmptyState>
          </div>
        </template>
      </AsyncState>

      <LoadMore
        v-if="hasNextPage && !usersPending"
        :loading="usersLoading"
        label="Load more users"
        @load="loadNextPage"
      />
    </div>

    <Modal
      v-model:open="createOpen"
      title="Create user"
      description="The user starts with a recurring budget and the selected model access group."
      size="lg"
    >
      <form id="create-user-form" class="space-y-4" @submit.prevent="submitCreate">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="label" for="user-id">User ID <span class="normal-case text-zinc-400">(optional)</span></label>
            <input id="user-id" v-model="form.id" class="input mono" maxlength="256" placeholder="Auto-generated" />
            <p class="hint">Immutable. Leave blank to generate a <span class="mono">usr_</span> ID.</p>
          </div>
          <div>
            <label class="label" for="user-budget">Budget (USD)</label>
            <input id="user-budget" v-model="form.budget_usd" class="input" inputmode="decimal" required placeholder="10.00" />
          </div>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="label" for="user-cadence">Cadence</label>
            <select id="user-cadence" v-model="form.cadence" class="input">
              <option value="monthly">Monthly</option>
              <option value="weekly">Weekly</option>
            </select>
          </div>
          <div>
            <label class="label" for="user-group">Model access group</label>
            <select id="user-group" v-model="form.model_access_group" class="input">
              <option v-for="group in groups" :key="group" :value="group">{{ group }}</option>
            </select>
          </div>
        </div>
        <div>
          <label class="label" for="user-anchor">Renewal anchor</label>
          <input id="user-anchor" v-model="form.anchor" class="input" type="datetime-local" required />
          <p class="hint">Normalized to UTC. A future anchor blocks inference until that date.</p>
        </div>
        <div v-if="formError" class="rounded-lg border border-red-200 bg-red-50 px-3.5 py-3 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300">
          {{ formError }}
        </div>
      </form>
      <template #footer>
        <button class="btn btn-secondary" :disabled="creating" @click="closeCreate">Cancel</button>
        <button form="create-user-form" type="submit" class="btn btn-primary" :disabled="creating">
          {{ creating ? 'Creating…' : 'Create user' }}
        </button>
      </template>
    </Modal>
  </div>
</template>
