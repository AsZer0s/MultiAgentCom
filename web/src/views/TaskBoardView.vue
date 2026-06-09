<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { api, type Task, type AgentRun } from '@/api/client'
import { useSSE } from '@/composables/useSSE'

const props = defineProps<{ id: string }>()

const tasks = ref<Task[]>([])
const loading = ref(true)
const runningTask = ref<string | null>(null)
const { connected, data: sseData } = useSSE('/status/stream')

const columns = [
  { key: 'CREATED', label: 'Created' },
  { key: 'IN_PROGRESS', label: 'In Progress' },
  { key: 'HUMAN_OVERRIDE', label: 'Human Override' },
  { key: 'DONE', label: 'Done' },
  { key: 'FAILED', label: 'Failed' },
]

function tasksForStatus(status: string): Task[] {
  return tasks.value.filter(t => t.status === status)
}

async function load() {
  loading.value = true
  try {
    tasks.value = await api.listTasks(props.id)
  } catch {} finally { loading.value = false }
}

async function startRun(taskId: string) {
  runningTask.value = taskId
  try {
    await api.startRun(props.id, taskId)
    setTimeout(load, 2000)
  } catch (e: any) {
    alert('Failed: ' + e.message)
  } finally {
    runningTask.value = null
  }
}

async function retryTask(taskId: string) {
  try {
    await api.retryTask(props.id, taskId)
    setTimeout(load, 1000)
  } catch (e: any) {
    alert('Failed: ' + e.message)
  }
}

// Auto-refresh on SSE events
watch(sseData, () => { load() })

watch(() => props.id, load)
onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Task Board</h1>
      <div style="display: flex; gap: 8px; align-items: center;">
        <div class="sse-indicator">
          <span class="sse-dot" :class="connected ? 'connected' : 'disconnected'"></span>
          {{ connected ? 'Live' : 'Offline' }}
        </div>
        <button class="btn btn-sm" @click="load">Refresh</button>
        <router-link :to="`/projects/${id}`" class="btn btn-sm">Back</router-link>
      </div>
    </div>

    <div v-if="loading" style="text-align: center; padding: 40px; color: var(--text-secondary);">
      <span class="spinner"></span> Loading...
    </div>

    <div v-else class="board">
      <div v-for="col in columns" :key="col.key" class="board-column">
        <div class="board-column-title">
          {{ col.label }} ({{ tasksForStatus(col.key).length }})
        </div>
        <div v-for="task in tasksForStatus(col.key)" :key="task.id" class="board-card">
          <div class="board-card-title">{{ task.name }}</div>
          <div class="board-card-meta">
            {{ task.assigneeAgent }} · {{ task.type }}
          </div>
          <div style="display: flex; gap: 4px; margin-top: 8px;">
            <button v-if="task.status === 'CREATED' || task.status === 'FAILED'" class="btn btn-sm btn-primary"
              @click="startRun(task.id)" :disabled="runningTask === task.id">
              {{ runningTask === task.id ? 'Starting...' : 'Run' }}
            </button>
            <button v-if="task.status === 'FAILED'" class="btn btn-sm" @click="retryTask(task.id)">
              Retry
            </button>
            <router-link v-if="task.status === 'HUMAN_OVERRIDE'" :to="`/projects/${id}/hitl`" class="btn btn-sm">
              Handle
            </router-link>
          </div>
        </div>
        <div v-if="tasksForStatus(col.key).length === 0" style="font-size: 12px; color: var(--text-secondary); text-align: center; padding: 20px;">
          No tasks
        </div>
      </div>
    </div>
  </div>
</template>
