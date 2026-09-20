@echo off
rem Build the Windows executable. The result is a single file with no
rem runtime dependency, the tray icon and the settings page are built in.
setlocal
cd /d "%~dp0"

echo [1/2] go vet ...
go vet ./...
if errorlevel 1 goto :fail

echo [2/2] go build ...
go build -trimpath -ldflags "-s -w -H=windowsgui" -o SDAutoLogin.exe .
if errorlevel 1 goto :fail

echo.
echo Built: %~dp0SDAutoLogin.exe
echo Double click to run. The program stays in the notification area.
exit /b 0

:fail
echo.
echo Build failed.
exit /b 1
