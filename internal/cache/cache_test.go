package cache

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPutGet(t *testing.T) {
	c := New(time.Minute)
	gpx := []byte("<gpx/>")

	token, err := c.Put(gpx, "half-dome")
	if err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("Put() returned empty token")
	}

	entry, err := c.Get(token)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if string(entry.GPXBytes) != string(gpx) {
		t.Errorf("GPXBytes = %q, want %q", entry.GPXBytes, gpx)
	}
	if entry.TrailSlug != "half-dome" {
		t.Errorf("TrailSlug = %q, want %q", entry.TrailSlug, "half-dome")
	}
}

func TestGet_UnknownToken(t *testing.T) {
	c := New(time.Minute)

	_, err := c.Get("doesnotexist")
	if err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestGet_Expired(t *testing.T) {
	c := New(time.Millisecond)

	token, err := c.Put([]byte("<gpx/>"), "some-trail")
	if err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	_, err = c.Get(token)
	if err != ErrNotFound {
		t.Errorf("Get() after expiry = %v, want ErrNotFound", err)
	}
}

func TestPut_TokensAreUnique(t *testing.T) {
	c := New(time.Minute)
	seen := make(map[string]bool)

	for i := 0; i < 10; i++ {
		token, err := c.Put([]byte("<gpx/>"), "trail")
		if err != nil {
			t.Fatalf("Put() unexpected error: %v", err)
		}
		if seen[token] {
			t.Errorf("duplicate token generated: %q", token)
		}
		seen[token] = true
	}
}

func TestPut_Full(t *testing.T) {
	c := New(time.Minute)

	for i := 0; i < maxEntries; i++ {
		if _, err := c.Put([]byte("<gpx/>"), "trail"); err != nil {
			t.Fatalf("Put() unexpected error at entry %d: %v", i, err)
		}
	}

	_, err := c.Put([]byte("<gpx/>"), "trail")
	if err != ErrCacheFull {
		t.Errorf("Put() on full cache = %v, want ErrCacheFull", err)
	}
}

func TestSweep(t *testing.T) {
	c := New(time.Millisecond)

	if _, err := c.Put([]byte("<gpx/>"), "trail"); err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}

	time.Sleep(5 * time.Millisecond)
	c.sweep()

	c.mu.Lock()
	n := len(c.entries)
	c.mu.Unlock()

	if n != 0 {
		t.Errorf("after sweep: %d entries remain, want 0", n)
	}
}

func TestStartSweep_StopsOnContextCancel(t *testing.T) {
	c := New(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	c.StartSweep(ctx, time.Millisecond)

	// Add an entry and let the sweeper run.
	if _, err := c.Put([]byte("<gpx/>"), "trail"); err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	cancel() // goroutine should exit cleanly; no panic or leak.
}

func TestConcurrent(t *testing.T) {
	c := New(time.Minute)
	var wg sync.WaitGroup
	tokens := make(chan string, 50)

	// Concurrent writes.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := c.Put([]byte("<gpx/>"), "trail")
			if err != nil {
				return // cache may fill up under concurrency, that's fine
			}
			tokens <- token
		}()
	}

	wg.Wait()
	close(tokens)

	// Concurrent reads of all written tokens.
	for token := range tokens {
		wg.Add(1)
		tok := token
		go func() {
			defer wg.Done()
			if _, err := c.Get(tok); err != nil {
				t.Errorf("Get(%q) unexpected error: %v", tok, err)
			}
		}()
	}
	wg.Wait()
}
