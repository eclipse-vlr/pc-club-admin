# Loaded by start-container.ps1 in its locked scope. No Google key is read here.
function Get-ExcelSettings {
    param([string]$FileId, [string]$CredentialsFile, [string]$ConfirmFileId, [switch]$Disabled)
    $file = Join-Path $root 'deploy/.excel.staging.json'
    $supplied = $FileId -or $CredentialsFile -or $ConfirmFileId
    if ($Disabled -and $supplied) { throw 'Нельзя одновременно подключать и отключать Excel.' }
    if ($Disabled) { return $null }
    if ($supplied) {
        if (-not $FileId -or -not $CredentialsFile -or $FileId -cne $ConfirmFileId) {
            throw 'Укажите ExcelFileId, ExcelCredentialsFile и тот же ID тестовой копии в ConfirmTestFileId.'
        }
        $result = @{ file_id=$FileId; credentials_path=$CredentialsFile }
    } elseif (Test-Path -LiteralPath $file) {
        $item = Get-Item -LiteralPath $file -Force
        if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $item.Length -gt 8192) {
            throw 'Некорректный файл настройки тестового Excel.'
        }
        try { $stored = [IO.File]::ReadAllText($file) | ConvertFrom-Json } catch { throw 'Не удалось прочитать настройки тестового Excel.' }
        if ($null -eq $stored -or @($stored.PSObject.Properties.Name).Count -ne 2 -or
            -not $stored.PSObject.Properties['file_id'] -or -not $stored.PSObject.Properties['credentials_path'] -or
            $stored.file_id -isnot [string] -or $stored.credentials_path -isnot [string]) {
            throw 'Некорректные поля настройки тестового Excel.'
        }
        $result = @{ file_id=$stored.file_id; credentials_path=$stored.credentials_path }
    } else { return $null }
    if ($result.file_id -cnotmatch '^[A-Za-z0-9_-]{10,200}$' -or $result.credentials_path -match '[\x00-\x1f]') {
        throw 'Некорректный ID тестового файла или путь к ключу.'
    }
    # Require an existing ordinary file outside the distribution, never a directory
    # that Docker could create, a network share, or a link into a different location.
    if (-not [IO.Path]::IsPathRooted($result.credentials_path) -or $result.credentials_path.StartsWith('\\') -or
        ($env:OS -eq 'Windows_NT' -and $result.credentials_path -notmatch '^[A-Za-z]:[\\/]')) {
        throw 'Укажите абсолютный путь к локальному JSON-ключу вне папки приложения.'
    }
    if (-not (Test-Path -LiteralPath $result.credentials_path -PathType Leaf)) { throw 'JSON-ключ не найден на этом ПК. Содержимое ключа в чат не отправляйте.' }
    $key = Get-Item -LiteralPath $result.credentials_path -Force
    $prefix = [IO.Path]::GetFullPath($root).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if ($key.FullName.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase) -or $key.Length -eq 0 -or $key.Length -gt 65536) {
        throw 'Ключ должен находиться вне папки приложения и быть обычным JSON-файлом.'
    }
    $cursor = $key
    while ($null -ne $cursor) {
        if ($cursor.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Ссылки в пути к JSON-ключу не поддерживаются.' }
        if ($cursor -is [IO.FileInfo]) { $cursor = $cursor.Directory } else { $cursor = $cursor.Parent }
    }
    $result.credentials_path = $key.FullName.Replace('\', '/')
    if ($supplied -and (Test-Path -LiteralPath $file)) {
        $previous = Get-ExcelSettings
        if ($previous.file_id -cne $result.file_id -or $previous.credentials_path -cne $result.credentials_path) {
            throw 'Сначала отключите прежний Excel с DisableExcel. Рабочий адрес не заменяется автоматически.'
        }
    }
    return $result
}

function Save-ExcelSettings {
    param([hashtable]$Settings)
    $file = Join-Path $root 'deploy/.excel.staging.json'
    if (Test-Path -LiteralPath $file) { return }
    $stream = [IO.File]::Open($file, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        if ($env:OS -eq 'Windows_NT') {
            $acl = New-Object System.Security.AccessControl.FileSecurity
            $acl.SetAccessRuleProtection($true, $false)
            $owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
            $acl.SetOwner($owner)
            $acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($owner, 'FullControl', 'Allow')))
            Set-Acl -LiteralPath $file -AclObject $acl
        } else { [IO.File]::SetUnixFileMode($file, [IO.UnixFileMode]::UserRead -bor [IO.UnixFileMode]::UserWrite) }
        $bytes = (New-Object Text.UTF8Encoding($false)).GetBytes(($Settings | ConvertTo-Json -Compress))
        $stream.Write($bytes, 0, $bytes.Length)
        $stream.Flush($true)
    } finally { $stream.Dispose() }
}
