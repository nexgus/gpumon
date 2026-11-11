@echo off
rem Clears all GPU Monitor settings so that the program starts with default
rem settings next time, the same as after a fresh install. Recordings (the
rem history folder in the data folder) are kept.
rem
rem Usage: reset-settings.bat [-y]
rem   -y  Delete without asking for confirmation.
rem
rem Deletes settings.json and the settings.json.* backups and temporary files
rem in the data folder %LOCALAPPDATA%\gpumon. When GPUMON_DATA_DIR is set, it
rem is used instead, the same way the GUI decides its data folder.
rem
rem The GUI writes its complete in-memory settings back to the file on every
rem change, so this script refuses to run while the GUI is running.
rem
rem Comments are in English because cmd parses a batch file in the console
rem code page, and UTF-8 Chinese text may be misparsed under code page 950.
setlocal EnableExtensions

rem When started by double-clicking, cmd runs with /c and closes the window on
rem exit, so pause at the end to keep the result readable.
set "PAUSE_AT_END="
setlocal EnableDelayedExpansion
if /I not "!CMDCMDLINE:/c =!"=="!CMDCMDLINE!" (endlocal & set "PAUSE_AT_END=1") else endlocal

set "ASSUME_YES="
if "%~1"=="" goto :args_done
if /I "%~1"=="-y" (
    set "ASSUME_YES=1"
    goto :args_done
)
if /I "%~1"=="-h" goto :help
if /I "%~1"=="--help" goto :help
call :usage 1>&2
set "RC=2"
goto :end

:help
call :usage
set "RC=0"
goto :end

:args_done
if defined GPUMON_DATA_DIR (
    set "DATA_DIR=%GPUMON_DATA_DIR%"
) else if defined LOCALAPPDATA (
    set "DATA_DIR=%LOCALAPPDATA%\gpumon"
) else (
    set "DATA_DIR=%APPDATA%\gpumon"
)

rem The GUI executable is gpumon.exe (installed by the .msi) or
rem gpumon-<version>-windows-amd64.exe (direct output of build.sh).
tasklist /NH 2>nul | findstr /I /B /C:"gpumon" >nul
if not errorlevel 1 (
    echo GPU Monitor is running. Quit it first, then run this script again. 1>&2
    set "RC=1"
    goto :end
)

if not exist "%DATA_DIR%\settings.json*" (
    echo No settings found in "%DATA_DIR%". Nothing to do.
    set "RC=0"
    goto :end
)

echo The following files will be deleted:
for %%F in ("%DATA_DIR%\settings.json*") do echo   %%~fF
echo Recordings in "%DATA_DIR%\history" are kept.

if defined ASSUME_YES goto :delete
set "ANS="
set /p "ANS=Continue? [y/N] "
if /I "%ANS%"=="y" goto :delete
if /I "%ANS%"=="yes" goto :delete
echo Cancelled.
set "RC=1"
goto :end

:delete
del /F /Q "%DATA_DIR%\settings.json*" 2>nul
if exist "%DATA_DIR%\settings.json*" (
    echo Some files could not be deleted. 1>&2
    set "RC=1"
    goto :end
)
echo Settings cleared. GPU Monitor will start with default settings.
set "RC=0"
goto :end

:usage
echo Usage: reset-settings.bat [-y]
echo   -y  Delete without asking for confirmation.
exit /b 0

:end
if defined PAUSE_AT_END pause
exit /b %RC%
