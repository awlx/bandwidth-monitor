cask "bandwidth-top" do
  version "0.0.36"

  on_arm do
    sha256 "f71d415ea51ac31500b3310a82b82500f71af7e5bd2543db8d7da810d9693f17"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_arm64.tar.gz"
  end
  on_intel do
    sha256 "67f81dc7baf1cd2bd43545435638a0e96c86e037279212dca7aa7345e90510ed"

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
