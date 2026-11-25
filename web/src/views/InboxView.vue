<script setup lang="ts">
import { ref } from 'vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import EventRow from '../components/EventRow.vue'
import LoadMore from '../components/LoadMore.vue'
import LoadState from '../components/LoadState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { usePagedList } from '../composables/usePagedList'
import { ApiError, api } from '../lib/api'
import { storeCursor, unread } from '../lib/inbox'
import type { CardBrief } from '../lib/types'

const claims = ref<CardBrief[]>([])
const openQuestions = ref(0)
// A stored cursor from another database (the server was reset) names no event
// there; only the oldest position still means something.
async function readInbox(limit: number) {
  try {
    return await api.inbox(unread.cursor, limit)
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 422 && e.problem.errors?.some((f) => f.field === 'cursor'))) throw e
    storeCursor('0')
    return api.inbox(unread.cursor, limit)
  }
}

const { items: events, remaining, loading, error, load } = usePagedList(async (limit) => {
  // The cursor is the client's: send the stored one, store the returned one.
  const r = await readInbox(limit)
  claims.value = r.claims ?? []
  openQuestions.value = r.open_questions
  storeCursor(r.cursor)
  if (r.remaining === 0) unread.count = 0
  return { items: r.events, next: r.cursor, remaining: r.remaining }
}, { autoLoad: true })
</script>

<template>
  <div class="page">
    <h1 class="page-title">Inbox</h1>
    <p class="meta">Your claimed cards and what others did since you last looked.</p>
    <ErrorBanner :error="error" :retry="() => load()" />
    <LoadState :loading="loading && !events.length" />

    <section v-if="claims.length">
      <h2 class="h2">Your claims</h2>
      <ul class="list">
        <li v-for="c in claims" :key="c.id" class="list-row flex-wrap">
          <span class="ident w-10 shrink-0">#{{ c.number }}</span>
          <span class="min-w-0 flex-1 basis-48 truncate font-medium">{{ c.title }}</span>
          <span class="w-24 shrink-0"><StatusBadge :status="c.status" /></span>
        </li>
      </ul>
    </section>

    <p v-if="openQuestions" class="text-sm"><span class="badge badge-question">{{ openQuestions }}</span> open {{ openQuestions === 1 ? 'question' : 'questions' }}.</p>

    <section>
      <h2 class="h2">What others did</h2>
      <LoadState :loading="false" :empty="!loading && !events.length && !error" empty-text="Nothing new." />
      <ul v-if="events.length" class="list">
        <EventRow v-for="e in events" :key="e.id" :event="e" />
      </ul>
      <LoadMore class="mt-2" :has-more="remaining > 0" :loading="loading" :remaining="remaining" @load="load(true)" />
    </section>
  </div>
</template>
