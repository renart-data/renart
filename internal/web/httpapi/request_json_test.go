package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeJSONObject(t *testing.T) {
	t.Parallel()

	type requestBody struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name string
		body string
		max  int64
		want requestBody
		err  string
	}{
		{name: "object", body: `{"name":"orders"}`, want: requestBody{Name: "orders"}},
		{name: "unknown field", body: `{"name":"orders","extra":true}`, err: `unknown field "extra"`},
		{name: "null", body: `null`, err: "request body must be a JSON object"},
		{name: "two objects", body: `{"name":"one"}{"name":"two"}`, err: "invalid trailing JSON"},
		{name: "malformed", body: `{"name":`, err: "unexpected EOF"},
		{name: "oversized", body: `{"name":"orders"}`, max: 8, err: "request body too large"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			got, err := decodeJSONObject[requestBody](response, request, test.max)
			if test.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
