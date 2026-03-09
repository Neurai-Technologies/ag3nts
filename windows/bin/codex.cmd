@echo off
set "SSD="
for %%d in (D E F G H I J K L M N O P Q R S T U V W X Y Z C) do (
    if exist "%%d:\Terminal-AI\windows\codex-cli\codex-launch.cmd" set "SSD=%%d"
)
if not defined SSD (
    echo Codex CLI not found. Is the SSD connected?
    exit /b 1
)
REM Build combined AGENTS.md from ag3nts.md + stub
copy /y "%SSD%:\Terminal-AI\shared\ag3nts.md" "%SSD%:\Terminal-AI\windows\codex-cli\config\AGENTS.md" >nul
echo. >> "%SSD%:\Terminal-AI\windows\codex-cli\config\AGENTS.md"
echo --- >> "%SSD%:\Terminal-AI\windows\codex-cli\config\AGENTS.md"
echo. >> "%SSD%:\Terminal-AI\windows\codex-cli\config\AGENTS.md"
type "%SSD%:\Terminal-AI\shared\codex-cli\AGENTS.md" >> "%SSD%:\Terminal-AI\windows\codex-cli\config\AGENTS.md"
call "%SSD%:\Terminal-AI\windows\codex-cli\codex-launch.cmd" %*
