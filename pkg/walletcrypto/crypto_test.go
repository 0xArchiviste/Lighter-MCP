package walletcrypto

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	salt, err := RandomSalt(16)
	if err != nil {
		t.Fatal(err)
	}
	key := DeriveKey("master-pass", "unlock-pass", salt)
	pt := []byte(`{"api_key_private_key":"abc","eth_private_key":""}`)
	sealed, err := Seal(pt, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("mismatch: %s vs %s", got, pt)
	}
}

func TestWrongPasswordFails(t *testing.T) {
	salt, _ := RandomSalt(16)
	k1 := DeriveKey("a", "b", salt)
	k2 := DeriveKey("a", "c", salt)
	pt := []byte(`{}`)
	sealed, _ := Seal(pt, k1)
	_, err := Open(k2, sealed)
	if err == nil {
		t.Fatal("expected decrypt error")
	}
}

func TestJSONPayload(t *testing.T) {
	type sec struct {
		APIKeyPrivateKey string `json:"api_key_private_key"`
	}
	b, _ := json.Marshal(sec{APIKeyPrivateKey: "0xabc"})
	salt, _ := RandomSalt(16)
	key := DeriveKey("m", "u", salt)
	sealed, err := Seal(b, key)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Open(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	var s sec
	if err := json.Unmarshal(out, &s); err != nil {
		t.Fatal(err)
	}
	if s.APIKeyPrivateKey != "0xabc" {
		t.Fatalf("got %q", s.APIKeyPrivateKey)
	}
}
