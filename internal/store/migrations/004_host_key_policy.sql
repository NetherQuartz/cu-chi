ALTER TABLE servers ADD COLUMN host_key_policy text NOT NULL DEFAULT 'manual'
 CHECK (host_key_policy IN ('manual','tofu'));
