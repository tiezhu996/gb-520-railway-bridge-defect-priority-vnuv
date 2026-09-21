import type { EntityConfig } from './domain';

export type DefectState = 'new' | 'verified' | 'monitoring' | 'mitigated' | 'closed';
export const ALL_DEFECT_STATE: readonly DefectState[] = ['new', 'verified', 'monitoring', 'mitigated', 'closed'];
export type PriorityLevel = 'observe' | 'restrict' | 'urgent';
export const ALL_PRIORITY_LEVEL: readonly PriorityLevel[] = ['observe', 'restrict', 'urgent'];

export type PriorityReviewState = 'pending' | 'maintained' | 'upgraded' | 'released';
export const ALL_PRIORITY_REVIEW_STATE: readonly PriorityReviewState[] = ['pending', 'maintained', 'upgraded', 'released'];

export const PRIORITY_REVIEW_LABELS: Record<PriorityReviewState, string> = {
  pending: '待复查',
  maintained: '已维持',
  upgraded: '已升级立即处置',
  released: '已解除',
};

export const PRIORITY_REVIEW_OUTCOME_LABELS: Record<'maintain' | 'upgrade' | 'release', string> = {
  maintain: '维持原决定',
  upgrade: '升级为立即处置',
  release: '解除',
};

export const ENTITY_CONFIGS: readonly EntityConfig[] = [
  { key: 'bridgeAsset', path: 'bridges', label: '桥梁资产', statuses: ['active', 'restricted', 'closed', 'retired'] as const },
  { key: 'inspectionRound', path: 'inspections', label: '检查批次', statuses: ['planned', 'running', 'review', 'completed'] as const },
  { key: 'defectFinding', path: 'defects', label: '缺陷发现', statuses: ['new', 'verified', 'monitoring', 'mitigated', 'closed'] as const },
  { key: 'priorityDecision', path: 'priorities', label: '优先级决定', statuses: ['draft', 'observe', 'restrict', 'urgent', 'released'] as const }
];
