cask "bandwidth-top" do
  version "0.0.43"

  on_arm do
    sha256 "be8e0c297f8f68e7eeda5b7b1643eca6f44911c4c0bc76d5d43bb5c9e0b33d88"

    url "https://github.com/awlx/bandwidth-monitor/releases/download/v#{version}/bandwidth-top_#{version}_darwin_arm64.tar.gz"
  end
  on_intel do
    sha256 "e76fb8b09db93ac240049c50b0d6cbf7929c0e9e3b0801bf04d15442aa1ab15a"

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
