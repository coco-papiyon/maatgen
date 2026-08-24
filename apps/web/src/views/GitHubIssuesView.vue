<script setup lang="ts">
import { useGitHubItemList } from '../github/useGitHubItemList';
import { githubWorkspace } from '../github/workspace';

const { items, loading, error, projectsUnavailable, fetchedAt, state, assignee, author, labelsText, text, project, status, refresh } =
  useGitHubItemList('issue');

function statusValue(item: (typeof items.value)[number]): string {
  const field = (item.projectFields ?? []).find((candidate) => candidate.fieldName.toLowerCase() === 'status');
  return field?.value ?? (item.projectsError ? '（取得不可）' : '—');
}
</script>

<template>
  <div class="github-view">
    <div class="github-card-header">
      <h1 class="github-view-title">Issue一覧</h1>
      <button type="button" :disabled="loading" @click="refresh">再取得</button>
    </div>

    <p v-if="!githubWorkspace" class="github-empty-hint">
      Sessionを選択すると、そのリポジトリが自動的に対象になります（画面右上に表示されます）。
    </p>

    <template v-else>
      <form class="github-filter-bar" @submit.prevent="refresh">
        <label>state
          <select v-model="state" @change="refresh">
            <option value="open">open</option>
            <option value="closed">closed</option>
            <option value="all">all</option>
          </select>
        </label>
        <label>assignee<input v-model="assignee" placeholder="login" /></label>
        <label>author<input v-model="author" placeholder="login" /></label>
        <label>label<input v-model="labelsText" placeholder="bug, P1" /></label>
        <label>project<input v-model="project" placeholder="Roadmap" /></label>
        <label>status<input v-model="status" placeholder="Ready" /></label>
        <label>キーワード<input v-model="text" placeholder="title / body" /></label>
        <button type="submit">絞り込む</button>
      </form>

      <p v-if="error" class="github-error">{{ error }}</p>
      <p v-else-if="loading" class="github-hint">読み込み中…</p>
      <p v-else-if="!items.length" class="github-hint">該当するIssueはありません。</p>
      <template v-else>
        <p class="github-meta">{{ items.length }}件 / 取得時刻 {{ fetchedAt }}</p>
        <p v-if="projectsUnavailable" class="github-error">一部の項目でProject情報を取得できませんでした。</p>
        <table class="github-table">
          <thead>
            <tr>
              <th>#</th><th>タイトル</th><th>state</th><th>author</th><th>assignees</th><th>labels</th><th>Status</th><th>更新</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in items" :key="item.number">
              <td>
                <RouterLink :to="`/github/issues/${item.number}`">#{{ item.number }}</RouterLink>
              </td>
              <td>{{ item.title }}</td>
              <td>{{ item.state }}</td>
              <td>{{ item.author.login }}</td>
              <td>{{ (item.assignees ?? []).map((a) => a.login).join(', ') }}</td>
              <td>{{ (item.labels ?? []).map((l) => l.name).join(', ') }}</td>
              <td>{{ statusValue(item) }}</td>
              <td>{{ item.updatedAt }}</td>
            </tr>
          </tbody>
        </table>
      </template>
    </template>
  </div>
</template>
