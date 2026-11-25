<script setup lang="ts">
import { computed, reactive, useId, watch } from 'vue'
import { canGiveRight, sortRights } from '../lib/rights'
import type { AgentRight, SessionGrant } from '../lib/types'

// Puts one chat on duty in a project: until the end it answers questions there for its owner, within the
// instructions. A project the chat already has a grant in starts from that grant, so extending a duty
// keeps its instructions and ticks.
const props = defineProps<{
  projects: string[]
  /** The chat's live grants. */
  grants: SessionGrant[]
  /** What the agent itself holds in a project: the chat has these with or without a grant. */
  inherited: (slug: string) => AgentRight[]
  busy: boolean
}>()
const emit = defineEmits<{
  submit: [slug: string, rights: AgentRight[], duty: { ends_at: string; instructions?: string }]
  cancel: []
}>()

const DUTY_HOURS = 8
const id = useId()

// The value of a datetime-local input: local time to the minute.
function localInput(d: Date): string {
  const p = (n: number): string => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

const form = reactive({
  slug: props.projects[0] ?? '',
  endsAt: localInput(new Date(Date.now() + DUTY_HOURS * 3600 * 1000)),
  instructions: '',
  ready: false,
  decide: false,
})

const grant = computed(() => props.grants.find((g) => g.project === form.slug))
const fromAgent = (right: AgentRight): boolean => props.inherited(form.slug).includes(right)

// A project with a grant starts from it. Leaving such a project for one without a grant starts empty, so one
// project's instructions and ticks never go to another; text typed for a project without a grant is kept.
function fill(from?: string): void {
  const g = grant.value
  if (!g && !props.grants.some((x) => x.project === from)) return
  form.instructions = g?.instructions ?? ''
  form.ready = !!g?.rights.includes('ready')
  form.decide = !!g?.rights.includes('decide')
}
fill()
watch(
  () => form.slug,
  (_, from) => fill(from),
)

// The grant is saved whole, so the chat's other extra rights go with it; the server checks the whole set against the giver.
const rights = computed(() =>
  sortRights([
    ...(grant.value?.rights ?? []).filter((r) => r !== 'ready' && r !== 'decide'),
    'answer',
    ...(form.ready && !fromAgent('ready') ? ['ready'] : []),
    ...(form.decide && !fromAgent('decide') ? ['decide'] : []),
  ]),
)
const end = computed(() => new Date(form.endsAt).getTime())
const inFuture = computed(() => end.value > Date.now())
const mayGive = computed(() => rights.value.every((r) => canGiveRight(form.slug, r)))

function submit(): void {
  if (!form.slug || !inFuture.value || !mayGive.value) return
  emit('submit', form.slug, rights.value, { ends_at: new Date(end.value).toISOString(), instructions: form.instructions.trim() || undefined })
}
</script>

<template>
  <form class="space-y-1.5 bg-sunken p-3" @submit.prevent="submit">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
      <label class="label mb-0" :for="`${id}-project`">Project</label>
      <select :id="`${id}-project`" v-model="form.slug" class="field field-auto">
        <option v-for="p in projects" :key="p" :value="p">{{ p }}</option>
      </select>
      <label class="label mb-0" :for="`${id}-end`">Ends</label>
      <input :id="`${id}-end`" v-model="form.endsAt" type="datetime-local" class="field field-auto" required />
    </div>
    <div class="space-y-1">
      <label class="label" :for="`${id}-text`">Instructions</label>
      <textarea
        :id="`${id}-text`"
        v-model="form.instructions"
        class="field"
        rows="3"
        maxlength="4000"
        placeholder="What it may settle and what is yours, for example: may ready backlog in lanes server and cli; nothing about pricing"
      ></textarea>
    </div>
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      <label class="flex items-center gap-x-1.5" :title="fromAgent('ready') ? 'From the agent' : undefined">
        <input
          type="checkbox"
          :checked="form.ready || fromAgent('ready')"
          :disabled="fromAgent('ready') || !canGiveRight(form.slug, 'ready')"
          @change="form.ready = ($event.target as HTMLInputElement).checked"
        />
        <span>may ready backlog cards</span>
      </label>
      <label class="flex items-center gap-x-1.5" :title="fromAgent('decide') ? 'From the agent' : undefined">
        <input
          type="checkbox"
          :checked="form.decide || fromAgent('decide')"
          :disabled="fromAgent('decide') || !canGiveRight(form.slug, 'decide')"
          @change="form.decide = ($event.target as HTMLInputElement).checked"
        />
        <span>may accept or reject decisions</span>
      </label>
    </div>
    <p class="meta">The chat answers questions in the project until the end time, and what it settles waits in your "Answered for you" list.</p>
    <p v-if="!inFuture" class="meta">The end must be in the future.</p>
    <p v-else-if="!mayGive" class="meta">You need the reviewer role in {{ form.slug }} to give these rights.</p>
    <div class="flex flex-wrap items-center gap-2">
      <button type="submit" class="btn btn-primary" :disabled="busy || !form.slug || !inFuture || !mayGive">Put on duty</button>
      <button type="button" class="btn" @click="emit('cancel')">Cancel</button>
    </div>
  </form>
</template>
