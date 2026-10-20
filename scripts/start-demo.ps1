# Start an existing local installation. Never generates or replaces secrets.
[CmdletBinding()]
param(
    [string]$DriveFileId = '',
    [string]$Credentials = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$originalLocation = Get-Location
try {
    Set-Location (Split-Path -Parent $PSScriptRoot)
    if (-not (Test-Path -LiteralPath '.env' -PathType Leaf)) {
        throw 'Local .env is missing. Restore your existing configuration before starting.'
    }
    . "$PSScriptRoot/load-env.ps1"
    $env:APP_ENV = 'test'
    # Drive requires explicit arguments on each launch; never inherit another target.
    $env:DRIVE_EXCEL_SYNC = 'disabled'
    if ($DriveFileId -or $Credentials) {
        if ($DriveFileId -notmatch '^[A-Za-z0-9_-]{10,200}$' -or
            -not $Credentials -or -not (Test-Path -LiteralPath $Credentials -PathType Leaf)) {
            throw 'Provide both a valid test copy -DriveFileId and an existing -Credentials JSON path.'
        }
        $env:DRIVE_EXCEL_FILE_ID = $DriveFileId
        $env:DRIVE_EXCEL_CREDENTIALS_FILE = (Resolve-Path -LiteralPath $Credentials).Path
        $env:DRIVE_EXCEL_SYNC = 'test'
    }
    if (-not $env:DATABASE_URL -or -not $env:PIN_PEPPER -or $env:PIN_PEPPER.Length -lt 32) {
        throw 'DATABASE_URL or the original PIN_PEPPER is missing in .env. Do not generate a replacement pepper.'
    }
    if (-not $env:HTTP_ADDR) { $env:HTTP_ADDR = '127.0.0.1:8080' }
    if ($env:HTTP_ADDR -notmatch '^127\.0\.0\.1:(\d{1,5})$') {
        throw 'Demo launcher requires HTTP_ADDR=127.0.0.1:<port>.'
    }
    $port = [int]$matches[1]
    if ($port -lt 1 -or $port -gt 65535) { throw 'Invalid HTTP port.' }
    $expectedOrigin = "http://127.0.0.1:$port"
    if ($env:APP_ORIGIN -and $env:APP_ORIGIN -ne $expectedOrigin) {
        throw 'APP_ORIGIN must match the local HTTP_ADDR for this demo.'
    }
    $env:APP_ORIGIN = $expectedOrigin
    $listener = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { $_.LocalPort -eq $port })
    if ($listener.Count -gt 0) {
        $processIds = ($listener | Select-Object -ExpandProperty OwningProcess -Unique) -join ', '
        throw "Port $port is already in use (PID: $processIds). Stop the previous API in its window with Ctrl+C."
    }
    foreach ($command in @('go', 'docker', 'npm.cmd')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) { throw "Missing command: $command" }
    }
    & go version
    if ($LASTEXITCODE -ne 0) { throw 'Go is unavailable.' }
    & docker info --format '{{.ServerVersion}}'
    if ($LASTEXITCODE -ne 0) { throw 'Start Docker Desktop and wait for the engine to become ready.' }
    & docker compose up -d --wait --wait-timeout 90 db
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL did not become ready.' }
    & go run ./cmd/migrate up
    if ($LASTEXITCODE -ne 0) { throw 'Database migration failed. API was not started.' }
    if (-not (Test-Path -LiteralPath 'web/node_modules' -PathType Container)) {
        & npm.cmd --prefix web ci
        if ($LASTEXITCODE -ne 0) { throw 'Frontend dependency installation failed.' }
    }
    & npm.cmd --prefix web run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
    Write-Host "Open $expectedOrigin after the API starts. Keep this window open; Ctrl+C stops the API."
    Write-Host "Test mode. Drive synchronization: $env:DRIVE_EXCEL_SYNC."
    & go run ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw 'API exited with an error. Check the messages above.' }
}
finally {
    Set-Location $originalLocation
}
