#!/bin/sh
set -e

if command -v dpkg-maintscript-helper >/dev/null 2>&1 &&
    dpkg-maintscript-helper supports rm_conffile; then
    dpkg-maintscript-helper rm_conffile /etc/bandwidth-monitor/env -- "$@"
fi
