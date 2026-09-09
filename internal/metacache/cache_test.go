package metacache

import (
	"sync"
	"testing"
	"time"
)

func TestCacheHit(t *testing.T) {
	c := New(time.Minute)
	c.Set("k", "v")
	v, ok := c.Get("k")
	if !ok || v != "v" {
		t.Fatalf("应命中缓存, got %v %v", v, ok)
	}
}

func TestCacheMiss(t *testing.T) {
	c := New(time.Minute)
	if _, ok := c.Get("nope"); ok {
		t.Fatal("未写入的 key 不应命中")
	}
}

func TestCacheExpiry(t *testing.T) {
	c := New(2 * time.Minute)
	var now time.Time
	c.nowFunc = func() time.Time { return now }
	c.Set("k", "v")
	now = now.Add(3 * time.Minute)
	if _, ok := c.Get("k"); ok {
		t.Fatal("过期后不应命中")
	}
}

func TestCacheConcurrent(t *testing.T) {
	c := New(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Set("k", i)
			_, _ = c.Get("k")
		}(i)
	}
	wg.Wait()
}
