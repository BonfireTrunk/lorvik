<script setup lang="ts">
import { computed } from 'vue'
import { useMapFocus } from '../../composables/useMapFocus'
import type { PlacedEdge } from '../../lib/map/elkGraph'
import { midpoint, roundedPath, withArrow } from '../../lib/map/path'
import { isUndirected } from '../../lib/map/types'

const props = defineProps<{ edge: PlacedEdge }>()
const focus = useMapFocus()

const shape = computed(() => {
  if (isUndirected(props.edge.kind) || props.edge.kind === 'mention') return { d: roundedPath(props.edge.points), head: '' }
  const { line, head } = withArrow(props.edge.points)
  return { d: roundedPath(line), head }
})
const dim = computed(() => !!focus.litEdges.value && !focus.litEdges.value.has(props.edge.id))
const lit = computed(() => !!focus.litEdges.value?.has(props.edge.id))
const label = computed(() => {
  const count = props.edge.merged?.count
  if (!count) return null
  const text = String(count)
  const w = text.length * 8 + 14
  const at = midpoint(props.edge.points)
  return { text, w, x: at.x, y: at.y }
})
const pick = (): void => {
  if (props.edge.merged) focus.picked.value = props.edge.id
}
</script>

<template>
  <g class="map-edge" :class="[`map-edge-${edge.kind}`, { 'map-dim': dim, 'map-lit': lit, 'map-edge-merged': !!edge.merged }]">
    <path :d="roundedPath(edge.points)" class="map-edge-hit" @mouseenter="focus.hoverEdge.value = edge.id" @mouseleave="focus.hoverEdge.value = ''" @click="pick" />
    <path :d="shape.d" class="map-edge-line" />
    <path v-if="shape.head" :d="shape.head" class="map-edge-head" />
    <g v-if="label" class="map-edge-label" role="button" tabindex="0" :data-edge-label="edge.id" :aria-label="`${label.text} ${edge.kind} links. List them`" @keydown.enter.prevent="pick" @keydown.space.prevent="pick" @mouseenter="focus.hoverEdge.value = edge.id" @mouseleave="focus.hoverEdge.value = ''" @click="pick">
      <rect :x="label.x - label.w / 2" :y="label.y - 10" :width="label.w" height="20" rx="10" />
      <text :x="label.x" :y="label.y">{{ label.text }}</text>
    </g>
  </g>
</template>
