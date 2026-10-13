# OpenRouter fallback smoke

З кореня repo: `go run ./minions/openrouter-smoke -run-cloud -no-byok -model google/gemma-4-31b-it:free`.

Без обох opt-in прапорців генерація не запускається. `-no-byok` означає підтверджений акаунт без ключів інших провайдерів. `.env` читається без shell-source; секрет не виводиться. Один synthetic digest через production FreeOpenRouter/DigestFallback після імітованого тимчасового збою primary. Новий приватний каталог у tmp містить isolated SQLite, source/artifact/failure history та result.json. Сайт, користувацькі дані та .env не змінюються. Prices/cost/non-BYOK guards залишаються чинними; немає paid fallback чи автоматичних повторів. Failed HTTP/format/policy result — nonzero, не успішна перевірка.

`-data-collection allow` explicitly permits provider collection/training for
this synthetic check when it matches the user's choice. The default is deny;
account/workspace restrictions remain effective. It never changes those
settings. A passed single small digest does not establish model quality,
long-source coverage, recommendation calibration or continuing availability.
