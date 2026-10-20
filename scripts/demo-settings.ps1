# Data-only local configuration. Never read or copy the service-account JSON.
function New-DemoDriveSettings {
    param([string]$FileId, [string]$CredentialsPath)
    if ($FileId -notmatch '^[A-Za-z0-9_-]{10,200}$' -or -not $CredentialsPath -or
        -not (Test-Path -LiteralPath $CredentialsPath -PathType Leaf)) {
        throw 'Provide both a valid test copy -DriveFileId and an existing -Credentials JSON path.'
    }
    return [pscustomobject]@{ format = 1; drive_file_id = $FileId; credentials_file = (Resolve-Path -LiteralPath $CredentialsPath).Path }
}

function Read-DemoDriveSettings {
    param([string]$SettingsPath)
    if (-not (Test-Path -LiteralPath $SettingsPath)) { return $null }
    try {
        $file = Get-Item -LiteralPath $SettingsPath -Force
        if (-not ($file -is [System.IO.FileInfo]) -or $file.Length -gt 8192) { throw 'Invalid file.' }
        $settings = Get-Content -LiteralPath $SettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json
        if (-not ($settings -is [pscustomobject])) { throw 'Invalid object.' }
        $names = @($settings.PSObject.Properties.Name)
        if ($names.Count -ne 3 -or @($names | Where-Object { $_ -notin @('format', 'drive_file_id', 'credentials_file') }).Count -gt 0 -or
            $settings.format -cne 1 -or -not ($settings.drive_file_id -is [string]) -or -not ($settings.credentials_file -is [string])) {
            throw 'Invalid fields.'
        }
        return New-DemoDriveSettings -FileId $settings.drive_file_id -CredentialsPath $settings.credentials_file
    }
    catch { throw 'Saved demo settings are invalid or the key file is unavailable. Save valid settings again, or use -WithoutDrive.' }
}

function Save-DemoDriveSettings {
    param([string]$SettingsPath, $Settings)
    if (Test-Path -LiteralPath $SettingsPath -PathType Container) { throw 'Demo settings path must be a file.' }
    # Revalidate before replacing an existing configuration.
    $validated = New-DemoDriveSettings -FileId $Settings.drive_file_id -CredentialsPath $Settings.credentials_file
    $temporary = $SettingsPath + '.' + [guid]::NewGuid().ToString('N') + '.tmp'
    try {
        $json = $validated | ConvertTo-Json
        [System.IO.File]::WriteAllText($temporary, $json, (New-Object System.Text.UTF8Encoding($false)))
        Move-Item -LiteralPath $temporary -Destination $SettingsPath -Force
    }
    finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force }
    }
}
