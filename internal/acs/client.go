// Package acs defines the ACS client interface and related types.
package acs

import "context"

// ScanConfig represents an existing ACS compliance scan configuration.
type ScanConfig struct {
	ID       string
	ScanName string
}

// ACSClusterInfo represents a cluster managed by ACS Central.
type ACSClusterInfo struct {
	ID                string // ACS internal cluster ID
	Name              string // cluster display name
	ProviderClusterID string // from providerMetadata.cluster.id (e.g. OpenShift cluster ID)
}

// Client is the interface for interacting with the ACS compliance scan configuration API.
type Client interface {
	// Preflight checks ACS connectivity and authentication.
	// IMP-CLI-015, IMP-CLI-016
	Preflight(ctx context.Context) error

	ListScanConfigs(ctx context.Context) ([]ScanConfig, error)
	CreateScanConfig(ctx context.Context, payload interface{}) (string, error)
	UpdateScanConfig(ctx context.Context, id string, payload interface{}) error

	// ListClusters returns all clusters known to ACS Central.
	// Used by cluster ID discovery (IMP-MAP-017, IMP-MAP-018).
	ListClusters(ctx context.Context) ([]ACSClusterInfo, error)
}

// HTTPError represents an HTTP error response from the ACS API.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return e.Message
}
