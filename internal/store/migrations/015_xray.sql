CREATE SEQUENCE xray_revision;
CREATE TABLE xray_nodes (
 server_id uuid PRIMARY KEY REFERENCES servers(id),
 identity bytea NOT NULL, public_key text NOT NULL, short_id text NOT NULL,
 sni text NOT NULL DEFAULT '', revision bigint NOT NULL DEFAULT nextval('xray_revision'),
 applied bigint NOT NULL DEFAULT 0, error text NOT NULL DEFAULT '',
 retry_at timestamptz NOT NULL DEFAULT now(), published boolean NOT NULL DEFAULT true
);
CREATE TABLE xray_devices (
 device_id uuid PRIMARY KEY REFERENCES devices(id),
 secret bytea NOT NULL, token_hash text NOT NULL UNIQUE,
 desired boolean NOT NULL DEFAULT true, revision bigint NOT NULL
);
