<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import KindBadge from './KindBadge.vue'
import CardLink from './card/CardLink.vue'
import ErrorBanner from './ErrorBanner.vue'
import TimeAgo from './TimeAgo.vue'
import { useAction } from '../composables/useAction'
import { useEventRefresh } from '../composables/useEventRefresh'
import { ApiError, api } from '../lib/api'
import { behalfBy } from '../lib/actors'
import { behalfVerb } from '../lib/cards'
import { isUser } from '../lib/roles'
import type { Card } from '../lib/types'

// What an agent settled for the signed-in person that they have not kept: answers to questions and
// accepted or rejected decisions, across projects. Keep says they have looked; Change opens the card,
// where the answer form or the decision's own actions change it. Renders nothing when empty.
const emit = defineEmits<{ open: [slug: string, number: number] }>()

const PAGE = 50
const items = ref<Card[]>([])
const error = ref<unknown>(null)
const { busy, error: actionError, run } = useAction()

const total = computed(() => items.value.length)

async function load(): Promise<void> {
  if (!isUser()) {
    items.value = []
    return
  }
  try {
    // Closed cards are listed only when asked for: an answered question is done, a rejected decision dropped.
    const page = await api.allCards({ answered_for: 'me', status: 'done,dropped', limit: PAGE })
    const cards = await Promise.all(
      page.items.map(async (c) => {
        try {
          return await api.card(c.project!, c.number)
        } catch (e) {
          if (e instanceof ApiError && (e.status === 403 || e.status === 404)) return undefined
          throw e
        }
      }),
    )
    items.value = cards.filter((c): c is Card => !!c?.behalf && !c.behalf.kept_at).sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    error.value = null
  } catch (e) {
    error.value = e
  }
}

// Someone settled it first (409) or it changed: the reload shows what stands now.
async function keep(c: Card): Promise<void> {
  await run(async () => {
    await api.keepCard(c.project, c.number, { version: c.version })
  })
  await load()
}

const SETTLED = new Set(['card.answered', 'card.status_changed', 'card.kept'])
useEventRefresh({
  slug: () => '',
  source: () => [],
  match: (e) => SETTLED.has(e.type),
  reload: () => void load(),
  delay: 400,
})

onMounted(() => void load())
</script>

<template>
  <section v-if="total || error || actionError" class="panel" aria-label="Answered for you">
    <h2 v-if="total" class="h2 mb-0 px-3 pt-2">Answered for you <span class="font-mono">{{ total }}</span></h2>
    <ErrorBanner :error="error" :retry="() => void load()" />
    <ErrorBanner :error="actionError" />
    <ul v-if="items.length" class="divide-y divide-line px-3">
      <li v-for="c in items" :key="c.id" class="list-row flex-wrap items-center">
        <span class="ident w-12 text-sm">#{{ c.number }}</span>
        <KindBadge :kind="c.kind" />
        <CardLink
          :slug="c.project"
          :number="c.number"
          class="lnk min-w-0 flex-1 basis-full truncate font-medium sm:basis-0"
          @open="emit('open', c.project, c.number)"
          >{{ c.title }}</CardLink
        >
        <TimeAgo class="meta w-20 text-right" :at="c.updated_at" />
        <button type="button" class="btn" :disabled="busy" :aria-label="`Keep #${c.number}`" @click="keep(c)">Keep</button>
        <button type="button" class="btn btn-quiet" :aria-label="`Change #${c.number}`" @click="emit('open', c.project, c.number)">Change</button>
        <div class="min-w-0 basis-full space-y-0.5 text-sm">
          <p class="break-words"><span class="font-medium">{{ behalfVerb(c.kind, c.status) }}</span> by {{ behalfBy(c.behalf!) }}</p>
          <p v-if="c.kind !== 'decision' && c.question?.answer" class="line-clamp-3 break-words whitespace-pre-line">{{ c.question.answer }}</p>
          <p class="break-words text-muted"><span class="font-medium">Reason:</span> {{ c.behalf!.reason }}</p>
        </div>
      </li>
    </ul>
  </section>
</template>
