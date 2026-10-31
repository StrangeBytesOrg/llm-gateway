import { reactive } from 'vue'

export type ToastTone = 'success' | 'error' | 'info'

export interface ToastMessage {
  id: number
  title: string
  description?: string
  tone: ToastTone
}

const messages = reactive<ToastMessage[]>([])
let nextId = 1

export function useToasts(): ToastMessage[] {
  return messages
}

export function dismissToast(id: number): void {
  const index = messages.findIndex((message) => message.id === id)
  if (index >= 0) messages.splice(index, 1)
}

export function toast(title: string, description?: string, tone: ToastTone = 'success'): void {
  const id = nextId++
  messages.push({ id, title, description, tone })
  window.setTimeout(() => dismissToast(id), 5200)
}
