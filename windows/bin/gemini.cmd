@echo off
set "SSD="
for %%d in (D E F G H I J K L M N O P Q R S T U V W X Y Z C) do (
    if exist "%%d:\Terminal-AI\windows\gemini-cli\gemini-launch.cmd" set "SSD=%%d"
)
if not defined SSD (
    echo Gemini CLI not found. Is the SSD connected?
    exit /b 1
)
fc /b "%SSD%:\Terminal-AI\shared\ag3nts.md" "%SSD%:\Terminal-AI\windows\gemini-cli\config\ag3nts.md" >nul 2>&1
if errorlevel 1 copy /y "%SSD%:\Terminal-AI\shared\ag3nts.md" "%SSD%:\Terminal-AI\windows\gemini-cli\config\ag3nts.md" >nul
fc /b "%SSD%:\Terminal-AI\shared\gemini-cli\GEMINI.md" "%SSD%:\Terminal-AI\windows\gemini-cli\config\GEMINI.md" >nul 2>&1
if errorlevel 1 copy /y "%SSD%:\Terminal-AI\shared\gemini-cli\GEMINI.md" "%SSD%:\Terminal-AI\windows\gemini-cli\config\GEMINI.md" >nul
call "%SSD%:\Terminal-AI\windows\gemini-cli\gemini-launch.cmd" %*
