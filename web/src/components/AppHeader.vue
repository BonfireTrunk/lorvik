<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { keyLabel, overlays, useKeymap } from '../composables/useKeymap'
import { actorName } from '../lib/actors'
import { auth, clearSession } from '../lib/auth'
import { unread, unreadLabel } from '../lib/inbox'
import { sectionTo, sections, type NavSection } from '../lib/navigation'
import { projects, setCurrentProject } from '../lib/projects'
import IconInbox from '~icons/lucide/inbox'
import IconKeyboard from '~icons/lucide/keyboard'
import IconLogOut from '~icons/lucide/log-out'
import IconSearch from '~icons/lucide/search'

const route = useRoute()
const router = useRouter()
const slug = computed(() => projects.current)

const items = computed(() => sections
  .filter((section) => section.bar && sectionTo(section, slug.value))
  .map((section) => ({ ...section, to: sectionTo(section, slug.value)! })),
)

function current(item: NavSection): 'page' | undefined {
  return item.names.includes(String(route.name)) ? 'page' : undefined
}

function go(section: NavSection): void {
  const to = sectionTo(section, slug.value)
  if (to) void router.push(to)
}

// Bound here because the header owns the sections; the help lists them too.
useKeymap([
  { keys: '?', label: 'Show or hide this help', group: 'General', run: () => (overlays.help = !overlays.help), layers: ['page', 'panel', 'dialog'] },
  { keys: 'mod+k', label: 'Command palette: pages, #number, search', group: 'General', run: () => (overlays.palette = true) },
  ...sections
    .filter((section) => section.keys)
    .map((section) => ({ keys: section.keys!, label: section.label, group: 'Go to', run: () => go(section) })),
])

function switchTo(e: Event): void {
  const v = (e.target as HTMLSelectElement).value
  if (!v) return
  // Picking a project asks to see that project, so a list in all scope leaves it.
  const { scope, ...query } = route.query
  if (!route.params.slug) {
    setCurrentProject(v)
    if (scope !== undefined) void router.replace({ query })
    return
  }
  const name = route.name === 'activity' || route.name === 'map' || route.name === 'board' ? route.name : 'project'
  void router.push({ name, params: { slug: v }, query: name === 'activity' ? query : undefined })
}
</script>

<template>
  <header class="app-header border-b-2 border-fg bg-canvas">
    <div class="mx-auto flex max-w-[1600px] flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 min-[75rem]:flex-nowrap">
      <router-link to="/" class="shrink-0 text-xl font-semibold no-underline" aria-label="Lorvik, home">Lorvik</router-link>
      <select class="field w-32 min-[40rem]:w-44" :value="slug" aria-label="Project" @change="switchTo">
        <option value="" disabled>Select project</option>
        <option v-for="p in projects.list" :key="p.slug" :value="p.slug">{{ p.name }}</option>
      </select>
      <nav class="order-last -mx-4 w-[calc(100%+2rem)] overflow-x-auto px-4 min-[75rem]:order-none min-[75rem]:mx-0 min-[75rem]:w-auto min-[75rem]:px-0" aria-label="Main">
        <div class="flex gap-0.5">
          <router-link v-for="i in items" :key="i.label" class="nav-link" :to="i.to" :aria-current="current(i)">{{
            i.label
          }}</router-link>
          <button type="button" class="nav-link cursor-pointer min-[75rem]:hidden" @click="clearSession()">Sign out</button>
        </div>
      </nav>
      <div class="ml-auto flex shrink-0 items-center gap-2">
        <button type="button" class="btn btn-quiet" @click="overlays.palette = true">
          <IconSearch />Search<kbd class="kbd hidden min-[85rem]:inline-flex">{{ keyLabel('mod+k') }}</kbd>
        </button>
        <button
          type="button"
          class="btn btn-quiet btn-icon hidden min-[75rem]:inline-flex"
          aria-label="Keyboard shortcuts"
          title="Keyboard shortcuts (?)"
          aria-keyshortcuts="?"
          @click="overlays.help = true"
        >
          <IconKeyboard />
        </button>
        <router-link
          class="btn btn-quiet"
          :to="{ name: 'inbox' }"
          :aria-current="route.name === 'inbox' ? 'page' : undefined"
          :aria-label="unread.count ? `Inbox, ${unreadLabel(unread.count)} unread` : 'Inbox'"
        >
          <IconInbox />Inbox
          <span v-if="unread.count" class="badge badge-count">{{ unreadLabel(unread.count) }}</span>
        </router-link>
        <span v-if="auth.me" class="hidden pl-2 text-base text-muted min-[85rem]:inline">{{ actorName(auth.me.actor) }}</span>
        <button type="button" class="btn btn-quiet btn-icon hidden min-[75rem]:inline-flex" aria-label="Sign out" title="Sign out" @click="clearSession()">
          <IconLogOut />
        </button>
      </div>
    </div>
  </header>
</template>
