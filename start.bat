@echo off
setlocal

cd /d "%~dp0"

set "PORT=%~1"
if "%PORT%"=="" set "PORT=3100"

echo === Building Maatgen ===
call corepack pnpm build
if errorlevel 1 (
  echo.
  echo Build failed. Maatgen was not started.
  exit /b 1
)

echo.
echo === Building Agent Manager executable ===
go -C "%~dp0apps\agent-manager" build -o "%~dp0apps\agent-manager\agent-manager.exe" ./cmd/agent-manager
if errorlevel 1 (
  echo.
  echo Agent Manager build failed. Maatgen was not started.
  exit /b 1
)

echo.
echo === Starting Maatgen ===
set "MAATGEN_START_PORT=%PORT%"
powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command ^
  "$portNumber = [int]$env:MAATGEN_START_PORT; " ^
  "$listeners = @(Get-NetTCPConnection -LocalPort $portNumber -State Listen -ErrorAction SilentlyContinue); " ^
  "$processIds = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique); " ^
  "if ($processIds.Count -gt 0) { " ^
  "  Write-Host ('Port {0} is in use. Stopping listener PID(s): {1}' -f $portNumber, ($processIds -join ', ')); " ^
  "  foreach ($processId in $processIds) { Stop-Process -Id $processId -Force -ErrorAction Stop }; " ^
  "  Start-Sleep -Milliseconds 200; " ^
  "  if (Get-NetTCPConnection -LocalPort $portNumber -State Listen -ErrorAction SilentlyContinue) { throw ('Port {0} is still in use.' -f $portNumber) } " ^
  "}"
if errorlevel 1 (
  echo Maatgen was not started because port %PORT% could not be released.
  exit /b 1
)
echo Open http://127.0.0.1:%PORT%/ in your browser.
"%~dp0apps\agent-manager\agent-manager.exe" ^
  --config "config\providers.json" ^
  --static-dir "%~dp0apps\web\dist" ^
  --data-dir "%~dp0.maatgen" ^
  --port %PORT%

endlocal
