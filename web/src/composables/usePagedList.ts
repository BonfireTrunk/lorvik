import { computed, onMounted, ref, watch, type Ref, type WatchSource } from 'vue'
import type { Page } from '../lib/types'

const PAGE = 50

interface PagedListOptions {
  autoLoad?: boolean
  source?: WatchSource | WatchSource[]
}

/**
 * A keyset-paged list. `fetchPage` gets the page size and the cursor (unset for
 * the first page); `load()` replaces the list, `load(true)` appends the next page.
 */
export function usePagedList<T>(fetchPage: (limit: number, cursor?: string) => Promise<Page<T>>, options: PagedListOptions = {}) {
  const items = ref<T[]>([]) as Ref<T[]>
  const next = ref('')
  const remaining = ref(0)
  const loading = ref(true)
  const error = ref<unknown>(null)
  const hasMore = computed(() => next.value !== '' && remaining.value > 0)

  // A page answered after a newer load started belongs to a list no longer shown.
  let latest = 0

  async function load(more = false): Promise<void> {
    const mine = ++latest
    loading.value = true
    try {
      const page = await fetchPage(PAGE, more ? next.value : undefined)
      if (mine !== latest) return
      items.value = more ? [...items.value, ...page.items] : page.items
      next.value = page.next ?? ''
      remaining.value = page.remaining
      error.value = null
    } catch (e) {
      if (mine === latest) error.value = e
    } finally {
      if (mine === latest) loading.value = false
    }
  }

  if (options.autoLoad) onMounted(() => void load())
  if (options.source) watch(options.source, () => void load())

  return { items, remaining, loading, error, hasMore, load }
}
