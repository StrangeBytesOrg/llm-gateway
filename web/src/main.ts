import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { PiniaColada } from '@pinia/colada'
import App from './App.vue'
import router from './router'
import { setUnauthorizedHandler } from './api/client'
import { initTheme } from './lib/theme'
import './style.css'

initTheme()

const app = createApp(App)
app.use(createPinia())
app.use(PiniaColada, {
  queryOptions: {
    staleTime: 15_000,
    refetchOnWindowFocus: false,
  },
})
app.use(router)

setUnauthorizedHandler(() => {
  const current = router.currentRoute.value
  if (current.name === 'login') return
  const redirect = encodeURIComponent(current.fullPath)
  window.location.assign(`/login?redirect=${redirect}`)
})

app.mount('#app')
