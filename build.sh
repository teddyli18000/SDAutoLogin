#!/usr/bin/env bash
# 构建单文件可执行程序（无运行时依赖）
set -e
cd "$(dirname "$0")"

echo "[1/2] go vet ..."
go vet ./...

echo "[2/2] go build ..."
go build -trimpath -ldflags "-s -w" -o SDAutoLogin .

echo
echo "构建完成: $(pwd)/SDAutoLogin"
echo "macOS 编译需要已安装 Xcode Command Line Tools（xcode-select --install）"
