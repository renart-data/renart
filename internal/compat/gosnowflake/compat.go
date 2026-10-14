// Package gosnowflake adapts the v1 API used by Bruin to the official v2 driver.
// It contains no driver implementation and does not register a second driver.
package gosnowflake

import (
	"context"

	v2 "github.com/snowflakedb/gosnowflake/v2"
)

type (
	Config   = v2.Config
	UUID     = v2.UUID
	SFLogger = v2.SFLogger
)

const (
	AuthTypeSnowflake = v2.AuthTypeSnowflake
	AuthTypeJwt       = v2.AuthTypeJwt
	AuthTypeOAuth     = v2.AuthTypeOAuth
)

func DSN(config *Config) (string, error) { return v2.DSN(config) }
func GetLogger() SFLogger                { return v2.GetLogger() }
func NewUUID() UUID                      { return v2.NewUUID() }

// WithMultiStatement preserves v1's unused error result. The v1 implementation
// always returned nil; v2 removed that return value without changing the context.
func WithMultiStatement(ctx context.Context, count int) (context.Context, error) {
	return v2.WithMultiStatement(ctx, count), nil
}

func WithQueryIDChan(ctx context.Context, ids chan<- string) context.Context {
	return v2.WithQueryIDChan(ctx, ids)
}

func WithRequestID(ctx context.Context, id UUID) context.Context {
	return v2.WithRequestID(ctx, id)
}

func WithQueryTag(ctx context.Context, tag string) context.Context {
	return v2.WithQueryTag(ctx, tag)
}
