<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Project } from '@/api/client'
import { useI18n } from '@/i18n'

const router = useRouter()
const { t } = useI18n()
const projects = ref<Project[]>([])
const loading = ref(true)
const showCreate = ref(false)
const newName = ref('')
const newDesc = ref('')
const creating = ref(false)

async function loadProjects() {
  try {
    loading.value = true
    projects.value = await api.listProjects()
  } catch (e: any) {
    console.error('Failed to load projects:', e.message)
  } finally {
    loading.value = false
  }
}

async function createProject() {
  if (!newName.value.trim()) return
  creating.value = true
  try {
    const project = await api.createProject({ name: newName.value, description: newDesc.value || undefined })
    projects.value.push(project)
    showCreate.value = false
    newName.value = ''
    newDesc.value = ''
    router.push(`/projects/${project.id}`)
  } catch (e: any) {
    alert(t('projects.createFailed') + ': ' + e.message)
  } finally {
    creating.value = false
  }
}

onMounted(loadProjects)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">{{ t('projects.title') }}</h1>
      <button class="btn btn-primary" @click="showCreate = true">{{ t('projects.newProject') }}</button>
    </div>

    <div v-if="showCreate" class="card" style="margin-bottom: 24px;">
      <div class="card-title">{{ t('projects.createProject') }}</div>
      <div class="form-group">
        <label class="form-label">{{ t('projects.projectName') }}</label>
        <input v-model="newName" class="form-input" :placeholder="t('projects.projectNamePlaceholder')" />
      </div>
      <div class="form-group">
        <label class="form-label">{{ t('projects.projectDescription') }}</label>
        <textarea v-model="newDesc" class="form-textarea" :placeholder="t('projects.projectDescriptionPlaceholder')"></textarea>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="btn btn-primary" @click="createProject" :disabled="creating">
          {{ creating ? t('projects.creating') : t('common.create') }}
        </button>
        <button class="btn" @click="showCreate = false">{{ t('common.cancel') }}</button>
      </div>
    </div>

    <div v-if="projects.length === 0 && !loading" class="card" style="text-align: center; padding: 40px;">
      <p style="color: var(--text-secondary);">{{ t('projects.noProjects') }}</p>
    </div>

    <div v-else class="grid grid-3">
      <div v-for="project in projects" :key="project.id" class="card" style="cursor: pointer;" @click="router.push(`/projects/${project.id}`)">
        <div class="card-title">{{ project.name }}</div>
        <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 12px;">{{ project.description || t('common.noData') }}</p>
        <div style="font-size: 12px; color: var(--text-secondary);">{{ project.id }}</div>
      </div>
    </div>
  </div>
</template>
