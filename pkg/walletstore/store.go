// Package walletstore loads encrypted Lighter wallets from Supabase and merges secrets into config.Config.
package walletstore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/walletcrypto"
)

const tableName = "lighter_wallet"

// Client is a minimal PostgREST client for the wallet table.
type Client struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

// NewClient requires Supabase project URL and service role key (server-side).
func NewClient(baseURL, serviceKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("supabase URL is empty")
	}
	if serviceKey == "" {
		return nil, fmt.Errorf("supabase service key is empty")
	}
	return &Client{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		http:       &http.Client{Timeout: 25 * time.Second},
	}, nil
}

func (c *Client) headers() http.Header {
	h := http.Header{}
	h.Set("apikey", c.serviceKey)
	h.Set("Authorization", "Bearer "+c.serviceKey)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	return h
}

// WalletRow is a DB row (PostgREST JSON uses snake_case from SQL).
type WalletRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AccountIndex int    `json:"account_index"`
	APIKeyIndex  int    `json:"api_key_index"`
	ChainID      int    `json:"chain_id"`
	BaseURL      string `json:"base_url"`
	Salt         string `json:"salt"`
	Ciphertext   string `json:"ciphertext"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// WalletMeta is safe to list (no secrets).
type WalletMeta struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	AccountIndex int    `json:"account_index"`
	APIKeyIndex  int    `json:"api_key_index"`
	ChainID      int    `json:"chain_id"`
	BaseURL      string `json:"base_url,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// SecretsPayload is encrypted at rest.
type SecretsPayload struct {
	APIKeyPrivateKey string `json:"api_key_private_key"`
	ETHPrivateKey    string `json:"eth_private_key,omitempty"`
}

// ListMeta returns wallet rows without ciphertext fields.
func (c *Client) ListMeta(ctx context.Context) ([]WalletMeta, error) {
	u := c.baseURL + "/rest/v1/" + tableName + "?select=id,name,account_index,api_key_index,chain_id,base_url,created_at&order=created_at.desc"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.headers()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list wallets: %s: %s", resp.Status, string(body))
	}
	var rows []WalletMeta
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode list: %w", err)
	}
	return rows, nil
}

// GetByID fetches one wallet row including ciphertext fields.
func (c *Client) GetByID(ctx context.Context, id string) (*WalletRow, error) {
	u := c.baseURL + "/rest/v1/" + tableName + "?id=eq." + url.QueryEscape(id) + "&select=*&limit=1"
	return c.getOne(ctx, u)
}

// GetByName fetches one wallet by unique name.
func (c *Client) GetByName(ctx context.Context, name string) (*WalletRow, error) {
	u := c.baseURL + "/rest/v1/" + tableName + "?name=eq." + url.QueryEscape(name) + "&select=*&limit=1"
	return c.getOne(ctx, u)
}

func (c *Client) getOne(ctx context.Context, u string) (*WalletRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header = c.headers()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("get wallet: %s: %s", resp.Status, string(body))
	}
	var rows []WalletRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("wallet not found")
	}
	return &rows[0], nil
}

// InsertWallet creates a row.
func (c *Client) InsertWallet(ctx context.Context, row map[string]any) error {
	b, err := json.Marshal([]map[string]any{row})
	if err != nil {
		return err
	}
	u := c.baseURL + "/rest/v1/" + tableName
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	h := c.headers()
	h.Set("Prefer", "return=minimal")
	req.Header = h
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("insert wallet: %s: %s", resp.Status, string(body))
	}
	return nil
}

// DeleteByID removes a wallet row.
func (c *Client) DeleteByID(ctx context.Context, id string) error {
	u := c.baseURL + "/rest/v1/" + tableName + "?id=eq." + url.QueryEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header = c.headers()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete wallet: %s: %s", resp.Status, string(body))
	}
	return nil
}

// DeleteByName removes a wallet row by unique name.
func (c *Client) DeleteByName(ctx context.Context, name string) error {
	u := c.baseURL + "/rest/v1/" + tableName + "?name=eq." + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.Header = c.headers()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete wallet: %s: %s", resp.Status, string(body))
	}
	return nil
}

// DecryptRow decrypts ciphertext using master + runtime passwords and merges into cfg.
func DecryptRow(row *WalletRow, masterPassword, runtimePassword string, baseCfg config.Config) (config.Config, error) {
	salt, err := base64.StdEncoding.DecodeString(row.Salt)
	if err != nil {
		return baseCfg, fmt.Errorf("salt: %w", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(row.Ciphertext)
	if err != nil {
		return baseCfg, fmt.Errorf("ciphertext: %w", err)
	}
	key := walletcrypto.DeriveKey(masterPassword, runtimePassword, salt)
	pt, err := walletcrypto.Open(key, sealed)
	if err != nil {
		return baseCfg, fmt.Errorf("decrypt wallet: %w (wrong master/unlock password or corrupt row)", err)
	}
	var sec SecretsPayload
	if err := json.Unmarshal(pt, &sec); err != nil {
		return baseCfg, fmt.Errorf("decode secrets json: %w", err)
	}
	out := baseCfg
	out.APIKeyPrivateKey = sec.APIKeyPrivateKey
	out.ETHPrivateKey = sec.ETHPrivateKey
	out.AccountIndex = uint32(row.AccountIndex)
	out.APIKeyIndex = uint32(row.APIKeyIndex)
	out.ChainID = uint32(row.ChainID)
	if row.BaseURL != "" {
		out.BaseURL = row.BaseURL
	}
	return out, nil
}

// EncryptWalletRow builds DB fields for insert.
func EncryptWalletRow(name string, accountIndex, apiKeyIndex int, chainID int, walletBaseURL, masterPassword, runtimePassword string, sec SecretsPayload) (map[string]any, error) {
	salt, err := walletcrypto.RandomSalt(16)
	if err != nil {
		return nil, err
	}
	key := walletcrypto.DeriveKey(masterPassword, runtimePassword, salt)
	pt, err := json.Marshal(sec)
	if err != nil {
		return nil, err
	}
	sealed, err := walletcrypto.Seal(pt, key)
	if err != nil {
		return nil, err
	}
	row := map[string]any{
		"name":          name,
		"account_index": accountIndex,
		"api_key_index": apiKeyIndex,
		"chain_id":      chainID,
		"salt":          base64.StdEncoding.EncodeToString(salt),
		"ciphertext":    base64.StdEncoding.EncodeToString(sealed),
		"updated_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if walletBaseURL != "" {
		row["base_url"] = walletBaseURL
	}
	return row, nil
}

// Resolve merges Supabase wallet secrets into cfg when WalletBackend is "supabase".
func Resolve(ctx context.Context, cfg config.Config) (config.Config, error) {
	if !strings.EqualFold(cfg.WalletBackend, "supabase") {
		return cfg, nil
	}
	supabaseURL := cfg.SupabaseURL
	if supabaseURL == "" {
		supabaseURL = os.Getenv("SUPABASE_URL")
	}
	svcKey := cfg.SupabaseServiceKey
	if svcKey == "" {
		svcKey = os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	}
	if supabaseURL == "" || svcKey == "" {
		return cfg, fmt.Errorf("supabase wallet backend requires LIGHTER_SUPABASE_URL + LIGHTER_SUPABASE_SERVICE_KEY, or SUPABASE_URL + SUPABASE_SERVICE_ROLE_KEY")
	}
	cl, err := NewClient(supabaseURL, svcKey)
	if err != nil {
		return cfg, err
	}
	var row *WalletRow
	switch {
	case cfg.WalletID != "":
		row, err = cl.GetByID(ctx, cfg.WalletID)
	case cfg.WalletName != "":
		row, err = cl.GetByName(ctx, cfg.WalletName)
	default:
		return cfg, fmt.Errorf("supabase wallet backend requires LIGHTER_WALLET_ID or LIGHTER_WALLET_NAME")
	}
	if err != nil {
		return cfg, err
	}
	master := cfg.WalletMasterPassword
	if master == "" {
		master = os.Getenv("LIGHTER_WALLET_MASTER_PASSWORD")
	}
	unlock := cfg.WalletUnlockPassword
	if unlock == "" {
		unlock = os.Getenv("LIGHTER_WALLET_UNLOCK_PASSWORD")
	}
	if master == "" || unlock == "" {
		return cfg, fmt.Errorf("supabase wallet backend requires LIGHTER_WALLET_MASTER_PASSWORD and LIGHTER_WALLET_UNLOCK_PASSWORD")
	}
	return DecryptRow(row, master, unlock, cfg)
}

// NewClientFromConfig builds a store client using resolved Supabase URL/key from env or cfg.
func NewClientFromConfig(cfg config.Config) (*Client, error) {
	u := cfg.SupabaseURL
	if u == "" {
		u = os.Getenv("SUPABASE_URL")
	}
	k := cfg.SupabaseServiceKey
	if k == "" {
		k = os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	}
	return NewClient(u, k)
}
