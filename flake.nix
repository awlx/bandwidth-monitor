{
  description = "Bandwidth Monitor — real-time network monitoring dashboard";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

    # GeoIP databases — run `nix flake update` to pull fresh versions.
    # These are from a public mirror that tracks MaxMind's weekly releases.
    geolite2-city = {
      url = "https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-City.mmdb";
      flake = false;
    };
    geolite2-asn = {
      url = "https://github.com/P3TERX/GeoLite.mmdb/raw/download/GeoLite2-ASN.mmdb";
      flake = false;
    };
  };

  outputs = { self, nixpkgs, flake-utils, geolite2-city, geolite2-asn }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        bandwidth-monitor = pkgs.buildGoModule {
          pname = "bandwidth-monitor";
          version = "0.0.17";
          src = ./.;

          vendorHash = null;

          buildInputs = [ pkgs.libpcap ];
          nativeBuildInputs = [ pkgs.pkg-config ];
          subPackages = [ "." "cmd/bandwidth-top" ];

          ldflags = [
            "-s" "-w"
            "-X" "bandwidth-monitor/version.Version=${bandwidth-monitor.version}"
          ];

          postInstall = ''
            # Install GeoIP databases from flake inputs
            install -Dm644 ${geolite2-city} $out/share/bandwidth-monitor/GeoLite2-City.mmdb
            install -Dm644 ${geolite2-asn} $out/share/bandwidth-monitor/GeoLite2-ASN.mmdb

            # Install support files
            install -Dm644 env.example $out/share/bandwidth-monitor/env.example
            install -Dm644 bandwidth-monitor.service $out/lib/systemd/system/bandwidth-monitor.service
          '';

          meta = with pkgs.lib; {
            description = "Real-time network monitoring dashboard for Linux";
            homepage = "https://github.com/awlx/bandwidth-monitor";
            license = licenses.agpl3Only;
            platforms = platforms.linux;
            mainProgram = "bandwidth-monitor";
          };
        };
      in
      {
        packages = {
          default = bandwidth-monitor;
          bandwidth-monitor = bandwidth-monitor;
        };

        apps.default = flake-utils.lib.mkApp {
          drv = bandwidth-monitor;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [ go gopls libpcap pkg-config ];
        };
      }
    ) // {
      nixosModules.default = { config, lib, pkgs, ... }:
        let
          cfg = config.services.bandwidth-monitor;
        in
        {
          options.services.bandwidth-monitor = {
            enable = lib.mkEnableOption "Bandwidth Monitor";

            package = lib.mkOption {
              type = lib.types.package;
              default = self.packages.${pkgs.system}.default;
              description = "The bandwidth-monitor package to use.";
            };

            listenAddress = lib.mkOption {
              type = lib.types.str;
              default = ":8080";
              description = "HTTP listen address.";
            };

            geoipDir = lib.mkOption {
              type = lib.types.nullOr lib.types.str;
              default = null;
              description = ''
                Path to directory containing GeoLite2-City.mmdb and
                GeoLite2-ASN.mmdb. If null, uses the databases bundled
                in the package (from flake inputs).

                Set this if you use services.geoipupdate to keep the
                databases fresh, e.g.:
                  geoipDir = "/var/lib/GeoIP";
              '';
              example = "/var/lib/GeoIP";
            };

            environmentFile = lib.mkOption {
              type = lib.types.nullOr lib.types.path;
              default = null;
              description = "Path to environment file with configuration.";
            };

            settings = lib.mkOption {
              type = lib.types.attrsOf lib.types.str;
              default = {};
              description = "Environment variables for bandwidth-monitor.";
              example = {
                ADGUARD_URL = "http://adguard.local";
                ADGUARD_USER = "admin";
              };
            };
          };

          config = lib.mkIf cfg.enable {
            systemd.services.bandwidth-monitor = {
              description = "Bandwidth Monitor";
              after = [ "network.target" ];
              wantedBy = [ "multi-user.target" ];

              environment = let
                geoDir = if cfg.geoipDir != null
                  then cfg.geoipDir
                  else "${cfg.package}/share/bandwidth-monitor";
              in {
                LISTEN = cfg.listenAddress;
                GEO_CITY = "${geoDir}/GeoLite2-City.mmdb";
                GEO_ASN = "${geoDir}/GeoLite2-ASN.mmdb";
              } // cfg.settings;

              serviceConfig = {
                ExecStart = "${cfg.package}/bin/bandwidth-monitor";
                DynamicUser = true;
                AmbientCapabilities = [ "CAP_NET_RAW" "CAP_NET_ADMIN" ];
                CapabilityBoundingSet = [ "CAP_NET_RAW" "CAP_NET_ADMIN" ];
                ProtectSystem = "strict";
                ProtectHome = true;
                PrivateTmp = true;
                NoNewPrivileges = true;
                Restart = "on-failure";
                RestartSec = 5;
              } // lib.optionalAttrs (cfg.environmentFile != null) {
                EnvironmentFile = cfg.environmentFile;
              } // lib.optionalAttrs (cfg.geoipDir != null) {
                ReadOnlyPaths = [ cfg.geoipDir ];
              };
            };
          };
        };
    };
}
