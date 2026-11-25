<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ActorName from '../ActorName.vue'
import KindBadge from '../KindBadge.vue'
import AnswerForm from './AnswerForm.vue'
import BehalfNote from './BehalfNote.vue'
import CardEditForm from './CardEditForm.vue'
import CardComments from './CardComments.vue'
import CardHistory from './CardHistory.vue'
import CardRef from './CardRef.vue'
import ErrorBanner from '../ErrorBanner.vue'
import ForPersonBadge from '../ForPersonBadge.vue'
import ForPersonClear from './ForPersonClear.vue'
import EventRow from '../EventRow.vue'
import LoadState from '../LoadState.vue'
import NoteBody from '../NoteBody.vue'
import PagedBody from '../PagedBody.vue'
import RelayedAnswer from './RelayedAnswer.vue'
import PriorityBadge from '../PriorityBadge.vue'
import StatusBadge from '../StatusBadge.vue'
import StoryBody from './StoryBody.vue'
import TransitionBar from './TransitionBar.vue'
import TimeAgo from '../TimeAgo.vue'
import { actorName, holderChat } from '../../lib/actors'
import { cardActionState } from '../../composables/useCardActions'
import { useAction } from '../../composables/useAction'
import { api } from '../../lib/api'
import { canHumanIn, canIn } from '../../lib/roles'
import { decisionWord } from '../../lib/cards'
import IconCornerLeftUp from '~icons/lucide/corner-left-up'
import IconGitBranch from '~icons/lucide/git-branch'
import IconList from '~icons/lucide/list'
import IconPencil from '~icons/lucide/pencil'
import type { Card, CardLink } from '../../lib/types'

const props = defineProps<{ slug: string; number: string }>()

const card = ref<Card | null>(null)
const editing = ref(false)
const error = ref<unknown>(null)
const storyTitle = ref('')
const { busy: claimBusy, error: claimError, run: runClaim } = useAction()

const num = computed(() => Number(props.number))
const unanswered = computed(() => card.value?.kind === 'question' && !card.value.question?.answer)
// Answering needs reviewer and a human; the server enforces it, the form is just hidden.
const canAnswer = computed(() => canHumanIn(props.slug, 'reviewer'))
// Branch and lane are card edits; the server decides who may make them.
const canEdit = computed(() => canIn(props.slug, 'member'))
// Releasing one's own claim is always allowed; ending or taking over another's live claim is a human triage act. The server enforces both.
const claimLive = computed(() => !!card.value?.claim && Date.parse(card.value.claim.expires_at) > Date.now())
const canTriageClaim = computed(() => canHumanIn(props.slug, 'reviewer') && claimLive.value && !card.value?.claim?.owned)
const canReleaseClaim = computed(() => !!card.value?.claim?.owned || canTriageClaim.value)

async function releaseClaim(): Promise<void> {
  const c = card.value
  if (!c) return
  if (canTriageClaim.value && !window.confirm(`Release the claim held by ${actorName(c.claim!.actor)}?`)) return
  if (await runClaim(async () => void (await api.releaseCard(props.slug, num.value, { version: c.version })))) await load()
}

async function takeOverClaim(): Promise<void> {
  const c = card.value
  if (!c || !window.confirm(`Take over the claim held by ${actorName(c.claim!.actor)}?`)) return
  if (await runClaim(async () => void (await api.claimCard(props.slug, num.value, { version: c.version })))) await load()
}

// A comment writes an event without touching updated_at, so the newest event can be later.
const lastActivity = computed(() => [card.value?.updated_at ?? '', card.value?.events?.[0]?.created_at ?? ''].reduce((a, b) => (Date.parse(b) > Date.parse(a) ? b : a)))
const isDecision = computed(() => card.value?.kind === 'decision')
const isStory = computed(() => card.value?.kind === 'story')
const stateWord = computed(() =>
  card.value && isDecision.value ? decisionWord(card.value.status, card.value.drop_reason, card.value.decision) : '',
)

async function load(): Promise<void> {
  try {
    card.value = await api.card(props.slug, num.value)
    error.value = null
  } catch (e) {
    error.value = e
    return
  }
  markSeen()
  void loadStoryTitle()
}

const loadPage = () => api.cardPage(props.slug, num.value)

// Best effort: without the title the breadcrumb still links to the story.
async function loadStoryTitle(): Promise<void> {
  const parent = card.value?.parent
  if (!parent) {
    storyTitle.value = ''
    return
  }
  const key = `${props.slug}/${num.value}`
  try {
    const story = await api.card(props.slug, parent)
    if (key === `${props.slug}/${num.value}`) storyTitle.value = story.title
  } catch {
    storyTitle.value = ''
  }
}

// Event-driven reloads call load() again; only a different card is a new view.
let seenKey = ''

// Clearing a reply is best effort: a failure must not surface in the card view.
function markSeen(): void {
  const key = `${props.slug}/${num.value}`
  if (key === seenKey) return
  seenKey = key
  void api.markCardSeen(props.slug, num.value).catch(() => undefined)
}

// Open blockers and blocking links share one "Blocked by" list; every other
// relation gets one heading over its cards.
const blockers = computed(() => {
  const open = card.value?.blocked_by ?? []
  const seen = new Set(open.map((b) => `${card.value?.project}/${b.number}`))
  const linked = (card.value?.links ?? [])
    .filter((l) => l.kind === 'blocks' && l.direction === 'in' && !seen.has(`${l.project ?? card.value?.project}/${l.number}`))
    .map((l) => ({ number: l.number, title: l.title, status: l.status, project: l.project }))
  return [...open.map((b) => ({ ...b, project: undefined as string | undefined })), ...linked]
})

const linkGroups = computed(() => {
  const groups = new Map<string, CardLink[]>()
  for (const l of card.value?.links ?? []) {
    if (l.kind === 'blocks' && l.direction === 'in') continue
    const k = linkLabel(l)
    groups.set(k, [...(groups.get(k) ?? []), l])
  }
  return [...groups].map(([label, links]) => ({ label: label.charAt(0).toUpperCase() + label.slice(1), links }))
})

function linkLabel(l: CardLink): string {
  const out = l.direction === 'out'
  switch (l.kind) {
    case 'blocks':
      return out ? 'blocks' : 'blocked by'
    case 'duplicates':
      return out ? 'duplicates' : 'duplicated by'
    case 'supersedes':
      return out ? 'supersedes' : 'superseded by'
    case 'applies':
      return out ? 'applies to' : 'applied by'
    default:
      return 'relates to'
  }
}

onMounted(load)
// A key or menu action moved this card from outside the transition bar.
watch(
  () => cardActionState.changed,
  (c) => c.key === `${props.slug}/${num.value}` && void load(),
)
watch(() => [props.slug, props.number], () => {
  card.value = null
  storyTitle.value = ''
  editing.value = false
  void load()
})
</script>

<template>
  <div class="space-y-6">
    <ErrorBanner :error="error" :retry="load" />
    <LoadState :loading="!card && !error" />

    <template v-if="card">
      <header class="space-y-3">
        <router-link
          v-if="card.parent"
          class="lnk inline-flex max-w-full items-center gap-1.5 text-sm text-muted"
          :to="{ name: 'card', params: { slug, number: card.parent } }"
          ><IconCornerLeftUp class="h-3.5 w-3.5 shrink-0" /><span class="truncate">Story <span class="ident">#{{ card.parent }}</span>{{ storyTitle ? ' ' + storyTitle : '' }}</span></router-link
        >
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <span class="ident text-sm text-muted">#{{ card.number }}</span>
          <StatusBadge v-if="isDecision" :status="stateWord" />
          <template v-else>
            <StatusBadge :status="card.status" />
            <span v-if="card.drop_reason" class="badge badge-st-dropped">{{ card.drop_reason }}</span>
          </template>
          <KindBadge v-if="card.kind !== 'task'" :kind="card.kind" />
          <ForPersonBadge v-if="card.for_person" />
          <PriorityBadge v-if="card.priority !== 'normal'" :priority="card.priority" />
        </div>
        <h1 id="card-title" class="detail-title">{{ card.title }}</h1>
        <p v-if="card.decision && (card.decision.accepted_by || card.decision.supersedes || card.decision.superseded_by)" class="flex flex-wrap items-center gap-x-3 text-sm text-muted">
          <span v-if="card.decision.accepted_by">
            accepted by <ActorName :who="card.decision.accepted_by" />
            <TimeAgo v-if="card.decision.accepted_at" :at="card.decision.accepted_at" />
          </span>
          <span v-if="card.decision.supersedes">
            supersedes
            <router-link class="lnk font-mono" :to="{ name: 'card', params: { slug, number: card.decision.supersedes } }">#{{ card.decision.supersedes }}</router-link>
          </span>
          <span v-if="card.decision.superseded_by">
            superseded by
            <router-link class="lnk font-mono" :to="{ name: 'card', params: { slug, number: card.decision.superseded_by } }">#{{ card.decision.superseded_by }}</router-link>
          </span>
        </p>
        <p v-if="card.fold" class="text-sm text-muted">
          <template v-if="card.fold.into">Folded into <code class="font-mono text-fg">{{ card.fold.into }}</code></template>
          <template v-else>Folded, no contract change</template>
          by <ActorName :who="card.fold.by" />
          <TimeAgo :at="card.fold.at" />
        </p>
        <dl v-if="card.assignee || card.claim" class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
          <template v-if="card.assignee">
            <dt class="text-muted">Assigned</dt>
            <dd><ActorName :who="card.assignee" /></dd>
          </template>
          <template v-if="card.claim">
            <dt class="text-muted">Claimed</dt>
            <dd class="flex flex-wrap items-center gap-1.5">
              <span class="flex min-w-0 items-center gap-1.5">
                <span class="swatch h-2 w-2" style="--c: var(--color-accent)" aria-hidden="true"></span>
                <span>
                  <ActorName :who="card.claim.actor" /><template v-if="card.claim.session"> ({{ holderChat(card.claim.session, card.claim.session_name) }})</template>, lease until
                  <TimeAgo :at="card.claim.expires_at" />
                </span>
              </span>
              <button v-if="canReleaseClaim" type="button" class="btn" :disabled="claimBusy" @click="releaseClaim">Release</button>
              <button v-if="canTriageClaim" type="button" class="btn" :disabled="claimBusy" @click="takeOverClaim">Take over</button>
            </dd>
          </template>
        </dl>
        <ErrorBanner :error="claimError" />
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted">
          <span v-if="card.branch" class="inline-flex items-center gap-1.5" title="Branch"
            ><IconGitBranch class="h-3.5 w-3.5" /><code class="font-mono text-fg">{{ card.branch }}</code></span
          >
          <span v-if="card.lane" class="inline-flex items-center gap-1.5" title="Lane"><IconList class="h-3.5 w-3.5" /><code class="font-mono text-fg">{{ card.lane }}</code></span>
          <button v-if="canEdit && !editing" type="button" class="btn-link" @click="editing = true">
            <IconPencil />{{ card.branch || card.lane ? 'Edit branch and lane' : 'Set branch or lane' }}
          </button>
          <span>created <TimeAgo :at="card.created_at" /> by <ActorName :who="card.created_by" /></span>
          <span :title="`version ${card.version}`">last activity <TimeAgo :at="lastActivity" /></span>
        </div>
        <CardEditForm v-if="editing" :card="card" @changed="load" @close="editing = false" />
      </header>

      <TransitionBar :card="card" @changed="load" />

      <StoryBody v-if="isStory" :card="card" />

      <PagedBody :page-bytes="card.page_bytes" :version="card.version" :has-text="!!card.body" :load="loadPage">
        <h2 class="sr-only">Body</h2>
        <NoteBody :text="card.body ?? ''" :project="slug" />
      </PagedBody>

      <CardHistory :slug="slug" :number="card.number" :version="card.version" />

      <ForPersonClear v-if="unanswered && card.for_person && canAnswer" :card="card" @changed="load" />
      <AnswerForm v-if="unanswered && canAnswer" :card="card" @changed="load" />
      <p v-else-if="unanswered" class="state">Waiting for a reviewer to answer this question.</p>
      <BehalfNote v-else-if="card.behalf" :card="card" :behalf="card.behalf" :can-answer="canAnswer" @changed="load" />
      <RelayedAnswer v-else-if="card.relayed" :card="card" :can-answer="canAnswer" @changed="load" />
      <section v-else-if="card.question?.answer">
        <h2 class="h2">Answer</h2>
        <div class="bg-sunken p-3">
          <NoteBody :text="card.question.answer" :project="slug" />
          <p class="meta mt-1">
            by <ActorName :who="card.question.answered_by" />
            <TimeAgo v-if="card.question.answered_at" :at="card.question.answered_at" />
          </p>
        </div>
      </section>

      <section v-if="blockers.length">
        <h2 class="h2">Blocked by</h2>
        <ul class="space-y-1">
          <CardRef v-for="b in blockers" :key="`${b.project ?? slug}/${b.number}`" :slug="b.project ?? slug" :number="b.number" :title="b.title" :status="b.status" />
        </ul>
      </section>

      <section v-for="g in linkGroups" :key="g.label">
        <h2 class="h2">{{ g.label }}</h2>
        <ul class="space-y-1">
          <CardRef
            v-for="l in g.links"
            :key="`${l.kind}${l.direction}${l.number}`"
            :slug="l.project ?? slug"
            :number="l.number"
            :title="l.title"
            :status="l.status"
          />
        </ul>
      </section>

      <CardComments :slug="slug" :number="card.number" />

      <details v-if="card.events?.length">
        <summary class="h2 mb-0 cursor-pointer hover:text-fg">Recent events ({{ card.events.length }})</summary>
        <ul class="list mt-2">
          <EventRow v-for="e in card.events" :key="e.id" :event="e" :slug="slug" here />
        </ul>
      </details>
    </template>
  </div>
</template>
