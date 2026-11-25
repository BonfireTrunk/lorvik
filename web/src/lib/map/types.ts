import type { CardBrief, CardKind, CardStatus, LinkKind, MapGroup, MapGroupLink, Progress } from '../types'

export type MapKind = CardKind | 'note'
export type EdgeKind = LinkKind | 'mention'

/** Link kinds that read the same both ways; a line of one has no arrow and may be turned to suit the layout. */
export function isUndirected(kind: EdgeKind): boolean {
  return kind === 'relates' || kind === 'duplicates'
}

export const MAP_KINDS: MapKind[] = ['task', 'question', 'decision', 'story', 'note']

/** A note and the cards of the project it is mentioned from. */
export interface MapNote {
  id: string
  slug: string
  title: string
  mentions: number[]
}

/** A link between two cards of the project; links to other projects are not part of the map. */
export interface MapLink {
  from: number
  to: number
  kind: LinkKind
}

/** How the map is drawn: every card, one closed box per story and lane, or the cards of one box alone. */
export type MapMode = 'cards' | 'collapsed' | 'group'

export interface MapSource {
  mode: MapMode
  /** The server's boxes and the link counts between them; read for the collapsed mode. */
  groups: MapGroup[]
  groupLinks: MapGroupLink[]
  /** The cards read so far; a card missing from the list may be unread, not absent. */
  cards: CardBrief[]
  /** Every link of the project; an end that is not loaded draws nothing. */
  links: MapLink[]
  /** Each story's progress over all its children, from the server, loaded or not. */
  progress: Map<number, Progress>
  notes: MapNote[]
}

export interface MapFilters {
  statuses: CardStatus[]
  kinds: MapKind[]
  /** A lane name, NO_LANE, or empty for every lane. */
  lane: string
  /** A story's number, or 0 for every story. */
  story: number
  linked: boolean
  /** Boxes opened on a collapsed map, by group key. */
  open: string[]
  /** A group key whose cards are drawn alone, or empty. */
  group: string
}

export const NO_LANE = '-'

export interface StoryBox {
  card: CardBrief
  children: CardBrief[]
  /** Progress over every child of the story, closed (done or dropped) counting as done, whatever the filters show or the map loaded. */
  done: number
  total: number
}

export interface GraphEdge {
  id: string
  kind: EdgeKind
  source: string
  target: string
  /** Set on a line between boxes: how many links it stands for, and the two group keys. */
  merged?: { count: number; from: string; to: string }
}

/** A box drawn closed, from the server's group counts. */
export interface GroupBox {
  key: string
  kind: 'story' | 'lane'
  /** The story's number; 0 for a lane. */
  number: number
  /** The story's title; '' for a lane. */
  title: string
  /** The lane's name; '' for a story, and for the cards with no lane. */
  lane: string
  status?: CardStatus
  /** Cards matching the filters, and their count by status (0 for a status the filters hide). */
  shown: number
  counts: Record<CardStatus, number>
}

/** The cards of one lane that sit in no drawn story box; lane is '' for cards without one. */
export interface LaneBox {
  lane: string
  children: CardBrief[]
}

export interface GraphModel {
  cards: CardBrief[]
  stories: StoryBox[]
  notes: MapNote[]
  edges: GraphEdge[]
  lanes: LaneBox[]
  /** Boxes drawn closed on a collapsed map. */
  groups: GroupBox[]
  shown: number
}

export const cardId = (n: number): string => `c${n}`
export const noteId = (slug: string): string => `n:${slug}`
export const laneId = (lane: string): string => `lane:${lane || NO_LANE}`

/** The id of the node that draws a group, closed or open. */
export const groupNodeId = (key: string): string => (key.startsWith('story:') ? cardId(Number(key.slice(6))) : laneId(key.slice(5)))
