import { watch } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import { api } from './lib/api'
import { auth, clearSession, setSession } from './lib/auth'
import { defaultView, readChosenView, saveChosenView } from './lib/projectView'
import { projects, loadProjects, setCurrentProject } from './lib/projects'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./views/LoginView.vue'), meta: { public: true } },
    { path: '/', name: 'home', redirect: () => (projects.current ? `/p/${projects.current}` : '/projects') },
    { path: '/projects', name: 'projects', component: () => import('./views/ProjectsView.vue') },
    {
      path: '/p/:slug',
      name: 'project',
      // Never rendered: beforeEnter always redirects to the view that should open.
      component: () => import('./views/BoardView.vue'),
      props: true,
      beforeEnter: async (to) => {
        const slug = String(to.params.slug)
        let view = readChosenView(slug)
        if (!view) {
          try {
            view = defaultView((await api.project(slug)).counts)
          } catch {
            view = 'board'
          }
        }
        return { name: view, params: { slug }, query: to.query, hash: to.hash, replace: true }
      },
    },
    { path: '/p/:slug/board', name: 'board', component: () => import('./views/BoardView.vue'), props: true },
    { path: '/p/:slug/map', name: 'map', component: () => import('./views/MapView.vue'), props: true },
    {
      path: '/p/:slug/cards/:number(\\d+)',
      name: 'card',
      component: () => import('./views/CardView.vue'),
      props: true,
    },
    { path: '/p/:slug/activity', name: 'activity', component: () => import('./views/ActivityView.vue'), props: true },
    { path: '/inbox', name: 'inbox', component: () => import('./views/InboxView.vue') },
    { path: '/admin', name: 'admin', component: () => import('./views/AdminView.vue') },
    { path: '/decisions', name: 'decisions', component: () => import('./views/DecisionsView.vue') },
    { path: '/stories', name: 'stories', component: () => import('./views/StoriesView.vue') },
    { path: '/notes', name: 'notes', component: () => import('./views/NotesView.vue') },
    { path: '/notes/:noteKey', name: 'note', component: () => import('./views/NoteView.vue'), props: true },
    { path: '/notes/:noteKey/edit', name: 'note-edit', component: () => import('./views/NoteEditView.vue'), props: true },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  if (to.meta.public) {
    return auth.token && auth.me ? { path: '/' } : true
  }
  if (!auth.token) return { name: 'login', query: { next: to.fullPath } }
  try {
    if (!auth.me) {
      const me = await api.me()
      // Read only now: that call replaces auth.token when the server rotated it.
      setSession(auth.token, me)
    }
    await loadProjects()
  } catch {
    // a 401 cleared the token; other failures surface in the view
    if (!auth.token) return { name: 'login', query: { next: to.fullPath } }
  }
  const slug = to.params.slug
  if (typeof slug === 'string' && slug !== projects.current) setCurrentProject(slug)
  return true
})

// Whichever of the Board or the Map the viewer went to is the one the project
// opens to next time. An automatic pick by the project route is not a choice,
// so a project that grows past the limit still opens on the Board by default.
router.afterEach((to, _from, failure) => {
  if (failure || (to.name !== 'board' && to.name !== 'map') || to.redirectedFrom?.name === 'project') return
  saveChosenView(String(to.params.slug), to.name)
})

// True while the page leaves for the sign-in page on purpose, so the watcher below does not send it there a second time with its own query.
let leaving = false

function leaveForSignIn(query: Record<string, string>): void {
  leaving = true
  clearSession()
  void router.push({ name: 'login', query }).finally(() => {
    leaving = false
  })
}

/** Leaves the signed-in org for the sign-in page, which then asks which org to enter or create. */
export function switchOrg(): void {
  leaveForSignIn({ choose: '1' })
}

/** Leaves after the account was deleted; the sign-in page says so. */
export function leaveDeletedAccount(): void {
  leaveForSignIn({ notice: 'deleted' })
}

// A 401 or sign-out clears the token; leave the current page for sign-in.
watch(
  () => auth.token,
  (token) => {
    const here = router.currentRoute.value
    if (!token && !leaving && here.name !== 'login') void router.replace({ name: 'login', query: { next: here.fullPath } })
  },
)
