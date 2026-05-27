// Auth
export interface LoginRequest {
  email: string;
  password: string;
  totp_code?: string;
}

export interface LoginResponse {
  token: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
  name: string;
}

export interface RegisterResponse {
  id: string;
  email: string;
  name: string;
  sub_token: string;
}

// Admin - Nodes
export interface NodeMetricsEntry {
  cpu_usage: number;
  memory_usage: number;
  disk_usage: number;
  load_avg: number;
  network_in: number;
  network_out: number;
  recorded_at: string;
}

export interface Node {
  id: string;
  name: string;
  country: string;
  region: string;
  ip: string;
  port: number;
  status: string;
  last_seen?: string;
  cpu_usage?: number;
  memory_usage?: number;
  disk_usage?: number;
  load_avg?: number;
  network_in?: number;
  network_out?: number;
  xray_version?: string;
  created_at: string;
}

export interface GenerateTokenResponse {
  token: string;
  install_command: string;
}

// Admin - Node Groups
export interface NodeGroup {
  id: string;
  name: string;
  node_count?: number;
  created_at: string;
}

export interface NodeGroupDetail {
  id: string;
  name: string;
  nodes: Node[];
  created_at: string;
}

export interface CreateNodeGroupRequest {
  name: string;
}

export interface UpdateNodeGroupRequest {
  name: string;
}

// Admin - Plans
export interface Plan {
  id: string;
  name: string;
  traffic_limit?: number;
  duration_days: number;
  max_devices: number;
  speed_limit?: number;
  node_group_id: string;
  node_group_name?: string;
  is_active: boolean;
  created_at: string;
}

export interface CreatePlanRequest {
  name: string;
  traffic_limit?: number;
  duration_days: number;
  max_devices: number;
  speed_limit?: number;
  node_group_id: string;
  is_active?: boolean;
}

export interface UpdatePlanRequest {
  name?: string;
  traffic_limit?: number;
  duration_days?: number;
  max_devices?: number;
  speed_limit?: number;
  node_group_id?: string;
  is_active?: boolean;
}

// Admin - Users
export interface User {
  id: string;
  email: string;
  name: string;
  status: string;
  is_active: boolean;
  plan_id?: string;
  plan_name?: string;
  traffic_used: number;
  plan_expires_at?: string;
  created_at: string;
}

export interface UserDetail extends User {
  devices: Device[];
  traffic_limit?: number;
}

export interface PaginatedUsers {
  users: User[];
  total: number;
  page: number;
  limit: number;
}

export interface UpdateUserRequest {
  name?: string;
  status?: string;
  is_active?: boolean;
  plan_id?: string;
  plan_expires_at?: string;
}

export interface CreateUserRequest {
  email: string;
  password: string;
  name: string;
  plan_id?: string;
  is_active?: boolean;
}

export interface CreateUserResponse {
  id: string;
  email: string;
  name: string;
  status: string;
  created_at: string;
}

export interface UpdateNodeRequest {
  name?: string;
  country?: string;
  region?: string;
}

export interface ListUsersParams {
  page?: number;
  limit?: number;
  search?: string;
  status?: string;
}

// Admin - Plan Requests
export interface PlanRequest {
  id: string;
  user_email: string;
  user_name: string;
  plan_name: string;
  plan_id: string;
  status: string;
  created_at: string;
}

// Admin - Stats
export interface DashboardStats {
  total_users: number;
  active_users: number;
  online_users: number;
  total_nodes: number;
  online_nodes: number;
  total_traffic_today: number;
  total_traffic_month: number;
  pending_requests: number;
}

export interface OnlineUser {
  email: string;
  device: string;
  node_name: string;
}

// User - Devices
export interface Device {
  id: string;
  name?: string;
  xray_uuid: string;
  subscription_url?: string;
  created_at: string;
}

export interface CreateDeviceRequest {
  name?: string;
}

// User - Profile
export interface UserProfile {
  id: string;
  email: string;
  name: string;
  status: string;
  plan_name?: string;
  traffic_used: number;
  traffic_limit?: number;
  plan_expires_at?: string;
}

export interface UpdateProfileRequest {
  name?: string;
  password?: string;
}

export interface TrafficStats {
  traffic_used: number;
  traffic_limit?: number;
  percentage: number;
  plan_expires_at?: string;
  days_remaining?: number;
}

export interface TrafficHistoryEntry {
  date: string;
  upload: number;
  download: number;
}

// Announcements
export interface Announcement {
  id: string;
  title: string;
  content: string;
  image_url?: string | null;
  is_active: boolean;
  expires_at?: string | null;
  created_at: string;
}

export interface CreateAnnouncementRequest {
  title: string;
  content: string;
  image_url?: string | null;
  expires_at?: string | null;
}

export interface UpdateAnnouncementRequest {
  title?: string;
  content?: string;
  image_url?: string | null;
  is_active?: boolean;
  expires_at?: string | null;
}

// Settings
export interface Setting {
  key: string;
  value: unknown;
  updated_at: string;
}

// Admin - Inbounds
export interface Inbound {
  id: string;
  node_id: string;
  protocol: string;
  port: number;
  tag: string;
  settings: Record<string, unknown>;
  enabled: boolean;
  created_at: string;
}

export interface CreateInboundRequest {
  protocol: string;
  port: number;
  tag: string;
  settings: Record<string, unknown>;
}

// Admin - User Templates
export interface UserTemplate {
  id: string;
  name: string;
  traffic_limit?: number;
  duration_days: number;
  max_devices: number;
  speed_limit?: number;
  node_group_id?: string;
  node_group_name?: string;
  created_at: string;
}

export interface CreateUserTemplateRequest {
  name: string;
  traffic_limit?: number;
  duration_days: number;
  max_devices: number;
  speed_limit?: number;
  node_group_id?: string;
}

// 2FA
export interface TwoFASetup {
  secret: string;
  url: string;
}

export interface TwoFAEnableRequest {
  secret: string;
  code: string;
}

export interface TwoFADisableRequest {
  totp_code: string;
}

export interface TwoFAStatus {
  enabled: boolean;
}

// Admin - Node TLS
export interface NodeTLSStatus {
  has_cert: boolean;
  cert_file: string;
  key_file: string;
  domain: string;
}

export interface IssueCertificateRequest {
  domain: string;
  email: string;
}

// Admin - Node Xray Version
export interface XrayVersionResponse {
  current_version: string;
  latest_version: string;
}

export interface UpdateXrayRequest {
  version?: string;
}

// Admin - Backup
export interface BackupEntry {
  key: string;
  size?: number;
  last_modified?: string;
}

export interface BackupListResponse {
  backups: BackupEntry[];
}

export interface TriggerBackupResponse {
  message: string;
  path: string;
}
