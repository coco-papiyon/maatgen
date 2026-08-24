<script setup lang="ts">
import { useGitHubItemList } from '../github/useGitHubItemList';
import { selectedRepository } from '../github/repositories';

const { items, loading, error, fetchedAt, state, assignee, author, labelsText, text, refresh } = useGitHubItemList('pull_request');
</script>

<template>
  <div class="github-view">
    <div class="github-card-header">
      <h1 class="github-view-title">PR一覧</h1>
      <button type="button" :disabled="loading" @click="refresh">再取得</button>
    </div>

    <p v-if="!selectedRepository" class="github-empty-hint">
      画面右上でリポジトリを選択してください。
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
        <label>キーワード<input v-model="text" placeholder="title / body" /></label>
        <button type="submit">絞り込む</button>
      </form>

      <p v-if="error" class="github-error">{{ error }}</p>
      <p v-else-if="loading" class="github-hint">読み込み中…</p>
      <p v-else-if="!items.length" class="github-hint">該当するPull Requestはありません。</p>
      <template v-else>
        <p class="github-meta">{{ items.length }}件 / 取得時刻 {{ fetchedAt }}</p>
        <table class="github-table">
          <thead>
            <tr>
              <th>#</th><th>タイトル</th><th>state</th><th>draft</th><th>author</th><th>base ← head</th><th>更新</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in items" :key="item.number">
              <td>
                <RouterLink :to="`/github/pulls/${item.number}`">#{{ item.number }}</RouterLink>
              </td>
              <td>{{ item.title }}</td>
              <td>{{ item.state }}</td>
              <td>{{ item.pullRequest?.draft ? 'draft' : '—' }}</td>
              <td>{{ item.author.login }}</td>
              <td>{{ item.pullRequest?.base.ref }} ← {{ item.pullRequest?.head.ref }}</td>
              <td>{{ item.updatedAt }}</td>
            </tr>
          </tbody>
        </table>
      </template>
    </template>
  </div>
</template>
