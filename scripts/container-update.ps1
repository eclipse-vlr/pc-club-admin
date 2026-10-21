# Called only after local-engine, image and Compose validation, under the start lock.
function Backup-ContainerBeforeUpdate {
    param([string[]]$Compose, [hashtable]$Settings, [string]$TargetImage)
    $backupRoot = Join-Path $root 'backups'
    if (-not (Test-Path -LiteralPath $backupRoot)) { New-Item -ItemType Directory -Path $backupRoot | Out-Null }
    $parent = Get-Item -LiteralPath $backupRoot -Force
    if (-not $parent.PSIsContainer -or ($parent.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Папка резервных копий должна быть обычным локальным каталогом.' }
    $directory = Join-Path $backupRoot ('before-update-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ') + '-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $directory | Out-Null
    if ($env:OS -eq 'Windows_NT') {
        $acl = New-Object System.Security.AccessControl.DirectorySecurity
        $acl.SetAccessRuleProtection($true, $false)
        $owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl.SetOwner($owner)
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($owner, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
        Set-Acl -LiteralPath $directory -AclObject $acl
    } else { [IO.File]::SetUnixFileMode($directory, [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite -bor [IO.UnixFileMode]::UserExecute) }
    Copy-Item -LiteralPath $configuration -Destination (Join-Path $directory 'env.staging')
    $excelConfiguration = Join-Path $root 'deploy/.excel.staging.json'
    if (Test-Path -LiteralPath $excelConfiguration) { Copy-Item -LiteralPath $excelConfiguration -Destination (Join-Path $directory 'excel.staging.json') }
    Invoke-LocalDocker -Arguments ($Compose + @('stop', 'api')) -Failure 'Не удалось остановить тестовый API перед резервированием. Обновление отменено.' | Out-Null
    $remote = '/tmp/pcclub-update-' + [guid]::NewGuid().ToString('N') + '.dump'
    $destination = Join-Path $directory 'database.dump'
    try {
        Invoke-LocalDocker -Arguments ($Compose + @('exec', '-T', 'db', 'pg_dump', '-U', 'pcclub_staging', '-d', 'pcclub_staging', '--format=custom', ('--file=' + $remote))) -Failure 'Резервная копия БД не создана. Образ и настройки не изменены; повторно запустите прежнюю версию.' | Out-Null
        Invoke-LocalDocker -Arguments ($Compose + @('exec', '-T', 'db', 'pg_restore', '--list', $remote)) -Failure 'Архив БД не читается. Обновление отменено.' | Out-Null
        Invoke-LocalDocker -Arguments ($Compose + @('cp', ('db:' + $remote), $destination)) -Failure 'Не удалось сохранить архив БД на ПК. Обновление отменено.' | Out-Null
        if (-not (Test-Path -LiteralPath $destination -PathType Leaf) -or (Get-Item -LiteralPath $destination).Length -eq 0) { throw 'Резервная копия не подтверждена. Обновление отменено.' }
        $manifest = @{ from_image=$Settings.STAGING_IMAGE; to_image=$TargetImage; database_sha256=(Get-FileHash -LiteralPath $destination -Algorithm SHA256).Hash.ToLowerInvariant() }
        [IO.File]::WriteAllText((Join-Path $directory 'manifest.json'), ($manifest | ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))
    } finally {
        Invoke-LocalDocker -Arguments ($Compose + @('exec', '-T', 'db', 'rm', '-f', $remote)) -Failure 'Удалите временный архив из тестового контейнера после проверки резервной копии.' | Out-Null
    }
    Write-Host "Резервная копия перед обновлением: $directory"
    # Only the immutable image reference changes. Preserve the original secrets,
    # port, timezone, line endings and all database volumes.
    $original = [IO.File]::ReadAllText($configuration)
    $next = $original.Replace(('STAGING_IMAGE=' + $Settings.STAGING_IMAGE), ('STAGING_IMAGE=' + $TargetImage))
    if ($next -ceq $original) { throw 'Исходная версия не найдена в настройках. Обновление отменено.' }
    # File replacement on the same volume keeps a partial write from becoming a
    # usable configuration. The temporary file inherits the original private ACL.
    $temporary = Join-Path $directory 'next.env'
    Copy-Item -LiteralPath $configuration -Destination $temporary
    [IO.File]::WriteAllText($temporary, $next, (New-Object Text.UTF8Encoding($false)))
    [IO.File]::Replace($temporary, $configuration, (Join-Path $directory 'replaced.env'))
}
