<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { cardHref, isPlainLinkClick } from '../../lib/panelLinks'

const props = defineProps<{ slug: string; number: number }>()
const emit = defineEmits<{ open: [] }>()
const router = useRouter()
const href = computed(() => cardHref(router, props.slug, props.number))

// Real hrefs keep ctrl/cmd/middle click opening the full page; a plain click
// opens the card panel in place.
function onClick(e: MouseEvent): void {
  if (!isPlainLinkClick(e, (e.currentTarget as HTMLAnchorElement).target)) return
  e.preventDefault()
  emit('open')
}
</script>

<template>
  <a :href="href" @click="onClick"><slot /></a>
</template>
