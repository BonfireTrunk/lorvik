<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useAction, useCanSubmit } from '../../composables/useAction'
import { useEventRefresh } from '../../composables/useEventRefresh'
import { useHistoryList } from '../../composables/useHistoryList'
import { api } from '../../lib/api'
import { canIn } from '../../lib/roles'
import { sendOnModEnter } from '../../lib/sendKey'
import type { Comment } from '../../lib/types'
import ActorName from '../ActorName.vue'
import ErrorBanner from '../ErrorBanner.vue'
import LoadMore from '../LoadMore.vue'
import LoadState from '../LoadState.vue'
import NoteBody from '../NoteBody.vue'
import TimeAgo from '../TimeAgo.vue'
import MarkdownHint from '../MarkdownHint.vue'

const props = defineProps<{ slug: string; number: number }>()

const text = ref('')
const { busy, error, run } = useAction()
const canSubmit = useCanSubmit(busy, text)
const list = useHistoryList(
  (q) => api.comments(props.slug, props.number, q),
  (c: Comment) => c.id,
)
const { loading, error: loadError, hasOlder, loadOlder } = list
// The API lists newest first; the thread reads oldest first, earlier pages above.
const items = computed(() => [...list.items.value].reverse())

function load(): Promise<void> {
  return list.refresh()
}

async function post(): Promise<void> {
  await run(async () => {
    await api.createComment(props.slug, props.number, text.value.trim())
    text.value = ''
    await load()
  })
}

const onKeydown = sendOnModEnter(() => canSubmit.value, () => void post())

// Any event about this card (card.comment and the rest) reloads the thread.
// Only `items` is replaced, so a draft in the textarea is untouched.
useEventRefresh({
  slug: () => props.slug,
  source: () => [props.slug, props.number],
  match: (e) => e.subject.type === 'card' && e.subject.number === props.number,
  reload: () => void load(),
  delay: 250,
})

onMounted(load)
watch(
  () => [props.slug, props.number],
  () => {
    list.reset()
    void load()
  },
)
</script>

<template>
  <section>
    <h2 class="h2">Comments</h2>
    <div class="space-y-2">
      <LoadMore :has-more="hasOlder" :loading="loading" @load="loadOlder()" />
      <div v-if="items.length" class="list">
        <div v-for="h in items" :key="h.id" class="py-3">
          <p class="mb-1 flex gap-3 text-xs">
            <span class="font-medium"><ActorName :who="h.actor" /></span>
            <TimeAgo class="text-muted" :at="h.created_at" />
          </p>
          <NoteBody :text="h.body" :project="slug" />
        </div>
      </div>
      <LoadState :loading="false" :empty="!items.length && !error && !loadError" empty-text="No comments yet." />
      <form v-if="canIn(slug, 'member')" class="space-y-2 pt-1" @submit.prevent="post">
        <label class="label" :for="`comment-${number}`">Add a comment</label>
        <textarea :id="`comment-${number}`" v-model="text" class="field" rows="3" :aria-describedby="`comment-${number}-hint`" @keydown="onKeydown"></textarea>
        <MarkdownHint :id="`comment-${number}-hint`">Ctrl+Enter posts.</MarkdownHint>
        <button type="submit" class="btn" :disabled="!canSubmit">Post</button>
      </form>
      <ErrorBanner :error="loadError" :retry="load" />
      <ErrorBanner :error="error" />
    </div>
  </section>
</template>
