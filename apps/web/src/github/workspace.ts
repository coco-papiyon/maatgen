import { ref } from 'vue';

// The GitHub monitoring area (ADR-007 section 7) tracks whichever local
// repository the user is currently working in: App.vue sets this to the
// selected Session's workspace path every time selection changes (see
// App.vue's selectSession), and Shell.vue resolves/auto-provisions the
// corresponding GitHub repository from it (see github/repository.ts). This
// ref is a module-level singleton (not per-component state) so every
// GitHub view and the common navigation stay in sync.
export const githubWorkspace = ref('');
