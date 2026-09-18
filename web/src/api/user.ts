import { get, post, del, put, setClientTokenType } from './client';
import type {
  UserProfile,
  UpdateProfileRequest,
  TrafficStats,
  Device,
  CreateDeviceRequest,
  PlanRequest,
  Plan,
  Announcement,
  AvailableNode,
  UserSummary,
} from './types';

function applyUserClient() {
  setClientTokenType('user');
}

export function getProfile(): Promise<UserProfile> {
  applyUserClient();
  return get<UserProfile>('/api/v1/user/profile');
}

export function updateProfile(req: UpdateProfileRequest): Promise<UserProfile> {
  applyUserClient();
  return put<UserProfile>('/api/v1/user/profile', req);
}

export function getTrafficStats(): Promise<TrafficStats> {
  applyUserClient();
  return get<TrafficStats>('/api/v1/user/traffic');
}

export function getSummary(): Promise<UserSummary> {
  applyUserClient();
  return get<UserSummary>('/api/v1/user/summary');
}

export function regenerateSubToken(): Promise<{ sub_token: string }> {
  applyUserClient();
  return post<{ sub_token: string }>('/api/v1/user/sub-token/regenerate');
}

export function listDevices(): Promise<Device[]> {
  applyUserClient();
  return get<Device[]>('/api/v1/user/devices');
}

export function createDevice(req: CreateDeviceRequest): Promise<Device> {
  applyUserClient();
  return post<Device>('/api/v1/user/devices', req);
}

export function deleteDevice(id: string): Promise<void> {
  applyUserClient();
  return del<void>(`/api/user/devices/${id}`);
}

export function createPlanRequest(planId: string): Promise<PlanRequest> {
  applyUserClient();
  return post<PlanRequest>('/api/v1/user/plan-requests', { plan_id: planId });
}

export function listMyPlanRequests(): Promise<PlanRequest[]> {
  applyUserClient();
  return get<PlanRequest[]>('/api/v1/user/plan-requests');
}

export function listPlans(): Promise<Plan[]> {
  applyUserClient();
  return get<Plan[]>('/api/v1/user/plans');
}

export function listAvailableNodes(): Promise<AvailableNode[]> {
  applyUserClient();
  return get<AvailableNode[]>('/api/v1/user/nodes');
}

export function listAnnouncements(): Promise<Announcement[]> {
  applyUserClient();
  return get<Announcement[]>('/api/v1/user/announcements');
}
