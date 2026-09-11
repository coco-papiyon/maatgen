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
echo Open http://127.0.0.1:%PORT%/ in your browser.
"%~dp0apps\agent-manager\agent-manager.exe" ^
  --config "config\providers.json" ^
  --static-dir "%~dp0apps\web\dist" ^
  --data-dir "%~dp0.maatgen" ^
  --port %PORT%

endlocal
