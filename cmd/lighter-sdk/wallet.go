package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/walletstore"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	walletCmd := &cobra.Command{
		Use:   "wallet",
		Short: "Manage Supabase-backed encrypted wallets",
	}
	walletCmd.AddCommand(walletListCmd())
	walletCmd.AddCommand(walletAddCmd())
	walletCmd.AddCommand(walletDeleteCmd())
	rootCmd.AddCommand(walletCmd)
}

func walletListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List wallet metadata (no secrets)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Load()
			cl, err := walletstore.NewClientFromConfig(cfg)
			if err != nil {
				return err
			}
			rows, err := cl.ListMeta(cmd.Context())
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		},
	}
}

func walletAddCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "add",
		Short: "Encrypt and store a wallet row in Supabase",
		Long: `Requires Supabase URL + service key (LIGHTER_SUPABASE_* or SUPABASE_* env).
Uses Argon2id(master_password || runtime_password, per-wallet salt) then AES-256-GCM for secrets JSON.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Load()
			cl, err := walletstore.NewClientFromConfig(cfg)
			if err != nil {
				return err
			}
			name, _ := cmd.Flags().GetString("name")
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("--name is required")
			}
			acc, _ := cmd.Flags().GetInt("account-index")
			if acc <= 0 {
				return fmt.Errorf("--account-index must be > 0")
			}
			apiIdx, _ := cmd.Flags().GetInt("api-key-index")
			chain, _ := cmd.Flags().GetInt("chain-id")
			baseURL, _ := cmd.Flags().GetString("base-url")
			apiPriv, _ := cmd.Flags().GetString("api-key-private")
			ethPriv, _ := cmd.Flags().GetString("eth-private-key")
			if strings.TrimSpace(apiPriv) == "" {
				return fmt.Errorf("--api-key-private is required")
			}

			master := os.Getenv("LIGHTER_WALLET_MASTER_PASSWORD")
			if master == "" {
				fmt.Fprint(os.Stderr, "Master password (LIGHTER_WALLET_MASTER_PASSWORD not set): ")
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					return err
				}
				master = string(b)
				fmt.Fprintln(os.Stderr)
			}
			unlock := os.Getenv("LIGHTER_WALLET_UNLOCK_PASSWORD")
			if unlock == "" {
				fmt.Fprint(os.Stderr, "Runtime unlock password: ")
				b1, err := term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr)
				fmt.Fprint(os.Stderr, "Repeat runtime unlock password: ")
				b2, err := term.ReadPassword(int(os.Stdin.Fd()))
				if err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr)
				if string(b1) != string(b2) {
					return fmt.Errorf("unlock passwords do not match")
				}
				unlock = string(b1)
			}

			sec := walletstore.SecretsPayload{
				APIKeyPrivateKey: strings.TrimSpace(apiPriv),
				ETHPrivateKey:    strings.TrimSpace(ethPriv),
			}
			row, err := walletstore.EncryptWalletRow(name, acc, apiIdx, chain, baseURL, master, unlock, sec)
			if err != nil {
				return err
			}
			if err := cl.InsertWallet(context.Background(), row); err != nil {
				return err
			}
			got, err := cl.GetByName(context.Background(), name)
			if err != nil {
				return fmt.Errorf("stored wallet but failed to re-read: %w", err)
			}
			fmt.Fprintf(os.Stdout, "wallet created id=%s name=%s\n", got.ID, got.Name)
			return nil
		},
	}
	c.Flags().String("name", "", "unique wallet name")
	c.Flags().Int("account-index", 0, "Lighter account index")
	c.Flags().Int("api-key-index", 2, "Lighter API key index")
	c.Flags().Int("chain-id", 304, "Lighter chain id")
	c.Flags().String("base-url", "", "optional LIGHTER_BASE_URL override stored with wallet")
	c.Flags().String("api-key-private", "", "LIGHTER-style API private key (sensitive)")
	c.Flags().String("eth-private-key", "", "optional ETH private key")
	_ = c.MarkFlagRequired("name")
	_ = c.MarkFlagRequired("account-index")
	return c
}

func walletDeleteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "delete",
		Short: "Delete a wallet by id or name",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Load()
			cl, err := walletstore.NewClientFromConfig(cfg)
			if err != nil {
				return err
			}
			id, _ := cmd.Flags().GetString("id")
			name, _ := cmd.Flags().GetString("name")
			switch {
			case id != "":
				return cl.DeleteByID(cmd.Context(), id)
			case name != "":
				return cl.DeleteByName(cmd.Context(), name)
			default:
				return fmt.Errorf("provide --id or --name")
			}
		},
	}
	c.Flags().String("id", "", "wallet uuid")
	c.Flags().String("name", "", "wallet unique name")
	return c
}
