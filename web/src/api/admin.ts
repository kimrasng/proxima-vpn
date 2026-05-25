import { get, post, put, del, setClientTokenType } from './client';
import type {
  Node,
  NodeMetricsEntry,
  GenerateTokenResponse,
  NodeGroup,
  NodeGroupDetail,
  CreateNodeGroupRequest,
  UpdateNodeGroupRequest,
  Plan,
  CreatePlanRequest,
  UpdatePlanRequest,
  User,
  UserDetail,
  PaginatedUsers,
  UpdateUserRequest,
  CreateUserRequest,
  CreateUserResponse,
  UpdateNodeRequest,
  ListUsersParams,
  PlanRequest,
  DashboardStats,
  OnlineUser,
  LoginRequest,
  LoginResponse,
  TwoFASetup,
  TwoFAEnableRequest,
  TwoFADisableRequest,
  TwoFAStatus,
  Announcement,
  Setting,
  Inbound,
  CreateInboundRequest,
  UserTemplate,
  CreateUserTemplateRequest,
  TrafficHistoryEntry,
  NodeTLSStatus,
  IssueCertificateRequest,
  XrayVersionResponse,
  UpdateXrayRequest,
  BackupListResponse,
  TriggerBackupResponse,
} from './types';

function applyAdminClient() {
  setClientTokenType('admin');
}

export function adminLogin(req: LoginRequest): Promise<LoginResponse> {
  applyAdminClient();
  return post<LoginResponse>('/api/v1/admin/auth/login', req);
}

export function admin2FAStatus(): Promise<TwoFAStatus> {
  applyAdminClient();
  return get<TwoFAStatus>('/api/v1/admin/auth/2fa/status');
}

export function admin2FASetup(): Promise<TwoFASetup> {
  applyAdminClient();
  return get<TwoFASetup>('/api/v1/admin/auth/2fa/setup');
}

export function admin2FAEnable(req: TwoFAEnableRequest): Promise<void> {
  applyAdminClient();
  return post<void>('/api/v1/admin/auth/2fa/enable', req);
}

export function admin2FADisable(req: TwoFADisableRequest): Promise<void> {
  applyAdminClient();
  return post<void>('/api/v1/admin/auth/2fa/disable', req);
}

export function listNodes(): Promise<Node[]> {
  applyAdminClient();
  return get<Node[]>('/api/v1/admin/nodes');
}

export function getNode(id: string): Promise<Node> {
  applyAdminClient();
  return get<Node>(`/api/v1/admin/nodes/${id}`);
}

export function getNodeMetrics(id: string, hours = 24): Promise<NodeMetricsEntry[]> {
  applyAdminClient();
  return get<NodeMetricsEntry[]>(`/api/v1/admin/nodes/${id}/metrics?hours=${hours}`);
}

export function deleteNode(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/nodes/${id}`);
}

export function generateNodeToken(): Promise<GenerateTokenResponse> {
  applyAdminClient();
  return post<GenerateTokenResponse>('/api/v1/admin/nodes/token');
}

export function listNodeGroups(): Promise<NodeGroup[]> {
  applyAdminClient();
  return get<NodeGroup[]>('/api/v1/admin/node-groups');
}

export function getNodeGroup(id: string): Promise<NodeGroupDetail> {
  applyAdminClient();
  return get<NodeGroupDetail>(`/api/v1/admin/node-groups/${id}`);
}

export function createNodeGroup(req: CreateNodeGroupRequest): Promise<NodeGroup> {
  applyAdminClient();
  return post<NodeGroup>('/api/v1/admin/node-groups', req);
}

export function updateNodeGroup(id: string, req: UpdateNodeGroupRequest): Promise<NodeGroup> {
  applyAdminClient();
  return put<NodeGroup>(`/api/v1/admin/node-groups/${id}`, req);
}

export function deleteNodeGroup(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/node-groups/${id}`);
}

export function setNodeGroupNodes(id: string, nodeIds: string[]): Promise<void> {
  applyAdminClient();
  return put<void>(`/api/v1/admin/node-groups/${id}/nodes`, { node_ids: nodeIds });
}

export function listPlans(): Promise<Plan[]> {
  applyAdminClient();
  return get<Plan[]>('/api/v1/admin/plans');
}

export function getPlan(id: string): Promise<Plan> {
  applyAdminClient();
  return get<Plan>(`/api/v1/admin/plans/${id}`);
}

export function createPlan(req: CreatePlanRequest): Promise<Plan> {
  applyAdminClient();
  return post<Plan>('/api/v1/admin/plans', req);
}

export function updatePlan(id: string, req: UpdatePlanRequest): Promise<Plan> {
  applyAdminClient();
  return put<Plan>(`/api/v1/admin/plans/${id}`, req);
}

export function deletePlan(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/plans/${id}`);
}

export function listUsers(params?: ListUsersParams): Promise<PaginatedUsers> {
  applyAdminClient();
  const searchParams = new URLSearchParams();
  if (params?.page) searchParams.set('page', String(params.page));
  if (params?.limit) searchParams.set('limit', String(params.limit));
  if (params?.search) searchParams.set('search', params.search);
  if (params?.status) searchParams.set('status', params.status);
  const query = searchParams.toString();
  return get<PaginatedUsers>(`/api/v1/admin/users${query ? `?${query}` : ''}`);
}

export function getUser(id: string): Promise<UserDetail> {
  applyAdminClient();
  return get<UserDetail>(`/api/v1/admin/users/${id}`);
}

export function updateUser(id: string, req: UpdateUserRequest): Promise<User> {
  applyAdminClient();
  return put<User>(`/api/v1/admin/users/${id}`, req);
}

export function deleteUser(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/users/${id}`);
}

export function listPlanRequests(status?: string): Promise<PlanRequest[]> {
  applyAdminClient();
  const query = status ? `?status=${encodeURIComponent(status)}` : '';
  return get<PlanRequest[]>(`/api/v1/admin/plan-requests${query}`);
}

export function reviewPlanRequest(id: string, action: 'approve' | 'reject'): Promise<void> {
  applyAdminClient();
  return put<void>(`/api/v1/admin/plan-requests/${id}`, { action });
}

export function getDashboardStats(): Promise<DashboardStats> {
  applyAdminClient();
  return get<DashboardStats>('/api/v1/admin/stats');
}

export function getOnlineUsers(): Promise<OnlineUser[]> {
  applyAdminClient();
  return get<OnlineUser[]>('/api/v1/admin/online-users');
}

export function getTrafficHistory(): Promise<TrafficHistoryEntry[]> {
  applyAdminClient();
  return get<TrafficHistoryEntry[]>('/api/v1/admin/stats/traffic-history');
}

export function listAnnouncements(): Promise<Announcement[]> {
  applyAdminClient();
  return get<Announcement[]>('/api/v1/admin/announcements');
}

export function createAnnouncement(req: { title: string; content: string }): Promise<Announcement> {
  applyAdminClient();
  return post<Announcement>('/api/v1/admin/announcements', req);
}

export function updateAnnouncement(id: string, req: { title?: string; content?: string; is_active?: boolean }): Promise<Announcement> {
  applyAdminClient();
  return put<Announcement>(`/api/v1/admin/announcements/${id}`, req);
}

export function deleteAnnouncement(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/announcements/${id}`);
}

export function getSettings(): Promise<Setting[]> {
  applyAdminClient();
  return get<Setting[]>('/api/v1/admin/settings');
}

export function updateSettings(settings: Record<string, unknown>): Promise<void> {
  applyAdminClient();
  return put<void>('/api/v1/admin/settings', settings);
}

export function listInbounds(nodeId: string): Promise<Inbound[]> {
  applyAdminClient();
  return get<Inbound[]>(`/api/v1/admin/nodes/${nodeId}/inbounds`);
}

export function createInbound(nodeId: string, req: CreateInboundRequest): Promise<Inbound> {
  applyAdminClient();
  return post<Inbound>(`/api/v1/admin/nodes/${nodeId}/inbounds`, req);
}

export function toggleInbound(id: string): Promise<Inbound> {
  applyAdminClient();
  return put<Inbound>(`/api/v1/admin/inbounds/${id}/toggle`);
}

export function deleteInbound(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/inbounds/${id}`);
}

export function listUserTemplates(): Promise<UserTemplate[]> {
  applyAdminClient();
  return get<UserTemplate[]>('/api/v1/admin/user-templates');
}

export function createUserTemplate(req: CreateUserTemplateRequest): Promise<UserTemplate> {
  applyAdminClient();
  return post<UserTemplate>('/api/v1/admin/user-templates', req);
}

export function updateUserTemplate(id: string, req: Partial<CreateUserTemplateRequest>): Promise<UserTemplate> {
  applyAdminClient();
  return put<UserTemplate>(`/api/v1/admin/user-templates/${id}`, req);
}

export function deleteUserTemplate(id: string): Promise<void> {
  applyAdminClient();
  return del<void>(`/api/v1/admin/user-templates/${id}`);
}

export function getNodeTLSStatus(nodeId: string): Promise<NodeTLSStatus> {
  applyAdminClient();
  return get<NodeTLSStatus>(`/api/v1/admin/nodes/${nodeId}/tls`);
}

export function issueNodeCertificate(nodeId: string, req: IssueCertificateRequest): Promise<void> {
  applyAdminClient();
  return post<void>(`/api/v1/admin/nodes/${nodeId}/tls/issue`, req);
}

export function getNodeXrayVersion(nodeId: string): Promise<XrayVersionResponse> {
  applyAdminClient();
  return get<XrayVersionResponse>(`/api/v1/admin/nodes/${nodeId}/xray`);
}

export function updateNodeXray(nodeId: string, req: UpdateXrayRequest): Promise<void> {
  applyAdminClient();
  return post<void>(`/api/v1/admin/nodes/${nodeId}/xray/update`, req);
}

export function createUser(req: CreateUserRequest): Promise<CreateUserResponse> {
  applyAdminClient();
  return post<CreateUserResponse>('/api/v1/admin/users', req);
}

export function resetUserTraffic(id: string): Promise<void> {
  applyAdminClient();
  return post<void>(`/api/v1/admin/users/${id}/reset-traffic`);
}

export function updateNode(id: string, req: UpdateNodeRequest): Promise<Node> {
  applyAdminClient();
  return put<Node>(`/api/v1/admin/nodes/${id}`, req);
}

export function triggerBackup(): Promise<TriggerBackupResponse> {
  applyAdminClient();
  return post<TriggerBackupResponse>('/api/v1/admin/backup/trigger');
}

export function listBackups(): Promise<BackupListResponse> {
  applyAdminClient();
  return get<BackupListResponse>('/api/v1/admin/backup/list');
}

export function getBackupDownloadUrl(key?: string): string {
  applyAdminClient();
  const base = '/api/v1/admin/backup/download';
  return key ? `${base}?key=${encodeURIComponent(key)}` : base;
}
