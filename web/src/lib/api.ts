import { resolveActors, setActorLoader } from './actors'
import { auth, clearSession, replaceToken } from './auth'
import { noteUnread, unread } from './inbox'
import type {
  Actors,
  Agent,
  AgentJoin,
  AgentRight,
  AgentRequestList,
  AnswerCard,
  Card,
  CardBrief,
  ClaimCard,
  CreateCard,
  CreateProject,
  FoldCard,
  CreateToken,
  Comment,
  GitHubAuth,
  GitHubLink,
  Inbox,
  Invite,
  KeepCard,
  LiveClaim,
  Lock,
  Login,
  MapGroup,
  MapGroupLink,
  MarkForPerson,
  LorvikEvent,
  Me,
  Membership,
  ProjectMember,
  HTMLPage,
  Note,
  NoteBrief,
  CardRevision,
  NoteRevision,
  Page,
  Problem,
  Project,
  ProjectLink,
  ProjectMention,
  ReplyList,
  SearchResult,
  SignInOrg,
  SignInOrgs,
  DeletionPreview,
  Token,
  TokenCreated,
  TransitionCard,
  UpdateAgent,
  UpdateCard,
  UpdateNote,
  UpdateProject,
  UpdateUser,
  User,
  HistoryQuery,
} from './types'

/** before= with this value asks GET /events for the newest page. */
export const NEWEST_EVENT = '9223372036854775807'

const base: string = import.meta.env.VITE_API_BASE ?? ''

const HEADER_CURSOR = 'X-Lorvik-Cursor'
const HEADER_UNREAD = 'X-Lorvik-Unread'
const HEADER_TOKEN = 'X-Lorvik-Token'

export function apiUrl(path: string): string {
  return `${base}${path}`
}

/** A non-2xx API response, carrying the RFC 9457 problem body when there was one. */
export class ApiError extends Error {
  readonly status: number
  readonly problem: Problem

  constructor(status: number, problem: Problem) {
    super(problem.detail ?? problem.title)
    this.status = status
    this.problem = problem
  }

  get hint(): string {
    return this.problem.hint ?? ''
  }

  get isConflict(): boolean {
    return this.status === 409
  }

  get isNotFound(): boolean {
    return this.status === 404
  }
}

export interface ProblemText {
  detail: string
  hint: string
}

/** The text an error banner shows: an API problem's detail and hint, a ProblemText as it is, or any other error's message. */
export function errorText(e: unknown): ProblemText {
  if (isProblemText(e)) return e
  if (e instanceof ApiError) {
    const fields = (e.problem.errors ?? []).map((f) => `${f.field}: ${f.message}`).join('; ')
    return { detail: fields ? `${e.message} (${fields})` : e.message, hint: e.hint }
  }
  return { detail: e instanceof Error ? e.message : String(e), hint: '' }
}

function isProblemText(e: unknown): e is ProblemText {
  return typeof e === 'object' && e !== null && !(e instanceof Error) && typeof (e as ProblemText).detail === 'string' && typeof (e as ProblemText).hint === 'string'
}

type Query = Record<string, string | number | boolean | undefined>

interface Options {
  body?: unknown
  query?: Query
  token?: string
  /** Told the replacement when the server rotated an explicit token on this call. */
  rotated?: (token: string) => void
}

function qs(query?: Query): string {
  if (!query) return ''
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== '') p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

async function toProblem(res: Response): Promise<Problem> {
  try {
    const p = (await res.json()) as Problem
    if (p && typeof p.title === 'string') return p
  } catch {
    // not JSON
  }
  return { title: res.statusText || 'Request failed', status: res.status }
}

/** Headers every authenticated request carries. */
export function authHeaders(token: string): Record<string, string> {
  const h: Record<string, string> = {}
  if (token) h.Authorization = `Bearer ${token}`
  if (token && unread.orgId) h[HEADER_CURSOR] = unread.cursor
  return h
}

/**
 * Reads the headers every response may carry: a rotated token replaces the
 * stored one at once, and the unread count feeds the header badge.
 */
export function readResponseHeaders(res: Response, explicitToken: boolean): void {
  if (!explicitToken) {
    const next = res.headers.get(HEADER_TOKEN)
    if (next) replaceToken(next)
  }
  noteUnread(res.headers.get(HEADER_UNREAD))
}

async function request<T>(method: string, path: string, opts: Options = {}): Promise<T> {
  const token = opts.token ?? auth.token
  const headers: Record<string, string> = { Accept: 'application/json', ...authHeaders(token) }
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'

  let res: Response
  try {
    res = await fetch(apiUrl(`/api/v1${path}${qs(opts.query)}`), {
      method,
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    })
  } catch {
    throw new ApiError(0, { title: 'Network error', status: 0, detail: 'Cannot reach the server.' })
  }
  readResponseHeaders(res, opts.token !== undefined)
  const next = res.headers.get(HEADER_TOKEN)
  if (next) opts.rotated?.(next)
  if (res.status === 401 && opts.token === undefined && token !== '' && token === auth.token) clearSession()
  if (!res.ok) throw new ApiError(res.status, await toProblem(res))
  if (res.status === 204) return undefined as T
  const data = (await res.json()) as T
  if (path !== '/actors' && auth.token) resolveActors(data)
  return data
}

const enc = encodeURIComponent
const cardPath = (slug: string, n: number): string => `/projects/${enc(slug)}/cards/${n}`

interface EventsQuery {
  project?: string
  after?: string
  before?: string
  limit?: number
}

// These lists are always read to the end, so each request asks for the most it can.
export const MAX_PAGE = 200

export const api = {
  me: () => request<Me>('GET', '/me'),
  /**
   * Reads the profile behind a secret that is not the session's yet. The server hands a rotated
   * token back once, on the call that rotated it, so the secret to keep may differ from the one sent.
   */
  signIn: async (secret: string): Promise<{ token: string; me: Me }> => {
    let token = secret
    const me = await request<Me>('GET', '/me', { token: secret, rotated: (next) => (token = next) })
    return { token, me }
  },
  /** Password login. Answers 404 with a hint when the server has it disabled. */
  login: (body: Login) => request<TokenCreated>('POST', '/login', { body, token: '' }),
  /** The three calls below take the ten-minute sign-in token a sign-in without one org answers with. */
  signInOrgs: (secret: string) => request<SignInOrgs>('GET', '/login/orgs', { token: secret }),
  createOrg: (secret: string, name: string) => request<SignInOrg>('POST', '/login/orgs', { body: { name }, token: secret }),
  enterOrg: (secret: string, orgId: string, tokenName: string) =>
    request<TokenCreated>('POST', `/login/orgs/${enc(orgId)}/token`, { body: { token_name: tokenName }, token: secret }),
  /** Deletes the caller's account for good; confirm is their login. A sign-in token may be given in place of the session's. */
  deletionPreview: () => request<DeletionPreview>('GET', '/me/deletion'),
  deleteMe: (confirm: string, secret?: string) => request<void>('DELETE', '/me', { body: { confirm }, token: secret }),
  updateMe: (body: UpdateUser) => request<User>('PATCH', '/me', { body }),
  githubAuth: () => request<GitHubAuth>('GET', '/auth/github', { token: '' }),
  /** The GitHub authorize URL for linking an account to the signed-in user; also sets the oauth cookie. */
  githubLink: (next?: string) => request<GitHubLink>('POST', '/me/github-link', { body: next ? { next } : undefined }),
  /** Removes the caller's GitHub sign-in; 409 when it is their only way to sign in. */
  githubUnlink: () => request<void>('DELETE', '/me/github-link'),
  actors: (ids: string[]) => request<Actors>('GET', '/actors', { query: { id: ids.join(',') } }),

  projects: (cursor?: string) => request<Page<Project>>('GET', '/projects', { query: { limit: MAX_PAGE, cursor } }),
  /** One project with its card counts by status. */
  project: (slug: string) => request<Project>('GET', `/projects/${enc(slug)}`),
  createProject: (body: CreateProject) => request<Project>('POST', '/projects', { body }),
  updateProject: (slug: string, body: UpdateProject) => request<Project>('PATCH', `/projects/${enc(slug)}`, { body }),

  /** The caller's own rule for their agents joining this project. */
  putAgentJoin: (slug: string, agentJoin: AgentJoin) =>
    request<Project>('PUT', `/projects/${enc(slug)}/agent-join`, { body: { agent_join: agentJoin } }),

  cards: (slug: string, query: Query) => request<Page<CardBrief>>('GET', `/projects/${enc(slug)}/cards`, { query }),
  links: (slug: string, cursor?: string) => request<Page<ProjectLink>>('GET', `/projects/${enc(slug)}/links`, { query: { limit: MAX_PAGE, cursor } }),
  mentions: (slug: string, cursor?: string) => request<Page<ProjectMention>>('GET', `/projects/${enc(slug)}/mentions`, { query: { limit: MAX_PAGE, cursor } }),
  /** One box per story and per lane, with the status counts of what it holds. */
  mapGroups: (slug: string, cursor?: string) => request<Page<MapGroup>>('GET', `/projects/${enc(slug)}/map/groups`, { query: { limit: MAX_PAGE, cursor } }),
  /** Link counts between the boxes, one row per ordered pair of boxes and link kind. */
  mapGroupLinks: (slug: string, cursor?: string) => request<Page<MapGroupLink>>('GET', `/projects/${enc(slug)}/map/group-links`, { query: { limit: MAX_PAGE, cursor } }),
  /** project is 'all' (default) or a slug; rows carry their project. */
  allCards: (query: Query) => request<Page<CardBrief>>('GET', '/cards', { query }),
  unfolded: (slug: string, limit?: number, cursor?: string) =>
    request<Page<CardBrief>>('GET', `/projects/${enc(slug)}/unfolded`, { query: { limit, cursor } }),
  card: (slug: string, n: number) => request<Card>('GET', cardPath(slug, n)),
  cardPage: (slug: string, n: number) => request<HTMLPage>('GET', `${cardPath(slug, n)}/page`),
  createCard: (slug: string, body: CreateCard) => request<Card>('POST', `/projects/${enc(slug)}/cards`, { body }),
  updateCard: (slug: string, n: number, body: UpdateCard) => request<Card>('PATCH', cardPath(slug, n), { body }),
  transition: (slug: string, n: number, body: TransitionCard) =>
    request<Card>('POST', `${cardPath(slug, n)}/transition`, { body }),
  claimCard: (slug: string, n: number, body: ClaimCard) => request<Card>('POST', `${cardPath(slug, n)}/claim`, { body }),
  releaseCard: (slug: string, n: number, body: ClaimCard) => request<Card>('POST', `${cardPath(slug, n)}/release`, { body }),
  foldCard: (slug: string, n: number, body: FoldCard) => request<Card>('POST', `${cardPath(slug, n)}/fold`, { body }),
  comments: (slug: string, n: number, page?: HistoryQuery) => request<Comment[]>('GET', `${cardPath(slug, n)}/comments`, { query: { ...page } }),
  createComment: (slug: string, n: number, body: string) =>
    request<Comment>('POST', `${cardPath(slug, n)}/comments`, { body: { body } }),
  replies: (slug: string, limit?: number, cursor?: string) =>
    request<ReplyList>('GET', `/projects/${enc(slug)}/replies`, { query: { limit, cursor } }),
  markCardSeen: (slug: string, n: number) => request<void>('POST', `${cardPath(slug, n)}/seen`),
  markRepliesSeen: (slug: string) => request<void>('POST', `/projects/${enc(slug)}/replies/seen`),
  answer: (slug: string, n: number, body: AnswerCard) => request<Card>('POST', `${cardPath(slug, n)}/answer`, { body }),
  /** The person an agent settled the card for says they have looked at it. */
  keepCard: (slug: string, n: number, body: KeepCard) => request<Card>('POST', `${cardPath(slug, n)}/keep`, { body }),
  markForPerson: (slug: string, n: number, body: MarkForPerson) => request<Card>('POST', `${cardPath(slug, n)}/for-person`, { body }),

  notes: (query: Query) => request<Page<NoteBrief>>('GET', '/notes', { query }),
  /** key is a slug (with project, or none for org-level) or an id. */
  note: (key: string, project?: string) => request<Note>('GET', `/notes/${enc(key)}`, { query: { project } }),
  notePage: (key: string, project?: string) => request<HTMLPage>('GET', `/notes/${enc(key)}/page`, { query: { project } }),
  updateNote: (key: string, project: string | undefined, body: UpdateNote) =>
    request<Note>('PATCH', `/notes/${enc(key)}`, { query: { project }, body }),
  cardRevisions: (slug: string, number: number, page?: HistoryQuery) =>
    request<CardRevision[]>('GET', `/projects/${enc(slug)}/cards/${number}/revisions`, { query: { ...page } }),
  cardRevision: (slug: string, number: number, version: number) =>
    request<CardRevision>('GET', `/projects/${enc(slug)}/cards/${number}/revisions/${version}`),
  noteRevisions: (key: string, project?: string, page?: HistoryQuery) =>
    request<NoteRevision[]>('GET', `/notes/${enc(key)}/revisions`, { query: { project, ...page } }),
  noteRevision: (key: string, version: number, project?: string) =>
    request<NoteRevision>('GET', `/notes/${enc(key)}/revisions/${version}`, { query: { project } }),

  /** Cards and notes across the projects the caller can read, or one project. */
  search: (q: string, project?: string, limit?: number, cursor?: string) =>
    request<SearchResult>('GET', '/search', { query: { q, project, limit, cursor } }),

  /** Oldest first; after= pages to newer events, before= to older ones. */
  events: (q: EventsQuery) => request<Page<LorvikEvent>>('GET', '/events', { query: { ...q } }),
  liveClaims: (cursor?: string) => request<Page<LiveClaim>>('GET', '/claims', { query: { limit: MAX_PAGE, cursor } }),
  locks: (cursor?: string) => request<Page<Lock>>('GET', '/locks', { query: { limit: MAX_PAGE, cursor } }),
  releaseLock: (name: string, force: boolean) =>
    request<void>('DELETE', `/locks/${enc(name)}`, { query: { force: force ? true : undefined } }),
  inbox: (cursor: string, limit = 50) => request<Inbox>('GET', '/inbox', { query: { cursor, limit } }),

  /** all=true also lists pending rows (requests and invites), for admins. */
  members: (all = false, cursor?: string) =>
    request<Page<Membership>>('GET', '/members', { query: { all: all || undefined, limit: MAX_PAGE, cursor } }),
  inviteMember: (body: Invite) => request<Membership>('POST', '/members/invite', { body }),
  /** People take a role, agents take rights. */
  putMember: (kind: string, id: string, grant: { role: string } | { rights: AgentRight[] }) =>
    request<Membership>('PUT', `/members/${enc(kind)}/${enc(id)}`, { body: grant }),
  removeMember: (kind: string, id: string) => request<void>('DELETE', `/members/${enc(kind)}/${enc(id)}`),

  projectMembers: (slug: string, cursor?: string) =>
    request<Page<ProjectMember>>('GET', `/projects/${enc(slug)}/members`, { query: { limit: MAX_PAGE, cursor } }),
  /** People take a role, agents take rights. */
  putProjectMember: (slug: string, kind: string, id: string, grant: { role: string } | { rights: AgentRight[] }) =>
    request<ProjectMember>('PUT', `/projects/${enc(slug)}/members/${enc(kind)}/${enc(id)}`, { body: grant }),
  removeProjectMember: (slug: string, kind: string, id: string) =>
    request<void>('DELETE', `/projects/${enc(slug)}/members/${enc(kind)}/${enc(id)}`),

  /** With all, revoked agents are listed too; removed ones never are. */
  agents: (all = false, cursor?: string) => request<Page<Agent>>('GET', '/agents', { query: { all: all || undefined, limit: MAX_PAGE, cursor } }),
  agent: (id: string) => request<Agent>('GET', `/agents/${enc(id)}`),
  approveAgent: (id: string) => request<Agent>('POST', `/agents/${enc(id)}/approve`),
  revokeAgent: (id: string) => request<void>('DELETE', `/agents/${enc(id)}`),
  restoreAgent: (id: string) => request<Agent>('POST', `/agents/${enc(id)}/restore`),
  /** For good: the agent leaves every list and its harness's next chat registers a new one. */
  removeAgent: (id: string) => request<void>('POST', `/agents/${enc(id)}/remove`),
  bindAgent: (id: string, slug: string, rights: AgentRight[]) =>
    request<Agent>('PUT', `/agents/${enc(id)}/projects/${enc(slug)}`, { body: { rights } }),
  /** Gives one chat of an agent these rights in a project, on top of the agent's own. */
  putAgentSessionGrant: (id: string, label: string, slug: string, rights: AgentRight[], duty?: { ends_at?: string; instructions?: string }) =>
    request<Agent>('PUT', `/agents/${enc(id)}/sessions/${enc(label)}/projects/${enc(slug)}`, { body: { rights, ...duty } }),
  deleteAgentSessionGrant: (id: string, label: string, slug: string) =>
    request<void>('DELETE', `/agents/${enc(id)}/sessions/${enc(label)}/projects/${enc(slug)}`),

  updateAgent: (id: string, body: UpdateAgent) => request<Agent>('PATCH', `/agents/${enc(id)}`, { body }),
  /** The caller's agents' pending requests to join projects, oldest first. */
  agentRequests: () => request<AgentRequestList>('GET', '/agent-requests'),
  allowAgentRequest: (id: string, slug: string) => request<Agent>('PUT', `/agent-requests/${enc(id)}/${enc(slug)}`),
  denyAgentRequest: (id: string, slug: string) => request<void>('DELETE', `/agent-requests/${enc(id)}/${enc(slug)}`),

  tokens: (cursor?: string) => request<Page<Token>>('GET', '/tokens', { query: { limit: MAX_PAGE, cursor } }),
  createToken: (body: CreateToken) => request<TokenCreated>('POST', '/tokens', { body }),
  revokeToken: (id: string) => request<void>('DELETE', `/tokens/${enc(id)}`),
}

setActorLoader((ids) => api.actors(ids))

/** Every row of a keyset-paged list, for a screen that is wrong unless it sees all of them. */
export async function allPages<T>(fetchPage: (cursor?: string) => Promise<Page<T>>): Promise<T[]> {
  const items: T[] = []
  let cursor: string | undefined
  for (;;) {
    const page = await fetchPage(cursor)
    items.push(...page.items)
    if (!page.next || page.remaining <= 0) return items
    cursor = page.next
  }
}

export async function startGitHubLink(next?: string): Promise<void> {
  window.location.assign((await api.githubLink(next)).url)
}

const COUNT_CAP = 100

/** "100+" when the server's capped remaining count is reached. */
export function remainingLabel(n: number): string {
  return n >= COUNT_CAP ? `${COUNT_CAP}+` : String(n)
}
