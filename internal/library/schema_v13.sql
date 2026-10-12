CREATE TABLE chat_sessions (
 id TEXT PRIMARY KEY, state_json TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL
);
CREATE TABLE chat_turns (
 id INTEGER PRIMARY KEY, session_id TEXT NOT NULL REFERENCES chat_sessions(id),
 request_token TEXT NOT NULL, request_hash TEXT NOT NULL, user_text TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('planning','applying','done','interrupted')),
 created_at TEXT NOT NULL, deadline INTEGER NOT NULL, response_json TEXT NOT NULL DEFAULT '',
 UNIQUE(session_id,request_token)
);
CREATE INDEX chat_history ON chat_turns(session_id,id DESC);
CREATE TRIGGER immutable_chat_input BEFORE UPDATE ON chat_turns
WHEN NEW.session_id<>OLD.session_id OR NEW.request_token<>OLD.request_token OR NEW.request_hash<>OLD.request_hash OR NEW.user_text<>OLD.user_text
BEGIN SELECT RAISE(ABORT,'immutable chat input'); END;
PRAGMA user_version=13;
