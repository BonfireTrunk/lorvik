<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadState from '../components/LoadState.vue'
import { useAction, useCanSubmit } from '../composables/useAction'
import { useProjectQuery } from '../composables/useProjectQuery'
import { ApiError, api } from '../lib/api'
import { sendOnModEnter } from '../lib/sendKey'
import type { Note } from '../lib/types'
import IconArrowLeft from '~icons/lucide/arrow-left'
import MarkdownHint from '../components/MarkdownHint.vue'

const props = defineProps<{ noteKey: string }>()
const router = useRouter()

const project = useProjectQuery()
const note = ref<Note | null>(null)
const title = ref('')
const body = ref('')
const { busy, error, run } = useAction()
const canSubmit = useCanSubmit(busy, body)

async function load(): Promise<void> {
  try {
    const n = await api.note(props.noteKey, project.value)
    note.value = n
    title.value = n.title
    body.value = n.body ?? ''
  } catch (e) {
    error.value = e
  }
}

async function save(): Promise<void> {
  if (!note.value) return
  const n = note.value
  await run(
    async () => {
      await api.updateNote(n.id, undefined, { version: n.version, title: title.value, body: body.value })
      await router.push({ name: 'note', params: { noteKey: n.id } })
    },
    {
      // Keep the user's edits; only the version needs to move on conflict.
      onError: (e) => {
        if (e instanceof ApiError && e.isConflict && e.problem.current_version) n.version = e.problem.current_version
      },
    },
  )
}

const onKeydown = sendOnModEnter(() => canSubmit.value, () => void save())

onMounted(load)
</script>

<template>
  <form v-if="note" class="page-reading" @submit.prevent="save">
    <router-link class="lnk inline-flex items-center gap-1 text-sm" :to="{ name: 'note', params: { noteKey: note.id } }"
      ><IconArrowLeft class="h-3.5 w-3.5" />Cancel</router-link
    >
    <h1 class="page-title">Edit note <span class="font-mono text-sm text-muted">{{ note.slug }}</span></h1>
    <div>
      <label class="label" for="t">Title</label>
      <input id="t" v-model="title" class="field" maxlength="200" />
    </div>
    <div>
      <label class="label" for="b">Body</label>
      <textarea id="b" v-model="body" class="field font-mono" rows="20" aria-describedby="b-hint" @keydown="onKeydown"></textarea>
      <MarkdownHint id="b-hint">Ctrl+Enter saves.</MarkdownHint>
    </div>
    <ErrorBanner :error="error" />
    <p v-if="error && note" class="meta">
      Your edits are kept. If someone else changed the note, the version was updated to v{{ note.version }}; review and save again.
    </p>
    <button type="submit" class="btn btn-primary" :disabled="!canSubmit">Save (v{{ note.version }})</button>
  </form>
  <div v-else class="page-reading">
    <ErrorBanner :error="error" :retry="load" />
    <LoadState :loading="!error" />
  </div>
</template>
