---

## Verifying this release

Every archive, `checksums.txt`, per-archive CycloneDX SBOM, and container image below is signed with Sigstore cosign in keyless mode. The signing identity is the Kahi release workflow pinned to this tag:

```
--certificate-identity    https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/{{ .Tag }}
--certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Full instructions: https://github.com/kahiteam/kahi/blob/main/docs/verifying-releases.md

Requires cosign v3.0.0 or later.

### Quick example: archive

```bash
cosign verify-blob \
  --certificate-identity    "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/{{ .Tag }}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle kahi_{{ .Version }}_linux_amd64.tar.gz.sigstore.json \
  kahi_{{ .Version }}_linux_amd64.tar.gz
```

### Quick example: container image

```bash
cosign verify \
  --certificate-identity    "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/{{ .Tag }}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/kahiteam/kahi:{{ .Version }}

cosign verify-attestation --type cyclonedx \
  --certificate-identity    "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/{{ .Tag }}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/kahiteam/kahi:{{ .Version }}

cosign verify-attestation --type slsaprovenance1 \
  --certificate-identity    "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/{{ .Tag }}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/kahiteam/kahi:{{ .Version }}
```
