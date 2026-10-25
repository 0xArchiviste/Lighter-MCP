package api

import "testing"

func TestLoadProxiesEmptyIsDirect(t *testing.T) {
	p, _, err := loadProxies("")
	if err != nil || p != nil {
		t.Fatalf("loadProxies(\"\") = %v, %v; want nil pool, nil error", p, err)
	}
}

func TestLoadProxiesMissingExplicitFileErrors(t *testing.T) {
	if _, _, err := loadProxies(t.TempDir() + "/missing.txt"); err == nil {
		t.Fatal("want error for unreadable explicit proxy file")
	}
}
