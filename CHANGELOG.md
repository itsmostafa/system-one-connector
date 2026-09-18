# Changelog

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
