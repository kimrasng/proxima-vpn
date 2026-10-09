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
  readonly entry_hostname: string | null;
  readonly entry_dns_status: 'pending' | 'ready' | 'conflict' | 'error' | 'deleting' | 'deleted' | null;
  readonly entry_dns_error_code: 'provider_unavailable' | 'provider_rejected' | 'ownership_conflict' | 'invalid_configuration' | 'dns_mismatch' | null;
  readonly reality_client_sni: string | null;
  readonly reality_sni_status: 'valid' | 'conflict' | 'not_applicable' | null;
  readonly reality_sni_error_code: 'no_common_name' | 'invalid_sni' | 'listener_mismatch' | 'not_reality' | null;
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
  shaping_mode?: string;
  shaping_tiers: number | null;
  shaping_error: string | null;
  xray_too_old: boolean;
  xray_minimum: string;
  xray_version_warning: string;
  online_devices: number;
  // capacity is what the plans routed here could place, not an enforced ceiling.
  capacity: number;
  os_family: string;
  role: NodeRole;
  publish_direct: boolean;
  // 0 means no cap rather than "refuse everything".
  max_concurrent_conns: number;
  firewall_preset: string;
  // Resolved list the installer was given, e.g. "443,20001-22000".
  firewall_ports: string;
  created_at: string;
  // The node's single inbound; only ListNodes fills these, and they are absent
  // when the node has no inbound yet.
  inbound_protocol?: string;
  inbound_port?: number;
  inbound_enabled?: boolean;
  // Only populated by GetNode, keyed by language code; absent on list rows.
  labels?: Record<string, string>;
}

export type NodeOSFamily = 'debian' | 'rhel' | 'alpine';

export type NodeRole = 'exit' | 'relay' | 'both';

export type ChainTransport = 'tcp' | 'udp' | 'tcp_udp';

export interface NodeChain {
  id: string;
  name: string;
  relay_pool_id?: string;
  relay_pool_name?: string;
  entry_node_id?: string;
  entry_node_name?: string;
  entry_host: string;
  entry_port?: number;
  exit_node_id: string;
  exit_node_name: string;
  exit_port: number;
  transport: ChainTransport;
  mode: string;
  priority: number;
  enabled: boolean;
  health: string;
  group_ids: string[];
}

export interface CreateNodeChainRequest {
  name: string;
  entry_node_id: string;
  entry_host?: string;
  entry_port: number;
  exit_node_id: string;
  exit_port: number;
  transport: ChainTransport;
  priority: number;
}

export interface CreateNodeChainBatchRequest {
  entry_node_id: string;
  routes: Array<{ name: string; exit_node_id: string; exit_port: number; entry_port?: number; transport: ChainTransport }>;
  plan_ids: string[];
  preview: boolean;
}

export interface CreateNodeChainBatchResponse {
  routes: NodeChain[];
  preview: boolean;
}

export interface PlanRoutesResponse {
  chain_ids: string[];
  node_group_id: string;
  speed_limit?: number;
  speed_enforcement: 'shared_tier' | 'device_global_v1' | 'unlimited';
  warnings: string[];
}

export interface UpdateNodeChainRequest {
  name?: string;
  entry_host?: string;
  priority?: number;
  enabled?: boolean;
}

export type NodeFirewallPreset = 'standard' | 'web_alt' | 'high_port' | 'custom';

export interface NodePortSpec {
  start: number;
  end: number;
}

export interface ProvisionNodeRequest {
  os_family?: NodeOSFamily;
  role?: NodeRole;
  name?: string;
  country?: string;
  region?: string;
  port?: number;
  traffic_multiplier?: number;
  max_concurrent_conns?: number;
  firewall_preset?: NodeFirewallPreset;
  custom_ports?: NodePortSpec[];
}

export interface GenerateTokenResponse {
  token: string;
  install_command: string;
  node_id: string;
  firewall_ports: string;
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
// Integer cents on the wire; only the display layer divides by 100.
export interface PlanPrice {
  duration_days: number;
  price_cents: number;
}

// text keyed by language code; a missing key was never authored, not blank.
export interface PlanFeature {
  included: boolean;
  text: Record<string, string>;
}

export interface Plan {
  advertise: boolean;
  is_advertised: boolean;
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
  // Only populated by GetPlan, absent on list rows.
  prices?: PlanPrice[];
  features?: PlanFeature[];
  // Derived server-side: true iff the plan has at least one priced duration.
  purchasable: boolean;
}

export interface CreatePlanRequest {
  id?: string;
  advertise?: boolean;
  name: string;
  traffic_limit?: number;
  duration_days: number;
  max_devices: number;
  max_concurrent?: number;
  speed_limit?: number;
  node_group_id: string;
  is_active?: boolean;
  // Undefined leaves the set untouched; a present array fully replaces it.
  // Feature array order is the display order.
  prices?: PlanPrice[];
  features?: PlanFeature[];
}

export interface UpdatePlanRequest {
  advertise?: boolean;
  is_advertised?: boolean;
  name?: string;
  // Omitted preserves the saved value; null clears an optional limit.
  traffic_limit?: number | null;
  duration_days?: number;
  max_devices?: number;
  max_concurrent?: number | null;
  speed_limit?: number | null;
  node_group_id?: string;
  is_active?: boolean;
  prices?: PlanPrice[];
  features?: PlanFeature[];
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
  readonly reality_client_sni?: string;
  role?: NodeRole;
  publish_direct?: boolean;
  name?: string;
  country?: string;
  region?: string;
  traffic_multiplier?: number;
  os_family?: NodeOSFamily;
  max_concurrent_conns?: number;
  firewall_preset?: NodeFirewallPreset;
  custom_ports?: NodePortSpec[];
  labels?: Record<string, string>;
}

export interface ListUsersParams {
  page?: number;
  limit?: number;
  search?: string;
  status?: string;
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
  // True when the node has been removed. Evaluation can never resolve such an
  // alert, because nothing reports for it, so it has to be closed by hand.
  node_deleted: boolean;
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

// total counts all rows matching the filters, not just this page.
export interface NodeEventPage {
  items: ActivityEntry[];
  total: number;
  limit: number;
  offset: number;
}

export interface NodeEventFilterOptions {
  event_types: string[];
  severities: AlertSeverity[];
}

export interface NodeEventQuery {
  eventTypes?: string[];
  severities?: string[];
  hours?: number;
  limit?: number;
  offset?: number;
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

export interface UUIDEvictionStatus {
  device_uuid: string;
  epoch: string;
  state: 'pending' | 'confirmed';
  requested_at: string;
  confirmed_at: string | null;
  required_node_ids: string[];
  acknowledged_node_ids: string[];
  pending_node_ids: string[];
  node_names: Record<string,string>;
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

// User - Profile
export interface UserProfile {
  sub_token?: string;
  id: string;
  email: string;
  name: string;
  status: string;
  plan_name?: string;
  traffic_used: number;
  traffic_limit?: number;
  plan_expires_at?: string;
  language: string;
}

export interface UpdateProfileRequest {
  name?: string;
  password?: string;
  language?: string;
}

// What /api/v1/user/nodes returns: location only, no connection detail.
export interface AvailableNode {
  name: string;
  country: string;
  region: string;
  status: string;
}

// User - Plans
export interface UserPlanPrice {
  duration_days: number;
  price_cents: number;
}

// Resolved server-side to one language: ?lang=, then profile, then English.
export interface UserPlanFeature {
  text: string;
  included: boolean;
}

export interface UserPlanItem {
  id: string;
  name: string;
  traffic_limit?: number;
  duration_days: number;
  max_devices: number;
  speed_limit?: number;
  prices: UserPlanPrice[];
  features: UserPlanFeature[];
  purchasable: boolean;
}

// Orders
export type OrderStatus = 'pending' | 'paid' | 'cancelled' | 'expired';

export interface CreateOrderRequest {
  plan_id: string;
  duration_days: number;
  promotion_code?: string;
}

export interface OrderResponse {
  id: string;
  plan_id: string;
  plan_name: string;
  duration_days: number;
  price_cents: number;
  discount_cents?: number;
  status: OrderStatus;
  created_at: string;
  expires_at?: string;
  provider?: string;
  paid_at?: string;
  cancelled_at?: string;
}

export type PromotionRejectReason =
  | 'not_found'
  | 'inactive'
  | 'outside_window'
  | 'plan_not_eligible'
  | 'below_minimum'
  | 'not_first_purchase'
  | 'user_not_allowed'
  | 'overall_cap_reached'
  | 'user_cap_reached';

// The order is created either way; `promotion_rejected` only reports that the
// code was ignored, so callers must still treat the response as a success.
export interface CreateOrderResult {
  order: OrderResponse;
  promotion_rejected?: PromotionRejectReason;
}

// Promotions
export type PromotionDiscountType = 'percent' | 'fixed';

export interface PromotionRequest {
  code: string;
  discount_type: PromotionDiscountType;
  discount_value: number;
  valid_from: string;
  valid_until: string;
  min_order_cents: number;
  max_redemptions: number | null;
  max_redemptions_per_user: number;
  first_purchase_only: boolean;
  plan_ids: string[];
  duration_days: number[];
  allowed_user_ids: string[];
  is_active?: boolean;
}

export interface PromotionCode {
  id: string;
  code: string;
  discount_type: PromotionDiscountType;
  discount_value: number;
  valid_from: string;
  valid_until: string;
  min_order_cents: number;
  max_redemptions: number | null;
  max_redemptions_per_user: number;
  first_purchase_only: boolean;
  plan_ids: string[];
  duration_days: number[];
  allowed_user_ids: string[];
  redeemed_count: number;
  is_active: boolean;
  created_at: string;
}

// Checkout
export type PaymentMode = 'redirect' | 'manual';

export interface StartCheckoutRequest {
  provider: string;
}

export interface StartCheckoutResponse {
  provider: string;
  mode: PaymentMode;
  // Only present when mode === 'redirect'.
  redirect_url?: string;
}

export interface PaymentProviderItem {
  name: string;
  mode: PaymentMode;
}

// The context fields are additive and omitempty on the server: they are absent
// for an order created before capture shipped and for one whose tracking columns
// retention has purged, which is why they are optional rather than empty strings.
export interface AdminOrderItem extends OrderResponse {
  user_email: string;
  user_name: string;
  paid_by?: string;
  client_ip?: string;
  browser_family?: string;
  os_family?: string;
}

// Order audit (GET /admin/orders/:id/audit)
//
// request_context and grant are `null` rather than zeroed: "never captured" and
// "captured as empty" are different answers when reviewing a disputed charge, so
// the UI must be able to tell them apart. payment_events is always an array.
export interface AdminOrderAuditOrder {
  id: string;
  user_id: string;
  user_email: string;
  user_name: string;
  plan_id: string;
  plan_name: string;
  duration_days: number;
  price_cents: number;
  discount_cents: number;
  status: OrderStatus;
  // An opaque provider name, not a registry key: 'promotion' settles a
  // zero-cost order without being a payment provider at all. Never switch on it.
  provider: string;
  provider_session_id: string;
  created_at: string;
  paid_at: string | null;
  // Not nullable, unlike the timestamps around it: the column is
  // `TEXT NOT NULL DEFAULT ''`, so an unpaid order carries "" and never null.
  paid_by: string;
  cancelled_at: string | null;
  expires_at: string | null;
  expired_at: string | null;
}

// Where the order came from. None of these is an authorization, eligibility or
// uniqueness input - device_fingerprint in particular is a correlation hint only.
export interface AdminOrderAuditContext {
  origin: string;
  client_ip: string;
  user_agent: string;
  browser_family: string;
  os_family: string;
  locale: string;
  device_fingerprint: string;
}

// The plan expiry on either side of the grant this order paid for. Null until a
// grant has actually run; either endpoint can be null on its own (a first
// purchase has no "before").
export interface AdminOrderAuditGrant {
  plan_expires_before: string | null;
  plan_expires_after: string | null;
}

export type PaymentEventOutcome = 'received' | 'granted' | 'duplicate' | 'ignored' | 'failed';

// One confirmation attempt against the order. The raw provider payload is
// deliberately absent from the endpoint - it can carry cardholder PII an order
// review never needs - so there is no field for it here either.
export interface AdminOrderAuditEvent {
  id: string;
  provider: string;
  external_id: string;
  amount_cents: number;
  currency: string;
  session_id: string;
  // Widened past PaymentEventOutcome deliberately: an outcome added server-side
  // must still render as itself rather than fall through a narrowed match.
  outcome: string;
  reason: string;
  request_ip: string;
  user_agent: string;
  request_id: string;
  actor_type: string;
  actor_id: string;
  received_at: string;
  processed_at: string | null;
}

export interface AdminOrderAudit {
  order: AdminOrderAuditOrder;
  request_context: AdminOrderAuditContext | null;
  grant: AdminOrderAuditGrant | null;
  payment_events: AdminOrderAuditEvent[];
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

// Admin - Subscription domain pool
//
// Health is deliberately server-observed metadata. It can show DNS, TLS, and
// certificate failures from the service network, but cannot establish whether a
// subscriber can reach a domain from their own network.
export type SubscriptionDomainHealthStatus = "healthy" | "warning" | "failed" | "unknown";

export interface SubscriptionDomain {
  id: string;
  domain: string;
  enabled: boolean;
  is_public: boolean;
  display_order: number;
  is_default: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateSubscriptionDomainRequest {
  domain: string;
  enabled: boolean;
  is_public: boolean;
  display_order: number;
  is_default: boolean;
}

export interface UpdateSubscriptionDomainRequest {
  domain?: string;
  enabled?: boolean;
  is_public?: boolean;
  display_order?: number;
  is_default?: boolean;
}

export interface SubscriptionDomainHealth {
  domain_id: string;
  dns_status: SubscriptionDomainHealthStatus;
  tls_status: SubscriptionDomainHealthStatus;
  certificate_status: SubscriptionDomainHealthStatus;
  certificate_expires_at: string | null;
  checked_at: string | null;
  error: string | null;
}

// GET /api/v1/user/subscription-domains only exposes enabled, public records.
// It intentionally omits service-side health: reachability belongs to the user
// network and is measured by the browser before the UI orders these candidates.
export interface PublicSubscriptionDomain {
  id: string;
  domain: string;
  display_order: number;
  is_default: boolean;
}
