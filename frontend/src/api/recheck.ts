
import { request } from './client';
import type { PriorityRecheck } from '../types/domain';

export async function listRechecks(page = 1, pageSize = 100) {
  return request<PriorityRecheck[]>(`/rechecks?page=${page}&pageSize=${pageSize}`);
}
export async function resolveRecheck(id: number, action: string, basis: string) {
  return request<PriorityRecheck>(`/rechecks/${id}/resolve`, {
    method: 'POST', body: JSON.stringify({ action, basis }),
  });
}
