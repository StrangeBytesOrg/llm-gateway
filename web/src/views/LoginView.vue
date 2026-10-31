<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowRight, Eye, EyeOff, Loader2, LockKeyhole, ShieldCheck } from '@lucide/vue'
import { ApiError } from '../api/client'
import { useLogin } from '../lib/auth'

const router = useRouter()
const route = useRoute()
const username = ref('')
const password = ref('')
const showPassword = ref(false)
const login = useLogin()
const { isLoading: loading, error: loginError, mutateAsync: loginAsync, reset: resetLogin } = login

const error = computed(() => {
  const err = loginError.value
  if (!err) return ''
  if (err instanceof ApiError) {
    if (err.status === 401) return 'Invalid username or password.'
    if (err.status === 503) {
      return 'Browser login is disabled. Set admin_username and admin_password_env, then restart the gateway.'
    }
    return err.detail
  }
  return 'Unable to sign in.'
})

const redirect = computed(() => {
  const value = route.query.redirect
  return typeof value === 'string' && value.startsWith('/') ? value : '/'
})

async function submit(): Promise<void> {
  if (loading.value) return
  resetLogin()
  try {
    await loginAsync({ username: username.value.trim(), password: password.value })
    await router.replace(redirect.value)
  } catch {
    // The mutation error is rendered from `error`.
  }
}
</script>

<template>
  <div class="relative flex min-h-full items-center justify-center bg-zinc-50 px-4 py-10 dark:bg-zinc-950">
    <div class="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_50%_0%,rgba(79,70,229,0.12),transparent_42%)] dark:bg-[radial-gradient(circle_at_50%_0%,rgba(99,102,241,0.16),transparent_42%)]" />

    <div class="relative w-full max-w-md">
      <div class="mb-8 flex flex-col items-center text-center">
        <div class="flex h-12 w-12 items-center justify-center rounded-2xl bg-indigo-600 text-base font-bold text-white shadow-lg shadow-indigo-600/20">
          G
        </div>
        <h1 class="mt-4 text-lg font-semibold tracking-tight text-zinc-900 dark:text-zinc-100">LLM Gateway</h1>
        <p class="mt-0.5 text-[11px] font-medium uppercase tracking-widest text-zinc-400 dark:text-zinc-500">Administration</p>
        <p class="mt-3 max-w-xs text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">Users, keys, and spend. One quiet place.</p>
      </div>

      <div class="rounded-2xl border border-zinc-200 bg-white p-6 shadow-xl shadow-zinc-950/5 sm:p-8 dark:border-zinc-800 dark:bg-zinc-900 dark:shadow-black/20">
        <div class="mb-7">
          <h2 class="text-2xl font-semibold tracking-tight text-zinc-900 dark:text-zinc-50">Sign in</h2>
          <p class="mt-1.5 text-sm text-zinc-500 dark:text-zinc-400">Use the admin credentials configured on this gateway.</p>
        </div>

        <form class="space-y-4" @submit.prevent="submit">
          <div>
            <label for="username" class="label">Username</label>
            <input
              id="username"
              v-model="username"
              class="input"
              autocomplete="username"
              autofocus
              required
              maxlength="64"
              placeholder="admin"
            />
          </div>
          <div>
            <label for="password" class="label">Password</label>
            <div class="relative">
              <input
                id="password"
                v-model="password"
                class="input pr-11"
                :type="showPassword ? 'text' : 'password'"
                autocomplete="current-password"
                required
                maxlength="256"
                placeholder="••••••••••••"
              />
              <button
                type="button"
                class="absolute right-1 top-1/2 -translate-y-1/2 rounded-md p-2 text-zinc-400 transition hover:bg-zinc-100 hover:text-zinc-700 dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
                :title="showPassword ? 'Hide password' : 'Show password'"
                @click="showPassword = !showPassword"
              >
                <EyeOff v-if="showPassword" class="h-4 w-4" />
                <Eye v-else class="h-4 w-4" />
              </button>
            </div>
          </div>

          <div
            v-if="error"
            class="rounded-lg border border-red-200 bg-red-50 px-3.5 py-3 text-xs leading-relaxed text-red-700 dark:border-red-900/60 dark:bg-red-500/10 dark:text-red-300"
            role="alert"
          >
            {{ error }}
          </div>

          <button type="submit" class="btn btn-primary w-full py-2.5" :disabled="loading">
            <Loader2 v-if="loading" class="h-4 w-4 animate-spin" />
            <LockKeyhole v-else class="h-4 w-4" />
            {{ loading ? 'Signing in…' : 'Sign in' }}
            <ArrowRight v-if="!loading" class="h-4 w-4 opacity-70" />
          </button>
        </form>
      </div>

      <div class="mt-6 flex flex-col items-center gap-1.5 text-[11px] text-zinc-400 dark:text-zinc-500">
        <div class="flex items-center gap-1.5">
          <ShieldCheck class="h-3.5 w-3.5 text-emerald-500" />
          Session tokens expire after 12 hours
        </div>
        <p>Internal use only · Keep the admin listener on a private network</p>
      </div>
    </div>
  </div>
</template>
