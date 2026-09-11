package snapshot

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stuttgart-things/homerun2-config-viewer/internal/discovery"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// builder returns results tagged with the call number, or the error set on it.
type builder struct {
	calls atomic.Int32
	mu    sync.Mutex
	err   error
}

func (b *builder) build(context.Context) (*discovery.Result, error) {
	n := b.calls.Add(1)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	return &discovery.Result{Namespace: strconv.Itoa(int(n))}, nil
}

func (b *builder) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func newCache(b *builder, ttl time.Duration) (*Cache, *clock) {
	clk := &clock{now: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)}
	c := New(b.build, ttl)
	c.now = clk.Now
	return c, clk
}

func get(t *testing.T, c *Cache) Snapshot {
	t.Helper()
	s, err := c.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return s
}

func TestCache_ReusesWithinTTL(t *testing.T) {
	b := &builder{}
	c, clk := newCache(b, 10*time.Second)

	first := get(t, c)
	clk.Advance(9 * time.Second)
	if again := get(t, c); again.Result != first.Result || b.calls.Load() != 1 {
		t.Errorf("rebuilt within the TTL: calls=%d", b.calls.Load())
	}

	clk.Advance(2 * time.Second)
	fresh := get(t, c)
	if fresh.Result == first.Result || b.calls.Load() != 2 {
		t.Errorf("did not rebuild after the TTL: calls=%d", b.calls.Load())
	}
	if !fresh.TakenAt.Equal(clk.Now()) {
		t.Errorf("TakenAt = %v, want %v", fresh.TakenAt, clk.Now())
	}
}

func TestCache_ZeroTTLRebuildsEveryTime(t *testing.T) {
	b := &builder{}
	c, _ := newCache(b, 0)
	for range 3 {
		get(t, c)
	}
	if b.calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", b.calls.Load())
	}
}

func TestCache_FailureKeepsLastGoodSnapshot(t *testing.T) {
	b := &builder{}
	c, clk := newCache(b, 10*time.Second)
	good := get(t, c)

	boom := errors.New("apiserver unavailable")
	b.fail(boom)
	clk.Advance(11 * time.Second)

	stale := get(t, c)
	if stale.Result != good.Result || !stale.TakenAt.Equal(good.TakenAt) {
		t.Error("a failed rebuild must keep the last good snapshot")
	}
	if !errors.Is(stale.RefreshError, boom) || !stale.RefreshFailedAt.Equal(clk.Now()) {
		t.Errorf("RefreshError = %v at %v", stale.RefreshError, stale.RefreshFailedAt)
	}

	// Not retried before the TTL passes again.
	clk.Advance(5 * time.Second)
	get(t, c)
	if b.calls.Load() != 2 {
		t.Errorf("retried a failure within the TTL: calls=%d", b.calls.Load())
	}

	// Recovery clears the error.
	b.fail(nil)
	clk.Advance(6 * time.Second)
	if recovered := get(t, c); recovered.RefreshError != nil || recovered.Result == good.Result {
		t.Errorf("after recovery: %+v", recovered)
	}
}

func TestCache_ErrorBeforeAnySuccess(t *testing.T) {
	b := &builder{}
	boom := errors.New("forbidden")
	b.fail(boom)
	c, clk := newCache(b, 10*time.Second)

	if _, err := c.Get(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// Within the TTL the error is answered from cache, not retried.
	if _, err := c.Get(context.Background()); !errors.Is(err, boom) || b.calls.Load() != 1 {
		t.Errorf("err = %v, calls = %d", err, b.calls.Load())
	}
	clk.Advance(11 * time.Second)
	b.fail(nil)
	get(t, c)
}

func TestCache_ConcurrentRequestsShareOneRebuild(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	c := New(func(context.Context) (*discovery.Result, error) {
		calls.Add(1)
		<-release
		return &discovery.Result{}, nil
	}, time.Minute)

	const n = 20
	var wg sync.WaitGroup
	results := make(chan *discovery.Result, n)
	for range n {
		wg.Go(func() {
			s, err := c.Get(context.Background())
			if err != nil {
				t.Errorf("Get: %v", err)
				return
			}
			results <- s.Result
		})
	}

	// Let every goroutine reach the shared rebuild before it finishes.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	if calls.Load() != 1 {
		t.Errorf("build calls = %d, want 1", calls.Load())
	}
	var first *discovery.Result
	for r := range results {
		if first == nil {
			first = r
		}
		if r != first {
			t.Error("concurrent requests got different results")
		}
	}
}

func TestCache_CallerCancelDoesNotCancelTheRebuild(t *testing.T) {
	release := make(chan struct{})
	var buildCtxCanceled atomic.Bool
	c := New(func(ctx context.Context) (*discovery.Result, error) {
		<-release
		buildCtxCanceled.Store(ctx.Err() != nil)
		return &discovery.Result{Namespace: "built"}, nil
	}, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller got %v", err)
	}

	// A second caller joins the same rebuild and gets its result.
	second := make(chan Snapshot, 1)
	go func() {
		s, err := c.Get(context.Background())
		if err != nil {
			t.Errorf("second Get: %v", err)
		}
		second <- s
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)

	if s := <-second; s.Result == nil || s.Result.Namespace != "built" {
		t.Errorf("second caller got %+v", s)
	}
	if buildCtxCanceled.Load() {
		t.Error("the rebuild's context was canceled with its first caller")
	}
}

func TestCache_BuildTimeout(t *testing.T) {
	c := New(func(ctx context.Context) (*discovery.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, time.Minute)
	c.buildTimeout = 20 * time.Millisecond

	if _, err := c.Get(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the build timeout", err)
	}
}
