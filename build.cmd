@echo off
rem Build the Windows executable through the task runner, so the flags live in
rem one place. See "go run ./tools/build" for every task.
setlocal
cd /d "%~dp0"

go run ./tools/build build
if errorlevel 1 exit /b 1

echo.
echo Built: %~dp0dist\SDAutoLogin.exe
echo Double click to run. The program stays in the notification area.
exit /b 0
