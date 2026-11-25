import { nextTick, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import type { MapLayout } from '../lib/map/layout'
import type { GraphModel } from '../lib/map/types'
import type { CardBrief } from '../lib/types'

/** Layouts start at least this far apart, so pages arriving in a stream lay out twice a second at most. */
export const RELAYOUT_MIN_MS = 500
/** How long cards glide to their new places; the CSS transition on the nodes uses the same time. */
export const GLIDE_MS = 200

interface Options {
  model: () => GraphModel
  needs: (card: CardBrief) => boolean
  measure: () => { width: number; height: number }
  fit: (animate: boolean) => void
  /** True while the viewer pans, drags or hovers; a new layout waits until it is false. */
  holding: () => boolean
  /** True once the viewer has moved the map; until then every layout is fitted, so pages arriving keep the whole map in view. */
  touched: () => boolean
}

/**
 * Lays the model out with ELK whenever it changes, at most twice a second, and
 * never while the viewer is holding the map still. Only the newest run may
 * publish. The first layout after `reset`, the first one that has cards after
 * an empty one, and every one until the viewer moves the map, fit the map to
 * the pane; later ones keep the viewport.
 * `gliding` is true while the cards move to their new places.
 */
export function useMapLayout(layout: Ref<MapLayout | null>, o: Options) {
  const laying = ref(false)
  const gliding = ref(false)
  const reduceMotion = typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : null
  let run = 0
  let glideTimer: ReturnType<typeof setTimeout> | undefined
  let fitNext = true
  let startedAt = 0
  let dirty = false

  // ELK, its worker and the layout code are fetched while the first pages are
  // still on the way, so the first layout does not wait for them.
  const modules = Promise.all([import('../lib/map/layout'), import('../lib/map/elk')]).then(([{ layoutMap }, { workerEngine }]) => ({ layoutMap, engine: workerEngine() }))

  async function relayout(): Promise<void> {
    const mine = ++run
    laying.value = true
    // An empty map lays out at once and does not hold back the first page.
    startedAt = o.model().shown ? Date.now() : 0
    const { width, height } = o.measure()
    const { layoutMap, engine } = await modules
    try {
      const next = await layoutMap(o.model(), { engine, aspect: width / Math.max(height, 1), needs: o.needs })
      if (mine !== run) return
      // The viewer took hold while ELK ran; the cards must not move under them, so this result is dropped and the layout runs again on release.
      if (o.holding()) {
        dirty = true
        return
      }
      const glides = !!layout.value?.nodes.length && !reduceMotion?.matches
      if (glides) gliding.value = true
      layout.value = next
      await nextTick()
      // Drawing a big map can take longer than the glide; the time runs from the moment the new places are in the page.
      if (glides) {
        clearTimeout(glideTimer)
        glideTimer = setTimeout(() => (gliding.value = false), GLIDE_MS)
      }
      if ((fitNext || !o.touched()) && next.nodes.length) {
        fitNext = false
        o.fit(false)
      } else if (!next.nodes.length) {
        fitNext = true
      }
    } finally {
      if (mine === run) {
        laying.value = false
        if (dirty && !o.holding()) schedule()
      }
    }
  }

  let timer: ReturnType<typeof setTimeout> | undefined
  function schedule(): void {
    dirty = true
    if (timer || laying.value) return
    timer = setTimeout(start, Math.max(0, startedAt + RELAYOUT_MIN_MS - Date.now()))
  }

  function start(): void {
    timer = undefined
    if (o.holding()) return
    dirty = false
    void relayout()
  }

  // Immediate: a map opened from the cache has its whole model at once and may never change after.
  watch(o.model, schedule, { flush: 'post', immediate: true })
  watch(o.holding, (held) => {
    if (!held && dirty) schedule()
  })

  /** Drops the layout, so the next one fits the pane again. */
  function reset(): void {
    layout.value = null
    fitNext = true
  }

  onBeforeUnmount(() => {
    clearTimeout(timer)
    clearTimeout(glideTimer)
    run++
  })

  return { laying, gliding, reset }
}
