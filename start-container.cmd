@echo off
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-container.ps1" %*
if errorlevel 1 (
  echo Startup failed. Keep the configuration and database; see the message above.
  pause
  exit /b 1
)
