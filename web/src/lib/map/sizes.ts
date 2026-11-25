import type { CardBrief } from '../types'

export const CARD_W = 244
export const CARD_H = 116
export const NEEDS_H = 24
export const NOTE_W = 208
export const NOTE_H = 46
export const STORY_MIN_W = 380
export const STORY_HEAD = 62
export const STORY_PAD = 18
export const STORY_EMPTY_H = 76
/** A closed story or lane box on a collapsed map. */
export const GROUP_W = 268
export const GROUP_H = 118
export const GRID_GAP = 16
export const LANE_HEAD = 32
/** Space between a box's header and the first card in it. */
export const HEAD_GAP = 16
export const COMPONENT_GAP = 56
/** Grid columns grow with the square root of the card count times this, so a box reads wider than tall. */
const GRID_WIDENING = 1.3
/** A long lane grows wide up to this many columns, so it reads as a block rather than a column that sets the whole map's height. */
const GRID_COLS_MAX = 8

/** Cards that wait on a human carry a header band, so they are taller. */
export function cardHeight(needs: boolean): number {
  return needs ? CARD_H + NEEDS_H : CARD_H
}

export interface Grid {
  w: number
  h: number
  cells: { card: CardBrief; x: number; y: number }[]
}

/** Columns for n cards in a box that reads roughly 1.6 times wider than tall. */
export function gridCols(n: number): number {
  return Math.max(1, Math.min(n, GRID_COLS_MAX, Math.round(Math.sqrt(n * GRID_WIDENING))))
}

/** Cards in rows of `cols`, each row as tall as its tallest card. */
export function gridLayout(cards: CardBrief[], cols: number, needs: (c: CardBrief) => boolean): Grid {
  const cells: Grid['cells'] = []
  let y = 0
  for (let i = 0; i < cards.length; i += cols) {
    const row = cards.slice(i, i + cols)
    const rowH = Math.max(...row.map((c) => cardHeight(needs(c))))
    row.forEach((card, j) => cells.push({ card, x: j * (CARD_W + GRID_GAP), y }))
    y += rowH + GRID_GAP
  }
  const used = Math.min(cols, cards.length)
  return { w: used * CARD_W + Math.max(0, used - 1) * GRID_GAP, h: Math.max(0, y - GRID_GAP), cells }
}
