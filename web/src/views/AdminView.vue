<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAction } from '../composables/useAction'
import ActorName from '../components/ActorName.vue'
import LoadState from '../components/LoadState.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import DeleteAccountDialog from '../components/DeleteAccountDialog.vue'
import StatusBadge from '../components/StatusBadge.vue'
import DutyForm from '../components/DutyForm.vue'
import TimeAgo from '../components/TimeAgo.vue'
import RightsChecks from '../components/RightsChecks.vue'
import RightsGrid from '../components/RightsGrid.vue'
import { actorName, chatLabel } from '../lib/actors'
import { allPages, api, startGitHubLink, type ProblemText } from '../lib/api'
import { auth } from '../lib/auth'
import { globalProjectSlugs, projects } from '../lib/projects'
import { rightsInWords } from '../lib/rights'
import { ROLES, atLeast, canOrg, isUser } from '../lib/roles'
import { switchOrg } from '../router'
import type { Agent, AgentJoin, AgentSession, AgentRequest, AgentRight, Membership, Role, SessionGrant, Token } from '../lib/types'

const members = ref<Membership[]>([])
const agents = ref<Agent[]>([])
// The caller's own agents' requests to join projects; only the owner decides them.
const requests = ref<AgentRequest[]>([])
const tokens = ref<Token[]>([])
const { busy, error, run } = useAction()
const loading = ref(true)
const secret = ref('')
const tokenName = ref('')
const route = useRoute()
const router = useRouter()
const githubOn = ref(false)
const deleting = ref(false)
const linkNotice = ref('')
// A link that failed shows as a warning with what to do next, not as a quiet status line.
const linkProblem = ref<ProblemText | null>(null)
const LINK_PROBLEMS: Record<string, ProblemText> = {
  linked_elsewhere: {
    detail: 'That GitHub account is already linked to another account on this server, so it cannot be linked here.',
    hint: 'Signing in with GitHub opens that other account. If you no longer need it, sign in with GitHub, delete it with "Delete my account" on its Access page, then sign in here again and press Link GitHub.',
  },
  already_linked: { detail: 'A GitHub account is already linked to this account.', hint: 'Unlink it before linking another.' },
  github: { detail: 'GitHub did not answer.', hint: 'Press Link GitHub to try again.' },
  denied: { detail: 'GitHub did not authorise the link.', hint: 'Press Link GitHub and approve the request on GitHub.' },
  expired: { detail: 'That link request is no longer valid.', hint: 'Press Link GitHub to start it again.' },
}
const TIPS = {
  user: 'A person who signs in to the dashboard or the CLI.',
  agent: 'An AI agent. It acts through its own token, limited to the projects it has access to.',
  inactive: 'This membership is not active, so it grants no access.',
  harness: 'The tool or client the agent runs in.',
  active: 'Active: the agent can use its token.',
  pending: 'Pending: waiting for an admin to approve it before it can act.',
  revoked: 'Revoked: the agent and its tokens no longer work, and its chats cannot register it again. Restore brings it back.',
  revoke:
    'Stop this agent now. Its tokens stop working, its cards are unassigned and its chats lose their extra rights. Its chats cannot bring it back by themselves; you can restore it later.',
  restore:
    "Bring this agent back with the access it had. A chat of a harness lorvik recognises gets a new token on its next command; other agents need a new token from you. Card assignments and chats' extra rights do not come back. If your org approves agents, it waits for an admin.",
  remove:
    'Stop this agent and delete it for good. It loses all its access and cannot be restored; its past work stays on the board. The next chat of its harness on that machine registers a new agent with no access.',
  scope: 'What the token may do. Write allows changes; otherwise it is read only.',
} as const

function statusTip(status: string): string {
  return (TIPS as Record<string, string>)[status] ?? `Status: ${status}`
}

function bindingTip(slug: string): string {
  return `Rights in ${slug}. Other private projects need their own access.`
}

const JOIN_OPTIONS: { value: AgentJoin; label: string }[] = [
  { value: 'follow', label: 'Follow my settings' },
  { value: 'allow', label: 'Allow in all my projects' },
  { value: 'ask', label: 'Always ask' },
]

// A binding to the global project changes nothing: the shared-project role, or viewer, already applies there.
const grantProjects = computed(() => projects.list.filter((p) => p.scope !== 'global'))
const globalSlugs = computed(() => new Set(globalProjectSlugs()))

function projectAccess(a: Agent): { project: string; rights: AgentRight[] }[] {
  return (a.projects ?? [])
    .filter((b) => !globalSlugs.value.has(b.project) && b.project !== 'global')
    .map((b) => ({ project: b.project, rights: b.rights ?? [] }))
}

// What a chat already has in a project from its agent: the project rights plus any org-wide rights.
const agentRightsIn = (a: Agent, slug: string): AgentRight[] => [
  ...(a.rights ?? []),
  ...(projectAccess(a).find((b) => b.project === slug)?.rights ?? []),
]

const chatRights = (a: Agent, label: string, slug: string): AgentRight[] =>
  a.sessions?.find((s) => s.label === label)?.grants?.find((g) => g.project === slug)?.rights ?? []

const chatGrant = (a: Agent, label: string, slug: string): SessionGrant | undefined =>
  a.sessions?.find((s) => s.label === label)?.grants?.find((g) => g.project === slug)

// A grant with an end is a chat on duty.
const dutiesOf = (a: Agent, label: string): SessionGrant[] =>
  (a.sessions?.find((s) => s.label === label)?.grants ?? []).filter((g) => g.ends_at && !globalSlugs.value.has(g.project))

const pending = computed(() => (isAdmin.value ? members.value.filter((m) => m.status === 'pending') : []))
const requested = computed(() => pending.value.filter((m) => !m.granted_by))
const invited = computed(() => pending.value.filter((m) => m.granted_by))
const listed = computed(() => (isAdmin.value ? members.value.filter((m) => m.status === 'active') : members.value))

const grantRole = reactive<Record<string, Role>>({})
const inviteLogin = ref('')
const inviteRole = ref<Role>('viewer')

const bind = reactive<Record<string, { slug: string; rights: AgentRight[] }>>({})

// Member and agent management need admin; admins grant up to reviewer, owners any role.
const activeTokens = computed(() => tokens.value.filter((t) => t.status === 'active'))
const endedTokens = computed(() => tokens.value.filter((t) => t.status !== 'active'))
const isAdmin = computed(() => canOrg('admin'))
const grantable = computed(() => ROLES.filter((r) => atLeast(auth.me?.role, 'owner') || !atLeast(r, 'admin')))

function canManage(a: Agent): boolean {
  return isAdmin.value || a.created_by === auth.me?.actor.id
}

const requestsOf = (a: Agent): AgentRequest[] => requests.value.filter((r) => r.agent.id === a.id)

const REVOKED_NOTE =
  "These agents cannot act, and their chats cannot register them again. Restore brings one back with the access it had. Remove deletes it for good, so its harness's next chat starts a new agent."

// Agents the caller registered come first; an admin sees everyone else's below them. Revoked agents the caller can
// restore or remove come last.
const agentGroups = computed(() => {
  const live = agents.value.filter((a) => a.status !== 'revoked')
  const mine = live.filter((a) => a.created_by === auth.me?.actor.id)
  const others = live.filter((a) => a.created_by !== auth.me?.actor.id)
  const revoked = agents.value.filter((a) => a.status === 'revoked' && canManage(a))
  return [
    { label: 'Your agents', items: mine, note: '' },
    { label: "Other people's agents", items: others, note: '' },
    { label: 'Revoked agents', items: revoked, note: REVOKED_NOTE },
  ].filter((g) => g.items.length)
})

// A harness agent is named after its harness ("claude-code@host"), so the badge would only repeat it.
const namedForHarness = (a: Agent): boolean => a.name === a.harness || a.name.startsWith(`${a.harness}@`)

async function load(): Promise<void> {
  try {
    // Pending, invited and active rows are split on the client, so every page is needed.
    const [m, a, t, r] = await Promise.all([
      allPages((cursor) => api.members(isAdmin.value, cursor)),
      allPages((cursor) => api.agents(true, cursor)),
      allPages((cursor) => api.tokens(cursor)),
      isUser() ? api.agentRequests().then((x) => x.items) : Promise.resolve([]),
    ])
    members.value = m
    requests.value = r
    // The list omits roles; the detail endpoint carries the org role and project bindings. A revoked agent the caller
    // cannot manage is not shown, so it is not fetched.
    const shown = a.filter((x) => x.status !== 'revoked' || canManage(x))
    agents.value = await Promise.all(shown.map((x) => api.agent(x.id).catch(() => x)))
    tokens.value = t
    error.value = null
  } catch (e) {
    error.value = e
  } finally {
    loading.value = false
  }
}

async function act(fn: () => Promise<unknown>): Promise<void> {
  await run(async () => {
    // A failed change, such as a request already decided in another tab, reloads too, so no stale row stays beside the error.
    try {
      await fn()
    } finally {
      await load()
    }
  })
}

function setRole(m: Membership, e: Event): void {
  const role = (e.target as HTMLSelectElement).value
  void act(() => api.putMember(m.actor.kind, m.actor.id, { role }))
}

const setMemberRights = (m: Membership, rights: AgentRight[]) => act(() => api.putMember(m.actor.kind, m.actor.id, { rights }))

function setAccountJoin(e: Event): void {
  const agentJoin = (e.target as HTMLInputElement).checked ? 'allow' : 'follow'
  void act(async () => {
    const user = await api.updateMe({ agent_join: agentJoin })
    if (auth.me) auth.me.user = user
  })
}

function setAgentJoin(a: Agent, e: Event): void {
  const agentJoin = (e.target as HTMLSelectElement).value as AgentJoin
  void act(() => api.updateAgent(a.id, { agent_join: agentJoin }))
}

async function createToken(): Promise<void> {
  await run(async () => {
    const t = await api.createToken({ name: tokenName.value.trim() || undefined })
    secret.value = t.secret
    tokenName.value = ''
    await load()
  })
}

async function invite(): Promise<void> {
  const login = inviteLogin.value.trim().replace(/^@/, '')
  await act(async () => {
    await api.inviteMember({ login, role: inviteRole.value })
    inviteLogin.value = ''
  })
}

const grant = (m: Membership) => act(() => api.putMember(m.actor.kind, m.actor.id, { role: grantRole[m.actor.id] ?? 'viewer' }))
const removeMember = (m: Membership) => act(() => api.removeMember(m.actor.kind, m.actor.id))
const approveAgent = (id: string) => act(() => api.approveAgent(id))
const allowRequest = (r: AgentRequest) => act(() => api.allowAgentRequest(r.agent.id, r.project))
const denyRequest = (r: AgentRequest) => act(() => api.denyAgentRequest(r.agent.id, r.project))
const revokeAgent = (id: string) => act(() => api.revokeAgent(id))
const restoreAgent = (id: string) => act(() => api.restoreAgent(id))

function removeAgent(a: Agent): void {
  if (!window.confirm(`Remove ${a.name} for good? It stops working, loses all its access and cannot be restored. Its past work stays on the board.`)) return
  void act(() => api.removeAgent(a.id))
}
const revokeToken = (id: string) => act(() => api.revokeToken(id))
const bindAgent = (id: string) => act(() => api.bindAgent(id, bindOf(id).slug, bindOf(id).rights))
const setBindingRights = (a: Agent, slug: string, rights: AgentRight[]) => act(() => api.bindAgent(a.id, slug, rights))

// A chat's extra rights; taking back the last one removes the grant. The grant's end and instructions are sent again, since a save replaces them.
const setChatRights = (a: Agent, label: string, slug: string, rights: AgentRight[]) =>
  act(() => {
    const g = chatGrant(a, label, slug)
    return rights.length
      ? api.putAgentSessionGrant(a.id, label, slug, rights, { ends_at: g?.ends_at, instructions: g?.instructions })
      : api.deleteAgentSessionGrant(a.id, label, slug)
  })

// Where a chat can be put on duty: the agent's project access, and with an org membership every shared project.
function dutyProjects(a: Agent): string[] {
  const shared = a.rights?.length ? grantProjects.value.filter((p) => p.visibility === 'org').map((p) => p.slug) : []
  return [...new Set([...projectAccess(a).map((b) => b.project), ...shared])]
}

const dutyOpen = reactive<Record<string, boolean>>({})
const editOpen = reactive<Record<string, boolean>>({})

const CHATS_HELP =
  "A chat's grant also covers its subagents (labels ending -c<n>). lorvik status shows a chat's label."
const OLD_CHAT_MS = 24 * 3600 * 1000

// Chats seen in the last day first, then older ones for a fold; when every chat is old, no fold.
function chatGroups(sessions: AgentSession[]): { old: boolean; items: AgentSession[] }[] {
  const cut = Date.now() - OLD_CHAT_MS
  const isOld = (s: AgentSession): boolean => !s.last_seen_at || Date.parse(s.last_seen_at) < cut
  const recent = sessions.filter((s) => !isOld(s))
  const old = sessions.filter(isOld)
  if (!recent.length) return [{ old: false, items: old }]
  return old.length ? [{ old: false, items: recent }, { old: true, items: old }] : [{ old: false, items: recent }]
}

// "+ answer in lorvik" for each project where the chat holds rights of its own beyond a duty.
function chatExtras(s: AgentSession): string[] {
  return (s.grants ?? [])
    .filter((g) => g.rights?.length && !g.ends_at && !globalSlugs.value.has(g.project) && g.project !== 'global')
    .map((g) => `+ ${rightsInWords(g.rights)} in ${g.project}`)
}
const dutyKey = (a: Agent, label: string): string => `${a.id}/${label}`

async function putOnDuty(a: Agent, label: string, slug: string, rights: AgentRight[], duty: { ends_at: string; instructions?: string }): Promise<void> {
  await act(() => api.putAgentSessionGrant(a.id, label, slug, rights, duty))
  if (!error.value) dutyOpen[dutyKey(a, label)] = false
}

function bindOf(id: string): { slug: string; rights: AgentRight[] } {
  return (bind[id] ??= { slug: grantProjects.value[0]?.slug ?? '', rights: ['work'] })
}

async function linkGitHub(): Promise<void> {
  linkProblem.value = null
  await run(() => startGitHubLink())
}

async function unlinkGitHub(): Promise<void> {
  linkNotice.value = ''
  linkProblem.value = null
  await run(async () => {
    await api.githubUnlink()
    auth.me = await api.me()
    linkNotice.value = 'GitHub unlinked from your account.'
  })
}

const joinLink = computed(() => `${window.location.origin}/auth/github/start?org=${encodeURIComponent(auth.me?.org.id ?? '')}`)
const joinCopied = ref(false)

async function copyJoinLink(): Promise<void> {
  joinCopied.value = false
  try {
    await navigator.clipboard.writeText(joinLink.value)
    joinCopied.value = true
  } catch {
    document.getElementById('join-link')?.focus()
  }
}

function readLinkResult(): void {
  const { linked, error: code, ...rest } = route.query
  if (linked === '1') linkNotice.value = 'GitHub linked to your account.'
  else if (typeof code === 'string' && code in LINK_PROBLEMS) linkProblem.value = LINK_PROBLEMS[code]
  else return
  void router.replace({ query: rest })
}

onMounted(() => {
  readLinkResult()
  void api.githubAuth().then((g) => (githubOn.value = g.enabled), () => undefined)
  void load()
})
</script>

<template>
  <div class="page space-y-6">
    <div class="space-y-1">
      <h1 class="page-title">Access</h1>
      <p class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <span>Org: <span class="font-medium">{{ auth.me?.org.name }}</span></span>
        <button class="btn" type="button" @click="switchOrg()">Switch or create org</button>
      </p>
      <p class="meta">Who can reach this org, and how. Roles: viewer reads, member works cards, reviewer also reviews, admin manages access, owner has full control.</p>
    </div>
    <ErrorBanner :error="error" :retry="load" />
    <LoadState :loading="loading" />

    <section>
      <h2 class="h2">Org members</h2>
      <p class="meta mb-2">People and agents with a shared-project role. An agent with only project access is not listed here; see Agents. Project roles are managed on the <router-link to="/projects" class="lnk">Projects</router-link> page.</p>
      <section v-if="isAdmin && pending.length" class="mb-4">
        <h3 class="h2">Waiting for access</h3>
        <ul class="list">
          <li v-for="m in requested" :key="m.actor.id" class="list-row flex-wrap items-center gap-y-1.5">
            <span class="min-w-0 flex-1 basis-40 truncate font-medium"><ActorName :who="m.actor" /></span>
            <span class="meta">asked to join</span>
            <div class="ml-auto flex items-center gap-x-2">
              <select
                class="field field-auto"
                :value="grantRole[m.actor.id] ?? 'viewer'"
                aria-label="Role"
                @change="grantRole[m.actor.id] = ($event.target as HTMLSelectElement).value as Role"
              >
                <option v-for="r in ROLES" :key="r" :value="r" :disabled="!grantable.includes(r)">{{ r }}</option>
              </select>
              <button class="btn" type="button" :disabled="busy" @click="grant(m)">Grant</button>
              <button class="btn btn-danger" type="button" :disabled="busy" @click="removeMember(m)">Decline</button>
            </div>
          </li>
          <li v-for="m in invited" :key="m.actor.id" class="list-row flex-wrap items-center gap-y-1.5">
            <span class="min-w-0 flex-1 basis-40 truncate font-medium"><ActorName :who="m.actor" /></span>
            <span class="meta">Invited as {{ m.role }}, has not signed in yet</span>
            <button class="btn btn-danger ml-auto" type="button" :disabled="busy" @click="removeMember(m)">Revoke invite</button>
          </li>
        </ul>
      </section>
      <ul class="list">
        <li v-for="m in listed" :key="m.actor.id" class="list-row flex-wrap items-center gap-y-1.5">
          <span class="min-w-0 flex-1 basis-40 truncate font-medium"><ActorName :who="m.actor" /></span>
          <span class="badge" :title="m.actor.kind === 'agent' ? TIPS.agent : TIPS.user">{{ m.actor.kind }}</span>
          <StatusBadge v-if="m.status !== 'active'" :status="m.status" :title="TIPS.inactive" />
          <div class="ml-auto flex items-center gap-x-2">
            <RightsChecks
              v-if="m.actor.kind === 'agent' && isAdmin && m.status === 'active'"
              :model-value="m.rights ?? []"
              :slug="undefined"
              :label="`${actorName(m.actor)} in the org`"
              :disabled="busy"
              required
              @update:model-value="setMemberRights(m, $event)"
            />
            <span v-else-if="m.actor.kind === 'agent'" class="meta" title="Org-wide rights">{{ m.rights?.length ? rightsInWords(m.rights) : 'read only' }}</span>
            <select
              v-else-if="isAdmin && m.status === 'active'"
              class="field field-auto"
              :value="m.role"
              :disabled="busy"
              aria-label="Role"
              @change="setRole(m, $event)"
            >
              <option v-for="r in ROLES" :key="r" :value="r" :disabled="!grantable.includes(r)">{{ r }}</option>
            </select>
            <span v-else class="meta" title="Shared-project role">{{ m.role }}</span>
            <button
              v-if="isAdmin && m.status === 'active'"
              class="btn btn-danger"
              type="button"
              :disabled="busy"
              @click="removeMember(m)"
            >
              Remove
            </button>
          </div>
        </li>
      </ul>
      <form v-if="isAdmin" class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1.5" @submit.prevent="invite">
        <span class="label mb-0">Invite by GitHub username</span>
        <input v-model="inviteLogin" class="field field-auto w-48" aria-label="GitHub username" autocomplete="off" required />
        <select v-model="inviteRole" class="field field-auto" aria-label="Role">
          <option v-for="r in ROLES" :key="r" :value="r" :disabled="!grantable.includes(r)">{{ r }}</option>
        </select>
        <button class="btn btn-primary" type="submit" :disabled="busy || !inviteLogin.trim()">Invite</button>
      </form>
      <div v-if="isAdmin && githubOn" class="mt-3 space-y-1.5">
        <label class="label" for="join-link">People who sign in through this link ask to join the org; you grant their role under Waiting for access.</label>
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1.5">
          <input id="join-link" class="field field-auto w-96 max-w-full font-mono" :value="joinLink" readonly />
          <button class="btn" type="button" @click="copyJoinLink">Copy</button>
          <span v-if="joinCopied" class="meta" role="status">Copied</span>
        </div>
      </div>
    </section>

    <section>
      <h2 class="h2">Agents</h2>
      <p class="meta mb-2">AI agents registered for this org. Each row says what the agent may do and where it asks to join. Agents hold rights per project and per chat, and accept or reject decisions only for their owner, with the decide right.</p>
      <label v-if="isUser()" class="mb-2 flex items-center gap-x-2">
        <input
          type="checkbox"
          :checked="auth.me?.user?.agent_join === 'allow'"
          :disabled="busy"
          @change="setAccountJoin"
        />
        <span>Let my agents join my projects without asking</span>
      </label>
      <p class="meta mb-2">Revoke stops an agent and keeps it, so it can be restored. Remove deletes it for good; its harness's next chat then registers a new agent with no access.</p>
      <LoadState :loading="false" :empty="!agentGroups.length && !loading" empty-text="No agents." />
      <template v-for="g in agentGroups" :key="g.label">
        <h3 v-if="agentGroups.length > 1 || g.note" class="h2 mt-4">{{ g.label }}</h3>
        <p v-if="g.note" class="meta mb-2">{{ g.note }}</p>
        <ul class="list">
          <li v-for="a in g.items" :key="a.id" class="space-y-1.5 px-1 py-2">
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
              <span class="min-w-0 flex-1 basis-40 truncate font-medium">{{ a.name }}</span>
              <span v-if="!namedForHarness(a)" class="badge" :title="TIPS.harness">{{ a.harness }}</span>
              <StatusBadge :status="a.status" :title="statusTip(a.status)" />
              <span class="meta" title="The person who registered this agent and can manage it.">by <ActorName :who="a.created_by" /></span>
              <div class="ml-auto flex items-center gap-x-2">
                <button v-if="isAdmin && a.status === 'pending'" class="btn" type="button" :disabled="busy" @click="approveAgent(a.id)">
                  Approve
                </button>
                <button v-if="canManage(a) && a.status === 'revoked'" type="button" class="btn" :title="TIPS.restore" :disabled="busy" @click="restoreAgent(a.id)">
                  Restore
                </button>
                <button
                  v-if="canManage(a) && a.status !== 'revoked'"
                  type="button"
                  class="btn btn-danger"
                  :title="TIPS.revoke"
                  :disabled="busy"
                  @click="revokeAgent(a.id)"
                >
                  Revoke
                </button>
                <button v-if="canManage(a)" type="button" class="btn btn-danger" :title="TIPS.remove" :disabled="busy" @click="removeAgent(a)">
                  Remove
                </button>
              </div>
            </div>
            <div class="space-y-0.5 text-xs">
              <template v-if="a.rights?.length">
                <p title="The agent has these rights in every org-visible project and in Global.">
                  <span class="text-muted">Shared projects:</span> {{ rightsInWords(a.rights) }}
                </p>
                <p class="text-muted">Includes Global. Private projects need their own access.</p>
              </template>
              <RightsGrid
                v-if="projectAccess(a).length"
                :rows="projectAccess(a).map((b) => ({
                  key: b.project,
                  label: b.project,
                  slug: b.project,
                  modelValue: b.rights,
                  required: true,
                  title: bindingTip(b.project),
                  groupLabel: `${a.name} in ${b.project}`,
                  disabled: busy || !canManage(a) || a.status !== 'active',
                }))"
                @change="(project: string, rights: AgentRight[]) => setBindingRights(a, project, rights)"
              />
              <p v-if="!a.rights?.length && a.projects?.length" class="text-muted">Global: read only, included automatically.</p>
              <p v-if="!a.rights?.length && !a.projects?.length" class="text-muted">none yet, it cannot act anywhere</p>
            </div>
            <div v-if="a.sessions?.length" class="space-y-1.5 text-xs">
              <h4 class="text-muted">
                Chats ({{ a.sessions.length }})
                <span class="cursor-help" :title="CHATS_HELP" aria-hidden="true">?</span>
                <span class="sr-only">{{ CHATS_HELP }}</span>
              </h4>
              <template v-for="grp in chatGroups(a.sessions)" :key="String(grp.old)">
                <component :is="grp.old ? 'details' : 'div'">
                  <summary v-if="grp.old" class="meta cursor-pointer">{{ grp.items.length }} older chats</summary>
                  <ul class="divide-y divide-line" :class="{ 'mt-1.5': grp.old }">
                    <li v-for="s in grp.items" :key="s.label" class="space-y-1.5 py-1.5">
                      <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                        <span class="min-w-0 flex-1 basis-48 truncate font-mono" :title="chatLabel(s.label, s.name)">{{ chatLabel(s.label, s.name) }}</span>
                        <TimeAgo v-if="s.last_seen_at" class="meta" :at="s.last_seen_at" />
                        <span v-else class="meta">never seen</span>
                        <span v-if="!chatExtras(s).length && !dutiesOf(a, s.label).length" class="meta">Same as the agent</span>
                        <span v-else-if="chatExtras(s).length">{{ chatExtras(s).join(', ') }}</span>
                        <div class="ml-auto flex items-center gap-x-1">
                          <button
                            v-if="canManage(a) && a.status === 'active' && projectAccess(a).length"
                            type="button"
                            class="btn btn-quiet h-6 px-2 text-xs"
                            :aria-label="`Change the rights of chat ${s.label} of ${a.name}`"
                            :aria-expanded="!!editOpen[dutyKey(a, s.label)]"
                            @click="editOpen[dutyKey(a, s.label)] = !editOpen[dutyKey(a, s.label)]"
                          >
                            Change
                          </button>
                          <button
                            v-if="canManage(a) && a.status === 'active' && dutyProjects(a).length && !dutyOpen[dutyKey(a, s.label)]"
                            type="button"
                            class="btn btn-quiet h-6 px-2 text-xs"
                            :disabled="busy"
                            :aria-label="`Put chat ${s.label} of ${a.name} on duty`"
                            title="Lets this chat answer questions for you, until the end time you set"
                            @click="dutyOpen[dutyKey(a, s.label)] = true"
                          >
                            Put on duty
                          </button>
                        </div>
                      </div>
                      <div v-for="g in dutiesOf(a, s.label)" :key="g.project" class="min-w-0 space-y-1">
                        <p class="flex flex-wrap items-center gap-x-2 gap-y-1">
                          <span class="font-medium">On duty in <span class="font-mono">{{ g.project }}</span>, ends <TimeAgo :at="g.ends_at!" /></span>
                          <button
                            v-if="canManage(a)"
                            type="button"
                            class="btn btn-danger h-6 px-2 text-xs"
                            :disabled="busy"
                            :aria-label="`End duty of chat ${s.label} of ${a.name} in ${g.project}`"
                            title="Takes back this chat's rights in the project"
                            @click="setChatRights(a, s.label, g.project, [])"
                          >
                            End duty
                          </button>
                        </p>
                        <p v-if="g.instructions" class="break-words whitespace-pre-line text-muted">{{ g.instructions }}</p>
                      </div>
                      <RightsGrid
                        v-if="editOpen[dutyKey(a, s.label)] && projectAccess(a).length"
                        :rows="projectAccess(a).map((b) => ({
                          key: b.project,
                          label: b.project,
                          slug: b.project,
                          modelValue: chatRights(a, s.label, b.project),
                          inherited: agentRightsIn(a, b.project),
                          groupLabel: `chat ${s.label} of ${a.name} in ${b.project}`,
                          disabled: busy || !canManage(a) || a.status !== 'active',
                        }))"
                        @change="(project: string, rights: AgentRight[]) => setChatRights(a, s.label, project, rights)"
                      />
                      <DutyForm
                        v-if="dutyOpen[dutyKey(a, s.label)]"
                        :projects="dutyProjects(a)"
                        :grants="s.grants ?? []"
                        :inherited="(slug) => agentRightsIn(a, slug)"
                        :busy="busy"
                        @submit="(slug, rights, duty) => putOnDuty(a, s.label, slug, rights, duty)"
                        @cancel="dutyOpen[dutyKey(a, s.label)] = false"
                      />
                    </li>
                  </ul>
                </component>
              </template>
            </div>
            <div v-for="r in requestsOf(a)" :key="r.project" class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
              <span class="font-medium">Asks to join {{ r.project_name }} to {{ rightsInWords(r.rights) }}</span>
              <TimeAgo class="meta" :at="r.requested_at" />
              <button type="button" class="btn" :disabled="busy" :aria-label="`Allow ${a.name} in ${r.project_name}`" @click="allowRequest(r)">Allow</button>
              <button type="button" class="btn btn-quiet" :disabled="busy" :aria-label="`Deny ${a.name} in ${r.project_name}`" @click="denyRequest(r)">Deny</button>
            </div>
            <div class="flex flex-wrap items-start gap-x-6 gap-y-1.5">
              <div v-if="isUser() && canManage(a) && a.status === 'active'" class="flex flex-wrap items-center gap-x-3 gap-y-1.5">
                <label class="label mb-0" :for="`join-${a.id}`">Joining projects</label>
                <select
                  :id="`join-${a.id}`"
                  class="field field-auto"
                  :value="a.agent_join"
                  :disabled="busy"
                  @change="setAgentJoin(a, $event)"
                >
                  <option v-for="o in JOIN_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
                </select>
              </div>
              <details v-if="canManage(a) && a.status === 'active'">
                <summary class="meta cursor-pointer py-1">Give access by hand</summary>
                <form class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1.5" @submit.prevent="bindAgent(a.id)">
                  <span class="label mb-0">Add project access</span>
                  <select v-model="bindOf(a.id).slug" class="field field-auto" aria-label="Project">
                    <option v-for="p in grantProjects" :key="p.slug" :value="p.slug">{{ p.slug }}</option>
                  </select>
                  <RightsChecks v-model="bindOf(a.id).rights" :slug="bindOf(a.id).slug" :label="`new access for ${a.name} in ${bindOf(a.id).slug}`" required />
                  <button class="btn" type="submit" :disabled="busy || !bindOf(a.id).slug || !bindOf(a.id).rights.length">Add access</button>
                </form>
              </details>
            </div>
          </li>
        </ul>
      </template>
    </section>

    <section v-if="githubOn || linkNotice || linkProblem || auth.me?.user">
      <h2 class="h2">Your sign-in</h2>
      <p v-if="auth.me?.user?.github_login" class="mb-2">Linked to GitHub as <span class="font-medium">{{ auth.me.user.github_login }}</span>.</p>
      <p v-else class="meta mb-2">
        Link the GitHub account you sign in with to this user, so signing in with GitHub opens this account instead of
        creating a second one.
      </p>
      <p v-if="linkNotice" class="mb-2" role="status">{{ linkNotice }}</p>
      <ErrorBanner :error="linkProblem" class="mb-2" />
      <div class="flex flex-wrap gap-2">
        <button v-if="githubOn && !auth.me?.user?.github_login" type="button" class="btn" :disabled="busy" @click="linkGitHub">Link GitHub</button>
        <button v-if="auth.me?.user?.github_login" type="button" class="btn btn-danger" :disabled="busy" @click="unlinkGitHub">Unlink GitHub</button>
        <button v-if="auth.me?.user" type="button" class="btn btn-danger" :disabled="busy" @click="deleting = true">Delete my account</button>
      </div>
      <DeleteAccountDialog v-if="deleting && auth.me?.user" :login="auth.me.user.login" @close="deleting = false" />
    </section>

    <section>
      <h2 class="h2">Your tokens</h2>
      <p class="meta mb-2">
        Each row is one credential: a browser sign-in, a <code class="font-mono">lorvik login</code>, or the bootstrap command. The
        prefix identifies it and is not the secret. Revoking one signs that client out.
      </p>
      <form class="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1.5" @submit.prevent="createToken">
        <input v-model="tokenName" class="field field-auto w-56" placeholder="Name (optional)" aria-label="Token name" maxlength="64" />
        <button class="btn btn-primary" type="submit" :disabled="busy">Create token</button>
      </form>
      <div v-if="secret" class="panel mb-3 space-y-1.5 p-3">
        <p class="meta">Shown once. Copy it now.</p>
        <code class="block font-mono break-all">{{ secret }}</code>
        <button type="button" class="btn" @click="secret = ''">Done</button>
      </div>
      <LoadState :loading="false" :empty="!tokens.length && !loading" empty-text="No tokens." />
      <!-- One grid holds both lists; lists and rows are subgrids, so every column lines up across them. -->
      <div class="sm:grid sm:grid-cols-[auto_minmax(0,1fr)_repeat(6,auto)] sm:gap-x-3">
        <ul v-if="activeTokens.length" class="list sm:col-span-full sm:grid sm:grid-cols-subgrid">
          <li v-for="t in activeTokens" :key="t.id" class="list-row flex-wrap items-center gap-y-1.5 sm:col-span-full sm:grid sm:grid-cols-subgrid">
            <span class="font-mono text-xs" title="Identifies this token. It is not the secret.">{{ t.prefix }}</span>
            <span class="min-w-0 flex-1 basis-32 truncate">{{ t.name }}</span>
            <span class="badge justify-self-start" :title="TIPS.scope">{{ t.scope }}</span>
            <StatusBadge :status="t.status" title="Active: this token works." />
            <span class="meta">created <TimeAgo :at="t.created_at" /></span>
            <span class="meta">{{ t.last_used_at ? 'used' : 'never used' }} <TimeAgo v-if="t.last_used_at" :at="t.last_used_at" /></span>
            <span class="meta" title="After this time the token stops working and the client must sign in again.">expires <TimeAgo :at="t.expires_at" /></span>
            <button type="button" class="btn btn-danger justify-self-end" :disabled="busy" @click="revokeToken(t.id)">Revoke</button>
          </li>
        </ul>
        <template v-if="endedTokens.length">
          <h3 class="h2 mt-4 sm:col-span-full">Revoked or replaced</h3>
          <ul class="list sm:col-span-full sm:grid sm:grid-cols-subgrid">
            <li v-for="t in endedTokens" :key="t.id" class="list-row flex-wrap items-center gap-y-1.5 sm:col-span-full sm:grid sm:grid-cols-subgrid">
              <span class="font-mono text-xs" title="Identifies this token. It is not the secret.">{{ t.prefix }}</span>
              <span class="min-w-0 flex-1 basis-32 truncate">{{ t.name }}</span>
              <span class="badge justify-self-start" :title="TIPS.scope">{{ t.scope }}</span>
              <StatusBadge :status="t.status" :title="`Status: ${t.status}. This token no longer works.`" />
              <span class="meta">created <TimeAgo :at="t.created_at" /></span>
              <span class="meta">{{ t.last_used_at ? 'used' : 'never used' }} <TimeAgo v-if="t.last_used_at" :at="t.last_used_at" /></span>
            </li>
          </ul>
        </template>
      </div>
    </section>
  </div>
</template>
