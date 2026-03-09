@echo off
set SCRIPT_DIR=%~dp0
set PATH=%SCRIPT_DIR%..\node\node-v25.8.0-win-x64;%PATH%
"%SCRIPT_DIR%gemini.cmd" %*
