# Isolated checks only: no Docker, API, database, browser or Google requests.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$repo = Split-Path -Parent $PSScriptRoot
$fixture = Join-Path ([System.IO.Path]::GetTempPath()) ('pcclub-demo-test-' + [guid]::NewGuid().ToString('N'))
$originalLocation = Get-Location
function Assert-True { param([bool]$Condition, [string]$Message); if (-not $Condition) { throw $Message } }
function Assert-Rejected {
    param([scriptblock]$Work)
    $rejected = $false
    try { & $Work | Out-Null } catch { $rejected = $true }
    Assert-True $rejected 'Expected an invalid configuration to be rejected.'
}
function go { throw 'Test must not start Go.' }
function docker { throw 'Test must not start Docker.' }
function npm.cmd { throw 'Test must not start npm.' }
try {
    New-Item -ItemType Directory -Path (Join-Path $fixture 'scripts') | Out-Null
    foreach ($name in @('start-demo.ps1', 'load-env.ps1', 'demo-settings.ps1', 'test-demo.ps1')) {
        $source = Join-Path $PSScriptRoot $name
        $tokens = $null; $parseErrors = $null
        [System.Management.Automation.Language.Parser]::ParseFile($source, [ref]$tokens, [ref]$parseErrors) | Out-Null
        Assert-True ($parseErrors.Count -eq 0) "PowerShell parse failed: $name"
        Copy-Item -LiteralPath $source -Destination (Join-Path $fixture 'scripts')
    }
    . "$PSScriptRoot/demo-settings.ps1"
    $key = Join-Path $fixture 'key with space.json'
    $marker = 'synthetic-private-content-must-not-be-copied'
    [System.IO.File]::WriteAllText($key, $marker)
    $settingsPath = Join-Path $fixture '.demo.local.json'
    Assert-True ($null -eq (Read-DemoDriveSettings -SettingsPath $settingsPath)) 'Missing settings should disable Drive.'
    Assert-Rejected { New-DemoDriveSettings -FileId '../invalid' -CredentialsPath $key }
    Assert-Rejected { New-DemoDriveSettings -FileId 'synthetic-test-copy' -CredentialsPath (Join-Path $fixture 'missing.json') }
    $settings = New-DemoDriveSettings -FileId 'synthetic-test-copy' -CredentialsPath $key
    Save-DemoDriveSettings -SettingsPath $settingsPath -Settings $settings
    $saved = Read-DemoDriveSettings -SettingsPath $settingsPath
    Assert-True ($saved.drive_file_id -eq 'synthetic-test-copy' -and $saved.credentials_file -eq $key) 'Saved target was not restored.'
    $original = Get-Content -LiteralPath $settingsPath -Raw
    Assert-True (-not $original.Contains($marker)) 'Private file contents were copied.'
    $invalid = [pscustomobject]@{ drive_file_id = '../invalid'; credentials_file = $key }
    Assert-Rejected { Save-DemoDriveSettings -SettingsPath $settingsPath -Settings $invalid }
    Assert-True ((Get-Content -LiteralPath $settingsPath -Raw) -eq $original) 'Invalid save replaced previous configuration.'
    foreach ($invalidJSON in @('{broken', '[]', '{"format":2}', '{"format":1,"drive_file_id":"synthetic-test-copy","credentials_file":"missing","extra":"not allowed"}')) {
        [System.IO.File]::WriteAllText($settingsPath, $invalidJSON)
        Assert-Rejected { Read-DemoDriveSettings -SettingsPath $settingsPath }
    }
    [System.IO.File]::WriteAllText($settingsPath, ('x' * 8193))
    Assert-Rejected { Read-DemoDriveSettings -SettingsPath $settingsPath }
    # Real configuration-only entrypoint works without .env or any runtime tools.
    $launcher = Join-Path $fixture 'scripts/start-demo.ps1'
    & $launcher -SaveDriveSettings -DriveFileId 'synthetic-test-copy' -Credentials $key
    Assert-True ((Read-DemoDriveSettings -SettingsPath $settingsPath).drive_file_id -eq 'synthetic-test-copy') 'Configuration-only launcher failed.'
    Assert-Rejected { & $launcher -SaveDriveSettings }
    Assert-Rejected { & $launcher -WithoutDrive -DriveFileId 'synthetic-test-copy' -Credentials $key }
    Assert-True ((Get-Location).Path -eq $originalLocation.Path) 'Launcher did not restore working directory.'
    foreach ($ignored in @('.demo.local.json', '.demo.local.json.test.tmp')) {
        & git -C $repo check-ignore --quiet --no-index $ignored
        Assert-True ($LASTEXITCODE -eq 0) 'Local settings or temporary file is not ignored by Git.'
    }
    Assert-True ((Get-Content -LiteralPath $key -Raw) -eq $marker) 'Original key file was changed.'
    Write-Host 'Demo settings and configuration-only launcher checks passed. No application was started.'
}
catch { Write-Host $_.ScriptStackTrace; throw }
finally {
    Set-Location $originalLocation
    if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture -Recurse -Force }
}
