// Package cofetch provides a Kubernetes dynamic client for fetching
// Compliance Operator resources (ScanSettingBindings, ScanSettings).
package cofetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/stackrox/co-importer/internal/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// GVRs for Compliance Operator resources.
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

// COClient abstracts Compliance Operator resource discovery.
type COClient interface {
	// ListScanSettingBindings returns all ScanSettingBindings in the configured namespace(s).
	ListScanSettingBindings(ctx context.Context) ([]models.ScanSettingBinding, error)
	// GetScanSetting fetches a named ScanSetting from the given namespace.
	GetScanSetting(ctx context.Context, namespace, name string) (*models.ScanSetting, error)
	// PatchSSBSettingsRef patches the settingsRef.name of a ScanSettingBinding.
	PatchSSBSettingsRef(ctx context.Context, namespace, ssbName, newSettingsRefName string) error
}

// k8sClient is the production implementation of COClient backed by a dynamic K8s client.
type k8sClient struct {
	dynClient     dynamic.Interface
	namespace     string // empty string means all namespaces
	allNamespaces bool
}

// NewClient creates a COClient using kubeconfig from the given path and context.
// If kubeConfigPath is empty, default loading rules apply.
// If contextName is empty, the current context is used.
func NewClient(kubeConfigPath, contextName, namespace string, allNamespaces bool) (COClient, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{}
	if kubeConfigPath != "" {
		loadingRules.ExplicitPath = kubeConfigPath
	} else {
		loadingRules = clientcmd.NewDefaultClientConfigLoadingRules()
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build kubeconfig: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	ns := namespace
	if allNamespaces {
		ns = ""
	}

	return &k8sClient{
		dynClient:     dynClient,
		namespace:     ns,
		allNamespaces: allNamespaces,
	}, nil
}

// NewClientFromDynamic creates a COClient from an existing dynamic.Interface.
// Useful for testing with fake clients.
func NewClientFromDynamic(dynClient dynamic.Interface, namespace string, allNamespaces bool) COClient {
	ns := namespace
	if allNamespaces {
		ns = ""
	}
	return &k8sClient{
		dynClient:     dynClient,
		namespace:     ns,
		allNamespaces: allNamespaces,
	}
}

// ListScanSettingBindings returns all ScanSettingBindings from the configured namespace(s).
func (c *k8sClient) ListScanSettingBindings(ctx context.Context) ([]models.ScanSettingBinding, error) {
	list, err := c.dynClient.Resource(ssbGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list ScanSettingBindings in namespace %q: %w", c.namespace, err)
	}

	result := make([]models.ScanSettingBinding, 0, len(list.Items))
	for _, item := range list.Items {
		ssb, parseErr := parseScanSettingBinding(item.Object)
		if parseErr != nil {
			// Skip malformed resources rather than aborting the whole list.
			continue
		}
		result = append(result, ssb)
	}
	return result, nil
}

// GetScanSetting fetches a named ScanSetting from the given namespace.
func (c *k8sClient) GetScanSetting(ctx context.Context, namespace, name string) (*models.ScanSetting, error) {
	obj, err := c.dynClient.Resource(ssGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get ScanSetting %q in namespace %q: %w", name, namespace, err)
	}

	ss, err := parseScanSetting(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("parse ScanSetting %q: %w", name, err)
	}
	return ss, nil
}

// PatchSSBSettingsRef patches the settingsRef.name of a ScanSettingBinding.
func (c *k8sClient) PatchSSBSettingsRef(ctx context.Context, namespace, ssbName, newSettingsRefName string) error {
	patch := map[string]interface{}{
		"settingsRef": map[string]interface{}{
			"name": newSettingsRefName,
		},
	}
	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	_, err = c.dynClient.Resource(ssbGVR).Namespace(namespace).Patch(
		ctx, ssbName, types.MergePatchType, patchData, metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch SSB %q settingsRef in namespace %q: %w", ssbName, namespace, err)
	}
	return nil
}

// parseScanSettingBinding converts an unstructured map into a models.ScanSettingBinding.
// ScanSettingBinding fields (profiles, settingsRef) are at the top level, not under spec.
func parseScanSettingBinding(obj map[string]interface{}) (models.ScanSettingBinding, error) {
	meta, _ := obj["metadata"].(map[string]interface{})
	name, _ := meta["name"].(string)
	namespace, _ := meta["namespace"].(string)

	if name == "" {
		return models.ScanSettingBinding{}, errors.New("ScanSettingBinding has no name")
	}

	// Parse profiles list.
	var profiles []models.ProfileRef
	if rawProfiles, ok := obj["profiles"].([]interface{}); ok {
		for _, rp := range rawProfiles {
			pm, ok := rp.(map[string]interface{})
			if !ok {
				continue
			}
			profiles = append(profiles, models.ProfileRef{
				Name: stringField(pm, "name"),
				Kind: stringField(pm, "kind"),
			})
		}
	}

	// Parse settingsRef.
	scanSettingName := ""
	if sr, ok := obj["settingsRef"].(map[string]interface{}); ok {
		scanSettingName = stringField(sr, "name")
	}

	return models.ScanSettingBinding{
		Namespace:       namespace,
		Name:            name,
		ScanSettingName: scanSettingName,
		Profiles:        profiles,
	}, nil
}

// parseScanSetting converts an unstructured map into a models.ScanSetting.
// The schedule field is at the top level in the ScanSetting resource.
func parseScanSetting(obj map[string]interface{}) (*models.ScanSetting, error) {
	meta, _ := obj["metadata"].(map[string]interface{})
	name, _ := meta["name"].(string)
	namespace, _ := meta["namespace"].(string)

	if name == "" {
		return nil, errors.New("ScanSetting has no name")
	}

	schedule, _ := obj["schedule"].(string)

	return &models.ScanSetting{
		Namespace: namespace,
		Name:      name,
		Schedule:  schedule,
	}, nil
}

// stringField safely extracts a string value from an unstructured map.
func stringField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}
