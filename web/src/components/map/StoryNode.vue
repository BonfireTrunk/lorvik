<script setup lang="ts">
import { computed } from 'vue'
import { useMapNodeFocus } from '../../composables/useMapFocus'
import { storyKey } from '../../lib/map/collapse'
import type { PlacedNode } from '../../lib/map/elkGraph'
import CardLink from '../card/CardLink.vue'
import ClosedBox from './ClosedBox.vue'
import MapNodeHandles from './MapNodeHandles.vue'
import IconLayers from '~icons/lucide/layers'
import IconMinimize from '~icons/lucide/minimize-2'

const props = defineProps<{ node: PlacedNode; slug: string; opened: string[] }>()
const emit = defineEmits<{ open: [number: number]; group: [key: string]; collapse: [key: string] }>()
const { hit } = useMapNodeFocus(() => props.node.id)

const story = computed(() => props.node.story!)
const card = computed(() => story.value.card)
const key = computed(() => storyKey(card.value.number))
const closable = computed(() => props.opened.includes(key.value))
const pct = computed(() => (story.value.total ? Math.round((story.value.done / story.value.total) * 100) : 0))
</script>

<template>
  <ClosedBox v-if="node.group" :node="node" @open="emit('group', $event)" />
  <div
    v-else
    class="map-story"
    :class="{ 'map-hit': hit }"
    :style="{ width: `${node.w}px`, height: `${node.h}px`, '--c': `var(--color-st-${card.status})` }"
  >
    <MapNodeHandles />
    <CardLink
      :slug="slug"
      :number="card.number"
      class="map-story-head"
      :class="{ 'map-closable': closable }"
      :data-card="card.number"
      :aria-label="`Story ${card.number}, ${card.title}, ${card.status}, ${story.done} of ${story.total} done`"
      @open="emit('open', card.number)"
    >
      <span class="flex min-w-0 items-center gap-2">
        <IconLayers class="h-4 w-4 shrink-0 text-muted" />
        <span class="ident shrink-0 text-muted">#{{ card.number }}</span>
        <span class="truncate text-base font-semibold" :title="card.title">{{ card.title }}</span>
      </span>
      <span class="mt-2.5 flex items-center gap-2">
        <span class="badge badge-tight" :class="`badge-st-${card.status}`"><span class="swatch h-2 w-2" aria-hidden="true"></span>{{ card.status }}</span>
        <span v-if="card.lane" class="badge badge-tight" title="Lane">{{ card.lane }}</span>
        <span v-if="story.total" class="ml-auto flex items-center gap-2 text-xs text-muted">
          <span class="map-progress" role="img" :aria-label="`${story.done} of ${story.total} done`"><span :style="{ width: `${pct}%` }"></span></span>
          <span class="tabular">{{ story.done }}/{{ story.total }}</span>
        </span>
      </span>
    </CardLink>
    <button v-if="closable" type="button" class="btn btn-quiet btn-icon map-collapse nopan" :aria-label="`Collapse story ${card.number} into a box`" title="Collapse into a box" @click="emit('collapse', key)">
      <IconMinimize />
    </button>
  </div>
</template>
