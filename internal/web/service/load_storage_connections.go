package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
)

// Unlike database URLs, Sling's filesystem URL parser does not promote query
// parameters to connection properties. Keep credentials and custom S3 endpoints
// in the structured connection payload shared by discovery and Load execution.
func slingS3ConnectionPayload(connection config.S3Connection) (string, error) {
	bucket := strings.TrimSpace(connection.BucketName)
	if strings.ContainsAny(bucket, "/\\?#@:") {
		return "", fmt.Errorf("S3 bucket must be a bucket name, not a URL")
	}
	u := url.URL{Scheme: "s3", Host: bucket, Path: "/" + strings.TrimLeft(connection.PathToFile, "/")}
	payload := map[string]string{"type": "s3", "url": u.String()}
	for key, value := range map[string]string{
		"access_key_id": connection.AccessKeyID, "secret_access_key": connection.SecretAccessKey,
		"endpoint": strings.TrimSpace(connection.EndpointURL),
	} {
		if value != "" {
			payload[key] = value
		}
	}
	encoded, err := json.Marshal(payload)
	return string(encoded), err
}

func slingSFTPConnectionURI(connection config.SFTPConnection) (string, error) {
	port := connection.Port
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 || strings.TrimSpace(connection.Host) == "" {
		return "", fmt.Errorf("SFTP requires a host and a valid port")
	}
	u := url.URL{Scheme: "sftp", Host: net.JoinHostPort(connection.Host, strconv.Itoa(port)), User: url.UserPassword(connection.Username, connection.Password), Path: "/"}
	return u.String(), nil
}
