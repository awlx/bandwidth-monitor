package bandwidthtop

import (
	"strings"

	"bandwidth-monitor/version"
)

const userAgentProduct = "bandwidth-top/"

func UserAgent() string {
	value := version.Version
	if value == "" || value == "dev" {
		if version.Commit == "" || version.Commit == "unknown" || !validHTTPToken(version.Commit) {
			return userAgentProduct + "devel"
		}
		value = "0.0.0-git." + version.Commit
	}
	if !validHTTPToken(value) {
		return userAgentProduct + "devel"
	}
	return userAgentProduct + value
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	const separators = "()<>@,;:\\\"/[]?={} \t"
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e || strings.IndexByte(separators, value[i]) >= 0 {
			return false
		}
	}
	return true
}
