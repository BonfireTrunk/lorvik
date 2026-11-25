<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAction } from '../composables/useAction'
import ActorName from './ActorName.vue'
import ErrorBanner from './ErrorBanner.vue'
import LoadState from './LoadState.vue'
import { actorName } from '../lib/actors'
import { allPages, api } from '../lib/api'
import { rightsInWords } from '../lib/rights'
import type { AgentRight, Membership, ProjectMember } from '../lib/types'
import RightsChecks from './RightsChecks.vue'

const props = defineProps<{ slug: string; canManage: boolean; global?: boolean }>()

const PROJECT_ROLES = ['viewer', 'member', 'reviewer'] as const

const open = ref(false)
const loading = ref(false)
const loaded = ref(false)
const { busy, error, run } = useAction()
const members = ref<ProjectMember[]>([])
const orgMembers = ref<Membership[]>([])
const pick = ref('')
const pickRole = ref<string>('member')
const pickRights = ref<AgentRight[]>(['work'])
const pickIsAgent = computed(() => pick.value.startsWith('agent/'))

const active = computed(() => members.value.filter((m) => m.status === 'active'))

// Org actors not yet in the project; the value is "kind/id".
const candidates = computed(() => {
  const have = new Set(active.value.map((m) => m.actor.id))
  return orgMembers.value.filter((m) => m.status === 'active' && !have.has(m.actor.id))
})

async function load(): Promise<void> {
  loading.value = true
  try {
    members.value = await allPages((cursor) => api.projectMembers(props.slug, cursor))
    if (props.canManage) orgMembers.value = await allPages((cursor) => api.members(false, cursor))
    error.value = null
    loaded.value = true
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

function toggle(e: Event): void {
  open.value = (e.target as HTMLDetailsElement).open
  if (open.value && !loaded.value) void load()
}

async function act(fn: () => Promise<unknown>): Promise<void> {
  await run(async () => {
    await fn()
    await load()
  })
}

function setRole(m: ProjectMember, e: Event): void {
  const role = (e.target as HTMLSelectElement).value
  void act(() => api.putProjectMember(props.slug, m.actor.kind, m.actor.id, { role }))
}

function setRights(m: ProjectMember, rights: AgentRight[]): void {
  void act(() => api.putProjectMember(props.slug, m.actor.kind, m.actor.id, { rights }))
}

const remove = (m: ProjectMember) => act(() => api.removeProjectMember(props.slug, m.actor.kind, m.actor.id))

async function add(): Promise<void> {
  const [kind, id] = pick.value.split('/')
  if (!kind || !id) return
  await act(() =>
    api.putProjectMember(props.slug, kind, id, kind === 'agent' ? { rights: pickRights.value } : { role: pickRole.value }),
  )
  pick.value = ''
}
</script>

<template>
  <details @toggle="toggle">
    <summary class="meta cursor-pointer focus-visible:outline-2 focus-visible:outline-accent">
      Members<template v-if="loaded"> ({{ active.length }})</template>
    </summary>
    <div v-if="open" class="mt-2 space-y-2">
      <ErrorBanner :error="error" :retry="load" />
      <LoadState :loading="loading && !loaded" :empty="loaded && !active.length" empty-text="No project members yet." />
      <p v-if="global" class="meta">Global is open to every member of the org with their shared-project role; people and agents with only project access read it as viewers. Roles added here change nothing.</p>
      <ul v-if="active.length" class="list">
        <li v-for="m in active" :key="m.actor.id" class="list-row flex-wrap items-center gap-y-1.5">
          <span class="min-w-0 flex-1 basis-32 truncate font-medium"><ActorName :who="m.actor" /></span>
          <span class="badge">{{ m.actor.kind }}</span>
          <div class="ml-auto flex items-center gap-x-2">
            <RightsChecks
              v-if="m.actor.kind === 'agent' && canManage && !global"
              :model-value="m.rights ?? []"
              :slug="slug"
              :label="`${actorName(m.actor)} in ${slug}`"
              :disabled="busy"
              required
              @update:model-value="setRights(m, $event)"
            />
            <span v-else-if="m.actor.kind === 'agent'" class="meta">{{ rightsInWords(m.rights) }}</span>
            <select
              v-else-if="canManage && !global"
              class="field field-auto"
              :value="m.role"
              :disabled="busy"
              :aria-label="`Role of ${actorName(m.actor)}`"
              @change="setRole(m, $event)"
            >
              <option v-for="r in PROJECT_ROLES" :key="r" :value="r">{{ r }}</option>
            </select>
            <span v-else class="meta">{{ m.role }}</span>
            <button
              v-if="canManage && !global"
              type="button"
              class="btn btn-danger"
              :disabled="busy"
              :aria-label="`Remove ${actorName(m.actor)}`"
              @click="remove(m)"
            >
              Remove
            </button>
          </div>
        </li>
      </ul>
      <form v-if="canManage && !global && loaded" class="flex flex-wrap items-center gap-x-3 gap-y-1.5" @submit.prevent="add">
        <select v-model="pick" class="field field-auto" aria-label="Actor to add">
          <option value="" disabled>{{ candidates.length ? 'Add org member…' : 'Everyone is already a member' }}</option>
          <option v-for="c in candidates" :key="c.actor.id" :value="`${c.actor.kind}/${c.actor.id}`">
            <ActorName :who="c.actor" /> ({{ c.actor.kind }})
          </option>
        </select>
        <RightsChecks v-if="pickIsAgent" v-model="pickRights" :slug="slug" :label="`new member in ${slug}`" required />
        <select v-else v-model="pickRole" class="field field-auto" aria-label="Role to grant">
          <option v-for="r in PROJECT_ROLES" :key="r" :value="r">{{ r }}</option>
        </select>
        <button class="btn" type="submit" :disabled="busy || !pick || (pickIsAgent && !pickRights.length)">Add</button>
      </form>
    </div>
  </details>
</template>
