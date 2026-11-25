<script setup lang="ts">
import type { Card, CardKind } from '../../lib/types'
import SidePanel from '../SidePanel.vue'
import AddCardForm from './AddCardForm.vue'

defineProps<{ slug: string; preset?: CardKind; pickProject?: boolean }>()
const emit = defineEmits<{ created: [card: Card]; close: [] }>()
</script>

<template>
  <SidePanel labelledby="new-card-title" @close="emit('close')">
    <template #leading>
      <h2 id="new-card-title" class="px-2 font-semibold">{{ preset === 'decision' ? 'New decision' : 'New card' }}</h2>
    </template>
    <AddCardForm :slug="slug" :preset="preset" :pick-project="pickProject" @created="emit('created', $event)" @close="emit('close')" />
  </SidePanel>
</template>
