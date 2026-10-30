-- An admin can stop someone's stream from the Activity screen. The message is
-- shown to the viewer and kept in history.
ALTER TABLE playback_sessions ADD COLUMN terminated_message TEXT;
ALTER TABLE playback_sessions ADD COLUMN terminated_by TEXT;
CREATE INDEX playback_sessions_started ON playback_sessions (started_at);
