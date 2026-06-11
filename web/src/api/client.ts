const API_BASE = import.meta.env.VITE_API_BASE || ''

let authToken = ''

export function setAuthToken(token: string) {
  authToken = token
}

export function getAuthToken(): string {
  return authToken
}

export async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string> || {}),
  }
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }
  const res = await fetch(`${API_BASE}${path}`, { ...options, headers })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new ApiError(res.status, body.code || 'UNKNOWN', body.message || res.statusText)
  }
  return res.json()
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

// ── Project API ─────────────────────────────────────
export interface Project {
  id: string
  name: string
  description?: string
  createdAt: string
  updatedAt: string
}

export interface Requirement {
  id: string
  projectId: string
  title: string
  content: string
  constraints?: string[]
  acceptanceHints?: string[]
  createdAt: string
}

export interface Task {
  id: string
  projectId: string
  planId: string
  name: string
  type: string
  status: 'CREATED' | 'IN_PROGRESS' | 'HUMAN_OVERRIDE' | 'DONE' | 'FAILED'
  assigneeAgent: string
  dependsOn?: string[]
  inputRef?: string
  outputRef?: string
  createdAt: string
  updatedAt: string
}

export interface AgentRun {
  id: string
  projectId: string
  taskId: string
  agentType: string
  model: string
  status: 'PENDING' | 'RUNNING' | 'SUCCEEDED' | 'FAILED'
  promptTokens?: number
  completionTokens?: number
  totalTokens?: number
  estimatedCostUsd?: number
  resultSummary?: string
  error?: string
  startedAt: string
  endedAt?: string
}

export interface Contract {
  id: string
  projectId: string
  version: number
  name: string
  summary: string
  endpoints: any[]
  schemas: any[]
  createdAt: string
}

export interface Snapshot {
  id: string
  projectId: string
  branch: string
  reason: string
  stable: boolean
  createdAt: string
}

export interface HumanOverride {
  id: string
  projectId: string
  taskId: string
  operator: string
  instruction: string
  lockScope: string
  createdAt: string
  appliedAt?: string
}

export interface ConflictEntry {
  id: string
  projectId: string
  kind: string
  scope: string
  reason: string
  status: string
  createdAt: string
}

export interface AlertEntry {
  id: string
  projectId: string
  severity: string
  type: string
  message: string
  timestamp: string
}

export interface AuditEntry {
  id: string
  projectId: string
  actor: string
  action: string
  resourceType: string
  resourceId: string
  summary: string
  timestamp: string
}

export interface CommunicationEntry {
  id: string
  projectId: string
  from: string
  to: string
  type: string
  taskId: string
  payloadRef?: string
  timestamp: string
}

export interface TokenCostPoint {
  runId: string
  taskId: string
  agentType: string
  promptTokens: number
  completionTokens: number
  totalTokens: number
  estimatedCostUsd: number
  timestamp: string
}

export interface StatusMatrixView {
  agents: StatusMatrixAgent[]
  taskCounts: Record<string, number>
}

export interface StatusMatrixAgent {
  agent: string
  status: string
  createdTasks: number
  runningTasks: number
  humanOverrideTasks: number
  doneTasks: number
  failedTasks: number
  totalTasks: number
}

// ── API Methods ─────────────────────────────────────
export const api = {
  // Projects
  createProject: (input: { name: string; description?: string }) =>
    apiFetch<Project>('/projects', { method: 'POST', body: JSON.stringify(input) }),
  listProjects: () =>
    apiFetch<Project[]>('/projects'),
  getProject: (id: string) =>
    apiFetch<Project>(`/projects/${id}`),

  // Requirements
  addRequirement: (projectId: string, input: { title: string; content: string; constraints?: string[]; acceptanceHints?: string[] }) =>
    apiFetch<Requirement>(`/projects/${projectId}/requirements`, { method: 'POST', body: JSON.stringify(input) }),
  listRequirements: (projectId: string) =>
    apiFetch<Requirement[]>(`/projects/${projectId}/requirements`),

  // Plans
  generatePlan: (projectId: string) =>
    apiFetch<{ plan: any; task: Task }>(`/projects/${projectId}/plan`, { method: 'POST' }),

  // Contracts
  generateContract: (projectId: string) =>
    apiFetch<Contract>(`/projects/${projectId}/contracts/generate`, { method: 'POST' }),
  listContracts: (projectId: string) =>
    apiFetch<Contract[]>(`/projects/${projectId}/contracts`),

  // Tasks
  listTasks: (projectId: string) =>
    apiFetch<Task[]>(`/projects/${projectId}/tasks`),
  dispatchTasks: (projectId: string) =>
    apiFetch<{ contract: Contract; tasks: Task[] }>(`/projects/${projectId}/tasks/dispatch`, { method: 'POST' }),

  // Runs
  startRun: (projectId: string, taskId: string) =>
    apiFetch<{ task: Task; run: AgentRun }>(`/projects/${projectId}/tasks/run`, { method: 'POST', body: JSON.stringify({ taskId }) }),
  getRunStatus: (projectId: string, runId: string) =>
    apiFetch<{ run: AgentRun; task: Task; artifacts: any[] }>(`/projects/${projectId}/runs/${runId}/status`),
  retryTask: (projectId: string, taskId: string) =>
    apiFetch<Task>(`/projects/${projectId}/tasks/${taskId}/retry`, { method: 'POST' }),

  // HITL
  applyOverride: (projectId: string, input: { taskId: string; instruction: string; operator?: string; lockScope?: string }) =>
    apiFetch<any>(`/projects/${projectId}/overrides`, { method: 'POST', body: JSON.stringify(input) }),
  applyCodeLock: (projectId: string, input: { path: string; content: string; createdBy: string; lockMode?: string }) =>
    apiFetch<any>(`/projects/${projectId}/locks`, { method: 'POST', body: JSON.stringify(input) }),
  listConflicts: (projectId: string) =>
    apiFetch<{ items: ConflictEntry[]; count: number }>(`/projects/${projectId}/conflicts`),
  resolveConflict: (projectId: string, conflictId: string, input: { resolution: string; note?: string }) =>
    apiFetch<ConflictEntry>(`/projects/${projectId}/conflicts/${conflictId}/resolve`, { method: 'POST', body: JSON.stringify(input) }),

  // Observability
  getStatusMatrix: (projectId?: string) =>
    apiFetch<StatusMatrixView>(`/status/matrix${projectId ? `?projectId=${projectId}` : ''}`),
  listAlerts: (projectId: string) =>
    apiFetch<{ items: AlertEntry[]; count: number }>(`/projects/${projectId}/alerts`),
  listAuditLogs: (projectId: string) =>
    apiFetch<{ items: AuditEntry[]; count: number }>(`/projects/${projectId}/audit-logs`),
  listCommunications: (projectId: string) =>
    apiFetch<{ items: CommunicationEntry[]; count: number }>(`/projects/${projectId}/communications`),
  getTokenCosts: (projectId: string) =>
    apiFetch<{ points: TokenCostPoint[]; totalTokens: number; estimatedCostUsd: number }>(`/projects/${projectId}/token-costs`),
  listSandboxes: (projectId: string) =>
    apiFetch<any>(`/projects/${projectId}/sandboxes`),
  listSnapshots: (projectId: string) =>
    apiFetch<Snapshot[]>(`/projects/${projectId}/snapshots`),

  // Delivery
  exportDelivery: (projectId: string, runId: string) =>
    apiFetch<any>(`/projects/${projectId}/delivery/export`, { method: 'POST', body: JSON.stringify({ runId }) }),

  // Health
  health: () => apiFetch<{ status: string; service: string; timestamp: string }>('/health'),
  ready: () => apiFetch<{ status: string; service: string } & any>('/ready'),

  // LLM Providers
  listLLMProviders: () => apiFetch<{ activeProvider: string; providers: LLMProvider[] }>('/llm/providers'),

  // Admin Config
  getAdminConfig: () => apiFetch<AdminConfig>('/admin/config'),
  updateAdminConfig: (input: AdminConfigUpdate) =>
    apiFetch<{ code: string; message: string }>('/admin/config', { method: 'PUT', body: JSON.stringify(input) }),
}

export interface AdminConfig {
  runtime: {
    provider: string
    timeout: string
    claude: { apiKey: string; model: string; baseURL: string; maxTokens: number }
    openai: { apiKey: string; model: string; baseURL: string; maxTokens: number; format: string }
    gemini: { apiKey: string; model: string; baseURL: string; maxTokens: number }
    http: { endpoint: string; bearerToken: string; maxAttempts: number; retryDelay: string }
  }
  token: {
    promptPricePerMillion: number
    outputPricePerMillion: number
    budgetWarnUSD: number
    budgetBlockUSD: number
  }
  s3: {
    provider: string
    endpoint: string
    accessKey: string
    secretKey: string
    bucket: string
    region: string
    useSSL: boolean
  }
  alert: { webhookURL: string }
  oidc: {
    issuer: string
    clientID: string
    clientSecret: string
    redirectURL: string
  }
}

export interface AdminConfigUpdate {
  runtime?: {
    provider?: string
    claude?: { apiKey?: string; model?: string; baseURL?: string; maxTokens?: number }
    openai?: { apiKey?: string; model?: string; baseURL?: string; maxTokens?: number; format?: string }
    gemini?: { apiKey?: string; model?: string; baseURL?: string; maxTokens?: number }
    http?: { endpoint?: string; bearerToken?: string; maxAttempts?: number }
  }
  token?: {
    promptPricePerMillion?: number
    outputPricePerMillion?: number
    budgetWarnUSD?: number
    budgetBlockUSD?: number
  }
  s3?: {
    provider?: string
    endpoint?: string
    accessKey?: string
    secretKey?: string
    bucket?: string
    region?: string
    useSSL?: boolean
  }
  alert?: { webhookURL?: string }
  oidc?: {
    issuer?: string
    clientID?: string
    clientSecret?: string
    redirectURL?: string
  }
}

export interface LLMProvider {
  id: string
  name: string
  available: boolean
  active: boolean
  model?: string
}
