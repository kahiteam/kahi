# Verifying Releases

Every Kahi container release is signed with [Sigstore cosign](https://docs.sigstore.dev/) in keyless mode. The container image on `ghcr.io/kahiteam/kahi` is signed by digest and carries a CycloneDX SBOM attestation and a SLSA build-provenance attestation. GoReleaser archives, `checksums.txt`, and per-archive CycloneDX SBOMs published on the GitHub Release each ship with a matching `.sigstore.json` bundle (single-file Sigstore bundle containing signature, Fulcio certificate, and Rekor inclusion proof).

Keyless signing means no long-lived keys exist at any point. A short-lived certificate is issued by [Fulcio](https://docs.sigstore.dev/fulcio/overview/) for the GitHub Actions OIDC token used by the release workflow, and the signature is recorded in the [Rekor](https://docs.sigstore.dev/rekor/overview/) transparency log. Verification checks that the signature came from the Kahi release workflow running against a specific tag ref.

## Prerequisites

- [cosign](https://docs.sigstore.dev/cosign/installation/) v3.0.0 or later (required for `--bundle` verification of the single-file Sigstore bundle)
- [cyclonedx-cli](https://github.com/CycloneDX/cyclonedx-cli) (optional, for SBOM validation)

## Identity and issuer

Every `cosign verify*` command below requires two flags:

```
--certificate-identity   https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/<tag>
--certificate-oidc-issuer https://token.actions.githubusercontent.com
```

`<tag>` is the exact git tag you are verifying (for example `v0.1.0`). The identity is pinned to the tag ref; no branch fallback is accepted.

In the examples that follow, shell variables pin the tag once:

```bash
TAG=v0.1.0
VERSION=${TAG#v}
IDENTITY="https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/${TAG}"
ISSUER="https://token.actions.githubusercontent.com"
```

## Verifying an archive

Download the archive together with its matching `.sigstore.json` bundle from the release assets, then:

```bash
ASSET=kahi_${VERSION}_linux_amd64.tar.gz

cosign verify-blob \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  --bundle "${ASSET}.sigstore.json" \
  "${ASSET}"
```

`Verified OK` indicates success. Any other output, or a non-zero exit, means the archive should be treated as untrusted.

The `.sigstore.json` file is a single Sigstore bundle containing the signature, the Fulcio-issued certificate, and the Rekor transparency-log inclusion proof. Because the proof is embedded, you can verify offline by adding `--offline` to the command above.

## Verifying checksums.txt

```bash
cosign verify-blob \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  --bundle checksums.txt.sigstore.json \
  checksums.txt
```

Once `checksums.txt` itself is verified, you can use it to verify individual archives with `sha512sum -c checksums.txt` without re-running cosign on each one.

## Verifying the container image

Verify the image signature:

```bash
cosign verify \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  ghcr.io/kahiteam/kahi:${VERSION}
```

Verify the CycloneDX SBOM attestation:

```bash
cosign verify-attestation \
  --type cyclonedx \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  ghcr.io/kahiteam/kahi:${VERSION}
```

Verify the SLSA provenance attestation:

```bash
cosign verify-attestation \
  --type slsaprovenance1 \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  ghcr.io/kahiteam/kahi:${VERSION}
```

All three should succeed for a properly published release. The release workflow itself runs the same three verifications before the GitHub Release is promoted out of draft state, so a public release that fails any of these should not be possible.

## Consuming the SBOM

Each archive has a matching CycloneDX 1.5 JSON SBOM at `<archive>.cdx.json`, with its own `<archive>.cdx.json.sigstore.json` bundle. Verify the SBOM with the same `cosign verify-blob --bundle` flow as the archive itself; once verified, validate the structure:

```bash
cosign verify-blob \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  --bundle kahi_${VERSION}_linux_amd64.tar.gz.cdx.json.sigstore.json \
  kahi_${VERSION}_linux_amd64.tar.gz.cdx.json

cyclonedx-cli validate --input-file kahi_${VERSION}_linux_amd64.tar.gz.cdx.json
```

For the container image SBOM, download the attestation (legacy `cosign download sbom` is deprecated because our SBOM is attached as an in-toto attestation, not a separate OCI artifact):

```bash
cosign download attestation \
  --predicate-type=https://cyclonedx.org/bom \
  ghcr.io/kahiteam/kahi:${VERSION} \
  | jq -r '.payload | @base64d | fromjson | .predicate' \
  > image-sbom.cdx.json

cyclonedx-cli validate --input-file image-sbom.cdx.json
```

To convert a CycloneDX SBOM to SPDX:

```bash
cyclonedx-cli convert \
  --input-file kahi_${VERSION}_linux_amd64.tar.gz.cdx.json \
  --output-format spdxjson
```

## Pinning by digest

For production use, pin the container image by digest rather than tag:

```bash
DIGEST=$(cosign triangulate --type digest ghcr.io/kahiteam/kahi:${VERSION})

cosign verify \
  --certificate-identity "${IDENTITY}" \
  --certificate-oidc-issuer "${ISSUER}" \
  "${DIGEST}"
```

Digest verification is immune to tag reuse or mutation.
