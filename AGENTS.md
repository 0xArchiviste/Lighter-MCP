# AGENTS.md: lighter-mcp

Read this before starting the server, changing proxy behavior, or sending an order. `README.md` is the same facts in user form.

## Hard rules

- **Wallet B only.** Credentials come from `kaiba-orchestrator/.env`, never from `steppe-systems-internals/lighter/.env` (that file has `LIGHTER_ACCOUNT_INDEX=0` and the process will exit).
  - `LIGHTER_ACCOUNT_INDEX=194667`
  - `LIGHTER_API_KEY_INDEX=3`
  - `LIGHTER_API_KEY_PRIVATE_KEY` is the Lighter API key for that slot.
  - Wallet B's Ethereum key is `FUNDING_KEY` in that file. This process reads it only as `LIGHTER_ETH_PRIVATE_KEY`. `WALLET_B_PRIVATE_KEY` is a kaiba alias and is not read here.
- **Never print, log, commit, or pass secrets as arguments.** That covers API keys, `FUNDING_KEY`, Supabase service keys, wallet passwords, and proxy usernames or passwords. `proxies.txt` is gitignored. `proxyrot.Entry.String` is host and port only; keep it that way.
- **Load a narrow environment.** Copy only the `LIGHTER_*` names above, plus `LIGHTER_BASE_URL` (default `https://mainnet.zklighter.elliot.ai`), `LIGHTER_CHAIN_ID` (default `304`), and `LIGHTER_PROXY_FILE`. Do not source the whole kaiba `.env` into the process.
- **Orders are live.** `lighter_place_limit_order`, `lighter_cancel_order`, `lighter_set_tp_sl`, and `lighter_close_position` submit real Lighter transactions. Do not call them unless the user asked for that trade.
- **One listener.** The service is gRPC on `127.0.0.1:9090` with `-grpc-auth=none`. It is not a systemd unit. If the port is already taken, the existing process is the service; do not start another. Restart only when the user asks or the process has exited.
- **Proxy file.** Set `LIGHTER_PROXY_FILE` to the absolute path of this repo's `proxies.txt` unless the working directory is the repo root. A missing default file means direct connections. Sal asked for this rotating pool on 2026-10-01. `kaiba-orchestrator/treasury/AGENTS.md` still says not to proxy Lighter without that decision; this file is the later instruction for this server.

## Start

From this repo, after the narrow env is set:

```bash
go build -o /tmp/lighter-mcp ./cmd/lighter-mcp
/tmp/lighter-mcp -transport=grpc -grpc-addr=127.0.0.1:9090
```

Ready when stderr shows both:

```text
[proxy] rotating N proxies from <path>
gRPC listening on 127.0.0.1:9090 (auth=none) — gRPC-MCP mcp.v1.MCP + lighter.v1.Lighter SDK
```

stdio (`-transport=stdio`, the default) is only for a parent that speaks MCP on the pipes. `-grpc-auth` may be `none`, `bearer`, or `apikey`.

The local process started 2026-10-01 uses Wallet B, account `194667`, API key index `3`, and 100 proxies. Confirm with the listen log before assuming it is still up.

## Proxy behavior

Implemented in `pkg/proxyrot` and wired in `pkg/api.New`.

- Round-robin, one proxy per HTTP request. The same pool serves REST (`pkg/api`) and the SDK nonce and API-key client (`pkg/api/sdk_http.go`).
- Retry the next proxy, at most three times, on dial errors, HTTP 429, 502, 503, 504, and HTTP 403 whose `Content-Type` is not JSON. Return JSON 403 unchanged.
- WebSocket (`pkg/ws`) keeps the single proxy from `Client.NextProxy` for that dial.
- Supabase (`pkg/walletstore`) stays direct.
- Do not log proxy URLs with userinfo.

## Layout

| Path | Role |
|---|---|
| `cmd/lighter-mcp` | stdio MCP and gRPC server |
| `cmd/lighter-sdk` | CLI over the same tools, plus `wallet` for Supabase rows |
| `pkg/lighterapp` | Tool dispatch. Names are `lighter_*` in `pkg/lighterapp/stdio.go` |
| `pkg/lightergrpc` | gRPC listener for `mcp.v1` and `lighter.v1` |
| `pkg/api` | Lighter REST, including `sendTx` |
| `pkg/proxyrot` | Proxy file parser and rotating transport |
| `pkg/config` | Environment loading. `.env` in the working directory is optional and does not override variables already set |

## Checks

```bash
go test ./...
```

`pkg/proxyrot` tests the parser and rotation with fake lines. Do not add a test that reads `proxies.txt` or the kaiba `.env`.
