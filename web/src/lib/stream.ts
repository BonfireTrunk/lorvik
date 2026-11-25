import { resolveActors } from './actors'
import { auth, clearSession } from './auth'
import { ApiError, MAX_PAGE, NEWEST_EVENT, api, apiUrl, authHeaders, readResponseHeaders } from './api'
import type { LorvikEvent } from './types'

export type StreamState = 'connecting' | 'live' | 'polling'

const POLL_MS = 15_000

const SEEN_MAX = 512

interface Listener {
  project: string
  onEvent: (e: LorvikEvent) => void
  onState: (s: StreamState) => void
}

// Every live subscriber without a replay position shares one connection to
// the all-projects stream. Each fetch stream holds an HTTP/1.1 connection for
// its life and the browser allows six per origin, so one per component (board,
// strips, panel comments) left API calls queued behind them.
const shared = { listeners: new Set<Listener>(), stop: undefined as undefined | (() => void), state: 'connecting' as StreamState }

// Every open connection, so a page that is left can end them all. The browser
// may keep a left page in its back/forward cache with its fetches still open;
// five such pages use up the six connections per origin and stall the next page.
const open = new Set<{ pause: () => void; resume: () => void }>()

if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', () => open.forEach((c) => c.pause()))
  // Only a restore from the cache needs this; a fresh load starts its own streams.
  window.addEventListener('pageshow', (e) => {
    if (e.persisted) open.forEach((c) => c.resume())
  })
}

// The server sends a project's stream that project's events plus those with
// no project (locks, memberships); the all-projects stream sends every
// readable project's plus the same. Filtering here keeps that contract.
function forProject(project: string, e: LorvikEvent): boolean {
  return !project || !e.project || e.project === project
}

/**
 * Subscribes to new events and returns a stop function. `project` limits the
 * events to one project; empty means every project the viewer can see. `after`
 * is the position of the newest event already shown (empty: start at the head).
 * Subscribers without `after` share one connection; one with `after` opens its
 * own so the server replays what it missed.
 */
export function subscribeEvents(
  project: string,
  after: string,
  onEvent: (e: LorvikEvent) => void,
  onState: (s: StreamState) => void,
): () => void {
  if (after) return openStream(project, after, onEvent, onState)
  const l: Listener = { project, onEvent, onState }
  shared.listeners.add(l)
  if (shared.stop) {
    onState(shared.state)
  } else {
    shared.state = 'connecting'
    shared.stop = openStream(
      '',
      '',
      (e) => {
        for (const x of [...shared.listeners]) if (forProject(x.project, e)) x.onEvent(e)
      },
      (s) => {
        shared.state = s
        for (const x of [...shared.listeners]) x.onState(s)
      },
    )
  }
  return () => {
    if (!shared.listeners.delete(l) || shared.listeners.size) return
    shared.stop?.()
    shared.stop = undefined
  }
}

/**
 * One connection to the event stream, with its own resume position.
 *
 * The browser EventSource cannot send an Authorization header, and the API only
 * accepts the bearer token in that header, so the SSE stream
 * (GET /api/v1/events/stream) is read with fetch() and a small frame parser
 * instead. Each event's `position` is the resume cursor. If the
 * stream cannot be opened or drops repeatedly, this falls back to polling
 * GET /api/v1/events every 15s.
 */
function openStream(
  project: string,
  after: string,
  onEvent: (e: LorvikEvent) => void,
  onState: (s: StreamState) => void,
): () => void {
  let ctl = new AbortController()
  let last = after
  // Event ids already delivered, oldest first. The server orders the feed by
  // position, so order is trusted and this only drops an event a replay or a
  // poll sends twice.
  const seen = new Set<string>()
  let failures = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const deliver = (e: LorvikEvent): void => {
    if (seen.has(e.id)) return
    seen.add(e.id)
    if (seen.size > SEEN_MAX) seen.delete(seen.values().next().value!)
    last = e.position
    resolveActors(e)
    onEvent(e)
  }

  const poll = async (signal: AbortSignal): Promise<void> => {
    onState('polling')
    try {
      for (;;) {
        if (!last) {
          // No position yet: start from the newest event so only later ones arrive.
          const head = await api.events({ project: project || undefined, before: NEWEST_EVENT, limit: 1 })
          const first = head.items[0]
          if (first) last = first.position
          break
        }
        const page = await api.events({ project: project || undefined, after: last, limit: MAX_PAGE })
        page.items.forEach(deliver)
        if (page.items.length < MAX_PAGE) break
      }
    } catch (e) {
      // A position from another database (the server was reset) is a 422 on
      // every poll; start again from the head. Anything else: keep polling, a
      // 401 already cleared the session.
      if (e instanceof ApiError && e.status === 422) last = ''
    }
    if (!signal.aborted && auth.token) timer = setTimeout(() => void poll(signal), POLL_MS)
  }

  const run = async (signal: AbortSignal): Promise<void> => {
    onState('connecting')
    const q = new URLSearchParams()
    if (project) q.set('project', project)
    if (last) q.set('after', last)
    const res = await fetch(apiUrl(`/api/v1/events/stream?${q.toString()}`), {
      headers: { Accept: 'text/event-stream', ...authHeaders(auth.token) },
      signal,
    })
    readResponseHeaders(res, false)
    if (res.status === 401) {
      clearSession()
      return
    }
    if (res.status === 422 && last) {
      // The resume position names no event here (the server was reset):
      // reconnect from the head instead of failing into polling.
      last = ''
      return
    }
    if (!res.ok || !res.body) throw new Error(`stream status ${res.status}`)
    onState('live')
    failures = 0
    if (!last) {
      // Opened at the head with no position: read where the head is, so a
      // reconnect or a page restored from the back/forward cache resumes from
      // there instead of skipping what came in between. An event the stream
      // delivers first sets the position and wins.
      void api.events({ project: project || undefined, before: NEWEST_EVENT, limit: 1 }).then(
        (head) => {
          if (!last && head.items[0]) last = head.items[0].position
        },
        () => {},
      )
    }
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      buf += value
      let i = buf.indexOf('\n\n')
      while (i >= 0) {
        const frame = buf.slice(0, i)
        buf = buf.slice(i + 2)
        const lines = frame.split('\n')
        const data = lines
          .filter((l) => l.startsWith('data:'))
          .map((l) => l.slice(5).trimStart())
          .join('\n')
        if (data) {
          try {
            const ev = JSON.parse(data) as LorvikEvent
            // The SSE id is the event id; trust it if the body lacks one.
            const sseId = lines.find((l) => l.startsWith('id:'))?.slice(3).trim()
            if (!ev.id && sseId) ev.id = sseId
            deliver(ev)
          } catch {
            // ignore malformed frame
          }
        }
        i = buf.indexOf('\n\n')
      }
    }
  }

  // Each run of the loop holds the signal it started with, so a loop ended by
  // pagehide cannot be revived by the new controller made on pageshow.
  const loop = async (signal: AbortSignal): Promise<void> => {
    while (!signal.aborted && auth.token) {
      try {
        await run(signal)
      } catch {
        if (signal.aborted) return
        failures++
      }
      if (signal.aborted || !auth.token) return
      if (failures >= 3) {
        void poll(signal)
        return
      }
      await new Promise((r) => setTimeout(r, 2000))
    }
  }

  const pause = (): void => {
    ctl.abort()
    if (timer) clearTimeout(timer)
  }
  // `last` and `seen` survive a pause, so the stream resumes after the last
  // event delivered and the replay cannot repeat one.
  const resume = (): void => {
    pause()
    ctl = new AbortController()
    failures = 0
    void loop(ctl.signal)
  }

  const handle = { pause, resume }
  open.add(handle)
  void loop(ctl.signal)
  return () => {
    open.delete(handle)
    pause()
  }
}
