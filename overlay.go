package main

/*
#cgo linux pkg-config: gtk+-3.0 gdk-x11-3.0 webkit2gtk-4.1
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
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	webview "github.com/webview/webview_go"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

type OverlayRenderer struct {
	w         webview.WebView
	md        goldmark.Markdown
	chromaCS  string
	streamBuf strings.Builder
	pendingMu sync.Mutex
	pendingJS strings.Builder
	closed    atomic.Bool
	onAction  func(HotkeyAction)
}

func NewOverlayRenderer() *OverlayRenderer {
	// Create GTK window with RGBA visual BEFORE webview touches it
	gtkWin := C.create_rgba_window()
	w := webview.NewWindow(false, unsafe.Pointer(gtkWin))
	w.SetTitle("sn-monitor")
	w.SetSize(700, 900, webview.HintNone)

	// Now the webkit child exists — make its background transparent
	C.set_transparent(gtkWin)

	o := &OverlayRenderer{
		w:        w,
		md:       newMarkdownRenderer(),
		chromaCS: generateChromaCSS(),
	}

	// Bind action handler for clickable overlay buttons.
	// Build reverse map from actionNames (name→action).
	nameToAction := make(map[string]HotkeyAction, len(actionNames))
	for a, n := range actionNames {
		nameToAction[n] = a
	}
	w.Bind("_action", func(name string) {
		if o.onAction == nil {
			return
		}
		a, ok := nameToAction[name]
		if !ok {
			return
		}
		o.onAction(a)
	})

	// Bind a JS-callable function that drains pending JS updates.
	// The JS side polls this every 80ms via setInterval.
	// Because Bind callbacks run on the GTK main thread, this avoids
	// all cross-thread Dispatch calls that were causing SIGSEGV.
	w.Bind("_pollUpdates", func() string {
		o.pendingMu.Lock()
		js := o.pendingJS.String()
		o.pendingJS.Reset()
		o.pendingMu.Unlock()
		return js
	})

	w.SetHtml(o.buildShell("Ready."))
	C.show_window(gtkWin)
	return o
}

func (o *OverlayRenderer) SetActionHandler(fn func(HotkeyAction)) {
	o.onAction = fn
}

// eval queues a JS snippet to be executed on the next poll cycle.
func (o *OverlayRenderer) eval(js string) {
	if o.closed.Load() {
		return
	}
	o.pendingMu.Lock()
	o.pendingJS.WriteString(js)
	o.pendingMu.Unlock()
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
	o.closed.Store(true)
	o.w.Destroy()
}

func (o *OverlayRenderer) Render(markdown string) error {
	html, err := o.markdownToHTML(markdown)
	if err != nil {
		html = "<pre>" + escapeHTML(markdown) + "</pre>"
	}

	js := "document.getElementById('chat-content').innerHTML=" + jsString(html) + ";" +
		"document.getElementById('footer-status').textContent='';"
	o.eval(js)
	return nil
}

func (o *OverlayRenderer) SetStatus(status string) {
	js := "document.getElementById('footer-status').textContent=" + jsString(status) + ";"
	o.eval(js)
}

func (o *OverlayRenderer) StreamStart() {
	o.streamBuf.Reset()
	js := "document.getElementById('chat-content').innerHTML='<pre id=\"stream\"></pre>';" +
		"document.getElementById('footer-status').textContent='';"
	o.eval(js)
}

func (o *OverlayRenderer) StreamDelta(delta string) {
	o.streamBuf.WriteString(delta)
	js := "var s=document.getElementById('stream');if(s)s.textContent+=" + jsString(delta) + ";"
	o.eval(js)
}

func (o *OverlayRenderer) StreamDone() {
	html, err := o.markdownToHTML(o.streamBuf.String())
	if err != nil {
		html = "<pre>" + escapeHTML(o.streamBuf.String()) + "</pre>"
	}
	wrapped := `<div class="response-block">` + html +
		`<button class="explain-btn" onclick="_action('explain')" title="Explain further">?</button></div>`
	js := "document.getElementById('chat-content').innerHTML=" + jsString(wrapped) + ";"
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamStart() {
	o.streamBuf.Reset()
	js := `var c=document.getElementById('chat-content');` +
		`c.innerHTML+='<hr><h3 style="color:#7ec8e3">▼ follow-up</h3><pre id="stream"></pre>';` +
		`document.getElementById('footer-status').textContent='';` +
		`c.scrollTop=c.scrollHeight;`
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamDelta(delta string) {
	o.streamBuf.WriteString(delta)
	js := "var s=document.getElementById('stream');if(s){s.textContent+=" + jsString(delta) + ";s.scrollIntoView(false);}"
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamDone() {
	html, err := o.markdownToHTML(o.streamBuf.String())
	if err != nil {
		html = "<pre>" + escapeHTML(o.streamBuf.String()) + "</pre>"
	}
	wrapped := `<div class="response-block">` + html +
		`<button class="explain-btn" onclick="_action('explain')" title="Explain further">?</button></div>`
	js := `var s=document.getElementById('stream');` +
		`if(s){var d=document.createElement('div');d.innerHTML=` + jsString(wrapped) + `;s.replaceWith(d);d.scrollIntoView(false);}`
	o.eval(js)
}

func (o *OverlayRenderer) AppendTranscriptChunk(source, text string) {
	ts := time.Now().Format("15:04:05")
	chunk := fmt.Sprintf(`<div class="transcript-chunk"><span class="ts">[%s</span> <span class="src">%s</span><span class="ts">]</span> %s</div>`,
		escapeHTML(ts), escapeHTML(source), escapeHTML(text))
	js := `var t=document.getElementById('transcript-content');` +
		`t.innerHTML+=` + jsString(chunk) + `;` +
		`if(t.classList.contains('active')){t.scrollTop=t.scrollHeight;}` +
		`else{window._transcriptBadge++;var btn=document.getElementById('tab-transcript');` +
		`var b=btn.querySelector('.tab-badge');if(!b){b=document.createElement('span');b.className='tab-badge';btn.appendChild(b);}` +
		`b.textContent=window._transcriptBadge;}`
	o.eval(js)
}

func (o *OverlayRenderer) SetAudioRecording(recording bool) {
	audioLabel := keyLabels[HotkeyAudioCapture]
	label := audioLabel
	bg, color := "''", "''"
	if recording {
		label = `<span class="rec-dot"></span>` + escapeHTML(audioLabel)
		bg, color = "'rgba(220,50,50,0.7)'", "'#fff'"
	}
	js := fmt.Sprintf(`var b=document.getElementById('btn-audio');if(b){b.style.background=%s;b.style.color=%s;b.innerHTML=%s;}`, bg, color, jsString(label))
	o.eval(js)
}

func (o *OverlayRenderer) SetMicRecording(recording bool) {
	micLabel := keyLabels[HotkeyFollowUp]
	label := escapeHTML(micLabel)
	bg, color := "''", "''"
	if recording {
		label = `<span class="rec-dot"></span>` + escapeHTML(micLabel)
		bg, color = "'rgba(220,50,50,0.7)'", "'#fff'"
	}
	js := fmt.Sprintf(`var b=document.getElementById('btn-voice');if(b){b.style.background=%s;b.style.color=%s;b.innerHTML=%s;}`, bg, color, jsString(label))
	o.eval(js)
}

func (o *OverlayRenderer) Clear() {
	js := "document.getElementById('chat-content').innerHTML='';" +
		"document.getElementById('transcript-content').innerHTML='';" +
		"document.getElementById('footer-status').textContent='Cleared.';"
	o.eval(js)
}

func (o *OverlayRenderer) Close() {
	o.closed.Store(true)
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
#tab-bar {
  display: flex; gap: 0; border-bottom: 1px solid rgba(255,255,255,0.2);
}
#tab-bar button {
  flex: 1; background: rgba(255,255,255,0.05); border: none;
  color: #888; font: inherit; font-size: 12px; padding: 6px 12px;
  cursor: pointer; position: relative;
}
#tab-bar button.active {
  background: rgba(255,255,255,0.12); color: #e0e0e0;
  border-bottom: 2px solid #7ec8e3;
}
#tab-bar button:hover { background: rgba(255,255,255,0.1); }
.tab-badge {
  display: inline-block; background: #e8a735; color: #000;
  font-size: 9px; padding: 0 4px; border-radius: 8px;
  margin-left: 4px; vertical-align: top; min-width: 14px; text-align: center;
}
.tab-content {
  padding: 8px 12px 52px; word-wrap: break-word; overflow-wrap: break-word;
  display: none;
}
.tab-content.active { display: block; }
.tab-content h1,.tab-content h2,.tab-content h3 { color: #7ec8e3; margin: 12px 0 6px; }
.tab-content h1 { font-size: 18px; }
.tab-content h2 { font-size: 16px; }
.tab-content h3 { font-size: 14px; }
.tab-content p { margin: 6px 0; }
.tab-content ul,.tab-content ol { margin: 6px 0 6px 20px; }
.tab-content pre {
  background: rgba(0,0,0,0.35);
  padding: 10px;
  border-radius: 4px;
  overflow-x: auto;
  margin: 8px 0;
  font-size: 12px;
  white-space: pre-wrap;
  word-wrap: break-word;
}
.tab-content code { font-family: inherit; }
.tab-content p code {
  background: rgba(255,255,255,0.1);
  padding: 1px 4px;
  border-radius: 3px;
}
.tab-content table { border-collapse: collapse; margin: 8px 0; }
.tab-content th,.tab-content td { border: 1px solid #555; padding: 4px 8px; }
.tab-content th { background: rgba(255,255,255,0.08); }
.tab-content strong { color: #f0f0f0; }
.tab-content hr { border: none; border-top: 1px solid #555; margin: 12px 0; }
.response-block { position: relative; }
.explain-btn {
  position: absolute; top: 4px; right: 4px;
  background: rgba(255,255,255,0.1); border: 1px solid rgba(255,255,255,0.2);
  color: #7ec8e3; font-size: 14px; width: 22px; height: 22px;
  border-radius: 50%; cursor: pointer; line-height: 20px; text-align: center;
}
.explain-btn:hover { background: rgba(126,200,227,0.2); color: #fff; }
.transcript-chunk {
  padding: 4px 0; border-bottom: 1px solid rgba(255,255,255,0.05);
  font-size: 12px;
}
.transcript-chunk .ts { color: #888; }
.transcript-chunk .src { color: #7ec8e3; font-weight: bold; }
@keyframes pulse-dot { 0%,100% { opacity: 1; } 50% { opacity: 0.3; } }
.rec-dot {
  display: inline-block; width: 8px; height: 8px;
  background: #ff4444; border-radius: 50%;
  margin-right: 4px; vertical-align: middle;
  animation: pulse-dot 1s ease-in-out infinite;
}
#footer {
  position: fixed; bottom: 0; left: 0; right: 0;
  background: rgba(20,20,20,0.85);
  border-top: 1px solid rgba(255,255,255,0.15);
  color: #888; font-size: 11px; padding: 4px 12px;
  text-align: center;
}
#footer-status { color: #e8a735; font-size: 11px; margin-bottom: 2px; }
#footer-btns { display:flex; gap:6px; justify-content:center; flex-wrap:wrap; }
#footer-btns button {
  background:rgba(255,255,255,0.08); border:1px solid rgba(255,255,255,0.2);
  color:#ccc; font:inherit; font-size:11px; padding:2px 8px; border-radius:3px;
  cursor:pointer;
}
#footer-btns button:hover { background:rgba(255,255,255,0.18); color:#fff; }
#footer-btns button:active { background:rgba(255,255,255,0.25); }
` + o.chromaCS + `
</style></head><body>
<div id="drag-handle">&#x2630; sn-monitor</div>
<div id="status">` + escapeHTML(statusText) + `</div>
<div id="tab-bar">
  <button id="tab-chat" class="active" onclick="switchTab('chat')">Chat</button>
  <button id="tab-transcript" onclick="switchTab('transcript')">Transcript</button>
</div>
<div id="chat-content" class="tab-content active"></div>
<div id="transcript-content" class="tab-content"></div>
<div id="footer"><div id="footer-status"></div><div id="footer-btns">
` + buildFooterButtons() + `
</div></div>
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
window._transcriptBadge=0;
window.switchTab=function(name){
  var chatBtn=document.getElementById('tab-chat');
  var transBtn=document.getElementById('tab-transcript');
  var chatDiv=document.getElementById('chat-content');
  var transDiv=document.getElementById('transcript-content');
  if(name==='transcript'){
    chatBtn.className='';transBtn.className='active';
    chatDiv.className='tab-content';transDiv.className='tab-content active';
    window._transcriptBadge=0;
    var b=transBtn.querySelector('.tab-badge');if(b)b.remove();
    transDiv.scrollTop=transDiv.scrollHeight;
  } else {
    transBtn.className='';chatBtn.className='active';
    transDiv.className='tab-content';chatDiv.className='tab-content active';
  }
};
setInterval(function(){
  _pollUpdates().then(function(js){if(js)eval(js);});
},80);
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

var buttonIDs = map[HotkeyAction]string{
	HotkeyFollowUp:     "btn-voice",
	HotkeyAudioCapture: "btn-audio",
}

func buildFooterButtons() string {
	var buf strings.Builder
	for _, a := range keyOrder {
		name := actionNames[a]
		label := keyLabels[a]
		id := buttonIDs[a]
		idAttr := ""
		if id != "" {
			idAttr = ` id="` + id + `"`
		}
		buf.WriteString(fmt.Sprintf(`<button%s onclick="_action('%s')">%s</button>`+"\n", idAttr, name, escapeHTML(label)))
	}
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
