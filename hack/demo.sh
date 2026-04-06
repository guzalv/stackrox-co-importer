#!/usr/bin/env bash
# demo.sh — Interactive demo of the CO → ACS scheduled scan importer.
#
# Prerequisites:
#   - kubectl configured with a context pointing to an OCP cluster with CO installed
#   - ACS Central reachable from this machine
#   - ROX_ADMIN_PASSWORD or ROX_API_TOKEN set
#   - ROX_ENDPOINT set
#   - python3 in PATH
#   - The importer binary built: make build
#
# Optional second cluster (auto-detected):
#   - Set SECOND_KUBECONFIG (default: ~/.kube/config-secured-cluster)
#   - If reachable and CO is installed, multi-cluster steps are enabled
#
# Usage:
#   ROX_ADMIN_PASSWORD=admin ROX_ENDPOINT=central.example.com ./hack/demo.sh
#
# Non-interactive:
#   DEMO_AUTO=1 DEMO_PAUSE=1 ROX_ADMIN_PASSWORD=admin ROX_ENDPOINT=... ./hack/demo.sh

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMPORTER="${SCRIPT_DIR}/../bin/compliance-operator-importer"
CO_NS="${CO_NAMESPACE:-openshift-compliance}"

ACS_ENDPOINT="${ROX_ENDPOINT:?ROX_ENDPOINT must be set}"
ACS_URL="https://${ACS_ENDPOINT#*://}"

if [[ -n "${ROX_ADMIN_PASSWORD:-}" ]]; then
    CURL_AUTH=(-u "${ROX_ADMIN_USER:-admin}:${ROX_ADMIN_PASSWORD}")
elif [[ -n "${ROX_API_TOKEN:-}" ]]; then
    CURL_AUTH=(-H "Authorization: Bearer ${ROX_API_TOKEN}")
else
    echo "ERROR: set ROX_ADMIN_PASSWORD or ROX_API_TOKEN" >&2; exit 1
fi

IMPORTER_FLAGS=(--endpoint "$ACS_ENDPOINT" --insecure-skip-verify)

DEMO_PREFIX="demo-import"
SSB_CIS="${DEMO_PREFIX}-cis-scan"
SSB_MODERATE="${DEMO_PREFIX}-moderate-scan"
SSB_PCI="${DEMO_PREFIX}-pci-dss-scan"
SCAN_SETTING="${DEMO_PREFIX}-setting"
REPORT_JSON="/tmp/co-acs-importer-demo.json"

PRIMARY_KUBECONFIG="${HOME}/.kube/config"
SECOND_KUBECONFIG="${SECOND_KUBECONFIG:-${HOME}/.kube/config-secured-cluster}"
HAS_SECOND_CLUSTER=0
MULTI_KUBECONFIG="${PRIMARY_KUBECONFIG}"

KC1="kubectl --kubeconfig ${PRIMARY_KUBECONFIG}"
KC2=""  # set during preflight

# ─────────────────────────────────────────────────────────────────────────────
# Display helpers
# ─────────────────────────────────────────────────────────────────────────────

BOLD='\033[1m' DIM='\033[2m' CYAN='\033[36m' GREEN='\033[32m'
YELLOW='\033[33m' MAGENTA='\033[35m' RESET='\033[0m'

banner() {
    local width=72
    echo ""
    echo -e "${CYAN}${BOLD}$(printf '═%.0s' $(seq 1 $width))${RESET}"
    echo "$1"
    echo -e "${CYAN}${BOLD}$(printf '═%.0s' $(seq 1 $width))${RESET}"
    echo ""
}
section()  { echo ""; echo -e "${MAGENTA}${BOLD}── $1 ──${RESET}"; echo ""; }
info()     { echo -e "${DIM}$1${RESET}"; }
narrate()  { echo -e "${YELLOW}$1${RESET}"; }
success()  { echo -e "${GREEN}  ✓ $1${RESET}"; }

pause() {
    echo ""
    if [[ "${DEMO_AUTO:-}" == "1" ]]; then
        sleep "${DEMO_PAUSE:-2}"
    else
        echo -ne "${DIM}Press ENTER to continue...${RESET}"
        read -r
    fi
    echo ""
}

run_cmd() {
    echo -e "${BOLD}\$ $*${RESET}"
    "$@" 2>&1 || true
    echo ""
}

# Run the importer, prepending KUBECONFIG to the display when multi-cluster.
run_importer() {
    local kc_label=""
    if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
        kc_label="KUBECONFIG=~/.kube/config:~/.kube/config-secured-cluster "
    fi
    echo -e "${BOLD}\$ ${kc_label}$(basename "$IMPORTER") $*${RESET}"
    KUBECONFIG="$MULTI_KUBECONFIG" "$IMPORTER" "$@" 2>&1 || true
    echo ""
}

# ─────────────────────────────────────────────────────────────────────────────
# ACS helpers
# ─────────────────────────────────────────────────────────────────────────────

acs_api() {
    local method="$1" path="$2"; shift 2
    curl -sk "${CURL_AUTH[@]}" -X "$method" \
        -H "Content-Type: application/json" -H "Accept: application/json" \
        "${ACS_URL}${path}" "$@"
}

# Show ACS scan configs for the three demo SSBs.
# Uses per-config GET to get clusterStatus (not available in the list endpoint).
show_acs_demo_configs() {
    local list_json
    list_json=$(acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null)

    # Extract IDs + names for matching configs.
    local matches
    matches=$(echo "$list_json" | python3 -c "
import sys, json
data = json.load(sys.stdin)
targets = {'${SSB_CIS}', '${SSB_MODERATE}', '${SSB_PCI}'}
for c in sorted(data.get('configurations', []), key=lambda x: x['scanName']):
    if c['scanName'] in targets:
        print(c['id'] + ' ' + c['scanName'])
" 2>/dev/null)

    if [[ -z "$matches" ]]; then
        info "  (none found)"
        return
    fi

    echo "$matches" | while read -r cfg_id cfg_name; do
        local detail
        detail=$(acs_api GET "/v2/compliance/scan/configurations/${cfg_id}" 2>/dev/null)
        echo "$detail" | python3 -c "
import sys, json
c = json.load(sys.stdin)
sched  = c.get('scanConfig', {}).get('scanSchedule', {})
profs  = c.get('scanConfig', {}).get('profiles', [])
status = c.get('clusterStatus', [])
names  = [cs.get('clusterName','?') for cs in status]
h, m   = sched.get('hour','?'), sched.get('minute', 0)
itv    = sched.get('intervalType', '?').lower()
print(f\"  {c['scanName']}\")
print(f\"    schedule: {itv} at {h}:{m:02d}\")
print(f\"    profiles: {', '.join(profs)}\")
print(f\"    clusters: {', '.join(names) if names else '(resolving)'}\")
print(f\"    id:       {c['id']}\")
" 2>/dev/null
    done
}

# Show the current schedule of each demo SSB (from the K8s ScanSetting it references)
# vs what ACS thinks the schedule is.
show_schedule_gap() {
    echo -e "${BOLD}On the cluster(s) — source of truth:${RESET}"
    for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
        local setting sched1 sched2
        setting=$($KC1 get scansettingbinding "$ssb" -n "$CO_NS" \
            -o jsonpath='{.settingsRef.name}' 2>/dev/null || echo "?")
        sched1=$($KC1 get scansetting "$setting" -n "$CO_NS" \
            -o jsonpath='{.schedule}' 2>/dev/null || echo "?")
        if [[ $HAS_SECOND_CLUSTER -eq 1 && -n "$KC2" ]]; then
            local setting2
            setting2=$($KC2 get scansettingbinding "$ssb" -n "$CO_NS" \
                -o jsonpath='{.settingsRef.name}' 2>/dev/null || echo "?")
            sched2=$($KC2 get scansetting "$setting2" -n "$CO_NS" \
                -o jsonpath='{.schedule}' 2>/dev/null || echo "?")
            echo -e "  ${ssb}: cluster-1=${sched1}  cluster-2=${sched2}"
        else
            echo -e "  ${ssb}: ${sched1}"
        fi
    done
    echo ""
    echo -e "${BOLD}In ACS — what Central thinks:${RESET}"
    acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
targets = {'${SSB_CIS}', '${SSB_MODERATE}', '${SSB_PCI}'}
for c in sorted(data.get('configurations', []), key=lambda x: x['scanName']):
    if c['scanName'] in targets:
        sched = c.get('scanConfig', {}).get('scanSchedule', {})
        h, m  = sched.get('hour','?'), sched.get('minute', 0)
        itv   = sched.get('intervalType', '?').lower()
        print(f\"  {c['scanName']}: {itv} at {h}:{m:02d}\")
" 2>/dev/null
    echo ""
}

# ─────────────────────────────────────────────────────────────────────────────
# CO resource management
# ─────────────────────────────────────────────────────────────────────────────

apply_co_resources() {
    local kc_cmd="$1" schedule="${2:-0 2 * * *}"
    $kc_cmd apply -f - << EOF
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: ${SCAN_SETTING}
  namespace: ${CO_NS}
  labels:
    app.kubernetes.io/created-by: co-importer-demo
schedule: "${schedule}"
roles: [worker, master]
rawResultStorage:
  rotation: 3
  size: 1Gi
---
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: ${SSB_CIS}
  namespace: ${CO_NS}
  labels:
    app.kubernetes.io/created-by: co-importer-demo
profiles:
  - name: ocp4-cis
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: ${SCAN_SETTING}
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
---
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: ${SSB_MODERATE}
  namespace: ${CO_NS}
  labels:
    app.kubernetes.io/created-by: co-importer-demo
profiles:
  - name: ocp4-moderate
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: ${SCAN_SETTING}
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
---
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSettingBinding
metadata:
  name: ${SSB_PCI}
  namespace: ${CO_NS}
  labels:
    app.kubernetes.io/created-by: co-importer-demo
profiles:
  - name: ocp4-pci-dss
    kind: Profile
    apiGroup: compliance.openshift.io/v1alpha1
settingsRef:
  name: ${SCAN_SETTING}
  kind: ScanSetting
  apiGroup: compliance.openshift.io/v1alpha1
EOF
}

# Patch all ScanSettings currently referenced by demo SSBs on a given cluster.
# Uses dynamic lookup so it works even after the adoption workflow has changed
# SSB settingsRef fields away from the shared SCAN_SETTING.
patch_all_demo_schedules() {
    local kc_cmd="$1" new_schedule="$2"
    local patched=()
    for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
        local setting
        setting=$($kc_cmd get scansettingbinding "$ssb" -n "$CO_NS" \
            -o jsonpath='{.settingsRef.name}' 2>/dev/null || echo "")
        if [[ -z "$setting" ]]; then continue; fi
        # Skip if already patched (multiple SSBs can share a ScanSetting).
        if [[ " ${patched[*]:-} " =~ " ${setting} " ]]; then continue; fi
        if $kc_cmd get scansetting "$setting" -n "$CO_NS" &>/dev/null 2>&1; then
            $kc_cmd patch scansetting "$setting" -n "$CO_NS" \
                --type merge -p "{\"schedule\": \"${new_schedule}\"}" 2>/dev/null || true
            patched+=("$setting")
            echo -e "  patched ScanSetting ${BOLD}${setting}${RESET} → ${new_schedule}"
        fi
    done
}

# ─────────────────────────────────────────────────────────────────────────────
# Cleanup
# ─────────────────────────────────────────────────────────────────────────────

cleanup_demo_resources() {
    local quiet="${1:-}"
    if [[ -z "$quiet" ]]; then info "Cleaning up demo resources..."; fi

    cleanup_cluster() {
        local kc_cmd="$1"
        # SSBs
        for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
            $kc_cmd delete scansettingbinding "$ssb" -n "$CO_NS" \
                --ignore-not-found 2>/dev/null || true
        done
        # Shared ScanSetting + any SSB-named ScanSettings created by adoption.
        for name in "$SCAN_SETTING" "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
            $kc_cmd delete scansetting "$name" -n "$CO_NS" \
                --ignore-not-found 2>/dev/null || true
        done
    }

    cleanup_cluster "$KC1"
    if [[ $HAS_SECOND_CLUSTER -eq 1 && -n "$KC2" ]]; then cleanup_cluster "$KC2"; fi

    # Delete demo scan configs from ACS.
    acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null \
        | python3 -c "
import sys, json
data = json.load(sys.stdin)
for c in data.get('configurations', []):
    if c['scanName'].startswith('${DEMO_PREFIX}-'):
        print(c['id'])
" 2>/dev/null | while read -r cfg_id; do
        acs_api DELETE "/v2/compliance/scan/configurations/$cfg_id" >/dev/null 2>&1 || true
    done

    if [[ -z "$quiet" ]]; then success "Done"; fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Preflight
# ─────────────────────────────────────────────────────────────────────────────

preflight() {
    if [[ ! -x "$IMPORTER" ]]; then
        echo "ERROR: importer binary not found at ${IMPORTER}. Run 'make build'." >&2; exit 1
    fi
    if ! $KC1 cluster-info &>/dev/null; then
        echo "ERROR: kubectl cannot reach the primary cluster" >&2; exit 1
    fi
    if ! acs_api GET "/v1/metadata" 2>/dev/null | python3 -c "import sys,json;json.load(sys.stdin)" &>/dev/null; then
        echo "ERROR: cannot reach ACS at ${ACS_URL}" >&2; exit 1
    fi

    if [[ -f "$SECOND_KUBECONFIG" ]]; then
        if KUBECONFIG="$SECOND_KUBECONFIG" kubectl cluster-info &>/dev/null 2>&1; then
            if KUBECONFIG="$SECOND_KUBECONFIG" kubectl \
                    get crd scansettingbindings.compliance.openshift.io &>/dev/null 2>&1; then
                HAS_SECOND_CLUSTER=1
                KC2="kubectl --kubeconfig ${SECOND_KUBECONFIG}"
                MULTI_KUBECONFIG="${PRIMARY_KUBECONFIG}:${SECOND_KUBECONFIG}"
            fi
        fi
    fi
}

trap 'echo ""; cleanup_demo_resources' EXIT

# ═════════════════════════════════════════════════════════════════════════════
#  DEMO
# ═════════════════════════════════════════════════════════════════════════════

preflight
cleanup_demo_resources quiet

clear
banner "CO → ACS Scheduled Scan Importer — Interactive Demo"

narrate "Problem: your team uses Compliance Operator to schedule automated"
narrate "scans across your clusters. ACS Central has no knowledge of these"
narrate "schedules. You'd need to manually recreate each scan in the ACS UI"
narrate "— for every cluster you manage."
echo ""
narrate "Solution: this tool reads CO ScanSettingBindings and creates matching"
narrate "ACS compliance scan configurations automatically — with idempotency,"
narrate "dry-run preview, drift detection, and multi-cluster support."
echo ""

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    narrate "What we'll cover (multi-cluster demo):"
    narrate "  1. Create identical CO resources on TWO clusters"
    narrate "  2. Dry-run — what would be created?"
    narrate "  3. Apply — 3 ACS scan configs, each targeting BOTH clusters"
    narrate "  4. Idempotency — second run is a no-op"
    narrate "  5. Cross-cluster conflict — importer detects schedule divergence"
    narrate "  6. Drift — CO schedules changed on all clusters"
    narrate "  7. Skip mode — safe default: do not overwrite"
    narrate "  8. Overwrite mode — re-sync ACS to cluster schedules"
else
    narrate "What we'll cover:"
    narrate "  1. Create demo CO resources (ScanSetting + 3 SSBs)"
    narrate "  2. Dry-run preview — what would be created?"
    narrate "  3. Apply — create the ACS scan configs"
    narrate "  4. Idempotency — second run is a no-op"
    narrate "  5. Drift — CO schedule changed directly on the cluster"
    narrate "  6. Skip mode — safe default: do not overwrite"
    narrate "  7. Overwrite mode — re-sync ACS to cluster schedule"
fi
echo ""
info "Primary cluster: $($KC1 config current-context 2>/dev/null || echo '?')"
if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    info "Second cluster:  $(KUBECONFIG="$SECOND_KUBECONFIG" kubectl config current-context 2>/dev/null || echo '?')"
fi
info "ACS endpoint:    ${ACS_URL}"
info "CO namespace:    ${CO_NS}"
info "Report:          ${REPORT_JSON}"

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Step 1: Create CO resources
# ─────────────────────────────────────────────────────────────────────────────

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    banner "Step 1: Create Demo CO Resources on Both Clusters"
    narrate "We'll create identical ScanSettings and SSBs on BOTH clusters."
    narrate "Same names, same schedule (02:00), same profiles."
    narrate ""
    narrate "The importer will detect the matching SSBs and merge them into a"
    narrate "single ACS scan config targeting both clusters at once."
    narrate ""
    narrate "Without this tool: 6 manual entries in ACS (3 SSBs × 2 clusters)."
    narrate "With this tool: 3 ACS scan configs, each covering the whole fleet."
else
    banner "Step 1: Create Demo CO Resources"
    narrate "A ScanSetting (daily at 02:00) and three SSBs, each targeting a"
    narrate "different compliance profile. In practice these already exist on"
    narrate "the cluster — we create them here for a clean demo."
fi

pause

section "cluster-1: applying ScanSetting + 3 SSBs"
info "  schedule: 0 2 * * *   profiles: ocp4-cis | ocp4-moderate | ocp4-pci-dss"
echo ""
apply_co_resources "$KC1" "0 2 * * *"

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    section "cluster-2: applying same resources"
    apply_co_resources "$KC2" "0 2 * * *"
fi

section "verify: CO resources on cluster-1"
$KC1 get scansettingbindings,scansettings \
    -n "$CO_NS" -l "app.kubernetes.io/created-by=co-importer-demo" \
    -o custom-columns='KIND:.kind,NAME:.metadata.name,SCHEDULE:.schedule,PROFILES:.profiles[*].name' \
    --no-headers 2>/dev/null || true

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    section "verify: CO resources on cluster-2"
    $KC2 get scansettingbindings,scansettings \
        -n "$CO_NS" -l "app.kubernetes.io/created-by=co-importer-demo" \
        -o custom-columns='KIND:.kind,NAME:.metadata.name,SCHEDULE:.schedule,PROFILES:.profiles[*].name' \
        --no-headers 2>/dev/null || true
fi

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Step 2: Dry run
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 2: Dry Run"

narrate "Preview the plan with --dry-run before touching ACS."
narrate "--report-json writes a machine-readable report, useful for CI gating."
if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    narrate ""
    narrate "With KUBECONFIG pointing to both clusters, the importer discovers"
    narrate "both contexts, resolves their ACS cluster IDs, and plans the merge."
fi

pause

run_importer "${IMPORTER_FLAGS[@]}" --dry-run --report-json "$REPORT_JSON"

section "report summary"
python3 -c "
import json
with open('${REPORT_JSON}') as f:
    r = json.load(f)
c = r.get('counts', {})
print(f'  discovered={c.get(\"discovered\",\"?\")}  create={c.get(\"create\",\"?\")}  skip={c.get(\"skip\",\"?\")}  failed={c.get(\"failed\",\"?\")}')
for item in r.get('items', []):
    name   = item.get('source', {}).get('bindingName','?')
    action = item.get('action','?')
    print(f'  {name}: {action}')
" 2>/dev/null || info "(check ${REPORT_JSON} directly)"

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Step 3: Apply
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 3: Apply"

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    narrate "One ACS scan config per unique SSB name, targeting BOTH clusters."
    narrate "Single policy definition — entire fleet covered."
else
    narrate "One ACS scan config per SSB, mapping the CO cron schedule to ACS format."
fi

pause

run_importer "${IMPORTER_FLAGS[@]}" --report-json "$REPORT_JSON"

section "verify: ACS scan configs"
show_acs_demo_configs

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Step 4: Idempotency
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 4: Idempotency"

narrate "Run again with no changes. All three should be skipped."

pause

run_importer "${IMPORTER_FLAGS[@]}"

narrate "All demo SSBs skipped. Matches by scanName — never creates duplicates."
narrate "Safe to run on a cron schedule."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Step 5 (multi-cluster only): Cross-cluster schedule conflict
# ─────────────────────────────────────────────────────────────────────────────

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    banner "Step 5: Cross-Cluster Schedule Conflict"

    narrate "A team on cluster-1 changes their scan schedule (GitOps, kubectl patch)."
    narrate "Cluster-2 keeps the old schedule. Same SSB name, different schedules."
    narrate ""
    narrate "The importer cannot determine which schedule is 'correct' — it refuses"
    narrate "to create or update a split-brain configuration."

    pause

    section "patching cluster-1 only: 02:00 → 06:00"
    patch_all_demo_schedules "$KC1" "0 6 * * *"
    echo ""
    echo -e "  cluster-1: ${BOLD}0 6 * * *${RESET} (06:00)"
    echo -e "  cluster-2: ${BOLD}0 2 * * *${RESET} (02:00)  ← unchanged"

    section "running importer — expects schedule mismatch conflicts"
    run_importer "${IMPORTER_FLAGS[@]}"

    narrate "Schedule mismatches detected — affected SSBs skipped."
    narrate "Fix: unify schedules across all clusters, then re-run."

    pause

    section "restoring cluster-1 to 02:00"
    patch_all_demo_schedules "$KC1" "0 2 * * *"
    narrate "Both clusters back to 02:00 — consistent again."

    pause

    DRIFT_STEP=6; SKIP_STEP=7; OVERWRITE_STEP=8
else
    DRIFT_STEP=5; SKIP_STEP=6; OVERWRITE_STEP=7
fi

# ─────────────────────────────────────────────────────────────────────────────
#  Drift
# ─────────────────────────────────────────────────────────────────────────────

banner "Step ${DRIFT_STEP}: Simulate Schedule Drift"

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    narrate "An operator edits the CO ScanSetting on all clusters — new schedule:"
    narrate "02:00 → 05:00. ACS has no watch on CO resources and stays at 02:00."
else
    narrate "An operator edits the CO ScanSetting directly on the cluster."
    narrate "ACS has no watch on CO resources and stays at the old schedule."
fi
narrate "Silent drift."

pause

section "before"
show_schedule_gap

section "patching all clusters: 02:00 → 05:00"
patch_all_demo_schedules "$KC1" "0 5 * * *"
if [[ $HAS_SECOND_CLUSTER -eq 1 && -n "$KC2" ]]; then
    patch_all_demo_schedules "$KC2" "0 5 * * *"
fi

section "after — the gap"
show_schedule_gap
narrate "Cluster(s): 05:00.  ACS: 02:00.  Drift is live."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Skip mode
# ─────────────────────────────────────────────────────────────────────────────

banner "Step ${SKIP_STEP}: Default Mode — Drift Preserved"

narrate "Without --overwrite-existing the importer sees the names already in ACS"
narrate "and leaves them alone. Never silently modifies production configs."

pause

run_importer "${IMPORTER_FLAGS[@]}"

narrate "Skipped — drift untouched. No surprises."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Overwrite mode
# ─────────────────────────────────────────────────────────────────────────────

banner "Step ${OVERWRITE_STEP}: Overwrite Mode — Drift Resolved"

narrate "--overwrite-existing: re-read all CO schedules and PUT to ACS."
narrate "This is the reconcile path — run it whenever CO resources change."

pause

run_importer "${IMPORTER_FLAGS[@]}" --overwrite-existing

section "verify: cluster(s) and ACS now agree"
show_schedule_gap
narrate "In sync at 05:00."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Done — EXIT trap handles cleanup
# ─────────────────────────────────────────────────────────────────────────────

banner "Demo Complete"

if [[ $HAS_SECOND_CLUSTER -eq 1 ]]; then
    echo -e "  ${GREEN}1.${RESET} Created identical CO resources on 2 clusters"
    echo -e "  ${GREEN}2.${RESET} Dry-run: 2 cluster sources, 3 merges planned"
    echo -e "  ${GREEN}3.${RESET} Applied: 3 ACS scan configs, each targeting both clusters"
    echo -e "  ${GREEN}4.${RESET} Idempotency: no duplicates on re-run"
    echo -e "  ${GREEN}5.${RESET} Cross-cluster conflict: schedule divergence detected"
    echo -e "  ${GREEN}6.${RESET} Drift: CO schedule changed, ACS unaware"
    echo -e "  ${GREEN}7.${RESET} Skip: existing configs preserved (safe default)"
    echo -e "  ${GREEN}8.${RESET} Overwrite: ACS re-synced to cluster schedules"
else
    echo -e "  ${GREEN}1.${RESET} Created ScanSetting + 3 SSBs"
    echo -e "  ${GREEN}2.${RESET} Dry-run with JSON report"
    echo -e "  ${GREEN}3.${RESET} Applied: 3 ACS scan configs"
    echo -e "  ${GREEN}4.${RESET} Idempotency: no duplicates on re-run"
    echo -e "  ${GREEN}5.${RESET} Drift: CO schedule changed, ACS unaware"
    echo -e "  ${GREEN}6.${RESET} Skip: existing configs preserved (safe default)"
    echo -e "  ${GREEN}7.${RESET} Overwrite: ACS re-synced to cluster schedule"
fi
echo ""
echo -e "  ${DIM}Mode: $([[ $HAS_SECOND_CLUSTER -eq 1 ]] && echo 'multi-cluster' || echo 'single-cluster')${RESET}"
echo -e "  ${DIM}Report: ${REPORT_JSON}${RESET}"
echo ""

# EXIT trap handles cleanup.
