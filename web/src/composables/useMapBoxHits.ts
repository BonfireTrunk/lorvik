import { computed, type Ref } from 'vue'
import { groupOfCard } from '../lib/map/collapse'
import { groupNodeId, type GroupBox } from '../lib/map/types'
import type { CardBrief, CardStatus } from '../lib/types'

export interface BoxHit {
  card: CardBrief
  box: GroupBox
}

/**
 * Cards a search finds inside the closed boxes of a collapsed map, which the
 * map holds no cards of. A hit counts when its box is drawn closed and its
 * status is one the filters show. `ids` are the nodes of those boxes, to mark
 * them like any other match.
 */
export function useMapBoxHits(found: Ref<CardBrief[]>, boxes: () => GroupBox[], statuses: () => CardStatus[]) {
  const hits = computed<BoxHit[]>(() => {
    const byKey = new Map(boxes().map((b) => [b.key, b]))
    return found.value
      .filter((c) => statuses().includes(c.status))
      .flatMap((card) => {
        const box = byKey.get(groupOfCard(card))
        return box ? [{ card, box }] : []
      })
  })
  const ids = computed(() => [...new Set(hits.value.map((h) => groupNodeId(h.box.key)))])

  return { hits, ids }
}
