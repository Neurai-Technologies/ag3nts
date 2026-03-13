@echo off
REM Auto-detect SSD (searches up to 3 levels deep), sync shared configs, launch Claude Code.
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
echo Claude Code not found. Is the SSD connected?
exit /b 1
:found
fc /b "%BASE_PATH%\shared\ag3nts.md" "%BASE_PATH%\windows\claude-code\config\ag3nts.md" >nul 2>&1
if errorlevel 1 copy /y "%BASE_PATH%\shared\ag3nts.md" "%BASE_PATH%\windows\claude-code\config\ag3nts.md" >nul
fc /b "%BASE_PATH%\shared\claude-code\CLAUDE.md" "%BASE_PATH%\windows\claude-code\config\CLAUDE.md" >nul 2>&1
if errorlevel 1 copy /y "%BASE_PATH%\shared\claude-code\CLAUDE.md" "%BASE_PATH%\windows\claude-code\config\CLAUDE.md" >nul
"%BASE_PATH%\windows\claude-code\bin\claude.exe" %*
