import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import App from './App.vue';

// ADR-007 section 7: a common navigation area with "Session" and "GitHub
// 監視" (with its own Event history/Issue/PR/Settings sub-areas), each a
// real, deep-linkable, browser-back-compatible route rather than an
// in-page tab. App.vue itself is unchanged and becomes the "/" route's
// component.
const routes: RouteRecordRaw[] = [
  { path: '/', name: 'sessions', component: App },
  { path: '/github', redirect: { name: 'github-events' } },
  {
    path: '/github/events',
    name: 'github-events',
    component: () => import('./views/GitHubEventsView.vue'),
  },
  {
    path: '/github/issues',
    name: 'github-issues',
    component: () => import('./views/GitHubIssuesView.vue'),
  },
  {
    path: '/github/issues/:number',
    name: 'github-issue-detail',
    component: () => import('./views/GitHubIssueDetailView.vue'),
    props: (route) => ({ number: Number(route.params.number) }),
  },
  {
    path: '/github/pulls',
    name: 'github-pulls',
    component: () => import('./views/GitHubPullsView.vue'),
  },
  {
    path: '/github/pulls/:number',
    name: 'github-pull-detail',
    component: () => import('./views/GitHubPullDetailView.vue'),
    props: (route) => ({ number: Number(route.params.number) }),
  },
  {
    path: '/github/settings',
    name: 'github-settings',
    component: () => import('./views/GitHubSettingsView.vue'),
  },
];

export function createAppRouter() {
  return createRouter({
    history: createWebHistory(),
    routes,
    scrollBehavior(_to, _from, savedPosition) {
      // ADR-007 section 7: returning from a Session/Issue/PR detail to the
      // event history (or issue/PR list) restores scroll position.
      return savedPosition ?? { top: 0 };
    },
  });
}
