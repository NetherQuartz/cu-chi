-- Retain a deleted target ID so a queued device test can never fan out to other browsers.
ALTER TABLE notifications ADD COLUMN target_subscription_id uuid;
