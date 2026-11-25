import { reactive, watch } from 'vue'
import { allPages, api } from './api'
import { auth, readOrgStored, writeOrgStored } from './auth'
import type { Project } from './types'

export const projects = reactive<{ list: Project[]; loaded: boolean; current: string }>({
  list: [],
  loaded: false,
  current: '',
})

// The selected project and the list belong to one org: they are read again whenever the signed-in org changes.
watch(
  () => auth.me?.org.id,
  (id) => {
    projects.list = []
    projects.loaded = false
    projects.current = id ? readOrgStored('project') : ''
  },
  { flush: 'sync' },
)

export function setCurrentProject(slug: string): void {
  projects.current = slug
  writeOrgStored('project', slug)
}

export function globalProjectSlugs(): string[] {
  return projects.list.filter((p) => p.scope === 'global').map((p) => p.slug)
}

export function globalProjectSlug(fallback = ''): string {
  return globalProjectSlugs()[0] ?? fallback
}

export async function loadProjects(force = false): Promise<void> {
  if (projects.loaded && !force) return
  projects.list = await allPages((cursor) => api.projects(cursor))
  projects.loaded = true
  if (projects.current && !projects.list.some((p) => p.slug === projects.current)) {
    projects.current = ''
  }
}

/** Which projects a list covers: the selected one, or everything visible. */
export type ListScope = 'project' | 'all'

/** The `project` query value for a scope: omitted lists everything visible. */
export function scopeProject(scope: ListScope): string | undefined {
  return scope === 'project' ? projects.current || undefined : undefined
}
