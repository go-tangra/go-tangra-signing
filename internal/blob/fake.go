package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Store used by tests and (optionally) the app in dev. It
// keeps object bytes in a map guarded by a mutex and computes the same SHA-256
// checksum on Put as the real client. PresignGet returns a deterministic,
// non-network fake URL. Zero value is ready to use.
type Fake struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// NewFake returns an initialised Fake. The zero value works too; this is just a
// convenience for callers that prefer a constructor.
func NewFake() *Fake {
	return &Fake{objects: make(map[string][]byte)}
}

// compile-time check that *Fake satisfies Store.
var _ Store = (*Fake)(nil)

// Put reads all of r (up to size is advisory only), stores the bytes under key,
// and returns the SHA-256 hex of those bytes.
// EnsureBucket is a no-op for the in-memory fake.
func (f *Fake) EnsureBucket(_ context.Context) error { return nil }

func (f *Fake) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("blob(fake): read body for %q: %w", key, err)
	}
	sum := sha256.Sum256(data)

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	// store a private copy so later mutations of the caller's slice can't alter it
	stored := make([]byte, len(data))
	copy(stored, data)
	f.objects[key] = stored

	return hex.EncodeToString(sum[:]), nil
}

// Get returns a reader over the stored bytes for key, or an error if absent.
func (f *Fake) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	data, ok := f.objects[key]
	if !ok {
		return nil, fmt.Errorf("blob(fake): object %q not found", key)
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	return io.NopCloser(bytes.NewReader(buf)), nil
}

// PresignGet returns a deterministic fake URL. It does not verify existence in a
// way the real presigner would; it is retrievable via getFakeURL in tests.
func (f *Fake) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("https://fake-blob.local/%s?ttl=%d", key, int64(ttl.Seconds())), nil
}

// Delete removes key. Deleting an absent key is a no-op (idempotent).
func (f *Fake) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

// List returns up to max keys under prefix, sorted.
func (f *Fake) List(_ context.Context, prefix string, max int) ([]string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// Has reports whether key is stored (tests).
func (f *Fake) Has(key string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.objects[key]
	return ok
}
