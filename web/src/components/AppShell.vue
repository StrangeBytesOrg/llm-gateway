<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useQuery } from '@pinia/colada'
import {
  Activity,
  ChevronRight,
  LayoutDashboard,
  LogOut,
  Menu,
  Monitor,
  Moon,
  ScrollText,
  Settings,
  Sun,
  Users,
  X,
} from '@lucide/vue'
import {
  DialogClose,
  DialogContent,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { useLogout, sessionQuery } from '../lib/auth'
import { setTheme, theme } from '../lib/theme'

const route = useRoute()
const router = useRouter()
const mobileOpen = ref(false)
const { data: session } = useQuery(sessionQuery)
const logout = useLogout()
const { isLoading: signingOut, mutateAsync: logoutAsync } = logout

const nav = [
  { name: 'overview', label: 'Overview', icon: LayoutDashboard },
  { name: 'users', label: 'Users', icon: Users },
  { name: 'requests', label: 'Requests', icon: Activity },
  { name: 'audit', label: 'Audit log', icon: ScrollText },
  { name: 'settings', label: 'Settings', icon: Settings },
]

const activeName = computed(() => {
  if (route.name === 'user') return 'users'
  if (route.name === 'request') return 'requests'
  return String(route.name ?? '')
})

const themeIcon = computed(() => {
  if (theme.value === 'light') return Sun
  if (theme.value === 'dark') return Moon
  return Monitor
})

const themeLabel = computed(() => {
  if (theme.value === 'light') return 'Light theme'
  if (theme.value === 'dark') return 'Dark theme'
  return 'System theme'
})

const initial = computed(() => (session.value?.username || 'A').slice(0, 1).toUpperCase())

function cycleTheme(): void {
  setTheme(theme.value === 'light' ? 'dark' : theme.value === 'dark' ? 'system' : 'light')
}

async function signOut(): Promise<void> {
  if (signingOut.value) return
  try {
    await logoutAsync()
  } catch {
    // The logout mutation clears local credentials even if the request failed.
  } finally {
    router.push({ name: 'login' })
  }
}

function closeMobile(): void {
  mobileOpen.value = false
}
</script>

<template>
  <div class="min-h-full bg-zinc-50 dark:bg-zinc-950">
    <aside
      class="fixed inset-y-0 left-0 z-30 hidden w-64 flex-col border-r border-zinc-200 bg-white/90 backdrop-blur lg:flex dark:border-zinc-800 dark:bg-zinc-950/90"
    >
      <div class="flex h-16 items-center gap-3 border-b border-zinc-200 px-5 dark:border-zinc-800">
        <div class="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-600 text-white shadow-sm">
          <span class="text-sm font-bold tracking-tight">G</span>
        </div>
        <div class="min-w-0">
          <p class="truncate text-sm font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">LLM Gateway</p>
          <p class="text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Administration</p>
        </div>
      </div>

      <nav class="flex-1 space-y-1 px-3 py-4">
        <RouterLink
          v-for="item in nav"
          :key="item.name"
          :to="{ name: item.name }"
          class="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition"
          :class="
            activeName === item.name
              ? 'bg-indigo-50 text-indigo-700 dark:bg-indigo-500/10 dark:text-indigo-300'
              : 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-900 dark:hover:text-zinc-100'
          "
        >
          <component
            :is="item.icon"
            class="h-4 w-4"
            :class="activeName === item.name ? 'text-indigo-600 dark:text-indigo-400' : 'text-zinc-400 group-hover:text-zinc-600 dark:group-hover:text-zinc-300'"
          />
          {{ item.label }}
          <ChevronRight v-if="activeName === item.name" class="ml-auto h-3.5 w-3.5 opacity-60" />
        </RouterLink>
      </nav>

      <div class="border-t border-zinc-200 p-3 dark:border-zinc-800">
        <div class="flex items-center gap-3 rounded-lg px-2 py-2">
          <div
            class="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-zinc-100 text-sm font-semibold text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
          >
            {{ initial }}
          </div>
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">{{ session?.username || 'Admin' }}</p>
            <p class="text-[11px] text-zinc-500 dark:text-zinc-400">
              {{ session?.kind === 'token' ? 'Admin token' : 'Browser session' }}
            </p>
          </div>
          <button
            class="rounded-lg p-2 text-zinc-400 transition hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
            title="Sign out"
            :disabled="signingOut"
            @click="signOut"
          >
            <LogOut class="h-4 w-4" />
          </button>
        </div>
        <div class="mt-1 flex items-center justify-between px-2">
          <button
            class="inline-flex items-center gap-1.5 rounded-md px-1.5 py-1 text-[11px] font-medium text-zinc-500 transition hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200"
            :title="themeLabel"
            @click="cycleTheme"
          >
            <component :is="themeIcon" class="h-3.5 w-3.5" />
            {{ themeLabel }}
          </button>
          <a
            href="/openapi.json"
            target="_blank"
            rel="noreferrer"
            class="text-[11px] font-medium text-zinc-400 transition hover:text-indigo-600 dark:hover:text-indigo-400"
          >
            API contract
          </a>
        </div>
      </div>
    </aside>

    <div class="lg:pl-64">
      <header
        class="sticky top-0 z-20 flex h-16 items-center gap-3 border-b border-zinc-200 bg-white/80 px-4 backdrop-blur lg:hidden dark:border-zinc-800 dark:bg-zinc-950/80"
      >
        <button
          class="rounded-lg p-2 text-zinc-600 transition hover:bg-zinc-100 dark:text-zinc-300 dark:hover:bg-zinc-800"
          aria-label="Open navigation"
          @click="mobileOpen = true"
        >
          <Menu class="h-5 w-5" />
        </button>
        <div class="flex min-w-0 items-center gap-2">
          <div class="flex h-7 w-7 items-center justify-center rounded-lg bg-indigo-600 text-[11px] font-bold text-white">
            G
          </div>
          <span class="truncate text-sm font-semibold text-zinc-900 dark:text-zinc-100">LLM Gateway</span>
        </div>
        <button
          class="ml-auto rounded-lg p-2 text-zinc-500 transition hover:bg-zinc-100 dark:hover:bg-zinc-800"
          :title="themeLabel"
          @click="cycleTheme"
        >
          <component :is="themeIcon" class="h-4 w-4" />
        </button>
      </header>

      <main class="mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
        <RouterView />
      </main>
    </div>

    <DialogRoot v-model:open="mobileOpen">
      <DialogPortal>
        <DialogOverlay class="fixed inset-0 z-40 bg-zinc-950/40 backdrop-blur-sm animate-overlay-in" />
        <DialogContent
          class="fixed inset-y-0 left-0 z-50 flex w-72 flex-col border-r border-zinc-200 bg-white shadow-2xl animate-content-in dark:border-zinc-800 dark:bg-zinc-950"
        >
          <DialogTitle class="sr-only">Navigation</DialogTitle>
          <div class="flex h-16 items-center gap-3 border-b border-zinc-200 px-5 dark:border-zinc-800">
            <div class="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-600 text-sm font-bold text-white">G</div>
            <div>
              <p class="text-sm font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">LLM Gateway</p>
              <p class="text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:text-zinc-500">Administration</p>
            </div>
            <DialogClose
              class="ml-auto rounded-lg p-1.5 text-zinc-400 transition hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
              aria-label="Close navigation"
            >
              <X class="h-4 w-4" />
            </DialogClose>
          </div>
          <nav class="flex-1 space-y-1 px-3 py-4">
            <RouterLink
              v-for="item in nav"
              :key="item.name"
              :to="{ name: item.name }"
              class="flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition"
              :class="
                activeName === item.name
                  ? 'bg-indigo-50 text-indigo-700 dark:bg-indigo-500/10 dark:text-indigo-300'
                  : 'text-zinc-600 hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-900 dark:hover:text-zinc-100'
              "
              @click="closeMobile"
            >
              <component :is="item.icon" class="h-4 w-4" />
              {{ item.label }}
            </RouterLink>
          </nav>
          <div class="border-t border-zinc-200 p-4 dark:border-zinc-800">
            <div class="mb-3 flex items-center gap-3">
              <div class="flex h-9 w-9 items-center justify-center rounded-full bg-zinc-100 text-sm font-semibold text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
                {{ initial }}
              </div>
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-zinc-900 dark:text-zinc-100">{{ session?.username || 'Admin' }}</p>
                <p class="text-[11px] text-zinc-500 dark:text-zinc-400">{{ session?.kind === 'token' ? 'Admin token' : 'Browser session' }}</p>
              </div>
            </div>
            <button class="btn btn-secondary w-full" :disabled="signingOut" @click="signOut">
              <LogOut class="h-4 w-4" />
              Sign out
            </button>
          </div>
        </DialogContent>
      </DialogPortal>
    </DialogRoot>
  </div>
</template>
