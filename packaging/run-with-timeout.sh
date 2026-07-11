#!/bin/sh
set -u

usage() {
	echo "usage: $0 [--homebrew] SECONDS COMMAND_NAME COMMAND [ARG ...]" >&2
	exit 2
}

homebrew=false
if [ "${1:-}" = "--homebrew" ]; then
	homebrew=true
	shift
fi

[ "$#" -ge 3 ] || usage
seconds=$1
command_name=$2
shift 2

case "$seconds" in
	'' | *[!0-9]*) usage ;;
esac
[ "$seconds" -gt 0 ] || usage
[ -n "$command_name" ] || usage

if [ "$homebrew" = true ]; then
	HOMEBREW_NO_AUTO_UPDATE=1
	export HOMEBREW_NO_AUTO_UPDATE
fi

ruby -e '
require "open3"

def group_alive?(pid)
  Process.kill(0, -pid)
  true
rescue Errno::ESRCH
  false
rescue Errno::EPERM
  true
end

def signal_group(signal, pid)
  Process.kill(signal, -pid)
rescue Errno::ESRCH
  nil
end

def terminate_group(pid)
  signal_group("TERM", pid)
  deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + 2
  while group_alive?(pid) &&
        Process.clock_gettime(Process::CLOCK_MONOTONIC) < deadline
    begin
      Process.waitpid(pid, Process::WNOHANG)
    rescue Errno::ECHILD
      nil
    end
    sleep 0.05
  end
  signal_group("KILL", pid) if group_alive?(pid)
  begin
    Process.waitpid(pid)
  rescue Errno::ECHILD
    nil
  end
end

def report_process_group(pid)
  output, status = Open3.capture2e(
    "/bin/ps", "-axo", "pid=,ppid=,pgid=,state=,etime=,comm="
  )
  return unless status.success?

  rows = output.lines.filter_map do |line|
    fields = line.split(nil, 6)
    next unless fields.length == 6 && fields[2].to_i == pid

    fields[0, 5] + [File.basename(fields[5].strip)]
  end
  return if rows.empty?

  warn "Process group state (pid ppid pgid state elapsed command):"
  rows.each { |row| warn row.join(" ") }
end

timeout = Integer(ARGV.shift, 10)
command_name = ARGV.shift
command = ARGV
started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
deadline = started + timeout
forwarded_signal = nil

%w[HUP INT TERM].each do |signal|
  Signal.trap(signal) { forwarded_signal = signal }
end

begin
  pid = Process.spawn(*command, pgroup: true)
rescue SystemCallError => error
  warn "Unable to start #{command_name}: #{error.message}"
  exit 127
end

loop do
  waited = Process.waitpid2(pid, Process::WNOHANG)
  if waited
    status = waited[1]
    exit(status.exitstatus || 128 + status.termsig)
  end

  if forwarded_signal
    warn "Interrupted while running #{command_name}; terminating its process group"
    terminate_group(pid)
    exit 128 + Signal.list.fetch(forwarded_signal)
  end

  now = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  if now >= deadline
    warn "Timed out after #{timeout}s: #{command_name}"
    report_process_group(pid)
    terminate_group(pid)
    exit 124
  end

  sleep [0.1, deadline - now].min
end
' "$seconds" "$command_name" "$@"
status=$?

if [ "$status" -eq 124 ] && [ "$homebrew" = true ]; then
	helper=$(CDPATH= cd -- "$(dirname "$0")" && pwd)/$(basename "$0")
	"$helper" 10 "Homebrew version and config diagnostics" sh -c '
		HOMEBREW_NO_AUTO_UPDATE=1 brew --version 2>&1 | sed -n "1,3p"
		HOMEBREW_NO_AUTO_UPDATE=1 brew config 2>&1 |
			awk "/^(HOMEBREW_VERSION|ORIGIN|HEAD|Last commit|Branch|Core tap JSON|Core cask tap JSON|CPU|Clang|Git|Curl|macOS|CLT|Xcode|Rosetta):/"
	' || true
	"$helper" 10 "Homebrew lock and cache diagnostics" sh -c '
		prefix=$(HOMEBREW_NO_AUTO_UPDATE=1 brew --prefix) || exit
		cache=$(HOMEBREW_NO_AUTO_UPDATE=1 brew --cache) || exit
		locks=$prefix/var/homebrew/locks

		if [ -d "$locks" ]; then
			set -- "$locks"/*
			if [ "$#" -eq 1 ] && [ ! -e "$1" ] && [ ! -L "$1" ]; then
				echo "Homebrew lock entries: 0"
			else
				echo "Homebrew lock entries: $#"
				printf "%s\n" "$@" | sed "s|.*/||" | sed -n "1,20p"
			fi
		else
			echo "Homebrew lock entries: 0"
		fi

		if [ -d "$cache" ]; then
			set -- "$cache"/*
			if [ "$#" -eq 1 ] && [ ! -e "$1" ] && [ ! -L "$1" ]; then
				echo "Homebrew cache entries: 0"
			else
				echo "Homebrew cache entries: $#"
			fi
		else
			echo "Homebrew cache entries: 0"
		fi
	' || true
fi

exit "$status"
