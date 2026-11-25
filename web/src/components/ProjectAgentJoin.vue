<script setup lang="ts">
import { ref } from 'vue'
import { useAction } from '../composables/useAction'
import ErrorBanner from './ErrorBanner.vue'
import LoadState from './LoadState.vue'
import { api } from '../lib/api'
import { isUser } from '../lib/roles'
import type { AgentJoin, Project } from '../lib/types'

const props = defineProps<{ slug: string; canManage: boolean }>()

const open = ref(false)
const loading = ref(false)
const project = ref<Project | null>(null)
const { busy, error, run } = useAction()

// my_agent_join is only on the single-project read, so the list's row is not enough.
async function load(): Promise<void> {
  loading.value = true
  try {
    project.value = await api.project(props.slug)
    error.value = null
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

function toggle(e: Event): void {
  open.value = (e.target as HTMLDetailsElement).open
  if (open.value && !project.value) void load()
}

async function setMine(e: Event): Promise<void> {
  const value = (e.target as HTMLSelectElement).value as AgentJoin
  const ok = await run(async () => {
    project.value = await api.putAgentJoin(props.slug, value)
  })
  if (!ok) await load()
}

async function setAsk(e: Event): Promise<void> {
  const value = (e.target as HTMLSelectElement).value as 'follow' | 'ask'
  const ok = await run(async () => {
    // The update answers without my_agent_join, so the caller's own rule is kept.
    const updated = await api.updateProject(props.slug, { agent_join: value })
    project.value = { ...updated, my_agent_join: project.value?.my_agent_join }
  })
  if (!ok) await load()
}
</script>

<template>
  <details @toggle="toggle">
    <summary class="meta cursor-pointer focus-visible:outline-2 focus-visible:outline-accent">Agents joining</summary>
    <div v-if="open" class="mt-2 space-y-2">
      <ErrorBanner :error="error" :retry="load" />
      <LoadState :loading="loading && !project" />
      <template v-if="project">
        <div v-if="isUser()" class="meta flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <label :for="`mine-${slug}`">My agents here</label>
          <select :id="`mine-${slug}`" class="field field-auto" :value="project.my_agent_join || 'follow'" :disabled="busy" @change="setMine">
            <option value="follow">Follow my settings</option>
            <option value="allow">Allow without asking</option>
            <option value="ask">Always ask</option>
          </select>
        </div>
        <div v-if="canManage" class="meta flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <label :for="`ask-${slug}`" title="Members cannot loosen this">Agents always ask here</label>
          <select :id="`ask-${slug}`" class="field field-auto" :value="project.agent_join" :disabled="busy" @change="setAsk">
            <option value="follow">No, follow each owner's settings</option>
            <option value="ask">Yes, every agent asks</option>
          </select>
        </div>
      </template>
    </div>
  </details>
</template>
