<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, type StatusMatrixAgent, type LLMProvider } from '@/api/client'
import { useSSE } from '@/composables/useSSE'
import { useI18n } from '@/i18n'

const { t } = useI18n()
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
      <h1 class="page-title">{{ t('dashboard.title') }}</h1>
      <div class="sse-indicator">
        <span class="sse-dot" :class="connected ? 'connected' : 'disconnected'"></span>
        {{ connected ? t('common.live') : t('common.offline') }}
      </div>
    </div>

    <div v-if="loading" class="loading-state">
      <span class="spinner"></span> {{ t('common.loading') }}
    </div>

    <div v-else-if="error" class="card">
      <p style="color: var(--red)">{{ error }}</p>
    </div>

    <div v-else>
      <!-- Agent Fleet -->
      <div class="section-label">{{ t('dashboard.title') }} — AGENT FLEET</div>
      <div class="grid grid-3" style="margin-bottom: 24px;">
        <div v-for="agent in agents" :key="agent.agent" class="card agent-card">
          <div class="agent-header">
            <span class="agent-name">{{ agent.agent }}</span>
            <span class="pill" :class="`pill-${agent.status.toLowerCase()}`">{{ agent.status }}</span>
          </div>
          <div class="agent-stats">
            <div class="stat">
              <div class="stat-value stat-done">{{ agent.doneTasks }}</div>
              <div class="stat-label">{{ t('taskStatus.done') }}</div>
            </div>
            <div class="stat">
              <div class="stat-value stat-running">{{ agent.runningTasks }}</div>
              <div class="stat-label">{{ t('taskStatus.inProgress') }}</div>
            </div>
            <div class="stat">
              <div class="stat-value stat-failed">{{ agent.failedTasks }}</div>
              <div class="stat-label">{{ t('taskStatus.failed') }}</div>
            </div>
          </div>
          <div class="agent-total">
            {{ t('common.total') }}: {{ agent.totalTasks }}
          </div>
        </div>
      </div>

      <div v-if="agents.length === 0" class="card" style="text-align: center; padding: 48px;">
        <p style="color: var(--text-dim); margin-bottom: 16px;">{{ t('dashboard.noAgents') }}</p>
        <router-link to="/projects" class="btn btn-primary">{{ t('dashboard.createProject') }}</router-link>
      </div>

      <!-- LLM Providers -->
      <div class="section-label">{{ t('dashboard.llmProviders') }}</div>
      <div class="card">
        <div style="font-size: 12px; color: var(--text-dim); margin-bottom: 20px;">
          {{ t('settings.activeProvider') }}: <span style="color: var(--cyan); font-weight: 700;">{{ activeProvider || '—' }}</span>
          <span style="margin-left: 8px; color: var(--text-ghost);">{{ t('dashboard.llmConfigHint') }}</span>
        </div>
        <div class="grid grid-4">
          <div v-for="p in providers" :key="p.id" class="provider-card" :class="{ active: p.active, unavailable: !p.available }">
            <div class="provider-name">{{ p.name }}</div>
            <div class="provider-model" v-if="p.model">{{ p.model }}</div>
            <div class="provider-status">
              <span v-if="p.active" class="pill pill-done">{{ t('dashboard.active') }}</span>
              <span v-else-if="p.available" class="pill pill-created">{{ t('dashboard.available') }}</span>
              <span v-else class="pill pill-failed">{{ t('dashboard.notConfigured') }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.section-label {
  font-family: var(--font-display);
  font-size: 10px;
  font-weight: 700;
  color: var(--text-ghost);
  text-transform: uppercase;
  letter-spacing: 2px;
  margin-bottom: 12px;
}
.agent-card { transition: all 0.2s; }
.agent-card:hover { border-color: var(--cyan-dim); }
.agent-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.agent-name {
  font-family: var(--font-display);
  font-size: 14px;
  font-weight: 700;
  color: var(--text-bright);
}
.agent-stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin-bottom: 16px;
}
.stat-done { color: var(--green); }
.stat-running { color: var(--cyan); }
.stat-failed { color: var(--red); }
.agent-total {
  font-size: 11px;
  color: var(--text-ghost);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
}
</style>
