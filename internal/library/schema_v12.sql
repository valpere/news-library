CREATE TABLE knowledge_batches (
 request_token TEXT PRIMARY KEY, batch_limit INTEGER NOT NULL, include_retry INTEGER NOT NULL,
 created_at TEXT NOT NULL, result_json TEXT NOT NULL
);
CREATE TRIGGER immutable_knowledge_batch_update BEFORE UPDATE ON knowledge_batches BEGIN SELECT RAISE(ABORT,'immutable batch'); END;
CREATE TRIGGER immutable_knowledge_batch_delete BEFORE DELETE ON knowledge_batches BEGIN SELECT RAISE(ABORT,'immutable batch'); END;
PRAGMA user_version=12;
