<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { api, type ConflictEntry, type Task } from '@/api/client'
import { useI18n } from '@/i18n'

const props = defineProps<{ id: string }>()
const { t } = useI18n()

const tasks = ref<Task[]>([])
const conflicts = ref<ConflictEntry[]>([])
const loading = ref(true)

// Override form
const overrideTaskId = ref('')
const overrideInstruction = ref('')
const overrideOperator = ref('')
const overrideScope = ref('TASK')
const applyingOverride = ref(false)

// Lock form
const lockPath = ref('')
const lockContent = ref('')
const lockCreatedBy = ref('')
const applyingLock = ref(false)

// Conflict resolution
const resolutionNote = ref('')
const resolvingId = ref('')

async function load() {
  loading.value = true
  try {
    tasks.value = await api.listTasks(props.id)
    const result = await api.listConflicts(props.id)
    conflicts.value = result.items || []
  } catch {} finally { loading.value = false }
}

async function applyOverride() {
  if (!overrideTaskId.value || !overrideInstruction.value) return
  applyingOverride.value = true
  try {
    await api.applyOverride(props.id, {
      taskId: overrideTaskId.value,
      instruction: overrideInstruction.value,
      operator: overrideOperator.value || undefined,
      lockScope: overrideScope.value,
    })
    overrideInstruction.value = ''
    overrideOperator.value = ''
    await load()
  } catch (e: any) {
    alert(t('hitl.overrideFailed') + ': ' + e.message)
  } finally { applyingOverride.value = false }
}

async function applyCodeLock() {
  if (!lockPath.value || !lockCreatedBy.value) return
  applyingLock.value = true
  try {
    await api.applyCodeLock(props.id, {
      path: lockPath.value,
      content: lockContent.value,
      createdBy: lockCreatedBy.value,
    })
    lockPath.value = ''
    lockContent.value = ''
    lockCreatedBy.value = ''
  } catch (e: any) {
    alert(t('hitl.codeLockFailed') + ': ' + e.message)
  } finally { applyingLock.value = false }
}

async function resolveConflict(conflictId: string) {
  resolvingId.value = conflictId
  try {
    await api.resolveConflict(props.id, conflictId, {
      resolution: 'RESOLVED',
      note: resolutionNote.value || undefined,
    })
    resolutionNote.value = ''
    await load()
  } catch (e: any) {
    alert(t('hitl.resolveFailed') + ': ' + e.message)
  } finally { resolvingId.value = '' }
}

watch(() => props.id, load)
onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">{{ t('hitl.title') }}</h1>
      <router-link :to="`/projects/${id}`" class="btn btn-sm">{{ t('common.back') }}</router-link>
    </div>

    <!-- Override section -->
    <div class="card" style="margin-bottom: 20px;">
      <div class="card-title">{{ t('hitl.override') }}</div>
      <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
        {{ t('hitl.instructionPlaceholder') }}
      </p>
      <div class="grid grid-2">
        <div class="form-group">
          <label class="form-label">{{ t('common.name') }}</label>
          <select v-model="overrideTaskId" class="form-select">
            <option value="">{{ t('hitl.selectTask') }}</option>
            <option v-for="task in tasks.filter(t => t.status === 'IN_PROGRESS' || t.status === 'HUMAN_OVERRIDE')" :key="task.id" :value="task.id">
              {{ task.name }} ({{ task.status }})
            </option>
          </select>
        </div>
        <div class="form-group">
          <label class="form-label">{{ t('hitl.operator') }}</label>
          <input v-model="overrideOperator" class="form-input" :placeholder="t('hitl.operatorPlaceholder')" />
        </div>
      </div>
      <div class="form-group">
        <label class="form-label">{{ t('hitl.instruction') }}</label>
        <textarea v-model="overrideInstruction" class="form-textarea" :placeholder="t('hitl.instructionPlaceholder')"></textarea>
      </div>
      <div class="form-group">
        <label class="form-label">{{ t('hitl.lockScope') }}</label>
        <select v-model="overrideScope" class="form-select">
          <option value="TASK">{{ t('hitl.lockScopeTask') }}</option>
          <option value="FILE">{{ t('hitl.lockScopeFile') }}</option>
          <option value="MODULE">{{ t('hitl.lockScopeModule') }}</option>
        </select>
      </div>
      <button class="btn btn-primary" @click="applyOverride" :disabled="applyingOverride">
        {{ applyingOverride ? t('hitl.applying') : t('hitl.applyOverride') }}
      </button>
    </div>

    <!-- Code Lock section -->
    <div class="card" style="margin-bottom: 20px;">
      <div class="card-title">{{ t('hitl.codeLock') }}</div>
      <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
        {{ t('hitl.contentPlaceholder') }}
      </p>
      <div class="grid grid-2">
        <div class="form-group">
          <label class="form-label">{{ t('hitl.path') }}</label>
          <input v-model="lockPath" class="form-input" :placeholder="t('hitl.pathPlaceholder')" />
        </div>
        <div class="form-group">
          <label class="form-label">{{ t('hitl.createdBy') }}</label>
          <input v-model="lockCreatedBy" class="form-input" :placeholder="t('hitl.createdByPlaceholder')" />
        </div>
      </div>
      <div class="form-group">
        <label class="form-label">{{ t('hitl.content') }}</label>
        <textarea v-model="lockContent" class="form-textarea" :placeholder="t('hitl.contentPlaceholder')"></textarea>
      </div>
      <button class="btn btn-primary" @click="applyCodeLock" :disabled="applyingLock">
        {{ applyingLock ? t('common.loading') : t('hitl.applyCodeLock') }}
      </button>
    </div>

    <!-- Conflicts section -->
    <div class="card">
      <div class="card-title">{{ t('hitl.conflicts') }} ({{ conflicts.length }})</div>
      <div v-if="conflicts.length === 0" style="color: var(--text-secondary); font-size: 13px;">
        {{ t('hitl.noConflicts') }}
      </div>
      <div v-for="conflict in conflicts" :key="conflict.id" style="border: 1px solid var(--border); border-radius: 6px; padding: 12px; margin-bottom: 8px;">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
          <span class="pill pill-human_override">{{ conflict.status }}</span>
          <span style="font-size: 12px; color: var(--text-secondary);">{{ conflict.kind }}</span>
        </div>
        <p style="font-size: 13px; margin-bottom: 8px;">{{ conflict.reason }}</p>
        <div v-if="conflict.status !== 'RESOLVED'" style="display: flex; gap: 8px;">
          <input v-model="resolutionNote" class="form-input" :placeholder="t('hitl.resolutionPlaceholder')" style="flex: 1;" />
          <button class="btn btn-sm btn-primary" @click="resolveConflict(conflict.id)" :disabled="resolvingId === conflict.id">
            {{ resolvingId === conflict.id ? t('common.loading') : t('hitl.resolveConflict') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
