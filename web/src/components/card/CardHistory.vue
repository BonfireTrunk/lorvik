<script setup lang="ts">
import { api } from '../../lib/api'
import type { HistoryQuery } from '../../lib/types'
import RevisionHistory from '../RevisionHistory.vue'

// Past titles and bodies of the card; each row is the text an edit replaced.
const props = defineProps<{ slug: string; number: number; version: number }>()

function fetchList(q: HistoryQuery) {
  return api.cardRevisions(props.slug, props.number, q)
}

function fetchRevision(version: number) {
  return api.cardRevision(props.slug, props.number, version)
}
</script>

<template>
  <RevisionHistory
    :source="`${slug}/${number}`"
    :version="version"
    :project="slug"
    :fetch-list="fetchList"
    :fetch-revision="fetchRevision"
  />
</template>
