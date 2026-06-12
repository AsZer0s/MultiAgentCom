<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuth } from '@/composables/useAuth'
import { useI18n } from '@/i18n'

const router = useRouter()
const { isLoggedIn, actor, logout: authLogout } = useAuth()
const { t, locale, setLocale } = useI18n()

function logout() {
  authLogout()
  router.push('/login')
}

function toggleLocale() {
  const next = locale.value === 'zh' ? 'en' : 'zh'
  setLocale(next)
}
</script>

<template>
  <nav class="app-nav">
    <div class="nav-brand">
      <router-link to="/">
        <span class="brand-glyph">⬡</span> MultiAgentCom
      </router-link>
    </div>
    <div class="nav-links">
      <router-link to="/">{{ t('nav.dashboard') }}</router-link>
      <router-link v-if="isLoggedIn" to="/projects">{{ t('nav.projects') }}</router-link>
      <router-link v-if="isLoggedIn" to="/settings">{{ t('nav.settings') }}</router-link>
    </div>
    <div class="nav-auth">
      <button class="btn btn-sm btn-ghost" @click="toggleLocale">
        {{ locale === 'zh' ? 'EN' : '中' }}
      </button>
      <template v-if="isLoggedIn">
        <span class="nav-user">◆ {{ actor }}</span>
        <button class="btn btn-sm" @click="logout">{{ t('nav.logout') }}</button>
      </template>
      <router-link v-else to="/login" class="btn btn-sm btn-primary">{{ t('nav.login') }}</router-link>
    </div>
  </nav>
</template>

<style scoped>
.brand-glyph {
  color: var(--cyan);
  font-size: 18px;
  margin-right: 4px;
}
</style>
