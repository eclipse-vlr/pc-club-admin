# Real local Git repositories only; no GitHub, application, Docker or keys.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$sourceRoot = Split-Path -Parent $PSScriptRoot
$root = Join-Path ([System.IO.Path]::GetTempPath()) ('pcclub-update-test-' + [guid]::NewGuid().ToString('N'))
$script:failFetch = $false
$utf8 = New-Object System.Text.UTF8Encoding($false)
function Assert-UpdateTest { param([bool]$Condition, [string]$Message); if (-not $Condition) { throw $Message } }
function Assert-UpdateRejected {
    param([scriptblock]$Work)
    $rejected = $false
    try { & $Work | Out-Null } catch { $rejected = $true }
    Assert-UpdateTest $rejected 'Expected the unsafe update to be rejected.'
}
function TestGit {
    param([string[]]$Arguments)
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $output = @(& git @Arguments 2>&1); $code = $LASTEXITCODE }
    finally { $ErrorActionPreference = $old }
    if ($code -ne 0) { throw "Fixture Git failed: $($output -join ' ')" }
    return (($output | ForEach-Object { $_.ToString() }) -join [char]10).Trim()
}
function Put-UpdateFile {
    param([string]$Path, [string]$Text)
    [System.IO.Directory]::CreateDirectory((Split-Path -Parent $Path)) | Out-Null
    [System.IO.File]::WriteAllText($Path, $Text, $utf8)
}
function New-UpdateFixture {
    $directory = Join-Path $root ([guid]::NewGuid().ToString('N') + ' space')
    $server = Join-Path $directory 'server.git'
    $source = Join-Path $directory 'source'
    $client = Join-Path $directory 'client space'
    $backup = Join-Path $directory 'backups'
    New-Item -ItemType Directory -Path $directory | Out-Null
    TestGit -Arguments @('init', '--bare', '--initial-branch=main', $server) | Out-Null
    TestGit -Arguments @('init', '--initial-branch=main', $source) | Out-Null
    foreach ($pair in @(@('user.name', 'Synthetic'), @('user.email', 'synthetic@example.invalid'), @('commit.gpgsign', 'false'), @('core.autocrlf', 'false'))) {
        TestGit -Arguments @('-C', $source, 'config', $pair[0], $pair[1]) | Out-Null
    }
    Put-UpdateFile (Join-Path $source '.gitignore') (".env" + [char]10 + ".demo.local.json" + [char]10 + "web/node_modules/" + [char]10 + "web/dist/" + [char]10)
    Put-UpdateFile (Join-Path $source 'code.txt') ('base' + [char]13 + [char]10)
    [System.IO.File]::WriteAllBytes((Join-Path $source 'binary.dat'), [byte[]]@(0, 1, 2, 255))
    TestGit -Arguments @('-C', $source, 'add', '.') | Out-Null
    TestGit -Arguments @('-C', $source, 'commit', '-m', 'Synthetic base') | Out-Null
    $before = TestGit -Arguments @('-C', $source, 'rev-parse', 'HEAD')
    TestGit -Arguments @('-C', $source, 'remote', 'add', 'origin', $server) | Out-Null
    TestGit -Arguments @('-C', $source, 'push', 'origin', 'main') | Out-Null
    TestGit -Arguments @('-c', 'core.autocrlf=false', 'clone', $server, $client) | Out-Null
    TestGit -Arguments @('-C', $client, 'remote', 'set-url', 'origin', 'https://github.com/eclipse-vlr/pc-club-admin.git') | Out-Null
    foreach ($pair in @(@('user.name', 'Synthetic'), @('user.email', 'synthetic@example.invalid'), @('commit.gpgsign', 'false'), @('core.autocrlf', 'false'))) {
        TestGit -Arguments @('-C', $client, 'config', $pair[0], $pair[1]) | Out-Null
    }
    Put-UpdateFile (Join-Path $source 'code.txt') ('updated' + [char]13 + [char]10)
    Put-UpdateFile (Join-Path $source 'new.txt') 'new tracked file'
    TestGit -Arguments @('-C', $source, 'add', '.') | Out-Null
    TestGit -Arguments @('-C', $source, 'commit', '-m', 'Synthetic update') | Out-Null
    TestGit -Arguments @('-C', $source, 'push', 'origin', 'main') | Out-Null
    $target = TestGit -Arguments @('-C', $source, 'rev-parse', 'HEAD')
    $key = Join-Path $directory 'outside key.json'
    Put-UpdateFile $key 'synthetic key marker, not a credential'
    Put-UpdateFile (Join-Path $client '.env') 'PIN_PEPPER=synthetic-original-pepper-do-not-replace'
    Put-UpdateFile (Join-Path $client '.demo.local.json') (@{ format = 1; drive_file_id = 'synthetic-test-copy'; credentials_file = $key } | ConvertTo-Json)
    Put-UpdateFile (Join-Path $client 'web/node_modules/marker') 'installed dependencies'
    Put-UpdateFile (Join-Path $client 'web/dist/marker') 'compiled UI'
    $script:testRemote = $server
    return [pscustomobject]@{ Directory = $directory; Server = $server; Source = $source; Client = $client; Backup = $backup; Before = $before; Target = $target; Key = $key }
}
function Get-UpdateTestHead { param($Fixture); return TestGit -Arguments @('-C', $Fixture.Client, 'rev-parse', 'HEAD') }
function Check-UpdateFixturePreserved {
    param($Fixture)
    Assert-UpdateTest ((Get-Content -LiteralPath (Join-Path $Fixture.Client '.env') -Raw) -eq 'PIN_PEPPER=synthetic-original-pepper-do-not-replace') 'Original environment was replaced.'
    Assert-UpdateTest ((Get-Content -LiteralPath $Fixture.Key -Raw) -eq 'synthetic key marker, not a credential') 'External key marker was changed.'
    Assert-UpdateTest (Test-Path -LiteralPath (Join-Path $Fixture.Client 'web/node_modules/marker')) 'Ignored dependencies were removed.'
    Assert-UpdateTest (Test-Path -LiteralPath (Join-Path $Fixture.Client 'web/dist/marker')) 'Ignored build was removed.'
    Assert-UpdateTest (-not (Test-Path -LiteralPath (Join-Path $Fixture.Client '.git/pcclub-update.lock'))) 'Updater lock was not released.'
}
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $updater = Join-Path $PSScriptRoot 'update-demo.ps1'
    $tokens = $null; $parseErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile($updater, [ref]$tokens, [ref]$parseErrors) | Out-Null
    Assert-UpdateTest ($parseErrors.Count -eq 0) 'Updater does not parse.'
    . $updater
    function Fetch-UpdateTarget {
        if ($script:failFetch) { throw 'Synthetic fetch failure.' }
        Invoke-UpdateGit -Arguments @('fetch', '--no-tags', '--no-recurse-submodules', $script:testRemote,
            '+refs/heads/main:refs/remotes/origin/main') -Label 'Synthetic local fetch' | Out-Null
    }

    # Default preview changes no working files, HEAD or backups.
    $fixture = New-UpdateFixture
    $settingsHash = (Get-FileHash -LiteralPath (Join-Path $fixture.Client '.demo.local.json')).Hash
    $plan = Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup
    Assert-UpdateTest ($plan.Mode -eq 'fast_forward' -and -not $plan.Applied) 'Wrong preview mode.'
    Assert-UpdateTest ((Get-UpdateTestHead $fixture) -eq $fixture.Before) 'Preview moved HEAD.'
    Assert-UpdateTest (-not (Test-Path -LiteralPath $fixture.Backup)) 'Preview created a backup.'
    $plan = Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup -Apply
    Assert-UpdateTest ($plan.Applied -and (Get-UpdateTestHead $fixture) -eq $fixture.Target) 'Fast-forward update failed.'
    Assert-UpdateTest ((TestGit -Arguments @('-C', $fixture.Client, 'rev-parse', $plan.BackupRef)) -eq $fixture.Before) 'Backup ref does not preserve the old HEAD.'
    TestGit -Arguments @('-C', $fixture.Client, 'bundle', 'verify', (Join-Path $plan.BackupPath 'history.bundle')) | Out-Null
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        $acl = Get-Acl -LiteralPath $plan.BackupPath
        Assert-UpdateTest ($acl.AreAccessRulesProtected -and @($acl.Access).Count -eq 1) 'Backup directory ACL is not private.'
    }
    Assert-UpdateTest ((Get-FileHash -LiteralPath (Join-Path $fixture.Client '.demo.local.json')).Hash -eq $settingsHash) 'Saved Drive settings changed.'
    Check-UpdateFixturePreserved $fixture
    $again = Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup -Apply
    Assert-UpdateTest ($again.Mode -eq 'up_to_date' -and -not $again.Applied) 'Repeated update should be a no-op.'
    Write-Host 'PASS: preview, fast-forward, preserved settings and repeat.'

    # A new root with the exact old code tree models author/message rewrites.
    $fixture = New-UpdateFixture
    $baseTree = TestGit -Arguments @('-C', $fixture.Source, 'rev-parse', ($fixture.Before + '^{tree}'))
    $tipTree = TestGit -Arguments @('-C', $fixture.Source, 'rev-parse', 'HEAD^{tree}')
    $newRoot = TestGit -Arguments @('-C', $fixture.Source, 'commit-tree', $baseTree, '-m', 'New root identity')
    $newTip = TestGit -Arguments @('-C', $fixture.Source, 'commit-tree', $tipTree, '-p', $newRoot, '-m', 'New update identity')
    TestGit -Arguments @('-C', $fixture.Source, 'push', '--force', $fixture.Server, ($newTip + ':refs/heads/main')) | Out-Null
    $plan = Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup -Apply
    Assert-UpdateTest ($plan.Mode -eq 'history_rewrite' -and $plan.MatchingCommit -eq $newRoot -and (Get-UpdateTestHead $fixture) -eq $newTip) 'Rewritten history was not reconciled by the exact tree.'
    Check-UpdateFixturePreserved $fixture
    Write-Host 'PASS: unrelated roots with identical old code.'

    # Staged/unstaged binary and CRLF changes are backed up without text conversion.
    $fixture = New-UpdateFixture
    Put-UpdateFile (Join-Path $fixture.Client 'code.txt') ('staged' + [char]13 + [char]10)
    [System.IO.File]::WriteAllBytes((Join-Path $fixture.Client 'binary.dat'), [byte[]]@(0, 10, 13, 254))
    TestGit -Arguments @('-C', $fixture.Client, 'add', 'code.txt', 'binary.dat') | Out-Null
    $indexTree = TestGit -Arguments @('-C', $fixture.Client, 'write-tree')
    Put-UpdateFile (Join-Path $fixture.Client 'code.txt') ('working' + [char]13 + [char]10)
    [System.IO.File]::WriteAllBytes((Join-Path $fixture.Client 'binary.dat'), [byte[]]@(0, 20, 13, 253))
    $unicodeName = 'draft ' + [char]0x044F + " 'quote'.txt"
    Put-UpdateFile (Join-Path $fixture.Client $unicodeName) 'untracked work'
    $workingHash = (Get-FileHash -LiteralPath (Join-Path $fixture.Client 'binary.dat')).Hash
    Assert-UpdateRejected { Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup -Apply }
    Assert-UpdateTest ((Get-UpdateTestHead $fixture) -eq $fixture.Before) 'Dirty update moved HEAD.'
    Assert-UpdateTest ((Get-FileHash -LiteralPath (Join-Path $fixture.Client 'binary.dat')).Hash -eq $workingHash) 'Working binary was replaced.'
    $backup = @(Get-ChildItem -LiteralPath $fixture.Backup -Directory)[0].FullName
    $restore = Join-Path $fixture.Directory 'restore'
    TestGit -Arguments @('-c', 'core.autocrlf=false', 'clone', (Join-Path $backup 'history.bundle'), $restore) | Out-Null
    TestGit -Arguments @('-C', $restore, 'config', 'core.autocrlf', 'false') | Out-Null
    TestGit -Arguments @('-C', $restore, 'apply', '--index', '--binary', (Join-Path $backup 'staged.patch')) | Out-Null
    TestGit -Arguments @('-C', $restore, 'apply', '--binary', (Join-Path $backup 'unstaged.patch')) | Out-Null
    Assert-UpdateTest ((TestGit -Arguments @('-C', $restore, 'write-tree')) -eq $indexTree) 'Restored index differs.'
    Assert-UpdateTest ((Get-FileHash -LiteralPath (Join-Path $restore 'binary.dat')).Hash -eq $workingHash) 'Binary patch is not byte-exact.'
    Assert-UpdateTest ((Get-FileHash -LiteralPath (Join-Path $restore 'code.txt')).Hash -eq (Get-FileHash -LiteralPath (Join-Path $fixture.Client 'code.txt')).Hash) 'CRLF patch changed bytes.'
    Assert-UpdateTest ((Get-Content -LiteralPath (Join-Path (Join-Path $backup 'untracked') $unicodeName) -Raw) -eq 'untracked work') 'Unicode untracked filename was not preserved.'
    Assert-UpdateTest (-not (Test-Path -LiteralPath (Join-Path $backup '.env'))) 'Ignored environment was copied.'
    Check-UpdateFixturePreserved $fixture
    Write-Host 'PASS: dirty refusal and actual restoration of binary, CRLF and untracked work.'

    $fixture = New-UpdateFixture
    Put-UpdateFile (Join-Path $fixture.Client 'local.txt') 'unpublished local commit'
    TestGit -Arguments @('-C', $fixture.Client, 'add', 'local.txt') | Out-Null
    TestGit -Arguments @('-C', $fixture.Client, 'commit', '-m', 'Local work') | Out-Null
    $local = Get-UpdateTestHead $fixture
    $plan = Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup
    Assert-UpdateTest ($plan.Mode -eq 'local_commits') 'Unknown local commits were not detected.'
    Assert-UpdateRejected { Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup -Apply }
    Assert-UpdateTest ((Get-UpdateTestHead $fixture) -eq $local) 'Unpublished commit was discarded.'
    Check-UpdateFixturePreserved $fixture
    Write-Host 'PASS: unpublished local commits are backed up and kept.'

    foreach ($scenario in @('wrong-origin', 'detached', 'index-lock', 'own-lock', 'merge', 'assume-unchanged', 'skip-worktree', 'inside-backup', 'relative-backup', 'backup-file', 'fetch-failure', 'ignored-collision', 'remote-env', 'remote-link')) {
        $fixture = New-UpdateFixture
        $backupDirectory = $fixture.Backup
        switch ($scenario) {
            'wrong-origin' { TestGit -Arguments @('-C', $fixture.Client, 'remote', 'set-url', 'origin', 'https://example.invalid/other.git') | Out-Null }
            'detached' { TestGit -Arguments @('-C', $fixture.Client, 'checkout', '--detach') | Out-Null }
            'index-lock' { Put-UpdateFile (Join-Path $fixture.Client '.git/index.lock') 'other process lock' }
            'own-lock' { Put-UpdateFile (Join-Path $fixture.Client '.git/pcclub-update.lock') 'existing updater lock' }
            'merge' { Put-UpdateFile (Join-Path $fixture.Client '.git/MERGE_HEAD') $fixture.Before }
            'assume-unchanged' { TestGit -Arguments @('-C', $fixture.Client, 'update-index', '--assume-unchanged', 'code.txt') | Out-Null }
            'skip-worktree' { TestGit -Arguments @('-C', $fixture.Client, 'update-index', '--skip-worktree', 'code.txt') | Out-Null }
            'inside-backup' { $backupDirectory = Join-Path $fixture.Client 'inside' }
            'relative-backup' { $backupDirectory = 'relative-backup' }
            'backup-file' { Put-UpdateFile $fixture.Backup 'a file, not a directory' }
            'fetch-failure' { $script:failFetch = $true }
            'ignored-collision' {
                Put-UpdateFile (Join-Path $fixture.Client '.git/info/exclude') 'new.txt'
                Put-UpdateFile (Join-Path $fixture.Client 'new.txt') 'ignored local file'
            }
            'remote-env' {
                Put-UpdateFile (Join-Path $fixture.Source '.env') 'accidentally tracked configuration'
                TestGit -Arguments @('-C', $fixture.Source, 'add', '-f', '.env') | Out-Null
                TestGit -Arguments @('-C', $fixture.Source, 'commit', '-m', 'Unsafe target') | Out-Null
                TestGit -Arguments @('-C', $fixture.Source, 'push', 'origin', 'main') | Out-Null
            }
            'remote-link' {
                $blob = TestGit -Arguments @('-C', $fixture.Source, 'rev-parse', 'HEAD:code.txt')
                TestGit -Arguments @('-C', $fixture.Source, 'update-index', '--add', '--cacheinfo', ("120000," + $blob + ",link")) | Out-Null
                TestGit -Arguments @('-C', $fixture.Source, 'commit', '-m', 'Unsupported link') | Out-Null
                TestGit -Arguments @('-C', $fixture.Source, 'push', 'origin', 'main') | Out-Null
            }
        }
        Assert-UpdateRejected { Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $backupDirectory -Apply }
        $script:failFetch = $false
        Assert-UpdateTest ((Get-UpdateTestHead $fixture) -eq $fixture.Before) "Unsafe scenario moved HEAD: $scenario"
        if ($scenario -eq 'own-lock') {
            Assert-UpdateTest ((Get-Content -LiteralPath (Join-Path $fixture.Client '.git/pcclub-update.lock') -Raw) -eq 'existing updater lock') 'Another updater lock was removed.'
        } else { Check-UpdateFixturePreserved $fixture }
        if ($scenario -eq 'index-lock') {
            Assert-UpdateTest ((Get-Content -LiteralPath (Join-Path $fixture.Client '.git/index.lock') -Raw) -eq 'other process lock') 'Another Git lock was removed.'
        }
        Write-Host "PASS: rejected $scenario."
    }
    # A simultaneous checkout can keep HEAD unchanged while changing the branch.
    $fixture = New-UpdateFixture
    $script:realBackup = (Get-Command Save-UpdateBackup).ScriptBlock
    function Save-UpdateBackup {
        param($Plan, [string]$Directory, [string[]]$Untracked)
        & $script:realBackup -Plan $Plan -Directory $Directory -Untracked $Untracked
        TestGit -Arguments @('-C', $script:updateRepo, 'checkout', '-b', 'concurrent-feature') | Out-Null
    }
    Assert-UpdateRejected { Invoke-PCClubUpdate -RepositoryPath $fixture.Client -BackupDirectory $fixture.Backup -Apply }
    Set-Item Function:Save-UpdateBackup -Value $script:realBackup
    Assert-UpdateTest ((Get-UpdateTestHead $fixture) -eq $fixture.Before) 'Concurrent branch checkout was overwritten.'
    Assert-UpdateTest ((TestGit -Arguments @('-C', $fixture.Client, 'rev-parse', 'main')) -eq $fixture.Before) 'Main was moved after a concurrent checkout.'
    Check-UpdateFixturePreserved $fixture
    Write-Host 'PASS: a concurrent checkout at the same commit aborts the update.'
    # The downloaded standalone entrypoint accepts an explicit repository path.
    $standalone = Join-Path $root 'standalone updater.ps1'
    Copy-Item -LiteralPath $updater -Destination $standalone
    $fixture = New-UpdateFixture
    TestGit -Arguments @('-C', $fixture.Client, 'checkout', '--detach') | Out-Null
    Assert-UpdateRejected { & $standalone -RepositoryPath $fixture.Client -Apply }
    Check-UpdateFixturePreserved $fixture
    Write-Host 'Update checks passed. All remotes were local synthetic repositories; no application was started.'
}
catch { Write-Host $_.ScriptStackTrace; throw }
finally {
    if (Test-Path -LiteralPath $root) { Remove-Item -LiteralPath $root -Recurse -Force }
}
