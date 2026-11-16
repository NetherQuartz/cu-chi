CREATE TABLE users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 issuer text NOT NULL,
 subject text NOT NULL,
 username text NOT NULL,
 role text NOT NULL CHECK (role IN ('admin', 'user', 'denied')),
 created_at timestamptz NOT NULL DEFAULT now(),
 last_login_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (issuer, subject)
);
CREATE TABLE sessions (
 token_hash text PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 csrf_token text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);
CREATE TABLE login_attempts (
 state_hash text PRIMARY KEY,
 encrypted_payload bytea NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE TABLE devices (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
 platform text NOT NULL CHECK (platform IN ('ios','android','windows','macos','gateway')),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id, name)
);
CREATE INDEX devices_owner_idx ON devices(user_id);
CREATE TABLE servers (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
 hostname text NOT NULL,
 ssh_port integer NOT NULL CHECK (ssh_port BETWEEN 1 AND 65535),
 ssh_user text NOT NULL,
 host_key_fingerprint text NOT NULL,
 encrypted_private_key bytea NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(hostname, ssh_port)
);
CREATE TABLE audit_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL,
 resource_id text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
