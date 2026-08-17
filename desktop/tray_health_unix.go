//go:build !windows && !darwin && cgo

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	statusNotifierWatcherName  = "org.kde.StatusNotifierWatcher"
	statusNotifierWatcherPath  = dbus.ObjectPath("/StatusNotifierWatcher")
	statusNotifierWatcherIFace = "org.kde.StatusNotifierWatcher"
	statusNotifierPollInterval = time.Second
	statusNotifierProbeTimeout = 750 * time.Millisecond
)

func readStatusNotifierSnapshot(ctx context.Context, conn *dbus.Conn, itemName string) (statusNotifierSnapshot, error) {
	snapshot := statusNotifierSnapshot{}
	bus := conn.Object("org.freedesktop.DBus", dbus.ObjectPath("/org/freedesktop/DBus"))
	if err := bus.CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, statusNotifierWatcherName).Store(&snapshot.WatcherOwner); err != nil {
		return snapshot, nil
	}
	if snapshot.WatcherOwner == "" {
		return snapshot, nil
	}
	watcher := conn.Object(statusNotifierWatcherName, statusNotifierWatcherPath)
	var value dbus.Variant
	if err := watcher.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, statusNotifierWatcherIFace, "IsStatusNotifierHostRegistered").Store(&value); err != nil {
		return snapshot, err
	}
	snapshot.Host, _ = value.Value().(bool)
	if err := watcher.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, statusNotifierWatcherIFace, "RegisteredStatusNotifierItems").Store(&value); err != nil {
		return snapshot, err
	}
	snapshot.Items, _ = value.Value().([]string)
	_ = bus.CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, itemName).Store(&snapshot.ItemOwner)
	return snapshot, nil
}

func (a *App) startTrayHealthMonitor(t *desktopTray) {
	if a == nil || t == nil {
		return
	}
	ctx, cancel := context.WithCancel(a.bootContext())
	t.healthMu.Lock()
	if t.healthStopped {
		t.healthMu.Unlock()
		cancel()
		return
	}
	t.cancel = cancel
	t.healthMu.Unlock()
	a.goSafe("trayStatusNotifierMonitor", func() {
		itemName := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
		var conn *dbus.Conn
		defer func() {
			if conn != nil {
				_ = conn.Close()
			}
		}()
		probe := func() (bool, string) {
			if conn == nil {
				var err error
				conn, err = dbus.SessionBusPrivateNoAutoStartup()
				if err != nil || conn.Auth(nil) != nil || conn.Hello() != nil {
					if conn != nil {
						_ = conn.Close()
						conn = nil
					}
					return false, "no_session_bus"
				}
			}
			probeCtx, cancel := context.WithTimeout(ctx, statusNotifierProbeTimeout)
			defer cancel()
			if err := conn.BusObject().CallWithContext(probeCtx, "org.freedesktop.DBus.Peer.Ping", 0).Err; err != nil {
				_ = conn.Close()
				conn = nil
				return false, "no_session_bus"
			}
			snapshot, err := readStatusNotifierSnapshot(probeCtx, conn, itemName)
			if err != nil {
				return false, "watcher_unresponsive"
			}
			return evaluateStatusNotifierSnapshot(snapshot, itemName)
		}

		for {
			ready, reason := probe()
			if ready {
				a.setTrayHealth(t, "ready", "")
			} else {
				a.setTrayHealth(t, "unavailable", reason)
			}
			timer := time.NewTimer(statusNotifierPollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
}

// Linux readiness is established by the DBus monitor, not systray.onReady.
func (a *App) trayConfigured(*desktopTray) {}
