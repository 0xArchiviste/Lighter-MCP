package proxyrot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLine(t *testing.T) {
	e, err := ParseLine("resi.example:4444:user-session-abc:secret")
	if err != nil {
		t.Fatal(err)
	}
	if e.Host != "resi.example" || e.Port != "4444" || e.User != "user-session-abc" || e.Pass != "secret" || e.Scheme != "http" {
		t.Fatalf("parsed %+v", e)
	}
	if e.String() != "http://resi.example:4444" {
		t.Fatalf("string leaked or wrong: %s", e.String())
	}
	u := e.URL()
	if u.User.Username() != "user-session-abc" {
		t.Fatalf("url user %s", u.User.Username())
	}

	e, err = ParseLine("https://user:p%40ss@proxy.example:8443")
	if err != nil {
		t.Fatal(err)
	}
	if e.Scheme != "https" || e.Host != "proxy.example" || e.Port != "8443" || e.User != "user" || e.Pass != "p@ss" {
		t.Fatalf("url form %+v", e)
	}

	if _, err := ParseLine("not-a-proxy"); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadFileRoundRobin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxies.txt")
	body := "# comment\n\nhost-a:1:user:pass\nhost-b:2:user:pass\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Len() != 2 {
		t.Fatalf("len %d", p.Len())
	}
	if p.Next().Host != "host-a" || p.Next().Host != "host-b" || p.Next().Host != "host-a" {
		t.Fatal("rotation order")
	}
}
