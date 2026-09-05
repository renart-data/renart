package databrowser

import (
	"context"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"renart/internal/web/dataaddress"
	"renart/internal/web/model"
	"testing"
)

func TestDurableAddressSurvivesRevisionWithoutPreview(t *testing.T) {
	revision := int64(1)
	s := New(Dependencies{
		ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) {
			return "dev", []ConnectionConfig{{Name: "warehouse", Type: "postgres", Queryable: true}}, revision, nil
		},
		ListTables: func(context.Context, string, string, string) ([]Table, error) {
			return []Table{{Name: `"db"."Mixed.Schema"."Order.Items"`, DatabaseName: "db", SchemaName: "Mixed.Schema", ShortName: "Order.Items"}}, nil
		},
		ListColumns: func(_ context.Context, _, name, _ string) ([]model.SQLColumn, error) {
			require.Equal(t, `"db"."Mixed.Schema"."Order.Items"`, name)
			return []model.SQLColumn{{Name: "Total", Type: "INTEGER"}}, nil
		},
		RunQuery: func(context.Context, string, string, string, int) (QueryResult, error) {
			t.Fatal("resolving must not preview rows")
			return QueryResult{}, nil
		},
	})
	address := dataaddress.Address{SourceKind: "warehouse", Connection: "warehouse", ConnectionType: "postgres", Database: "db", Schema: "Mixed.Schema", Name: "Order.Items"}
	first, apiErr := s.Resolve(context.Background(), ResolveRequest{Environment: "dev", Address: address})
	require.Nil(t, apiErr)
	require.Equal(t, &address, first.Object.Address)
	revision++
	_, apiErr = s.Object(context.Background(), first.Object.ID, "dev")
	require.Equal(t, "data_browser_revision_stale", apiErr.Code)
	fresh, apiErr := s.Resolve(context.Background(), ResolveRequest{Environment: "dev", Address: address})
	require.Nil(t, apiErr)
	require.NotEqual(t, first.Object.ID, fresh.Object.ID)
	address.Name = "order.items"
	_, apiErr = s.Resolve(context.Background(), ResolveRequest{Environment: "dev", Address: address})
	require.Equal(t, "data_browser_object_not_found", apiErr.Code)
}

func TestAddressRejectsWrongScopeAndUnsafeLocalPaths(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "orders.csv"), []byte("id\n1"), 0600))
	s := New(Dependencies{WorkspaceRoot: root, ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) { return "dev", nil, 1, nil }})
	for _, path := range []string{"../orders.csv", "/tmp/orders.csv", ".git/orders.csv", "node_modules/orders.csv"} {
		_, err := s.Resolve(context.Background(), ResolveRequest{Environment: "dev", Address: dataaddress.Address{SourceKind: "local_files", Path: path}})
		require.NotNil(t, err, path)
	}
	_, err := s.Resolve(context.Background(), ResolveRequest{Environment: "wrong", Address: dataaddress.Address{SourceKind: "local_files", Path: "orders.csv"}})
	require.NotNil(t, err)
	result, err := s.Resolve(context.Background(), ResolveRequest{Environment: "dev", Address: dataaddress.Address{SourceKind: "local_files", Path: "orders.csv"}})
	require.Nil(t, err)
	require.Equal(t, "orders.csv", result.Object.Address.Path)
}

func TestQuotedIdentifierBoundaries(t *testing.T) {
	require.Equal(t, `"db"." spaced "`, quoteQualifiedIdentifier(` db . " spaced " `))
	require.Equal(t, `"db"."Mixed.Schema"."Order.Items"`, quoteQualifiedIdentifier(`"db"."Mixed.Schema"."Order.Items"`))
	require.Equal(t, `"a""b"."c.d"`, quoteQualifiedIdentifier("`a\"b`.`c.d`"))
	require.Equal(t, "Order.Items", shortObjectName(`"db"."Mixed.Schema"."Order.Items"`))
}

func TestDurableAddressDoesNotGuessAmongDuplicateTables(t *testing.T) {
	s := New(Dependencies{ListConnections: staticConnections([]ConnectionConfig{{Name: "pg", Type: "postgres", Queryable: true}}), ListTables: func(context.Context, string, string, string) ([]Table, error) {
		return []Table{{Name: "public.orders", SchemaName: "public", ShortName: "orders"}, {Name: "public.orders", SchemaName: "public", ShortName: "orders"}}, nil
	}})
	_, err := s.Resolve(context.Background(), ResolveRequest{Environment: "dev", Address: dataaddress.Address{SourceKind: "warehouse", Connection: "pg", ConnectionType: "postgres", Schema: "public", Name: "orders"}})
	require.NotNil(t, err)
	require.Equal(t, "data_browser_object_not_found", err.Code)
}
