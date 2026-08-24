<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink, RouterView, useRoute } from 'vue-router';
import { useAgentApi } from './github/useAgentApi';
import { githubRepositoryLabel, githubRepositoryStatus, watchGitHubRepository } from './github/repository';
import { githubWorkspace } from './github/workspace';

const route = useRoute();
watchGitHubRepository(useAgentApi());

function routeName(): string {
  return typeof route.name === 'string' ? route.name : '';
}

const isIssuesArea = computed(() => routeName().startsWith('github-issue'));
const isPullsArea = computed(() => routeName().startsWith('github-pull'));

const repositoryDisplay = computed(() => {
  switch (githubRepositoryStatus.value) {
    case 'resolved':
      return githubRepositoryLabel.value;
    case 'resolving':
      return '解決中…';
    case 'ambiguous':
      return 'remoteが複数あります（設定で選択）';
    case 'unavailable':
      return 'GitHubリポジトリ未検出';
    default:
      return 'セッション未選択';
  }
});
</script>

<template>
  <div class="shell">
    <nav class="shell-nav" aria-label="共通ナビゲーション">
      <RouterLink to="/" class="shell-nav-link" :class="{ active: routeName() === 'sessions' }">Session</RouterLink>
      <RouterLink to="/github/issues" class="shell-nav-link" :class="{ active: isIssuesArea }">Issue</RouterLink>
      <RouterLink to="/github/pulls" class="shell-nav-link" :class="{ active: isPullsArea }">PR</RouterLink>
      <RouterLink to="/github/events" class="shell-nav-link" :class="{ active: routeName() === 'github-events' }">イベント履歴</RouterLink>
      <RouterLink to="/github/settings" class="shell-nav-link" :class="{ active: routeName() === 'github-settings' }">設定</RouterLink>
      <span class="shell-repository" :class="githubRepositoryStatus" :title="githubWorkspace">{{ repositoryDisplay }}</span>
    </nav>
    <div class="shell-body">
      <RouterView />
    </div>
  </div>
</template>
