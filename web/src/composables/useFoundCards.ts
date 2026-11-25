import { onBeforeUnmount, ref, watch } from 'vue'
import { STATUS_ORDER } from '../lib/cards'
import * as cache from '../lib/cardCache'
import type { CardBrief, CardStatus } from '../lib/types'

/** How long the search text rests before the server is asked. */
const ASK_DELAY_MS = 300

/**
 * The cards the server finds for a search in every status, so a view can tell
 * the matches it shows from those its filters hide. The statuses the view
 * shows are asked apart from the rest, so hidden matches never crowd out shown
 * ones. One search serves every list built on it; each narrows the cards to
 * what it needs.
 */
export function useFoundCards(slug: () => string, search: () => string, shown: () => CardStatus[]) {
  const found = ref<CardBrief[]>([])
  const error = ref<unknown>(null)
  let timer: ReturnType<typeof setTimeout> | undefined
  let generation = 0

  async function ask(): Promise<void> {
    const mine = ++generation
    const text = search()
    if (!text.trim()) {
      found.value = []
      return
    }
    try {
      const on = shown()
      const cards = await cache.findCards(slug(), text, [on, STATUS_ORDER.filter((s) => !on.includes(s))])
      if (mine !== generation) return
      found.value = cards
      error.value = null
    } catch (e) {
      if (mine === generation) error.value = e
    }
  }

  // The old project's matches never show against the new project.
  watch(slug, () => {
    generation++
    found.value = []
    error.value = null
  })
  watch(
    () => [slug(), search(), shown().join()],
    () => {
      clearTimeout(timer)
      timer = setTimeout(() => void ask(), ASK_DELAY_MS)
    },
    { immediate: true },
  )
  onBeforeUnmount(() => {
    clearTimeout(timer)
    generation++
  })

  return { found, error }
}
