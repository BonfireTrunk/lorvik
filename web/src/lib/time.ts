import { auth } from './auth'

function fmt(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  const d = Math.floor(h / 24)
  if (d < 30) return `${d}d`
  const mo = Math.floor(d / 30)
  if (mo < 12) return `${mo}mo`
  return `${Math.floor(d / 365)}y`
}

/** "3m ago", "in 2h", "just now". */
export function relativeTime(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const diff = Math.round((now - t) / 1000)
  if (Math.abs(diff) < 5) return 'just now'
  return diff < 0 ? `in ${fmt(-diff)}` : `${fmt(diff)} ago`
}

export function timeRemaining(iso: string, now: number = Date.now()): string {
  const s = Math.max(0, Math.round((Date.parse(iso) - now) / 1000))
  return s < 60 ? `${s}s left` : `${Math.floor(s / 60)}m left`
}

/**
 * Absolute time for the viewer: the timezone and locale set on the user's
 * profile when present, otherwise the browser's.
 */
export function absoluteTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const user = auth.me?.user
  try {
    return d.toLocaleString(user?.locale || undefined, { timeZone: user?.timezone || undefined })
  } catch {
    // an unknown zone or locale on the profile: fall back to the browser's
    return d.toLocaleString()
  }
}
