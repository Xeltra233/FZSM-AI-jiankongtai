@echo off
cd /d %~dp0
bin\fzsm-doctor.exe -c config\config.yaml
pause

