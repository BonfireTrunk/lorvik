<script setup lang="ts">
import { computed } from 'vue'
import { useMapNodeFocus } from '../../composables/useMapFocus'
import { STATUS_ORDER } from '../../lib/cards'
import { groupLabel, groupSpoken } from '../../lib/map/collapse'
import type { PlacedNode } from '../../lib/map/elkGraph'
import MapNodeHandles from './MapNodeHandles.vue'
import IconLayers from '~icons/lucide/layers'
import IconList from '~icons/lucide/list'

const props = defineProps<{ node: PlacedNode }>()
const emit = defineEmits<{ open: [key: string] }>()
const { dim, hit, hover } = useMapNodeFocus(() => props.node.id)

const box = computed(() => props.node.group!)
const isStory = computed(() => box.value.kind === 'story')
const name = computed(() => groupLabel(box.value))
const parts = computed(() => STATUS_ORDER.filter((s) => box.value.counts[s] > 0).map((s) => ({ status: s, n: box.value.counts[s] })))
const label = computed(() => `${isStory.value ? `Story ${box.value.number}, ` : ''}${groupSpoken(box.value)}, ${box.value.shown} cards: ${parts.value.map((p) => `${p.n} ${p.status}`).join(', ')}. Opens its list`)
</script>

<template>
  <button
    type="button"
    class="map-story map-closed"
    :class="{ 'map-hit': hit, 'map-dim': dim }"
    :style="{ width: `${node.w}px`, height: `${node.h}px`, '--c': isStory ? `var(--color-st-${box.status})` : 'var(--color-line)' }"
    :aria-label="label"
    v-bind="hover"
    @click="emit('open', box.key)"
  >
    <MapNodeHandles />
    <span class="flex min-w-0 items-center gap-2">
      <component :is="isStory ? IconLayers : IconList" class="h-4 w-4 shrink-0 text-muted" />
      <span v-if="isStory" class="ident shrink-0 text-muted">#{{ box.number }}</span>
      <span class="truncate text-base font-semibold" :title="name">{{ name }}</span>
    </span>
    <span class="mt-2 flex items-center gap-2 text-xs text-muted">
      <span v-if="isStory && box.status" class="badge badge-tight" :class="`badge-st-${box.status}`"><span class="swatch h-2 w-2" aria-hidden="true"></span>{{ box.status }}</span>
      <span class="tabular">{{ box.shown }} {{ box.shown === 1 ? 'card' : 'cards' }}</span>
    </span>
    <span class="map-split" aria-hidden="true">
      <span v-for="p in parts" :key="p.status" :style="{ flexGrow: p.n, background: `var(--color-st-${p.status})` }"></span>
    </span>
    <span class="mt-2 flex flex-wrap gap-x-2.5 gap-y-0.5 text-xs text-muted tabular">
      <span v-for="p in parts" :key="p.status" class="inline-flex items-center gap-1" :title="`${p.n} ${p.status}`">
        <span class="swatch" :style="{ '--c': `var(--color-st-${p.status})` }" aria-hidden="true"></span>{{ p.n }}
      </span>
    </span>
  </button>
</template>
