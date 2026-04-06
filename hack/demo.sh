#!/usr/bin/env bash
# demo.sh — Interactive demo of the CO → ACS scheduled scan importer.
#
# Prerequisites:
#   - kubectl configured with a context pointing to an OCP cluster with the
#     Compliance Operator installed
#   - ACS Central reachable from this machine
#   - ROX_ADMIN_PASSWORD or ROX_API_TOKEN set
#   - ROX_ENDPOINT set
#   - python3 in PATH
#   - The importer binary built: make build
#
# Usage:
#   ROX_ADMIN_PASSWORD=admin ROX_ENDPOINT=central.example.com ./hack/demo.sh
#
# Non-interactive mode (for testing):
#   DEMO_AUTO=1 DEMO_PAUSE=0 ROX_ADMIN_PASSWORD=admin ROX_ENDPOINT=... ./hack/demo.sh

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Configuration
# ─────────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMPORTER="${SCRIPT_DIR}/../bin/compliance-operator-importer"
CO_NS="${CO_NAMESPACE:-openshift-compliance}"
DEMO_CONTEXT="${DEMO_CONTEXT:-$(kubectl config current-context 2>/dev/null)}"

# Resolve ACS endpoint.
ACS_ENDPOINT="${ROX_ENDPOINT:?ROX_ENDPOINT must be set}"
ACS_URL="${ACS_ENDPOINT#http://}"
ACS_URL="${ACS_URL#https://}"
ACS_URL="https://${ACS_URL}"

# Auth for curl calls (mirrors the importer's own auth logic).
if [[ -n "${ROX_ADMIN_PASSWORD:-}" ]]; then
    CURL_AUTH=(-u "${ROX_ADMIN_USER:-admin}:${ROX_ADMIN_PASSWORD}")
elif [[ -n "${ROX_API_TOKEN:-}" ]]; then
    CURL_AUTH=(-H "Authorization: Bearer ${ROX_API_TOKEN}")
else
    echo "ERROR: set ROX_ADMIN_PASSWORD or ROX_API_TOKEN" >&2
    exit 1
fi

# Importer flags — scoped to the demo context so we only process demo SSBs.
IMPORTER_FLAGS=(
    --endpoint "$ACS_ENDPOINT"
    --insecure-skip-verify
    --context "$DEMO_CONTEXT"
)

# Demo resource names — prefixed to avoid collisions with real workloads.
DEMO_PREFIX="demo-import"
SSB_CIS="${DEMO_PREFIX}-cis-scan"
SSB_MODERATE="${DEMO_PREFIX}-moderate-scan"
SSB_PCI="${DEMO_PREFIX}-pci-dss-scan"
SCAN_SETTING="${DEMO_PREFIX}-setting"

# JSON report path.
REPORT_JSON="/tmp/co-acs-importer-demo.json"

# ─────────────────────────────────────────────────────────────────────────────
# Helpers
# ─────────────────────────────────────────────────────────────────────────────

BOLD='\033[1m'
DIM='\033[2m'
CYAN='\033[36m'
GREEN='\033[32m'
YELLOW='\033[33m'
RED='\033[31m'
MAGENTA='\033[35m'
RESET='\033[0m'

banner() {
    local width=72
    echo ""
    echo -e "${CYAN}${BOLD}$(printf '═%.0s' $(seq 1 $width))${RESET}"
    echo "$1"
    echo -e "${CYAN}${BOLD}$(printf '═%.0s' $(seq 1 $width))${RESET}"
    echo ""
}

section() {
    echo ""
    echo -e "${MAGENTA}${BOLD}── $1 ──${RESET}"
    echo ""
}

info()    { echo -e "${DIM}$1${RESET}"; }
narrate() { echo -e "${YELLOW}$1${RESET}"; }
success() { echo -e "${GREEN}  ✓ $1${RESET}"; }

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

# Print and run a command, showing its output.
run_cmd() {
    echo -e "${BOLD}\$ $*${RESET}"
    "$@" 2>&1 || true
    echo ""
}

acs_api() {
    local method="$1" path="$2"
    shift 2
    curl -sk "${CURL_AUTH[@]}" -X "$method" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json" \
        "${ACS_URL}${path}" "$@"
}

# ─────────────────────────────────────────────────────────────────────────────
# Cleanup — removes all demo resources
# ─────────────────────────────────────────────────────────────────────────────

cleanup_demo_resources() {
    local quiet="${1:-}"
    if [[ -z "$quiet" ]]; then info "Cleaning up demo resources..."; fi

    # Delete demo SSBs from the cluster.
    for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
        kubectl delete scansettingbinding "$ssb" -n "$CO_NS" --ignore-not-found 2>/dev/null || true
    done

    # Delete the demo ScanSettings (shared + any ACS-adopted ones named after SSBs).
    kubectl delete scansetting "${SCAN_SETTING}" -n "$CO_NS" --ignore-not-found 2>/dev/null || true
    for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
        kubectl delete scansetting "$ssb" -n "$CO_NS" --ignore-not-found 2>/dev/null || true
    done

    # Delete demo scan configs from ACS (query by prefix).
    local configs
    configs=$(acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null || true)
    echo "$configs" | python3 -c "
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
# Pre-flight checks
# ─────────────────────────────────────────────────────────────────────────────

preflight() {
    if [[ ! -x "$IMPORTER" ]]; then
        echo "ERROR: importer binary not found at ${IMPORTER}" >&2
        echo "       Run 'make build' first." >&2
        exit 1
    fi
    if ! kubectl cluster-info &>/dev/null; then
        echo "ERROR: kubectl cannot reach the cluster" >&2
        exit 1
    fi
    local meta
    meta=$(acs_api GET "/v1/metadata" 2>/dev/null) || true
    if ! echo "$meta" | python3 -c "import sys,json; json.load(sys.stdin)" &>/dev/null; then
        echo "ERROR: cannot reach ACS at ${ACS_URL}" >&2
        exit 1
    fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Trap — clean up on exit or interrupt
# ─────────────────────────────────────────────────────────────────────────────

trap 'echo ""; cleanup_demo_resources' EXIT

# ═════════════════════════════════════════════════════════════════════════════
#  DEMO START
# ═════════════════════════════════════════════════════════════════════════════

preflight
cleanup_demo_resources quiet  # silently remove leftovers from a previous run

clear
banner "CO → ACS Scheduled Scan Importer — Interactive Demo"

narrate "Problem: your team uses Compliance Operator to schedule automated"
narrate "scans on the cluster. ACS Central has no knowledge of these schedules."
narrate "You'd need to manually recreate each scan in the ACS UI."
echo ""
narrate "Solution: this tool reads CO ScanSettingBindings and creates matching"
narrate "ACS compliance scan configurations automatically — with idempotency,"
narrate "dry-run preview, and drift detection."
echo ""
narrate "What we'll cover:"
narrate "  1. Create demo CO resources (ScanSetting + 3 SSBs)"
narrate "  2. Dry-run preview — what would be created?"
narrate "  3. Apply — create the ACS scan configs"
narrate "  4. Idempotency — second run is a no-op"
narrate "  5. Drift — someone edits the CO schedule directly"
narrate "  6. Skip mode — importer detects but does not overwrite (safe default)"
narrate "  7. Overwrite mode — importer re-syncs ACS to the cluster schedule"
echo ""
info "Cluster context:  ${DEMO_CONTEXT}"
info "ACS:              ${ACS_URL}"
info "CO namespace:     ${CO_NS}"
info "Report output:    ${REPORT_JSON}"

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 1: Create demo ScanSetting and ScanSettingBindings
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 1: Create Demo CO Resources"

narrate "We'll create a ScanSetting (daily at 02:00) and three SSBs, each"
narrate "targeting a different compliance profile. In practice these already"
narrate "exist on the cluster — we're creating them here for a clean demo."

pause

section "Applying ScanSetting + 3 ScanSettingBindings"
info "Schedule: 0 2 * * * (daily at 02:00)"
info "Profiles: ocp4-cis | ocp4-moderate | ocp4-pci-dss"
echo ""

# All four resources in a single kubectl apply.
kubectl apply -f - << EOF
apiVersion: compliance.openshift.io/v1alpha1
kind: ScanSetting
metadata:
  name: ${SCAN_SETTING}
  namespace: ${CO_NS}
  labels:
    app.kubernetes.io/created-by: co-importer-demo
schedule: "0 2 * * *"
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

echo ""
section "Verify: demo resources on the cluster"
kubectl get scansettingbindings.compliance.openshift.io,scansettings.compliance.openshift.io \
    -n "$CO_NS" -l "app.kubernetes.io/created-by=co-importer-demo" \
    -o custom-columns='KIND:.kind,NAME:.metadata.name,SCHEDULE:.schedule,PROFILES:.profiles[*].name' \
    --no-headers

narrate ""
narrate "Three SSBs ready, all referencing the same ScanSetting (schedule: 0 2 * * *)."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 2: Dry run (with JSON report)
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 2: Dry Run"

narrate "Before touching ACS, preview the plan with --dry-run."
narrate "--report-json writes a machine-readable report — useful for CI pipelines"
narrate "that need to gate on what the importer would do."
narrate ""
narrate "The importer is scoped to --context ${DEMO_CONTEXT} so it only"
narrate "processes SSBs from this cluster, not others it might have access to."

pause

run_cmd "$IMPORTER" "${IMPORTER_FLAGS[@]}" --dry-run --report-json "$REPORT_JSON"

section "Report summary (JSON)"
python3 -c "
import json
with open('${REPORT_JSON}') as f:
    r = json.load(f)
counts = r.get('counts', {})
print(f'  discovered={counts.get(\"discovered\",\"?\")}  create={counts.get(\"create\",\"?\")}  skip={counts.get(\"skip\",\"?\")}  failed={counts.get(\"failed\",\"?\")}')
for item in r.get('items', []):
    name = item.get('source', {}).get('bindingName','?')
    action = item.get('action','?')
    cfg_id = item.get('acsScanConfigId','')
    note = f'  id={cfg_id}' if cfg_id else ''
    print(f'  {name}: {action}{note}')
" 2>/dev/null || info "(check ${REPORT_JSON} directly)"

narrate ""
narrate "Three creates planned for the demo SSBs; 'sedge' would be skipped"
narrate "(it already exists in ACS). No changes made yet."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 3: Apply — real import
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 3: Apply (Happy Path)"

narrate "Now for real. The importer creates one ACS scan config per SSB,"
narrate "mapping the CO cron schedule into the ACS schedule format."

pause

run_cmd "$IMPORTER" "${IMPORTER_FLAGS[@]}" --report-json "$REPORT_JSON"

section "Verify: scan configs now exist in ACS"
acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
targets = {'${SSB_CIS}', '${SSB_MODERATE}', '${SSB_PCI}'}
for c in data.get('configurations', []):
    if c['scanName'] in targets:
        sched = c.get('scanConfig', {}).get('scanSchedule', {})
        profiles = c.get('scanConfig', {}).get('profiles', [])
        print(f\"  {c['scanName']}\")
        print(f\"    schedule: {sched.get('intervalType','?')} {sched.get('hour','?')}:{sched.get('minute',0):02d}\")
        print(f\"    profiles: {', '.join(profiles)}\")
        print(f\"    id:       {c['id']}\")
" 2>/dev/null

narrate ""
narrate "All three scan configs created. ACS now knows about the CO schedules."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 4: Idempotency
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 4: Idempotency"

narrate "Run the importer again with no changes on the cluster."
narrate "It should detect all three as already existing and skip them."

pause

run_cmd "$IMPORTER" "${IMPORTER_FLAGS[@]}"

narrate "All demo SSBs skipped (plus any others in the namespace)."
narrate "The importer matches by scanName and never creates duplicates."
narrate "Safe to run on a cron schedule."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 5: Simulate schedule drift
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 5: Simulate Schedule Drift"

narrate "Real-world scenario: an operator edits the CO ScanSetting directly"
narrate "on the cluster (kubectl patch, GitOps apply, etc.)."
narrate "ACS has no webhook or watch on CO resources — it stays at the old"
narrate "schedule. The cluster runs at the new time. Silent drift."

pause

section "Who does each SSB reference right now?"
for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
    setting=$(kubectl get scansettingbinding "$ssb" -n "$CO_NS" \
        -o jsonpath='{.settingsRef.name}' 2>/dev/null || echo "?")
    schedule=$(kubectl get scansetting "$setting" -n "$CO_NS" \
        -o jsonpath='{.schedule}' 2>/dev/null || echo "?")
    echo -e "  ${ssb} → ${setting}  (${schedule})"
done
echo ""

# Patch all ScanSettings the demo SSBs currently reference.
section "Patching: 0 2 * * * → 0 5 * * * on every referenced ScanSetting"
patched=()
for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
    setting=$(kubectl get scansettingbinding "$ssb" -n "$CO_NS" \
        -o jsonpath='{.settingsRef.name}' 2>/dev/null || true)
    if [[ -n "$setting" ]] && [[ ! " ${patched[*]} " =~ " ${setting} " ]]; then
        kubectl patch scansetting "$setting" -n "$CO_NS" \
            --type merge -p '{"schedule": "0 5 * * *"}' 2>/dev/null
        echo -e "  Patched ScanSetting ${BOLD}${setting}${RESET}: schedule → 0 5 * * *"
        patched+=("$setting")
    fi
done

section "Cluster vs ACS: the gap"
echo -e "${BOLD}On the cluster (what actually runs):${RESET}"
for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
    setting=$(kubectl get scansettingbinding "$ssb" -n "$CO_NS" \
        -o jsonpath='{.settingsRef.name}' 2>/dev/null || echo "?")
    schedule=$(kubectl get scansetting "$setting" -n "$CO_NS" \
        -o jsonpath='{.schedule}' 2>/dev/null || echo "?")
    echo -e "  ${ssb}: ${schedule}"
done
echo ""
echo -e "${BOLD}In ACS (what Central thinks):${RESET}"
acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
targets = {'${SSB_CIS}', '${SSB_MODERATE}', '${SSB_PCI}'}
for c in data.get('configurations', []):
    if c['scanName'] in targets:
        sched = c.get('scanConfig', {}).get('scanSchedule', {})
        print(f\"  {c['scanName']}: {sched.get('intervalType','?')} {sched.get('hour','?')}:{sched.get('minute',0):02d}\")
" 2>/dev/null
echo ""
narrate "Cluster: 05:00. ACS: 02:00. Drift is live."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 6: Run without --overwrite-existing (drift preserved)
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 6: Default Mode — Drift Preserved"

narrate "Without --overwrite-existing the importer skips all three."
narrate "It sees the names already exist in ACS and leaves them alone."
narrate "This is the safe default: never silently modify production configs."

pause

run_cmd "$IMPORTER" "${IMPORTER_FLAGS[@]}"

narrate "Demo SSBs skipped — drift untouched. No surprises."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  STEP 7: Run with --overwrite-existing (drift resolved)
# ─────────────────────────────────────────────────────────────────────────────

banner "Step 7: Overwrite Mode — Drift Resolved"

narrate "--overwrite-existing: re-read all CO schedules and PUT to ACS."
narrate "This is the reconcile path: run it whenever CO resources change."

pause

run_cmd "$IMPORTER" "${IMPORTER_FLAGS[@]}" --overwrite-existing

section "Verify: ACS schedule matches the cluster"
echo -e "${BOLD}Cluster (source of truth):${RESET}"
for ssb in "$SSB_CIS" "$SSB_MODERATE" "$SSB_PCI"; do
    setting=$(kubectl get scansettingbinding "$ssb" -n "$CO_NS" \
        -o jsonpath='{.settingsRef.name}' 2>/dev/null || echo "?")
    schedule=$(kubectl get scansetting "$setting" -n "$CO_NS" \
        -o jsonpath='{.schedule}' 2>/dev/null || echo "?")
    echo -e "  ${ssb}: ${schedule}"
done
echo ""
echo -e "${BOLD}ACS (after overwrite):${RESET}"
acs_api GET "/v2/compliance/scan/configurations?pagination.limit=1000" 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
targets = {'${SSB_CIS}', '${SSB_MODERATE}', '${SSB_PCI}'}
for c in data.get('configurations', []):
    if c['scanName'] in targets:
        sched = c.get('scanConfig', {}).get('scanSchedule', {})
        print(f\"  {c['scanName']}: {sched.get('intervalType','?')} {sched.get('hour','?')}:{sched.get('minute',0):02d}\")
" 2>/dev/null
echo ""
narrate "Both show 05:00. In sync."

pause

# ─────────────────────────────────────────────────────────────────────────────
#  Done — EXIT trap handles cleanup automatically
# ─────────────────────────────────────────────────────────────────────────────

banner "Demo Complete"

echo -e "  ${GREEN}1.${RESET} Created CO resources — ScanSetting + 3 SSBs"
echo -e "  ${GREEN}2.${RESET} Dry-run with JSON report — preview before committing"
echo -e "  ${GREEN}3.${RESET} Apply — 3 ACS scan configs created from CO schedules"
echo -e "  ${GREEN}4.${RESET} Idempotency — safe to re-run, no duplicates"
echo -e "  ${GREEN}5.${RESET} Drift — CO schedule changed, ACS unaware"
echo -e "  ${GREEN}6.${RESET} Default skip — existing configs preserved"
echo -e "  ${GREEN}7.${RESET} Overwrite — ACS re-synced to cluster schedule"
echo ""
echo -e "  ${DIM}For multi-cluster: pass multiple --context flags.${RESET}"
echo -e "  ${DIM}Report: ${REPORT_JSON}${RESET}"
echo ""

# EXIT trap handles cleanup automatically.
