import type { RouteLocationRaw } from 'vue-router'

export interface NavSection {
  name: string
  label: string
  // Route names that count as being inside this section.
  names: string[]
  project?: boolean
  // In the header's section bar; the inbox has its own button there.
  bar: boolean
  keys?: string
}

// One order for the header, the palette and the go-to keys.
export const sections: NavSection[] = [
  // The project route opens the Board or the Map, whichever the viewer was last on.
  { name: 'project', label: 'Board', names: ['board', 'map', 'card'], project: true, bar: true, keys: 'g b' },
  { name: 'decisions', label: 'Decisions', names: ['decisions'], bar: true, keys: 'g d' },
  { name: 'stories', label: 'Stories', names: ['stories'], bar: true, keys: 'g s' },
  { name: 'notes', label: 'Notes', names: ['notes', 'note', 'note-edit'], bar: true, keys: 'g n' },
  { name: 'activity', label: 'Activity', names: ['activity'], project: true, bar: true, keys: 'g a' },
  { name: 'inbox', label: 'Inbox', names: ['inbox'], bar: false, keys: 'g i' },
  { name: 'projects', label: 'Projects', names: ['projects'], bar: true, keys: 'g p' },
  { name: 'admin', label: 'Access', names: ['admin'], bar: true },
]

export function sectionTo(section: NavSection, slug: string): RouteLocationRaw | undefined {
  if (section.project && !slug) return undefined
  return { name: section.name, params: section.project ? { slug } : undefined }
}
