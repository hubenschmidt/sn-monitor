window._clearOnProcess = false;
window._ctxMenu = null;
window._setupMenu = null;
window._showPopup = function (refName, closeFn, anchor) {
  var m = document.createElement("div");
  m.className = "ctx-popup";
  document.getElementById("footer-btns").appendChild(m);
  window[refName] = m;
  if (anchor) {
    var r = anchor.getBoundingClientRect();
    var pr = m.parentNode.getBoundingClientRect();
    m.style.left = r.left - pr.left + r.width / 2 + "px";
  }
  setTimeout(function () {
    document.addEventListener("click", function h() {
      closeFn();
      document.removeEventListener("click", h);
    });
  }, 0);
  return m;
};
window._closeCtx = function () {
  if (_ctxMenu) {
    _ctxMenu.remove();
    _ctxMenu = null;
  }
};
window._closeSetup = function () {
  if (_setupMenu) {
    _setupMenu.remove();
    _setupMenu = null;
  }
};
window._showCtxMenu = function (e) {
  e.stopPropagation();
  if (_ctxMenu) {
    _closeCtx();
    return;
  }
  var m = _showPopup("_ctxMenu", _closeCtx, e.currentTarget);
  m.innerHTML =
    "<div onclick=\"_selectContext('dir')\">Directory</div><div onclick=\"_selectContext('file')\">File</div>";
};
window._showSetupMenu = function (e) {
  e.stopPropagation();
  if (_setupMenu) {
    _closeSetup();
    return;
  }
  var m = _showPopup("_setupMenu", _closeSetup, e.currentTarget);
  m.innerHTML =
    '<div id="btn-soundcheck" onclick="_action(\'soundcheck\')">Sound Check</div>' +
    '<div onclick="_showMPXSub()">Mouse (MPX)</div>' +
    '<div style="border-top:1px solid #444;padding:6px 14px"><label style="cursor:pointer;display:flex;align-items:center;gap:6px"><input type="checkbox" id="chk-clear-ctx"' +
    (window._clearOnProcess ? " checked" : "") +
    ' onchange="window._clearOnProcess=this.checked;_setClearOnProcess(this.checked)"> clear context on process?</label></div>';
};
window._showMPXSub = function () {
  _closeSetup();
  _isMPXActive().then(function (active) {
    if (active) {
      _teardownMPX();
      return;
    }
    _listMice().then(function (raw) {
      var mice = JSON.parse(raw);
      if (!mice || !mice.length) return;
      var m = _showPopup(
        "_setupMenu",
        _closeSetup,
        document.getElementById("btn-setup"),
      );
      for (var i = 0; i < mice.length; i++) {
        (function (mouse) {
          var d = document.createElement("div");
          d.textContent = mouse.Name + " (" + mouse.ID + ")";
          d.onclick = function () {
            _setupMPX(mouse.ID);
            _closeSetup();
          };
          m.appendChild(d);
        })(mice[i]);
      }
    });
  });
};
window._viewScreenshot = function (src) {
  var lb = document.getElementById("ss-lightbox");
  lb.querySelector("img").src = src;
  lb.classList.add("active");
};
window._autoSize = function (el) {
  el.style.height = "auto";
  el.style.height = el.scrollHeight + "px";
};
window._hlTimers = {};
window._syncHighlight = function (taId, hlId) {
  var ta = document.getElementById(taId);
  var hl = document.getElementById(hlId);
  if (!ta || !hl) return;
  var lang = document.getElementById("sandbox-lang").textContent || "";
  _highlight(ta.value, lang).then(function (html) {
    hl.innerHTML = html;
  });
};
window._debouncedSync = function (taId, hlId) {
  clearTimeout(window._hlTimers[taId]);
  window._hlTimers[taId] = setTimeout(function () {
    _syncHighlight(taId, hlId);
  }, 150);
};
window._bindEditor = function (taId, hlId) {
  var ta = document.getElementById(taId);
  ta.addEventListener("input", function () {
    _autoSize(this);
    _debouncedSync(taId, hlId);
  });
  ta.addEventListener("scroll", function () {
    var p = this.parentNode.querySelector(".editor-highlight");
    p.scrollTop = this.scrollTop;
    p.scrollLeft = this.scrollLeft;
  });
};
_bindEditor("sandbox-editor", "sandbox-editor-hl");
_bindEditor("sandbox-tests", "sandbox-tests-hl");
window._updateCtxReceipt = function () {
  var ss = document.querySelectorAll("#ctx-screenshots .ctx-cb");
  var selS = 0, totS = ss.length;
  for (var i = 0; i < ss.length; i++) { if (ss[i].checked) selS++; }
  var tc = document.querySelectorAll(".chunk-cb");
  var selT = 0, totT = tc.length, tChars = 0;
  for (var i = 0; i < tc.length; i++) {
    if (tc[i].checked) selT++;
  }
  var fc = document.querySelectorAll("#ctx-files .ctx-file-cb");
  var inclF = 0, totF = fc.length;
  for (var i = 0; i < fc.length; i++) { if (fc[i].checked) inclF++; }
  var r = selS + "/" + totS + " screenshots, " + totT + " transcript chunks";
  if (selT) r += " (" + selT + " selected)";
  r += ", " + inclF + "/" + totF + " source files";
};
window._selectAllTranscript = function () {
  document.querySelectorAll(".chunk-cb").forEach(function (cb) {
    if (cb.checked) return;
    cb.checked = true;
    cb.dispatchEvent(new Event("change"));
  });
  _refreshContext();
};
window._refreshContext = function () {
  _getContextState().then(function (raw) {
    var st = JSON.parse(raw);
    st.screenshots = st.screenshots || [];
    st.transcript = st.transcript || [];
    st.files = st.files || [];
    var sh = '<div class="ctx-section-title">Screenshots</div>';
    if (!st.screenshots.length) {
      sh += '<div style="color:#666">none</div>';
    }
    for (var i = 0; i < st.screenshots.length; i++) {
      var s = st.screenshots[i];
      sh +=
        '<div class="row row-center ctx-item"><input type="checkbox" class="row-ctrl ctx-cb"' +
        (s.selected ? " checked" : "") +
        " onchange=\"_toggleScreenshot(" + s.id + ",this.checked);_updateCtxReceipt()\">" +
        '<span class="row-fill" style="color:' + (s.selected ? "#7ec8e3" : "#555") + '">[' +
        s.time + "] #" + s.id + "</span>" +
        '<button class="row-end ctx-rm" onclick="_removeScreenshot(' + s.id +
        ');_refreshContext()">\u00d7</button></div>';
    }
    document.getElementById("ctx-screenshots").innerHTML = sh;
    var th = '<div class="ctx-section-title">Transcript</div>';
    var selT = st.transcript.filter(function (t) { return t.selected; });
    if (!st.transcript.length) {
      th += '<div class="row row-center" style="color:#666">none</div>';
    } else if (!selT.length) {
      th += '<div class="row row-center" style="color:#666">' + st.transcript.length + ' chunks (none selected)</div>';
    } else {
      for (var i = 0; i < selT.length; i++) {
        var t = selT[i];
        var preview = t.text.length > 60 ? t.text.substring(0, 60) + "\u2026" : t.text;
        th +=
          '<div class="row row-center ctx-item">' +
          '<span class="row-fill" style="color:#ccc">[' + t.time + " " + t.source + "] " + preview + "</span>" +
          '<button class="row-end ctx-rm" onclick="_toggleChunk(' + t.id +
          ',false);var r=document.querySelector(\'.transcript-chunk[data-id=&quot;' + t.id +
          '&quot;] .chunk-cb\');if(r)r.checked=false;_refreshContext()">\u00d7</button></div>';
      }
    }
    document.getElementById("ctx-transcript").innerHTML = th;
    var fh = '<div class="ctx-section-title">Source Files</div>';
    if (st.contextDir) {
      fh +=
        '<div style="color:#666;font-size:11px;margin-bottom:4px">' +
        st.contextDir +
        "</div>";
    }
    if (!st.files.length) {
      fh += '<div style="color:#666">none</div>';
    }
    for (var i = 0; i < st.files.length; i++) {
      var f = st.files[i];
      fh +=
        '<div class="row row-center ctx-file-entry"><input type="checkbox" class="row-ctrl ctx-file-cb" ' +
        (f.excluded ? "" : "checked") +
        " onchange=\"_toggleContextFile('" +
        f.path.replace(/\x27/g, "\\'") +
        "',!this.checked);_updateCtxReceipt()\"><span class=\"row-fill\"" +
        (f.excluded ? ' style="color:#555"' : "") +
        ">" +
        f.path +
        "</span><button class=\"row-end ctx-rm\" onclick=\"_toggleContextFile('" +
        f.path.replace(/\x27/g, "\\'") +
        "',true);_refreshContext()\">\u00d7</button></div>";
    }
    document.getElementById("ctx-files").innerHTML = fh;
    _updateCtxReceipt();
  });
};
window._runCombined = function () {
  var code = document.getElementById("sandbox-editor").value;
  var tests = document.getElementById("sandbox-tests").value;
  var lang = document.getElementById("sandbox-lang").textContent;
  _runSandbox(code, tests, lang);
};
window._autoScroll = true;
window._transcriptAutoScroll = true;
window._setupAutoScroll = function (id, flag) {
  var el = document.getElementById(id);
  el.addEventListener("wheel", function () {
    window[flag] = false;
  });
  el.addEventListener("scroll", function () {
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 5)
      window[flag] = true;
  });
};
_setupAutoScroll("content-area", "_autoScroll");
window._transcriptAutoScroll = window._autoScroll;

(function () {
  var ci = document.getElementById("chat-input");
  ci.addEventListener("focus", function () { _setFocus(true); });
  ci.addEventListener("blur", function () { _setFocus(false); });
})();
window._logBadge = 0;
window._logIdx = -1;
window._sandboxLangs = {
  python: 1,
  go: 1,
  javascript: 1,
  js: 1,
  typescript: 1,
  ts: 1,
  cpp: 1,
  "c++": 1,
  rust: 1,
  java: 1,
};
window.switchTab = function (name) {
  var tabs = [
    "chat",
    "transcript",
    "screenshots",
    "sandbox",
    "context",
    "trace",
    "log",
  ];
  for (var i = 0; i < tabs.length; i++) {
    var btn = document.getElementById("tab-" + tabs[i]);
    var div = document.getElementById(tabs[i] + "-content");
    if (tabs[i] === name) {
      btn.className = "active";
      div.className = "tab-content active";
    } else {
      btn.className = "";
      div.className = "tab-content";
    }
  }
  var cib = document.getElementById("chat-input-bar");
  if (cib) { cib.className = name === "chat" ? "visible" : ""; }
  if (name === "transcript") {
    window._autoScroll = true;
    document.getElementById("content-area").scrollTop =
      document.getElementById("content-area").scrollHeight;
  }
  if (name === "context") {
    _refreshContext();
  }
  if (name === "log") {
    window._logBadge = 0;
    var b = document.getElementById("tab-log").querySelector(".tab-badge");
    if (b) b.remove();
    document.getElementById("log-output").scrollTop =
      document.getElementById("log-output").scrollHeight;
  }
};
window._injectSandboxButtons = function () {
  var wraps = document.querySelectorAll("#chat-content .highlight[data-lang]");
  for (var i = 0; i < wraps.length; i++) {
    var wrap = wraps[i];
    var lang = wrap.getAttribute("data-lang");
    if (!window._sandboxLangs[lang]) continue;
    if (wrap.querySelector(".sandbox-btn")) continue;
    var pre = wrap.querySelector("pre");
    if (!pre) continue;
    var code = pre.querySelector("code") || pre;
    wrap.style.position = "relative";
    var btn = document.createElement("button");
    btn.className = "sandbox-btn";
    btn.textContent = "\u25B6 Sandbox";
    btn.onclick = (function (c, l) {
      return function () {
        _sendToSandbox(c.textContent, l);
      };
    })(code, lang);
    wrap.appendChild(btn);
  }
};
(function vuPump() {
  _pollVU().then(function (js) {
    if (js) eval(js);
  });
  requestAnimationFrame(vuPump);
})();
setInterval(function () {
  _pollUpdates().then(function (js) {
    if (js) eval(js);
  });
}, 33);
window._toggleObserveTrace = function (id) {
  var el = document.querySelector('.observe-trace[data-trace-id="' + id + '"]');
  if (!el) return;
  el.classList.toggle("expanded");
  var detail = el.querySelector(".observe-detail");
  detail.classList.toggle("open");
};
window._updateDeleteBtn = function () {
  var cbs = document.querySelectorAll(".trace-cb:checked");
  var btn = document.getElementById("delete-traces-btn");
  btn.style.display = cbs.length ? "block" : "none";
};
window._deleteTraces = function () {
  var cbs = document.querySelectorAll(".trace-cb:checked");
  var ids = [];
  for (var i = 0; i < cbs.length; i++) {
    ids.push(parseInt(cbs[i].value));
  }
  if (!ids.length) return;
  _removeTraces(JSON.stringify(ids));
};
setInterval(function () {
  _pollLog(window._logIdx).then(function (raw) {
    if (!raw) return;
    var entries = JSON.parse(raw);
    if (!entries || !entries.length) return;
    var out = document.getElementById("log-output");
    var logTab = document.getElementById("log-content");
    var isActive = logTab.classList.contains("active");
    for (var i = 0; i < entries.length; i++) {
      var e = entries[i];
      var ts = e.Time ? e.Time.substring(11, 19) : "";
      var cls = "log-" + e.Level;
      out.innerHTML +=
        '<div class="row row-center ' + cls + '">' +
        '<span class="row-ctrl log-ts">' + ts + '</span>' +
        '<span class="row-ctrl log-level">[' + e.Level + ']</span>' +
        '<span class="row-fill">' + e.Message + '</span></div>';
      window._logIdx = e.Index;
    }
    if (isActive) {
      out.scrollTop = out.scrollHeight;
      return;
    }
    var errs = 0;
    for (var j = 0; j < entries.length; j++) {
      if (entries[j].Level === "error") errs++;
    }
    if (!errs) return;
    window._logBadge += errs;
    var btn = document.getElementById("tab-log");
    var b = btn.querySelector(".tab-badge");
    if (!b) {
      b = document.createElement("span");
      b.className = "tab-badge";
      btn.appendChild(b);
    }
    b.textContent = window._logBadge;
  });
}, 500);
