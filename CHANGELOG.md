# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).



## [Unreleased]

## [0.5.1] - 2023-04-04

### Added

- Added the use of the runtime/default seccomp profile.
- Added support for CAPI cluster cleanup

### Changed

- Change deletion timeout to 4 hours (was 10).
- Allowed more volumes in psp to prevent seccompprofile changes from spinning pods.
- Update to Go 1.19.

## [0.5.0] - 2022-06-02

### Added

- Ignore clusters if managed via `flux`.

### Changed

- Reconcile `v1beta1` Cluster CR's.

## [0.4.0] - 2022-03-24

### Added

- Support cleaning up of App-based clusters

## [0.3.0] - 2022-01-19

### Changed

- Change deletion timeout to 10 hours (was 8) to allow a test cluster to survive a full working day.

## [0.2.1] - 2021-12-09

### Fixed

- Fix reconciling.

## [0.2.0] - 2021-12-08

### Added

- Consider cluster deletion when `keep-until` label is set.

## [0.1.0] - 2021-12-07

### Added

- Metrics
- Dry run
- Helm chart
- Init



[Unreleased]: https://github.com/giantswarm/cluster-cleaner/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/giantswarm/cluster-cleaner/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/giantswarm/cluster-cleaner/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/giantswarm/cluster-cleaner/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/giantswarm/cluster-cleaner/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/giantswarm/cluster-cleaner/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/giantswarm/cluster-cleaner/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/giantswarm/cluster-cleaner/releases/tag/v0.1.0
