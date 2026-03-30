// Package acs defines the ACS client interface and related types.
package acs

import "context"

// ScanConfig represents an existing ACS compliance scan configuration.
type ScanConfig struct {
	ID       string
	ScanName string
}

// Client is the interface for interacting with the ACS compliance scan configuration API.
type Client interface {
	ListScanConfigs(ctx context.Context) ([]ScanConfig, error)
	CreateScanConfig(ctx context.Context, payload interface{}) (string, error)
	UpdateScanConfig(ctx context.Context, id string, payload interface{}) error
}

// HTTPError represents an HTTP error response from the ACS API.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return e.Message
}
