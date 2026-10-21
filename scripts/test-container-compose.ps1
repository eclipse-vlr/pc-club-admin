# Real Compose parser with intercepted registry, HTTP and container mutations.
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$nativeDocker=(Get-Command docker -CommandType Application | Select-Object -First 1).Source
$fixture=Join-Path ([IO.Path]::GetTempPath()) ('pcclub real compose '+[guid]::NewGuid().ToString('N'))
$global:PCClubRealCompose=@{ Parsed=$false; Mutations=0 }
function docker {
    $global:LASTEXITCODE=0
    if ($args[2] -eq 'info') { return 'linux' }
    if ($args[2] -eq 'image' -and $args[3] -eq 'pull') { return 'intercepted registry download' }
    if ($args[2] -eq 'image' -and $args[3] -eq 'inspect') {
        return @{Os='linux';Architecture='amd64';Config=@{User='10001:10001';Labels=@{
            'org.opencontainers.image.source'='https://github.com/eclipse-vlr/pc-club-admin'
            'org.opencontainers.image.revision'=('a'*40)
        }}} | ConvertTo-Json -Depth 5 -Compress
    }
    if ($args[2] -eq 'ps' -or $args[2] -eq 'volume') { return '' }
    if ($args[2] -eq 'compose' -and ($args -contains 'version')) { return 'Docker Compose v2' }
    if ($args[2] -eq 'compose' -and ($args -contains 'config')) {
        $global:PCClubRealCompose.Parsed=$true
        & $nativeDocker @args
        return
    }
    if ($args[2] -eq 'compose' -and (($args -contains 'up') -or ($args -contains 'run') -or ($args -contains 'exec'))) {
        $global:PCClubRealCompose.Mutations++
        if (($args -join ' ').Contains('SELECT EXISTS')) { return 'f' }
        return 'intercepted database/container operation'
    }
    throw 'Unexpected Docker operation in parser-only integration.'
}
function Invoke-RestMethod {
    param([string]$Uri,[int]$TimeoutSec)
    if ($Uri.EndsWith('/readyz')) { return @{status='ready'} }
    return @{environment='test';cash_policy_status='unverified';excel_sync_enabled=[bool]$env:STAGING_EXCEL_FILE_ID}
}
function Start-Process { throw 'Browser must stay closed.' }
try {
    New-Item -ItemType Directory -Path (Join-Path $fixture 'scripts'),(Join-Path $fixture 'deploy') | Out-Null
    Copy-Item (Join-Path $PSScriptRoot 'start-container.ps1') (Join-Path $fixture 'scripts')
    Copy-Item (Join-Path $PSScriptRoot 'container-excel.ps1') (Join-Path $fixture 'scripts')
    Copy-Item (Join-Path (Split-Path -Parent $PSScriptRoot) 'deploy/compose.staging.yaml') (Join-Path $fixture 'deploy')
    Copy-Item (Join-Path (Split-Path -Parent $PSScriptRoot) 'deploy/compose.excel-test.yaml') (Join-Path $fixture 'deploy')
    & (Join-Path $fixture 'scripts/start-container.ps1') -Image ('ghcr.io/eclipse-vlr/pc-club-admin@sha256:'+('a'*64)) -NoBrowser
    if (-not $global:PCClubRealCompose.Parsed -or $global:PCClubRealCompose.Mutations -ne 5) { throw 'Real Compose parser or expected interception was skipped.' }
    $keyFile=Join-Path ([IO.Path]::GetTempPath()) ('synthetic parser key '+[guid]::NewGuid().ToString('N')+'.json')
    try {
        [IO.File]::WriteAllText($keyFile,'synthetic, not a Google key')
        & (Join-Path $fixture 'scripts/start-container.ps1') -NoBrowser -ExcelFileId 'synthetic-copy' -ExcelCredentialsFile $keyFile -ConfirmTestFileId 'synthetic-copy'
        if ($global:PCClubRealCompose.Mutations -ne 11) { throw 'Excel connection did not use actual Compose with intercepted access check.' }
    } finally { Remove-Item -LiteralPath $keyFile -Force -ErrorAction SilentlyContinue }
    Write-Host 'PASS: launcher accepted actual Docker Compose JSON; registry, HTTP and all five container/database operations were intercepted.'
} finally {
    Remove-Item -LiteralPath $fixture -Recurse -Force
    Remove-Variable PCClubRealCompose -Scope Global
}
