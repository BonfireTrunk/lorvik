<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCardActionKeys } from '../../composables/useCardActions'
import { useCardPanel } from '../../composables/useCardPanel'
import { isEditable } from '../../composables/useKeymap'
import { panelLinkTarget } from '../../lib/panelLinks'
import SidePanel from '../SidePanel.vue'
import CardDetail from './CardDetail.vue'
import IconArrowLeft from '~icons/lucide/arrow-left'
import IconMaximize from '~icons/lucide/maximize-2'

const props = defineProps<{ slug: string; number: number; pageSlug?: string }>()
const emit = defineEmits<{ close: [] }>()

const route = useRoute()
const router = useRouter()
const cardPanel = useCardPanel(props.pageSlug)

// Swaps push history, so the previous panel is one Back away. The router keeps
// the previous location in history.state.back; a card there means Back
// returns to a panel rather than leaving the page.
const canBack = computed(() => {
  void route.fullPath
  const back = window.history.state?.back
  return typeof back === 'string' && /[?&]card=/.test(back)
})

function back(): void {
  if (canBack.value) router.back()
}

// Backspace outside a field goes back, as Escape closes. Alt+Left, the
// browser's own Back, works as well.
function onKeydown(e: KeyboardEvent): void {
  if (e.key !== 'Backspace' || !canBack.value) return
  if (isEditable(e.target)) return
  e.preventDefault()
  back()
}

useCardActionKeys('panel', () => ({ slug: props.slug, number: props.number }))

// Links to any card swap the panel (pushed so Back returns to the previous
// card). Modified clicks fall through to the browser and open the full page.
// Bound in the capture phase: RouterLink's own handler runs at the anchor and
// would already have started the full-page navigation by the time a bubbling
// listener saw it.
function onLinkClick(e: MouseEvent): void {
  const t = panelLinkTarget(e, router)
  if (!t) return
  e.preventDefault()
  if (t.slug !== props.slug || t.number !== props.number) cardPanel.open(t.slug, t.number)
}
</script>

<template>
  <SidePanel labelledby="card-title" keymap="panel" @close="emit('close')" @keydown="onKeydown">
    <template #leading>
      <button v-if="canBack" type="button" class="btn btn-quiet btn-icon" aria-label="Back" title="Back (Backspace)" aria-keyshortcuts="Backspace" @click="back">
        <IconArrowLeft />
      </button>
      <router-link class="btn btn-quiet" :to="{ name: 'card', params: { slug, number } }" title="Open as page"><IconMaximize />Open as page</router-link>
    </template>
    <div @click.capture="onLinkClick">
      <CardDetail :key="number" :slug="slug" :number="String(number)" />
    </div>
  </SidePanel>
</template>
