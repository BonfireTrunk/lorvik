import { reactive } from 'vue'
import { readStored, writeStored } from './storage'

const PREFIX = 'lorvik.inbox.'

/** Unread count from X-Lorvik-Unread: 0..101, where 101 means more than 100. */
export const unread = reactive<{ count: number; orgId: string; cursor: string }>({
  count: 0,
  orgId: '',
  cursor: '0',
})

export function unreadLabel(n: number): string {
  return n > 100 ? '100+' : String(n)
}

/** Selects the org whose stored cursor is sent on requests. */
export function useOrg(orgId: string): void {
  if (unread.orgId === orgId) return
  unread.orgId = orgId
  unread.cursor = readStored(PREFIX + orgId) || '0'
}

/** Stores the cursor returned by GET /inbox. */
export function storeCursor(cursor: string): void {
  if (!unread.orgId || !cursor) return
  unread.cursor = cursor
  writeStored(PREFIX + unread.orgId, cursor)
}

export function noteUnread(header: string | null): void {
  if (header === null) return
  const n = Number(header)
  if (Number.isFinite(n)) unread.count = Math.max(0, n)
}

export function resetInbox(): void {
  unread.count = 0
  unread.orgId = ''
  unread.cursor = '0'
}
