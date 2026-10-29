package cofetch

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// newFakeDynClient builds a fake dynamic client seeded with ScanSettingBinding objects.
func newFakeDynClient(t *testing.T, objs ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	t.Helper()
	gvrToListKind := map[schema.GroupVersionResource]string{
		ssbGVR: "ScanSettingBindingList",
	}
	runtimeObjs := make([]runtime.Object, len(objs))
	for i, o := range objs {
		runtimeObjs[i] = o
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, runtimeObjs...)
}

// TestListScanSettingBindings_PopulatesLabels verifies that labels on the K8s
// ScanSettingBinding object are read into models.ScanSettingBinding.Labels, which
// is required to distinguish ACS-managed SSBs from external ones.
// IMP-ADEXT-001, IMP-ADEXT-013
func TestListScanSettingBindings_PopulatesLabels(t *testing.T) {
	external := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "compliance.openshift.io/v1alpha1",
		"kind":       "ScanSettingBinding",
		"metadata": map[string]interface{}{
			"name":      "cis-weekly",
			"namespace": "openshift-compliance",
		},
	}}
	stackroxManaged := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "compliance.openshift.io/v1alpha1",
		"kind":       "ScanSettingBinding",
		"metadata": map[string]interface{}{
			"name":      "acs-weekly",
			"namespace": "openshift-compliance",
			"labels": map[string]interface{}{
				"app.kubernetes.io/name": "stackrox",
			},
		},
	}}

	dyn := newFakeDynClient(t, external, stackroxManaged)
	c := &client{dynClient: dyn, allNamespaces: true}

	bindings, err := c.ListScanSettingBindings(context.Background())
	if err != nil {
		t.Fatalf("ListScanSettingBindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(bindings))
	}

	byName := map[string]int{}
	for i, b := range bindings {
		byName[b.Name] = i
	}

	gotExternal := bindings[byName["cis-weekly"]]
	if len(gotExternal.Labels) != 0 {
		t.Errorf("expected no labels on cis-weekly, got %v", gotExternal.Labels)
	}

	gotManaged := bindings[byName["acs-weekly"]]
	if gotManaged.Labels["app.kubernetes.io/name"] != "stackrox" {
		t.Errorf("expected app.kubernetes.io/name=stackrox label on acs-weekly, got %v", gotManaged.Labels)
	}
}
