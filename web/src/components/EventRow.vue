<script setup lang="ts">
import { computed } from 'vue'
import ActorName from './ActorName.vue'
import TimeAgo from './TimeAgo.vue'
import type { RouteLocationRaw } from 'vue-router'
import { actorName } from '../lib/actors'
import { describeEvent } from '../lib/events'
import type { LorvikEvent } from '../lib/types'

// here: the row sits on the event's own card, which it neither names nor links.
const props = defineProps<{ event: LorvikEvent; slug?: string; showProject?: boolean; here?: boolean }>()

const sentence = computed(() => describeEvent(props.event, (id) => actorName(id), props.here))

const to = computed<RouteLocationRaw | undefined>(() => {
  const e = props.event
  if (props.here && e.subject.type === 'card') return undefined
  const project = e.project ?? props.slug
  switch (e.subject.type) {
    case 'card':
      return e.subject.number && project
        ? { name: 'card', params: { slug: project, number: e.subject.number } }
        : undefined
    case 'note':
      return { name: 'note', params: { noteKey: e.subject.id } }
    case 'project': {
      const slug = typeof e.payload?.slug === 'string' ? e.payload.slug : ''
      return slug ? { name: 'project', params: { slug } } : undefined
    }
    default:
      return undefined
  }
})
</script>

<template>
  <li class="list-row text-sm">
    <TimeAgo class="meta w-20 shrink-0" :at="event.created_at" />
    <span v-if="showProject" class="meta w-24 shrink-0 truncate font-mono">{{ event.project }}</span>
    <span class="w-28 shrink-0 truncate font-medium" :title="event.actor.kind"><ActorName :who="event.actor" /></span>
    <span class="min-w-0 break-words">
      <router-link v-if="to" class="lnk-quiet" :to="to">{{ sentence }}</router-link>
      <template v-else>{{ sentence }}</template>
    </span>
  </li>
</template>
