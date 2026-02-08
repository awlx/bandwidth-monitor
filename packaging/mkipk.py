#!/usr/bin/env python3
"""Build an opkg-compatible .ipk archive (ar format without trailing / on member names)."""
import sys
import os

def ar_header(name, size):
    """Create a BSD-style ar member header (no trailing slash)."""
    hdr = "%-16s%-12d%-6d%-6d%-8s%-10d`\n" % (name, 0, 0, 0, "100644", size)
    return hdr.encode()

def main():
    if len(sys.argv) < 3:
        print("Usage: mkipk.py output.ipk debian-binary control.tar.gz data.tar.gz", file=sys.stderr)
        sys.exit(1)

    output = sys.argv[1]
    members = sys.argv[2:]

    with open(output, 'wb') as f:
        f.write(b'!<arch>\n')
        for name in members:
            data = open(name, 'rb').read()
            f.write(ar_header(os.path.basename(name), len(data)))
            f.write(data)
            if len(data) % 2:
                f.write(b'\n')

    print(f"Created {output} ({os.path.getsize(output)} bytes)")

if __name__ == '__main__':
    main()
