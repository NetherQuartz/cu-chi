ALTER TABLE servers ADD COLUMN deleted_at timestamptz;
ALTER TABLE servers ADD COLUMN edit_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE servers ADD COLUMN display_name text GENERATED ALWAYS AS
 (name || CASE WHEN deleted_at IS NULL THEN '' ELSE ' (удалён)' END) STORED;

ALTER TABLE servers DROP CONSTRAINT servers_hostname_ssh_port_key;
CREATE UNIQUE INDEX servers_active_address ON servers(hostname,ssh_port) WHERE deleted_at IS NULL;

ALTER TABLE servers DROP CONSTRAINT server_ssh_access;
ALTER TABLE servers ADD CONSTRAINT server_ssh_access CHECK (
 (deleted_at IS NOT NULL AND ssh_key_id IS NULL AND encrypted_private_key IS NULL)
 OR (deleted_at IS NULL AND (
   (ssh_auth_method='saved_key' AND ssh_key_id IS NOT NULL AND encrypted_private_key IS NULL)
   OR (ssh_auth_method IN ('key','password') AND ssh_key_id IS NULL AND encrypted_private_key IS NOT NULL)
 ))
);

ALTER TABLE profiles DROP CONSTRAINT profiles_state_check;
ALTER TABLE profiles ADD CONSTRAINT profiles_state_check CHECK(state IN ('pending','ready','revoking','revoked','archived'));
ALTER TABLE jobs DROP CONSTRAINT jobs_status_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK(status IN ('queued','running','succeeded','failed','cancelled'));

CREATE TABLE archived_xray_devices (
 device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
 server_id uuid NOT NULL REFERENCES servers(id),
 PRIMARY KEY(device_id,server_id)
);

ALTER TABLE audit_events ADD COLUMN server_id uuid REFERENCES servers(id);
UPDATE audit_events a SET server_id=s.id FROM servers s
 WHERE (a.action LIKE 'server.%' OR a.action='xray.deploy') AND s.id::text=a.resource_id;
UPDATE audit_events a SET server_id=p.server_id FROM profiles p
 WHERE a.action='profile.issued' AND p.id::text=a.resource_id;
