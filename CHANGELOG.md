# Changelog

All notable changes to FatTrace. Format follows [Keep a Changelog](https://keepachangelog.com/); versions follow semver.

## [Unreleased]
### Added
- Module path is now `github.com/Xeayy/FatTrace`; `go install github.com/Xeayy/FatTrace/cmd/fat@latest` works.
- `fat --version` / `fat -v` are accepted in addition to `fat version`.
- `docs/release-template.md`; the release workflow can be started manually and tolerates a missing release body.
- ARM64 installer (`FatTrace-Setup-<ver>-windows-arm64.exe`); `release.ps1` builds both architectures.
### Changed
- `fat version` falls back to Go build info, so `go install` builds report the module version instead of `0.1.0-dev`.
- CI and release builds use the latest Go 1.27.x patch (1.27.4+) to pick up `net/http` security fixes.
- CI runs `govulncheck` and builds every package.

## [1.3.1] - 2026-11-12
### Added
- Windows installer `FatTrace-Setup-<ver>-windows-amd64.exe` (Inno Setup): wizard install, per-user by default without admin rights, desktop & Start Menu shortcuts, uninstaller, launch-after-install with tray + dashboard.
### Changed
- Icon: dedicated simplified glyph for 16/24/32 px (mixed-size ICO); ≥48 px uses the master artwork. Fixes blurry tray/taskbar icons.

## [1.3.0] - 2026-11-12
### Added
- Official icon ("measuring tape around a disk") embedded in `fat.exe` / `fatgui.exe`, tray, shortcut and web favicon.
- `GET /favicon.ico`.
### Notes
- Repository made public under MIT; history sanitized.

## [1.2.0] - 2026-11-11
### Added
- Unexplained-growth browser (`#/unexplained`) with pagination/filter/sort.
- Rule assistant: preview candidate rule hits and generate a `rules.yaml` draft (read-only; user confirms).
- `GET /api/unexplained`, `GET /api/rules/preview`, `GET /api/categories`.

## [1.1.0] - 2026-11-11
### Added
- `fatgui.exe`: GUI-subsystem launcher, double-click starts tray + dashboard with no console flash.
- `scripts/pecheck` to verify PE subsystems.

## [1.0.2] - 2026-11-11
### Added
- Volume-gap attribution: distinguishes scan-invisible system areas from genuine file-level gaps when the volume check warns.
### Fixed
- Negative byte sizes now formatted with units (e.g. `-133.20 GiB`).

## [1.0.1] - 2026-11-10
### Added
- Collection health alerts (failed / consecutive-failure / stale data) in doctor, tray and Web.
- `alerts.stale_hours` (default 36h).

## [1.0.0] - 2026-11-08
### Added
- First stable release: NTFS snapshot scanner (FileID), diff engine with move/rename attribution, rule explanation (75 rules), single-file HTML report, read-only local Web UI, tray, scheduled daily/weekly collection, retention & alerts, `fat setup` onboarding.

[1.3.1]: https://github.com/Xeayy/FatTrace/releases/tag/v1.3.1
[1.3.0]: https://github.com/Xeayy/FatTrace/releases/tag/v1.3.0
[1.2.0]: https://github.com/Xeayy/FatTrace/releases/tag/v1.2.0
[1.1.0]: https://github.com/Xeayy/FatTrace/releases/tag/v1.1.0
[1.0.2]: https://github.com/Xeayy/FatTrace/releases/tag/v1.0.2
[1.0.1]: https://github.com/Xeayy/FatTrace/releases/tag/v1.0.1
[1.0.0]: https://github.com/Xeayy/FatTrace/releases/tag/v1.0.0
