// Package metacache 提供进程级的元数据查询结果缓存（TTL + 并发安全）。
//
// 用于缓存 DataGrip 的 information_schema 表结构探测结果，避免每次刷新都转发 Yearning。
// 值以 interface{} 存储（通常是原始查询结果结构副本），由调用方命中时重新构造结果集，
// 避免复用 go-mysql 结果集实例（其 Close 会 returnToPool）。
package metacache

import (
	"sync"
	"time"
)

// Cache 是带 TTL 的并发安全缓存。
type Cache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cacheItem
	nowFunc func() time.Time // 便于测试注入
}

type cacheItem struct {
	value  interface{}
	expire time.Time
}

// New 构造一个 TTL 为 ttl 的缓存。
func New(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Cache{
		ttl:     ttl,
		entries: make(map[string]cacheItem),
		nowFunc: time.Now,
	}
}

// Get 命中返回 value，未命中或过期返回 (nil, false)。
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	it, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if c.nowFunc().After(it.expire) {
		c.Delete(key)
		return nil, false
	}
	return it.value, true
}

// Set 写入一条缓存。
func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	c.entries[key] = cacheItem{
		value:  value,
		expire: c.nowFunc().Add(c.ttl),
	}
	c.mu.Unlock()
}

// Delete 删除一条缓存。
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// Len 返回当前缓存条目数（含未过期与已过期未清理的）。
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
