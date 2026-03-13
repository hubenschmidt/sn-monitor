package render

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

static void set_keep_above(void *gtkWindow, int above) {
	gtk_window_set_keep_above(GTK_WINDOW(gtkWindow), above);
}

static void set_no_focus(void *gtkWindow) {
	gtk_window_set_accept_focus(GTK_WINDOW(gtkWindow), FALSE);
	gtk_window_set_focus_on_map(GTK_WINDOW(gtkWindow), FALSE);
}

static void set_accept_focus(void *gtkWindow, int accept) {
	gtk_window_set_accept_focus(GTK_WINDOW(gtkWindow), accept ? TRUE : FALSE);
	if (accept) {
		gtk_window_present(GTK_WINDOW(gtkWindow));
	}
}

static void move_window(void *gtkWindow, int x, int y) {
	gtk_window_move(GTK_WINDOW(gtkWindow), x, y);
}

static void resize_and_move(void *gtkWindow, int x, int y, int w, int h) {
	gtk_window_move(GTK_WINDOW(gtkWindow), x, y);
	gtk_window_resize(GTK_WINDOW(gtkWindow), w, h);
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

// fix_signal_handlers patches all signal handlers installed by
// GTK/WebKit to include SA_ONSTACK so the Go runtime can safely
// forward signals to them.
#include <signal.h>
static void fix_signal_handlers(void) {
	int sigs[] = {SIGSEGV, SIGBUS, SIGFPE, SIGPIPE, SIGABRT};
	for (int i = 0; i < (int)(sizeof(sigs)/sizeof(sigs[0])); i++) {
		struct sigaction sa;
		if (sigaction(sigs[i], NULL, &sa) != 0)
			continue;
		if (sa.sa_handler == SIG_DFL || sa.sa_handler == SIG_IGN)
			continue;
		if (sa.sa_flags & SA_ONSTACK)
			continue;
		sa.sa_flags |= SA_ONSTACK;
		sigaction(sigs[i], &sa, NULL);
	}
}
*/
import "C"

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	"second-nature/internal/applog"
	"second-nature/internal/audio"
	appctx "second-nature/internal/context"
	"second-nature/internal/model"
	"second-nature/internal/sandbox"
	"second-nature/internal/system"
)

//go:embed overlay.css
var overlayCSS string

//go:embed overlay.js
var overlayJS string

var ClearOnProcess atomic.Bool

type OverlayRenderer struct {
	w         webview.WebView
	gtkWin    unsafe.Pointer
	md        goldmark.Markdown
	chromaCS  string
	streamBuf strings.Builder
	pendingMu sync.Mutex
	pendingJS strings.Builder
	closed    atomic.Bool
	currentTraceID     int
	onAction           func(model.HotkeyAction)
	onToggleChunk      func(int, bool)
	onToggleScreenshot func(int, bool)
	onRemoveScreenshot func(int)
	onRemoveTraces     func([]int)
	onChatMessage      func(string)
	appState           *model.AppState
	ac                 *audio.AudioCapture
	provider           model.Provider
	vuJS   atomic.Pointer[string]
	fsGeom atomic.Pointer[[4]int]
	isFS       atomic.Bool
	needsRaise atomic.Bool

	sandboxMu   sync.Mutex
	sandboxCode string
	sandboxLang string
}

func NewOverlayRenderer() *OverlayRenderer {
	// Create GTK window with RGBA visual BEFORE webview touches it
	gtkWin := C.create_rgba_window()
	C.set_no_focus(gtkWin)
	w := webview.NewWindow(false, unsafe.Pointer(gtkWin))
	w.SetTitle("second-nature")
	w.SetSize(700, 900, webview.HintNone)

	// Now the webkit child exists — make its background transparent
	C.set_transparent(gtkWin)

	// Patch signal handlers installed by GTK/WebKit to include SA_ONSTACK
	// so the Go runtime can safely forward signals to them.
	C.fix_signal_handlers()

	o := &OverlayRenderer{
		w:        w,
		gtkWin:   unsafe.Pointer(gtkWin),
		md:       newMarkdownRenderer(),
		chromaCS: generateChromaCSS(),
	}

	// Bind action handler for clickable overlay buttons.
	nameToAction := make(map[string]model.HotkeyAction, len(model.ActionNames))
	for a, n := range model.ActionNames {
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

	w.Bind("_toggleScreenshot", func(id int, checked bool) {
		if o.onToggleScreenshot == nil {
			return
		}
		o.onToggleScreenshot(id, checked)
	})

	w.Bind("_removeScreenshot", func(id int) {
		if o.onRemoveScreenshot == nil {
			return
		}
		o.onRemoveScreenshot(id)
	})

	w.Bind("_removeTraces", func(idsJSON string) {
		if o.onRemoveTraces == nil {
			return
		}
		var ids []int
		if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
			return
		}
		o.onRemoveTraces(ids)
	})

	w.Bind("_toggleFS", func() {
		g := o.fsGeom.Load()
		if g == nil {
			return
		}
		if o.isFS.Load() {
			C.resize_and_move(o.gtkWin, C.int(g[0]), C.int(g[1]), 700, 900)
			o.isFS.Store(false)
			o.eval(`document.getElementById('fs-btn').textContent='\u26F6';`)
			return
		}
		C.resize_and_move(o.gtkWin, C.int(g[0]), C.int(g[1]), C.int(g[2]), C.int(g[3]))
		o.isFS.Store(true)
		o.eval(`document.getElementById('fs-btn').textContent='\u2750';`)
	})

	w.Bind("_listMice", func() string {
		mice := system.ListMice()
		b, _ := json.Marshal(mice)
		return string(b)
	})

	w.Bind("_isMPXActive", func() bool {
		return system.IsMPXActive()
	})

	w.Bind("_setupMPX", func(deviceID string) {
		go func() {
			if err := system.SetupMPX(deviceID); err != nil {
				applog.AppLog.Error("mpx: %v", err)
				return
			}
			o.eval("document.getElementById('btn-setup').textContent='Setup \u2713';")
		}()
	})

	w.Bind("_teardownMPX", func() {
		go func() {
			system.TeardownMPX()
			o.eval("document.getElementById('btn-setup').textContent='Setup';")
		}()
	})

	w.Bind("_selectContext", func(mode string) {
		C.set_keep_above(o.gtkWin, 0)
		go func() {
			defer o.needsRaise.Store(true)
			path := pickPath(mode)
			if path == "" {
				return
			}
			if o.provider != nil {
				o.provider.SetContextDir(path)
			}
			appctx.CtxFileSelection.Clear()
			o.SetFileSysLabel(path)
		}()
	})

	w.Bind("_getContextState", func() string {
		return o.buildContextStateJSON()
	})

	w.Bind("_toggleContextFile", func(path string, excluded bool) {
		appctx.CtxFileSelection.SetExcluded(path, excluded)
	})

	w.Bind("_removeTranscriptEntry", func(id int) {
		if o.ac == nil {
			return
		}
		o.ac.RemoveEntry(id)
	})

	w.Bind("_restoreArtifactContext", func(traceID int) {
		t := o.appState.GetTrace(traceID)
		if t == nil {
			return
		}
		if t.ContextDir != "" && o.provider != nil {
			o.provider.SetContextDir(t.ContextDir)
			appctx.CtxFileSelection.Clear()
			o.SetFileSysLabel(t.ContextDir)
		}
		o.restoreTraceScreenshots(t)
	})

	w.Bind("_restoreTraceScreenshot", func(ssID int) {
		e := o.appState.RestoreScreenshot(ssID)
		if e == nil {
			return
		}
		o.AppendScreenshot(e.ID, e.Data)
	})

	w.Bind("_chatSend", func(text string) {
		if o.onChatMessage == nil {
			return
		}
		if text == "" {
			return
		}
		o.onChatMessage(text)
	})

	w.Bind("_setFocus", func(accept bool) {
		v := 0
		if accept {
			v = 1
		}
		C.set_accept_focus(o.gtkWin, C.int(v))
	})

	// Bind a JS-callable function that drains pending JS updates.
	w.Bind("_pollUpdates", func() string {
		C.fix_signal_handlers()
		if o.needsRaise.CompareAndSwap(true, false) {
			C.set_keep_above(o.gtkWin, 1)
		}
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
		entries := applog.AppLog.Since(after)
		if len(entries) == 0 {
			return ""
		}
		b, _ := json.Marshal(entries)
		return string(b)
	})

	w.Bind("_sendToSandbox", func(code, lang string) {
		applog.AppLog.Info("overlay: sendToSandbox lang=%s code=%d bytes", lang, len(code))
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
			res := sandbox.RunSandbox(code, lang)
			o.renderSandboxResult(res)
		}()
	})

	w.Bind("_runSandbox", func(code, tests, lang string) {
		combined := code
		if tests != "" {
			combined = code + "\n" + tests
		}
		applog.AppLog.Info("overlay: runSandbox lang=%s code=%d bytes", lang, len(combined))
		if combined == "" {
			applog.AppLog.Warn("overlay: runSandbox called with empty code")
			return
		}
		o.sandboxMu.Lock()
		o.sandboxCode = combined
		o.sandboxLang = lang
		o.sandboxMu.Unlock()
		o.eval("document.getElementById('sandbox-output').innerHTML='<span style=\"color:#888\">running...</span>';")

		go func() {
			res := sandbox.RunSandbox(combined, lang)
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

			res := sandbox.RunSandbox(combined, lang)
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

	w.Bind("_setClearOnProcess", func(on bool) { ClearOnProcess.Store(on) })

	w.SetHtml(o.buildShell())
	C.show_window(gtkWin)
	return o
}

func (o *OverlayRenderer) SetActionHandler(fn func(model.HotkeyAction)) {
	o.onAction = fn
}

func (o *OverlayRenderer) SetToggleChunkHandler(fn func(int, bool)) {
	o.onToggleChunk = fn
}

func (o *OverlayRenderer) SetToggleScreenshotHandler(fn func(int, bool)) {
	o.onToggleScreenshot = fn
}

func (o *OverlayRenderer) SetRemoveScreenshotHandler(fn func(int)) {
	o.onRemoveScreenshot = fn
}

func (o *OverlayRenderer) SetChatMessageHandler(fn func(string)) {
	o.onChatMessage = fn
}

func (o *OverlayRenderer) SetRemoveTracesHandler(fn func([]int)) {
	o.onRemoveTraces = fn
}

func (o *OverlayRenderer) SetProvider(p model.Provider)            { o.provider = p }
func (o *OverlayRenderer) SetAppState(s *model.AppState)           { o.appState = s }
func (o *OverlayRenderer) SetAudioCapture(ac *audio.AudioCapture)  { o.ac = ac }

func (o *OverlayRenderer) SetFileSysLabel(path string) {
	label := filepath.Base(path)
	o.eval(`document.getElementById('btn-context').textContent=` + jsString("file sys: "+label) + `;` +
		`_refreshContext();`)
}

func (o *OverlayRenderer) MoveToMonitor(x, y int) {
	C.move_window(o.gtkWin, C.int(x), C.int(y))
}

func (o *OverlayRenderer) Fullscreen(x, y, w, h int) {
	g := [4]int{x, y, w, h}
	o.fsGeom.Store(&g)
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
		if err := system.SetAlwaysOnTop(xid); err != nil {
			fmt.Printf("warning: could not set always-on-top: %v\n", err)
		}
		g := o.fsGeom.Load()
		if g != nil {
			C.resize_and_move(o.gtkWin, C.int(g[0]), C.int(g[1]), C.int(g[2]), C.int(g[3]))
			o.isFS.Store(true)
			o.eval(`document.getElementById('fs-btn').textContent='\u2750';`)
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
		"document.getElementById('footer-status').textContent='';" +
		"document.getElementById('tab-chat').classList.add('streaming');"
	o.eval(js)
}

func (o *OverlayRenderer) streamDelta(delta string) {
	o.streamBuf.WriteString(delta)
	js := "var s=document.getElementById('stream');if(s){s.textContent+=" + jsString(delta) + ";if(window._autoScroll)s.scrollIntoView(false);}"
	o.eval(js)
}

func (o *OverlayRenderer) StreamDelta(delta string) { o.streamDelta(delta) }

func (o *OverlayRenderer) wrapResponse() string {
	html, err := o.markdownToHTML(o.streamBuf.String())
	if err != nil {
		html = "<pre>" + escapeHTML(o.streamBuf.String()) + "</pre>"
	}
	inner := `<div class="response-block">` + html +
		`<div class="response-actions">` +
		`<button class="action-btn simplify-btn" onclick="_action('simplify')" title="Simplify">&#8722;</button>` +
		`<button class="action-btn optimize-btn" onclick="_action('optimize')" title="Optimize">&#43;</button>` +
		`<button class="action-btn explain-btn" onclick="_action('explain')" title="Explain further">?</button>` +
		`</div></div>`
	tid := o.currentTraceID
	return fmt.Sprintf(
		`<div class="trace-group" data-trace-id="%d">`+
			`<div class="row row-center trace-header">`+
			`<input type="checkbox" class="row-ctrl trace-cb" value="%d" onchange="_updateDeleteBtn()">`+
			`<span class="row-fill trace-label">trace #%d</span></div>%s</div>`,
		tid, tid, tid, inner)
}

func (o *OverlayRenderer) StreamDone() {
	wrapped := o.wrapResponse()
	js := "var c=document.getElementById('chat-content'),ca=document.getElementById('content-area'),st=ca.scrollTop;" +
		"c.innerHTML=" + jsString(wrapped) + ";_injectSandboxButtons();" +
		"if(!window._autoScroll)ca.scrollTop=st;" +
		"document.getElementById('tab-chat').classList.remove('streaming');"
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamStart() {
	o.streamBuf.Reset()
	js := `window._autoScroll=true;var c=document.getElementById('chat-content');` +
		`c.innerHTML+='<hr><h3 style="color:#7ec8e3">▼ follow-up</h3><pre id="stream"></pre>';` +
		`document.getElementById('footer-status').textContent='';` +
		`document.getElementById('content-area').scrollTop=document.getElementById('content-area').scrollHeight;` +
		`document.getElementById('tab-chat').classList.add('streaming');`
	o.eval(js)
}

func (o *OverlayRenderer) AppendStreamDelta(delta string) { o.streamDelta(delta) }

func (o *OverlayRenderer) AppendStreamDone() {
	wrapped := o.wrapResponse()
	js := `var s=document.getElementById('stream');` +
		`if(s){var ca=document.getElementById('content-area'),st=ca.scrollTop;` +
		`var d=document.createElement('div');d.innerHTML=` + jsString(wrapped) + `;s.replaceWith(d);` +
		`if(window._autoScroll)d.scrollIntoView(false);else ca.scrollTop=st;}_injectSandboxButtons();` +
		`document.getElementById('tab-chat').classList.remove('streaming');`
	o.eval(js)
}

func (o *OverlayRenderer) AppendTranscriptChunk(source, text string, id int) {
	ts := time.Now().Format("15:04:05")
	srcClass := "src-" + source
	chunk := fmt.Sprintf(
		`<div class="row transcript-chunk" data-id="%d">`+
			`<input type="checkbox" class="row-ctrl chunk-cb" onchange="_toggleChunk(%d,this.checked)">`+
			`<span class="ts">[%s</span> <span class="src %s">%s</span><span class="ts">]</span> %s</div>`,
		id, id, escapeHTML(ts), srcClass, escapeHTML(source), escapeHTML(text))
	js := `var t=document.getElementById('transcript-content');` +
		`t.innerHTML+=` + jsString(chunk) + `;` +
		`if(window._autoScroll){var ca=document.getElementById('content-area');ca.scrollTop=ca.scrollHeight;}` +
		`_refreshContext();` +
		`var tb=document.getElementById('tab-transcript');tb.classList.add('streaming');` +
		`clearTimeout(window._txPulse);window._txPulse=setTimeout(function(){tb.classList.remove('streaming');},2000);`
	o.eval(js)
}

func (o *OverlayRenderer) ClearTranscriptCheckboxes() {
	js := `document.querySelectorAll('.chunk-cb').forEach(function(cb){cb.checked=false;});`
	o.eval(js)
}

func (o *OverlayRenderer) setRecordingButton(elemID, label string, recording bool) {
	bg, color := "''", "''"
	if recording {
		label = `<span class="rec-dot"></span>` + label
		bg, color = "'rgba(220,50,50,0.7)'", "'#fff'"
	}
	js := fmt.Sprintf(`var b=document.getElementById(%s);if(b){b.style.background=%s;b.style.color=%s;b.innerHTML=%s;}`,
		jsString(elemID), bg, color, jsString(label))
	o.eval(js)
}

func (o *OverlayRenderer) SetAudioRecording(recording bool) {
	o.setRecordingButton("btn-audio", escapeHTML(model.KeyLabels[model.HotkeyAudioCapture]), recording)
}

func (o *OverlayRenderer) SetSoundCheck(active bool) {
	o.setRecordingButton("btn-setup", "Setup", active)
	vuDisplay := "'none'"
	if active {
		vuDisplay = "'flex'"
	}
	o.eval(fmt.Sprintf(`document.getElementById('vu-meters').style.display=%s;`, vuDisplay))
}

func (o *OverlayRenderer) SetMicRecording(recording bool) {
	o.setRecordingButton("btn-voice", escapeHTML(model.KeyLabels[model.HotkeyFollowUp]), recording)
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

func (o *OverlayRenderer) AppendScreenshot(id int, data []byte) {
	b64 := base64.StdEncoding.EncodeToString(data)
	ts := time.Now().Format("15:04:05")
	html := fmt.Sprintf(
		`<div class="ss-entry" data-id="%d">`+
			`<input type="checkbox" class="ss-cb" checked onchange="_toggleScreenshot(%d,this.checked)">`+
			`<button class="ss-rm" onclick="_removeScreenshot(%d)">×</button>`+
			`<img src="data:image/jpeg;base64,%s" onclick="_viewScreenshot(this.src)">`+
			`<span class="ss-ts">%s</span></div>`,
		id, id, id, b64, escapeHTML(ts))
	js := `var g=document.getElementById('screenshot-grid');` +
		`g.innerHTML+=` + jsString(html) + `;_refreshContext();` +
		`var sb=document.getElementById('tab-screenshots');sb.classList.add('streaming');` +
		`clearTimeout(window._ssPulse);window._ssPulse=setTimeout(function(){sb.classList.remove('streaming');},2000);`
	o.eval(js)
}

func (o *OverlayRenderer) restoreTraceScreenshots(t *model.Trace) {
	for _, ssID := range t.ScreenIDs {
		e := o.appState.RestoreScreenshot(ssID)
		if e == nil {
			continue
		}
		o.AppendScreenshot(e.ID, e.Data)
	}
}

func (o *OverlayRenderer) RemoveScreenshot(id int) {
	js := fmt.Sprintf(`var el=document.querySelector('.ss-entry[data-id="%d"]');if(el)el.remove();_refreshContext();`, id)
	o.eval(js)
}

func (o *OverlayRenderer) ClearScreenshotCheckboxes() {
	o.eval(`document.querySelectorAll('.ss-cb').forEach(function(cb){cb.checked=false;});`)
}

func (o *OverlayRenderer) SetScreenCount(count int) {
	o.eval(`_refreshContext();`)
}

func (o *OverlayRenderer) SetCurrentTraceID(id int) {
	o.currentTraceID = id
}

func (o *OverlayRenderer) AddObserveTrace(trace model.Trace) {
	ts := trace.Time.Format("15:04:05")
	var parts []string
	if trace.ScreenCount > 0 {
		parts = append(parts, fmt.Sprintf("%d screenshot(s)", trace.ScreenCount))
	}
	if trace.HasContext {
		parts = append(parts, fmt.Sprintf("%d source file(s)", len(trace.ContextFiles)))
	}
	if trace.HasTranscript {
		parts = append(parts, "transcript")
	}
	summary := strings.Join(parts, ", ")
	detail := ""
	if len(trace.ScreenIDs) > 0 {
		detail += "<div><b>Screenshots:</b></div>"
		for i, ssID := range trace.ScreenIDs {
			ts := ""
			if i < len(trace.ScreenTimes) {
				ts = trace.ScreenTimes[i].Format("15:04:05")
			}
			detail += fmt.Sprintf(
				`<div class="row row-center" style="padding-left:8px">`+
					`<span class="row-fill" style="color:#aaa">#%d [%s]</span>`+
					`<button class="row-end trace-restore" onclick="event.stopPropagation();_restoreTraceScreenshot(%d)" title="Restore screenshot">&#8635;</button>`+
					`</div>`, ssID, escapeHTML(ts), ssID)
		}
	}
	if trace.HasContext {
		info, err := os.Stat(trace.ContextDir)
		label := escapeHTML(trace.ContextDir)
		if err == nil && !info.IsDir() {
			label = escapeHTML(strings.Join(trace.ContextFiles, ", "))
		}
		detail += "<div><b>File sys:</b> " + label + "</div>"
	}
	if trace.TranscriptSnippet != "" {
		detail += "<div><b>Transcript:</b></div><pre style=\"font-size:11px;color:#aaa;margin:2px 0;white-space:pre-wrap\">" + escapeHTML(trace.TranscriptSnippet) + "</pre>"
	}
	restoreBtn := fmt.Sprintf(
		` <button class="row-end trace-restore" onclick="event.stopPropagation();_restoreArtifactContext(%d)" title="Restore all context">&#8635;</button>`,
		trace.ID)
	html := fmt.Sprintf(
		`<div class="observe-trace" data-trace-id="%d">`+
			`<div class="row row-center observe-header" onclick="_toggleObserveTrace(%d)">`+
			`<input type="checkbox" class="row-ctrl trace-cb" value="%d" onclick="event.stopPropagation();_updateDeleteBtn()">`+
			`<span class="row-ctrl observe-chevron">&#9654;</span>`+
			`<span class="row-fill">[%s] #%d — %s</span>%s</div>`+
			`<div class="observe-detail">%s</div></div>`,
		trace.ID, trace.ID, trace.ID, escapeHTML(ts), trace.ID, escapeHTML(summary), restoreBtn, detail)
	js := `var oc=document.getElementById('trace-content');` +
		`oc.insertAdjacentHTML('afterbegin',` + jsString(html) + `);`
	o.eval(js)
}

func (o *OverlayRenderer) RemoveObserveTrace(traceID int) {
	js := fmt.Sprintf(
		`var ot=document.querySelector('.observe-trace[data-trace-id="%d"]');if(ot)ot.remove();`+
			`var tg=document.querySelector('.trace-group[data-trace-id="%d"]');if(tg)tg.remove();`+
			`_updateDeleteBtn();`,
		traceID, traceID)
	o.eval(js)
}

func (o *OverlayRenderer) Clear() {
	js := "document.getElementById('chat-content').innerHTML='';" +
		"document.getElementById('transcript-content').innerHTML='';" +
		"document.getElementById('screenshot-grid').innerHTML='';" +
		"document.getElementById('trace-content').innerHTML='';" +
		"document.getElementById('delete-traces-btn').style.display='none';" +
		"document.getElementById('ctx-screenshots').innerHTML='';" +
		"document.getElementById('ctx-transcript').innerHTML='';" +
		"document.getElementById('ctx-files').innerHTML='';" +
		"document.getElementById('footer-status').textContent='Cleared.';"
	o.eval(js)
}

func (o *OverlayRenderer) ClearContextData() {
	o.eval(`document.getElementById('screenshot-grid').innerHTML='';` +
		`document.getElementById('transcript-content').innerHTML='';`)
}

func (o *OverlayRenderer) Close() {
	if !o.closed.CompareAndSwap(false, true) {
		return
	}
	o.w.Terminate()
}

func (o *OverlayRenderer) renderSandboxResult(r model.SandboxResult) {
	applog.AppLog.Info("overlay: rendering sandbox result exit=%d", r.ExitCode)
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

type ctxScreenshot struct {
	ID       int    `json:"id"`
	Time     string `json:"time"`
	Selected bool   `json:"selected"`
}

type ctxTranscript struct {
	ID       int    `json:"id"`
	Source   string `json:"source"`
	Text     string `json:"text"`
	Time     string `json:"time"`
	Selected bool   `json:"selected"`
}

type ctxFile struct {
	Path     string `json:"path"`
	Excluded bool   `json:"excluded"`
}

type ctxState struct {
	Screenshots []ctxScreenshot `json:"screenshots"`
	Transcript  []ctxTranscript `json:"transcript"`
	Files       []ctxFile       `json:"files"`
	ContextDir  string          `json:"contextDir"`
}

func (o *OverlayRenderer) buildContextStateJSON() string {
	var st ctxState

	if o.appState != nil {
		o.appState.Mu.Lock()
		for _, s := range o.appState.Shots {
			st.Screenshots = append(st.Screenshots, ctxScreenshot{
				ID:       s.ID,
				Time:     s.Time.Format("15:04:05"),
				Selected: o.appState.Selected[s.ID],
			})
		}
		o.appState.Mu.Unlock()
	}

	if o.ac != nil {
		entries := o.ac.Entries()
		sel := o.ac.SelectedMap()
		for _, e := range entries {
			st.Transcript = append(st.Transcript, ctxTranscript{
				ID:       e.ID,
				Source:   e.Source,
				Text:     e.Text,
				Time:     e.Time.Format("15:04:05"),
				Selected: sel[e.ID],
			})
		}
	}

	if o.provider != nil {
		st.ContextDir = o.provider.ContextDir()
	}
	excluded := appctx.CtxFileSelection.ExcludedSet()
	files := appctx.ListContextFiles(st.ContextDir)
	for _, f := range files {
		st.Files = append(st.Files, ctxFile{Path: f, Excluded: excluded[f]})
	}

	b, _ := json.Marshal(st)
	return string(b)
}

func (o *OverlayRenderer) markdownToHTML(md string) (string, error) {
	var buf bytes.Buffer
	err := o.md.Convert([]byte(md), &buf)
	return buf.String(), err
}

func (o *OverlayRenderer) buildShell() string {
	return `<!DOCTYPE html><html><head><style>` +
		overlayCSS + o.chromaCS +
		`</style></head><body>` +
		o.buildHTML() +
		`<script>` + overlayJS + `</script>` +
		`</body></html>`
}

func (o *OverlayRenderer) buildHTML() string {
	return `<div id="tab-bar">
  <button id="tab-chat" class="active" onclick="switchTab('chat')">Chat</button>
  <button id="tab-transcript" onclick="switchTab('transcript')">Transcript</button>
  <button id="tab-screenshots" onclick="switchTab('screenshots')">Screenshots</button>
  <button id="tab-sandbox" onclick="switchTab('sandbox')">Sandbox</button>
  <button id="tab-context" onclick="switchTab('context')">Context</button>
  <button id="tab-trace" onclick="switchTab('trace')">Trace</button>
  <button id="tab-log" onclick="switchTab('log')">Log</button>
</div>
<div id="content-area">
<div id="chat-content" class="tab-content active"><div style="text-align:right;padding:4px 8px"><button class="ctx-clear-btn" onclick="document.getElementById('chat-content').querySelectorAll('.trace-group').forEach(function(e){e.remove()})">Clear Chat</button></div></div>
<div id="chat-input-bar" class="visible"><input id="chat-input" type="text" placeholder="Send a message..." onkeydown="if(event.key==='Enter'){_chatSend(this.value);this.value=''}"><button id="chat-send-btn" onclick="_chatSend(document.getElementById('chat-input').value);document.getElementById('chat-input').value=''">Send</button></div>
<div id="transcript-content" class="tab-content"><div id="transcript-controls" style="text-align:right;padding:4px 8px"><button class="ctx-clear-btn" style="color:#7ec8e3;border-color:rgba(126,200,227,0.3)" onclick="_selectAllTranscript()">Select All</button></div></div>
<div id="screenshots-content" class="tab-content"><div id="screenshot-grid"></div></div>
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
  <div id="sandbox-controls" class="row row-center">
    <button id="sandbox-run" onclick="_runCombined()">&#9654; Run</button>
    <button id="sandbox-test" onclick="_genTests(document.getElementById('sandbox-editor').value,document.getElementById('sandbox-lang').textContent)">&#9654; Test</button>
    <span id="sandbox-lang"></span>
  </div>
  <pre id="sandbox-output"></pre>
</div>
<div id="context-content" class="tab-content">
  <div id="ctx-active">
    <div style="text-align:right;padding:4px 8px"><button class="ctx-clear-btn" onclick="_action('clear');_refreshContext()">Clear All</button></div>
    <div id="ctx-screenshots"></div>
    <div id="ctx-transcript"></div>
    <div id="ctx-files"></div>
  </div>
</div>
<div id="trace-content" class="tab-content"></div>
<div id="log-content" class="tab-content"><div id="log-output"></div></div>
<button id="delete-traces-btn" style="display:none" onclick="_deleteTraces()">Delete selected</button>
<div id="ss-lightbox" onclick="this.classList.remove('active')"><img></div>
</div>
<div id="footer"><div id="footer-status"></div>
<div id="vu-meters">
  <span class="vu-label">mic</span>
  <div class="vu-track"><div id="vu-mic" class="vu-fill"></div></div>
  <span class="vu-label">audio</span>
  <div class="vu-track"><div id="vu-audio" class="vu-fill"></div></div>
</div>
<div id="footer-btns">
` + buildFooterButtons() + `<button id="btn-setup" onclick="_showSetupMenu(event)">Setup</button>
</div></div>`
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

var buttonIDs = map[model.HotkeyAction]string{
	model.HotkeyFollowUp:     "btn-voice",
	model.HotkeyAudioCapture: "btn-audio",
	model.HotkeySoundCheck:   "btn-setup",
}

func buildFooterButtons() string {
	var buf strings.Builder
	for _, a := range model.KeyOrder {
		name := model.ActionNames[a]
		label := model.KeyLabels[a]
		id := buttonIDs[a]
		idAttr := ""
		if id != "" {
			idAttr = ` id="` + id + `"`
		}
		buf.WriteString(fmt.Sprintf(`<button%s onclick="_action('%s')">%s</button>`+"\n", idAttr, name, escapeHTML(label)))
		if a == model.HotkeyFollowUp {
			buf.WriteString(`<button id="btn-context" onclick="_showCtxMenu(event)">file sys</button>` + "\n")
		}
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

func pickPath(mode string) string {
	args := []string{"--file-selection", "--title=Select context"}
	if mode == "dir" {
		args = append(args, "--directory")
	}
	out, err := exec.Command("zenity", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
