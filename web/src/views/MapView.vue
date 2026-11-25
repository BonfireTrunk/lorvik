<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, watch } from 'vue'
import AnsweredForYou from '../components/AnsweredForYou.vue'
import AwaitingYou from '../components/AwaitingYou.vue'
import BoardMapSwitch from '../components/BoardMapSwitch.vue'
import CardPanel from '../components/card/CardPanel.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import HiddenHits from '../components/HiddenHits.vue'
import EdgeLinks from '../components/map/EdgeLinks.vue'
import GroupPanel from '../components/map/GroupPanel.vue'
import MapBoxHits from '../components/map/MapBoxHits.vue'
import MapCanvas from '../components/map/MapCanvas.vue'
import MapControls from '../components/map/MapControls.vue'
import MapFilters from '../components/map/MapFilters.vue'
import MapLoading from '../components/map/MapLoading.vue'
import MapMinimap from '../components/map/MapMinimap.vue'
import Replies from '../components/Replies.vue'
import { useCardPanel } from '../composables/useCardPanel'
import { useKeymap } from '../composables/useKeymap'
import { provideMapFocus } from '../composables/useMapFocus'
import { useFoundCards } from '../composables/useFoundCards'
import { useHiddenHits } from '../composables/useHiddenHits'
import { useMapBoxHits } from '../composables/useMapBoxHits'
import { useMapData } from '../composables/useMapData'
import { useMapFilters } from '../composables/useMapFilters'
import { useMapNav } from '../composables/useMapNav'
import { useMapLayout } from '../composables/useMapLayout'
import { useMapSearch } from '../composables/useMapSearch'
import { useMapViewport } from '../composables/useMapViewport'
import { countIn, fitsOpen, groupOfCard, groupSpoken } from '../lib/map/collapse'
import { flowEdges, flowNodes } from '../lib/map/flow'
import { buildGraph } from '../lib/map/graph'
import { hiddenContext, hiddenReason, hiders, revealFilters, type HiddenContext } from '../lib/map/hidden'
import type { MapLayout } from '../lib/map/layout'
import { MAP_KINDS, cardId, groupNodeId } from '../lib/map/types'
import { humanAsk } from '../lib/roles'
import type { CardBrief } from '../lib/types'
import IconArrowLeft from '~icons/lucide/arrow-left'

const props = defineProps<{ slug: string }>()

// The viewport glide to a node takes 300ms (useMapViewport centre).
const FOCUS_AFTER_MOVE_MS = 350

const f = useMapFilters()
const data = useMapData(() => props.slug, () => f.filters.value)
const panel = useCardPanel(props.slug)

const model = computed(() => buildGraph(data.source.value, f.filters.value))
// Only boxes the viewer opened can be collapsed; a map small enough to draw every card has none.
const opened = computed(() => (data.mode.value === 'collapsed' ? f.filters.value.open : []))
const focus = provideMapFocus(() => model.value.edges)

const collapsed = computed(() => data.mode.value === 'collapsed')
// Past the card lists, the pickers come from the server's boxes; the cards read so far are only the boxes opened.
const lanes = computed(() =>
  data.mode.value === 'cards'
    ? [...new Set(data.source.value.cards.map((c) => c.lane).filter((l): l is string => !!l))].sort()
    : [...new Set(data.source.value.groups.map((g) => g.lane).filter((l): l is string => !!l))].sort(),
)
const stories = computed<Pick<CardBrief, 'number' | 'title'>[]>(() =>
  data.mode.value === 'cards'
    ? data.source.value.cards.filter((c) => c.kind === 'story').sort((a, b) => b.number - a.number)
    : data.source.value.groups.flatMap((g) => (g.story ? [{ number: g.story, title: g.title ?? '' }] : [])).sort((a, b) => b.number - a.number),
)
const narrowed = computed(() => collapsed.value && (!!f.filters.value.lane || f.filters.value.kinds.length < MAP_KINDS.length))

function needs(c: CardBrief): boolean {
  return !!humanAsk(props.slug, c)
}

// Set by the Awaiting you strip, which already loads them: the closed boxes holding one are marked on the minimap.
const awaiting = ref<CardBrief[]>([])
const awaitingBoxes = computed(() => new Set(awaiting.value.filter(needs).map(groupOfCard)))

const pane = ref<HTMLElement | null>(null)
const layout = shallowRef<MapLayout | null>(null)
const nodes = computed(() => flowNodes(layout.value?.nodes ?? []))
const edges = computed(() => flowEdges(layout.value?.edges ?? []))
const { flow, bounds, view, paneSize, moving, touched, measure, fit, reveal, onWheel, zoomBy, centre, panTo } = useMapViewport(pane, layout)
const holding = computed(() => moving.value || !!focus.hoverNode.value || !!focus.hoverEdge.value)
const { laying, gliding, reset: resetLayout } = useMapLayout(layout, { model: () => model.value, needs, measure, fit, holding: () => holding.value, touched: () => touched.value })
const found = useFoundCards(() => props.slug, () => f.search.value, () => f.filters.value.statuses)
const boxHits = useMapBoxHits(found.found, () => (collapsed.value ? model.value.groups : []), () => f.filters.value.statuses)

const hiddenCtx = computed<HiddenContext>(() => hiddenContext(f.filters.value, data.source.value.mode, model.value, data.source.value.cards))
const hidden = useHiddenHits(found.found, (c) => hiddenReason(c, hiddenCtx.value))
const { matchList, current: matchAt, nextMatch, show } = useMapSearch(() => f.search.value, () => layout.value?.nodes ?? [], focus.matches, centre, () => boxHits.ids.value)
const hiddenChip = ref<{ focus: () => void } | null>(null)

// A hidden card chosen from the list: the map moves to it, or to its box, once the next layout draws one.
const REVEAL_WAIT_MS = 10_000
// After each move to it, how long a newer layout may still move the target before the map stops following it.
const REVEAL_FOLLOW_MS = 1500
let revealing: CardBrief | null = null
// Set when the strip's row asked: its link keeps focus until the card is drawn, and the map opens the box holding the card rather than stopping at it.
let locating: HTMLElement | null = null
// Where the map last moved to for this reveal. Layouts keep arriving while the turned-on statuses load and can each move the target, so the map follows it until they stop.
let revealedAt = ''
let revealTimer: ReturnType<typeof setTimeout> | undefined

function stopRevealing(): void {
  revealing = null
  locating = null
  revealedAt = ''
  clearTimeout(revealTimer)
}

function settleReveal(): void {
  const c = revealing
  if (!c || laying.value) return
  const nodes = layout.value?.nodes ?? []
  const box = groupNodeId(groupOfCard(c))
  const n = nodes.find((x) => x.id === cardId(c.number)) ?? (!locating && boxHits.ids.value.includes(box) ? nodes.find((x) => x.id === box) : undefined)
  if (!n) {
    if (locating) unhide(c)
    return
  }
  const at = `${n.id} ${n.ax} ${n.ay}`
  if (at === revealedAt) return
  const first = !revealedAt
  revealedAt = at
  clearTimeout(revealTimer)
  revealTimer = setTimeout(stopRevealing, REVEAL_FOLLOW_MS)
  if (first) show(n.id)
  else centre(n.id)
  // The pane pans a focused node into view, which would cut the move to it short; focus follows the move.
  const from = locating
  setTimeout(() => {
    if (document.activeElement && document.activeElement !== document.body && document.activeElement !== from) return
    const el = pane.value?.querySelector<HTMLElement>(`.vue-flow__node[data-id="${CSS.escape(n.id)}"] :is(a, button)`)
    if (el) el.focus({ preventScroll: true })
    else hiddenChip.value?.focus()
  }, FOCUS_AFTER_MOVE_MS)
}

watch([layout, laying, () => boxHits.ids.value], settleReveal)
watch([() => f.search.value, () => props.slug], stopRevealing)

/** Turns off only what hides the card, then moves to it once it is drawn; the move is the viewer's, so the layout does not fit the whole map over it. */
function showHidden(c: CardBrief, from: HTMLElement | null = null): void {
  f.setFilters(revealFilters(c, hiddenCtx.value))
  touched.value = true
  stopRevealing()
  revealing = c
  locating = from
  revealTimer = setTimeout(stopRevealing, REVEAL_WAIT_MS)
  settleReveal()
}

/** A row of the Awaiting you strip: the map moves to the card as for a hidden search hit, and opens the closed box holding it. */
function locate(c: CardBrief): void {
  showHidden(c, document.activeElement instanceof HTMLElement ? document.activeElement : document.body)
}

/**
 * Run on each layout until the located card is drawn. The map is judged as drawn, so the counts and boxes it
 * needs are in: a closed box holding the card opens, or shows alone past the card limit, and a filter only
 * seen once the box's cards are read ("only cards with links") is turned off.
 */
function unhide(c: CardBrief): void {
  const ctx = hiddenCtx.value
  const h = hiders(c, ctx)
  if (h.includes('unloaded')) return
  if (h.length) {
    void f.setFilters(revealFilters(c, ctx))
    return
  }
  const key = groupOfCard(c)
  if (ctx.mode !== 'collapsed' || !ctx.boxes.has(key) || ctx.filters.open.includes(key)) return
  void (fitsOpen(data.source.value.groups, ctx.filters.open, key, ctx.filters.statuses) ? f.openBox(key) : f.showGroup(key))
}

useMapNav(pane, () => layout.value?.nodes ?? [], () => model.value.edges, () => props.slug)

const search = ref<{ focusSearch: () => void } | null>(null)

const listed = computed(() => (collapsed.value ? data.source.value.groups.find((g) => g.key === f.listedKey.value) : undefined))
const listedFits = computed(() => !!listed.value && fitsOpen(data.source.value.groups, f.filters.value.open, listed.value.key, f.filters.value.statuses))
const listedShown = computed(() => (listed.value ? countIn(listed.value.counts, f.filters.value.statuses) : 0))
const pickedEdge = computed(() => model.value.edges.find((e) => e.id === focus.picked.value && e.merged))
const alone = computed(() => data.source.value.groups.find((g) => g.key === (f.filters.value.group || `story:${f.filters.value.story}`)))
const aloneName = computed(() => (alone.value ? (alone.value.kind === 'story' ? `Story #${alone.value.story} ` : '') + groupSpoken(alone.value) : ''))

/** Opens the box on the canvas when the cards drawn stay within the limit, else as a map of its own. */
function expand(key: string): void {
  const groups = data.source.value.groups
  if (fitsOpen(groups, f.filters.value.open, key, f.filters.value.statuses)) {
    f.openBox(key)
    touched.value = true
    centreWhenDrawn.value = groupNodeId(key)
  } else f.showGroup(key)
}

// The Collapse button leaves with the open box, so focus that was on it moves to the closed box once it is drawn.
const focusWhenDrawn = ref('')
function collapse(key: string): void {
  focusWhenDrawn.value = pane.value?.contains(document.activeElement) ? groupNodeId(key) : ''
  void f.closeBox(key)
}
watch(layout, (l) => {
  const id = focusWhenDrawn.value
  if (!id || !l?.nodes.some((n) => n.id === id)) return
  focusWhenDrawn.value = ''
  setTimeout(() => pane.value?.querySelector<HTMLElement>(`.vue-flow__node[data-id="${CSS.escape(id)}"] :is(a, button)`)?.focus({ preventScroll: true }), FOCUS_AFTER_MOVE_MS)
})

// A box opened from the side list or a search hit is drawn by the next layout; the map moves to it then, and the layout does not fit the whole map over that.
const centreWhenDrawn = ref('')
watch(layout, (l) => {
  if (centreWhenDrawn.value && l?.nodes.some((n) => n.id === centreWhenDrawn.value)) {
    centre(centreWhenDrawn.value)
    centreWhenDrawn.value = ''
  }
})

useKeymap([
  { keys: '/', label: 'Search the map', group: 'Map', run: () => search.value?.focusSearch() },
  { keys: 'f', label: 'Fit the whole map', group: 'Map', run: () => fit(), layers: ['page'] },
  { keys: '+', label: 'Zoom in', group: 'Map', run: () => zoomBy(1.25), layers: ['page'] },
  { keys: '-', label: 'Zoom out', group: 'Map', run: () => zoomBy(0.8), layers: ['page'] },
  {
    keys: 'Escape',
    label: 'Clear the search and hover',
    group: 'Map',
    run: () => {
      f.setSearch('')
      focus.release()
    },
    layers: ['page'],
  },
])

onMounted(() => void data.load())

watch(
  () => props.slug,
  () => {
    // The old project's tiles go without a mouseleave; a hover left on one would hold back every layout of the new project.
    focus.release()
    resetLayout()
    touched.value = false
    awaiting.value = []
    void data.load()
  },
)

const empty = computed(() => !data.loading.value && !laying.value && !!layout.value && layout.value.nodes.length === 0)
</script>

<template>
  <div class="flex h-[calc(100dvh-6.25rem)] min-h-[32rem] flex-col gap-3">
    <ErrorBanner :error="data.error.value" :retry="data.load" />
    <AwaitingYou class="max-h-[40%] shrink-0 overflow-y-auto [&_.needs-plate]:sticky [&_.needs-plate]:top-0" :slug="slug" :locate="locate" @open="panel.open" @project="awaiting = $event" />
    <AnsweredForYou class="max-h-[20%] shrink-0 overflow-y-auto" @open="panel.open" />
    <Replies class="max-h-[20%] shrink-0 overflow-y-auto" :slug="slug" @open="(n) => panel.open(slug, n)" />
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
      <BoardMapSwitch :slug="slug" current="map" />
      <button v-if="data.mode.value === 'group'" type="button" class="btn" @click="f.showGroup('')">
        <IconArrowLeft class="h-4 w-4" />Whole map<span class="sr-only">. Now showing {{ aloneName }} alone</span>
      </button>
      <MapFilters
        ref="search"
        :filters="f.filters.value"
        :search="f.search.value"
        :lanes="lanes"
        :stories="stories"
        :shown="model.shown"
        :total="data.total.value || data.source.value.cards.length"
        :busy="data.loading.value || data.fetching.value"
        @status="f.toggleStatus"
        @kind="f.toggleKind"
        @lane="f.setLane"
        @story="f.setStory"
        @linked="f.setLinked"
        @search="f.setSearch"
        @next="nextMatch"
        @refresh="data.load"
      />
    </div>
    <div class="flex min-h-0 flex-1 gap-3">
      <div ref="pane" class="panel relative min-h-0 min-w-0 flex-1 overflow-hidden" :class="{ 'map-gliding': gliding }" @pointerdown="touched = true" @focusin="reveal" @wheel="onWheel">
        <MapCanvas :nodes="nodes" :edges="edges" :slug="slug" :opened="opened" @open="(n) => panel.open(slug, n)" @group="f.setList" @collapse="collapse" />
        <MapLoading v-if="data.loading.value || data.fetching.value" :progress="data.progress.value" :blocking="data.loading.value" />
        <p v-else-if="empty" class="absolute inset-0 z-10 grid place-items-center text-sm text-muted">No cards match the filters.</p>
        <!-- Sits under the search box, so it reads as the box's own line. -->
        <div class="pointer-events-none absolute top-2 right-3 z-20 flex w-[min(26rem,calc(100%-1.5rem))] flex-col items-end gap-1.5">
          <p class="text-xs text-muted tabular" aria-live="polite">
            <span v-if="f.search.value && (matchList.length || !hidden.length)" class="float px-2 py-0.5">{{ matchList.length ? `${matchAt + 1} of ${matchList.length}` : 'No matches' }}</span>
          </p>
          <HiddenHits ref="hiddenChip" :hits="hidden" :none="!matchList.length" @pick="showHidden" />
          <MapBoxHits :slug="slug" :hits="boxHits.hits.value" @card="(n) => panel.open(slug, n)" @open="expand" />
        </div>
        <p v-if="narrowed" class="float pointer-events-none absolute bottom-3 left-1/2 z-10 -translate-x-1/2 px-2.5 py-1 text-xs text-muted">The lane and kind filters apply to boxes you open.</p>
        <EdgeLinks v-if="pickedEdge" :slug="slug" :edge="pickedEdge" @close="focus.picked.value = ''" @card="(n) => panel.open(slug, n)" />
        <MapControls :zoom="view.zoom" @zoom-in="zoomBy(1.25)" @zoom-out="zoomBy(0.8)" @fit="fit()" @actual="flow.zoomTo(1, { duration: 200 })" />
        <MapMinimap v-if="layout" :nodes="layout.nodes" :boxes="awaitingBoxes" :bounds="bounds" :view="view" :width="paneSize.width" :height="paneSize.height" @pan="panTo" />
      </div>
      <GroupPanel
        v-if="listed"
        :slug="slug"
        :group="listed"
        :statuses="f.filters.value.statuses"
        :shown="listedShown"
        :fits="listedFits"
        @close="f.setList('')"
        @expand="expand(listed.key)"
        @card="(n) => panel.open(slug, n)"
      />
    </div>
    <CardPanel v-if="panel.number.value" :slug="panel.slug.value" :number="panel.number.value" :page-slug="slug" @close="panel.close()" />
  </div>
</template>
