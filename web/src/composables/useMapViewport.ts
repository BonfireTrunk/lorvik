import { computed, onBeforeUnmount, onMounted, ref, type Ref } from 'vue'
import { useVueFlow } from '@vue-flow/core'
import type { MapLayout } from '../lib/map/layout'
import { MAP_FLOW_ID, MAX_ZOOM, MIN_ZOOM, boundsOf, centreOn, fitRect, revealRect, wheelView } from '../lib/map/viewport'

const FIT_INSET = 64
const WHEEL_IDLE_MS = 250

/** The map's pan and zoom over the pane element: measuring it, fitting, revealing a focused node, the wheel, and the zoom buttons. */
export function useMapViewport(pane: Ref<HTMLElement | null>, layout: Ref<MapLayout | null>) {
  const flow = useVueFlow(MAP_FLOW_ID)
  const bounds = computed(() => boundsOf(layout.value?.nodes ?? []))
  const view = computed(() => flow.viewport.value)
  const paneSize = ref({ width: 1, height: 1 })

  /** True while the viewer drags the map or scrolls it, and for a moment after the last wheel tick. */
  const moving = ref(false)
  /** Whether the viewer has scrolled, clicked or moved focus on the map since it was last reset; until then each layout is fitted. */
  const touched = ref(false)
  let dragging = false
  let wheeling: ReturnType<typeof setTimeout> | undefined
  // Vue Flow starts a move on every press on the pane but ends it only when the view changed, so a
  // click that does not pan (opening a box, say) would hold every later layout; the press ending ends it too.
  function endMove(): void {
    removeEventListener('pointerup', endMove)
    removeEventListener('pointercancel', endMove)
    dragging = false
    moving.value = !!wheeling
  }
  flow.onMoveStart(() => {
    dragging = true
    moving.value = true
    addEventListener('pointerup', endMove)
    addEventListener('pointercancel', endMove)
  })
  flow.onMoveEnd(endMove)

  function measure(): { width: number; height: number } {
    const el = pane.value
    if (el) paneSize.value = { width: el.clientWidth, height: el.clientHeight }
    return paneSize.value
  }

  function fit(animate = true): void {
    const { width, height } = measure()
    void flow.setViewport(fitRect(bounds.value, width, height - FIT_INSET), { duration: animate ? 300 : 0 })
  }

  function reveal(e: FocusEvent): void {
    const id = e.target instanceof Element ? e.target.closest('.vue-flow__node')?.getAttribute('data-id') : null
    const n = id ? layout.value?.nodes.find((x) => x.id === id) : null
    if (!n) return
    touched.value = true
    const { width, height } = measure()
    const next = revealRect({ x: n.ax, y: n.ay, w: n.w, h: n.h }, view.value, width, height)
    if (next) void flow.setViewport(next, { duration: 150 })
  }

  function onWheel(e: WheelEvent): void {
    if (!(e.target instanceof Element) || !e.target.closest('.vue-flow') || !pane.value) return
    e.preventDefault()
    touched.value = true
    moving.value = true
    clearTimeout(wheeling)
    wheeling = setTimeout(() => {
      wheeling = undefined
      moving.value = dragging
    }, WHEEL_IDLE_MS)
    const box = pane.value.getBoundingClientRect()
    const w = { dx: e.deltaX, dy: e.deltaY, mode: e.deltaMode, zoom: e.ctrlKey || e.metaKey, shift: e.shiftKey }
    void flow.setViewport(wheelView(view.value, w, e.clientX - box.left, e.clientY - box.top, box.height))
  }

  function zoomBy(factor: number): void {
    void flow.zoomTo(Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, view.value.zoom * factor)), { duration: 150 })
  }

  function centre(id: string): void {
    const n = layout.value?.nodes.find((x) => x.id === id)
    if (!n) return
    const { width, height } = measure()
    void flow.setViewport(centreOn(n, width, height, Math.max(view.value.zoom, 0.85)), { duration: 300 })
  }

  function panTo(x: number, y: number): void {
    const { width, height } = measure()
    void flow.setViewport({ zoom: view.value.zoom, x: width / 2 - x * view.value.zoom, y: height / 2 - y * view.value.zoom }, { duration: 150 })
  }

  let resize: ResizeObserver | undefined
  onMounted(() => {
    if (pane.value) {
      // The pane changes height when the strips above it come and go; a map the viewer has not moved is fitted again.
      resize = new ResizeObserver(() => {
        const was = paneSize.value
        const now = measure()
        if (!touched.value && layout.value?.nodes.length && (now.width !== was.width || now.height !== was.height)) fit(false)
      })
      resize.observe(pane.value)
    }
  })
  onBeforeUnmount(() => {
    resize?.disconnect()
    clearTimeout(wheeling)
    removeEventListener('pointerup', endMove)
    removeEventListener('pointercancel', endMove)
  })

  return { flow, bounds, view, paneSize, moving, touched, measure, fit, reveal, onWheel, zoomBy, centre, panTo }
}
