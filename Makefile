BINARY=bandwidth-monitor
INSTALL_DIR=/opt/bandwidth-monitor
SERVICE_FILE=bandwidth-monitor.service

GEOLITE2_COUNTRY=GeoLite2-Country.mmdb
GEOLITE2_ASN=GeoLite2-ASN.mmdb
GEOLITE2_COUNTRY_URL=https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-Country.mmdb
GEOLITE2_ASN_URL=https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-ASN.mmdb

.PHONY: build run clean geoip install uninstall

build:
	go build -o $(BINARY) .

build_stripped:
	# Build and strip the binary. 
	go build -ldflags="-s -w" -o $(BINARY) .

geoip:
	@[ -f $(GEOLITE2_COUNTRY) ] || { echo "Downloading GeoLite2-Country.mmdb..."; curl -fSL -o $(GEOLITE2_COUNTRY) $(GEOLITE2_COUNTRY_URL); }
	@[ -f $(GEOLITE2_ASN) ] || { echo "Downloading GeoLite2-ASN.mmdb..."; curl -fSL -o $(GEOLITE2_ASN) $(GEOLITE2_ASN_URL); }

run: geoip build
	sudo ./$(BINARY)

run-noroot: build
	./$(BINARY)

install: geoip build
	@echo "Installing to $(INSTALL_DIR)..."
	sudo mkdir -p $(INSTALL_DIR)
	sudo cp $(BINARY) $(INSTALL_DIR)/
	sudo cp $(GEOLITE2_COUNTRY) $(GEOLITE2_ASN) $(INSTALL_DIR)/
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
	rm -f $(BINARY)
