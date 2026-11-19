ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check
 CHECK (kind IN ('outage','management','recovery','reminder','test','job_failed','xray_available'));

ALTER TABLE xray_nodes ADD COLUMN availability_announced boolean NOT NULL DEFAULT false;
-- Servers already offered in subscriptions are not new when the application upgrades.
UPDATE xray_nodes SET availability_announced=true WHERE published AND applied>0;
