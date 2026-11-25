<script setup lang="ts">
import { ref } from 'vue'
import ActorName from '../components/ActorName.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import LoadMore from '../components/LoadMore.vue'
import LoadState from '../components/LoadState.vue'
import ScopeSelect from '../components/ScopeSelect.vue'
import StatusBadge from '../components/StatusBadge.vue'
import TimeAgo from '../components/TimeAgo.vue'
import { usePagedList } from '../composables/usePagedList'
import { api } from '../lib/api'
import { useListScope } from '../composables/useListScope'
import { projects, scopeProject } from '../lib/projects'

const scope = useListScope()
const archived = ref(false)

const { items, remaining, loading, error, hasMore, load } = usePagedList((limit, cursor) =>
  api.notes({ project: scopeProject(scope.value), archived: archived.value || undefined, limit, cursor }),
  { autoLoad: true, source: [scope, archived, () => projects.current] },
)

</script>

<template>
  <div class="page">
    <h1 class="page-title">Notes</h1>
    <p class="meta">Written notes, each addressed by project and slug.</p>
    <div class="toolbar">
      <ScopeSelect v-model="scope" />
      <div class="cells" role="group" aria-label="Archived">
        <button type="button" class="cell" :aria-pressed="archived" @click="archived = !archived">Include archived</button>
      </div>
    </div>
    <ErrorBanner :error="error" :retry="() => load()" />
    <LoadState :loading="loading && !items.length" :empty="!loading && !items.length && !error" empty-text="No notes here." />
    <ul v-if="items.length" class="list">
      <li v-for="n in items" :key="n.id">
        <router-link :to="{ name: 'note', params: { noteKey: n.id } }" class="list-row flex-wrap">
          <span class="min-w-0 flex-1 basis-48 truncate font-medium">{{ n.title }}</span>
          <span class="meta w-48 truncate font-mono">{{ n.project }}/{{ n.slug }}</span>
          <span class="meta w-28 truncate"><ActorName :who="n.updated_by" /></span>
          <TimeAgo class="meta w-16 text-right" :at="n.updated_at" />
          <span class="w-20 shrink-0"><StatusBadge v-if="n.status === 'archived'" status="archived" /></span>
        </router-link>
      </li>
    </ul>
    <LoadMore :has-more="hasMore" :loading="loading" :remaining="remaining" @load="load(true)" />
  </div>
</template>
