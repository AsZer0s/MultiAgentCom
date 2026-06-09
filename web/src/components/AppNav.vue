<script setup lang="ts">
import { useRouter } from 'vue-router'
import { getAuthToken, setAuthToken } from '@/api/client'

const router = useRouter()
const isLoggedIn = () => !!getAuthToken()

function logout() {
  setAuthToken('')
  localStorage.removeItem('auth_token')
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
      <router-link v-if="isLoggedIn()" to="/projects">Projects</router-link>
      <router-link v-if="isLoggedIn()" to="/settings">Settings</router-link>
    </div>
    <div class="nav-auth">
      <button v-if="isLoggedIn()" class="btn btn-sm" @click="logout">Logout</button>
      <router-link v-else to="/login" class="btn btn-sm">Login</router-link>
    </div>
  </nav>
</template>
