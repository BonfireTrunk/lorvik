import { readOrgStored, writeOrgStored } from './auth'
import { OPEN_STATUSES } from './cards'
import type { StatusCounts } from './types'

export type ProjectView = 'board' | 'map'

/** A project with this many open cards or fewer opens on the Map by default. */
export const MAP_DEFAULT_MAX_OPEN = 100

const viewKey = (slug: string) => `view.${slug}`

/** The view the viewer was last on in this project, if any. */
export function readChosenView(slug: string): ProjectView | undefined {
  const v = readOrgStored(viewKey(slug))
  return v === 'board' || v === 'map' ? v : undefined
}

export function saveChosenView(slug: string, view: ProjectView): void {
  writeOrgStored(viewKey(slug), view)
}

/** Open cards: backlog, ready, working and waiting. */
export function openCardCount(counts: StatusCounts): number {
  return OPEN_STATUSES.reduce((n, s) => n + counts[s], 0)
}

export function defaultView(counts: StatusCounts | undefined): ProjectView {
  return counts && openCardCount(counts) > MAP_DEFAULT_MAX_OPEN ? 'board' : 'map'
}
