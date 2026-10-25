package ws

import "testing"

func TestNewClientPath(t *testing.T) {
	cases := []struct {
		env, base, want string
	}{
		{"", "https://mainnet.zklighter.elliot.ai", "wss://mainnet.zklighter.elliot.ai/stream"},
		{"", "http://localhost:8080/", "ws://localhost:8080/stream"},
		{"/custom", "https://example.com", "wss://example.com/custom"},
	}
	for _, tc := range cases {
		t.Setenv("LIGHTER_WS_PATH", tc.env)
		c := NewClient(tc.base, "", nil)
		if c.wsURL != tc.want {
			t.Errorf("env=%q base=%q: got %q, want %q", tc.env, tc.base, c.wsURL, tc.want)
		}
		c.cancel()
	}
}
