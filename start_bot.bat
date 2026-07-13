@echo off
cd /d %~dp0
if not exist logs mkdir logs
echo starting Go bot + dashboard...
start "fzsm-bot" /MIN bin\fzsm-bot.exe -c config.yaml -primary -mode live -every 18
start "fzsm-dashboard" /MIN bin\fzsm-dashboard.exe -c config.yaml -host 127.0.0.1 -port 8787 -html web\dashboard.html
timeout /t 2 >nul
echo.
echo Dashboard: http://127.0.0.1:8787/
echo Bot: bin\fzsm-bot.exe -primary
bin\fzsm-doctor.exe -c config.yaml
pause
