<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import CardPanel from '../components/card/CardPanel.vue'
import NewCardPanel from '../components/card/NewCardPanel.vue'
import CardTile from '../components/card/CardTile.vue'
import ContextMenu from '../components/card/ContextMenu.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import HiddenHits from '../components/HiddenHits.vue'
import LoadMore from '../components/LoadMore.vue'
import SearchBox from '../components/SearchBox.vue'
import BoardMapSwitch from '../components/BoardMapSwitch.vue'
import AnsweredForYou from '../components/AnsweredForYou.vue'
import AwaitingYou from '../components/AwaitingYou.vue'
import RunningNow from '../components/RunningNow.vue'
import Replies from '../components/Replies.vue'
import { useCardPanel } from '../composables/useCardPanel'
import { useEventRefresh } from '../composables/useEventRefresh'
import { useCardActionKeys } from '../composables/useCardActions'
import { useFoundCards } from '../composables/useFoundCards'
import { useHiddenHits } from '../composables/useHiddenHits'
import { useKeymap } from '../composables/useKeymap'
import { useSearchCursor } from '../composables/useSearchCursor'
import { MAX_PAGE, api } from '../lib/api'
import * as cardCache from '../lib/cardCache'
import { PAGE, type StatusList } from '../lib/cardCache'
import { boardReason } from '../lib/boardHidden'
import { STATUS_ORDER, isClosed, storyWaitsOn } from '../lib/cards'
import { titleMatcher } from '../lib/cardSearch'
import { canIn, canHumanIn, humanAsk } from '../lib/roles'
import { readOrgStored, writeOrgStored } from '../lib/auth'
import type { Card, CardBrief, CardStatus } from '../lib/types'
import IconChevronDown from '~icons/lucide/chevron-down'
import IconChevronRight from '~icons/lucide/chevron-right'
import IconPlus from '~icons/lucide/plus'

const props = defineProps<{ slug: string }>()

const COLUMNS = STATUS_ORDER
// Pages of one column a search will read to reach a card before it opens the card in the panel instead.
const REVEAL_PAGES = 20
// Finished work folds away; the other columns are always open.
const COLLAPSIBLE: CardStatus[] = ['done', 'dropped']
// What an empty column means, so an empty board still says where work goes.
const EMPTY: Record<CardStatus, string> = {
  backlog: 'Nothing parked.',
  ready: 'Nothing ready to pick up.',
  working: 'Nobody is working a card.',
  waiting: 'Nothing waiting on an answer.',
  done: 'Nothing finished yet.',
  dropped: 'Nothing dropped.',
}

interface Column {
  items: CardBrief[]
  next: string
  remaining: number
  loaded: boolean
  busy: boolean
  collapsed: boolean
  /** Rows the viewer asked for: the first page and each "load more". */
  want: number
}

function emptyColumns(): Record<CardStatus, Column> {
  const m = {} as Record<CardStatus, Column>
  for (const s of COLUMNS) {
    m[s] = { items: [], next: '', remaining: 0, loaded: false, busy: false, collapsed: COLLAPSIBLE.includes(s), want: PAGE }
  }
  return m
}

const panel = useCardPanel(props.slug)
const adding = ref(false)

function created(card: Card): void {
  adding.value = false
  loadOpen(true)
  panel.open(card.project, card.number)
}

function openPanel(n: number): void {
  panel.open(props.slug, n)
}

// Per-viewer column visibility, kept in localStorage.
const HIDDEN_KEY = 'board.hidden'

function readHidden(): CardStatus[] {
  try {
    const v: unknown = JSON.parse(readOrgStored(HIDDEN_KEY) || '[]')
    return Array.isArray(v) ? COLUMNS.filter((s) => v.includes(s)) : []
  } catch {
    return []
  }
}

const hidden = ref<CardStatus[]>(readHidden())
const shown = computed(() => COLUMNS.filter((s) => !hidden.value.includes(s)))

function toggleVisible(s: CardStatus): void {
  const was = hidden.value.includes(s)
  hidden.value = was ? hidden.value.filter((h) => h !== s) : [...hidden.value, s]
  writeOrgStored(HIDDEN_KEY, JSON.stringify(hidden.value))
  if (was && !cols[s].collapsed && !cols[s].loaded) void loadColumn(s)
}

// Open stories feed the toolbar filter; every story, closed ones too, names
// the parent chip on its children's tiles.
const stories = computed(() => cardCache.storiesOf(props.slug) ?? [])
const storyTitles = computed(() => new Map(stories.value.map((c) => [c.number, c.title])))
const openStories = computed(() => stories.value.filter((c) => !isClosed(c.status)))
const storyFilter = ref(0)
const filterStory = computed(() => stories.value.find((c) => c.number === storyFilter.value))

async function loadStories(force: boolean): Promise<void> {
  try {
    await cardCache.readStories(props.slug, force)
  } catch (e) {
    error.value = e
  }
}

watch(stories, () => {
  if (storyFilter.value && !filterStory.value) storyFilter.value = 0
})

// With a story picked, a column shows the story itself above its children.
function visible(s: CardStatus): CardBrief[] {
  const story = filterStory.value
  return story && story.status === s ? [story, ...cols[s].items] : cols[s].items
}

// Set by the strips above the board, which already load these lists.
const awaiting = ref<CardBrief[]>([])
const replied = ref<number[]>([])

function needsOf(c: CardBrief): string | undefined {
  if (!canHumanIn(props.slug, 'reviewer')) return undefined
  const ask = humanAsk(props.slug, c)
  if (ask) return ask
  if (c.kind !== 'story' || isClosed(c.status)) return undefined
  return storyWaitsOn(awaiting.value.filter((a) => a.parent === c.number)) || undefined
}

const cols = reactive<Record<CardStatus, Column>>(emptyColumns())
// The rows a column had asked for before a reveal paged it; the live refresh keeps reading as many rows as `want`, so the reveal's pages are given back when the search ends.
const wantBeforeReveal = new Map<CardStatus, number>()
const error = ref<unknown>(null)

// The columns of the whole board come from the card cache, which keeps them
// current from live events. With a story picked they are that story's
// children, read directly.
async function fetchColumn(s: CardStatus, more: boolean, force: boolean): Promise<Pick<StatusList, 'items' | 'next' | 'remaining'>> {
  const c = cols[s]
  if (!storyFilter.value) return more ? cardCache.readMore(props.slug, s) : cardCache.readStatus(props.slug, s, force, c.want)
  // A refresh re-reads as many rows as are shown so "load more" survives it.
  const limit = more ? PAGE : Math.min(MAX_PAGE, Math.max(PAGE, c.items.length))
  const page = await api.cards(props.slug, { status: s, parent: storyFilter.value, limit, cursor: more ? c.next : undefined })
  return { items: more ? [...c.items, ...page.items] : page.items, next: page.next ?? '', remaining: page.remaining }
}

// Shows what the cache holds for each column, so a visit starts from it.
function syncFromCache(): void {
  if (storyFilter.value) return
  const lists = cardCache.listsOf(props.slug)
  for (const s of COLUMNS) {
    const list = lists?.get(s)
    if (!list) continue
    const c = cols[s]
    c.items = list.items
    c.next = list.next
    c.remaining = list.remaining
    c.loaded = true
  }
}

// Each column is its own keyset-paged list, loaded when it is open.
async function loadColumn(s: CardStatus, more = false, force = false): Promise<void> {
  const c = cols[s]
  if (c.busy) return
  c.busy = true
  const slug = props.slug
  try {
    const page = await fetchColumn(s, more, force)
    if (slug !== props.slug) return
    c.items = page.items
    c.next = page.next
    c.remaining = page.remaining
    c.loaded = true
    if (more) c.want = page.items.length
    error.value = null
  } catch (e) {
    error.value = e
  } finally {
    c.busy = false
  }
}

function loadColumns(force: boolean): void {
  for (const s of shown.value) if (!cols[s].collapsed) void loadColumn(s, false, force)
  for (const s of shown.value) if (cols[s].collapsed && cols[s].loaded) void loadColumn(s, false, force)
}

function loadOpen(force = false): void {
  void loadStories(force)
  loadColumns(force)
}

watch(() => cardCache.listsOf(props.slug), syncFromCache)

// Live changes re-read the open and the loaded collapsed columns as far as
// they were paged; with a story picked the board reads its lists itself.
const need: cardCache.Need = {
  rows: (s) => (!storyFilter.value && shown.value.includes(s) && (!cols[s].collapsed || cols[s].loaded) ? cols[s].want : 0),
  details: false,
}
watch(
  () => props.slug,
  (slug, _, onCleanup) => onCleanup(cardCache.holdProject(slug, need)),
  { immediate: true },
)

function toggle(s: CardStatus): void {
  const c = cols[s]
  c.collapsed = !c.collapsed
  if (!c.collapsed && !c.loaded) void loadColumn(s)
}

function reset(): void {
  Object.assign(cols, emptyColumns())
  wantBeforeReveal.clear()
}

// Keyboard focus walks the tiles as drawn, so story filters, hidden and
// collapsed columns and "load more" need no bookkeeping here. Enter on a
// focused tile is its click and opens the panel.
const board = ref<HTMLElement | null>(null)

function tilesOf(col: Element): HTMLElement[] {
  return [...col.querySelectorAll<HTMLElement>('[data-card]')]
}

function moveFocus(dx: number, dy: number): void {
  const cols = [...(board.value?.querySelectorAll('[data-col]') ?? [])].filter((c) => tilesOf(c).length)
  if (!cols.length) return
  const active = document.activeElement
  const cur = active instanceof HTMLElement ? active.closest<HTMLElement>('[data-card]') : null
  const ci = cur ? cols.findIndex((c) => c.contains(cur)) : -1
  if (!cur || ci < 0) {
    tilesOf(cols[0]!)[0]?.focus()
    return
  }
  const row = tilesOf(cols[ci]!).indexOf(cur)
  const col = cols[Math.max(0, Math.min(cols.length - 1, ci + dx))]!
  const tiles = tilesOf(col)
  tiles[Math.max(0, Math.min(tiles.length - 1, row + dy))]?.focus()
}

// The search is kept in the URL query like the Map's, so a search can be shared.
const route = useRoute()
const search = computed(() => (typeof route.query.q === 'string' ? route.query.q : ''))

function setSearch(q: string): void {
  const { q: _, ...rest } = route.query
  void router.replace({ query: q ? { ...rest, q } : rest })
}

const searchBox = ref<{ focus: () => void } | null>(null)
const hiddenChip = ref<{ focus: () => void } | null>(null)

// Tiles as drawn: shown, open columns, with the picked story above its children.
const drawn = computed(() => new Set(shown.value.filter((s) => !cols[s].collapsed).flatMap((s) => visible(s).map((c) => c.number))))
const matching = computed(() => {
  const matches = titleMatcher(search.value)
  if (!matches) return []
  return shown.value.filter((s) => !cols[s].collapsed).flatMap((s) => visible(s).filter(matches).map((c) => String(c.number)))
})

function scrollTo(id: string): void {
  void nextTick(() => {
    const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
    board.value?.querySelector(`[data-card="${id}"]`)?.scrollIntoView({ block: 'center', inline: 'center', behavior: reduce ? 'auto' : 'smooth' })
  })
}

const cursor = useSearchCursor(() => search.value, () => matching.value, (id) => drawn.value.has(Number(id)), scrollTo, false)
const hits = computed(() => new Set(cursor.matchList.value))

const found = useFoundCards(() => props.slug, () => search.value, () => shown.value)
const hiddenHits = useHiddenHits(found.found, (c) => boardReason(c, { hidden: hidden.value, collapsed: (s) => cols[s].collapsed, story: storyFilter.value, drawn: (x) => drawn.value.has(x.number) }))

watch(search, (q) => {
  if (q) return
  for (const [s, want] of wantBeforeReveal) cols[s].want = want
  wantBeforeReveal.clear()
})

// Waits for the column's read in progress, if any.
async function settled(s: CardStatus): Promise<void> {
  if (cols[s].busy) await new Promise<void>((done) => watch(() => cols[s].busy, () => done(), { once: true }))
}

// Undoes what keeps the card off the board, pages its column until the tile is there, then moves to it. A card past the pages limit opens in the panel.
async function revealCard(c: CardBrief): Promise<void> {
  const s = c.status
  if (storyFilter.value && c.number !== storyFilter.value && c.parent !== storyFilter.value) {
    storyFilter.value = 0
    // The story watch resets the columns, which would fold a column opened before it.
    await nextTick()
  }
  if (hidden.value.includes(s)) toggleVisible(s)
  if (cols[s].collapsed) toggle(s)
  await settled(s)
  for (let i = 0; i < REVEAL_PAGES && cols[s].loaded && !drawn.value.has(c.number) && cols[s].next && cols[s].remaining > 0; i++) {
    if (!wantBeforeReveal.has(s)) wantBeforeReveal.set(s, cols[s].want)
    await loadColumn(s, true)
    await settled(s)
  }
  if (drawn.value.has(c.number)) {
    cursor.show(String(c.number))
    await nextTick()
    const tile = board.value?.querySelector<HTMLElement>(`[data-card="${c.number}"]`)
    if (tile) tile.focus({ preventScroll: true })
    else hiddenChip.value?.focus()
  } else panel.open(props.slug, c.number)
}

useKeymap([
  { keys: '/', label: 'Search the board', group: 'Board', run: () => searchBox.value?.focus() },
  { keys: 'Escape', label: 'Clear the search', group: 'Board', run: () => search.value && setSearch(''), layers: ['page'] },
  { keys: 'j', label: 'Next card in the column', group: 'Board', run: () => moveFocus(0, 1), layers: ['page'] },
  { keys: 'k', label: 'Previous card in the column', group: 'Board', run: () => moveFocus(0, -1), layers: ['page'] },
  { keys: 'h', label: 'Column to the left', group: 'Board', run: () => moveFocus(-1, 0), layers: ['page'] },
  { keys: 'l', label: 'Column to the right', group: 'Board', run: () => moveFocus(1, 0), layers: ['page'] },
  { keys: 'Enter', label: 'Open the focused card', group: 'Board', run: () => undefined, layers: [] },
])

// The action keys work on the focused tile; the panel binds its own while open.
useCardActionKeys('page', () => {
  const active = document.activeElement
  const tile = active instanceof HTMLElement && board.value?.contains(active) ? active.closest<HTMLElement>('[data-card]') : null
  return tile ? { slug: props.slug, number: Number(tile.dataset.card) } : null
})

// Right-click, the ContextMenu key or Shift+F10 on a tile. The keyboard
// variants report no pointer position, so the menu opens under the tile.
const router = useRouter()
const menu = ref<{ card: CardBrief; x: number; y: number; anchor: HTMLElement | null } | null>(null)

function openMenu(e: MouseEvent, card: CardBrief): void {
  const anchor = e.currentTarget instanceof HTMLElement ? e.currentTarget : null
  let x = e.clientX
  let y = e.clientY
  if (!x && !y && anchor) {
    const r = anchor.getBoundingClientRect()
    x = r.left
    y = r.bottom
  }
  menu.value = { card, x, y, anchor }
}

function openPage(n: number): void {
  void router.push({ name: 'card', params: { slug: props.slug, number: n } })
}

// The cache applies live events to the whole board; a story's children are
// read directly, so those columns reload shortly after any new event.
useEventRefresh({
  slug: () => props.slug,
  source: () => [props.slug],
  reload: () => {
    if (storyFilter.value) loadColumns(true)
  },
  delay: 400,
})

onMounted(() => {
  syncFromCache()
  loadOpen()
})
watch(
  () => props.slug,
  () => {
    reset()
    storyFilter.value = 0
    awaiting.value = []
    replied.value = []
    syncFromCache()
    loadOpen()
  },
)
watch(storyFilter, () => {
  reset()
  syncFromCache()
  loadOpen()
})
</script>

<template>
  <div class="space-y-4">
    <ErrorBanner :error="error" :retry="() => loadOpen(true)" />
    <RunningNow :slug="slug" />
    <AwaitingYou :slug="slug" @open="panel.open" @project="awaiting = $event" />
    <AnsweredForYou @open="panel.open" />
    <Replies :slug="slug" @open="openPanel" @cards="replied = $event" />
    <div class="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
      <BoardMapSwitch :slug="slug" current="board" />
      <div class="flex items-center gap-2">
        <label for="story-filter" class="text-xs font-medium text-muted">Story</label>
        <select id="story-filter" v-model.number="storyFilter" class="field w-56 min-[40rem]:w-72">
          <option :value="0">All</option>
          <option v-for="c in openStories" :key="c.number" :value="c.number">S#{{ c.number }} {{ c.title }}</option>
        </select>
      </div>
      <div class="flex flex-wrap items-center gap-2" role="group" aria-label="Columns">
        <span class="text-xs font-medium text-muted">Columns</span>
        <div class="cells cells-toggle">
          <button
            v-for="s in COLUMNS"
            :key="s"
            type="button"
            class="cell capitalize"
            :aria-pressed="!hidden.includes(s)"
            @click="toggleVisible(s)"
          >
            {{ s }}
          </button>
        </div>
      </div>
      <div class="relative ml-auto" role="search" aria-label="Board search">
        <SearchBox ref="searchBox" :search="search" @search="setSearch" @next="cursor.nextMatch" />
        <!-- Sits under the search box, so it reads as the box's own line. -->
        <div class="pointer-events-none absolute top-full right-0 z-20 mt-1.5 flex w-[min(26rem,calc(100vw-2rem))] flex-col items-end gap-1.5">
          <p class="text-xs text-muted tabular" aria-live="polite">
            <span v-if="search && (cursor.matchList.value.length || !hiddenHits.length)" class="float px-2 py-0.5">{{ cursor.matchList.value.length ? `${cursor.current.value + 1} of ${cursor.matchList.value.length}` : 'No matches' }}</span>
          </p>
          <HiddenHits ref="hiddenChip" :hits="hiddenHits" what="not shown" :none="!cursor.matchList.value.length" @pick="revealCard" />
        </div>
      </div>
    </div>
    <div ref="board" class="relative flex items-start gap-4 overflow-x-auto pb-3">
      <section
        v-for="s in shown"
        :key="s"
        class="shrink-0"
        :class="cols[s].collapsed ? 'w-32' : 'w-64'"
        :aria-label="s"
        :data-col="s"
      >
        <button
          type="button"
          class="flex h-9 w-full items-center gap-2 border-b-2 border-fg text-left disabled:cursor-default"
          :aria-expanded="COLLAPSIBLE.includes(s) ? !cols[s].collapsed : undefined"
          :disabled="!COLLAPSIBLE.includes(s)"
          @click="toggle(s)"
        >
          <span class="swatch" :style="{ '--c': `var(--color-st-${s})` }" aria-hidden="true"></span>
          <span class="font-semibold capitalize">{{ s }}</span>
          <span v-if="cols[s].loaded" class="font-mono text-sm text-muted">{{
            cols[s].remaining > 0 ? `${cols[s].items.length}+` : cols[s].items.length
          }}</span>
          <span v-if="COLLAPSIBLE.includes(s)" class="ml-auto text-muted" :title="cols[s].collapsed ? 'Show' : 'Hide'">
            <component :is="cols[s].collapsed ? IconChevronRight : IconChevronDown" class="h-4 w-4" />
            <span class="sr-only">{{ cols[s].collapsed ? 'Show' : 'Hide' }}</span>
          </span>
        </button>
        <div v-if="!cols[s].collapsed" class="mt-2 space-y-2">
          <button v-if="s === 'ready' && canIn(slug, 'member')" type="button" class="btn btn-quiet w-full justify-start text-muted" @click="adding = true"><IconPlus />Add card</button>
          <div v-if="!cols[s].loaded && !error" class="space-y-2" role="status">
            <span class="sr-only">Loading {{ s }}…</span>
            <div v-for="i in 3" :key="i" class="skeleton h-16"></div>
          </div>
          <CardTile
            v-for="c in visible(s)"
            :key="c.number"
            :card="c"
            :slug="slug"
            :stories="storyTitles"
            :needs="needsOf(c)"
            :new-reply="replied.includes(c.number)"
            :hit="hits.has(String(c.number))"
            @open="openPanel"
            @contextmenu.prevent="openMenu($event, c)"
          />
          <p v-if="cols[s].loaded && !visible(s).length" class="py-2 text-sm text-muted">{{ EMPTY[s] }}</p>
          <LoadMore
            class="btn-quiet w-full justify-center"
            :has-more="!!cols[s].next && cols[s].remaining > 0"
            :loading="cols[s].busy"
            :remaining="cols[s].remaining"
            @load="loadColumn(s, true)"
          />
        </div>
      </section>
    </div>
    <ContextMenu
      v-if="menu"
      :key="menu.card.number"
      :card="menu.card"
      :slug="slug"
      :x="menu.x"
      :y="menu.y"
      :anchor="menu.anchor"
      @open="openPanel"
      @page="openPage"
      @close="menu = null"
    />
    <NewCardPanel v-if="adding" :slug="slug" @created="created" @close="adding = false" />
    <CardPanel v-if="panel.number.value" :slug="panel.slug.value" :number="panel.number.value" :page-slug="slug" @close="panel.close()" />
  </div>
</template>
