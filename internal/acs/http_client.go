// Package acs provides a concrete HTTP implementation of the Client interface.
package acs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// HTTPClientConfig holds connection parameters for the ACS HTTP client.
type HTTPClientConfig struct {
	Endpoint           string
	Token              string
	Username, Password string
	CACertFile         string
	InsecureSkipVerify bool
	RequestTimeout     time.Duration
}

// httpClient implements the Client interface over HTTP.
type httpClient struct {
	baseURL    string
	httpClient *http.Client
	token      string
	username   string
	password   string
}

// NewHTTPClient creates a new ACS HTTP client from the given config.
func NewHTTPClient(cfg HTTPClientConfig) (Client, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // user-controlled flag
	}

	if cfg.CACertFile != "" {
		caCert, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert file %q: %w", cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("CA cert file %q contains no valid PEM certificates", cfg.CACertFile)
		}
		tlsCfg.RootCAs = pool
	}

	timeout := cfg.RequestTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &httpClient{
		baseURL: strings.TrimRight(cfg.Endpoint, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
		token:    cfg.Token,
		username: cfg.Username,
		password: cfg.Password,
	}, nil
}

// setAuth adds authentication headers to a request.
// IMP-CLI-002: token → Bearer header; username+password → HTTP basic auth.
func (c *httpClient) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
		return
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
}

// do executes an HTTP request and returns the response body.
// Non-2xx responses are returned as *HTTPError.
func (c *httpClient) do(req *http.Request) ([]byte, error) {
	c.setAuth(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// IMP-CLI-016a: hint about TLS errors
		errStr := err.Error()
		if strings.Contains(errStr, "x509") || strings.Contains(errStr, "certificate") || strings.Contains(errStr, "tls") {
			return nil, fmt.Errorf("%w; hint: use --ca-cert-file to provide the CA certificate, or --insecure-skip-verify to skip TLS verification", err)
		}
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := string(body)
		// IMP-CLI-016: auth error remediation hints
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			msg = fmt.Sprintf("authentication failed (HTTP %d): %s; check that your ROX_API_TOKEN or ROX_ADMIN_PASSWORD is correct and has the required permissions", resp.StatusCode, msg)
		}
		return nil, &HTTPError{StatusCode: resp.StatusCode, Message: msg}
	}

	return body, nil
}

// Preflight probes ACS auth by listing scan configurations with limit=1.
// IMP-CLI-015
func (c *httpClient) Preflight(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v2/compliance/scan/configurations?pagination.limit=1", nil)
	if err != nil {
		return fmt.Errorf("building preflight request: %w", err)
	}
	_, err = c.do(req)
	return err
}

// ListScanConfigs retrieves all ACS scan configurations.
func (c *httpClient) ListScanConfigs(ctx context.Context) ([]ScanConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v2/compliance/scan/configurations?pagination.limit=1000", nil)
	if err != nil {
		return nil, fmt.Errorf("building list request: %w", err)
	}

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Configurations []struct {
			ID       string `json:"id"`
			ScanName string `json:"scanName"`
		} `json:"configurations"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing scan config list: %w", err)
	}

	configs := make([]ScanConfig, len(resp.Configurations))
	for i, c := range resp.Configurations {
		configs[i] = ScanConfig{ID: c.ID, ScanName: c.ScanName}
	}
	return configs, nil
}

// CreateScanConfig creates a new ACS scan configuration.
// Returns the ID of the created configuration.
func (c *httpClient) CreateScanConfig(ctx context.Context, payload interface{}) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v2/compliance/scan/configurations", strings.NewReader(string(data)))
	if err != nil {
		return "", fmt.Errorf("building create request: %w", err)
	}

	body, err := c.do(req)
	if err != nil {
		return "", err
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing create response: %w", err)
	}
	return resp.ID, nil
}

// UpdateScanConfig updates an existing ACS scan configuration.
func (c *httpClient) UpdateScanConfig(ctx context.Context, id string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.baseURL+"/v2/compliance/scan/configurations/"+id, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("building update request: %w", err)
	}

	_, err = c.do(req)
	return err
}

// ListClusters retrieves all clusters from ACS Central.
func (c *httpClient) ListClusters(ctx context.Context) ([]ACSClusterInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/clusters", nil)
	if err != nil {
		return nil, fmt.Errorf("building clusters request: %w", err)
	}

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Clusters []struct {
			ID               string `json:"id"`
			Name             string `json:"name"`
			Status           json.RawMessage `json:"status"`
			ProviderMetadata struct {
				Cluster struct {
					ID string `json:"id"`
				} `json:"cluster"`
			} `json:"providerMetadata"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing cluster list: %w", err)
	}

	clusters := make([]ACSClusterInfo, len(resp.Clusters))
	for i, c := range resp.Clusters {
		clusters[i] = ACSClusterInfo{
			ID:                        c.ID,
			Name:                      c.Name,
			ProviderMetadataClusterID: c.ProviderMetadata.Cluster.ID,
		}
	}
	return clusters, nil
}
