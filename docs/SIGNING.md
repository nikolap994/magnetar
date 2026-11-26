# Release signing & verification

Magnetar's releases are built only by GitHub Actions from a tagged commit. The pipeline
([`.github/workflows/release.yml`](../.github/workflows/release.yml)) produces:

| Artifact | Purpose |
| --- | --- |
| `magnetar_<v>_windows_<arch>_setup.exe` | Windows installer |
| `magnetar_<v>_<arch>.deb` | Ubuntu/Debian package |
| `magnetar_<v>_<os>_<arch>.tar.gz` / `.zip` | portable builds |
| `SHA256SUMS` | checksums of every artifact |
| `SHA256SUMS.sigstore.json` | Sigstore (cosign) keyless signature bundle for the checksum file |
| GitHub attestation | SLSA build provenance linking each file to the exact workflow run and commit |

Keyless signing means there is no private key to leak: the signature is bound to this repository's release workflow
identity through GitHub's OIDC token and recorded in the public [Rekor](https://search.sigstore.dev/) transparency log.

## Verify a download

```sh
# 1) Provenance (easiest) — requires the GitHub CLI
gh attestation verify magnetar_0.1.0_windows_amd64_setup.exe --repo nikolap994/magnetar

# 2) Signature of the checksum list — requires cosign
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github.com/nikolap994/magnetar/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# 3) Your file against the signed checksums
sha256sum --ignore-missing -c SHA256SUMS            # Linux
Get-FileHash .\magnetar_0.1.0_windows_amd64_setup.exe  # Windows (compare with SHA256SUMS)
```

## Windows Authenticode (SmartScreen)

Sigstore proves where a binary came from, but Windows SmartScreen only recognises Authenticode signatures. Open-source
projects can get Authenticode signing for free through the [SignPath Foundation](https://signpath.org/):

1. Apply at <https://signpath.org/apply> with the repository URL (requirements: OSI license ✔, public CI builds ✔,
   no proprietary binaries ✔).
2. Once accepted, install the SignPath GitHub App on the repository and add these to the repo settings:
   - secret `SIGNPATH_API_TOKEN`
   - variables `SIGNPATH_ORGANIZATION_ID`, `SIGNPATH_PROJECT_SLUG`, `SIGNPATH_POLICY_SLUG`
3. Add a signing step to the release workflow using
   [`signpath/github-action-submit-signing-request`](https://github.com/SignPath/github-action-submit-signing-request):
   sign `magnetar.exe` after `make binaries`, then sign the setup `.exe` after `make installer`, before checksums are
   generated.

Until then Windows may show "Windows protected your PC" on first launch; click **More info → Run anyway**, after
verifying the download as shown above.
