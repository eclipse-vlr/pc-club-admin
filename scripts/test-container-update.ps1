# Actual PostgreSQL dump/copy/restore; isolated throwaway Compose project only.
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$root=Join-Path ([IO.Path]::GetTempPath()) ('pcclub-update-check-'+[guid]::NewGuid().ToString('N'))
$configuration=Join-Path $root '.env.staging'
$dockerHost=if ($env:OS -eq 'Windows_NT') { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
function Invoke-LocalDocker {
    param([string[]]$Arguments,[string]$Failure)
    $output=@(& docker --host $dockerHost @Arguments 2>&1)
    if ($LASTEXITCODE -ne 0) { throw ($Failure + ': ' + ($output -join "`n")) }
    return ($output | ForEach-Object { $_.ToString() }) -join "`n"
}
$saved=@{}
foreach ($name in @('DOCKER_HOST','DOCKER_CONTEXT','DOCKER_TLS','DOCKER_TLS_VERIFY','DOCKER_CERT_PATH')) {
    $saved[$name]=[Environment]::GetEnvironmentVariable($name,'Process')
    [Environment]::SetEnvironmentVariable($name,$null,'Process')
}
$project='pcclub-update-check-'+[guid]::NewGuid().ToString('N')
$compose=@('compose','--project-name',$project,'--file',(Join-Path $root 'compose.yaml'))
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $yaml=@'
services:
  db:
    image: postgres:17.6-bookworm
    environment:
      POSTGRES_USER: pcclub_staging
      POSTGRES_DB: pcclub_staging
      POSTGRES_PASSWORD: synthetic-update-check
    healthcheck:
      test: [CMD-SHELL, 'test "$$(head -n 1 /var/lib/postgresql/data/postmaster.pid 2>/dev/null)" = 1 && pg_isready -U pcclub_staging -d pcclub_staging']
      interval: 1s
      timeout: 2s
      retries: 40
  api:
    image: postgres:17.6-bookworm
'@
    [IO.File]::WriteAllText((Join-Path $root 'compose.yaml'),$yaml)
    $old='ghcr.io/eclipse-vlr/pc-club-admin@sha256:'+('a'*64)
    $next='ghcr.io/eclipse-vlr/pc-club-admin@sha256:'+('b'*64)
    $original="STAGING_IMAGE=$old`nSTAGING_PIN_PEPPER=synthetic-private-value`n"
    [IO.File]::WriteAllText($configuration,$original)
    if ($env:OS -ne 'Windows_NT') { [IO.File]::SetUnixFileMode($configuration,[IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite) }
    Invoke-LocalDocker -Arguments ($compose+@('up','-d','--wait','db')) -Failure 'Isolated database did not start.' | Out-Null
    $sql="CREATE TABLE update_fixture(id integer PRIMARY KEY, value text NOT NULL); INSERT INTO update_fixture VALUES(1,'synthetic-before-update');"
    Invoke-LocalDocker -Arguments ($compose+@('exec','-T','db','psql','-U','pcclub_staging','-d','pcclub_staging','-v','ON_ERROR_STOP=1','-c',$sql)) -Failure 'Fixture failed.' | Out-Null
    . (Join-Path $PSScriptRoot 'container-update.ps1')
    Backup-ContainerBeforeUpdate -Compose $compose -Settings @{STAGING_IMAGE=$old} -TargetImage $next
    if ([IO.File]::ReadAllText($configuration) -cne $original.Replace($old,$next)) { throw 'Update changed settings unexpectedly.' }
    $backup=Get-ChildItem -LiteralPath (Join-Path $root 'backups') -Directory | Select-Object -First 1
    if ([IO.File]::ReadAllText((Join-Path $backup.FullName 'env.staging')) -cne $original) { throw 'Original settings missing from backup.' }
    if ($env:OS -ne 'Windows_NT' -and [int][IO.File]::GetUnixFileMode($backup.FullName) -ne 448) { throw 'Backup directory is not private.' }
    $manifest=[IO.File]::ReadAllText((Join-Path $backup.FullName 'manifest.json')) | ConvertFrom-Json
    if ($manifest.database_sha256 -cne (Get-FileHash (Join-Path $backup.FullName 'database.dump') -Algorithm SHA256).Hash.ToLowerInvariant()) { throw 'Archive checksum differs.' }
    Invoke-LocalDocker -Arguments ($compose+@('exec','-T','db','createdb','-U','pcclub_staging','pcclub_restored_test')) -Failure 'Restore DB failed.' | Out-Null
    Invoke-LocalDocker -Arguments ($compose+@('cp',(Join-Path $backup.FullName 'database.dump'),'db:/tmp/restore.dump')) -Failure 'Restore copy failed.' | Out-Null
    Invoke-LocalDocker -Arguments ($compose+@('exec','-T','db','pg_restore','-U','pcclub_staging','-d','pcclub_restored_test','--exit-on-error','/tmp/restore.dump')) -Failure 'Restore failed.' | Out-Null
    $value=Invoke-LocalDocker -Arguments ($compose+@('exec','-T','db','psql','-U','pcclub_staging','-d','pcclub_restored_test','-At','-c','SELECT value FROM update_fixture WHERE id=1')) -Failure 'Read restored DB failed.'
    if ($value.Trim() -cne 'synthetic-before-update') { throw 'Restored data differs.' }
    Write-Host 'PASS: actual isolated PostgreSQL backup, binary Docker copy, checksum, private directory, image-only update and restore.'
} finally {
    try { Invoke-LocalDocker -Arguments ($compose+@('down','--volumes')) -Failure 'Clean up the isolated update check.' | Out-Null }
    finally {
        foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name,$saved[$name],'Process') }
        Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
    }
}
