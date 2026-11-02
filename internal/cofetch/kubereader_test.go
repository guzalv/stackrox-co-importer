package cofetch

import (
	"encoding/base64"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// IMP-MAP-018a: Secret .data read through the dynamic (unstructured) client is
// base64-encoded by the API server and NOT decoded by the client. The fixture
// below encodes the value the same way the real API server serializes it,
// including a trailing newline that the stored value commonly carries. The
// reader MUST base64-decode and trim it.
func TestGetSecretData_DecodesAndTrims(t *testing.T) {
	secret := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      "helm-effective-cluster-name",
			"namespace": "stackrox",
		},
		"data": map[string]interface{}{
			"cluster-name": base64.StdEncoding.EncodeToString([]byte("ga-ocp-cron\n")),
		},
	}}

	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), secret)
	r := &KubeReaderFromDynamic{DynClient: dc}

	got, err := r.GetSecretData("stackrox", "helm-effective-cluster-name", "cluster-name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ga-ocp-cron" {
		t.Errorf("GetSecretData: got %q, want %q", got, "ga-ocp-cron")
	}
}

// IMP-MAP-018a: ConfigMap .data is plain text (only binaryData is base64). The
// reader MUST return it unchanged and MUST NOT base64-decode it.
func TestGetConfigMapData_PlainText(t *testing.T) {
	cm := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      "admission-control",
			"namespace": "stackrox",
		},
		"data": map[string]interface{}{
			"cluster-id": "plain-cluster-id-123",
		},
	}}

	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), cm)
	r := &KubeReaderFromDynamic{DynClient: dc}

	got, err := r.GetConfigMapData("stackrox", "admission-control", "cluster-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "plain-cluster-id-123" {
		t.Errorf("GetConfigMapData: got %q, want %q", got, "plain-cluster-id-123")
	}
}
