<script setup lang="ts">
import { computed } from 'vue'
import { VueFlow, useVueFlow, type Edge, type Node } from '@vue-flow/core'
import '@vue-flow/core/dist/style.css'
import { MAP_FLOW_ID, MAX_ZOOM, MIN_ZOOM } from '../../lib/map/viewport'
import type { EdgeData, NodeData } from '../../lib/map/flow'
import CardNode from './CardNode.vue'
import LaneNode from './LaneNode.vue'
import NoteNode from './NoteNode.vue'
import RoutedEdge from './RoutedEdge.vue'
import StoryNode from './StoryNode.vue'

/** `opened`: the boxes the viewer opened, which show a Collapse button. */
defineProps<{ nodes: Node<NodeData>[]; edges: Edge<EdgeData>[]; slug: string; opened: string[] }>()
const emit = defineEmits<{ open: [number: number]; group: [key: string]; collapse: [key: string] }>()

const { viewport } = useVueFlow(MAP_FLOW_ID)

const GAP = 28
const grid = computed(() => {
  const gap = GAP * viewport.value.zoom
  return { gap, x: viewport.value.x % gap, y: viewport.value.y % gap, r: Math.max(0.6, Math.min(1.4, viewport.value.zoom * 1.1)) }
})
</script>

<template>
  <!-- The wheel is MapView's (wheelView): Vue Flow's pan-on-scroll pans on a pinch outside macOS. -->
  <VueFlow
    :id="MAP_FLOW_ID"
    class="map-flow"
    :nodes="nodes"
    :edges="edges"
    :min-zoom="MIN_ZOOM"
    :max-zoom="MAX_ZOOM"
    :nodes-draggable="false"
    :nodes-connectable="false"
    :elements-selectable="false"
    :zoom-on-double-click="false"
    :zoom-on-scroll="false"
    :zoom-on-pinch="false"
    :pan-on-scroll="false"
    :zoom-activation-key-code="null"
    :pan-activation-key-code="null"
    :pane-click-distance="5"
    :delete-key-code="null"
    :selection-key-code="null"
    :multi-selection-key-code="null"
    :disable-keyboard-a11y="true"
  >
    <svg class="map-bg" aria-hidden="true">
      <defs>
        <pattern id="map-dots" patternUnits="userSpaceOnUse" :x="grid.x" :y="grid.y" :width="grid.gap" :height="grid.gap">
          <circle :cx="grid.gap / 2" :cy="grid.gap / 2" :r="grid.r" />
        </pattern>
      </defs>
      <rect width="100%" height="100%" fill="url(#map-dots)" />
    </svg>
    <template #node-card="{ data }"><CardNode :node="data.node" :slug="slug" @open="emit('open', $event)" /></template>
    <template #node-story="{ data }"><StoryNode :node="data.node" :slug="slug" :opened="opened" @open="emit('open', $event)" @group="emit('group', $event)" @collapse="emit('collapse', $event)" /></template>
    <template #node-note="{ data }"><NoteNode :node="data.node" :slug="slug" /></template>
    <template #node-lane="{ data }"><LaneNode :node="data.node" :opened="opened" @group="emit('group', $event)" @collapse="emit('collapse', $event)" /></template>
    <template #edge-routed="{ data }"><RoutedEdge :edge="data.edge" /></template>
  </VueFlow>
</template>

<style>
.map-flow { background: color-mix(in oklch, var(--color-sunken) 55%, var(--color-canvas)); }
.map-bg { position: absolute; inset: 0; width: 100%; height: 100%; pointer-events: none; fill: color-mix(in oklch, var(--color-muted) 38%, transparent); }
.map-handle { opacity: 0; pointer-events: none; width: 1px; height: 1px; min-width: 0; min-height: 0; border: 0; }

.map-card {
  position: relative; display: flex; flex-direction: column; overflow: hidden; text-decoration: none; color: var(--color-fg);
  background: var(--color-panel); border: 1px solid var(--color-line); border-radius: 6px;
  box-shadow: 0 1px 2px oklch(20% 0.02 260 / 0.06);
  transition: opacity 140ms, border-color 120ms, box-shadow 120ms;
}
.map-card:hover { border-color: var(--color-fg); box-shadow: 0 4px 14px -6px oklch(20% 0.02 260 / 0.35); }
.map-stripe { position: absolute; inset: 0 auto 0 0; width: 4px; background: var(--c); z-index: 1; }
.map-band {
  display: flex; align-items: center; gap: 6px; height: 24px; padding: 0 12px 0 16px; font-size: 12px; font-weight: 600;
  color: var(--color-accent-fg); background: var(--color-question);
}
.map-needs { border: 2px solid var(--color-question); }
.map-title { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }

.map-story {
  position: relative; border: 1px solid color-mix(in oklch, var(--c) 38%, var(--color-line)); border-radius: 10px;
  background: color-mix(in oklch, var(--color-panel) 62%, transparent); transition: opacity 140ms;
  box-shadow: inset 0 3px 0 var(--c);
}
.map-story-head { display: block; padding: 10px 14px 0 14px; text-decoration: none; color: var(--color-fg); border-radius: 10px 10px 0 0; }
.map-story-head:hover .truncate { text-decoration: underline; text-underline-offset: 3px; }
.map-progress { display: inline-block; width: 72px; height: 6px; border-radius: 3px; background: var(--color-line); overflow: hidden; }
.map-progress > span { display: block; height: 100%; background: var(--color-st-done); }

.map-note {
  display: flex; align-items: center; gap: 8px; padding: 0 12px; text-decoration: none; color: var(--color-fg);
  background: var(--color-panel); border: 1px dashed var(--color-muted); border-radius: 6px; transition: opacity 140ms, border-color 120ms;
}
.map-note:hover { border-style: solid; border-color: var(--color-fg); }

.map-lane { --c: var(--color-line); }

.map-closed {
  display: flex; flex-direction: column; align-items: stretch; padding: 12px 14px; text-align: left; cursor: pointer; color: var(--color-fg);
  background: var(--color-panel); font: inherit;
  transition: opacity 140ms, border-color 120ms, box-shadow 120ms;
}
.map-closed:hover { border-color: var(--color-fg); box-shadow: 0 4px 14px -6px oklch(20% 0.02 260 / 0.35); }
.map-closed:focus-visible { outline: 2px solid var(--color-focus); outline-offset: 2px; }
.map-split { display: flex; gap: 2px; height: 6px; margin-top: 10px; border-radius: 3px; overflow: hidden; background: var(--color-line); }
.map-split > span { min-width: 3px; }
.map-lane-head { display: flex; align-items: center; gap: 8px; padding: 12px 14px 0; }
.map-collapse { position: absolute; top: 6px; right: 6px; }
.map-story-head.map-closable, .map-lane.map-closable .map-lane-head { padding-right: 48px; }

.map-dim { opacity: 0.2; }
.map-hit { outline: 3px solid var(--color-accent); outline-offset: 2px; box-shadow: 0 0 0 7px color-mix(in oklch, var(--color-accent) 22%, transparent); }
.map-card:focus-visible, .map-note:focus-visible, .map-story-head:focus-visible { outline: 2px solid var(--color-focus); outline-offset: 2px; }

.map-edge { transition: opacity 140ms; }
/* Nodes glide to a new layout (GLIDE_MS in useMapLayout); the lines are routed to the final places, so they fade out while the cards move and back in after. */
.map-flow .vue-flow__node { transition: transform 200ms ease-out; }
.map-gliding .map-edge { opacity: 0; transition: opacity 60ms; }
@media (prefers-reduced-motion: reduce) {
  .map-flow .vue-flow__node { transition: none; }
}
.map-edge-hit { fill: none; stroke: transparent; stroke-width: 14; pointer-events: stroke; cursor: default; }
.map-edge-merged .map-edge-hit { cursor: pointer; }
.map-edge-label { cursor: pointer; }
.map-edge-label:focus { outline: none; }
.map-edge-label:focus-visible rect { stroke: var(--color-focus); stroke-width: 3; }
.map-edge-label rect { fill: var(--color-panel); stroke: currentColor; stroke-width: 1; }
.map-edge-label text { fill: var(--color-fg); font-size: 12px; font-variant-numeric: tabular-nums; text-anchor: middle; dominant-baseline: central; }
.map-edge-line, .map-edge-head { fill: none; stroke-linecap: round; stroke-linejoin: round; pointer-events: none; transition: stroke-width 100ms; }
.map-edge-head { fill: currentColor; stroke: none; }
.map-edge-blocks { color: color-mix(in oklch, var(--color-fg) 78%, transparent); }
.map-edge-blocks .map-edge-line { stroke: currentColor; stroke-width: 1.7; }
.map-edge-relates { color: var(--color-muted); }
.map-edge-relates .map-edge-line { stroke: currentColor; stroke-width: 1.3; stroke-dasharray: 7 5; opacity: 0.7; }
.map-edge-applies { color: var(--color-question); }
.map-edge-applies .map-edge-line { stroke: currentColor; stroke-width: 2; stroke-dasharray: 0.1 5; }
.map-edge-duplicates, .map-edge-supersedes { color: var(--color-st-dropped); }
.map-edge-duplicates .map-edge-line, .map-edge-supersedes .map-edge-line { stroke: currentColor; stroke-width: 1.2; stroke-dasharray: 3 3; }
.map-edge-mention { color: var(--color-accent); }
.map-edge-mention .map-edge-line { stroke: currentColor; stroke-width: 1.4; stroke-dasharray: 0.1 4; opacity: 0.8; }
.map-edge.map-lit .map-edge-line { stroke-width: 2.6; opacity: 1; }
.map-edge.map-dim { opacity: 0.08; }
</style>
