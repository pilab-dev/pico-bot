package waftools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupWHOIS_WithName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"network": map[string]interface{}{
				"name": "Google LLC",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "8.8.8.8", server.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, "Google LLC", provider)
}

func TestLookupWHOIS_WithHandleOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"network": map[string]interface{}{
				"handle": "AS15169",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "8.8.8.8", server.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, "AS15169 (AS15169)", provider)
}

func TestLookupWHOIS_WithStartAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"network": map[string]interface{}{
				"startAddress": "192.168.0.0/16",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "192.168.1.1", server.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, "192.168.0.0/16", provider)
}

func TestLookupWHOIS_NameAndHandle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"network": map[string]interface{}{
				"name":   "Google LLC",
				"handle": "AS15169",
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "8.8.8.8", server.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, "Google LLC (AS15169)", provider)
}

func TestLookupWHOIS_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "192.168.1.1", server.URL, nil)
	require.NoError(t, err)
	assert.Contains(t, provider, "Status:404")
}

func TestLookupWHOIS_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "192.168.1.1", server.URL, nil)
	require.Error(t, err)
	assert.Contains(t, provider, "Parse error")
}

func TestLookupWHOIS_EmptyNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider, _, err := LookupWHOIS(context.Background(), "192.168.1.1", server.URL, nil)
	require.NoError(t, err)
	assert.Equal(t, "Unknown", provider)
}

func TestLookupWHOIS_DefaultURL(t *testing.T) {
	// Skip this test as it requires network access and external RDAP service
	t.Skip("Skipping integration test that requires network access")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider, _, err := LookupWHOIS(ctx, "158.173.159.116", "", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, provider)
	assert.NotEqual(t, "Unknown", provider)
}

func TestParseLogEntry_Valid(t *testing.T) {
	// Test format without backticks (WAF log format)
	line := `2026-04-12T18:06:51+02:00 ERR Proxy error error="unsupported protocol scheme """ url=/`

	entry, err := ParseLogEntry(line)
	require.NoError(t, err)
	assert.Equal(t, "ERR", entry.Level)
	assert.Equal(t, "Proxy error", entry.Message)
	assert.Equal(t, "unsupported protocol scheme ", entry.Error)
	assert.Equal(t, "/", entry.URL)
}

func TestParseLogEntry_WithBackticks(t *testing.T) {
	// Test format with backticks (old format)
	line := "2026-04-12T18:06:51+02:00 ERR Proxy `error` error=\"test\" url=/test"

	entry, err := ParseLogEntry(line)
	require.NoError(t, err)
	assert.Equal(t, "ERR", entry.Level)
	assert.Equal(t, "Proxy", entry.Component)
	assert.Equal(t, "error", entry.Message)
}

func TestParseLogEntry_WithTraceID(t *testing.T) {
	line := `2026-04-12T18:06:51+02:00 ERR Failed to publish request event error="rpc error: code = Canceled desc = context canceled" trace_id=1776010011774-5sasasasbtasasas`

	entry, err := ParseLogEntry(line)
	require.NoError(t, err)
	assert.Equal(t, "ERR", entry.Level)
	assert.Equal(t, "Failed to publish request event", entry.Message)
	assert.Contains(t, entry.Error, "Canceled")
	assert.Equal(t, "1776010011774-5sasasasbtasasas", entry.TraceID)
}

func TestParseLogEntry_WAFRejection(t *testing.T) {
	line := `2026-04-12T21:24:09+02:00 WRN Request rejected by header validation error="Invalid host header" client_ip=185.177.126.133:18476 method=HEAD metric=invalid_host_header url=/`

	entry, err := ParseLogEntry(line)
	require.NoError(t, err)
	assert.Equal(t, "WRN", entry.Level)
	assert.Equal(t, "Request rejected by header validation", entry.Message)
	assert.Equal(t, "Invalid host header", entry.Error)
	assert.Equal(t, "185.177.126.133", entry.ClientIP)
	assert.Equal(t, "HEAD", entry.Method)
	assert.Equal(t, "invalid_host_header", entry.Metric)
	assert.Equal(t, "/", entry.URL)
}

func TestParseLogEntry_InfoLevel(t *testing.T) {
	line := "2026-04-12T17:59:03+02:00 INF WAF `initialized successfully` component=waf"

	entry, err := ParseLogEntry(line)
	require.NoError(t, err)
	assert.Equal(t, "INF", entry.Level)
	assert.Equal(t, "WAF", entry.Component)
	assert.Equal(t, "initialized successfully", entry.Message)
}

func TestParseLogEntry_Invalid(t *testing.T) {
	line := "invalid log line"

	entry, err := ParseLogEntry(line)
	require.Error(t, err)
	assert.Nil(t, entry)
}

func TestParseLogFile(t *testing.T) {
	content := `2026-04-12T18:06:51+02:00 ERR Proxy error error="test" url=/test
2026-04-12T17:59:03+02:00 INF WAF initialized successfully component=waf
invalid line
2026-04-12T18:06:51+02:00 ERR Failed error="fail" trace_id=abc123`

	tmpfile, err := os.CreateTemp("", "waf_test*.log")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(content)
	require.NoError(t, err)
	tmpfile.Close()

	entries, err := ParseLogFile(tmpfile.Name())
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	assert.Equal(t, "ERR", entries[0].Level)
	assert.Equal(t, "INF", entries[1].Level)
	assert.Equal(t, "abc123", entries[2].TraceID)
}
