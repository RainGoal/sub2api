// Date-only bounds retain the legacy inclusive-calendar-day semantics.
// Precise bounds are RFC3339 instants and use [start, end).
export const formatRangeInput = (value: string, end = false): string => {
  if (!value) return ''
  const dateOnly = /^\d{4}-\d{2}-\d{2}$/.test(value)
  const date = new Date(dateOnly ? `${value}T00:00:00` : value)
  if (dateOnly && end) date.setDate(date.getDate() + 1)
  if (!Number.isFinite(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

export const getLast24HoursRange = () => {
  const end = new Date(Math.floor(Date.now() / 1000) * 1000)
  return {
    start: new Date(end.getTime() - 24 * 60 * 60 * 1000).toISOString(),
    end: end.toISOString(),
  }
}

export const getUsageRangeGranularity = (start: string, end: string): 'hour' | 'day' => {
  const startMs = new Date(start.length === 10 ? `${start}T00:00:00` : start).getTime()
  const endMs = new Date(end.length === 10 ? `${end}T00:00:00` : end).getTime()
  return endMs - startMs <= 24 * 60 * 60 * 1000 ? 'hour' : 'day'
}

export const toUsageISOString = (value: string | undefined, end = false): string | undefined => {
  if (!value) return undefined
  if (value.length !== 10) return new Date(value).toISOString()
  const date = new Date(`${value}T00:00:00`)
  if (end) date.setDate(date.getDate() + 1)
  return date.toISOString()
}
