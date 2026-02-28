package main

/*
#cgo linux pkg-config: gtk+-3.0 gdk-x11-3.0 webkit2gtk-4.0
#include <gdk/gdkx.h>
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

static unsigned long get_x11_xid(void *gtkWindow) {
	GdkWindow *gdkWin = gtk_widget_get_window(GTK_WIDGET(gtkWindow));
	if (!gdkWin) return 0;
	return gdk_x11_window_get_xid(GDK_WINDOW(gdkWin));
}

// Create a GtkWindow with RGBA visual set before realization.
// This MUST happen before webview.NewWindow() so the window
// supports per-pixel alpha from the start.
static void *create_rgba_window() {
	gtk_init(NULL, NULL);
	GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	GdkScreen *screen = gtk_widget_get_screen(win);
	GdkVisual *visual = gdk_screen_get_rgba_visual(screen);
	if (visual) {
		gtk_widget_set_visual(win, visual);
	}
	gtk_widget_set_app_paintable(win, TRUE);
	gtk_window_set_default_size(GTK_WINDOW(win), 700, 900);
	return (void *)win;
}

static void show_window(void *gtkWindow) {
	gtk_widget_show_all(GTK_WIDGET(gtkWindow));
}

// Paint the window background as fully transparent.
static gboolean on_draw(GtkWidget *widget, cairo_t *cr, gpointer data) {
	(void)widget; (void)data;
	cairo_set_source_rgba(cr, 0, 0, 0, 0);
	cairo_set_operator(cr, CAIRO_OPERATOR_SOURCE);
	cairo_paint(cr);
	return FALSE;
}

// Recursively find the WebKitWebView and set its background transparent.
static void find_and_clear_webkit(GtkWidget *widget) {
	if (WEBKIT_IS_WEB_VIEW(widget)) {
		GdkRGBA transparent = {0, 0, 0, 0};
		webkit_web_view_set_background_color(WEBKIT_WEB_VIEW(widget), &transparent);
		return;
	}
	if (GTK_IS_CONTAINER(widget)) {
		GList *children = gtk_container_get_children(GTK_CONTAINER(widget));
		for (GList *l = children; l != NULL; l = l->next) {
			find_and_clear_webkit(GTK_WIDGET(l->data));
		}
		g_list_free(children);
	}
}

// Set up transparency: draw handler on window + clear webkit bg.
static void set_transparent(void *gtkWindow) {
	GtkWidget *win = GTK_WIDGET(gtkWindow);
	g_signal_connect(win, "draw", G_CALLBACK(on_draw), NULL);
	find_and_clear_webkit(win);
}
*/
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	webview "github.com/webview/webview_go"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

type OverlayRenderer struct {
	w        webview.WebView
	md       goldmark.Markdown
	chromaCS string
}

func NewOverlayRenderer() *OverlayRenderer {
	// Create GTK window with RGBA visual BEFORE webview touches it
	gtkWin := C.create_rgba_window()
	w := webview.NewWindow(false, unsafe.Pointer(gtkWin))
	w.SetTitle("sn-monitor")
	w.SetSize(700, 900, webview.HintNone)

	// Now the webkit child exists — make its background transparent
	C.set_transparent(gtkWin)

	o := &OverlayRenderer{w: w, md: newMarkdownRenderer(), chromaCS: generateChromaCSS()}
	w.SetHtml(o.buildShell("Ready. Press Left+Right to capture."))
	C.show_window(gtkWin)
	return o
}

func (o *OverlayRenderer) Run() {
	o.w.Dispatch(func() {
		gtkWin := o.w.Window()
		xid := uint32(C.get_x11_xid(unsafe.Pointer(gtkWin)))
		if xid == 0 {
			return
		}
		if err := setAlwaysOnTop(xid); err != nil {
			fmt.Printf("warning: could not set always-on-top: %v\n", err)
		}
	})
	o.w.Run()
	o.w.Destroy()
}

func (o *OverlayRenderer) Render(markdown string) error {
	html, err := o.markdownToHTML(markdown)
	if err != nil {
		html = "<pre>" + escapeHTML(markdown) + "</pre>"
	}

	js := "document.getElementById('content').innerHTML=" + jsString(html) + ";" +
		"document.getElementById('status').textContent='';"
	o.w.Dispatch(func() { o.w.Eval(js) })
	return nil
}

func (o *OverlayRenderer) SetStatus(status string) {
	js := "document.getElementById('status').textContent=" + jsString(status) + ";"
	o.w.Dispatch(func() { o.w.Eval(js) })
}

func (o *OverlayRenderer) Close() {
	o.w.Terminate()
}

func (o *OverlayRenderer) markdownToHTML(md string) (string, error) {
	var buf bytes.Buffer
	err := o.md.Convert([]byte(md), &buf)
	return buf.String(), err
}

func (o *OverlayRenderer) buildShell(statusText string) string {
	return `<!DOCTYPE html><html><head><style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  background: rgba(20,20,20,0.35);
  color: #e0e0e0;
  font-family: 'JetBrains Mono','Fira Code','Cascadia Code',monospace;
  font-size: 13px;
  line-height: 1.5;
  padding: 0;
  overflow-y: auto;
}
#drag-handle {
  cursor: grab; height: 28px; background: rgba(255,255,255,0.15);
  text-align: center; font-size: 12px; color: #aaa;
  line-height: 28px; user-select: none;
  border-bottom: 1px solid rgba(255,255,255,0.2);
}
#drag-handle:active { cursor: grabbing; }
#status { color: #888; font-size: 12px; padding: 8px 12px 0; }
#content { padding: 8px 12px 12px; }
#content h1,#content h2,#content h3 { color: #7ec8e3; margin: 12px 0 6px; }
#content h1 { font-size: 18px; }
#content h2 { font-size: 16px; }
#content h3 { font-size: 14px; }
#content p { margin: 6px 0; }
#content ul,#content ol { margin: 6px 0 6px 20px; }
#content pre {
  background: rgba(0,0,0,0.35);
  padding: 10px;
  border-radius: 4px;
  overflow-x: auto;
  margin: 8px 0;
  font-size: 12px;
}
#content code { font-family: inherit; }
#content p code {
  background: rgba(255,255,255,0.1);
  padding: 1px 4px;
  border-radius: 3px;
}
#content table { border-collapse: collapse; margin: 8px 0; }
#content th,#content td { border: 1px solid #555; padding: 4px 8px; }
#content th { background: rgba(255,255,255,0.08); }
#content strong { color: #f0f0f0; }
#content hr { border: none; border-top: 1px solid #555; margin: 12px 0; }
` + o.chromaCS + `
</style></head><body>
<div id="drag-handle">&#x2630; sn-monitor</div>
<div id="status">` + escapeHTML(statusText) + `</div>
<div id="content"></div>
<script>
(function(){
  var h=document.getElementById('drag-handle'),d=false,sx,sy;
  h.onmousedown=function(e){d=true;sx=e.screenX;sy=e.screenY};
  document.onmousemove=function(e){
    if(!d)return;
    window.moveBy(e.screenX-sx,e.screenY-sy);
    sx=e.screenX;sy=e.screenY;
  };
  document.onmouseup=function(){d=false};
})();
</script>
</body></html>`
}

func newMarkdownRenderer() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			highlighting.NewHighlighting(
				highlighting.WithStyle("monokai"),
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
			),
		),
	)
}

func generateChromaCSS() string {
	var buf bytes.Buffer
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	style := styles.Get("monokai")
	formatter.WriteCSS(&buf, style)
	return buf.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
