@echo off
rem 构建 Windows 单文件可执行程序（无任何运行时依赖）
setlocal
cd /d "%~dp0"

echo [1/2] go vet ...
go vet ./...
if errorlevel 1 goto :fail

echo [2/2] go build ...
go build -trimpath -ldflags "-s -w -H=windowsgui" -o SDAutoLogin.exe .
if errorlevel 1 goto :fail

echo.
echo 构建完成: %~dp0SDAutoLogin.exe
echo 双击运行即可，程序会常驻在任务栏通知区域。
exit /b 0

:fail
echo.
echo 构建失败。
exit /b 1
