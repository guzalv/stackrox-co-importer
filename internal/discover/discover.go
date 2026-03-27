// Package discover resolves the ACS cluster ID for a given Kubernetes context.
// IMP-MAP-016, IMP-MAP-017, IMP-MAP-018, IMP-MAP-016a
package discover

import (
	"fmt"
	"strings"
)

// KubeReader abstracts Kubernetes read operations for cluster discovery.
type KubeReader interface {
	// GetConfigMapData reads a key from a ConfigMap in the given namespace.
	GetConfigMapData(namespace, name, key string) (string, error)
	// GetClusterVersionID reads spec.clusterID from a ClusterVersion resource.
	GetClusterVersionID(name string) (string, error)
	// GetSecretData reads a key from a Secret in the given namespace.
	GetSecretData(namespace, name, key string) (string, error)
}

// ACSCluster represents an ACS cluster entry used for matching.
type ACSCluster struct {
	ID               string // ACS internal cluster ID
	Name             string // cluster name in ACS
	ProviderClusterID string // providerMetadata.cluster.id (OpenShift cluster ID)
}

// ACSClusterLister retrieves the list of clusters known to ACS Central.
type ACSClusterLister interface {
	ListClusters() ([]ACSCluster, error)
}

// Discoverer resolves the ACS cluster ID for a kubecontext by trying
// three methods in order: admission-control ConfigMap, ClusterVersion, helm Secret.
type Discoverer struct {
	Kube KubeReader
	ACS  ACSClusterLister
}

// methodError records a single discovery method's failure.
type methodError struct {
	Method string
	Err    error
}

// Resolve tries each discovery method in order and returns the ACS cluster ID.
// IMP-MAP-016: Try admission-control ConfigMap first.
// IMP-MAP-017: Fallback to ClusterVersion.
// IMP-MAP-018: Fallback to helm-effective-cluster-name Secret.
// IMP-MAP-016a: If all fail, return a combined error listing each method's failure.
func (d *Discoverer) Resolve() (string, error) {
	var failures []methodError

	// Method 1: admission-control ConfigMap (IMP-MAP-016)
	clusterID, err := d.Kube.GetConfigMapData("stackrox", "admission-control", "cluster-id")
	if err == nil && clusterID != "" {
		return clusterID, nil
	}
	if err != nil {
		failures = append(failures, methodError{Method: "admission-control ConfigMap", Err: err})
	}

	// Method 2: ClusterVersion (IMP-MAP-017)
	ocpClusterID, err := d.Kube.GetClusterVersionID("version")
	if err == nil && ocpClusterID != "" {
		// Match against ACS cluster list by providerMetadata.cluster.id
		clusters, listErr := d.ACS.ListClusters()
		if listErr != nil {
			failures = append(failures, methodError{Method: "ClusterVersion", Err: listErr})
		} else {
			for _, c := range clusters {
				if c.ProviderClusterID == ocpClusterID {
					return c.ID, nil
				}
			}
			failures = append(failures, methodError{
				Method: "ClusterVersion",
				Err:    fmt.Errorf("no ACS cluster with providerMetadata.cluster.id %q", ocpClusterID),
			})
		}
	} else if err != nil {
		failures = append(failures, methodError{Method: "ClusterVersion", Err: err})
	}

	// Method 3: helm-effective-cluster-name Secret (IMP-MAP-018)
	clusterName, err := d.Kube.GetSecretData("stackrox", "helm-effective-cluster-name", "cluster-name")
	if err == nil && clusterName != "" {
		clusters, listErr := d.ACS.ListClusters()
		if listErr != nil {
			failures = append(failures, methodError{Method: "helm-effective-cluster-name Secret", Err: listErr})
		} else {
			for _, c := range clusters {
				if c.Name == clusterName {
					return c.ID, nil
				}
			}
			failures = append(failures, methodError{
				Method: "helm-effective-cluster-name Secret",
				Err:    fmt.Errorf("no ACS cluster named %q", clusterName),
			})
		}
	} else if err != nil {
		failures = append(failures, methodError{Method: "helm-effective-cluster-name Secret", Err: err})
	}

	// IMP-MAP-016a: all methods failed
	var parts []string
	for _, f := range failures {
		parts = append(parts, fmt.Sprintf("%s: %v", f.Method, f.Err))
	}
	return "", fmt.Errorf("all cluster discovery methods failed: %s", strings.Join(parts, "; "))
}
