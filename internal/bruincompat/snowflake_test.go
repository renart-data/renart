package bruincompat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	bruin "github.com/bruin-data/bruin/pkg/snowflake"
	v1 "github.com/snowflakedb/gosnowflake"
	v2 "github.com/snowflakedb/gosnowflake/v2"
	"github.com/stretchr/testify/require"
)

func TestSnowflakeBruinAuthenticationUsesV2Config(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	for _, auth := range []string{"password", "oauth", "jwt"} {
		t.Run(auth, func(t *testing.T) {
			config := bruin.Config{Account: "test-account", Username: "test-user", Region: "eu-central-1", Database: "DATABASE", Schema: "PUBLIC", Warehouse: "COMPUTE", Role: "ANALYST"}
			wantAuth := v2.AuthTypeSnowflake
			switch auth {
			case "password":
				config.Password = "synthetic-password"
			case "oauth":
				config.Token = "synthetic-oauth-token"
				wantAuth = v2.AuthTypeOAuth
			case "jwt":
				config.PrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
				wantAuth = v2.AuthTypeJwt
			}
			dsn, err := config.DSN()
			require.NoError(t, err)
			parsed, err := v2.ParseDSN(dsn)
			require.NoError(t, err)
			require.Equal(t, wantAuth, parsed.Authenticator)
			require.Equal(t, config.Database, parsed.Database)
			require.Equal(t, config.Schema, parsed.Schema)
			require.Equal(t, config.Role, parsed.Role)
			require.Equal(t, config.Warehouse, parsed.Warehouse)
			require.Equal(t, config.Password, parsed.Password)
			require.Equal(t, config.Token, parsed.Token)
			if auth == "jwt" {
				require.True(t, key.Equal(parsed.PrivateKey))
			}
		})
	}
}

func TestSnowflakeCompatibilityExecutesWithV2Contexts(t *testing.T) {
	type queryRequest struct {
		ID   string
		Body map[string]any
	}
	queries := make(chan queryRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/session/v1/login-request":
			io.WriteString(w, `{"success":true,"data":{"token":"synthetic-session-token","masterToken":"synthetic-master-token","sessionId":42,"parameters":[]}}`)
		case "/queries/v1/query-request":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode query: %v", err)
				http.Error(w, "invalid query", http.StatusBadRequest)
				return
			}
			queries <- queryRequest{ID: r.URL.Query().Get("requestId"), Body: body}
			io.WriteString(w, `{"success":true,"data":{"queryId":"compat-query-id","queryResultFormat":"json","rowtype":[{"name":"VALUE","type":"fixed","scale":0,"precision":38,"nullable":false}],"rowset":[["42"]],"total":1,"returned":1}}`)
		case "/session", "/telemetry/send":
			io.WriteString(w, `{"success":true,"data":{}}`)
		default:
			t.Errorf("unexpected Snowflake request: %s", r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, rawPort, err := net.SplitHostPort(endpoint.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(rawPort)
	require.NoError(t, err)
	dsn, err := v1.DSN(&v1.Config{Account: "test-account", User: "test-user", Password: "synthetic-password", Host: host, Port: port, Protocol: "http", LoginTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second})
	require.NoError(t, err)
	db, err := sql.Open("snowflake", dsn)
	require.NoError(t, err)
	defer db.Close()
	// Importing both paths must register exactly the official driver once.
	require.IsType(t, v2.SnowflakeDriver{}, db.Driver())
	require.Equal(t, reflect.TypeFor[v2.Config](), reflect.TypeFor[v1.Config]())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	requestID := v1.NewUUID()
	ids := make(chan string, 1)
	ctx = v1.WithRequestID(ctx, requestID)
	ctx = v1.WithQueryIDChan(ctx, ids)
	ctx = v1.WithQueryTag(ctx, "renart-compat-test")
	ctx, err = v1.WithMultiStatement(ctx, 0)
	require.NoError(t, err)
	var value int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT 42").Scan(&value))
	require.Equal(t, 42, value)
	request := <-queries
	require.Equal(t, requestID.String(), request.ID)
	require.Equal(t, "SELECT 42", request.Body["sqlText"])
	require.Equal(t, map[string]any{"MULTI_STATEMENT_COUNT": float64(0), "QUERY_TAG": "renart-compat-test"}, request.Body["parameters"])
	select {
	case id := <-ids:
		require.Equal(t, "compat-query-id", id)
	case <-ctx.Done():
		t.Fatal("v2 driver did not receive the compatibility query-ID channel")
	}
}

func TestSnowflakeCompatibilityLoggerRedactsSecrets(t *testing.T) {
	previous := v2.GetLogger()
	logger := v2.CreateDefaultLogger()
	var output bytes.Buffer
	logger.SetOutput(&output)
	require.NoError(t, logger.SetLogLevel("debug"))
	require.NoError(t, v2.SetLogger(logger))
	t.Cleanup(func() { require.NoError(t, v2.SetLogger(previous)) })
	for _, sample := range []struct{ message, secret string }{
		{"password=synthetic-password-123456789", "synthetic-password-123456789"},
		{"https://example.invalid/chunk?X-Amz-Signature=" + strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"adding header: x-amz-server-side-encryption-customer-key, value: " + strings.Repeat("b", 44), strings.Repeat("b", 44)},
	} {
		output.Reset()
		v1.GetLogger().WithContext(t.Context()).Debug(sample.message)
		require.NotEmpty(t, output.String(), "the test must exercise enabled logging")
		require.NotContains(t, output.String(), sample.secret)
		require.Contains(t, output.String(), "****")
	}
}
