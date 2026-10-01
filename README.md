# lighter-mcp

Lighter.xyz trading server for people and agents. One process exposes the same tools over stdio MCP and over gRPC (`mcp.v1.MCP` plus `lighter.v1.Lighter`). HTTP and WebSocket calls to Lighter go through the rotating proxy list in `proxies.txt` when that file is present.

## Run

The long-running service is gRPC on loopback. stdio is for a parent process that owns the pipes.

```bash
go build -o /tmp/lighter-mcp ./cmd/lighter-mcp
/tmp/lighter-mcp -transport=grpc -grpc-addr=127.0.0.1:9090
```

Flags:

| Flag | Default | Meaning |
|---|---|---|
| `-transport` | `stdio` | `stdio` or `grpc` |
| `-grpc-addr` | `127.0.0.1:9090` | Listen address for `-transport=grpc` |
| `-grpc-auth` | `none` | `none`, `bearer`, or `apikey` |
| `-grpc-api-key` | `dev-key` | Expected `x-mcp-api-key` when `-grpc-auth=apikey` |

A healthy start prints two lines:

```text
[proxy] rotating N proxies from <path>
gRPC listening on 127.0.0.1:9090 (auth=none) — gRPC-MCP mcp.v1.MCP + lighter.v1.Lighter SDK
```

There is no systemd unit. If nothing is listening on `127.0.0.1:9090`, start it again. Do not start a second copy on the same port.

`lighter-sdk` is the same API as a CLI (`balance`, `markets`, `positions`, `order`, and so on). It reads the same environment.

## Wallet

Trading uses Wallet B. The values live in `kaiba-orchestrator/.env` (mode `0600`, not in git):

| Variable | Role |
|---|---|
| `LIGHTER_ACCOUNT_INDEX` | Wallet B's Lighter account. Current value: `194667`. |
| `LIGHTER_API_KEY_INDEX` | API key slot. Current value: `3`. |
| `LIGHTER_API_KEY_PRIVATE_KEY` | Lighter API key for that account and slot. |
| `FUNDING_KEY` | Wallet B's Ethereum key. Pass it to this process as `LIGHTER_ETH_PRIVATE_KEY` when an L1 signature is required. `WALLET_B_PRIVATE_KEY` is an alias in kaiba, not a name this server reads. |

Do not use `steppe-systems-internals/lighter/.env`. Its `LIGHTER_ACCOUNT_INDEX` is `0`, and this server exits when the account index is zero.

Required or the process exits: `LIGHTER_API_KEY_PRIVATE_KEY`, and a non-zero `LIGHTER_ACCOUNT_INDEX`. Optional Supabase wallets use `LIGHTER_WALLET_BACKEND=supabase` plus `LIGHTER_WALLET_ID` or `LIGHTER_WALLET_NAME`, the master and unlock passwords, and the Supabase URL and service key. See `lighter-sdk wallet`.

Other defaults: `LIGHTER_BASE_URL=https://mainnet.zklighter.elliot.ai`, `LIGHTER_CHAIN_ID=304`, `LIGHTER_API_KEY_INDEX=2` only when the variable is unset. Wallet B's slot is `3`, so set it explicitly.

## Proxies

`LIGHTER_PROXY_FILE` defaults to `proxies.txt` in the working directory. Point it at an absolute path if the process is not started from this repo.

Each non-empty, non-comment line is one proxy:

```text
host:port:user:pass
host:port
https://user:pass@host:port
```

Lighter REST uses the next proxy on every request. A dead connection, HTTP 429, 502, 503, or 504, or a non-JSON HTTP 403, is tried on the next proxy, up to three times. A JSON 403 is the API's own answer and is returned as-is. A WebSocket dial keeps the one proxy chosen for that connection. Supabase wallet calls are not proxied.

A missing default `proxies.txt` means direct connections. A bad line, or a `LIGHTER_PROXY_FILE` that cannot be read, is logged and the process continues direct.

`proxies.txt` is gitignored. Do not commit it. Logs print the file path and the count, not usernames or passwords.

## Tools

The stdio names and the gRPC methods are the same operations:

| Tool | What it does |
|---|---|
| `lighter_get_balance` | Collateral and account info for the configured account. |
| `lighter_list_markets` | Markets with prices. Optional `limit` (default 10). |
| `lighter_get_market` | One market by `symbol` (`ETH`, `BTC`, …). |
| `lighter_list_positions` | Open positions. Optional `market` filter. |
| `lighter_place_limit_order` | Good-till-time limit order: `market`, `side` (`buy` or `sell`), `price`, `size`. |
| `lighter_cancel_order` | Cancel by `market` and `client_order_index`. |
| `lighter_set_tp_sl` | Take-profit and stop-loss sized to the open position: `market`, `tp_price`, `sl_price`. |
| `lighter_close_position` | Close the position on `market`. |
| `lighter_calculate_indicator` | `rsi`, `macd`, `sma`, `ema`, `bollinger`, `atr`, or `all`. Optional `period` (default 14). |

Order, cancel, take-profit, stop-loss, and close send real transactions.

## Build

```bash
make build   # ./lighter-mcp and ./lighter-sdk
make test
make proto   # only when the .proto files change
```
