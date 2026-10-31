export function formatUSD(value: string | number | null | undefined, maximumFractionDigits = 4): string {
  if (value === null || value === undefined || value === '') return '—'
  const number = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(number)) return String(value)
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits,
  }).format(number)
}

export function trimUSD(value: string): string {
  if (!value.includes('.')) return value
  return value.replace(/0+$/, '').replace(/\.$/, '')
}

export function formatDate(unix: number | null | undefined): string {
  if (!unix) return '—'
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(unix * 1000))
}

export function formatRelative(unix: number | null | undefined): string {
  if (!unix) return '—'
  const delta = unix - Math.floor(Date.now() / 1000)
  const absolute = Math.abs(delta)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  if (absolute < 60) return formatter.format(delta, 'second')
  if (absolute < 3600) return formatter.format(Math.round(delta / 60), 'minute')
  if (absolute < 86400) return formatter.format(Math.round(delta / 3600), 'hour')
  return formatter.format(Math.round(delta / 86400), 'day')
}

export function formatDuration(start: number | null | undefined, end: number | null | undefined): string {
  if (!start || !end || end < start) return '—'
  const seconds = end - start
  if (seconds < 1) return `${Math.round(seconds * 1000)} ms`
  if (seconds < 60) return `${seconds.toFixed(1)} s`
  return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`
}

export function truncate(value: string, length = 48): string {
  return value.length > length ? `${value.slice(0, length - 1)}…` : value
}

export function percent(spent: string | number, budget: string | number): number {
  const spentNumber = Number(spent)
  const budgetNumber = Number(budget)
  if (!Number.isFinite(spentNumber) || !Number.isFinite(budgetNumber) || budgetNumber <= 0) return 0
  return Math.min(100, Math.max(0, (spentNumber / budgetNumber) * 100))
}

export function toDateTimeLocal(unix: number): string {
  const date = new Date(unix * 1000)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function fromDateTimeLocal(value: string): string {
  return new Date(value).toISOString()
}
