package riskcache

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bennett-17/paper-trail/internal/risk"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	return &Cache{Dir: t.TempDir()}
}

func TestGetMissesOnEmptyCache(t *testing.T) {
	c := newTestCache(t)
	if _, ok := c.Get(Key("nonprofit", "Example", 5), time.Hour); ok {
		t.Error("expected a miss on an empty cache")
	}
}

func TestSetThenGetHits(t *testing.T) {
	c := newTestCache(t)
	entities := []risk.Entity{risk.NewEntity("nonprofit", "1", "Example Org", nil, nil)}
	key := Key("nonprofit", "Example", 5)

	c.Set(key, entities)
	got, ok := c.Get(key, time.Hour)
	if !ok {
		t.Fatal("expected a hit after Set")
	}
	if len(got) != 1 || got[0].Name != "Example Org" {
		t.Errorf("got %+v, want the entity that was Set", got)
	}
}

func TestGetMissesWhenExpired(t *testing.T) {
	c := newTestCache(t)
	key := Key("nonprofit", "Example", 5)
	c.Set(key, []risk.Entity{risk.NewEntity("nonprofit", "1", "Example Org", nil, nil)})

	// Manually backdate the entry past a very short TTL.
	c.mu.Lock()
	e := c.entries[key]
	e.CachedAt = time.Now().Add(-2 * time.Hour)
	c.entries[key] = e
	c.mu.Unlock()

	if _, ok := c.Get(key, time.Hour); ok {
		t.Error("expected a miss for an entry older than the TTL")
	}
}

func TestKeyDistinguishesByLimitAndSourceAndQuery(t *testing.T) {
	if Key("nonprofit", "Example", 5) == Key("nonprofit", "Example", 10) {
		t.Error("keys with different limits should differ")
	}
	if Key("nonprofit", "Example", 5) == Key("ukcharity", "Example", 5) {
		t.Error("keys with different sources should differ")
	}
	if Key("nonprofit", "Example", 5) == Key("nonprofit", "Other", 5) {
		t.Error("keys with different queries should differ")
	}
	if Key("nonprofit", "Example", 5) != Key("nonprofit", "EXAMPLE", 5) {
		t.Error("query matching should be case-insensitive")
	}
}

func TestSaveThenNewCacheLoadsPersistedEntry(t *testing.T) {
	dir := t.TempDir()
	c1 := &Cache{Dir: dir}
	key := Key("nonprofit", "Example", 5)
	c1.Set(key, []risk.Entity{risk.NewEntity("nonprofit", "1", "Example Org", nil, nil)})
	c1.Save()

	c2 := &Cache{Dir: dir}
	got, ok := c2.Get(key, time.Hour)
	if !ok {
		t.Fatal("expected a fresh Cache pointed at the same directory to see the saved entry")
	}
	if len(got) != 1 || got[0].Name != "Example Org" {
		t.Errorf("got %+v after reload", got)
	}
}

func TestDisabledCacheAlwaysMisses(t *testing.T) {
	c := &Cache{Dir: ""}
	key := Key("nonprofit", "Example", 5)
	c.Set(key, []risk.Entity{risk.NewEntity("nonprofit", "1", "Example Org", nil, nil)})
	c.Save()

	if _, ok := c.Get(key, time.Hour); ok {
		t.Error("a cache with an empty Dir should never hit, even right after Set")
	}
}

func TestSaveWithoutChangesDoesNotWriteFile(t *testing.T) {
	dir := t.TempDir()
	c := &Cache{Dir: dir}
	c.Save() // nothing was ever Set

	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("Save with no changes wrote %v, want no files", matches)
	}
}
