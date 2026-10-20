// Package execfs is the remote file layer for machines that cannot serve SFTP:
// a WSL distribution, or an SSH host whose sshd refuses the subsystem. Every
// operation is a small fixed POSIX script run under /bin/sh with each path
// passed as a positional argument, so no path is ever spliced into script text.
// Outcomes the caller must tell apart (missing, a directory, a tool the machine
// lacks, too large) are exit statuses this package defines, never a tool's words.
package execfs
