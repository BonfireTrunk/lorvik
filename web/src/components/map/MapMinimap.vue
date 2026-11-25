<script setup lang="ts">
import { computed } from 'vue'
import type { PlacedNode } from '../../lib/map/elkGraph'
import type { Rect, View } from '../../lib/map/viewport'

// `boxes`: the keys of the closed boxes holding a card that needs the viewer.
const props = defineProps<{ nodes: PlacedNode[]; boxes: Set<string>; bounds: Rect; view: View; width: number; height: number }>()
const emit = defineEmits<{ pan: [x: number, y: number] }>()

const W = 184
const H = 116

const seen = computed<Rect>(() => ({
  x: -props.view.x / props.view.zoom,
  y: -props.view.y / props.view.zoom,
  w: props.width / props.view.zoom,
  h: props.height / props.view.zoom,
}))
const box = computed(() => {
  const a = props.bounds
  const b = seen.value
  const x0 = Math.min(a.x, b.x)
  const y0 = Math.min(a.y, b.y)
  const x1 = Math.max(a.x + a.w, b.x + b.w)
  const y1 = Math.max(a.y + a.h, b.y + b.h)
  const pad = Math.max(x1 - x0, y1 - y0) * 0.03
  return { x: x0 - pad, y: y0 - pad, w: x1 - x0 + pad * 2, h: y1 - y0 + pad * 2 }
})

// One minimap pixel in map units, so the marks keep their size at any zoom.
const px = computed(() => Math.max(box.value.w / W, box.value.h / H))
// What needs the viewer is ringed in magenta, so a card too small to see at this scale still shows where it is.
const marked = computed(() => props.nodes.filter((n) => n.needs || (n.group && props.boxes.has(n.group.key))))

function click(e: MouseEvent): void {
  const el = e.currentTarget as SVGSVGElement
  const r = el.getBoundingClientRect()
  const s = Math.min(r.width / box.value.w, r.height / box.value.h)
  const ox = (r.width - box.value.w * s) / 2
  const oy = (r.height - box.value.h * s) / 2
  emit('pan', box.value.x + (e.clientX - r.left - ox) / s, box.value.y + (e.clientY - r.top - oy) / s)
}
</script>

<template>
  <svg
    class="float absolute right-3 bottom-3 z-10 cursor-pointer overflow-hidden bg-panel"
    :width="W"
    :height="H"
    :viewBox="`${box.x} ${box.y} ${box.w} ${box.h}`"
    role="img"
    aria-label="Minimap: click to move the view"
    @click="click"
  >
    <template v-for="n in nodes" :key="n.id">
      <rect v-if="n.kind === 'story' || n.kind === 'lane'" :x="n.ax" :y="n.ay" :width="n.w" :height="n.h" rx="14" fill="none" stroke="var(--color-muted)" stroke-width="3" stroke-opacity="0.5" />
      <rect v-else :x="n.ax" :y="n.ay" :width="n.w" :height="n.h" rx="4" :fill="n.card ? `var(--color-st-${n.card.status})` : 'var(--color-muted)'" />
    </template>
    <rect
      v-for="n in marked"
      :key="`needs-${n.id}`"
      :x="n.ax - px * 3"
      :y="n.ay - px * 3"
      :width="n.w + px * 6"
      :height="n.h + px * 6"
      :rx="px * 3"
      fill="var(--color-question)"
      :fill-opacity="n.group ? 0.25 : 1"
      stroke="var(--color-question)"
      :stroke-width="px * 2"
    />
    <rect :x="seen.x" :y="seen.y" :width="seen.w" :height="seen.h" fill="var(--color-accent)" fill-opacity="0.12" stroke="var(--color-accent)" :stroke-width="box.w / W * 1.5" />
  </svg>
</template>
