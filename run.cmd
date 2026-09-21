@echo off
rem Start the built program without a console window; it stays in the
rem notification area. Build it first with build.cmd.
start "" "%~dp0dist\SDAutoLogin.exe"
