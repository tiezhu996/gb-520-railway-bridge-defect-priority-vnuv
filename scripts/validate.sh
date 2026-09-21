#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
set -a
if [ -f .env ]; then . ./.env; else . ./.env.example; fi
set +a

(command -v jq >/dev/null 2>&1) || { echo "jq is required for API validation" >&2; exit 1; }

cleanup() { docker compose down -v --remove-orphans; }
docker compose down -v --remove-orphans
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
	trap cleanup INT TERM
else
	trap cleanup EXIT INT TERM
fi

(cd backend && go test ./... && go vet ./... && go build ./...)
(cd frontend && npm ci --no-audit --no-fund && npm run typecheck && npm run build)
docker compose config --quiet
docker compose up -d --build

i=0
until curl -fsS "http://127.0.0.1:${BACKEND_PORT:-19520}/healthz" >/dev/null; do
	i=$((i+1))
	[ "$i" -lt 60 ] || { docker compose logs; exit 1; }
	sleep 2
done
curl -fsS "http://127.0.0.1:${FRONTEND_PORT:-18520}/" >/dev/null

login_token() {
	curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT:-19520}/api/auth/login" \
		-H 'Content-Type: application/json' \
		-d "{\"username\":\"$1\",\"password\":\"Admin123!\"}" | jq -er '.data.token'
}

viewer_token=$(login_token viewer)
operator_token=$(login_token operator)
reviewer_token=$(login_token reviewer)
admin_token=$(login_token admin)

curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/session" -H "Authorization: Bearer $viewer_token" | jq -e '.data.role == "viewer"' >/dev/null
viewer_write_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/bridges" -H "Authorization: Bearer $viewer_token" -H 'Content-Type: application/json' -d '{}')
[ "$viewer_write_status" = "403" ]
viewer_audit_status=$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${BACKEND_PORT}/api/audits" -H "Authorization: Bearer $viewer_token")
[ "$viewer_audit_status" = "403" ]
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits?page=1&pageSize=20" -H "Authorization: Bearer $reviewer_token" | jq -e '.data | type == "array"' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/runtime" -H "Authorization: Bearer $admin_token" | jq -e '.data.appName and .data.databaseDriver and (.data.requestLimit > 0)' >/dev/null

now=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
code="PD-SMOKE-$(date +%s)"
create_payload=$(jq -n --arg code "$code" --arg now "$now" '{code:$code,name:"空卷验收优先级决定",description:"验证不可变版本链",facility:"K42 桥梁作业区",owner:"现场处置组",category:"结构缺陷",riskLevel:"critical",metricValue:88,metricUnit:"score",effectiveAt:$now,evidence:"裂缝照片与量测记录 v1",relatedCode:"DF-001"}')
created=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities" -H "Authorization: Bearer $operator_token" -H 'X-Request-ID: smoke-create' -H 'Content-Type: application/json' -d "$create_payload")
priority_id=$(printf '%s' "$created" | jq -er '.data.id')
printf '%s' "$created" | jq -e '.data.status == "draft" and .data.version == 1 and .data.preparedBy == "operator" and (.data.revisions | length == 1)' >/dev/null

update_payload=$(jq -n --arg now "$now" '{expectedVersion:1,name:"空卷验收优先级决定",description:"复核前补充量测证据",facility:"K42 桥梁作业区",owner:"现场处置组",category:"结构缺陷",riskLevel:"critical",metricValue:93,metricUnit:"score",effectiveAt:$now,evidence:"裂缝照片、量测记录与复测记录 v2",relatedCode:"DF-001"}')
updated=$(curl -fsS -X PUT "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$priority_id" -H "Authorization: Bearer $operator_token" -H 'X-Request-ID: smoke-update' -H 'Content-Type: application/json' -d "$update_payload")
printf '%s' "$updated" | jq -e '.data.version == 2 and (.data.revisions | length == 2)' >/dev/null

transition_payload='{"status":"urgent","expectedVersion":2,"reason":"独立复核确认需立即处置"}'
operator_final_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$priority_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$transition_payload")
[ "$operator_final_status" = "403" ]

self_code="PD-SELF-$(date +%s)"
self_payload=$(printf '%s' "$create_payload" | jq --arg code "$self_code" '.code = $code')
self_created=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -d "$self_payload")
self_id=$(printf '%s' "$self_created" | jq -er '.data.id')
self_final_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$self_id/transition" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -d '{"status":"observe","expectedVersion":1,"reason":"不得自行复核自己的决定"}')
[ "$self_final_status" = "422" ]

curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$priority_id/transition" -H "Authorization: Bearer $reviewer_token" -H 'X-Request-ID: smoke-review' -H 'Content-Type: application/json' -d "$transition_payload" | jq -e '.data.status == "urgent" and .data.version == 3' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$priority_id" -H "Authorization: Bearer $reviewer_token" | jq -e '
	.data.status == "urgent" and
	(.data.revisions | length == 3) and
	([.data.revisions[].evidence] == ["裂缝照片与量测记录 v1","裂缝照片、量测记录与复测记录 v2","裂缝照片、量测记录与复测记录 v2"]) and
	([.data.revisions[].actor] == ["operator","operator","reviewer"]) and
	([.data.revisions[].requestId] == ["smoke-create","smoke-update","smoke-review"])' >/dev/null

locked_status=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$priority_id" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$(printf '%s' "$update_payload" | jq '.expectedVersion = 3')")
[ "$locked_status" = "422" ]
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audit-summary?windowHours=24" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.total >= 3 and .data.transitions >= 1' >/dev/null

# 严重缺陷触发优先级复查：operator 拟制限速决定，reviewer 独立复核定稿。
recheck_facility="K77 复查桥梁"
recheck_decision_code="PD-RCHK-$(date +%s)"
recheck_decision_payload=$(jq -n --arg code "$recheck_decision_code" --arg now "$now" --arg facility "$recheck_facility" '{code:$code,name:"复查源限速决定",description:"验证严重缺陷触发复查",facility:$facility,owner:"现场处置组",category:"结构缺陷",riskLevel:"high",metricValue:76,metricUnit:"score",effectiveAt:$now,evidence:"限速量测证据 v1",relatedCode:"DF-001"}')
recheck_decision=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$recheck_decision_payload")
recheck_decision_id=$(printf '%s' "$recheck_decision" | jq -er '.data.id')
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$recheck_decision_id/transition" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -d '{"status":"restrict","expectedVersion":1,"reason":"独立复核确认限速"}' | jq -e '.data.status == "restrict" and .data.version == 2' >/dev/null

# 同桥严重缺陷核实后系统生成一条待复查事项，原决定继续生效。
recheck_defect_code="DF-RCHK-$(date +%s)"
recheck_defect_payload=$(jq -n --arg code "$recheck_defect_code" --arg now "$now" --arg facility "$recheck_facility" '{code:$code,name:"K77 严重裂缝",description:"触发复查的严重缺陷",facility:$facility,owner:"现场检查组",category:"结构缺陷",riskLevel:"critical",metricValue:96,metricUnit:"score",effectiveAt:$now,evidence:"裂缝影像与复测记录",relatedCode:"IR-001"}')
recheck_defect=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/defects" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$recheck_defect_payload")
recheck_defect_id=$(printf '%s' "$recheck_defect" | jq -er '.data.id')
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/defects/$recheck_defect_id/transition" -H "Authorization: Bearer $operator_token" -H 'X-Request-ID: smoke-verify' -H 'Content-Type: application/json' -d '{"status":"verified","expectedVersion":1,"reason":"现场核实严重缺陷"}' | jq -e '.data.status == "verified"' >/dev/null
recheck_list=$(curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/rechecks?defectId=$recheck_defect_id" -H "Authorization: Bearer $reviewer_token")
printf '%s' "$recheck_list" | jq -e --arg code "$recheck_decision_code" '.meta.total == 1 and .data[0].status == "pending" and .data[0].decisionCode == $code and .data[0].triggerLevel == "restrict" and .data[0].decisionPreparedBy == "operator"' >/dev/null
recheck_id=$(printf '%s' "$recheck_list" | jq -er '.data[0].id')
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$recheck_decision_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.status == "restrict" and .data.version == 2' >/dev/null

# 同一缺陷再次核实仍只保留一条待复查事项。
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/defects/$recheck_defect_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d '{"status":"monitoring","expectedVersion":2,"reason":"转入监测观察"}' >/dev/null
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/defects/$recheck_defect_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d '{"status":"verified","expectedVersion":3,"reason":"再次核实缺陷"}' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/rechecks?defectId=$recheck_defect_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.meta.total == 1' >/dev/null

# 处理人须为复查员或管理员，且不同于原拟制人。
operator_resolve_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/rechecks/$recheck_id/resolve" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d '{"action":"maintain","basis":"操作员不应通过"}')
[ "$operator_resolve_status" = "403" ]
recheck_self_decision_code="PD-RSELF-$(date +%s)"
recheck_self_payload=$(printf '%s' "$recheck_decision_payload" | jq --arg code "$recheck_self_decision_code" '.code = $code')
recheck_self_decision=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -d "$recheck_self_payload")
recheck_self_decision_id=$(printf '%s' "$recheck_self_decision" | jq -er '.data.id')
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$recheck_self_decision_id/transition" -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -d '{"status":"observe","expectedVersion":1,"reason":"管理员独立复核"}' >/dev/null
recheck_self_defect_code="DF-RSELF-$(date +%s)"
recheck_self_defect_payload=$(printf '%s' "$recheck_defect_payload" | jq --arg code "$recheck_self_defect_code" '.code = $code')
recheck_self_defect=$(curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/defects" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$recheck_self_defect_payload")
recheck_self_defect_id=$(printf '%s' "$recheck_self_defect" | jq -er '.data.id')
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/defects/$recheck_self_defect_id/transition" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d '{"status":"verified","expectedVersion":1,"reason":"现场核实严重缺陷"}' >/dev/null
recheck_self_id=$(curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/rechecks?defectId=$recheck_self_defect_id" -H "Authorization: Bearer $reviewer_token" | jq -er '.data[0].id')
self_resolve_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/rechecks/$recheck_self_id/resolve" -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' -d '{"action":"maintain","basis":"原拟制人不应通过"}')
[ "$self_resolve_status" = "422" ]
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/rechecks/$recheck_self_id/resolve" -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -d '{"action":"release","basis":"缺陷复测降级，解除本次复查"}' | jq -e '.data.status == "released" and .data.handledBy == "admin"' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$recheck_self_decision_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.status == "observe" and .data.version == 2 and (.data.revisions | length == 2)' >/dev/null

# 升级为立即处置会追加决定版本并留存替代依据；重复处理失败且不覆盖原决定和版本。
curl -fsS -X POST "http://127.0.0.1:${BACKEND_PORT}/api/rechecks/$recheck_id/resolve" -H "Authorization: Bearer $reviewer_token" -H 'X-Request-ID: smoke-recheck-escalate' -H 'Content-Type: application/json' -d '{"action":"escalate","basis":"复查确认病害发展，需立即处置"}' | jq -e '.data.status == "escalated" and .data.handledBy == "reviewer" and (.data.basis | length > 0)' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$recheck_decision_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.status == "urgent" and .data.version == 3 and (.data.revisions | length == 3) and (.data.revisions[2].actor == "reviewer") and (.data.revisions[2].requestId == "smoke-recheck-escalate")' >/dev/null
replay_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:${BACKEND_PORT}/api/rechecks/$recheck_id/resolve" -H "Authorization: Bearer $admin_token" -H 'Content-Type: application/json' -d '{"action":"maintain","basis":"重复处理应被拒绝"}')
[ "$replay_status" = "422" ]
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/priorities/$recheck_decision_id" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.status == "urgent" and .data.version == 3 and (.data.revisions | length == 3)' >/dev/null

# 审计可回读复查触发与处理；种子数据自带一条待复查事项。
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/audits/PriorityRecheck/$recheck_id" -H "Authorization: Bearer $reviewer_token" | jq -e '[.data[].action] | contains(["recheck-trigger", "recheck-resolve"])' >/dev/null
curl -fsS "http://127.0.0.1:${BACKEND_PORT}/api/rechecks?status=pending" -H "Authorization: Bearer $viewer_token" | jq -e '.data | map(select(.defectCode == "DF-004" and .decisionCode == "PD-002")) | length == 1' >/dev/null

docker compose ps
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
	echo "KEEP_RUNNING=1: containers left running for browser validation"
else
	cleanup
	trap - EXIT INT TERM
fi
