<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api, type Project, type Requirement, type Task, type Contract } from '@/api/client'
import { useSSE } from '@/composables/useSSE'
import { useI18n } from '@/i18n'

const props = defineProps<{ id: string }>()
const route = useRoute()
const { t } = useI18n()

const project = ref<Project | null>(null)
const requirements = ref<Requirement[]>([])
const tasks = ref<Task[]>([])
const contracts = ref<Contract[]>([])
const loading = ref(true)
const error = ref('')

const { connected } = useSSE('/status/stream')

// Requirement form
const reqTitle = ref('')
const reqContent = ref('')
const addingReq = ref(false)
const generatingPlan = ref(false)
const generatingContract = ref(false)
const dispatchingTasks = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    project.value = await api.getProject(props.id)
    requirements.value = await api.listRequirements(props.id)
    tasks.value = await api.listTasks(props.id)
    contracts.value = await api.listContracts(props.id)
  } catch (e: any) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function addRequirement() {
  if (!reqTitle.value.trim()) return
  addingReq.value = true
  try {
    const req = await api.addRequirement(props.id, {
      title: reqTitle.value,
      content: reqContent.value,
    })
    requirements.value.push(req)
    reqTitle.value = ''
    reqContent.value = ''
  } catch (e: any) {
    alert(t('projectDetail.addFailed') + ': ' + e.message)
  } finally {
    addingReq.value = false
  }
}

async function generatePlan() {
  generatingPlan.value = true
  try {
    await api.generatePlan(props.id)
    tasks.value = await api.listTasks(props.id)
  } catch (e: any) {
    alert(t('common.error') + ': ' + e.message)
  } finally {
    generatingPlan.value = false
  }
}

async function generateContract() {
  generatingContract.value = true
  try {
    await api.generateContract(props.id)
    contracts.value = await api.listContracts(props.id)
  } catch (e: any) {
    alert(t('common.error') + ': ' + e.message)
  } finally {
    generatingContract.value = false
  }
}

async function dispatchTasks() {
  dispatchingTasks.value = true
  try {
    await api.dispatchTasks(props.id)
    tasks.value = await api.listTasks(props.id)
  } catch (e: any) {
    alert(t('common.error') + ': ' + e.message)
  } finally {
    dispatchingTasks.value = false
  }
}

watch(() => props.id, load)
onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">{{ project?.name || t('common.loading') }}</h1>
      <div style="display: flex; gap: 8px; align-items: center;">
        <div class="sse-indicator">
          <span class="sse-dot" :class="connected ? 'connected' : 'disconnected'"></span>
          {{ connected ? t('common.live') : t('common.offline') }}
        </div>
        <router-link :to="`/projects/${id}/board`" class="btn btn-sm">{{ t('projectDetail.taskBoard') }}</router-link>
        <router-link :to="`/projects/${id}/hitl`" class="btn btn-sm">{{ t('projectDetail.hitl') }}</router-link>
      </div>
    </div>

    <div v-if="error" class="card"><p style="color: var(--danger)">{{ error }}</p></div>

    <!-- Requirements -->
    <div class="card" style="margin-bottom: 20px;">
      <div class="card-title">{{ t('projectDetail.requirementsCount', { count: requirements.length }) }}</div>
      <div class="form-group">
        <input v-model="reqTitle" class="form-input" :placeholder="t('projectDetail.requirementTitle')" />
      </div>
      <div class="form-group">
        <textarea v-model="reqContent" class="form-textarea" :placeholder="t('projectDetail.requirementContent')"></textarea>
      </div>
      <button class="btn btn-primary btn-sm" @click="addRequirement" :disabled="addingReq">
        {{ addingReq ? t('projectDetail.adding') : t('projectDetail.addRequirement') }}
      </button>

      <div v-if="requirements.length > 0" style="margin-top: 16px;">
        <div class="table-wrapper">
          <table>
            <thead><tr><th>{{ t('common.name') }}</th><th>{{ t('common.created') }}</th></tr></thead>
            <tbody>
              <tr v-for="req in requirements" :key="req.id">
                <td>{{ req.title }}</td>
                <td>{{ new Date(req.createdAt).toLocaleString() }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Actions -->
    <div class="card" style="margin-bottom: 20px;">
      <div class="card-title">{{ t('projectDetail.actions') }}</div>
      <div style="display: flex; gap: 8px; flex-wrap: wrap;">
        <button class="btn btn-sm" @click="generatePlan" :disabled="generatingPlan">
          {{ generatingPlan ? t('projectDetail.generatingPlan') : t('projectDetail.generatePlan') }}
        </button>
        <button class="btn btn-sm" @click="generateContract" :disabled="generatingContract">
          {{ generatingContract ? t('projectDetail.generatingContract') : t('projectDetail.generateContract') }}
        </button>
        <button class="btn btn-sm" @click="dispatchTasks" :disabled="dispatchingTasks">
          {{ dispatchingTasks ? t('projectDetail.dispatchingTasks') : t('projectDetail.dispatchTasks') }}
        </button>
      </div>
    </div>

    <!-- Tasks summary -->
    <div class="card" style="margin-bottom: 20px;">
      <div class="card-title">{{ t('projectDetail.tasksCount', { count: tasks.length }) }}</div>
      <div class="table-wrapper">
        <table>
          <thead><tr><th>{{ t('common.name') }}</th><th>{{ t('common.type') }}</th><th>{{ t('common.agent') }}</th><th>{{ t('common.status') }}</th></tr></thead>
          <tbody>
            <tr v-for="task in tasks" :key="task.id">
              <td>{{ task.name }}</td>
              <td>{{ task.type }}</td>
              <td>{{ task.assigneeAgent }}</td>
              <td><span class="pill" :class="`pill-${(task.status || '').toLowerCase()}`">{{ task.status || 'UNKNOWN' }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Contracts -->
    <div class="card">
      <div class="card-title">{{ t('projectDetail.contractsCount', { count: contracts.length }) }}</div>
      <div class="table-wrapper">
        <table>
          <thead><tr><th>{{ t('common.name') }}</th><th>{{ t('common.version') }}</th><th>{{ t('projectDetail.endpoints') }}</th><th>{{ t('common.created') }}</th></tr></thead>
          <tbody>
            <tr v-for="c in contracts" :key="c.id">
              <td>{{ c.name }}</td>
              <td>v{{ c.version }}</td>
              <td>{{ c.endpoints?.length || 0 }}</td>
              <td>{{ new Date(c.createdAt).toLocaleString() }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
