package acs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── setAuth ───────────────────────────────────────────────────────────────────

func TestSetAuth_Token(t *testing.T) {
	c := &httpClient{token: "my-token"}
	req, _ := http.NewRequest(http.MethodGet, "http://x", http.NoBody)
	c.setAuth(req)

	got := req.Header.Get("Authorization")
	if got != "Bearer my-token" {
		t.Errorf("Authorization: got %q, want %q", got, "Bearer my-token")
	}
}

func TestSetAuth_BasicAuth(t *testing.T) {
	c := &httpClient{username: "admin", password: "secret"}
	req, _ := http.NewRequest(http.MethodGet, "http://x", http.NoBody)
	c.setAuth(req)

	user, pass, ok := req.BasicAuth()
	if !ok {
		t.Fatal("expected basic auth, got none")
	}
	if user != "admin" || pass != "secret" {
		t.Errorf("basic auth: got user=%q pass=%q, want admin/secret", user, pass)
	}
}

// Token takes priority over basic auth credentials.
func TestSetAuth_TokenTakesPriority(t *testing.T) {
	c := &httpClient{token: "tok", username: "admin", password: "secret"}
	req, _ := http.NewRequest(http.MethodGet, "http://x", http.NoBody)
	c.setAuth(req)

	got := req.Header.Get("Authorization")
	if !strings.HasPrefix(got, "Bearer ") {
		t.Errorf("expected Bearer token, got %q", got)
	}
}

func TestSetAuth_NoCredentials(t *testing.T) {
	c := &httpClient{}
	req, _ := http.NewRequest(http.MethodGet, "http://x", http.NoBody)
	c.setAuth(req)

	if auth := req.Header.Get("Authorization"); auth != "" {
		t.Errorf("expected no Authorization header, got %q", auth)
	}
}

// ── HTTP response handling via httptest ───────────────────────────────────────

func newTestClient(t *testing.T, handler http.HandlerFunc) (Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := NewHTTPClient(HTTPClientConfig{
		Endpoint:       srv.URL,
		Token:          "test-token",
		RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}
	return client, srv
}

func TestPreflight_Success(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"configurations": []interface{}{}})
	})

	if err := client.Preflight(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPreflight_Unauthorized(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})

	err := client.Preflight(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}

	var httpErr *HTTPError
	if !isHTTPError(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != 401 {
		t.Errorf("StatusCode: got %d, want 401", httpErr.StatusCode)
	}
	// IMP-CLI-016: auth remediation hint in message
	if !strings.Contains(httpErr.Message, "ROX_API_TOKEN") && !strings.Contains(httpErr.Message, "authentication failed") {
		t.Errorf("expected auth hint in error message, got: %q", httpErr.Message)
	}
}

func TestPreflight_Forbidden(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})

	err := client.Preflight(context.Background())
	var httpErr *HTTPError
	if !isHTTPError(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if httpErr.StatusCode != 403 {
		t.Errorf("StatusCode: got %d, want 403", httpErr.StatusCode)
	}
}

func TestPreflight_ServerError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})

	err := client.Preflight(context.Background())
	var httpErr *HTTPError
	if !isHTTPError(err, &httpErr) {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if httpErr.StatusCode != 500 {
		t.Errorf("StatusCode: got %d, want 500", httpErr.StatusCode)
	}
}

// ── ListScanConfigs: JSON parsing ─────────────────────────────────────────────

func TestListScanConfigs_ParsesResponse(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"configurations": []map[string]interface{}{
				{"id": "id-1", "scanName": "config-one"},
				{"id": "id-2", "scanName": "config-two"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	configs, err := client.ListScanConfigs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
	if configs[0].ID != "id-1" || configs[0].ScanName != "config-one" {
		t.Errorf("configs[0]: got %+v", configs[0])
	}
	if configs[1].ID != "id-2" || configs[1].ScanName != "config-two" {
		t.Errorf("configs[1]: got %+v", configs[1])
	}
}

func TestListScanConfigs_Empty(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"configurations": []interface{}{}})
	})

	configs, err := client.ListScanConfigs(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
}

// ── ListClusters: JSON parsing ────────────────────────────────────────────────

func TestListClusters_ParsesProviderMetadata(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"clusters": []map[string]interface{}{
				{
					"id":   "acs-id-1",
					"name": "my-cluster",
					"providerMetadata": map[string]interface{}{
						"cluster": map[string]interface{}{"id": "ocp-provider-id"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	clusters, err := client.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	c := clusters[0]
	if c.ID != "acs-id-1" {
		t.Errorf("ID: got %q, want %q", c.ID, "acs-id-1")
	}
	if c.Name != "my-cluster" {
		t.Errorf("Name: got %q, want %q", c.Name, "my-cluster")
	}
	if c.ProviderMetadataClusterID != "ocp-provider-id" {
		t.Errorf("ProviderMetadataClusterID: got %q, want %q", c.ProviderMetadataClusterID, "ocp-provider-id")
	}
}

// ── Authorization header forwarded ────────────────────────────────────────────

func TestDo_BearerTokenForwarded(t *testing.T) {
	var gotAuth string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]interface{}{"configurations": []interface{}{}})
	})

	client.Preflight(context.Background()) //nolint:errcheck // only testing that the auth header is forwarded, not the response

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, "Bearer test-token")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isHTTPError(err error, target **HTTPError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}
