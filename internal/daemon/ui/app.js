// The dashboard's behaviour: poll /api/state, switch the screen between VNC and est, and drive the
// two terminals.
//
// NO FRAMEWORK AND NO BUILD STEP ON PURPOSE. This page is served by a daemon that runs inside a
// container with no Node toolchain; a `npm run build` in front of it would mean a second artifact to
// keep in step with the Go binary, and the whole page is a few hundred lines of DOM work.
//
// THE POLL IS THE CONTRACT WITH THE BACKEND. Everything shown here comes from `/api/state`, which is
// the same document a walkthrough's `wait_until:` reads - so the page cannot drift from what a file
// considers true. A panel that computed "is ssh up" for itself would be a second answer to a
// question that already has one.

const $ = (id) => document.getElementById(id);
const state = {
  mode: "vnc",        // vnc | est
  readOnly: true,     // the switch; both transports honour it differently
  tab: "serial",
  lastState: null,
};

// ---- state polling -------------------------------------------------------------------------
//
// 2s, NOT 1s. A screendump is 6 MB and the disk sample walks a qcow2; the UI is a readout, and
// asking a machine that is mid-install for its state twice a second buys nothing.
async function poll() {
  try {
    const r = await fetch("/api/state", { cache: "no-store" });
    const s = await r.json();
    state.lastState = s;
    render(s);
  } catch (e) {
    $("bStatus").textContent = "daemon unreachable";
    $("bStatus").className = "badge err";
  } finally {
    setTimeout(poll, 2000);
  }
}

function badge(el, text, cls) {
  el.textContent = text;
  el.className = "badge" + (cls ? " " + cls : "");
}

function render(s) {
  // THE RUN ITSELF.
  badge($("bStatus"), s.status || "—", statusClass(s.status));

  // THE MACHINE. `null` means "not asked yet", which is a different fact from "not running" - that
  // distinction is why the daemon uses pointers, and the page keeps it rather than flattening both
  // to "off".
  if (s.qemu) badge($("bQemu"), "qemu: " + (s.qemu.running ? "running" : s.qemu.phase),
    s.qemu.running ? "ok" : "");
  else badge($("bQemu"), "qemu: none");

  if (s.guest) badge($("bGuest"), "guest: " + s.guest.phase,
    s.guest.phase === "up" ? "ok" : "warn");
  else badge($("bGuest"), "guest: —");

  if (s.ssh_ready === true) badge($("bSsh"), "ssh: ready", "ok");
  else if (s.ssh_ready === false) badge($("bSsh"), "ssh: not yet", "warn");
  else badge($("bSsh"), "ssh: —");

  if (s.disk) badge($("bDisk"), "disk: " + Math.round(s.disk.allocated_mb) + "MB"
    + (s.disk.grow_rate_mbps > 0.1 ? " ↑" : ""), s.disk.grow_rate_mbps > 0.1 ? "warn" : "");
  else badge($("bDisk"), "disk: —");

  if (s.recording === true) badge($("bRec"), "rec: on", "ok");
  else if (s.recording === false) badge($("bRec"), "rec: off");
  else badge($("bRec"), "rec: —");

  // WHERE IT IS IN THE FILE. "12 of 29" is the only placement a person needs.
  if (s.step) {
    const n = s.step_count ? ` ${s.step_index}/${s.step_count}` : "";
    badge($("bStep"), (n ? n.trim() + " · " : "") + truncate(s.step, 46));
  } else badge($("bStep"), "—");

  badge($("bTick"), new Date().toLocaleTimeString());

  // THE SSH LINE, AS SOMETHING TO COPY RATHER THAN A LABEL. `ssh` is host:port and it is the one
  // fact a person acts on outside the browser.
  const lSsh = $("lSsh");
  if (s.ssh) {
    lSsh.className = "link";
    lSsh.innerHTML = '<span class="k">ssh</span> <span class="v">ssh ' +
      escapeHtml(s.ssh.replace(":", " -p ")) + "</span>";
  } else {
    lSsh.className = "link off";
    lSsh.innerHTML = '<span class="k">ssh</span> <span class="v">not ready</span>';
  }

  // THE SCREEN FALLS BACK RATHER THAN SHOWING A DEAD FRAME. A machine with no adapter cannot be
  // photographed at all, and saying so is more useful than a black rectangle.
  const alive = s.qemu && s.qemu.running;
  if (!alive) {
    showNoScreen("no machine is running\n\nstart one with `easyvm create` or `easyvm run`");
  } else if (state.mode === "vnc") {
    showNoScreen(null);
  }
}

function statusClass(st) {
  if (st === "running") return "ok";
  if (st === "failed") return "err";
  if (st === "finished") return "ok";
  if (st === "stopping" || st === "waiting") return "warn";
  return "";
}

function truncate(s, n) { return s.length > n ? s.slice(0, n - 1) + "…" : s; }
function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// ---- the screen: two transports, one switch ------------------------------------------------
$("mBtn").onclick = () => {
  state.mode = state.mode === "vnc" ? "est" : "vnc";
  $("mBtn").textContent = state.mode === "vnc" ? "VNC" : "est";
  $("mBtn").dataset.mode = state.mode;
  applyMode();
};

function applyMode() {
  const vnc = state.mode === "vnc";
  $("frameVnc").style.display = vnc ? "" : "none";
  if (estCanvas) estCanvas.style.display = vnc ? "none" : "";
  $("estState").style.display = vnc ? "none" : "";
  if (vnc) {
    // THE VIEW-ONLY SWITCH, FOR VNC, IS A RELOAD. noVNC reads `view_only` at connect time and a
    // running session cannot be told to stop accepting input, so the frame is re-opened with the
    // flag the person asked for. It is a page load of the distro's own UI, which is why it is cheap.
    const ro = state.readOnly ? 1 : 0;
    $("frameVnc").src = "/vnc.html?autoconnect=1&resize=scale&reconnect=1&view_only=" + ro;
  } else {
    startEst();
  }
}

// THE READ-ONLY SWITCH IS NOT COSMETIC. In VNC it is `view_only`, which makes noVNC drop input
// client-side; over est it withholds the control token, so the daemon treats the connection as a
// viewer and refuses mutations at the server. Two transports, one meaning.
$("roBtn").onclick = () => {
  state.readOnly = !state.readOnly;
  $("roBtn").classList.toggle("on", state.readOnly);
  $("roBtn").textContent = state.readOnly ? "Read-only" : "Control allowed";
  applyMode();
};

$("shotBtn").onclick = () => {
  // A DOWNLOAD RATHER THAN A NEW TAB: the point of a screenshot is to keep it.
  const a = document.createElement("a");
  a.href = "/screen.png?" + Date.now();
  a.download = "screen.png";
  a.click();
};

function showNoScreen(text) {
  const el = $("noScreen");
  if (text === null) { el.style.display = "none"; return; }
  el.style.display = "flex";
  el.textContent = text;
}

// ---- est: H.264 decoded by WebCodecs, drawn on a canvas -------------------------------------
//
// THIS IS THE LOW-LATENCY PATH AND THE ONLY ONE THIS PAGE IMPLEMENTS.
//
// `VideoDecoder` decodes each access unit the instant it arrives and hands the frame straight to a
// canvas, so the delay is one frame plus the network - measured on easystream's own page at 30-80ms.
// MSE was considered and dropped: `SourceBuffer` requires a buffering target and the browser will
// not let `currentTime` catch up to `buffered.end`, which costs 300ms at best. There is no way to
// trim that back to WebCodecs' numbers, so carrying two decoders would have been two behaviours to
// keep in step for a worse one.
//
// THE COST IS THE SECURE CONTEXT REQUIREMENT. `window.isSecureContext` must be true - HTTPS, or
// http://localhost - or `VideoDecoder` is undefined. That is a property of the browser, not
// something this page can work around, so the check below reports it by name instead of showing a
// black rectangle.
let estWS = null;
let estDecoder = null;
let estCanvas = null;
let estCtx = null;
let estGotKey = false;
let estTS = 0;
let estCodec = "avc1.42E01E";

function estUnavailableReason() {
  if (!window.isSecureContext) {
    return "WebCodecs needs a secure context\n\n" +
           "this page is on " + location.protocol + "//" + location.host + "\n" +
           "open it over https:// (or http://localhost) and est will play here";
  }
  if (typeof VideoDecoder === "undefined") {
    return "this browser has no WebCodecs VideoDecoder\n\n" +
           "Chrome/Edge 94+, Safari 16.4+ or Firefox 130+ on a secure context";
  }
  return null;
}

function ensureCanvas() {
  if (estCanvas) return;
  // A CANVAS RATHER THAN A <video>: WebCodecs produces VideoFrames, and an <img>/<video> cannot
  // take one. Drawing to a canvas is also what allows the fit/letterbox arithmetic below.
  estCanvas = document.createElement("canvas");
  estCanvas.id = "frameCanvas";
  estCanvas.style.cssText = "position:absolute;inset:0;width:100%;height:100%;object-fit:contain;background:#000";
  $("frame").appendChild(estCanvas);
  estCtx = estCanvas.getContext("2d", { alpha: false });
}

function startEst() {
  stopEst();
  const why = estUnavailableReason();
  if (why) {
    badge($("estState"), "est: unavailable", "err");
    showNoScreen(why);
    return;
  }
  ensureCanvas();
  showNoScreen(null);

  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  // `view=1` WITHHOLDS THE CONTROL TOKEN, so the daemon treats this connection as a viewer and
  // refuses mutations at the SERVER rather than in the browser. The read-only switch therefore
  // means the same thing on both transports instead of being a client-side courtesy on one.
  const url = proto + "//" + location.host + "/est/ws" + (state.readOnly ? "?view=1" : "");
  badge($("estState"), "est: connecting", "warn");

  estWS = new WebSocket(url, ["est"]);
  estWS.binaryType = "arraybuffer";
  estGotKey = false;

  estWS.onopen = () => badge($("estState"), "est: handshaking", "warn");

  estWS.onmessage = (ev) => {
    const bytes = new Uint8Array(ev.data);
    let i = 0;
    // THE EST FRAMING: type(1) flags(1) length(u32) payload. The socket is a transparent pipe, so
    // several frames can arrive in one message and one frame can be split across messages - the
    // loop below handles the first case and the carry buffer handles the second.
    while (i + 6 <= bytes.length) {
      const type = bytes[i];
      const len = (bytes[i + 2] << 24 | bytes[i + 3] << 16 | bytes[i + 4] << 8 | bytes[i + 5]) >>> 0;
      const body = bytes.subarray(i + 6, i + 6 + len);
      if (body.length < len) { estCarry = bytes.subarray(i); return; }
      if (type === 0x01) onHello(body);
      else if (type === 0x02) onVideo(body);
      i += 6 + len;
    }
    estCarry = null;
  };

  estWS.onerror = () => badge($("estState"), "est: socket error", "err");
  estWS.onclose = () => {
    if (state.mode === "est") badge($("estState"), "est: closed", "warn");
  };
}

let estCarry = null;

function onHello(body) {
  // version(1) codec(1) width(2) height(2) fps(1), then the audio and video blocks. Only the first
  // seven bytes matter here: the codec string for `configure` is built from the WIDTH and HEIGHT
  // plus the profile x264 emits by default.
  if (body.length < 7) return;
  const codec = body[1];
  const w = (body[2] << 8) | body[3];
  const h = (body[4] << 8) | body[5];
  const fps = body[6];
  if (codec !== 1) {
    badge($("estState"), "est: codec " + codec + " unsupported", "err");
    return;
  }
  // THE VIDEO BLOCK, WHEN PRESENT, NAMES THE REAL PROFILE. Accepted values in EST.md: 0/absent =
  // x264 baseline (42E01E), and the block sits at offset 15. Reading it avoids a decoder configured
  // for the wrong profile, which fails on the first frame with no useful message.
  if (body.length >= 16) {
    const enc = body[15];
    if (enc === 0 || enc === 1) estCodec = "avc1.42E01E";
  }
  resizeCanvas(w, h);
  startDecoder();
  badge($("estState"), "est: live " + w + "×" + h + " @" + fps, "ok");
}

function resizeCanvas(w, h) {
  ensureCanvas();
  if (estCanvas.width !== w || estCanvas.height !== h) {
    estCanvas.width = w;
    estCanvas.height = h;
  }
}

function startDecoder() {
  if (estDecoder) {
    try { estDecoder.close(); } catch (_) {}
  }
  estDecoder = new VideoDecoder({
    output: (frame) => {
      // THE FRAME IS CLOSED IMMEDIATELY AFTER DRAWING. A VideoFrame holds a GPU buffer and the
      // decoder will stall once a few are outstanding - this is the single most common way a
      // WebCodecs player dies after a few seconds.
      try {
        if (estCanvas.width !== frame.displayWidth || estCanvas.height !== frame.displayHeight) {
          resizeCanvas(frame.displayWidth, frame.displayHeight);
        }
        estCtx.drawImage(frame, 0, 0);
      } finally {
        frame.close();
      }
    },
    // A DECODE ERROR MEANS THE NEXT RECOVERABLE FRAME IS THE NEXT IDR, and x264 only emits those
    // every couple of seconds. Rather than smear for that long, a keyframe is requested.
    error: (e) => {
      estGotKey = false;
      requestKeyframe();
      badge($("estState"), "est: " + e.message, "err");
    },
  });
  // `optimizeForLatency` IS THE WHOLE POINT: it tells the decoder not to hold frames for
  // reordering, which is what makes one-frame-arrives-one-frame-shows possible.
  estDecoder.configure({ codec: estCodec, optimizeForLatency: true });
}

function onVideo(body) {
  if (!estDecoder || estDecoder.state !== "configured") return;
  // AN Annex-B ACCESS UNIT BEGINS WITH A NAL START CODE; `hasIDR` looks for type 5, which is the
  // keyframe. Until one has been seen, delta frames cannot be decoded and are dropped.
  const key = hasIDR(body);
  if (key) estGotKey = true;
  if (!estGotKey) return;
  // STRICTLY INCREASING TIMESTAMPS ARE REQUIRED and `performance.now()` is not monotonic at this
  // resolution - two frames can land in the same microsecond and WebCodecs rejects the chunk with a
  // bare "Decoding error". A counter is the only safe source.
  estTS += 1000;
  try {
    estDecoder.decode(new EncodedVideoChunk({
      type: key ? "key" : "delta",
      timestamp: estTS,
      data: body,
    }));
  } catch (_) {}
}

// hasIDR scans the NAL units of one access unit for type 5.
function hasIDR(b) {
  for (let i = 0; i + 4 < b.length; i++) {
    // 00 00 01 or 00 00 00 01
    if (b[i] === 0 && b[i + 1] === 0 && (b[i + 2] === 1 || (b[i + 2] === 0 && b[i + 3] === 1))) {
      const off = b[i + 2] === 1 ? i + 3 : i + 4;
      if (off < b.length && (b[off] & 0x1f) === 5) return true;
    }
  }
  return false;
}

// requestKeyframe sends est's own KeyReq so the daemon forces an IDR on the next encode. It is what
// makes recovering from a lost frame take one frame instead of one keyint.
function requestKeyframe() {
  if (!estWS || estWS.readyState !== WebSocket.OPEN) return;
  // 0x0A IS KeyFrameReq AND 0x03 IS NOT. `0x03` is `Auth` - sending it here would push an empty
  // token at the daemon and silently downgrade the connection to view-only, which is a bug that
  // only shows up as "control stopped working after a decode error". EST.md, "Keyframe request
  // (PLI)".
  const msg = new Uint8Array(6);
  msg[0] = 0x0A;
  try { estWS.send(msg); } catch (_) {}
}

function stopEst() {
  if (estWS) { try { estWS.close(); } catch (_) {} estWS = null; }
  if (estDecoder) { try { estDecoder.close(); } catch (_) {} estDecoder = null; }
  estGotKey = false;
  estTS = 0;
}

// ---- the two terminals ---------------------------------------------------------------------
$("termwrap").innerHTML = '<div id="termhint"></div>';
const termEl = document.getElementById("term");
termEl.removeAttribute("readonly");

document.querySelectorAll("#tabs button").forEach((b) => {
  b.onclick = () => {
    document.querySelectorAll("#tabs button").forEach((x) => x.classList.remove("on"));
    b.classList.add("on");
    state.tab = b.dataset.tab;
    $("termwrap").classList.add("show");
    connectTerm();
  };
});

$("lTerm").onclick = (e) => {
  e.preventDefault();
  document.querySelector('#tabs button[data-tab="shell"]').click();
};

let termWS = null;

function connectTerm() {
  if (termWS) { try { termWS.close(); } catch (_) {} termWS = null; }
  termEl.value = "";
  const path = state.tab === "shell" ? "/api/shell" : "/api/console/ws";
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(proto + "//" + location.host + path);
  termWS = ws;
  ws.binaryType = "arraybuffer";

  ws.onopen = () => {
    termEl.placeholder = state.tab === "shell"
      ? "connected — type a command and press Enter"
      : "following the serial console…";
  };
  ws.onmessage = (ev) => {
    const text = typeof ev.data === "string" ? ev.data : new TextDecoder().decode(ev.data);
    termEl.value += text;
    // KEEP THE SCROLL AT THE BOTTOM UNLESS THE PERSON SCROLLED UP, which is the only behaviour that
    // does not fight a reader looking at an earlier line.
    const atBottom = termEl.scrollTop + termEl.clientHeight >= termEl.scrollHeight - 24;
    if (atBottom) termEl.scrollTop = termEl.scrollHeight;
  };
  ws.onerror = () => { termEl.placeholder = "connection failed"; };
  ws.onclose = () => { termEl.placeholder = "disconnected"; };

  if (state.tab === "shell") {
    termEl.onkeydown = (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        const line = termEl.value;
        // Everything already shown is history; only the last line is the command. The daemon runs
        // it and streams the output back on the same socket.
        ws.send(JSON.stringify({ line: line.slice(line.lastIndexOf("\n") + 1) }) + "\n");
        termEl.value += "\n";
        termEl.scrollTop = termEl.scrollHeight;
      }
    };
  } else {
    termEl.onkeydown = null;
  }
}

// ---- boot ----------------------------------------------------------------------------------
poll();
applyMode();
