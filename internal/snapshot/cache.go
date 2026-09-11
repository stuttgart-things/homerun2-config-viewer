// Package snapshot caches what discovery found in a namespace, so a page
// reload does not turn into a round of Kubernetes API calls.
package snapshot

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
)

// DefaultBuildTimeout bounds one rebuild.
const DefaultBuildTimeout = 30 * time.Second

// Snapshot is one view of the namespace.
type Snapshot struct {
	Result *discovery.Result
	// TakenAt is when Result was read.
	TakenAt time.Time
	// RefreshError is set when the latest rebuild failed after an earlier one
	// succeeded: Result is then the last good snapshot, and this says why it
	// is not newer.
	RefreshError error
	// RefreshFailedAt is when that rebuild failed.
	RefreshFailedAt time.Time
}

// BuildFunc reads a fresh result.
type BuildFunc func(context.Context) (*discovery.Result, error)

// Cache hands out snapshots and rebuilds them at most once per TTL.
//
// Concurrent requests for a stale snapshot share one rebuild. A failed rebuild
// keeps the last good snapshot and is not retried before the TTL passes again,
// so an unhealthy API server is not hit harder for being unhealthy.
type Cache struct {
	build        BuildFunc
	ttl          time.Duration
	buildTimeout time.Duration
	now          func() time.Time

	group singleflight.Group

	mu        sync.Mutex
	good      *discovery.Result
	goodAt    time.Time
	lastErr   error
	lastErrAt time.Time
}

// New returns a cache that rebuilds with build at most every ttl. A zero ttl
// rebuilds for every request, still sharing one rebuild among concurrent ones.
func New(build BuildFunc, ttl time.Duration) *Cache {
	return &Cache{build: build, ttl: ttl, buildTimeout: DefaultBuildTimeout, now: time.Now}
}

// Get returns the current snapshot, rebuilding it when it is older than the
// TTL. It returns an error only when no rebuild has succeeded yet, or when ctx
// ends first.
func (c *Cache) Get(ctx context.Context) (Snapshot, error) {
	c.mu.Lock()
	if c.freshLocked() {
		defer c.mu.Unlock()
		if c.good == nil {
			return Snapshot{}, c.lastErr
		}
		return c.snapshotLocked(), nil
	}
	c.mu.Unlock()

	// The rebuild outlives the request that started it: others may be waiting
	// on it, so one caller giving up must not cancel it.
	result := c.group.DoChan("snapshot", func() (any, error) {
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.buildTimeout)
		defer cancel()
		res, err := c.build(buildCtx)

		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.lastErr, c.lastErrAt = err, c.now()
			return nil, err
		}
		c.good, c.goodAt = res, c.now()
		c.lastErr, c.lastErrAt = nil, time.Time{}
		return nil, nil
	})

	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case r := <-result:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.good == nil {
			return Snapshot{}, r.Err
		}
		return c.snapshotLocked(), nil
	}
}

// freshLocked reports whether the latest attempt, successful or not, is
// younger than the TTL.
func (c *Cache) freshLocked() bool {
	if c.ttl <= 0 {
		return false
	}
	last := c.goodAt
	if c.lastErrAt.After(last) {
		last = c.lastErrAt
	}
	return !last.IsZero() && c.now().Sub(last) < c.ttl
}

func (c *Cache) snapshotLocked() Snapshot {
	return Snapshot{
		Result:          c.good,
		TakenAt:         c.goodAt,
		RefreshError:    c.lastErr,
		RefreshFailedAt: c.lastErrAt,
	}
}
