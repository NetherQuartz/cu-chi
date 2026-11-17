ALTER TABLE notifications ADD COLUMN gateway_id uuid REFERENCES gateways(id) ON DELETE CASCADE;
