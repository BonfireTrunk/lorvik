<script setup lang="ts">
import { ref } from 'vue'
import { STATUS_ORDER } from '../../lib/cards'
import SearchBox from '../SearchBox.vue'
import { MAP_KINDS, NO_LANE, type MapFilters, type MapKind } from '../../lib/map/types'
import type { CardBrief, CardStatus } from '../../lib/types'
import IconCircleHelp from '~icons/lucide/circle-help'
import IconDiamond from '~icons/lucide/diamond'
import IconFileText from '~icons/lucide/file-text'
import IconLayers from '~icons/lucide/layers'
import IconLink from '~icons/lucide/link'
import IconRotateCw from '~icons/lucide/rotate-cw'
import IconSquareCheck from '~icons/lucide/square-check'

defineProps<{
  filters: MapFilters
  search: string
  lanes: string[]
  stories: Pick<CardBrief, 'number' | 'title'>[]
  shown: number
  total: number
  busy: boolean
}>()
const emit = defineEmits<{
  status: [CardStatus]
  kind: [MapKind]
  lane: [string]
  story: [number]
  linked: [boolean]
  search: [string]
  next: []
  refresh: []
}>()

const KINDS: Record<MapKind, { label: string; icon: typeof IconLayers }> = {
  task: { label: 'Tasks', icon: IconSquareCheck },
  question: { label: 'Questions', icon: IconCircleHelp },
  decision: { label: 'Decisions', icon: IconDiamond },
  story: { label: 'Stories', icon: IconLayers },
  note: { label: 'Notes', icon: IconFileText },
}

const box = ref<{ focus: () => void } | null>(null)
defineExpose({ focusSearch: () => box.value?.focus() })
</script>

<template>
  <div class="contents" role="search" aria-label="Map filters">
    <div class="cells cells-toggle" role="group" aria-label="Status">
      <button v-for="s in STATUS_ORDER" :key="s" type="button" class="cell inline-flex items-center gap-1.5 capitalize" :aria-pressed="filters.statuses.includes(s)" @click="emit('status', s)">
        <span class="swatch" :style="{ '--c': `var(--color-st-${s})` }" aria-hidden="true"></span>{{ s }}
      </button>
    </div>
    <div class="cells cells-toggle" role="group" aria-label="Kinds">
      <button
        v-for="k in MAP_KINDS"
        :key="k"
        type="button"
        class="cell inline-flex items-center gap-1.5"
        :aria-pressed="filters.kinds.includes(k)"
        :title="KINDS[k].label"
        @click="emit('kind', k)"
      >
        <component :is="KINDS[k].icon" class="h-4 w-4" /><span class="sr-only min-[100rem]:not-sr-only">{{ KINDS[k].label }}</span>
      </button>
    </div>
    <select class="field w-36" :value="filters.lane" aria-label="Lane" @change="emit('lane', ($event.target as HTMLSelectElement).value)">
      <option value="">All lanes</option>
      <option :value="NO_LANE">No lane</option>
      <option v-for="l in lanes" :key="l" :value="l">{{ l }}</option>
    </select>
    <select class="field w-48" :value="filters.story" aria-label="Story" @change="emit('story', Number(($event.target as HTMLSelectElement).value))">
      <option :value="0">All stories</option>
      <option v-for="s in stories" :key="s.number" :value="s.number">S#{{ s.number }} {{ s.title }}</option>
    </select>
    <div class="cells cells-toggle">
      <button type="button" class="cell inline-flex items-center gap-1.5" :aria-pressed="filters.linked" @click="emit('linked', !filters.linked)">
        <IconLink class="h-4 w-4" />Only cards with links
      </button>
    </div>
    <span class="text-sm text-muted tabular" role="status">Showing {{ shown }} of {{ total }}</span>
    <div class="ml-auto flex items-center gap-3">
      <button type="button" class="btn btn-quiet btn-icon" aria-label="Reload the map" title="Reload" :disabled="busy" @click="emit('refresh')"><IconRotateCw :class="busy ? 'animate-spin' : ''" /></button>
      <SearchBox ref="box" :search="search" @search="emit('search', $event)" @next="emit('next')" />
    </div>
  </div>
</template>
