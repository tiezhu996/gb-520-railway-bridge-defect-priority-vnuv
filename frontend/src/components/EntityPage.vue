
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import type { DomainRecord, EntityConfig, PriorityDecisionRevision, PriorityRecheck } from '../types/domain';
import { allowedTargets, formatDate } from '../utils/format';
import { useAuth } from '../hooks/useAuth';
import { useRecheckStore } from '../stores/recheck';
import StatusBadge from './common/StatusBadge.vue';
import SeverityBadge from './common/SeverityBadge.vue';
import EvidenceGallery from './common/EvidenceGallery.vue';
import MetricCard from './common/MetricCard.vue';
import ConfirmDialog from './common/ConfirmDialog.vue';

const props = withDefaults(defineProps<{ config: EntityConfig; store: any; showEvidence?: boolean }>(), { showEvidence: false });
const { session, canAtLeast } = useAuth();
const rechecks = useRecheckStore();
const search = ref('');
const showCreate = ref(false);
const pending = ref<{ item: DomainRecord; status: string } | null>(null);
const resolving = ref<PriorityRecheck | null>(null);
const resolveAction = ref('maintain');
const resolveBasis = ref('');
const canWrite = computed(() => canAtLeast('operator'));
const showRecheck = computed(() => ['defectFinding', 'priorityDecision'].includes(props.config.key));
const highRisk = computed(() => props.store.items.filter((item: DomainRecord) => ['high', 'critical'].includes(item.riskLevel)).length);
const pendingRechecks = computed(() => rechecks.items.filter((item) => item.status === 'pending').length);
const recheckByDefect = computed(() => new Map(rechecks.items.map((item) => [item.defectFindingId, item])));
const rechecksByDecision = computed(() => {
	const grouped = new Map<number, PriorityRecheck[]>();
	for (const item of rechecks.items) {
		const list = grouped.get(item.priorityDecisionId) || [];
		list.push(item);
		grouped.set(item.priorityDecisionId, list);
	}
	return grouped;
});

onMounted(() => {
	void props.store.load(props.config.path);
	if (showRecheck.value) void rechecks.load();
});

function targetsFor(item: DomainRecord): readonly string[] {
	if (props.config.key === 'priorityDecision') {
		if (!canAtLeast('reviewer') || item.preparedBy === session.value?.username) return [];
	} else if (!canWrite.value) return [];
	return allowedTargets(props.config.key, item.status);
}

function latestRevision(item: DomainRecord): PriorityDecisionRevision | undefined {
	return item.revisions?.[item.revisions.length - 1];
}

function canResolve(item: PriorityRecheck): boolean {
	return item.status === 'pending' && canAtLeast('reviewer') && item.decisionPreparedBy !== session.value?.username;
}

function openResolve(item: PriorityRecheck) {
	resolving.value = item;
	resolveAction.value = 'maintain';
	resolveBasis.value = '';
}

async function createDemo() {
	const now = Date.now();
	const created = await props.store.createRecord(props.config.path, {
		code: `${props.config.key.toUpperCase()}-${String(now).slice(-6)}`, name: `新增${props.config.label}`,
		description: '通过前端工作台创建的业务记录', facility: 'K42 桥梁作业区', owner: session.value?.displayName || '现场操作员',
		category: '结构复核', riskLevel: 'medium', metricValue: 25, metricUnit: 'score', effectiveAt: new Date().toISOString(),
		evidence: '现场照片、量测记录与检查批次已完成核对', relatedCode: props.config.key === 'priorityDecision' ? 'DF-001' : 'IR-001',
	});
	if (created) { search.value = ''; showCreate.value = false; }
}

async function confirmTransition() {
	if (!pending.value) return;
	const changed = await props.store.transition(props.config.path, pending.value.item, pending.value.status);
	if (changed) {
		search.value = '';
		pending.value = null;
		if (showRecheck.value) await rechecks.load();
	}
}

async function confirmResolve() {
	if (!resolving.value) return;
	if (resolveBasis.value.trim().length < 3) { rechecks.error = '替代依据至少需要 3 个字符'; return; }
	const resolved = await rechecks.resolve(resolving.value.id, resolveAction.value, resolveBasis.value.trim());
	if (resolved) { resolving.value = null; await props.store.load(props.config.path); }
}
</script>

<template>
	<main class="workspace">
		<header class="page-header">
			<div><p class="eyebrow">业务工作台</p><h1>{{ config.label }}</h1><p>统一管理{{ config.label }}的状态、风险、证据与责任人。</p></div>
			<el-button v-if="canWrite" type="primary" @click="showCreate = true">新增{{ config.label }}</el-button>
		</header>
		<section class="metrics">
			<MetricCard label="记录总数" :value="store.meta.total" detail="当前筛选范围"/>
			<MetricCard label="高风险" :value="highRisk" detail="需要优先复核"/>
			<MetricCard v-if="showRecheck" label="待复查" :value="pendingRechecks" detail="严重缺陷触发"/>
			<MetricCard label="状态种类" :value="new Set(store.items.map((item: DomainRecord) => item.status)).size" detail="状态机覆盖"/>
		</section>
		<section v-if="showEvidence" class="evidence-panel"><header><strong>证据摘要</strong><span>最近四条记录</span></header><EvidenceGallery :records="store.items"/></section>
		<section class="toolbar"><el-input v-model="search" :placeholder="`搜索${config.label}编码或名称`" clearable/><el-button type="primary" @click="store.load(config.path, search)">查询</el-button><el-button @click="search = ''; store.load(config.path)">重置</el-button></section>
		<el-alert v-if="store.error" :title="store.error" type="error" show-icon/>
		<el-alert v-if="showRecheck && rechecks.error" :title="rechecks.error" type="error" show-icon/>
		<section class="table-shell">
			<el-table v-loading="store.loading" :data="store.items">
				<el-table-column prop="code" label="编码" width="150"/>
				<el-table-column label="名称" min-width="180"><template #default="{ row }"><strong>{{ row.name }}</strong><small>{{ row.facility }}</small></template></el-table-column>
				<el-table-column label="状态" width="130"><template #default="{ row }"><StatusBadge :status="row.status"/></template></el-table-column>
				<el-table-column label="风险" width="90"><template #default="{ row }"><SeverityBadge v-if="['defectFinding', 'priorityDecision'].includes(config.key)" :severity="row.riskLevel"/><span v-else>{{ row.riskLevel }}</span></template></el-table-column>
				<el-table-column prop="owner" label="责任人" min-width="130"/>
				<el-table-column label="指标" width="120"><template #default="{ row }">{{ row.metricValue }} {{ row.metricUnit }}</template></el-table-column>
				<el-table-column v-if="config.key === 'defectFinding'" label="触发决定" min-width="210"><template #default="{ row }"><template v-if="recheckByDefect.get(row.id)"><strong>{{ recheckByDefect.get(row.id)!.decisionCode }} · {{ recheckByDefect.get(row.id)!.triggerLevel }}</strong><small>复查状态 <StatusBadge :status="recheckByDefect.get(row.id)!.status"/></small></template><span v-else class="muted">-</span></template></el-table-column>
				<el-table-column v-if="config.key === 'priorityDecision'" label="版本审计" width="250"><template #default="{ row }"><strong>v{{ row.version }} · {{ row.preparedBy }}</strong><small>{{ latestRevision(row)?.actor }} · {{ latestRevision(row)?.requestId }}</small><small>{{ latestRevision(row)?.evidence }}</small></template></el-table-column>
				<el-table-column v-if="config.key === 'priorityDecision'" label="复查状态" min-width="280"><template #default="{ row }"><div v-for="item in rechecksByDecision.get(row.id) || []" :key="item.id" class="recheck-row"><StatusBadge :status="item.status"/><strong>{{ item.defectCode }}</strong><small v-if="item.status === 'pending'">待复查 · {{ formatDate(item.createdAt) }}</small><small v-else>{{ item.handledBy }} · {{ item.basis }}</small><el-button v-if="canResolve(item)" link type="primary" @click="openResolve(item)">复查处理</el-button></div><span v-if="!(rechecksByDecision.get(row.id) || []).length" class="muted">-</span></template></el-table-column>
				<el-table-column label="更新时间" width="180"><template #default="{ row }">{{ formatDate(row.updatedAt) }}</template></el-table-column>
				<el-table-column label="操作" width="300"><template #default="{ row }"><div class="row-actions"><el-button v-for="target in targetsFor(row)" :key="target" link type="primary" @click="pending = { item: row, status: target }">推进至 {{ target }}</el-button><span v-if="targetsFor(row).length === 0" class="muted">无可用操作</span></div></template></el-table-column>
			</el-table>
		</section>
		<ConfirmDialog v-model="showCreate" :title="`新增${config.label}`" @confirm="createDemo"><p>将创建一条包含完整责任人、风险和证据信息的记录。</p></ConfirmDialog>
		<ConfirmDialog :model-value="Boolean(pending)" title="确认状态迁移" @update:model-value="pending = null" @confirm="confirmTransition"><p>状态迁移会写入审计日志并保留请求号；优先级定稿后不可覆盖。</p><strong>{{ pending?.item.status }} → {{ pending?.status }}</strong></ConfirmDialog>
		<ConfirmDialog :model-value="Boolean(resolving)" title="严重缺陷复查处理" @update:model-value="resolving = null" @confirm="confirmResolve">
			<p>严重缺陷 {{ resolving?.defectCode }} 已核实，同桥决定 {{ resolving?.decisionCode }}（{{ resolving?.triggerLevel }}）继续生效，请复查并留存替代依据。</p>
			<el-select v-model="resolveAction" style="width: 100%; margin-bottom: 8px">
				<el-option label="维持原决定" value="maintain"/>
				<el-option label="升级为立即处置" value="escalate"/>
				<el-option label="解除复查" value="release"/>
			</el-select>
			<el-input v-model="resolveBasis" type="textarea" :rows="3" placeholder="替代依据（必填，至少 3 个字符）"/>
		</ConfirmDialog>
	</main>
</template>
