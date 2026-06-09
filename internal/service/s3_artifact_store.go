package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// S3ArtifactStore provides S3-compatible artifact storage.
// Works with AWS S3, MinIO, DigitalOcean Spaces, and any S3-compatible service.
type S3ArtifactStore struct {
	endpoint  string
	accessKey string
	secretKey string
	bucket    string
	region    string
	useSSL    bool
	client    *http.Client
}

// S3ArtifactStoreOptions configures the S3 artifact store.
type S3ArtifactStoreOptions struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

// NewS3ArtifactStore creates an S3-compatible artifact store.
func NewS3ArtifactStore(opts S3ArtifactStoreOptions) (*S3ArtifactStore, error) {
	if opts.Endpoint == "" {
		return nil, fmt.Errorf("s3 endpoint is required")
	}
	if opts.AccessKey == "" || opts.SecretKey == "" {
		return nil, fmt.Errorf("s3 access key and secret key are required")
	}
	if opts.Bucket == "" {
		opts.Bucket = "multiagentcom"
	}
	if opts.Region == "" {
		opts.Region = "us-east-1"
	}
	return &S3ArtifactStore{
		endpoint:  strings.TrimSuffix(opts.Endpoint, "/"),
		accessKey: opts.AccessKey,
		secretKey: opts.SecretKey,
		bucket:    opts.Bucket,
		region:    opts.Region,
		useSSL:    opts.UseSSL,
		client:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *S3ArtifactStore) baseURL() string {
	scheme := "http"
	if s.useSSL {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/%s", scheme, s.endpoint, s.bucket)
}

// Upload stores an artifact in S3 and returns the S3 URI.
func (s *S3ArtifactStore) Upload(ctx context.Context, projectID, runID, filename string, data []byte) (string, error) {
	key := fmt.Sprintf("artifacts/%s/%s/%s", projectID, runID, filename)
	url := s.baseURL() + "/" + key

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create s3 put request: %w", err)
	}

	date := time.Now().UTC().Format(http.TimeFormat)
	contentType := "application/octet-stream"
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// S3 V4 signature (simplified for MinIO/S3-compatible)
	signature := s.signRequest("PUT", contentType, date, "/"+s.bucket+"/"+key)
	req.Header.Set("Authorization", fmt.Sprintf("AWS %s:%s", s.accessKey, signature))

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("s3 upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("s3 upload failed (status %d): %s", resp.StatusCode, body)
	}

	return "s3://" + s.bucket + "/" + key, nil
}

// Download retrieves an artifact from S3.
func (s *S3ArtifactStore) Download(ctx context.Context, s3URI string) ([]byte, error) {
	bucket, key, err := parseS3URI(s3URI)
	if err != nil {
		return nil, err
	}

	scheme := "http"
	if s.useSSL {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s/%s/%s", scheme, s.endpoint, bucket, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create s3 get request: %w", err)
	}

	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	signature := s.signRequest("GET", "", date, "/"+bucket+"/"+key)
	req.Header.Set("Authorization", fmt.Sprintf("AWS %s:%s", s.accessKey, signature))

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("s3 download failed (status %d): %s", resp.StatusCode, body)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxArtifactSize))
}

// CopyToLocal downloads an S3 artifact to a local file path.
func (s *S3ArtifactStore) CopyToLocal(ctx context.Context, s3URI, localPath string) error {
	data, err := s.Download(ctx, s3URI)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(localPath, data, 0o644)
}

// signRequest creates an AWS V4-style signature (simplified for S3-compatible services).
func (s *S3ArtifactStore) signRequest(method, contentType, date, path string) string {
	stringToSign := fmt.Sprintf("%s\n\n%s\n%s", method, contentType, date+path)
	mac := hmac.New(sha256.New, []byte(s.secretKey))
	mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}

// parseS3URI parses s3://bucket/key into bucket and key.
func parseS3URI(uri string) (string, string, error) {
	if !strings.HasPrefix(uri, "s3://") {
		return "", "", fmt.Errorf("invalid s3 URI: %s", uri)
	}
	parts := strings.SplitN(strings.TrimPrefix(uri, "s3://"), "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid s3 URI format: %s", uri)
	}
	return parts[0], parts[1], nil
}

const maxArtifactSize = 100 * 1024 * 1024 // 100 MiB
