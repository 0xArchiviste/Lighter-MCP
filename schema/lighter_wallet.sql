-- Encrypted Lighter wallet records (Supabase / Postgres).
-- Apply in Supabase SQL editor or via migrations tooling.

create table if not exists public.lighter_wallet (
  id uuid primary key default gen_random_uuid(),
  name text not null,
  account_index integer not null,
  api_key_index integer not null default 2,
  chain_id integer not null default 304,
  base_url text,
  salt text not null,
  ciphertext text not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (name)
);

create index if not exists lighter_wallet_name_idx on public.lighter_wallet (name);

comment on table public.lighter_wallet is 'Lighter credentials: secrets JSON is AES-256-GCM encrypted; key is Argon2id(master_password || runtime_password, salt). Ciphertext stores nonce||blob from AES-GCM.';
