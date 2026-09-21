
import { request } from './client';
import type { PriorityReview, PriorityReviewOutcome } from '../types/domain';

export async function listPriorityReviews(page = 1, pageSize = 50, status = '', search = '') {
  const query = new URLSearchParams({ page: String(page), pageSize: String(pageSize) });
  if (status) query.set('status', status);
  if (search) query.set('search', search);
  return request<PriorityReview[]>(`/priority-reviews?${query.toString()}`);
}

export async function resolvePriorityReview(id: number, expectedVersion: number, outcome: PriorityReviewOutcome, replacementBasis: string) {
  return request<PriorityReview>(`/priority-reviews/${id}/resolve`, {
    method: 'POST',
    body: JSON.stringify({ expectedVersion, outcome, replacementBasis }),
  });
}
