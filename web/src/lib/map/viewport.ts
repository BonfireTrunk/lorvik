import type { PlacedNode } from './elkGraph'

export const MAP_FLOW_ID = 'lorvik-map'

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}

export interface View {
  x: number
  y: number
  zoom: number
}

export const MIN_ZOOM = 0.06
export const MAX_ZOOM = 2.5

/** The viewport that shows `rect` centred in a `width` by `height` pane, with `pad` of the pane left around it. */
export function fitRect(rect: Rect, width: number, height: number, pad = 0.04, maxZoom = 1.15): View {
  const zoom = Math.max(MIN_ZOOM, Math.min(maxZoom, (width * (1 - pad * 2)) / rect.w, (height * (1 - pad * 2)) / rect.h))
  return { zoom, x: (width - rect.w * zoom) / 2 - rect.x * zoom, y: (height - rect.h * zoom) / 2 - rect.y * zoom }
}

export function boundsOf(nodes: PlacedNode[]): Rect {
  if (!nodes.length) return { x: 0, y: 0, w: 1, h: 1 }
  const x0 = Math.min(...nodes.map((n) => n.ax))
  const y0 = Math.min(...nodes.map((n) => n.ay))
  const x1 = Math.max(...nodes.map((n) => n.ax + n.w))
  const y1 = Math.max(...nodes.map((n) => n.ay + n.h))
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 }
}

/** The viewport that centres the node's rectangle at `zoom`. */
export function centreOn(n: PlacedNode, width: number, height: number, zoom: number): View {
  return { zoom, x: width / 2 - (n.ax + n.w / 2) * zoom, y: height / 2 - (n.ay + n.h / 2) * zoom }
}

export interface Wheel {
  dx: number
  dy: number
  /** WheelEvent.deltaMode: 0 pixels, 1 lines, 2 pages. */
  mode: number
  zoom: boolean
  shift: boolean
}

const LINE_PX = 20
const ZOOM_PER_PX = 0.01
const MAX_ZOOM_STEP = 0.35

/**
 * The viewport after one wheel event at (`px`, `py`) in a pane `page` pixels
 * tall, read the way design tools read it: scrolling pans, so a touchpad
 * moves the map with two fingers; ctrl or cmd with the wheel zooms at the
 * pointer, and so does a pinch, which browsers report as a ctrl wheel; shift
 * turns a mouse wheel sideways. One step is capped so a mouse notch zooms
 * about as far as the + button.
 */
export function wheelView(view: View, w: Wheel, px: number, py: number, page: number): View {
  const unit = w.mode === 1 ? LINE_PX : w.mode === 2 ? page : 1
  if (w.zoom) {
    const step = Math.max(-MAX_ZOOM_STEP, Math.min(MAX_ZOOM_STEP, -w.dy * unit * ZOOM_PER_PX))
    const zoom = Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, view.zoom * 2 ** step))
    const k = zoom / view.zoom
    return { zoom, x: px - (px - view.x) * k, y: py - (py - view.y) * k }
  }
  let dx = w.dx * unit
  let dy = w.dy * unit
  if (w.shift && !dx) [dx, dy] = [dy, 0]
  return { zoom: view.zoom, x: view.x - dx, y: view.y - dy }
}

/** The smallest pan that brings `rect` fully into a `width` by `height` pane with `margin` to spare, or null when it already is. A rect larger than the pane is aligned to its top left. */
export function revealRect(rect: Rect, view: View, width: number, height: number, margin = 32): View | null {
  const shift = (start: number, size: number, offset: number, extent: number): number => {
    const lo = start * view.zoom + offset
    const hi = lo + size * view.zoom
    if (lo < margin) return margin - lo
    if (hi > extent - margin) return Math.max(margin - lo, extent - margin - hi)
    return 0
  }
  const dx = shift(rect.x, rect.w, view.x, width)
  const dy = shift(rect.y, rect.h, view.y, height)
  return dx || dy ? { zoom: view.zoom, x: view.x + dx, y: view.y + dy } : null
}
