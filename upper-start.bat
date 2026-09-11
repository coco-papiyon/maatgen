@echo off
setlocal

REM Starts Maatgen as an upper node: same as start.bat, plus a
REM dedicated --relay-listen so lower nodes on other machines can dial in.
REM Usage: upper-start.bat [port] [relay-port]

cd /d "%~dp0"

set "PORT=%~1"
if "%PORT%"=="" set "PORT=3200"

set "RELAY_PORT=%~2"
if "%RELAY_PORT%"=="" set /a "RELAY_PORT=PORT+1"

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
echo === Starting Maatgen (upper node) ===
echo Open http://127.0.0.1:%PORT%/ in your browser.
echo Lower nodes should use: --upstream-url ws://^<this-host^>:%RELAY_PORT%/api/relay/connect
"%~dp0apps\agent-manager\agent-manager.exe" ^
  --config "config\providers.json" ^
  --static-dir "%~dp0apps\web\dist" ^
  --data-dir "%~dp0.maatgen" ^
  --port %PORT% ^
  --relay-listen 0.0.0.0:%RELAY_PORT%

endlocal
