CREATE TABLE ssh_keys (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
 public_key text NOT NULL,
 fingerprint text NOT NULL,
 encrypted_private_key bytea NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE servers ADD COLUMN ssh_auth_method text NOT NULL DEFAULT 'key'
 CHECK (ssh_auth_method IN ('key','password','saved_key'));
ALTER TABLE servers ADD COLUMN ssh_key_id uuid REFERENCES ssh_keys(id) ON DELETE RESTRICT;
ALTER TABLE servers ALTER COLUMN encrypted_private_key DROP NOT NULL;
-- Legacy column also stores encrypted passwords; associated data remains server:<id>.
ALTER TABLE servers ADD CONSTRAINT server_ssh_access CHECK (
 (ssh_auth_method = 'saved_key' AND ssh_key_id IS NOT NULL AND encrypted_private_key IS NULL)
 OR (ssh_auth_method IN ('key','password') AND ssh_key_id IS NULL AND encrypted_private_key IS NOT NULL)
);
