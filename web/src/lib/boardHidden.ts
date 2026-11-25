import type { CardBrief, CardStatus } from './types'

export interface BoardContext {
  hidden: CardStatus[]
  collapsed: (status: CardStatus) => boolean
  /** The story the board is narrowed to, or 0. */
  story: number
  /** Whether a tile for the card is drawn. */
  drawn: (card: CardBrief) => boolean
}

const word = (s: string): string => s.charAt(0).toUpperCase() + s.slice(1)

/**
 * Why a card has no tile on the board; null when it has one. A card in an open
 * column the board has not paged to yet is "not loaded", the last reason.
 */
export function boardReason(c: CardBrief, ctx: BoardContext): { label: string } | null {
  if (ctx.drawn(c)) return null
  const why: string[] = []
  if (ctx.hidden.includes(c.status)) why.push(`${word(c.status)} column hidden`)
  else if (ctx.collapsed(c.status)) why.push(`${word(c.status)} column collapsed`)
  if (ctx.story && c.number !== ctx.story && c.parent !== ctx.story) why.push('Story filter')
  return { label: why.length ? why.join(', ') : 'Not loaded yet' }
}
