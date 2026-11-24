package gtm

import (
	"testing"
	"time"
)

type sampleItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestSnapshotCacheDefensiveCopyAndExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	c := NewSnapshotCache(true, 30*time.Second)
	c.now = func() time.Time { return now }

	key := workspaceCachePrefix("1", "2", "3") + "tags"
	original := []sampleItem{{ID: "10", Name: "GA4"}}
	c.Set(key, original)

	// Mutate original after Set: cached value must not change.
	original[0].Name = "MUTATED"

	var first []sampleItem
	if !c.Get(key, &first) {
		t.Fatalf("expected cache hit")
	}
	if len(first) != 1 || first[0].Name != "GA4" {
		t.Fatalf("unexpected cached slice: %+v", first)
	}

	// Mutate returned slice: next Get must still receive clean copy.
	first[0].Name = "MUTATED_AGAIN"
	var second []sampleItem
	if !c.Get(key, &second) {
		t.Fatalf("expected cache hit")
	}
	if second[0].Name != "GA4" {
		t.Fatalf("cache was mutated via returned copy: %+v", second)
	}

	// Advance past TTL -> miss.
	now = now.Add(31 * time.Second)
	var expired []sampleItem
	if c.Get(key, &expired) {
		t.Fatalf("expected cache miss after TTL")
	}
}

func TestSnapshotCacheInvalidation(t *testing.T) {
	c := NewSnapshotCache(true, 30*time.Second)
	k1 := workspaceCachePrefix("1", "2", "3") + "tags"
	k2 := workspaceCachePrefix("1", "2", "4") + "tags"

	c.Set(k1, []sampleItem{{ID: "1", Name: "A"}})
	c.Set(k2, []sampleItem{{ID: "2", Name: "B"}})

	c.InvalidateWorkspace("1", "2", "3")

	var out []sampleItem
	if c.Get(k1, &out) {
		t.Fatalf("expected workspace 3 to be invalidated")
	}
	if !c.Get(k2, &out) {
		t.Fatalf("expected workspace 4 to remain cached")
	}

	c.InvalidateContainer("1", "2")
	if c.Get(k2, &out) {
		t.Fatalf("expected container invalidation to remove workspace 4")
	}
}

func TestSnapshotCacheDisabled(t *testing.T) {
	c := NewSnapshotCache(false, 30*time.Second)
	c.Set("k", []sampleItem{{ID: "1", Name: "A"}})
	var out []sampleItem
	if c.Get("k", &out) {
		t.Fatalf("expected disabled cache to miss")
	}
}
