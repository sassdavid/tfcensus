# ADR-0012: Third-party notices in every release archive

- Status: accepted (2026-11-10)

## Context

A release binary contains code from every Go module it imports and from the Go standard library.
Their licenses attach conditions to redistributing that code, binaries included:

| License | Condition on binary redistribution |
|---|---|
| MIT | "The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software." |
| BSD-2-Clause, BSD-3-Clause (Go itself, `golang.org/x/*`, pflag) | "Redistributions in binary form must reproduce the above copyright notice, this list of conditions and the following disclaimer in the documentation and/or other materials provided with the distribution." |
| Apache-2.0 (cobra, yaml v4, jsonschema) | §4: give recipients a copy of the license and keep the attribution notices of any NOTICE file. |
| MPL-2.0 (hashicorp modules) | §3.2: the source of the covered files must be available, and recipients of the binary must be told how to get it. |

relgap also embeds data from other projects: snapshots of the mise and aqua registries (MIT).

Until this ADR, the archives held only our own `LICENSE` and `README.md`.

## Decision

- `scripts/licenses.sh` (shared by both repos) runs `go-licenses` v2 for each release platform and
  merges the results into `third_party_licenses/`. Per-platform runs matter because some modules are
  compiled in for one OS only (cobra imports mousetrap on Windows). The directory holds:
  - the LICENSE, NOTICE, COPYING and PATENTS files of each module;
  - Go's `LICENSE` and `PATENTS` from the GOROOT of the toolchain that built the release;
  - every tracked `*.LICENSE` file, the convention for embedded third-party data, under `data/`;
  - `THIRD_PARTY_NOTICES.md`, which lists each module with its version, license, and the source zip
    on the Go module mirror (`https://proxy.golang.org/<module>/@v/<version>.zip`).
- goreleaser runs the script as a `before` hook and adds the directory to every archive. tfcensus
  also copies it into the container image under `/usr/share/doc/tfcensus/`.
- `mise run lint:licenses` (part of `mise run check` and the CI lint job) fails when a dependency
  has a forbidden, restricted (GPL-style) or unrecognized license on any release platform. The CI
  snapshot job fails when an archive lacks the notices.
- MPL-2.0 source is linked, not copied. Mozilla's MPL 2.0 FAQ (Q8) says that when you distribute an
  executable containing unmodified MPL code, "you must inform the recipients where they can get the
  source for the MPLed code". The module mirror serves the exact version, and sum.golang.org pins
  its checksum. If we ever modify an MPL file, its source must ship or be published by us.

## Alternatives rejected

- Copy the full source of MPL modules into the archives (go-licenses' default). Allowed, but not
  required for unmodified code, and it grows with each hashicorp module: hcl/v2 alone would add
  about 3 MB to each tfcensus archive.
- Embed the notices in the binary behind a `licenses` command, as gh does. That only matters for
  bare-binary installs; `go install` users build from source and get notices from the module cache.
  Revisit if a distribution channel ships the binary without the archive.
- Hand-maintained `THIRD_PARTY_NOTICES`. It goes stale with every Renovate update.

## Consequences

- Each archive grows by about 90 KB of text before compression.
- A dependency with a license go-licenses doesn't recognize blocks the build until someone checks
  it. That is intended.
- Embedded data from another project needs its license committed next to it as `<name>.LICENSE`.

## Sources

- MIT, BSD and Apache-2.0 texts: https://opensource.org/licenses
- MPL 2.0 §3.2 and §3.3: https://www.mozilla.org/en-US/MPL/2.0/
- MPL 2.0 FAQ, Q8: https://www.mozilla.org/en-US/MPL/2.0/FAQ/
- go-licenses: https://github.com/google/go-licenses
- The same pattern in aqua and gh: https://zenn.dev/shunsuke_suzuki/articles/go-oss-licenses and
  `script/licenses` in cli/cli
