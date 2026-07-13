@echo off
cd /d %~dp0
echo stopping Go bot/dashboard...
taskkill /IM fzsm-bot.exe /F >nul 2>&1
taskkill /IM fzsm-dashboard.exe /F >nul 2>&1
echo done.
pause
