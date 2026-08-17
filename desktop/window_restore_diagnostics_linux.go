//go:build linux && cgo

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>
#include <string.h>

typedef struct {
  GMutex mutex;
  GCond cond;
  gint refs;
  gboolean done;
  gboolean presented;
} ReasonixWindowProbe;

static void reasonix_window_probe_unref(ReasonixWindowProbe *probe) {
  if (!g_atomic_int_dec_and_test(&probe->refs)) return;
  g_cond_clear(&probe->cond);
  g_mutex_clear(&probe->mutex);
  g_free(probe);
}

static gboolean reasonix_probe_main_window(gpointer data) {
  ReasonixWindowProbe *probe = (ReasonixWindowProbe *)data;
  gboolean presented = FALSE;
  GList *windows = gtk_window_list_toplevels();
  for (GList *item = windows; item != NULL; item = item->next) {
    GtkWidget *widget = GTK_WIDGET(item->data);
    if (!GTK_IS_WINDOW(widget)) continue;
    const gchar *title = gtk_window_get_title(GTK_WINDOW(widget));
    if (title == NULL || strcmp(title, "Reasonix") != 0) continue;
    GdkWindow *window = gtk_widget_get_window(widget);
    if (window == NULL) continue;
    GdkWindowState state = gdk_window_get_state(window);
    if (gtk_widget_get_visible(widget) && gtk_widget_get_mapped(widget) &&
        (state & (GDK_WINDOW_STATE_ICONIFIED | GDK_WINDOW_STATE_WITHDRAWN)) == 0) {
      presented = TRUE;
      break;
    }
  }
  g_list_free(windows);
  g_mutex_lock(&probe->mutex);
  probe->presented = presented;
  probe->done = TRUE;
  g_cond_signal(&probe->cond);
  g_mutex_unlock(&probe->mutex);
  reasonix_window_probe_unref(probe);
  return G_SOURCE_REMOVE;
}

static int reasonix_main_window_is_presented(void) {
  ReasonixWindowProbe *probe = g_new0(ReasonixWindowProbe, 1);
  g_mutex_init(&probe->mutex);
  g_cond_init(&probe->cond);
  probe->refs = 2;
  g_main_context_invoke(NULL, reasonix_probe_main_window, probe);
  g_mutex_lock(&probe->mutex);
  gint64 deadline = g_get_monotonic_time() + G_TIME_SPAN_SECOND;
  while (!probe->done && g_cond_wait_until(&probe->cond, &probe->mutex, deadline)) {}
  gboolean result = probe->done && probe->presented;
  g_mutex_unlock(&probe->mutex);
  reasonix_window_probe_unref(probe);
  return result ? 1 : 0;
}
*/
import "C"

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func windowRestoreDiagnosticsSupported() bool { return true }

func windowRestoreOwnerAlive(pid int) bool {
	return desktopProcessAlive(pid)
}

func windowRestoreConfirmed() bool {
	return C.reasonix_main_window_is_presented() == 1
}

func showWindowRestoreFailure(app *App) {
	if app == nil || app.ctx == nil {
		return
	}
	// gtk_window_present is safe to repeat and is the only Linux presentation
	// action. Show the native warning after it, so failure cannot strand a
	// background-only process.
	runtime.WindowUnminimise(app.ctx)
	_, _ = runtime.MessageDialog(app.ctx, runtime.MessageDialogOptions{
		Type:  runtime.WarningDialog,
		Title: "Reasonix window recovery / Reasonix 窗口恢复",
		Message: "Reasonix could not confirm that the main window is visible. The window was presented again; if it remains unavailable, exit Reasonix normally and restart it.\n\n" +
			"Reasonix 无法确认主窗口已可见，已再次尝试呈现窗口；若仍不可用，请正常退出后重新启动。",
		Buttons:       []string{"OK"},
		DefaultButton: "OK",
	})
}
