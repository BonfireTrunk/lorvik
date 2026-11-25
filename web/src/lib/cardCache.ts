import { shallowReactive } from 'vue'
import { ApiError, MAX_PAGE, allPages, api } from './api'
import { STATUS_ORDER } from './cards'
import { classify, mergeChange, touchesMap, type MapChange } from './map/refresh'
import type { MapLink, MapNote } from './map/types'
import { subscribeEvents } from './stream'
import type { CardBrief, CardStatus, MapGroup, MapGroupLink, NoteBrief, Progress, ProjectLink, ProjectMention, StatusCounts } from './types'

/** Data older than this is read again on the next visit. */
export const STALE_MS = 5 * 60_000
/** Projects kept at once; the least recently used one is dropped first. */
export const MAX_PROJECTS = 3
/** Rows the first read of a status asks for, and each "load more". */
export const PAGE = 50
/** Events must be quiet this long before the cache re-reads what they changed. */
const REFRESH_DELAY_MS = 400

/** One status's cards in server order, and how far the cursor chain has been read. */
export interface StatusList {
  items: CardBrief[]
  next: string
  remaining: number
  /** When the list was read from the start; "load more" does not change it. */
  at: number
}

interface Cached {
  lists: Map<CardStatus, StatusList>
  stories: CardBrief[] | null
  storiesAt: number
  /** Card counts of the whole project by status, for the map's "shown of total" and its page progress. */
  counts: StatusCounts | null
  totalAt: number
  /** Links and notes are the map's, each read as a whole paged list. */
  details: MapDetails | null
  detailAt: number
  /** The server's story and lane boxes with their status counts; they also give a story's progress. */
  boxes: { items: MapGroup[]; at: number } | null
  /** The link counts between the boxes. */
  boxLinks: { items: MapGroupLink[]; at: number } | null
  /** The cards of single boxes, by `groupId`. */
  groups: Map<string, GroupCards>
  /** Every link of the project with its ends' numbers, for listing the links behind a line between two boxes. */
  linkRows: { items: ProjectLink[]; at: number } | null
  /** Counts the re-reads that have landed, so a list kept outside the cache knows to read again. */
  version: number
}

interface GroupCards {
  items: CardBrief[]
  at: number
}

/** A box whose cards a view draws, in the given statuses. */
export interface GroupRead {
  key: string
  statuses: CardStatus[]
}

const groupId = (g: GroupRead): string => `${g.key}|${g.statuses.join(',')}`

/** What the map draws besides cards. */
interface MapDetails {
  links: MapLink[]
  /** Each note with the cards that mention it. */
  notes: MapNote[]
}

/** What an open view needs kept current from live events. */
export interface Need {
  /** Rows of a status the view shows: 0 for none, Infinity for the whole list. */
  rows(status: CardStatus): number
  /** Whether the view draws links, story progress and notes. */
  details: boolean
  /** Whether the view draws the collapsed map's boxes and the link counts between them. */
  summary?: boolean
  /** The boxes whose cards the view draws. */
  groups?: GroupRead[]
}

interface Slot {
  data: Cached
  usedAt: number
  pending: MapChange | null
  timer: ReturnType<typeof setTimeout> | undefined
  working: boolean
  stop: () => void
  flights: Map<string, Promise<unknown>>
  /** Every read in progress; a re-read for a change waits for them, so it lands after any read that began before the change. */
  running: Set<Promise<unknown>>
  holders: Set<Need>
}

// The slots map is reactive so views notice a project entering or leaving; each
// slot's data is replaced field by field, so a view re-reads only what changed.
const slots = shallowReactive(new Map<string, Slot>())

function fresh(at: number): boolean {
  return Date.now() - at < STALE_MS
}

export function isComplete(list: StatusList): boolean {
  return !list.next || list.remaining <= 0
}

function alive(project: string, slot: Slot): boolean {
  return slots.get(project) === slot
}

function emptyData(): Cached {
  return {
    lists: new Map(),
    stories: null,
    storiesAt: 0,
    counts: null,
    totalAt: 0,
    details: null,
    detailAt: 0,
    boxes: null,
    boxLinks: null,
    groups: new Map(),
    linkRows: null,
    version: 0,
  }
}

function drop(project: string): void {
  const slot = slots.get(project)
  if (!slot) return
  slot.stop()
  clearTimeout(slot.timer)
  slots.delete(project)
}

/** The project's slot, created on first use. Opening one past the limit drops the least recently used. */
function slotFor(project: string): Slot {
  let slot = slots.get(project)
  if (!slot) {
    const made: Slot = {
      data: shallowReactive(emptyData()),
      usedAt: 0,
      pending: null,
      timer: undefined,
      working: false,
      stop: () => undefined,
      flights: new Map(),
      running: new Set(),
      holders: new Set(),
    }
    // The cache follows the project's events itself, so a change lands once
    // whichever view is open, or none.
    made.stop = subscribeEvents(project, '', (e) => {
      if (!touchesMap(e)) return
      made.pending = made.pending ? mergeChange(made.pending, classify(e)) : classify(e)
      clearTimeout(made.timer)
      made.timer = setTimeout(() => void drain(project, made), REFRESH_DELAY_MS)
    }, () => undefined)
    slots.set(project, made)
    slot = made
    while (slots.size > MAX_PROJECTS) {
      const oldest = [...slots].filter(([, s]) => s !== made && !s.holders.size).sort((a, b) => a[1].usedAt - b[1].usedAt)[0]
      if (!oldest) break
      drop(oldest[0])
    }
  }
  slot.usedAt = Date.now()
  return slot
}

/**
 * Keeps the project current while the calling view is open; returns the
 * release. A project no view holds is not re-read on a change, only marked
 * stale, so the next visit reads it again.
 */
export function holdProject(project: string, need: Need): () => void {
  const slot = slotFor(project)
  slot.holders.add(need)
  return () => slot.holders.delete(need)
}

/** Empties the cache and stops following events; used on logout so no user's data outlives the session. */
export function clearCardCache(): void {
  for (const project of [...slots.keys()]) drop(project)
}

function track<T>(slot: Slot, p: Promise<T>): Promise<T> {
  slot.running.add(p)
  void p.finally(() => slot.running.delete(p)).catch(() => undefined)
  return p
}

/** Runs `read` once per key at a time, so two views asking for the same list share one request. */
function flight<T>(slot: Slot, key: string, read: () => Promise<T>): Promise<T> {
  const running = slot.flights.get(key)
  if (running) return running as Promise<T>
  const p = track(slot, read().finally(() => slot.flights.delete(key)))
  slot.flights.set(key, p)
  return p
}

interface ListRead {
  /** Keep reading until at least this many rows are held. */
  atLeast: number
  /** Keep reading until the cursor chain ends. */
  all: boolean
  /** Continue a partly read list instead of starting over. */
  from?: StatusList
}

async function readList(project: string, slot: Slot, status: CardStatus, o: ListRead): Promise<StatusList> {
  // A partial read is shown page by page, but only while it adds cards, so a
  // re-read never shrinks a list in view. It carries no stale clock until the
  // read ends, so no other reader takes it for a finished list.
  const show = (l: StatusList, last: boolean): void => {
    if (!alive(project, slot)) return
    const held = slot.data.lists.get(status)
    if (last || !held || l.items.length > held.items.length) slot.data.lists = new Map(slot.data.lists).set(status, last ? l : { ...l, at: 0 })
  }
  const wants = (l: StatusList): boolean => !isComplete(l) && (o.all || l.items.length < o.atLeast)
  let list = o.from
  if (!list) {
    const first = await api.cards(project, { status, limit: Math.min(MAX_PAGE, Math.max(PAGE, o.atLeast)) })
    list = { items: first.items, next: first.next ?? '', remaining: first.remaining, at: Date.now() }
    if (wants(list)) show(list, false)
  }
  while (wants(list)) {
    const page = await api.cards(project, { status, limit: MAX_PAGE, cursor: list.next })
    list = { items: [...list.items, ...page.items], next: page.next ?? '', remaining: page.remaining, at: list.at }
    if (wants(list)) show(list, false)
  }
  show(list, true)
  return list
}

/** The cards read so far for a status; reactive. */
export function listsOf(project: string): Map<CardStatus, StatusList> | undefined {
  return slots.get(project)?.data.lists
}

/** The first rows of a status: the cached list when it is fresh, else `rows` rows read from the start. */
export function readStatus(project: string, status: CardStatus, force = false, rows = PAGE): Promise<StatusList> {
  const slot = slotFor(project)
  const held = slot.data.lists.get(status)
  if (held && !force && fresh(held.at)) return Promise.resolve(held)
  return flight(slot, status, () => readList(project, slot, status, { atLeast: rows, all: false }))
}

/** A list replaced this many times while "load more" reads its next page is left as it is. */
const MORE_TRIES = 3

/** The next page of a status after the rows held; read again on the new list when a refresh replaced it meanwhile. */
export async function readMore(project: string, status: CardStatus): Promise<StatusList> {
  const slot = slotFor(project)
  for (let i = 0; ; i++) {
    const held = slot.data.lists.get(status)
    if (!held) return readStatus(project, status)
    if (i === MORE_TRIES) return held
    const page = await track(slot, api.cards(project, { status, limit: PAGE, cursor: held.next }))
    const list = { items: [...held.items, ...page.items], next: page.next ?? '', remaining: page.remaining, at: held.at }
    if (!alive(project, slot)) return list
    if (slot.data.lists.get(status) !== held) continue
    slot.data.lists = new Map(slot.data.lists).set(status, list)
    return list
  }
}

/** Makes each status's list complete and fresh, reading only the ones that are not. */
export async function readComplete(project: string, statuses: CardStatus[]): Promise<void> {
  const slot = slotFor(project)
  await Promise.all(
    statuses.map((status) => {
      const held = slot.data.lists.get(status)
      const current = held && fresh(held.at) ? held : undefined
      if (current && isComplete(current)) return undefined
      return flight(slot, `${status}:all`, () => readList(project, slot, status, { atLeast: 0, all: true, from: current }))
    }),
  )
}

/** The complete lists' cards of the given statuses, in the order given. */
export function cardsOf(project: string, statuses: CardStatus[]): CardBrief[] {
  const lists = slots.get(project)?.data.lists
  return statuses.flatMap((s) => {
    const list = lists?.get(s)
    return list && isComplete(list) ? list.items : []
  })
}

export function storiesOf(project: string): CardBrief[] | null {
  return slots.get(project)?.data.stories ?? null
}

/** Every story, any status; cached while fresh. */
export async function readStories(project: string, force = false): Promise<CardBrief[]> {
  const slot = slotFor(project)
  const held = slot.data.stories
  if (held && !force && fresh(slot.data.storiesAt)) return held
  return flight(slot, 'stories', async () => {
    const items = await allPages((cursor) => api.cards(project, { kind: 'story', limit: MAX_PAGE, cursor }))
    if (alive(project, slot)) {
      slot.data.stories = items
      slot.data.storiesAt = Date.now()
    }
    return items
  })
}

function sum(counts: StatusCounts): number {
  return STATUS_ORDER.reduce((n, k) => n + counts[k], 0)
}

export function countsOf(project: string): StatusCounts | null {
  return slots.get(project)?.data.counts ?? null
}

export function totalOf(project: string): number {
  const counts = slots.get(project)?.data.counts
  return counts ? sum(counts) : 0
}

/** How many cards the project holds in the given statuses; 0 until the counts are read. */
export function countOf(project: string, statuses: CardStatus[]): number {
  const counts = slots.get(project)?.data.counts
  return counts ? statuses.reduce((n, s) => n + counts[s], 0) : 0
}

export async function readTotal(project: string, force = false): Promise<void> {
  const slot = slotFor(project)
  if (!force && fresh(slot.data.totalAt)) return
  await flight(slot, 'total', async () => {
    const counts = (await api.project(project)).counts
    if (!alive(project, slot)) return
    slot.data.counts = counts ?? null
    slot.data.totalAt = Date.now()
  })
}

/** Whether a status's list is complete and not marked stale by a change. */
export function isMapReady(project: string, status: CardStatus): boolean {
  const list = slots.get(project)?.data.lists.get(status)
  return !!list && list.at > 0 && isComplete(list)
}

/** Whether the links and notes are held and fresh. */
export function hasDetails(project: string): boolean {
  const d = slots.get(project)?.data
  return !!d?.details && fresh(d.detailAt)
}

/** A story's progress over all its children: closed (done or dropped) over every status. */
function progressOf(groups: MapGroup[]): Map<number, Progress> {
  const progress = new Map<number, Progress>()
  for (const g of groups) {
    if (g.kind === 'story' && g.story) progress.set(g.story, { done: g.counts.done + g.counts.dropped, total: sum(g.counts) })
  }
  return progress
}

/** Each note that the project's cards mention, with the numbers of those cards. */
function notesOf(notes: NoteBrief[], mentions: ProjectMention[]): MapNote[] {
  const byId = new Map(notes.map((n) => [n.id, n]))
  const found = new Map<string, MapNote>()
  for (const m of mentions) {
    const n = byId.get(m.note.id)
    if (!n) continue
    const note = found.get(n.id) ?? { id: n.id, slug: n.slug, title: n.title, mentions: [] }
    note.mentions.push(m.card)
    found.set(n.id, note)
  }
  return [...found.values()]
}

/** Reads the project's links, mentions, notes and boxes, each as a whole paged list, unless they are held and fresh. */
export async function readDetails(project: string, force = false): Promise<void> {
  const slot = slotFor(project)
  if (!force && hasDetails(project)) return
  await flight(slot, 'details', async () => {
    const [links, mentions, notes] = await Promise.all([
      allPages((cursor) => api.links(project, cursor)),
      allPages((cursor) => api.mentions(project, cursor)),
      allPages((cursor) => api.notes({ project, limit: MAX_PAGE, cursor })),
      readBoxes(project, slot, force),
    ])
    if (!alive(project, slot)) return
    slot.data.details = {
      links: links.filter((l) => !l.from.project && !l.to.project).map((l) => ({ from: l.from.number, to: l.to.number, kind: l.kind })),
      notes: notesOf(notes, mentions),
    }
    slot.data.detailAt = Date.now()
  })
}

export interface CachedMap {
  cards: CardBrief[]
  groups: MapGroup[]
  groupLinks: MapGroupLink[]
  links: MapLink[]
  progress: Map<number, Progress>
  notes: MapNote[]
}

/**
 * What the map draws. Its cards come from one source only, so a list the
 * cache kept for another mode never feeds the box reads, or the other way
 * round: the status lists with `lists`, even one still being read, else the
 * box reads the views hold. The details are drawn once they have arrived.
 */
export function mapOf(project: string, lists: boolean): CachedMap | null {
  const slot = slots.get(project)
  const d = slot?.data
  if (!slot || !d) return null
  let sources: GroupCards[]
  if (lists) sources = [...d.lists.values()]
  else {
    const wanted = new Set([...slot.holders].flatMap((h) => h.groups ?? []).map(groupId))
    sources = [...d.groups].filter(([id]) => wanted.has(id)).map(([, g]) => g)
  }
  // A card that changed status between two page reads can sit in two lists for a moment.
  const seen = new Set<number>()
  const cards = sources.flatMap((l) => l.items).filter((c) => !seen.has(c.number) && seen.add(c.number))
  const boxes = d.boxes?.items ?? []
  return { cards, groups: boxes, groupLinks: d.boxLinks?.items ?? [], links: d.details?.links ?? [], progress: progressOf(boxes), notes: d.details?.notes ?? [] }
}

/** One worker per project, so changes that arrive during a re-read are merged and applied after it. */
async function drain(project: string, slot: Slot): Promise<void> {
  if (slot.working) return
  slot.working = true
  try {
    while (slot.pending && alive(project, slot)) {
      // A read that began before the change can finish after it; waiting for
      // every read in progress keeps it from overwriting what this one reads.
      await Promise.allSettled([...slot.running])
      const change = slot.pending
      slot.pending = null
      if (!change || !alive(project, slot)) break
      if (!slot.holders.size) {
        expire(slot)
        continue
      }
      // The re-read goes through the reading functions, which count as use; a
      // project nobody is looking at must not stay fresh in the LRU order.
      const usedAt = slot.usedAt
      try {
        await refresh(project, slot, change)
        slot.data.version++
      } catch {
        // A failed re-read leaves the data possibly behind the server; marking it stale makes the next visit read it again.
        expire(slot)
      } finally {
        slot.usedAt = usedAt
      }
    }
  } finally {
    slot.working = false
  }
}

function expire(slot: Slot): void {
  const d = slot.data
  d.lists = new Map([...d.lists].map(([s, l]) => [s, { ...l, at: 0 }]))
  d.storiesAt = 0
  d.totalAt = 0
  d.detailAt = 0
  if (d.boxes) d.boxes = { ...d.boxes, at: 0 }
  if (d.boxLinks) d.boxLinks = { ...d.boxLinks, at: 0 }
  d.groups = new Map([...d.groups].map(([id, g]) => [id, { ...g, at: 0 }]))
  if (d.linkRows) d.linkRows = { ...d.linkRows, at: 0 }
  d.version++
}

/**
 * Re-reads what the open views need of what `change` can have moved: each
 * status as far as the view that shows most of it, and the link, mention and
 * group lists only for a view that draws them. Anything else is marked stale.
 */
async function refresh(project: string, slot: Slot, change: MapChange): Promise<void> {
  const holders = [...slot.holders]
  const rows = (s: CardStatus): number => Math.max(0, ...holders.map((h) => h.rows(s)))
  const d = slot.data
  const held = [...d.lists.keys()]
  d.lists = new Map([...d.lists].map(([s, l]) => [s, rows(s) ? l : { ...l, at: 0 }]))
  const details = change.details && d.detailAt > 0
  const wanted = details && holders.some((h) => h.details)
  if (details && !wanted) d.detailAt = 0
  const summary = change.details && !!d.boxLinks && d.boxLinks.at > 0
  const summaryWanted = summary && holders.some((h) => h.summary)
  if (summary && !summaryWanted) d.boxLinks = { ...d.boxLinks!, at: 0 }
  if (d.boxes && !wanted && !summaryWanted) d.boxes = { ...d.boxes, at: 0 }
  if (change.details && d.linkRows) d.linkRows = { ...d.linkRows, at: 0 }
  const groups = new Map(holders.flatMap((h) => h.groups ?? []).map((g) => [groupId(g), g]))
  // A box nobody draws keeps its cards but is marked stale, like a list nobody shows.
  d.groups = new Map([...d.groups].map(([id, g]) => [id, groups.has(id) ? g : { ...g, at: 0 }]))
  await Promise.all([
    ...[...groups.values()].map((g) => readGroupCards(project, g, true)),
    summaryWanted ? readSummary(project, true) : undefined,
    ...held.filter((s) => rows(s)).map((s) => readList(project, slot, s, rows(s) === Infinity ? { atLeast: 0, all: true } : { atLeast: rows(s), all: false })),
    d.stories ? readStories(project, true) : undefined,
    d.totalAt ? readTotal(project, true) : undefined,
    wanted ? readDetails(project, true) : undefined,
  ])
}

export function versionOf(project: string): number {
  return slots.get(project)?.data.version ?? 0
}

/** Whether the boxes and the link counts between them are held and fresh. */
export function hasSummary(project: string): boolean {
  const d = slots.get(project)?.data
  return !!d?.boxes && !!d.boxLinks && fresh(d.boxes.at) && fresh(d.boxLinks.at)
}

/** The server's box rows, read whole once however many readers want them; kept while fresh. */
async function readBoxes(project: string, slot: Slot, force: boolean): Promise<MapGroup[]> {
  const held = slot.data.boxes
  if (held && !force && fresh(held.at)) return held.items
  return flight(slot, 'boxes', async () => {
    const items = await allPages((cursor) => api.mapGroups(project, cursor))
    if (alive(project, slot)) slot.data.boxes = { items, at: Date.now() }
    return items
  })
}

/** Reads the project's boxes and the link counts between them, unless they are held and fresh. */
export async function readSummary(project: string, force = false): Promise<void> {
  const slot = slotFor(project)
  if (!force && hasSummary(project)) return
  await Promise.all([
    readBoxes(project, slot, force),
    flight(slot, 'boxLinks', async () => {
      const items = await allPages((cursor) => api.mapGroupLinks(project, cursor))
      if (alive(project, slot)) slot.data.boxLinks = { items, at: Date.now() }
    }),
  ])
}

/** The card list's filters that name a box's cards: a story's children, or the cards of no story with that lane. */
function boxFilter(key: string): Record<string, string | number | boolean> {
  if (key.startsWith('story:')) return { parent: Number(key.slice(6)) }
  const lane = key.slice(5)
  return lane ? { no_story: true, lane } : { no_story: true, no_lane: true }
}

async function readStoryCard(project: string, key: string, force: boolean): Promise<CardBrief[]> {
  if (!key.startsWith('story:')) return []
  const number = Number(key.slice(6))
  return (await readStories(project, force)).filter((c) => c.number === number)
}

/** The cards of one box in the given statuses (a story also brings its own card), read whole; cached while fresh. */
export async function readGroupCards(project: string, g: GroupRead, force = false): Promise<CardBrief[]> {
  const slot = slotFor(project)
  const id = groupId(g)
  const held = slot.data.groups.get(id)
  if (held && !force && fresh(held.at)) return held.items
  return flight(slot, `group:${id}`, async () => {
    const [own, cards] = await Promise.all([
      readStoryCard(project, g.key, force),
      allPages((cursor) => api.cards(project, { ...boxFilter(g.key), status: g.statuses.join(','), limit: MAX_PAGE, cursor })),
    ])
    const items = [...own, ...cards]
    if (alive(project, slot)) slot.data.groups = new Map(slot.data.groups).set(id, { items, at: Date.now() })
    return items
  })
}

/** Whether a box's cards are held and fresh. */
export function hasGroupCards(project: string, g: GroupRead): boolean {
  const held = slots.get(project)?.data.groups.get(groupId(g))
  return !!held && held.at > 0 && fresh(held.at)
}

export interface GroupPage {
  items: CardBrief[]
  /** Opaque; pass it back to read the page after this one. Empty at the end. */
  next: string
}

/** One page of a box's cards in board order, from the server. */
export async function readGroupPage(project: string, g: GroupRead, cursor = '', limit = PAGE): Promise<GroupPage> {
  const page = await api.cards(project, { ...boxFilter(g.key), status: g.statuses.join(','), limit, cursor })
  return { items: page.items, next: page.next ?? '' }
}

/** Every link of the project, ends as card numbers; cached while fresh. */
export async function readLinkRows(project: string): Promise<ProjectLink[]> {
  const slot = slotFor(project)
  const held = slot.data.linkRows
  if (held && fresh(held.at)) return held.items
  return flight(slot, 'linkRows', async () => {
    const items = (await allPages((cursor) => api.links(project, cursor))).filter((l) => !l.from.project && !l.to.project)
    if (alive(project, slot)) slot.data.linkRows = { items, at: Date.now() }
    return items
  })
}

/** How many matches of a search one set of statuses lists. */
const SEARCH_LIMIT = 20

/**
 * The cards a search text finds: the server's full-text matches in each set of
 * statuses, up to SEARCH_LIMIT a set, and the card with that number when the
 * text is one. Separate sets keep matches in one set from crowding out another's.
 */
export async function findCards(project: string, text: string, sets: CardStatus[][]): Promise<CardBrief[]> {
  const q = text.trim().replace(/^s?#/, '')
  if (!q) return []
  const number = /^\d+$/.test(q) ? Number(q) : 0
  const [one, ...pages] = await Promise.all([
    number
      ? api.card(project, number).catch((e: unknown) => {
          if (e instanceof ApiError && e.status === 404) return null
          throw e
        })
      : null,
    ...sets.filter((set) => set.length).map((set) => api.cards(project, { q, status: set.join(','), limit: SEARCH_LIMIT })),
  ])
  const seen = new Set<number>()
  return [...(one ? [one] : []), ...pages.flatMap((p) => p.items)].filter((c) => !seen.has(c.number) && !!seen.add(c.number))
}
