BINARY=bandwidth-monitor
GATEWAY_BINARY=apns-gateway
INSTALL_DIR=/opt/bandwidth-monitor
SERVICE_FILE=bandwidth-monitor.service

# Version injection: use git tag if on a tag, otherwise short commit hash.
GIT_VERSION := $(shell git describe --tags --exact-match 2>/dev/null)
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS_VERSION := -X bandwidth-monitor/version.Commit=$(GIT_COMMIT)
ifneq ($(GIT_VERSION),)
  LDFLAGS_VERSION += -X bandwidth-monitor/version.Version=$(GIT_VERSION)
endif

GEOLITE2_CITY=GeoLite2-City.mmdb
GEOLITE2_ASN=GeoLite2-ASN.mmdb
GEOLITE2_CITY_URL=https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-City.mmdb
GEOLITE2_ASN_URL=https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-ASN.mmdb

.PHONY: build build-gateway run clean geoip install uninstall

build:
	go build -ldflags="$(LDFLAGS_VERSION)" -o $(BINARY) .

build_stripped:
	# Build and strip the binary.
	go build -ldflags="-s -w $(LDFLAGS_VERSION)" -o $(BINARY) .

# The Live Activity push relay — a separate, minimal binary meant to run on any ordinary host (not
# the router). See apnsgateway/main.go and docs/apns-gateway.md.
build-gateway:
	go build -ldflags="$(LDFLAGS_VERSION)" -o $(GATEWAY_BINARY) ./apnsgateway

geoip:
	@[ -f $(GEOLITE2_CITY) ] || { echo "Downloading GeoLite2-City.mmdb..."; curl -fSL -o $(GEOLITE2_CITY) $(GEOLITE2_CITY_URL); }
	@[ -f $(GEOLITE2_ASN) ] || { echo "Downloading GeoLite2-ASN.mmdb..."; curl -fSL -o $(GEOLITE2_ASN) $(GEOLITE2_ASN_URL); }

run: geoip build
	sudo ./$(BINARY)

run-noroot: build
	./$(BINARY)

install: geoip build
	@echo "Installing to $(INSTALL_DIR)..."
	sudo mkdir -p $(INSTALL_DIR)
	sudo cp $(BINARY) $(INSTALL_DIR)/
	sudo cp $(GEOLITE2_CITY) $(GEOLITE2_ASN) $(INSTALL_DIR)/
	@if [ ! -f $(INSTALL_DIR)/.env ]; then \
		sudo cp env.example $(INSTALL_DIR)/.env; \
		sudo chmod 0600 $(INSTALL_DIR)/.env; \
		echo "Created $(INSTALL_DIR)/.env — edit it with your credentials"; \
	fi
	sudo cp $(SERVICE_FILE) /etc/systemd/system/
	sudo systemctl daemon-reload
	sudo systemctl enable $(SERVICE_FILE)
	sudo systemctl restart $(BINARY)
	@echo "Installed and started. Check: systemctl status $(BINARY)"

uninstall:
	@echo "Removing $(BINARY)..."
	-sudo systemctl stop $(BINARY)
	-sudo systemctl disable $(BINARY)
	sudo rm -f /etc/systemd/system/$(SERVICE_FILE)
	sudo systemctl daemon-reload
	sudo rm -rf $(INSTALL_DIR)
	@echo "Uninstalled."

clean:
	rm -f $(BINARY) $(GATEWAY_BINARY)
