@echo off
cd /d %~dp0
echo Go-only bootstrap
if not exist auth\cookies.json (
  echo missing auth\cookies.json
  echo import cookies first, e.g. auth helper JSON into auth\cookies.json
  pause
  exit /b 1
)
if not exist bin\fzsm-bot.exe (
  echo build missing. run: go -C go build -o ..\bin\fzsm-bot.exe .\cmd\bot
  pause
  exit /b 1
)
call start_bot.bat

