// Package acs provides the HTTP implementation of the ACS Client interface.
package acs

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// HTTPClientConfig configures the HTTP-based ACS client.
type HTTPClientConfig struct {
	Endpoint           string        // e.g. "https://central.example.com"
	Token              string        // Bearer token (mutually exclusive with Username/Password)
	Username           string        // HTTP Basic auth username
	Password           string        // HTTP Basic auth password
	CACertFile         string        // path to PEM CA certificate file
	InsecureSkipVerify bool          // skip TLS certificate verification
	RequestTimeout     time.Duration // per-request timeout; defaults to 30s
}

// httpClient is the concrete HTTP implementation of the Client interface.
type httpClient struct {
	client  *http.Client
	baseURL string
	cfg     HTTPClientConfig
}

// NewHTTPClient creates an HTTP-based Client from the given configuration.
func NewHTTPClient(cfg HTTPClientConfig) (*httpClient, error) {
	tlsCfg, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("acs: building TLS config: %w", err)
	}

	timeout := cfg.RequestTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &httpClient{
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
			Timeout: timeout,
		},
		baseURL: cfg.Endpoint,
		cfg:     cfg,
	}, nil
}

// buildTLSConfig constructs a tls.Config from the client configuration.
func buildTLSConfig(cfg HTTPClientConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // controlled by explicit CLI flag
	}

	if cfg.CACertFile != "" {
		pemData, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert file %q: %w", cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("no valid PEM certificates found in %q", cfg.CACertFile)
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}

// addAuth adds the appropriate Authorization header to the request.
// Token auth takes precedence over basic auth.
func (c *httpClient) addAuth(req *http.Request) error {
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		return nil
	}
	if c.cfg.Username != "" {
		req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
		return nil
	}
	return errors.New("acs: no authentication configured (set token or username/password)")
}

// Preflight checks ACS connectivity and auth by calling:
//
//	GET /v2/compliance/scan/configurations?pagination.limit=1
//
// Only HTTP 200 is treated as success; any other status returns an error.
// Implements IMP-CLI-015, IMP-CLI-016.
func (c *httpClient) Preflight(ctx context.Context) error {
	url := c.baseURL + "/v2/compliance/scan/configurations?pagination.limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("acs: preflight request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := c.addAuth(req); err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("acs: preflight failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return &HTTPError{
				StatusCode: resp.StatusCode,
				Message:    "acs: preflight: HTTP 401 Unauthorized - check ROX_API_TOKEN or ROX_ADMIN_PASSWORD",
			}
		case http.StatusForbidden:
			return &HTTPError{
				StatusCode: resp.StatusCode,
				Message:    "acs: preflight: HTTP 403 Forbidden - token lacks required permissions",
			}
		default:
			return &HTTPError{
				StatusCode: resp.StatusCode,
				Message:    fmt.Sprintf("acs: preflight: unexpected HTTP %d", resp.StatusCode),
			}
		}
	}
	return nil
}

// ListScanConfigs returns all existing scan configuration summaries.
//
//	GET /v2/compliance/scan/configurations?pagination.limit=1000
//
// Implements IMP-IDEM-001 (used to build the existing-name set).
func (c *httpClient) ListScanConfigs(ctx context.Context) ([]ScanConfig, error) {
	url := c.baseURL + "/v2/compliance/scan/configurations?pagination.limit=1000"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("acs: list request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := c.addAuth(req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("acs: list scan configurations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("acs: list scan configurations: HTTP %d", resp.StatusCode),
		}
	}

	var listResp struct {
		Configurations []struct {
			ID       string `json:"id"`
			ScanName string `json:"scanName"`
		} `json:"configurations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("acs: decoding list response: %w", err)
	}

	result := make([]ScanConfig, 0, len(listResp.Configurations))
	for _, cfg := range listResp.Configurations {
		result = append(result, ScanConfig{
			ID:       cfg.ID,
			ScanName: cfg.ScanName,
		})
	}
	return result, nil
}

// CreateScanConfig sends POST /v2/compliance/scan/configurations and returns
// the ID of the newly created configuration.
// Implements IMP-IDEM-001.
func (c *httpClient) CreateScanConfig(ctx context.Context, payload interface{}) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("acs: marshalling create payload: %w", err)
	}

	url := c.baseURL + "/v2/compliance/scan/configurations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("acs: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := c.addAuth(req); err != nil {
		return "", err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("acs: create scan configuration: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("POST /v2/compliance/scan/configurations returned HTTP %d: %s", resp.StatusCode, readBodySnippet(resp)),
		}
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", fmt.Errorf("acs: decoding create response: %w", err)
	}
	if created.ID == "" {
		return "", errors.New("acs: create response contained empty id")
	}
	return created.ID, nil
}

// UpdateScanConfig sends PUT /v2/compliance/scan/configurations/{id} to update
// an existing scan configuration.
// Implements IMP-IDEM-008.
func (c *httpClient) UpdateScanConfig(ctx context.Context, id string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("acs: marshalling update payload: %w", err)
	}

	url := c.baseURL + "/v2/compliance/scan/configurations/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("acs: update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := c.addAuth(req); err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("acs: update scan configuration: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("PUT /v2/compliance/scan/configurations/%s returned HTTP %d: %s", id, resp.StatusCode, readBodySnippet(resp)),
		}
	}

	return nil
}

// clusterStatus is used to parse the status field from a cluster response.
type clusterStatus struct {
	ProviderMetadata struct {
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
	} `json:"providerMetadata"`
}

// clusterResponse represents a single cluster in the ACS API response.
type clusterResponse struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Status clusterStatus `json:"status"`
}

// clustersListResponse matches GET /v1/clusters.
type clustersListResponse struct {
	Clusters []clusterResponse `json:"clusters"`
}

// ListClusters returns all clusters managed by ACS.
//
//	GET /v1/clusters
//
// Used for cluster ID discovery (IMP-MAP-017, IMP-MAP-018).
func (c *httpClient) ListClusters(ctx context.Context) ([]ACSClusterInfo, error) {
	url := c.baseURL + "/v1/clusters"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("acs: list clusters request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if err := c.addAuth(req); err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("acs: list clusters: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("acs: list clusters: HTTP %d", resp.StatusCode),
		}
	}

	var listResp clustersListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("acs: decoding clusters response: %w", err)
	}

	result := make([]ACSClusterInfo, 0, len(listResp.Clusters))
	for _, cl := range listResp.Clusters {
		result = append(result, ACSClusterInfo{
			ID:                cl.ID,
			Name:              cl.Name,
			ProviderClusterID: cl.Status.ProviderMetadata.Cluster.ID,
		})
	}
	return result, nil
}

// readBodySnippet reads up to 512 bytes from the response body for error reporting.
func readBodySnippet(resp *http.Response) string {
	const maxBytes = 512
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil || len(body) == 0 {
		return "(no response body)"
	}
	snippet := string(body)
	// Try to extract a cleaner message from JSON error responses.
	var parsed struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Message != "" {
			return parsed.Message
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	return snippet
}
