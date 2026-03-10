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

static void set_no_focus(void *gtkWindow) {
	gtk_window_set_accept_focus(GTK_WINDOW(gtkWindow), FALSE);
	gtk_window_set_focus_on_map(GTK_WINDOW(gtkWindow), FALSE);
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
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	webview "github.com/webview/webview_go"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/util"
)

type OverlayRenderer struct {
	w         webview.WebView
	md        goldmark.Markdown
	chromaCS  string
	streamBuf strings.Builder
	pendingMu sync.Mutex
	pendingJS strings.Builder
	closed    atomic.Bool
	onAction      func(HotkeyAction)
	onToggleChunk func(int, bool)
	provider      Provider
	vuJS      atomic.Pointer[string]

	sandboxMu   sync.Mutex
	sandboxCode string
	sandboxLang string
}

func NewOverlayRenderer() *OverlayRenderer {
	// Create GTK window with RGBA visual BEFORE webview touches it
	gtkWin := C.create_rgba_window()
	C.set_no_focus(gtkWin)
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

	w.Bind("_toggleChunk", func(id int, checked bool) {
		if o.onToggleChunk == nil {
			return
		}
		o.onToggleChunk(id, checked)
	})

	// Bind a JS-callable function that drains pending JS updates.
	// The JS side polls this every 33ms via setInterval.
	// Because Bind callbacks run on the GTK main thread, this avoids
	// all cross-thread Dispatch calls that were causing SIGSEGV.
	w.Bind("_pollUpdates", func() string {
		o.pendingMu.Lock()
		js := o.pendingJS.String()
		o.pendingJS.Reset()
		o.pendingMu.Unlock()
		return js
	})

	w.Bind("_pollVU", func() string {
		p := o.vuJS.Swap(nil)
		if p == nil {
			return ""
		}
		return *p
	})

	w.Bind("_pollLog", func(after int) string {
		entries := AppLog.Since(after)
		if len(entries) == 0 {
			return ""
		}
		b, _ := json.Marshal(entries)
		return string(b)
	})

	w.Bind("_sendToSandbox", func(code, lang string) {
		AppLog.Info("overlay: sendToSandbox lang=%s code=%d bytes", lang, len(code))
		o.sandboxMu.Lock()
		o.sandboxCode = code
		o.sandboxLang = lang
		o.sandboxMu.Unlock()

		js := "document.getElementById('sandbox-editor').value=" + jsString(code) + ";" +
			"document.getElementById('sandbox-lang').textContent=" + jsString(lang) + ";" +
			"document.getElementById('sandbox-tests').value='';" +
			"document.getElementById('sandbox-tests-hl').innerHTML='';" +
			"document.getElementById('sandbox-tests-wrap').style.display='none';" +
			"document.getElementById('sandbox-tests-label').style.display='none';" +
			"switchTab('sandbox');" +
			"_autoSize(document.getElementById('sandbox-editor'));" +
			"_syncHighlight('sandbox-editor','sandbox-editor-hl');" +
			"document.getElementById('sandbox-output').innerHTML='<span style=\"color:#888\">running...</span>';"
		o.eval(js)

		go func() {
			res := RunSandbox(code, lang)
			o.renderSandboxResult(res)
		}()
	})

	w.Bind("_runSandbox", func(code, tests, lang string) {
		combined := code
		if tests != "" {
			combined = code + "\n" + tests
		}
		AppLog.Info("overlay: runSandbox lang=%s code=%d bytes", lang, len(combined))
		if combined == "" {
			AppLog.Warn("overlay: runSandbox called with empty code")
			return
		}
		o.sandboxMu.Lock()
		o.sandboxCode = combined
		o.sandboxLang = lang
		o.sandboxMu.Unlock()
		o.eval("document.getElementById('sandbox-output').innerHTML='<span style=\"color:#888\">running...</span>';")

		go func() {
			res := RunSandbox(combined, lang)
			o.renderSandboxResult(res)
		}()
	})

	w.Bind("_genTests", func(code, lang string) {
		if o.provider == nil {
			o.eval("document.getElementById('sandbox-output').innerHTML='<span class=\"sandbox-fail\">error: no provider configured</span>';")
			return
		}
		if code == "" {
			return
		}
		o.eval("document.getElementById('sandbox-output').innerHTML='<span style=\"color:#888\">generating tests...</span>';")

		go func() {
			prompt := "Generate test assertions for the following " + lang + " code. " +
				"Do NOT redeclare or redefine any functions/classes — just call them. " +
				"Use assertions with expected values (e.g. console.assert, assert, if/throw) that print a failure message when wrong and print nothing when correct. " +
				"At the end, print a summary line like 'N/N tests passed'. " +
				"Only output raw executable code — no markdown fences, no explanation.\n\n" + code
			result, err := o.provider.Summarize(prompt)
			if err != nil {
				o.eval("document.getElementById('sandbox-output').innerHTML=" + jsString(`<span class="sandbox-fail">error: `+err.Error()+`</span>`) + ";")
				return
			}

			showTests := "document.getElementById('sandbox-tests-label').style.display='block';" +
				"document.getElementById('sandbox-tests-wrap').style.display='block';" +
				"document.getElementById('sandbox-tests').value=" + jsString(result) + ";" +
				"_autoSize(document.getElementById('sandbox-tests'));" +
				"_syncHighlight('sandbox-tests','sandbox-tests-hl');"
			o.eval(showTests)
			o.eval("document.getElementById('sandbox-output').innerHTML='<span style=\"color:#888\">running...</span>';")

			combined := code + "\n" + result
			o.sandboxMu.Lock()
			o.sandboxCode = combined
			o.sandboxLang = lang
			o.sandboxMu.Unlock()

			res := RunSandbox(combined, lang)
			o.renderSandboxResult(res)
		}()
	})

	w.Bind("_highlight", func(code, lang string) string {
		lexer := lexers.Get(lang)
		if lexer == nil {
			lexer = lexers.Fallback
		}
		tokens, err := lexer.Tokenise(nil, code)
		if err != nil {
			return escapeHTML(code)
		}
		formatter := chromahtml.New(chromahtml.WithClasses(true))
		var buf bytes.Buffer
		_ = formatter.Format(&buf, styles.Get("monokai"), tokens)
		return buf.String()
	})

	w.SetHtml(o.buildShell("Ready."))
	C.show_window(gtkWin)
	return o
}

func (o *OverlayRenderer) SetActionHandler(fn func(HotkeyAction)) {
	o.onAction = fn
}

func (o *OverlayRenderer) SetToggleChunkHandler(fn func(int, bool)) {
	o.onToggleChunk = fn
}

func (o *OverlayRenderer) SetProvider(p Provider) { o.provider = p }

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
		"document.getElementById('footer-status').textContent='';_injectSandboxButtons();"
	o.eval(js)
	return nil
}

func (o *OverlayRenderer) SetStatus(status string) {
	js := "document.getElementById('footer-status').textContent=" + jsString(status) + ";"
	o.eval(js)
}

func (o *OverlayRenderer) StreamStart() {
	o.streamBuf.Reset()
	js := "window._autoScroll=true;" +
		"document.getElementById('chat-content').innerHTML='<pre id=\"stream\"></pre>';" +
		"document.getElementById('footer-status').textContent='';"
	o.eval(js)
}

func (o *OverlayRenderer) StreamDelta(delta string) {
	o.streamBuf.WriteString(delta)
	js := "var s=document.getElementById('stream');if(s){s.textContent+=" + jsString(delta) + ";if(window._autoScroll)s.scrollIntoView(false);}"
	o.eval(js)
}

func (o *OverlayRenderer) StreamDone() {
	html, err := o.markdownToHTML(o.streamBuf.String())
	if err != nil {
		html = "<pre>" + escapeHTML(o.streamBuf.String()) + "</pre>"
	}
	wrapped := `<div class="response-block">` + html +
		`<button class="explain-btn" onclick="_action('explain')" title="Explain further">?</button></div>`
	js := "var c=document.getElementById('chat-content'),st=c.scrollTop;" +
		"c.innerHTML=" + jsString(wrapped) + ";_injectSandboxButtons();" +
		"if(!window._autoScroll)c.scrollTop=st;"
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamStart() {
	o.streamBuf.Reset()
	js := `window._autoScroll=true;var c=document.getElementById('chat-content');` +
		`c.innerHTML+='<hr><h3 style="color:#7ec8e3">▼ follow-up</h3><pre id="stream"></pre>';` +
		`document.getElementById('footer-status').textContent='';` +
		`c.scrollTop=c.scrollHeight;`
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamDelta(delta string) {
	o.streamBuf.WriteString(delta)
	js := "var s=document.getElementById('stream');if(s){s.textContent+=" + jsString(delta) + ";if(window._autoScroll)s.scrollIntoView(false);}"
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
		`if(s){var c=document.getElementById('chat-content'),st=c.scrollTop;` +
		`var d=document.createElement('div');d.innerHTML=` + jsString(wrapped) + `;s.replaceWith(d);` +
		`if(window._autoScroll)d.scrollIntoView(false);else c.scrollTop=st;}_injectSandboxButtons();`
	o.eval(js)
}

func (o *OverlayRenderer) AppendTranscriptChunk(source, text string, id int) {
	ts := time.Now().Format("15:04:05")
	srcClass := "src-" + source
	chunk := fmt.Sprintf(
		`<div class="transcript-chunk" data-id="%d">`+
			`<input type="checkbox" class="chunk-cb" onchange="_toggleChunk(%d,this.checked)">`+
			`<span class="ts">[%s</span> <span class="src %s">%s</span><span class="ts">]</span> %s</div>`,
		id, id, escapeHTML(ts), srcClass, escapeHTML(source), escapeHTML(text))
	js := `var t=document.getElementById('transcript-content');` +
		`t.innerHTML+=` + jsString(chunk) + `;` +
		`if(window._transcriptAutoScroll){t.scrollTop=t.scrollHeight;}`
	o.eval(js)
}

func (o *OverlayRenderer) ClearTranscriptCheckboxes() {
	js := `document.querySelectorAll('.chunk-cb').forEach(function(cb){cb.checked=false;});`
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

func (o *OverlayRenderer) SetSoundCheck(active bool) {
	scLabel := keyLabels[HotkeySoundCheck]
	label := escapeHTML(scLabel)
	bg, color := "''", "''"
	vuDisplay := "'none'"
	if active {
		label = `<span class="rec-dot"></span>` + escapeHTML(scLabel)
		bg, color = "'rgba(220,50,50,0.7)'", "'#fff'"
		vuDisplay = "'flex'"
	}
	js := fmt.Sprintf(`var b=document.getElementById('btn-soundcheck');if(b){b.style.background=%s;b.style.color=%s;b.innerHTML=%s;}`+
		`document.getElementById('vu-meters').style.display=%s;`, bg, color, jsString(label), vuDisplay)
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

func (o *OverlayRenderer) UpdateVU(micLevel, audioLevel float64) {
	js := fmt.Sprintf(
		"var m=document.getElementById('vu-mic'),a=document.getElementById('vu-audio');"+
			"if(m){m.style.width='%.0f%%';m.style.background=%s;}"+
			"if(a){a.style.width='%.0f%%';a.style.background=%s;}",
		micLevel*100, vuColor(micLevel),
		audioLevel*100, vuColor(audioLevel),
	)
	o.vuJS.Store(&js)
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

func (o *OverlayRenderer) renderSandboxResult(r SandboxResult) {
	AppLog.Info("overlay: rendering sandbox result exit=%d", r.ExitCode)
	var parts []string
	if r.Stdout != "" {
		parts = append(parts, escapeHTML(r.Stdout))
	}
	if r.Stderr != "" {
		parts = append(parts, `<span class="sandbox-fail">`+escapeHTML(r.Stderr)+`</span>`)
	}
	if r.Error != "" {
		parts = append(parts, `<span class="sandbox-fail">error: `+escapeHTML(r.Error)+`</span>`)
	}

	cls := "sandbox-ok"
	if r.ExitCode != 0 {
		cls = "sandbox-fail"
	}
	footer := fmt.Sprintf(`<span class="%s">exit %d</span>`, cls, r.ExitCode)
	html := strings.Join(parts, "\n") + "\n" + footer

	o.eval("document.getElementById('sandbox-output').innerHTML=" + jsString(html) + ";")
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
  display: flex; align-items: flex-start; gap: 4px;
  padding: 4px 0; border-bottom: 1px solid rgba(255,255,255,0.05);
  font-size: 12px;
}
.chunk-cb { margin-top: 2px; flex-shrink: 0; cursor: pointer; accent-color: #7ec8e3; }
.transcript-chunk .ts { color: #888; }
.transcript-chunk .src { font-weight: bold; }
.src-audio { color: #7ec8e3; }
.src-mic { color: #e05050; }
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
#vu-meters { display:none; gap:8px; padding:2px 12px; align-items:center; }
.vu-label { font-size:9px; color:#888; width:32px; }
.vu-track { flex:1; height:4px; background:rgba(255,255,255,0.1); border-radius:2px; overflow:hidden; }
.vu-fill { height:100%; width:0%; border-radius:2px; }
.editor-wrap { position:relative; }
.editor-highlight { position:absolute; top:0; left:0; right:0; bottom:0; margin:0; pointer-events:none; overflow:hidden; font-family:inherit; font-size:12px; line-height:1.5; padding:10px; border:1px solid transparent; border-radius:4px; white-space:pre; tab-size:2; background:transparent; }
.editor-highlight code { font-family:inherit; }
#sandbox-editor, #sandbox-tests { position:relative; z-index:1; background:rgba(0,0,0,0.35); color:transparent; caret-color:#e0e0e0; font-family:inherit; font-size:12px; line-height:1.5; border:1px solid rgba(255,255,255,0.15); border-radius:4px; padding:10px; resize:none; overflow-y:auto; white-space:pre; overflow-wrap:normal; overflow-x:auto; tab-size:2; width:100%; }
#sandbox-editor { min-height:60px; max-height:50vh; }
#sandbox-editor:focus { outline:none; border-color:rgba(126,200,227,0.4); }
#sandbox-editor::selection { background:rgba(126,200,227,0.3); }
#sandbox-tests-label { color:#7ec8e3; font-size:11px; margin-top:8px; display:none; }
#sandbox-tests { min-height:40px; max-height:30vh; background:rgba(0,0,0,0.25); border-color:rgba(80,140,220,0.3); }
#sandbox-tests:focus { outline:none; border-color:rgba(80,140,220,0.5); }
#sandbox-tests::selection { background:rgba(80,140,220,0.3); }
#sandbox-controls { display:flex; gap:8px; align-items:center; padding:4px 0; }
#sandbox-run { background:rgba(80,180,80,0.3); border:1px solid rgba(80,180,80,0.5); color:#7ec8e3; font:inherit; font-size:12px; padding:4px 12px; border-radius:3px; cursor:pointer; }
#sandbox-run:hover { background:rgba(80,180,80,0.5); }
#sandbox-test { background:rgba(80,140,220,0.3); border:1px solid rgba(80,140,220,0.5); color:#7ec8e3; font:inherit; font-size:12px; padding:4px 12px; border-radius:3px; cursor:pointer; }
#sandbox-test:hover { background:rgba(80,140,220,0.5); }
#sandbox-lang { color:#888; font-size:11px; }
#sandbox-output { background:rgba(0,0,0,0.5); padding:10px; border-radius:4px; font-size:12px; white-space:pre-wrap; max-height:400px; overflow-y:auto; margin-top:8px; }
.sandbox-btn { position:absolute; bottom:4px; right:4px; background:rgba(80,180,80,0.2); border:1px solid rgba(80,180,80,0.4); color:#7ec8e3; font-size:11px; padding:1px 8px; border-radius:3px; cursor:pointer; }
.sandbox-btn:hover { background:rgba(80,180,80,0.4); color:#fff; }
.sandbox-ok { color:#50b050; } .sandbox-fail { color:#e05050; }
#log-output { font-size:11px; white-space:pre-wrap; color:#ccc; max-height:100%; overflow-y:auto; }
.log-error { color:#e05050; } .log-warn { color:#e8a735; } .log-info { color:#888; }
` + o.chromaCS + `
</style></head><body>
<div id="drag-handle">&#x2630; sn-monitor</div>
<div id="status">` + escapeHTML(statusText) + `</div>
<div id="tab-bar">
  <button id="tab-chat" class="active" onclick="switchTab('chat')">Chat</button>
  <button id="tab-transcript" onclick="switchTab('transcript')">Transcript</button>
  <button id="tab-sandbox" onclick="switchTab('sandbox')">Sandbox</button>
  <button id="tab-log" onclick="switchTab('log')">Log</button>
</div>
<div id="chat-content" class="tab-content active"></div>
<div id="transcript-content" class="tab-content"></div>
<div id="sandbox-content" class="tab-content">
  <div class="editor-wrap">
    <pre class="editor-highlight"><code id="sandbox-editor-hl"></code></pre>
    <textarea id="sandbox-editor" spellcheck="false" placeholder="Paste or send code here..."></textarea>
  </div>
  <div id="sandbox-tests-label">Generated tests:</div>
  <div class="editor-wrap" style="display:none" id="sandbox-tests-wrap">
    <pre class="editor-highlight"><code id="sandbox-tests-hl"></code></pre>
    <textarea id="sandbox-tests" spellcheck="false" placeholder="Generated tests appear here..."></textarea>
  </div>
  <div id="sandbox-controls">
    <button id="sandbox-run" onclick="_runCombined()">&#9654; Run</button>
    <button id="sandbox-test" onclick="_genTests(document.getElementById('sandbox-editor').value,document.getElementById('sandbox-lang').textContent)">&#9654; Test</button>
    <span id="sandbox-lang"></span>
  </div>
  <pre id="sandbox-output"></pre>
</div>
<div id="log-content" class="tab-content"><pre id="log-output"></pre></div>
<div id="footer"><div id="footer-status"></div>
<div id="vu-meters">
  <span class="vu-label">mic</span>
  <div class="vu-track"><div id="vu-mic" class="vu-fill"></div></div>
  <span class="vu-label">audio</span>
  <div class="vu-track"><div id="vu-audio" class="vu-fill"></div></div>
</div>
<div id="footer-btns">
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
window._autoSize=function(el){el.style.height='auto';el.style.height=el.scrollHeight+'px';};
window._hlTimers={};
window._syncHighlight=function(taId,hlId){
  var ta=document.getElementById(taId);
  var hl=document.getElementById(hlId);
  if(!ta||!hl)return;
  var lang=document.getElementById('sandbox-lang').textContent||'';
  _highlight(ta.value,lang).then(function(html){hl.innerHTML=html;});
};
window._debouncedSync=function(taId,hlId){
  clearTimeout(window._hlTimers[taId]);
  window._hlTimers[taId]=setTimeout(function(){_syncHighlight(taId,hlId);},150);
};
var edTA=document.getElementById('sandbox-editor');
var tsTA=document.getElementById('sandbox-tests');
edTA.addEventListener('input',function(){_autoSize(this);_debouncedSync('sandbox-editor','sandbox-editor-hl');});
tsTA.addEventListener('input',function(){_autoSize(this);_debouncedSync('sandbox-tests','sandbox-tests-hl');});
edTA.addEventListener('scroll',function(){var p=this.parentNode.querySelector('.editor-highlight');p.scrollTop=this.scrollTop;p.scrollLeft=this.scrollLeft;});
tsTA.addEventListener('scroll',function(){var p=this.parentNode.querySelector('.editor-highlight');p.scrollTop=this.scrollTop;p.scrollLeft=this.scrollLeft;});
window._runCombined=function(){
  var code=document.getElementById('sandbox-editor').value;
  var tests=document.getElementById('sandbox-tests').value;
  var lang=document.getElementById('sandbox-lang').textContent;
  _runSandbox(code,tests,lang);
};
window._autoScroll=true;
(function(){
  var cc=document.getElementById('chat-content');
  cc.addEventListener('wheel',function(){window._autoScroll=false;});
  cc.addEventListener('scroll',function(){
    if(cc.scrollTop+cc.clientHeight>=cc.scrollHeight-5)window._autoScroll=true;
  });
})();
window._transcriptAutoScroll=true;
(function(){
  var tc=document.getElementById('transcript-content');
  tc.addEventListener('wheel',function(){window._transcriptAutoScroll=false;});
  tc.addEventListener('scroll',function(){
    if(tc.scrollTop+tc.clientHeight>=tc.scrollHeight-5)window._transcriptAutoScroll=true;
  });
})();
window._logBadge=0;
window._logIdx=-1;
window._sandboxLangs={'python':1,'go':1,'javascript':1,'js':1,'typescript':1,'ts':1,'cpp':1,'c++':1,'rust':1,'java':1};
window.switchTab=function(name){
  var tabs=['chat','transcript','sandbox','log'];
  for(var i=0;i<tabs.length;i++){
    var btn=document.getElementById('tab-'+tabs[i]);
    var div=document.getElementById(tabs[i]+'-content');
    if(tabs[i]===name){btn.className='active';div.className='tab-content active';}
    else{btn.className='';div.className='tab-content';}
  }
  if(name==='transcript'){
    window._transcriptAutoScroll=true;
    document.getElementById('transcript-content').scrollTop=document.getElementById('transcript-content').scrollHeight;
  }
  if(name==='log'){
    window._logBadge=0;
    var b=document.getElementById('tab-log').querySelector('.tab-badge');if(b)b.remove();
    document.getElementById('log-output').scrollTop=document.getElementById('log-output').scrollHeight;
  }
};
window._injectSandboxButtons=function(){
  var wraps=document.querySelectorAll('#chat-content .highlight[data-lang]');
  for(var i=0;i<wraps.length;i++){
    var wrap=wraps[i];
    var lang=wrap.getAttribute('data-lang');
    if(!window._sandboxLangs[lang])continue;
    var pre=wrap.querySelector('pre');
    if(!pre||pre.querySelector('.sandbox-btn'))continue;
    var code=pre.querySelector('code')||pre;
    pre.style.position='relative';
    var btn=document.createElement('button');
    btn.className='sandbox-btn';
    btn.textContent='\u25B6 Sandbox';
    btn.onclick=(function(c,l){return function(){_sendToSandbox(c.textContent,l);};})(code,lang);
    pre.appendChild(btn);
  }
};
(function vuPump(){
  _pollVU().then(function(js){if(js)eval(js);});
  requestAnimationFrame(vuPump);
})();
setInterval(function(){
  _pollUpdates().then(function(js){if(js)eval(js);});
},33);
setInterval(function(){
  _pollLog(window._logIdx).then(function(raw){
    if(!raw)return;
    var entries=JSON.parse(raw);
    if(!entries||!entries.length)return;
    var out=document.getElementById('log-output');
    var logTab=document.getElementById('log-content');
    var isActive=logTab.classList.contains('active');
    for(var i=0;i<entries.length;i++){
      var e=entries[i];
      var ts=e.Time?e.Time.substring(11,19):'';
      var cls='log-'+e.Level;
      out.innerHTML+='<span class="'+cls+'">'+ts+' ['+e.Level+'] '+e.Message+'</span>\n';
      window._logIdx=e.Index;
    }
    if(isActive){out.scrollTop=out.scrollHeight;return;}
    var errs=0;for(var j=0;j<entries.length;j++){if(entries[j].Level==='error')errs++;}
    if(!errs)return;
    window._logBadge+=errs;
    var btn=document.getElementById('tab-log');
    var b=btn.querySelector('.tab-badge');
    if(!b){b=document.createElement('span');b.className='tab-badge';btn.appendChild(b);}
    b.textContent=window._logBadge;
  });
},500);
</script>
</body></html>`
}

func newMarkdownRenderer() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			highlighting.NewHighlighting(
				highlighting.WithStyle("monokai"),
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				highlighting.WithWrapperRenderer(func(w util.BufWriter, ctx highlighting.CodeBlockContext, entering bool) {
					lang, ok := ctx.Language()
					if entering {
						if ok {
							_, _ = fmt.Fprintf(w, `<div class="highlight" data-lang="%s">`, string(lang))
							return
						}
						w.WriteString(`<div class="highlight">`)
						return
					}
					w.WriteString(`</div>`)
				}),
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

func vuColor(level float64) string {
	if level > 0.75 {
		return "'#e05050'"
	}
	if level > 0.45 {
		return "'#e8a735'"
	}
	return "'#50b050'"
}

var buttonIDs = map[HotkeyAction]string{
	HotkeyFollowUp:     "btn-voice",
	HotkeyAudioCapture: "btn-audio",
	HotkeySoundCheck:   "btn-soundcheck",
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
