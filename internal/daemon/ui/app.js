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
    const body = await r.json();
    // THE BADGES WERE ALL READING `undefined`, AND THIS IS WHY.
    //
    // `/api/state` answers `{"ok":true,"state":{...}}` and this used to pass the ENVELOPE to
    // `render`, which reads `s.qemu`, `s.guest`, `s.ssh_ready`, `s.disk`, `s.recording` and `s.step`.
    // Every one of those was undefined, so the header showed `qemu: none  guest: —` on a guest that
    // was running - and `alive` was false, which meant `showNoScreen("no machine is running")` was
    // written over the est panel's own explanation on every poll.
    const s = body && body.state ? body.state : body;
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

// ---- the screen: two transports, chosen by which button is pressed --------------------------
//
// THIS WAS ONE BUTTON THAT TOGGLED. A toggle answers "the other one" and never says what the current
// one is, so the only way to know what you are looking at was to remember what you clicked last.
// Two buttons mean the selected one is visible as a fact, and picking the other is one click
// regardless of state.
document.querySelectorAll("#modebar button").forEach((b) => {
  b.onclick = () => {
    if (state.mode === b.dataset.mode) return;
    state.mode = b.dataset.mode;
    applyMode();
  };
});

function applyMode() {
  const vnc = state.mode === "vnc";
  // THE SELECTION IS DRAWN FROM `state.mode`, so it cannot disagree with what the frame is showing.
  document.querySelectorAll("#modebar button").forEach((b) => {
    b.classList.toggle("on", b.dataset.mode === state.mode);
  });
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
// THE BYTES OF A FRAME THAT ARRIVED SPLIT ACROSS TWO WEBSOCKET MESSAGES.
let estCarry = null;

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

// controlToken reads the token from the URL, the same place easystream's own UI reads it
// (`?token=` or `#token=`). A CONTROL connection needs one; a viewer does not, and a guest with no
// token configured treats every connection as control (`auth.rs: is_open`).
function controlToken() {
  const q = new URLSearchParams(location.search);
  const t = q.get("token");
  if (t) return t;
  if (location.hash.startsWith("#token=")) return decodeURIComponent(location.hash.slice(7));
  return "";
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
  // THE LISTENERS ARE ATTACHED EVEN IN READ-ONLY MODE, because the token can arrive from the URL
  // and a mode change should not need a reload. `sendKey` and the button path already refuse to
  // transmit when `state.readOnly` is set, so a viewer moves nothing.
  attachEstInput();
  showNoScreen(null);

  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  // READ-ONLY IS "NO TOKEN", NOT A FLAG. easystream's `authorize` maps a missing or wrong token to
  // VIEW and refuses every mutation for it (EST.md, and `auth.rs`), so a viewer that sends no
  // subprotocol is read-only BY THE SERVER'S OWN RULE.
  //
  // ONE PATH, NO DOUBLING. easystream used to serve an HTTP surface beside the stream, and this
  // page reached it through a prefix-stripping proxy. Both are gone: easystream now speaks `est://`
  // and nothing else, this daemon provides the WebSocket, and the translation between WebSocket and
  // the guest's raw TCP socket happens without a frame being parsed. This page is therefore the
  // only client that understands the protocol at all.
  const url = proto + "//" + location.host + "/est/ws";
  badge($("estState"), "est: connecting", "warn");

  // THE SUBPROTOCOL IS ALWAYS EXACTLY `est`, AND THE TOKEN IS A FRAME.
  //
  // MEASURED: this used to send `["est", controlToken()]`, and the page answered `est: closed`
  // with no picture. A browser requires the server's 101 response to NAME one of the protocols it
  // offered; the daemon echoes `est` and knows nothing about a token, so the extra name was an
  // offer the server could not accept - and the browser failed the socket rather than ignoring it.
  //
  // est carries the token in a `0x03` Auth frame instead (EST.md; `dispatch` -> `T_AUTH` in
  // easystream's est.rs), which is sent below once the socket is open. No Auth frame at all is a
  // valid VIEW connection, which is exactly what "read-only" means here.
  estWS = new WebSocket(url, ["est"]);
  estWS.binaryType = "arraybuffer";
  estGotKey = false;

  estWS.onopen = () => {
    badge($("estState"), "est: handshaking", "warn");
    // 0x03 IS Auth. Its payload is the token as UTF-8 bytes, and the daemon answers `0x04` AuthOk
    // with the mode it granted - so a wrong token degrades to VIEW instead of breaking the picture.
    const t = controlToken();
    if (!state.readOnly && t) {
      const f = new Uint8Array(6 + t.length);
      f[0] = 0x03;
      new DataView(f.buffer).setUint32(2, t.length);
      f.set(new TextEncoder().encode(t), 6);
      try { estWS.send(f); } catch (_) {}
    }
  };

  estWS.onmessage = (ev) => {
    // THE CARRY HAS TO BE PREPENDED, AND IT WAS WRITTEN BUT NEVER READ.
    //
    // MEASURED in a browser against a running guest: the est picture was torn - the left third
    // correct and the rest smeared into horizontal bands - which is what a decoder does when it is
    // handed the wrong bytes, not what a broken desktop looks like.
    //
    // THE FRAMING IS type(1) flags(1) length(u32) payload, and the socket is a transparent pipe: one
    // WebSocket message can hold several frames and one frame can span messages. This loop handled
    // several-per-message and DETECTED the split case - `estCarry = bytes.subarray(i)` - and then
    // the next message started parsing at `ev.data[0]` anyway. The carried bytes were dropped, so a
    // truncated header was followed by a message whose first video bytes were read AS a header: the
    // length field became whatever pixel data happened to be there, and every frame after it was
    // cut at the wrong offset. `estCarry` was assigned on line 265 and read on none.
    let merged;
    if (estCarry) {
      merged = new Uint8Array(estCarry.length + ev.data.byteLength);
      merged.set(estCarry, 0);
      merged.set(new Uint8Array(ev.data), estCarry.length);
      estCarry = null;
    } else {
      merged = new Uint8Array(ev.data);
    }
    const bytes = merged;
    let i = 0;
    while (i + 6 <= bytes.length) {
      const type = bytes[i];
      const len = (bytes[i + 2] << 24 | bytes[i + 3] << 16 | bytes[i + 4] << 8 | bytes[i + 5]) >>> 0;
      // A LENGTH THAT CANNOT BE REAL IS A DESYNC, AND CARRYING IT WOULD MAKE IT PERMANENT.
      //
      // 64 MiB is far above any access unit this encoder produces (a 1024x768 IDR is tens of KiB),
      // so a value that large means the parser is not looking at a header. Dropping the buffer is
      // the only recovery the protocol offers; the next key frame restores the picture.
      if (len > (64 << 20)) { estCarry = null; return; }
      const end = i + 6 + len;
      if (end > bytes.length) { estCarry = bytes.subarray(i); return; }
      const body = bytes.subarray(i + 6, end);
      if (type === 0x01) onHello(body);
      else if (type === 0x02) onVideo(body);
      i = end;
    }
    // ANY REMAINDER SHORTER THAN A HEADER IS CARRIED, NOT DISCARDED: those are the first bytes of
    // the next frame's header.
    estCarry = i < bytes.length ? bytes.subarray(i) : null;
  };

  estWS.onerror = () => badge($("estState"), "est: socket error", "err");
  estWS.onclose = () => {
    if (state.mode === "est") badge($("estState"), "est: closed", "warn");
  };
}

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

// ---- input: the half that makes the picture a desktop ----------------------------------------
//
// MEASURED: a CONTROL connection decoded and displayed the guest's screen and could not move the
// pointer, because NOTHING IN THIS FILE EVER SENT AN INPUT FRAME. The page had exactly two `send`
// calls - the 0x03 Auth on open and the 0x0A key-frame request on a decode error - and no mouse or
// keyboard listener of any kind. The daemon granted CONTROL and the guest ignored the user, which
// reads as "control does not work" and was "control was never asked for".
//
// THE WIRE FORMAT IS est.rs `dispatch`, and every field is checked against it:
//
//	0x10 Move     x:i16 BE, y:i16 BE              guest coordinates, not CSS pixels
//	0x11 Button   num:u8, down:u8                 1 left, 2 middle, 3 right
//	0x12 Scroll   0,0,0,0, dx:u16 BE, dy:u16 BE   four pad bytes, then the two deltas
//	0x13 Key      nameLen:u8, name, char:u32 LE, down:u8
//	0x14 Text     UTF-8 bytes, typed by keystroke
//
// THE COORDINATES ARE THE PART THAT IS EASY TO GET WRONG. The canvas is the guest's own geometry
// (1024x768) and CSS scales it to fit with `object-fit: contain`, so `offsetX` is in DISPLAY pixels
// and sending it unchanged would put the pointer somewhere else entirely on any window that is not
// exactly the guest's size. `toGuest` divides by the ratio the browser is actually drawing at and
// subtracts the letterbox.

function estSend(type, payload) {
  if (!estWS || estWS.readyState !== WebSocket.OPEN) return;
  // READ-ONLY IS ENFORCED HERE RATHER THAN AT EACH CALL SITE, so a listener added later cannot
  // forget it. The daemon and the guest both drop mutations for a VIEW connection anyway; this is
  // what keeps the page from sending them at all.
  if (state.readOnly) return;
  const f = new Uint8Array(6 + (payload ? payload.length : 0));
  f[0] = type;
  new DataView(f.buffer).setUint32(2, payload ? payload.length : 0);
  if (payload) f.set(payload, 6);
  try { estWS.send(f); } catch (_) {}
}

// toGuest maps a pointer event on the canvas to guest pixels, or null when the event is in the
// letterbox margin - which is not part of the guest's screen and must not move its pointer.
//
// THIS IS THE SAME ARITHMETIC THE PUBLISHED UI USES (`toSource` in easystream-webui), and the reason
// it is not `offsetX` is worth stating: the canvas is the guest's geometry and CSS scales it to fit,
// so display pixels and guest pixels are different numbers on every window that is not exactly the
// guest's size. `clientX` minus the drawn rectangle's left, divided by the drawn width, times the
// input width - then clamped, because a pointer captured at the very edge can report one pixel past.
function toGuest(clientX, clientY) {
  if (!estCanvas) return null;
  const r = estCanvas.getBoundingClientRect();
  const gw = estCanvas.width || 1024;
  const gh = estCanvas.height || 768;
  const scale = Math.min(r.width / gw, r.height / gh);
  const dw = gw * scale;
  const dh = gh * scale;
  const ox = r.left + (r.width - dw) / 2;
  const oy = r.top + (r.height - dh) / 2;
  const x = Math.round((clientX - ox) / scale);
  const y = Math.round((clientY - oy) / scale);
  if (x < 0 || y < 0 || x >= gw || y >= gh) return null;
  return [x, y];
}

// MOVES ARE COALESCED TO ONE PER ANIMATION FRAME, AND THAT IS NOT AN OPTIMISATION.
//
// MEASURED in the published UI's own comments: without it, every pointermove during a press floods
// the guest with MOVE events, so a plain click arrives as "down, many drags, up" - which a desktop
// with a press-and-hold meaning treats as a long press. One move per frame is also what a real
// pointer does.
let estPendingMove = null, estMoveRaf = 0;
function flushEstMove() {
  if (estMoveRaf) { cancelAnimationFrame(estMoveRaf); estMoveRaf = 0; }
  const m = estPendingMove;
  estPendingMove = null;
  if (!m) return;
  const b = new Uint8Array(4);
  const d = new DataView(b.buffer);
  d.setInt16(0, m[0]);
  d.setInt16(2, m[1]);
  estSend(0x10, b);
}
function sendMove(clientX, clientY) {
  const m = toGuest(clientX, clientY);
  if (!m) return;
  estPendingMove = m;
  if (estMoveRaf) return;
  estMoveRaf = requestAnimationFrame(flushEstMove);
}

// THE BUTTON EVENT MUST FOLLOW THE POINTER POSITION IT APPLIES TO, so anything queued is flushed
// first - otherwise a click lands where the pointer was one frame ago.
//
// THE BUTTON NUMBER IS THE X PROTOCOL'S and the DOM's is offset by one: 0 left, 1 middle, 2 right
// become 1, 2, 3. Sending `ev.button` unchanged makes a right-click a middle-click.
function sendButton(ev, down) {
  if (ev.button > 2) return;
  flushEstMove();
  estSend(0x11, new Uint8Array([ev.button + 1, down ? 1 : 0]));
}

// THE DELTAS ARE SIGNED 16-BIT BIG-ENDIAN, WHICH IS NOT WHAT THE FIELD LIST SUGGESTS.
//
// easystream's `u16at` is `i16::from_be_bytes` despite the name, and the published UI writes them
// with `setInt16`. Writing `setUint16(1)` for "one notch down" happens to be the same bytes, but
// writing a negative delta as unsigned makes an upward scroll of 1 become 65535 - so the sign is
// the part that matters and it is taken from the browser rather than normalised.
function sendScroll(ev, clientX, clientY) {
  const m = toGuest(clientX, clientY);
  if (!m) return;
  const b = new Uint8Array(8);
  const d = new DataView(b.buffer);
  d.setInt16(0, m[0]);
  d.setInt16(2, m[1]);
  d.setInt16(4, Math.max(-32768, Math.min(32767, Math.round(ev.deltaX))));
  d.setInt16(6, Math.max(-32768, Math.min(32767, Math.round(ev.deltaY))));
  estSend(0x12, b);
}

// THE KEY TABLE IS INDEXED BY `e.code`, NOT `e.key`, AND THAT IS THE WHOLE POINT.
//
// `e.key` is what the key WOULD TYPE - it is "!" for shift+1 and "a" for both `a` and `A` - so a
// table built from it cannot tell a physical key from a character. `e.code` names the physical key
// ("KeyA", "Digit1"), which is what a remote keyboard needs.
//
// THE NAMES ARE X KEYSYMS. `T_KEY` documents "single-char name = a literal character (Ctrl+C);
// multi-char = a named key (Return, F5)", so these strings go to the guest's keymap verbatim.
const estNamedKeys = {
  Enter: "Return", NumpadEnter: "Return", Backspace: "Backspace", Tab: "Tab",
  Escape: "Escape", Delete: "Delete", Insert: "Insert", Space: "Space",
  ArrowUp: "Up", ArrowDown: "Down", ArrowLeft: "Left", ArrowRight: "Right",
  Home: "Home", End: "End", PageUp: "PageUp", PageDown: "PageDown",
  ShiftLeft: "Shift", ShiftRight: "Shift", ControlLeft: "Control", ControlRight: "Control",
  AltLeft: "Alt", AltRight: "Alt", MetaLeft: "Meta", MetaRight: "Meta",
  F1: "F1", F2: "F2", F3: "F3", F4: "F4", F5: "F5", F6: "F6",
  F7: "F7", F8: "F8", F9: "F9", F10: "F10", F11: "F11", F12: "F12",
};

// Key: nameLen:u8, name bytes, char:u32 LITTLE-endian, down:u8.
//
// THE char FIELD IS UNUSED FOR A NAME and is still written, because the daemon reads it at that
// offset whether or not the name is what it acts on - `dispatch` computes `b[1+n .. 1+n+4]` before
// it decides, so a short buffer would be rejected as malformed.
function sendKey(name, ch, down) {
  const nb = new TextEncoder().encode(name || "");
  const b = new Uint8Array(1 + nb.length + 4 + 1);
  b[0] = nb.length;
  b.set(nb, 1);
  new DataView(b.buffer).setUint32(1 + nb.length, ch >>> 0, true);
  b[1 + nb.length + 4] = down ? 1 : 0;
  estSend(0x13, b);
}
function sendText(str) {
  estSend(0x14, new TextEncoder().encode(str));
}

// A KEYSTROKE ONLY REACHES THE FOCUSED ELEMENT, and a canvas cannot be focused - so the keyboard
// listeners go on the window and refuse while the user is typing in the page's own fields. This is
// the arrangement the published UI uses (`typingInField`), and it is what makes the keyboard work
// without the user first clicking the picture, which nobody would guess to do.
function estTypingInField() {
  const a = document.activeElement;
  return !!a && (a.tagName === "INPUT" || a.tagName === "TEXTAREA" || a.isContentEditable);
}

function attachEstInput() {
  if (!estCanvas || estCanvas.dataset.inputBound) return;
  estCanvas.dataset.inputBound = "1";
  estCanvas.style.cursor = "none";
  // POINTER EVENTS COVER MOUSE, PEN AND TOUCH WITH ONE API, which is why they are used rather than
  // mouse events: a laptop with a touchscreen should not need a second code path.
  estCanvas.addEventListener("pointermove", (e) => sendMove(e.clientX, e.clientY));
  estCanvas.addEventListener("pointerdown", (e) => {
    try { estCanvas.setPointerCapture(e.pointerId); } catch (_) {}
    sendMove(e.clientX, e.clientY);
    sendButton(e, true);
  });
  estCanvas.addEventListener("pointerup", (e) => {
    sendButton(e, false);
    try { estCanvas.releasePointerCapture(e.pointerId); } catch (_) {}
  });
  estCanvas.addEventListener("pointercancel", (e) => sendButton(e, false));
  estCanvas.addEventListener("wheel", (e) => {
    e.preventDefault();
    sendScroll(e, e.clientX, e.clientY);
  }, { passive: false });
  estCanvas.addEventListener("contextmenu", (e) => e.preventDefault());

  window.addEventListener("keydown", (e) => {
    if (state.mode !== "est" || estTypingInField()) return;
    const named = estNamedKeys[e.code];
    if (named) { e.preventDefault(); sendKey(named, 0, true); return; }
    // A PRINTABLE KEY WITH A MODIFIER HELD IS A KEY EVENT CARRYING THE CHARACTER, NOT TEXT.
    //
    // This is the difference between Ctrl+C copying and Ctrl+C typing a "c": the guest presses the
    // named key while the modifier's own keydown is already down, and `T_TEXT` has no way to express
    // that. The published UI separates the two cases for exactly this reason.
    if (e.key.length !== 1) return;
    e.preventDefault();
    if (e.ctrlKey || e.altKey || e.metaKey) {
      const cp = e.key.codePointAt(0);
      sendKey(e.key, cp, true);
      sendKey(e.key, cp, false);
    } else {
      sendText(e.key);
    }
  });
  window.addEventListener("keyup", (e) => {
    if (state.mode !== "est" || estTypingInField()) return;
    const named = estNamedKeys[e.code];
    if (named) { e.preventDefault(); sendKey(named, 0, false); }
  });
}

// ---- the two terminals ---------------------------------------------------------------------
// THE TEXTAREA IS DECLARED IN THE PAGE AND THIS LINE USED TO DELETE IT.
//
// MEASURED in a browser: `app.js:400 TypeError: Cannot read properties of null (reading
// 'removeAttribute')`, thrown before anything else in this file ran. The line above it set
// `termwrap.innerHTML = '<div id="termhint"></div>'`, which removes every child - including
// `<textarea id="term">` - and the next line then read that removed element. `#termhint` was never
// referenced anywhere else in this file or in the page, so the assignment had no remaining purpose.
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
