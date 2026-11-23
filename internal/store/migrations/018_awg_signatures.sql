ALTER TABLE servers ADD COLUMN awg_signature text NOT NULL DEFAULT '',
 ADD COLUMN signature_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE profiles ADD COLUMN signature_revision bigint NOT NULL DEFAULT 1;
ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check
 CHECK(kind IN ('outage','management','recovery','reminder','test','job_failed','xray_available','awg_profile_update'));
