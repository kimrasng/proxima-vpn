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
  // When status last flipped online/offline. Null for nodes that have not
  // changed state since the column was introduced.
  status_changed_at?: string | null;
  last_seen?: string;
  cpu_usage?: number;
  memory_usage?: number;
  disk_usage?: number;
  load_avg?: number;
  network_in?: number;
  network_out?: number;
  xray_version?: string;
  // Quota charged per transferred byte on this node; 1 bills at face value.
  traffic_multiplier: number;
  // Nullable because they come from DB columns the node may not have reported yet.
  shaping_ok: boolean | null;
  shaping_tiers: number | null;
  shaping_error: string | null;
  xray_too_old: boolean;
  xray_minimum: string;
  xray_version_warning: string;
  online_devices: number;
  // capacity is what the plans routed here could place, not an enforced ceiling.
  capacity: number;
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
  // null means the plan predates the cap and falls back to max_devices.
  max_concurrent: number | null;
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
  max_concurrent?: number;
  speed_limit?: number;
  node_group_id: string;
  is_active?: boolean;
}

export interface UpdatePlanRequest {
  name?: string;
  traffic_limit?: number;
  duration_days?: number;
  max_devices?: number;
  max_concurrent?: number;
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
  sub_token: string;
  plan_started_at?: string;
  traffic_reset_at?: string;
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

export interface UserNodeTraffic {
  node_id: string;
  node_name: string;
  upload: number;
  download: number;
  total: number;
}

export interface UserTraffic {
  traffic_used: number;
  traffic_limit: number | null;
  traffic_remaining: number | null;
  percentage: number;
  unlimited: boolean;
  window: TrafficWindow;
  by_node: UserNodeTraffic[];
}

export interface LoginHistoryEntry {
  id: string;
  user_id: string | null;
  actor_type: string;
  attempted_email: string;
  success: boolean;
  failure_reason: string;
  ip: string;
  user_agent: string;
  created_at: string;
}

export interface UpdateNodeRequest {
  name?: string;
  country?: string;
  region?: string;
  traffic_multiplier?: number;
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
export interface DashboardDeltas {
  // False until a 24h-old baseline exists; shown as "no comparison yet", not zero.
  available: boolean;
  active_alerts: number;
  online_nodes: number;
  online_users: number;
  total_users: number;
  traffic_today: number;
}

export interface DashboardStats {
  total_users: number;
  active_users: number;
  online_users: number;
  total_nodes: number;
  online_nodes: number;
  total_traffic_today: number;
  upload_today: number;
  download_today: number;
  total_traffic_month: number;
  pending_requests: number;
  active_alerts: number;
  deltas: DashboardDeltas;
  generated_at: string;
}

export type AlertSeverity = "info" | "warning" | "error" | "success";

export interface DashboardAlert {
  kind: string;
  severity: AlertSeverity;
  count: number;
}

export interface NodeIssue {
  node_id: string;
  node_name: string;
  country: string;
  region: string;
  status: string;
  kind: string;
  severity: AlertSeverity;
  value: number;

  // Lifecycle, added when alerts became stateful.
  alert_id: string;
  // "firing" is a live condition; "stale" is one frozen because the node stopped
  // reporting, so its reading is last-known rather than current.
  state: "firing" | "stale";
  fired_at: string | null;
  duration_seconds: number;
  acked: boolean;
  acked_by: string;
  silenced_until: string | null;
}

export interface DashboardAlerts {
  items: DashboardAlert[];
  node_issues: NodeIssue[];
  // Counts only firing alerts that are neither acknowledged nor silenced, so it
  // is the number of things still waiting on somebody.
  total: number;
  pending_requests: number;
  // When the evaluator last ran. The panel polls faster than the sweep, so this
  // explains a screen that legitimately shows the same data twice.
  evaluated_at: string;
}

export interface NodeTraffic {
  node_id: string;
  node_name: string;
  status: string;
  upload: number;
  download: number;
}

export type TrafficWindow = "today" | "week" | "month";

export interface ActivityEntry {
  id: string;
  event_type: string;
  severity: AlertSeverity;
  actor_type: string;
  actor_id: string;
  actor_label: string;
  target_type: string | null;
  target_id: string | null;
  detail: Record<string, unknown>;
  created_at: string;
}

// online_ips/max_concurrent/over_cap are per USER, so several rows of the same
// user repeat the same numbers: they share one cap.
export interface OnlineAddress {
  ip: string;
  last_seen: string;
}

export interface OnlineUser {
  email: string;
  name: string;
  device_id: string;
  device: string;
  node_id: string;
  node_name: string;
  addresses: OnlineAddress[] | null;
  online_ips: number;
  // 0 means no cap is configured for the user's plan.
  max_concurrent: number;
  over_cap: boolean;
  // Null when the session started before the node agent began stamping it.
  connected_since: string | null;
  traffic_today: number;
}

export interface TerminateSessionResponse {
  message: string;
  cooldown_minutes: number;
}

// User - Devices
export interface Device {
  id: string;
  name?: string;
  xray_uuid: string;
  wg_public_key?: string;
  wg_address?: string;
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

// What /api/v1/user/nodes returns: location only, no connection detail.
export interface AvailableNode {
  name: string;
  country: string;
  region: string;
  status: string;
}

export interface TrafficStats {
  traffic_used: number;
  traffic_limit?: number;
  percentage: number;
  plan_expires_at?: string;
  days_remaining?: number;
}

// online_ips counts distinct live source addresses pool-wide and is the figure
// max_concurrent applies to; online counts device credentials with a live connection.
export interface UserSummary {
  plan_name: string | null;
  status: string;
  traffic_used: number;
  traffic_limit: number | null;
  plan_expires_at: string | null;
  devices: number;
  max_devices: number;
  online: number;
  online_ips: number;
  max_concurrent: number;
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
