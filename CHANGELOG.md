# Changelog

## [0.4.4](https://github.com/itsmostafa/typesafe-mcp/compare/v0.4.3...v0.4.4) (2026-09-23)


### Features

* **tools:** add items to evaluate one question set over many records ([aa2d57e](https://github.com/itsmostafa/typesafe-mcp/commit/aa2d57e6480fe900d65479d685d39b31829f0380))
* **tools:** add items to evaluate one question set over many records ([adada41](https://github.com/itsmostafa/typesafe-mcp/commit/adada41eed1d368491c83a5b31d2e9948c14f728))
* **tools:** warn agents off arithmetic and oversized state ([04f3f13](https://github.com/itsmostafa/typesafe-mcp/commit/04f3f1306797765866accbfe7b3f70f08cfbf802))


### Bug Fixes

* **client:** reject non-JSON success bodies on every path ([747599f](https://github.com/itsmostafa/typesafe-mcp/commit/747599f5c6441637ea7f493870dbc3d7371d4285))
* **tools:** cap the combined size of an items batch ([7066f46](https://github.com/itsmostafa/typesafe-mcp/commit/7066f46cf73775c5d5d378baab926dd07ca572dd))
* **tools:** reject unknown question types and noul criteria keys the API drops ([a6a0b4e](https://github.com/itsmostafa/typesafe-mcp/commit/a6a0b4e5f8586f97d5410c0fee0472b103126dfe))
* **tools:** reject unknown question types and noul criteria keys the API drops ([d66817c](https://github.com/itsmostafa/typesafe-mcp/commit/d66817cf7fe8b0d238ce6662a768b3a6f3481138))

## [0.4.3](https://github.com/itsmostafa/typesafe-mcp/compare/v0.4.2...v0.4.3) (2026-09-21)


### Features

* **cli:** allow TYPESAFE_BASE_URL to retarget the TypeSafe route ([e701eb8](https://github.com/itsmostafa/typesafe-mcp/commit/e701eb88307f0ee33b2d02ea92cd00bfe9f4ae4f))
* **cli:** allow TYPESAFE_BASE_URL to retarget the TypeSafe route ([c170074](https://github.com/itsmostafa/typesafe-mcp/commit/c170074a7c7c8499e46d839f657e8495e33faf26))
* **cli:** allow TYPESAFE_BASE_URL to retarget the TypeSafe route ([8ca57c9](https://github.com/itsmostafa/typesafe-mcp/commit/8ca57c974b3a349198d7b5cf54141c44e7b3a14a))


### Bug Fixes

* **cli:** validate TYPESAFE_BASE_URL before it reaches client configs ([0a1a200](https://github.com/itsmostafa/typesafe-mcp/commit/0a1a200b81140122657d750cb52f24833b3a1bac))


### Miscellaneous Chores

* release 0.4.3 ([b82458c](https://github.com/itsmostafa/typesafe-mcp/commit/b82458c89f69130de2e01af4761de603bdd406d5))

## [0.4.2](https://github.com/itsmostafa/typesafe-mcp/compare/v0.4.1...v0.4.2) (2026-09-20)


### Bug Fixes

* **client:** reject oversized responses instead of truncating them ([7565a80](https://github.com/itsmostafa/typesafe-mcp/commit/7565a80bf1e8b4778acb4f622ae187f09b0d0246))
* **setup:** stop removing MCP servers named jev ([01d7d1d](https://github.com/itsmostafa/typesafe-mcp/commit/01d7d1d87d904dbf482926fa5f843f47a2827857)), closes [#14](https://github.com/itsmostafa/typesafe-mcp/issues/14)
* stop removing MCP servers named jev ([3a98e16](https://github.com/itsmostafa/typesafe-mcp/commit/3a98e16726e8dca63eae338d3aa04655700c27ab))
* stop silently altering evidence in and out of the evaluate tool ([d700fde](https://github.com/itsmostafa/typesafe-mcp/commit/d700fde14b1c52892f21cc7644eb420cc6e81ea3))
* **tools:** forward numbers as written instead of rounding past 2^53 ([b5aefe4](https://github.com/itsmostafa/typesafe-mcp/commit/b5aefe437d2ffdff2a22aca2b3a99081fb3e1dd2))

## [0.4.1](https://github.com/itsmostafa/typesafe-mcp/compare/v0.4.0...v0.4.1) (2026-09-18)


### Features

* **tools:** reject malformed criteria before the API request ([58c8012](https://github.com/itsmostafa/typesafe-mcp/commit/58c801294f14c5ba8370ed914e1806837a73991c))
* **tools:** reject malformed criteria before the API request ([8ba84b2](https://github.com/itsmostafa/typesafe-mcp/commit/8ba84b26dd79e86a4c9c660461ea02b205992ea3))
* **tools:** warn agents against priming state with their own conclusions ([1167615](https://github.com/itsmostafa/typesafe-mcp/commit/1167615df775d9204f6883b0f5aa72ed86ee58d1))
* **tools:** warn agents against priming state with their own conclusions ([febaa39](https://github.com/itsmostafa/typesafe-mcp/commit/febaa39befaeed6d5f9045f2bb400c71eefb5e73))


### Miscellaneous Chores

* release 0.4.1 ([386726b](https://github.com/itsmostafa/typesafe-mcp/commit/386726bf74be889861bf55f7978ad2c31694309b))

## [0.4.0](https://github.com/itsmostafa/typesafe-mcp/compare/v0.3.0...v0.4.0) (2026-09-18)


### ⚠ BREAKING CHANGES

* `jev` is now `evaluate`. Installed 0.3.x binaries look for a `jev-<os>-<arch>.tar.gz` asset, so `jev update` fails against this release and cannot self-update across the rename; reinstall with install.sh, then re-run `evaluate setup mcp`. `JEV_INSTALL_DIR` is no longer read, and `go install .../cmd/jev@latest` no longer resolves.
* `jev mcp setup` is now `jev setup mcp`.

### Features

* move mcp setup under jev setup and add jev setup pi ([6a74477](https://github.com/itsmostafa/typesafe-mcp/commit/6a7447759fa2dea329064d544e3b12b1a0521fb9))
* move mcp setup under jev setup and add jev setup pi ([8d276bb](https://github.com/itsmostafa/typesafe-mcp/commit/8d276bba83af5bcc2f12539d91d26ba405325663))
* rename the cli from jev to evaluate ([7e96633](https://github.com/itsmostafa/typesafe-mcp/commit/7e9663347b824968480c6fd37a9c01591a8cb7f4))
* **setup:** drop pre-rename jev registrations on setup ([111cf15](https://github.com/itsmostafa/typesafe-mcp/commit/111cf15550f2a0bfd4209112a69bf78ed7a1fce5))
* **tools:** sharpen the evaluate tool description ([7433a7f](https://github.com/itsmostafa/typesafe-mcp/commit/7433a7f06ed43b576c850d86d1904ca6c460bde2))


### Bug Fixes

* **setup:** harden the pi extension and setup command tree ([d199f83](https://github.com/itsmostafa/typesafe-mcp/commit/d199f83e12ae8acc1346c96b19fbbd2aa76e9559))
* **setup:** honor an absolute PI_CODING_AGENT_DIR without HOME ([f29bb7a](https://github.com/itsmostafa/typesafe-mcp/commit/f29bb7acd5f323afd2a261c38a4b0d4482d54909))

## [0.3.0](https://github.com/itsmostafa/typesafe-mcp/compare/v0.2.0...v0.3.0) (2026-09-18)


### Features

* **client:** run Jev through OpenRouter as well as the TypeSafe API ([12c1aaa](https://github.com/itsmostafa/typesafe-mcp/commit/12c1aaabcd614b65477f0110375c3e80600238aa))
* **client:** run Jev through OpenRouter as well as the TypeSafe API ([1c79b89](https://github.com/itsmostafa/typesafe-mcp/commit/1c79b89a36bd78a060e246f0e51d5645a5cf0728))

## [0.2.0](https://github.com/itsmostafa/typesafe-mcp/compare/v0.1.0...v0.2.0) (2026-09-17)


### Features

* **cli:** add jev update self-update ([75ebc49](https://github.com/itsmostafa/typesafe-mcp/commit/75ebc499c6ded4e75d9e1fd2a42b3e68fc134190))
* **cli:** add jev update self-update ([6d3d519](https://github.com/itsmostafa/typesafe-mcp/commit/6d3d5190f800081da26299db18b6b51b69f514b4))

## 0.1.0 (2026-09-17)


### Continuous Integration

* **release:** publish GitHub releases with release-please ([304328f](https://github.com/itsmostafa/typesafe-mcp/commit/304328fe1401ba7d259eb5f52c0ad7da168757a3))

## Changelog
