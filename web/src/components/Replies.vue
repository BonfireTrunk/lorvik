<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import CardLink from './card/CardLink.vue'
import ErrorBanner from './ErrorBanner.vue'
import { useAction } from '../composables/useAction'
import { useEventRefresh } from '../composables/useEventRefresh'
import { actorName } from '../lib/actors'
import { markdownText } from '../lib/markdown'
import { ApiError, allPages, api } from '../lib/api'
import { relativeTime } from '../lib/time'
import type { Reply } from '../lib/types'

// Comments by others on cards the viewer wrote on or opened, since they last
// looked. Opening a card clears its reply, and Mark all read clears them all.
// Renders nothing when there are none.
const props = defineProps<{ slug: string }>()

const emit = defineEmits<{ open: [number: number]; cards: [numbers: number[]] }>()
const route = useRoute()
const LIMIT = 100
const items = ref<Reply[]>([])
const error = ref<unknown>(null)
const { busy, error: markError, run } = useAction()

function firstLine(body: string): string {
  return markdownText(body).split('\n')[0] ?? ''
}

// The chip shows the card; who replied, when and what stay one hover or focus away.
function tooltip(r: Reply): string {
  return `${actorName(r.comment.actor)}, ${relativeTime(r.comment.created_at)}: ${firstLine(r.comment.body)}`
}

async function load(): Promise<void> {
  const slug = props.slug
  try {
    // Walked fresh each time: a card with a new reply moves to the top, so a
    // saved cursor would skip it. The board's badge needs every card anyway.
    const replies = await allPages((cursor) => api.replies(slug, LIMIT, cursor))
    if (slug !== props.slug) return
    items.value = replies
    emit('cards', replies.map((r) => r.card.number))
    error.value = null
  } catch (e) {
    if (slug !== props.slug) return
    // A viewer who cannot read the project has no replies to show.
    if (e instanceof ApiError && e.status === 403) {
      items.value = []
      emit('cards', [])
      error.value = null
      return
    }
    error.value = e
  }
}

async function markAll(): Promise<void> {
  await run(async () => {
    await api.markRepliesSeen(props.slug)
    await load()
  })
}

useEventRefresh({
  slug: () => props.slug,
  source: () => [props.slug],
  match: (e) => e.type === 'card.comment',
  reload: () => void load(),
  delay: 400,
})

onMounted(() => void load())
watch(() => props.slug, () => void load())

// Opening a card marks it seen, but no event announces that, so the strip
// would keep the reply until the next comment. A closing panel is the signal.
watch(
  () => route.query.card,
  (now, before) => {
    if (before && !now) void load()
  },
)
</script>

<template>
  <section v-if="items.length || error" class="space-y-1.5" aria-label="Replies to you">
    <ErrorBanner :error="error" :retry="() => void load()" />
    <ErrorBanner :error="markError" />
    <div v-if="items.length" class="flex flex-wrap items-center gap-x-2 gap-y-1.5">
      <h2 class="h2 mb-0">Replies to you <span class="font-semibold text-fg">{{ items.length }}</span></h2>
      <button type="button" class="btn btn-quiet h-8" :disabled="busy" @click="markAll">Mark all read</button>
      <CardLink
        v-for="r in items"
        :key="r.card.number"
        :slug="slug"
        :number="r.card.number"
        class="panel flex h-8 max-w-72 items-center gap-x-2 px-2 text-sm no-underline hover:border-fg"
        :title="tooltip(r)"
        :aria-label="`#${r.card.number} ${r.card.title}: ${tooltip(r)}`"
        @open="emit('open', r.card.number)"
      >
        <span class="meta ident">#{{ r.card.number }}</span>
        <span class="truncate">{{ r.card.title }}</span>
        <span v-if="r.unseen > 1" class="badge badge-count">+{{ r.unseen - 1 }}</span>
      </CardLink>
    </div>
  </section>
</template>
