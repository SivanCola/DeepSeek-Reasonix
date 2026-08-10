#ifndef REASONIX_WEBKIT_DIAGNOSTICS_LINUX_H
#define REASONIX_WEBKIT_DIAGNOSTICS_LINUX_H

void reasonix_install_webkit_observer(void);
extern void reasonixWebKitRuntimeReady(int major, int minor, int micro, int gpu_mode);
extern void reasonixWebKitProcessTerminated(int reason, int recovery, unsigned long long generation);

#endif
