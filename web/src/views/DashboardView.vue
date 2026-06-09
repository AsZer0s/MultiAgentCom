<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, type StatusMatrixAgent, type LLMProvider } from '@/api/client'
import { useSSE } from '@/composables/useSSE'

const agents = ref<StatusMatrixAgent[]>([])
const providers = ref<LLMProvider[]>([])
const activeProvider = ref('')
const loading = ref(true)
const error = ref('')

const { data: sseData, connected } = useSSE('/status/stream')

async function loadMatrix() {
  try {
    loading.value = true
    const [matrixResult, providersResult] = await Promise.all([
      api.getStatusMatrix(),
      api.listLLMProviders(),
    ])
    agents.value = matrixResult.agents || []
    providers.value = providersResult.providers || []
    activeProvider.value = providersResult.activeProvider || ''
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

onMounted(loadMatrix)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Dashboard</h1>
      <div class="sse-indicator">
        <span class="sse-dot" :class="connected ? 'connected' : 'disconnected'"></span>
        {{ connected ? 'Live' : 'Offline' }}
      </div>
    </div>

    <div v-if="loading" class="loading-state">
      <span class="spinner"></span> Loading...
    </div>

    <div v-else-if="error" class="card">
      <p style="color: var(--danger)">Failed to load: {{ error }}</p>
    </div>

    <div v-else class="grid grid-3">
      <div v-for="agent in agents" :key="agent.agent" class="card">
        <div class="card-title">{{ agent.agent }}</div>
        <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 12px;">
          <span class="pill" :class="`pill-${agent.status.toLowerCase()}`">{{ agent.status }}</span>
        </div>
        <div class="grid grid-3" style="gap: 8px;">
          <div class="stat">
            <div class="stat-value">{{ agent.doneTasks }}</div>
            <div class="stat-label">Done</div>
          </div>
          <div class="stat">
            <div class="stat-value">{{ agent.runningTasks }}</div>
            <div class="stat-label">Running</div>
          </div>
          <div class="stat">
            <div class="stat-value">{{ agent.failedTasks }}</div>
            <div class="stat-label">Failed</div>
          </div>
        </div>
        <div style="margin-top: 12px; font-size: 12px; color: var(--text-secondary);">
          Total: {{ agent.totalTasks }} tasks
        </div>
      </div>
    </div>

    <div v-if="!loading && agents.length === 0" class="card" style="text-align: center; padding: 40px;">
      <p style="color: var(--text-secondary);">No agents registered. Create a project to get started.</p>
      <router-link to="/projects" class="btn btn-primary" style="margin-top: 16px;">Create Project</router-link>
    </div>

    <div class="card" style="margin-top: 24px;">
      <div class="card-title">LLM Providers</div>
      <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
        Active: <strong>{{ activeProvider || 'none' }}</strong> — Configure via environment variables and restart server to switch.
      </p>
      <div class="grid grid-4">
        <div v-for="p in providers" :key="p.id" class="provider-card" :class="{ active: p.active, unavailable: !p.available }">
          <div class="provider-name">{{ p.name }}</div>
          <div class="provider-model" v-if="p.model">{{ p.model }}</div>
          <div class="provider-status">
            <span v-if="p.active" class="pill pill-done">Active</span>
            <span v-else-if="p.available" class="pill pill-created">Available</span>
            <span v-else class="pill pill-failed">Not Configured</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stat { text-align: center; }
.stat-value { font-size: 20px; font-weight: 700; }
.stat-label { font-size: 11px; color: var(--text-secondary); text-transform: uppercase; }
.loading-state { display: flex; align-items: center; gap: 12px; color: var(--text-secondary); padding: 40px; justify-content: center; }
.provider-card {
  padding: 16px;
  border-radius: var(--radius);
  border: 1px solid var(--border);
  background: var(--bg);
  transition: border-color 0.2s;
}
.provider-card.active {
  border-color: var(--success);
  background: rgba(63, 185, 80, 0.05);
}
.provider-card.unavailable {
  opacity: 0.6;
}
.provider-name {
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 4px;
}
.provider-model {
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 8px;
  font-family: monospace;
}
.provider-status {
  margin-top: 8px;
}
</style>
