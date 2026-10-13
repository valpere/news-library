# Звірка приймання FR-10, FR-15 і FR-16

Це звірка визначених у REQUIREMENTS критеріїв AT-10/15/16 на поточній
реалізації. Статус `passed offline` спирається на перевірки із синтетичними
джерелами та генератором. Він не є оцінкою якості модельних дайджестів,
особистої корисності, затримок живого сервісу або опціонального fallback.

| Критерій | Прямий доказ | Висновок |
|---|---|---|
| FR-10: перезапуск продовжує незавершений етап | `TestLeaseRestartRetryAndIdempotency`, `TestDigestWorkerRestartAndFailurePreservesSource` у [store_test.go](../internal/library/store_test.go) та [digest_test.go](../internal/library/digest_test.go); `TestLongDigestResumesSavedPartsAndPreservesSource` у [chunk_digest_test.go](../internal/library/chunk_digest_test.go) перевіряє закриття/відкриття бази й відсутність повторних викликів завершених частин | passed offline |
| FR-10: відновлення не переписує людські рішення | `TestRecoveryPreservesManualSourceAndHumanJudgment` у [acceptance_test.go](../internal/library/acceptance_test.go): оцінка, ручний тег і прочитання встановлені до двох перезапусків та повтору генерації; подія оцінки порівнюється повністю | passed offline |
| FR-15: оцінка 3 приймається; неоцінене не дорівнює 0 | `TestRatingHistoryAndIndependentActions` у [store_test.go](../internal/library/store_test.go); усі сім рівнів, включно з 3, та окремі фільтри `0`/`unrated` — [usefulness_test.go](../internal/library/usefulness_test.go) | passed offline |
| FR-15: нове джерело/дайджест не переприв’язує стару оцінку | `TestDigestQueueRecoveryAndRatingContext` у [digest_test.go](../internal/library/digest_test.go), `TestFeedbackAndRegenerationPreserveExactContext` у [feedback_test.go](../internal/library/feedback_test.go); lifecycle-тест зберігає старий source ID, null digest/date та всю подію після нового snapshot | passed offline |
| FR-16: відсутній текст можна додати вручну без втрати URL | lifecycle-тест перевіряє URL після збою, `origin=manual`, `extractor=manual-v1` і невідому повноту; `TestNeedsInputAndManualWinsRace` у [store_test.go](../internal/library/store_test.go) також перевіряє пріоритет ручного джерела перед застарілим fetch | passed offline |
| FR-16: без тексту немає вигаданого дайджесту | lifecycle-тест запускає worker з увімкненою генерацією, але відсутнім джерелом: 0 викликів генератора, 0 generated artifacts, null selected source і null оцінка | passed offline |

Додаткові докази recovery: [knowledge_batch_test.go](../internal/library/knowledge_batch_test.go)
перевіряє повтор receipt без нових задач, збережений metadata checkpoint,
точну модель продовженої спроби та історію відмов. [digest_validation_test.go](../internal/library/digest_validation_test.go)
перевіряє типи збоїв, діагностику, backup і відсутність автоматичного
повтору format/configuration/limit/source failures.

Перевірки входять до `make check` (Go tests з race detector, build та vet).
Lifecycle-тест додатково запускається окремо без мережі й живих моделей.
Приймання оцінюється за перевірками; зміна позначки в таблиці сама по собі
не є доказом. Поведінку застосунку та дані користувача ця звірка не змінює.
