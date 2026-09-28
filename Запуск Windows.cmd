@echo off
setlocal DisableDelayedExpansion
chcp 65001 >nul
cd /d "%~dp0"
if errorlevel 1 exit /b 1
if exist "%~dp0carriersim.exe" (
    "%~dp0carriersim.exe" %*
    set "CARRIER_STATUS=%ERRORLEVEL%"
    goto finish
)
echo Не найден carriersim.exe. Соберите: go build -o carriersim.exe ./cmd/carriersim
set "CARRIER_STATUS=1"
:finish
if not "%CARRIER_STATUS%"=="0" if "%~1"=="" pause
exit /b %CARRIER_STATUS%
