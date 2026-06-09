<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { api, type AdminConfig, type AdminConfigUpdate } from '@/api/client'

const config = ref<AdminConfig | null>(null)
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const success = ref('')
const activeTab = ref('runtime')

// Form state
const runtimeProvider = ref('')
const claudeApiKey = ref('')
const claudeModel = ref('')
const claudeBaseURL = ref('')
const claudeMaxTokens = ref(4096)
const openaiApiKey = ref('')
const openaiModel = ref('')
const openaiBaseURL = ref('')
const openaiMaxTokens = ref(4096)
const openaiFormat = ref('chat')
const geminiApiKey = ref('')
const geminiModel = ref('')
const geminiBaseURL = ref('')
const geminiMaxTokens = ref(4096)
const tokenPromptPrice = ref(1.5)
const tokenOutputPrice = ref(2.5)
const tokenBudgetWarn = ref(0)
const tokenBudgetBlock = ref(0)
const s3Provider = ref('filesystem')
const s3Endpoint = ref('')
const s3AccessKey = ref('')
const s3SecretKey = ref('')
const s3Bucket = ref('')
const s3Region = ref('')
const s3UseSSL = ref(false)
const alertWebhookURL = ref('')
const oidcIssuer = ref('')
const oidcClientID = ref('')
const oidcClientSecret = ref('')
const oidcRedirectURL = ref('')

async function loadConfig() {
  try {
    loading.value = true
    const data = await api.getAdminConfig()
    config.value = data
    runtimeProvider.value = data.runtime.provider
    claudeModel.value = data.runtime.claude.model
    claudeBaseURL.value = data.runtime.claude.baseURL
    claudeMaxTokens.value = data.runtime.claude.maxTokens
    openaiModel.value = data.runtime.openai.model
    openaiBaseURL.value = data.runtime.openai.baseURL
    openaiMaxTokens.value = data.runtime.openai.maxTokens
    openaiFormat.value = data.runtime.openai.format
    geminiModel.value = data.runtime.gemini.model
    geminiBaseURL.value = data.runtime.gemini.baseURL
    geminiMaxTokens.value = data.runtime.gemini.maxTokens
    tokenPromptPrice.value = data.token.promptPricePerMillion
    tokenOutputPrice.value = data.token.outputPricePerMillion
    tokenBudgetWarn.value = data.token.budgetWarnUSD
    tokenBudgetBlock.value = data.token.budgetBlockUSD
    s3Provider.value = data.s3.provider
    s3Endpoint.value = data.s3.endpoint
    s3Bucket.value = data.s3.bucket
    s3Region.value = data.s3.region
    s3UseSSL.value = data.s3.useSSL
    oidcIssuer.value = data.oidc.issuer
    oidcClientID.value = data.oidc.clientID
    oidcRedirectURL.value = data.oidc.redirectURL
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function saveSection(section: string) {
  try {
    saving.value = true
    error.value = ''
    success.value = ''

    const update: AdminConfigUpdate = {}
    if (section === 'runtime') {
      update.runtime = {
        provider: runtimeProvider.value,
        claude: {
          ...(claudeApiKey.value ? { apiKey: claudeApiKey.value } : {}),
          model: claudeModel.value,
          baseURL: claudeBaseURL.value,
          maxTokens: claudeMaxTokens.value,
        },
        openai: {
          ...(openaiApiKey.value ? { apiKey: openaiApiKey.value } : {}),
          model: openaiModel.value,
          baseURL: openaiBaseURL.value,
          maxTokens: openaiMaxTokens.value,
          format: openaiFormat.value,
        },
        gemini: {
          ...(geminiApiKey.value ? { apiKey: geminiApiKey.value } : {}),
          model: geminiModel.value,
          baseURL: geminiBaseURL.value,
          maxTokens: geminiMaxTokens.value,
        },
      }
    } else if (section === 'token') {
      update.token = {
        promptPricePerMillion: tokenPromptPrice.value,
        outputPricePerMillion: tokenOutputPrice.value,
        budgetWarnUSD: tokenBudgetWarn.value,
        budgetBlockUSD: tokenBudgetBlock.value,
      }
    } else if (section === 's3') {
      update.s3 = {
        provider: s3Provider.value,
        endpoint: s3Endpoint.value,
        ...(s3AccessKey.value ? { accessKey: s3AccessKey.value } : {}),
        ...(s3SecretKey.value ? { secretKey: s3SecretKey.value } : {}),
        bucket: s3Bucket.value,
        region: s3Region.value,
        useSSL: s3UseSSL.value,
      }
    } else if (section === 'alert') {
      update.alert = { ...(alertWebhookURL.value ? { webhookURL: alertWebhookURL.value } : {}) }
    } else if (section === 'oidc') {
      update.oidc = {
        issuer: oidcIssuer.value,
        clientID: oidcClientID.value,
        ...(oidcClientSecret.value ? { clientSecret: oidcClientSecret.value } : {}),
        redirectURL: oidcRedirectURL.value,
      }
    }

    const result = await api.updateAdminConfig(update)
    success.value = result.message
    // Clear secret fields after save
    claudeApiKey.value = ''
    openaiApiKey.value = ''
    geminiApiKey.value = ''
    s3AccessKey.value = ''
    s3SecretKey.value = ''
    alertWebhookURL.value = ''
    oidcClientSecret.value = ''
  } catch (e: any) {
    error.value = e.message
  } finally {
    saving.value = false
  }
}

onMounted(loadConfig)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Settings</h1>
      <span class="pill pill-done">Admin</span>
    </div>

    <div v-if="loading" class="loading-state">
      <span class="spinner"></span> Loading...
    </div>

    <div v-else-if="error && !config" class="card">
      <p style="color: var(--danger)">{{ error }}</p>
    </div>

    <template v-else>
      <div v-if="success" class="alert alert-success">{{ success }}</div>
      <div v-if="error" class="alert alert-danger">{{ error }}</div>

      <div class="tabs">
        <button class="tab" :class="{ active: activeTab === 'runtime' }" @click="activeTab = 'runtime'">Runtime</button>
        <button class="tab" :class="{ active: activeTab === 'token' }" @click="activeTab = 'token'">Token Cost</button>
        <button class="tab" :class="{ active: activeTab === 's3' }" @click="activeTab = 's3'">S3 Storage</button>
        <button class="tab" :class="{ active: activeTab === 'alert' }" @click="activeTab = 'alert'">Alerts</button>
        <button class="tab" :class="{ active: activeTab === 'oidc' }" @click="activeTab = 'oidc'">OIDC</button>
      </div>

      <!-- Runtime Tab -->
      <div v-if="activeTab === 'runtime'" class="card">
        <div class="card-title">Runtime Provider</div>
        <div class="form-group">
          <label>Active Provider</label>
          <select v-model="runtimeProvider" class="form-select">
            <option value="local">Local (Mock)</option>
            <option value="claude">Claude (Anthropic)</option>
            <option value="openai">OpenAI Compatible</option>
            <option value="gemini">Gemini (Google)</option>
            <option value="http">HTTP Endpoint</option>
          </select>
        </div>

        <h3 style="margin: 20px 0 12px; font-size: 15px;">Claude</h3>
        <div class="grid grid-2">
          <div class="form-group">
            <label>API Key (leave empty to keep current)</label>
            <input v-model="claudeApiKey" type="password" class="form-input" placeholder="sk-ant-...">
          </div>
          <div class="form-group">
            <label>Model</label>
            <input v-model="claudeModel" class="form-input">
          </div>
          <div class="form-group">
            <label>Base URL</label>
            <input v-model="claudeBaseURL" class="form-input">
          </div>
          <div class="form-group">
            <label>Max Tokens</label>
            <input v-model.number="claudeMaxTokens" type="number" class="form-input">
          </div>
        </div>

        <h3 style="margin: 20px 0 12px; font-size: 15px;">OpenAI</h3>
        <div class="grid grid-2">
          <div class="form-group">
            <label>API Key (leave empty to keep current)</label>
            <input v-model="openaiApiKey" type="password" class="form-input" placeholder="sk-...">
          </div>
          <div class="form-group">
            <label>Model</label>
            <input v-model="openaiModel" class="form-input">
          </div>
          <div class="form-group">
            <label>Base URL</label>
            <input v-model="openaiBaseURL" class="form-input">
          </div>
          <div class="form-group">
            <label>Max Tokens</label>
            <input v-model.number="openaiMaxTokens" type="number" class="form-input">
          </div>
          <div class="form-group">
            <label>Format</label>
            <select v-model="openaiFormat" class="form-select">
              <option value="chat">Chat Completions</option>
              <option value="completions">Legacy Completions (Codex)</option>
            </select>
          </div>
        </div>

        <h3 style="margin: 20px 0 12px; font-size: 15px;">Gemini</h3>
        <div class="grid grid-2">
          <div class="form-group">
            <label>API Key (leave empty to keep current)</label>
            <input v-model="geminiApiKey" type="password" class="form-input" placeholder="AIza...">
          </div>
          <div class="form-group">
            <label>Model</label>
            <input v-model="geminiModel" class="form-input">
          </div>
          <div class="form-group">
            <label>Base URL</label>
            <input v-model="geminiBaseURL" class="form-input">
          </div>
          <div class="form-group">
            <label>Max Tokens</label>
            <input v-model.number="geminiMaxTokens" type="number" class="form-input">
          </div>
        </div>

        <button class="btn btn-primary" style="margin-top: 16px" @click="saveSection('runtime')" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save Runtime Settings' }}
        </button>
      </div>

      <!-- Token Tab -->
      <div v-if="activeTab === 'token'" class="card">
        <div class="card-title">Token Cost Tracking</div>
        <div class="grid grid-2">
          <div class="form-group">
            <label>Prompt Price (per 1M tokens)</label>
            <input v-model.number="tokenPromptPrice" type="number" step="0.1" class="form-input">
          </div>
          <div class="form-group">
            <label>Output Price (per 1M tokens)</label>
            <input v-model.number="tokenOutputPrice" type="number" step="0.1" class="form-input">
          </div>
          <div class="form-group">
            <label>Budget Warn (USD)</label>
            <input v-model.number="tokenBudgetWarn" type="number" step="0.01" class="form-input">
          </div>
          <div class="form-group">
            <label>Budget Block (USD)</label>
            <input v-model.number="tokenBudgetBlock" type="number" step="0.01" class="form-input">
          </div>
        </div>
        <button class="btn btn-primary" style="margin-top: 16px" @click="saveSection('token')" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save Token Settings' }}
        </button>
      </div>

      <!-- S3 Tab -->
      <div v-if="activeTab === 's3'" class="card">
        <div class="card-title">Artifact Storage</div>
        <div class="form-group">
          <label>Storage Provider</label>
          <select v-model="s3Provider" class="form-select">
            <option value="filesystem">Local Filesystem</option>
            <option value="s3">S3 / MinIO</option>
          </select>
        </div>
        <template v-if="s3Provider === 's3'">
          <div class="grid grid-2">
            <div class="form-group">
              <label>Endpoint</label>
              <input v-model="s3Endpoint" class="form-input" placeholder="localhost:9000">
            </div>
            <div class="form-group">
              <label>Bucket</label>
              <input v-model="s3Bucket" class="form-input">
            </div>
            <div class="form-group">
              <label>Access Key (leave empty to keep current)</label>
              <input v-model="s3AccessKey" type="password" class="form-input">
            </div>
            <div class="form-group">
              <label>Secret Key (leave empty to keep current)</label>
              <input v-model="s3SecretKey" type="password" class="form-input">
            </div>
            <div class="form-group">
              <label>Region</label>
              <input v-model="s3Region" class="form-input">
            </div>
            <div class="form-group">
              <label>Use SSL</label>
              <select v-model="s3UseSSL" class="form-select">
                <option :value="false">No</option>
                <option :value="true">Yes</option>
              </select>
            </div>
          </div>
        </template>
        <button class="btn btn-primary" style="margin-top: 16px" @click="saveSection('s3')" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save Storage Settings' }}
        </button>
      </div>

      <!-- Alert Tab -->
      <div v-if="activeTab === 'alert'" class="card">
        <div class="card-title">Alert Webhook</div>
        <div class="form-group">
          <label>Webhook URL (leave empty to keep current)</label>
          <input v-model="alertWebhookURL" class="form-input" placeholder="https://hooks.slack.com/...">
        </div>
        <button class="btn btn-primary" style="margin-top: 16px" @click="saveSection('alert')" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save Alert Settings' }}
        </button>
      </div>

      <!-- OIDC Tab -->
      <div v-if="activeTab === 'oidc'" class="card">
        <div class="card-title">OIDC / OAuth2</div>
        <div class="grid grid-2">
          <div class="form-group">
            <label>Issuer URL</label>
            <input v-model="oidcIssuer" class="form-input" placeholder="https://accounts.google.com">
          </div>
          <div class="form-group">
            <label>Client ID</label>
            <input v-model="oidcClientID" class="form-input">
          </div>
          <div class="form-group">
            <label>Client Secret (leave empty to keep current)</label>
            <input v-model="oidcClientSecret" type="password" class="form-input">
          </div>
          <div class="form-group">
            <label>Redirect URL</label>
            <input v-model="oidcRedirectURL" class="form-input" placeholder="https://your-app.com/auth/oidc/callback">
          </div>
        </div>
        <button class="btn btn-primary" style="margin-top: 16px" @click="saveSection('oidc')" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save OIDC Settings' }}
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.loading-state { display: flex; align-items: center; gap: 12px; color: var(--text-secondary); padding: 40px; justify-content: center; }
.tabs { display: flex; gap: 4px; margin-bottom: 16px; border-bottom: 1px solid var(--border); padding-bottom: 4px; }
.tab {
  padding: 8px 16px; border: none; background: none; color: var(--text-secondary);
  cursor: pointer; border-radius: var(--radius) var(--radius) 0 0; font-size: 13px;
}
.tab.active { color: var(--accent); background: var(--card-bg); border: 1px solid var(--border); border-bottom-color: var(--card-bg); margin-bottom: -5px; }
.alert { padding: 12px 16px; border-radius: var(--radius); margin-bottom: 16px; font-size: 14px; }
.alert-success { background: rgba(63, 185, 80, 0.1); color: var(--success); border: 1px solid var(--success); }
.alert-danger { background: rgba(248, 81, 73, 0.1); color: var(--danger); border: 1px solid var(--danger); }
</style>
