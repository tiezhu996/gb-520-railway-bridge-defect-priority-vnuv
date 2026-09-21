
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import EntityPage from '../components/EntityPage.vue';
import ConfirmDialog from '../components/common/ConfirmDialog.vue';
import StatusBadge from '../components/common/StatusBadge.vue';
import { ENTITY_CONFIGS, PRIORITY_REVIEW_LABELS, PRIORITY_REVIEW_OUTCOME_LABELS } from '../types/status';
import { usePriorityDecisionStore } from '../stores/priority-decision';
import { usePriorityReviewStore } from '../stores/priority-review';
import { useAuth } from '../hooks/useAuth';
import { formatDate } from '../utils/format';
import type { PriorityReview, PriorityReviewOutcome } from '../types/domain';

const store = usePriorityDecisionStore();
const reviewStore = usePriorityReviewStore();
const { session, canAtLeast } = useAuth();

const statusFilter = ref('');
const pendingAction = ref<{ review: PriorityReview; outcome: PriorityReviewOutcome } | null>(null);
const replacementBasis = ref('');

const filteredReviews = computed(() =>
  statusFilter.value ? reviewStore.items.filter((review) => review.status === statusFilter.value) : reviewStore.items,
);
const pendingCount = computed(() => reviewStore.items.filter((review) => review.status === 'pending').length);

onMounted(() => {
  void reviewStore.load();
});

// A reviewer/admin may resolve a review only when they are not the preparer of
// the triggered decision.
function canResolve(review: PriorityReview): boolean {
  return review.status === 'pending'
    && canAtLeast('reviewer')
    && session.value?.username !== review.originalPreparedBy;
}

function openResolve(review: PriorityReview, outcome: PriorityReviewOutcome) {
  pendingAction.value = { review, outcome };
  replacementBasis.value = '';
}

async function confirmResolve() {
  if (!pendingAction.value) return;
  const { review, outcome } = pendingAction.value;
  const ok = await reviewStore.resolve(review.id, review.version, outcome, replacementBasis.value);
  if (ok) {
    pendingAction.value = null;
    // The decision's appended revision must refresh too.
    await store.load('priorities');
  }
}
</script>

<template>
	<EntityPage :config="ENTITY_CONFIGS[3]" :store="store"/>
	<main class="workspace">
		<section class="review-panel">
			<header class="page-header">
				<div>
					<p class="eyebrow">严重缺陷触发优先级复查</p>
					<h2>优先级复查</h2>
					<p>严重缺陷核实时，同桥已有观察或限速终态决定即生成待复查事项；原决定在复查期间继续生效。</p>
				</div>
				<div class="review-filters">
					<el-radio-group v-model="statusFilter" size="small">
						<el-radio-button label="">全部</el-radio-button>
						<el-radio-button label="pending">待复查 ({{ pendingCount }})</el-radio-button>
						<el-radio-button label="maintained">已维持</el-radio-button>
						<el-radio-button label="upgraded">已升级</el-radio-button>
						<el-radio-button label="released">已解除</el-radio-button>
					</el-radio-group>
					<el-button size="small" @click="reviewStore.load(statusFilter)">刷新</el-button>
				</div>
			</header>
			<el-alert v-if="reviewStore.error" :title="reviewStore.error" type="error" show-icon/>
			<section class="table-shell">
				<el-table v-loading="reviewStore.loading" :data="filteredReviews">
					<el-table-column prop="code" label="复查编号" width="140"/>
					<el-table-column label="缺陷 / 同桥" min-width="200">
						<template #default="{ row }">
							<strong>{{ row.defectCode }}</strong>
							<small>{{ row.facility }}</small>
						</template>
					</el-table-column>
					<el-table-column label="触发决定" width="170">
						<template #default="{ row }">
							<strong>{{ row.decisionCode }}</strong>
							<small>原状态 {{ row.originalStatus }} · 拟制 {{ row.originalPreparedBy }}</small>
						</template>
					</el-table-column>
					<el-table-column label="复查状态" width="150">
						<template #default="{ row }"><StatusBadge :status="row.status"/></template>
					</el-table-column>
					<el-table-column label="复查结果" min-width="240">
						<template #default="{ row }">
							<template v-if="row.status === 'pending'"><span class="muted">等待非拟制复查员处理，原决定继续生效</span></template>
							<template v-else>
								<strong>{{ PRIORITY_REVIEW_LABELS[row.status as keyof typeof PRIORITY_REVIEW_LABELS] }}</strong>
								<small>{{ row.reviewedBy }} · {{ formatDate(row.reviewedAt || '') }} · 决定 v{{ row.resultingDecisionVersion }}</small>
								<small class="review-basis">替代依据：{{ row.replacementBasis }}</small>
								<small>请求号 {{ row.reviewRequestId }}</small>
							</template>
						</template>
					</el-table-column>
					<el-table-column label="触发信息" width="200">
						<template #default="{ row }">
							<small>{{ row.triggeredBy }} · {{ formatDate(row.triggeredAt) }}</small>
							<small>{{ row.triggerRequestId }}</small>
						</template>
					</el-table-column>
					<el-table-column label="操作" width="300">
						<template #default="{ row }">
							<div v-if="canResolve(row)" class="row-actions">
								<el-button link type="primary" @click="openResolve(row, 'maintain')">{{ PRIORITY_REVIEW_OUTCOME_LABELS.maintain }}</el-button>
								<el-button link type="danger" @click="openResolve(row, 'upgrade')">{{ PRIORITY_REVIEW_OUTCOME_LABELS.upgrade }}</el-button>
								<el-button link type="warning" @click="openResolve(row, 'release')">{{ PRIORITY_REVIEW_OUTCOME_LABELS.release }}</el-button>
							</div>
							<span v-else-if="row.status === 'pending'" class="muted">
								{{ canAtLeast('reviewer') ? '不可处理原拟制决定' : '需复查员/管理员处理' }}
							</span>
							<span v-else class="muted">已完成</span>
						</template>
					</el-table-column>
				</el-table>
			</section>
		</section>

		<ConfirmDialog
			:model-value="Boolean(pendingAction)"
			:title="pendingAction ? `确认${PRIORITY_REVIEW_OUTCOME_LABELS[pendingAction.outcome]}` : ''"
			@update:model-value="(value: boolean) => { if (!value) pendingAction = null; }"
			@confirm="confirmResolve"
		>
			<p v-if="pendingAction">
				对 {{ pendingAction.review.decisionCode }}（原 {{ pendingAction.review.originalStatus }}）执行
				<strong>{{ PRIORITY_REVIEW_OUTCOME_LABELS[pendingAction.outcome] }}</strong>。
				处理结果会追加不可变版本，原决定与版本不会被覆盖。
			</p>
			<el-input
				v-model="replacementBasis"
				type="textarea"
				:rows="3"
				placeholder="请输入替代依据（必填，3-2000 字）"
				maxlength="2000"
				show-word-limit
			/>
		</ConfirmDialog>
	</main>
</template>

<style scoped>
.review-panel {
	margin-top: 24px;
	padding-top: 16px;
	border-top: 1px solid var(--el-border-color-lighter);
}
.review-filters {
	display: flex;
	flex-direction: column;
	gap: 8px;
	align-items: flex-end;
}
.review-basis {
	color: var(--el-text-color-regular);
}
.triggered-reviews {
	display: flex;
	flex-direction: column;
	gap: 4px;
}
.triggered-review {
	display: flex;
	flex-direction: column;
	gap: 2px;
}
</style>
