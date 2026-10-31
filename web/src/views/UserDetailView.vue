<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@pinia/colada'
import {
  ArrowLeft,
  CalendarClock,
  CircleDollarSign,
  KeyRound,
  Pencil,
  Plus,
  RefreshCw,
  RotateCw,
  ShieldAlert,
  Trash2,
  Wallet,
} from '@lucide/vue'
import { SwitchRoot, SwitchThumb, TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import EmptyState from '../components/EmptyState.vue'
import LoadingSpinner from '../components/LoadingSpinner.vue'
import CopyButton from '../components/CopyButton.vue'
import Modal from '../components/Modal.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import SecretDialog from '../components/SecretDialog.vue'
import { ApiError, messageOf } from '../api/client'
import { decodeId } from '../lib/encoding'
import { formatDate, formatUSD, fromDateTimeLocal, percent, trimUSD } from '../lib/format'
import { configQuery } from '../lib/queries/system'
import {
  keysQuery,
  policiesQuery,
  usageQuery,
  userQuery,
  useCreateKey,
  useRevokeKey,
  useRotateKey,
  useUpdateUser,
} from '../lib/queries/users'
import { toast } from '../lib/toast'
import type { Key } from '../types'

const props = defineProps<{ id: string }>()

const userId = computed(() => {
  try {
    return decodeId(props.id)
  } catch {
    return ''
  }
})

const {
  data: user,
  isPending: userPending,
  error: userError,
  refetch: refetchUser,
} = useQuery(() => userQuery(userId.value))
const { data: summary, refetch: refetchUsage } = useQuery(() => usageQuery(userId.value))
const { data: keysPage, refetch: refetchKeys } = useQuery(() => keysQuery(userId.value))
const { data: policiesPage, refetch: refetchPolicies } = useQuery(() => policiesQuery(userId.value))
const { data: config, refetch: refetchConfig } = useQuery(configQuery)

const keys = computed(() => keysPage.value?.data ?? [])
const policies = computed(() => policiesPage.value?.data ?? [])
const loading = computed(() => (userId.value ? userPending.value : false))
const error = computed(() => {
  if (!userId.value) return 'Invalid user ID.'
  return messageOf(userError.value, 'Unable to load this user.')
})
const activeTab = ref('keys')

const editOpen = ref(false)
const editForm = reactive({ enabled: true, budget_usd: '0.00', model_access_group: '' })
const {
  isLoading: saving,
  error: updateError,
  mutateAsync: updateUserAsync,
  reset: resetUpdate,
} = useUpdateUser()
const editError = computed(() => messageOf(updateError.value, 'Unable to update the user.'))

const createKeyOpen = ref(false)
const keyForm = reactive({ alias: '', expires: '' })
const {
  isLoading: creatingKey,
  error: createKeyError,
  mutateAsync: createKeyAsync,
  reset: resetCreateKey,
} = useCreateKey()
const keyError = computed(() => {
  const err = createKeyError.value
  if (!err) return ''
  if (err instanceof ApiError && err.status === 409) {
    return 'A key with that alias already exists for this user.'
  }
  return messageOf(err, 'Unable to create the key.')
})

const rotateTarget = ref<Key | null>(null)
const rotateOpen = ref(false)
const { isLoading: rotating, mutateAsync: rotateKeyAsync } = useRotateKey()

const revokeTarget = ref<Key | null>(null)
const revokeOpen = ref(false)
const { isLoading: revoking, mutateAsync: revokeKeyAsync } = useRevokeKey()

const secretOpen = ref(false)
const secretAlias = ref('')
const secretValue = ref('')

const groups = computed(() => Object.keys(config.value?.model_access_groups ?? {}).sort())
const spent = computed(() => Number(summary.value?.current_period?.spent_usd ?? 0))
const budget = computed(() => Number(user.value?.budget_usd ?? 0))
const remaining = computed(() => Math.max(0, budget.value - spent.value))
const usedPercent = computed(() => percent(spent.value, budget.value))

function load(): void {
  refetchUser()
  refetchUsage()
  refetchKeys()
  refetchPolicies()
  refetchConfig()
}

function openEdit(): void {
  if (!user.value) return
  resetUpdate()
  editForm.enabled = user.value.enabled
  editForm.budget_usd = trimUSD(user.value.budget_usd)
  editForm.model_access_group = user.value.model_access_group
  editOpen.value = true
}

async function saveEdit(): Promise<void> {
  if (!user.value || saving.value) return
  resetUpdate()
  try {
    await updateUserAsync({
      id: user.value.id,
      body: {
        enabled: editForm.enabled,
        budget_usd: editForm.budget_usd,
        ...(editForm.model_access_group ? { model_access_group: editForm.model_access_group } : {}),
      },
    })
    editOpen.value = false
  } catch {
    // The mutation error is rendered from `editError`.
  }
}

function openCreateKey(): void {
  resetCreateKey()
  keyForm.alias = ''
  keyForm.expires = ''
  createKeyOpen.value = true
}

async function createKey(): Promise<void> {
  if (!user.value || creatingKey.value) return
  resetCreateKey()
  try {
    const result = await createKeyAsync({
      id: user.value.id,
      body: {
        ...(keyForm.alias.trim() ? { alias: keyForm.alias.trim() } : {}),
        ...(keyForm.expires ? { expires: fromDateTimeLocal(keyForm.expires) } : {}),
      },
    })
    createKeyOpen.value = false
    secretAlias.value = result.key.alias
    secretValue.value = result.secret
    secretOpen.value = true
  } catch {
    // The mutation error is rendered from `keyError`.
  }
}

function askRotate(key: Key): void {
  rotateTarget.value = key
  rotateOpen.value = true
}

async function rotateKey(): Promise<void> {
  if (!user.value || !rotateTarget.value || rotating.value) return
  try {
    const result = await rotateKeyAsync({ id: user.value.id, alias: rotateTarget.value.alias })
    rotateOpen.value = false
    secretAlias.value = result.key.alias
    secretValue.value = result.secret
    secretOpen.value = true
  } catch (err) {
    toast('Could not rotate key', messageOf(err, 'Unable to rotate the key.'), 'error')
  }
}

function askRevoke(key: Key): void {
  revokeTarget.value = key
  revokeOpen.value = true
}

async function revokeKey(): Promise<void> {
  if (!user.value || !revokeTarget.value || revoking.value) return
  try {
    await revokeKeyAsync({ id: user.value.id, alias: revokeTarget.value.alias })
    revokeOpen.value = false
  } catch (err) {
    toast('Could not revoke key', messageOf(err, 'Unable to revoke the key.'), 'error')
  }
}
</script>

<template>
  <div>
    <div class="mb-4">
      <RouterLink :to="{ name: 'users' }" class="inline-flex items-center gap-1.5 text-xs font-medium text-zinc-500 transition hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200">
        <ArrowLeft class="h-3.5 w-3.5" />
        All users
      </RouterLink>
    </div>

    <div v-if="loading" class="card flex items-center justify-center py-24">
      <LoadingSpinner label="Loading user" />
    </div>

    <div v-else-if="error" class="card px-6 py-16 text-center">
      <p class="text-sm font-medium text-zinc-900 dark:text-zinc-100">{{ error }}</p>
      <RouterLink :to="{ name: 'users' }" class="btn btn-secondary mt-4">Back to users</RouterLink>
    </div>

    <template v-else-if="user">
      <PageHeader :title="user.id" description="Recurring budget, model access, keys, and usage for this user." eyebrow="User detail">
        <template #actions>
          <StatusBadge :status="user.enabled ? 'enabled' : 'disabled'" />
          <button class="btn btn-secondary" :disabled="loading" @click="load">
            <RefreshCw class="h-4 w-4" />
            Refresh
          </button>
          <button class="btn btn-primary" @click="openEdit">
            <Pencil class="h-4 w-4" />
            Edit user
          </button>
        </template>
      </PageHeader>

      <div class="mb-4 flex flex-wrap items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
        <span class="inline-flex items-center gap-1.5 rounded-md bg-zinc-100 px-2 py-1 font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
          Group · {{ user.model_access_group }}
        </span>
        <span class="inline-flex items-center gap-1.5 rounded-md bg-zinc-100 px-2 py-1 font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
          <CalendarClock class="h-3.5 w-3.5" />
          {{ user.cadence }} · anchor {{ formatDate(user.anchor_unix) }}
        </span>
        <CopyButton :value="user.id" label="Copy user ID" :compact="false" />
      </div>

      <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Budget" :value="formatUSD(user.budget_usd)" :hint="`${user.cadence} recurring limit`" :icon="Wallet" tone="indigo" />
        <StatCard label="Spent this period" :value="formatUSD(summary?.current_period?.spent_usd ?? '0')" :hint="summary?.current_period ? `${formatDate(summary.current_period.start_unix)} – ${formatDate(summary.current_period.end_unix)}` : 'No active period'" :icon="CircleDollarSign" tone="amber" />
        <StatCard label="Remaining" :value="formatUSD(remaining)" hint="Known settled spending only" :icon="Wallet" tone="emerald" />
        <StatCard label="Accounting backlog" :value="(summary?.pending_requests ?? 0) + (summary?.unresolved_requests ?? 0)" :hint="`${summary?.pending_requests ?? 0} pending · ${summary?.unresolved_requests ?? 0} unresolved`" :icon="ShieldAlert" :tone="(summary?.unresolved_requests ?? 0) > 0 ? 'red' : (summary?.pending_requests ?? 0) > 0 ? 'amber' : 'emerald'" />
      </div>

      <div class="card mt-4 p-4 sm:p-5">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <p class="text-[11px] font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">Current period</p>
            <p class="mt-1 text-sm text-zinc-700 dark:text-zinc-300">
              <span class="font-semibold text-zinc-900 dark:text-zinc-100">{{ formatUSD(summary?.current_period?.spent_usd ?? '0') }}</span>
              of {{ formatUSD(user.budget_usd) }}
            </p>
          </div>
          <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ usedPercent.toFixed(0) }}% used</p>
        </div>
        <div class="mt-3 h-2 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
          <div
            class="h-full rounded-full transition-all"
            :class="usedPercent >= 100 ? 'bg-red-500' : usedPercent >= 80 ? 'bg-amber-500' : 'bg-indigo-500'"
            :style="{ width: `${Math.max(usedPercent, usedPercent > 0 ? 2 : 0)}%` }"
          />
        </div>
        <p v-if="summary?.current_period" class="mt-2 text-[11px] text-zinc-400 dark:text-zinc-500">
          {{ formatDate(summary.current_period.start_unix) }} – {{ formatDate(summary.current_period.end_unix) }}
        </p>
      </div>

      <TabsRoot v-model="activeTab" class="mt-5">
        <TabsList class="flex gap-1 rounded-lg border border-zinc-200 bg-zinc-100/70 p-1 dark:border-zinc-800 dark:bg-zinc-900">
          <TabsTrigger
            value="keys"
            class="rounded-md px-3 py-1.5 text-xs font-medium text-zinc-500 transition data-[state=active]:bg-white data-[state=active]:text-zinc-900 data-[state=active]:shadow-sm dark:text-zinc-400 dark:data-[state=active]:bg-zinc-800 dark:data-[state=active]:text-zinc-100"
          >
            Keys <span class="ml-1 text-[10px] text-zinc-400">{{ keys.length }}</span>
          </TabsTrigger>
          <TabsTrigger
            value="usage"
            class="rounded-md px-3 py-1.5 text-xs font-medium text-zinc-500 transition data-[state=active]:bg-white data-[state=active]:text-zinc-900 data-[state=active]:shadow-sm dark:text-zinc-400 dark:data-[state=active]:bg-zinc-800 dark:data-[state=active]:text-zinc-100"
          >
            Usage
          </TabsTrigger>
          <TabsTrigger
            value="policies"
            class="rounded-md px-3 py-1.5 text-xs font-medium text-zinc-500 transition data-[state=active]:bg-white data-[state=active]:text-zinc-900 data-[state=active]:shadow-sm dark:text-zinc-400 dark:data-[state=active]:bg-zinc-800 dark:data-[state=active]:text-zinc-100"
          >
            Policies <span class="ml-1 text-[10px] text-zinc-400">{{ policies.length }}</span>
          </TabsTrigger>
        </TabsList>

        <TabsContent value="keys" class="mt-3 focus:outline-none">
          <section class="card overflow-hidden">
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
              <div>
                <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Keys</h2>
                <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Secrets are returned once when created or rotated.</p>
              </div>
              <button class="btn btn-primary btn-sm" @click="openCreateKey">
                <Plus class="h-3.5 w-3.5" />
                Create key
              </button>
            </div>
            <div v-if="keys.length === 0" class="px-4">
              <EmptyState title="No keys yet" description="Create a key to let this user call the inference API." :icon="KeyRound">
                <button class="btn btn-primary" @click="openCreateKey">
                  <Plus class="h-4 w-4" />
                  Create key
                </button>
              </EmptyState>
            </div>
            <div v-else class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr class="border-b border-zinc-200 dark:border-zinc-800">
                    <th class="table-head">Alias</th>
                    <th class="table-head">Prefix</th>
                    <th class="table-head">Status</th>
                    <th class="table-head">Expires</th>
                    <th class="table-head">Created</th>
                    <th class="table-head">Rotated</th>
                    <th class="table-head text-right">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="key in keys" :key="key.id" class="border-b border-zinc-100 last:border-0 dark:border-zinc-800/70">
                    <td class="table-cell">
                      <p class="mono font-medium text-zinc-800 dark:text-zinc-200">{{ key.alias }}</p>
                      <p class="mt-0.5 text-[11px] text-zinc-400 dark:text-zinc-500">{{ key.id }}</p>
                    </td>
                    <td class="table-cell mono text-zinc-500 dark:text-zinc-400">{{ key.prefix }}…</td>
                    <td class="table-cell"><StatusBadge :status="key.status" /></td>
                    <td class="table-cell text-xs text-zinc-500 dark:text-zinc-400">{{ key.expires_unix ? formatDate(key.expires_unix) : 'Never' }}</td>
                    <td class="table-cell text-xs text-zinc-500 dark:text-zinc-400">{{ formatDate(key.created_unix) }}</td>
                    <td class="table-cell text-xs text-zinc-500 dark:text-zinc-400">{{ key.rotated_unix ? formatDate(key.rotated_unix) : '—' }}</td>
                    <td class="table-cell">
                      <div class="flex items-center justify-end gap-1">
                        <button class="btn btn-ghost btn-sm" title="Rotate key" @click="askRotate(key)">
                          <RotateCw class="h-3.5 w-3.5" />
                          Rotate
                        </button>
                        <button
                          class="btn btn-ghost btn-sm text-red-600 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-500/10"
                          title="Revoke key"
                          :disabled="key.revoked"
                          @click="askRevoke(key)"
                        >
                          <Trash2 class="h-3.5 w-3.5" />
                          Revoke
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </TabsContent>

        <TabsContent value="usage" class="mt-3 focus:outline-none">
          <section class="card overflow-hidden">
            <div class="border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
              <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Period history</h2>
              <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Latest 100 recurring periods for this user.</p>
            </div>
            <div v-if="(summary?.periods.length ?? 0) === 0" class="px-4">
              <EmptyState title="No usage periods yet" description="A period is created when the user makes their first admitted request." :icon="CircleDollarSign" />
            </div>
            <div v-else class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr class="border-b border-zinc-200 dark:border-zinc-800">
                    <th class="table-head">Period</th>
                    <th class="table-head">Starts</th>
                    <th class="table-head">Ends</th>
                    <th class="table-head text-right">Spent</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="period in summary?.periods" :key="period.id" class="border-b border-zinc-100 last:border-0 dark:border-zinc-800/70">
                    <td class="table-cell mono text-zinc-500 dark:text-zinc-400">{{ period.id }}</td>
                    <td class="table-cell text-xs text-zinc-600 dark:text-zinc-400">{{ formatDate(period.start_unix) }}</td>
                    <td class="table-cell text-xs text-zinc-600 dark:text-zinc-400">{{ formatDate(period.end_unix) }}</td>
                    <td class="table-cell text-right font-medium text-zinc-800 dark:text-zinc-200">{{ formatUSD(period.spent_usd) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </TabsContent>

        <TabsContent value="policies" class="mt-3 focus:outline-none">
          <section class="card overflow-hidden">
            <div class="border-b border-zinc-200 px-4 py-3.5 dark:border-zinc-800">
              <h2 class="text-sm font-semibold text-zinc-900 dark:text-zinc-100">Budget policies</h2>
              <p class="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">Transitions retain previous policies. PATCH edits the active limit in place.</p>
            </div>
            <div v-if="policies.length === 0" class="px-4">
              <EmptyState title="No policies" description="This user has no retained budget policies." :icon="Wallet" />
            </div>
            <div v-else class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr class="border-b border-zinc-200 dark:border-zinc-800">
                    <th class="table-head">Policy</th>
                    <th class="table-head">Budget</th>
                    <th class="table-head">Cadence</th>
                    <th class="table-head">Anchor</th>
                    <th class="table-head">Created</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="policy in policies" :key="policy.id" class="border-b border-zinc-100 last:border-0 dark:border-zinc-800/70">
                    <td class="table-cell mono text-zinc-600 dark:text-zinc-400">{{ policy.id }}</td>
                    <td class="table-cell font-medium text-zinc-800 dark:text-zinc-200">{{ formatUSD(policy.budget_usd) }}</td>
                    <td class="table-cell capitalize text-zinc-600 dark:text-zinc-400">{{ policy.cadence }}</td>
                    <td class="table-cell text-xs text-zinc-500 dark:text-zinc-400">{{ formatDate(policy.anchor_unix) }}</td>
                    <td class="table-cell text-xs text-zinc-500 dark:text-zinc-400">{{ formatDate(policy.created_unix) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </TabsContent>
      </TabsRoot>
    </template>

    <Modal v-model:open="editOpen" title="Edit user" description="PATCH updates the active policy in place and preserves spending." size="lg">
      <form id="edit-user-form" class="space-y-4" @submit.prevent="saveEdit">
        <div class="flex items-center justify-between rounded-lg border border-zinc-200 px-3.5 py-3 dark:border-zinc-800">
          <div>
            <p class="text-sm font-medium text-zinc-900 dark:text-zinc-100">Inference enabled</p>
            <p class="text-xs text-zinc-500 dark:text-zinc-400">Disabled users cannot admit new requests.</p>
          </div>
          <SwitchRoot
            v-model="editForm.enabled"
            aria-label="Inference enabled"
            class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full bg-zinc-200 transition data-[state=checked]:bg-indigo-600 dark:bg-zinc-700"
          >
            <SwitchThumb class="block h-5 w-5 translate-x-0.5 rounded-full bg-white shadow transition data-[state=checked]:translate-x-[22px]" />
          </SwitchRoot>
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="label" for="edit-budget">Budget (USD)</label>
            <input id="edit-budget" v-model="editForm.budget_usd" class="input" inputmode="decimal" required />
          </div>
          <div>
            <label class="label" for="edit-group">Model access group</label>
            <select id="edit-group" v-model="editForm.model_access_group" class="input">
              <option v-for="group in groups" :key="group" :value="group">{{ group }}</option>
            </select>
          </div>
        </div>
        <div v-if="editError" class="rounded-lg border border-red-200 bg-red-50 px-3.5 py-3 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300">
          {{ editError }}
        </div>
      </form>
      <template #footer>
        <button class="btn btn-secondary" :disabled="saving" @click="editOpen = false">Cancel</button>
        <button form="edit-user-form" type="submit" class="btn btn-primary" :disabled="saving">
          {{ saving ? 'Saving…' : 'Save changes' }}
        </button>
      </template>
    </Modal>

    <Modal v-model:open="createKeyOpen" title="Create key" description="The alias is unique within this user. The secret is returned once." size="md">
      <form id="create-key-form" class="space-y-4" @submit.prevent="createKey">
        <div>
          <label class="label" for="key-alias">Alias <span class="normal-case text-zinc-400">(optional)</span></label>
          <input id="key-alias" v-model="keyForm.alias" class="input mono" maxlength="256" placeholder="Auto-generated" />
        </div>
        <div>
          <label class="label" for="key-expires">Expires <span class="normal-case text-zinc-400">(optional)</span></label>
          <input id="key-expires" v-model="keyForm.expires" class="input" type="datetime-local" />
          <p class="hint">Leave blank for a non-expiring credential. Must be in the future.</p>
        </div>
        <div v-if="keyError" class="rounded-lg border border-red-200 bg-red-50 px-3.5 py-3 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300">
          {{ keyError }}
        </div>
      </form>
      <template #footer>
        <button class="btn btn-secondary" :disabled="creatingKey" @click="createKeyOpen = false">Cancel</button>
        <button form="create-key-form" type="submit" class="btn btn-primary" :disabled="creatingKey">
          {{ creatingKey ? 'Creating…' : 'Create key' }}
        </button>
      </template>
    </Modal>

    <ConfirmDialog
      v-model:open="rotateOpen"
      title="Rotate key?"
      :description="`A new secret will replace the credential for ${rotateTarget?.alias ?? 'this alias'}. The previous secret stops working after commit.`"
      confirm-label="Rotate key"
      tone="primary"
      :loading="rotating"
      @confirm="rotateKey"
    >
      <div class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs leading-relaxed text-amber-800 dark:border-amber-900/60 dark:bg-amber-500/10 dark:text-amber-200">
        Save the new secret when it is shown. It cannot be retrieved later.
      </div>
    </ConfirmDialog>

    <ConfirmDialog
      v-model:open="revokeOpen"
      title="Revoke key?"
      :description="`New requests using ${revokeTarget?.alias ?? 'this alias'} will be rejected. In-flight requests may finish and settle.`"
      confirm-label="Revoke key"
      :loading="revoking"
      @confirm="revokeKey"
    />

    <SecretDialog v-model:open="secretOpen" :alias="secretAlias" :secret="secretValue" />
  </div>
</template>
