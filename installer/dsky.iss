; Inno Setup script for DSKY.
;
; Deliberately a per-user install: PrivilegesRequired=lowest puts everything
; under %LOCALAPPDATA%\Programs\dsky and touches only HKCU, so nobody needs
; administrator rights to install a tool they are going to run as themselves.
; That also matches the paths install.ps1 uses, so the two agree and
; `dsky uninstall` knows the same places.
;
; Built by the release workflow; version comes in as /DAppVersion=v0.0.0.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceDir
  #define SourceDir "..\dist"
#endif

[Setup]
; Stable across versions so upgrades replace rather than stack up.
AppId={{8F3C2A61-5D74-4E2B-9C18-7A6B0E4D9F23}
AppName=DSKY
AppVersion={#AppVersion}
AppPublisher=Uplink Research LLC
AppPublisherURL=https://uplinkresearch.com
AppSupportURL=https://github.com/uplinkresearch/dsky
AppUpdatesURL=https://github.com/uplinkresearch/dsky/releases
DefaultDirName={localappdata}\Programs\dsky
DefaultGroupName=DSKY
DisableProgramGroupPage=yes
DisableDirPage=yes
PrivilegesRequired=lowest
OutputDir=..\dist
; No version in the name: the product page links to
; releases/latest/download/<this>, which only works if the name never moves.
OutputBaseFilename=dsky-setup-amd64
SetupIconFile=..\dsky.ico
UninstallDisplayIcon={app}\dsky-app.exe
UninstallDisplayName=DSKY
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
LicenseFile=..\LICENSE

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Shortcuts:"
Name: "addtopath"; Description: "Add dsky to my PATH (so it works in any terminal)"; GroupDescription: "Command line:"

[Files]
Source: "{#SourceDir}\dsky.exe";     DestDir: "{app}"; Flags: ignoreversion
; The `compose` alias, kept because the original one-shot workflow is spelled
; that way and people have it in muscle memory and scripts.
Source: "{#SourceDir}\dsky.exe";     DestDir: "{app}"; DestName: "compose.exe"; Flags: ignoreversion
Source: "{#SourceDir}\dsky-app.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\dsky.ico";               DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\DSKY"; Filename: "{app}\dsky-app.exe"; IconFilename: "{app}\dsky.ico"; Comment: "Build and flash bootable OS installers"
Name: "{group}\Uninstall DSKY"; Filename: "{uninstallexe}"
Name: "{autodesktop}\DSKY"; Filename: "{app}\dsky-app.exe"; IconFilename: "{app}\dsky.ico"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; \
  ValueData: "{olddata};{app}"; Check: NeedsAddPath(ExpandConstant('{app}')); Tasks: addtopath

[Run]
Filename: "{app}\dsky-app.exe"; Description: "Open DSKY"; Flags: nowait postinstall skipifsilent

[Code]
// NeedsAddPath keeps PATH idempotent: reinstalling must not append the same
// folder again, and an existing entry must not be disturbed.
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', OrigPath) then
  begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Uppercase(Param) + ';', ';' + Uppercase(OrigPath) + ';') = 0;
end;

// RemoveFromPath undoes the above on uninstall, leaving every other entry
// alone rather than rewriting the whole variable from a template.
procedure RemoveFromPath(Param: string);
var
  OrigPath, NewPath: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', OrigPath) then
    exit;
  NewPath := ';' + OrigPath + ';';
  P := Pos(';' + Uppercase(Param) + ';', Uppercase(NewPath));
  if P = 0 then
    exit;
  Delete(NewPath, P, Length(Param) + 1);
  // Trim the sentinel semicolons added above.
  NewPath := Copy(NewPath, 2, Length(NewPath) - 2);
  RegWriteExpandStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', NewPath);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  LibDir: string;
begin
  if CurUninstallStep <> usPostUninstall then
    exit;
  RemoveFromPath(ExpandConstant('{app}'));

  // The library holds downloaded operating systems and built media, and is
  // routinely tens of gigabytes. Deleting it silently would throw away hours
  // of downloading, and keeping it makes a reinstall instant — so ask, and
  // default to keeping. Workspaces are never touched: they are the
  // operator's own git repositories and live wherever they keep code.
  LibDir := ExpandConstant('{localappdata}\dsky');
  if not DirExists(LibDir) then
    exit;
  if MsgBox('Also delete the downloaded operating systems and built media?' #13#10#13#10
          + LibDir + #13#10#13#10
          + 'Keep them and a reinstall starts with everything already cached.'
          + ' Your workspaces are not affected either way.',
            mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    DelTree(LibDir, True, True, True);
end;
