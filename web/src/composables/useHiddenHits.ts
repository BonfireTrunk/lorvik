import { computed, type Ref } from 'vue'
import type { CardBrief } from '../lib/types'

export interface HiddenHit<R> {
  card: CardBrief
  reason: R
}

/**
 * The found cards a view does not show, each with the reason `why` gives; a
 * card `why` answers null for is shown. `why` reads the view's state, so the
 * list follows it as filters change.
 */
export function useHiddenHits<R>(found: Ref<CardBrief[]>, why: (card: CardBrief) => R | null) {
  return computed<HiddenHit<R>[]>(() =>
    found.value.flatMap((card) => {
      const reason = why(card)
      return reason ? [{ card, reason }] : []
    }),
  )
}
