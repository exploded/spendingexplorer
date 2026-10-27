@echo off
setlocal enabledelayedexpansion

echo Building spendingexplorer...
go build -o spendingexplorer.exe ./cmd/spendingexplorer/
if errorlevel 1 (
    echo Build failed!
    exit /b 1
)

if not exist .env (
    if exist .env.example (
        copy .env.example .env
        echo Created .env from .env.example
    )
)

if exist .env (
    for /f "usebackq tokens=1,* delims==" %%a in (.env) do (
        echo %%a | findstr /r "^#" >nul
        if errorlevel 1 (
            if not "%%a"=="" (
                set "%%a=%%b"
            )
        )
    )
)

if not exist spending.db (
    echo spending.db not found. Run: sh reload.sh
    exit /b 1
)

echo Starting spendingexplorer on port %PORT%...
spendingexplorer.exe
