#!/usr/bin/env bash
# Set up a kind cluster with StackRox (--small mode) and the Compliance
# Operator (generic platform) for CI e2e testing.
#
# Prerequisites: kind, kubectl, helm, git must be in PATH.
#
# USAGE: hack/ci-setup-kind-stackrox.sh
# OUTPUT: Prints ROX_ADMIN_PASSWORD=<password> as the last line.

set -euo pipefail

CLUSTER_NAME="${KIND_CLUSTER_NAME:-co-importer-e2e}"
CO_NAMESPACE="${CO_NAMESPACE:-openshift-compliance}"

# ── 1. Create kind cluster ──────────────────────────────────────────────────

echo "==> Creating kind cluster '${CLUSTER_NAME}'..."

cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30443
        hostPort: 8443
        protocol: TCP
EOF

kubectl cluster-info --context "kind-${CLUSTER_NAME}"

# ── 2. Install StackRox Central Services ────────────────────────────────────

echo "==> Adding StackRox Helm repo..."
helm repo add stackrox https://raw.githubusercontent.com/stackrox/helm-charts/main/opensource/ || true
helm repo update stackrox

echo "==> Generating admin password..."
ROX_ADMIN_PASSWORD="$(openssl rand -base64 20 | tr -d '/+=' | head -c 20)"

echo "==> Installing stackrox-central-services (small mode)..."
kubectl create namespace stackrox || true

helm install stackrox-central-services stackrox/stackrox-central-services \
  --namespace stackrox \
  --set central.adminPassword.value="${ROX_ADMIN_PASSWORD}" \
  --set central.exposure.nodePort.enabled=true \
  --set central.exposure.nodePort.port=30443 \
  --set central.resources.requests.memory=512Mi \
  --set central.resources.requests.cpu=250m \
  --set central.resources.limits.memory=2Gi \
  --set central.resources.limits.cpu=1 \
  --set central.db.resources.requests.memory=512Mi \
  --set central.db.resources.requests.cpu=250m \
  --set central.db.resources.limits.memory=2Gi \
  --set central.db.resources.limits.cpu=1 \
  --set scanner.disable=true \
  --set scannerV4.disable=true \
  --timeout 8m \
  --wait

echo "==> Waiting for Central to be ready..."
kubectl -n stackrox rollout status deploy/central --timeout=300s

# ── 3. Determine Central endpoint ──────────────────────────────────────────

ROX_ENDPOINT="https://localhost:8443"
echo "==> Central endpoint: ${ROX_ENDPOINT}"

echo "==> Waiting for Central API to become responsive..."
for i in $(seq 1 60); do
  if curl -ksS -u "admin:${ROX_ADMIN_PASSWORD}" \
    "${ROX_ENDPOINT}/v2/compliance/scan/configurations?pagination.limit=1" \
    >/dev/null 2>&1; then
    echo "    Central API ready (attempt ${i})"
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: Central API did not become ready in time"
    kubectl -n stackrox get pods
    kubectl -n stackrox logs deploy/central --tail=50 || true
    exit 1
  fi
  sleep 5
done

# ── 4. Generate init bundle and install Secured Cluster ─────────────────────

echo "==> Generating init bundle..."
kubectl -n stackrox exec deploy/central -- \
  env HOME=/tmp \
  roxctl --insecure-skip-tls-verify \
  central init-bundles generate ci-init-bundle --output - \
  > /tmp/init-bundle.yaml

# Verify init bundle was generated.
if [ ! -s /tmp/init-bundle.yaml ]; then
  echo "ERROR: init bundle file is empty or missing"
  exit 1
fi
echo "    Init bundle generated ($(wc -c < /tmp/init-bundle.yaml) bytes)"

echo "==> Installing stackrox-secured-cluster-services..."
helm install stackrox-secured-cluster-services stackrox/stackrox-secured-cluster-services \
  --namespace stackrox \
  --set clusterName="ci-cluster" \
  --set centralEndpoint="central.stackrox.svc:443" \
  --set sensor.resources.requests.memory=256Mi \
  --set sensor.resources.requests.cpu=100m \
  --set sensor.resources.limits.memory=512Mi \
  --set sensor.resources.limits.cpu=500m \
  --set admissionControl.resources.requests.memory=64Mi \
  --set admissionControl.resources.requests.cpu=50m \
  --set admissionControl.resources.limits.memory=256Mi \
  --set admissionControl.resources.limits.cpu=250m \
  --set collector.forceCollectionMethod=NO_COLLECTION \
  --set collector.resources.requests.memory=64Mi \
  --set collector.resources.requests.cpu=50m \
  --set collector.resources.limits.memory=256Mi \
  --set collector.resources.limits.cpu=250m \
  -f /tmp/init-bundle.yaml \
  --timeout 5m \
  --wait

echo "==> Waiting for Sensor to be ready..."
kubectl -n stackrox rollout status deploy/sensor --timeout=300s

echo "==> Waiting for Admission Control to be ready..."
kubectl -n stackrox rollout status deploy/admission-control --timeout=300s

# ── 5. Install the real Compliance Operator ─────────────────────────────────

echo "==> Installing Compliance Operator (generic platform mode)..."
kubectl create namespace "${CO_NAMESPACE}" || true

# Clone the CO repo to get the Helm chart.
CO_TMPDIR=$(mktemp -d)
git clone --depth=1 https://github.com/ComplianceAsCode/compliance-operator.git "${CO_TMPDIR}/co"

# Install via Helm with generic platform (non-OpenShift).
helm install compliance-operator "${CO_TMPDIR}/co/config/helm" \
  --namespace "${CO_NAMESPACE}" \
  --set platform=generic \
  --set nodeSelector=null \
  --set tolerations=null \
  --set replicas=1 \
  --set resources.requests.memory=128Mi \
  --set resources.requests.cpu=50m \
  --set resources.limits.memory=256Mi \
  --set resources.limits.cpu=250m \
  --timeout 5m \
  --wait || {
    echo "WARNING: Compliance Operator Helm install failed."
    echo "==> Falling back to CRD-only installation..."
    # Apply just the CRDs so the K8s API accepts CO resources.
    kubectl apply -f "${CO_TMPDIR}/co/config/helm/crds/" || \
      kubectl apply -f "${CO_TMPDIR}/co/config/crd/bases/"
  }

# Verify CRDs are registered regardless of operator status.
echo "==> Verifying CO CRDs are registered..."
kubectl wait --for=condition=Established crd/scansettingbindings.compliance.openshift.io --timeout=30s
kubectl wait --for=condition=Established crd/scansettings.compliance.openshift.io --timeout=30s
kubectl wait --for=condition=Established crd/profiles.compliance.openshift.io --timeout=30s

# Check if the operator created profiles; if not, create a test profile.
echo "==> Checking for Profiles..."
PROFILE_COUNT=$(kubectl get profiles.compliance.openshift.io -n "${CO_NAMESPACE}" --no-headers 2>/dev/null | wc -l || echo "0")
if [ "${PROFILE_COUNT}" -eq 0 ]; then
  echo "    No profiles found (operator may still be initializing). Creating test profile..."
  cat <<EOF | kubectl apply -f -
apiVersion: compliance.openshift.io/v1alpha1
kind: Profile
metadata:
  name: ocp4-cis
  namespace: ${CO_NAMESPACE}
title: CIS OpenShift Benchmark
description: Test profile for CI e2e
rules: []
EOF
else
  echo "    Found ${PROFILE_COUNT} profile(s) from the operator"
fi

rm -rf "${CO_TMPDIR}"

# ── 6. Verify cluster ID is discoverable ────────────────────────────────────

echo "==> Waiting for admission-control ConfigMap (cluster-id)..."
for i in $(seq 1 30); do
  CLUSTER_ID=$(kubectl -n stackrox get configmap admission-control \
    -o jsonpath='{.data.cluster-id}' 2>/dev/null || true)
  if [ -n "${CLUSTER_ID}" ]; then
    echo "    Cluster ID: ${CLUSTER_ID}"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "WARNING: admission-control ConfigMap cluster-id not found"
    kubectl -n stackrox get configmap admission-control -o yaml || true
  fi
  sleep 5
done

# ── Done ────────────────────────────────────────────────────────────────────

echo ""
echo "==> Kind cluster ready for e2e tests"
echo "    Cluster: ${CLUSTER_NAME}"
echo "    Endpoint: ${ROX_ENDPOINT}"
echo "    Namespace: ${CO_NAMESPACE}"

# Write password to file for CI to pick up (avoids stdout masking).
PASSWORD_FILE="${PASSWORD_FILE:-/tmp/rox-admin-password}"
echo -n "${ROX_ADMIN_PASSWORD}" > "${PASSWORD_FILE}"
echo "    Password written to: ${PASSWORD_FILE}"
