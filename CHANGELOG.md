# Changelog

## [0.9.0](https://github.com/sassdavid/tfcensus/compare/v0.8.0...v0.9.0) (2026-11-17)


### Features

* **docs:** add social cards, icons and the accessibility checks (M26) ([#93](https://github.com/sassdavid/tfcensus/issues/93)) ([fdacb5f](https://github.com/sassdavid/tfcensus/commit/fdacb5f2008c549d7fe7a4e71d23651dda2cd331))

## [0.8.0](https://github.com/sassdavid/tfcensus/compare/v0.7.0...v0.8.0) (2026-11-17)


### Features

* **docs:** add the guides, concepts and help pages (M25) ([e676d37](https://github.com/sassdavid/tfcensus/commit/e676d37e271b244c4860be65c728c02e233c2179))


### Bug Fixes

* **docs:** take related link titles from the frontmatter title ([#90](https://github.com/sassdavid/tfcensus/issues/90)) ([2a36b04](https://github.com/sassdavid/tfcensus/commit/2a36b0498d50e4e4e72e896f7014ff78a9118c4b))
* **doctor:** drop the advice to promote a Resource Explorer aggregator index ([e676d37](https://github.com/sassdavid/tfcensus/commit/e676d37e271b244c4860be65c728c02e233c2179))
* **scan:** read states again when state decryption is turned on ([e676d37](https://github.com/sassdavid/tfcensus/commit/e676d37e271b244c4860be65c728c02e233c2179))
* **scan:** use path-style S3 addressing for bucket settings under a custom endpoint ([e676d37](https://github.com/sassdavid/tfcensus/commit/e676d37e271b244c4860be65c728c02e233c2179))
* **scan:** warn about encrypted states in scan and whois ([e676d37](https://github.com/sassdavid/tfcensus/commit/e676d37e271b244c4860be65c728c02e233c2179))

## [0.7.0](https://github.com/sassdavid/tfcensus/compare/v0.6.0...v0.7.0) (2026-11-17)


### Features

* **docs:** add getting started and the generated tool reference ([#89](https://github.com/sassdavid/tfcensus/issues/89)) ([2bcb6ba](https://github.com/sassdavid/tfcensus/commit/2bcb6ba0448d7620a27156db33847a193037bc75))


### Bug Fixes

* **httpx:** redact X-Api-Key in trace logs ([#87](https://github.com/sassdavid/tfcensus/issues/87)) ([70c00c9](https://github.com/sassdavid/tfcensus/commit/70c00c9a0b1c589edc7fb3b900d630363f93e57c))

## [0.6.0](https://github.com/sassdavid/tfcensus/compare/v0.5.0...v0.6.0) (2026-11-17)


### Features

* **docs:** generate the CLI and reference pages from code ([#86](https://github.com/sassdavid/tfcensus/issues/86)) ([48d24f7](https://github.com/sassdavid/tfcensus/commit/48d24f7ca67a14938bc2a90edfe499d93467758b))


### Bug Fixes

* **deps:** update aws-sdk-go-v2 monorepo ([#83](https://github.com/sassdavid/tfcensus/issues/83)) ([6b4bac2](https://github.com/sassdavid/tfcensus/commit/6b4bac2d94dd1fcb50fe104f7d4d76d702a17918))
* **deps:** update module github.com/carapace-sh/carapace to v1.18.2 ([#81](https://github.com/sassdavid/tfcensus/issues/81)) ([0b394db](https://github.com/sassdavid/tfcensus/commit/0b394db5e1e47459fde764a28969a36c0ada9559))

## [0.5.0](https://github.com/sassdavid/tfcensus/compare/v0.4.0...v0.5.0) (2026-11-17)


### Features

* **docs:** build the docs site foundation (M22) ([#77](https://github.com/sassdavid/tfcensus/issues/77)) ([12bba52](https://github.com/sassdavid/tfcensus/commit/12bba5249b57df79c1d8e66b3f0e74dec664157e))

## [0.4.0](https://github.com/sassdavid/tfcensus/compare/v0.3.0...v0.4.0) (2026-11-16)


### Features

* **inventory:** drop stale Resource Explorer records and skip Kubernetes objects ([#64](https://github.com/sassdavid/tfcensus/issues/64)) ([f939ffc](https://github.com/sassdavid/tfcensus/commit/f939ffc2ff0e3e4fc123b4ed5745159dda3a147b))
* **inventory:** list VPC Lattice through Cloud Control by default ([#65](https://github.com/sassdavid/tfcensus/issues/65)) ([84a9157](https://github.com/sassdavid/tfcensus/commit/84a915749a4a072e11d51fe2785873cf7fa51a12))
* **inventory:** read ENI owners and replace requester-managed-eni ([#62](https://github.com/sassdavid/tfcensus/issues/62)) ([b00be88](https://github.com/sassdavid/tfcensus/commit/b00be88fb5870b959331e19b9770b8078fcd33af))
* **ownership:** add the controller, AWS default and AWS service rules ([#66](https://github.com/sassdavid/tfcensus/issues/66)) ([6e08ffe](https://github.com/sassdavid/tfcensus/commit/6e08ffe748c337fae2d3ac264b3bd0f5ebcdd74a))
* **scan:** list the AWS Config types scan doesn't map ([#67](https://github.com/sassdavid/tfcensus/issues/67)) ([23c57e4](https://github.com/sassdavid/tfcensus/commit/23c57e447485f61618319464c21770ffc0f6fa83))
* **scan:** pass a parent's category down to its children ([#61](https://github.com/sassdavid/tfcensus/issues/61)) ([bebf30c](https://github.com/sassdavid/tfcensus/commit/bebf30c28af2825779ea2ad7f10ea5aa2cb2609e))
* **typemap:** curate the validation account's types and match policy attachments ([#63](https://github.com/sassdavid/tfcensus/issues/63)) ([3340277](https://github.com/sassdavid/tfcensus/commit/3340277c59f0f9756d18b70e0650980aec4368e3))


### Bug Fixes

* **inventory:** list IAM principals with their attachments ([#71](https://github.com/sassdavid/tfcensus/issues/71)) ([2a4f9c4](https://github.com/sassdavid/tfcensus/commit/2a4f9c44bfc651515ceaf44137c0048a9e633172))
* **inventory:** list VPC Lattice VPC associations per service network ([#70](https://github.com/sassdavid/tfcensus/issues/70)) ([5179288](https://github.com/sassdavid/tfcensus/commit/5179288b55cac4a0c1594762e01ad3102bd40dd3))
* **ownership:** treat ElastiCache's system IAM user as an AWS default ([#74](https://github.com/sassdavid/tfcensus/issues/74)) ([ddf4565](https://github.com/sassdavid/tfcensus/commit/ddf4565930bdc47818ade8b629e7b5b21f7191d1))
* **scan:** classify the false positives accounts 2 and 3 found ([#73](https://github.com/sassdavid/tfcensus/issues/73)) ([2383fb2](https://github.com/sassdavid/tfcensus/commit/2383fb23eee9606df94b40f77a0f9d20f8bf3605))
* **scan:** classify the false positives the v0.4.0 rerun found ([#72](https://github.com/sassdavid/tfcensus/issues/72)) ([94a1492](https://github.com/sassdavid/tfcensus/commit/94a1492538ace445012fd5974c924bb458368073))

## [0.3.0](https://github.com/sassdavid/tfcensus/compare/v0.2.0...v0.3.0) (2026-11-16)


### Features

* **cli:** add cache info|clear, doctor versions and account filters ([#50](https://github.com/sassdavid/tfcensus/issues/50)) ([eb609a3](https://github.com/sassdavid/tfcensus/commit/eb609a3e8d81e77ff9c182fdad459c426b769958))
* **config:** add config init and stop the config search at the git root ([#49](https://github.com/sassdavid/tfcensus/issues/49)) ([8db5196](https://github.com/sassdavid/tfcensus/commit/8db51961f6f1a11757aa1fb75fb22198491701d9))
* **inventory:** add the Config aggregator fast path ([#51](https://github.com/sassdavid/tfcensus/issues/51)) ([89d5717](https://github.com/sassdavid/tfcensus/commit/89d571732a25f1a86d886f022b957edf021c4863))
* **mcp:** add triage_drift and explain_unmanaged prompts ([#44](https://github.com/sassdavid/tfcensus/issues/44)) ([396a184](https://github.com/sassdavid/tfcensus/commit/396a1842ab471b18db5c7c1fa089ddbca799e573))
* **move:** add move suggest with its own strict 1:1 engine ([#55](https://github.com/sassdavid/tfcensus/issues/55)) ([3581f18](https://github.com/sassdavid/tfcensus/commit/3581f185259ed512288ae4fb17f351a500097a82))
* **move:** support non-default workspaces of s3 backends ([#53](https://github.com/sassdavid/tfcensus/issues/53)) ([63ffd06](https://github.com/sassdavid/tfcensus/commit/63ffd0671f1601ec4db0c95255cb32094bba68f9))
* **scan:** read CloudFormation stacks as a second IaC source ([#52](https://github.com/sassdavid/tfcensus/issues/52)) ([d65c9e1](https://github.com/sassdavid/tfcensus/commit/d65c9e12172be9192c3bd3c0798b8459f4e88ced))
* **tfstate:** read OpenTofu encrypted state through tofu show -json ([#54](https://github.com/sassdavid/tfcensus/issues/54)) ([80df9ff](https://github.com/sassdavid/tfcensus/commit/80df9ff3bf9d249d465e57b5f3f83af01b6f314e))


### Bug Fixes

* **aws:** follow ADR-0005 retries, SDK logging and S3 best practices ([#48](https://github.com/sassdavid/tfcensus/issues/48)) ([8bc1e85](https://github.com/sassdavid/tfcensus/commit/8bc1e85915ae460b0935584cddad7216f585ac78))
* **deps:** update aws-sdk-go-v2 monorepo ([#41](https://github.com/sassdavid/tfcensus/issues/41)) ([7ecfe87](https://github.com/sassdavid/tfcensus/commit/7ecfe8766a38155997cd638f9edeaff3a54efd5c))
* **deps:** update aws-sdk-go-v2 monorepo ([#47](https://github.com/sassdavid/tfcensus/issues/47)) ([c38c50b](https://github.com/sassdavid/tfcensus/commit/c38c50b9ac8e837d42d03b27a8b15df4b6a757e0))
* **deps:** update module gopkg.in/dnaeon/go-vcr.v4 to v4.0.8 ([#39](https://github.com/sassdavid/tfcensus/issues/39)) ([4bd631a](https://github.com/sassdavid/tfcensus/commit/4bd631a53cc945a75715eed3b35c8df168adf175))

## [0.2.0](https://github.com/sassdavid/tfcensus/compare/v0.1.0...v0.2.0) (2026-11-13)


### Features

* add --out, Markdown truncation and the GitHub PR comment (M13) ([#26](https://github.com/sassdavid/tfcensus/issues/26)) ([9f3382e](https://github.com/sassdavid/tfcensus/commit/9f3382eb8c6e7260a7e9db876b76e319e7771eda))
* add shell completion, carapace shells and man pages (M12) ([#23](https://github.com/sassdavid/tfcensus/issues/23)) ([04b637d](https://github.com/sassdavid/tfcensus/commit/04b637dcba80fc79443585ed97575b9302410d89))

## 0.1.0 (2026-11-13)


### Features

* add inventory layers, the typemap and matching (M5) ([#10](https://github.com/sassdavid/tfcensus/issues/10)) ([74a54d3](https://github.com/sassdavid/tfcensus/commit/74a54d331a3e0b7a6bb3a8c14d82515b1f6f7b79))
* add notify, report html and a read-only mcp server (M10) ([#17](https://github.com/sassdavid/tfcensus/issues/17)) ([4ca5c88](https://github.com/sassdavid/tfcensus/commit/4ca5c8879a28cc16f509c091371469dc3c09794b))
* add ownership rules, the scan report and Cloud Control (M6) ([#11](https://github.com/sassdavid/tfcensus/issues/11)) ([278e730](https://github.com/sassdavid/tfcensus/commit/278e730d277045f1b20286dcc644e61b4f365f73))
* add tfcensus audit backend (M4) ([#9](https://github.com/sassdavid/tfcensus/issues/9)) ([fe032fb](https://github.com/sassdavid/tfcensus/commit/fe032fb508ecc9da0e83da36881eaa253ba2998d))
* add tfcensus drift (M7) ([#12](https://github.com/sassdavid/tfcensus/issues/12)) ([f4efffc](https://github.com/sassdavid/tfcensus/commit/f4efffc88a16e5d0e4635268e3fd103ff1aa12be))
* add tfcensus move with surgery, native and rollback (M9) ([#15](https://github.com/sassdavid/tfcensus/issues/15)) ([d22f261](https://github.com/sassdavid/tfcensus/commit/d22f2610ea438525f4ea5f5a214bfbd5a86620b8))
* add tfcensus whois and codify (M8) ([#13](https://github.com/sassdavid/tfcensus/issues/13)) ([e7618f2](https://github.com/sassdavid/tfcensus/commit/e7618f22071776ba75ff5475350abc1a47affec8))
* add the shared MCP kit from relgap ([#16](https://github.com/sassdavid/tfcensus/issues/16)) ([e77ed87](https://github.com/sassdavid/tfcensus/commit/e77ed8725395cfb32846c17d188bd3222979a89c))
* discover terragrunt units and their state backends (M1) ([#5](https://github.com/sassdavid/tfcensus/issues/5)) ([125f090](https://github.com/sassdavid/tfcensus/commit/125f090b5a50ead9098d12370dad67b22071d09f))
* load AWS accounts and add tfcensus doctor (M2) ([#7](https://github.com/sassdavid/tfcensus/issues/7)) ([9b48a62](https://github.com/sassdavid/tfcensus/commit/9b48a62095a5bf0542d7178a1948b330e9d4ef06))
* read unit state from S3 and add tfcensus inventory (M3) ([#8](https://github.com/sassdavid/tfcensus/issues/8)) ([7fbc903](https://github.com/sassdavid/tfcensus/commit/7fbc903731fe66acfab31c02ac345271218af18c))
* set up the CLI foundation (M0) ([#1](https://github.com/sassdavid/tfcensus/issues/1)) ([4797bd7](https://github.com/sassdavid/tfcensus/commit/4797bd71c07564e5382755f3811c2eb46e5cda12))


### Bug Fixes

* start releases at 0.1.0 ([#3](https://github.com/sassdavid/tfcensus/issues/3)) ([2d389a0](https://github.com/sassdavid/tfcensus/commit/2d389a099978e328cec62c9f2dcd142c1e05bd2b))
