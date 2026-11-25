<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ForPersonBadge from './ForPersonBadge.vue'
import KindBadge from './KindBadge.vue'
import CardLink from './card/CardLink.vue'
import ErrorBanner from './ErrorBanner.vue'
import TimeAgo from './TimeAgo.vue'
import { useAction } from '../composables/useAction'
import { useEventRefresh } from '../composables/useEventRefresh'
import { ApiError, MAX_PAGE, allPages, api } from '../lib/api'
import { auth } from '../lib/auth'
import { awaitsHuman } from '../lib/cards'
import { globalProjectSlug } from '../lib/projects'
import { canHumanIn, isUser } from '../lib/roles'
import { rightsInWords } from '../lib/rights'
import type { AgentRequest, CardBrief } from '../lib/types'

// Questions and proposed decisions waiting for a human: the global project's on
// every board, and this board's own for a viewer who can settle them. A signed-in
// human also sees their agents' requests to join projects. Renders nothing when empty.
// With `locate`, a row of this board names its card there instead of opening it.
const props = defineProps<{ slug: string; locate?: (card: CardBrief) => void }>()
const emit = defineEmits<{ open: [slug: string, number: number]; project: [cards: CardBrief[]] }>()

interface Row {
  slug: string
  card: CardBrief
}

const items = ref<Row[]>([])
const requests = ref<AgentRequest[]>([])
const error = ref<unknown>(null)
const requestsError = ref<unknown>(null)
const { busy, error: actionError, run } = useAction()

const total = computed(() => items.value.length + requests.value.length)
const globalSlug = computed(() => globalProjectSlug())

// Accepting and rejecting need a human reviewer; the server enforces it.
function canDecide(slug: string): boolean {
  return canHumanIn(slug, 'reviewer')
}

async function loadProject(slug: string): Promise<Row[]> {
  try {
    const [questions, decisions] = await Promise.all(
      (['question', 'decision'] as const).map((kind) => allPages((cursor) => api.cards(slug, { status: 'ready', kind, limit: MAX_PAGE, cursor }))),
    )
    return [...questions, ...decisions].map((card) => ({ slug, card }))
  } catch (e) {
    // A viewer without read on the project has nothing to answer there.
    if (e instanceof ApiError && (e.status === 403 || e.status === 404)) return []
    throw e
  }
}

async function load(): Promise<void> {
  const slug = props.slug
  const global = globalSlug.value
  try {
    // A global row is listed for everyone who can read it; a board's own only for who can act.
    const [own, shared] = await Promise.all([
      slug !== global && canDecide(slug) ? loadProject(slug) : [],
      global ? loadProject(global) : [],
    ])
    if (slug !== props.slug) return
    const awaits = (r: Row): boolean => awaitsHuman(r.card, auth.me?.actor)
    const mine = own.filter(awaits)
    const here = (slug === global ? shared : own).filter(awaits)
    emit('project', here.map((r) => r.card))
    // Questions marked for a person come first.
    items.value = [...mine, ...shared].sort(
      (a, b) => Number(!!b.card.for_person) - Number(!!a.card.for_person) || b.card.updated_at.localeCompare(a.card.updated_at),
    )
    error.value = null
  } catch (e) {
    if (slug !== props.slug) return
    error.value = e
  }
}

async function loadRequests(): Promise<void> {
  if (!isUser()) {
    requests.value = []
    return
  }
  try {
    requests.value = (await api.agentRequests()).items
    requestsError.value = null
  } catch (e) {
    requestsError.value = e
  }
}

async function answerRequest(act: () => Promise<unknown>): Promise<void> {
  await run(async () => {
    await act()
  })
  await loadRequests()
}

const allowRequest = (r: AgentRequest) => answerRequest(() => api.allowAgentRequest(r.agent.id, r.project))
const denyRequest = (r: AgentRequest) => answerRequest(() => api.denyAgentRequest(r.agent.id, r.project))
const allowEverywhere = (r: AgentRequest) =>
  answerRequest(async () => {
    await api.updateAgent(r.agent.id, { agent_join: 'allow' })
    await api.allowAgentRequest(r.agent.id, r.project)
  })

// The brief carries no version, so the card is read first; a stale read
// surfaces as a conflict and the list reloads either way.
async function decide(r: Row, accept: boolean): Promise<void> {
  await run(async () => {
    const full = await api.card(r.slug, r.card.number)
    await api.transition(r.slug, r.card.number, {
      version: full.version,
      status: accept ? 'done' : 'dropped',
      drop_reason: accept ? undefined : 'rejected',
    })
  })
  await load()
}

useEventRefresh({
  slug: () => globalSlug.value,
  source: () => [globalSlug.value],
  reload: () => void load(),
  delay: 400,
})
const JOIN_EVENTS = new Set(['agent.join_requested', 'agent.joined', 'agent.join_denied', 'agent.updated'])
useEventRefresh({
  slug: () => '',
  source: () => [],
  match: (e) => JOIN_EVENTS.has(e.type),
  reload: () => void loadRequests(),
  delay: 400,
})
useEventRefresh({
  slug: () => props.slug,
  source: () => [props.slug],
  reload: () => void load(),
  delay: 400,
})

onMounted(() => {
  void load()
  void loadRequests()
})
watch(() => props.slug, () => void load())
</script>

<template>
  <section v-if="total || error || requestsError || actionError" :class="total ? 'needs-frame' : ''" aria-label="Awaiting you">
    <h2 v-if="total" class="needs-plate">Awaiting you <span class="font-mono">{{ total }}</span></h2>
    <ErrorBanner :error="error" :retry="() => void load()" />
    <ErrorBanner :error="requestsError" :retry="() => void loadRequests()" />
    <ErrorBanner :error="actionError" />
    <ul v-if="requests.length" class="divide-y divide-line px-3">
      <li v-for="r in requests" :key="`${r.agent.id}/${r.project}`" class="list-row flex-wrap items-center">
        <span class="min-w-0 flex-1 basis-full font-medium sm:basis-0">
          {{ r.agent.name }}
          <span class="meta font-normal">asks to join {{ r.project_name }} to {{ rightsInWords(r.rights) }}</span>
        </span>
        <span class="badge" title="The tool or client the agent runs in.">{{ r.agent.harness }}</span>
        <span v-if="r.agent.machine" class="meta">{{ r.agent.machine }}</span>
        <TimeAgo class="meta w-20 text-right" :at="r.requested_at" />
        <button type="button" class="btn" :disabled="busy" :aria-label="`Allow ${r.agent.name} in ${r.project_name}`" @click="allowRequest(r)">Allow</button>
        <button type="button" class="btn btn-quiet" :disabled="busy" :aria-label="`Deny ${r.agent.name} in ${r.project_name}`" @click="denyRequest(r)">Deny</button>
        <button
          type="button"
          class="btn btn-quiet"
          :disabled="busy"
          title="Lets this agent join any of your projects without asking"
          @click="allowEverywhere(r)"
        >
          Allow in all my projects
        </button>
      </li>
    </ul>
    <ul v-if="items.length" class="divide-y divide-line px-3" :class="requests.length ? 'border-t border-line' : ''">
      <li v-for="r in items" :key="r.card.id" class="list-row flex-wrap items-center">
        <span class="ident w-12 text-sm">#{{ r.card.number }}</span>
        <KindBadge :kind="r.card.kind" />
        <span v-if="r.slug !== slug" class="badge" title="Applies across projects">global</span>
        <ForPersonBadge v-if="r.card.for_person" />
        <CardLink
          :slug="r.slug"
          :number="r.card.number"
          class="lnk min-w-0 flex-1 basis-full truncate font-medium sm:basis-0"
          :title="locate && r.slug === slug ? 'Show it on the map' : undefined"
          @open="locate && r.slug === slug ? locate(r.card) : emit('open', r.slug, r.card.number)"
          >{{ r.card.title }}<span v-if="locate && r.slug === slug" class="sr-only">, show it on the map</span></CardLink
        >
        <TimeAgo class="meta w-20 text-right" :at="r.card.updated_at" />
        <template v-if="canDecide(r.slug)">
          <button v-if="r.card.kind === 'question'" type="button" class="btn" @click="emit('open', r.slug, r.card.number)">Answer</button>
          <template v-else>
            <button type="button" class="btn" :disabled="busy" @click="decide(r, true)">Accept</button>
            <button type="button" class="btn btn-quiet" :disabled="busy" @click="decide(r, false)">Reject</button>
          </template>
        </template>
      </li>
    </ul>
  </section>
</template>
