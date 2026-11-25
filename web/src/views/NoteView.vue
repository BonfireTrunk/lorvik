<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import ActorName from '../components/ActorName.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadState from '../components/LoadState.vue'
import NoteBody from '../components/NoteBody.vue'
import PagedBody from '../components/PagedBody.vue'
import RevisionHistory from '../components/RevisionHistory.vue'
import StatusBadge from '../components/StatusBadge.vue'
import TimeAgo from '../components/TimeAgo.vue'
import { useProjectQuery } from '../composables/useProjectQuery'
import { ApiError, api } from '../lib/api'
import { canIn, canOrg } from '../lib/roles'
import type { Backlink, HistoryQuery, Note } from '../lib/types'
import IconArrowLeft from '~icons/lucide/arrow-left'
import IconHistory from '~icons/lucide/history'
import IconPencil from '~icons/lucide/pencil'

// key is a slug (with ?project=) or a note id.
const props = defineProps<{ noteKey: string }>()
const note = ref<Note | null>(null)
const error = ref<unknown>(null)
const busy = ref(false)
const history = ref<InstanceType<typeof RevisionHistory> | null>(null)

const project = useProjectQuery()
const canWrite = computed(() => (note.value?.project ? canIn(note.value.project, 'member') : canOrg('member')))

async function load(): Promise<void> {
  note.value = null
  try {
    note.value = await api.note(props.noteKey, project.value)
    error.value = null
  } catch (e) {
    error.value = e
  }
}

async function setArchived(archive: boolean): Promise<void> {
  if (!note.value) return
  busy.value = true
  try {
    note.value = await api.updateNote(note.value.id, undefined, {
      version: note.value.version,
      status: archive ? 'archived' : 'active',
    })
    error.value = null
  } catch (e) {
    error.value = e
    if (e instanceof ApiError && e.isConflict) {
      const keep = e
      await load()
      error.value = keep
    }
  } finally {
    busy.value = false
  }
}

const loadPage = () => api.notePage(note.value!.id, undefined)

function fetchRevisions(q: HistoryQuery) {
  return api.noteRevisions(note.value!.id, undefined, q)
}

function fetchRevision(version: number) {
  return api.noteRevision(note.value!.id, version)
}

function backlinkTo(b: Backlink) {
  if (b.type === 'card' && b.number && b.project) return { name: 'card', params: { slug: b.project, number: b.number } }
  if (b.type === 'note') return { name: 'note', params: { noteKey: b.id } }
  return undefined
}

function backlinkLabel(b: Backlink): string {
  if (b.type === 'card' && b.number) return `#${b.number} ${b.title}`
  return b.title
}

onMounted(load)
watch(() => [props.noteKey, project.value], load)
</script>

<template>
  <div class="page-reading">
    <router-link class="lnk inline-flex items-center gap-1 text-sm" :to="{ name: 'notes' }"><IconArrowLeft class="h-3.5 w-3.5" />Notes</router-link>
    <ErrorBanner :error="error" :retry="load" />
    <LoadState :loading="!note && !error" />
    <template v-if="note">
      <header class="space-y-2">
        <div class="toolbar">
          <h1 class="detail-title">{{ note.title }}</h1>
          <StatusBadge v-if="note.status === 'archived'" status="archived" />
          <div class="ml-auto flex gap-x-2">
            <button type="button" class="btn" :aria-expanded="history?.open ?? false" @click="history?.toggle()"><IconHistory />{{ history?.open ? 'Hide history' : 'History' }}</button>
            <template v-if="canWrite">
              <button type="button" class="btn" :disabled="busy" @click="setArchived(note.status !== 'archived')">
                {{ note.status === 'archived' ? 'Unarchive' : 'Archive' }}
              </button>
              <router-link
                v-if="note.status !== 'archived'"
                class="btn"
                :to="{ name: 'note-edit', params: { noteKey: note.id } }"
                ><IconPencil />Edit</router-link
              >
            </template>
          </div>
        </div>
        <p class="meta flex flex-wrap gap-x-3">
          <span class="font-mono">{{ note.project }}/{{ note.slug }}</span>
          <span>
            updated by <ActorName :who="note.updated_by" />
            <TimeAgo :at="note.updated_at" />
          </span>
          <span>v{{ note.version }}</span>
        </p>
      </header>

      <RevisionHistory
        ref="history"
        :source="note.id"
        :version="note.version"
        :project="note.project"
        :fetch-list="fetchRevisions"
        :fetch-revision="fetchRevision"
        replace-body
        @error="error = $event"
      >
        <PagedBody :page-bytes="note.page_bytes" :version="note.version" :has-text="!!note.body" :load="loadPage">
          <div class="panel p-3"><NoteBody :text="note.body ?? ''" :project="note.project" /></div>
        </PagedBody>
      </RevisionHistory>

      <section v-if="note.backlinks?.length">
        <h2 class="h2">Backlinks</h2>
        <ul class="list">
          <li v-for="b in note.backlinks" :key="`${b.type}${b.id}`" class="list-row">
            <span class="meta w-12 shrink-0">{{ b.type }}</span>
            <router-link v-if="backlinkTo(b)" class="lnk min-w-0 flex-1" :to="backlinkTo(b)!">{{ backlinkLabel(b) }}</router-link>
            <span v-else class="min-w-0 flex-1">{{ backlinkLabel(b) }}</span>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>
