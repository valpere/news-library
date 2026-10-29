-- A material in the trash may be erased for good (Store.PurgeTrashed), so the
-- delete guards of its own history now let DELETE through only while its
-- trash_entries row exists. Live materials stay immutable. Connection notes and
-- rule versions keep their unconditional guards.
DROP TRIGGER IF EXISTS immutable_snapshot_delete;
CREATE TRIGGER immutable_snapshot_delete BEFORE DELETE ON source_versions
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable snapshot'); END;
DROP TRIGGER IF EXISTS immutable_artifact_delete;
CREATE TRIGGER immutable_artifact_delete BEFORE DELETE ON generated_artifacts
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable artifact'); END;
DROP TRIGGER IF EXISTS immutable_rating_delete;
CREATE TRIGGER immutable_rating_delete BEFORE DELETE ON rating_events
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable rating'); END;
DROP TRIGGER IF EXISTS immutable_filter_delete;
CREATE TRIGGER immutable_filter_delete BEFORE DELETE ON filter_decisions
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable decision'); END;
DROP TRIGGER IF EXISTS immutable_feedback_delete;
CREATE TRIGGER immutable_feedback_delete BEFORE DELETE ON digest_feedback
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable feedback'); END;
DROP TRIGGER IF EXISTS immutable_prediction_delete;
CREATE TRIGGER immutable_prediction_delete BEFORE DELETE ON interest_predictions
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM generated_artifacts WHERE id=OLD.digest_id))
 BEGIN SELECT RAISE(ABORT,'immutable prediction'); END;
DROP TRIGGER IF EXISTS immutable_checkpoint_delete;
CREATE TRIGGER immutable_checkpoint_delete BEFORE DELETE ON digest_checkpoints
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM jobs WHERE id=OLD.job_id))
 BEGIN SELECT RAISE(ABORT,'immutable checkpoint'); END;
DROP TRIGGER IF EXISTS immutable_job_failure_delete;
CREATE TRIGGER immutable_job_failure_delete BEFORE DELETE ON job_failures
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM jobs WHERE id=OLD.job_id))
 BEGIN SELECT RAISE(ABORT,'immutable job failure'); END;
DROP TRIGGER IF EXISTS immutable_article_knowledge_delete;
CREATE TRIGGER immutable_article_knowledge_delete BEFORE DELETE ON article_knowledge
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=OLD.item_id)
 BEGIN SELECT RAISE(ABORT,'immutable knowledge'); END;
DROP TRIGGER IF EXISTS immutable_knowledge_failure_delete;
CREATE TRIGGER immutable_knowledge_failure_delete BEFORE DELETE ON knowledge_failures
 WHEN NOT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=(SELECT item_id FROM knowledge_runs WHERE id=OLD.run_id))
 BEGIN SELECT RAISE(ABORT,'immutable failure'); END;
PRAGMA user_version=18;
