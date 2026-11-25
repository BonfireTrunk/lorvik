import type { Pt } from './elkGraph'

/** The point halfway along a polyline, by length. */
export function midpoint(points: Pt[]): Pt {
  const total = points.slice(1).reduce((n, p, i) => n + Math.hypot(p.x - points[i]!.x, p.y - points[i]!.y), 0)
  let left = total / 2
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1]!
    const b = points[i]!
    const len = Math.hypot(b.x - a.x, b.y - a.y)
    if (left <= len && len > 0) return { x: a.x + ((b.x - a.x) * left) / len, y: a.y + ((b.y - a.y) * left) / len }
    left -= len
  }
  return points[points.length - 1] ?? { x: 0, y: 0 }
}

/** A polyline with each corner rounded by up to `radius`. */
export function roundedPath(points: Pt[], radius = 7): string {
  if (points.length < 2) return ''
  let d = `M${points[0]!.x} ${points[0]!.y}`
  for (let i = 1; i < points.length - 1; i++) {
    const a = points[i - 1]!
    const b = points[i]!
    const c = points[i + 1]!
    const inLen = Math.hypot(b.x - a.x, b.y - a.y)
    const outLen = Math.hypot(c.x - b.x, c.y - b.y)
    const r = Math.min(radius, inLen / 2, outLen / 2)
    if (r < 0.5) {
      d += ` L${b.x} ${b.y}`
      continue
    }
    const p1 = { x: b.x - ((b.x - a.x) / inLen) * r, y: b.y - ((b.y - a.y) / inLen) * r }
    const p2 = { x: b.x + ((c.x - b.x) / outLen) * r, y: b.y + ((c.y - b.y) / outLen) * r }
    d += ` L${p1.x} ${p1.y} Q${b.x} ${b.y} ${p2.x} ${p2.y}`
  }
  const last = points[points.length - 1]!
  return `${d} L${last.x} ${last.y}`
}

/** The line shortened by `by` at its end, and the arrowhead triangle that fills the gap. */
export function withArrow(points: Pt[], by = 9, half = 4.5): { line: Pt[]; head: string } {
  const end = points[points.length - 1]!
  let i = points.length - 2
  while (i > 0 && Math.hypot(end.x - points[i]!.x, end.y - points[i]!.y) < 0.5) i--
  const prev = points[i]!
  const len = Math.hypot(end.x - prev.x, end.y - prev.y) || 1
  const ux = (end.x - prev.x) / len
  const uy = (end.y - prev.y) / len
  const cut = Math.min(by, len)
  const base = { x: end.x - ux * cut, y: end.y - uy * cut }
  const line = [...points.slice(0, i + 1), base]
  const head = `M${end.x} ${end.y} L${base.x - uy * half} ${base.y + ux * half} L${base.x + uy * half} ${base.y - ux * half}Z`
  return { line, head }
}
