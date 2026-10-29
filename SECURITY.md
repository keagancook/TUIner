# Security and privacy

Tuiner is a local terminal application. It opens the selected microphone while
in tuner mode and sends metronome clicks to the default audio output. It does not
record audio to disk, upload audio, or contain application telemetry or a network
server. The audio backend may use your system's configured audio service.

On Linux, Tuiner optionally runs `wpctl` and `pactl` from your PATH to display the
system input's name. Use a trusted PATH and do not run Tuiner as root.

Settings are stored under the operating system's user config directory in
`tuiner/` (normally `~/.config/tuiner/` on Linux). Diagnostics are stored under the
user cache directory in `tuiner/debug.log`. Diagnostics can include local device
names and paths. Review logs before sharing them. Do not add credentials or
private data to custom tuning definitions or bug reports.

## Reporting a vulnerability

Use the repository's private vulnerability reporting feature if it is enabled.
Do not post credentials, personal logs, or exploitable security details in a
public issue. If private reporting is unavailable, open an issue requesting a
private contact channel without including sensitive details.

The latest source revision is the supported version. Automated checks and code
review reduce risk but are not a security guarantee. Go vulnerability scans do
not assess the system-provided PortAudio or ALSA libraries; keep those updated
through your operating system's package manager.
