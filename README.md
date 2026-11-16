# gpumon

gpumon is a desktop GUI application for Windows and macOS that monitors the GPUs of remote Linux hosts over SSH. It shows utilization, memory, temperature, power draw, and per-process usage in real time, in a layout similar to `nvtop`.

![gpumon screens: add host, monitor, process filter, recordings, history, settings, and about](docs/images/screens.gif)

## Features

- **No root required.** A regular user account with SSH access is enough.
- **Nothing to install on the remote host.** The application uploads a single executable to the remote host. It does not depend on `nvtop`, `nvidia-smi`, or any other tool. When the remote copy differs from the one bundled with the application, the application shows a notice and replaces the copy.
- **No remote daemon.** The remote program runs only while the SSH connection is open. It installs no service and leaves no background process.
- **Multiple vendors.** NVIDIA (NVML), AMD (sysfs), and Intel (sysfs / fdinfo).
- **Multiple hosts.** One application window can monitor one or more hosts. Each monitored host has its own tab.
- **Fine-grained sampling.** Every host is sampled every 200 ms. The monitor charts zoom smoothly from 1 minute to 24 hours with the toolbar buttons, Ctrl or ⌘ plus the scroll wheel, or a trackpad pinch.
- **Process table.** The process table lists the processes on every GPU of a host. You can sort it by any column, filter it by GPU, user, or command, and pin rows to the top.
- **Automatic recording.** Every connection is recorded to a file on the local machine. The recordings are listed per host, and you can open or delete them.
- **History.** The history view opens a recording. You can drag on a chart to zoom in to a time range, and click a chart to pin a time and see the processes at that time. A summary card shows the maximum of each value over the whole recording, and a click on a maximum jumps to the time it occurred.
- **Gaps are marked.** A chart does not join the line across a period without data. The period is shaded, and its tooltip gives the reason, such as a disconnection.
- **Automatic reconnection.** After an unexpected disconnection, the application reconnects without a dialog and continues the same recording.
- **Runs in the background.** Minimizing the window hides it to the Windows system tray or the macOS menu bar. Monitoring and recording continue.
- **Uses `~/.ssh/config`.** A host name from `~/.ssh/config` fills in the port and key file in the host form.
- **Light and dark appearance, English and Traditional Chinese.** Both are set on the Settings page and take effect immediately.
- **Non-intrusive.** The application does not change any system setting on the remote host.
- **No stored passwords.** Login passwords and key passphrases are never written to disk or to the operating system's credential store.

## Requirements

Local machine:

- Windows (amd64) or macOS (arm64).

Remote host:

- Linux x86_64 with glibc. Distributions that use musl, such as Alpine Linux, are not supported.
- SSH login with a regular user account.
- A home directory that is writable and executable. The remote program is placed in `~/.gpumon/`.

## Tested GPUs

- NVIDIA RTX PRO 6000 Blackwell Workstation Edition
- NVIDIA GeForce RTX 4080 SUPER
- Intel UHD Graphics 770 (Raptor Lake-S GT1)

## License

gpumon is released under the MIT License. See [LICENSE.md](LICENSE.md).
