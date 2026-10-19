# Run from repository root: . ./scripts/load-env.ps1
# Plain KEY=VALUE only; do not execute arbitrary .env contents.
Get-Content .env | ForEach-Object {
    if ($_ -match '^([A-Z][A-Z0-9_]*)=(.*)$') {
        [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
    }
}
