<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { PAGE_MIN_HEIGHT, pageSize, pageSrcdoc, reportedHeight } from '../lib/pageFrame'
import PageExpanded from './PageExpanded.vue'
import IconMaximize from '~icons/lucide/maximize-2'

// The agent-written page of a card or note. The frame is sandboxed with
// allow-scripts only (never allow-same-origin or any navigation, popup, form,
// modal or download flag) and the srcdoc starts with the lockdown prefix of
// lib/pageFrame.ts. The parent only listens: it never posts to the frame.
const props = defineProps<{ html: string; bytes: number }>()

const frame = ref<HTMLIFrameElement | null>(null)
const height = ref(PAGE_MIN_HEIGHT)
const expanded = ref(false)

function onMessage(e: MessageEvent): void {
  if (!frame.value || e.source !== frame.value.contentWindow) return
  const h = reportedHeight(e.data)
  if (h !== null) height.value = h
}

onMounted(() => window.addEventListener('message', onMessage))
onBeforeUnmount(() => window.removeEventListener('message', onMessage))

const label = computed(() => `Agent-written page, ${pageSize(props.bytes)}`)
</script>

<template>
  <div>
    <div class="mb-2 flex items-center gap-x-3 text-sm">
      <span class="font-medium">Page</span>
      <span class="meta ident">{{ pageSize(bytes) }}</span>
      <span class="meta" title="Written by an agent. It cannot see your session or reach the network, except to load script libraries from cdnjs.cloudflare.com, cdn.jsdelivr.net and unpkg.com.">Runs sandboxed</span>
      <button type="button" class="btn btn-quiet ml-auto" @click="expanded = true"><IconMaximize />Expand</button>
    </div>
    <div v-if="!expanded" class="border border-line">
      <iframe
        ref="frame"
        class="block w-full border-0 bg-white"
        :style="{ height: height + 'px' }"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        :title="label"
        :srcdoc="pageSrcdoc(html)"
      ></iframe>
    </div>
    <div v-else class="state border border-line p-3 text-sm" :style="{ height: height + 'px' }">Page is open full screen.</div>
    <PageExpanded v-if="expanded" :html="html" :label="label" @close="expanded = false" />
  </div>
</template>
