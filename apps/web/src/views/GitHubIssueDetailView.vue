<script setup lang="ts">
import { toRef } from 'vue';
import { useGitHubItemDetail } from '../github/useGitHubItemDetail';
import { selectedRepository } from '../github/repositories';

const props = defineProps<{ number: number }>();
const numberRef = toRef(props, 'number');
const { item, relatedEvents, loading, error } = useGitHubItemDetail('issue', () => numberRef.value);
</script>

<template>
  <div class="github-view">
    <p v-if="!selectedRepository" class="github-empty-hint">画面右上でリポジトリを選択してください。</p>
    <p v-else-if="loading" class="github-hint">読み込み中…</p>
    <p v-else-if="error" class="github-error">{{ error }}</p>
    <template v-else-if="item">
      <div class="github-card-header">
        <h1 class="github-view-title">#{{ item.number }} {{ item.title }}</h1>
        <a :href="item.url" target="_blank" rel="noopener">GitHubで開く</a>
      </div>
      <dl class="github-kv">
        <dt>state</dt><dd>{{ item.state }}</dd>
        <dt>author</dt><dd>{{ item.author.login }}</dd>
        <dt>assignees</dt><dd>{{ item.assignees.map((a) => a.login).join(', ') || '—' }}</dd>
        <dt>labels</dt><dd>{{ item.labels.map((l) => l.name).join(', ') || '—' }}</dd>
        <dt v-if="item.milestone">milestone</dt><dd v-if="item.milestone">{{ item.milestone.title }}</dd>
        <dt>更新</dt><dd>{{ item.updatedAt }}</dd>
      </dl>
      <p v-if="item.projectsError" class="github-error">Project情報を取得できませんでした: {{ item.projectsError }}</p>
      <ul v-else-if="item.projectFields?.length" class="github-plain-list">
        <li v-for="field in item.projectFields" :key="`${field.projectTitle}-${field.fieldName}`">
          {{ field.projectTitle }} / {{ field.fieldName }}: {{ field.value }}
        </li>
      </ul>
      <div class="markdown-body">{{ item.body }}</div>

      <section class="github-card">
        <h2>関連する監視イベント</h2>
        <p v-if="!relatedEvents.length" class="github-hint">このIssueに関連する監視イベントはありません。</p>
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
    </template>
  </div>
</template>
