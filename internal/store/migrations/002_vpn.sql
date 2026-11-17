ALTER TABLE servers ADD COLUMN endpoint text NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN vpn_port integer NOT NULL DEFAULT 51820 CHECK (vpn_port BETWEEN 1 AND 65535);
ALTER TABLE servers ADD COLUMN vpn_state text NOT NULL DEFAULT 'new';
ALTER TABLE servers ADD COLUMN vpn_error text NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN encrypted_vpn_settings bytea;
ALTER TABLE servers ADD COLUMN desired_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN applied_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN next_address integer NOT NULL DEFAULT 2;
ALTER TABLE servers ADD COLUMN checked_at timestamptz;
CREATE TABLE vpn_preferences (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 default_server_id uuid REFERENCES servers(id)
);
INSERT INTO vpn_preferences(singleton) VALUES(true);
CREATE TABLE profiles (
 id uuid PRIMARY KEY,
 device_id uuid NOT NULL REFERENCES devices(id),
 server_id uuid NOT NULL REFERENCES servers(id),
 revision integer NOT NULL,
 address text NOT NULL,
 public_key text NOT NULL UNIQUE,
 encrypted_keys bytea NOT NULL,
 desired boolean NOT NULL DEFAULT true,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','ready','revoking','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(),
 applied_at timestamptz,
 UNIQUE(device_id,revision), UNIQUE(server_id,address)
);
CREATE UNIQUE INDEX profiles_desired_device ON profiles(device_id) WHERE desired;
CREATE TABLE jobs (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 server_id uuid NOT NULL REFERENCES servers(id),
 kind text NOT NULL CHECK(kind IN ('check','deploy','sync')),
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed')),
 stage text NOT NULL DEFAULT 'queued',
 run_token uuid,
 attempts integer NOT NULL DEFAULT 0,
 error text NOT NULL DEFAULT '',
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 finished_at timestamptz,
 run_after timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX jobs_pending_server ON jobs(server_id) WHERE status='queued';
CREATE INDEX jobs_schedule ON jobs(run_after,created_at) WHERE status='queued';
