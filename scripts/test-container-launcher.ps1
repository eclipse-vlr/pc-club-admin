# Real file/ACL checks with intercepted Docker/HTTP; no application, engine or registry.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('pcclub container test ' + [guid]::NewGuid().ToString('N'))
$launcher = Join-Path $fixture 'scripts/start-container.ps1'
$file = Join-Path $fixture 'deploy/.env.staging'
$image = 'ghcr.io/eclipse-vlr/pc-club-admin@sha256:' + ('a' * 64)
$previousExit = Get-Variable -Name LASTEXITCODE -Scope Global -ErrorAction SilentlyContinue
$previousExitValue = if ($null -ne $previousExit) { $previousExit.Value } else { 0 }
$global:PCClubContainerFixture = @{ Mode = ''; Calls = @(); ExpectedHost = ''; HasUsers = $false }
function Assert-Container { param([bool]$Condition, [string]$Message); if (-not $Condition) { throw $Message } }
function Invoke-Rejected {
    param([scriptblock]$Work)
    $message = ''
    try { & $Work | Out-Null } catch { $message = $_.Exception.Message }
    Assert-Container ([bool]$message) 'Expected launcher refusal.'
    Assert-Container (-not $message.Contains('synthetic-provider-secret')) 'Provider details escaped.'
}
function docker {
    $state = $global:PCClubContainerFixture
    $global:LASTEXITCODE = 0
    Assert-Container ($args[0] -eq '--host' -and $args[1] -eq $state.ExpectedHost) 'A remote Docker selector was used.'
    Assert-Container (-not $env:DOCKER_CONTEXT -and -not $env:DOCKER_TLS_VERIFY) 'Caller Docker selectors leaked.'
    $command = (@($args)[2..($args.Count - 1)]) -join ' '
    $state.Calls += $command
    if ($command -eq 'info --format {{.OSType}}') {
        if ($state.Mode -eq 'engine') { $global:LASTEXITCODE = 1; return 'synthetic-provider-secret' }
        return 'linux'
    }
    if ($command -eq 'compose version') { return 'Docker Compose version v2.40.0' }
    if ($command.StartsWith('ps -a ') -or $command.StartsWith('volume ls ')) {
        if ($state.Mode -eq 'existing-data') { return 'synthetic-existing-volume' }
        return ''
    }
    if ($command.StartsWith('image pull ')) {
        if ($state.Mode -eq 'pull') { $global:LASTEXITCODE = 1; return 'synthetic-provider-secret' }
        return 'downloaded'
    }
    if ($command.StartsWith('image inspect ')) {
        $source = if ($state.Mode -eq 'wrong-image') { 'https://example.invalid/other' } else { 'https://github.com/eclipse-vlr/pc-club-admin' }
        return @{ Os='linux'; Architecture='amd64'; Config=@{ User='10001:10001'; Labels=@{'org.opencontainers.image.source'=$source; 'org.opencontainers.image.revision'=('b'*40)}}} | ConvertTo-Json -Depth 5 -Compress
    }
    Assert-Container ($command.StartsWith('compose --project-name pc-club-staging --env-file ')) 'Unexpected Docker mutation.'
    Assert-Container ($env:STAGING_IMAGE -eq $image) 'Caller image overrode saved settings.'
    if ($command.EndsWith('config --format json')) {
        $app = @{ image=$image; environment=@{
            APP_ENV='test'; CASH_POLICY_STATUS='unverified'; DRIVE_EXCEL_SYNC='disabled'
            PIN_PEPPER=$env:STAGING_PIN_PEPPER
            DATABASE_URL=('postgres://pcclub_staging:'+$env:STAGING_DB_PASSWORD+'@db/pcclub_staging?sslmode=disable')
        }; ports=@(@{host_ip='127.0.0.1';published='18080'}) }
        if ($state.Mode -eq 'unsafe-compose') { $app.environment.DATABASE_URL='postgres://example.invalid/real' }
        return @{ name='pc-club-staging';services=@{
            api=$app;migrate=$app;admin=$app
            db=@{environment=@{POSTGRES_USER='pcclub_staging';POSTGRES_DB='pcclub_staging';POSTGRES_PASSWORD=$env:STAGING_DB_PASSWORD};volumes=@(@{source='staging_postgres_data'})}
        };volumes=@{staging_postgres_data=@{name='pc-club-staging_staging_postgres_data'}} } | ConvertTo-Json -Depth 8 -Compress
    }
    if ($state.Mode -eq 'migration' -and $command.EndsWith(' migrate')) { $global:LASTEXITCODE=1; return 'synthetic-provider-secret' }
    if ($command.Contains('SELECT EXISTS')) {
        if ($state.Mode -eq 'database-status') { return 'unexpected' }
        if ($state.HasUsers) { return 't' } else { return 'f' }
    }
    if ($state.Mode -eq 'bootstrap' -and $command.Contains('bootstrap-manager')) { $global:LASTEXITCODE=1; return }
    if ($command.Contains('bootstrap-manager')) { $state.HasUsers=$true }
    return 'done'
}
function Invoke-RestMethod {
    param([string]$Uri,[int]$TimeoutSec)
    Assert-Container ($Uri.StartsWith('http://127.0.0.1:18080/')) 'Non-local HTTP request.'
    if ($Uri.EndsWith('/readyz')) { return @{status='ready'} }
    return @{environment='test';cash_policy_status='unverified';excel_sync_enabled=$false}
}
function Start-Process { throw 'Tests must not open a browser.' }
function Read-Host { throw 'The packaged release must not ask the operator to choose a digest.' }
$saved = @{}
foreach ($key in @('DOCKER_HOST','DOCKER_CONTEXT','DOCKER_TLS_VERIFY','STAGING_IMAGE')) {
    $saved[$key]=[Environment]::GetEnvironmentVariable($key,'Process')
}
$originalLocation = (Get-Location).Path
try {
    New-Item -ItemType Directory -Path (Join-Path $fixture 'scripts'),(Join-Path $fixture 'deploy') | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'start-container.ps1') -Destination $launcher
    [IO.File]::WriteAllText((Join-Path $fixture 'deploy/compose.staging.yaml'), 'synthetic intercepted fixture')
    [IO.File]::WriteAllText((Join-Path $fixture '.env'), 'synthetic-existing-development-settings')
    [IO.File]::WriteAllText((Join-Path $fixture '.demo.local.json'), 'synthetic-existing-drive-settings')
    [IO.File]::WriteAllText((Join-Path $fixture 'release-image.txt'), $image)
    $global:PCClubContainerFixture.ExpectedHost = if ($env:OS -eq 'Windows_NT') { 'npipe:////./pipe/dockerDesktopLinuxEngine' } else { 'unix:///var/run/docker.sock' }
    $env:DOCKER_HOST='tcp://example.invalid:2376'; $env:DOCKER_CONTEXT='remote';$env:DOCKER_TLS_VERIFY='1';$env:STAGING_IMAGE='untrusted:latest'
    foreach ($invalid in @('ghcr.io/eclipse-vlr/pc-club-admin:latest', 'ghcr.io/other/project@sha256:' + ('a'*64), $image.ToUpperInvariant())) {
        Invoke-Rejected { & $launcher -Image $invalid -NoBrowser }
    }
    Assert-Container ($global:PCClubContainerFixture.Calls.Count -eq 0) 'Invalid image reached Docker.'
    foreach ($mode in @('engine','existing-data','pull','wrong-image')) {
        $global:PCClubContainerFixture.Mode=$mode;$global:PCClubContainerFixture.Calls=@()
        Invoke-Rejected { & $launcher -Image $image -NoBrowser }
        Assert-Container (-not (Test-Path -LiteralPath $file)) 'Preflight failure created new secrets.'
        Assert-Container (-not ($global:PCClubContainerFixture.Calls -match 'run --rm|up -d')) 'Preflight failure reached the database.'
    }
    $global:PCClubContainerFixture.Mode='';$global:PCClubContainerFixture.Calls=@()
    $output = (& $launcher -NoBrowser *>&1 | Out-String)
    Assert-Container (Test-Path -LiteralPath $file) 'First launch did not create settings.'
    $original = [IO.File]::ReadAllBytes($file)
    $text = [IO.File]::ReadAllText($file)
    $values=@{}
    foreach ($line in $text.Split([char]10)) { if ($line -match '^([A-Z_]+)=(.+)$') { $values[$matches[1]]=$matches[2].TrimEnd([char]13) } }
    Assert-Container ($values.STAGING_DB_PASSWORD -match '^[0-9a-f]{64}$' -and $values.STAGING_PIN_PEPPER -match '^[0-9a-f]{64}$') 'Weak or absent generated settings.'
    Assert-Container ($values.STAGING_DB_PASSWORD -ne $values.STAGING_PIN_PEPPER) 'Secrets were reused.'
    Assert-Container (-not $output.Contains($values.STAGING_PIN_PEPPER) -and -not $output.Contains($values.STAGING_DB_PASSWORD)) 'Secrets were printed.'
    Assert-Container (@($global:PCClubContainerFixture.Calls -match 'bootstrap-manager').Count -eq 1) 'First manager was not initialized once.'
    if ($env:OS -eq 'Windows_NT') {
        $acl=Get-Acl -LiteralPath $file
        Assert-Container $acl.AreAccessRulesProtected 'Secret file still inherits access.'
        $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
        foreach ($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
            Assert-Container ($rule.IdentityReference.Value -eq $sid) 'Secret file grants another identity access.'
        }
    } else {
        Assert-Container ([int][IO.File]::GetUnixFileMode($file) -eq 384) 'Secret file is not mode 0600.'
    }
    $global:PCClubContainerFixture.Calls=@()
    & $launcher -NoBrowser | Out-Null
    Assert-Container (-not ($global:PCClubContainerFixture.Calls -match 'bootstrap-manager')) 'Restart repeated manager setup.'
    Assert-Container ([Convert]::ToBase64String([IO.File]::ReadAllBytes($file)) -eq [Convert]::ToBase64String($original)) 'Restart changed saved settings.'
    Invoke-Rejected { & $launcher -Image ($image.Replace(('a'*64),('c'*64))) -NoBrowser }
    foreach ($bad in @(
        ($text + [Environment]::NewLine + 'STAGING_HTTP_PORT=18081'),
        ($text + [Environment]::NewLine + 'UNKNOWN_SETTING=invalid'),
        $text.Replace($image,'pc-club-admin:old-local-tag'),
        $text.Replace('STAGING_HTTP_PORT=18080','STAGING_HTTP_PORT=0'),
        $text.Replace('STAGING_PIN_PEPPER=','MISSING_PIN_PEPPER=')
    )) {
        [IO.File]::WriteAllText($file,$bad)
        $global:PCClubContainerFixture.Calls=@()
        Invoke-Rejected { & $launcher -NoBrowser }
        Assert-Container ($global:PCClubContainerFixture.Calls.Count -eq 0) 'Invalid saved settings reached Docker.'
        Assert-Container ([IO.File]::ReadAllText($file) -eq $bad) 'Rejected settings were rewritten.'
    }
    [IO.File]::WriteAllBytes($file,$original)
    foreach ($mode in @('unsafe-compose','migration','database-status','bootstrap')) {
        $global:PCClubContainerFixture.Mode=$mode;$global:PCClubContainerFixture.Calls=@()
        if ($mode -eq 'bootstrap') { $global:PCClubContainerFixture.HasUsers=$false }
        Invoke-Rejected { & $launcher -BootstrapManager -NoBrowser }
        Assert-Container (-not ($global:PCClubContainerFixture.Calls -match 'up -d .* api$')) 'Failure started API.'
        Assert-Container ([IO.File]::ReadAllText($file) -eq $text) 'Failure replaced original settings.'
    }
    $global:PCClubContainerFixture.Mode='';$global:PCClubContainerFixture.Calls=@()
    & $launcher -NoBrowser | Out-Null
    Assert-Container (@($global:PCClubContainerFixture.Calls -match 'bootstrap-manager').Count -eq 1 -and $global:PCClubContainerFixture.HasUsers) 'Incomplete setup was not resumed from authoritative database state.'
    Assert-Container ([IO.File]::ReadAllText($file) -eq $text) 'Resumed setup replaced original settings.'
    $held=[IO.File]::Open((Join-Path $fixture 'deploy/.container-start.lock'),[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    try { Invoke-Rejected { & $launcher -NoBrowser } } finally { $held.Dispose() }
    Assert-Container ($env:DOCKER_HOST -eq 'tcp://example.invalid:2376' -and $env:DOCKER_CONTEXT -eq 'remote' -and $env:DOCKER_TLS_VERIFY -eq '1' -and $env:STAGING_IMAGE -eq 'untrusted:latest') 'Caller environment was not restored.'
    Assert-Container ((Get-Location).Path -eq $originalLocation) 'Caller directory was not restored.'
    Assert-Container ([IO.File]::ReadAllText((Join-Path $fixture '.env')) -eq 'synthetic-existing-development-settings') 'Development settings changed.'
    Assert-Container ([IO.File]::ReadAllText((Join-Path $fixture '.demo.local.json')) -eq 'synthetic-existing-drive-settings') 'Drive settings changed.'
    Write-Host 'Container launcher checks passed: first setup/restart, private secrets, preserved settings, local engine, pinned image, unsafe config and failure refusals.'
} finally {
    $global:LASTEXITCODE = $previousExitValue
    foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key,$saved[$key],'Process') }
    Remove-Variable PCClubContainerFixture -Scope Global
    Remove-Item -LiteralPath $fixture -Recurse -Force -ErrorAction SilentlyContinue
}
