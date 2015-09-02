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
