cask "bandwidth-top" do
  version "0.0.41"

  on_arm do
    sha256 "c4855cc93a9ad8803a694799a2a2e8cc606062746626d251b1d14f5fe060a3ec"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_arm64.tar.gz"
  end
  on_intel do
    sha256 "95342b31e7c0c0c121559d137cc8a559463cb298868ee29213ee4c4c6a880c04"

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
