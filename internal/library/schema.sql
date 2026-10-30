-- Baseline schema of news-library 0.1.0 (no migrations, no earlier versions).
-- Changes to the model edit this file; databases of other builds are refused by Open.
CREATE TABLE items (
 id INTEGER PRIMARY KEY, original_url TEXT NOT NULL, canonical_url TEXT NOT NULL UNIQUE,
 title TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL,
 is_read INTEGER NOT NULL DEFAULT 0 CHECK(is_read IN (0,1)),
 state TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '',
 rating INTEGER CHECK(rating BETWEEN 0 AND 6), rated_at TEXT CHECK(rated_at IS NULL OR rating IS NOT NULL),
 filter_state TEXT NOT NULL DEFAULT 'not_checked' CHECK(filter_state IN ('not_checked','passed','rejected','overridden')),
 filter_reason TEXT NOT NULL DEFAULT '');
CREATE TABLE source_aliases (
 url TEXT PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id)
);
CREATE TABLE jobs (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','failed','cancelled')), attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0, lease_until INTEGER NOT NULL DEFAULT 0,
 token TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT ''
, stage TEXT NOT NULL DEFAULT 'fetch' CHECK(stage IN ('fetch','digest')), config_version TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '', prompt_version TEXT NOT NULL DEFAULT '', input_limit INTEGER NOT NULL DEFAULT 0, refresh INTEGER NOT NULL DEFAULT 0, filter_override INTEGER NOT NULL DEFAULT 0 CHECK(filter_override IN (0,1)), interests TEXT NOT NULL DEFAULT '', chunk_limit INTEGER NOT NULL DEFAULT 0, chunk_max_parts INTEGER NOT NULL DEFAULT 0,
 failure_kind TEXT NOT NULL DEFAULT '' CHECK(failure_kind IN ('','temporary','format','configuration','limit','source','unknown')),
 failure_retryable INTEGER NOT NULL DEFAULT 0 CHECK(failure_retryable IN (0,1)),
 response_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '');
CREATE TABLE texts (
 item_id INTEGER PRIMARY KEY REFERENCES items(id), title TEXT NOT NULL,
 hash TEXT NOT NULL, retrieved_at TEXT NOT NULL, origin TEXT NOT NULL,
 extractor TEXT NOT NULL, completeness TEXT NOT NULL, warning TEXT NOT NULL,
 text_path TEXT NOT NULL
);
CREATE TABLE item_tags (
 item_id INTEGER NOT NULL REFERENCES items(id), tag TEXT NOT NULL, origin TEXT NOT NULL DEFAULT 'human',
 PRIMARY KEY(item_id,tag,origin)
);
CREATE TABLE digests (
 item_id INTEGER PRIMARY KEY REFERENCES items(id),
 model TEXT NOT NULL, actual_model TEXT NOT NULL, provider TEXT NOT NULL,
 prompt_version TEXT NOT NULL, created_at TEXT NOT NULL, input_tokens INTEGER NOT NULL DEFAULT 0,
 output_tokens INTEGER NOT NULL DEFAULT 0,
 markdown_path TEXT NOT NULL, hash TEXT NOT NULL, chunk_count INTEGER NOT NULL DEFAULT 1
);
CREATE VIRTUAL TABLE item_fts USING fts5(title,text,url,content='',contentless_delete=1,tokenize='unicode61');
CREATE TABLE cache_entries (url TEXT PRIMARY KEY,final_url TEXT NOT NULL,content_type TEXT NOT NULL,body_path TEXT NOT NULL,hash TEXT NOT NULL,fetched_at TEXT NOT NULL,expires_at INTEGER NOT NULL,partial INTEGER NOT NULL, last_accessed INTEGER NOT NULL DEFAULT 0, response_headers TEXT NOT NULL DEFAULT '{}', checked_at TEXT NOT NULL DEFAULT '');
CREATE TABLE digest_feedback (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 quality TEXT NOT NULL CHECK(quality IN ('good','inaccurate','incomplete','unclear')), note TEXT NOT NULL, recorded_at TEXT NOT NULL
);
CREATE TABLE interest_predictions (
 item_id INTEGER PRIMARY KEY REFERENCES digests(item_id), probability REAL NOT NULL CHECK(probability BETWEEN 0 AND 1),
 reason TEXT NOT NULL, profile TEXT NOT NULL, profile_hash TEXT NOT NULL
);
CREATE TABLE digest_checkpoints (
 job_id INTEGER NOT NULL REFERENCES jobs(id), round INTEGER NOT NULL, part INTEGER NOT NULL,
 input_hash TEXT NOT NULL, markdown_path TEXT NOT NULL, hash TEXT NOT NULL,
 actual_model TEXT NOT NULL, provider TEXT NOT NULL, created_at TEXT NOT NULL, input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
 PRIMARY KEY(job_id,round,part)
);
CREATE TABLE embedding_runs (
 item_id INTEGER NOT NULL REFERENCES texts(item_id), version TEXT NOT NULL,
 model_revision TEXT NOT NULL, chunk_bytes INTEGER NOT NULL, dimensions INTEGER NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','done','failed','cancelled')),
 next_part INTEGER NOT NULL DEFAULT 0, total_parts INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0, next_attempt INTEGER NOT NULL DEFAULT 0,
 token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL,
 PRIMARY KEY(item_id,version)
);
CREATE TABLE embedding_chunks (
 item_id INTEGER NOT NULL, version TEXT NOT NULL, part INTEGER NOT NULL,
 start_byte INTEGER NOT NULL, end_byte INTEGER NOT NULL, vector TEXT NOT NULL,
 PRIMARY KEY(item_id,version,part),
 FOREIGN KEY(item_id,version) REFERENCES embedding_runs(item_id,version) ON DELETE CASCADE
);
CREATE TABLE connection_notes (
 id INTEGER PRIMARY KEY, stable_id TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL
);
CREATE TABLE connection_versions (
 id INTEGER PRIMARY KEY, note_id INTEGER NOT NULL REFERENCES connection_notes(id),
 revision INTEGER NOT NULL, title TEXT NOT NULL, category TEXT NOT NULL,
 tags_json TEXT NOT NULL, questions_json TEXT NOT NULL, body TEXT NOT NULL, search_text TEXT NOT NULL,
 markdown_path TEXT NOT NULL, hash TEXT NOT NULL, created_at TEXT NOT NULL, author TEXT NOT NULL DEFAULT 'user', model TEXT NOT NULL DEFAULT '', prompt_version TEXT NOT NULL DEFAULT '',
 UNIQUE(note_id,revision)
);
CREATE TABLE connection_members (
 version_id INTEGER NOT NULL REFERENCES connection_versions(id),
 item_id INTEGER NOT NULL REFERENCES items(id), role TEXT NOT NULL,
 PRIMARY KEY(version_id,item_id)
);
CREATE TABLE knowledge_runs (
 id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id),
 model TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','done','failed','cancelled')),
 attempts INTEGER NOT NULL DEFAULT 0, token TEXT NOT NULL DEFAULT '', lease_until INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
 linked_count INTEGER NOT NULL DEFAULT 0, UNIQUE(item_id,prompt_version,model)
);
CREATE TABLE article_knowledge (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL UNIQUE REFERENCES knowledge_runs(id),
 item_id INTEGER NOT NULL REFERENCES items(id), category TEXT NOT NULL,
 tags_json TEXT NOT NULL, questions_json TEXT NOT NULL, search_text TEXT NOT NULL, markdown_path TEXT NOT NULL, hash TEXT NOT NULL,
 model TEXT NOT NULL, prompt_version TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE knowledge_failures (
 id INTEGER PRIMARY KEY, run_id INTEGER NOT NULL REFERENCES knowledge_runs(id), kind TEXT NOT NULL,
 message TEXT NOT NULL, recorded_at TEXT NOT NULL, response_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT ''
);
CREATE TABLE knowledge_batches (
 request_token TEXT PRIMARY KEY, batch_limit INTEGER NOT NULL, include_retry INTEGER NOT NULL,
 created_at TEXT NOT NULL, result_json TEXT NOT NULL
);
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
CREATE TABLE trash_entries (
 item_id INTEGER PRIMARY KEY REFERENCES items(id),
 reason TEXT NOT NULL, moved_at TEXT NOT NULL
);
CREATE TABLE search_events (
 id INTEGER PRIMARY KEY,
 query TEXT NOT NULL, mode TEXT NOT NULL, actual_mode TEXT NOT NULL,
 filters TEXT NOT NULL, result_ids TEXT NOT NULL,
 created_at TEXT NOT NULL, not_found_at TEXT
);
CREATE TABLE search_judgments (
 id INTEGER PRIMARY KEY,
 event_id INTEGER NOT NULL REFERENCES search_events(id),
 item_id INTEGER NOT NULL REFERENCES items(id),
 score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 2),
 recorded_at TEXT NOT NULL
);
CREATE TABLE completeness_confirmations (
 id INTEGER PRIMARY KEY,
 item_id INTEGER NOT NULL REFERENCES texts(item_id),
 complete INTEGER NOT NULL CHECK(complete IN (0,1)),
 note TEXT NOT NULL DEFAULT '',
 recorded_at TEXT NOT NULL
);
CREATE VIEW active_items AS SELECT * FROM items
 WHERE NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=items.id);
CREATE UNIQUE INDEX one_active_fetch ON jobs(item_id) WHERE stage='fetch' AND state IN ('queued','running','retry_wait');
CREATE UNIQUE INDEX one_digest_job ON jobs(item_id,config_version) WHERE stage='digest';
CREATE INDEX embedding_version_state ON embedding_runs(version,state,item_id);
CREATE INDEX embedding_chunk_version ON embedding_chunks(version,item_id);
CREATE INDEX connection_backlinks ON connection_members(item_id,version_id);
CREATE INDEX knowledge_current ON knowledge_runs(item_id,updated_at DESC,id DESC);
CREATE INDEX chat_history ON chat_turns(session_id,id DESC);
CREATE INDEX search_judgments_event ON search_judgments(event_id,item_id,id);
CREATE INDEX completeness_confirmations_item ON completeness_confirmations(item_id,id);
CREATE TRIGGER immutable_connection_version_update BEFORE UPDATE ON connection_versions BEGIN SELECT RAISE(ABORT,'immutable connection version'); END;
CREATE TRIGGER immutable_connection_version_delete BEFORE DELETE ON connection_versions BEGIN SELECT RAISE(ABORT,'immutable connection version'); END;
CREATE TRIGGER immutable_connection_member_update BEFORE UPDATE ON connection_members BEGIN SELECT RAISE(ABORT,'immutable connection member'); END;
CREATE TRIGGER immutable_connection_member_delete BEFORE DELETE ON connection_members BEGIN SELECT RAISE(ABORT,'immutable connection member'); END;
CREATE TRIGGER immutable_knowledge_batch_update BEFORE UPDATE ON knowledge_batches BEGIN SELECT RAISE(ABORT,'immutable batch'); END;
CREATE TRIGGER immutable_knowledge_batch_delete BEFORE DELETE ON knowledge_batches BEGIN SELECT RAISE(ABORT,'immutable batch'); END;
CREATE TRIGGER immutable_chat_input BEFORE UPDATE ON chat_turns
WHEN NEW.session_id<>OLD.session_id OR NEW.request_token<>OLD.request_token OR NEW.request_hash<>OLD.request_hash OR NEW.user_text<>OLD.user_text
BEGIN SELECT RAISE(ABORT,'immutable chat input'); END;
PRAGMA user_version=1;
