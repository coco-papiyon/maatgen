<script setup lang="ts">
import { toRef } from 'vue';
import { useGitHubItemDetail } from '../github/useGitHubItemDetail';
import { githubWorkspace } from '../github/workspace';

const props = defineProps<{ number: number }>();
const numberRef = toRef(props, 'number');
const { item, relatedEvents, loading, error } = useGitHubItemDetail('pull_request', () => numberRef.value);
</script>

<template>
  <div class="github-view">
    <p v-if="!githubWorkspace" class="github-empty-hint">Sessionを選択すると、そのリポジトリが自動的に対象になります。</p>
    <p v-else-if="loading" class="github-hint">読み込み中…</p>
    <p v-else-if="error" class="github-error">{{ error }}</p>
    <template v-else-if="item">
      <div class="github-card-header">
        <h1 class="github-view-title">#{{ item.number }} {{ item.title }}</h1>
        <a :href="item.url" target="_blank" rel="noopener">GitHubで開く</a>
      </div>
      <dl class="github-kv">
        <dt>state</dt><dd>{{ item.state }}<span v-if="item.pullRequest?.draft"> (draft)</span></dd>
        <dt>author</dt><dd>{{ item.author.login }}</dd>
        <dt>branch</dt><dd>{{ item.pullRequest?.base.ref }} ← {{ item.pullRequest?.head.ref }}</dd>
        <dt>labels</dt><dd>{{ item.labels.map((l) => l.name).join(', ') || '—' }}</dd>
        <dt>更新</dt><dd>{{ item.updatedAt }}</dd>
      </dl>
      <div class="markdown-body">{{ item.body }}</div>

      <section class="github-card">
        <h2>関連する監視イベント</h2>
        <p v-if="!relatedEvents.length" class="github-hint">このPRに関連する監視イベントはありません。</p>
        <ul v-else class="github-rule-list">
          <li v-for="event in relatedEvents" :key="event.id" class="github-rule">
            <div>
              <span class="github-badge" :class="`status-${event.status}`">{{ event.status }}</span>
              <span class="github-meta">{{ event.action }} / {{ event.createdAt }}</span>
            </div>
            <RouterLink v-if="event.sessionId" :to="`/?session=${event.sessionId}`">Sessionを見る</RouterLink>
          </li>
        </ul>
      </section>
      <p class="github-meta">GitHubへのコメント・ラベル変更・レビュー投稿はMaatgenから行いません。</p>
    </template>
  </div>
</template>
