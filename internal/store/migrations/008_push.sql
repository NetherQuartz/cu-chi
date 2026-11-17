CREATE TABLE push_identity (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 public_key text NOT NULL,
 encrypted_private_key bytea NOT NULL
);
CREATE TABLE push_subscriptions (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 endpoint_hash text NOT NULL UNIQUE,
 encrypted_subscription bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE notification_preferences (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 outages boolean NOT NULL DEFAULT true,
 recovery boolean NOT NULL DEFAULT true,
 reminders boolean NOT NULL DEFAULT true,
 operations boolean NOT NULL DEFAULT true
);
CREATE TABLE push_outbox (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
 subscription_id uuid NOT NULL REFERENCES push_subscriptions(id) ON DELETE CASCADE,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','accepted','failed','skipped')),
 attempts integer NOT NULL DEFAULT 0,
 next_at timestamptz NOT NULL DEFAULT now(),
 lease uuid,
 last_code integer NOT NULL DEFAULT 0,
 UNIQUE(notification_id,subscription_id)
);
CREATE INDEX push_outbox_due ON push_outbox(next_at) WHERE status IN ('pending','sending');
