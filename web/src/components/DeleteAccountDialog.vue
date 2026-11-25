<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAction } from '../composables/useAction'
import { useModalDialog } from '../composables/useModalDialog'
import { api } from '../lib/api'
import type { DeletionPreview } from '../lib/types'
import { leaveDeletedAccount } from '../router'
import ErrorBanner from './ErrorBanner.vue'

// secret, when given, is a sign-in token (the Create-an-org screen); otherwise the session's own token is used.
const props = defineProps<{ login: string; secret?: string }>()
const emit = defineEmits<{ close: []; deleted: [] }>()

const router = useRouter()
const { busy, error, run } = useAction()
const typed = ref('')
const matches = computed(() => typed.value === props.login)
// What a delete would close or be refused by; a sign-in token belongs to no org, so there is nothing to ask.
// Its own action, so loading it neither disables Cancel nor mixes its failure with the delete's.
const preview = ref<DeletionPreview | null>(null)
const previewAction = useAction()
const blocked = computed(() => preview.value?.blocked_by ?? [])
const ready = computed(() => !!props.secret || (preview.value !== null && blocked.value.length === 0))

function loadPreview(): void {
  void previewAction.run(async () => {
    preview.value = await api.deletionPreview()
  })
}

onMounted(() => {
  if (!props.secret) loadPreview()
})

function count(n: number, one: string): string {
  return `${n} ${one}${n === 1 ? '' : 's'}`
}

async function submit(): Promise<void> {
  if (!matches.value || !ready.value) return
  const ok = await run(() => api.deleteMe(props.login, props.secret))
  if (!ok) return
  emit('deleted')
  // A sign-in token lives in the choice screen's memory only, and that screen is already the sign-in page;
  // a stored session, if any, is someone else's.
  if (props.secret) await router.replace({ path: '/login', query: { notice: 'deleted' } })
  else leaveDeletedAccount()
}

const { dialog: dlg, dialogEvents } = useModalDialog(() => emit('close'))
</script>

<template>
  <dialog ref="dlg" class="float m-auto w-full max-w-md p-5 text-fg backdrop:bg-scrim" aria-label="Delete my account" v-on="dialogEvents">
    <form class="space-y-3" @submit.prevent="submit">
      <h2 class="page-title">Delete my account</h2>
      <p>This cannot be undone.</p>
      <div>
        <p class="font-medium">What goes</p>
        <ul class="list-disc pl-5">
          <li>Your place in every org.</li>
          <li>The agents you created; they are revoked.</li>
          <li>Your GitHub link and password.</li>
          <li>Your tokens.</li>
        </ul>
      </div>
      <div v-if="preview?.closes.length">
        <p class="font-medium">Orgs that close with it</p>
        <p class="meta">You are the only person in these, so nobody can reach their projects and cards again.</p>
        <ul class="list-disc pl-5">
          <li v-for="(o, i) in preview.closes" :key="i">Closes {{ o.name }}: {{ count(o.projects, 'project') }}, {{ count(o.cards, 'card') }}.</li>
        </ul>
      </div>
      <p v-if="blocked.length" role="alert">
        You are the only owner of {{ blocked.join(', ') }}, beside other people. Make one of them owner on the Access page first.
      </p>
      <div>
        <p class="font-medium">What stays</p>
        <p>Your name on past cards, comments and history, with "(deleted)" after it.</p>
      </div>
      <div>
        <label class="label" for="delete-confirm">Type your login, {{ login }}, to confirm</label>
        <input id="delete-confirm" v-model="typed" class="field" autocomplete="off" autofocus />
      </div>
      <template v-if="previewAction.error.value">
        <ErrorBanner :error="previewAction.error.value" />
        <button type="button" class="btn" :disabled="previewAction.busy.value" @click="loadPreview">Retry</button>
      </template>
      <ErrorBanner :error="error" />
      <div class="flex justify-end gap-2">
        <button type="button" class="btn" :disabled="busy" @click="emit('close')">Cancel</button>
        <button type="submit" class="btn btn-danger" :disabled="busy || !matches || !ready">Delete my account</button>
      </div>
    </form>
  </dialog>
</template>
