package discover

import (
	"errors"
	"strings"
	"testing"
)

// fakeKubeReader implements KubeReader for tests.
type fakeKubeReader struct {
	configMapData map[string]string // "ns/name/key" -> value
	configMapErr  map[string]error  // "ns/name/key" -> error
	clusterVerID  string
	clusterVerErr error
	secretData    map[string]string // "ns/name/key" -> value
	secretErr     map[string]error  // "ns/name/key" -> error
}

func (f *fakeKubeReader) GetConfigMapData(ns, name, key string) (string, error) {
	k := ns + "/" + name + "/" + key
	if err, ok := f.configMapErr[k]; ok {
		return "", err
	}
	return f.configMapData[k], nil
}

func (f *fakeKubeReader) GetClusterVersionID(name string) (string, error) {
	return f.clusterVerID, f.clusterVerErr
}

func (f *fakeKubeReader) GetSecretData(ns, name, key string) (string, error) {
	k := ns + "/" + name + "/" + key
	if err, ok := f.secretErr[k]; ok {
		return "", err
	}
	return f.secretData[k], nil
}

// fakeACSLister implements ACSClusterLister for tests.
type fakeACSLister struct {
	clusters []ACSCluster
	err      error
}

func (f *fakeACSLister) ListClusters() ([]ACSCluster, error) {
	return f.clusters, f.err
}

// IMP-MAP-016
func TestResolve_Method1_ConfigMap(t *testing.T) {
	kube := &fakeKubeReader{
		configMapData: map[string]string{
			"stackrox/admission-control/cluster-id": "cluster-from-configmap",
		},
	}
	d := &Discoverer{Kube: kube, ACS: &fakeACSLister{}}

	id, err := d.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "cluster-from-configmap" {
		t.Errorf("got %q, want %q", id, "cluster-from-configmap")
	}
}

// IMP-MAP-016: method 1 fails, IMP-MAP-017: method 2 succeeds via provider ID match
func TestResolve_Method2_ClusterVersion(t *testing.T) {
	kube := &fakeKubeReader{
		configMapErr: map[string]error{
			"stackrox/admission-control/cluster-id": errors.New("not found"),
		},
		clusterVerID: "ocp-provider-id-123",
	}
	acs := &fakeACSLister{
		clusters: []ACSCluster{
			{ID: "acs-id-abc", Name: "my-cluster", ProviderClusterID: "ocp-provider-id-123"},
		},
	}
	d := &Discoverer{Kube: kube, ACS: acs}

	id, err := d.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "acs-id-abc" {
		t.Errorf("got %q, want %q", id, "acs-id-abc")
	}
}

// IMP-MAP-017: ClusterVersion found but no ACS cluster matches provider ID
func TestResolve_Method2_NoProviderMatch(t *testing.T) {
	acs := &fakeACSLister{
		clusters: []ACSCluster{
			{ID: "acs-id-abc", Name: "other-cluster", ProviderClusterID: "different-id"},
		},
	}
	kube2 := &fakeKubeReader{
		configMapErr: map[string]error{
			"stackrox/admission-control/cluster-id": errors.New("not found"),
		},
		clusterVerID: "ocp-provider-id-123",
		secretErr: map[string]error{
			"stackrox/helm-effective-cluster-name/cluster-name": errors.New("not found"),
		},
	}
	d := &Discoverer{Kube: kube2, ACS: acs}

	_, err := d.Resolve()
	if err == nil {
		t.Fatal("expected error when no provider ID matches, got nil")
	}
}

// IMP-MAP-018: method 3 succeeds via cluster name match
func TestResolve_Method3_HelmSecret(t *testing.T) {
	secretKey := "stackrox/helm-effective-cluster-name/cluster-name"
	kube := &fakeKubeReader{
		configMapErr: map[string]error{
			"stackrox/admission-control/cluster-id": errors.New("not found"),
		},
		clusterVerErr: errors.New("not an OpenShift cluster"),
		secretData:    map[string]string{secretKey: "my-cluster-name"},
	}
	acs := &fakeACSLister{
		clusters: []ACSCluster{
			{ID: "acs-id-xyz", Name: "my-cluster-name"},
		},
	}
	d := &Discoverer{Kube: kube, ACS: acs}

	id, err := d.Resolve()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "acs-id-xyz" {
		t.Errorf("got %q, want %q", id, "acs-id-xyz")
	}
}

// IMP-MAP-016a: all methods fail → combined error
func TestResolve_AllMethodsFail(t *testing.T) {
	kube := &fakeKubeReader{
		configMapErr: map[string]error{
			"stackrox/admission-control/cluster-id": errors.New("configmap not found"),
		},
		clusterVerErr: errors.New("clusterversion not found"),
		secretErr: map[string]error{
			"stackrox/helm-effective-cluster-name/cluster-name": errors.New("secret not found"),
		},
	}
	d := &Discoverer{Kube: kube, ACS: &fakeACSLister{}}

	_, err := d.Resolve()
	if err == nil {
		t.Fatal("expected error when all methods fail, got nil")
	}

	// Error message should mention all three methods.
	errStr := err.Error()
	for _, method := range []string{"admission-control", "ClusterVersion", "helm-effective-cluster-name"} {
		if !strings.Contains(errStr, method) {
			t.Errorf("error should mention %q, got: %v", method, err)
		}
	}
}

// Method 2: ACS ListClusters fails
func TestResolve_Method2_ACSListError(t *testing.T) {
	kube := &fakeKubeReader{
		configMapErr: map[string]error{
			"stackrox/admission-control/cluster-id": errors.New("not found"),
		},
		clusterVerID: "ocp-id",
		secretErr: map[string]error{
			"stackrox/helm-effective-cluster-name/cluster-name": errors.New("not found"),
		},
	}
	acs := &fakeACSLister{err: errors.New("ACS API unavailable")}
	d := &Discoverer{Kube: kube, ACS: acs}

	_, err := d.Resolve()
	if err == nil {
		t.Fatal("expected error when ACS list fails, got nil")
	}
}
