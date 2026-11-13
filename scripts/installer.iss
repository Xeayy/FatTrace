; Inno Setup Script for FatTrace
; Generates standard Windows Installer (Setup.exe) with Desktop Shortcut, Start Menu, Uninstaller, and Launch action.

#ifndef MyAppVersion
  #define MyAppVersion "1.3.1"
#endif

#ifndef MyAppArch
  #define MyAppArch "amd64"
#endif

#define MyAppName "FatTrace"
#define MyAppPublisher "Xeayy"
#define MyAppURL "https://github.com/Xeayy/FatTrace"
#define MyAppExeName "FatTrace.exe"

[Setup]
AppId={{B189DF27-9481-4C5C-8E1E-2B60EC37494A}}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
LicenseFile=..\LICENSE
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputDir=..\dist
OutputBaseFilename=FatTrace-Setup-{#MyAppVersion}-windows-{#MyAppArch}
SetupIconFile=..\assets\icon\fattrace.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
#if MyAppArch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif

[Languages]
; ChineseSimplified.isl 随仓库提供（Inno Setup 官方发行版不自带简体中文）
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
; Main GUI launcher (FatTrace.exe)
Source: "..\dist\fatgui-{#MyAppVersion}-windows-{#MyAppArch}.exe"; DestDir: "{app}"; DestName: "FatTrace.exe"; Flags: ignoreversion
; Also provide fatgui.exe for CLI scripts / backwards compatibility
Source: "..\dist\fatgui-{#MyAppVersion}-windows-{#MyAppArch}.exe"; DestDir: "{app}"; DestName: "fatgui.exe"; Flags: ignoreversion
; Core engine CLI binary (fat.exe)
Source: "..\dist\fat-{#MyAppVersion}-windows-{#MyAppArch}.exe"; DestDir: "{app}"; DestName: "fat.exe"; Flags: ignoreversion
; Documentation & license
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
; Icon assets
Source: "..\assets\icon\fattrace.ico"; DestDir: "{app}\assets\icon"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; IconFilename: "{app}\{#MyAppExeName}"
Name: "{group}\FatTrace 命令行 (fat.exe)"; Filename: "{cmd}"; Parameters: "/k cd /d ""{app}"" && fat.exe help"; WorkingDir: "{app}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; IconFilename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Run]
; Option to launch the GUI app on setup completion
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
Type: filesandordirs; Name: "{app}\assets"
