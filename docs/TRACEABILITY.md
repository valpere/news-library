# Сценарії, вимоги та приймання

Канонічні вимоги — [REQUIREMENTS.md](REQUIREMENTS.md), компоненти —
[HLD.md](HLD.md). FR-NN нижче — коротке посилання на REQ-FR-NN.
Кожен AT-NN означає сценарій із колонки «Як перевірити» відповідної вимоги.
Статус `partial` означає виконане приймання тільки зазначеного етапу; зелений `tumanomir check` не означає реалізованого
чи пройденого тесту. У PR реалізації додати шлях до тесту й оновити статус AT.

| Вимога | Сценарії | Етап | Приймання | Статус |
|---|---|---|---|---|
| FR-01 | UC-01 | M1 | AT-01 | passed: `internal/library/store_test.go::TestIngestConcurrentRestart` |
| FR-02 | UC-01 | M1 | AT-02 | passed: `internal/library/store_test.go::TestIngestConcurrentRestart` |
| FR-03 | UC-01, UC-02 | M1 | AT-03 | passed: [TestWorkerFailureAndStoredText](../internal/library/source_test.go) |
| FR-04 | UC-02, UC-03 | M1, M2 | AT-04 | partial M1: [TestArticleFixtures / TestYouTubeFixtures](../internal/library/source_test.go); M2 artifacts planned |
| FR-05 | UC-06 | M2 | AT-05 | planned |
| FR-06 | UC-05, UC-06 | M2 | AT-06 | planned |
| FR-07 | UC-02, UC-03 | M1, M2 | AT-07 | partial M1: [TestRatingHistoryAndIndependentActions](../internal/library/store_test.go); M2 predictions planned |
| FR-08 | UC-07 | M2 | AT-08 | planned |
| FR-09 | UC-04 | M1 | AT-09 | passed: [TestWebWorkflowAndGETDoesNotRead](../internal/library/web_test.go) |
| FR-10 | UC-01, UC-10 | M1, M2 | AT-10 | partial M1: [TestLeaseRestartRetryAndIdempotency / TestCrashOnLastAttemptTerminates](../internal/library/store_test.go); M2 stages planned |
| FR-11 | UC-05 | M1, M3 | AT-11 | partial M1 FTS: [TestFTSFiltersAndStableOrder](../internal/library/store_test.go); M3 vector planned |
| FR-12 | UC-09 | M3 | AT-12 | planned |
| FR-13 | UC-08 | M4 | AT-13 | planned |
| FR-14 | UC-12 | M4 | AT-14 | planned |
| FR-15 | UC-03 | M1, M2 | AT-15 | partial M1: [TestRatingHistoryAndIndependentActions](../internal/library/store_test.go), [TestWebRejectsInvalidScoresAndForeignContext](../internal/library/web_test.go); M2 digest links planned |
| FR-16 | UC-10 | M1, M2 | AT-16 | partial M1: [TestNeedsInputAndManualWinsRace](../internal/library/store_test.go), [TestYouTubeFixtures](../internal/library/source_test.go); M2 enrichment planned |
| FR-17 | UC-11 | M1 | AT-17 | passed: [TestWebWorkflowAndGETDoesNotRead](../internal/library/web_test.go) |
| FR-18 | UC-05 | M1, M2, M3 | AT-18 | partial M1: [TestFTSFiltersAndStableOrder](../internal/library/store_test.go); M2 Confidence/M3 vector planned |
| FR-19 | UC-09 | M3 | AT-19 | planned |
| FR-20 | UC-01, UC-02, UC-10 | M1 | AT-20 | passed: [TestFetchNetworkBoundaries](../internal/library/source_test.go), [TestWebMutationBoundaries](../internal/library/web_test.go) |

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
