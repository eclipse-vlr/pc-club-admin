# Standalone updater: preview by default; never starts the application or database.
[CmdletBinding()]
param(
    [string]$RepositoryPath = '',
    [string]$BackupDirectory = '',
    [switch]$Apply
)

function ConvertTo-UpdateArgument {
    param([string]$Argument)
    # Windows PowerShell 5.1 has no ProcessStartInfo.ArgumentList.
    $escaped = [regex]::Replace($Argument, '(\\*)"', '$1$1\"')
    $escaped = [regex]::Replace($escaped, '(\\+)$', '$1$1')
    return '"' + $escaped + '"'
}

function Invoke-UpdateGit {
    param([string[]]$Arguments, [string]$Label = 'Git operation', [string]$OutputPath = '', [switch]$AllowFailure)
    $info = New-Object System.Diagnostics.ProcessStartInfo
    $info.FileName = $script:updateGit
    $info.WorkingDirectory = $script:updateRepo
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.StandardOutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $info.StandardErrorEncoding = New-Object System.Text.UTF8Encoding($false)
    $info.EnvironmentVariables['GIT_TERMINAL_PROMPT'] = '0'
    $info.EnvironmentVariables['GCM_INTERACTIVE'] = 'never'
    $info.EnvironmentVariables['GIT_OPTIONAL_LOCKS'] = '0'
    $all = @('-c', 'core.quotepath=false', '-c', 'gc.auto=0') + $Arguments
    if ($info.PSObject.Properties['ArgumentList']) {
        foreach ($argument in $all) { $info.ArgumentList.Add($argument) }
    } else {
        $info.Arguments = (($all | ForEach-Object { ConvertTo-UpdateArgument $_ }) -join ' ')
    }
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $info
    $stream = $null
    $started = $false
    try {
        if ($OutputPath) { $stream = [System.IO.File]::Open($OutputPath, 'CreateNew', 'Write', 'None') }
        $started = $process.Start()
        $errorTask = $process.StandardError.ReadToEndAsync()
        if ($stream) { $outputTask = $process.StandardOutput.BaseStream.CopyToAsync($stream) }
        else { $outputTask = $process.StandardOutput.ReadToEndAsync() }
        if (-not $process.WaitForExit(60000)) { throw "$Label timed out. No automatic retry was made." }
        if (-not $outputTask.Wait(5000) -or -not $errorTask.Wait(5000)) { throw "$Label output did not complete." }
        $code = $process.ExitCode
        if ($code -ne 0 -and -not $AllowFailure) {
            # Never print native stderr: it may contain URLs with credentials.
            throw "$Label failed (exit $code). Check Git access and retry the preview."
        }
        $text = ''
        if ($stream) { $stream.Flush($true) } else { $text = $outputTask.Result }
        return [pscustomobject]@{ Code = $code; Text = $text }
    }
    finally {
        if ($started -and -not $process.HasExited) {
            if ($PSVersionTable.PSVersion.Major -ge 7) { $process.Kill($true) } else { $process.Kill() }
        }
        if ($stream) { $stream.Dispose() }
        $process.Dispose()
    }
}

function Test-UpdateProtectedPath {
    param([string]$Path)
    $leaf = ($Path -split '/')[-1]
    return (($leaf -ne '.env.example' -and $leaf -match '^\.env($|\.)') -or
        $leaf -match '^\.demo\.local\.json' -or $leaf -match '\.service-account\.json$' -or
        $Path -match '^(secrets|reference|backups|bin|web/node_modules|web/dist)(/|$)')
}

function Assert-UpdatePath {
    param([string]$Path)
    if (-not $Path -or $Path -match '(^/|\\|:|[\x00-\x1f])' -or
        @($Path -split '/' | Where-Object { $_ -in @('', '.', '..', '.git') }).Count -gt 0 -or
        (Test-UpdateProtectedPath $Path)) { throw 'A protected or unsupported repository path requires manual review.' }
    $current = $script:updateRepo
    foreach ($part in ($Path -split '/')) {
        $current = Join-Path $current $part
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
                throw 'A link or junction in the working tree requires manual review.'
            }
        }
    }
}

function Get-UpdatePaths {
    param([string[]]$Arguments)
    $result = Invoke-UpdateGit -Arguments $Arguments
    foreach ($path in ($result.Text -split [char]0)) {
        if ($path) { Assert-UpdatePath $path; $path }
    }
}

function Fetch-UpdateTarget {
    # Kept separate so isolated tests replace only the remote transport.
    Invoke-UpdateGit -Arguments @('fetch', '--no-tags', '--no-recurse-submodules', 'origin',
        '+refs/heads/main:refs/remotes/origin/main') -Label 'Fetch origin/main' | Out-Null
}

function Protect-UpdateBackup {
    param([string]$Directory)
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl = New-Object System.Security.AccessControl.DirectorySecurity
        $acl.SetOwner($identity)
        $acl.SetAccessRuleProtection($true, $false)
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($identity, 'FullControl',
            'ContainerInherit, ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
        Set-Acl -LiteralPath $Directory -AclObject $acl
    } else {
        & chmod 700 $Directory
        if ($LASTEXITCODE -ne 0) { throw 'Could not restrict backup directory access.' }
    }
}

function Save-UpdateBackup {
    param($Plan, [string]$Directory, [string[]]$Untracked)
    $name = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff') + '-' + [guid]::NewGuid().ToString('N')
    $destination = Join-Path $Directory $name
    New-Item -ItemType Directory -Path $destination | Out-Null
    Protect-UpdateBackup $destination
    $bundle = Join-Path $destination 'history.bundle'
    Invoke-UpdateGit -Arguments @('bundle', 'create', $bundle, '--all') -Label 'History backup' | Out-Null
    Invoke-UpdateGit -Arguments @('bundle', 'verify', $bundle) -Label 'History backup verification' | Out-Null
    Invoke-UpdateGit -Arguments @('diff', '--cached', '--binary', '--no-ext-diff', '--no-textconv', 'HEAD', '--') -OutputPath (Join-Path $destination 'staged.patch') -Label 'Staged changes backup' | Out-Null
    Invoke-UpdateGit -Arguments @('diff', '--binary', '--no-ext-diff', '--no-textconv', '--') -OutputPath (Join-Path $destination 'unstaged.patch') -Label 'Working changes backup' | Out-Null
    $files = @()
    foreach ($path in $Untracked) {
        Assert-UpdatePath $path
        $source = Join-Path $script:updateRepo $path
        $copy = Join-Path (Join-Path $destination 'untracked') $path
        [System.IO.Directory]::CreateDirectory((Split-Path -Parent $copy)) | Out-Null
        [System.IO.File]::Copy($source, $copy, $false)
        $digest = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
        if ($digest -ne (Get-FileHash -LiteralPath $copy -Algorithm SHA256).Hash) { throw 'Untracked file backup verification failed.' }
        $files += [pscustomobject]@{ path = $path; sha256 = $digest }
    }
    $Plan.BackupPath = $destination
    $Plan.BackupRef = 'refs/pcclub-backups/' + $name
    $manifest = [pscustomobject]@{
        format = 1; old_head = $Plan.OldHead; target = $Plan.Target; mode = $Plan.Mode
        dirty = $Plan.Dirty; backup_ref = $Plan.BackupRef
        bundle_sha256 = (Get-FileHash -LiteralPath $bundle -Algorithm SHA256).Hash
        untracked = @($files)
    }
    [System.IO.File]::WriteAllText((Join-Path $destination 'manifest.json'),
        ($manifest | ConvertTo-Json -Depth 4), (New-Object System.Text.UTF8Encoding($false)))
    Write-Host "Verified local backup: $destination"
}

function Invoke-PCClubUpdate {
    [CmdletBinding()]
    param([string]$RepositoryPath, [string]$BackupDirectory = '', [switch]$Apply)
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Version Latest
    foreach ($variable in @('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_OBJECT_DIRECTORY', 'GIT_ALTERNATE_OBJECT_DIRECTORIES')) {
        if ([Environment]::GetEnvironmentVariable($variable, 'Process')) { throw 'Custom Git repository/index environment settings must be cleared first.' }
    }
    $script:updateGit = @(Get-Command git -CommandType Application -ErrorAction Stop)[0].Source
    $script:updateRepo = (Resolve-Path -LiteralPath $RepositoryPath -ErrorAction Stop).Path
    if ((Get-Item -LiteralPath $script:updateRepo -Force).Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
        throw 'The repository directory must not be a link or junction.'
    }
    $gitDirectory = Join-Path $script:updateRepo '.git'
    if (-not (Test-Path -LiteralPath $gitDirectory -PathType Container) -or
        ((Get-Item -LiteralPath $gitDirectory -Force).Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
        throw 'Use a normal Git checkout, not a linked worktree or bare repository.'
    }
    $root = (Invoke-UpdateGit -Arguments @('rev-parse', '--show-toplevel')).Text.Trim()
    if ([System.IO.Path]::GetFullPath($root) -ne [System.IO.Path]::GetFullPath($script:updateRepo)) { throw 'RepositoryPath must be the checkout root.' }
    $branch = Invoke-UpdateGit -Arguments @('symbolic-ref', '--quiet', '--short', 'HEAD') -AllowFailure
    if ($branch.Code -ne 0 -or $branch.Text.Trim() -cne 'main') { throw 'Switch to main without discarding local work before updating.' }
    if ((Invoke-UpdateGit -Arguments @('rev-parse', '--is-shallow-repository')).Text.Trim() -ne 'false') { throw 'A full Git history is required for a verified backup.' }
    $sparse = Invoke-UpdateGit -Arguments @('config', '--bool', '--get', 'core.sparseCheckout') -AllowFailure
    if ($sparse.Text.Trim() -eq 'true') { throw 'A complete working tree is required before updating.' }
    foreach ($operation in @('MERGE_HEAD', 'CHERRY_PICK_HEAD', 'REVERT_HEAD', 'rebase-apply', 'rebase-merge', 'sequencer', 'BISECT_LOG', 'index.lock')) {
        if (Test-Path -LiteralPath (Join-Path $gitDirectory $operation)) { throw 'Finish the current Git operation before updating.' }
    }
    $remote = (Invoke-UpdateGit -Arguments @('remote', 'get-url', '--all', 'origin')).Text.Trim()
    if ($remote -notmatch '^(https://github\.com/eclipse-vlr/pc-club-admin(\.git)?|git@github\.com:eclipse-vlr/pc-club-admin(\.git)?)$') {
        throw 'origin must point directly to eclipse-vlr/pc-club-admin on GitHub.'
    }
    if (-not $BackupDirectory) {
        $localData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
        if (-not $localData) { throw 'Specify a private BackupDirectory outside the repository.' }
        $BackupDirectory = Join-Path $localData 'PCClubAdmin/update-backups'
    }
    if (-not [System.IO.Path]::IsPathRooted($BackupDirectory)) { throw 'BackupDirectory must be an absolute path.' }
    $BackupDirectory = [System.IO.Path]::GetFullPath($BackupDirectory)
    $prefix = $script:updateRepo.TrimEnd([char[]]@('/', '\')) + [System.IO.Path]::DirectorySeparatorChar
    if ($BackupDirectory -eq $script:updateRepo -or $BackupDirectory.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'BackupDirectory must be outside the repository.'
    }
    $ancestorPath = $BackupDirectory
    while ($ancestorPath) {
        if (Test-Path -LiteralPath $ancestorPath) {
            $item = Get-Item -LiteralPath $ancestorPath -Force
            if (-not ($item -is [System.IO.DirectoryInfo]) -or
                ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) { throw 'BackupDirectory must use real directories, not files, links or junctions.' }
        }
        $ancestorPath = Split-Path -Parent $ancestorPath
    }
    # A configured Google key must remain outside the checkout; only its path is inspected.
    $settings = Join-Path $script:updateRepo '.demo.local.json'
    $keyPaths = @([Environment]::GetEnvironmentVariable('DRIVE_EXCEL_CREDENTIALS_FILE', 'Process'))
    if (Test-Path -LiteralPath $settings) {
        $file = Get-Item -LiteralPath $settings -Force
        if (-not ($file -is [System.IO.FileInfo]) -or $file.Length -gt 8192 -or
            ($file.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) { throw 'Saved demo settings require manual review.' }
        try { $data = Get-Content -LiteralPath $settings -Raw -Encoding UTF8 | ConvertFrom-Json }
        catch { throw 'Saved demo settings require manual review.' }
        if (-not $data -or -not $data.PSObject.Properties['credentials_file'] -or -not ($data.credentials_file -is [string])) { throw 'Saved demo settings require manual review.' }
        $keyPaths += $data.credentials_file
    }
    foreach ($keyPath in $keyPaths) {
        if (-not $keyPath) { continue }
        if (-not [System.IO.Path]::IsPathRooted($keyPath)) { throw 'The Google credential path must be absolute and outside the repository.' }
        $full = [System.IO.Path]::GetFullPath($keyPath)
        if ($full -eq $script:updateRepo -or $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Keep the Google key outside the repository before updating.' }
    }
    $lockPath = Join-Path $gitDirectory 'pcclub-update.lock'
    $lock = $null
    try {
        $lock = [System.IO.File]::Open($lockPath, 'CreateNew', 'Write', 'None')
        $indexFlags = (Invoke-UpdateGit -Arguments @('ls-files', '-v', '-z')).Text -split [char]0
        if (@($indexFlags | Where-Object { $_ -and $_ -cnotmatch '^H ' }).Count -gt 0) { throw 'Sparse, unmerged or assume-unchanged index entries require manual review.' }
        $tracked = @(Get-UpdatePaths -Arguments @('ls-files', '-z'))
        $stages = (Invoke-UpdateGit -Arguments @('ls-files', '--stage', '-z')).Text -split [char]0
        if (@($stages | Where-Object { $_ -and $_ -notmatch '^(100644|100755) ' }).Count -gt 0) {
            throw 'Tracked links or submodules require manual review.'
        }
        $oldHead = (Invoke-UpdateGit -Arguments @('rev-parse', '--verify', 'HEAD')).Text.Trim()
        Write-Host 'Fetching origin/main without changing the working files...'
        Fetch-UpdateTarget
        $target = (Invoke-UpdateGit -Arguments @('rev-parse', '--verify', 'refs/remotes/origin/main^{commit}')).Text.Trim()
        $targetEntries = (Invoke-UpdateGit -Arguments @('ls-tree', '-r', '--full-tree', '-z', $target)).Text -split [char]0
        $targets = @()
        $names = New-Object 'System.Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
        foreach ($entry in $targetEntries) {
            if (-not $entry) { continue }
            if ($entry -notmatch '^(100644|100755) blob [0-9a-f]{40,64}\t(.+)$') { throw 'Links or submodules in the new version require manual review.' }
            $path = $matches[2]
            if (-not $names.Add($path)) { throw 'Case-colliding target paths are not supported on Windows.' }
            Assert-UpdatePath $path
            $targets += $path
        }
        $status = (Invoke-UpdateGit -Arguments @('status', '--porcelain=v1', '-z', '--untracked-files=all')).Text
        $untracked = @(Get-UpdatePaths -Arguments @('ls-files', '--others', '--exclude-standard', '-z'))
        $total = [long]0
        foreach ($path in $untracked) {
            $file = Get-Item -LiteralPath (Join-Path $script:updateRepo $path) -Force
            if (-not ($file -is [System.IO.FileInfo])) { throw 'An untracked path is not a regular file.' }
            $total += $file.Length
        }
        if ($untracked.Count -gt 1000 -or $total -gt 64MB) { throw 'Too many or oversized untracked files; review them before updating.' }
        foreach ($path in $targets) {
            if ($path -notin $tracked -and (Test-Path -LiteralPath (Join-Path $script:updateRepo $path))) {
                throw 'A new tracked path collides with a local file or directory. No working files were changed.'
            }
            $parent = Split-Path -Parent (Join-Path $script:updateRepo $path)
            while ($parent.Length -gt $script:updateRepo.Length) {
                if (Test-Path -LiteralPath $parent -PathType Leaf) { throw 'A local file blocks a new directory. No working files were changed.' }
                $parent = Split-Path -Parent $parent
            }
        }
        $mode = 'up_to_date'
        $match = ''
        if ($oldHead -ne $target) {
            $ancestor = Invoke-UpdateGit -Arguments @('merge-base', '--is-ancestor', $oldHead, $target) -AllowFailure
            if ($ancestor.Code -eq 0) { $mode = 'fast_forward' }
            elseif ($ancestor.Code -eq 1) {
                $mode = 'local_commits'
                $tree = (Invoke-UpdateGit -Arguments @('rev-parse', 'HEAD^{tree}')).Text.Trim()
                $history = (Invoke-UpdateGit -Arguments @('log', '--format=%H %T', $target)).Text -split '\r?\n'
                foreach ($line in $history) {
                    if ($line -match '^([0-9a-f]{40,64}) ([0-9a-f]{40,64})$' -and $matches[2] -eq $tree) {
                        $match = $matches[1]; $mode = 'history_rewrite'; break
                    }
                }
            } else { throw 'Could not compare Git histories.' }
        }
        $plan = [pscustomobject]@{ Mode = $mode; OldHead = $oldHead; Target = $target; MatchingCommit = $match; Dirty = [bool]$status; BackupPath = ''; BackupRef = ''; Applied = $false }
        Write-Host "Update mode: $mode; local changes: $($plan.Dirty)."
        if (-not $Apply) { Write-Host 'Preview only. Run again with -Apply after reviewing the plan.'; return $plan }
        if ($mode -eq 'up_to_date') { Write-Host 'Already current. Local files were left in place.'; return $plan }
        Save-UpdateBackup -Plan $plan -Directory $BackupDirectory -Untracked $untracked
        if ($plan.Dirty -or $mode -eq 'local_commits') {
            throw "Local work was backed up; update stopped without changing it. Review the backup at $($plan.BackupPath)."
        }
        $current = (Invoke-UpdateGit -Arguments @('rev-parse', 'HEAD')).Text.Trim()
        $currentBranch = Invoke-UpdateGit -Arguments @('symbolic-ref', '--quiet', '--short', 'HEAD') -AllowFailure
        $currentStatus = (Invoke-UpdateGit -Arguments @('status', '--porcelain=v1', '-z', '--untracked-files=all')).Text
        if ($current -ne $oldHead -or $currentStatus -or $currentBranch.Code -ne 0 -or $currentBranch.Text.Trim() -cne 'main') {
            throw 'Local work or branch changed during backup. Update stopped.'
        }
        Invoke-UpdateGit -Arguments @('update-ref', $plan.BackupRef, $oldHead) -Label 'Local backup reference' | Out-Null
        Invoke-UpdateGit -Arguments @('reset', '--keep', $target) -Label 'Working tree update' | Out-Null
        if ((Invoke-UpdateGit -Arguments @('rev-parse', 'HEAD')).Text.Trim() -ne $target) { throw 'Update result requires manual review; keep the backup.' }
        $plan.Applied = $true
        Write-Host 'Code updated. Database, ignored settings and external key were not updated. No application was started.'
        return $plan
    }
    finally {
        if ($lock) { $lock.Dispose(); [System.IO.File]::Delete($lockPath) }
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    if (-not $RepositoryPath) { $RepositoryPath = Split-Path -Parent $PSScriptRoot }
    Invoke-PCClubUpdate -RepositoryPath $RepositoryPath -BackupDirectory $BackupDirectory -Apply:$Apply
}
