<script setup lang="ts">
import { ref, onMounted, watch, computed } from 'vue'
import { api, type Task } from '@/api/client'
import { useSSE } from '@/composables/useSSE'

const props = defineProps<{ id: string }>()

const tasks = ref<Task[]>([])
const initialLoading = ref(true)
const runningTask = ref<string | null>(null)
const { connected, data: sseData } = useSSE('/status/stream')

const columns = [
  { key: 'CREATED', label: 'Created' },
  { key: 'IN_PROGRESS', label: 'In Progress' },
  { key: 'HUMAN_OVERRIDE', label: 'Human Override' },
  { key: 'DONE', label: 'Done' },
  { key: 'FAILED', label: 'Failed' },
]

const tasksByStatus = computed(() => {
  const map: Record<string, Task[]> = {}
  for (const col of columns) {
    map[col.key] = []
  }
  for (const t of tasks.value) {
    if (map[t.status]) {
      map[t.status].push(t)
    }
  }
  return map
})

// Silent background refresh - no loading indicator
async function silentRefresh() {
  try {
    const fresh = await api.listTasks(props.id)
    tasks.value = fresh
  } catch {}
}

// Initial load with loading indicator
async function initialLoad() {
  initialLoading.value = true
  try {
    tasks.value = await api.listTasks(props.id)
  } catch {} finally {
    initialLoading.value = false
  }
}

async function startRun(taskId: string) {
  runningTask.value = taskId
  try {
    await api.startRun(props.id, taskId)
    // Silent refresh after a short delay
    setTimeout(silentRefresh, 1500)
  } catch (e: any) {
    alert('Failed: ' + e.message)
  } finally {
    runningTask.value = null
  }
}

async function retryTask(taskId: string) {
  try {
    await api.retryTask(props.id, taskId)
    setTimeout(silentRefresh, 1000)
  } catch (e: any) {
    alert('Failed: ' + e.message)
  }
}

// Silent auto-refresh on SSE events - no loading state
watch(sseData, () => {
  silentRefresh()
})

watch(() => props.id, initialLoad)
onMounted(initialLoad)
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
        <router-link :to="`/projects/${id}`" class="btn btn-sm">Back</router-link>
      </div>
    </div>

    <div v-if="initialLoading" style="text-align: center; padding: 40px; color: var(--text-secondary);">
      <span class="spinner"></span> Loading...
    </div>

    <div v-else class="board">
      <div v-for="col in columns" :key="col.key" class="board-column">
        <div class="board-column-title">
          {{ col.label }} ({{ tasksByStatus[col.key].length }})
        </div>
        <TransitionGroup name="task-card" tag="div">
          <div v-for="task in tasksByStatus[col.key]" :key="task.id" class="board-card">
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
        </TransitionGroup>
        <div v-if="tasksByStatus[col.key].length === 0" class="board-empty">
          No tasks
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.board-empty {
  font-size: 12px;
  color: var(--text-secondary);
  text-align: center;
  padding: 20px;
}

/* Smooth transitions for task cards */
.task-card-enter-active {
  transition: all 0.3s ease-out;
}

.task-card-leave-active {
  transition: all 0.2s ease-in;
}

.task-card-enter-from {
  opacity: 0;
  transform: translateY(-10px);
}

.task-card-leave-to {
  opacity: 0;
  transform: scale(0.95);
}

.task-card-move {
  transition: transform 0.3s ease;
}
</style>
