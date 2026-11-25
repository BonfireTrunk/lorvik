<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useMapNodeFocus } from '../../composables/useMapFocus'
import type { PlacedNode } from '../../lib/map/elkGraph'
import { isPlainLinkClick } from '../../lib/panelLinks'
import MapNodeHandles from './MapNodeHandles.vue'
import IconFileText from '~icons/lucide/file-text'

const props = defineProps<{ node: PlacedNode; slug: string }>()
const { dim, hit, hover } = useMapNodeFocus(() => props.node.id)
const router = useRouter()

const note = computed(() => props.node.note!)
const to = computed(() => ({ name: 'note', params: { noteKey: note.value.slug }, query: { project: props.slug } }))
const href = computed(() => router.resolve(to.value).href)

function go(e: MouseEvent): void {
  if (!isPlainLinkClick(e)) return
  e.preventDefault()
  void router.push(to.value)
}
</script>

<template>
  <a
    :href="href"
    class="map-note"
    :class="{ 'map-dim': dim, 'map-hit': hit }"
    :style="{ width: `${node.w}px`, height: `${node.h}px` }"
    :aria-label="`Note ${note.title}`"
    @click="go"
    v-bind="hover"
  >
    <MapNodeHandles />
    <IconFileText class="h-4 w-4 shrink-0 text-muted" />
    <span class="min-w-0">
      <span class="block truncate text-xs leading-tight font-medium">{{ note.title }}</span>
      <span class="block truncate font-mono text-2xs text-muted">{{ note.slug }}</span>
    </span>
  </a>
</template>
