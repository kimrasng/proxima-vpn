import { get, post, put, del, setClientTokenType } from './client';

export type GuideFormat = 'markdown' | 'html';

export interface GuideCategory {
  id: string;
  name: string;
  sort_order: number;
}

export interface AdminGuide {
  id: string;
  category_id: string | null;
  title: string;
  summary: string;
  format: GuideFormat;
  content: string;
  rendered_html: string;
  is_published: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface GuideInput {
  category_id: string | null;
  title: string;
  summary: string;
  format: GuideFormat;
  content: string;
  is_published: boolean;
  sort_order: number;
}

export interface GuideSummary {
  id: string;
  category_id: string | null;
  title: string;
  summary: string;
  updated_at: string;
}

export interface GuideDetail {
  id: string;
  title: string;
  summary: string;
  category_id: string | null;
  category_name: string | null;
  rendered_html: string;
  updated_at: string;
}

const asAdmin = () => setClientTokenType('admin');
const asUser = () => setClientTokenType('user');

export function listGuideCategories(): Promise<GuideCategory[]> {
  asAdmin();
  return get<GuideCategory[]>('/api/v1/admin/guides/categories');
}

export function createGuideCategory(req: { name: string; sort_order: number }): Promise<GuideCategory> {
  asAdmin();
  return post<GuideCategory>('/api/v1/admin/guides/categories', req);
}

export function updateGuideCategory(id: string, req: { name: string; sort_order: number }): Promise<GuideCategory> {
  asAdmin();
  return put<GuideCategory>(`/api/v1/admin/guides/categories/${id}`, req);
}

export function deleteGuideCategory(id: string): Promise<void> {
  asAdmin();
  return del<void>(`/api/v1/admin/guides/categories/${id}`);
}

export function listAdminGuides(): Promise<AdminGuide[]> {
  asAdmin();
  return get<AdminGuide[]>('/api/v1/admin/guides');
}

export function createGuide(req: GuideInput): Promise<AdminGuide> {
  asAdmin();
  return post<AdminGuide>('/api/v1/admin/guides', req);
}

export function updateGuide(id: string, req: GuideInput): Promise<AdminGuide> {
  asAdmin();
  return put<AdminGuide>(`/api/v1/admin/guides/${id}`, req);
}

export function deleteGuide(id: string): Promise<void> {
  asAdmin();
  return del<void>(`/api/v1/admin/guides/${id}`);
}

export function previewGuide(format: GuideFormat, content: string): Promise<{ html: string }> {
  asAdmin();
  return post<{ html: string }>('/api/v1/admin/guides/preview', { format, content });
}

export function uploadGuideImage(file: File): Promise<{ url: string }> {
  asAdmin();
  const formData = new FormData();
  formData.append('image', file);
  return post<{ url: string }>('/api/v1/admin/uploads/image', formData);
}

export function listPublishedGuides(): Promise<{ categories: GuideCategory[]; guides: GuideSummary[] }> {
  asUser();
  return get<{ categories: GuideCategory[]; guides: GuideSummary[] }>('/api/v1/user/guides');
}

export function getPublishedGuide(id: string): Promise<GuideDetail> {
  asUser();
  return get<GuideDetail>(`/api/v1/user/guides/${id}`);
}
