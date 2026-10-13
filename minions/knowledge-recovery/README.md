# Відновлення невдалого аналізу

З кореня репозиторію. Типово read-only аудит збережених відповідей, без
генерації й міграції бази:

```sh
mkdir -p tmp
go run ./minions/knowledge-recovery -data data -output tmp/knowledge-audit.json
```

За явним дорученням користувача, після backup та без інших активних workers:

```sh
build/news-library backup -source data -output tmp/before-knowledge-recovery
go run ./minions/knowledge-recovery -data data -apply -run-cloud -limit 5 \
  -output tmp/knowledge-recovery-result.json
```

`-apply` окремо дозволяє зміни; `-run-cloud` дозволяє нову генерацію через
локальний Ollama, коли saved reply не проходить перевірку. Без нього весь
вибраний пакет має бути replayable. Запущена/призупинена після restore черга
не обходиться. Повтор зберігає точну модель, prompt і checkpoint наявного run.
За наявності нової ще не початої обробки helper відмовляється працювати:
для неї використовуйте звичайну чергу, recovery лише відновлює невдале. Ключ береться тільки з process env
`OLLAMA_API_KEY` за потреби; зазвичай daemon вже автентифікований.

Перевірки schema/evidence/candidate ownership не послаблюються; category/tags/
questions у combined links reply валідовані, але не замінюють checkpoint.
Історія відмов та оцінок збережена; завершені записи не ставляться знову.
Incomplete batches return nonzero after saving per-item outcomes.
Persistent request token записується у звіті; повторне використання не
генерує нової роботи. Типові тимчасові відмови зберігають звичайну retry_wait
політику, але CLI виконує лише обмежену кількість worker steps.
Не запускає source/digest/embedding/Telegram workers чи сайт.
