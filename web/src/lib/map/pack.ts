export interface Box {
  w: number
  h: number
}

export interface Pack {
  places: { x: number; y: number }[]
  w: number
  h: number
}

/** The packing width is tried from MIN to MAX times the square-root-of-area estimate, in STEP increments. */
const WIDTH_FACTOR_MIN = 0.5
const WIDTH_FACTOR_MAX = 2.2
const WIDTH_FACTOR_STEP = 0.05
/** Divides the packed area in the score, so a tighter pack wins a near tie on aspect. */
const AREA_PENALTY_DIVISOR = 40

/**
 * Skyline packing: boxes, tallest first, each go where their top is nearest the canvas's top within `width`,
 * leftmost on a tie. Short boxes then stack in the space beside a tall one, which rows of shelves leave empty.
 * Places come back in input order.
 */
export function skylinePack(boxes: Box[], width: number, gap: number): Pack {
  const order = boxes.map((_, i) => i).sort((a, b) => boxes[b]!.h - boxes[a]!.h)
  const places = boxes.map(() => ({ x: 0, y: 0 }))
  // Left to right, where each stretch of the canvas starts and the first free y below what is packed there; the last runs on.
  let sky: { x: number; y: number }[] = [{ x: 0, y: 0 }]
  const floorAt = (x: number): number => sky.filter((s) => s.x <= x).pop()!.y
  let maxX = 0
  let maxY = 0
  for (const i of order) {
    const b = boxes[i]!
    const end = (x: number): number => x + b.w + gap
    let best: { x: number; y: number } | undefined
    for (let j = 0; j < sky.length; j++) {
      const x = sky[j]!.x
      if (x > 0 && x + b.w > width) break
      let y = 0
      for (let k = j; k < sky.length && sky[k]!.x < end(x); k++) y = Math.max(y, sky[k]!.y)
      if (!best || y < best.y) best = { x, y }
    }
    const { x, y } = best!
    places[i] = { x, y }
    maxX = Math.max(maxX, x + b.w)
    maxY = Math.max(maxY, y + b.h)
    const right = end(x)
    const after = sky.filter((s) => s.x >= right)
    const next = [...sky.filter((s) => s.x < x), { x, y: y + b.h + gap }, ...(after[0]?.x === right ? [] : [{ x: right, y: floorAt(right) }]), ...after]
    sky = next.filter((s, k) => k === 0 || s.y !== next[k - 1]!.y)
  }
  return { places, w: maxX, h: maxY }
}

/**
 * The packing width whose result reads closest to `aspect` (width over height);
 * ties go to the smaller area.
 */
export function packToAspect(boxes: Box[], gap: number, aspect: number): Pack {
  if (!boxes.length) return { places: [], w: 0, h: 0 }
  const maxW = Math.max(...boxes.map((b) => b.w))
  const area = boxes.reduce((s, b) => s + (b.w + gap) * (b.h + gap), 0)
  let best: Pack | undefined
  let bestScore = Infinity
  for (let f = WIDTH_FACTOR_MIN; f <= WIDTH_FACTOR_MAX; f += WIDTH_FACTOR_STEP) {
    const pack = skylinePack(boxes, Math.max(maxW, Math.sqrt(area * aspect) * f), gap)
    const score = Math.abs(Math.log(pack.w / pack.h / aspect)) + (pack.w * pack.h) / (area * AREA_PENALTY_DIVISOR)
    if (score < bestScore) {
      best = pack
      bestScore = score
    }
  }
  return best!
}
