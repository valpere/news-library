# news-library

Особиста бібліотека для одного користувача: URL статті/YouTube → доступний
текст і дайджест → власна оцінка з історією → збереження/відкладання → пошук.

M1 реалізовано: один локальний Go-сервіс, SQLite, стійка черга, статті та
YouTube-субтитри, ручний текст, незалежні оцінки/розміщення/читання, теги,
FTS і фільтри. Додано дайджести Ollama cloud, окремі файли текстів та
спільний HTTP-кеш, пояснювані правила відсіву, окремі відгуки про
дайджести й прогноз інтересу за явно заданим профілем. Free-only fallback
залишається опціональним кроком M2. M3 semantic search/read API та M4 Telegram/backup tooling реалізовані; Telegram потребує приватного налаштування.

## Запуск

Потрібні Go 1.26+ і Make. Перший build завантажує зафіксовані Go-залежності.

```sh
make build
./build/news-library
# або make run
```

Відкрити http://localhost:8787 або http://127.0.0.1:8787. Дані зберігаються в `data/library.sqlite`
разом із SQLite WAL/SHM. Тексти — `data/texts/`, дайджести —
`data/digests/`, отримані HTTP-відповіді — `data/cache/http/`. Каталог виключено з Git. Зупинити через Ctrl+C.
Після перезапуску записи та черга залишаються, worker відновлює завдання
після завершення lease. Картка автоматично очікує отримання тексту та генерації дайджесту,
зберігаючи незавершене введення. До появи результату оцінка прихована.
Для вже прочитаного джерела є окрема явна дія оцінювання.

```sh
./build/news-library -data /absolute/private/library -bind 127.0.0.1:8788
./build/news-library -no-worker  # UI без виконання черги
./build/news-library -help
```

Локальні налаштування читаються з `./.env`; зразок — [.env.example](.env.example).
Наявний файл збережіть; переносіть лише потрібні відсутні параметри.
Пріоритет: flags → process environment → `.env` → defaults.
`-env-file /path/to/config` обирає файл; `-env-file ''` вимикає читання файла.
`.env` не виконується як shell script і не входить у Git.

| Параметр / змінна `.env` | Типово | Призначення |
|---|---|---|
| `-data` / `NEWS_LIBRARY_DATA` | `data` | Корінь SQLite та файлів, створюється з правами 0700 |
| `-bind` / `NEWS_LIBRARY_BIND` | `127.0.0.1:8787` | Явна loopback IP |
| `-model` / `OLLAMA_MODEL` | `gemma4:cloud` | Лише підтверджена cloud-модель Ollama |
| `-ollama-url` / `OLLAMA_API_URL` | `http://localhost:11434/api/chat` | Локальний cloud-шлюз; optional `OLLAMA_API_KEY` |
| `-digest-timeout` / `NEWS_LIBRARY_DIGEST_TIMEOUT` | `300s` | Час генерації |
| `-digest-max-bytes` / `NEWS_LIBRARY_DIGEST_MAX_BYTES` | `163840` | Ліміт тексту одного запиту/частини |
| `-chunk-max-parts` / `NEWS_LIBRARY_CHUNK_MAX_PARTS` | `64` | Ліміт частин на етапі, 2..512 |
| `-fetch-timeout` / `NEWS_LIBRARY_FETCH_TIMEOUT` | `30s` | Час отримання джерела |
| `-max-bytes` / `NEWS_LIBRARY_MAX_BYTES` | `4194304` | Ліміт джерела та ручного тексту |
| `-lease` / `NEWS_LIBRARY_LEASE` | `1m` | Lease з heartbeat |
| `-attempts` / `NEWS_LIBRARY_ATTEMPTS` | `3` | Максимум спроб завдання |
| `-backoff` / `NEWS_LIBRARY_BACKOFF` | `10s` | Початкова затримка повтору |
| `-interests` / `NEWS_LIBRARY_INTERESTS` | порожньо | Поточні інтереси; без профілю прогноз відсутній |
| `-filter-rules` / `NEWS_LIBRARY_FILTER_RULES` | `[]` | JSON-масив правил |
| `-filter-threshold` / `NEWS_LIBRARY_FILTER_THRESHOLD` | `0.8` | Відсів лише при R > T |
| `-cache-max-bytes` / `NEWS_LIBRARY_CACHE_MAX_BYTES` | `536870912` | Ліміт HTTP body files, 512 MiB |
| `-cache-retention` / `NEWS_LIBRARY_CACHE_RETENTION` | `168h` | Збереження простроченого кешу для explicit stale |
| `-cache-cleanup-interval` / `NEWS_LIBRARY_CACHE_CLEANUP_INTERVAL` | `1h` | Період очищення |
| `-cache-ttl` / `NEWS_LIBRARY_CACHE_TTL` | `24h` | Максимальний строк свіжості кешу; 0 = bypass |
| `-no-worker` / `NEWS_LIBRARY_NO_WORKER` | `false` | Перегляд без виконання черги |
| `-no-digests` / `NEWS_LIBRARY_NO_DIGESTS` | `false` | Отримання тексту без генерації |

Основний результат — український дайджест із секціями «Про що матеріал»,
«Ключові тези», «Деталі», «Практичне застосування», optional «Факти / цитати».
Є вкладки, копіювання Markdown та завантаження Markdown/HTML; повний текст
відкривається окремо. Помилка cloud зберігає текст і URL, показує причину
та окремий повтор генерації. Локальна генерація та автоматичний платний
fallback виключені. Адаптер перевіряє remote identity через `/api/tags`;
назву `gemma4:31b-cloud` можна зіставити з `gemma4:cloud`, тільки якщо
inventory підтверджує той самий remote model. Зміна моделі не переписує
готові результати чи оцінки. Довгий текст обробляється частинами з проміжними дайджестами й фінальним
синтезом. Усі байти UTF-8 розподіляються без пропусків, у результаті видно
кількість вихідних частин. Готові частини зберігаються у власному `digests/`
і переживають рестарт/повтор завдання. Старі завдання з попередньою
стратегією не перемикаються мовчки: явний retry використовує нову конфігурацію.
Ліміт частин і вісім етапів скорочення обмежують обробку. Якщо модель не
скорочує нотатки, відповідає завеликим дайджестом або джерело перевищує ці
межі, помилка видима; весь доступний текст і готові частини збережені.
Timeout генерації стосується всього завдання; його можна збільшити для
великих матеріалів. Usage підсумовує успішні кроки; невідомий usage помилок
не вигадується.

Спільний HTTP-кеш доступний іншим локальним інструментам:

```sh
curl --get 'http://localhost:8787/cache' \
  --data-urlencode 'url=https://example.com/article' -o source.html
# refresh=1 примусово отримує джерело; stale=1 дозволяє прострочений кеш
```

Це endpoint отримання HTTP/HTTPS-ресурсів, без CONNECT/MITM. Бібліотека
використовує той самий кеш; явний повтор отримання обходить свіжий запис.
Відповідь містить `X-News-Library-Cache` (HIT/MISS/REVALIDATED/STALE/BYPASS), ETag,
час отримання, кінцевий URL і повноту. Запити кешу не додають матеріалів,
не оцінюють їх і не запускають моделей. Отримання anonymous, з фіксованими
upstream headers; `private`, `no-store`, `no-cache`, cookies та непідтримуваний Vary
не кешуються. HTML віддається як attachment із sandbox CSP.
TTL керує свіжістю. Очищення виконується при запуску, за розкладом і
після збереження нової відповіді: видаляє старий прострочений кеш, файли
без посилань і найдавніше використані HTTP bodies понад ліміт. Спільний
body кількох URL рахується один раз. Первинні `texts/` і `digests/`,
оцінки та бібліотечні стани не очищуються. Відповідь понад бюджет або
помилка запису кешу дає BYPASS; отриманий текст залишається доступним
бібліотеці. Після TTL або при `refresh=1` кеш перевіряє джерело через
`If-None-Match` / `If-Modified-Since`, якщо є валідатори й перевірене тіло.
`304` дає REVALIDATED і поновлює TTL без завантаження тіла; `200` замінює
відповідь. `X-Source-Fetched-At` лишається часом отримання тіла,
`X-Source-Checked-At` показує останню перевірку. Валідатори надсилаються
лише до точного збереженого кінцевого URL. Після мережевого збою звичайний
запит повертає помилку; прострочене тіло доступне лише з явним `stale=1`.
TTL=0 вимикає кешування й умовні запити.

Міграції v1–v9 зберігають ID, чергу та історію оцінок; v3 переносить
SQL-тексти у файли. Перед оновленням зупиніть старий процес і зробіть копію
каталогу даних. Для відновлення потрібні узгоджена SQLite-копія **та**
`texts/` і `digests/`; кеш можна пропустити. Шляхи відносні до кореня даних.
Файли перевіряються за SHA-256; відсутність первинного файла показується
як помилка. Пошкоджений кеш отримується повторно. Backup/restore commands наведені нижче.

Текст одразу зберігається у власних незмінних файлах; «Зберегти» лише змінює розміщення.
Власна оцінка — ціле 0..9223372036854775807; відсутня оцінка відрізняється
від 0. Кожна оцінка додає незмінну подію з відомими версіями показаного тексту й дайджесту.
Для історичного рішення можна явно залишити дату невідомою. GET не
позначає матеріал прочитаним. Час показано в Europe/Kyiv.

Пошук використовує поточну вибрану версію тексту, заголовок і URL.
Слова шукаються буквально з AND. Різні фільтри поєднуються з AND,
теги через кому — з OR. Фільтр оцінки: число або `unrated`.
Неоцінені йдуть останніми при обох напрямках сортування за оцінкою;
остаточний ключ — ID за зростанням. Старі версії доступні в картці.

## Правила, прогноз інтересу та відгуки

Початкові правила порожні. Налаштовуйте їх у `.env`, наприклад:

```dotenv
NEWS_LIBRARY_FILTER_THRESHOLD=0.8
NEWS_LIBRARY_FILTER_RULES='[{"id":"ads","reason":"advertising","field":"url","contains":"/ads/","probability":0.95}]'
NEWS_LIBRARY_INTERESTS='Поточні теми та задачі, які мене цікавлять'
```

`field` — `url`, `title` або `text`; `contains` — буквальний фрагмент без
урахування регістру. Порожні збіги та неправильні ймовірності відхиляються.
Однакова `reason` (без регістру й зовнішніх пробілів) позначає одну причину:
береться найбільший внесок її збігів. Для різних причин R = 1 − ∏(1 − p).
Відсів тільки при R > T; p = 0 допустиме. Незалежність різних причин —
припущення ваших правил; алгоритм не визначає її з тексту пояснень.
URL-перевірка виконується до fetch; title/text — після збереження snapshot.
Відсіяне залишається у FTS і фільтрі «Відсіяне», з версією правил,
внесками й історією рішення. Генерація для цього проходу не запускається.
«Повернути до обробки» фіксує override і запускає пропущений етап;
повторне додавання URL або звичайний retry не обходять відсів.

З непорожнім `NEWS_LIBRARY_INTERESTS` той самий cloud-запит повертає
дайджест і валідований прогноз інтересу. Confidence — оцінка ймовірності
інтересу, без підтвердженої калібровки. Профіль і його hash зберігаються
разом із завданням/прогнозом. Без профілю прогноз відсутній; відсутність
відрізняється від 0%. Пошук має мінімум/максимум Confidence у відсотках
та сортування, яке ставить відсутнє останнім. Низький прогноз не відсіює
матеріал і не змінює власної оцінки.

«Якість дайджесту» зберігає датований відгук на конкретну версію: вдалий,
помилки, пропущене важливе або незрозумілий, із коментарем. Відгук не
викликає модель і не змінює користь матеріалу. «Створити нову версію
дайджесту» — окрема дія; використовує поточну конфігурацію, залишаючи
попередній результат доступним під час обробки й після помилки. Версії
можна відкрити окремо; старі оцінки й відгуки зберігають свої посилання.
Зміна `.env` застосовується після рестарту і не переобробляє готові записи.

## Джерела та обмеження

Статті виділяються за допомогою
[Readeck Readability](https://pkg.go.dev/codeberg.org/readeck/go-readability/v2).
Повний текст екранується; Markdown дайджесту рендериться без raw HTML
і небезпечних посилань. Повнота статті після виділення
невідома; перевищення ліміту чи перервана відповідь позначені `partial`.
Доступна частина зберігається; вона може не містити повного оригіналу.
Сторінки, що потребують входу/JavaScript, можуть вимагати ручного тексту.

YouTube-адаптер перебирає XML/JSON-доріжки, спочатку налаштовані мови
(типово `uk,en`), у межах мови — людські перед автоматичними. Якщо watch
tracks порожні чи недоступні, optional `yt-dlp` отримує актуальні metadata;
сам текст завантажується захищеним HTTP-клієнтом/кешем бібліотеки. Fallback
не завантажує відео/аудіо й не генерує дайджест. `yt-dlp` має бути доступним
у PATH; `NEWS_LIBRARY_YT_DLP` задає executable, порожнє значення вимикає
fallback; `NEWS_LIBRARY_YOUTUBE_LANGUAGES=uk,en` задає порядок мов.
Інші доступні мови також пробуються, до 8 доріжок на кожен етап. Автоматичні
субтитри/переклад явно позначені. Обидва шляхи блокують приватні адреси;
metadata helper ігнорує сторонні config/plugins/cache, має deadline/output
limits. Недоступний текст зберігає явний стан і ручний текст/retry; аудіо
не транскрибується. Після оновлення невдалі записи потребують явного
«Повторити отримання» в картці. Live smoke — окрема дія, поза offline CI.

Fetcher блокує private/loopback/link-local та спеціальні адреси, перевіряє
всі результати DNS, з’єднується з перевіреною IP й повторює перевірку після
redirect. Він не використовує proxy з оточення. UI перевіряє Host, Origin,
CSRF та browser fetch metadata. Агентські облікові дані не приймаються
для мутацій; окремий read API з’явиться на M3. Для LAN/VPS потрібні
окрема авторизація та TLS, яких M1 ще не надає.

## Перевірки

```sh
make test   # Go acceptance tests з race detector і go vet
make check  # специфікація, build, тести, документи та локальні пілоти за наявності
```

Для `make spec-check`/`make check` потрібні Python 3 та `tumanomir` у
`.build/tumanomir`, PATH або `TUMANOMIR=/absolute/path/to/tumanomir`.
CI збирає прилад із зафіксованого коміту. Тести використовують fixtures;
запитів до генеративних моделей немає. Приватні пілоти й локальна
передача `tmp/` не входять у публічний checkout.

Контракт і наступні етапи: [START_HERE.md](START_HERE.md),
[REQUIREMENTS](docs/REQUIREMENTS.md), [HLD](docs/HLD.md),
[BACKLOG](docs/BACKLOG.md), [TRACEABILITY](docs/TRACEABILITY.md).

Браузерний smoke (окремо, Python Playwright та Chrome):
`python3 scripts/smoke_ui.py`, `python3 scripts/smoke_digest_ui.py` і
`python3 scripts/smoke_m2_ui.py` і `python3 scripts/smoke_long_digest_ui.py`. За потреби `SMOKE_CHROME=/path/to/chrome`.
Він запускає binary у тимчасовому каталозі, перевіряє форми, текст,
оцінки, FTS і рестарт; знімки — `tmp/smoke-{search,detail}.png`.
Потребує попереднього `make build`. Другий smoke використовує fake cloud
шлюз і перевіряє вкладки, clipboard, exports, provenance та failure/retry;
третій перевіряє відсів/override, Confidence, feedback і версії;
живих запитів до моделей немає.

Повторювані агентські дії — у `minions/`, тимчасові файли — у `tmp/`:

```sh
python3 minions/project_check.py                 # make check + public HEAD docs/spec
python3 minions/project_check.py --public-only   # лише committed public docs/spec
python3 minions/pr_gate.py 8 --head <reviewed-head-sha> --base <reviewed-base-sha>
# Окремий живий cloud smoke, коли потрібна інтеграційна перевірка:
python3 minions/live_digest_smoke.py --run-cloud  # isolated fixture у tmp/
# --prediction додає synthetic test profile
```

PR gate лише читає GitHub; його результат не замінює рев’ю та локальні перевірки.
Навички: [project-check](minions/skills/project-check/SKILL.md),
[pr-gate](minions/skills/pr-gate/SKILL.md),
[live-digest-smoke](minions/skills/live-digest-smoke/SKILL.md).
Для локального виявлення Codex підключіть їх symlink у `.agents/skills/`;
створюйте тимчасові артефакти в `tmp/`, а придатні до повторного використання
скрипти переносіть до `minions/`.


Пошук за змістом (M3): увімкніть `NEWS_LIBRARY_EMBEDDINGS=true` у `.env`.
Потрібна встановлена local `bge-m3:latest`; програма не завантажує модель.
Default endpoint `NEWS_LIBRARY_EMBED_URL=http://localhost:11434/api/embed`,
таймаут `NEWS_LIBRARY_EMBED_TIMEOUT=30s`. Генерація дайджестів лишається cloud.
На сайті доступні «За словами», «За змістом» і «Слова та зміст»; автоматичне
semantic сортування — relevance, можна явно обрати дату/оцінку/Confidence.
Неповний індекс і FTS fallback позначені. «Перебудувати індекс» змінює
лише похідні дані; первинні тексти, дайджести й власні рішення збережені.

Агентський read API: задайте `NEWS_LIBRARY_READ_TOKEN` у приватному `.env`
(32..256 random non-whitespace bytes). Порожній token вимикає API.
Запити мають `Authorization: Bearer …`; секрет не вставляйте в чат/репозиторій.

- `GET /api/read/search?q=…&mode=hybrid&placement=deferred&limit=20` — спільні
  UI metadata filters; mode fts/vector/hybrid, default fts, limit1..100.
- `GET /api/read/items/1` — збережений текст, digest/model provenance і rating history.
- `GET /api/read/items/1/context` — до4000 UTF-8 bytes saved digest/excerpt,
  власна оцінка з датою/null, source completeness, placement і tags.
- `GET /api/read/items/1/fragment?start=0&end=100` — source byte boundaries,
  до16384 bytes; optional `source=ID` дозволяє historical snapshot цього item.

GET не ставить read, не генерує текст і не індексує бібліотеку; semantic
query використовує лише local embedding. Агентські credentials відхиляються
в усіх mutation endpoints. База зберігає похідні vectors/offsets; первинні
source files залишаються окремо. Точний vector scan розрахований на особисту
бібліотеку; ANN для значно більшого обсягу — майбутня оптимізація.

Offline M3 browser/API smoke: `python3 scripts/smoke_m3_ui.py`.
Окрема real local bge-m3 перевірка на трьох synthetic sources:
`go run minions/live_semantic_smoke.go -run-local-embeddings`.
Вона не запускає generation і зберігає fixture evidence в `tmp/`.


Резервне копіювання й відновлення (після `make build`):

```sh
./build/news-library backup -source data -output tmp/my-backup
./build/news-library verify-backup -source tmp/my-backup
./build/news-library restore -source tmp/my-backup -output tmp/my-restored-library
./build/news-library -data tmp/my-restored-library  # workers/delivery paused
# Після перевірки відновленої копії та зупинки старого instance:
./build/news-library -data tmp/my-restored-library -resume-restored
```

Вказуйте фактичний каталог даних у `-source`; output має не існувати.
Backup працює з live SQLite snapshot, не мігрує source, включає referenced
texts/digests/checkpoints/frozen messages та metadata/history/queue.
Cache/config/.env не копіюються. SHA/integrity/foreign-key checks відхиляють
corruption, missing files або symlinks. Невдалий transfer лишає marker і не
відкривається сервісом; повтор потребує нового output. Restore зберігає source
queue і human history, ставить background pause. Усі unfinished imported
Telegram deliveries стають unknown, бо після snapshot вони могли вже надійти;
confirmed message IDs залишаються. Automatic replay немає.

Telegram: сторінка **Telegram-добірки** готує immutable preview зі saved digests
без AI викликів. Вибираються latest unread, без rejected/rating0 і вже
confirmed/active/unknown digest-to-recipient; send не ставить read. Перед ручною
відправкою видно саме збережені messages і recipient; частини мають незалежні
states/message IDs. Failed retry не повторює sent parts, unknown лише manual
перевірка без retry. Daily пропущені випуски не надолужуються автоматично.

Налаштування лише у приватному `.env`:

```dotenv
NEWS_LIBRARY_TELEGRAM_ENABLED=false
NEWS_LIBRARY_TELEGRAM_BOT_TOKEN=
NEWS_LIBRARY_TELEGRAM_CHAT_ID=
NEWS_LIBRARY_TELEGRAM_DAILY=09:00
NEWS_LIBRARY_TELEGRAM_SIZE=5
```

User обрав09:00 Europe/Kyiv/до5. Заповніть bot token і numeric chat ID приватно,
потім увімкніть ENABLED=true. Порожній DAILY означає лише на вимогу. Due window
5min; поза ним або без готових матеріалів persisted missed, без late catchup.
Bot token не копіюється в backup і не показується у UI/help/errors. Реальних
Telegram повідомлень у розробці не надсилали; приймання з mock API.
Offline browser/CLI smoke: `python3 scripts/smoke_m4_ui.py`.

## Нотатки-зв’язкові

У меню «Нотатки-зв’язкові» можна створити тематичний Markdown, вибрати
щонайменше дві статті та пояснити, що вони підтверджують, уточнюють чи
спростовують. Category — широка тема, tags — поняття, questions — питання
для розуміння. Наявні метадані показуються як підказки. Це ручний перший
експеримент; створення нотатки не запускає моделі й не змінює статті.

Зворотні посилання з карток статей з’являються автоматично. Редагування
зберігає нову версію; старі тексти й посилання залишаються в історії.
Markdown із frontmatter можна завантажити для конкретної версії; усі
версії входять у backup. Пошук нотаток охоплює також tags/category/questions.
Списки обмежені 100 результатами; нові статті вибираються серед останніх 100.
Authenticated read API: `/api/read/connections?q=…` і
`/api/read/connections/{id}?version=…`; item API повертає backlinks.
Існуюча `~/doc/llm-wiki/wiki` залишається замороженою, імпорт відкладено.

## Пакетне додавання

На головній сторінці «Додати посилання пачкою» приймає до 500 URL, по одному
на рядок. Після додавання видно результати й посилання на непрочитані вхідні:
можна залишити матеріали оброблятися та переглядати пізніше. Дублікати не
обробляються повторно; помилки не гублять інші URL, проблемні рядки можна
виправити й надіслати знову. Черга зберігається після перезапуску.
