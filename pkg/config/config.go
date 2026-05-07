package config

import (
	"os"
	"strconv"

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

	return Config{
		BaseURL:          getenv("LIGHTER_BASE_URL", "https://mainnet.zklighter.elliot.ai"),
		AccountIndex:     parseUint32(getenv("LIGHTER_ACCOUNT_INDEX", "0")),
		APIKeyIndex:      parseUint32(getenv("LIGHTER_API_KEY_INDEX", "2")),
		APIKeyPrivateKey: getenv("LIGHTER_API_KEY_PRIVATE_KEY", ""),
		ETHPrivateKey:    getenv("LIGHTER_ETH_PRIVATE_KEY", ""),
		ChainID:          parseUint32(getenv("LIGHTER_CHAIN_ID", "304")),
	}
}
