CREATE TABLE health_state (
 server_id uuid PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
 attempted_at timestamptz NOT NULL,
 result text NOT NULL CHECK(result IN ('ok','data','management','host_key_mismatch','configuration','skipped')),
 failures integer NOT NULL DEFAULT 0,
 successes integer NOT NULL DEFAULT 0,
 incident_id uuid,
 incident_kind text NOT NULL DEFAULT '',
 opened_at timestamptz
);
ALTER TABLE servers ADD COLUMN maintenance_until timestamptz;
CREATE TABLE notifications (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 event_key text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('outage','management','recovery','reminder','test','job_failed')),
 severity text NOT NULL CHECK(severity IN ('info','warning','critical')),
 server_id uuid REFERENCES servers(id) ON DELETE CASCADE,
 incident_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '1 day',
 read_at timestamptz,
 UNIQUE(user_id,event_key)
);
CREATE INDEX notifications_user_idx ON notifications(user_id,created_at DESC);
