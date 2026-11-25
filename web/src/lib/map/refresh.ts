import type { LorvikEvent } from '../types'

/** What a batch of events asks the map to re-read. The card lists are re-read for every change; this says whether the link, mention and group lists are too. */
export interface MapChange {
  details: boolean
}

/** Events that move a card's claim or comments but not its links, notes, status or place. */
const CARD_ONLY = new Set(['card.claimed', 'card.released', 'card.claim_expired', 'card.comment'])

export function mergeChange(a: MapChange, b: MapChange): MapChange {
  return { details: a.details || b.details }
}

/** Whether an event can change what the map draws. Project, agent, member and token events cannot. */
export function touchesMap(e: LorvikEvent): boolean {
  return e.type.startsWith('card.') || e.type.startsWith('note.')
}

/** An event whose type is not understood counts as changing the details. */
export function classify(e: LorvikEvent): MapChange {
  return { details: !CARD_ONLY.has(e.type) }
}
