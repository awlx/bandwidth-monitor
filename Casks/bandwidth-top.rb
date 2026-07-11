cask "bandwidth-top" do
  version "0.0.34"

  on_arm do
    sha256 "db8dada267295b250d01f615d2e42981fc0bdc7a9bde70dee4c17741b20b4f93"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_arm64.tar.gz"
  end
  on_intel do
    sha256 "b9ea0724c209d8801831b2127e532bc31b82fa77c7bbbabc1de299bf781193c8"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_amd64.tar.gz"
  end

  name "bandwidth-top"
  desc "Interactive per-flow network traffic viewer"
  homepage "https://github.com/awlx/bandwidth-monitor"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: :monterey

  binary "bandwidth-top"

  caveats <<~EOS
    bandwidth-top captures packets through macOS /dev/bpf* devices. Run it
    with sudo, or ask an administrator to grant a dedicated trusted group
    read/write access to those devices. Never make BPF devices world-accessible.

    This cask installs only the standalone bandwidth-top CLI. It does not
    install or run the Linux-only bandwidth-monitor daemon.
  EOS
end
