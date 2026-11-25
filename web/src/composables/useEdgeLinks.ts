import { onBeforeUnmount, ref, watch } from 'vue'
import * as cache from '../lib/cardCache'
import { STATUS_ORDER } from '../lib/cards'
import { isUndirected, type GraphEdge } from '../lib/map/types'
import type { CardBrief } from '../lib/types'

export interface LinkRow {
  from: CardBrief
  to: CardBrief
}

/**
 * The links one line between two boxes stands for. The server counts them but
 * does not list them, so the project's links are read and kept where both ends
 * are cards of the two boxes (either way round for a kind with no direction).
 */
export function useEdgeLinks(slug: () => string, edge: () => GraphEdge | null) {
  const rows = ref<LinkRow[]>([])
  const loading = ref(false)
  const error = ref<unknown>(null)
  let generation = 0

  async function load(): Promise<void> {
    const e = edge()
    const mine = ++generation
    if (!e?.merged) {
      rows.value = []
      return
    }
    const project = slug()
    const { from, to } = e.merged
    loading.value = true
    error.value = null
    try {
      const [links, a, b] = await Promise.all([
        cache.readLinkRows(project),
        cache.readGroupCards(project, { key: from, statuses: STATUS_ORDER }),
        cache.readGroupCards(project, { key: to, statuses: STATUS_ORDER }),
      ])
      if (mine !== generation) return
      const inA = new Map(a.map((c) => [c.number, c]))
      const inB = new Map(b.map((c) => [c.number, c]))
      const out: LinkRow[] = []
      for (const l of links) {
        if (l.kind !== e.kind) continue
        const ab = inA.get(l.from.number) && inB.get(l.to.number)
        if (ab) out.push({ from: inA.get(l.from.number)!, to: inB.get(l.to.number)! })
        else if (isUndirected(l.kind) && inB.get(l.from.number) && inA.get(l.to.number)) out.push({ from: inB.get(l.from.number)!, to: inA.get(l.to.number)! })
      }
      rows.value = out
    } catch (err) {
      if (mine === generation) error.value = err
    } finally {
      if (mine === generation) loading.value = false
    }
  }

  watch(() => edge()?.id, load, { immediate: true })
  onBeforeUnmount(() => generation++)

  return { rows, loading, error, retry: load }
}
