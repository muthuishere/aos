@echo off
rem Live Windows check: run in a desktop session with aos.exe, testform.exe and
rem hiddendesktop.exe next to this file (build them on any OS, see README.md).
setlocal
set AOS_NO_SKILL_AUTOINSTALL=1
"%~dp0hiddendesktop.exe" %*
exit /b %ERRORLEVEL%
