# PC Club Admin

Веб-приложение одного компьютерного клуба. Сейчас выполнен инфраструктурный каркас этапов 0–1; для реального учёта кассы и передачи смен не готово. Основное ТЗ: [docs/PC_Club_Codex_Technical_Spec.md](docs/PC_Club_Codex_Technical_Spec.md).

Go 1.25.3, PostgreSQL 17.6, React 19.2, TypeScript 5.9, Vite 7; Node.js 24.19 и npm 11.9, Docker Engine/Desktop + Compose v2. На Windows включите Linux containers. Все команды выполняются из корня pc-club-admin, если не указано иное.

## Архитектура и границы

Один Go backend отдаёт JSON API и собранный frontend с одного origin; PostgreSQL хранит данные. В dev Vite проксирует /api, /healthz и /readyz на Go. Config валидируется при старте. Миграции запускаются отдельной CLI; приложение само их не применяет и отказывается стартовать с отсутствующей/неподходящей схемой.

Реализованы `/healthz`, `/readyz`, `/api/v1/status`, статическая оболочка UI и схема настроек клуба. Авторизации, смен, денег и персональных данных пока нет. Публичный status сообщает `operations_enabled=false`, а готовность самого MVP не декларируется.

`CASH_POLICY_STATUS=unverified` — единственное разрешённое значение этой версии. `APP_ENV=production` оставляет только диагностику и оболочку: readiness отвечает 503 `CASH_POLICY_UNVERIFIED`. Установить `verified` флагом нельзя: сначала нужны реальная проверка, согласованная версия политики, ADR, миграция и тесты. Предварительная арифметика будет изолирована на этапе 4 только в test-mode.

Проектные допущения и обязательные ограничения: [docs/decisions](docs/decisions). Часовой пояс `Europe/Moscow` требует подтверждения владельцем. Миграция сохраняет его в singleton клуба; повторный запуск не меняет настройки. Матрица реализации: [docs/requirements.md](docs/requirements.md).

## Быстрый запуск — PowerShell

Установите Go, Node и Docker Desktop. Создайте локальную конфигурацию (не коммитить). Пример пароля в `.env.example` предназначен только для loopback dev-базы. Если меняете пароль, одновременно исправьте `DATABASE_URL`; специальные символы URL должны быть percent-encoded.

```powershell
Copy-Item .env.example .env
. ./scripts/load-env.ps1
docker compose up -d --wait db
go mod download
go run ./cmd/migrate up
Push-Location web
npm ci
npm run build
Pop-Location
go run ./cmd/api
```

Откройте http://127.0.0.1:8080. В другом терминале:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/readyz
Invoke-RestMethod http://127.0.0.1:8080/api/v1/status
```

Для frontend-разработки при работающем API:

```powershell
Set-Location web
npm run dev
```

Vite: http://127.0.0.1:5173. API слушает loopback; не публикуйте этот dev-сервер в интернет. Настройки `.env` Go автоматически не читает: в каждом новом PowerShell-терминале выполняйте `. ./scripts/load-env.ps1` из корня. Скрипт разбирает только простые строки `KEY=VALUE`, без кавычек/подстановок.

## Быстрый запуск — Linux

```bash
cp .env.example .env
set -a
. ./.env
set +a
docker compose up -d --wait db
go mod download
go run ./cmd/migrate up
npm --prefix web ci
npm --prefix web run build
go run ./cmd/api
```

Исполняйте через shell только свой локальный `.env`. Для dev UI в отдельном терминале: `npm --prefix web run dev`.

## Миграции и диагностика

`go run ./cmd/migrate up` выполняет единственную миграцию этого этапа транзакционно под advisory lock; повтор безопасен. В БД записывается SHA-256 SQL. Изменение уже применённой миграции, неизвестная версия или несовпадение timezone отклоняются. Новые изменения схемы в следующих этапах требуют нового SQL и расширения manifest, не правки применённого файла.

`/healthz` проверяет только жизнь процесса; `/readyz` в test-mode проверяет доступность БД, manifest/checksum и настройки клуба с тайм-аутом 2 секунды. Ответ не раскрывает URL БД, SQL или stacktrace. Если БД недоступна на старте, API завершится с ненулевым кодом; если БД потеряна после старта, readiness станет 503. При production неподтверждённая политика приоритетно возвращает `CASH_POLICY_UNVERIFIED`.

SQL up/down находятся в `migrations/`, но автоматической команды down нет. Down уничтожает данные; для реальной среды нужны совместимость версий и проверенное восстановление из копии. `docker compose down` сохраняет данные в volume; не добавляйте `-v`, если данные нужны. Обновление этого каркаса: остановить API, сохранить резервную копию при наличии данных, обновить код, `go mod download`, `npm ci`, `npm run build`, отдельный `migrate up`, затем запустить API. Несовместимая версия намеренно не стартует.

## Проверки — PowerShell

```powershell
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/api ./cmd/migrate
Push-Location web
npm run typecheck
npm run lint
npm run build
Pop-Location
docker compose config --quiet
```

Для `-race` на Windows нужен доступный C toolchain (например MinGW-w64); можно выполнить в WSL/Linux. CI использует Linux.

Integration suite требует отдельную БД с именем, заканчивающимся `_test`. Не указывайте рабочую БД. Тест создаёт уникальную схему и удаляет только её. Если TEST_DATABASE_URL не задан, suite с integration tag завершается ошибкой, не ложным PASS.

```powershell
docker compose exec -T db createdb -U pcclub pcclub_test
$env:TEST_DATABASE_URL = "postgres://pcclub:local-development-only@localhost:5432/pcclub_test?sslmode=disable"
go test -race -tags=integration ./internal/storage/postgres -count=1
```

Linux:

```bash
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/api ./cmd/migrate
docker compose exec -T db createdb -U pcclub pcclub_test
TEST_DATABASE_URL='postgres://pcclub:local-development-only@localhost:5432/pcclub_test?sslmode=disable' go test -race -tags=integration ./internal/storage/postgres -count=1
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
docker compose config --quiet
```

Если тестовая БД уже создана, пропустите createdb. CI проверяет format/vet/unit/race/integration, сборку frontend и HTTP smoke после миграции. Бизнес E2E пока отсутствуют: вход/передача ещё не реализованы; они добавляются на этапах 2–6. Текущие результаты фактически выполненных проверок: [docs/verification.md](docs/verification.md).

## Следующие этапы и эксплуатация

Следующий этап 2: пользователи и роли, PIN/manager password, enrollment браузера, сессии, CSRF/Origin, аудит и устойчивые блокировки попыток входа. Bootstrap управляющего и регистрация ПК пока не реализованы: готовых команд для них нет. Не создавайте публичную регистрацию или общий seed-пароль.

Далее по ТЗ: 31 пункт чек-листа и первая opening-смена; изолированный тестовый расчёт кассы; атомарная передача с двумя личными входами, блокировками, авторством, аудитом и идемпотентностью. Менеджерского обхода кассы/чек-листа не будет. Поломка требует фиксации, а не обязательного ремонта. API LANGAME и автоматизация его браузера отсутствуют и не планируются в MVP.

Контейнерная поставка API/frontend, HTTPS reverse proxy, команды backup/restore и проверка восстановления — этап 8. Нынешний Compose запускает только dev PostgreSQL и не является production-конфигурацией. До пилота нужны ежедневная зашифрованная копия вне сервера, согласованный срок хранения, измеренное восстановление на отдельной БД и условия раздела 16 ТЗ. Непроверенные инструкции восстановления здесь не выдаются за готовую поставку.

При потере связи с API передача в приложении недоступна. Будущий UI не должен объявлять успех без коммита и должен перечитывать состояние после тайм-аута. Аварийный внешний порядок (бумага/Excel) нужно согласовать до пилота. Офлайн-обходов нет. Реальные Excel, скриншоты и персональные данные не коммитить; `reference/` исключён из Git.
