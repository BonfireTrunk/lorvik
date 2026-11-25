import { computed, ref, watch } from 'vue'
import * as cache from '../lib/cardCache'
import { aloneGroup, mapMode } from '../lib/map/collapse'
import type { MapFilters, MapSource } from '../lib/map/types'

export interface MapProgress {
  label: string
  done: number
  total: number
}

const EMPTY: MapSource = { mode: 'cards', cards: [], links: [], progress: new Map(), notes: [], groups: [], groupLinks: [] }

/**
 * Everything the map draws, read through the shared card cache. What is read
 * depends on the mode the filters and the project's size call for:
 * - cards: the card pages of the statuses the filters show, drawn as they
 *   arrive, and the project's link, mention, note and story-group lists;
 * - collapsed: only the boxes and the link counts between them, plus the cards
 *   of boxes the viewer opened (and the link lists once one is open);
 * - group: the cards of one box, the boxes (for the pickers), and the link lists.
 * `load` reads what the cache lacks or holds stale. `loading` blocks the map
 * only until the first cards or boxes are in; `fetching` stays up until every
 * page has arrived. A status the filters turn on later is added quietly, and
 * one they turn off stays loaded until a change marks it stale. Live changes
 * land in the cache itself.
 */
export function useMapData(slug: () => string, filters: () => MapFilters) {
  const loading = ref(false)
  const fetching = ref(false)
  const error = ref<unknown>(null)
  const mode = computed(() => mapMode(filters(), cache.countsOf(slug())))
  const source = computed<MapSource>(() => {
    const held = cache.mapOf(slug(), mode.value === 'cards')
    // Until the counts are in, which mode applies is not known, and drawing the wrong one costs a layout.
    return held && cache.countsOf(slug()) ? { ...held, mode: mode.value } : EMPTY
  })
  const total = computed(() => cache.totalOf(slug()))
  const statuses = () => filters().statuses
  const progress = computed<MapProgress>(() => ({
    label: loading.value ? (mode.value === 'collapsed' ? 'Reading the map' : 'Reading cards') : 'Loading more cards',
    done: source.value.cards.filter((c) => statuses().includes(c.status)).length,
    total: mode.value === 'collapsed' ? 0 : cache.countOf(slug(), statuses()),
  }))
  let generation = 0
  let working = false

  /** The boxes whose cards the current mode draws. */
  function wantedGroups(): cache.GroupRead[] {
    const f = filters()
    const keys = mode.value === 'group' ? [aloneGroup(f)] : mode.value === 'collapsed' ? f.open : []
    return keys.map((key) => ({ key, statuses: f.statuses }))
  }

  // Live changes keep what the current mode draws complete, with its links.
  const need: cache.Need = {
    rows: (s) => (mode.value === 'cards' && statuses().includes(s) ? Infinity : 0),
    get details() {
      return mode.value !== 'collapsed' || filters().open.length > 0
    },
    get summary() {
      return mode.value !== 'cards'
    },
    get groups() {
      return wantedGroups()
    },
  }
  watch(slug, (project, _, onCleanup) => onCleanup(cache.holdProject(project, need)), { immediate: true })

  // The first page ends the blocking state; the rest arrives under a quiet indicator.
  watch(
    () => source.value.cards.length + source.value.groups.length,
    (n) => {
      if (n) loading.value = false
    },
  )

  /** Reads what the current mode draws that the cache does not hold complete and fresh. */
  async function readFor(project: string): Promise<void> {
    const f = filters()
    if (mode.value === 'collapsed') {
      await Promise.all([cache.readSummary(project), ...wantedGroups().map((g) => cache.readGroupCards(project, g)), f.open.length ? cache.readDetails(project) : undefined])
    } else if (mode.value === 'group') {
      await Promise.all([cache.readSummary(project), ...wantedGroups().map((g) => cache.readGroupCards(project, g)), cache.readDetails(project)])
    } else {
      await Promise.all([cache.readComplete(project, f.statuses), cache.readDetails(project)])
    }
  }

  /** Whether the current mode draws something the cache lacks. */
  function lacking(project: string): boolean {
    if (mode.value === 'collapsed') return !cache.hasSummary(project) || wantedGroups().some((g) => !cache.hasGroupCards(project, g)) || (filters().open.length > 0 && !cache.hasDetails(project))
    if (mode.value === 'group') return !cache.hasSummary(project) || wantedGroups().some((g) => !cache.hasGroupCards(project, g)) || !cache.hasDetails(project)
    return statuses().some((s) => !cache.isMapReady(project, s))
  }

  async function load(): Promise<void> {
    const mine = ++generation
    const project = slug()
    loading.value = !source.value.cards.length && !source.value.groups.length
    fetching.value = true
    error.value = null
    try {
      // The counts decide the mode, so they come before anything else.
      await cache.readTotal(project)
      await readFor(project)
    } catch (e) {
      if (mine === generation) error.value = e
    } finally {
      if (mine === generation) {
        loading.value = false
        fetching.value = false
        ensure()
      }
    }
  }

  /** Reads what the current mode lacks after the filters changed. */
  async function addMissing(project: string): Promise<void> {
    const mine = generation
    fetching.value = true
    try {
      await readFor(project)
      if (mine === generation) error.value = null
    } catch (e) {
      if (mine === generation) error.value = e
      throw e
    } finally {
      fetching.value = false
    }
  }

  function ensure(): void {
    void addAll()
  }

  async function addAll(): Promise<void> {
    if (working || fetching.value) return
    working = true
    try {
      for (;;) {
        const project = slug()
        if (!lacking(project) || fetching.value) break
        await addMissing(project)
      }
    } catch {
      // addMissing already put the failure in `error`; stop instead of retrying in a loop.
    } finally {
      working = false
    }
  }

  watch(() => [mode.value, statuses().join(), filters().open.join(), aloneGroup(filters())], ensure)

  return { source, mode, loading, fetching, error, progress, total, load, ensure }
}
