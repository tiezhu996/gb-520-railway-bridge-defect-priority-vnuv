import { defineStore } from 'pinia';
import { listPriorityReviews, resolvePriorityReview } from '../api/priority-review';
import type { PageMeta, PriorityReview, PriorityReviewOutcome } from '../types/domain';

export const usePriorityReviewStore = defineStore('priorityReview', {
  state: () => ({
    items: [] as PriorityReview[],
    meta: { page: 1, pageSize: 50, total: 0 } as PageMeta,
    loading: false,
    error: '',
  }),
  actions: {
    async load(status = '', search = '') {
      this.loading = true;
      this.error = '';
      try {
        const result = await listPriorityReviews(1, 50, status, search);
        this.items = result.data;
        this.meta = result.meta || { page: 1, pageSize: 50, total: result.data.length };
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error);
      } finally {
        this.loading = false;
      }
    },
    async resolve(id: number, expectedVersion: number, outcome: PriorityReviewOutcome, replacementBasis: string): Promise<boolean> {
      this.loading = true;
      this.error = '';
      try {
        await resolvePriorityReview(id, expectedVersion, outcome, replacementBasis);
        await this.load();
        return true;
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error);
        return false;
      } finally {
        this.loading = false;
      }
    },
  },
});
