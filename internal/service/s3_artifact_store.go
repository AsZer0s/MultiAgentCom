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

// S3ArtifactStore provides S3-compatible artifact storage with AWS SigV4 signing.
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

func (s *S3ArtifactStore) scheme() string {
	if s.useSSL {
		return "https"
	}
	return "http"
}

func (s *S3ArtifactStore) host() string {
	return s.endpoint
}

// Upload stores an artifact in S3 and returns the S3 URI.
func (s *S3ArtifactStore) Upload(ctx context.Context, projectID, runID, filename string, data []byte) (string, error) {
	key := fmt.Sprintf("artifacts/%s/%s/%s", projectID, runID, filename)
	url := fmt.Sprintf("%s://%s/%s/%s", s.scheme(), s.host(), s.bucket, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create s3 put request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	s.signRequestV4(req, data)

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

	url := fmt.Sprintf("%s://%s/%s/%s", s.scheme(), s.host(), bucket, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create s3 get request: %w", err)
	}

	s.signRequestV4(req, nil)

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

// signRequestV4 implements AWS Signature Version 4.
func (s *S3ArtifactStore) signRequestV4(req *http.Request, body []byte) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// Set Host header BEFORE computing canonical request.
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", sha256Hex(body))

	// Canonical request.
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQueryString := req.URL.Query().Encode()

	// Signed headers.
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.URL.Host, req.Header.Get("X-Amz-Content-Sha256"), amzDate)

	payloadHash := sha256Hex(body)
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method, canonicalURI, canonicalQueryString, canonicalHeaders, signedHeaders, payloadHash)

	// String to sign.
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	// Signing key.
	signingKey := s.getSignatureKey(dateStamp)
	signature := hmacSHA256(signingKey, stringToSign)

	// Authorization header.
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, credentialScope, signedHeaders, hex.EncodeToString(signature))
	req.Header.Set("Authorization", authHeader)
}

func (s *S3ArtifactStore) getSignatureKey(dateStamp string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+s.secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, s.region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
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
