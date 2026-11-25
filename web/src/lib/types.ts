// Mirrors internal/apitypes/types.go. Field names are the wire contract.
// Ids are snowflake decimal strings. Timestamps are RFC 3339 strings.

export type ActorKind = 'user' | 'agent'
export type CardKind = 'task' | 'question' | 'story' | 'decision'
export type CardStatus = 'backlog' | 'ready' | 'working' | 'waiting' | 'done' | 'dropped'
export type DropReason = 'obsolete' | 'duplicate' | 'wontfix' | 'rejected' | 'revoked' | 'superseded'
export type Priority = 'low' | 'normal' | 'high' | 'urgent'
export type LinkKind = 'blocks' | 'relates' | 'duplicates' | 'supersedes' | 'applies'
export type Concurrency = 'parallel' | 'serial'
export type Role = 'viewer' | 'member' | 'reviewer' | 'admin' | 'owner'

export interface ProblemField {
  field: string
  message: string
}

export interface Problem {
  type?: string
  title: string
  status: number
  detail?: string
  instance?: string
  errors?: ProblemField[]
  hint?: string
  current_version?: number
}

export interface Actor {
  kind: ActorKind
  id: string
}

export interface User {
  id: string
  login: string
  name?: string
  email?: string
  avatar_url?: string
  locale?: string
  timezone?: string
  status: 'active' | 'suspended' | 'deleted'
  created_at: string
  /** The account switch; only in /me and PATCH /me. */
  agent_join?: UserAgentJoin
  /** The GitHub account linked to the user; only in /me. */
  github_login?: string
}

/** The owner's account switch: allow lets their agents join their projects without asking. */
export type UserAgentJoin = 'follow' | 'allow'

/** An agent's own join setting, or the owner's rule for one project. */
export type AgentJoin = 'follow' | 'allow' | 'ask'

export interface UpdateUser {
  agent_join?: UserAgentJoin
}

/** What an agent may do in a project; decide lets it accept or reject a proposed decision for its owner. */
export type AgentRight = 'work' | 'ready' | 'triage' | 'answer' | 'decide'

/** The rights a chat was given in one project, on top of its agent's. */
export interface SessionGrant {
  project: string
  rights: AgentRight[]
  /** When the grant ends by itself; absent until taken back. */
  ends_at?: string
  /** The owner's words to the chat. */
  instructions?: string
}

/** One chat seen using an agent's token, by its base label. */
export interface AgentSession {
  label: string
  name?: string
  last_seen_at?: string
  grants?: SessionGrant[]
}

export interface Agent {
  id: string
  name: string
  harness: string
  machine?: string
  /** removed is for good; such an agent is in no list and only GET /agents/{id} still names it. */
  status: 'active' | 'pending' | 'revoked' | 'removed'
  created_by: string
  agent_join: AgentJoin
  created_at: string
  updated_at: string
  role?: string
  /** Org-wide rights. */
  rights?: AgentRight[]
  projects?: ProjectRole[]
  sessions?: AgentSession[]
}

export interface UpdateAgent {
  agent_join?: AgentJoin
}

/** An agent's pending request to join a project, for its owner to allow or deny. */
export interface AgentRequest {
  agent: Agent
  project: string
  project_name: string
  /** The rights allowing gives. */
  rights: AgentRight[]
  requested_at: string
}

export interface AgentRequestList {
  items: AgentRequest[]
}

export interface Actors {
  users: User[]
  agents: Agent[]
}

export interface Org {
  id: string
  name: string
  status: string
  created_at: string
}

export interface ProjectRole {
  project: string
  role: string
  /** Agents only. */
  rights?: AgentRight[]
}

export interface Me {
  actor: Actor
  org: Org
  role?: Role
  projects?: ProjectRole[]
  user?: User
  agent?: Agent
  token_prefix: string
  token_expires_at: string
  session_name?: string
}

export interface Membership {
  actor: Actor
  role: Role
  /** Agents only. */
  rights?: AgentRight[]
  status: 'active' | 'pending' | 'removed'
  granted_by?: string
  created_at: string
  updated_at: string
}

export interface ProjectMember {
  project: string
  actor: Actor
  role: 'viewer' | 'member' | 'reviewer'
  /** Agents only. */
  rights?: AgentRight[]
  status: 'active' | 'removed'
  granted_by?: string
  created_at: string
  updated_at: string
}

export interface StatusCounts {
  backlog: number
  ready: number
  working: number
  waiting: number
  done: number
  dropped: number
  open_questions: number
}

export interface Project {
  id: string
  slug: string
  name: string
  description?: string
  visibility: 'org' | 'private'
  concurrency: Concurrency
  scope: 'project' | 'global'
  lease: string
  status: 'active' | 'archived'
  created_by: Actor
  created_at: string
  updated_at: string
  role?: string
  /** ask when a project admin wants every agent asked in. */
  agent_join: 'follow' | 'ask'
  /** The caller's own rule for their agents here; on GET /projects/{slug} and PUT agent-join. */
  my_agent_join?: AgentJoin
  counts?: StatusCounts
}

export interface Claim {
  actor: Actor
  session?: string
  session_name?: string
  expires_at: string
  /** The claim is the viewer's own and live. */
  mine?: boolean
  /** The claim is the viewer's own, live or lapsed. */
  owned?: boolean
}

export interface Question {
  choices?: string[]
  answer?: string
  answered_by?: Actor
  answered_at?: string
}

export interface CardLink {
  kind: LinkKind
  /** Slug of the other card's project when it is not this card's. */
  project?: string
  number: number
  title: string
  status: CardStatus
  direction: 'in' | 'out'
}

/** What a decision card adds; supersedes and superseded_by are card numbers in its project. */
export interface CardDecision {
  accepted_by?: Actor
  accepted_at?: string
  supersedes?: number
  superseded_by?: number
}

/** Where a closed card's result went; into is empty when nothing durable changed. */
export interface CardFold {
  into: string
  at: string
  by: Actor
}

/** One end of a link listed for a project; project is set when the card is in another project. */
export interface LinkEnd {
  number: number
  project?: string
}

/** A row of GET /projects/{slug}/links. */
export interface ProjectLink {
  from: LinkEnd
  to: LinkEnd
  kind: LinkKind
}

/** A row of GET /projects/{slug}/mentions: a card that references a note. */
export interface ProjectMention {
  card: number
  note: { id: string; slug: string }
}

/** A row of GET /projects/{slug}/map/groups: a story, or a lane of the cards outside any story. */
export interface MapGroup {
  key: string
  kind: 'story' | 'lane'
  story?: number
  title?: string
  status?: CardStatus
  lane?: string
  counts: StatusCounts
}

/** A row of GET /projects/{slug}/map/group-links: links of one kind from cards of group `from` to cards of group `to`. */
export interface MapGroupLink {
  from: string
  to: string
  kind: LinkKind
  count: number
}

/** A story's children: done or dropped over all. */
/** The agent-written HTML page of a card or note. */
export interface HTMLPage {
  html: string
  bytes: number
  /** Version of the card or note the page belongs to. */
  version: number
}

export interface Progress {
  done: number
  total: number
}

/** An agent's settling of a card (an answer or a verdict) that stands for a person until they keep it. */
export interface Behalf {
  for: Actor
  /** The agent. */
  by: Actor
  session?: string
  session_name?: string
  reason: string
  /** Absent until the person has looked at it. */
  kept_at?: string
}

export interface CardBrief {
  id: string
  /** Project slug; set on rows of GET /cards. */
  project?: string
  number: number
  decision?: CardDecision
  kind: CardKind
  status: CardStatus
  title: string
  priority: Priority
  parent?: number
  claim?: Claim
  assignee?: Actor
  branch?: string
  lane?: string
  drop_reason?: DropReason
  fold?: CardFold
  /** A question whose answer an agent relayed. */
  relayed?: boolean
  /** A question only a person may answer. */
  for_person?: boolean
  behalf?: Behalf
  /** Size of the card's HTML page in bytes; absent when it has none. */
  page_bytes?: number
  updated_at: string
  closed_at?: string
}

export interface Comment {
  id: string
  card: number
  actor: Actor
  body: string
  created_at: string
}

export interface Reply {
  card: CardBrief
  comment: Comment
  /** Comments by others on this card since the viewer last saw it. */
  unseen: number
}

export type ReplyList = Page<Reply>

export interface Subject {
  type: 'card' | 'note' | 'project' | 'agent' | 'membership' | 'token'
  id: string
  number?: number
}

export interface LorvikEvent {
  id: string
  /** Fixed-width feed position; string order is feed order. The cursor for after= and before=. */
  position: string
  type: string
  project?: string
  actor: Actor
  subject: Subject
  payload?: Record<string, unknown>
  created_at: string
}

export interface Backlink {
  type: 'card' | 'note'
  id: string
  project?: string
  number?: number
  slug?: string
  title: string
}

export type NoteStatus = 'active' | 'archived'

export interface Note {
  id: string
  project?: string
  slug: string
  title: string
  body?: string
  page_bytes?: number
  status: NoteStatus
  version: number
  created_by: Actor
  updated_by: Actor
  created_at: string
  updated_at: string
  backlinks?: Backlink[]
}

export interface NoteRevision {
  id: string
  version: number
  updated_by: Actor
  title: string
  body?: string
  page?: string
  created_at: string
}

export interface CardRevision {
  id: string
  version: number
  updated_by: Actor
  title: string
  body?: string
  page?: string
  created_at: string
}

export interface NoteBrief {
  id: string
  slug: string
  title: string
  project?: string
  status: NoteStatus
  page_bytes?: number
  updated_by: Actor
  updated_at: string
}

export interface Card {
  id: string
  project: string
  number: number
  kind: CardKind
  status: CardStatus
  /** A question whose answer an agent relayed. */
  relayed?: boolean
  /** A question only a person may answer. */
  for_person?: boolean
  behalf?: Behalf
  drop_reason?: DropReason
  title: string
  body?: string
  page_bytes?: number
  parent?: number
  priority: Priority
  assignee?: Actor
  branch?: string
  lane?: string
  created_by: Actor
  version: number
  claim?: Claim
  question?: Question
  decision?: CardDecision
  fold?: CardFold
  created_at: string
  updated_at: string
  closed_at?: string
  links?: CardLink[]
  children?: CardBrief[]
  progress?: Progress
  comment?: Comment
  events?: LorvikEvent[]
  blocked_by?: CardBrief[]
}

/** The paging part of every list response. next/prev are opaque cursors; remaining is capped at 100. */
/** One match from GET /search: a card (number, kind, status) or a note (slug). */
export interface SearchHit {
  type: 'card' | 'note'
  id: string
  kind?: CardKind
  number?: number
  slug?: string
  project?: string
  title: string
  status?: CardStatus
  snippet: string
  rank: number
}

export type SearchResult = Page<SearchHit>

/** One page of a newest-first comment or revision list: older than `before` (a comment id or a version), at most `limit`. */
export interface HistoryQuery {
  before?: string
  limit: number
}

export interface Page<T> {
  items: T[]
  next?: string
  prev?: string
  remaining: number
}

/** A row of GET /claims: a live claim and the card it holds. */
export interface LiveClaim {
  card: { project: string; number: number; title: string; kind: CardKind; status: CardStatus }
  holder: Actor
  session?: string
  session_name?: string
  claimed_at: string
  expires_at: string
  mine?: boolean
}

export interface Lock {
  id: string
  name: string
  holder: Actor
  session?: string
  session_name?: string
  purpose: string
  card?: number
  project?: string
  acquired_at: string
  expires_at: string
}

export interface Inbox {
  events: LorvikEvent[]
  cursor: string
  remaining: number
  claims: CardBrief[]
  claims_other_sessions: CardBrief[]
  open_questions: number
}

export interface Token {
  id: string
  actor: Actor
  name?: string
  prefix: string
  scope: 'read' | 'write' | 'signin'
  status: 'active' | 'rotated' | 'revoked'
  expires_at: string
  replaced_by?: string
  last_used_at?: string
  created_at: string
}

export interface TokenCreated {
  token: Token
  secret: string
}

export interface AgentCreated {
  agent: Agent
  token: string
  token_expires_at: string
}

export interface Login {
  login: string
  password: string
  org?: string
  choose?: boolean
  token_name?: string
}

/** An org a sign-in token may list; member and invited can be entered, requested waits for an admin. */
export interface SignInOrg {
  id: string
  name: string
  role: Role
  status: 'member' | 'invited' | 'requested'
}

/** GET /me/deletion: what deleting the account would do now. */
export interface DeletionPreview {
  closes: { name: string; projects: number; cards: number }[]
  blocked_by: string[]
}

export interface SignInOrgs {
  login: string
  orgs: SignInOrg[]
}

export interface GitHubAuth {
  enabled: boolean
  client_id?: string
}

export interface GitHubLink {
  url: string
}

export interface Invite {
  login: string
  role: Role
}

export interface CreateCard {
  kind?: CardKind
  title: string
  body?: string
  /** Number of an open story in the same project. */
  parent?: number
  priority?: Priority
  status?: 'backlog' | 'ready'
}

/** Fields omitted are unchanged; an empty string clears branch or lane. */
export interface UpdateCard {
  version: number
  branch?: string
  lane?: string
}

export interface CreateProject {
  slug: string
  name: string
  description?: string
  /** Omitted takes the org's default. */
  visibility?: 'org' | 'private'
}

export interface UpdateProject {
  concurrency?: Concurrency
  agent_join?: 'follow' | 'ask'
}

export interface TransitionCard {
  version: number
  status: CardStatus
  drop_reason?: DropReason
  duplicate?: number
  note?: string
  /** Required when a story moves to done. */
  fold?: Fold
  /** Drops the open children along with a story being dropped. */
  cascade?: boolean
}

/** Exactly one of into and none is set. */
export interface Fold {
  into?: string
  none?: boolean
}

export interface ClaimCard {
  version: number
}

export interface FoldCard extends Fold {
  version: number
}

export interface KeepCard {
  version: number
}

export interface MarkForPerson {
  version: number
  /** Takes the mark off; users only. */
  clear?: boolean
  note?: string
}

export interface AnswerCard {
  version: number
  answer: string
  decide?: boolean
  /** Agents only: the answer was heard from the human in chat. */
  relayed?: boolean
  note?: string
}

export interface UpdateNote {
  version: number
  title?: string
  body?: string
  status?: NoteStatus
}

export interface CreateAgent {
  name: string
  harness?: string
  machine?: string
  rights?: AgentRight[]
  projects?: string[]
  org_wide?: boolean
}

export interface CreateToken {
  name?: string
  scope?: 'read' | 'write'
  expires_in?: string
  agent?: string
}
