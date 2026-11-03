@echo off
rem Install aos on Windows from a GitHub release, a local folder, a mirror URL,
rem or a single aos.exe. No Go toolchain, no administrator.
rem
rem   curl -fsSL -o install.cmd https://raw.githubusercontent.com/muthuishere/aos/main/install.cmd && install.cmd
rem   install.cmd /version v0.6.0
rem   install.cmd /from C:\path\to\release-folder      (goreleaser archives + checksums.txt)
rem   install.cmd /from https://mirror.example/aos/v0.6.0/
rem   install.cmd /from .\aos_0.5.0_windows_arm64.zip     (one release archive: unpacked)
rem   install.cmd /from .\aos.exe                       (copied in as-is)
rem   install.cmd /dir %USERPROFILE%\bin
rem
rem --from / --version / --dir work too. Environment equivalents: AOS_FROM,
rem AOS_VERSION, AOS_INSTALL_DIR. Default dir: %LOCALAPPDATA%\Programs\aos,
rem added to your user PATH (/no-path or AOS_NO_PATH=1 leaves PATH alone).
rem
rem Plain cmd.exe only (no PowerShell): curl.exe, tar.exe and certutil ship
rem with Windows 10 1803+ and Windows 11.
setlocal EnableExtensions DisableDelayedExpansion

set "REPO=muthuishere/aos"
set "FROM=%AOS_FROM%"
if not defined FROM set "FROM=%AOS_DOWNLOAD_BASE%"
set "VERSION=%AOS_VERSION%"
set "DIR=%AOS_INSTALL_DIR%"
if not defined DIR set "DIR=%LOCALAPPDATA%\Programs\aos"
set "NOPATH=%AOS_NO_PATH%"

rem ---- arguments ("/from=X" arrives as two words: cmd splits on '=') ----
:args
if "%~1"=="" goto args_done
set "A=%~1"
if /i "%A%"=="/from"      (set "FROM=%~2" & shift & shift & goto args)
if /i "%A%"=="--from"     (set "FROM=%~2" & shift & shift & goto args)
if /i "%A%"=="/version"   (set "VERSION=%~2" & shift & shift & goto args)
if /i "%A%"=="--version"  (set "VERSION=%~2" & shift & shift & goto args)
if /i "%A%"=="/dir"       (set "DIR=%~2" & shift & shift & goto args)
if /i "%A%"=="--dir"      (set "DIR=%~2" & shift & shift & goto args)
if /i "%A%"=="/no-path"   (set "NOPATH=1" & shift & goto args)
if /i "%A%"=="--no-path"  (set "NOPATH=1" & shift & goto args)
if /i "%A%"=="/?"         goto usage
if /i "%A%"=="/h"         goto usage
if /i "%A%"=="--help"     goto usage
call :fail "unknown argument: %A%"
exit /b 1
:usage
echo install.cmd [/from ^<folder^|url^|aos.exe^|archive.zip^>] [/version vX.Y.Z] [/dir ^<path^>] [/no-path]
exit /b 0
:args_done

for %%T in (curl.exe tar.exe certutil.exe) do (
  where %%T >nul 2>nul || (call :fail "%%T is missing; it ships with Windows 10 1803+ and Windows 11" & exit /b 1)
)

set "ARCH=amd64"
if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "ARCH=arm64"
if /i "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "ARCH=arm64"

set "TMPD=%TEMP%\aos-install-%RANDOM%%RANDOM%"
mkdir "%TMPD%" >nul 2>nul
if not exist "%DIR%" mkdir "%DIR%"
if not exist "%DIR%" (call :fail "could not create %DIR%" & goto cleanup_fail)
set "TARGET=%DIR%\aos.exe"

rem ---- a single local file: an archive or the executable ----
if defined FROM if exist "%FROM%" if not exist "%FROM%\*" goto from_file
goto from_release

:from_file
for %%F in ("%FROM%") do set "FEXT=%%~xF"
if /i "%FEXT%"==".zip" (
  echo ==^> Installing from the archive %FROM%
  mkdir "%TMPD%\x" >nul 2>nul
  tar -xf "%FROM%" -C "%TMPD%\x" || (call :fail "could not unpack %FROM%" & goto cleanup_fail)
  if not exist "%TMPD%\x\aos.exe" (call :fail "%FROM% does not contain aos.exe" & goto cleanup_fail)
  copy /y "%TMPD%\x\aos.exe" "%TARGET%" >nul || (call :fail "could not write %TARGET%" & goto cleanup_fail)
) else (
  echo ==^> Installing the executable %FROM%
  copy /y "%FROM%" "%TARGET%" >nul || (call :fail "could not write %TARGET%" & goto cleanup_fail)
)
goto installed

rem ---- a release: GitHub, a mirror URL or a local folder ----
:from_release
set "ISURL=0"
if defined FROM goto have_base
if not defined VERSION (
  for /f "delims=" %%U in ('curl -fsSL -o nul -w "%%{url_effective}" "https://github.com/%REPO%/releases/latest"') do set "LATEST=%%U"
)
if not defined VERSION (
  if not defined LATEST (call :fail "could not resolve the latest release of %REPO%" & goto cleanup_fail)
  for %%V in ("%LATEST:/=" "%") do set "VERSION=%%~V"
)
if not "%VERSION:~0,1%"=="v" set "VERSION=v%VERSION%"
set "BASE=https://github.com/%REPO%/releases/download/%VERSION%"
set "ISURL=1"
goto base_ready
:have_base
set "BASE=%FROM%"
if "%BASE:~-1%"=="/" set "BASE=%BASE:~0,-1%"
if "%BASE:~-1%"=="\" set "BASE=%BASE:~0,-1%"
if /i "%BASE:~0,7%"=="http://" set "ISURL=1"
if /i "%BASE:~0,8%"=="https://" set "ISURL=1"
if "%ISURL%"=="0" if not exist "%BASE%\*" (call :fail "/from %FROM% is not a folder, an executable, or an http(s) URL" & goto cleanup_fail)
:base_ready
echo ==^> Installing aos for windows/%ARCH% from %BASE%

set "VNUM="
if defined VERSION set "VNUM=%VERSION:v=%"
set "ARCHIVE="
set "SUMS=%TMPD%\checksums.txt"
call :fetch checksums.txt "%SUMS%"
if errorlevel 1 goto no_sums
for /f "usebackq tokens=1,2" %%a in ("%SUMS%") do call :pick "%%b"
if not defined ARCHIVE (call :fail "no aos archive for windows/%ARCH% at %BASE%" & goto cleanup_fail)
goto have_archive
:no_sums
del "%SUMS%" >nul 2>nul
if defined VNUM (
  set "ARCHIVE=aos_%VNUM%_windows_%ARCH%.zip"
  goto have_archive
)
if "%ISURL%"=="1" (call :fail "%BASE% has no checksums.txt to find the archive by; pass /version" & goto cleanup_fail)
for /f "delims=" %%n in ('dir /b /a-d "%BASE%\aos_*_windows_%ARCH%.zip" 2^>nul') do if not defined ARCHIVE set "ARCHIVE=%%n"
if not defined ARCHIVE (call :fail "no aos archive for windows/%ARCH% at %BASE%" & goto cleanup_fail)
:have_archive

set "ZIP=%TMPD%\%ARCHIVE%"
call :fetch "%ARCHIVE%" "%ZIP%"
if errorlevel 1 (call :fail "download failed: %BASE%/%ARCHIVE%" & goto cleanup_fail)
if exist "%SUMS%" (
  call :verify
  if errorlevel 1 goto cleanup_fail
  echo     sha256 verified
) else (
  echo WARNING: %BASE% has no checksums.txt -- installing %ARCHIVE% WITHOUT verifying it 1>&2
)
mkdir "%TMPD%\x" >nul 2>nul
tar -xf "%ZIP%" -C "%TMPD%\x" || (call :fail "could not unpack %ARCHIVE%" & goto cleanup_fail)
if not exist "%TMPD%\x\aos.exe" (call :fail "%ARCHIVE% does not contain aos.exe" & goto cleanup_fail)
copy /y "%TMPD%\x\aos.exe" "%TARGET%" >nul || (call :fail "could not write %TARGET%" & goto cleanup_fail)

:installed
rd /s /q "%TMPD%" >nul 2>nul
echo     %TARGET%
if defined NOPATH goto run_once
set "UPATH="
for /f "skip=2 tokens=2,*" %%a in ('reg query HKCU\Environment /v Path 2^>nul') do set "UPATH=%%b"
echo ;%UPATH%; | find /i ";%DIR%;" >nul && goto run_once
if defined UPATH (set "NEWPATH=%UPATH%;%DIR%") else (set "NEWPATH=%DIR%")
rem reg, not setx: setx truncates PATH at 1024 characters.
reg add HKCU\Environment /v Path /t REG_EXPAND_SZ /d "%NEWPATH%" /f >nul || (call :fail "could not update your user PATH" & exit /b 1)
rem setx broadcasts the environment change so new terminals see the PATH.
setx AOS_INSTALLED 1 >nul 2>nul
reg delete HKCU\Environment /v AOS_INSTALLED /f >nul 2>nul
echo     added %DIR% to your user PATH (open a new terminal to pick it up)
:run_once
rem Running it once also installs the bundled agent skill.
endlocal & set "PATH=%DIR%;%PATH%" & "%DIR%\aos.exe" --version
exit /b %ERRORLEVEL%

rem ---- helpers ----------------------------------------------------------------

:fetch
rem :fetch <name> <dest> -- download or copy one release file; errorlevel 1 on failure
if "%ISURL%"=="1" (
  curl -fsSL -o "%~2" "%BASE%/%~1" 2>nul || exit /b 1
) else (
  copy /y "%BASE%\%~1" "%~2" >nul 2>nul || exit /b 1
)
exit /b 0

:pick
rem :pick <name> -- the first checksums entry that is this arch's archive (and version)
if defined ARCHIVE exit /b 0
set "N=%~1"
echo %N%| findstr /r /i /c:"^aos_.*_windows_%ARCH%\.zip$" >nul || exit /b 0
if defined VNUM (
  echo %N%| find "_%VNUM%_" >nul || exit /b 0
)
set "ARCHIVE=%N%"
exit /b 0

:verify
set "WANT="
for /f "usebackq tokens=1,2" %%a in ("%SUMS%") do if /i "%%b"=="%ARCHIVE%" set "WANT=%%a"
set "GOT="
for /f "skip=1 delims=" %%h in ('certutil -hashfile "%ZIP%" SHA256') do if not defined GOT set "GOT=%%h"
if defined GOT set "GOT=%GOT: =%"
if not defined WANT (call :fail "%ARCHIVE% is not listed in checksums.txt -- refusing to install" & exit /b 1)
if /i not "%WANT%"=="%GOT%" (call :fail "checksum mismatch for %ARCHIVE% -- refusing to install" & exit /b 1)
exit /b 0

:fail
echo install: %~1 1>&2
exit /b 1

:cleanup_fail
if defined TMPD rd /s /q "%TMPD%" >nul 2>nul
exit /b 1
