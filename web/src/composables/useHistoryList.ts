import { computed, ref, type Ref } from 'vue'
import type { HistoryQuery } from '../lib/types'

const PAGE = 50

/**
 * A newest-first comment or revision list, read a page at a time. These lists
 * carry no cursor or count, so one row more than a page is asked for: a full
 * page then knows whether older rows exist. `refresh()` rereads the newest page
 * and keeps the older rows already loaded, so an event does not fold the list
 * back to one page.
 */
export function useHistoryList<T>(fetch: (q: HistoryQuery) => Promise<T[]>, key: (t: T) => string) {
  const items = ref<T[]>([]) as Ref<T[]>
  // The before value of the next older page; empty when the oldest is loaded.
  const older = ref('')
  const loading = ref(false)
  const error = ref<unknown>(null)

  async function page(before?: string): Promise<{ rows: T[]; next: string }> {
    const got = await fetch({ before, limit: PAGE + 1 })
    const rows = got.length > PAGE ? got.slice(0, PAGE) : got
    return { rows, next: got.length > PAGE ? key(rows[rows.length - 1]!) : '' }
  }

  async function run(fn: () => Promise<void>): Promise<void> {
    loading.value = true
    try {
      await fn()
      error.value = null
    } catch (e) {
      error.value = e
    } finally {
      loading.value = false
    }
  }

  function refresh(): Promise<void> {
    return run(async () => {
      const { rows, next } = await page()
      const seam = next ? items.value.findIndex((t) => key(t) === next) : -1
      if (seam >= 0) {
        items.value = [...rows, ...items.value.slice(seam + 1)]
      } else {
        items.value = rows
        older.value = next
      }
    })
  }

  function loadOlder(): Promise<void> {
    return run(async () => {
      const { rows, next } = await page(older.value)
      items.value = [...items.value, ...rows]
      older.value = next
    })
  }

  function reset(): void {
    items.value = []
    older.value = ''
    error.value = null
  }

  return { items, loading, error, hasOlder: computed(() => older.value !== ''), refresh, loadOlder, reset }
}
