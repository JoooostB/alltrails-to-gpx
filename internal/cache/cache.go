package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ErrCacheFull is returned when Put is called but the cache has reached its
// maximum entry count.
var ErrCacheFull = errors.New("cache is full")

// ErrNotFound is returned when a token does not exist or has expired.
var ErrNotFound = errors.New("token not found or expired")

const maxEntries = 20 // limits memory to ~200 MB at 10 MB per entry

// Entry is a cached GPX conversion result.
type Entry struct {
	GPXBytes  []byte
	TrailSlug string
	ExpiresAt time.Time
}

// Cache is a thread-safe in-memory store of GPX conversion results, keyed by
// random UUID tokens.
type Cache struct {
	mu      sync.Mutex
	entries map[string]Entry
	ttl     time.Duration
}

// New returns an empty Cache with the given TTL.
func New(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]Entry),
		ttl:     ttl,
	}
}

// Put stores a GPX result and returns the access token.
// Returns ErrCacheFull when the maximum entry count has been reached.
func (c *Cache) Put(gpxBytes []byte, trailSlug string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= maxEntries {
		return "", ErrCacheFull
	}

	token, err := newToken()
	if err != nil {
		return "", err
	}

	c.entries[token] = Entry{
		GPXBytes:  gpxBytes,
		TrailSlug: trailSlug,
		ExpiresAt: time.Now().Add(c.ttl),
	}
	return token, nil
}

// Get retrieves a cache entry by token. Returns ErrNotFound if the token is
// unknown or has expired.
func (c *Cache) Get(token string) (Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[token]
	if !ok || time.Now().After(e.ExpiresAt) {
		delete(c.entries, token)
		return Entry{}, ErrNotFound
	}
	return e, nil
}

// StartSweep launches a background goroutine that evicts expired entries on
// the given interval. It stops when ctx is cancelled.
func (c *Cache) StartSweep(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.sweep()
			}
		}
	}()
}

func (c *Cache) sweep() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for token, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, token)
		}
	}
}

// newToken returns a cryptographically random 32-character hex token.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
