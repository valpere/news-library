CREATE TABLE job_failures (
 id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES jobs(id), attempt INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('temporary','format','configuration','limit','source','unknown')),
 message TEXT NOT NULL, retryable INTEGER NOT NULL CHECK(retryable IN (0,1)),
 response_path TEXT NOT NULL DEFAULT '', hash TEXT NOT NULL DEFAULT '', recorded_at TEXT NOT NULL
);
CREATE INDEX job_failure_history ON job_failures(job_id,id DESC);
CREATE TRIGGER immutable_job_failure_update BEFORE UPDATE ON job_failures BEGIN SELECT RAISE(ABORT,'immutable job failure'); END;
CREATE TRIGGER immutable_job_failure_delete BEFORE DELETE ON job_failures BEGIN SELECT RAISE(ABORT,'immutable job failure'); END;
PRAGMA user_version=10;
