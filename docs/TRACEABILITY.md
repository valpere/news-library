# Сценарії, вимоги та приймання

Канонічні вимоги — [REQUIREMENTS.md](REQUIREMENTS.md), компоненти —
[HLD.md](HLD.md). FR-NN нижче — коротке посилання на REQ-FR-NN.
Кожен AT-NN означає сценарій із колонки «Як перевірити» відповідної вимоги.
Статус `partial` означає виконане приймання тільки зазначеного етапу; зелений `tumanomir check` не означає реалізованого
чи пройденого тесту. У PR реалізації додати шлях до тесту й оновити статус AT.

| Вимога | Сценарії | Етап | Приймання | Статус |
|---|---|---|---|---|
| FR-01 | UC-01 | M1 | AT-01 | passed: [TestIngestConcurrentRestart](../internal/library/store_test.go) |
| FR-02 | UC-01 | M1 | AT-02 | passed: [TestIngestConcurrentRestart](../internal/library/store_test.go) |
| FR-03 | UC-01, UC-02 | M1 | AT-03 | passed: [TestWorkerFailureAndStoredText](../internal/library/source_test.go) |
| FR-04 | UC-02, UC-03 | M1, M2 | AT-04 | passed source + digest provenance: [TestArticleFixtures / TestYouTubeFixtures](../internal/library/source_test.go), [TestDigestQueueRecoveryAndRatingContext](../internal/library/digest_test.go) |
| FR-05 | UC-06 | M2 | AT-05 | passed: [TestURLRejectZeroModelCallsAndExplicitOverride / TestSourceRejectKeepsTextSearchAndHumanHistory](../internal/library/filter_test.go) |
| FR-06 | UC-05, UC-06 | M2 | AT-06 | passed: [TestSourceRejectKeepsTextSearchAndHumanHistory](../internal/library/filter_test.go), [M2 browser smoke](../scripts/smoke_m2_ui.py) |
| FR-07 | UC-02, UC-03 | M1, M2 | AT-07 | passed M1/M2: [TestRatingHistoryAndIndependentActions](../internal/library/store_test.go), [TestPredictionsSeparateFromJudgmentsAndSearch](../internal/library/prediction_test.go) |
| FR-08 | UC-07 | M2 | AT-08 | passed: [TestFeedbackAndRegenerationPreserveExactContext](../internal/library/feedback_test.go) |
| FR-09 | UC-04 | M1 | AT-09 | passed: [TestWebWorkflowAndGETDoesNotRead](../internal/library/web_test.go) |
| FR-10 | UC-01, UC-10 | M1, M2 | AT-10 | partial M1: [TestLeaseRestartRetryAndIdempotency / TestCrashOnLastAttemptTerminates](../internal/library/store_test.go); digest recovery: [TestDigestQueueRecoveryAndRatingContext](../internal/library/digest_test.go); future filter stages planned |
| FR-11 | UC-05 | M1, M3 | AT-11 | passed M1 FTS: [TestFTSFiltersAndStableOrder](../internal/library/store_test.go); M3 passed: [semantic filters/FTS fallback](../internal/library/semantic_test.go) |
| FR-12 | UC-09 | M3 | AT-12 | passed: [read authentication/denied mutations](../internal/library/semantic_test.go) |
| FR-13 | UC-08 | M4 | AT-13 | passed offline: [preview/partial retry, unknown/no replay, Kyiv slots](../internal/library/delivery_test.go) |
| FR-14 | UC-12 | M4 | AT-14 | passed: [live backup/restore primary/history/queue/checkpoints and corruption guards](../internal/library/backup_test.go), [CLI/paused activation](../cmd/news-library/archive_test.go) |
| FR-15 | UC-03 | M1, M2 | AT-15 | partial M1: [TestRatingHistoryAndIndependentActions](../internal/library/store_test.go), [TestWebRejectsInvalidScoresAndForeignContext](../internal/library/web_test.go); passed digest links: [TestDigestQueueRecoveryAndRatingContext / TestDigestPrimaryUIAndExports](../internal/library/digest_test.go) |
| FR-16 | UC-10 | M1, M2 | AT-16 | partial M1: [TestNeedsInputAndManualWinsRace](../internal/library/store_test.go), [TestYouTubeFixtures](../internal/library/source_test.go); digest failure/retry: [browser smoke](../scripts/smoke_digest_ui.py); passed long-source processing: [TestLongDigestResumesSavedPartsAndPreservesSource](../internal/library/chunk_digest_test.go) |
| FR-17 | UC-11 | M1 | AT-17 | passed: [TestWebWorkflowAndGETDoesNotRead](../internal/library/web_test.go) |
| FR-18 | UC-05 | M1, M2, M3 | AT-18 | passed M1: [TestFTSFiltersAndStableOrder](../internal/library/store_test.go); passed M2 Confidence: [TestPredictionsSeparateFromJudgmentsAndSearch](../internal/library/prediction_test.go); M3 passed: [semantic filters/FTS fallback](../internal/library/semantic_test.go) |
| FR-19 | UC-09 | M3 | AT-19 | passed: [saved digest/excerpt, dated context, ownership/UTF-8 bounds](../internal/library/semantic_test.go) |
| FR-20 | UC-01, UC-02, UC-10 | M1 | AT-20 | passed: [TestFetchNetworkBoundaries](../internal/library/source_test.go), [TestWebMutationBoundaries / TestWebLocalhostAccessAndOrigin](../internal/library/web_test.go) |

## Перевірка специфікації

`make spec-check` запускає лише детермінований `tumanomir check` на
REQUIREMENTS.md. Нуль розпізнаних вимог, втрачений рядок трасування,
невідомий сценарій або компонент — помилка, а не успішний n/a.
Для всіх визначених вимог потрібне трасування (K_drift=0): це правило
повноти розмітки, не емпіричний поріг якості. D_const залишається
інформаційним; текст не треба скорочувати заради його росту.

Скрипт додатково звіряє посилання на реальні заголовки компонентів HLD,
повноту цієї таблиці, ID сценаріїв і запланованого приймання. Семантичну
правильність зв'язків перевіряє рев'ю; скрипт її не встановлює.

Перевага надається `.build/tumanomir`, інакше використовується бінарник
з PATH; можна явно задати `TUMANOMIR=/absolute/path/to/tumanomir`.
CI збирає прилад із коміту `b8dfe2efb03fefb5a42da76cd29f2b44b2f1bdda`.
Зміни приладу переглядати окремо; встановлення залежностей може вимагати
мережі, сам `check` працює офлайн. Історичні discovery/експерименти не
входять до перевірюваного контракту.

Стохастичне `measure` запускається окремо на незмінному вузькому витягу.
Фіксувати hash входу, версію приладу, модель, prompt, N і параметри.
Порівняння різних моделей не вважати ефектом уточнення вимог. Поріг
D_pair за замовчуванням не є критерієм приймання news-library.

Перший вимір до/після уточнення оцінок збережено локально в
`experiments/spec-precision/2026-10-10/` разом із входами, конфігурацією
та межами інтерпретації. Він не є залежністю публічного checkout.

Додаткове приймання погоджених уточнень:
- [Міграція, відновлення файлів і failure rollback](../internal/library/files_test.go).
- [Спільний HTTP-кеш, одночасність, upstream privacy, SSRF і нуль jobs/items](../internal/library/cache_test.go).
- [Cloud-only adapter, partial/invalid output і safe Markdown](../internal/library/digest_test.go).
- [Конфігурація та private errors](../cmd/news-library/config_test.go).
- [Очікування без оцінки та незмінні GET](../internal/library/web_test.go).
- [Браузерний дайджест: вкладки, clipboard, exports, rating context, failure/retry](../scripts/smoke_digest_ui.py).

M2 також перевіряють [JSON/probability validation](../internal/library/prediction_test.go),
[immutable rule history і причина без повторного внеску](../internal/library/filter_test.go),
[v3 migration без вигаданих прогнозів](../internal/library/files_test.go) та
[M2 config validation](../cmd/news-library/config_test.go).

Довгі джерела: [UTF-8 coverage, checkpoint restart/retry, bounds/fencing](../internal/library/chunk_digest_test.go).
HTTP validation: [persisted validators/restart, 304/changed 200/privacy, exact redirect target,
missing body/unexpected 304, failure/explicit stale, concurrent cleanup, v5 migration](../internal/library/cache_revalidation_test.go).
HTTP eviction: [shared physical size/LRU, orphan cleanup, primary preservation, write failure](../internal/library/cache_cleanup_test.go).

[Браузерний multipart/exports/periodic-cleanup smoke](../scripts/smoke_long_digest_ui.py).

M3 acceptance: [index restart/fencing, reject zero calls, explicit rebuild/migration,
model/snapshot isolation, metadata filters, FTS fallback and read API](../internal/library/semantic_test.go),
[config precedence/token errors](../cmd/news-library/config_test.go),
[browser/API fixture](../scripts/smoke_m3_ui.py).
Explicit real local embedding integration: [synthetic multilingual retrieval smoke](../minions/live_semantic_smoke.go).

M4: [Telegram statuses/secret-safe adapter/agent denial/restored delivery quarantine](../internal/library/delivery_test.go),
[backup integrity and no-overwrite/no-symlink](../internal/library/backup_test.go),
[Telegram config errors/precedence](../cmd/news-library/config_test.go),
[browser preview and real CLI restore smoke](../scripts/smoke_m4_ui.py).
Real bot/destination activation is pending private setup; no live Telegram calls in acceptance.

Нотатки-зв’язкові розширюють FR-04/11/12/14: immutable source provenance,
версії та актуальні backlinks, Ukrainian metadata/body search, агентський
GET-only доступ і backup всіх Markdown. Offline acceptance:
`connections_test.go` — versions/backlinks/human state, ownership/corruption,
concurrent edit conflict, backup/restore, escaped web/draft/API authorization.
Browser fixture — `scripts/smoke_connections_ui.py`. Автоматичний AI-синтез
і імпорт замороженої wiki не входять у це приймання.

Пакетне введення розширює FR-01/02/10/17. `batch_test.go` перевіряє
partial acceptance, canonical duplicates/repeated submit, перезапуск зі
стійкими jobs, незмінність read/placement, bounds-before-write, ізольований
DB failure, CSRF/Origin/agent guards і escaped correction form.
Browser fixture: `scripts/smoke_batch_ui.py` — пачка, виправлення рядка,
повтор, непрочитані вхідні та збереження черги після restart.


YouTube failover FR-03/04/16: `youtube_metadata_test.go` — мовний перебір,
metadata fallback/ASR provenance, private targets denial у helper proxy,
output bounds/context termination і language validation; config precedence
у `cmd/news-library/config_test.go`. Попередні source XML/srv3/json3 fixtures
та pipeline tests лишаються чинними. Explicit live helper:
`go run ./minions/source-smoke -fetch-url URL`, без userDB/models.

Digest validation/failure classification FR-04/08/10/14/16:
`digest_validation_test.go` — heading equivalence/code blocks/optional facts,
specific invalid sections/limits, immutable failure events/retry/backup,
permanent-vs-temporary automatic policy, escaped advice/response UI і
збережений source при втраті diagnostics. `scripts/smoke_digest_ui.py`
перевіряє явний format label, no automatic repeat і запит іншого варіанту.
`minions/validate-digest` перевіряє локальний Markdown або captured Ollama
response offline без моделей/запису; private actual replies не публікуються.


Пакетний retry FR-04/08/10/14: `digest_retry_batch_test.go` перевіряє
classification/legacy ambiguity, explicit unknown selection, CSRF/agent denial,
zero writes при malformed/duplicate input, повтор POST, immutable failures,
human history, stale attempts/sources, missing files і migrated filter rejection
з partial progress. `scripts/smoke_digest_retry_batch_ui.py`: дві format failures
→ checked preview → enqueue обох → два готові дайджести зі збереженими текстами,
лише loopback fake cloud без userDB/live calls.


Автоматичні metadata/connections FR-04/05/07/08/10/11/12/14/19:
`knowledge_test.go` — первісний pipeline, reused vocabulary, exact source refs,
model author, backlinks обох статей, human/source immutability, question/tag
search, duplicate avoidance, metadata checkpoint + permanent failure/no auto
retry + restored backup/diagnostics, stale lease/source/evidence/unknown IDs,
early rejected zero calls, read-only UI guard/MD export, restart recovery і
bounded temporary attempts, thematic grouping/manual author revision, schema10
migration without file rewrite/backfill, strict cloud JSON/local model rejection,
exact checkpoint after model switch і primary metadata file-loss warning.
Config default/env/flag tests у config_test.go.
`scripts/smoke_knowledge_ui.py` — два URL з manual source → automatic digest/
metadata/thematic note → backlinks/author/MD/search, тільки loopback fake cloud.
Попередні digest-only browser fixtures явно вимикають knowledge для ізоляції.


Пакетна knowledge дообробка FR-04/05/07/08/10/11/12/14:
knowledge_batch_test.go — наступні5/решта, done/active skip, model switch
не regenerates completed data, exact pinned checkpoint після збою,
failure history, blocked causes/retry opt-out/new priority, malformed
limit/missing files, no starvation через битий перший файл, CSRF/agent
denial, durable receipt і paused restore, concurrent replay та replay
після завершення або нового failure. Browser smoke_knowledge_batch_ui.py
створює старі ready digests, вмикає knowledge після restart, batch →
metadata/notes, повтор того самого POST і новий пакет дають zero new calls
для completed items; digest calls незмінні. Усе isolated fake cloud.
