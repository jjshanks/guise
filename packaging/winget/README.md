# winget packaging

These manifests publish guise to the [Windows Package Manager](https://learn.microsoft.com/windows/package-manager/)
so users can `winget install jjshanks.guise`. guise ships as a single bare
`guise.exe`, so this is a winget **portable** package (no MSI, no elevation —
consistent with guise's HKCU-only design). The portable install does **not** run
`guise --register`; registering as the default browser stays a manual one-time step
(see the project [README](../../README.md#install)).

The files here are the canonical source for the manifest and the seed for the
one-time bootstrap submission below. Once the package exists in `microsoft/winget-pkgs`,
each `v*` release auto-opens a version-bump PR via
[`.github/workflows/winget.yml`](../../.github/workflows/winget.yml).

## One-time bootstrap (first submission)

The release workflow's [`winget-releaser`](https://github.com/vedantmgoyal9/winget-releaser)
action only **updates** a package that already exists in `winget-pkgs`. The very first
version must be submitted by hand:

1. **Fork** [`microsoft/winget-pkgs`](https://github.com/microsoft/winget-pkgs) under the
   maintainer account (`jjshanks`). `winget-releaser` pushes the version-bump branch to
   this fork before opening the upstream PR.
2. **Create a classic PAT** with the `public_repo` scope and save it as the repository
   secret **`WINGET_TOKEN`** (Settings → Secrets and variables → Actions). The workflow
   fails loudly without it.
3. **Submit the first version** with
   [`wingetcreate`](https://github.com/microsoft/winget-create):

   ```powershell
   winget install wingetcreate
   wingetcreate submit packaging/winget/
   ```

   (or `wingetcreate new https://github.com/jjshanks/guise/releases/download/v0.4.0/guise.exe`
   to generate the manifests interactively). This opens the initial `winget-pkgs` PR.

After that PR is merged, the automated workflow takes over for every subsequent release.

## Updating the seed manifests

The checked-in version/hash track the latest release for the bootstrap path. The
automated PRs bump the upstream copy directly, so these seed files only need a refresh
if you ever re-bootstrap. To regenerate the `InstallerSha256`:

```powershell
(Invoke-WebRequest https://github.com/jjshanks/guise/releases/download/vX.Y.Z/guise.exe.sha256).Content
```

## Validating locally

```powershell
winget validate --manifest packaging/winget
# Installs the guise shim from the local manifest (needs the real published asset):
winget install --manifest packaging/winget
```
