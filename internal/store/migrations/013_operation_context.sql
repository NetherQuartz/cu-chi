-- Preserve every initiator when several profile changes merge into one server job.
CREATE TABLE job_actors (
 job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
 user_id uuid NOT NULL REFERENCES users(id),
 PRIMARY KEY(job_id,user_id)
);
INSERT INTO job_actors(job_id,user_id) SELECT id,created_by FROM jobs;
ALTER TABLE notifications ADD COLUMN actor_names text NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN operation text NOT NULL DEFAULT '';
-- Older merged jobs only retain their original initiator; never invent other actors.
UPDATE notifications n SET actor_names=u.username
 FROM jobs j JOIN users u ON u.id=j.created_by
 WHERE n.kind='job_failed' AND n.event_key='job:'||j.id::text;

UPDATE notifications n SET operation='migration_cleanup',severity='info'
 FROM jobs j JOIN servers s ON s.id=j.server_id
 WHERE n.kind='job_failed' AND n.event_key='job:'||j.id::text AND j.kind='sync' AND j.error IN ('SSH connection failed','SSH operation timed out or cancelled')
 AND (s.id<>(SELECT default_server_id FROM vpn_preferences)
 AND EXISTS(SELECT 1 FROM profiles p WHERE p.server_id=s.id AND p.state='revoking')
 AND NOT EXISTS(SELECT 1 FROM profiles p WHERE p.server_id=s.id AND p.desired AND p.state='pending')
 AND NOT EXISTS(SELECT 1 FROM profiles p WHERE p.server_id=s.id AND p.state='revoking'
   AND NOT EXISTS(SELECT 1 FROM profiles replacement WHERE replacement.device_id=p.device_id AND replacement.desired AND replacement.server_id<>s.id)));
