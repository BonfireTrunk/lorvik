<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAction } from '../composables/useAction'
import { api } from '../lib/api'
import { loadProjects } from '../lib/projects'
import { sendOnModEnter } from '../lib/sendKey'
import { slugFromName } from '../lib/slug'
import type { Project } from '../lib/types'
import ErrorBanner from './ErrorBanner.vue'
import MarkdownHint from './MarkdownHint.vue'

const emit = defineEmits<{ created: [project: Project]; close: [] }>()

const name = ref('')
const slug = ref('')
const description = ref('')
const visibility = ref<'' | 'org' | 'private'>('')
// The slug follows the name until the person types in the slug field, and again once they empty it.
const slugEdited = ref(false)
const { busy, error, run } = useAction()

function onName(): void {
  if (!slugEdited.value) slug.value = slugFromName(name.value)
}

function onSlug(): void {
  slug.value = slug.value.toLowerCase()
  slugEdited.value = slug.value !== ''
}

async function submit(): Promise<void> {
  let project: Project | undefined
  const ok = await run(async () => {
    project = await api.createProject({
      slug: slug.value.trim(),
      name: name.value.trim(),
      description: description.value.trim() || undefined,
      visibility: visibility.value || undefined,
    })
  })
  if (!ok || !project) return
  // The project exists now; a failed reload must not keep the form open for a second create.
  await loadProjects(true).catch(() => {})
  emit('created', project)
}

const canSubmit = computed(() => !busy.value && !!name.value.trim() && !!slug.value.trim())
const onDescriptionKeydown = sendOnModEnter(() => canSubmit.value, () => void submit())
</script>

<template>
  <form class="space-y-2" aria-label="New project" @submit.prevent="submit">
    <input
      v-model="name"
      class="field"
      placeholder="Name"
      required
      maxlength="200"
      aria-label="Name"
      autofocus
      @input="onName"
    />
    <input
      v-model="slug"
      class="field font-mono"
      placeholder="slug"
      required
      maxlength="64"
      aria-label="Slug"
      aria-describedby="new-project-slug-hint"
      autocapitalize="none"
      spellcheck="false"
      @input="onSlug"
    />
    <p id="new-project-slug-hint" class="meta">Lowercase letters, digits and hyphens, not starting or ending with a hyphen; used in URLs and the CLI. It cannot be changed later.</p>
    <textarea
      v-model="description"
      class="field"
      rows="6"
      placeholder="Description (optional)"
      aria-label="Description"
      aria-describedby="new-project-description-hint"
      maxlength="4000"
      @keydown="onDescriptionKeydown"
    ></textarea>
    <MarkdownHint id="new-project-description-hint">Ctrl+Enter creates.</MarkdownHint>
    <select v-model="visibility" class="field" aria-label="Visibility">
      <option value="">Org default</option>
      <option value="org">Everyone in the org</option>
      <option value="private">Private</option>
    </select>
    <ErrorBanner :error="error" />
    <div class="flex gap-2">
      <button class="btn btn-primary" :disabled="!canSubmit">Create project</button>
      <button type="button" class="btn btn-quiet" @click="emit('close')">Cancel</button>
    </div>
  </form>
</template>
