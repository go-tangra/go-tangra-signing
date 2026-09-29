package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/go-tangra/go-tangra-signing/v4/internal/config"
	"github.com/go-tangra/go-tangra-signing/v4/internal/warden"
)

// lazyWarden dials the warden module on first use so signing starts while
// warden is down; a dial failure reads as unavailable.
type lazyWarden struct {
	app     *App
	service string
	mu      sync.Mutex
	c       warden.Client
}

func (l *lazyWarden) client(ctx context.Context) (warden.Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c == nil {
		conn, err := l.app.Freya.Client(ctx, l.service)
		if err != nil {
			return nil, err
		}
		l.c = warden.New(conn)
	}
	return l.c, nil
}

// Meta implements warden.Client.
func (l *lazyWarden) Meta(ctx context.Context, ref string) (warden.SecretMeta, error) {
	c, err := l.client(ctx)
	if err != nil {
		return warden.SecretMeta{}, warden.ErrUnavailable
	}
	return c.Meta(ctx, ref)
}

// Credentials implements warden.Client.
func (l *lazyWarden) Credentials(ctx context.Context, ref string) (warden.Credentials, error) {
	c, err := l.client(ctx)
	if err != nil {
		return warden.Credentials{}, warden.ErrUnavailable
	}
	return c.Credentials(ctx, ref)
}

// trustRoots builds the qualified/platform root pool of verification
// (research D11): the system store when enabled plus a PEM bundle. nil when
// neither is configured (tenant CAs only).
func trustRoots(v config.Verify) (*x509.CertPool, error) {
	if !v.UseSystemRoots && v.ExtraRootsFile == "" {
		return nil, nil
	}
	pool := x509.NewCertPool()
	if v.UseSystemRoots {
		sys, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("system roots: %w", err)
		}
		pool = sys
	}
	if v.ExtraRootsFile != "" {
		pem, err := os.ReadFile(v.ExtraRootsFile)
		if err != nil {
			return nil, fmt.Errorf("verify.extra_roots_file: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("verify.extra_roots_file: no certificates")
		}
	}
	return pool, nil
}
