#!/bin/sh
set -e

# Complete the transition from the old dpkg-managed conffile. Modified
# administrator configuration is retained as .dpkg-bak by the helper.
if command -v dpkg-maintscript-helper >/dev/null 2>&1 &&
    dpkg-maintscript-helper supports rm_conffile; then
    dpkg-maintscript-helper rm_conffile /etc/bandwidth-monitor/env -- "$@"
fi

# Create working directory for GeoIP databases
mkdir -p /var/lib/bandwidth-monitor
chmod 0755 /var/lib/bandwidth-monitor

# Seed configuration only on first install. It is deliberately not a package
# conffile, so upgrades never prompt or replace administrator settings.
config_dir=/etc/bandwidth-monitor
config_file=$config_dir/env
example_file=/usr/share/bandwidth-monitor/env.example
mkdir -p "$config_dir"
if [ ! -e "$config_file" ]; then
    if [ -e "$config_file.dpkg-bak" ]; then
        mv "$config_file.dpkg-bak" "$config_file"
    else
        install -m 0600 "$example_file" "$config_file"
    fi
fi

# Always enable and (re)start the service.  The old package's prerm may
# have disabled it during upgrade; we cannot reliably detect this because
# dpkg sometimes passes an empty old-version to postinst.
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
    systemctl enable bandwidth-monitor 2>/dev/null || true
    systemctl restart bandwidth-monitor 2>/dev/null || true
fi

echo ""
echo "bandwidth-monitor installed."
echo "  Edit /etc/bandwidth-monitor/env with your settings"
echo "  (Optional) Place GeoLite2-*.mmdb in /var/lib/bandwidth-monitor/"
echo ""
