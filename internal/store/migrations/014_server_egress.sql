ALTER TABLE servers ADD COLUMN egress_mode text NOT NULL DEFAULT 'direct' CHECK(egress_mode IN ('direct','warp'));
ALTER TABLE servers ADD COLUMN applied_egress_mode text NOT NULL DEFAULT 'direct' CHECK(applied_egress_mode IN ('direct','warp'));
ALTER TABLE servers ADD COLUMN encrypted_warp_config bytea;
