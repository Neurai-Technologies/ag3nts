@echo off
REM Auto-detect SSD (searches up to 3 levels deep), build combined AGENTS.md, launch Codex CLI.
set "BASE_PATH="
for %%d in (D E F G H I J K L M N O P Q R S T U V W X Y Z C) do (
    if exist "%%d:\ag3nts\shared\ag3nts.md" (set "BASE_PATH=%%d:\ag3nts" & goto :found)
)
for %%d in (D E F G H I J K L M N O P Q R S T U V W X Y Z C) do (
    for /d %%a in ("%%d:\*") do (
        if exist "%%a\ag3nts\shared\ag3nts.md" (set "BASE_PATH=%%a\ag3nts" & goto :found)
    )
)
for %%d in (D E F G H I J K L M N O P Q R S T U V W X Y Z C) do (
    for /d %%a in ("%%d:\*") do (
        for /d %%b in ("%%a\*") do (
            if exist "%%b\ag3nts\shared\ag3nts.md" (set "BASE_PATH=%%b\ag3nts" & goto :found)
        )
    )
)
echo Codex CLI not found. Is the SSD connected?
exit /b 1
:found
REM Build combined AGENTS.md from ag3nts.md + stub
copy /y "%BASE_PATH%\shared\ag3nts.md" "%BASE_PATH%\windows\codex-cli\config\AGENTS.md" >nul
echo. >> "%BASE_PATH%\windows\codex-cli\config\AGENTS.md"
echo --- >> "%BASE_PATH%\windows\codex-cli\config\AGENTS.md"
echo. >> "%BASE_PATH%\windows\codex-cli\config\AGENTS.md"
type "%BASE_PATH%\shared\codex-cli\AGENTS.md" >> "%BASE_PATH%\windows\codex-cli\config\AGENTS.md"
call "%BASE_PATH%\windows\codex-cli\codex-launch.cmd" %*
