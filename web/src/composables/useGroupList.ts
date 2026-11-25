import { onBeforeUnmount, ref, watch } from 'vue'
import * as cache from '../lib/cardCache'
import type { CardBrief, CardStatus } from '../lib/types'

/**
 * The cards of one box of the collapsed map, a page at a time. `more` reads
 * the next page. A live change in the project reads the pages already shown
 * again, so the list follows the cards without jumping back to the top.
 */
export function useGroupList(slug: () => string, key: () => string, statuses: () => CardStatus[]) {
  const items = ref<CardBrief[]>([])
  const next = ref('')
  const loading = ref(false)
  const error = ref<unknown>(null)
  let generation = 0

  async function read(rows: number): Promise<void> {
    const mine = ++generation
    const project = slug()
    const g = { key: key(), statuses: statuses() }
    loading.value = true
    error.value = null
    try {
      const out: CardBrief[] = []
      let cursor = ''
      do {
        const page = await cache.readGroupPage(project, g, cursor, cache.PAGE)
        out.push(...page.items)
        cursor = page.next
      } while (cursor && out.length < rows)
      if (mine !== generation) return
      items.value = out
      next.value = cursor
    } catch (e) {
      if (mine === generation) error.value = e
    } finally {
      if (mine === generation) loading.value = false
    }
  }

  async function more(): Promise<void> {
    if (!next.value || loading.value) return
    const mine = ++generation
    const project = slug()
    loading.value = true
    try {
      const page = await cache.readGroupPage(project, { key: key(), statuses: statuses() }, next.value, cache.PAGE)
      if (mine !== generation) return
      items.value = [...items.value, ...page.items]
      next.value = page.next
    } catch (e) {
      if (mine === generation) error.value = e
    } finally {
      if (mine === generation) loading.value = false
    }
  }

  const restart = (): Promise<void> => {
    items.value = []
    next.value = ''
    return read(cache.PAGE)
  }

  watch(() => [slug(), key(), statuses().join()], restart, { immediate: true })
  // Reading again as far as the list had come; the shown rows stay until the new ones land.
  watch(() => cache.versionOf(slug()), () => void read(Math.max(items.value.length, cache.PAGE)))
  onBeforeUnmount(() => generation++)

  return { items, next, loading, error, more, retry: restart }
}
