<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import DeleteAccountDialog from '../components/DeleteAccountDialog.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import { useAction } from '../composables/useAction'
import { ApiError, api, startGitHubLink } from '../lib/api'
import { auth, clearSession, setSession, takeHandoff } from '../lib/auth'
import type { SignInOrg } from '../lib/types'

const route = useRoute()
const router = useRouter()

type Mode = 'token' | 'password'
const mode = ref<Mode>('token')
const token = ref('')
const login = ref('')
const password = ref('')
const { busy, error, run } = useAction()
// Set once POST /login answers 404: the server has password login off.
const passwordOff = ref(false)

const githubEnabled = ref(false)
// The user of a session this browser already holds; a plain GitHub sign-in would create a second user instead of linking.
const signedInAs = ref<{ name: string; canLink: boolean } | null>(null)

// The sign-in token of a sign-in that is not for one org yet, and the orgs it may enter. Memory only: it lives ten minutes and is never stored.
// viaGitHub: the hint about an unlinked GitHub account only makes sense after a GitHub sign-in.
const choice = ref<{ secret: string; login: string; orgs: SignInOrg[]; viaGitHub: boolean } | null>(null)
const deleting = ref(false)
const notice = ref('')
const expired = ref(false)

// Set when the person came to pick an org again: the sign-in then asks for the choice even if only one org fits.
const choose = computed(() => !!route.query.choose)

const next = computed(() =>
  typeof route.query.next === 'string' && route.query.next.startsWith('/') && !route.query.next.startsWith('//')
    ? route.query.next
    : '/',
)

const callbackError = computed(() => {
  const login = typeof route.query.login === 'string' ? route.query.login : ''
  switch (route.query.error) {
    case 'pending':
      return `Signed in on GitHub as ${login}. Your access to ${typeof route.query.org_name === 'string' && route.query.org_name ? route.query.org_name : 'this org'} is waiting for an admin; ask them to grant your role on the Access page.`
    case 'denied':
      return 'GitHub did not authorise the sign-in.'
    case 'org':
      return 'This sign-in link names an org that does not exist.'
    case 'github':
      return 'GitHub did not answer; try again.'
    default:
      return ''
  }
})

// The credential awaiting a successful GET /me. The handoff cookie is already gone by then, so a transient
// failure must not lose it; it lives in memory only and is dropped on success or when the server rejects it.
const kept = ref('')
const retryable = computed(() => kept.value !== '' && error.value !== null)

async function finish(secret: string): Promise<void> {
  kept.value = secret
  try {
    const { token, me } = await api.signIn(secret)
    setSession(token, me)
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) kept.value = ''
    throw e
  }
  kept.value = ''
  await router.replace(next.value)
}

async function retry(): Promise<void> {
  const secret = kept.value
  await run(() => finish(secret))
}

function signInWithGitHub(): void {
  window.location.assign(`/auth/github/start?next=${encodeURIComponent(next.value)}${choose.value ? '&choose=1' : ''}`)
}

async function openChoice(secret: string, viaGitHub: boolean): Promise<void> {
  const found = await api.signInOrgs(secret)
  choice.value = { secret, login: found.login, orgs: found.orgs, viaGitHub }
}

function leaveChoice(): void {
  choice.value = null
  deleting.value = false
  expired.value = false
}

async function inChoice(fn: () => Promise<void>): Promise<void> {
  expired.value = false
  await run(async () => {
    try {
      await fn()
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 401) throw e
      choice.value = null
      expired.value = true
    }
  })
}

async function enter(orgId: string): Promise<void> {
  const current = choice.value
  if (!current) return
  await inChoice(async () => finish((await api.enterOrg(current.secret, orgId, 'dashboard')).secret))
}

const newOrg = ref('')

async function createOrg(): Promise<void> {
  const current = choice.value
  if (!current) return
  await inChoice(async () => {
    const created = await api.createOrg(current.secret, newOrg.value.trim())
    await finish((await api.enterOrg(current.secret, created.id, 'dashboard')).secret)
  })
}

const orgState = (o: SignInOrg): string => (o.status === 'invited' ? `invited as ${o.role}` : o.role)

function hasNextQuery(): boolean {
  return typeof route.query.next === 'string'
}

async function linkGitHub(): Promise<void> {
  await run(() => startGitHubLink(hasNextQuery() ? next.value : undefined))
}

function signOutFirst(): void {
  clearSession()
  signedInAs.value = null
}

async function loadSession(): Promise<void> {
  if (!auth.token) return
  try {
    const me = auth.me ?? (await api.me())
    signedInAs.value = { name: me.user?.name || me.user?.login || me.agent?.name || me.actor.id, canLink: !!me.user }
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) clearSession()
  }
}

// Read once and cleared; watched, because deleting from the choice screen stays on this page.
watch(
  () => route.query.notice,
  (v) => {
    if (v !== 'deleted') return
    notice.value = 'Your account was deleted.'
    const { notice: _read, ...rest } = route.query
    void router.replace({ query: rest })
  },
  { immediate: true },
)

onMounted(async () => {
  const handoff = takeHandoff()
  if (handoff && !choose.value) {
    await run(() => finish(handoff))
    return
  }
  // The sign-in forms sit behind the choice: leaving it, or its ten minutes running out, shows them.
  if (handoff) await inChoice(() => openChoice(handoff, true))
  try {
    githubEnabled.value = (await api.githubAuth()).enabled
  } catch {
    githubEnabled.value = false
  }
  if (githubEnabled.value) await loadSession()
})

async function submitToken(): Promise<void> {
  await run(() => finish(token.value.trim()))
}

async function submitPassword(): Promise<void> {
  await run(
    async () => {
      const created = await api.login({
        login: login.value.trim(),
        password: password.value,
        choose: choose.value || undefined,
        token_name: 'dashboard',
      })
      password.value = ''
      if (created.token.scope === 'signin') await openChoice(created.secret, false)
      else await finish(created.secret)
    },
    {
      onError: (e) => {
        if (e instanceof ApiError && e.status === 404) {
          // Password login is disabled here: fall back to the token form.
          passwordOff.value = true
          mode.value = 'token'
        }
      },
    },
  )
}
</script>

<template>
  <div class="mx-auto mt-16 max-w-sm space-y-4">
    <div class="space-y-1">
      <h1 class="text-2xl font-bold">Lorvik</h1>
      <p v-if="!choice" class="meta">Sign in to continue.</p>
    </div>
    <p v-if="callbackError" class="text-muted" role="alert">{{ callbackError }}</p>
    <p v-if="notice" class="text-muted" role="status">{{ notice }}</p>
    <p v-if="expired" class="text-muted" role="alert">That sign-in expired; sign in again.</p>
    <template v-if="choice">
      <p>You are signed in. Pick where to go.</p>
      <ul v-if="choice.orgs.length" class="space-y-2">
        <li v-for="o in choice.orgs" :key="o.id">
          <p v-if="o.status === 'requested'" class="meta">{{ o.name }}: waiting for an admin</p>
          <button v-else class="btn w-full justify-between" type="button" :disabled="busy" @click="enter(o.id)">
            <span class="truncate">{{ o.name }}</span>
            <span class="meta">{{ orgState(o) }}</span>
          </button>
        </li>
      </ul>
      <template v-else>
        <p class="meta">
          You belong to no org on this server yet. Create one, or ask an admin of an existing org to
          {{ choice.viaGitHub ? 'invite your GitHub username' : 'add you' }}.
        </p>
        <p v-if="choice.viaGitHub" class="meta">This GitHub account is not linked to any account on this server. If you already have one, sign in with it and link GitHub on the Access page instead.</p>
      </template>
      <ErrorBanner :error="error" />
      <button v-if="retryable" class="btn w-full justify-center" type="button" :disabled="busy" @click="retry">Retry</button>
      <form class="space-y-3" @submit.prevent="createOrg">
        <div>
          <label class="label" for="neworg">Create an org</label>
          <input id="neworg" v-model="newOrg" class="field" maxlength="200" autocomplete="off" required />
        </div>
        <button class="btn btn-primary w-full justify-center" type="submit" :disabled="busy || !newOrg.trim()">Create org</button>
      </form>
      <button class="btn btn-quiet w-full justify-center" type="button" :disabled="busy" @click="leaveChoice">Sign in as someone else</button>
      <button v-if="!choice.orgs.length" class="btn btn-danger w-full justify-center" type="button" :disabled="busy" @click="deleting = true">Delete this account</button>
      <DeleteAccountDialog v-if="deleting" :login="choice.login" :secret="choice.secret" @close="deleting = false" @deleted="leaveChoice" />
    </template>
    <template v-else>
      <template v-if="githubEnabled && signedInAs">
        <p>You are signed in as {{ signedInAs.name }}.</p>
        <ErrorBanner :error="error" />
        <button v-if="signedInAs.canLink" class="btn btn-primary w-full justify-center" type="button" :disabled="busy" @click="linkGitHub">
          Link GitHub to this account
        </button>
        <button class="btn w-full justify-center" type="button" :disabled="busy" @click="signOutFirst">Sign out first</button>
        <p class="meta">or replace the session with a credential</p>
      </template>
      <template v-else-if="githubEnabled">
        <button class="btn btn-primary w-full justify-center" type="button" @click="signInWithGitHub">Sign in with GitHub</button>
        <p class="meta">or with a credential</p>
      </template>
      <div v-if="!passwordOff" class="cells" role="group" aria-label="Sign-in method">
        <button class="cell" :aria-pressed="mode === 'token'" type="button" @click="mode = 'token'">API token</button>
        <button class="cell" :aria-pressed="mode === 'password'" type="button" @click="mode = 'password'">Password</button>
      </div>

      <form v-if="mode === 'token'" class="space-y-3" @submit.prevent="submitToken">
        <p class="meta">
          Paste an API token, from <code class="font-mono">lorvikd admin bootstrap</code> or
          <code class="font-mono">lorvik tokens create</code>.
        </p>
        <p v-if="passwordOff" class="meta">Password sign-in is not enabled on this server.</p>
        <div>
          <label class="label" for="tok">API token</label>
          <input id="tok" v-model="token" class="field font-mono" type="password" autocomplete="off" required autofocus />
        </div>
        <ErrorBanner :error="error" />
        <button v-if="retryable" class="btn w-full justify-center" type="button" :disabled="busy" @click="retry">Retry</button>
        <button class="btn w-full justify-center" :class="{ 'btn-primary': !githubEnabled }" type="submit" :disabled="busy || !token.trim()">Sign in</button>
      </form>

      <form v-else class="space-y-3" @submit.prevent="submitPassword">
        <div>
          <label class="label" for="lg">Login</label>
          <input id="lg" v-model="login" class="field" autocomplete="username" required autofocus />
        </div>
        <div>
          <label class="label" for="pw">Password</label>
          <input id="pw" v-model="password" class="field" type="password" autocomplete="current-password" required />
        </div>
        <ErrorBanner :error="error" />
        <button v-if="retryable" class="btn w-full justify-center" type="button" :disabled="busy" @click="retry">Retry</button>
        <button class="btn w-full justify-center" :class="{ 'btn-primary': !githubEnabled }" type="submit" :disabled="busy || !login.trim() || !password">Sign in</button>
      </form>
    </template>
  </div>
</template>
