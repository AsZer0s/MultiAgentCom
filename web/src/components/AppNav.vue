<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useAuth } from '@/composables/useAuth'

const router = useRouter()
const { isLoggedIn, actor, logout: authLogout } = useAuth()

function logout() {
  authLogout()
  router.push('/login')
}
</script>

<template>
  <nav class="app-nav">
    <div class="nav-brand">
      <router-link to="/">MultiAgentCom</router-link>
    </div>
    <div class="nav-links">
      <router-link to="/">Dashboard</router-link>
      <router-link v-if="isLoggedIn" to="/projects">Projects</router-link>
      <router-link v-if="isLoggedIn" to="/settings">Settings</router-link>
    </div>
    <div class="nav-auth">
      <template v-if="isLoggedIn">
        <span class="nav-user">{{ actor }}</span>
        <button class="btn btn-sm" @click="logout">Logout</button>
      </template>
      <router-link v-else to="/login" class="btn btn-sm">Login</router-link>
    </div>
  </nav>
</template>

<style scoped>
.nav-user {
  font-size: 13px;
  color: var(--text-secondary);
  margin-right: 8px;
}
</style>
