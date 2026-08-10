#include "webkit_diagnostics_linux.h"

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

#define REASONIX_RECOVERY_COOLDOWN_US (30 * G_USEC_PER_SEC)
#define REASONIX_RECOVERY_TIMEOUT_SECONDS 30

static WebKitWebView *reasonix_web_view = NULL;
static gboolean reasonix_recovery_pending = FALSE;
static gboolean reasonix_recovery_load_failed = FALSE;
static gint64 reasonix_last_recovery_at = 0;
static guint reasonix_recovery_timeout_id = 0;
static guint64 reasonix_generation = 0;
static guint64 reasonix_pending_generation = 0;
static WebKitWebProcessTerminationReason reasonix_pending_reason = WEBKIT_WEB_PROCESS_CRASHED;

static GtkWidget *reasonix_find_web_view(GtkWidget *widget) {
  if (WEBKIT_IS_WEB_VIEW(widget)) return widget;
  if (!GTK_IS_CONTAINER(widget)) return NULL;
  GList *children = gtk_container_get_children(GTK_CONTAINER(widget));
  GtkWidget *found = NULL;
  for (GList *item = children; item != NULL && found == NULL; item = item->next) {
    found = reasonix_find_web_view(GTK_WIDGET(item->data));
  }
  g_list_free(children);
  return found;
}

static void reasonix_finish_recovery(int outcome) {
  if (!reasonix_recovery_pending) return;
  reasonix_recovery_pending = FALSE;
  reasonix_recovery_load_failed = FALSE;
  if (reasonix_recovery_timeout_id != 0) {
    g_source_remove(reasonix_recovery_timeout_id);
    reasonix_recovery_timeout_id = 0;
  }
  reasonixWebKitProcessTerminated((int)reasonix_pending_reason, outcome,
                                  (unsigned long long)reasonix_pending_generation);
}

static gboolean reasonix_recovery_timeout(gpointer data) {
  (void)data;
  reasonix_recovery_timeout_id = 0;
  if (reasonix_recovery_pending) {
    reasonix_recovery_pending = FALSE;
    reasonix_recovery_load_failed = FALSE;
    reasonixWebKitProcessTerminated((int)reasonix_pending_reason, 2,
                                    (unsigned long long)reasonix_pending_generation);
  }
  return G_SOURCE_REMOVE;
}

static void reasonix_web_process_terminated(WebKitWebView *web_view,
                                            WebKitWebProcessTerminationReason reason,
                                            gpointer data) {
  (void)data;
  gint64 now = g_get_monotonic_time();
  guint64 generation = ++reasonix_generation;
  if (reasonix_recovery_pending ||
      (reasonix_last_recovery_at != 0 && now - reasonix_last_recovery_at < REASONIX_RECOVERY_COOLDOWN_US)) {
    reasonixWebKitProcessTerminated((int)reason, 0, (unsigned long long)generation);
    return;
  }
  reasonix_last_recovery_at = now;
  reasonix_pending_reason = reason;
  reasonix_pending_generation = generation;
  reasonix_recovery_pending = TRUE;
  reasonix_recovery_load_failed = FALSE;
  reasonix_recovery_timeout_id = g_timeout_add_seconds(REASONIX_RECOVERY_TIMEOUT_SECONDS,
                                                        reasonix_recovery_timeout, NULL);
  webkit_web_view_reload(web_view);
}

static gboolean reasonix_load_failed(WebKitWebView *web_view, WebKitLoadEvent event,
                                     const gchar *uri, GError *error, gpointer data) {
  (void)web_view;
  (void)event;
  (void)uri;
  (void)error;
  (void)data;
  if (reasonix_recovery_pending) reasonix_recovery_load_failed = TRUE;
  return FALSE;
}

static void reasonix_load_changed(WebKitWebView *web_view, WebKitLoadEvent event, gpointer data) {
  (void)web_view;
  (void)data;
  if (reasonix_recovery_pending && event == WEBKIT_LOAD_FINISHED) {
    reasonix_finish_recovery(reasonix_recovery_load_failed ? 2 : 1);
  }
}

static void reasonix_web_view_destroyed(GtkWidget *widget, gpointer data) {
  (void)widget;
  (void)data;
  if (reasonix_recovery_pending) reasonix_finish_recovery(2);
  reasonix_web_view = NULL;
}

static gboolean reasonix_attach_webkit_observer(gpointer data) {
  (void)data;
  if (reasonix_web_view != NULL) return G_SOURCE_REMOVE;
  GList *windows = gtk_window_list_toplevels();
  GtkWidget *found = NULL;
  for (GList *item = windows; item != NULL && found == NULL; item = item->next) {
    found = reasonix_find_web_view(GTK_WIDGET(item->data));
  }
  g_list_free(windows);
  if (found == NULL) return G_SOURCE_REMOVE;
  if (g_signal_lookup("web-process-terminated", WEBKIT_TYPE_WEB_VIEW) == 0) {
    return G_SOURCE_REMOVE;
  }

  WebKitWebView *web_view = WEBKIT_WEB_VIEW(found);
  if (g_signal_connect(web_view, "web-process-terminated",
                       G_CALLBACK(reasonix_web_process_terminated), NULL) == 0) {
    return G_SOURCE_REMOVE;
  }
  reasonix_web_view = web_view;
  g_signal_connect(reasonix_web_view, "load-failed", G_CALLBACK(reasonix_load_failed), NULL);
  g_signal_connect(reasonix_web_view, "load-changed", G_CALLBACK(reasonix_load_changed), NULL);
  g_signal_connect(reasonix_web_view, "destroy", G_CALLBACK(reasonix_web_view_destroyed), NULL);

  WebKitSettings *settings = webkit_web_view_get_settings(reasonix_web_view);
  WebKitHardwareAccelerationPolicy policy = WEBKIT_HARDWARE_ACCELERATION_POLICY_ON_DEMAND;
  if (settings != NULL) policy = webkit_settings_get_hardware_acceleration_policy(settings);
  reasonixWebKitRuntimeReady((int)webkit_get_major_version(), (int)webkit_get_minor_version(),
                            (int)webkit_get_micro_version(), (int)policy);
  return G_SOURCE_REMOVE;
}

void reasonix_install_webkit_observer(void) {
  g_main_context_invoke(NULL, reasonix_attach_webkit_observer, NULL);
}
