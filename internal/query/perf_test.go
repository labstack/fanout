package query

import (
	"context"
	"testing"
	"time"
)

func TestCache_SetAndGet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := NewCache(ctx, 1*time.Second)

	// Set a value
	cache.Set("key1", "value1")

	// Get should return the value
	val, ok := cache.Get("key1")
	if !ok {
		t.Error("Get() should return true for existing key")
	}
	if val != "value1" {
		t.Errorf("Get() = %v, want %v", val, "value1")
	}

	// Get non-existent key
	_, ok = cache.Get("nonexistent")
	if ok {
		t.Error("Get() should return false for non-existent key")
	}
}

func TestCache_Expiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := NewCache(ctx, 50*time.Millisecond)

	cache.Set("key1", "value1")

	// Should exist immediately
	_, ok := cache.Get("key1")
	if !ok {
		t.Error("Get() should return true immediately after Set()")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, ok = cache.Get("key1")
	if ok {
		t.Error("Get() should return false after TTL expires")
	}
}

func TestCache_OverwriteValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := NewCache(ctx, 1*time.Second)

	cache.Set("key1", "value1")
	cache.Set("key1", "value2")

	val, ok := cache.Get("key1")
	if !ok {
		t.Error("Get() should return true for existing key")
	}
	if val != "value2" {
		t.Errorf("Get() = %v, want %v (overwritten value)", val, "value2")
	}
}
