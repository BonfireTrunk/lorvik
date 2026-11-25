import { computed, onBeforeUnmount, ref, watch } from 'vue'

const GO_DELAY_MS = 250

/**
 * Walks the ids a search matches. A new search goes to its first match after
 * a pause; `nextMatch` walks the rest, and `current` is the match it is on,
 * from 0. With `follow` off, the pause and late matches only set the cursor on
 * the first match; `go` runs on `nextMatch` and `show`. `show` makes an id the current match once `present` says it is
 * drawn, even when the text does not match it by itself: a card the server
 * found by stem, or one a filter had hidden.
 */
export function useSearchCursor(search: () => string, found: () => string[], present: (id: string) => boolean, go: (id: string) => void, follow = true) {
  const shown = ref('')
  const matchList = computed(() => [...new Set([...found(), ...(shown.value && present(shown.value) ? [shown.value] : [])])])

  let timer: ReturnType<typeof setTimeout> | undefined
  // Whether this search has been moved to a match; late matches (inside closed boxes) arrive after the pause, and the first of them moves it then.
  let moved = false
  // A shown id that was not drawn yet when it was asked for.
  let waiting = false
  const cursor = ref(0)
  const current = computed(() => (matchList.value.length ? cursor.value % matchList.value.length : 0))
  let paused = false
  watch(search, () => {
    clearTimeout(timer)
    shown.value = ''
    waiting = false
    cursor.value = 0
    moved = false
    paused = false
    timer = setTimeout(() => {
      paused = true
      if (matchList.value[0]) {
        moved = true
        if (follow) go(matchList.value[0])
      }
    }, GO_DELAY_MS)
  })
  watch(matchList, (ids) => {
    if (waiting) {
      const at = ids.indexOf(shown.value)
      if (at < 0) return
      waiting = false
      moved = true
      cursor.value = at
      go(shown.value)
      return
    }
    if (!paused || moved || !ids[0]) return
    moved = true
    if (follow) go(ids[0])
  })

  function show(id: string): void {
    shown.value = id
    const at = matchList.value.indexOf(id)
    if (at < 0) {
      waiting = true
      return
    }
    waiting = false
    moved = true
    cursor.value = at
    go(id)
  }

  function nextMatch(): void {
    if (!matchList.value.length) return
    cursor.value = (current.value + 1) % matchList.value.length
    go(matchList.value[cursor.value]!)
  }

  onBeforeUnmount(() => clearTimeout(timer))

  return { matchList, current, nextMatch, show }
}
