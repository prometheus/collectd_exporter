## master / unreleased

## 0.6.0 / 2023-12-08

* [CHANGE] Update minimum Go version to 1.20 (#145)
* [FEATURE] Add TLS authentication using official exporter-toolkit (#113)
* [BUGFIX] Restore version command line flag and fix build info (#104)
* [FEATURE] Added s390x support to docker image (#98)

## 0.5.0 / 2020-05-08

* [CHANGE] Switch logging to go-kit (#85)

## 0.4.0 / 2018-01-22

* [CHANGE] Append `_total` to metric name of collectd Counter and Derive types (#41)
* [CHANGE] Update flag invocation (#47)
* [FEATURE] Support collectd types.db file (#35)
* [FEATURE] Set receive buffer on UDP socket receiving collectd binary data (#36)
* [BUGFIX] Sanitize metric name output (#51)

## 0.3.1 / 2016-06-06

Re-release of 0.3.0 without functional changes due to release process issues.

## 0.3.0 / 2016-05-24

BREAKING CHANGES:

* To disambiguate metric names, the plugin name will no longer be omitted in most cases (#23)
* The structure of the tarball has changed, see prometheus/docs#447

All changes:

* [CHANGE] Logs now have the common Prometheus format
* [CHANGE] Only omit the plugin name if it is equal to the collectd type
* [ENHANCEMENT] The `write_http` example is updated to match the current collectd write_http plugin config format.
* [ENHANCEMENT] Now built with Go 1.6.2.
* [ENHANCEMENT] New release and binary build process.
* [CHANGE] Reorganised release binary tarballs.

## 0.2.0 / 2015-09-03

BREAKING CHANGES:

* Flag names have been changed to emulate the config options of collectd's
  network plugin. Run `./collectd_exporter -h` to see up-to-date flag names.

All changes:

* [FEATURE] Implement support for collectd's binary protocol.
* [FEATURE] Add server startup logging.
* [CHANGE] Change flag names to reflect collectd network plugin config options.
* [ENHANCEMENT] Documentation updates and cleanups.
* [ENHANCEMENT] Add unit tests for generated metric names and labels.
* [ENHANCEMENT] New Dockerfile using `alpine-golang-make-onbuild` base image.
* [CLEANUP] Rewrite based on the official collectd Go package.
* [CLEANUP] Update `Makefile.COMMON` from https://github.com/prometheus/utils.

## 0.1.0 / 2015-03-28

Initial release.
