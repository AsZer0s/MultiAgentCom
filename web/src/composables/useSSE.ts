import { ref, onMounted, onUnmounted } from 'vue'

const SSE_BASE = import.meta.env.VITE_SSE_BASE || ''

export function useSSE(path: string) {
  const data = ref<any>(null)
  const connected = ref(false)
  const error = ref<string | null>(null)
  let eventSource: EventSource | null = null
  let reconnectTimer: number | null = null

  function connect() {
    const token = localStorage.getItem('auth_token') || ''
    let url = `${SSE_BASE}${path}`
    if (token) {
      url += (url.includes('?') ? '&' : '?') + `token=${encodeURIComponent(token)}`
    }

    eventSource = new EventSource(url)

    eventSource.onopen = () => {
      connected.value = true
      error.value = null
    }

    eventSource.addEventListener('status', (e: MessageEvent) => {
      try {
        data.value = JSON.parse(e.data)
      } catch {
        data.value = e.data
      }
    })

    eventSource.onerror = () => {
      connected.value = false
      error.value = 'SSE connection lost, reconnecting...'
      eventSource?.close()
      reconnectTimer = window.setTimeout(connect, 3000)
    }
  }

  onMounted(connect)

  onUnmounted(() => {
    eventSource?.close()
    if (reconnectTimer != null) {
      clearTimeout(reconnectTimer)
    }
  })

  return { data, connected, error, reconnect: connect }
}
