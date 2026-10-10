## Changelog
## Highlights
- **Update from the web UI.** On v1.31.0, click **Install update** and qui updates and restarts itself. The old version stays as a `.bak` file for a rollback. Docker users pull the image as always.
- **Steady torrent list on phones.** It no longer bounces at the end of the list.
- **Labels for every torrent state.** For example `(F) Fetching Metadata` instead of `forcedMetaDL`.
- **Windows, updating from 1.30?** v1.30.0 does not add the new tray app by itself. Copy `qui-tray.exe` from the zip into the folder with `qui.exe` once unless you did already. After that, each update replaces both files.
### Bug Fixes
* 3c4b86129e146293a62aff7b4be524e17dd08af9: fix(mobile): stop the torrent list from jumping when the footer nav hides (#3067) (@s0up4200)
* 97ddbfda05a4b1b69b32acecefa274c692bd8919: fix(web): take t() fallbacks from the locale files instead of hardcoded English (#2837) (@ColinHebert)
### Other Changes
* 6fc21293de309af62a3ead1ede94e45d717aa2b1: chore(github): add CODEOWNERS (#3069) (@s0up4200)
* 3cd6f2954549b2adf5f4fdf724788fb06396a7cb: ci(triage): triage only bug discussions automatically (#3075) (@s0up4200)
* 4541a4e7ed8639b341a9371ba76aa4d06b04ee91: docs: explain the missing qui-tray.exe after an update from 1.30 and add a generic webhook example (#3065) (@s0up4200)
* e87b8aadf6f9bafd38c3f2f447e78d50453be7ae: docs: prefer a proven library for spec-defined logic (#3068) (@s0up4200)

**Full Changelog**: https://github.com/autobrr/qui/compare/v1.31.0...v1.31.1

## Docker images

- `docker pull ghcr.io/autobrr/qui:v1.31.1`
- `docker pull ghcr.io/autobrr/qui:latest`


## What to do next?

- Join our [Discord server](https://discord.autobrr.com/qui)

Thank you for using qui!


