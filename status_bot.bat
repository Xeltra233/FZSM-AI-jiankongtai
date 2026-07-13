@echo off
cd /d %~dp0
echo === processes ===
tasklist /FI "IMAGENAME eq fzsm-bot.exe"
tasklist /FI "IMAGENAME eq fzsm-dashboard.exe"
echo.
echo === doctor ===
bin\fzsm-doctor.exe -c config.yaml
pause
