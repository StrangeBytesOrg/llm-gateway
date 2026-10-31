import { ref } from 'vue'

export type Theme = 'light' | 'dark' | 'system'

const storageKey = 'gateway.theme'
const stored = localStorage.getItem(storageKey)
export const theme = ref<Theme>(stored === 'light' || stored === 'dark' ? stored : 'system')
const media = window.matchMedia('(prefers-color-scheme: dark)')

function apply(): void {
  const dark = theme.value === 'dark' || (theme.value === 'system' && media.matches)
  document.documentElement.classList.toggle('dark', dark)
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? '#09090b' : '#fafafa')
}

export function setTheme(next: Theme): void {
  theme.value = next
  localStorage.setItem(storageKey, next)
  apply()
}

export function initTheme(): void {
  apply()
  media.addEventListener('change', () => {
    if (theme.value === 'system') apply()
  })
}
