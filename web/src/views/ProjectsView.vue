<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import ErrorBanner from '../components/ErrorBanner.vue'
import IconPlus from '~icons/lucide/plus'
import LoadState from '../components/LoadState.vue'
import ProjectAgentJoin from '../components/ProjectAgentJoin.vue'
import NewProjectPanel from '../components/NewProjectPanel.vue'
import ProjectMembers from '../components/ProjectMembers.vue'
import StatusBadge from '../components/StatusBadge.vue'
import TimeAgo from '../components/TimeAgo.vue'
import WorkingNow from '../components/WorkingNow.vue'
import { useAction } from '../composables/useAction'
import { api } from '../lib/api'
import { projects, loadProjects, setCurrentProject } from '../lib/projects'
import { canHumanIn, canOrg, isUser } from '../lib/roles'
import type { Concurrency, Project } from '../lib/types'
import { countByProject, liveClaims } from '../lib/working'
import NoteBody from '../components/NoteBody.vue'

const router = useRouter()
const { error, run } = useAction()
const creating = ref(false)
const saving = ref('')
const workingCounts = computed(() => countByProject(liveClaims()))

// The global project comes first: it holds what applies across projects.
const ordered = computed(() => [...projects.list].sort((a, b) => Number(b.scope === 'global') - Number(a.scope === 'global')))

// The server limits this to human admins; the select is just hidden from everyone else.
function canSetConcurrency(p: Project): boolean {
  return canHumanIn(p.slug, 'admin') && p.status === 'active'
}

// Creating a project is an org-level right the server gives to human admins; the button is just hidden from everyone else.
const canCreate = computed(() => isUser() && canOrg('admin'))
const onlyGlobal = computed(() => projects.loaded && projects.list.every((p) => p.scope === 'global'))

async function created(p: Project): Promise<void> {
  creating.value = false
  setCurrentProject(p.slug)
  await router.push({ name: 'board', params: { slug: p.slug } })
}

async function setConcurrency(p: Project, value: Concurrency): Promise<void> {
  saving.value = p.slug
  try {
    const ok = await run(async () => {
      const updated = await api.updateProject(p.slug, { concurrency: value })
      p.concurrency = updated.concurrency
    })
    if (!ok) await loadProjects(true)
  } finally {
    saving.value = ''
  }
}

async function refresh(): Promise<void> {
  await run(() => loadProjects(true))
}

onMounted(refresh)
</script>

<template>
  <div class="page">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="space-y-1">
        <h1 class="page-title">Projects</h1>
        <p class="meta">Every project you can see, with its concurrency and who works in it.</p>
        <p v-if="onlyGlobal" class="meta">
          {{ canCreate ? 'This org has no projects yet. Create one to start a board.' : 'This org has no projects yet. Ask an admin to create one.' }}
        </p>
      </div>
      <button v-if="canCreate" type="button" class="btn btn-primary" @click="creating = true"><IconPlus />New project</button>
    </div>
    <WorkingNow />
    <ErrorBanner :error="error" :retry="refresh" />
    <LoadState
      :loading="!projects.loaded && !error"
      :empty="projects.loaded && !projects.list.length"
      empty-text="No projects yet. Create one with: lorvik project create"
    />
    <ul v-if="projects.list.length" class="list">
      <li v-for="p in ordered" :key="p.id" class="space-y-1.5 px-1 py-3">
        <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <router-link :to="{ name: 'project', params: { slug: p.slug } }" class="lnk font-medium">{{ p.name }}</router-link>
          <span class="meta font-mono">{{ p.slug }}</span>
          <span v-if="p.scope === 'global'" class="badge badge-accent">global</span>
          <span v-if="p.slug === projects.current" class="badge">last used</span>
          <span v-if="p.visibility === 'private'" class="badge">private</span>
          <StatusBadge v-if="p.status === 'archived'" status="archived" />
          <span v-if="workingCounts[p.slug]" class="badge">{{ workingCounts[p.slug] }} working</span>
          <span class="ml-auto flex items-baseline gap-3">
            <span class="meta">updated <TimeAgo :at="p.updated_at" /></span>
            <span v-if="p.role" class="badge">{{ p.role }}</span>
          </span>
        </div>
        <NoteBody v-if="p.description" :text="p.description" :project="p.slug" class="text-muted" />
        <div class="meta flex items-center gap-3">
          <label :for="`conc-${p.slug}`" title="parallel: a lead fans child cards out to subagents; serial: work them in order">Concurrency</label>
          <select
            v-if="canSetConcurrency(p)"
            :id="`conc-${p.slug}`"
            class="field field-auto"
            :value="p.concurrency"
            :disabled="saving === p.slug"
            @change="setConcurrency(p, ($event.target as HTMLSelectElement).value as Concurrency)"
          >
            <option value="parallel">parallel</option>
            <option value="serial">serial</option>
          </select>
          <span v-else :id="`conc-${p.slug}`" class="badge">{{ p.concurrency }}</span>
        </div>
        <ProjectAgentJoin v-if="p.scope !== 'global'" :slug="p.slug" :can-manage="canSetConcurrency(p)" />
        <ProjectMembers :slug="p.slug" :global="p.scope === 'global'" :can-manage="canSetConcurrency(p)" />
      </li>
    </ul>
    <NewProjectPanel v-if="creating" @created="created" @close="creating = false" />
  </div>
</template>
