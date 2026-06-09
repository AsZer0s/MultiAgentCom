<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { setAuthToken, api } from '@/api/client'

const router = useRouter()
const token = ref('')
const error = ref('')
const loading = ref(false)
const showOIDC = computed(() => !!import.meta.env.VITE_OIDC_AUTH_URL)
const oidcURL = computed(() => (import.meta.env.VITE_OIDC_AUTH_URL as string) || '')

async function login() {
  loading.value = true
  error.value = ''
  try {
    setAuthToken(token.value)
    localStorage.setItem('auth_token', token.value)
    await api.health()
    router.push('/')
  } catch (e: any) {
    error.value = e.message || 'Authentication failed'
    setAuthToken('')
    localStorage.removeItem('auth_token')
  } finally {
    loading.value = false
  }
}

function oidcLogin() {
  if (oidcURL.value) {
    window.location.href = oidcURL.value
  }
}
</script>

<template>
  <div class="login-page">
    <div class="card login-card">
      <h1 class="login-title">MultiAgentCom</h1>
      <p class="login-subtitle">Multi-Agent Development Platform</p>

      <form @submit.prevent="login" class="login-form">
        <div class="form-group">
          <label class="form-label">API Token</label>
          <input v-model="token" type="password" class="form-input" placeholder="Enter your API token" />
        </div>
        <button type="submit" class="btn btn-primary login-btn" :disabled="loading">
          <span v-if="loading" class="spinner"></span>
          {{ loading ? 'Signing in...' : 'Sign In' }}
        </button>
        <p v-if="error" class="login-error">{{ error }}</p>
      </form>

      <div v-if="showOIDC" class="login-divider">
        <span>or</span>
      </div>
      <button v-if="showOIDC" class="btn oidc-btn" @click="oidcLogin">
        Sign in with SSO
      </button>
    </div>
  </div>
</template>

<style scoped>
.login-page { display: flex; align-items: center; justify-content: center; min-height: 100vh; }
.login-card { width: 100%; max-width: 400px; }
.login-title { font-size: 28px; font-weight: 700; margin-bottom: 4px; }
.login-subtitle { color: var(--text-secondary); margin-bottom: 28px; }
.login-form { display: flex; flex-direction: column; gap: 12px; }
.login-btn { width: 100%; justify-content: center; padding: 10px; }
.login-error { color: var(--danger); font-size: 13px; margin-top: 4px; }
.login-divider { text-align: center; color: var(--text-secondary); margin: 20px 0; position: relative; }
.login-divider::before, .login-divider::after { content: ''; position: absolute; top: 50%; width: 40%; height: 1px; background: var(--border); }
.login-divider::before { left: 0; }
.login-divider::after { right: 0; }
.oidc-btn { width: 100%; justify-content: center; }
</style>
