// Package cofetch fetches Compliance Operator resources from Kubernetes
// using the dynamic client.
package cofetch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stackrox/co-importer/internal/models"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	ssbGVR = schema.GroupVersionResource{
		Group:    "compliance.openshift.io",
		Version:  "v1alpha1",
		Resource: "scansettingbindings",
	}
	ssGVR = schema.GroupVersionResource{
		Group:    "compliance.openshift.io",
		Version:  "v1alpha1",
		Resource: "scansettings",
	}
)

// COClient abstracts operations on Compliance Operator resources.
type COClient interface {
	ListScanSettingBindings(ctx context.Context) ([]models.ScanSettingBinding, error)
	GetScanSetting(ctx context.Context, namespace, name string) (*models.ScanSetting, error)
	PatchSSBSettingsRef(ctx context.Context, namespace, ssbName, newSettingsRefName string) error
}

// client is the concrete implementation using the Kubernetes dynamic client.
type client struct {
	dynClient     dynamic.Interface
	namespace     string
	allNamespaces bool
}

// NewClient creates a COClient for the given kubeconfig context.
// IMP-CLI-003: each kubeconfig file is loaded independently.
func NewClient(kubeconfigPath, contextName, namespace string, allNamespaces bool) (COClient, error) {
	rules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfigPath,
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("building kubeconfig for context %q (file %q): %w", contextName, kubeconfigPath, err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	return &client{
		dynClient:     dynClient,
		namespace:     namespace,
		allNamespaces: allNamespaces,
	}, nil
}

// ListScanSettingBindings fetches all ScanSettingBindings in the configured scope.
func (c *client) ListScanSettingBindings(ctx context.Context) ([]models.ScanSettingBinding, error) {
	ns := c.namespace
	if c.allNamespaces {
		ns = ""
	}

	list, err := c.dynClient.Resource(ssbGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing ScanSettingBindings: %w", err)
	}

	var result []models.ScanSettingBinding
	for _, item := range list.Items {
		ssb := models.ScanSettingBinding{
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
		}

		// CO ScanSettingBinding has fields at top level (not under spec):
		// profiles[].name, profiles[].kind, settingsRef.name

		// Parse settingsRef
		if settingsRef, ok := item.Object["settingsRef"].(map[string]interface{}); ok {
			if name, ok := settingsRef["name"].(string); ok {
				ssb.ScanSettingName = name
			}
		}

		// Parse profiles
		if profiles, ok := item.Object["profiles"].([]interface{}); ok {
			for _, p := range profiles {
				if pm, ok := p.(map[string]interface{}); ok {
					ref := models.ProfileRef{}
					if name, ok := pm["name"].(string); ok {
						ref.Name = name
					}
					if kind, ok := pm["kind"].(string); ok {
						ref.Kind = kind
					}
					ssb.Profiles = append(ssb.Profiles, ref)
				}
			}
		}

		result = append(result, ssb)
	}

	return result, nil
}

// GetScanSetting fetches a specific ScanSetting by name.
func (c *client) GetScanSetting(ctx context.Context, namespace, name string) (*models.ScanSetting, error) {
	item, err := c.dynClient.Resource(ssGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting ScanSetting %s/%s: %w", namespace, name, err)
	}

	ss := &models.ScanSetting{
		Name:      item.GetName(),
		Namespace: item.GetNamespace(),
	}

	// Schedule is at top level in ScanSetting
	if schedule, ok := item.Object["schedule"].(string); ok {
		ss.Schedule = schedule
	}

	return ss, nil
}

// PatchSSBSettingsRef patches the SSB's settingsRef.name to a new value.
func (c *client) PatchSSBSettingsRef(ctx context.Context, namespace, ssbName, newSettingsRefName string) error {
	patch := map[string]interface{}{
		"settingsRef": map[string]interface{}{
			"name": newSettingsRefName,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling patch: %w", err)
	}

	_, err = c.dynClient.Resource(ssbGVR).Namespace(namespace).Patch(
		ctx, ssbName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patching SSB %s/%s settingsRef: %w", namespace, ssbName, err)
	}
	return nil
}

// KubeReaderFromDynamic wraps a dynamic.Interface to implement discover.KubeReader.
type KubeReaderFromDynamic struct {
	DynClient dynamic.Interface
}

// GetConfigMapData reads a key from a ConfigMap.
func (r *KubeReaderFromDynamic) GetConfigMapData(namespace, name, key string) (string, error) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	cm, err := r.DynClient.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if data, ok := cm.Object["data"].(map[string]interface{}); ok {
		if val, ok := data[key].(string); ok {
			return val, nil
		}
	}
	return "", fmt.Errorf("key %q not found in ConfigMap %s/%s", key, namespace, name)
}

// GetClusterVersionID reads spec.clusterID from a ClusterVersion resource.
func (r *KubeReaderFromDynamic) GetClusterVersionID(name string) (string, error) {
	gvr := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusterversions",
	}
	cv, err := r.DynClient.Resource(gvr).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if spec, ok := cv.Object["spec"].(map[string]interface{}); ok {
		if clusterID, ok := spec["clusterID"].(string); ok {
			return clusterID, nil
		}
	}
	return "", fmt.Errorf("spec.clusterID not found in ClusterVersion %q", name)
}

// GetSecretData reads a key from a Secret.
func (r *KubeReaderFromDynamic) GetSecretData(namespace, name, key string) (string, error) {
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	secret, err := r.DynClient.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	// Secrets can have data (base64-decoded by the API) or stringData
	if data, ok := secret.Object["data"].(map[string]interface{}); ok {
		if val, ok := data[key].(string); ok {
			// The dynamic client returns base64-decoded data as string
			return val, nil
		}
	}
	if data, ok := secret.Object["stringData"].(map[string]interface{}); ok {
		if val, ok := data[key].(string); ok {
			return val, nil
		}
	}
	return "", fmt.Errorf("key %q not found in Secret %s/%s", key, namespace, name)
}

// NewDynamicClientForContext creates a dynamic.Interface for a specific kubeconfig file and context.
func NewDynamicClientForContext(kubeconfigPath, contextName string) (dynamic.Interface, error) {
	rules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfigPath,
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("building kubeconfig for context %q: %w", contextName, err)
	}

	return dynamic.NewForConfig(config)
}

// ContextsFromKubeconfig returns all context names from a kubeconfig file.
func ContextsFromKubeconfig(kubeconfigPath string) ([]string, error) {
	rules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfigPath,
	}
	config, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig %q: %w", kubeconfigPath, err)
	}

	var contexts []string
	for name := range config.Contexts {
		contexts = append(contexts, name)
	}
	return contexts, nil
}

// KubeconfigFiles returns the list of kubeconfig files from KUBECONFIG env var
// (colon-separated) or the default ~/.kube/config.
func KubeconfigFiles(getenv func(string) string) []string {
	kcEnv := getenv("KUBECONFIG")
	if kcEnv == "" {
		home := getenv("HOME")
		if home == "" {
			home = "."
		}
		return []string{home + "/.kube/config"}
	}
	return strings.Split(kcEnv, ":")
}
