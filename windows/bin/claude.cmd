@echo off
set "SSD="
for %%d in (D E F G H I J K L M N O P Q R S T U V W X Y Z C) do (
    if exist "%%d:\Terminal-AI\windows\claude-code\bin\claude.exe" set "SSD=%%d"
)
if not defined SSD (
    echo Claude Code not found. Is the SSD connected?
    exit /b 1
)
fc /b "%SSD%:\Terminal-AI\shared\ag3nts.md" "%SSD%:\Terminal-AI\windows\claude-code\config\ag3nts.md" >nul 2>&1
if errorlevel 1 copy /y "%SSD%:\Terminal-AI\shared\ag3nts.md" "%SSD%:\Terminal-AI\windows\claude-code\config\ag3nts.md" >nul
fc /b "%SSD%:\Terminal-AI\shared\claude-code\CLAUDE.md" "%SSD%:\Terminal-AI\windows\claude-code\config\CLAUDE.md" >nul 2>&1
if errorlevel 1 copy /y "%SSD%:\Terminal-AI\shared\claude-code\CLAUDE.md" "%SSD%:\Terminal-AI\windows\claude-code\config\CLAUDE.md" >nul
"%SSD%:\Terminal-AI\windows\claude-code\bin\claude.exe" %*
