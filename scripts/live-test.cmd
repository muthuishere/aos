@echo off
rem live-test.cmd — focus-independent feature pass for aos on Windows.
rem   scripts\live-test.cmd [path\to\aos.exe]
rem The checks live in a Go program (native only; see AGENTS.md).
rem Build it on any OS and put livetest.exe next to this file or in test\windows:
rem   GOOS=windows GOARCH=arm64 go build -o scripts/livetest.exe ./test/windows/livetest
rem Asserts only on machine-checkable state (JSON results, WM_GETTEXT / UI
rem Automation read-back, window list IDs, events.json), on monitor 1 only
rem (AOS_TEST_MONITOR to change). Prints PASS/FAIL per step; exits non-zero if
rem any step failed.
setlocal
set "FFDIR=%LOCALAPPDATA%\Microsoft\WinGet\Packages\Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe\ffmpeg-9.0.2-full_build\bin"
if exist "%FFDIR%\ffmpeg.exe" set "PATH=%FFDIR%;%PATH%"
set AOS_NO_SKILL_AUTOINSTALL=1
set "LT=%~dp0livetest.exe"
if not exist "%LT%" set "LT=%~dp0..\test\windows\livetest.exe"
if not exist "%LT%" (
  echo live-test: livetest.exe not found next to this file or in test\windows; build it first 1>&2
  exit /b 2
)
"%LT%" %1
exit /b %ERRORLEVEL%
