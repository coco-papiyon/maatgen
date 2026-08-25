<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { RouterLink, RouterView, useRoute } from 'vue-router';
import { useAgentApi } from './github/useAgentApi';
import { githubRepositoryStatus, watchGitHubRepository } from './github/repository';
import { refreshRepositories, remoteGroups, selectRemote, selectedRemoteKey } from './github/repositories';

const route = useRoute();
const api = useAgentApi();
watchGitHubRepository(api);
onMounted(() => void refreshRepositories(api));

function routeName(): string {
  return typeof route.name === 'string' ? route.name : '';
}

const isIssuesArea = computed(() => routeName().startsWith('github-issue'));
const isPullsArea = computed(() => routeName().startsWith('github-pull'));

function onRemoteChange(event: Event) {
  selectRemote((event.target as HTMLSelectElement).value);
}
</script>

<template>
  <div class="shell">
    <nav class="shell-nav" aria-label="共通ナビゲーション">
      <RouterLink to="/" class="shell-nav-link" :class="{ active: routeName() === 'sessions' }">Session</RouterLink>
      <RouterLink to="/github/issues" class="shell-nav-link" :class="{ active: isIssuesArea }">Issue</RouterLink>
      <RouterLink to="/github/pulls" class="shell-nav-link" :class="{ active: isPullsArea }">PR</RouterLink>
      <RouterLink to="/github/events" class="shell-nav-link" :class="{ active: routeName() === 'github-events' }">Job</RouterLink>
      <RouterLink to="/github/settings" class="shell-nav-link" :class="{ active: routeName() === 'github-settings' }">設定</RouterLink>
      <select
        v-if="remoteGroups.length"
        class="shell-repository"
        :class="githubRepositoryStatus"
        aria-label="リモートリポジトリ"
        :title="selectedRemoteKey"
        :value="selectedRemoteKey"
        @change="onRemoteChange"
      >
        <option v-for="group in remoteGroups" :key="group.key" :value="group.key">{{ group.owner }}/{{ group.name }}</option>
      </select>
      <span v-else class="shell-repository idle">リポジトリ未登録</span>
    </nav>
    <div class="shell-body">
      <RouterView />
    </div>
  </div>
</template>
