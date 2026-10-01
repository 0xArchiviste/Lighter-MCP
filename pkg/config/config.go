package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds Lighter API credentials and defaults for this MCP server.
type Config struct {
	BaseURL          string
	AccountIndex     uint32
	APIKeyIndex      uint32
	APIKeyPrivateKey string
	ETHPrivateKey    string // Optional; used by some account flows outside MCP
	ChainID          uint32 // Lighter chain ID (default: 304 for mainnet)

	// WalletBackend: "" or "env" uses LIGHTER_* secrets below. "supabase" loads an encrypted row.
	WalletBackend        string
	SupabaseURL          string
	SupabaseServiceKey   string
	WalletMasterPassword string // Prefer env LIGHTER_WALLET_MASTER_PASSWORD; never log.
	WalletUnlockPassword string // Runtime unlock; prefer env LIGHTER_WALLET_UNLOCK_PASSWORD.
	WalletID             string // UUID of row in lighter_wallet
	WalletName           string // Unique wallet name (alternative to ID)

	// ProxyFile is a host:port:user:pass list. Lighter HTTP uses it round-robin.
	// Empty means proxies.txt in the working directory when that file exists.
	ProxyFile string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseUint32(s string) uint32 {
	v, _ := strconv.ParseUint(s, 10, 32)
	return uint32(v)
}

// Load reads configuration from the environment (and optional .env in cwd).
func Load() Config {
	_ = godotenv.Load()
	_ = godotenv.Load(".env")

	backend := strings.TrimSpace(strings.ToLower(getenv("LIGHTER_WALLET_BACKEND", "")))
	if backend == "" {
		backend = "env"
	}

	return Config{
		BaseURL:          getenv("LIGHTER_BASE_URL", "https://mainnet.zklighter.elliot.ai"),
		AccountIndex:     parseUint32(getenv("LIGHTER_ACCOUNT_INDEX", "0")),
		APIKeyIndex:      parseUint32(getenv("LIGHTER_API_KEY_INDEX", "2")),
		APIKeyPrivateKey: getenv("LIGHTER_API_KEY_PRIVATE_KEY", ""),
		ETHPrivateKey:    getenv("LIGHTER_ETH_PRIVATE_KEY", ""),
		ChainID:          parseUint32(getenv("LIGHTER_CHAIN_ID", "304")),

		WalletBackend:        backend,
		SupabaseURL:          getenv("LIGHTER_SUPABASE_URL", ""),
		SupabaseServiceKey:   getenv("LIGHTER_SUPABASE_SERVICE_KEY", ""),
		WalletMasterPassword: getenv("LIGHTER_WALLET_MASTER_PASSWORD", ""),
		WalletUnlockPassword: getenv("LIGHTER_WALLET_UNLOCK_PASSWORD", ""),
		WalletID:             getenv("LIGHTER_WALLET_ID", ""),
		WalletName:           getenv("LIGHTER_WALLET_NAME", ""),
		ProxyFile:            getenv("LIGHTER_PROXY_FILE", "proxies.txt"),
	}
}
