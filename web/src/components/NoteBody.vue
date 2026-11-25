<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { isPlainLinkClick } from '../lib/panelLinks'
import { renderMarkdown } from '../lib/markdown'

// project is the project the text belongs to; it scopes bare [[card:N]] and
// [[note:slug]] references. Empty for org-level text, where a bare card
// reference is ambiguous and stays plain text.
const props = defineProps<{ text: string; project?: string }>()
const router = useRouter()

const html = computed(() => renderMarkdown(props.text, props.project))

// In-app reference links navigate through the router instead of reloading.
function onClick(e: MouseEvent) {
  if (!isPlainLinkClick(e)) return
  const a = (e.target as Element | null)?.closest('a[data-route]')
  const href = a?.getAttribute('href')
  if (!href) return
  e.preventDefault()
  void router.push(href)
}
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- renderMarkdown output is sanitised (html: false, validated links) -->
  <div class="md" @click="onClick" v-html="html"></div>
</template>
