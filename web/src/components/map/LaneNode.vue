<script setup lang="ts">
import { computed } from 'vue'
import { laneKey, laneLabel, laneSpoken } from '../../lib/map/collapse'
import type { PlacedNode } from '../../lib/map/elkGraph'
import ClosedBox from './ClosedBox.vue'
import IconList from '~icons/lucide/list'
import IconMinimize from '~icons/lucide/minimize-2'

const props = defineProps<{ node: PlacedNode; opened: string[] }>()
const emit = defineEmits<{ group: [key: string]; collapse: [key: string] }>()

const lane = computed(() => props.node.lane!)
const label = computed(() => laneLabel(lane.value.lane))
const spoken = computed(() => laneSpoken(lane.value.lane))
const what = computed(() => (lane.value.lane ? `${lane.value.lane}: cards outside any story` : 'Cards outside any story, with no lane'))
const key = computed(() => laneKey(lane.value.lane))
const closable = computed(() => props.opened.includes(key.value))
</script>

<template>
  <ClosedBox v-if="node.group" :node="node" @open="emit('group', $event)" />
  <div v-else class="map-story map-lane" :class="{ 'map-closable': closable }" role="group" :aria-label="`${spoken}, ${lane.children.length} cards`" :style="{ width: `${node.w}px`, height: `${node.h}px` }">
    <div class="map-lane-head">
      <IconList class="h-4 w-4 shrink-0 text-muted" />
      <span class="min-w-0 truncate text-sm font-semibold" :title="what">{{ label }}</span>
      <span class="font-mono text-xs text-muted">{{ lane.children.length }}</span>
    </div>
    <button v-if="closable" type="button" class="btn btn-quiet btn-icon map-collapse nopan" :aria-label="`Collapse ${spoken} into a box`" title="Collapse into a box" @click="emit('collapse', key)">
      <IconMinimize />
    </button>
  </div>
</template>
