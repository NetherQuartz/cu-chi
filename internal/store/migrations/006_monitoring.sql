ALTER TABLE servers ADD COLUMN country_code text NOT NULL DEFAULT '' CHECK(country_code='' OR country_code ~ '^[A-Z]{2}$');
ALTER TABLE servers ADD COLUMN city text NOT NULL DEFAULT '' CHECK(length(city)<=80);
CREATE TABLE monitor_state (
 server_id uuid PRIMARY KEY REFERENCES servers(id),
 attempted_at timestamptz NOT NULL,
 succeeded_at timestamptz,
 error text NOT NULL DEFAULT '',
 failures integer NOT NULL DEFAULT 0,
 sample jsonb
);
CREATE TABLE host_samples (
 server_id uuid NOT NULL REFERENCES servers(id), at timestamptz NOT NULL, point jsonb NOT NULL,
 PRIMARY KEY(server_id,at)
);
CREATE INDEX host_samples_expiry ON host_samples(at);
CREATE TABLE peer_counters (
 profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
 epoch text NOT NULL, rx bigint NOT NULL, tx bigint NOT NULL, at timestamptz NOT NULL,
 handshake timestamptz
);
-- Historical totals deliberately survive device/profile deletion. Owner and name are captured at measurement time.
CREATE TABLE traffic_totals (
 device_id uuid NOT NULL, server_id uuid NOT NULL REFERENCES servers(id), user_id uuid NOT NULL REFERENCES users(id),
 device_name text NOT NULL, upload bigint NOT NULL DEFAULT 0, download bigint NOT NULL DEFAULT 0,
 first_at timestamptz NOT NULL, last_at timestamptz NOT NULL, handshake timestamptz,
 PRIMARY KEY(device_id,server_id)
);
CREATE INDEX traffic_owner ON traffic_totals(user_id);
CREATE TABLE traffic_samples (
 device_id uuid NOT NULL, server_id uuid NOT NULL, at timestamptz NOT NULL,
 upload bigint NOT NULL CHECK(upload>=0), download bigint NOT NULL CHECK(download>=0), gap boolean NOT NULL, seconds double precision NOT NULL DEFAULT 0,
 PRIMARY KEY(device_id,server_id,at),
 FOREIGN KEY(device_id,server_id) REFERENCES traffic_totals(device_id,server_id)
);
CREATE INDEX traffic_samples_expiry ON traffic_samples(at);
