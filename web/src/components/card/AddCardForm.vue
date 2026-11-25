<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAction } from '../../composables/useAction'
import { MAX_PAGE, allPages, api } from '../../lib/api'
import { projects } from '../../lib/projects'
import { sendOnModEnter } from '../../lib/sendKey'
import ErrorBanner from '../ErrorBanner.vue'
import type { Card, CardBrief, CardKind, Priority } from '../../lib/types'
import MarkdownHint from '../MarkdownHint.vue'

// pickProject adds a project select (the global project included) for pages
// that are not tied to one board; otherwise cards go to `slug`.
const props = defineProps<{ slug: string; preset?: CardKind; pickProject?: boolean }>()
const emit = defineEmits<{ created: [card: Card]; close: [] }>()

const title = ref('')
const kind = ref<CardKind>(props.preset ?? 'task')
const priority = ref<Priority>('normal')
const body = ref('')
const project = ref(props.slug)
const parent = ref('')
const stories = ref<CardBrief[]>([])
const { busy, error, run } = useAction()

const targetSlug = computed(() => (props.pickProject ? project.value : props.slug))
const activeProjects = computed(() => projects.list.filter((p) => p.status === 'active'))
// A story is top level, so it never picks a parent.
const canPickParent = computed(() => kind.value !== 'story')

// Only open stories can take a child; a closed story refuses new children.
async function loadStories(): Promise<void> {
  const slug = targetSlug.value
  parent.value = ''
  try {
    const items = await allPages((cursor) => api.cards(slug, { kind: 'story', open: true, limit: MAX_PAGE, cursor }))
    if (slug === targetSlug.value) stories.value = items
  } catch {
    // a viewer who cannot read the project has no stories to offer
    stories.value = []
  }
}

watch(targetSlug, () => void loadStories(), { immediate: true })

async function submit(): Promise<void> {
  await run(async () => {
    const card = await api.createCard(targetSlug.value, {
      kind: kind.value,
      title: title.value.trim(),
      body: body.value.trim() || undefined,
      parent: canPickParent.value && parent.value ? Number(parent.value) : undefined,
      priority: priority.value,
      status: 'ready',
    })
    title.value = ''
    body.value = ''
    parent.value = ''
    emit('created', card)
  })
}


const canSubmit = computed(() => !busy.value && !!title.value.trim() && !!targetSlug.value)
const onBodyKeydown = sendOnModEnter(() => canSubmit.value, () => void submit())
</script>

<template>
  <form class="space-y-2" aria-label="Add card" @submit.prevent="submit">
    <select v-if="pickProject" v-model="project" class="field" aria-label="Project">
      <option v-for="p in activeProjects" :key="p.slug" :value="p.slug">
        {{ p.name }}{{ p.scope === 'global' ? ' (global)' : '' }}
      </option>
    </select>
    <input v-model="title" class="field" placeholder="Title" required maxlength="200" aria-label="Title" autofocus />
    <div class="flex gap-2">
      <select v-model="kind" class="field" aria-label="Kind">
        <option value="task">task</option>
        <option value="question">question</option>
        <option value="story">story</option>
        <option value="decision">decision</option>
      </select>
      <select v-model="priority" class="field" aria-label="Priority">
        <option>low</option>
        <option>normal</option>
        <option>high</option>
        <option>urgent</option>
      </select>
    </div>
    <select v-if="canPickParent && stories.length" v-model="parent" class="field" aria-label="Parent story">
      <option value="">No parent story</option>
      <option v-for="s in stories" :key="s.number" :value="String(s.number)">S#{{ s.number }} {{ s.title }}</option>
    </select>
    <textarea v-model="body" class="field" rows="12" placeholder="Body (optional)" aria-label="Body" aria-describedby="add-card-body-hint" @keydown="onBodyKeydown"></textarea>
    <MarkdownHint id="add-card-body-hint">Ctrl+Enter adds.</MarkdownHint>
    <ErrorBanner :error="error" />
    <div class="flex gap-2">
      <button class="btn btn-primary" :disabled="!canSubmit">Add to ready</button>
      <button type="button" class="btn btn-quiet" @click="emit('close')">Cancel</button>
    </div>
  </form>
</template>
