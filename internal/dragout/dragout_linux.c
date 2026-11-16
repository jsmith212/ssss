// GTK4 implementation of dragout_start. Wails v3 hands us its GtkWindow*; the
// drag is started directly on the window's GdkSurface, bypassing GtkDragSource,
// because the gesture is recognised in the webview, not by a GTK controller.

#include <gtk/gtk.h>

#include "dragout_cgo.h"
#include "_cgo_export.h"

// How often the watchdog samples the button, and how many consecutive
// "released" samples end the session.
#define WATCHDOG_INTERVAL_MS 200
#define WATCHDOG_RELEASED_TICKS 3
// How long to wait for the target to finish after the drop was performed.
#define FINISH_TIMEOUT_S 30

typedef struct {
    GtkWindow *window;
    GdkContentProvider *content;
    char *label;
    uintptr_t handle;
} Request;

typedef struct {
    GdkDrag *drag;
    uintptr_t handle;
    gboolean performed;
    guint released_ticks;
    guint watchdog_id;
    guint finish_id;
    GtkWidget *web_view; // weak
} Session;

static void request_free(Request *req) {
    g_clear_object(&req->window);
    g_clear_object(&req->content);
    g_free(req->label);
    g_free(req);
}

// Offers the files as GDK_TYPE_FILE_LIST, which GDK serialises to the
// file-transfer portal types and text/uri-list, plus a literal text/uri-list
// so that plain X11/XWayland targets (DAWs, mostly) find it listed first and
// are served without going through the serialiser registry.
static GdkContentProvider *content_new(char **paths, int n) {
    GFile **files = g_new0(GFile *, n);
    GString *uris = g_string_new(NULL);
    for (int i = 0; i < n; i++) {
        files[i] = g_file_new_for_path(paths[i]);
        char *uri = g_file_get_uri(files[i]);
        g_string_append(uris, uri);
        g_string_append(uris, "\r\n");
        g_free(uri);
    }

    GdkFileList *list = gdk_file_list_new_from_array(files, n);
    GBytes *bytes = g_string_free_to_bytes(uris);
    GdkContentProvider *providers[2] = {
        gdk_content_provider_new_for_bytes("text/uri-list", bytes),
        gdk_content_provider_new_typed(GDK_TYPE_FILE_LIST, list),
    };
    GdkContentProvider *content = gdk_content_provider_new_union(providers, 2);

    g_bytes_unref(bytes);
    g_boxed_free(GDK_TYPE_FILE_LIST, list);
    for (int i = 0; i < n; i++) g_object_unref(files[i]);
    g_free(files);
    return content;
}

static void ensure_css(GdkDisplay *display) {
    static const char key[] = "ssss-dragout-css";
    if (g_object_get_data(G_OBJECT(display), key)) return;

    GtkCssProvider *css = gtk_css_provider_new();
    gtk_css_provider_load_from_string(css,
        ".ssss-drag-pill {"
        "  background-color: rgba(34, 34, 38, 0.94);"
        "  color: #ededef;"
        "  border: 1px solid rgba(255, 255, 255, 0.12);"
        "  border-radius: 10px;"
        "  padding: 4px 10px;"
        "  font-size: 12px;"
        "}");
    gtk_style_context_add_provider_for_display(display, GTK_STYLE_PROVIDER(css),
                                               GTK_STYLE_PROVIDER_PRIORITY_APPLICATION);
    g_object_set_data_full(G_OBJECT(display), key, css, g_object_unref);
}

// A text pill needs no icon theme, which a bare Wails install may lack.
static void set_icon(GdkDrag *drag, const char *label) {
    ensure_css(gdk_drag_get_display(drag));

    GtkWidget *pill = gtk_label_new(label);
    gtk_label_set_ellipsize(GTK_LABEL(pill), PANGO_ELLIPSIZE_MIDDLE);
    gtk_label_set_max_width_chars(GTK_LABEL(pill), 40);
    gtk_widget_add_css_class(pill, "ssss-drag-pill");

    GtkWidget *icon = gtk_drag_icon_get_for_drag(drag);
    gtk_drag_icon_set_child(GTK_DRAG_ICON(icon), pill);
    // Negative hotspot: the pill hangs below and right of the pointer instead
    // of covering the drop position.
    gdk_drag_set_hotspot(drag, -12, -12);
}

// Finds the web view below the window. It is looked up by type name so this
// file needs no WebKit headers.
static GtkWidget *find_web_view(GtkWidget *widget) {
    if (widget == NULL) return NULL;
    for (GType t = G_OBJECT_TYPE(widget); t != 0; t = g_type_parent(t)) {
        if (g_strcmp0(g_type_name(t), "WebKitWebView") == 0) return widget;
    }
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child != NULL;
         child = gtk_widget_get_next_sibling(child)) {
        GtkWidget *found = find_web_view(child);
        if (found != NULL) return found;
    }
    return NULL;
}

// On Wayland the compositor owns the pointer for the length of the drag, so
// the button release that ends it never reaches our surface. The web view's
// click gesture is left in the middle of the press that started the drag, and
// it then swallows the whole next press: no mousedown reaches the page until
// a full press and release has gone by. A GtkDragSource avoids this by
// resetting its gesture; the gesture here is WebKit's, so it is reset from
// the outside once the button is known to be up. X11 needs none of this: GDK
// sees the release there.
static void forget_press(Session *s) {
    if (s->web_view == NULL) return;
    GdkDisplay *display = gtk_widget_get_display(s->web_view);
    if (g_strcmp0(G_OBJECT_TYPE_NAME(display), "GdkWaylandDisplay") != 0) return;

    GListModel *controllers = gtk_widget_observe_controllers(s->web_view);
    guint n = g_list_model_get_n_items(controllers);
    for (guint i = 0; i < n; i++) {
        GtkEventController *c = g_list_model_get_item(controllers, i);
        if (GTK_IS_GESTURE(c)) gtk_event_controller_reset(c);
        g_object_unref(c);
    }
    g_object_unref(controllers);
}

static void session_end(Session *s, int result) {
    g_signal_handlers_disconnect_by_data(s->drag, s);
    g_clear_handle_id(&s->watchdog_id, g_source_remove);
    g_clear_handle_id(&s->finish_id, g_source_remove);

    // Idempotent; the backend has already done this on its own paths. It
    // releases the seat's reference and hides the drag surface.
    gdk_drag_drop_done(s->drag, result == DRAGOUT_DROPPED);
    g_object_unref(s->drag);

    g_clear_weak_pointer(&s->web_view);
    uintptr_t handle = s->handle;
    g_free(s);
    dragoutDone(handle, result);
}

static gboolean on_finish_timeout(gpointer data) {
    Session *s = data;
    s->finish_id = 0;
    // The target took the drop but never confirmed completion.
    session_end(s, DRAGOUT_DROPPED);
    return G_SOURCE_REMOVE;
}

static void on_drop_performed(GdkDrag *drag, gpointer data) {
    Session *s = data;
    s->performed = TRUE;
    forget_press(s);
    g_clear_handle_id(&s->watchdog_id, g_source_remove);
    if (s->finish_id == 0)
        s->finish_id = g_timeout_add_seconds(FINISH_TIMEOUT_S, on_finish_timeout, s);
}

static void on_dnd_finished(GdkDrag *drag, gpointer data) {
    session_end(data, DRAGOUT_DROPPED);
}

static void on_cancel(GdkDrag *drag, GdkDragCancelReason reason, gpointer data) {
    // Dropped on nothing, refused or Escape: the button is up all the same.
    forget_press(data);
    session_end(data, DRAGOUT_CANCELLED);
}

static gboolean button_down(GdkSurface *surface, GdkDevice *pointer) {
    GdkModifierType mask = 0;
    gdk_surface_get_device_position(surface, pointer, NULL, NULL, &mask);
    return (mask & GDK_BUTTON1_MASK) != 0;
}

// A Wayland compositor silently ignores start_drag when the serial of the
// button press is no longer current (the button went up while the request
// was in flight), and then no signal ever ends the GdkDrag. In that case the
// surface keeps the pointer and sees the release, which is what this
// detects. During a real drag the compositor withholds the release from us
// (Wayland), or GDK turns it into drop-performed/cancel synchronously (X11),
// so the button never reads as released while the session is still open.
static gboolean on_watchdog(gpointer data) {
    Session *s = data;
    if (button_down(gdk_drag_get_surface(s->drag), gdk_drag_get_device(s->drag))) {
        s->released_ticks = 0;
        return G_SOURCE_CONTINUE;
    }
    if (++s->released_ticks < WATCHDOG_RELEASED_TICKS) return G_SOURCE_CONTINUE;

    s->watchdog_id = 0;
    session_end(s, DRAGOUT_CANCELLED);
    return G_SOURCE_REMOVE;
}

static int begin(Request *req) {
    if (!GTK_IS_WINDOW(req->window)) return DRAGOUT_ERR_NO_WINDOW;
    GdkSurface *surface = gtk_native_get_surface(GTK_NATIVE(req->window));
    if (surface == NULL) return DRAGOUT_ERR_NO_WINDOW;

    GdkSeat *seat = gdk_display_get_default_seat(gdk_surface_get_display(surface));
    GdkDevice *pointer = seat ? gdk_seat_get_pointer(seat) : NULL;
    if (pointer == NULL) return DRAGOUT_ERR_START_FAILED;

    if (!button_down(surface, pointer)) return DRAGOUT_ERR_BUTTON_RELEASED;

    // GDK takes the serial of the pointer's last button press for the Wayland
    // request and grabs the pointer itself on X11, so no event is needed.
    GdkDrag *drag = gdk_drag_begin(surface, pointer, req->content, GDK_ACTION_COPY, 0, 0);
    if (drag == NULL) return DRAGOUT_ERR_START_FAILED;

    set_icon(drag, req->label);

    Session *s = g_new0(Session, 1);
    s->drag = drag;
    s->handle = req->handle;
    g_set_weak_pointer(&s->web_view, find_web_view(GTK_WIDGET(req->window)));
    g_signal_connect(drag, "drop-performed", G_CALLBACK(on_drop_performed), s);
    g_signal_connect(drag, "dnd-finished", G_CALLBACK(on_dnd_finished), s);
    g_signal_connect(drag, "cancel", G_CALLBACK(on_cancel), s);
    s->watchdog_id = g_timeout_add(WATCHDOG_INTERVAL_MS, on_watchdog, s);
    return DRAGOUT_DROPPED;
}

static gboolean begin_on_main(gpointer data) {
    Request *req = data;
    int result = begin(req);
    uintptr_t handle = req->handle;
    request_free(req);
    if (result != DRAGOUT_DROPPED) dragoutDone(handle, result);
    return G_SOURCE_REMOVE;
}

void dragout_start(void *window, char **paths, int n, const char *label, uintptr_t handle) {
    Request *req = g_new0(Request, 1);
    req->window = window ? g_object_ref(window) : NULL;
    req->content = content_new(paths, n);
    req->label = g_strdup(label);
    req->handle = handle;
    // High priority: every millisecond of delay is a chance for the button to
    // be released before the drag exists.
    g_main_context_invoke_full(NULL, G_PRIORITY_HIGH, begin_on_main, req, NULL);
}
