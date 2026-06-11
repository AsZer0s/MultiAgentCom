import { ref, computed } from 'vue'
import { setAuthToken, getAuthToken } from '@/api/client'

const token = ref(localStorage.getItem('auth_token') || '')
const actor = ref(localStorage.getItem('auth_actor') || '')

// Initialize auth state from localStorage
if (token.value) {
  setAuthToken(token.value)
}

export function useAuth() {
  const isLoggedIn = computed(() => !!token.value)

  function login(newToken: string, actorName?: string) {
    token.value = newToken
    actor.value = actorName || 'user'
    setAuthToken(newToken)
    localStorage.setItem('auth_token', newToken)
    localStorage.setItem('auth_actor', actor.value)
  }

  function logout() {
    token.value = ''
    actor.value = ''
    setAuthToken('')
    localStorage.removeItem('auth_token')
    localStorage.removeItem('auth_actor')
  }

  function getToken() {
    return token.value
  }

  return {
    token,
    actor,
    isLoggedIn,
    login,
    logout,
    getToken,
  }
}
