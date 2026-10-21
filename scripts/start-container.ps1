# An isolated test installation using a published immutable image. No Go, Node or build.
[CmdletBinding()]
param(
    [string]$Image = '',
    [switch]$BootstrapManager,
    [switch]$NoBrowser
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$originalLocation = Get-Location
$savedEnvironment = @{}
$lock = $null
$root = Split-Path -Parent $PSScriptRoot
$configuration = Join-Path $root 'deploy/.env.staging'
$composeFile = Join-Path $root 'deploy/compose.staging.yaml'
$imagePattern = '^ghcr\.io/eclipse-vlr/pc-club-admin@sha256:[0-9a-f]{64}$'
$dockerHost = if ($env:OS -eq 'Windows_NT') { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }

function Invoke-LocalDocker {
    param([string[]]$Arguments, [string]$Failure, [switch]$Interactive)
    # Provider/Compose diagnostics can contain settings. Expose a fixed error instead.
    $ErrorActionPreference = 'Continue'
    if ($Interactive) {
        & docker --host $dockerHost @Arguments
        if ($LASTEXITCODE -ne 0) { throw $Failure }
    } else {
        $result = @(& docker --host $dockerHost @Arguments 2>&1)
        if ($LASTEXITCODE -ne 0) { throw $Failure }
        return ($result | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine
    }
}
function New-RandomHex {
    $bytes = New-Object byte[] 32
    $random = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $random.GetBytes($bytes) } finally { $random.Dispose() }
    return [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
}
function Read-ContainerSettings {
    if ((Get-Item -LiteralPath $configuration -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw 'Настройки должны храниться в обычном локальном файле.'
    }
    if ((Get-Item -LiteralPath $configuration -Force).Length -gt 4096) { throw 'Некорректные настройки тестового контура.' }
    $values = @{}
    foreach ($line in [IO.File]::ReadAllLines($configuration)) {
        if (-not $line -or $line.StartsWith('#')) { continue }
        if ($line -notmatch '^(STAGING_IMAGE|STAGING_DB_PASSWORD|STAGING_PIN_PEPPER|STAGING_TIMEZONE|STAGING_HTTP_PORT)=(.+)$') {
            throw 'В deploy/.env.staging обнаружен неизвестный параметр.'
        }
        $key = $matches[1]; $value = $matches[2]
        if ($values.ContainsKey($key)) { throw 'В настройках обнаружен повтор параметра.' }
        $values[$key] = $value
    }
    if ($values.Count -ne 5 -or $values.STAGING_IMAGE -cnotmatch $imagePattern -or
        $values.STAGING_DB_PASSWORD -cnotmatch '^[0-9a-f]{64}$' -or
        $values.STAGING_PIN_PEPPER -cnotmatch '^[0-9a-f]{64}$' -or
        $values.STAGING_TIMEZONE -cnotmatch '^[A-Za-z_+-]+(/[A-Za-z0-9_+.-]+)*$' -or
        $values.STAGING_HTTP_PORT -notmatch '^[0-9]{1,5}$') { throw 'Настройки некорректны. Сохраните исходный файл для восстановления.' }
    $port = [int]$values.STAGING_HTTP_PORT
    if ($port -lt 1 -or $port -gt 65535) { throw 'Некорректный локальный порт.' }
    return $values
}

try {
    Set-Location $root
    $lock = [IO.File]::Open((Join-Path $root 'deploy/.container-start.lock'), [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    $existing = Test-Path -LiteralPath $configuration
    if ($existing -and -not (Test-Path -LiteralPath $configuration -PathType Leaf)) { throw 'По пути настроек находится не файл.' }
    $settings = $null
    if ($existing) {
        $settings = Read-ContainerSettings
        if ($Image -and $Image -cne $settings.STAGING_IMAGE) {
            throw 'Сохранённая версия образа не изменена. Перед обновлением создайте резервную копию и выполните инструкцию обновления.'
        }
        $Image = $settings.STAGING_IMAGE
    } elseif (-not $Image) {
        $Image = Read-Host 'Вставьте ссылку ghcr.io образ@sha256 из итогов успешного CI в GitHub'
    }
    if ($Image -cnotmatch $imagePattern) { throw 'Нужна полная ссылка ghcr.io/eclipse-vlr/pc-club-admin@sha256 из итогов CI.' }
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { throw 'Установите и запустите Docker Desktop с контейнерами Linux.' }
    # Always address the local engine; preserve credentials and restore caller selectors.
    foreach ($name in @('DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_TLS', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH',
                        'STAGING_IMAGE', 'STAGING_DB_PASSWORD', 'STAGING_PIN_PEPPER', 'STAGING_TIMEZONE', 'STAGING_HTTP_PORT')) {
        $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $null, 'Process')
    }
    $engine = Invoke-LocalDocker -Arguments @('info', '--format', '{{.OSType}}') -Failure 'Запустите Docker Desktop и выберите контейнеры Linux.'
    if ($engine.Trim() -cne 'linux') { throw 'Нужен режим контейнеров Linux.' }
    Invoke-LocalDocker -Arguments @('compose', 'version') -Failure 'Нужен Docker Compose версии 2 или новее.' | Out-Null
    if (-not $existing) {
        $containers = Invoke-LocalDocker -Arguments @('ps', '-a', '--filter', 'label=com.docker.compose.project=pc-club-staging', '--format', '{{.ID}}') -Failure 'Не удалось проверить существующую тестовую установку.'
        $volumes = Invoke-LocalDocker -Arguments @('volume', 'ls', '--filter', 'label=com.docker.compose.project=pc-club-staging', '--format', '{{.Name}}') -Failure 'Не удалось проверить существующую базу.'
        if ($containers.Trim() -or $volumes.Trim()) { throw 'Найдена прежняя тестовая установка. Восстановите её исходный deploy/.env.staging. Новые секреты не созданы.' }
    }
    Write-Host 'Скачиваю выбранный тестовый образ...'
    Invoke-LocalDocker -Arguments @('image', 'pull', $Image) -Failure 'Не удалось скачать образ. Проверьте ссылку, соединение и доступ к пакету GHCR.' | Out-Null
    $description = Invoke-LocalDocker -Arguments @('image', 'inspect', '--format', '{{json .}}', $Image) -Failure 'Не удалось проверить скачанный образ.'
    $metadata = $description | ConvertFrom-Json
    if ($metadata.Os -cne 'linux' -or $metadata.Architecture -cne 'amd64' -or $metadata.Config.User -cne '10001:10001' -or
        $metadata.Config.Labels.'org.opencontainers.image.source' -cne 'https://github.com/eclipse-vlr/pc-club-admin' -or
        $metadata.Config.Labels.'org.opencontainers.image.revision' -cnotmatch '^[0-9a-f]{40}$') {
        throw 'Образ не соответствует выпуску PC Club Admin.'
    }
    if (-not $existing) {
        # CreateNew and private permissions before any secret bytes are written.
        $stream = [IO.File]::Open($configuration, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            if ($env:OS -eq 'Windows_NT') {
                $acl = New-Object System.Security.AccessControl.FileSecurity
                $acl.SetAccessRuleProtection($true, $false)
                $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User
                $acl.SetOwner($identity)
                $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($identity, 'FullControl', 'Allow')
                $acl.AddAccessRule($rule)
                Set-Acl -LiteralPath $configuration -AclObject $acl
            } else {
                [IO.File]::SetUnixFileMode($configuration, [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite)
            }
            $text = @(
                '# Isolated synthetic test data; preserve this file.'
                "STAGING_IMAGE=$Image"
                "STAGING_DB_PASSWORD=$(New-RandomHex)"
                "STAGING_PIN_PEPPER=$(New-RandomHex)"
                'STAGING_TIMEZONE=Europe/Moscow'
                'STAGING_HTTP_PORT=18080'
                ''
            ) -join [Environment]::NewLine
            $bytes = (New-Object Text.UTF8Encoding($false)).GetBytes($text)
            $stream.Write($bytes, 0, $bytes.Length)
            $stream.Flush($true)
        } finally { $stream.Dispose() }
        $settings = Read-ContainerSettings
    }
    foreach ($key in $settings.Keys) { [Environment]::SetEnvironmentVariable($key, $settings[$key], 'Process') }
    $compose = @('compose', '--project-name', 'pc-club-staging', '--env-file', $configuration, '-f', $composeFile)
    $rendered = Invoke-LocalDocker -Arguments ($compose + @('config', '--format', 'json')) -Failure 'Некорректная конфигурация тестового контура.'
    $effective = $rendered | ConvertFrom-Json
    if ($effective.name -cne 'pc-club-staging') { throw 'Неожиданное имя проекта Compose.' }
    foreach ($serviceName in @('api', 'migrate', 'admin')) {
        $service = $effective.services.$serviceName
        if ($service.image -cne $Image -or $service.environment.APP_ENV -cne 'test' -or
            $service.environment.CASH_POLICY_STATUS -cne 'unverified' -or
            $service.environment.DRIVE_EXCEL_SYNC -cne 'disabled' -or
            $service.environment.PIN_PEPPER -cne $settings.STAGING_PIN_PEPPER -or
            $service.environment.DATABASE_URL -cne ("postgres://pcclub_staging:" + $settings.STAGING_DB_PASSWORD + '@db/pcclub_staging?sslmode=disable')) {
            throw 'Compose должен использовать отдельные тестовые образ, базу и денежную политику.'
        }
    }
    $databaseVolume = $effective.volumes.staging_postgres_data
    if ($databaseVolume.name -cne 'pc-club-staging_staging_postgres_data' -or
        ($databaseVolume.PSObject.Properties['external'] -and $databaseVolume.external) -or
        $effective.services.db.environment.POSTGRES_USER -cne 'pcclub_staging' -or
        $effective.services.db.environment.POSTGRES_DB -cne 'pcclub_staging' -or
        $effective.services.db.environment.POSTGRES_PASSWORD -cne $settings.STAGING_DB_PASSWORD -or
        $effective.services.db.volumes.Count -ne 1 -or
        $effective.services.db.volumes[0].source -cne 'staging_postgres_data' -or
        $effective.services.db.PSObject.Properties['ports'] -or
        $effective.services.api.ports.Count -ne 1 -or
        $effective.services.api.ports[0].host_ip -cne '127.0.0.1' -or
        [int]$effective.services.api.ports[0].published -ne [int]$settings.STAGING_HTTP_PORT) {
        throw 'Compose должен использовать собственный том базы и API только на локальном адресе.'
    }
    Invoke-LocalDocker -Arguments ($compose + @('up', '-d', '--wait', '--wait-timeout', '90', 'db')) -Failure 'Тестовая база не стала доступной.' | Out-Null
    Invoke-LocalDocker -Arguments ($compose + @('run', '--rm', '-T', 'migrate')) -Failure 'Миграция не выполнена. Настройки и база сохранены.' | Out-Null
    $userState = Invoke-LocalDocker -Arguments ($compose + @('exec', '-T', 'db', 'psql', '-U', 'pcclub_staging', '-d', 'pcclub_staging', '-At', '-v', 'ON_ERROR_STOP=1', '-c', 'SELECT EXISTS (SELECT 1 FROM users)')) -Failure 'Не удалось проверить состояние первой настройки.'
    $userState = $userState.Trim()
    if ($userState -cne 't' -and $userState -cne 'f') { throw 'База не подтвердила состояние первой настройки.' }
    if ($BootstrapManager -and $userState -ceq 't') { throw 'Пользователи уже созданы; повторная инициализация запрещена.' }
    if ($userState -ceq 'f') {
        Write-Host 'Создайте первого тестового управляющего. Введите новый пароль дважды по запросу.'
        Invoke-LocalDocker -Arguments ($compose + @('run', '--rm', 'admin', 'bootstrap-manager', '--login', 'manager', '--name', 'Управляющий тестового контура')) -Failure 'Создание управляющего не завершено. Сохраните настройки; повтор с -BootstrapManager нужен только для незавершённой установки.' -Interactive
    }
    Invoke-LocalDocker -Arguments ($compose + @('up', '-d', '--wait', '--wait-timeout', '90', 'api')) -Failure 'Приложение не стало доступным. Сохраните настройки и базу.' | Out-Null
    $origin = 'http://127.0.0.1:' + $settings.STAGING_HTTP_PORT
    $null = Invoke-RestMethod -Uri ($origin + '/readyz') -TimeoutSec 5
    $status = Invoke-RestMethod -Uri ($origin + '/api/v1/status') -TimeoutSec 5
    if ($status.environment -cne 'test' -or $status.cash_policy_status -cne 'unverified' -or $status.excel_sync_enabled -ne $false) {
        throw 'Приложение не подтвердило отдельный тестовый режим.'
    }
    Write-Host "Тестовая версия готова: $origin"
    Write-Host 'Логин: manager. Только вымышленные данные; кассовый расчёт предварительный; Drive выключен.'
    if (-not $NoBrowser) { Start-Process $origin }
} finally {
    if ($lock) { $lock.Dispose() }
    foreach ($name in $savedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process') }
    Set-Location $originalLocation
}
