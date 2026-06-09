<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Project } from '@/api/client'

const router = useRouter()
const projects = ref<Project[]>([])
const loading = ref(true)
const showCreate = ref(false)
const newName = ref('')
const newDesc = ref('')
const creating = ref(false)

async function loadProjects() {
  // Since we don't have a list projects endpoint, use status matrix to infer
  // In practice, we'd add a list endpoint; for now we'll attempt direct creation
  loading.value = false
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
    alert('Failed to create project: ' + e.message)
  } finally {
    creating.value = false
  }
}

onMounted(loadProjects)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Projects</h1>
      <button class="btn btn-primary" @click="showCreate = true">New Project</button>
    </div>

    <div v-if="showCreate" class="card" style="margin-bottom: 24px;">
      <div class="card-title">Create Project</div>
      <div class="form-group">
        <label class="form-label">Name</label>
        <input v-model="newName" class="form-input" placeholder="My Project" />
      </div>
      <div class="form-group">
        <label class="form-label">Description</label>
        <textarea v-model="newDesc" class="form-textarea" placeholder="Optional description..."></textarea>
      </div>
      <div style="display: flex; gap: 8px;">
        <button class="btn btn-primary" @click="createProject" :disabled="creating">
          {{ creating ? 'Creating...' : 'Create' }}
        </button>
        <button class="btn" @click="showCreate = false">Cancel</button>
      </div>
    </div>

    <div v-if="projects.length === 0 && !loading" class="card" style="text-align: center; padding: 40px;">
      <p style="color: var(--text-secondary);">No projects yet. Create your first project to start.</p>
    </div>

    <div v-else class="grid grid-3">
      <div v-for="project in projects" :key="project.id" class="card" style="cursor: pointer;" @click="router.push(`/projects/${project.id}`)">
        <div class="card-title">{{ project.name }}</div>
        <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 12px;">{{ project.description || 'No description' }}</p>
        <div style="font-size: 12px; color: var(--text-secondary);">{{ project.id }}</div>
      </div>
    </div>
  </div>
</template>
