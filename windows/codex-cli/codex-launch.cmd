@echo off

REM Resolve script directory
set SCRIPT_DIR=%~dp0

REM Portable Node location
set NODE_DIR=%SCRIPT_DIR%..\node\node-v25.8.0-win-x64

REM Temporarily prepend Node to PATH
set PATH=%NODE_DIR%;%PATH%

REM Run Codex CLI
"%SCRIPT_DIR%node_modules\.bin\codex.cmd" %*

@echo off

REM Resolve current directory
set SCRIPT_DIR=%~dp0

REM Locate portable Node automatically
set NODE_DIR=%SCRIPT_DIR%..\node

for /d %%i in ("%NODE_DIR%\node-v*") do (
    set NODE_BIN=%%i
)

REM Prepend Node to PATH
set PATH=%NODE_BIN%;%PATH%

REM Run Codex
"%SCRIPT_DIR%node_modules\.bin\codex.cmd" %*