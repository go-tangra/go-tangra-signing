// Package blob provides the object-storage abstraction of the signing service
// (copied from go-tangra-paperless-v4): a small Store interface, a real
// S3/MinIO-backed implementation (minio-go) and (in fake.go) an in-memory fake
// for tests. Keys are per tenant (keys.go). Put computes the SHA-256 of the uploaded bytes by teeing the reader
// through a sha256 hash while the SDK streams the body to the server, so the
// returned checksum is always the SHA-256 hex of the exact bytes stored.
//
// Security: AccessKey/SecretKey come from the configuration. This package MUST NEVER log the secret key
// or the object bytes, and never embeds either in an error string.
package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store is the object-storage abstraction used by the signing service.
type Store interface {
	// EnsureBucket creates the configured bucket if it does not already exist.
	// It is idempotent and safe to call on every startup; when the bucket is
	// present (e.g. pre-provisioned by ops) it performs no create.
	EnsureBucket(ctx context.Context) error
	// Put streams r (size bytes, contentType) to key and returns the SHA-256 hex of the bytes.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (checksum string, err error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (url string, err error)
	Delete(ctx context.Context, key string) error
	// List returns the keys under prefix (bounded by max; the orphan sweep).
	List(ctx context.Context, prefix string, max int) ([]string, error)
}

// Config holds the connection settings for the real S3/MinIO-backed Store.
// Credentials (AccessKey/SecretKey) are supplied already-unsealed by the caller;
// this package never logs or otherwise exposes them.
type Config struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey string
	UseSSL                                         bool
}

// client is the real Store implementation backed by the MinIO Go SDK.
type client struct {
	mc     *minio.Client
	bucket string
	region string
}

// New builds a real Store talking to the S3/MinIO endpoint in cfg. It does not
// perform any network round-trip itself; connection errors surface on first use.
func New(cfg Config) (Store, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("blob: endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("blob: bucket is required")
	}
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		// minio.New only fails on a malformed endpoint; the error carries no
		// credential material, but we wrap it with a fixed prefix regardless.
		return nil, fmt.Errorf("blob: init client: %w", err)
	}
	return &client{mc: mc, bucket: cfg.Bucket, region: cfg.Region}, nil
}

// EnsureBucket creates the bucket if it is absent. Idempotent: it checks first
// and only creates when missing, so a pre-provisioned bucket (and credentials
// without create permission) is fine. The region is best-effort.
func (c *client) EnsureBucket(ctx context.Context) error {
	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("blob: check bucket: %w", err)
	}
	if exists {
		return nil
	}
	if err := c.mc.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{Region: c.region}); err != nil {
		// A concurrent creator (another instance) may have won the race.
		if exists2, e2 := c.mc.BucketExists(ctx, c.bucket); e2 == nil && exists2 {
			return nil
		}
		return fmt.Errorf("blob: create bucket: %w", err)
	}
	return nil
}

// Put streams r to key and returns the SHA-256 hex of the uploaded bytes. The
// checksum is computed by teeing r through a sha256 hash while the SDK uploads,
// so it always reflects the exact bytes the server received.
func (c *client) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	h := sha256.New()
	tee := io.TeeReader(r, h)
	if _, err := c.mc.PutObject(ctx, c.bucket, key, tee, size, minio.PutObjectOptions{
		ContentType: contentType,
	}); err != nil {
		return "", fmt.Errorf("blob: put %q: %w", key, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Get opens the object at key for reading. The caller must Close the reader.
func (c *client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("blob: get %q: %w", key, err)
	}
	// *minio.Object is lazy: GetObject never errors on a missing key here, so
	// probe with Stat to surface a not-found before handing back the reader.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("blob: get %q: %w", key, err)
	}
	return obj, nil
}

// PresignGet returns a time-limited presigned GET URL for key.
func (c *client) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("blob: presign %q: %w", key, err)
	}
	return u.String(), nil
}

// Delete removes the object at key.
func (c *client) Delete(ctx context.Context, key string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("blob: delete %q: %w", key, err)
	}
	return nil
}

// List returns up to max keys under prefix.
func (c *client) List(ctx context.Context, prefix string, max int) ([]string, error) {
	var out []string
	for obj := range c.mc.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return out, fmt.Errorf("blob: list %q: %w", prefix, obj.Err)
		}
		out = append(out, obj.Key)
		if len(out) >= max {
			break
		}
	}
	return out, nil
}
