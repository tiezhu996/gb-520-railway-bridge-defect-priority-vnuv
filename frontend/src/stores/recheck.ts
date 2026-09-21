
import { defineStore } from 'pinia';
import { listRechecks, resolveRecheck } from '../api/recheck';
import type { PriorityRecheck } from '../types/domain';

export const useRecheckStore = defineStore('priorityRecheck', {
  state: () => ({ items: [] as PriorityRecheck[], loading: false, error: '' }),
  actions: {
    async load() {
      this.loading = true; this.error = '';
      try { const result = await listRechecks(); this.items = result.data; }
      catch (error) { this.error = error instanceof Error ? error.message : String(error); }
      finally { this.loading = false; }
    },
    async resolve(id: number, action: string, basis: string): Promise<boolean> {
      this.loading = true; this.error = '';
      try { await resolveRecheck(id, action, basis); await this.load(); return true; }
      catch (error) { this.error = error instanceof Error ? error.message : String(error); return false; }
      finally { this.loading = false; }
    },
  },
});
