<script setup lang="ts">
import { computed } from 'vue'
import { useMapNodeFocus } from '../../composables/useMapFocus'
import { actorName } from '../../lib/actors'
import { humanActionWord, stateWord } from '../../lib/cards'
import type { PlacedNode } from '../../lib/map/elkGraph'
import CardLink from '../card/CardLink.vue'
import PriorityBadge from '../PriorityBadge.vue'
import MapNodeHandles from './MapNodeHandles.vue'
import IconCircleHelp from '~icons/lucide/circle-help'
import IconDiamond from '~icons/lucide/diamond'
import IconSquareCheck from '~icons/lucide/square-check'

const props = defineProps<{ node: PlacedNode; slug: string }>()
const emit = defineEmits<{ open: [number: number] }>()

const { dim, hit, hover } = useMapNodeFocus(() => props.node.id)
const card = computed(() => props.node.card!)
const icon = computed(() => (card.value.kind === 'question' ? IconCircleHelp : card.value.kind === 'decision' ? IconDiamond : IconSquareCheck))
const word = computed(() => stateWord(card.value.kind, card.value.status, card.value.drop_reason, card.value.decision))
const holder = computed(() => (card.value.claim ? actorName(card.value.claim.actor) : ''))
const needsWord = computed(() => humanActionWord(card.value.kind))
</script>

<template>
  <CardLink
    :slug="slug"
    :number="card.number"
    class="map-card"
    :data-card="card.number"
    :class="{ 'map-dim': dim, 'map-hit': hit, 'map-needs': node.needs }"
    :style="{ width: `${node.w}px`, height: `${node.h}px`, '--c': `var(--color-st-${card.status})` }"
    :aria-label="`${card.kind} ${card.number}, ${card.title}, ${word}`"
    @open="emit('open', card.number)"
    v-bind="hover"
  >
    <MapNodeHandles />
    <span class="map-stripe" aria-hidden="true"></span>
    <div v-if="node.needs" class="map-band"><IconCircleHelp class="h-3.5 w-3.5" />Needs you: {{ needsWord }}</div>
    <div class="flex min-w-0 flex-1 flex-col pt-3 pr-3 pb-3 pl-4">
      <div class="flex h-5 items-center gap-1.5">
        <component :is="icon" class="h-4 w-4 shrink-0" :class="card.kind === 'task' ? 'text-muted' : 'text-question'" />
        <span class="ident text-base text-muted">#{{ card.number }}</span>
        <span class="ml-auto flex items-center gap-1.5 text-xs">
          <span v-if="holder" class="inline-flex items-center gap-1 text-muted" :title="`Claimed by ${holder}`">
            <span class="swatch h-2 w-2" style="--c: var(--color-accent)" aria-hidden="true"></span>claimed
          </span>
          <PriorityBadge v-if="card.priority === 'high' || card.priority === 'urgent'" class="badge-tight" :priority="card.priority" />
        </span>
      </div>
      <div class="map-title mt-1.5 text-sm leading-snug font-medium">{{ card.title }}</div>
      <div class="mt-auto flex items-center gap-1.5 pt-2">
        <span class="badge badge-tight" :class="`badge-st-${card.status}`"><span class="swatch h-2 w-2" aria-hidden="true"></span>{{ word }}</span>
        <span v-if="card.lane" class="truncate text-xs text-muted" :title="`Lane ${card.lane}`">{{ card.lane }}</span>
      </div>
    </div>
  </CardLink>
</template>
